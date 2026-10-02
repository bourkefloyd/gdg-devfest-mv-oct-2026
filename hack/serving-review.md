# Serving review: instructor slides vs. our Word Hunt stack

Sources: `bourkefloyd/oxidizinggemma` `main` @ `8ff4ec7` (`TEACHERS_SLIDES.md`, `SLIDES.md`, `hack/hack-plan.md`, `hack/gcp.md`, `go-gateway/`, `src/main.rs`, `Cargo.toml`, `sky-gcp.yaml`). Written 3:55 PM PT. Working demo is due at 4:15, code freeze at 4:45, and judging at 5:15.

## 1. What the slides recommend

**The slides say nothing about Cloud Run, Cloud Run GPUs, concurrency, min instances, cold starts, batching, regions, quotas, Gemini, or Vertex.** Here is all the serving guidance they do contain:

- **Load weights with zero-copy mmap** (Slide 5; SLIDES "Safetensors and POSIX mmap"): *"`VarBuilder::from_mmaped_safetensors` … Startup Time: Reduced from 25+ seconds to < 1.5 seconds."*
- **Use a three-tier split** (Slide 9): *"Rust: raw compute … gRPC/Protobuf … port 50051. Go Gateway … SSE streaming on port 8080."* The stated reason is *"zero-downtime gateway restarts, independent autoscaling, and lightweight goroutine connection pooling."*
- **The gateway is cheap** (Slide 10): *"Handles 10,000+ open HTTP SSE client streams … < 20MB of RAM."*
- **Decode is bandwidth-bound** (Slide 8): *"Autoregressive LLMs are memory-bandwidth bound,"* and KV-cache sharing saves 57% of KV VRAM.
- **Deploy with SkyPilot on L4, A100, or H100** (Slide 12): `sky launch -c gemma4-gcp sky-gcp.yaml --yes`.
- **Benchmarks** (SLIDES "Hardware Benchmarks"): L4 FP16 at 32 ms TTFT and 68.4 tok/s; M3 Max at 45.2 tok/s. Our plan uses the README/BUILDERS_LOG figures instead: 4–6 tok/s on M3 Metal and 30–43 tok/s on H100. **Don't quote the slide numbers as our measurements.**

The slides never mention batching. The worker serves one request at a time and does no batching.

## 2. Where our setup matches

- **Three tiers and the gRPC contract:** the proto is unchanged, and the Go gateway calls `StreamGenerate` on the Rust worker at `:50051`. This matches Slide 9.
- **Independent scaling of the Gemma tier:** `internal/pool` does least-outstanding routing, a bounded queue (`GEMMA_MAX_QUEUE=64`), a wait limit (`GEMMA_MAX_WAIT_MS=5000`), and a failure cooldown. `GEMMA_PER_WORKER=1` matches the single `tokio::sync::Mutex` in `src/main.rs:54,82`, which is the right setting.
- **Gemini overflow:** Gemma's seat spills to Gemini with a "fallback" label (`seats.go` `labeledFallback`). This is the honest answer to having no batching, and it goes beyond what the slides cover.
- **Gemini/Vertex access:** the runtime service account has only `aiplatform.user` and the secret is in Secret Manager (`hack/gcp.md`). `players/config.go` chooses Vertex when `GOOGLE_GENAI_USE_VERTEXAI=true`. Cloud Build and Artifact Registry are ready.
- **mmap weight loading** is already in the Rust worker. Nothing to do.

## 3. Gaps and wrong settings, ranked by impact on the 5:15 demo

