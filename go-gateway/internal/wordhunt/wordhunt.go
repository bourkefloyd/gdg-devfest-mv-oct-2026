// Package wordhunt implements the Word Hunt game core: dictionary, board
// generation, solver, path/word validation and scoring. It does no I/O.
package wordhunt

import (
	"math/rand"
	"sort"
	"strings"
	"sync"

	"go-gateway/dict"
)

const (
	Size     = 4
	NumTiles = Size * Size
	MinLen   = 3
	MaxLen   = 16
)

// Board is a 4x4 grid of lowercase letters, indexed row-major 0..15.
type Board struct {
	Seed  int64
	Tiles [NumTiles]byte
}

// Reason explains why a word was rejected. ReasonOK means accepted.
type Reason string

const (
	ReasonOK             Reason = "ok"
	ReasonInvalidWord    Reason = "invalid_word"
	ReasonTooShort       Reason = "too_short"
	ReasonTooLong        Reason = "too_long"
	ReasonNotAWord       Reason = "not_a_word"
	ReasonNotOnBoard     Reason = "not_on_board"
	ReasonBadIndex       Reason = "bad_index"
	ReasonNotAdjacent    Reason = "not_adjacent"
	ReasonReusedTile     Reason = "reused_tile"
	ReasonLenMismatch    Reason = "length_mismatch"
	ReasonLetterMismatch Reason = "letter_mismatch"
	ReasonDuplicate      Reason = "duplicate"
	ReasonLate           Reason = "late"
)

// Dict is an immutable trie-backed word list, safe for concurrent use.
type Dict struct {
	root *node
	n    int
}

type node struct {
	next [26]*node
	word bool
}

var (
	defaultOnce sync.Once
	defaultDict *Dict
)

var dice = [NumTiles]string{
	"aaeegn", "abbjoo", "achops", "affkps",
	"aoottw", "cimotu", "deilrx", "delrvy",
	"distty", "eeghnw", "eeinsu", "ehrtvw",
	"eiosst", "elrtty", "himnqu", "hlnnrz",
}

// NewDict builds a trie from words. ASCII case is normalized; words outside
// the allowed [a-z]{3,16} form are ignored.
func NewDict(words []string) *Dict {
	d := &Dict{root: &node{}}
	for _, word := range words {
		d.Insert(word)
	}
	return d
}

// Insert adds a valid word and reports whether it was newly inserted.
// Build dictionaries before publishing them for concurrent reads.
func (d *Dict) Insert(word string) bool {
	word, ok := normalize(word)
	if !ok || len(word) < MinLen || len(word) > MaxLen {
		return false
	}
	if d.root == nil {
		d.root = &node{}
	}
	n := d.root
	for i := range len(word) {
		j := word[i] - 'a'
		if n.next[j] == nil {
			n.next[j] = &node{}
		}
		n = n.next[j]
	}
	if n.word {
		return false
	}
	n.word = true
	d.n++
	return true
}

// Contains reports whether word is in the dictionary.
func (d *Dict) Contains(word string) bool {
	n := d.find(word)
	return n != nil && n.word
}

// HasPrefix reports whether at least one dictionary word starts with prefix.
func (d *Dict) HasPrefix(prefix string) bool { return d.find(prefix) != nil }

// Len returns the number of words in the dictionary.
func (d *Dict) Len() int {
	if d == nil {
		return 0
	}
	return d.n
}

func (d *Dict) find(s string) *node {
	s, ok := normalize(s)
	if !ok || d == nil || d.root == nil {
		return nil
	}
	n := d.root
	for i := range len(s) {
		n = n.next[s[i]-'a']
		if n == nil {
			return nil
		}
	}
	return n
}

// String renders the tiles as 16 uppercase letters.
func (b Board) String() string {
	out := make([]byte, NumTiles)
	for i, t := range b.Tiles {
		if t >= 'a' && t <= 'z' {
			out[i] = t - 'a' + 'A'
		} else {
			out[i] = t
		}
	}
	return string(out)
}

// NewBoard deterministically generates a board from seed with at least
// minWords solutions in the default dictionary.
func NewBoard(seed int64, minWords int) Board {
	rng := rand.New(rand.NewSource(seed))
	d := Default()
	var best Board
	bestCount := -1
	// A bound prevents a bad caller-provided threshold from hanging forever.
	// The normal threshold of 40 is usually met on the first few boards.
	for attempt := 0; attempt < 10000; attempt++ {
		order := rng.Perm(NumTiles)
		b := Board{Seed: seed}
		for pos, die := range order {
			faces := dice[die]
			b.Tiles[pos] = faces[rng.Intn(len(faces))]
		}
		count := len(b.Solve(d))
		if count > bestCount {
			best, bestCount = b, count
		}
		if count >= minWords {
			return b
		}
	}
	return best
}

