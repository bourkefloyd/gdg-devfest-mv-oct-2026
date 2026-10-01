# Load test results

Status: in progress. S1 and mock-worker scaling are green. Real-model rows will
be filled after the Gemma weights finish downloading.

## Test environment

- Load harness: `631d683` (`track-e`)
- S1 gateway candidate: `3e5cccf` (`track-c`, tests green before the run)
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
| S1 gateway capacity | 1,000 VUs, 21,000 requests/run | 2,798 req/s median; ~8,000 games/min | create p50/p95/p99 0.279/0.598/1.073 ms; submit 0.125/0.333/0.671 ms; 0 unexpected errors | pass |
| S2 real hybrid | pending model integration | — | — | pending |
| S3 mock worker pool | 1 worker, 40 calls | 9.82 req/s | 4.072 s wall time | baseline |
| S3 mock worker pool | 2 workers, 80 calls | 19.65 req/s | 4.071 s wall time | 2.00x baseline |
| S3 mock worker pool | 4 workers, 160 calls | 39.31 req/s | 4.070 s wall time | 4.00x baseline |
| S3 real Metal worker | c=1/4/8 | — | — | pending weights |
| S4 abuse isolation | pending game API | — | — | pending |

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
