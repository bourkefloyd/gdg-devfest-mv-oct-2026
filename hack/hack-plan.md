# Hack plan: Word Hunt Arena — Build, Secure, and Scale

Gemma (local, Rust engine) races Gemini (Vertex AI / Gemini API) on the same Word Hunt board, through a hardened hybrid Go gateway, load tested with many concurrent games.

- **Owner:** Bourke Floyd. **Theme:** Build, Secure, and Scale. **Grading:** Gemini Cloud APIs (decides the winner), technical execution, security and reliability, scalability, agentic (optional). See [project-context.md](project-context.md).
- **Clock (Pacific Time / UTC):** start 2:00 PM / 21:00. Working demo **4:15 PM / 23:15**. Code freeze **4:45 PM / 23:45**. Judging **5:15 PM / 00:15**.
- **Code basis:** `bourkefloyd/oxidizinggemma` at `af8a6bb`. The private fork couldn't be cloned without credentials, so I read the public upstream at the same commit (the setup audit confirms the fork is a clean clone of `af8a6bb`).

---

## 1. What the code supports today, and what's missing

| Area | Today (`af8a6bb`) | Impact on this plan |
|---|---|---|
| Rust worker (`src/main.rs`) | Tonic gRPC `StreamGenerate(prompt, max_tokens, temperature)` streaming tokens. Model loads once, tries Metal, then CUDA, then CPU. | Gemma is ready as a text-in/token-stream backend. No changes are needed for the MVP. |
| Worker concurrency | One `Arc<Mutex<WorkerState>>`. Each request holds the lock for the whole prefill and decode, including `tx.send().await`. **One generation at a time per worker**, with no batching. | Gemma throughput is about 1 move in flight per worker. The scaling story is a worker pool, admission control, and spillover to Gemini, not "make one worker parallel". |
| Gemma speed | README/BUILDERS_LOG report about 4–6 tok/s on M3 Max Metal and 30–43 tok/s on H100. | A ~100-token Gemma move takes ~20 s on the Mac, which fits once inside an 80 s round. Gemma gets **one** call per game. |
| Sampling | `LogitsProcessor::new(42, …)` uses a fixed seed on every request. | The same board gives the same Gemma output. That's good for reproducible demos; note it for load tests. |
| Worker hardcodes | Binds `0.0.0.0:50051` with plaintext and no auth. Model dir is fixed at `models/gemma-4-e2b`. | Security finding: bind to `127.0.0.1` through an env var (small Rust change, Track E). |
| Go gateway (`go-gateway/main.go`) | One `POST /v1/chat/completions` that relays SSE from a single worker. `/health` is static. | Everything game-related is new. |
| Gateway security gaps | No auth. `CORS *`. No body-size limit. No `max_tokens` cap. Logs raw prompts. Errors are sent in-band as SSE tokens. Insecure gRPC. `/health` never checks the worker. | These become the "Secure" before/after story. |
| Ops | No tests (Rust or Go). No metrics. One `GRPC_WORKER_ADDR`. | Tests, Prometheus metrics, and a worker pool are all new. |
| Deploy | `sky-gcp.yaml` (L4) and `sky-nebius.yaml` (H100) SkyPilot specs. | Stretch goal: a second Gemma worker on GCP L4. |
| Weights | **Not on the Mac yet.** `google/gemma-4-E2B-it` is ~5 GB and gated (needs `HF_TOKEN` and the accepted license). | **Critical path: start the download at 2:00 PM.** Fallback: hosted Gemma through the Gemini API (verify the model is available at kickoff), labeled "Gemma (hosted)" in the UI. |

## 2. The game: Word Hunt

**Rules (GamePigeon-style):**
- 4x4 grid. 80 s round (configurable; load tests use 10–15 s).
- A word is a chain of tiles where each tile is adjacent to the previous one (8 directions). A tile can't be reused within a word.
- Minimum length 3, maximum 16. Each word scores once per player. There's no Qu tile, so every tile is one letter.
- Both players see the same board. Each scores independently (not Boggle-style cancellation), which makes the head-to-head race easy to read.

**Scoring** (GamePigeon values as commonly reported; treat as config): 3 letters = 100, 4 = 400, 5 = 800, 6 = 1400, 7 = 1800, 8 = 2200, then +400 for each additional letter.

