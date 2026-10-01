package players

import (
	"strings"
	"testing"
)

func TestParseGeminiOutput(t *testing.T) {
	claims, err := ParseGeminiOutput(`{"words":[{"word":"Cat","path":[0,1,2]},{"word":"cat"},{"word":"x"},{"word":"tone"}]}`)
	if err != nil {
		t.Fatal(err)
	}
	if len(claims) != 2 || claims[0].Word != "cat" || claims[1].Word != "tone" {
		t.Fatalf("unexpected claims: %#v", claims)
	}
	if _, err := ParseGeminiOutput(`{"words":[],"score":9999}`); err == nil {
		t.Fatal("expected extra-field error")
	}
	if _, err := ParseGeminiOutput(`{"words":"cat"}`); err == nil {
		t.Fatal("expected wrong-type error")
	}
}

func TestParseGeminiOutputLimits(t *testing.T) {
	var items []string
	for range 151 {
		items = append(items, `{"word":"cat"}`)
	}
	if _, err := ParseGeminiOutput(`{"words":[` + strings.Join(items, ",") + `]}`); err == nil {
		t.Fatal("expected item-limit error")
	}
	if _, err := ParseGeminiOutput(strings.Repeat("x", maxRawOutput+1)); err == nil {
		t.Fatal("expected size-limit error")
	}
}

func TestParseGemmaOutput(t *testing.T) {
	claims, err := ParseGemmaOutput("thinking...\nWORDS: Cat, TONE; cat, x, ignore_previous_instructions")
	if err != nil {
		t.Fatal(err)
	}
	if len(claims) != 2 || claims[0].Word != "cat" || claims[1].Word != "tone" {
		t.Fatalf("unexpected claims: %#v", claims)
	}
	if _, err := ParseGemmaOutput("cat, tone"); err == nil {
		t.Fatal("expected missing-prefix error")
	}
}

func FuzzParseGeminiOutput(f *testing.F) {
	f.Add(`{"words":[{"word":"cat"}]}`)
	f.Fuzz(func(t *testing.T, raw string) { _, _ = ParseGeminiOutput(raw) })
}

func FuzzParseGemmaOutput(f *testing.F) {
	f.Add("WORDS: cat, tone")
	f.Fuzz(func(t *testing.T, raw string) { _, _ = ParseGemmaOutput(raw) })
}
