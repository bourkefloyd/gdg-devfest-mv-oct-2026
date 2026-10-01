package wordhunt

import (
	"reflect"
	"slices"
	"testing"
)

func testBoard(s string) Board {
	if len(s) != NumTiles {
		panic("test board must have 16 tiles")
	}
	var b Board
	copy(b.Tiles[:], s)
	return b
}

func TestDict(t *testing.T) {
	d := NewDict([]string{"Cat", "cater", "DOG", "cat", "two words", "éclair", "an"})
	if got, want := d.Len(), 3; got != want {
		t.Fatalf("Len() = %d, want %d", got, want)
	}
	for _, word := range []string{"cat", "CAT", "Cater", "dog"} {
		if !d.Contains(word) {
			t.Errorf("Contains(%q) = false", word)
		}
	}
	for _, word := range []string{"ca", "two words", "éclair"} {
		if d.Contains(word) {
			t.Errorf("Contains(%q) = true", word)
		}
	}
	if !d.HasPrefix("CA") || !d.HasPrefix("cate") || d.HasPrefix("cow") {
		t.Fatal("prefix lookup incorrect")
	}
}

func TestDefaultDictionary(t *testing.T) {
	if got := Default().Len(); got != 170398 {
		t.Fatalf("default dictionary has %d words", got)
	}
	if !Default().Contains("oxidizing") {
		t.Fatal("ENABLE should contain oxidizing")
	}
}

func TestNeighbors(t *testing.T) {
	tests := map[int]int{0: 3, 3: 3, 5: 8, 6: 8, 12: 3, 15: 3, 1: 5, 4: 5}
	for pos, want := range tests {
		if got := len(neighbors(pos)); got != want {
			t.Errorf("neighbors(%d) = %d, want %d", pos, got, want)
		}
	}
}

func TestValidateWord(t *testing.T) {
	b := testBoard("catxdogxxxxxxxxt")
	d := NewDict([]string{"cat", "dog", "cog", "cater"})
	tests := []struct {
		name string
		word string
		path []int
		ok   bool
		want Reason
	}{
		{"derived", "CAT", nil, true, ReasonOK},
		{"valid path", "cat", []int{0, 1, 2}, true, ReasonOK},
		{"not adjacent", "cat", []int{0, 1, 15}, false, ReasonNotAdjacent},
		{"reused", "cat", []int{0, 1, 0}, false, ReasonReusedTile},
		{"bad index low", "cat", []int{-1, 1, 2}, false, ReasonBadIndex},
		{"bad index high", "cat", []int{0, 1, 16}, false, ReasonBadIndex},
		{"length mismatch", "cat", []int{0, 1}, false, ReasonLenMismatch},
		{"letters mismatch", "cat", []int{0, 1, 5}, false, ReasonLetterMismatch},
		{"too short", "an", nil, false, ReasonTooShort},
		{"too long", "abcdefghijklmnopq", nil, false, ReasonTooLong},
		{"invalid", "ca!", nil, false, ReasonInvalidWord},
		{"non ASCII", "cát", nil, false, ReasonInvalidWord},
		{"not dictionary", "cab", nil, false, ReasonNotAWord},
		{"not board", "cater", nil, false, ReasonNotOnBoard},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ok, got := ValidateWord(b, d, tt.word, tt.path)
			if ok != tt.ok || got != tt.want {
				t.Fatalf("ValidateWord = (%v, %q), want (%v, %q)", ok, got, tt.ok, tt.want)
			}
		})
	}
}

func TestSolve(t *testing.T) {
	b := testBoard("catxdogxxxxxxxxx")
	d := NewDict([]string{"cat", "dog", "cog", "cater", "god", "tax"})
	got := b.Solve(d)
	want := []string{"cat", "cog", "dog", "god"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Solve() = %v, want %v", got, want)
	}
}

func TestScore(t *testing.T) {
	want := []int{0, 0, 0, 100, 400, 800, 1400, 1800, 2200, 2600, 3000, 3400, 3800, 4200, 4600, 5000, 5400}
	for n, score := range want {
		if got := Score(string(make([]byte, n))); got != score {
			t.Errorf("Score(len=%d) = %d, want %d", n, got, score)
		}
	}
}

func TestNewBoardDeterministicAndMinimum(t *testing.T) {
	a := NewBoard(42, 40)
	b := NewBoard(42, 40)
	if a != b {
		t.Fatalf("same seed produced %q and %q", a.String(), b.String())
	}
	if got := len(a.Solve(Default())); got < 40 {
		t.Fatalf("board has %d words, want >= 40", got)
	}
	if slices.Contains(a.Tiles[:], byte(0)) {
		t.Fatal("board has empty tiles")
	}
}

func TestBoardString(t *testing.T) {
	if got := testBoard("abcdefghijklmnop").String(); got != "ABCDEFGHIJKLMNOP" {
		t.Fatalf("String() = %q", got)
	}
}

func FuzzValidateWord(f *testing.F) {
	b := testBoard("abcdefghijklmnop")
	d := NewDict([]string{"abc", "afk", "aei", "dhlp"})
	f.Add("abc", []byte{0, 1, 2})
	f.Add("afk", []byte{0, 5, 10})
	f.Fuzz(func(t *testing.T, word string, raw []byte) {
		if len(raw) > 32 {
			raw = raw[:32]
		}
		path := make([]int, len(raw))
		for i, n := range raw {
			path[i] = int(int8(n))
		}
		ValidateWord(b, d, word, path)
	})
}

func BenchmarkValidateWord(b *testing.B) {
	board := NewBoard(42, 40)
	words := board.Solve(Default())
	word := words[len(words)/2]
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		ValidateWord(board, Default(), word, nil)
	}
}

func BenchmarkSolve(b *testing.B) {
	board := NewBoard(42, 40)
	d := Default()
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		board.Solve(d)
	}
}
