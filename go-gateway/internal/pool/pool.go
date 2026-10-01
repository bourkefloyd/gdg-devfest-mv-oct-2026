// Package pool manages N Rust Gemma gRPC workers. Each Rust worker holds a
// single mutex around generation, so the pool treats every worker as
// concurrency 1 (configurable), picks the least-outstanding healthy worker,
// and bounds how long callers wait in the admission queue before spilling.
package pool

import (
	"errors"
	"fmt"
	"sync"
	"time"

	pb "go-gateway/proto"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

var (
	ErrNoWorkers    = errors.New("pool: no workers configured")
	ErrQueueFull    = errors.New("pool: admission queue full")
	ErrQueueTimeout = errors.New("pool: queue wait exceeded")
	ErrStreamIdle   = errors.New("pool: worker stream idle timeout")
)

type Config struct {
	Addrs        []string
	PerWorker    int           // concurrent generations per worker (Rust worker: 1)
	MaxQueue     int           // callers allowed to wait for a slot
	MaxWait      time.Duration // admission wait before ErrQueueTimeout
	IdleTimeout  time.Duration // max gap between streamed tokens
	FailCooldown time.Duration // a failed worker is skipped for this long
	DialOpts     []grpc.DialOption
}

func (c *Config) defaults() {
	if c.PerWorker <= 0 {
		c.PerWorker = 1
	}
	if c.MaxQueue <= 0 {
		c.MaxQueue = 64
	}
	if c.MaxWait <= 0 {
		c.MaxWait = 5 * time.Second
	}
	if c.IdleTimeout <= 0 {
		c.IdleTimeout = 10 * time.Second
	}
	if c.FailCooldown <= 0 {
		c.FailCooldown = 5 * time.Second
	}
	if len(c.DialOpts) == 0 {
		c.DialOpts = []grpc.DialOption{grpc.WithTransportCredentials(insecure.NewCredentials())}
	}
}

type worker struct {
	addr        string
	conn        *grpc.ClientConn
	client      pb.InferenceServiceClient
	outstanding int
	failedAt    time.Time
}

type Pool struct {
	cfg     Config
	mu      sync.Mutex
	freed   chan struct{} // closed and replaced whenever a slot frees up
	workers []*worker
	waiting int
}

func New(cfg Config) (*Pool, error) {
	cfg.defaults()
	if len(cfg.Addrs) == 0 {
		return nil, ErrNoWorkers
	}
	p := &Pool{cfg: cfg, freed: make(chan struct{})}
	for _, a := range cfg.Addrs {
		conn, err := grpc.NewClient(a, cfg.DialOpts...)
		if err != nil {
			p.Close()
			return nil, fmt.Errorf("pool: dial %s: %w", a, err)
		}
		p.workers = append(p.workers, &worker{addr: a, conn: conn, client: pb.NewInferenceServiceClient(conn)})
	}
	return p, nil
}

func (p *Pool) Close() {
	for _, w := range p.workers {
		w.conn.Close()
	}
}

func (p *Pool) healthyLocked(w *worker) bool {
	return w.failedAt.IsZero() || time.Since(w.failedAt) > p.cfg.FailCooldown
}

type Stats struct {
	Workers     int `json:"workers"`
	Healthy     int `json:"healthy"`
	Outstanding int `json:"outstanding"`
	Waiting     int `json:"waiting"`
}

func (p *Pool) Stats() Stats {
	p.mu.Lock()
	defer p.mu.Unlock()
	s := Stats{Workers: len(p.workers), Waiting: p.waiting}
	for _, w := range p.workers {
		s.Outstanding += w.outstanding
		if p.healthyLocked(w) {
			s.Healthy++
		}
	}
	return s
}

// Healthy reports workers not currently in failure cooldown.
func (p *Pool) Healthy() int { return p.Stats().Healthy }
