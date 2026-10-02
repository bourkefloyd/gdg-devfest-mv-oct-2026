ARENA_PORT ?= 8787
DEV_PORT ?= 8080

.PHONY: setup dev setup-local-model local-model arena arena-build arena-test

# Download Go modules and npm packages. Creates .env from .env.example when missing.
setup:
	cd go-gateway && go mod download
	cd web && npm ci
	@if [ ! -f .env ]; then cp .env.example .env; echo "Created .env from .env.example. Set GEMINI_API_KEY."; fi

# Gateway on 127.0.0.1:$(DEV_PORT) and the Vite app on :4317.
# Leaves :8787 free for the optional local DiffusionGemma server.
dev:
	@set -a; [ -f .env ] && . ./.env; set +a; \
	if [ -z "$${GEMINI_API_KEY:-}" ]; then echo "warning: GEMINI_API_KEY is empty in .env"; fi; \
	( cd go-gateway && HOST=127.0.0.1 PORT=$(DEV_PORT) PLAYERS=$${PLAYERS:-real} GATEWAY_API_KEYS=$${GATEWAY_API_KEYS:-dev-local} go run . ) & \
	gw=$$!; trap 'kill $$gw 2>/dev/null || true' EXIT INT TERM; \
	cd web && VITE_API_BASE=http://127.0.0.1:$(DEV_PORT) npm run dev

# Apple Silicon only. About 15 GB of weights download on first model start.
setup-local-model:
	python3 -m venv local-model/.venv
	local-model/.venv/bin/pip install -U pip
	local-model/.venv/bin/pip install -r local-model/requirements.txt

local-model:
	./local-model/run_arena.sh

arena-build:
	cd web && npm ci && npm run build

arena: arena-build
	cd go-gateway && \
		HOST=0.0.0.0 \
		PORT=$(ARENA_PORT) \
		PLAYERS=mock \
		GATEWAY_API_KEYS=arena-local \
		ARENA_STATIC_DIR=../web/dist \
		go run .

arena-test:
	cd go-gateway && go test -race ./internal/arena
	cd web && npm ci && npm run lint && npm run test && npm run build
