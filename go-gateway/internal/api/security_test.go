package api

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"

	"go-gateway/internal/pool"
)

func TestAuthRequired(t *testing.T) {
	e := newEnv(t, nil)
	for _, tc := range []struct{ method, path string }{
		{"POST", "/v1/games"}, {"GET", "/v1/games/abc"}, {"POST", "/v1/games/abc/words"},
		{"GET", "/v1/games/abc/events"}, {"POST", "/v1/chat/completions"},
	} {
		for _, key := range []string{"", "wrong-key"} {
			resp, out := e.do(tc.method, tc.path, key, nil)
			if resp.StatusCode != http.StatusUnauthorized || errCode(out) != "unauthorized" {
				t.Errorf("%s %s key=%q: %d %v", tc.method, tc.path, key, resp.StatusCode, out)
			}
		}
	}
	for _, p := range []string{"/healthz", "/readyz", "/metrics"} {
		if resp, _ := e.do("GET", p, "", nil); resp.StatusCode != 200 {
			t.Errorf("%s without key: %d", p, resp.StatusCode)
		}
	}
}

func TestCreateRateLimitPerKey(t *testing.T) {
	e := newEnv(t, func(c *Config) { c.CreateRate, c.CreateBurst = 2, 5 })
	var limited int
	for i := 0; i < 8; i++ {
		resp, out := e.do("POST", "/v1/games", keyA, map[string]any{"mode": "race", "duration_s": 1})
		if resp.StatusCode == http.StatusTooManyRequests {
			limited++
			if errCode(out) != "rate_limited" || resp.Header.Get("Retry-After") == "" {
				t.Fatalf("429 without code/Retry-After: %v", out)
			}
		}
	}
	if limited < 2 {
		t.Fatalf("expected 429s after burst of 5, got %d", limited)
	}
	if resp, _ := e.do("POST", "/v1/games", keyB, map[string]any{"duration_s": 1}); resp.StatusCode != http.StatusCreated {
		t.Fatalf("second key must be unaffected by first key's flood: %d", resp.StatusCode)
	}
}

func TestActiveGameCaps(t *testing.T) {
	e := newEnv(t, func(c *Config) { c.MaxActivePerKey, c.MaxActiveGames = 2, 3 })
	body := map[string]any{"mode": "human_vs_gemini", "duration_s": 30}
	e.create(keyA, body)
	e.create(keyA, body)
	resp, out := e.do("POST", "/v1/games", keyA, body)
	if resp.StatusCode != 429 || errCode(out) != "too_many_active_games_for_key" {
		t.Fatalf("per-key cap: %d %v", resp.StatusCode, out)
	}
	e.create(keyB, body)
	resp, out = e.do("POST", "/v1/games", keyB, body)
	if resp.StatusCode != 429 || errCode(out) != "too_many_active_games" {
		t.Fatalf("global cap: %d %v", resp.StatusCode, out)
	}
}

func TestBodyValidation(t *testing.T) {
	e := newEnv(t, nil)
	cases := []struct {
		name, body, code string
		status           int
	}{
		{"oversize", `{"mode":"race","pad":"` + strings.Repeat("a", 9000) + `"}`, "body_too_large", 413},
		{"unknown field", `{"mode":"race","score":9999}`, "invalid_json", 400},
		{"trailing data", `{"mode":"race"}{"mode":"race"}`, "invalid_json", 400},
		{"wrong type", `{"mode":7}`, "invalid_json", 400},
		{"empty", ``, "invalid_json", 400},
		{"bad mode", `{"mode":"chess"}`, "invalid_mode", 400},
		{"bad duration", `{"duration_s":100000}`, "invalid_duration", 400},
	}
	for _, tc := range cases {
		resp, out := e.do("POST", "/v1/games", keyA, tc.body)
		if resp.StatusCode != tc.status || errCode(out) != tc.code {
			t.Errorf("%s: got %d %v, want %d %s", tc.name, resp.StatusCode, out, tc.status, tc.code)
		}
	}
}

