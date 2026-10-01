package main

import (
	"context"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"go-gateway/internal/api"
	"go-gateway/internal/pool"
)

func env(k, def string) string {
	if v := strings.TrimSpace(os.Getenv(k)); v != "" {
		return v
	}
	return def
}

func envInt(k string, def int) int {
	if n, err := strconv.Atoi(os.Getenv(k)); err == nil && n > 0 {
		return n
	}
	return def
}

func splitList(s string) []string {
	var out []string
	for _, p := range strings.Split(s, ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

func main() {
	log := slog.New(slog.NewJSONHandler(os.Stderr, nil))
	slog.SetDefault(log)

	keys := splitList(os.Getenv("GATEWAY_API_KEYS"))
	if len(keys) == 0 {
		log.Error("GATEWAY_API_KEYS is required (comma-separated bearer keys)")
		os.Exit(1)
	}

	var workers *pool.Pool
	addrs := splitList(env("GRPC_WORKER_ADDRS", os.Getenv("GRPC_WORKER_ADDR")))
	if len(addrs) > 0 {
		var err error
		workers, err = pool.New(pool.Config{
			Addrs:     addrs,
			PerWorker: envInt("GEMMA_PER_WORKER", 1),
			MaxQueue:  envInt("GEMMA_MAX_QUEUE", 64),
			MaxWait:   time.Duration(envInt("GEMMA_MAX_WAIT_MS", 5000)) * time.Millisecond,
		})
		if err != nil {
			log.Error("worker pool", "err", err)
			os.Exit(1)
		}
		defer workers.Close()
		log.Info("gemma pool", "workers", len(addrs))
	}

	cfg := api.Config{
		APIKeys:        keys,
		AllowedOrigins: splitList(env("CORS_ALLOWED_ORIGINS", "http://localhost:4317,http://127.0.0.1:4317")),
		MaxActiveGames: envInt("MAX_ACTIVE_GAMES", 2000),
		Logger:         log,
	}
	wired := seatsFromEnv(log, workers)
	cfg.Seats, cfg.AgentSeats = wired.seats, wired.agentSeats
	if wired.commentator != nil {
		cfg.Commentator = wired.commentator
	}
	if workers != nil {
		cfg.Chat = workers
	}
	cfg.Ready = func(ctx context.Context) (bool, map[string]any) {
		d := map[string]any{"players": env("PLAYERS", "mock")}
		if workers == nil {
			return true, d
		}
		st := workers.Stats()
		d["gemma_pool"] = st
		// Gemma's seat spills to Gemini, so an empty pool alone isn't fatal.
		return st.Healthy > 0 || geminiReady(), d
	}
	srv := api.New(cfg)

	addr := net.JoinHostPort(env("HOST", "127.0.0.1"), env("PORT", "8787"))
	httpServer := &http.Server{
		Addr:              addr,
		Handler:           srv.Handler(),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       10 * time.Second,
		IdleTimeout:       120 * time.Second,
		MaxHeaderBytes:    16 << 10,
	}

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)
	go func() {
		log.Info("gateway listening", "addr", addr, "cors", cfg.AllowedOrigins)
		if err := httpServer.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Error("http server", "err", err)
			os.Exit(1)
		}
	}()
	<-stop
	log.Info("shutting down; draining games")
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	srv.Shutdown(ctx)
	_ = httpServer.Shutdown(ctx)
}
