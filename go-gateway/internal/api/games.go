package api

import (
	"context"
	"crypto/rand"
	"encoding/binary"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"go-gateway/internal/players"
	"go-gateway/internal/wordhunt"
)

type createGameReq struct {
	Mode      string `json:"mode"`
	DurationS *int   `json:"duration_s"`
	Seed      *int64 `json:"seed"`
	Agent     bool   `json:"agent"`
}

type createGameResp struct {
	GameID      string    `json:"game_id"`
	Mode        string    `json:"mode"`
	Tiles       string    `json:"tiles"`
	Seed        int64     `json:"seed"`
	DurationS   int       `json:"duration_s"`
	EndsAt      time.Time `json:"ends_at"`
	Players     []string  `json:"players"`
	StreamToken string    `json:"stream_token"`
}

func randSeed() int64 {
	var b [8]byte
	_, _ = rand.Read(b[:])
	return int64(binary.LittleEndian.Uint64(b[:]) &^ (1 << 63))
}

func (s *Server) handleCreateGame(w http.ResponseWriter, r *http.Request) {
	var req createGameReq
	if !s.decodeJSON(w, r, &req) {
		return
	}
	if req.Mode == "" {
		req.Mode = ModeRace
	}
	if req.Mode != ModeRace && req.Mode != ModeHuman && req.Mode != ModeLoad {
		writeError(w, http.StatusBadRequest, "invalid_mode", "mode must be race, human_vs_gemini or load")
		return
	}
	dur := s.cfg.DefaultDuration
	if req.DurationS != nil {
		dur = time.Duration(*req.DurationS) * time.Second
		if dur < s.cfg.MinDuration || dur > s.cfg.MaxDuration {
			writeError(w, http.StatusBadRequest, "invalid_duration",
				fmt.Sprintf("duration_s must be between %d and %d", int(s.cfg.MinDuration.Seconds()), int(s.cfg.MaxDuration.Seconds())))
			return
		}
	}
	seed := randSeed()
	if req.Seed != nil {
		seed = *req.Seed
	}

	key := keyID(r.Context())
	if code, ok := s.store.admit(key, s.cfg.MaxActiveGames, s.cfg.MaxActivePerKey); !ok {
		rateLimited.WithLabelValues(code).Inc()
		w.Header().Set("Retry-After", "5")
		writeError(w, http.StatusTooManyRequests, code, "active game limit reached")
		return
	}
	var seats []players.Player
	seatFn := s.cfg.Seats
	if req.Agent && s.cfg.AgentSeats != nil {
		seatFn = s.cfg.AgentSeats
	}
	if seatFn != nil {
		var err error
		if seats, err = seatFn(req.Mode); err != nil {
			s.store.finish(key)
			writeError(w, http.StatusServiceUnavailable, "no_players", "players unavailable for this mode")
			return
		}
	}

	now := time.Now()
	g := &Game{
		ID: newID(), Mode: req.Mode, KeyID: key, CreatedAt: now,
		Board:       wordhunt.NewBoard(seed, s.cfg.MinBoardWords),
		streamToken: newID() + newID(),
		dict:        s.cfg.Dict,
		endsAt:      now.Add(dur),
		players:     map[string]*playerState{},
		subs:        map[chan struct{}]struct{}{},
	}
	names := map[string]int{}
	seatNames := make([]string, len(seats))
	for i, p := range seats {
		name := p.Name()
		if names[name]++; names[name] > 1 || name == humanSeat {
			name = fmt.Sprintf("%s#%d", name, names[name])
		}
		seatNames[i] = name
		g.addPlayer(name, "model")
	}
	hasHuman := req.Mode == ModeHuman || req.Mode == ModeLoad
	if hasHuman {
		g.addPlayer(humanSeat, "human")
	}
	s.store.put(g, dur+10*time.Minute)
	gamesTotal.WithLabelValues(req.Mode).Inc()

	resp := createGameResp{
		GameID: g.ID, Mode: g.Mode, Tiles: g.Board.String(), Seed: seed,
		DurationS: int(dur / time.Second), EndsAt: g.endsAt,
		Players: append([]string(nil), g.order...), StreamToken: g.streamToken,
	}
	s.wg.Add(1)
	go s.runGame(g, seats, seatNames, hasHuman)
	writeJSON(w, http.StatusCreated, resp)
}

