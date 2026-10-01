// Package arena runs bounded, in-memory Word Hunt load-test tournaments.
//
// A Manager owns at most one active run. Starting a run cancels and drains the
// previous one before launching the replacement, which makes "start over"
// safe even when clients issue concurrent requests.
package arena

import (
	"context"
	"crypto/rand"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"go-gateway/internal/players"
	"go-gateway/internal/wordhunt"
)

const (
	DefaultGameCount = 24
	MaxGameCount     = 100
)

var (
	ErrInvalidGameCount = errors.New("game count must be positive")
	ErrTooManyGames     = errors.New("game count exceeds the configured cap")
	ErrInvalidDuration  = errors.New("duration is outside the configured range")
	ErrInvalidPlayerMix = errors.New("player mix must be bots, gemini, or mixed")
	ErrRunNotFound      = errors.New("arena run not found")
	ErrTooManyStreams   = errors.New("too many event streams for arena run")
)

// Status is the lifecycle state of a run.
type Status string

const (
	StatusRunning   Status = "running"
	StatusFinished  Status = "finished"
	StatusCancelled Status = "cancelled"
)

// GameSpec is passed to PlayerFactory once for every game.
type GameSpec struct {
	RunID     string
	GameID    string
	Index     int
	Board     wordhunt.Board
	Duration  time.Duration
	PlayerMix string
}

// PlayerFactory may provide API-backed players. It must be safe for concurrent
// calls. A nil factory uses a standalone trie solver and requires no API key.
type PlayerFactory func(context.Context, GameSpec) (players.Player, error)

// Config controls resource limits. Zero values select conservative defaults.
type Config struct {
	DefaultGames     int
	MaxGames         int
	MaxConcurrent    int
	DefaultDuration  time.Duration
	MinDuration      time.Duration
	MaxDuration      time.Duration
	MaxRuns          int
	MaxEvents        int
	MaxSubscribers   int
	SubscriberBuffer int
	WordsPerGame     int
	MaxReal          int
	Dictionary       *wordhunt.Dict
	PlayerFactory    PlayerFactory
	RealFactory      PlayerFactory
	Now              func() time.Time
}

// StartRequest starts N independent games. Duration bounds each player move.
type StartRequest struct {
	N          int           `json:"n,omitempty"`
	Count      int           `json:"count,omitempty"`
	Duration   time.Duration `json:"-"`
	DurationMS int64         `json:"duration_ms,omitempty"`
	DurationS  float64       `json:"duration_s,omitempty"`
	PlayerMix  string        `json:"player_mix,omitempty"`
	Seed       *int64        `json:"seed,omitempty"`
}

// StartResponse is safe to return directly to a React client.
type StartResponse struct {
	RunID      string    `json:"run_id"`
	Status     Status    `json:"status"`
	N          int       `json:"n"`
	DurationMS int64     `json:"duration_ms"`
	StartedAt  time.Time `json:"started_at"`
	EventsURL  string    `json:"events_url"`
	PlayerMix  string    `json:"player_mix"`
	Seed       int64     `json:"seed"`
	Tiles      string    `json:"tiles"`
}

// Stats is a race-safe point-in-time aggregate for a run.
type Stats struct {
	Running        int     `json:"running"`
	Completed      int     `json:"completed"`
	Total          int     `json:"total"`
	Words          int     `json:"words"`
	WordsPerSecond float64 `json:"words_per_second"`
	P50LatencyMS   float64 `json:"p50_latency_ms"`
	P95LatencyMS   float64 `json:"p95_latency_ms"`
	Errors         int     `json:"errors"`
}

// LeaderboardEntry is one completed game, sorted by score on final events.
type LeaderboardEntry struct {
	GameID        string   `json:"game_id"`
	Name          string   `json:"name"`
	Backend       string   `json:"backend"`
	Model         string   `json:"model"`
	Score         int      `json:"score"`
	Words         []string `json:"words"`
	WordCount     int      `json:"word_count"`
	LatencyMS     float64  `json:"latency_ms"`
	Fallback      bool     `json:"fallback,omitempty"`
	Error         string   `json:"error,omitempty"`
	PerfectScore  int      `json:"perfect_score"`
	TimeToScoreMS float64  `json:"time_to_score_ms"`
}

// Game describes a live board.
type Game struct {
	ID           string `json:"id"`
	Index        int    `json:"index"`
	Tiles        string `json:"tiles"`
	PlayerName   string `json:"player_name"`
	PerfectScore int    `json:"perfect_score"`
}

