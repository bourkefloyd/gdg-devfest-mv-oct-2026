#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
OUT="${OUT_DIR:-$ROOT/loadtest/results}"
RUNS="${RUNS:-3}"
mkdir -p "$OUT"

for ((i=1; i<=RUNS; i++)); do
  echo "S1 run $i/$RUNS"
  K6_WEB_DASHBOARD=true \
  K6_WEB_DASHBOARD_EXPORT="$OUT/s1-run-$i.html" \
    k6 run --summary-export "$OUT/s1-run-$i.json" \
    "$ROOT/loadtest/s1-gateway.js"
done

python3 - "$OUT" "$RUNS" <<'PY'
import json
import statistics
import sys
from pathlib import Path

out, count = Path(sys.argv[1]), int(sys.argv[2])
runs = [json.loads((out / f"s1-run-{i}.json").read_text()) for i in range(1, count + 1)]
wanted = {
    "game_create_duration": ("med", "p(95)", "p(99)"),
    "word_submit_duration": ("med", "p(95)", "p(99)"),
    "http_reqs": ("rate", "count"),
    "unexpected_error": ("rate", "value"),
    "expected_4xx": ("count", "rate"),
}
result = {}
for metric, fields in wanted.items():
    values = []
    for run in runs:
        entry = run.get("metrics", {}).get(metric, {})
        values.append(entry.get("values", entry))
    result[metric] = {
        field: statistics.median(v[field] for v in values if field in v)
        for field in fields
        if any(field in v for v in values)
    }
(out / "s1-median.json").write_text(json.dumps(result, indent=2) + "\n")
print(json.dumps(result, indent=2))
PY
