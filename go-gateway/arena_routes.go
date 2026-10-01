package main

import (
	"io/fs"
	"net/http"
	"os"
	"path"
	"strings"

	"go-gateway/internal/arena"
)

func newArenaHandler() http.Handler {
	return arena.NewHandler(arena.Config{})
}

// newArenaWebHandler serves Vite's build output and falls back to index.html
// for client-side routes. Assets with a missing extension still return 404.
func newArenaWebHandler() http.Handler {
	root := os.Getenv("ARENA_STATIC_DIR")
	if root == "" {
		root = "../arena-web/dist"
	}
	files := os.DirFS(root)
	server := http.FileServerFS(files)

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		name := strings.TrimPrefix(path.Clean(r.URL.Path), "/arena")
		name = strings.TrimPrefix(name, "/")
		if name == "" {
			name = "index.html"
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
