// Package wordhunt implements the Word Hunt game core: dictionary, board
// generation, solver, path/word validation and scoring. It does no I/O.
package wordhunt

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

// String renders the tiles as 16 uppercase letters.
func (b Board) String() string {
	out := make([]byte, NumTiles)
	for i, t := range b.Tiles {
		out[i] = t - 'a' + 'A'
	}
	return string(out)
}

// NewBoard deterministically generates a board from seed with at least
// minWords solutions in the default dictionary.
func NewBoard(seed int64, minWords int) Board { return Board{Seed: seed} }

// Solve returns every dictionary word on the board, sorted.
func (b Board) Solve(d *Dict) []string { return nil }

// ValidateWord checks word against the dictionary and board. If path is
// non-empty it is validated as given; otherwise a path is searched for.
func ValidateWord(b Board, d *Dict, word string, path []int) (ok bool, reason Reason) {
	return false, ReasonNotAWord
}

// Score returns the points for a word (0 if shorter than 3 letters).
func Score(word string) int { return 0 }

// Default returns the embedded ENABLE dictionary.
func Default() *Dict { return &Dict{root: &node{}} }
