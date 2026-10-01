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

var (
	geminiBreaker      = router.NewCircuitBreaker() // primary backend (Vertex when enabled)
	apiBreaker         = router.NewCircuitBreaker() // Gemini API key failover tier
	hostedGemmaBreaker = router.NewCircuitBreaker()
	apiFailover        bool
)

// geminiReady reports whether any Gemini backend's breaker admits calls.
func geminiReady() bool {
	return geminiConfigured() && (geminiBreaker.Allow() || (apiFailover && apiBreaker.Allow()))
}

func vertexEnabled() bool {
	v := os.Getenv("GOOGLE_GENAI_USE_VERTEXAI")
	return strings.EqualFold(v, "true") || v == "1"
}

func geminiConfigured() bool { return os.Getenv("GEMINI_API_KEY") != "" || vertexEnabled() }

// seatsFromEnv wires players per mode. PLAYERS=mock (default when no Gemini
// credentials are set) uses deterministic mocks; PLAYERS=real uses Gemini and
// the Gemma pool with the failover chain from the plan.
type wiring struct {
	seats, agentSeats api.SeatFunc
	commentator       players.Commentator
	arenaClient       *genai.Client
}

// genaiBackends is the Gemini failover pair: Primary is Vertex when
// GOOGLE_GENAI_USE_VERTEXAI=true (else the API key client); API is the
// API-key failover tier, nil when it would duplicate Primary.
type genaiBackends struct {
	Primary, API               *genai.Client
	PrimaryBreaker, APIBreaker *router.CircuitBreaker
}

func newBackends(ctx context.Context, log *slog.Logger) (genaiBackends, error) {
	out := genaiBackends{PrimaryBreaker: geminiBreaker, APIBreaker: apiBreaker}
	b, err := players.NewGenAIBackends(ctx)
	if err != nil {
		if !vertexEnabled() || os.Getenv("GEMINI_API_KEY") == "" {
			return out, err
		}
		// Vertex misconfigured (e.g. no ADC): keep serving on the API key.
		log.Warn("vertex unavailable; using Gemini API key only", "err", err)
		c, apiErr := genai.NewClient(ctx, &genai.ClientConfig{Backend: genai.BackendGeminiAPI, APIKey: os.Getenv("GEMINI_API_KEY")})
		if apiErr != nil {
			return out, errors.Join(err, apiErr)
		}
		out.Primary = c
		return out, nil
	}
	out.Primary = b.Primary
	if b.API != nil && b.API != b.Primary {
		out.API = b.API
	}
	return out, nil
}

func seatsFromEnv(log *slog.Logger, workers *pool.Pool) (w wiring) {
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
		w.seats = api.MockSeats(time.Duration(envInt("MOCK_LATENCY_MS", 1000)) * time.Millisecond)
		return w
	}

	b, err := newBackends(context.Background(), log)
	if err != nil {
		log.Error("gemini client; falling back to mock players", "err", err)
		w.seats = api.MockSeats(time.Second)
		return w
	}
	apiFailover = b.API != nil
	w.arenaClient = b.Primary
	log.Info("players: real", "gemini_primary", backendName(b.Primary), "api_key_failover", apiFailover,
		"gemma_pool", workers != nil, "gemini_model", env("GEMINI_PLAYER_MODEL", "default"))
	o := seatOpts{
		Model:     os.Getenv("GEMINI_PLAYER_MODEL"),
		LiteModel: os.Getenv("GEMINI_FALLBACK_MODEL"),
		// Vertex and the API key serve different model IDs (e.g. Vertex
		// 404s gemini-3.8-flash while the API key 404s gemini-2.5-flash).
		APIModel:     os.Getenv("GEMINI_API_PLAYER_MODEL"),
		APILiteModel: os.Getenv("GEMINI_API_FALLBACK_MODEL"),
		GemmaModel:   env("GEMMA_MODEL", "gemma-4-e2b"),
		HostedGemma:  os.Getenv("GEMMA_HOSTED_MODEL"),
	}
	w.seats = realSeats(b, workers, o)
	o.Agent = true
	w.agentSeats = realSeats(b, workers, o)
	w.commentator = players.NewGeminiCommentator(b.Primary, os.Getenv("GEMINI_COMMENTATOR_MODEL"))
	return w
}

