package players

import (
	"context"
	"time"

	"go-gateway/internal/wordhunt"
)

// MockPlayer is deterministic and useful for gateway and load tests.
type MockPlayer struct {
	PlayerName string
	Claims     []Claim
	Delay      time.Duration
	Err        error
}

func (p *MockPlayer) Name() string {
	if p.PlayerName == "" {
		return "Mock"
	}
	return p.PlayerName
}

func (p *MockPlayer) Play(ctx context.Context, _ wordhunt.Board, deadline time.Time) (Result, error) {
	start := time.Now()
	wait := time.NewTimer(p.Delay)
	defer wait.Stop()
	select {
	case <-ctx.Done():
		return Result{}, ctx.Err()
	case <-time.After(time.Until(deadline)):
		return Result{}, context.DeadlineExceeded
	case <-wait.C:
	}
	return Result{
		Claims:  append([]Claim(nil), p.Claims...),
		Backend: "mock",
		Model:   "deterministic",
		Latency: time.Since(start),
	}, p.Err
}

// SolverPlayer returns the authoritative solver output. Limit enables a weak,
// deterministic fallback instead of giving the seat a perfect score.
type SolverPlayer struct {
	Dict  *wordhunt.Dict
	Limit int
}

func (p *SolverPlayer) Name() string { return "Solver fallback" }

func (p *SolverPlayer) Play(ctx context.Context, b wordhunt.Board, deadline time.Time) (Result, error) {
	start := time.Now()
	select {
	case <-ctx.Done():
		return Result{}, ctx.Err()
	default:
	}
	if time.Now().After(deadline) {
		return Result{}, context.DeadlineExceeded
	}
	dict := p.Dict
	if dict == nil {
		dict = wordhunt.Default()
	}
	words := b.Solve(dict)
	limit := p.Limit
	if limit <= 0 || limit > len(words) {
		limit = len(words)
	}
	claims := make([]Claim, limit)
	for i := range claims {
		claims[i].Word = words[i]
	}
	return Result{
		Claims:   claims,
		Backend:  "solver",
		Model:    "trie-dfs",
		Latency:  time.Since(start),
		Fallback: true,
	}, nil
}
