#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
PROTO="$ROOT/proto/inference.proto"
PAYLOAD="$ROOT/loadtest/ghz-request.json"
OUT="${OUT_DIR:-$ROOT/loadtest/results}"
CALLS="${CALLS:-20}"
mkdir -p "$OUT"

command -v ghz >/dev/null || {
  echo "ghz is required (brew install ghz)" >&2
  exit 1
}

target="${REAL_WORKER_ADDR:-127.0.0.1:50051}"
for concurrency in 1 4 8; do
  echo "real worker: concurrency=$concurrency"
  ghz --insecure --proto "$PROTO" \
    --call inference.InferenceService.StreamGenerate \
    --data-file "$PAYLOAD" --total "$CALLS" --concurrency "$concurrency" \
    --format json --output "$OUT/s3-real-c${concurrency}.json" "$target"
done

echo "Wrote real-worker results to $OUT"
echo "For mock pool scaling, start 1, 2, then 4 mock workers and run:"
echo "  GRPC_WORKER_ADDRS=127.0.0.1:50061,... <gateway>"
echo "  k6 run -e BASE_URL=http://127.0.0.1:8787 -e VUS=100 loadtest/s1-gateway.js"