// ownedGame returns the game only if it belongs to the caller's key; other
// keys get the same 404 as a missing game.
func (s *Server) ownedGame(w http.ResponseWriter, r *http.Request) *Game {
	g := s.store.get(r.PathValue("id"))
	if g == nil || g.KeyID != keyID(r.Context()) {
		writeError(w, http.StatusNotFound, "game_not_found", "no such game")
		return nil
	}
	return g
}

func (s *Server) handleGetGame(w http.ResponseWriter, r *http.Request) {
	if g := s.ownedGame(w, r); g != nil {
		writeJSON(w, http.StatusOK, g.view())
	}
}

type submitReq struct {
	Word string `json:"word"`
	Path []int  `json:"path"`
}

type submitResp struct {
	Accepted bool   `json:"accepted"`
	Reason   string `json:"reason"`
	Points   int    `json:"points"`
	Total    int    `json:"total"`
}

// Malformed submissions are client errors; game-rule rejections are 200s.
var badRequestReasons = map[string]bool{
	string(wordhunt.ReasonInvalidWord): true,
	string(wordhunt.ReasonBadIndex):    true,
	string(wordhunt.ReasonLenMismatch): true,
}

func (s *Server) handleSubmitWord(w http.ResponseWriter, r *http.Request) {
	g := s.ownedGame(w, r)
	if g == nil {
		return
	}
	var req submitReq
	if !s.decodeJSON(w, r, &req) {
		return
	}
	g.mu.Lock()
	p := g.players[humanSeat]
	if p == nil {
		g.mu.Unlock()
		writeError(w, http.StatusConflict, "no_human_seat", "this game has no human player")
		return
	}
	e := g.claimLocked(p, req.Word, req.Path, time.Now())
	total := p.Score
	g.publishLocked("word", wordEvent(humanSeat, e, total))
	g.mu.Unlock()

	if badRequestReasons[e.Reason] {
		writeError(w, http.StatusBadRequest, e.Reason, "word rejected: "+e.Reason)
		return
	}
	writeJSON(w, http.StatusOK, submitResp{Accepted: e.Reason == string(wordhunt.ReasonOK), Reason: e.Reason, Points: e.Points, Total: total})
}

func wordEvent(player string, e WordEntry, total int) map[string]any {
	return map[string]any{"player": player, "word": e.Word, "accepted": e.Reason == string(wordhunt.ReasonOK),
		"reason": e.Reason, "points": e.Points, "total": total}
}

func (s *Server) runGame(g *Game, seats []players.Player, names []string, hasHuman bool) {
	defer s.wg.Done()
	endsAt := g.endsAt
	ctx, cancel := context.WithDeadline(s.ctx, endsAt)
	defer cancel()

	seatsDone := make(chan struct{})
	go func() {
		defer close(seatsDone)
		done := make(chan struct{}, len(seats))
		for i, p := range seats {
			go func() { s.playSeat(ctx, g, endsAt, names[i], p); done <- struct{}{} }()
		}
		for range seats {
			<-done
		}
	}()

	timer := time.NewTimer(time.Until(endsAt))
	defer timer.Stop()
	early := seatsDone
	if hasHuman {
		early = nil
	}
	select {
	case <-early:
	case <-timer.C:
	case <-s.ctx.Done():
	}

	g.mu.Lock()
	g.over = true
	if now := time.Now(); now.Before(g.endsAt) {
		g.endsAt = now
	}
	m := maxScore(g.Board, g.dict)
	g.maxScore = &m
	summary := g.summaryLocked(true)
	g.publishLocked("game_over", g.viewLocked(g.maxScore))
	g.mu.Unlock()
	s.store.finish(g.KeyID)
	cancel()

	select {
	case <-seatsDone:
	case <-time.After(2 * time.Second):
	}
	if s.cfg.Commentator != nil && s.ctx.Err() == nil {
		s.streamCommentary(g, summary)
	}
	g.closeEvents()
}

func (s *Server) streamCommentary(g *Game, summary players.GameSummary) {
	ctx, cancel := context.WithTimeout(s.ctx, s.cfg.CommentTimeout)
	defer cancel()
	out := make(chan string, 16)
	errc := make(chan error, 1)
	go func() { errc <- s.cfg.Commentator.Stream(ctx, summary, out); close(out) }()
	for chunk := range out {
		g.publish("commentary", map[string]any{"text": chunk})
	}
	if err := <-errc; err != nil {
		s.cfg.Logger.Warn("commentary failed", "game", g.ID, "err", errClass(err))
	}
}

