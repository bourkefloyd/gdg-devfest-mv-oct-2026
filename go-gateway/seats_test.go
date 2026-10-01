package main

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"go-gateway/internal/players"
	"go-gateway/internal/pool"
	"go-gateway/internal/router"
	"go-gateway/internal/wordhunt"
	pb "go-gateway/proto"

	"cloud.google.com/go/auth"
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

func testBackends(primary, apiKey *genai.Client) genaiBackends {
	return genaiBackends{Primary: primary, API: apiKey,
		PrimaryBreaker: router.NewCircuitBreaker(), APIBreaker: router.NewCircuitBreaker()}
}

type staticToken struct{}

func (staticToken) Token(context.Context) (*auth.Token, error) {
	return &auth.Token{Value: "test-token", Type: "Bearer", Expiry: time.Now().Add(time.Hour)}, nil
}

// fakeVertex is a Vertex-backend client against a fake server; like
// fakeGemini it fails the first `failures` calls (all if negative).
func fakeVertex(t *testing.T, failures int32) (*genai.Client, *atomic.Int32) {
	t.Helper()
	apiStyle, calls := fakeGemini(t, failures)
	c, err := genai.NewClient(context.Background(), &genai.ClientConfig{
		Backend: genai.BackendVertexAI, Project: "gen-lang-client-test", Location: "us-central1",
		Credentials: auth.NewCredentials(&auth.CredentialsOptions{TokenProvider: staticToken{}}),
		HTTPOptions: genai.HTTPOptions{BaseURL: apiStyle.ClientConfig().HTTPOptions.BaseURL},
	})
	if err != nil {
		t.Fatal(err)
	}
	return c, calls
}

func TestVertexFirstThenAPIKey(t *testing.T) {
	vertex, vCalls := fakeVertex(t, 0)
	apiKey, aCalls := fakeGemini(t, 0)
	res, err := play(seat(t, realSeats(testBackends(vertex, apiKey), nil, seatOpts{Model: "m"}), "gemini"))
	if err != nil || res.Backend != "vertex" || res.Fallback || vCalls.Load() != 1 || aCalls.Load() != 0 {
		t.Fatalf("healthy vertex: res=%+v err=%v vertex=%d api=%d", res, err, vCalls.Load(), aCalls.Load())
	}

	vertexDown, vdCalls := fakeVertex(t, -1)
	res, err = play(seat(t, realSeats(testBackends(vertexDown, apiKey), nil, seatOpts{Model: "m"}), "gemini"))
	if err != nil || res.Backend != "gemini-api" || !res.Fallback || vdCalls.Load() == 0 {
		t.Fatalf("vertex down: res=%+v err=%v vertex_calls=%d", res, err, vdCalls.Load())
	}
}

func TestAPITierUsesItsOwnModelID(t *testing.T) {
	var path atomic.Value
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path.Store(r.URL.Path)
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(geminiOK))
	}))
	t.Cleanup(ts.Close)
	apiKey, err := genai.NewClient(context.Background(), &genai.ClientConfig{
		Backend: genai.BackendGeminiAPI, APIKey: "test", HTTPOptions: genai.HTTPOptions{BaseURL: ts.URL}})
	if err != nil {
		t.Fatal(err)
	}
	vertexDown, _ := fakeVertex(t, -1)
	o := seatOpts{Model: "vertex-only-model", APIModel: "api-only-model"}
	res, err := play(seat(t, realSeats(testBackends(vertexDown, apiKey), nil, o), "gemini"))
	if err != nil || res.Backend != "gemini-api" || res.Model != "api-only-model" {
		t.Fatalf("res=%+v err=%v", res, err)
	}
	if p, _ := path.Load().(string); !strings.Contains(p, "api-only-model") {
		t.Fatalf("API tier requested %q", p)
	}
}

func TestNewBackendsFallsBackToAPIKeyWhenVertexMisconfigured(t *testing.T) {
	t.Setenv("GOOGLE_GENAI_USE_VERTEXAI", "true")
	t.Setenv("GOOGLE_CLOUD_PROJECT", "")
	t.Setenv("GOOGLE_CLOUD_LOCATION", "")
	t.Setenv("GEMINI_API_KEY", "test-key")
	b, err := newBackends(context.Background(), slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil || b.Primary == nil || backendName(b.Primary) != "gemini-api" || b.API != nil {
		t.Fatalf("b=%+v err=%v", b, err)
	}

	t.Setenv("GOOGLE_GENAI_USE_VERTEXAI", "")
	b, err = newBackends(context.Background(), slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil || backendName(b.Primary) != "gemini-api" || b.API != nil {
		t.Fatalf("api-only: b=%+v err=%v", b, err)
	}
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
	p := seat(t, realSeats(testBackends(client, nil), nil, seatOpts{Model: "m"}), "gemini")
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
	p := seat(t, realSeats(genaiBackends{Primary: client, PrimaryBreaker: breaker}, nil, seatOpts{Model: "m"}), "gemini")
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

func TestGemmaSeatWithoutPoolIsLabeledFallback(t *testing.T) {
	client, _ := fakeGemini(t, 0)
	res, err := play(seat(t, realSeats(testBackends(client, nil), nil, seatOpts{Model: "m"}), "gemma"))
	if err != nil || !res.Fallback || res.Backend != "gemini-api" {
		t.Fatalf("res=%+v err=%v", res, err)
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
	p := seat(t, realSeats(testBackends(client, nil), workers, seatOpts{Model: "m"}), "gemma")
	res, err := play(p)
	if err != nil || !res.Fallback || res.Backend != "gemini-api" {
		t.Fatalf("res=%+v err=%v", res, err)
	}
	if w.calls.Load() != 1 {
		t.Fatalf("mid-stream failure must not be retried on the worker, calls=%d", w.calls.Load())
	}
}
