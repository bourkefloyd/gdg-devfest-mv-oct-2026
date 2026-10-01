# Load test results

Status: in progress. S1 and mock-worker scaling are green. Real-model rows will
be filled after the Gemma weights finish downloading.

## Test environment

- Load harness: `4831a79` (`track-e`, latest `main` merged)
- Gateway: `df29dec` (`main`, complete Go and Rust builds green)
- Host: MacBook Pro (Mac17,7), Apple M5 Max, 64 GB RAM
- Go: 1.27.1 (`darwin/arm64`)
- Rust: 1.99.0
- k6: 2.3.0
- ghz: 0.121.0
- Gemma: `google/gemma-4-E2B-it`, local Metal worker
- Gemini backend/model: pending S2 integration

## Results

| Scenario | Load | Throughput | Latency / errors | Result |
|---|---:|---:|---|---|
| S1 gateway capacity | 1,000 VUs, 21,000 requests/run | 2,799 req/s median; ~8,000 games/min | create p50/p95/p99 0.284/0.623/1.069 ms; submit 0.127/0.341/0.707 ms; 0 unexpected errors | pass |
| S2 preflight | real Gemini + 1 serialized mock Gemma worker; 10→25→50 VUs | 50 games; 100% scored; 0 visible errors | Gemini p50/p95/p99 4.362/7.385/7.885 s; game p50/p95 8.535/9.793 s; 54% move fallback | partial: p50 missed |
| S2 real hybrid | real Gemini + real Metal Gemma | — | — | pending weights |
| S3 mock worker pool | 1 worker, 40 calls | 9.82 req/s | 4.072 s wall time | baseline |
| S3 mock worker pool | 2 workers, 80 calls | 19.65 req/s | 4.071 s wall time | 2.00x baseline |
| S3 mock worker pool | 4 workers, 160 calls | 39.31 req/s | 4.070 s wall time | 4.00x baseline |
| S3 real Metal worker | c=1/4/8 | — | — | pending weights |
| S4 abuse isolation | 200 req/s attacker + 1 req/s normal key | 201 req/s aggregate | 99.0% attacker 429; normal p95 0.378 ms vs 0.637 ms baseline; zero 5xx | pass |

```mermaid
xychart-beta
    title "S3 mock pool throughput"
    x-axis "Workers" [1, 2, 4]
    y-axis "Requests per second" 0 --> 45
    line [9.82, 19.65, 39.31]
```

## S1 notes

S1 ran three times against the pushed Track C gateway candidate with
`PLAYERS=mock`, 2 s mock latency, 1,000 distinct configured API keys, and one
game per VU. The table reports the median of the three runs. Each run admitted
1,000 games and issued 20 mixed word submissions per game. Of 21,000 requests,
4,000 (19.05%) were expected 400 responses for malformed or malicious inputs;
unexpected errors were zero. All encoded thresholds passed by wide margins.

The 2 s ramp, 3 s hold, and 2 s ramp-down intentionally put all 1,000 games in
flight while measuring admission and validation throughput. Game completion is
measured separately in S2 rather than folded into S1 request latency.

## S4 notes

The normal key's create p95 improved from 0.637 ms at baseline to 0.378 ms
while a separate key flooded at 200 requests/s, comfortably within the target
of no more than a 20% regression. The attacker received 429 on 1,980 of 2,000
requests (99.0%), while the normal player continued at 1 game/s. No request
returned 5xx.

## S2 preflight notes

Before the 10.2 GB Gemma weight transfer completed, S2 used the real
`gemini-3.8-flash` API and one mock gRPC worker configured with concurrency 1,
2 s latency, and ±0.5 s jitter. This exercises the real Gemini path and the
same worker-pool queue/spill behavior without claiming Metal inference numbers.

All 50 games completed with scores and no user-visible errors. Gemini met its
p95 and p99 targets but missed p50 by 0.362 s. Fifty-four of 100 player moves
fell back under the burst. Cumulative Prometheus labels showed fallback reaching
both seats (`solver` for Gemini, `gemini-api`/`solver` for Gemma), confirming
that the completion SLO held even while the nominal fallback target did not.
Backend-specific Gemini 429s are not exposed separately by the current metrics,
so this report does not infer a 429 count from fallback events.

## Interpretation

The mock service intentionally defaults to one concurrent generation, matching
the Rust worker's single `Mutex<WorkerState>`. Separate workers scale nearly
linearly because they have independent queues. This supports the gateway design:
add addresses to `GRPC_WORKER_ADDRS`, bound queue wait to five seconds, then
spill excess work to Gemini rather than increasing concurrency inside one
worker.

The Rust worker now binds `127.0.0.1:50051` by default. `GRPC_LISTEN_ADDR`
provides an explicit override, and `GEMMA_MODEL_DIR` lets the isolated worktree
use weights stored in the main checkout without copying them.

## Commands

```bash
# Validate JavaScript and shell syntax
k6 inspect loadtest/s1-gateway.js
k6 inspect loadtest/s2-real-hybrid.js
k6 inspect loadtest/s4-abuse.js
bash -n loadtest/*.sh

# Mock scaling
CALLS_PER_WORKER=40 MOCK_WORKER_DELAY=100ms \
  ./loadtest/run-s3-mock-scaling.sh

# Real Metal worker, after weights load
REAL_WORKER_ADDR=127.0.0.1:50051 CALLS=10 \
  ./loadtest/run-s3-ghz.sh
```

Raw ghz JSON, k6 summaries, the S1 HTML dashboard, and the final scaling CSV are
generated under gitignored `loadtest/results/`.
