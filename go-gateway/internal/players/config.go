package players

import (
	"context"
	"os"
	"strings"

	"google.golang.org/genai"
)

// NewGenAIClient builds the shared SDK client from the documented environment
// variables. Vertex is selected by GOOGLE_GENAI_USE_VERTEXAI=true; otherwise
// the Gemini API key backend is used.
func NewGenAIClient(ctx context.Context) (*genai.Client, error) {
	cfg := &genai.ClientConfig{}
	if strings.EqualFold(os.Getenv("GOOGLE_GENAI_USE_VERTEXAI"), "true") ||
		os.Getenv("GOOGLE_GENAI_USE_VERTEXAI") == "1" {
		cfg.Backend = genai.BackendVertexAI
		cfg.Project = os.Getenv("GOOGLE_CLOUD_PROJECT")
		cfg.Location = os.Getenv("GOOGLE_CLOUD_LOCATION")
	} else {
		cfg.Backend = genai.BackendGeminiAPI
		cfg.APIKey = os.Getenv("GEMINI_API_KEY")
	}
	return genai.NewClient(ctx, cfg)
}
