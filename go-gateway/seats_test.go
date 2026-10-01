package main

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"go-gateway/internal/players"
	"go-gateway/internal/pool"
	"go-gateway/internal/router"
	"go-gateway/internal/wordhunt"
	pb "go-gateway/proto"

	"google.golang.org/genai"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/test/bufconn"
)

const geminiOK = `{"candidates":[{"content":{"role":"model","parts":[{"text":"{\"words\":[{\"word\":\"cat\"},{\"word\":\"tone\"}]}"}]}}]}`

// fakeGemini fails the first `failures` calls with 503, then succeeds.
func fakeGemini(t *testing.T, failures int32) (*genai.Client, *atomic.Int32) {
	t.Helper()
	var calls atomic.Int32
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := calls.Add(1)
		w.Header().Set("Content-Type", "application/json")
		if failures < 0 || n <= failures {
			w.WriteHeader(http.StatusServiceUnavailable)
			w.Write([]byte(`{"error":{"code":503,"message":"overloaded","status":"UNAVAILABLE"}}`))
			return
		}
		w.Write([]byte(geminiOK))
	}))
	t.Cleanup(ts.Close)
	c, err := genai.NewClient(context.Background(), &genai.ClientConfig{
		Backend: genai.BackendGeminiAPI, APIKey: "test", HTTPOptions: genai.HTTPOptions{BaseURL: ts.URL},
	})
	if err != nil {
		t.Fatal(err)
	}
	return c, &calls
}

func seat(t *testing.T, seats func(string) ([]players.Player, error), name string) players.Player {
	t.Helper()
	ps, err := seats("race")
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range ps {
		if p.Name() == name {
			return p
		}
	}
	t.Fatalf("no seat %q", name)
	return nil
}

func play(p players.Player) (players.Result, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	return p.Play(ctx, wordhunt.NewBoard(5, 40), time.Now().Add(28*time.Second))
}

func TestGemini503TwiceThenSuccess(t *testing.T) {
	client, calls := fakeGemini(t, 2)
	p := seat(t, realSeats(client, nil, router.NewCircuitBreaker(), "m", "", "g"), "gemini")
	res, err := play(p)
	if err != nil || res.Fallback || res.Backend != "gemini-api" || len(res.Claims) != 2 {
		t.Fatalf("res=%+v err=%v", res, err)
	}
	if calls.Load() != 3 {
		t.Fatalf("calls = %d, want 3 (two retried 503s)", calls.Load())
	}
}

func TestGeminiDownFallsBackAndBreakerOpens(t *testing.T) {
	client, calls := fakeGemini(t, -1)
	breaker := router.NewCircuitBreaker()
	p := seat(t, realSeats(client, nil, breaker, "m", "", "g"), "gemini")
	for i := 0; i < 5; i++ {
		res, err := play(p)
		if err != nil || !res.Fallback || res.Backend != "solver" {
			t.Fatalf("play %d: res=%+v err=%v", i, res, err)
		}
	}
	before := calls.Load()
	if res, err := play(p); err != nil || !res.Fallback {
		t.Fatalf("open breaker play: %+v %v", res, err)
	}
	if calls.Load() != before {
		t.Fatalf("breaker open but Gemini still called (%d -> %d)", before, calls.Load())
	}
}

type dyingWorker struct {
	pb.UnimplementedInferenceServiceServer
	calls atomic.Int32
}

func (d *dyingWorker) StreamGenerate(_ *pb.GenerateRequest, s grpc.ServerStreamingServer[pb.GenerateResponse]) error {
	d.calls.Add(1)
	s.Send(&pb.GenerateResponse{Token: "WORDS: ca"})
	return errors.New("worker killed")
}

func TestGemmaKilledMidStreamFallsBackToGemini(t *testing.T) {
	lis := bufconn.Listen(1 << 20)
	srv := grpc.NewServer()
	w := &dyingWorker{}
	pb.RegisterInferenceServiceServer(srv, w)
	go srv.Serve(lis)
	t.Cleanup(srv.Stop)
	workers, err := pool.New(pool.Config{
		Addrs: []string{"passthrough:///gemma"},
		DialOpts: []grpc.DialOption{
			grpc.WithTransportCredentials(insecure.NewCredentials()),
			grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) { return lis.DialContext(ctx) }),
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(workers.Close)

	client, _ := fakeGemini(t, 0)
	p := seat(t, realSeats(client, workers, router.NewCircuitBreaker(), "m", "", "g"), "gemma")
	res, err := play(p)
	if err != nil || !res.Fallback || res.Backend != "gemini-api" {
		t.Fatalf("res=%+v err=%v", res, err)
	}
	if w.calls.Load() != 1 {
		t.Fatalf("mid-stream failure must not be retried on the worker, calls=%d", w.calls.Load())
	}
}
