package main

import (
	"testing"
)

func TestArenaProfileDisplayNames(t *testing.T) {
	for _, key := range []string{
		"ARENA_PROFILE_GEMINI_AGENT_PERCENT",
		"ARENA_PROFILE_GEMINI_AGENT_MODEL",
		"ARENA_PROFILE_GEMMA_BASELINE_PERCENT",
		"ARENA_PROFILE_GEMMA_BASELINE_MODEL",
		"ARENA_GEMMA_VERTEX",
	} {
		t.Setenv(key, "")
	}

	got := arenaProfiles()
	want := []arenaProfile{
		{Name: "Gemini 3.8 Flash", Percent: 50, Backend: "google-genai", ModelID: "gemini-3.8-flash", Strategy: "submit-words-agent"},
		{Name: "Gemma 4 26B A4B", Percent: 50, Backend: "gemini-api-hosted-gemma", ModelID: "gemma-4-26b-a4b-it", Strategy: "baseline"},
	}
	if len(got) != len(want) {
		t.Fatalf("len(arenaProfiles()) = %d, want %d", len(got), len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("profile %d = %+v, want %+v", i, got[i], want[i])
		}
	}
	if vertexGemmaEnabled() {
		t.Fatal("Vertex Gemma should be off by default")
	}
	t.Setenv("ARENA_GEMMA_VERTEX", "true")
	if !vertexGemmaEnabled() {
		t.Fatal("ARENA_GEMMA_VERTEX=true should enable Vertex Gemma")
	}
}
