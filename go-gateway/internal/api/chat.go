package api

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"go-gateway/internal/pool"
)

type chatReq struct {
	Prompt      string  `json:"prompt"`
	MaxTokens   int32   `json:"max_tokens"`
	Temperature float32 `json:"temperature"`
}

const maxPromptBytes = 4 << 10

// handleChat is the legacy completions relay, now authenticated and capped.
// Prompts are logged only as a hash and length.
func (s *Server) handleChat(w http.ResponseWriter, r *http.Request) {
	if s.cfg.Chat == nil {
		writeError(w, http.StatusServiceUnavailable, "no_backend", "no Gemma workers configured")
		return
	}
	var req chatReq
	if !s.decodeJSON(w, r, &req) {
		return
	}
	switch {
	case req.Prompt == "":
		writeError(w, http.StatusBadRequest, "invalid_prompt", "prompt is required")
		return
	case len(req.Prompt) > maxPromptBytes:
		writeError(w, http.StatusBadRequest, "invalid_prompt", "prompt too long")
		return
	case req.MaxTokens < 0 || req.MaxTokens > s.cfg.MaxChatTokens:
		writeError(w, http.StatusBadRequest, "invalid_max_tokens", fmt.Sprintf("max_tokens must be 1..%d", s.cfg.MaxChatTokens))
		return
	case req.Temperature < 0 || req.Temperature > 2:
		writeError(w, http.StatusBadRequest, "invalid_temperature", "temperature must be 0..2")
		return
	}
	if req.MaxTokens == 0 {
		req.MaxTokens = 128
	}
	sum := sha256.Sum256([]byte(req.Prompt))
	s.cfg.Logger.Info("chat", "key", keyID(r.Context()), "prompt_sha256", hex.EncodeToString(sum[:8]),
		"prompt_len", len(req.Prompt), "max_tokens", req.MaxTokens)

	ctx, cancel := context.WithTimeout(r.Context(), 60*time.Second)
	defer cancel()
	rc := http.NewResponseController(w)
	started := false
	start := func() {
		if !started {
			started = true
			w.Header().Set("Content-Type", "text/event-stream")
			w.Header().Set("Cache-Control", "no-cache")
			w.WriteHeader(http.StatusOK)
		}
	}
	_, err := s.cfg.Chat.Generate(ctx, pool.Request{Prompt: req.Prompt, MaxTokens: req.MaxTokens, Temperature: req.Temperature},
		func(tok string) error {
			start()
			b, _ := json.Marshal(map[string]any{"token": tok, "is_final": false})
			fmt.Fprintf(w, "data: %s\n\n", b)
			return rc.Flush()
		})
	if err != nil && !started {
		switch err {
		case pool.ErrQueueFull, pool.ErrQueueTimeout:
			w.Header().Set("Retry-After", "2")
			writeError(w, http.StatusServiceUnavailable, "overloaded", "all Gemma workers busy")
		default:
			writeError(w, http.StatusBadGateway, "backend_error", "Gemma worker failed")
		}
		return
	}
	start()
	if err != nil {
		fmt.Fprintf(w, "event: error\ndata: {\"code\":\"backend_error\"}\n\n")
	} else {
		fmt.Fprint(w, "data: {\"token\":\"\",\"is_final\":true}\n\n")
	}
	fmt.Fprint(w, "data: [DONE]\n\n")
	_ = rc.Flush()
}
