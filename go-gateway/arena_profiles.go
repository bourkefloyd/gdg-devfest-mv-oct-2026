package main

import (
	"context"
	"errors"
	"math/rand/v2"
	"os"
	"strconv"
	"strings"
	"time"

	"go-gateway/internal/arena"
	"go-gateway/internal/players"
	"go-gateway/internal/wordhunt"

	"google.golang.org/genai"
)

type arenaProfile struct {
	Name, Backend, ModelID, Strategy string
	Percent                          int
}

func arenaProfiles() []arenaProfile {
	return []arenaProfile{
		{Name: "Gemini 3.8 Flash", Percent: profilePercent("ARENA_PROFILE_GEMINI_AGENT_PERCENT", 50), Backend: "google-genai", ModelID: env("ARENA_PROFILE_GEMINI_AGENT_MODEL", "gemini-3.8-flash"), Strategy: "submit-words-agent"},
		{Name: "Gemma 4 26B A4B", Percent: profilePercent("ARENA_PROFILE_GEMMA_BASELINE_PERCENT", 50), Backend: "gemini-api-hosted-gemma", ModelID: env("ARENA_PROFILE_GEMMA_BASELINE_MODEL", "gemma-4-26b-a4b-it"), Strategy: "baseline"},
	}
}

func vertexGemmaEnabled() bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv("ARENA_GEMMA_VERTEX"))) {
	case "1", "true", "yes":
		return true
	default:
		return false
	}
}

func newArenaProfileFactory(api, vertex *genai.Client) arena.PlayerFactory {
	profiles := arenaProfiles()
	return func(_ context.Context, spec arena.GameSpec) (players.Player, error) {
		profile := chooseProfile(profiles, spec.Index, spec.Total)
		var player players.Player
		var acquire func(context.Context) (func(), error)
		switch {
		case profile.Strategy == "submit-words-agent":
			if api == nil {
				player = errorPlayer{err: errors.New("Google GenAI client is unavailable")}
				break
			}
			player = players.NewGeminiAgent(api, profile.ModelID, wordhunt.Default())
			acquire = players.AcquireGemini
		case profile.Strategy == "baseline" && vertexGemmaEnabled():
			if vertex == nil {
				player = errorPlayer{err: errors.New("Vertex Gemma is enabled but the Vertex client is unavailable")}
				break
			}
			profile.ModelID = env("ARENA_GEMMA_VERTEX_MODEL", "gemma-4-26b-a4b-it-maas")
			profile.Backend = "vertex-hosted-gemma"
			player = players.NewProfiledHostedGemmaPlayer(vertex, profile.ModelID, profile.Name, profile.Strategy)
			acquire = players.AcquireVertexGemma
		default:
			if api == nil {
				player = errorPlayer{err: errors.New("Google GenAI client is unavailable")}
				break
			}
			model := profile.ModelID
			player = players.NewProfiledHostedGemmaPlayer(api, model, profile.Name, profile.Strategy)
			acquire = func(ctx context.Context) (func(), error) {
				return players.AcquireGemma(ctx, model)
			}
		}
		if acquire != nil {
			player = slotPlayer{Player: player, acquire: acquire}
		}
		return &arenaRetryPlayer{Player: player, Profile: profile}, nil
	}
}

type slotPlayer struct {
	players.Player
	acquire func(context.Context) (func(), error)
}

func (p slotPlayer) Play(ctx context.Context, board wordhunt.Board, deadline time.Time) (players.Result, error) {
	release, err := p.acquire(ctx)
	if err != nil {
		return players.Result{}, err
	}
	defer release()
	return p.Player.Play(ctx, board, deadline)
}

func chooseProfile(profiles []arenaProfile, index, total int) arenaProfile {
	if total < 1 {
		total = 1
	}
	at := 0
	for i, profile := range profiles {
		count := (total*profile.Percent + 50) / 100
		if i == len(profiles)-1 || index < at+count {
			return profile
		}
		at += count
	}
	return profiles[len(profiles)-1]
}

type arenaRetryPlayer struct {
	Player  players.Player
	Profile arenaProfile
}

func (p *arenaRetryPlayer) Name() string { return p.Profile.Name }

func (p *arenaRetryPlayer) Play(ctx context.Context, board wordhunt.Board, deadline time.Time) (players.Result, error) {
	delays := [...]time.Duration{250 * time.Millisecond, 500 * time.Millisecond, time.Second, 2 * time.Second}
	var result players.Result
	var err error
	for attempt := 0; attempt <= len(delays); attempt++ {
		if !time.Now().Before(deadline) {
			err = context.DeadlineExceeded
			break
		}
		result, err = p.Player.Play(ctx, board, deadline)
		result.Profile = p.Profile.Name
		result.Retries = attempt
		if result.Backend == "" {
			result.Backend = p.Profile.Backend
		}
		if result.Model == "" {
			result.Model = p.Profile.ModelID
		}
		if err == nil {
			return result, nil
		}
		if attempt == len(delays) {
			break
		}
		delay := delays[attempt]
		if strings.Contains(err.Error(), "429") || strings.Contains(err.Error(), "RESOURCE_EXHAUSTED") {
			delay = 2 * time.Second
		}
		delay = delay/2 + time.Duration(rand.Int64N(int64(delay)+1))
		if remaining := time.Until(deadline); delay >= remaining {
			err = context.DeadlineExceeded
			break
		}
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return result, ctx.Err()
		case <-timer.C:
		}
	}
	return result, err
}

type errorPlayer struct{ err error }

func (p errorPlayer) Name() string { return "unavailable model" }
func (p errorPlayer) Play(context.Context, wordhunt.Board, time.Time) (players.Result, error) {
	return players.Result{}, p.err
}

func profilePercent(key string, fallback int) int {
	value, err := strconv.Atoi(strings.TrimSpace(os.Getenv(key)))
	if err != nil || value < 0 || value > 100 {
		return fallback
	}
	return value
}
