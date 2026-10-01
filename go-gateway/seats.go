package main

import (
	"time"

	"go-gateway/internal/api"
	"go-gateway/internal/pool"
)

// seatsFromEnv wires players for each mode. Until the real model players
// are merged this always returns mocks; MOCK_LATENCY_MS scales them.
func seatsFromEnv(_ *pool.Pool) api.SeatFunc {
	scale := time.Duration(envInt("MOCK_LATENCY_MS", 1000)) * time.Millisecond
	return api.MockSeats(scale)
}

func geminiReady() bool { return false }
