package players

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"regexp"
	"strings"
	"time"

	"go-gateway/internal/wordhunt"
	inference "go-gateway/proto"

	"google.golang.org/genai"
)

const maxRawOutput = 16 << 10

var (
	wordRE        = regexp.MustCompile(`^[a-z]{3,16}$`)
	wordsPrefixRE = regexp.MustCompile(`(?i)WORDS:`)
)

type geminiOutput struct {
	Words []struct {
		Word string `json:"word"`
		Path []int  `json:"path,omitempty"`
	} `json:"words"`
}

// ParseGeminiOutput strictly parses Gemini's structured response.
func ParseGeminiOutput(raw string) ([]Claim, error) {
	if len(raw) > maxRawOutput {
		return nil, errors.New("model output exceeds 16 KiB")
	}
	dec := json.NewDecoder(strings.NewReader(raw))
	dec.DisallowUnknownFields()
	var out geminiOutput
	if err := dec.Decode(&out); err != nil {
		return nil, fmt.Errorf("parse Gemini output: %w", err)
	}
	if err := ensureEOF(dec); err != nil {
		return nil, err
	}
	if len(out.Words) > 150 {
		return nil, errors.New("model returned more than 150 words")
	}
	claims := make([]Claim, 0, len(out.Words))
	seen := make(map[string]struct{}, len(out.Words))
	for _, item := range out.Words {
		word := strings.ToLower(strings.TrimSpace(item.Word))
		if !wordRE.MatchString(word) {
			continue
		}
		if _, ok := seen[word]; ok {
			continue
		}
		seen[word] = struct{}{}
		claims = append(claims, Claim{Word: word, Path: item.Path})
	}
	return claims, nil
}

func ensureEOF(dec *json.Decoder) error {
	var extra any
	if err := dec.Decode(&extra); err != io.EOF {
		if err == nil {
			return errors.New("unexpected trailing JSON")
		}
		return err
	}
	return nil
}

// ParseGemmaOutput parses the deliberately simple "WORDS: ..." contract.
func ParseGemmaOutput(raw string) ([]Claim, error) {
	if len(raw) > maxRawOutput {
		return nil, errors.New("model output exceeds 16 KiB")
	}
	loc := wordsPrefixRE.FindStringIndex(raw)
	if loc == nil {
		return nil, errors.New("missing WORDS: prefix")
	}
	parts := regexp.MustCompile(`[^A-Za-z]+`).Split(raw[loc[1]:], -1)
	claims := make([]Claim, 0, min(len(parts), 150))
	seen := map[string]struct{}{}
	for _, part := range parts {
		word := strings.ToLower(part)
		if !wordRE.MatchString(word) {
			continue
		}
		if _, ok := seen[word]; ok {
			continue
		}
		seen[word] = struct{}{}
		claims = append(claims, Claim{Word: word})
		if len(claims) == 150 {
			break
		}
	}
	return claims, nil
}

type GeminiPlayer struct {
	Client *genai.Client
	Model  string
}

func NewGeminiPlayer(client *genai.Client, model string) *GeminiPlayer {
	if model == "" {
		model = "gemini-3.8-flash"
	}
	return &GeminiPlayer{Client: client, Model: model}
}

func (p *GeminiPlayer) Name() string { return "Gemini" }

func (p *GeminiPlayer) Play(ctx context.Context, b wordhunt.Board, deadline time.Time) (Result, error) {
	start := time.Now()
	if p.Client == nil {
		return Result{}, errors.New("nil Gemini client")
	}
	ctx, cancel := moveContext(ctx, deadline)
	defer cancel()
	schema := &genai.Schema{
		Type: genai.TypeObject,
		Properties: map[string]*genai.Schema{
			"words": {
				Type: genai.TypeArray,
				Items: &genai.Schema{
					Type: genai.TypeObject,
					Properties: map[string]*genai.Schema{
						"word": {Type: genai.TypeString},
						"path": {Type: genai.TypeArray, Items: &genai.Schema{Type: genai.TypeInteger}},
					},
					Required: []string{"word"},
				},
			},
		},
		Required: []string{"words"},
	}
	prompt := boardPrompt(b) + "\nReturn up to 25 of the best Word Hunt words you can find. Each tile may be used once per word and consecutive tiles must touch in any of 8 directions."
	resp, err := p.Client.Models.GenerateContent(ctx, p.Model, genai.Text(prompt), &genai.GenerateContentConfig{
		ResponseMIMEType: "application/json",
		ResponseSchema:   schema,
		Temperature:      ptr(float32(0.2)),
		MaxOutputTokens:  4096,
		ThinkingConfig:   &genai.ThinkingConfig{ThinkingBudget: ptr(int32(128))},
	})
	if err != nil {
		return Result{}, err
	}
	raw := resp.Text()
	claims, err := ParseGeminiOutput(raw)
	return Result{Claims: claims, Backend: "gemini-api", Model: p.Model, Latency: time.Since(start), Raw: raw}, err
}

