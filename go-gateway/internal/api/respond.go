package api

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
)

type apiError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, code, msg string) {
	writeJSON(w, status, map[string]apiError{"error": {Code: code, Message: msg}})
}

// decodeJSON reads a size-limited body strictly: unknown fields, trailing
// data and oversize bodies are all rejected. It writes the error response
// itself and returns false on failure.
func (s *Server) decodeJSON(w http.ResponseWriter, r *http.Request, dst any) bool {
	if ct := r.Header.Get("Content-Type"); ct != "" && !strings.HasPrefix(ct, "application/json") {
		writeError(w, http.StatusUnsupportedMediaType, "unsupported_media_type", "Content-Type must be application/json")
		return false
	}
	r.Body = http.MaxBytesReader(w, r.Body, s.cfg.MaxBodyBytes)
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		var mbe *http.MaxBytesError
		if errors.As(err, &mbe) {
			writeError(w, http.StatusRequestEntityTooLarge, "body_too_large", "request body exceeds limit")
			return false
		}
		if errors.Is(err, io.EOF) {
			writeError(w, http.StatusBadRequest, "invalid_json", "request body is empty")
			return false
		}
		writeError(w, http.StatusBadRequest, "invalid_json", sanitizeDecodeErr(err))
		return false
	}
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		var mbe *http.MaxBytesError
		if errors.As(err, &mbe) {
			writeError(w, http.StatusRequestEntityTooLarge, "body_too_large", "request body exceeds limit")
			return false
		}
		writeError(w, http.StatusBadRequest, "invalid_json", "unexpected data after JSON object")
		return false
	}
	return true
}

// sanitizeDecodeErr keeps decoder messages short and never echoes the body.
func sanitizeDecodeErr(err error) string {
	msg := err.Error()
	if strings.HasPrefix(msg, "json: unknown field") {
		return msg
	}
	var ute *json.UnmarshalTypeError
	if errors.As(err, &ute) {
		return "wrong type for field " + ute.Field
	}
	return "malformed JSON"
}