func backendName(c *genai.Client) string {
	if c != nil && c.ClientConfig().Backend == genai.BackendVertexAI {
		return "vertex"
	}
	return "gemini-api"
}

type seatOpts struct {
	Model, LiteModel, GemmaModel string
	APIModel, APILiteModel       string // API-key tier overrides; empty reuses Model/LiteModel
	HostedGemma                  string // empty disables the hosted Gemma tier
	Agent                        bool   // Gemini seat uses the submit_words agent loop
}

func realSeats(b genaiBackends, workers *pool.Pool, o seatOpts) api.SeatFunc {
	dict := wordhunt.Default()
	wrap := func(p players.Player, br *router.CircuitBreaker) players.Player {
		return loggedTier{&router.BreakerPlayer{Breaker: br,
			Player: &router.RetryPlayer{Player: p, Attempts: 3, Retryable: retryableGemini}}}
	}
	// tiers returns the model on Vertex (or the sole backend), then on the
	// API key. Model IDs differ per backend, so the API tier may override.
	tiers := func(model, apiModel string) []players.Player {
		t := []players.Player{wrap(players.NewGeminiPlayer(b.Primary, model), b.PrimaryBreaker)}
		if b.API != nil {
			if apiModel == "" {
				apiModel = model
			}
			t = append(t, wrap(players.NewGeminiPlayer(b.API, apiModel), b.APIBreaker))
		}
		return t
	}

	var geminiChain []players.Player
	if o.Agent {
		geminiChain = append(geminiChain, &router.BreakerPlayer{Breaker: b.PrimaryBreaker, Player: players.NewGeminiAgent(b.Primary, o.Model, dict)})
	}
	geminiChain = append(geminiChain, tiers(o.Model, o.APIModel)...)
	spill := tiers(o.Model, o.APIModel)
	if o.LiteModel != "" {
		spill = tiers(o.LiteModel, o.APILiteModel)
		geminiChain = append(geminiChain, spill...)
	}
	geminiChain = append(geminiChain, &players.SolverPlayer{Dict: dict, Limit: 6})

	var gemmaChain []players.Player
	if workers != nil {
		gemmaChain = append(gemmaChain, &poolGemma{pool: workers, model: o.GemmaModel})
	}
	if o.HostedGemma != "" {
		gemmaChain = append(gemmaChain, wrap(players.NewHostedGemmaPlayer(b.Primary, o.HostedGemma), hostedGemmaBreaker))
	}
	gemmaChain = append(gemmaChain, labeledFallback{&router.FallbackPlayer{Chain: spill}}, &players.SolverPlayer{Dict: dict, Limit: 3})

	return func(m string) ([]players.Player, error) {
		g := &router.FallbackPlayer{NameLabel: "gemini", Chain: geminiChain}
		if m == api.ModeHuman {
			return []players.Player{g}, nil
		}
		return []players.Player{g, &router.FallbackPlayer{NameLabel: "gemma", Chain: gemmaChain}}, nil
	}
}

// loggedTier logs why a failover tier failed, since FallbackPlayer only
// surfaces the last error. Messages are truncated; prompts are never logged.
type loggedTier struct{ players.Player }

func (l loggedTier) Play(ctx context.Context, b wordhunt.Board, deadline time.Time) (players.Result, error) {
	res, err := l.Player.Play(ctx, b, deadline)
	if err != nil {
		msg := err.Error()
		if len(msg) > 200 {
			msg = msg[:200]
		}
		slog.Warn("model tier failed", "tier", l.Name(), "backend", res.Backend, "model", res.Model, "err", msg)
	}
	return res, err
}

// labeledFallback marks results as fallback even when it heads the chain, so
// Gemma's seat served by Gemini (no local pool) is never shown as Gemma.
type labeledFallback struct{ players.Player }

func (l labeledFallback) Play(ctx context.Context, b wordhunt.Board, deadline time.Time) (players.Result, error) {
	res, err := l.Player.Play(ctx, b, deadline)
	res.Fallback = true
	return res, err
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