// Word describes a validated word and its row-major tile path.
type Word struct {
	GameID     string `json:"game_id"`
	PlayerName string `json:"player_name"`
	Value      string `json:"word"`
	Path       []int  `json:"path"`
	Points     int    `json:"points"`
	Total      int    `json:"total"`
}

// Event is persisted for reconnecting SSE clients. Its shape deliberately
// keeps common fields at the top level for straightforward React reducers.
type Event struct {
	ID          uint64             `json:"id"`
	Type        string             `json:"type"`
	RunID       string             `json:"run_id"`
	At          time.Time          `json:"at"`
	Status      Status             `json:"status,omitempty"`
	Game        *Game              `json:"game,omitempty"`
	Word        *Word              `json:"word_event,omitempty"`
	GameID      string             `json:"game_id,omitempty"`
	PlayerName  string             `json:"player_name,omitempty"`
	WordValue   string             `json:"word,omitempty"`
	Path        []int              `json:"path,omitempty"`
	Points      int                `json:"points,omitempty"`
	TotalScore  int                `json:"total_score,omitempty"`
	Result      *LeaderboardEntry  `json:"result,omitempty"`
	Stats       *Stats             `json:"stats,omitempty"`
	Leaderboard []LeaderboardEntry `json:"leaderboard,omitempty"`
	Message     string             `json:"message,omitempty"`
}

// Snapshot is the non-streaming representation of a run.
type Snapshot struct {
	RunID       string             `json:"run_id"`
	Status      Status             `json:"status"`
	N           int                `json:"n"`
	DurationMS  int64              `json:"duration_ms"`
	StartedAt   time.Time          `json:"started_at"`
	FinishedAt  *time.Time         `json:"finished_at,omitempty"`
	Stats       Stats              `json:"stats"`
	Leaderboard []LeaderboardEntry `json:"leaderboard"`
	PlayerMix   string             `json:"player_mix"`
	Seed        int64              `json:"seed"`
	Tiles       string             `json:"tiles"`
}

// Manager coordinates runs and implements http.Handler in http.go.
type Manager struct {
	cfg Config

	startMu sync.Mutex
	mu      sync.RWMutex
	runs    map[string]*Run
	order   []string
	active  *Run
}

// Run is a single load-test tournament. Use Snapshot or Subscribe to inspect
// it; its mutable fields are intentionally private.
type Run struct {
	manager      *Manager
	id           string
	n            int
	duration     time.Duration
	playerMix    string
	seed         int64
	board        wordhunt.Board
	perfectScore int
	started      time.Time

	ctx    context.Context
	cancel context.CancelFunc
	done   chan struct{}

	mu          sync.Mutex
	status      Status
	finished    *time.Time
	running     int
	completed   int
	words       int
	errors      int
	latencies   []float64
	leaderboard []LeaderboardEntry
	sequence    uint64
	events      []Event
	subscribers map[chan Event]struct{}
}

// NewManager returns an isolated arena manager.
func NewManager(cfg Config) *Manager {
	cfg = withDefaults(cfg)
	return &Manager{
		cfg:  cfg,
		runs: make(map[string]*Run),
	}
}

