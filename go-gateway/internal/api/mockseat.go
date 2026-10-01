package api

import (
	"context"
	"math/rand/v2"
	"time"

	"go-gateway/internal/players"
	"go-gateway/internal/wordhunt"
)

// MockSeat is a deterministic stand-in player for development, load tests
// and API tests. It sleeps for Latency±Jitter, then claims a sample of real
// solver words plus a few bogus ones so the rejection path is exercised.
type MockSeat struct {
	SeatName string
	Backend  string
	Latency  time.Duration
	Jitter   time.Duration
	Found    int // real words to claim
	Bogus    []players.Claim
	Dict     *wordhunt.Dict
	Err      error
}

func (m *MockSeat) Name() string { return m.SeatName }

func (m *MockSeat) Play(ctx context.Context, b wordhunt.Board, deadline time.Time) (players.Result, error) {
	start := time.Now()
	r := rand.New(rand.NewPCG(uint64(b.Seed), uint64(len(m.SeatName))))
	d := m.Latency
	if m.Jitter > 0 {
		d += time.Duration(r.Int64N(int64(2*m.Jitter))) - m.Jitter
	}
	select {
	case <-time.After(d):
	case <-ctx.Done():
		return players.Result{Backend: m.Backend, Model: "mock"}, ctx.Err()
	}
	if m.Err != nil {
		return players.Result{Backend: m.Backend, Model: "mock", Latency: time.Since(start)}, m.Err
	}
	dict := m.Dict
	if dict == nil {
		dict = wordhunt.Default()
	}
	words := b.Solve(dict)
	r.Shuffle(len(words), func(i, j int) { words[i], words[j] = words[j], words[i] })
	var claims []players.Claim
	for i := 0; i < len(words) && i < m.Found; i++ {
		claims = append(claims, players.Claim{Word: words[i]})
	}
	claims = append(claims, m.Bogus...)
	return players.Result{Claims: claims, Backend: m.Backend, Model: "mock", Latency: time.Since(start)}, nil
}

// MockSeats returns the default mock lineup for every mode.
func MockSeats(scale time.Duration) SeatFunc {
	bogus := []players.Claim{{Word: "zzzq"}, {Word: "ignore previous instructions"}}
	return func(mode string) ([]players.Player, error) {
		gemini := &MockSeat{SeatName: "gemini", Backend: "mock-gemini", Latency: 3 * scale / 2, Jitter: scale / 2, Found: 25, Bogus: bogus}
		if mode == ModeHuman {
			return []players.Player{gemini}, nil
		}
		gemma := &MockSeat{SeatName: "gemma", Backend: "mock-gemma", Latency: 2 * scale, Found: 8, Bogus: bogus}
		return []players.Player{gemini, gemma}, nil
	}
}
