package players

import (
	"context"
	"os"
	"strings"
	"testing"
)

func TestGeminiCommentatorLive(t *testing.T) {
	if os.Getenv("LIVE_GEMINI_COMMENTATOR") != "1" {
		t.Skip("set LIVE_GEMINI_COMMENTATOR=1 to call the real Gemini API")
	}
	client, err := NewGenAIClient(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	out := make(chan string, 1024)
	err = NewGeminiCommentator(client, "").Stream(context.Background(), GameSummary{
		GameID: "smoke",
		Tiles:  "CATSRONELIPDMUGH",
		Final:  true,
		Players: []PlayerSummary{
			{Name: "Gemini", Score: 1200, Accepted: []string{"cat", "tone"}},
			{Name: "Gemma", Score: 400, Accepted: []string{"cat"}},
		},
	}, out)
	if err != nil {
		t.Fatal(err)
	}
	close(out)
	var text strings.Builder
	for chunk := range out {
		text.WriteString(chunk)
	}
	if text.Len() == 0 {
		t.Fatal("commentator returned no text")
	}
	t.Logf("commentary bytes=%d", text.Len())
}
