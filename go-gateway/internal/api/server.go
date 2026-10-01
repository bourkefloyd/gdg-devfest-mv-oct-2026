// Package api is the hardened HTTP surface of the gateway: bearer auth,
// per-key rate limits, strict body limits, CORS allowlist, the in-memory
// Word Hunt game store with SSE events, chat completions, and health/metrics.
package api

import (
	"context"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"go-gateway/internal/players"
	"go-gateway/internal/pool"
	"go-gateway/internal/wordhunt"

	"github.com/prometheus/client_golang/prometheus/promhttp"
	"golang.org/x/time/rate"
)

const (
	ModeRace  = "race"
	ModeHuman = "human_vs_gemini"
	ModeLoad  = "load"
)

// SeatFunc returns the model players for a game mode. Human seats are added
// by the API itself for human_vs_gemini and load modes.
type SeatFunc func(mode string) ([]players.Player, error)

// ChatBackend serves the legacy /v1/chat/completions relay.
type ChatBackend interface {
	Generate(ctx context.Context, req pool.Request, onToken func(string) error) (string, error)
}

// ReadyFunc reports whether the gateway can serve games, with detail for /readyz.
type ReadyFunc func(ctx context.Context) (bool, map[string]any)

type Config struct {
	APIKeys        []string
	AllowedOrigins []string

	MaxActiveGames  int // global
	MaxActivePerKey int

	CreateRate  rate.Limit
	CreateBurst int
	WordRate    rate.Limit
	WordBurst   int
	ReadRate    rate.Limit
	ReadBurst   int

	MaxBodyBytes    int64
	MaxChatTokens   int32
	DefaultDuration time.Duration
	MinDuration     time.Duration
	MaxDuration     time.Duration
	MinBoardWords   int
	HeartbeatEvery  time.Duration
	CommentTimeout  time.Duration

	Dict        *wordhunt.Dict
	Seats       SeatFunc
	AgentSeats  SeatFunc // used when a game is created with "agent": true
	Commentator players.Commentator
	Chat        ChatBackend
	Ready       ReadyFunc
	Logger      *slog.Logger
}

func (c *Config) defaults() {
	setInt := func(v *int, d int) {
		if *v <= 0 {
			*v = d
		}
	}
	setRate := func(v *rate.Limit, d rate.Limit) {
		if *v <= 0 {
			*v = d
		}
	}
	setDur := func(v *time.Duration, d time.Duration) {
		if *v <= 0 {
			*v = d
		}
	}
	setInt(&c.MaxActiveGames, 2000)
	setInt(&c.MaxActivePerKey, 10)
	setRate(&c.CreateRate, 2)
	setInt(&c.CreateBurst, 5)
	setRate(&c.WordRate, 20)
	setInt(&c.WordBurst, 20)
	setRate(&c.ReadRate, 50)
	setInt(&c.ReadBurst, 100)
	if c.MaxBodyBytes <= 0 {
		c.MaxBodyBytes = 8 << 10
	}
	if c.MaxChatTokens <= 0 {
		c.MaxChatTokens = 256
	}
	setDur(&c.DefaultDuration, 80*time.Second)
	setDur(&c.MinDuration, 5*time.Second)
	setDur(&c.MaxDuration, 120*time.Second)
	setInt(&c.MinBoardWords, 40)
	setDur(&c.HeartbeatEvery, 15*time.Second)
	setDur(&c.CommentTimeout, 15*time.Second)
	if c.Dict == nil {
		c.Dict = wordhunt.Default()
	}
	if c.Logger == nil {
		c.Logger = slog.Default()
	}
}

type Server struct {
	cfg    Config
	keys   *keyring
	limits *limiters
	store  *store
	mounts []mount
	wg     sync.WaitGroup // running games
	ctx    context.Context
	cancel context.CancelFunc
}

func New(cfg Config) *Server {
	cfg.defaults()
	ctx, cancel := context.WithCancel(context.Background())
	return &Server{
		cfg:    cfg,
		keys:   newKeyring(cfg.APIKeys),
		limits: newLimiters(cfg),
		store:  newStore(),
		ctx:    ctx,
		cancel: cancel,
	}
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	open := func(pattern string, h http.HandlerFunc) {
		mux.Handle(pattern, instrument(pattern, h))
	}
	authed := func(pattern, bucket string, h http.HandlerFunc) {
		mux.Handle(pattern, instrument(pattern, s.requireKey(s.rateLimit(bucket, h))))
	}

	open("GET /healthz", s.handleHealthz)
	open("GET /readyz", s.handleReadyz)
	mux.Handle("GET /metrics", promhttp.Handler())

	authed("POST /v1/games", bucketCreate, s.handleCreateGame)
	authed("GET /v1/games/{id}", bucketRead, s.handleGetGame)
	authed("POST /v1/games/{id}/words", bucketWord, s.handleSubmitWord)
	authed("POST /v1/chat/completions", bucketCreate, s.handleChat)
	// SSE accepts either the bearer key or the per-game stream token, since
	// browser EventSource cannot send an Authorization header.
	mux.Handle("GET /v1/games/{id}/events", s.streamAuth(s.rateLimit(bucketRead, http.HandlerFunc(s.handleEvents))))

	for _, m := range s.mounts {
		h := m.h
		if !m.public {
			h = s.requireKey(s.rateLimit(m.bucket, h))
		}
		mux.Handle(m.pattern, instrument(m.pattern, h))
	}

	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		writeError(w, http.StatusNotFound, "not_found", "no such route")
	})
	return recoverer(s.cfg.Logger, s.cors(securityHeaders(mux)))
}

type mount struct {
	pattern, bucket string
	public          bool
	h               http.Handler
}

// Rate-limit buckets available to mounted routes.
const (
	BucketCreate = bucketCreate
	BucketWord   = bucketWord
	BucketRead   = bucketRead
)

// Mount registers an extra route (Go 1.22 pattern, e.g. "POST /v1/arena")
// behind bearer auth and the given per-key rate-limit bucket, with the same
// CORS, security headers, metrics and recovery as built-in routes. Call it
// before Handler.
func (s *Server) Mount(pattern, bucket string, h http.Handler) {
	s.mounts = append(s.mounts, mount{pattern: pattern, bucket: bucket, h: h})
}

// MountPublic registers an extra unauthenticated route (dashboards, probes).
func (s *Server) MountPublic(pattern string, h http.Handler) {
	s.mounts = append(s.mounts, mount{pattern: pattern, public: true, h: h})
}

// KeyID returns the caller's non-reversible API key ID inside mounted handlers.
func KeyID(ctx context.Context) string { return keyID(ctx) }

// Shutdown stops accepting game work, waits up to ctx for running games to
// finish, then cancels whatever is left.
func (s *Server) Shutdown(ctx context.Context) {
	done := make(chan struct{})
	go func() { s.wg.Wait(); close(done) }()
	select {
	case <-done:
	case <-ctx.Done():
	}
	s.cancel()
	s.store.closeAll()
}
