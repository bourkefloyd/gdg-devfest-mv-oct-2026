package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"time"
)

// handleEvents streams game events as SSE. Every event carries an id so a
// reconnecting client resumes via Last-Event-ID; late joiners get a replay.
func (s *Server) handleEvents(w http.ResponseWriter, r *http.Request) {
	g := s.store.get(r.PathValue("id"))
	if g == nil || g.KeyID != keyID(r.Context()) {
		writeError(w, http.StatusNotFound, "game_not_found", "no such game")
		return
	}
	rc := http.NewResponseController(w)
	h := w.Header()
	h.Set("Content-Type", "text/event-stream")
	h.Set("Cache-Control", "no-cache")
	h.Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)

	last := 0
	if v, err := strconv.Atoi(r.Header.Get("Last-Event-ID")); err == nil && v > 0 {
		last = v
	}
	wake := g.subscribe()
	defer g.unsubscribe(wake)
	hb := time.NewTicker(s.cfg.HeartbeatEvery)
	defer hb.Stop()

	fmt.Fprint(w, "retry: 2000\n\n")
	for {
		evs, open := g.eventsSince(last)
		for _, ev := range evs {
			data, err := json.Marshal(ev.Data)
			if err != nil {
				continue
			}
			fmt.Fprintf(w, "id: %d\nevent: %s\ndata: %s\n\n", ev.ID, ev.Type, data)
			last = ev.ID
		}
		if err := rc.Flush(); err != nil {
			return
		}
		if !open {
			return
		}
		select {
		case <-wake:
		case <-hb.C:
			fmt.Fprint(w, ": heartbeat\n\n")
		case <-r.Context().Done():
			return
		case <-s.ctx.Done():
			return
		}
	}
}
