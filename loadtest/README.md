# Load tests

Prerequisites:

```bash
brew install k6 ghz
```

Download gated Gemma weights without exposing the HF token in process
arguments (the token is read from the environment, HF cache, or main-checkout
`.env`):

```bash
DOWNLOAD_PARTS=128 ./loadtest/download-gemma.sh
```

Start a deterministic worker from `go-gateway/`:

```bash
MOCK_WORKER_ADDR=127.0.0.1:50061 \
MOCK_WORKER_DELAY=2s MOCK_WORKER_JITTER=500ms \
go run ./cmd/mockworker
```

The mock defaults to one generation at a time, matching the Rust worker's
single `Mutex<WorkerState>`. Increase `MOCK_WORKER_CONCURRENCY` only when
testing a different worker design.

Run the scenarios:

```bash
# S1: mock gateway capacity
k6 run -e BASE_URL=http://127.0.0.1:8787 \
  -e API_KEYS=key1,key2 -e VUS=1000 loadtest/s1-gateway.js

# S2: budget-capped real Gemma + Gemini
k6 run -e BASE_URL=http://127.0.0.1:8787 \
  -e API_KEY=key1 loadtest/s2-real-hybrid.js

# S3: 1/2/4 deterministic workers, then the real worker at c=1/4/8
./loadtest/run-s3-mock-scaling.sh
REAL_WORKER_ADDR=127.0.0.1:50051 CALLS=10 ./loadtest/run-s3-ghz.sh

# S4: rate-limit isolation between two keys
k6 run -e FLOOD_KEY=attacker -e NORMAL_KEY=demo loadtest/s4-abuse.js
```

S1's default 1,000 VUs is intentionally aggressive. Smoke it first with
`VUS=10 RAMP=2s HOLD=5s`. S1 and S2 assign one distinct configured key per VU;
put the same comma-separated list in `GATEWAY_API_KEYS` and `API_KEYS`.
Otherwise the gateway's 2/s (burst 5) creation limit and 10-active-game cap
measure per-key admission rather than gateway capacity.

Use `K6_WEB_DASHBOARD=true K6_WEB_DASHBOARD_REPORT=loadtest/results/s1.html`
to retain the dashboard. Raw outputs under `loadtest/results/` are ignored so
machine-specific runs are not accidentally committed.