// moveDeadline is min(round_end - 3s, now + 25s), relaxed for very short rounds.
func moveDeadline(now, endsAt time.Time) time.Time {
	d := endsAt.Add(-3 * time.Second)
	if d.Sub(now) < 2*time.Second {
		d = endsAt
	}
	if cap := now.Add(25 * time.Second); d.After(cap) {
		d = cap
	}
	return d
}

func (s *Server) playSeat(ctx context.Context, g *Game, endsAt time.Time, name string, p players.Player) {
	now := time.Now()
	deadline := moveDeadline(now, endsAt)
	mctx, cancel := context.WithDeadline(ctx, deadline)
	defer cancel()

	g.mu.Lock()
	g.players[name].Status = "playing"
	g.publishLocked("player_started", map[string]any{"player": name})
	g.mu.Unlock()

	res, err := s.safePlay(mctx, p, g.Board, deadline)
	lat := res.Latency
	if lat == 0 {
		lat = time.Since(now)
	}
	backend := res.Backend
	if backend == "" {
		backend = "unknown"
	}
	outcome := "ok"
	if err != nil {
		outcome = errClass(err)
	}
	modelCall.WithLabelValues(backend, outcome).Observe(lat.Seconds())
	if res.Fallback {
		fallbackTotal.WithLabelValues(name, backend).Inc()
	}

	g.mu.Lock()
	defer g.mu.Unlock()
	ps := g.players[name]
	ps.Backend, ps.Model, ps.Fallback, ps.LatencyMs = res.Backend, res.Model, res.Fallback, lat.Milliseconds()
	if err != nil {
		ps.Status, ps.Error = "error", "move failed: "+outcome
	} else {
		ps.Status = "done"
		claims := res.Claims
		if len(claims) > maxClaimsPerMove {
			ps.Dropped += len(claims) - maxClaimsPerMove
			claims = claims[:maxClaimsPerMove]
		}
		t := time.Now()
		for _, c := range claims {
			// Model paths are advisory: score the word if any valid path
			// exists, and only record whether the claimed path was right.
			recordPathClaim(g, c)
			e := g.claimLocked(ps, c.Word, nil, t)
			g.publishLocked("word", wordEvent(name, e, ps.Score))
		}
	}
	g.publishLocked("player_result", map[string]any{
		"player": name, "status": ps.Status, "backend": ps.Backend, "model": ps.Model,
		"fallback": ps.Fallback, "latency_ms": ps.LatencyMs, "score": ps.Score,
		"accepted": len(ps.Accepted), "rejected": len(ps.Rejected) + ps.Dropped, "error": ps.Error,
	})
}

func recordPathClaim(g *Game, c players.Claim) {
	switch {
	case len(c.Path) == 0:
		pathClaims.WithLabelValues("none").Inc()
	case validPathIndices(c.Path):
		if ok, _ := wordhunt.ValidateWord(g.Board, g.dict, strings.ToLower(c.Word), c.Path); ok {
			pathClaims.WithLabelValues("correct").Inc()
			return
		}
		fallthrough
	default:
		pathClaims.WithLabelValues("wrong").Inc()
	}
}

func validPathIndices(path []int) bool {
	if len(path) > wordhunt.MaxLen {
		return false
	}
	for _, i := range path {
		if i < 0 || i >= wordhunt.NumTiles {
			return false
		}
	}
	return true
}

func (s *Server) safePlay(ctx context.Context, p players.Player, b wordhunt.Board, deadline time.Time) (res players.Result, err error) {
	defer func() {
		if v := recover(); v != nil {
			s.cfg.Logger.Error("player panic", "player", p.Name(), "err", v)
			err = errors.New("player panic")
		}
	}()
	return p.Play(ctx, b, deadline)
}

func errClass(err error) string {
	switch {
	case err == nil:
		return "ok"
	case errors.Is(err, context.DeadlineExceeded):
		return "timeout"
	case errors.Is(err, context.Canceled):
		return "canceled"
	default:
		return "error"
	}
}