func withDefaults(cfg Config) Config {
	if cfg.MaxGames <= 0 {
		cfg.MaxGames = MaxGameCount
	} else if cfg.MaxGames > MaxGameCount {
		cfg.MaxGames = MaxGameCount
	}
	if cfg.DefaultGames <= 0 {
		cfg.DefaultGames = min(DefaultGameCount, cfg.MaxGames)
	} else if cfg.DefaultGames > cfg.MaxGames {
		cfg.DefaultGames = cfg.MaxGames
	}
	if cfg.MaxConcurrent <= 0 {
		cfg.MaxConcurrent = cfg.MaxGames
	} else if cfg.MaxConcurrent > cfg.MaxGames {
		cfg.MaxConcurrent = cfg.MaxGames
	}
	if cfg.DefaultDuration <= 0 {
		cfg.DefaultDuration = 5 * time.Second
	}
	if cfg.MinDuration <= 0 {
		cfg.MinDuration = 50 * time.Millisecond
	}
	if cfg.MaxDuration < cfg.MinDuration {
		cfg.MaxDuration = max(60*time.Second, cfg.MinDuration)
	}
	if cfg.DefaultDuration < cfg.MinDuration || cfg.DefaultDuration > cfg.MaxDuration {
		cfg.DefaultDuration = cfg.MinDuration
	}
	if cfg.MaxRuns <= 0 {
		cfg.MaxRuns = 8
	}
	if cfg.MaxEvents <= 0 {
		cfg.MaxEvents = 20000
	}
	if cfg.MaxSubscribers <= 0 {
		cfg.MaxSubscribers = 32
	}
	if cfg.SubscriberBuffer <= 0 {
		cfg.SubscriberBuffer = 4096
	}
	if cfg.WordsPerGame <= 0 {
		cfg.WordsPerGame = 25
	} else if cfg.WordsPerGame > 150 {
		cfg.WordsPerGame = 150
	}
	if cfg.MaxReal <= 0 {
		cfg.MaxReal = 8
	} else if cfg.MaxReal > cfg.MaxGames {
		cfg.MaxReal = cfg.MaxGames
	}
	if cfg.Dictionary == nil {
		cfg.Dictionary = wordhunt.Default()
	}
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	if cfg.PlayerFactory == nil {
		dict, limit, maxReal, realFactory := cfg.Dictionary, cfg.WordsPerGame, cfg.MaxReal, cfg.RealFactory
		cfg.PlayerFactory = func(ctx context.Context, spec GameSpec) (players.Player, error) {
			if useReal(spec.PlayerMix, spec.Index, maxReal) && realFactory != nil {
				return realFactory(ctx, spec)
			}
			return &pacedSolverPlayer{
				inner: &players.SolverPlayer{Dict: dict, Limit: limit},
				delay: 180*time.Millisecond + time.Duration(spec.Index%9)*45*time.Millisecond,
			}, nil
		}
	}
	return cfg
}

func useReal(mix string, index, limit int) bool {
	switch mix {
	case "gemini", "mixed":
		return index < limit
	default:
		return false
	}
}

func (m *Manager) validate(req StartRequest) (int, time.Duration, string, error) {
	n := req.N
	if n == 0 {
		n = req.Count
	}
	if n == 0 {
		n = m.cfg.DefaultGames
	}
	if n < 0 {
		return 0, 0, "", ErrInvalidGameCount
	}
	if n > m.cfg.MaxGames {
		return 0, 0, "", fmt.Errorf("%w: maximum is %d", ErrTooManyGames, m.cfg.MaxGames)
	}
	duration := req.Duration
	if duration == 0 && req.DurationMS != 0 {
		if req.DurationMS < 0 || req.DurationMS > m.cfg.MaxDuration.Milliseconds() {
			return 0, 0, "", fmt.Errorf("%w: use %s through %s", ErrInvalidDuration, m.cfg.MinDuration, m.cfg.MaxDuration)
		}
		duration = time.Duration(req.DurationMS) * time.Millisecond
	}
	if duration == 0 && req.DurationS != 0 {
		if req.DurationS < 0 || req.DurationS != req.DurationS || req.DurationS > m.cfg.MaxDuration.Seconds() {
			return 0, 0, "", fmt.Errorf("%w: use %s through %s", ErrInvalidDuration, m.cfg.MinDuration, m.cfg.MaxDuration)
		}
		duration = time.Duration(req.DurationS * float64(time.Second))
	}
	if duration == 0 {
		duration = m.cfg.DefaultDuration
	}
	if duration < m.cfg.MinDuration || duration > m.cfg.MaxDuration {
		return 0, 0, "", fmt.Errorf("%w: use %s through %s", ErrInvalidDuration, m.cfg.MinDuration, m.cfg.MaxDuration)
	}
	mix := strings.ToLower(strings.TrimSpace(req.PlayerMix))
	if mix == "" {
		mix = "mixed"
	}
	if mix != "bots" && mix != "gemini" && mix != "mixed" {
		return 0, 0, "", ErrInvalidPlayerMix
	}
	return n, duration, mix, nil
}

