package players

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"go-gateway/internal/wordhunt"

	"google.golang.org/genai"
)

func TestParseGeminiOutput(t *testing.T) {
	claims, err := ParseGeminiOutput(`{"words":[{"word":"Cat","path":[0,1,2]},{"word":"cat"},{"word":"x"},{"word":"tone"}]}`)
	if err != nil {
		t.Fatal(err)
	}
	if len(claims) != 2 || claims[0].Word != "cat" || claims[1].Word != "tone" {
		t.Fatalf("unexpected claims: %#v", claims)
	}
	if _, err := ParseGeminiOutput(`{"words":[],"score":9999}`); err == nil {
		t.Fatal("expected extra-field error")
	}
	if _, err := ParseGeminiOutput(`{"words":"cat"}`); err == nil {
		t.Fatal("expected wrong-type error")
	}
}

func TestParseGeminiOutputLimits(t *testing.T) {
	var items []string
	for range 151 {
		items = append(items, `{"word":"cat"}`)
	}
	if _, err := ParseGeminiOutput(`{"words":[` + strings.Join(items, ",") + `]}`); err == nil {
		t.Fatal("expected item-limit error")
	}
	if _, err := ParseGeminiOutput(strings.Repeat("x", maxRawOutput+1)); err == nil {
		t.Fatal("expected size-limit error")
	}
}