**Board generation:** seeded RNG with the 16 classic Boggle dice (Qu face replaced with Q, or reroll on Q). The solver (trie DFS) runs on each board, which is then regenerated until it has ≥ 40 valid words. The solver output also gives "max possible score" and the **solver bot** fallback player.

**Dictionary:** ENABLE word list (public domain, ~173k words), normalized to lowercase `[a-z]{3,16}` and embedded with `go:embed`. Commit the source URL and SHA-256 in `dict/README`. Avoid SOWPODS/TWL because of licensing.

**Modes:** `race` (Gemma vs Gemini, spectator view: the main demo), `human_vs_gemini` (drag tiles in the UI), and `load` (mock or real models, short rounds).

**Go 9x9 (alternative, not built):** needs a full rules engine (captures, ko, area scoring). LLMs play it weakly. Games are long and sequential (~40+ dependent calls), so they make a poor fit for burst load tests. Word Hunt validates in milliseconds, is fully server-authoritative, and makes ~3–5 independent model calls per game.

## 3. Architecture

```
Browser (web/, Vite+React+TS+shadcn, :4317)
   │  HTTPS JSON + SSE, Bearer API key
   ▼
Go gateway (go-gateway, :8787)  ── auth · rate limit · validation · game store · metrics
   ├─ wordhunt/   board, trie, solver, path+word validation, scoring   (pure, no I/O)
   ├─ players/    Player interface; Gemini, Gemini agent, Gemma, mock, solver bot
   ├─ router/     backend choice, retries, breaker, spillover, budgets
   └─ pool/       N gRPC Gemma workers, least-outstanding, bounded queue
        │ gRPC StreamGenerate (existing proto, unchanged)
        ▼
   Rust Gemma workers (Metal on Mac; optional L4 via SkyPilot)
   Gemini: google.golang.org/genai → Vertex AI (ADC) or Gemini API (key)
```

**How Gemini is central** (criterion 1; each role maps to a visible feature):
1. **Strong player:** `gemini` Flash-class model with **structured output** (`responseMimeType: application/json` plus a `responseSchema`). Use a low thinking budget for latency.
2. **Agentic player (function calling):** Gemini gets tool `submit_words(words[])`. The server validates each word and returns per-word verdicts (`accepted`, `not_on_board`, `not_a_word`, `duplicate`). Gemini iterates on that verifiable feedback until the deadline or 4 turns. This is the agentic-innovation angle.
3. **Commentator:** a Flash-Lite-class model streams play-by-play and the end-of-game recap. Its input is only server-validated data (scores and accepted words), so it has no injection surface.
4. **Router and fallback tier:** when the Gemma pool is saturated or down, Gemma's seat spills to Gemini Flash-Lite, labeled in the UI. Gemini is also the target of the Vertex ↔ Gemini API backend failover.
5. **Both backends** through one SDK: `GOOGLE_GENAI_USE_VERTEXAI=true` selects Vertex, otherwise `GEMINI_API_KEY`. Model IDs come from env (`GEMINI_PLAYER_MODEL`, `GEMINI_COMMENTATOR_MODEL`). **Verify the current IDs with `models.list` at kickoff** rather than trusting defaults.

**Gemini output contract (player):**
```json
{"type":"object","properties":{"words":{"type":"array","maxItems":150,
  "items":{"type":"object","properties":{
    "word":{"type":"string"},
    "path":{"type":"array","items":{"type":"integer"}}},"required":["word"]}}},
 "required":["words"]}
```
The board is sent as a 4x4 letter grid plus tile indices 0–15. `path` is optional because LLMs are unreliable at coordinates. The server **re-derives** a path with the solver and records "path claim correct" as a metric.

**Gemma output contract:** a small model is unreliable at JSON, so the prompt asks for one line, `WORDS: cat, tone, stone`. `max_tokens` is 120.

**Parsing and validation pipeline (the same for every player, human or model):**
1. Cap raw output size at 16 KB.
2. Gemini: strict `json.Unmarshal` into the typed struct. Gemma: take the text after `WORDS:` and split on non-letters.
3. Normalize each candidate to lowercase. Drop anything not matching `^[a-z]{3,16}$`. Keep at most 150 candidates.
4. Check the trie, then check the board: the solver confirms a valid path exists, or the supplied path is validated for adjacency, no reuse, indices 0–15, and letters matching the word.
5. Dedupe per player. Enforce the deadline using the server clock.
6. Score on the server.