// Start cancels and drains the active run before starting a replacement.
func (m *Manager) Start(req StartRequest) (*Run, error) {
	n, duration, mix, err := m.validate(req)
	if err != nil {
		return nil, err
	}

	m.startMu.Lock()
	defer m.startMu.Unlock()

	m.mu.RLock()
	old := m.active
	m.mu.RUnlock()
	if old != nil {
		old.Cancel()
		<-old.Done()
	}

	ctx, cancel := context.WithCancel(context.Background())
	seed := randomSeed()
	if req.Seed != nil {
		seed = *req.Seed
	}
	board := wordhunt.NewBoard(seed, 20)
	perfectScore := 0
	for _, word := range board.Solve(m.cfg.Dictionary) {
		perfectScore += wordhunt.Score(word)
	}
	run := &Run{
		manager:      m,
		id:           newID(),
		n:            n,
		duration:     duration,
		playerMix:    mix,
		seed:         seed,
		board:        board,
		perfectScore: perfectScore,
		started:      m.cfg.Now().UTC(),
		ctx:          ctx,
		cancel:       cancel,
		done:         make(chan struct{}),
		status:       StatusRunning,
		latencies:    make([]float64, 0, n),
		leaderboard:  make([]LeaderboardEntry, 0, n),
		events:       make([]Event, 0, min(m.cfg.MaxEvents, n*(m.cfg.WordsPerGame+3)+2)),
		subscribers:  make(map[chan Event]struct{}),
	}

	m.mu.Lock()
	m.active = run
	m.runs[run.id] = run
	m.order = append(m.order, run.id)
	m.evictLocked()
	m.mu.Unlock()

	go run.execute()
	return run, nil
}

func (m *Manager) evictLocked() {
	for len(m.order) > m.cfg.MaxRuns {
		id := m.order[0]
		candidate := m.runs[id]
		if candidate == m.active {
			return
		}
		delete(m.runs, id)
		m.order = m.order[1:]
	}
}

// Get returns a retained run.
func (m *Manager) Get(id string) (*Run, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	run, ok := m.runs[id]
	return run, ok
}

// Cancel cancels a retained run. It returns false for an unknown id.
func (m *Manager) Cancel(id string) bool {
	run, ok := m.Get(id)
	if !ok {
		return false
	}
	run.Cancel()
	return true
}

// Active returns the active run, if any.
func (m *Manager) Active() (*Run, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.active, m.active != nil
}

// ID returns the run identifier.
func (r *Run) ID() string { return r.id }

// Done closes after the final event has been published.
func (r *Run) Done() <-chan struct{} { return r.done }

// Cancel is idempotent.
func (r *Run) Cancel() { r.cancel() }

// StartResponse returns the initial HTTP response payload.
func (r *Run) StartResponse() StartResponse {
	return StartResponse{
		RunID:      r.id,
		Status:     StatusRunning,
		N:          r.n,
		DurationMS: r.duration.Milliseconds(),
		StartedAt:  r.started,
		EventsURL:  "/api/arena/runs/" + r.id + "/events",
		PlayerMix:  r.playerMix,
		Seed:       r.seed,
		Tiles:      r.board.String(),
	}
}

// Snapshot returns a deep copy suitable for concurrent JSON encoding.
func (r *Run) Snapshot() Snapshot {
	r.mu.Lock()
	defer r.mu.Unlock()
	stats := r.statsLocked(r.manager.cfg.Now())
	return Snapshot{
		RunID:       r.id,
		Status:      r.status,
		N:           r.n,
		DurationMS:  r.duration.Milliseconds(),
		StartedAt:   r.started,
		FinishedAt:  cloneTime(r.finished),
		Stats:       stats,
		Leaderboard: cloneLeaderboard(r.leaderboard),
		PlayerMix:   r.playerMix,
		Seed:        r.seed,
		Tiles:       r.board.String(),
	}
}

func (r *Run) execute() {
	r.publish(Event{Type: "run_started", Status: StatusRunning, Stats: pointer(r.currentStats())})

	jobs := make(chan int)
	workers := min(r.n, r.manager.cfg.MaxConcurrent)
	var wg sync.WaitGroup
	wg.Add(workers)
	for range workers {
		go func() {
			defer wg.Done()
			for index := range jobs {
				if r.ctx.Err() != nil {
					return
				}
				r.play(index)
			}
		}()
	}
	for index := 0; index < r.n; index++ {
		select {
		case jobs <- index:
		case <-r.ctx.Done():
			close(jobs)
			wg.Wait()
			r.finish()
			return
		}
	}
	close(jobs)
	wg.Wait()
	r.finish()
}

