# Hack context

Theme: **Build, Secure, and Scale**

Judging begins at **5:15 PM PT**.

## Judging criteria

1. Gemini Cloud APIs
2. Technical execution
3. Security and reliability
4. Scalability and architecture
5. Agentic innovation (optional)

## Current status

- **Live:** https://wordrust-958584846348.us-central1.run.app/
- **Gemma minimal-thinking fix** (`a7a5000`) is on `main` and deploying.
- **Diffusion and diffusion JEV** are mock profiles running on Gemma 4 31B.
- **Real DiffusionGemma 26B** runs locally via MLX (about 1.9 s a move); the `localhost:8787` path is in progress.

See [architecture](architecture.md) and [load test results](load-test-results.md).

## Team tracks

- Track A — Game core (`track-a`)
- Track B — Model players (`track-b`)
- Track C — Gateway and security (`track-c`)
- Track D — Web UI (`track-d`)
- Track E — Infrastructure and load testing (`track-e`)

## Status

- **Track A — complete:** Game core merged in [PR #1](https://github.com/bourkefloyd/oxidizinggemma/pull/1). `go test -race ./...` passed; 30 s fuzz passed with 25.3M executions; validation benchmark 86.49 ns/op; solver benchmark 30.38 µs/op.
- **Track B:** _Add status._
- **Track C — complete:** Gateway, worker pool, and integration merged in [PR #5](https://github.com/bourkefloyd/oxidizinggemma/pull/5), [PR #7](https://github.com/bourkefloyd/oxidizinggemma/pull/7), and [PR #8](https://github.com/bourkefloyd/oxidizinggemma/pull/8). `go test -race ./...` passes, and `internal/api` passes 3x under `-race`.
  - **Security tests:** 401 on every protected route without a valid key; 429 after the create burst while a second key is unaffected; per-key and global active-game caps; 413 oversize, 400 unknown field, trailing data, or wrong type; CORS allowlist allows and denies; games scoped to their key (404 for others); `max_tokens` capped at 256; backend errors not echoed. The injection string returns 400 `invalid_word`; duplicates, out-of-range paths, forged non-adjacent paths, and late words are rejected; 50 concurrent submissions accept exactly once.
  - **Failover tests:** Gemini 503 twice then success is retried; with Gemini down, the seat falls back to the solver bot (labeled) and the breaker opens; a Gemma worker killed mid-stream isn't retried and the seat spills to Gemini (labeled); a failing or timed-out player still lets the game finish. Pool tests cover least-outstanding spreading, queue full and timeout, retry before the first token, and idle timeout.
  - **Config:** `GATEWAY_API_KEYS` (required), `HOST`/`PORT` (default `127.0.0.1:8787`), `CORS_ALLOWED_ORIGINS` (default `http://localhost:4317,http://127.0.0.1:4317`), `MAX_ACTIVE_GAMES`, `PLAYERS=mock|real` (real when Gemini credentials are set), `MOCK_LATENCY_MS`, `GRPC_WORKER_ADDRS`, `GEMMA_PER_WORKER`, `GEMMA_MAX_QUEUE`, `GEMMA_MAX_WAIT_MS`, `GEMMA_MODEL`, `GEMMA_HOSTED_MODEL`, `GEMINI_PLAYER_MODEL`, `GEMINI_FALLBACK_MODEL`, `GEMINI_COMMENTATOR_MODEL`.
  - **POSTs require `Content-Type: application/json`** or they get 415. This blocks cross-site form posts. `curl -d` sends a form type by default, so pass `-H 'Content-Type: application/json'` in k6 scripts and demo curls.
  - **`stream_token`:** `POST /v1/games` returns a per-game `stream_token`. `GET /v1/games/{id}/events?token=…` accepts it because browser `EventSource` can't send an auth header. `"agent": true` on create seats the Gemini agent. `api.Server.Mount` adds extra routes behind the same middleware.
- **Track D:** _Add status._
- **Track E:** _Add status._

## Documents

- [Project context](project-context.md)
- [Hack plan](hack-plan.md)
- [Local setup notes](local-setup.md)
- [Architecture](architecture.md)
- [Load test results](load-test-results.md)
- [Serving review](serving-review.md)
