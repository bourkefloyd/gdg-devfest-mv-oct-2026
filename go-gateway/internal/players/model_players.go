package players

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"regexp"
	"strconv"
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

type geminiWord struct {
	Word string `json:"word"`
	Path []int  `json:"path,omitempty"`
}

type geminiOutput struct {
	Words []geminiWord `json:"words"`
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
		if isUnexpectedEOF(err) {
			if words, salvageErr := salvageGeminiWords(raw); salvageErr == nil && len(words) > 0 {
				return claimsFromGeminiWords(words)
			}
		}
		return nil, fmt.Errorf("parse Gemini output: %w", err)
	}
	if err := ensureEOF(dec); err != nil {
		return nil, err
	}
	return claimsFromGeminiWords(out.Words)
}

func claimsFromGeminiWords(words []geminiWord) ([]Claim, error) {
	if len(words) > 150 {
		return nil, errors.New("model returned more than 150 words")
	}
	claims := make([]Claim, 0, len(words))
	seen := make(map[string]struct{}, len(words))
	for _, item := range words {
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

func salvageGeminiWords(raw string) ([]geminiWord, error) {
	dec := json.NewDecoder(strings.NewReader(raw))
	dec.DisallowUnknownFields()
	token, err := dec.Token()
	if err != nil || token != json.Delim('{') {
		return nil, errors.New("truncated output is not an object")
	}
	for dec.More() {
		key, err := dec.Token()
		if err != nil {
			return nil, err
		}
		if key != "words" {
			return nil, fmt.Errorf("unexpected field %q", key)
		}
		token, err = dec.Token()
		if err != nil || token != json.Delim('[') {
			return nil, errors.New("words is not an array")
		}
		var words []geminiWord
		for dec.More() {
			var item geminiWord
			if err := dec.Decode(&item); err != nil {
				if isUnexpectedEOF(err) && len(words) > 0 {
					return words, nil
				}
				return nil, err
			}
			words = append(words, item)
			if len(words) > 150 {
				return nil, errors.New("model returned more than 150 words")
			}
		}
		if _, err := dec.Token(); isUnexpectedEOF(err) && len(words) > 0 {
			return words, nil
		} else if err != nil {
			return nil, err
		}
		return words, nil
	}
	return nil, errors.New("truncated output has no words")
}

func isUnexpectedEOF(err error) bool {
	return errors.Is(err, io.ErrUnexpectedEOF) ||
		(err != nil && strings.Contains(err.Error(), "unexpected EOF"))
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
	Client          *genai.Client
	Model           string
	Backend         string
	Thinking        *genai.ThinkingConfig
	MaxOutputTokens int32
	MaxWords        int
}

func NewGeminiPlayer(client *genai.Client, model string) *GeminiPlayer {
	if model == "" {
		model = os.Getenv("GEMINI_PLAYER_MODEL")
	}
	if model == "" {
		model = "gemini-3.8-flash"
	}
	return &GeminiPlayer{
		Client: client, Model: model, Backend: genAIBackendName(client),
		Thinking:        playerThinkingConfig(),
		MaxOutputTokens: envInt32("GEMINI_PLAYER_MAX_OUTPUT_TOKENS", 8192, 512, 32768),
		MaxWords:        int(envInt32("GEMINI_PLAYER_MAX_WORDS", 20, 1, 150)),
	}
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
	prompt := geminiPlayerPrompt(b, p.MaxWords)
	resp, err := p.Client.Models.GenerateContent(ctx, p.Model, genai.Text(prompt), &genai.GenerateContentConfig{
		ResponseMIMEType: "application/json",
		ResponseSchema:   schema,
		Temperature:      ptr(float32(0.2)),
		MaxOutputTokens:  p.MaxOutputTokens,
		ThinkingConfig:   p.Thinking,
	})
	if err != nil {
		return Result{}, err
	}
	raw := resp.Text()
	claims, err := ParseGeminiOutput(raw)
	return Result{Claims: claims, Backend: p.Backend, Model: p.Model, Latency: time.Since(start), Raw: raw}, err
}

type GemmaPlayer struct {
	Client inference.InferenceServiceClient
	Model  string
}

// HostedGemmaPlayer uses the Gemini API as a spillover tier for Gemma's seat.
type HostedGemmaPlayer struct {
	Client   *genai.Client
	Model    string
	Profile  string
	Strategy string
}

func NewHostedGemmaPlayer(client *genai.Client, model string) *HostedGemmaPlayer {
	if model == "" {
		model = "gemma-4-26b-a4b-it"
	}
	return &HostedGemmaPlayer{Client: client, Model: model}
}

func NewProfiledHostedGemmaPlayer(client *genai.Client, model, profile, strategy string) *HostedGemmaPlayer {
	player := NewHostedGemmaPlayer(client, model)
	player.Profile, player.Strategy = profile, strategy
	return player
}

func (p *HostedGemmaPlayer) Name() string { return "Gemma (hosted)" }

func (p *HostedGemmaPlayer) Play(ctx context.Context, b wordhunt.Board, deadline time.Time) (Result, error) {
	start := time.Now()
	if p.Client == nil {
		return Result{}, errors.New("nil hosted Gemma client")
	}
	ctx, cancel := moveContext(ctx, deadline)
	defer cancel()
	instruction := "Immediately return up to 25 likely words. No explanation. Exactly one line: WORDS: word, word, word"
	switch p.Strategy {
	case "diffusion":
		instruction = "Explore diverse word candidates in parallel by length, then return the best verified-looking set. " + instruction
	case "diffusion-jev":
		instruction = "Use a generate-expand-verify strategy: propose diverse candidates, internally reject uncertain paths, then return only the survivors. " + instruction
	}
	resp, err := p.Client.Models.GenerateContent(
		ctx,
		p.Model,
		genai.Text(boardPrompt(b)+"\n"+instruction),
		&genai.GenerateContentConfig{
			Temperature:     ptr(float32(0.2)),
			MaxOutputTokens: 1024,
			// Gemma 4 thinks by default and spends the output budget on thought
			// tokens, so the answer never arrives before the move deadline.
			// thinkingBudget is rejected; MINIMAL is the supported switch.
			ThinkingConfig: &genai.ThinkingConfig{ThinkingLevel: genai.ThinkingLevelMinimal},
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
		Profile: p.Profile,
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

func geminiPlayerPrompt(b wordhunt.Board, maxWords int) string {
	return geminiSearchPrompt(b, maxWords) +
		"\nOmit path from the JSON; the server will derive it. Return only words formed from this board."
}

func geminiSearchPrompt(b wordhunt.Board, maxWords int) string {
	var neighbors []string
	for i := range b.Tiles {
		row, col := i/4, i%4
		var adjacent []string
		for dr := -1; dr <= 1; dr++ {
			for dc := -1; dc <= 1; dc++ {
				nextRow, nextCol := row+dr, col+dc
				if (dr == 0 && dc == 0) || nextRow < 0 || nextRow >= 4 || nextCol < 0 || nextCol >= 4 {
					continue
				}
				next := nextRow*4 + nextCol
				adjacent = append(adjacent, fmt.Sprintf("%d:%c", next, b.Tiles[next]))
			}
		}
		neighbors = append(neighbors, fmt.Sprintf("%d:%c -> [%s]", i, b.Tiles[i], strings.Join(adjacent, ", ")))
	}
	return boardPrompt(b) + "\n\nExact neighbor map (a word may move only along these links):\n" +
		strings.Join(neighbors, "\n") +
		"\n\nWorked rule example on an imaginary 2x2 board A B / C D: BAD is B(1)->A(0)->D(3), " +
		"because each step is adjacent and no tile repeats. Do not submit example words unless they exist on the real board.\n" +
		fmt.Sprintf("Find at most %d words. ", maxWords) +
		"Start with high-confidence 3-5 letter words, then add longer words only when every transition is in the neighbor map. " +
		"Never reuse an index in one word."
}

func moveContext(parent context.Context, deadline time.Time) (context.Context, context.CancelFunc) {
	limit := time.Now().Add(25 * time.Second)
	if d := deadline.Add(-3 * time.Second); d.Before(limit) {
		limit = d
	}
	return context.WithDeadline(parent, limit)
}

func ptr[T any](v T) *T { return &v }

func genAIBackendName(client *genai.Client) string {
	if client != nil && client.ClientConfig().Backend == genai.BackendVertexAI {
		return "vertex"
	}
	return "gemini-api"
}

func playerThinkingConfig() *genai.ThinkingConfig {
	if level := strings.ToUpper(strings.TrimSpace(os.Getenv("GEMINI_PLAYER_THINKING_LEVEL"))); level != "" {
		switch genai.ThinkingLevel(level) {
		case genai.ThinkingLevelMinimal, genai.ThinkingLevelLow,
			genai.ThinkingLevelMedium, genai.ThinkingLevelHigh:
			return &genai.ThinkingConfig{ThinkingLevel: genai.ThinkingLevel(level)}
		}
	}
	budget := int32(192)
	if raw := strings.TrimSpace(os.Getenv("GEMINI_PLAYER_THINKING_BUDGET")); raw != "" {
		if parsed, err := strconv.ParseInt(raw, 10, 32); err == nil {
			budget = int32(parsed)
		}
	}
	return &genai.ThinkingConfig{ThinkingBudget: &budget}
}

func envInt32(name string, fallback, minimum, maximum int32) int32 {
	raw := strings.TrimSpace(os.Getenv(name))
	if raw == "" {
		return fallback
	}
	value, err := strconv.ParseInt(raw, 10, 32)
	if err != nil || value < int64(minimum) || value > int64(maximum) {
		return fallback
	}
	return int32(value)
}
