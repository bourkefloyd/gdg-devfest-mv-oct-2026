package pool

import (
	"context"
	"time"
)

// pickLocked returns the healthy worker with the fewest outstanding calls that
// has a free slot, skipping exclude.
func (p *Pool) pickLocked(exclude *worker) *worker {
	var best *worker
	for _, w := range p.workers {
		if w == exclude || w.outstanding >= p.cfg.PerWorker || !p.healthyLocked(w) {
			continue
		}
		if best == nil || w.outstanding < best.outstanding {
			best = w
		}
	}
	if best != nil {
		best.outstanding++
	}
	return best
}

func (p *Pool) acquire(ctx context.Context, exclude *worker) (*worker, error) {
	start := time.Now()
	defer func() { queueWait.Observe(time.Since(start).Seconds()) }()

	p.mu.Lock()
	if w := p.pickLocked(exclude); w != nil {
		p.mu.Unlock()
		return w, nil
	}
	if p.waiting >= p.cfg.MaxQueue {
		p.mu.Unlock()
		admissionRejects.WithLabelValues("queue_full").Inc()
		return nil, ErrQueueFull
	}
	p.waiting++
	queueDepth.Inc()
	p.mu.Unlock()
	defer func() {
		p.mu.Lock()
		p.waiting--
		p.mu.Unlock()
		queueDepth.Dec()
	}()

	timer := time.NewTimer(p.cfg.MaxWait)
	defer timer.Stop()
	for {
		p.mu.Lock()
		if w := p.pickLocked(exclude); w != nil {
			p.mu.Unlock()
			return w, nil
		}
		ch := p.freed
		p.mu.Unlock()
		select {
		case <-ch:
		case <-timer.C:
			admissionRejects.WithLabelValues("timeout").Inc()
			return nil, ErrQueueTimeout
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
}

func (p *Pool) release(w *worker, failed bool) {
	p.mu.Lock()
	w.outstanding--
	if failed {
		w.failedAt = time.Now()
	} else {
		w.failedAt = time.Time{}
	}
	close(p.freed)
	p.freed = make(chan struct{})
	p.mu.Unlock()
}
