#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
OUT="${OUT_DIR:-$ROOT/loadtest/results}"
CALLS_PER_WORKER="${CALLS_PER_WORKER:-20}"
DELAY="${MOCK_WORKER_DELAY:-200ms}"
mkdir -p "$OUT"

command -v ghz >/dev/null || { echo "ghz is required (brew install ghz)" >&2; exit 1; }
(cd "$ROOT/go-gateway" && go build -o /tmp/wordhunt-mockworker ./cmd/mockworker)

pids=()
cleanup() {
  if ((${#pids[@]})); then kill "${pids[@]}" 2>/dev/null || true; fi
}
trap cleanup EXIT
echo "workers,total_calls,elapsed_seconds,requests_per_second" > "$OUT/s3-mock-scaling.csv"

for workers in 1 2 4; do
  pids=()
  for ((i=0; i<workers; i++)); do
    port=$((50061+i))
    MOCK_WORKER_ADDR="127.0.0.1:$port" MOCK_WORKER_DELAY="$DELAY" \
      MOCK_WORKER_JITTER=0s MOCK_WORKER_CONCURRENCY=1 \
      /tmp/wordhunt-mockworker >"$OUT/mockworker-$port.log" 2>&1 &
    pids+=("$!")
  done

  for ((i=0; i<workers; i++)); do
    port=$((50061+i))
    for _ in {1..50}; do nc -z 127.0.0.1 "$port" && break; sleep 0.1; done
  done

  start="$(python3 -c 'import time; print(time.monotonic())')"
  ghz_pids=()
  for ((i=0; i<workers; i++)); do
    port=$((50061+i))
    ghz --insecure --proto "$ROOT/proto/inference.proto" \
      --call inference.InferenceService.StreamGenerate \
      --data-file "$ROOT/loadtest/ghz-request.json" \
      --total "$CALLS_PER_WORKER" --concurrency 8 --format json \
      --output "$OUT/s3-mock-w${workers}-node${i}.json" "127.0.0.1:$port" &
    ghz_pids+=("$!")
  done
  for pid in "${ghz_pids[@]}"; do wait "$pid"; done
  end="$(python3 -c 'import time; print(time.monotonic())')"
  total=$((workers*CALLS_PER_WORKER))
  python3 - "$workers" "$total" "$start" "$end" >> "$OUT/s3-mock-scaling.csv" <<'PY'
import sys
workers, total = map(int, sys.argv[1:3])
elapsed = float(sys.argv[4]) - float(sys.argv[3])
print(f"{workers},{total},{elapsed:.6f},{total / elapsed:.3f}")
PY
  kill "${pids[@]}" 2>/dev/null || true
  wait "${pids[@]}" 2>/dev/null || true
  pids=()
done

cat "$OUT/s3-mock-scaling.csv"
