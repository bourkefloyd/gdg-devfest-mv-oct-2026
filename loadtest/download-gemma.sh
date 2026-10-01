#!/usr/bin/env bash
set -euo pipefail

REPO="${HF_REPO:-google/gemma-4-E2B-it}"
FILENAME="${HF_FILENAME:-model.safetensors}"
DEST="${GEMMA_MODEL_DIR:-$HOME/projects/oxidizinggemma/models/gemma-4-e2b}"
PARTS="${DOWNLOAD_PARTS:-16}"
EXPECTED_SHA256="${EXPECTED_SHA256:-2db5482b20d746879bb3ef79b5203e9075a2e2b98f54ec7c2f281c1477ddc550}"
URL="https://huggingface.co/$REPO/resolve/main/$FILENAME"

if [[ -n "${HF_TOKEN:-}" ]]; then
  token="$HF_TOKEN"
elif [[ -f "$HOME/.cache/huggingface/token" ]]; then
  token="$(<"$HOME/.cache/huggingface/token")"
elif [[ -f "$HOME/projects/oxidizinggemma/.env" ]]; then
  token="$(awk -F= '$1=="HF_TOKEN" {sub(/^HF_TOKEN=/, ""); print; exit}' \
    "$HOME/projects/oxidizinggemma/.env")"
else
  echo "HF_TOKEN is not available" >&2
  exit 2
fi
[[ -n "$token" ]] || { echo "HF_TOKEN is empty" >&2; exit 2; }

mkdir -p "$DEST/.parts"
headers="$(mktemp)"
trap 'rm -f "$headers"' EXIT
curl --fail --silent --show-error --location \
  -H "Authorization: Bearer $token" -r 0-0 -D "$headers" -o /dev/null "$URL"
total="$(awk 'BEGIN{IGNORECASE=1} /^content-range:/ {print $3}' "$headers" |
  tail -1 | tr -d '\r' | cut -d/ -f2)"
[[ "$total" =~ ^[0-9]+$ ]] || { echo "could not determine model size" >&2; exit 1; }
chunk=$(((total + PARTS - 1) / PARTS))
echo "Downloading $total bytes in $PARTS resumable parts"

pids=()
for ((i=0; i<PARTS; i++)); do
  start=$((i * chunk))
  end=$((start + chunk - 1))
  ((end >= total)) && end=$((total - 1))
  part="$(printf '%s/.parts/%s.part.%03d' "$DEST" "$FILENAME" "$i")"
  existing=0
  [[ -f "$part" ]] && existing="$(stat -f %z "$part")"
  expected=$((end - start + 1))
  if ((existing == expected)); then
    continue
  fi
  ((existing < expected)) || { echo "$part is oversized" >&2; exit 1; }
  range_start=$((start + existing))
  (
    curl --fail --silent --show-error --location \
      --retry 20 --retry-all-errors --connect-timeout 20 \
      --speed-limit 1024 --speed-time 60 \
      -H "Authorization: Bearer $token" \
      -r "$range_start-$end" "$URL" >> "$part"
    actual="$(stat -f %z "$part")"
    [[ "$actual" -eq "$expected" ]] ||
      { echo "$part: expected $expected bytes, got $actual" >&2; exit 1; }
  ) &
  pids+=("$!")
done

failed=0
for pid in "${pids[@]}"; do wait "$pid" || failed=1; done
((failed == 0)) || exit 1

tmp="$DEST/$FILENAME.assembling"
: > "$tmp"
for ((i=0; i<PARTS; i++)); do
  part="$(printf '%s/.parts/%s.part.%03d' "$DEST" "$FILENAME" "$i")"
  cat "$part" >> "$tmp"
done
actual_sha="$(shasum -a 256 "$tmp" | awk '{print $1}')"
[[ "$actual_sha" == "$EXPECTED_SHA256" ]] ||
  { echo "SHA-256 mismatch: $actual_sha" >&2; exit 1; }
mv "$tmp" "$DEST/$FILENAME"
echo "Verified $DEST/$FILENAME ($actual_sha)"