func (r *Run) play(index int) {
	r.setRunning(1)
	defer r.setRunning(-1)

	gameID := fmt.Sprintf("%s-%03d", r.id, index+1)
	name := funName(r.id, index)
	board := r.board
	game := Game{ID: gameID, Index: index, Tiles: board.String(), PlayerName: name, PerfectScore: r.perfectScore}
	r.publish(Event{Type: "game_started", Game: &game, Stats: pointer(r.currentStats())})

	spec := GameSpec{
		RunID: r.id, GameID: gameID, Index: index, Board: board, Duration: r.duration, PlayerMix: r.playerMix,
	}
	player, err := r.manager.cfg.PlayerFactory(r.ctx, spec)
	started := r.manager.cfg.Now()
	deadline := started.Add(r.duration)
	var result players.Result
	if err == nil && player == nil {
		err = errors.New("player factory returned nil player")
	}
	if err == nil {
		moveContext, cancel := context.WithDeadline(r.ctx, deadline)
		result, err = player.Play(moveContext, board, deadline)
		cancel()
	}
	latency := result.Latency
	if latency <= 0 {
		latency = r.manager.cfg.Now().Sub(started)
	}

	entry := LeaderboardEntry{
		GameID:       gameID,
		Name:         name,
		Backend:      valueOr(result.Backend, "unknown"),
		Model:        valueOr(result.Model, "unknown"),
		Words:        make([]string, 0, min(len(result.Claims), r.manager.cfg.WordsPerGame)),
		LatencyMS:    durationMS(latency),
		Fallback:     result.Fallback,
		PerfectScore: r.perfectScore,
	}
	if err != nil {
		if !errors.Is(err, context.Canceled) {
			entry.Error = err.Error()
			r.addError()
		}
		r.complete(entry)
		return
	}

	maxClaims := min(len(result.Claims), 500)
	seen := make(map[string]struct{}, maxClaims)
	for _, claim := range result.Claims[:maxClaims] {
		if r.ctx.Err() != nil {
			break
		}
		if !r.manager.cfg.Now().Before(deadline) {
			break
		}
		if len(entry.Words) >= r.manager.cfg.WordsPerGame {
			break
		}
		word := strings.ToLower(strings.TrimSpace(claim.Word))
		if _, exists := seen[word]; exists {
			continue
		}
		seen[word] = struct{}{}
		path := append([]int(nil), claim.Path...)
		if len(path) == 0 {
			path = findPath(board, word)
		}
		ok, _ := wordhunt.ValidateWord(board, r.manager.cfg.Dictionary, word, path)
		if !ok {
			continue
		}
		points := wordhunt.Score(word)
		entry.Score += points
		entry.Words = append(entry.Words, word)
		entry.WordCount = len(entry.Words)
		r.addWord()
		r.publish(Event{
			Type: "word",
			Word: &Word{
				GameID: gameID, PlayerName: name, Value: word,
				Path: path, Points: points, Total: entry.Score,
			},
			GameID:     gameID,
			PlayerName: name,
			WordValue:  word,
			Path:       append([]int(nil), path...),
			Points:     points,
			TotalScore: entry.Score,
			Stats:      pointer(r.currentStats()),
		})
		delay := 85*time.Millisecond + time.Duration((index+len(entry.Words))%6)*18*time.Millisecond
		if !waitContext(r.ctx, delay) {
			break
		}
	}
	r.complete(entry)
}

type pacedSolverPlayer struct {
	inner *players.SolverPlayer
	delay time.Duration
}

func (*pacedSolverPlayer) Name() string { return "Solver bot" }

func (p *pacedSolverPlayer) Play(ctx context.Context, board wordhunt.Board, deadline time.Time) (players.Result, error) {
	start := time.Now()
	if !waitContext(ctx, p.delay) {
		return players.Result{}, ctx.Err()
	}
	result, err := p.inner.Play(ctx, board, deadline)
	result.Backend = "in-process"
	result.Model = "Solver bot (baseline)"
	result.Fallback = false
	result.Latency = time.Since(start)
	return result, err
}

func waitContext(ctx context.Context, delay time.Duration) bool {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}

func (r *Run) complete(entry LeaderboardEntry) {
	entry.TimeToScoreMS = durationMS(r.manager.cfg.Now().Sub(r.started))
	r.mu.Lock()
	r.completed++
	r.latencies = append(r.latencies, entry.LatencyMS)
	r.leaderboard = append(r.leaderboard, cloneEntry(entry))
	stats := r.statsLocked(r.manager.cfg.Now())
	r.mu.Unlock()
	r.publish(Event{Type: "game_finished", Result: &entry, Stats: &stats})
}