Rejected candidates are kept with a reason and shown in the UI. Model text is never executed, never trusted for scores, and never fed into another prompt without validation.

**Per-game call burst:** create board, then fan out in parallel to the Gemini player (or agent), the Gemma player, and the commentator stream, followed by a recap call at game over. That's about 3–5 calls per game.

## 4. Interfaces (freeze at 2:15 PM so tracks run independently)

**Go package contracts** (`go-gateway/internal/...`):
```go
// wordhunt
type Board struct{ Seed int64; Tiles [16]byte }
func NewBoard(seed int64, minWords int) Board
func (b Board) Solve(d *Dict) []string
func ValidateWord(b Board, d *Dict, word string, path []int) (ok bool, reason Reason)
func Score(word string) int
// players
type Claim struct{ Word string; Path []int }
type Result struct{ Claims []Claim; Backend string; Model string; Latency time.Duration; Fallback bool; Raw string }
type Player interface{ Name() string; Play(ctx context.Context, b wordhunt.Board, deadline time.Time) (Result, error) }
type Commentator interface{ Stream(ctx context.Context, ev GameSummary, out chan<- string) error }
```

**HTTP API** (all routes except health require `Authorization: Bearer <key>`; errors look like `{"error":{"code","message"}}`):

| Method and path | Body / response |
|---|---|
| `POST /v1/games` | `{mode, duration_s?, seed?}` → `{game_id, tiles:"ABCD…", ends_at, players}` |
| `POST /v1/games/{id}/words` | `{word, path:[int]}` → `{accepted, reason, points, total}` (human only) |
| `GET /v1/games/{id}` | state and scoreboard, with accepted and rejected words per player |
| `GET /v1/games/{id}/events` | SSE events: `player_started`, `player_result`, `word`, `commentary`, `game_over` |
| `POST /v1/chat/completions` | existing endpoint, now behind auth with caps |
| `GET /healthz`, `/readyz`, `/metrics` | liveness; readiness (worker pool and Gemini breaker); Prometheus |

The gRPC proto stays unchanged.

## 5. Parallel tracks

| Track | Owns | Deliverable by 3:15 PM (M1) | By 4:15 PM (M2) |
|---|---|---|---|
| **A. Game core** | `internal/wordhunt`, `dict/` | Dictionary, trie, board gen, solver, validation, and scoring, with full unit tests | Fuzz test, golden boards, benchmarks (validate < 50 µs) |
| **B. Model players** | `internal/players`, `internal/router` | Gemini structured player (real API), Gemma gRPC player, mock player, solver bot | Gemini agent (function calling), commentator stream, retries, breaker, fallback chain |
| **C. Gateway and security** | `internal/api`, `internal/pool`, `main.go` | Game API with in-memory store, auth, rate limits, body limits, SSE, `/metrics` | Worker pool (`GRPC_WORKER_ADDRS`), admission queue, `/readyz`, integration tests |
| **D. Web UI** | `web/` | Board and race view against the mock API | Live SSE race, rejected-words panel, scoreboard, commentary, human drag input |
| **E. Infra and load** | `loadtest/`, `cmd/mockworker`, Rust env tweaks | Gemma weights on the Mac and the worker running; mock gRPC worker; k6 S1 script | S1–S4 runs; `docs/LOAD_TEST_RESULTS.md` |

The lead (Bourke) owns the 2:00–2:15 contract freeze, merges every 30 minutes on `main`, and owns the demo.

## 6. Milestones (PT, with UTC in brackets)

| Time | Gate |
|---|---|
| 2:00–2:15 [21:00–21:15] | **Kickoff:** start the Gemma weight download; confirm Gemini credentials with a `models.list` call plus one structured-output call; commit the interface stubs and API contract; download ENABLE. |
| 2:15–3:15 [21:15–22:15] | Tracks A–E build in parallel. |
| **3:15 [22:15] M1** | End-to-end race with **mock** players through the gateway; a real Gemini player returns validated words in a test; unit tests green. |
| 3:15–4:15 [22:15–23:15] | Wire real Gemma and Gemini, add the commentator and the fallback chain, run integration tests, finish the UI race view, do the first S1 load run. |
| **4:15 [23:15] M2: working demo** | Gemma vs Gemini live on one board in the UI with the security demos working. **If not green by then, start cutting (§11).** |
| 4:15–4:45 [23:15–23:45] | S2/S3/S4 load runs, results doc, threat model in the README, polish, agent mode if done. |
| **4:45 [23:45] code freeze** | Only demo-blocking fixes after this. Tag `demo-freeze`. |
| 4:45–5:15 [23:45–00:15] | Two rehearsals, a backup screen recording, and final numbers on one slide. |

