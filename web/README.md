# Word Hunt Arena web UI

Projector-ready Vite + React + TypeScript client for the Word Hunt gateway.

## Run

```bash
npm install
npm run dev
```

The app serves on `http://127.0.0.1:4317` and proxies `/v1`, `/healthz`, and
`/readyz` to the Go gateway on `http://127.0.0.1:8787`.

Set `VITE_GATEWAY_API_KEY` for the gateway bearer token. If the gateway is
unavailable, the UI automatically switches to a deterministic in-browser mock
that exercises the same race events. Set `VITE_MOCK_API=true` to force it.

The live event stream uses `fetch` rather than `EventSource`, because the game
SSE endpoint requires an `Authorization` header.
