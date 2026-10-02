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
- No 4:45 PM code freeze
- Build until 6:15 PM PT (01:15 UTC), per Bourke on 2026-10-02

## Direction
- Demo: models play Word Hunt (Boggle-style 4x4 grid; Go was the earlier idea) through a hybrid Gemma + Gemini gateway, then load test with many concurrent games
- JEPA-style model: stretch goal only

## GCP
- Project: gen-lang-client-0189911611 (wordrust-hack), region us-central1
- Team name: Word Rust (assumed from project name)

## Codebase
- Working repo (now public): [bourkefloyd/oxidizinggemma](https://github.com/bourkefloyd/oxidizinggemma), copied from jorgeajimenez/oxidizinggemma
- Local clone: ~/projects/oxidizinggemma on Bourke's MacBook
- Rust Gemma inference engine (Metal by default) behind a Go gRPC gateway
