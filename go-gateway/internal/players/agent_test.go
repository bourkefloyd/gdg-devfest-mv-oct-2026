package players

import (
	"context"
	"os"
	"testing"
	"time"

	"go-gateway/internal/wordhunt"
)

func TestWordsArgument(t *testing.T) {
	words, err := wordsArgument(map[string]any{"words": []any{"cat", "tone"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(words) != 2 || words[1] != "tone" {
		t.Fatalf("unexpected words: %#v", words)
	}
	for _, args := range []map[string]any{
		{},
		{"words": "cat"},
		{"words": []any{"cat", 42}},
	} {
		if _, err := wordsArgument(args); err == nil {
			t.Fatalf("expected error for %#v", args)
		}
	}
}

func TestGeminiAgentLive(t *testing.T) {
	if os.Getenv("LIVE_GEMINI_AGENT") != "1" {
		t.Skip("set LIVE_GEMINI_AGENT=1 to call the real Gemini API")
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
	result, err := NewGeminiAgent(client, "", wordhunt.Default()).
		Play(context.Background(), board, time.Now().Add(35*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("claims=%d latency=%s", len(result.Claims), result.Latency)
}
