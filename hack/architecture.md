# Word Rust / Word Hunt Arena: architecture

Source: `bourkefloyd/oxidizinggemma` `main` (`Dockerfile`, `hack/gcp.md`, `hack/arena-deploy.md`, `go-gateway/`). Rendered diagram: [`architecture.png`](architecture.png).

```mermaid
%%{init: {"theme":"base","flowchart":{"curve":"basis","nodeSpacing":40,"rankSpacing":60,"wrappingWidth":420,"padding":14},"themeVariables":{"fontSize":"20px","fontFamily":"DejaVu Sans, Arial, sans-serif","lineColor":"#5f6368"}}}%%
flowchart TB
  subgraph CLIENTS["Clients"]
    direction LR
    UI["<b>Browser · React arena</b><br/>grid of up to 100 live games<br/>POST /arena/runs · EventSource SSE"]
    K6["<b>k6 load tests</b><br/>s1 gateway · s2 real hybrid<br/>s4 abuse → 401 / 413 / 415 / 429"]
  end

  subgraph RUN["Cloud Run · service wordrust · us-central1"]
    direction LR
    GW["<b>Go gateway</b> · distroless nonroot<br/>serves web UI from same origin<br/>min = max = 1 instance · concurrency 1000<br/>4 vCPU · 2 GiB · no CPU throttling"]
    SEC["<b>Security middleware</b><br/>SHA-256 hashed bearer keys (constant-time)<br/>per-key rate limits · active-game caps<br/>body cap 413 · JSON-only 415 · strict 400<br/>CORS allowlist · per-game SSE stream_token"]
    ARENA["<b>Arena manager</b> · in-memory state<br/>≤ 100 games / run · 12 model-backed<br/>agent mix 10 / 30 / 30 / 30<br/>live leaderboard → SSE events"]
    RTR["<b>Router</b><br/>same-model retry + jittered backoff<br/>250 ms → 2 s · circuit breakers<br/>labeled fallback, never silent"]
    WH["<b>Word Hunt core</b><br/>embedded dictionary<br/>server-side word + path validation<br/>perfect-score solver (~30 µs)"]
    GW --> SEC --> ARENA --> RTR
    ARENA <--> WH
  end

  subgraph AI["Gemini Cloud APIs"]
    direction LR
    VTX["<b>Vertex AI</b> · location global<br/>gemini-3.8-flash<br/>race seats + commentator (primary)"]
    GAPI["<b>Gemini API</b> · key tier<br/>arena client · race failover tier"]
    AGENT["<b>Gemini agent</b> · 10%<br/>gemini-3.8-flash<br/>function call submit_words ⇄ validator"]
    GEMMA["<b>Hosted Gemma</b> · 3 × 30%<br/>baseline: gemma-4-26b-a4b-it<br/>diffusion, diffusion-JEV: gemma-4-31b-it"]
    GAPI --- AGENT
    GAPI --- GEMMA
  end

  subgraph OPS["GCP supporting services"]
    direction LR
    CB["<b>Cloud Build</b>"]
    AR["<b>Artifact Registry</b><br/>repo wordrust"]
    SM["<b>Secret Manager</b><br/>gateway-api-keys<br/>gemini-api-key"]
    IAM["<b>IAM · SA wordrust-run</b><br/>aiplatform.user · secretAccessor<br/>logWriter · metricWriter"]
    LOG["<b>Cloud Logging</b><br/>+ Monitoring"]
    CB -->|push image| AR
  end

  subgraph NEXT["Next · roadmap, not deployed"]
    direction LR
    RUST["Rust Gemma worker · gRPC<br/>SkyPilot / L4 GPU"]
    MEM["Memorystore shared state<br/>→ N gateway instances"]
    DIFF["Dedicated diffusion runners"]
  end

  UI -->|"HTTPS JSON + SSE"| GW
  K6 -->|"HTTPS + bearer key"| GW
  RTR -->|"arena games"| GAPI
  RTR -->|"race seats"| VTX
  VTX -.->|"failover"| GAPI
  AR -->|"deploy image"| GW
  SM -.->|"secrets → env"| GW
  IAM -.->|"runtime identity"| GW
  GW -.->|"logs"| LOG
  RTR -.-> RUST
  ARENA -.-> MEM
  GEMMA -.-> DIFF

  classDef client fill:#e8f0fe,stroke:#1a73e8,stroke-width:2px,color:#202124
  classDef run fill:#ffffff,stroke:#1a73e8,stroke-width:2px,color:#202124
  classDef sec fill:#fce8e6,stroke:#d93025,stroke-width:2px,color:#202124
  classDef ai fill:#f3e8fd,stroke:#9334e6,stroke-width:2px,color:#202124
  classDef ops fill:#e6f4ea,stroke:#188038,stroke-width:2px,color:#202124
  classDef next fill:#f8f9fa,stroke:#9aa0a6,stroke-width:2px,stroke-dasharray:6 4,color:#5f6368
  class UI,K6 client
  class GW,ARENA,WH,RTR run
  class SEC sec
  class GAPI,AGENT,GEMMA,VTX ai
  class CB,AR,SM,IAM,LOG ops
  class RUST,MEM,DIFF next
  style NEXT fill:#ffffff,stroke:#9aa0a6,stroke-dasharray:6 4
  style RUN fill:#f1f6fe,stroke:#1a73e8,stroke-width:2px
  style AI fill:#faf5ff,stroke:#9334e6
  style OPS fill:#f5fbf7,stroke:#188038
  style CLIENTS fill:#ffffff,stroke:#dadce0
```