func (r *Run) setRunning(delta int) {
	r.mu.Lock()
	r.running += delta
	r.mu.Unlock()
}

func (r *Run) addWord() {
	r.mu.Lock()
	r.words++
	r.mu.Unlock()
}

func (r *Run) addError() {
	r.mu.Lock()
	r.errors++
	r.mu.Unlock()
}

func (r *Run) currentStats() Stats {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.statsLocked(r.manager.cfg.Now())
}

func (r *Run) statsLocked(now time.Time) Stats {
	elapsed := now.Sub(r.started).Seconds()
	if elapsed <= 0 {
		elapsed = 0.001
	}
	latencies := append([]float64(nil), r.latencies...)
	sort.Float64s(latencies)
	return Stats{
		Running:        r.running,
		Completed:      r.completed,
		Total:          r.n,
		Words:          r.words,
		WordsPerSecond: float64(r.words) / elapsed,
		P50LatencyMS:   percentile(latencies, 0.50),
		P95LatencyMS:   percentile(latencies, 0.95),
		Errors:         r.errors,
	}
}

func (r *Run) finish() {
	r.mu.Lock()
	if r.status != StatusRunning {
		r.mu.Unlock()
		return
	}
	if r.ctx.Err() != nil {
		r.status = StatusCancelled
	} else {
		r.status = StatusFinished
	}
	now := r.manager.cfg.Now().UTC()
	r.finished = &now
	sort.SliceStable(r.leaderboard, func(i, j int) bool {
		if r.leaderboard[i].Score != r.leaderboard[j].Score {
			return r.leaderboard[i].Score > r.leaderboard[j].Score
		}
		return r.leaderboard[i].TimeToScoreMS < r.leaderboard[j].TimeToScoreMS
	})
	status := r.status
	stats := r.statsLocked(now)
	board := cloneLeaderboard(r.leaderboard)
	r.mu.Unlock()

	eventType := "run_finished"
	message := ""
	if status == StatusCancelled {
		eventType = "run_cancelled"
		message = "run cancelled"
	}
	r.publish(Event{
		Type: eventType, Status: status, Stats: &stats,
		Leaderboard: board, Message: message,
	})

	r.mu.Lock()
	for ch := range r.subscribers {
		close(ch)
		delete(r.subscribers, ch)
	}
	r.mu.Unlock()
	close(r.done)

	r.manager.mu.Lock()
	if r.manager.active == r {
		r.manager.active = nil
	}
	r.manager.evictLocked()
	r.manager.mu.Unlock()
}

func (r *Run) publish(event Event) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.sequence++
	event.ID = r.sequence
	event.RunID = r.id
	event.At = r.manager.cfg.Now().UTC()
	r.events = append(r.events, cloneEvent(event))
	if over := len(r.events) - r.manager.cfg.MaxEvents; over > 0 {
		copy(r.events, r.events[over:])
		r.events = r.events[:len(r.events)-over]
	}
	for ch := range r.subscribers {
		select {
		case ch <- cloneEvent(event):
		default:
			// A stalled browser cannot retain unbounded events. Closing its
			// stream lets EventSource reconnect and replay retained history.
			close(ch)
			delete(r.subscribers, ch)
		}
	}
}

// Subscribe atomically returns retained events after afterID and registers for
// future events. The cancel function must be called by the consumer.
func (r *Run) Subscribe(afterID uint64) ([]Event, <-chan Event, func(), error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	replay := make([]Event, 0, len(r.events))
	for _, event := range r.events {
		if event.ID > afterID {
			replay = append(replay, cloneEvent(event))
		}
	}
	if r.status != StatusRunning {
		closed := make(chan Event)
		close(closed)
		return replay, closed, func() {}, nil
	}
	if len(r.subscribers) >= r.manager.cfg.MaxSubscribers {
		return nil, nil, nil, ErrTooManyStreams
	}
	ch := make(chan Event, r.manager.cfg.SubscriberBuffer)
	r.subscribers[ch] = struct{}{}
	var once sync.Once
	cancel := func() {
		once.Do(func() {
			r.mu.Lock()
			if _, ok := r.subscribers[ch]; ok {
				delete(r.subscribers, ch)
				close(ch)
			}
			r.mu.Unlock()
		})
	}
	return replay, ch, cancel, nil
}

func percentile(sorted []float64, p float64) float64 {
	if len(sorted) == 0 {
		return 0
	}
	index := int(float64(len(sorted)-1)*p + 0.5)
	return sorted[min(index, len(sorted)-1)]
}