type GemmaPlayer struct {
	Client inference.InferenceServiceClient
	Model  string
}

// HostedGemmaPlayer uses the Gemini API as a spillover tier for Gemma's seat.
type HostedGemmaPlayer struct {
	Client *genai.Client
	Model  string
}

func NewHostedGemmaPlayer(client *genai.Client, model string) *HostedGemmaPlayer {
	if model == "" {
		model = "gemma-4-26b-a4b-it"
	}
	return &HostedGemmaPlayer{Client: client, Model: model}
}

func (p *HostedGemmaPlayer) Name() string { return "Gemma (hosted)" }

func (p *HostedGemmaPlayer) Play(ctx context.Context, b wordhunt.Board, deadline time.Time) (Result, error) {
	start := time.Now()
	if p.Client == nil {
		return Result{}, errors.New("nil hosted Gemma client")
	}
	ctx, cancel := moveContext(ctx, deadline)
	defer cancel()
	resp, err := p.Client.Models.GenerateContent(
		ctx,
		p.Model,
		genai.Text(boardPrompt(b)+"\nImmediately return up to 10 likely words. No explanation. Exactly one line: WORDS: word, word, word"),
		&genai.GenerateContentConfig{
			Temperature:     ptr(float32(0.2)),
			MaxOutputTokens: 1024,
		},
	)
	if err != nil {
		return Result{}, err
	}
	raw := resp.Text()
	claims, err := ParseGemmaOutput(raw)
	return Result{
		Claims:  claims,
		Backend: "gemini-api-hosted-gemma",
		Model:   p.Model,
		Latency: time.Since(start),
		Raw:     raw,
	}, err
}

func NewGemmaPlayer(client inference.InferenceServiceClient, model string) *GemmaPlayer {
	if model == "" {
		model = "gemma-4-e2b"
	}
	return &GemmaPlayer{Client: client, Model: model}
}

func (p *GemmaPlayer) Name() string { return "Gemma" }

func (p *GemmaPlayer) Play(ctx context.Context, b wordhunt.Board, deadline time.Time) (Result, error) {
	start := time.Now()
	if p.Client == nil {
		return Result{}, errors.New("nil Gemma client")
	}
	ctx, cancel := moveContext(ctx, deadline)
	defer cancel()
	stream, err := p.Client.StreamGenerate(ctx, &inference.GenerateRequest{
		Prompt:      boardPrompt(b) + "\nList likely words. Reply with exactly one line: WORDS: word, word, word",
		MaxTokens:   120,
		Temperature: 0.2,
	})
	if err != nil {
		return Result{}, err
	}
	var raw strings.Builder
	for {
		token, err := stream.Recv()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return Result{}, err
		}
		if raw.Len()+len(token.Token) > maxRawOutput {
			return Result{}, errors.New("model output exceeds 16 KiB")
		}
		raw.WriteString(token.Token)
		if token.IsFinal {
			break
		}
	}
	claims, err := ParseGemmaOutput(raw.String())
	return Result{Claims: claims, Backend: "local-grpc", Model: p.Model, Latency: time.Since(start), Raw: raw.String()}, err
}

func boardPrompt(b wordhunt.Board) string {
	var rows []string
	for row := 0; row < 4; row++ {
		var cells []string
		for col := 0; col < 4; col++ {
			i := row*4 + col
			cells = append(cells, fmt.Sprintf("%d:%c", i, b.Tiles[i]))
		}
		rows = append(rows, strings.Join(cells, " "))
	}
	return "Word Hunt board (index:letter):\n" + strings.Join(rows, "\n")
}

func moveContext(parent context.Context, deadline time.Time) (context.Context, context.CancelFunc) {
	limit := time.Now().Add(25 * time.Second)
	if d := deadline.Add(-3 * time.Second); d.Before(limit) {
		limit = d
	}
	return context.WithDeadline(parent, limit)
}

func ptr[T any](v T) *T { return &v }
