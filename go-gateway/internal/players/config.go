package players

import (
	"context"
	"errors"
	"os"
	"strings"

	"google.golang.org/genai"
)

type GenAIBackends struct {
	Primary        *genai.Client
	API            *genai.Client
	PrimaryBackend string
}

// NewGenAIBackends creates the selected primary plus an API-key fallback when
// Vertex is selected and GEMINI_API_KEY is also configured.
func NewGenAIBackends(ctx context.Context) (GenAIBackends, error) {
	if vertexEnabled() {
		primary, err := newVertexClient(ctx)
		if err != nil {
			return GenAIBackends{}, err
		}
		out := GenAIBackends{Primary: primary, PrimaryBackend: "vertex"}
		if os.Getenv("GEMINI_API_KEY") != "" {
			out.API, err = newGeminiAPIClient(ctx)
			if err != nil {
				return GenAIBackends{}, err
			}
		}
		return out, nil
	}
	api, err := newGeminiAPIClient(ctx)
	if err != nil {
		return GenAIBackends{}, err
	}
	return GenAIBackends{Primary: api, API: api, PrimaryBackend: "gemini-api"}, nil
}

// NewGenAIClient builds the shared SDK client from the documented environment
// variables. Vertex is selected by GOOGLE_GENAI_USE_VERTEXAI=true; otherwise
// the Gemini API key backend is used.
func NewGenAIClient(ctx context.Context) (*genai.Client, error) {
	backends, err := NewGenAIBackends(ctx)
	if err != nil {
		return nil, err
	}
	return backends.Primary, nil
}

func vertexEnabled() bool {
	return strings.EqualFold(os.Getenv("GOOGLE_GENAI_USE_VERTEXAI"), "true") ||
		os.Getenv("GOOGLE_GENAI_USE_VERTEXAI") == "1"
}

func newVertexClient(ctx context.Context) (*genai.Client, error) {
	project := os.Getenv("GOOGLE_CLOUD_PROJECT")
	location := os.Getenv("GOOGLE_CLOUD_LOCATION")
	if project == "" || location == "" {
		return nil, errors.New("Vertex requires GOOGLE_CLOUD_PROJECT and GOOGLE_CLOUD_LOCATION")
	}
	return genai.NewClient(ctx, &genai.ClientConfig{
		Backend:  genai.BackendVertexAI,
		Project:  project,
		Location: location,
	})
}

func newGeminiAPIClient(ctx context.Context) (*genai.Client, error) {
	if os.Getenv("GEMINI_API_KEY") == "" {
		return nil, errors.New("Gemini API requires GEMINI_API_KEY")
	}
	return genai.NewClient(ctx, &genai.ClientConfig{
		Backend: genai.BackendGeminiAPI,
		APIKey:  os.Getenv("GEMINI_API_KEY"),
	})
}