| # | Gap | Impact | Exact fix |
|---|---|---|---|
| 1 | **No Dockerfile exists.** `gcp.md` says not to deploy without one. | Cloud Run deploy is blocked. | Add a two-stage Go build: `golang:1.23` builds `go-gateway`, then `gcr.io/distroless/static`. Run `CGO_ENABLED=0 go build -o /gateway .` in `go-gateway/`. Deploy from that directory with `gcloud builds submit go-gateway --tag=$IMAGE`. Gateway only; skip the Rust worker. |
| 2 | **The gateway binds to `127.0.0.1`** (`main.go:98`, `HOST` defaults to `127.0.0.1`). Cloud Run injects `PORT=8080`, but the listener would be on loopback. | The revision fails its startup probe and never serves. | `--set-env-vars=HOST=0.0.0.0` (or change the default in `main.go:98`). Keep `--port=8080`. |
| 3 | **`GATEWAY_API_KEYS` is required** (`main.go:48-52` exits without it) and isn't in the `gcp.md` deploy command. | The container exits on boot. | Create a secret `gateway-api-keys` with the comma-separated keys, then add `--set-secrets=GATEWAY_API_KEYS=gateway-api-keys:latest`. Grant the runtime service account `secretAccessor` on it. |
| 4 | **The game store is in memory, but `gcp.md` sets `--max-instances=20 --concurrency=40`.** A game created on one instance gets its `/events` and `/words` requests on another and returns 404. SSE connections hold concurrency slots, so 40 per instance forces early scale-out. | Load-test errors and broken races as soon as a second instance starts. | `--min-instances=1 --max-instances=1 --concurrency=1000 --cpu=4 --memory=2Gi`. Add `--session-affinity` too, but it's best-effort, so it doesn't fix this by itself. The plan already describes sharding by `game_id` hash as unbuilt work; say that on the slide. |
| 5 | **Players run in background goroutines after `POST /v1/games` returns** (`internal/api/games.go:201-205`, commentator at `:252`). With default request-based billing, Cloud Run throttles CPU outside active requests. | Moves and commentary stall or finish late whenever no SSE stream is open, and k6 runs that only POST will look slow. | `--no-cpu-throttling` (instance-based billing). Keep `--timeout=300`, which exceeds the 80 s round. |
| 6 | **Cloud Run reserves some URL paths ending in `z`**, and `/healthz` is commonly intercepted by Google's front end. | Health and readiness checks against the Cloud Run URL may 404 during the demo. They don't break deploy, because the default startup probe is TCP. | Check with `curl $URL/healthz` and `curl $URL/readyz`. If either returns Google's 404, add `open("GET /health", s.handleHealthz)` and `open("GET /ready", s.handleReadyz)` at `internal/api/server.go:148`. Don't configure HTTP probes on the `z` paths. |
| 7 | **CORS defaults to localhost** (`main.go:74`), and the web app's Vite proxy is hardcoded to `127.0.0.1:8787` (`web/vite.config.ts:11-15`). | A browser can't reach the Cloud Run gateway. | **Keep the live UI demo local** (Mac gateway, Mac Metal worker). Use Cloud Run only as the scale and load-test target with k6 and curl. If the UI must point at Cloud Run, make the proxy target `process.env.GATEWAY_URL ?? "http://127.0.0.1:8787"` and set `CORS_ALLOWED_ORIGINS` to the UI origin. |
| 8 | **The default Gemini model ID was never verified on Vertex `us-central1`.** The code defaults to `gemini-3.8-flash` (`model_players.go:119`, `agent.go:25`) and `gemini-3.1-flash-lite` (`commentator.go:20`). The only Vertex smoke test in `gcp.md` used `gemini-2.5-flash`. Newer models are often served only from the `global` location. | Every Gemini seat drops to the solver bot, and the demo shows "fallback" for Gemini. | Run the `gcp.md` curl smoke test with each ID in `us-central1`. If one fails, either set `GOOGLE_CLOUD_LOCATION=global`, or pin the IDs: `--set-env-vars=GEMINI_PLAYER_MODEL=<verified>,GEMINI_FALLBACK_MODEL=<verified lite>,GEMINI_COMMENTATOR_MODEL=<verified lite>`. Set them explicitly either way. |
| 9 | **Two Gemini backends are configured at once.** `gcp.md` sets both `GEMINI_API_KEY` and `GOOGLE_GENAI_USE_VERTEXAI=true`. `config.go` then uses Vertex only, and the key is ignored. The plan's "Vertex → API key" failover is not wired in `seats.go`, which builds one client. | Low for the demo. Don't claim "Vertex ↔ API key failover" on stage. | Leave it as is. Say "Vertex primary, Flash-Lite, then solver bot" instead. |
| 10 | **Gemma on Cloud Run:** Cloud Run can't reach the Mac's worker, and `GRPC_WORKER_ADDRS` is unset there. | Expected. Gemma's seat on Cloud Run is served by Gemini and labeled fallback. | Leave `GRPC_WORKER_ADDRS` unset on Cloud Run. Optionally set `GEMMA_HOSTED_MODEL=<verified hosted Gemma ID>` so the seat is still Gemma. Say so when presenting the load numbers. |
| 11 | **The Rust worker still binds `0.0.0.0:50051`** (`src/main.rs:254`), with plaintext gRPC and no auth. `sky-gcp.yaml` opens port 50051 publicly. The plan says to bind `127.0.0.1`. | Security story: the threat-model row says this is mitigated, but the code still binds all interfaces. | Change `src/main.rs:254` to `std::env::var("WORKER_ADDR").unwrap_or("127.0.0.1:50051".into()).parse()?` and rebuild with `cargo build --release`. If that's not possible before freeze, change the threat-model row to "planned". |
| 12 | **`Cargo.toml` defaults to `features = ["metal"]`.** | Only matters for a Linux or CUDA image. Correct on the Mac. | For any CUDA build, use `--no-default-features --features cuda`, as `sky-gcp.yaml` already does. |