func TestCORSAllowlist(t *testing.T) {
	e := newEnv(t, nil)
	pre := func(origin string) *http.Response {
		req, _ := http.NewRequest("OPTIONS", e.ts.URL+"/v1/games", nil)
		req.Header.Set("Origin", origin)
		req.Header.Set("Access-Control-Request-Method", "POST")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		return resp
	}
	if r := pre("http://localhost:4317"); r.StatusCode != 204 || r.Header.Get("Access-Control-Allow-Origin") != "http://localhost:4317" {
		t.Fatalf("allowed origin preflight: %d %q", r.StatusCode, r.Header.Get("Access-Control-Allow-Origin"))
	}
	if r := pre("https://evil.example"); r.StatusCode != 403 || r.Header.Get("Access-Control-Allow-Origin") != "" {
		t.Fatalf("disallowed origin preflight: %d %q", r.StatusCode, r.Header.Get("Access-Control-Allow-Origin"))
	}
	req, _ := http.NewRequest("GET", e.ts.URL+"/healthz", nil)
	req.Header.Set("Origin", "https://evil.example")
	resp, _ := http.DefaultClient.Do(req)
	resp.Body.Close()
	if v := resp.Header.Get("Access-Control-Allow-Origin"); v != "" {
		t.Fatalf("ACAO leaked to disallowed origin: %q", v)
	}
}

func TestGamesAreScopedToKey(t *testing.T) {
	e := newEnv(t, nil)
	g := e.create(keyA, map[string]any{"mode": "human_vs_gemini", "duration_s": 10})
	id := g["game_id"].(string)
	for _, path := range []string{"/v1/games/" + id} {
		if resp, out := e.do("GET", path, keyB, nil); resp.StatusCode != 404 || errCode(out) != "game_not_found" {
			t.Fatalf("other key read game: %d %v", resp.StatusCode, out)
		}
	}
	if resp, _ := e.do("POST", "/v1/games/"+id+"/words", keyB, map[string]any{"word": "cat"}); resp.StatusCode != 404 {
		t.Fatalf("other key submitted word: %d", resp.StatusCode)
	}
}

func TestReadyz(t *testing.T) {
	ready := true
	e := newEnv(t, func(c *Config) {
		c.Ready = func(context.Context) (bool, map[string]any) { return ready, map[string]any{"gemma_pool": "x"} }
	})
	if resp, out := e.do("GET", "/readyz", "", nil); resp.StatusCode != 200 || out["ready"] != true {
		t.Fatalf("ready: %d %v", resp.StatusCode, out)
	}
	ready = false
	if resp, out := e.do("GET", "/readyz", "", nil); resp.StatusCode != 503 || out["ready"] != false {
		t.Fatalf("not ready: %d %v", resp.StatusCode, out)
	}
}

type fakeChat struct {
	tokens []string
	err    error
	got    pool.Request
}

func (f *fakeChat) Generate(ctx context.Context, req pool.Request, onToken func(string) error) (string, error) {
	f.got = req
	for _, t := range f.tokens {
		if err := onToken(t); err != nil {
			return "w", err
		}
	}
	return "w", f.err
}

func TestChatCompletions(t *testing.T) {
	fc := &fakeChat{tokens: []string{"hello", " world"}}
	e := newEnv(t, func(c *Config) { c.Chat = fc })
	resp, out := e.do("POST", "/v1/chat/completions", keyA, map[string]any{"prompt": "hi", "max_tokens": 1000})
	if resp.StatusCode != 400 || errCode(out) != "invalid_max_tokens" {
		t.Fatalf("max_tokens cap: %d %v", resp.StatusCode, out)
	}
	req, _ := http.NewRequest("POST", e.ts.URL+"/v1/chat/completions", strings.NewReader(`{"prompt":"hi","max_tokens":16}`))
	req.Header.Set("Authorization", "Bearer "+keyA)
	r, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	buf := new(strings.Builder)
	_, _ = io.Copy(buf, r.Body)
	r.Body.Close()
	if r.StatusCode != 200 || !strings.Contains(buf.String(), `"token":"hello"`) || !strings.Contains(buf.String(), "[DONE]") {
		t.Fatalf("stream: %d %s", r.StatusCode, buf)
	}
	if fc.got.MaxTokens != 16 {
		t.Fatalf("max_tokens not forwarded: %d", fc.got.MaxTokens)
	}

	fc.tokens, fc.err = nil, pool.ErrQueueTimeout
	if resp, out := e.do("POST", "/v1/chat/completions", keyA, map[string]any{"prompt": "hi"}); resp.StatusCode != 503 || errCode(out) != "overloaded" {
		t.Fatalf("overload: %d %v", resp.StatusCode, out)
	}
	fc.err = errors.New("worker exploded with secret detail")
	resp, out = e.do("POST", "/v1/chat/completions", keyA, map[string]any{"prompt": "hi"})
	if resp.StatusCode != 502 || strings.Contains(out["error"].(map[string]any)["message"].(string), "secret") {
		t.Fatalf("backend error leaked or wrong status: %d %v", resp.StatusCode, out)
	}
}
