package pool

import (
	"context"
	"errors"
	"io"
	"time"

	pb "go-gateway/proto"
)

type Request struct {
	Prompt      string
	MaxTokens   int32
	Temperature float32
}

// Generate streams tokens from one worker into onToken and returns the worker
// address used. A worker that fails before its first token is retried once on
// a different worker; once a token has been delivered it never retries.
func (p *Pool) Generate(ctx context.Context, req Request, onToken func(string) error) (string, error) {
	var tried *worker
	var lastErr error
	for attempt := 0; attempt < 2; attempt++ {
		w, err := p.acquire(ctx, tried)
		if err != nil {
			if lastErr != nil {
				return tried.addr, lastErr
			}
			return "", err
		}
		started, err := p.stream(ctx, w, req, onToken)
		p.release(w, err != nil && ctx.Err() == nil)
		if err == nil {
			workerCalls.WithLabelValues(w.addr, "ok").Inc()
			return w.addr, nil
		}
		workerCalls.WithLabelValues(w.addr, "error").Inc()
		if started || ctx.Err() != nil || len(p.workers) < 2 {
			return w.addr, err
		}
		tried, lastErr = w, err
	}
	return tried.addr, lastErr
}

func (p *Pool) stream(ctx context.Context, w *worker, req Request, onToken func(string) error) (started bool, err error) {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	s, err := w.client.StreamGenerate(ctx, &pb.GenerateRequest{Prompt: req.Prompt, MaxTokens: req.MaxTokens, Temperature: req.Temperature})
	if err != nil {
		return false, err
	}
	type msg struct {
		r   *pb.GenerateResponse
		err error
	}
	ch := make(chan msg, 1)
	idle := time.NewTimer(p.cfg.IdleTimeout)
	defer idle.Stop()
	for {
		go func() {
			r, err := s.Recv()
			ch <- msg{r, err}
		}()
		select {
		case m := <-ch:
			if errors.Is(m.err, io.EOF) {
				return started, nil
			}
			if m.err != nil {
				return started, m.err
			}
			if m.r.Token != "" {
				started = true
				if err := onToken(m.r.Token); err != nil {
					return started, err
				}
			}
			if m.r.IsFinal {
				return started, nil
			}
			idle.Reset(p.cfg.IdleTimeout)
		case <-idle.C:
			cancel()
			<-ch
			return started, ErrStreamIdle
		case <-ctx.Done():
			<-ch
			return started, ctx.Err()
		}
	}
}

// Text runs Generate and returns the concatenated output, capped at maxBytes.
func (p *Pool) Text(ctx context.Context, req Request, maxBytes int) (string, string, error) {
	var buf []byte
	addr, err := p.Generate(ctx, req, func(tok string) error {
		if len(buf)+len(tok) > maxBytes {
			return errOutputCap
		}
		buf = append(buf, tok...)
		return nil
	})
	if errors.Is(err, errOutputCap) {
		err = nil
	}
	return string(buf), addr, err
}

var errOutputCap = errors.New("pool: output cap reached")
