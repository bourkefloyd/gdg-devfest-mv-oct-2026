package players

import (
	"context"
	"encoding/json"
	"errors"

	"google.golang.org/genai"
)

// GeminiCommentator streams commentary generated exclusively from
// server-validated game data.
type GeminiCommentator struct {
	Client *genai.Client
	Model  string
}

func NewGeminiCommentator(client *genai.Client, model string) *GeminiCommentator {
	if model == "" {
		model = "gemini-3.1-flash-lite"
	}
	return &GeminiCommentator{Client: client, Model: model}
}

func (c *GeminiCommentator) Stream(ctx context.Context, summary GameSummary, out chan<- string) error {
	if c.Client == nil {
		return errors.New("nil Gemini client")
	}
	validated, err := json.Marshal(summary)
	if err != nil {
		return err
	}
	prompt := "Give concise, energetic play-by-play for this Word Hunt game. " +
		"Use only these server-validated facts; never invent words or scores:\n" + string(validated)
	for response, err := range c.Client.Models.GenerateContentStream(
		ctx,
		c.Model,
		genai.Text(prompt),
		&genai.GenerateContentConfig{
			Temperature:     ptr(float32(0.6)),
			MaxOutputTokens: 300,
			ThinkingConfig:  &genai.ThinkingConfig{ThinkingBudget: ptr(int32(64))},
		},
	) {
		if err != nil {
			return err
		}
		text := response.Text()
		if text == "" {
			continue
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case out <- text:
		}
	}
	return nil
}