Corrected deploy command (gateway only; items 2–5 and 8):

```bash
gcloud run deploy wordrust \
  --project=gen-lang-client-0189911611 --region=us-central1 \
  --image="$IMAGE" \
  --service-account="wordrust-run@gen-lang-client-0189911611.iam.gserviceaccount.com" \
  --set-secrets="GATEWAY_API_KEYS=gateway-api-keys:latest" \
  --set-env-vars="HOST=0.0.0.0,PLAYERS=real,GOOGLE_GENAI_USE_VERTEXAI=true,GOOGLE_CLOUD_PROJECT=gen-lang-client-0189911611,GOOGLE_CLOUD_LOCATION=us-central1,GEMINI_PLAYER_MODEL=<verified>,GEMINI_FALLBACK_MODEL=<verified-lite>,GEMINI_COMMENTATOR_MODEL=<verified-lite>,MAX_ACTIVE_GAMES=2000" \
  --port=8080 --cpu=4 --memory=2Gi --timeout=300 \
  --min-instances=1 --max-instances=1 --concurrency=1000 \
  --no-cpu-throttling --session-affinity \
  --allow-unauthenticated
```

Use `--allow-unauthenticated` here because the gateway already enforces its own bearer keys.

## 4. What to skip given the time

- **Gemma on Cloud Run GPU.** It needs a CUDA image, about 5 GB of weights baked in or mounted from GCS, `--gpu=1 --gpu-type=nvidia-l4 --cpu=4 --memory=16Gi --no-cpu-throttling --concurrency=1`, GPU quota in `us-central1`, and HTTP/2 end-to-end (`--use-http2`) for gRPC. The worker also hardcodes its bind address and the `models/gemma-4-e2b` path. Too much for the remaining time; describe it as the next step.
- **SkyPilot L4 worker** (cut item 3) and **mTLS** (cut item 4).
- **Batching, continuous batching, or removing the worker mutex.** Keep the pool-plus-spillover story.
- **Multi-instance gateway state** (Redis or `game_id` sharding). Describe it only.
- **Wiring Vertex ↔ API-key failover.** Present the existing chain instead.
