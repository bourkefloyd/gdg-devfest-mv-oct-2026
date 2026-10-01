package arena

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"
)

const maxRequestBody = 16 << 10

// Handler returns a standalone, no-key arena handler with default limits.
// It is intended to be mounted as mux.Handle("/arena/", arena.Handler()).
func Handler() http.Handler {
	return NewManager(Config{})
}

// NewHandler returns a handler backed by the supplied configuration.
func NewHandler(cfg Config) http.Handler {
	return NewManager(cfg)
}

// ServeHTTP supports:
//
//	POST   /arena/start
//	GET    /arena/runs/{run_id}/events
//	GET    /arena/runs/{run_id}
//	DELETE /arena/runs/{run_id}
//
// POST /arena/runs is an alias for start, and POST .../{run_id}/cancel is an
// alias for DELETE, making the API convenient for clients that cannot DELETE.
func (m *Manager) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	setCommonHeaders(w)
	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusNoContent)
		return
	}

	parts := arenaPath(r.URL.Path)
	if isStartPath(parts) {
		if r.Method != http.MethodPost {
			methodNotAllowed(w, http.MethodPost)
			return
		}
		m.handleStart(w, r)
		return
	}

	runID, action, ok := parseRunPath(parts)
	if !ok {
		writeError(w, http.StatusNotFound, "not_found", "arena endpoint not found")
		return
	}
	run, found := m.Get(runID)
	if !found {
		writeError(w, http.StatusNotFound, "run_not_found", ErrRunNotFound.Error())
		return
	}

	switch action {
	case "events":
		if r.Method != http.MethodGet {
			methodNotAllowed(w, http.MethodGet)
			return
		}
		m.handleEvents(w, r, run)
	case "cancel":
		if r.Method != http.MethodPost && r.Method != http.MethodDelete {
			methodNotAllowed(w, http.MethodPost, http.MethodDelete)
			return
		}
		run.Cancel()
		writeJSON(w, http.StatusAccepted, map[string]any{
			"run_id": run.ID(),
			"status": StatusCancelled,
		})
	case "":
		switch r.Method {
		case http.MethodGet:
			writeJSON(w, http.StatusOK, run.Snapshot())
		case http.MethodDelete:
			run.Cancel()
			writeJSON(w, http.StatusAccepted, map[string]any{
				"run_id": run.ID(),
				"status": StatusCancelled,
			})
		default:
			methodNotAllowed(w, http.MethodGet, http.MethodDelete)
		}
	default:
		writeError(w, http.StatusNotFound, "not_found", "arena endpoint not found")
	}
}

func (m *Manager) handleStart(w http.ResponseWriter, r *http.Request) {
	var request StartRequest
	if r.Body != nil {
		dec := json.NewDecoder(io.LimitReader(r.Body, maxRequestBody))
		dec.DisallowUnknownFields()
		if err := dec.Decode(&request); err != nil && !errors.Is(err, io.EOF) {
			writeError(w, http.StatusBadRequest, "invalid_json", "invalid start request: "+err.Error())
			return
		}
		if err := ensureJSONEOF(dec); err != nil {
			writeError(w, http.StatusBadRequest, "invalid_json", err.Error())
			return
		}
	}
	run, err := m.Start(request)
	if err != nil {
		code := "invalid_request"
		if errors.Is(err, ErrTooManyGames) {
			code = "game_cap_exceeded"
		}
		writeError(w, http.StatusBadRequest, code, err.Error())
		return
	}
	w.Header().Set("Location", run.StartResponse().EventsURL)
	writeJSON(w, http.StatusAccepted, run.StartResponse())
}

func (m *Manager) handleEvents(w http.ResponseWriter, r *http.Request, run *Run) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeError(w, http.StatusInternalServerError, "streaming_unsupported", "response writer does not support streaming")
		return
	}
	afterID := uint64(0)
	if value := r.Header.Get("Last-Event-ID"); value != "" {
		var err error
		afterID, err = strconv.ParseUint(value, 10, 64)
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid_event_id", "Last-Event-ID must be an unsigned integer")
			return
		}
	}
	replay, events, unsubscribe, err := run.Subscribe(afterID)
	if err != nil {
		if errors.Is(err, ErrTooManyStreams) {
			writeError(w, http.StatusTooManyRequests, "stream_cap_exceeded", err.Error())
			return
		}
		writeError(w, http.StatusInternalServerError, "stream_error", err.Error())
		return
	}
	defer unsubscribe()

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache, no-transform")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
	_, _ = io.WriteString(w, "retry: 1000\n\n")
	flusher.Flush()

	for _, event := range replay {
		if err := writeSSE(w, event); err != nil {
			return
		}
	}
	flusher.Flush()

	heartbeat := time.NewTicker(15 * time.Second)
	defer heartbeat.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case event, open := <-events:
			if !open {
				return
			}
			if err := writeSSE(w, event); err != nil {
				return
			}
			flusher.Flush()
		case <-heartbeat.C:
			if _, err := io.WriteString(w, ": keep-alive\n\n"); err != nil {
				return
			}
			flusher.Flush()
		}
	}
}

func writeSSE(w io.Writer, event Event) error {
	payload, err := json.Marshal(event)
	if err != nil {
		return err
	}
	if _, err := fmt.Fprintf(w, "id: %d\nevent: %s\ndata: ", event.ID, event.Type); err != nil {
		return err
	}
	if _, err := w.Write(payload); err != nil {
		return err
	}
	_, err = io.WriteString(w, "\n\n")
	return err
}

func arenaPath(path string) []string {
	path = strings.TrimSpace(path)
	path = strings.TrimPrefix(path, "/")
	path = strings.TrimPrefix(path, "api/")
	if path == "arena" {
		return nil
	}
	path = strings.TrimPrefix(path, "arena/")
	path = strings.Trim(path, "/")
	if path == "" {
		return nil
	}
	return strings.Split(path, "/")
}

func isStartPath(parts []string) bool {
	return len(parts) == 0 ||
		(len(parts) == 1 && (parts[0] == "start" || parts[0] == "runs"))
}

func parseRunPath(parts []string) (runID, action string, ok bool) {
	if len(parts) > 0 && parts[0] == "runs" {
		parts = parts[1:]
	}
	switch len(parts) {
	case 1:
		return parts[0], "", parts[0] != ""
	case 2:
		if parts[1] == "events" || parts[1] == "cancel" {
			return parts[0], parts[1], parts[0] != ""
		}
	}
	return "", "", false
}

func ensureJSONEOF(dec *json.Decoder) error {
	var extra any
	err := dec.Decode(&extra)
	if errors.Is(err, io.EOF) {
		return nil
	}
	if err == nil {
		return errors.New("start request must contain one JSON object")
	}
	return errors.New("invalid trailing JSON: " + err.Error())
}

func setCommonHeaders(w http.ResponseWriter) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Access-Control-Allow-Methods", "GET, POST, DELETE, OPTIONS")
	w.Header().Set("Access-Control-Allow-Headers", "Authorization, Content-Type, Last-Event-ID")
}

func methodNotAllowed(w http.ResponseWriter, methods ...string) {
	w.Header().Set("Allow", strings.Join(methods, ", "))
	writeError(w, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed")
}

func writeError(w http.ResponseWriter, status int, code, message string) {
	writeJSON(w, status, map[string]any{
		"error": map[string]string{
			"code":    code,
			"message": message,
		},
	})
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}
