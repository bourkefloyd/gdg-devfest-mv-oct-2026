package api

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"regexp"
	"strings"
	"sync"
	"time"

	"go-gateway/internal/players"
	"go-gateway/internal/wordhunt"
)

const (
	maxClaimsPerMove = 150
	maxRejectedKept  = 200
	humanSeat        = "human"
)

var wordRE = regexp.MustCompile(`^[a-z]{3,16}$`)

type WordEntry struct {
	Word   string `json:"word"`
	Path   []int  `json:"path,omitempty"`
	Points int    `json:"points"`
	Reason string `json:"reason"`
}

type playerState struct {
	Name      string      `json:"name"`
	Kind      string      `json:"kind"` // "model" or "human"
	Status    string      `json:"status"`
	Backend   string      `json:"backend,omitempty"`
	Model     string      `json:"model,omitempty"`
	Fallback  bool        `json:"fallback"`
	LatencyMs int64       `json:"latency_ms,omitempty"`
	Error     string      `json:"error,omitempty"`
	Score     int         `json:"score"`
	Accepted  []WordEntry `json:"accepted"`
	Rejected  []WordEntry `json:"rejected"`
	Dropped   int         `json:"rejected_dropped,omitempty"`

	seen map[string]bool
}

type Event struct {
	ID   int    `json:"-"`
	Type string `json:"-"`
	Data any    `json:"-"`
}

type Game struct {
	ID        string
	Mode      string
	KeyID     string
	Board     wordhunt.Board
	CreatedAt time.Time

	streamToken string
	dict        *wordhunt.Dict

	mu      sync.Mutex
	endsAt  time.Time
	over     bool
	maxScore *int // set at game over
	closed   bool // no more events will be published
	order   []string
	players map[string]*playerState
	events  []Event
	subs    map[chan struct{}]struct{}
}

func newID() string {
	var b [12]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}

func (g *Game) checkStreamToken(tok string) bool {
	return tok != "" && subtle.ConstantTimeCompare([]byte(tok), []byte(g.streamToken)) == 1
}

func (g *Game) addPlayer(name, kind string) {
	g.order = append(g.order, name)
	g.players[name] = &playerState{Name: name, Kind: kind, Status: "waiting",
		Accepted: []WordEntry{}, Rejected: []WordEntry{}, seen: map[string]bool{}}
}

// publishLocked appends an event to the replay log and wakes subscribers.
func (g *Game) publishLocked(typ string, data any) {
	if g.closed {
		return
	}
	g.events = append(g.events, Event{ID: len(g.events) + 1, Type: typ, Data: data})
	for ch := range g.subs {
		select {
		case ch <- struct{}{}:
		default:
		}
	}
}

func (g *Game) publish(typ string, data any) {
	g.mu.Lock()
	g.publishLocked(typ, data)
	g.mu.Unlock()
}

func (g *Game) closeEvents() {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.closed {
		return
	}
	g.closed = true
	for ch := range g.subs {
		close(ch)
	}
	g.subs = nil
}

// eventsSince returns events after `after`; open=false means the stream is
// closed and no further events will arrive.
func (g *Game) eventsSince(after int) ([]Event, bool) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if after < 0 || after > len(g.events) {
		after = len(g.events)
	}
	out := append([]Event(nil), g.events[after:]...)
	return out, !g.closed
}

func (g *Game) subscribe() chan struct{} {
	ch := make(chan struct{}, 1)
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.closed {
		close(ch)
		return ch
	}
	g.subs[ch] = struct{}{}
	return ch
}

func (g *Game) unsubscribe(ch chan struct{}) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if _, ok := g.subs[ch]; ok {
		delete(g.subs, ch)
	}
}

