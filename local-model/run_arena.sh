#!/bin/zsh
cd "$(dirname "$0")" || exit 1
if [[ ! -x .venv/bin/python ]]; then
  echo "Run make setup-local-model first" >&2
  exit 1
fi
exec .venv/bin/python -u local_arena.py
