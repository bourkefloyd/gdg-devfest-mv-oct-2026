# Project context

## Theme
Build, Secure, and Scale

## Hackathon grading criteria (dev track)
1. Gemini Cloud APIs (decides the winner)
2. Technical execution
3. Security, reliability
4. Scalability / architecture
5. Agentic innovation (optional)

Rigor is expected throughout (confirmed by Bourke): tests, threat model, reliability targets, measured load-test results.

## Deadline
- Judging starts 5:15 PM local (assumed US Pacific, 00:15 UTC)

## Direction
- Demo: models play Word Hunt (Boggle-style 4x4 grid; Go was the earlier idea) through a hybrid Gemma + Gemini gateway, then load test with many concurrent games
- JEPA-style model: stretch goal only

## Codebase
- Working repo (now public): [bourkefloyd/oxidizinggemma](https://github.com/bourkefloyd/oxidizinggemma), copied from jorgeajimenez/oxidizinggemma
- Local clone: ~/projects/oxidizinggemma on Bourke's MacBook
- Rust Gemma inference engine (Metal by default) behind a Go gRPC gateway
