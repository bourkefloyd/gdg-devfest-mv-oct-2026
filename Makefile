ARENA_PORT ?= 8787

.PHONY: arena arena-build arena-test

arena-build:
	cd web && npm ci --omit=optional && npm run build

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
	cd web && npm ci --omit=optional && npm run lint && npm run test && npm run build
