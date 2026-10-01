package players

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"go-gateway/internal/wordhunt"

	"google.golang.org/genai"
)

// GeminiAgent iterates on server validation feedback via submit_words.
type GeminiAgent struct {
	Client          *genai.Client
	Model           string
	Backend         string
	Dict            *wordhunt.Dict
	MaxOutputTokens int32
	MaxWords        int
}

func NewGeminiAgent(client *genai.Client, model string, dict *wordhunt.Dict) *GeminiAgent {
	if model == "" {
		model = "gemini-3.8-flash"
	}
	if dict == nil {
		dict = wordhunt.Default()
	}
	return &GeminiAgent{
		Client: client, Model: model, Backend: genAIBackendName(client), Dict: dict,
		MaxOutputTokens: envInt32("GEMINI_AGENT_MAX_OUTPUT_TOKENS", 8192, 512, 32768),
		MaxWords:        int(envInt32("GEMINI_AGENT_MAX_WORDS", 20, 1, 50)),
	}
}

func (p *GeminiAgent) Name() string { return "Gemini agent" }

func (p *GeminiAgent) Play(ctx context.Context, b wordhunt.Board, deadline time.Time) (Result, error) {
	start := time.Now()
	if p.Client == nil {
		return Result{}, errors.New("nil Gemini client")
	}
	ctx, cancel := moveContext(ctx, deadline)
	defer cancel()

	tool := &genai.Tool{FunctionDeclarations: []*genai.FunctionDeclaration{{
		Name:        "submit_words",
		Description: "Submit Word Hunt words for authoritative validation.",
		ParametersJsonSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"words": map[string]any{
					"type":     "array",
					"maxItems": p.MaxWords,
					"items":    map[string]any{"type": "string"},
				},
			},
			"required": []string{"words"},
		},
	}}}
	chat, err := p.Client.Chats.Create(ctx, p.Model, &genai.GenerateContentConfig{
		Tools: []*genai.Tool{tool},
		ToolConfig: &genai.ToolConfig{FunctionCallingConfig: &genai.FunctionCallingConfig{
			Mode: genai.FunctionCallingConfigModeAny,
		}},
		Temperature:     ptr(float32(0.2)),
		MaxOutputTokens: p.MaxOutputTokens,
		ThinkingConfig:  &genai.ThinkingConfig{ThinkingBudget: ptr(int32(128))},
	}, nil)
	if err != nil {
		return Result{}, err
	}

	response, err := chat.SendMessage(ctx, *genai.NewPartFromText(
		geminiSearchPrompt(b, p.MaxWords) + "\nCall submit_words with your best candidates.",
	))
	if err != nil {
		return Result{}, err
	}

	accepted := map[string]Claim{}
	var raw strings.Builder
	for turn := 0; turn < 4; turn++ {
		calls := response.FunctionCalls()
		if len(calls) == 0 {
			if turn > 0 {
				break
			}
			return Result{}, errors.New("agent did not call submit_words")
		}
		var words []string
		for _, call := range calls {
			if call.Name != "submit_words" {
				return Result{}, fmt.Errorf("agent called unknown tool %q", call.Name)
			}
			submitted, err := wordsArgument(call.Args)
			if err != nil {
				return Result{}, err
			}
			words = append(words, submitted...)
		}
		verdicts := make([]map[string]any, 0, len(words))
		for _, candidate := range words {
			word := strings.ToLower(strings.TrimSpace(candidate))
			reason := wordhunt.ReasonInvalidWord
			ok := false
			if wordRE.MatchString(word) {
				if _, duplicate := accepted[word]; duplicate {
					reason = wordhunt.ReasonDuplicate
				} else {
					ok, reason = wordhunt.ValidateWord(b, p.Dict, word, nil)
				}
			}
			if ok {
				if len(accepted) < 150 {
					accepted[word] = Claim{Word: word}
				} else {
					ok = false
					reason = wordhunt.ReasonInvalidWord
				}
			}
			verdicts = append(verdicts, map[string]any{
				"word":     word,
				"accepted": ok,
				"reason":   string(reason),
			})
		}
		fmt.Fprintf(&raw, "turn %d: %d submitted, %d accepted\n", turn+1, len(words), len(accepted))
		if turn == 3 {
			break
		}
		response, err = chat.SendMessage(ctx, *genai.NewPartFromFunctionResponse(
			"submit_words",
			map[string]any{"verdicts": verdicts, "instruction": "Try different valid words not already accepted."},
		))
		if err != nil {
			return Result{}, err
		}
	}
	claims := make([]Claim, 0, len(accepted))
	for _, claim := range accepted {
		claims = append(claims, claim)
	}
	sort.Slice(claims, func(i, j int) bool { return claims[i].Word < claims[j].Word })
	return Result{
		Claims:  claims,
		Backend: p.Backend + "-agent",
		Model:   p.Model,
		Latency: time.Since(start),
		Raw:     raw.String(),
	}, nil
}

func wordsArgument(args map[string]any) ([]string, error) {
	value, ok := args["words"]
	if !ok {
		return nil, errors.New("submit_words missing words")
	}
	values, ok := value.([]any)
	if !ok {
		if typed, ok := value.([]string); ok {
			return typed, nil
		}
		return nil, errors.New("submit_words words is not an array")
	}
	if len(values) > 150 {
		return nil, errors.New("submit_words exceeds 150 words")
	}
	words := make([]string, 0, len(values))
	for _, value := range values {
		word, ok := value.(string)
		if !ok {
			return nil, errors.New("submit_words contains a non-string")
		}
		words = append(words, word)
	}
	return words, nil
}
