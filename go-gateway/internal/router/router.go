// Package router provides model retries, circuit breaking, and fallback.
package router

import (
	"context"
	"errors"
	"math/rand/v2"
	"sync"
	"time"

	"go-gateway/internal/players"
	"go-gateway/internal/wordhunt"

	"google.golang.org/genai"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type RetryPlayer struct {
	Player    players.Player
	Attempts  int
	Retryable func(error) bool
	Sleep     func(context.Context, time.Duration) error
}

func (p *RetryPlayer) Name() string { return p.Player.Name() }

func (p *RetryPlayer) Play(ctx context.Context, b wordhunt.Board, deadline time.Time) (players.Result, error) {
	attempts := p.Attempts
	if attempts < 1 {
		attempts = 2
	}
	sleep := p.Sleep
	if sleep == nil {
		sleep = sleepContext
	}
	var err error
	for attempt := 0; attempt < attempts; attempt++ {
		var result players.Result
		result, err = p.Player.Play(ctx, b, deadline)
		if err == nil {
			return result, nil
		}
		if p.Retryable != nil && !p.Retryable(err) {
			break
		}
		if attempt+1 < attempts {
			base := 250 * time.Millisecond
			if attempt > 0 {
				base = time.Second
			}
			if err := sleep(ctx, time.Duration(rand.Int64N(int64(base)+1))); err != nil {
				return players.Result{}, err
			}
		}
	}
	return players.Result{}, err
}

type breakerState uint8

type outcome struct {
	at     time.Time
	failed bool
}

const (
	closed breakerState = iota
	open
	halfOpen
)

type CircuitBreaker struct {
	mu            sync.Mutex
	state         breakerState
	failures      int
	openedAt      time.Time
	halfOpenAfter time.Duration
	now           func() time.Time
	recent        []outcome
}

func NewCircuitBreaker() *CircuitBreaker {
	return &CircuitBreaker{halfOpenAfter: 15 * time.Second, now: time.Now}
}

func (b *CircuitBreaker) Allow() bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.state != open {
		return true
	}
	if b.now().Sub(b.openedAt) >= b.halfOpenAfter {
		b.state = halfOpen
		return true
	}
	return false
}

func (b *CircuitBreaker) Record(err error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	now := b.now()
	cutoff := now.Add(-30 * time.Second)
	kept := b.recent[:0]
	for _, item := range b.recent {
		if !item.at.Before(cutoff) {
			kept = append(kept, item)
		}
	}
	b.recent = append(kept, outcome{at: now, failed: err != nil})
	if len(b.recent) > 20 {
		b.recent = b.recent[len(b.recent)-20:]
	}
	if err == nil {
		b.failures = 0
		b.state = closed
		if len(b.recent) == 1 {
			b.recent = nil
		}
		return
	}
	b.failures++
	windowFailures := 0
	for _, item := range b.recent {
		if item.failed {
			windowFailures++
		}
	}
	if b.failures >= 5 || b.state == halfOpen ||
		(len(b.recent) >= 20 && windowFailures*2 > len(b.recent)) {
		b.state = open
		b.openedAt = now
	}
}

// GeminiRetryable implements the retry policy for transient SDK failures.
func GeminiRetryable(err error) bool {
	if errors.Is(err, context.DeadlineExceeded) {
		return true
	}
	var apiErr genai.APIError
	if errors.As(err, &apiErr) {
		return apiErr.Code == 429 || apiErr.Code == 500 || apiErr.Code == 503
	}
	code := status.Code(err)
	return code == codes.ResourceExhausted || code == codes.Internal ||
		code == codes.Unavailable || code == codes.DeadlineExceeded
}

var ErrCircuitOpen = errors.New("model circuit breaker is open")
var ErrNoBackends = errors.New("model fallback chain is empty")

type BreakerPlayer struct {
	Player  players.Player
	Breaker *CircuitBreaker
}

func (p *BreakerPlayer) Name() string { return p.Player.Name() }

func (p *BreakerPlayer) Play(ctx context.Context, b wordhunt.Board, deadline time.Time) (players.Result, error) {
	if !p.Breaker.Allow() {
		return players.Result{}, ErrCircuitOpen
	}
	result, err := p.Player.Play(ctx, b, deadline)
	p.Breaker.Record(err)
	return result, err
}

// FallbackPlayer tries players in order and always labels a non-primary result.
type FallbackPlayer struct {
	NameLabel string
	Chain     []players.Player
}

func (p *FallbackPlayer) Name() string {
	if p.NameLabel != "" {
		return p.NameLabel
	}
	if len(p.Chain) == 0 {
		return "unconfigured"
	}
	return p.Chain[0].Name()
}

func (p *FallbackPlayer) Play(ctx context.Context, b wordhunt.Board, deadline time.Time) (players.Result, error) {
	if len(p.Chain) == 0 {
		return players.Result{}, ErrNoBackends
	}
	var errs []error
	for i, candidate := range p.Chain {
		result, err := candidate.Play(ctx, b, deadline)
		if err == nil {
			result.Fallback = result.Fallback || i > 0
			return result, nil
		}
		errs = append(errs, err)
		if ctx.Err() != nil {
			break
		}
	}
	return players.Result{}, errors.Join(errs...)
}

func sleepContext(ctx context.Context, d time.Duration) error {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}
