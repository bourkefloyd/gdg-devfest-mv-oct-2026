package main

import "testing"

func TestArenaProfileDisplayNames(t *testing.T) {
	for _, key := range []string{
		"ARENA_PROFILE_GEMINI_AGENT_PERCENT",
		"ARENA_PROFILE_GEMINI_AGENT_MODEL",
		"ARENA_PROFILE_GEMMA_BASELINE_PERCENT",
		"ARENA_PROFILE_GEMMA_BASELINE_MODEL",
		"ARENA_PROFILE_GEMMA_DIFFUSION_PERCENT",
		"ARENA_PROFILE_GEMMA_DIFFUSION_MODEL",
		"ARENA_PROFILE_GEMMA_JEV_PERCENT",
		"ARENA_PROFILE_GEMMA_JEV_MODEL",
	} {
		t.Setenv(key, "")
	}

	got := arenaProfiles()
	want := []arenaProfile{
		{Name: "Gemini 3.8 Flash", Percent: 10, Backend: "google-genai", ModelID: "gemini-3.8-flash", Strategy: "submit-words-agent"},
		{Name: "Gemma 4 26B A4B", Percent: 30, Backend: "gemini-api-hosted-gemma", ModelID: "gemma-4-26b-a4b-it", Strategy: "baseline"},
		{Name: "Gemma 4 31B diffusion (mock)", Percent: 30, Backend: "gemini-api-hosted-gemma", ModelID: "gemma-4-26b-a4b-it", Strategy: "diffusion"},
		{Name: "Gemma 4 31B diffusion JEV (mock)", Percent: 30, Backend: "gemini-api-hosted-gemma", ModelID: "gemma-4-26b-a4b-it", Strategy: "diffusion-jev"},
	}
	if len(got) != len(want) {
		t.Fatalf("len(arenaProfiles()) = %d, want %d", len(got), len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("profile %d = %+v, want %+v", i, got[i], want[i])
		}
	}
}