// claimLocked runs one untrusted candidate through the shared validation
// pipeline: server-clock deadline, normalization, dedupe, dictionary and
// board path check, then server-side scoring.
func (g *Game) claimLocked(p *playerState, raw string, path []int, now time.Time) WordEntry {
	word := strings.ToLower(strings.TrimSpace(raw))
	e := WordEntry{Word: word, Path: path}
	reject := func(r wordhunt.Reason) WordEntry {
		e.Reason = string(r)
		validationRejects.WithLabelValues(e.Reason).Inc()
		if len(p.Rejected) < maxRejectedKept {
			if len(e.Word) > wordhunt.MaxLen+8 {
				e.Word = e.Word[:wordhunt.MaxLen+8]
			}
			if len(e.Path) > wordhunt.MaxLen {
				e.Path = e.Path[:wordhunt.MaxLen]
			}
			p.Rejected = append(p.Rejected, e)
		} else {
			p.Dropped++
		}
		return e
	}
	switch {
	case g.over || !now.Before(g.endsAt):
		return reject(wordhunt.ReasonLate)
	case !wordRE.MatchString(word):
		return reject(wordhunt.ReasonInvalidWord)
	case len(path) > wordhunt.MaxLen:
		return reject(wordhunt.ReasonLenMismatch)
	case p.seen[word]:
		return reject(wordhunt.ReasonDuplicate)
	}
	for _, i := range path {
		if i < 0 || i >= wordhunt.NumTiles {
			return reject(wordhunt.ReasonBadIndex)
		}
	}
	if ok, reason := wordhunt.ValidateWord(g.Board, g.dict, word, path); !ok {
		return reject(reason)
	}
	p.seen[word] = true
	e.Points = wordhunt.Score(word)
	e.Reason = string(wordhunt.ReasonOK)
	p.Score += e.Points
	p.Accepted = append(p.Accepted, e)
	return e
}

func (g *Game) summaryLocked(final bool) players.GameSummary {
	sum := players.GameSummary{GameID: g.ID, Tiles: g.Board.String(), Final: final}
	for _, name := range g.order {
		p := g.players[name]
		ps := players.PlayerSummary{Name: p.Name, Backend: p.Backend, Model: p.Model,
			Fallback: p.Fallback, Score: p.Score, Rejected: len(p.Rejected) + p.Dropped}
		for _, a := range p.Accepted {
			ps.Accepted = append(ps.Accepted, a.Word)
		}
		sum.Players = append(sum.Players, ps)
	}
	return sum
}

type GameView struct {
	GameID   string         `json:"game_id"`
	Mode     string         `json:"mode"`
	Tiles    string         `json:"tiles"`
	Seed     int64          `json:"seed"`
	EndsAt   time.Time      `json:"ends_at"`
	Over     bool           `json:"over"`
	Players  []*playerState `json:"players"`
	MaxScore *int           `json:"max_score,omitempty"`
}

func (g *Game) viewLocked(maxScore *int) GameView {
	v := GameView{GameID: g.ID, Mode: g.Mode, Tiles: g.Board.String(), Seed: g.Board.Seed,
		EndsAt: g.endsAt, Over: g.over, MaxScore: maxScore}
	for _, name := range g.order {
		cp := *g.players[name]
		cp.Accepted = append([]WordEntry(nil), cp.Accepted...)
		cp.Rejected = append([]WordEntry(nil), cp.Rejected...)
		v.Players = append(v.Players, &cp)
	}
	return v
}

func (g *Game) view() GameView {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.viewLocked(g.maxScore)
}

func maxScore(b wordhunt.Board, d *wordhunt.Dict) int {
	m := 0
	for _, w := range b.Solve(d) {
		m += wordhunt.Score(w)
	}
	return m
}

type store struct {
	mu      sync.Mutex
	games   map[string]*Game
	active  int
	byKey   map[string]int
	expires map[string]time.Time

	lastSweep time.Time
}

func newStore() *store {
	return &store{games: map[string]*Game{}, byKey: map[string]int{}, expires: map[string]time.Time{}}
}

func (s *store) get(id string) *Game {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.games[id]
}

// admit reserves an active-game slot for key, enforcing global and per-key caps.
func (s *store) admit(key string, global, perKey int) (string, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.active >= global {
		return "too_many_active_games", false
	}
	if s.byKey[key] >= perKey {
		return "too_many_active_games_for_key", false
	}
	s.active++
	s.byKey[key]++
	gamesActive.Inc()
	return "", true
}

func (s *store) finish(key string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.active--
	if s.byKey[key]--; s.byKey[key] <= 0 {
		delete(s.byKey, key)
	}
	gamesActive.Dec()
}

func (s *store) put(g *Game, retain time.Duration) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if now := time.Now(); now.Sub(s.lastSweep) > time.Second {
		s.lastSweep = now
		for id, exp := range s.expires {
			if now.After(exp) {
				delete(s.games, id)
				delete(s.expires, id)
			}
		}
	}
	s.games[g.ID] = g
	s.expires[g.ID] = g.CreatedAt.Add(retain)
}

func (s *store) closeAll() {
	s.mu.Lock()
	gs := make([]*Game, 0, len(s.games))
	for _, g := range s.games {
		gs = append(gs, g)
	}
	s.mu.Unlock()
	for _, g := range gs {
		g.closeEvents()
	}
}
