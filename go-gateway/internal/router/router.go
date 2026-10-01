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
	if err == nil {
		b.failures = 0
		b.state = closed
		return
	}
	b.failures++
	if b.failures >= 5 || b.state == halfOpen {
		b.state = open
		b.openedAt = b.now()
	}
}

var ErrCircuitOpen = errors.New("model circuit breaker is open")

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
	return p.Chain[0].Name()
}

func (p *FallbackPlayer) Play(ctx context.Context, b wordhunt.Board, deadline time.Time) (players.Result, error) {
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