func durationMS(d time.Duration) float64 {
	return float64(d) / float64(time.Millisecond)
}

func cloneTime(in *time.Time) *time.Time {
	if in == nil {
		return nil
	}
	out := *in
	return &out
}

func cloneEntry(in LeaderboardEntry) LeaderboardEntry {
	in.Words = append([]string(nil), in.Words...)
	return in
}

func cloneLeaderboard(in []LeaderboardEntry) []LeaderboardEntry {
	out := make([]LeaderboardEntry, len(in))
	for i := range in {
		out[i] = cloneEntry(in[i])
	}
	return out
}

func cloneEvent(in Event) Event {
	if in.Game != nil {
		game := *in.Game
		in.Game = &game
	}
	if in.Word != nil {
		word := *in.Word
		word.Path = append([]int(nil), in.Word.Path...)
		in.Word = &word
	}
	in.Path = append([]int(nil), in.Path...)
	if in.Result != nil {
		result := cloneEntry(*in.Result)
		in.Result = &result
	}
	if in.Stats != nil {
		stats := *in.Stats
		in.Stats = &stats
	}
	in.Leaderboard = cloneLeaderboard(in.Leaderboard)
	return in
}

func findPath(board wordhunt.Board, word string) []int {
	word = strings.ToLower(word)
	if len(word) == 0 || len(word) > wordhunt.NumTiles {
		return nil
	}
	path := make([]int, 0, len(word))
	var visit func(int, int, uint16) bool
	visit = func(pos, at int, used uint16) bool {
		tile := board.Tiles[pos]
		if tile >= 'A' && tile <= 'Z' {
			tile += 'a' - 'A'
		}
		if tile != word[at] {
			return false
		}
		path = append(path, pos)
		if at == len(word)-1 {
			return true
		}
		used |= 1 << pos
		row, col := pos/wordhunt.Size, pos%wordhunt.Size
		for dr := -1; dr <= 1; dr++ {
			for dc := -1; dc <= 1; dc++ {
				if dr == 0 && dc == 0 {
					continue
				}
				nextRow, nextCol := row+dr, col+dc
				if nextRow < 0 || nextRow >= wordhunt.Size || nextCol < 0 || nextCol >= wordhunt.Size {
					continue
				}
				next := nextRow*wordhunt.Size + nextCol
				if used&(1<<next) == 0 && visit(next, at+1, used) {
					return true
				}
			}
		}
		path = path[:len(path)-1]
		return false
	}
	for pos := 0; pos < wordhunt.NumTiles; pos++ {
		path = path[:0]
		if visit(pos, 0, 0) {
			return append([]int(nil), path...)
		}
	}
	return nil
}

func newID() string {
	var raw [8]byte
	if _, err := rand.Read(raw[:]); err == nil {
		return hex.EncodeToString(raw[:])
	}
	return fmt.Sprintf("%x", time.Now().UnixNano())
}

func randomSeed() int64 {
	var raw [8]byte
	if _, err := rand.Read(raw[:]); err == nil {
		return int64(binary.LittleEndian.Uint64(raw[:]) & uint64(^uint64(0)>>1))
	}
	return time.Now().UnixNano()
}

func seedFor(runID string, index int) int64 {
	var seed int64 = 1469598103934665603
	for _, b := range []byte(runID) {
		seed ^= int64(b)
		seed *= 1099511628211
	}
	return seed ^ int64(index+1)*0x517cc1b727220a95
}

func funName(runID string, index int) string {
	adjectives := [...]string{
		"Turbo", "Cosmic", "Sneaky", "Pixel", "Neon", "Mighty",
		"Quirky", "Rocket", "Fuzzy", "Clever", "Disco", "Hyper",
	}
	animals := [...]string{
		"Axolotl", "Badger", "Capybara", "Dingo", "Falcon", "Gecko",
		"Koala", "Narwhal", "Otter", "Panda", "Raven", "Yak",
	}
	offset := 0
	for _, b := range []byte(runID) {
		offset += int(b)
	}
	return fmt.Sprintf("%s %s %02d",
		adjectives[(index+offset)%len(adjectives)],
		animals[(index*5+offset)%len(animals)],
		index+1,
	)
}

func valueOr(value, fallback string) string {
	if value != "" {
		return value
	}
	return fallback
}

func pointer[T any](value T) *T { return &value }
