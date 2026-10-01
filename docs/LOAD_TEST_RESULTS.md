# Load test results

Status: in progress. The mock-worker benchmark is green; gateway and real-model
rows will be filled after the other tracks land the game API and the Gemma
weights finish downloading.

## Test environment

- Commit: `4893f35` (`track-e`)
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
| S1 gateway capacity | pending game API | — | — | pending |
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
