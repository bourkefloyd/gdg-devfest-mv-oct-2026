// Package players defines the Player and Commentator contracts shared by
// model, mock, human and solver-bot players.
package players

import (
	"context"
	"time"

	"go-gateway/internal/wordhunt"
)

// Claim is one word a player says it found, with an optional tile path.
type Claim struct {
	Word string
	Path []int
}

// Result is a player's full move. Claims are untrusted until validated.
type Result struct {
	Claims   []Claim
	Backend  string
	Model    string
	Latency  time.Duration
	Fallback bool
	Raw      string
}

type Player interface {
	Name() string
	Play(ctx context.Context, b wordhunt.Board, deadline time.Time) (Result, error)
}

// PlayerSummary holds only server-validated data for one player.
type PlayerSummary struct {
	Name     string
	Backend  string
	Model    string
	Fallback bool
	Score    int
	Accepted []string
	Rejected int
}

// GameSummary is the only input a Commentator receives.
type GameSummary struct {
	GameID   string
	Tiles    string
	Players  []PlayerSummary
	MaxScore int
	Final    bool
}

type Commentator interface {
	Stream(ctx context.Context, ev GameSummary, out chan<- string) error
}
