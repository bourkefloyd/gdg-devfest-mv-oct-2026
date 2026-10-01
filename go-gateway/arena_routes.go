package main

import (
	"context"
	"errors"
	"io/fs"
	"net/http"
	"os"
	"path"
	"strings"
	"time"

	"go-gateway/internal/api"
	"go-gateway/internal/arena"
	"go-gateway/internal/players"
)

func newArenaHandler(seats, agentSeats api.SeatFunc) http.Handler {
	var realFactory arena.PlayerFactory
	if env("PLAYERS", "mock") == "real" && seats != nil {
		realFactory = func(_ context.Context, spec arena.GameSpec) (players.Player, error) {
			seatFunc, mode, seat := seats, api.ModeHuman, 0
			if spec.PlayerMix == "mixed" && spec.Index%2 == 1 {
				mode, seat = api.ModeRace, 1
			} else if agentSeats != nil && spec.Index%4 == 2 {
				seatFunc = agentSeats
			}
			roster, err := seatFunc(mode)
			if err != nil {
				return nil, err
			}
			if len(roster) <= seat {
				return nil, errors.New("requested arena model seat is unavailable")
			}
			return roster[seat], nil
		}
	}
	return arena.NewHandler(arena.Config{
		MaxReal:         envInt("ARENA_MAX_REAL_GAMES", envInt("ARENA_MAX_GEMINI_GAMES", 8)),
		RealFactory:     realFactory,
		DefaultDuration: time.Duration(envInt("ARENA_GAME_SECONDS", 10)) * time.Second,
	})
}

// newArenaWebHandler serves Vite's build output and falls back to index.html
// for client-side routes. Assets with a missing extension still return 404.
func newArenaWebHandler() http.Handler {
	root := os.Getenv("ARENA_STATIC_DIR")
	if root == "" {
		root = "../web/dist"
	}
	files := os.DirFS(root)
	server := http.FileServerFS(files)

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		name := strings.TrimPrefix(path.Clean(r.URL.Path), "/")
		if name == "" || name == "index.html" {
			index, err := fs.ReadFile(files, "index.html")
			if err != nil {
				http.Error(w, "arena UI is not built; run `make arena-build`", http.StatusServiceUnavailable)
				return
			}
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			_, _ = w.Write(index)
			return
		}
		if _, err := fs.Stat(files, name); err == nil {
			clone := r.Clone(r.Context())
			clone.URL.Path = "/" + name
			server.ServeHTTP(w, clone)
			return
		}
		if path.Ext(name) != "" {
			http.NotFound(w, r)
			return
		}
		index, err := fs.ReadFile(files, "index.html")
		if err != nil {
			http.Error(w, "arena UI is not built; run `make arena-build`", http.StatusServiceUnavailable)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write(index)
	})
}
