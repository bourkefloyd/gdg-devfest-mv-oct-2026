package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"time"

	"go-gateway/internal/api"
	"go-gateway/internal/players"
	"go-gateway/internal/pool"
	"go-gateway/internal/router"
	"go-gateway/internal/wordhunt"

	"google.golang.org/genai"
)

var geminiBreaker = router.NewCircuitBreaker()

// geminiReady reports whether the shared Gemini breaker admits calls.
func geminiReady() bool { return geminiConfigured() && geminiBreaker.Allow() }

func geminiConfigured() bool {
	return os.Getenv("GEMINI_API_KEY") != "" || strings.EqualFold(os.Getenv("GOOGLE_GENAI_USE_VERTEXAI"), "true")
}

// seatsFromEnv wires players per mode. PLAYERS=mock (default when no Gemini
// credentials are set) uses deterministic mocks; PLAYERS=real uses Gemini and
// the Gemma pool with the failover chain from the plan.
func seatsFromEnv(log *slog.Logger, workers *pool.Pool) api.SeatFunc {
	mode := env("PLAYERS", "")
	if mode == "" {
		mode = "mock"
		if geminiConfigured() {
			mode = "real"
		}
	}
	os.Setenv("PLAYERS", mode)
	if mode != "real" {
		log.Info("players: mock")
		return api.MockSeats(time.Duration(envInt("MOCK_LATENCY_MS", 1000)) * time.Millisecond)
	}

	client, err := players.NewGenAIClient(context.Background())
	if err != nil {
		log.Error("gemini client; falling back to mock players", "err", err)
		return api.MockSeats(time.Second)
	}
	log.Info("players: real", "gemma_pool", workers != nil, "gemini_model", env("GEMINI_PLAYER_MODEL", "default"))
	return realSeats(client, workers, geminiBreaker, os.Getenv("GEMINI_PLAYER_MODEL"),
		os.Getenv("GEMINI_FALLBACK_MODEL"), env("GEMMA_MODEL", "gemma-4-e2b"))
}

func realSeats(client *genai.Client, workers *pool.Pool, breaker *router.CircuitBreaker, model, liteModel, gemmaModel string) api.SeatFunc {
	dict := wordhunt.Default()
	gemini := func(model string) players.Player {
		return &router.BreakerPlayer{Breaker: breaker,
			Player: &router.RetryPlayer{Player: players.NewGeminiPlayer(client, model), Attempts: 3, Retryable: retryableGemini}}
	}
	geminiChain := []players.Player{gemini(model)}
	spill := geminiChain[0]
	if liteModel != "" {
		spill = gemini(liteModel)
		geminiChain = append(geminiChain, spill)
	}
	geminiChain = append(geminiChain, &players.SolverPlayer{Dict: dict, Limit: 6})

	var gemmaChain []players.Player
	if workers != nil {
		gemmaChain = append(gemmaChain, &poolGemma{pool: workers, model: gemmaModel})
	}
	gemmaChain = append(gemmaChain, spill, &players.SolverPlayer{Dict: dict, Limit: 3})

	return func(m string) ([]players.Player, error) {
		g := &router.FallbackPlayer{NameLabel: "gemini", Chain: geminiChain}
		if m == api.ModeHuman {
			return []players.Player{g}, nil
		}
		return []players.Player{g, &router.FallbackPlayer{NameLabel: "gemma", Chain: gemmaChain}}, nil
	}
}

// retryableGemini follows the plan: retry 429/500/503 and deadlines, never 4xx.
func retryableGemini(err error) bool {
	var apiErr genai.APIError
	if errors.As(err, &apiErr) {
		return apiErr.Code == 429 || apiErr.Code == 500 || apiErr.Code == 503
	}
	return errors.Is(err, context.DeadlineExceeded)
}

// poolGemma plays the Gemma seat through the worker pool, so it gets
// least-outstanding routing, the admission queue (spilling after
// GEMMA_MAX_WAIT_MS), and retry-before-first-token.
type poolGemma struct {
	pool  *pool.Pool
	model string
}

func (p *poolGemma) Name() string { return "gemma" }

func (p *poolGemma) Play(ctx context.Context, b wordhunt.Board, deadline time.Time) (players.Result, error) {
	start := time.Now()
	ctx, cancel := context.WithDeadline(ctx, deadline)
	defer cancel()
	raw, addr, err := p.pool.Text(ctx, pool.Request{Prompt: gemmaPrompt(b), MaxTokens: 120, Temperature: 0.2}, 16<<10)
	res := players.Result{Backend: "local-grpc", Model: p.model, Latency: time.Since(start), Raw: raw}
	if err != nil {
		return res, fmt.Errorf("gemma pool (%s): %w", addr, err)
	}
	res.Claims, err = players.ParseGemmaOutput(raw)
	return res, err
}

func gemmaPrompt(b wordhunt.Board) string {
	var sb strings.Builder
	sb.WriteString("Word Hunt board (index:letter):\n")
	for row := 0; row < 4; row++ {
		for col := 0; col < 4; col++ {
			i := row*4 + col
			if col > 0 {
				sb.WriteByte(' ')
			}
			fmt.Fprintf(&sb, "%d:%c", i, b.Tiles[i])
		}
		sb.WriteByte('\n')
	}
	sb.WriteString("List likely words. Reply with exactly one line: WORDS: word, word, word")
	return sb.String()
}
