package router

import (
	"context"
	"os"
	"testing"
	"time"

	"go-gateway/internal/players"
	"go-gateway/internal/wordhunt"
)

func TestVertexToGeminiAPIFailoverLive(t *testing.T) {
	if os.Getenv("LIVE_GENAI_FAILOVER") != "1" {
		t.Skip("set LIVE_GENAI_FAILOVER=1 to call Vertex and Gemini API")
	}
	backends, err := players.NewGenAIBackends(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if backends.PrimaryBackend != "vertex" || backends.API == nil {
		t.Fatal("test requires Vertex primary and Gemini API fallback")
	}
	board := wordhunt.Board{Tiles: [16]byte{
		'c', 'a', 't', 's',
		'r', 'o', 'n', 'e',
		'l', 'i', 'p', 'd',
		'm', 'u', 'g', 'h',
	}}
	vertexModel := envOr("VERTEX_PLAYER_MODEL", "gemini-2.5-flash")
	apiModel := envOr("GEMINI_PLAYER_MODEL", "gemini-3.8-flash")

	vertex := players.NewGeminiPlayer(backends.Primary, vertexModel)
	vertexResult, err := vertex.Play(context.Background(), board, time.Now().Add(20*time.Second))
	if err != nil {
		t.Fatalf("Vertex smoke: %v", err)
	}
	vertexValid, vertexScore := validated(vertexResult.Claims, board)
	if vertexValid == 0 || vertexResult.Backend != "vertex" {
		t.Fatalf("Vertex result: %+v", vertexResult)
	}

	chain := &FallbackPlayer{Chain: []players.Player{
		players.NewGeminiPlayer(backends.Primary, "deliberately-invalid-model"),
		players.NewGeminiPlayer(backends.API, apiModel),
	}}
	start := time.Now()
	fallbackResult, err := chain.Play(context.Background(), board, time.Now().Add(30*time.Second))
	total := time.Since(start)
	if err != nil {
		t.Fatalf("Vertex to API failover: %v", err)
	}
	apiValid, apiScore := validated(fallbackResult.Claims, board)
	if !fallbackResult.Fallback || fallbackResult.Backend != "gemini-api" || apiValid == 0 {
		t.Fatalf("fallback result: %+v", fallbackResult)
	}
	t.Logf(
		"vertex model=%s latency=%s valid=%d score=%d; api fallback model=%s api_latency=%s total_latency=%s valid=%d score=%d",
		vertexResult.Model, vertexResult.Latency, vertexValid, vertexScore,
		fallbackResult.Model, fallbackResult.Latency, total, apiValid, apiScore,
	)
}

func validated(claims []players.Claim, board wordhunt.Board) (count, score int) {
	dict := wordhunt.Default()
	for _, claim := range claims {
		if ok, _ := wordhunt.ValidateWord(board, dict, claim.Word, nil); ok {
			count++
			score += wordhunt.Score(claim.Word)
		}
	}
	return count, score
}

func envOr(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}
