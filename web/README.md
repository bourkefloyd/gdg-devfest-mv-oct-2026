# Word Hunt Arena web UI

Projector-ready Vite + React + TypeScript client for the Word Hunt gateway.
The default view is the 1–100 game load arena; the Gemma-vs-Gemini race is
available from the **Head-to-head** tab.

## Run

```bash
npm install
npm run dev
```

The app serves on `http://127.0.0.1:4317` and proxies `/api`, `/v1`,
`/healthz`, and `/readyz` to the Go gateway on `http://127.0.0.1:8787`.

Set `VITE_GATEWAY_API_KEY` for the gateway bearer token. If the gateway is
unavailable, the UI automatically switches to a deterministic in-browser mock
that exercises the same race events. Set `VITE_MOCK_API=true` to force it.

The load arena uses reconnecting `EventSource` streams. The authenticated
head-to-head stream uses `fetch` so it can send the gateway bearer token.