**Legend.** Blue: clients and the Cloud Run gateway. Red: the security layer. Purple: Gemini Cloud APIs. Green: GCP supporting services. Solid arrows are live request paths. Dotted arrows are config, identity, or logging paths. Dashed grey boxes are on the roadmap and not deployed.

## One arena run, step by step

1. The browser loads the React UI from the same Cloud Run origin. The Go gateway serves `/app/web`, so there is no separate frontend host or CORS hop.
2. The user starts a run. `POST /arena/runs` sends JSON, and the gateway rejects anything that isn't `application/json` (415), anything over the body cap (413), and unknown fields or trailing data (400).
3. The arena manager draws one board, and the **Word Hunt solver** computes its perfect score server-side (about 30 µs). It creates up to 100 games and backs at most 12 of them with models (`ARENA_MAX_REAL_GAMES=12`).
4. Games are assigned agent profiles in a **10 / 30 / 30 / 30** mix: the Gemini agent (`gemini-3.8-flash`), then three hosted Gemma profiles: baseline on `gemma-4-26b-a4b-it`, and diffusion and diffusion-JEV on `gemma-4-31b-it`.
5. The **Gemini agent** uses function calling. The model calls `submit_words`, the server validates each word against the dictionary and the board path, and the model iterates on that feedback.
6. Each model call goes through the router. It retries the **same model** with jittered backoff (250 ms, 500 ms, 1 s, 2 s) until the round deadline. Failures are labeled in results and never silently substituted.
7. Scores come only from server-side validation, never from model output. The leaderboard and per-game progress stream to the browser over **SSE** (`GET …/events`, `retry: 1000`).
8. Logs go to Cloud Logging under the `wordrust-run` identity.

## Security and reliability

- Bearer keys come from Secret Manager (`gateway-api-keys`). The gateway stores only SHA-256 digests and compares them in constant time. Games are scoped to their key, and other keys get 404.
- Per-key token-bucket rate limits apply to create, word, and read routes, alongside per-key and global active-game caps.
- Strict input handling: body size cap, JSON-only content type, strict JSON decoding, and a CORS allowlist. Backend errors are never echoed to clients. Word injection, forged paths, duplicates, and late words are all rejected.
- Browser `EventSource` can't send auth headers, so SSE uses a per-game `stream_token` instead.
- The image is distroless and runs as `nonroot`. The `wordrust-run` service account has only four roles: `aiplatform.user`, `secretAccessor`, `logWriter`, and `metricWriter`. The Gemini key lives in Secret Manager (`gemini-api-key`), not in the image.
- Reliability: same-model retry with backoff, circuit breakers per tier, and Vertex → Gemini API key failover for race seats. A failed player still lets its game finish. These paths are covered by `go test -race` tests.

## Scale and architecture

- The deployment is deliberately a **single instance** (`min=max=1`, concurrency 1000, 4 vCPU, 2 GiB, no CPU throttling, session affinity). Arena state is in memory, so a second instance would break runs.
- The Go gateway is cheap per stream: goroutines handle each SSE stream. Model concurrency is bounded (12 model-backed games per run), and solver and validation are microsecond-scale.
- k6 scenarios: `s1` gateway throughput, `s2` real-model hybrid, and `s4` abuse, which checks for 401, 413, 415, and 429.
- **Next:** move state to Memorystore (or route deterministically by `run_id`) to run N instances; add a Rust Gemma gRPC worker on an L4 GPU via SkyPilot or Cloud Run GPU; add dedicated diffusion runners.