// Solve returns every dictionary word on the board, sorted.
func (b Board) Solve(d *Dict) []string {
	if d == nil || d.root == nil {
		return []string{}
	}
	found := make(map[string]struct{})
	var letters [NumTiles]byte
	var dfs func(pos int, n *node, used uint16, depth int)
	dfs = func(pos int, n *node, used uint16, depth int) {
		if depth == MaxLen {
			return
		}
		c := b.Tiles[pos]
		if c >= 'A' && c <= 'Z' {
			c += 'a' - 'A'
		}
		if c < 'a' || c > 'z' {
			return
		}
		n = n.next[c-'a']
		if n == nil {
			return
		}
		letters[depth] = c
		depth++
		if n.word && depth >= MinLen {
			found[string(letters[:depth])] = struct{}{}
		}
		used |= 1 << pos
		for _, next := range neighbors(pos) {
			if used&(1<<next) == 0 {
				dfs(next, n, used, depth)
			}
		}
	}
	for pos := 0; pos < NumTiles; pos++ {
		dfs(pos, d.root, 0, 0)
	}
	words := make([]string, 0, len(found))
	for word := range found {
		words = append(words, word)
	}
	sort.Strings(words)
	return words
}

// ValidateWord checks word against the dictionary and board. If path is
// non-empty it is validated as given; otherwise a path is searched for.
func ValidateWord(b Board, d *Dict, word string, path []int) (ok bool, reason Reason) {
	word, valid := normalize(word)
	if !valid {
		return false, ReasonInvalidWord
	}
	if len(word) < MinLen {
		return false, ReasonTooShort
	}
	if len(word) > MaxLen {
		return false, ReasonTooLong
	}
	if d == nil || !d.Contains(word) {
		return false, ReasonNotAWord
	}
	if len(path) == 0 {
		if findPath(b, word) {
			return true, ReasonOK
		}
		return false, ReasonNotOnBoard
	}
	if len(path) != len(word) {
		return false, ReasonLenMismatch
	}
	var used uint16
	for i, pos := range path {
		if pos < 0 || pos >= NumTiles {
			return false, ReasonBadIndex
		}
		if used&(1<<pos) != 0 {
			return false, ReasonReusedTile
		}
		if i > 0 && !adjacent(path[i-1], pos) {
			return false, ReasonNotAdjacent
		}
		tile := b.Tiles[pos]
		if tile >= 'A' && tile <= 'Z' {
			tile += 'a' - 'A'
		}
		if tile != word[i] {
			return false, ReasonLetterMismatch
		}
		used |= 1 << pos
	}
	return true, ReasonOK
}

// Score returns the points for a word (0 if shorter than 3 letters).
func Score(word string) int {
	switch n := len(word); n {
	case 0, 1, 2:
		return 0
	case 3:
		return 100
	case 4:
		return 400
	case 5:
		return 800
	case 6:
		return 1400
	case 7:
		return 1800
	default:
		return 2200 + 400*(n-8)
	}
}

// Default returns the embedded ENABLE dictionary.
func Default() *Dict {
	defaultOnce.Do(func() {
		defaultDict = NewDict(strings.Fields(dict.ENABLE))
	})
	return defaultDict
}

func normalize(s string) (string, bool) {
	if len(s) == 0 {
		return "", false
	}
	out := make([]byte, len(s))
	for i := range len(s) {
		c := s[i]
		if c >= 'A' && c <= 'Z' {
			c += 'a' - 'A'
		}
		if c < 'a' || c > 'z' {
			return "", false
		}
		out[i] = c
	}
	return string(out), true
}

func neighbors(pos int) []int {
	row, col := pos/Size, pos%Size
	out := make([]int, 0, 8)
	for dr := -1; dr <= 1; dr++ {
		for dc := -1; dc <= 1; dc++ {
			if dr == 0 && dc == 0 {
				continue
			}
			r, c := row+dr, col+dc
			if r >= 0 && r < Size && c >= 0 && c < Size {
				out = append(out, r*Size+c)
			}
		}
	}
	return out
}

func adjacent(a, b int) bool {
	if a < 0 || a >= NumTiles || b < 0 || b >= NumTiles {
		return false
	}
	ar, ac := a/Size, a%Size
	br, bc := b/Size, b%Size
	dr, dc := ar-br, ac-bc
	if dr < 0 {
		dr = -dr
	}
	if dc < 0 {
		dc = -dc
	}
	return (dr != 0 || dc != 0) && dr <= 1 && dc <= 1
}

func findPath(b Board, word string) bool {
	var search func(pos, at int, used uint16) bool
	search = func(pos, at int, used uint16) bool {
		tile := b.Tiles[pos]
		if tile >= 'A' && tile <= 'Z' {
			tile += 'a' - 'A'
		}
		if tile != word[at] {
			return false
		}
		if at == len(word)-1 {
			return true
		}
		used |= 1 << pos
		for _, next := range neighbors(pos) {
			if used&(1<<next) == 0 && search(next, at+1, used) {
				return true
			}
		}
		return false
	}
	for pos := 0; pos < NumTiles; pos++ {
		if search(pos, 0, 0) {
			return true
		}
	}
	return false
}
