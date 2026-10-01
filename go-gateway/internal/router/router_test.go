package router

import (
	"context"
	"errors"
	"testing"
	"time"

	"go-gateway/internal/players"
	"go-gateway/internal/wordhunt"

	"google.golang.org/genai"
)

type scriptedPlayer struct {
	errs  []error
	calls int
}

func (p *scriptedPlayer) Name() string { return "scripted" }
func (p *scriptedPlayer) Play(context.Context, wordhunt.Board, time.Time) (players.Result, error) {
	err := p.errs[min(p.calls, len(p.errs)-1)]
	p.calls++
	return players.Result{Backend: "test"}, err
}

func TestRetryPlayer(t *testing.T) {
	inner := &scriptedPlayer{errs: []error{errors.New("503"), errors.New("429"), nil}}
	p := &RetryPlayer{
		Player:   inner,
		Attempts: 3,
		Sleep:    func(context.Context, time.Duration) error { return nil },
	}
	if _, err := p.Play(context.Background(), wordhunt.Board{}, time.Now().Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	if inner.calls != 3 {
		t.Fatalf("calls = %d, want 3", inner.calls)
	}
}

func TestCircuitBreakerTransitions(t *testing.T) {
	now := time.Unix(100, 0)
	b := NewCircuitBreaker()
	b.now = func() time.Time { return now }
	for range 5 {
		b.Record(errors.New("failed"))
	}
	if b.Allow() {
		t.Fatal("breaker should be open")
	}
	now = now.Add(16 * time.Second)
	if !b.Allow() {
		t.Fatal("breaker should be half-open")
	}
	b.Record(nil)
	if !b.Allow() {
		t.Fatal("breaker should close after success")
	}
}

func TestCircuitBreakerOpensOnRollingErrorRate(t *testing.T) {
	b := NewCircuitBreaker()
	for i := range 20 {
		var err error
		if i%2 == 0 || i == 19 {
			err = errors.New("failed")
		}
		b.Record(err)
	}
	if b.Allow() {
		t.Fatal("breaker should open above 50% failures over 20 calls")
	}
}

func TestFallbackPlayer(t *testing.T) {
	first := &scriptedPlayer{errs: []error{errors.New("down")}}
	second := &scriptedPlayer{errs: []error{nil}}
	p := &FallbackPlayer{Chain: []players.Player{first, second}}
	result, err := p.Play(context.Background(), wordhunt.Board{}, time.Now().Add(time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if !result.Fallback || result.Backend != "test" {
		t.Fatalf("unexpected result: %#v", result)
	}
}

func TestEmptyFallbackPlayer(t *testing.T) {
	p := &FallbackPlayer{}
	if p.Name() != "unconfigured" {
		t.Fatalf("name = %q", p.Name())
	}
	if _, err := p.Play(context.Background(), wordhunt.Board{}, time.Now()); !errors.Is(err, ErrNoBackends) {
		t.Fatalf("error = %v", err)
	}
}

func TestGeminiRetryable(t *testing.T) {
	for _, err := range []error{
		context.DeadlineExceeded,
		genai.APIError{Code: 429},
		genai.APIError{Code: 500},
		genai.APIError{Code: 503},
	} {
		if !GeminiRetryable(err) {
			t.Fatalf("expected retryable: %v", err)
		}
	}
	if GeminiRetryable(genai.APIError{Code: 400}) {
		t.Fatal("400 must not be retried")
	}
}
