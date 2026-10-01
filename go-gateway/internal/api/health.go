package api

import (
	"context"
	"net/http"
	"time"
)

func (s *Server) handleHealthz(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *Server) handleReadyz(w http.ResponseWriter, r *http.Request) {
	if s.ctx.Err() != nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{"ready": false, "reason": "shutting_down"})
		return
	}
	ready, detail := true, map[string]any{}
	if s.cfg.Ready != nil {
		ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
		defer cancel()
		ready, detail = s.cfg.Ready(ctx)
		if detail == nil {
			detail = map[string]any{}
		}
	}
	s.store.mu.Lock()
	detail["games_active"] = s.store.active
	s.store.mu.Unlock()
	detail["ready"] = ready
	status := http.StatusOK
	if !ready {
		status = http.StatusServiceUnavailable
	}
	writeJSON(w, status, detail)
}