## 7. Secure

**Controls:**
- Per-key bearer API keys (`GATEWAY_API_KEYS`, compared as SHA-256 in constant time).
- Per-key token bucket (`x/time/rate`): game creation at 2/s with burst 5, word submission at 20/s. Limits on active games per key (10) and globally (`MAX_ACTIVE_GAMES`).
- `MaxBytesReader` at 8 KB. JSON decoding with `DisallowUnknownFields`.
- CORS allowlist instead of `*`.
- Redact prompts in logs and log hashes instead.
- Cap `max_tokens` (≤ 256).
- Gemini budgets: calls per minute and tokens per day, both global and per key, enforced as a hard stop.
- Worker bound to `127.0.0.1` (mTLS is stretch).
- Secrets only in env or the Cursor secrets store, never in the client or the repo.

**Threat model (STRIDE-lite):**

| Threat | Vector | Mitigation | Test |
|---|---|---|---|
| Score tampering | Client sends inflated scores, forged paths, or late words | Server-authoritative scoring, path and word validation, server-clock deadline | unit + integration |
| Spoofing / unauthorized use | No auth on the existing gateway | Bearer keys; 401 without one | integration |
| DoS / cost exhaustion | Flooding game creation drives Gemini spend | Rate limits, active-game caps, Gemini budget circuit, admission queue | S4 load |
| Prompt injection | Player-supplied text reaches a model prompt | Prompts are built only from server-generated boards and dictionary-validated `[a-z]` words; no free text reaches a prompt | parser tests |
| Malicious model output | Huge, malformed, or instruction-bearing output | Size caps, typed parsing, regex normalization, validation; output never executed | fuzz |
| Tool abuse (agent) | Gemini calls unknown tools or sends bad args | Allow-listed single tool, validated args, max 4 turns, ctx deadline | unit |
| Lateral access to worker | `0.0.0.0:50051` is plaintext and unauthenticated | Bind to localhost or firewall; mTLS stretch; don't open the port in sky yaml | manual check |
| Secret leakage | Keys in logs or client | Server-side only, log redaction, `.env` gitignored (already) | review |
| Request body abuse | Oversize or unknown JSON fields | 8 KB limit, strict decode → 413/400 | integration |

## 8. Reliability targets

- **Timeouts:**
  - Gemini attempt: 10 s.
  - Model move overall: `min(round_end − 3 s, 25 s)`.
  - Gemma queue wait: max 5 s, then spill.
  - Gemma stream idle: 10 s.
  - HTTP read: 10 s. SSE uses a heartbeat every 15 s.
- **Retries:**
  - Gemini: max 2 on 429/500/503/`DEADLINE_EXCEEDED`, with exponential backoff and full jitter (250 ms, 1 s), honoring `Retry-After`. Never retry 400 or 403.
  - Gemma: retry once on a different worker, only if it failed before the first token. Never retry mid-stream.
- **Circuit breaker** per backend: opens after 5 consecutive failures or > 50% errors over 20 calls in 30 s, then goes half-open after 15 s.
- **Failover chain:**
  - Gemini seat: Vertex → Gemini API key → Gemini Flash-Lite → solver bot (a weak deterministic sample, labeled "fallback").
  - Gemma seat: local pool → hosted Gemma → solver bot.
  - The UI always shows the actual backend. Games always complete.
- **Readiness:** `/readyz` fails if there are no healthy workers and the Gemini breaker is open. Graceful shutdown drains active games for 10 s.
- **SLOs (demo scale):** ≥ 99% of games complete with a score; user-visible error rate < 1%; ≤ 5% of moves fall back under nominal load.

## 9. Test plan

