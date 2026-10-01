package players

import "testing"

func TestWordsArgument(t *testing.T) {
	words, err := wordsArgument(map[string]any{"words": []any{"cat", "tone"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(words) != 2 || words[1] != "tone" {
		t.Fatalf("unexpected words: %#v", words)
	}
	for _, args := range []map[string]any{
		{},
		{"words": "cat"},
		{"words": []any{"cat", 42}},
	} {
		if _, err := wordsArgument(args); err == nil {
			t.Fatalf("expected error for %#v", args)
		}
	}
}
