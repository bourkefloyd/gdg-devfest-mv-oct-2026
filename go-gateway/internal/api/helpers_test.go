package api

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"go-gateway/internal/players"
	"go-gateway/internal/wordhunt"
)

const (
	keyA = "test-key-alpha"
	keyB = "test-key-bravo"
)

type testEnv struct {
	t   *testing.T
	srv *Server
	ts  *httptest.Server
}

func newEnv(t *testing.T, mod func(*Config)) *testEnv {
	t.Helper()
	cfg := Config{
		APIKeys:         []string{keyA, keyB},
		AllowedOrigins:  []string{"http://localhost:4317"},
		MinDuration:     time.Second,
		CreateRate:      1000,
		CreateBurst:     1000,
		WordRate:        1000,
		WordBurst:       1000,
		MaxActivePerKey: 1000,
		HeartbeatEvery:  200 * time.Millisecond,
		Seats:           MockSeats(40 * time.Millisecond),
		Logger:          slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
	if mod != nil {
		mod(&cfg)
	}
	s := New(cfg)
	ts := httptest.NewServer(s.Handler())
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		s.Shutdown(ctx)
		ts.Close()
	})
	return &testEnv{t: t, srv: s, ts: ts}
}

func (e *testEnv) do(method, path, key string, body any) (*http.Response, map[string]any) {
	e.t.Helper()
	var rdr io.Reader
	switch b := body.(type) {
	case nil:
	case string:
		rdr = strings.NewReader(b)
	default:
		buf, _ := json.Marshal(b)
		rdr = bytes.NewReader(buf)
	}
	req, _ := http.NewRequest(method, e.ts.URL+path, rdr)
	if key != "" {
		req.Header.Set("Authorization", "Bearer "+key)
	}
	if rdr != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		e.t.Fatal(err)
	}
	defer resp.Body.Close()
	var out map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&out)
	return resp, out
}

func (e *testEnv) create(key string, body any) map[string]any {
	e.t.Helper()
	resp, out := e.do("POST", "/v1/games", key, body)
	if resp.StatusCode != http.StatusCreated {
		e.t.Fatalf("create: %d %v", resp.StatusCode, out)
	}
	return out
}

func errCode(out map[string]any) string {
	m, _ := out["error"].(map[string]any)
	c, _ := m["code"].(string)
	return c
}

type sseEvent struct {
	Type string
	Data map[string]any
}

// readEvents reads SSE until game_over (plus any trailing events until the
// server closes the stream) or the timeout.
func (e *testEnv) readEvents(url string, header http.Header, timeout time.Duration) []sseEvent {
	e.t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, "GET", url, nil)
	for k, v := range header {
		req.Header[k] = v
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		e.t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		e.t.Fatalf("events: status %d", resp.StatusCode)
	}
	var evs []sseEvent
	var cur sseEvent
	sc := bufio.NewScanner(resp.Body)
	for sc.Scan() {
		line := sc.Text()
		switch {
		case strings.HasPrefix(line, "event: "):
			cur.Type = strings.TrimPrefix(line, "event: ")
		case strings.HasPrefix(line, "data: "):
			_ = json.Unmarshal([]byte(strings.TrimPrefix(line, "data: ")), &cur.Data)
		case line == "" && cur.Type != "":
			evs = append(evs, cur)
			cur = sseEvent{}
		}
	}
	return evs
}

func bearerHeader(key string) http.Header {
	return http.Header{"Authorization": {"Bearer " + key}}
}

// coreReady reports whether Track A's wordhunt implementation is merged
// (the interface stub solves nothing).
func coreReady() bool {
	return len(wordhunt.NewBoard(1, 40).Solve(wordhunt.Default())) > 0
}

func boardFrom(tiles string, seed int64) wordhunt.Board {
	b := wordhunt.Board{Seed: seed}
	for i := 0; i < len(b.Tiles) && i < len(tiles); i++ {
		b.Tiles[i] = tiles[i] - 'A' + 'a'
	}
	return b
}

type fakeCommentator struct{}

func (fakeCommentator) Stream(ctx context.Context, ev players.GameSummary, out chan<- string) error {
	out <- "What a round."
	out <- "Final scores are in."
	return nil
}

// fakePlayer returns a fixed result after a delay.
type fakePlayer struct {
	name  string
	delay time.Duration
	res   players.Result
	err   error
}

func (f *fakePlayer) Name() string { return f.name }

func (f *fakePlayer) Play(ctx context.Context, b wordhunt.Board, deadline time.Time) (players.Result, error) {
	select {
	case <-time.After(f.delay):
		return f.res, f.err
	case <-ctx.Done():
		return players.Result{Backend: f.res.Backend}, ctx.Err()
	}
}