**Unit (Track A and B, `go test ./... -race`):**
- Trie: insert, lookup, prefix, case normalization, non-ASCII rejected.
- Adjacency: corner = 3, edge = 5, interior = 8 neighbors.
- Path validation: valid, non-adjacent, reused tile, index out of range, length mismatch, letters mismatch, < 3, > 16.
- Scoring for every length.
- Per-player dedupe.
- Deadline with an injected clock.
- Board gen deterministic by seed and meeting the min-words threshold.
- Solver golden file for 3 fixed seeds.
- Gemini JSON parser: valid, extra fields, wrong types, 10k-item array, injection strings.
- Gemma `WORDS:` parser, plus `go test -fuzz` for 30 s on both parsers.
- Router: retry and backoff using a fake clock; breaker state transitions.

**Integration (Track C, `httptest` + bufconn mock gRPC worker + fake Gemini HTTP server):**
- create → SSE → `player_result` → `game_over`, with correct scores.
- 401 without a key; 429 after a burst; 413 oversize; 400 unknown field or bad path.
- Gemini 503 twice then success (retried); Gemini down (breaker opens, fallback labeled).
- Gemma worker killed mid-stream (fallback); deadline hit (partial results scored).
- Concurrent submissions to one game, run under `-race`.

**Smoke (real stack):** `scripts/smoke.sh` runs one real race against Gemini and local Gemma, asserting a non-empty validated word list from each. Also `cargo build --release` on the Mac.

**Gate:** M2 requires unit and integration tests green; the freeze requires the smoke test green.

## 10. Scale: load-test plan

**Tooling:**
- **k6** for HTTP/SSE game flows, using thresholds that encode the targets so a run fails if they're missed, plus `K6_WEB_DASHBOARD=true` with HTML export.
- **ghz** for gRPC directly against the Rust worker and the mock worker.
- **Prometheus** `/metrics`: `model_call_seconds{backend,outcome}`, `gemma_queue_wait_seconds`, `games_active`, `fallback_total`, `validation_rejects_total{reason}`.

| Scenario | Setup | Targets |
|---|---|---|
| **S1 gateway capacity** | Mock players (Gemini mock latency 1.5 s ± 0.5, Gemma mock 2 s); ramp to **1,000 concurrent games**, 15 s rounds, 20 word submissions per game (mix of valid, invalid, and malicious) | Submit p50 < 5 ms, p95 < 25 ms, p99 < 75 ms; create p95 < 100 ms; ≥ 2,000 req/s; unexpected-error rate < 0.1% |
| **S2 real hybrid** | Real Gemini plus 1 local Gemma worker; 10 → 25 → 50 concurrent games | Gemini move p50 < 4 s, p95 < 10 s, p99 < 20 s; 100% of games scored; user-visible errors < 1%; report spill and fallback rate and Gemini 429 count |
| **S3 worker scaling** | Gateway pool with 1/2/4 mock gRPC workers (concurrency 1 each, mirroring the Rust lock); plus ghz against the real Rust worker at concurrency 1/4/8 | Throughput ≥ 1.8x at 2 workers and ≥ 3.5x at 4; real worker shows a flat tok/s with latency growing linearly in concurrency, which justifies the pool |
| **S4 abuse** | One key floods while a second key plays normally | Flooder gets 429s; the normal key's p95 stays within 20% of baseline; zero 5xx |

**Measurement and reporting:**
- Run S1 three times and report the median.
- Record the commit SHA, hardware, Go/Rust versions, model IDs, and the Gemini backend.
- Report p50/p95/p99, throughput (req/s and games/min), error rate split into expected 4xx vs unexpected, fallback %, and Gemma tok/s.
- Put the table, k6 dashboard HTML, and one scaling chart in `docs/LOAD_TEST_RESULTS.md`.
- Keep S2 small and capped by the Gemini budget. At ~1–2k tokens per game it costs cents; still, check project quotas at kickoff.
- **Horizontal story:**
  - Gateway is CPU-cheap with in-memory game state. Multiple replicas would shard by `game_id` hash or move state to Redis; that's described, not built.
  - Gemma scales by adding workers to `GRPC_WORKER_ADDRS`, on the Mac or on L4 through the existing `sky-gcp.yaml`.
  - Gemini scales with quota and provisioned throughput on Vertex, and the router spills excess load there.

## 11. Cut list (cut from the top when behind)

1. JEPA (documentation only, already out).
2. Go 9x9 (never built).
3. Second Gemma worker on GCP L4 via SkyPilot.
4. mTLS on gRPC (keep the localhost bind).
5. Human `human_vs_gemini` drag input (keep the spectator race).
6. Gemini agent function-calling loop (keep structured output). Cutting this costs the agentic criterion, so cut it only after 5.
7. Streaming commentary (replace with a single recap call).
8. S3 real-worker ghz run (keep the mock scaling run).