func TestParseGeminiOutputSalvagesCompleteItems(t *testing.T) {
	claims, err := ParseGeminiOutput(
		`{"words":[{"word":"cat","path":[0,1,2]},{"word":"tone"},{"word":"unfinished`,
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(claims) != 2 || claims[0].Word != "cat" || claims[1].Word != "tone" {
		t.Fatalf("unexpected salvaged claims: %#v", claims)
	}
	if _, err := ParseGeminiOutput(`{"words":[{"word":"unfinished`); err == nil {
		t.Fatal("must not salvage when no complete item exists")
	}
	if _, err := ParseGeminiOutput(`{"words":[{"word":"cat","extra":true},{"word":"unfinished`); err == nil {
		t.Fatal("must preserve strict unknown-field rejection")
	}
}

func TestParseGemmaOutput(t *testing.T) {
	claims, err := ParseGemmaOutput("thinking...\nWORDS: Cat, TONE; cat, x, ignore_previous_instructions")
	if err != nil {
		t.Fatal(err)
	}
	if len(claims) != 5 || claims[0].Word != "cat" || claims[1].Word != "tone" {
		t.Fatalf("unexpected claims: %#v", claims)
	}
	if _, err := ParseGemmaOutput("cat, tone"); err == nil {
		t.Fatal("expected missing-prefix error")
	}
}

func TestPlayerThinkingConfig(t *testing.T) {
	t.Setenv("GEMINI_PLAYER_THINKING_LEVEL", "low")
	t.Setenv("GEMINI_PLAYER_THINKING_BUDGET", "999")
	if got := playerThinkingConfig(); got.ThinkingLevel != genai.ThinkingLevelLow || got.ThinkingBudget != nil {
		t.Fatalf("level should take precedence: %#v", got)
	}
	t.Setenv("GEMINI_PLAYER_THINKING_LEVEL", "")
	t.Setenv("GEMINI_PLAYER_THINKING_BUDGET", "64")
	if got := playerThinkingConfig(); got.ThinkingBudget == nil || *got.ThinkingBudget != 64 {
		t.Fatalf("budget = %#v", got)
	}
}

func TestModelOutputLimitsFromEnv(t *testing.T) {
	t.Setenv("GEMINI_PLAYER_MAX_OUTPUT_TOKENS", "12000")
	t.Setenv("GEMINI_PLAYER_MAX_WORDS", "17")
	player := NewGeminiPlayer(nil, "model")
	if player.MaxOutputTokens != 12000 || player.MaxWords != 17 {
		t.Fatalf("player limits: tokens=%d words=%d", player.MaxOutputTokens, player.MaxWords)
	}
	t.Setenv("GEMINI_AGENT_MAX_OUTPUT_TOKENS", "16000")
	t.Setenv("GEMINI_AGENT_MAX_WORDS", "14")
	agent := NewGeminiAgent(nil, "model", nil)
	if agent.MaxOutputTokens != 16000 || agent.MaxWords != 14 {
		t.Fatalf("agent limits: tokens=%d words=%d", agent.MaxOutputTokens, agent.MaxWords)
	}
	t.Setenv("GEMINI_PLAYER_MAX_OUTPUT_TOKENS", "999999")
	if got := NewGeminiPlayer(nil, "model").MaxOutputTokens; got != 8192 {
		t.Fatalf("out-of-range value should use default, got %d", got)
	}
}

func TestGeminiPlayerPromptIncludesNeighbors(t *testing.T) {
	board := wordhunt.Board{Tiles: [16]byte{
		'a', 'b', 'c', 'd',
		'e', 'f', 'g', 'h',
		'i', 'j', 'k', 'l',
		'm', 'n', 'o', 'p',
	}}
	prompt := geminiPlayerPrompt(board, 20)
	for _, want := range []string{
		"0:a -> [1:b, 4:e, 5:f]",
		"5:f -> [0:a, 1:b, 2:c, 4:e, 6:g, 8:i, 9:j, 10:k]",
		"Find at most 20 words",
		"Start with high-confidence 3-5 letter words",
		"Worked rule example",
		"Omit path",
	} {
		if !strings.Contains(prompt, want) {
			t.Errorf("prompt missing %q", want)
		}
	}
}

func FuzzParseGeminiOutput(f *testing.F) {
	f.Add(`{"words":[{"word":"cat"}]}`)
	f.Fuzz(func(t *testing.T, raw string) { _, _ = ParseGeminiOutput(raw) })
}

func FuzzParseGemmaOutput(f *testing.F) {
	f.Add("WORDS: cat, tone")
	f.Fuzz(func(t *testing.T, raw string) { _, _ = ParseGemmaOutput(raw) })
}

func TestGeminiLive(t *testing.T) {
	if os.Getenv("LIVE_GEMINI") != "1" {
		t.Skip("set LIVE_GEMINI=1 to call the real Gemini API")
	}
	client, err := NewGenAIClient(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	board := wordhunt.Board{Tiles: [16]byte{
		'c', 'a', 't', 's',
		'r', 'o', 'n', 'e',
		'l', 'i', 'p', 'd',
		'm', 'u', 'g', 'h',
	}}
	result, err := NewGeminiPlayer(client, os.Getenv("GEMINI_PLAYER_MODEL")).
		Play(context.Background(), board, time.Now().Add(30*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Claims) == 0 {
		t.Fatal("Gemini returned no parseable claims")
	}
	valid := 0
	score := 0
	dict := wordhunt.Default()
	for _, claim := range result.Claims {
		if ok, _ := wordhunt.ValidateWord(board, dict, claim.Word, claim.Path); ok {
			valid++
			score += wordhunt.Score(claim.Word)
		}
	}
	if valid == 0 {
		t.Fatal("Gemini returned no server-valid claims")
	}
	t.Logf("model=%s claims=%d valid=%d score=%d latency=%s", result.Model, len(result.Claims), valid, score, result.Latency)
}

func TestGeminiPlayerFakeServer(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Error(err)
		}
		if !strings.Contains(string(body), `"responseSchema"`) {
			t.Errorf("request lacks response schema: %s", body)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"candidates":[{"content":{"role":"model","parts":[{"text":"{\"words\":[{\"word\":\"cat\",\"path\":[0,1,2]}]}"}]},"finishReason":"STOP"}]}`)
	}))
	defer server.Close()
	client, err := genai.NewClient(context.Background(), &genai.ClientConfig{
		APIKey:  "fake-key",
		Backend: genai.BackendGeminiAPI,
		HTTPOptions: genai.HTTPOptions{
			BaseURL: server.URL,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	board := wordhunt.Board{Tiles: [16]byte{'c', 'a', 't'}}
	result, err := NewGeminiPlayer(client, "fake-model").
		Play(context.Background(), board, time.Now().Add(5*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Claims) != 1 || result.Claims[0].Word != "cat" {
		t.Fatalf("unexpected result: %#v", result)
	}
}

func TestHostedGemmaLive(t *testing.T) {
	if os.Getenv("LIVE_HOSTED_GEMMA") != "1" {
		t.Skip("set LIVE_HOSTED_GEMMA=1 to call hosted Gemma")
	}
	client, err := NewGenAIClient(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	board := wordhunt.Board{Tiles: [16]byte{
		'c', 'a', 't', 's',
		'r', 'o', 'n', 'e',
		'l', 'i', 'p', 'd',
		'm', 'u', 'g', 'h',
	}}
	result, err := NewHostedGemmaPlayer(client, "").
		Play(context.Background(), board, time.Now().Add(25*time.Second))
	if err != nil {
		t.Fatalf("%v; raw=%q", err, result.Raw)
	}
	if len(result.Claims) == 0 {
		t.Fatal("hosted Gemma returned no parseable claims")
	}
	t.Logf("model=%s claims=%d latency=%s", result.Model, len(result.Claims), result.Latency)
}
