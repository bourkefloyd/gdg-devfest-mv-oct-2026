package pool

import (
	"context"
	"errors"
	"net"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	pb "go-gateway/proto"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/test/bufconn"
)

// mockWorker mimics the Rust worker: one generation at a time behind a mutex.
type mockWorker struct {
	pb.UnimplementedInferenceServiceServer
	mu       sync.Mutex
	delay    time.Duration
	tokens   []string
	failPre  bool // fail before first token
	failMid  bool // fail after first token
	hang     bool // stop sending after first token
	calls    atomic.Int32
	inflight atomic.Int32
	maxSeen  atomic.Int32
}

func (m *mockWorker) StreamGenerate(req *pb.GenerateRequest, s grpc.ServerStreamingServer[pb.GenerateResponse]) error {
	m.calls.Add(1)
	m.mu.Lock()
	defer m.mu.Unlock()
	n := m.inflight.Add(1)
	defer m.inflight.Add(-1)
	if n > m.maxSeen.Load() {
		m.maxSeen.Store(n)
	}
	if m.failPre {
		return errors.New("boom")
	}
	time.Sleep(m.delay)
	for i, t := range m.tokens {
		if err := s.Send(&pb.GenerateResponse{Token: t, IsFinal: i == len(m.tokens)-1}); err != nil {
			return err
		}
		if i == 0 && m.failMid {
			return errors.New("mid-stream failure")
		}
		if i == 0 && m.hang {
			<-s.Context().Done()
			return s.Context().Err()
		}
	}
	return nil
}

func startPool(t *testing.T, cfg Config, workers map[string]*mockWorker) *Pool {
	t.Helper()
	lis := map[string]*bufconn.Listener{}
	names := make([]string, 0, len(workers))
	for name := range workers {
		names = append(names, name)
	}
	// Ties go to the first worker, so sorted names make selection deterministic.
	sort.Strings(names)
	for _, name := range names {
		w := workers[name]
		l := bufconn.Listen(1 << 20)
		srv := grpc.NewServer()
		pb.RegisterInferenceServiceServer(srv, w)
		go srv.Serve(l)
		t.Cleanup(srv.Stop)
		lis[name] = l
		cfg.Addrs = append(cfg.Addrs, "passthrough:///"+name)
	}
	cfg.DialOpts = []grpc.DialOption{
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithContextDialer(func(ctx context.Context, addr string) (net.Conn, error) {
			return lis[strings.TrimPrefix(addr, "passthrough:///")].DialContext(ctx)
		}),
	}
	p, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(p.Close)
	return p
}

func gen(p *Pool) (string, string, error) {
	return p.Text(context.Background(), Request{Prompt: "x", MaxTokens: 8}, 1<<14)
}

func TestNewRequiresWorkers(t *testing.T) {
	if _, err := New(Config{}); !errors.Is(err, ErrNoWorkers) {
		t.Fatalf("want ErrNoWorkers, got %v", err)
	}
}

func TestSpreadsAcrossWorkersAtConcurrencyOne(t *testing.T) {
	ws := map[string]*mockWorker{}
	for _, n := range []string{"a", "b"} {
		ws[n] = &mockWorker{delay: 150 * time.Millisecond, tokens: []string{"WORDS: ", "cat"}}
	}
	p := startPool(t, Config{MaxWait: 5 * time.Second}, ws)
	start := time.Now()
	var wg sync.WaitGroup
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if out, _, err := gen(p); err != nil || out != "WORDS: cat" {
				t.Errorf("gen = %q, %v", out, err)
			}
		}()
	}
	wg.Wait()
	el := time.Since(start)
	if el < 280*time.Millisecond || el > 1200*time.Millisecond {
		t.Fatalf("4 calls on 2 single-slot workers took %v, want ~300ms", el)
	}
	for n, w := range ws {
		if w.calls.Load() != 2 || w.maxSeen.Load() != 1 {
			t.Fatalf("worker %s calls=%d max_inflight=%d", n, w.calls.Load(), w.maxSeen.Load())
		}
	}
}

func TestAdmissionQueueFullAndTimeout(t *testing.T) {
	ws := map[string]*mockWorker{"a": {delay: 400 * time.Millisecond, tokens: []string{"x"}}}
	p := startPool(t, Config{MaxQueue: 1, MaxWait: 100 * time.Millisecond}, ws)
	errs := make(chan error, 3)
	for i := 0; i < 3; i++ {
		go func() { _, _, err := gen(p); errs <- err }()
		time.Sleep(20 * time.Millisecond)
	}
	var full, timeout, ok int
	for i := 0; i < 3; i++ {
		switch err := <-errs; {
		case err == nil:
			ok++
		case errors.Is(err, ErrQueueFull):
			full++
		case errors.Is(err, ErrQueueTimeout):
			timeout++
		default:
			t.Fatalf("unexpected err %v", err)
		}
	}
	if ok != 1 || full != 1 || timeout != 1 {
		t.Fatalf("ok=%d full=%d timeout=%d", ok, full, timeout)
	}
}

func TestRetryOnOtherWorkerBeforeFirstToken(t *testing.T) {
	bad := &mockWorker{failPre: true}
	good := &mockWorker{tokens: []string{"ok"}}
	p := startPool(t, Config{}, map[string]*mockWorker{"a-bad": bad, "b-good": good})
	for i := 0; i < 4; i++ {
		out, addr, err := gen(p)
		if err != nil || out != "ok" || !strings.HasSuffix(addr, "good") {
			t.Fatalf("gen = %q %q %v", out, addr, err)
		}
	}
	if bad.calls.Load() != 1 {
		t.Fatalf("failed worker should be tried once then cooled down, got %d calls", bad.calls.Load())
	}
	if st := p.Stats(); st.Healthy != 1 {
		t.Fatalf("healthy = %d, want 1", st.Healthy)
	}
}

func TestNoRetryMidStream(t *testing.T) {
	mid := &mockWorker{tokens: []string{"partial", "rest"}, failMid: true}
	other := &mockWorker{tokens: []string{"ok"}}
	p := startPool(t, Config{}, map[string]*mockWorker{"a-mid": mid, "b-other": other})
	out, _, err := gen(p)
	if err == nil || out != "partial" {
		t.Fatalf("want partial output + error, got %q %v", out, err)
	}
	if mid.calls.Load() != 1 || other.calls.Load() != 0 {
		t.Fatalf("mid-stream failure must not retry, calls=%d", mid.calls.Load())
	}
}

func TestIdleTimeout(t *testing.T) {
	ws := map[string]*mockWorker{"a": {tokens: []string{"first", "never"}, hang: true}}
	p := startPool(t, Config{IdleTimeout: 100 * time.Millisecond}, ws)
	start := time.Now()
	_, _, err := gen(p)
	if !errors.Is(err, ErrStreamIdle) {
		t.Fatalf("want ErrStreamIdle, got %v", err)
	}
	if time.Since(start) > time.Second {
		t.Fatalf("idle timeout took %v", time.Since(start))
	}
}

func TestOutputCap(t *testing.T) {
	ws := map[string]*mockWorker{"a": {tokens: []string{"aaaa", "bbbb", "cccc"}}}
	p := startPool(t, Config{}, ws)
	out, _, err := p.Text(context.Background(), Request{Prompt: "x"}, 8)
	if err != nil || out != "aaaabbbb" {
		t.Fatalf("got %q %v", out, err)
	}
}