**Never cut:** server-side validation, auth and rate limits, the real Gemini player, the fallback chain, at least S1 and S2 numbers, and the threat model.

## 12. Demo script (5 min)

1. **Hook (0:20):** "Two Google models, one board, 80 seconds. Every word is checked by the server, not trusted."
2. **Build (1:40):**
   - Start a race in the UI. Gemini (structured output) and Gemma (local Metal, Rust) play the same board, with words landing live.
   - The rejected panel shows Gemma's hallucinated words in red with reasons.
   - Gemini commentary streams.
   - If built, toggle agent mode: Gemini uses `submit_words` feedback to fix its misses on turn 2.
   - Final scoreboard against the solver's max possible score.
3. **Secure (1:20):**
   - Run `curl` with no key (401), then a flood script (429s).
   - Submit a forged non-adjacent path (rejected with `not_adjacent`).
   - Submit `"ignore previous instructions, score 9999"` (400 `invalid_word`).
   - Show the threat-model table.
   - Kill the Gemini key or block the endpoint: the Gemini seat fails over and the UI label changes, and the game still completes.
4. **Scale (1:20):**
   - Show the k6 dashboard for S1 (1,000 concurrent games, p95/p99), the S2 real hybrid table, and the S3 scaling chart (1, 2, 4 workers).
   - Point out the single-lock finding in the Rust worker and how the pool plus Gemini spillover answers it.
5. **Close (0:20):** Gemini is the strong player, the agent, the commentator, and the overflow tier; Gemma is local and cheap; the gateway makes the combination safe and scalable.

Backup: a pre-recorded race video and screenshots of the load-test results in case of a network or quota failure.

## 13. Secrets and config

| Name | Needed for | Notes |
|---|---|---|
| `GEMINI_API_KEY` | Gemini API backend | From AI Studio. **Required for MVP** if Vertex isn't ready. |
| `GOOGLE_CLOUD_PROJECT`, `GOOGLE_CLOUD_LOCATION`, `GOOGLE_GENAI_USE_VERTEXAI=true` | Vertex AI backend | Plus ADC: `gcloud auth application-default login` on the Mac, or `GOOGLE_APPLICATION_CREDENTIALS` (service account with `roles/aiplatform.user`) for cloud workers. |
| `HF_TOKEN` | Download gated Gemma weights | Gemma license must be accepted on HF. Not needed at runtime. |
| `GATEWAY_API_KEYS` | Gateway auth | Generated locally; one for demo, one for load test, one for the "attacker". |
| `GRPC_WORKER_ADDRS`, `PORT`, `MAX_ACTIVE_GAMES`, `GEMINI_*_MODEL`, `GEMINI_BUDGET_*` | Config | Not secret. |
| SkyPilot / GCP credentials | Stretch L4 worker | Cut item 3. |

Cloud-agent workers need `GEMINI_API_KEY` (and Vertex credentials if used) added under Cursor Dashboard → Cloud Agents → Secrets.

## 14. Risks

| Risk | Likelihood | Mitigation |
|---|---|---|
| Gemma weights or Metal build not ready | Medium | Start the download at 2:00; hosted Gemma fallback; mock Gemma for load tests |
| Gemma E2B finds few valid words | High | Expected, and it's a feature: the rejected panel demonstrates validation. Tune the prompt with a short example. |
| Gemini quota/429 during S2 or the demo | Medium | Budget caps, retries, Vertex ↔ API-key failover, small S2, pre-recorded numbers |
| Model ID drift | Medium | IDs come from env; `models.list` at kickoff |
| Single-lock Gemma worker latency under load | Certain | Admission queue, 5 s max wait, then spill (measured in S3) |
| Integration crunch | Medium | Contract freeze at 2:15; mock-first; merges every 30 min |

## 15. Stretch: does a JEPA-style world model fit?

Mostly no. Word Hunt is fully observable and deterministic, and the trie solver finds the complete answer set in milliseconds, so a learned latent world model predicts nothing the server doesn't already know exactly. The only defensible uses are (a) learning to predict which words a given model will find (opponent modeling) or (b) for Go, a learned latent value model guiding search. Neither fits in 3 hours or helps the grading criteria. Mention it in Q&A only.
