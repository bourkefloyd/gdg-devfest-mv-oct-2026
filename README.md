# Word Rust

**Placed top 4, Developer track, Google hackathon, Oct 1 2026, theme Build, Secure, and Scale.**

Gemini 3.8 Flash and Gemma 4 26B play the same Word Hunt board on Cloud Run. The server checks every word against the dictionary and the tile path, then streams the live boards and leaderboard. An optional DiffusionGemma 26B seat runs on this Mac and never receives a connection from Cloud Run.

**Live:** [wordrust-958584846348.us-central1.run.app](https://wordrust-958584846348.us-central1.run.app)

**Stack:** Go gateway, React arena, Gemini API for Gemma 4 26B, Vertex AI for Gemini 3.8 Flash. Optional local seat: MLX 4-bit DiffusionGemma.

**Google Cloud:** Cloud Run, Cloud Build, Artifact Registry, Secret Manager, IAM, Cloud Logging, Monitoring, Vertex AI.

**Architecture:** [hack/architecture.md](hack/architecture.md) and [hack/architecture.png](hack/architecture.png).

Live revision `wordrust-00009-6fb`, image `6caccf0` (includes #26, same board for the local seats). `ARENA_GEMINI_MAX_TURNS=1`.

### Numbers to have ready

`docs/judge-prep.md` was not in the local project store. These are the recorded runs.

| Run | Result |
| --- | --- |
| k6 public smoke | p50 72.3 ms, p95 1048.6 ms, 0.60% HTTP fails (one SSE timeout) |
| N=24 `e0d74cec94546d50` | Gemini 15,200 and 0 errors. Hosted Gemma seats scored 0 with deadline errors, before minimal thinking |
| N=100 `f10cc60dcd37dfb6` | `gemma-4-31b-it` mocks 60/60 `429`. Gemma 26B 13/30 `429`. Gemini 10/10 completed |
| 8-seat on `wordrust-00008-8kr` | One turn each. Gemini 2400, 2100, 2000, 2000. Gemma 26B 1200, 800, 500, 400. 0 errors, 0 retries |
| 8-seat on `wordrust-00009-6fb` | Run `eb4eb0bc83121539`. Gemini 12800, 10600, 10600, 8800. Gemma 26B 3300, 3300, 2900, 2900. 0 errors, 0 retries |

### Screenshots

The store files `final-leaderboard.png`, `final-arena-live.png`, and `final-arena-boards.png` were not on this Mac. These are the arena captures that were.

![Same board, cloud seats and local DiffusionGemma, with the leaderboard](hack/media/arena-same-board.png)

*Running arena. Cloud Gemini and Gemma seats share the board with local DiffusionGemma. Leaderboard on the right.*

![Arena grid ready for 100 games](hack/media/arena-grid.png)

*Idle grid, 100 boards, before a run starts.*

![Four live boards and the leaderboard](hack/media/arena-swipes.png)

*Four-game run. Swipes on the boards, leaderboard on the right.*

## Run locally

Prerequisites on the hack machine: Go 1.27.1 (`go-gateway/go.mod`), Node v24.14.0, npm 11.9.0. gcloud 586.0.0 is optional and only used to deploy. Put a Gemini API key in `.env`, copied from `.env.example`. `.env` is gitignored and is never committed.

```bash
make setup
make dev
```

`make setup` downloads Go modules and npm packages, and creates `.env` from `.env.example` when it is missing. `make dev` starts the gateway on `127.0.0.1:8080` and the Vite app on `:4317`.

Optional, Apple Silicon only. This is not required. Without it the page shows local diffusion as offline.

```bash
make setup-local-model
make local-model
```

`make setup-local-model` creates `local-model/.venv` and installs `mlx-optiq` ≥ 0.3.2. `make local-model` serves `mlx-community/diffusiongemma-26B-A4B-it-4bit` on `127.0.0.1:8787`. First start downloads about 15 GB and the process needs 16 GB or more of unified memory. The browser calls that address directly. Cloud Run does not.

---

# Gemma 4 Production Architecture: Go API Gateway + Rust gRPC Inference Worker

[![Rust](https://img.shields.io/badge/Language-Rust%202021-orange.svg)](https://www.rust-lang.org/)
[![Go](https://img.shields.io/badge/Language-Go%201.22+-cyan.svg)](https://golang.org/)
[![gRPC](https://img.shields.io/badge/Interface-gRPC%20%7C%20Protobuf-brightgreen.svg)](https://grpc.io/)
[![Engine](https://img.shields.io/badge/Engine-Candle%20(Hugging%20Face)-blue.svg)](https://github.com/huggingface/candle)
[![Model](https://img.shields.io/badge/Model-Google%20Gemma%204%20(E2B)-purple.svg)](https://huggingface.co/google)

A production-grade, multi-tier streaming inference system for **Google's Gemma 4** foundation model featuring:
* **Standalone Interactive CLI (`src/bin/cli.rs`):** Single-shot prompt inference directly from the terminal without networking overhead.
* **Rust Inference Worker (`src/main.rs`):** Persistent, long-running gRPC microservice built on **Tokio**, **Tonic**, and **Candle**. Keeps the Gemma 4 neural network warm in GPU memory (Apple Metal or NVIDIA CUDA) with zero initialization latency per request.
* **Multimodal Engine (`src/vision.rs`, `src/multimodal.rs`):** 16-layer Vision Transformer (ViT), 16x16 patch embedder, and projection head mapping visual tokens into the language model embedding space.
* **Go API Gateway (`go-gateway/`):** High-concurrency HTTP web server handling client traffic, managing connection lifecycles, and streaming tokens over Server-Sent Events (SSE).
* **Shared Interface (`proto/inference.proto`):** Type-safe bidirectional gRPC streaming contract.

## Word Hunt Arena

Launch 24–100 concurrent in-memory Word Hunt games and watch each solver swipe
the board live. The arena includes aggregate throughput/latency stats and a
final AI-player leaderboard, and runs without model credentials by default.

```bash
make arena
# open http://localhost:8787/

# Or build the same one-port container used for Cloud Run:
docker compose up --build
```

See [`hack/arena-deploy.md`](hack/arena-deploy.md) for configuration and
Cloud Run build steps.

---

## System Architecture

```mermaid
flowchart LR
    Client["Client / Frontend<br/>(Browser / cURL / Python)"]
    Gateway["Go API Gateway<br/>(Port :8080)"]
    Worker["Rust Candle Worker<br/>(Persistent GPU VRAM)"]

    Client -->|"HTTP POST /v1/chat/completions (Server-Sent Events)"| Gateway
    Gateway -->|"gRPC StreamGenerate (Port :50051 / HTTP2)"| Worker
    Worker -->|"Token-by-Token Stream"| Gateway
    Gateway -->|"Real-time SSE Token Stream"| Client
```

---

## Performance Matrix

| Metric | Apple Silicon M-Series (Metal FP16) | Nebius Cloud NVIDIA H100 80GB SXM5 (CUDA FP16) |
| :--- | :--- | :--- |
| **Model Boot / Warm-Up Time** | ~1.5 - 2.0 s (one-time) | **1.56 s** (one-time) |
| **Per-Request Init Overhead** | **0.00 ms** (Warm GPU VRAM) | **0.00 ms** (Warm GPU VRAM) |
| **Token Streaming Latency** | Instant first-token SSE | Instant first-token SSE |
| **Inference Speed** | ~4 - 6 tokens/sec | **30 - 43+ tokens/sec** |
| **Transport Protocol** | Low-latency binary gRPC | Low-latency binary gRPC |

---

## Prerequisites and Installation

### 1. System Requirements
- **Rust toolchain:** 1.80+ (`rustup default stable`)
- **Go toolchain:** 1.22+ (`brew install go` or `sudo apt install golang-go`)
- **Protobuf Compiler:** `protoc` (`brew install protobuf` or `sudo apt install protobuf-compiler`)
- **CUDA Toolkit (Linux Only):** CUDA 12.0+ with `nvcc` in `PATH`

### 2. Model Weights
Place the Google Gemma 4 model files in `models/gemma-4-e2b/`:
```
models/gemma-4-e2b/
├── config.json
├── tokenizer.json
└── model.safetensors
```

---

## Instructions and How to Run

### Option A: Standalone Single-Shot CLI Engine
Use this mode to test inference quickly directly from the terminal without launching network servers.

```bash
# On Apple Silicon (macOS Metal):
cargo run --bin cli --release "Explain zero-cost abstractions in Rust."

# On Linux (NVIDIA CUDA):
export PATH="/usr/local/cuda/bin:$PATH"
cargo run --bin cli --release --no-default-features --features cuda "Explain zero-cost abstractions in Rust."
```

---

### Option B: Scaled Production Architecture (Go Gateway + Rust gRPC Worker)

#### Step 1: Start the Rust gRPC Inference Worker (Terminal 1)
```bash
# On Apple Silicon (macOS Metal):
cargo run --release

# On Linux / Cloud GPU (NVIDIA CUDA):
export PATH="/usr/local/cuda/bin:$PATH"
export CUDA_HOME="/usr/local/cuda"
cargo run --release --no-default-features --features cuda
```
*The worker loads weights into GPU VRAM once at startup and listens on `0.0.0.0:50051`.*

#### Step 2: Start the Go API Gateway (Terminal 2)
```bash
cd go-gateway
go run main.go
```
*The gateway connects to the Rust worker and exposes an HTTP Server-Sent Events (SSE) endpoint on `http://0.0.0.0:8080`.*

#### Step 3: Stream Inference via cURL (Terminal 3)
```bash
curl -N -X POST http://localhost:8080/v1/chat/completions \
  -H "Content-Type: application/json" \
  -d '{
    "prompt": "Why are Rust and Go a great combination for AI infrastructure?",
    "max_tokens": 100,
    "temperature": 0.7
  }'
```

#### Example Server-Sent Events (SSE) Stream:
```json
data: {"token":"Rust","is_final":false}
data: {"token":" and","is_final":false}
data: {"token":" Go","is_final":false}
data: {"token":" complement","is_final":false}
data: {"token":" each","is_final":false}
data: {"token":" other","is_final":false}
data: {"token":" perfectly:","is_final":false}
data: {"token":" Go","is_final":false}
data: {"token":" delivers","is_final":false}
data: {"token":" rapid","is_final":false}
data: {"token":" concurrent","is_final":false}
data: {"token":" networking,","is_final":false}
data: {"token":" while","is_final":false}
data: {"token":" Rust","is_final":false}
data: {"token":" provides","is_final":false}
data: {"token":" zero-overhead","is_final":false}
data: {"token":" GPU","is_final":false}
data: {"token":" tensor","is_final":false}
data: {"token":" compute.","is_final":false}
data: [DONE]
```

---

### Option C: Multimodal Vision & Video CLI Engine

#### Running with an Image:
```bash
# On Apple Silicon (macOS Metal FP16):
cargo run --bin multimodal_cli --release -- photo.jpg "Describe what is depicted in this visual scene."

# On Linux (NVIDIA CUDA FP16):
export PATH="/usr/local/cuda/bin:$PATH"
cargo run --bin multimodal_cli --release --no-default-features --features cuda -- photo.png "Analyze the visual features."
```

#### Running with a Video:
```bash
# On Apple Silicon (macOS Metal FP16):
cargo run --bin multimodal_cli --release -- clip.mp4 "Summarize the key events in this video."

# On Linux (NVIDIA CUDA FP16):
export PATH="/usr/local/cuda/bin:$PATH"
cargo run --bin multimodal_cli --release --no-default-features --features cuda -- action.mov "What happens throughout this clip?"
```

---

## Multi-Cloud Deployment via SkyPilot (GCP + Nebius)

SkyPilot is configured as the standard orchestrator for all setup, execution, and teardown across **Google Cloud Platform (GCP)** and **Nebius Cloud**.

See [`SKYPILOT_GUIDE.md`](SKYPILOT_GUIDE.md) and [`docs/guides/GCP_SETUP.md`](docs/guides/GCP_SETUP.md) for full documentation.

### 1. Launch on Google Cloud (L4 / A100 / T4):
```bash
sky launch -c gemma4-gcp sky-gcp.yaml --yes
```

### 2. Launch on Nebius Cloud (H100 / L4):
```bash
sky launch -c gemma4-nebius sky-nebius.yaml --yes
```

### 3. Check Status and Monitor:
```bash
sky status
sky logs gemma4-gcp
```

### 4. Mandatory Teardown:
```bash
sky down gemma4-gcp --yes
# or teardown all clusters:
sky down --all --yes
```

---

## Protocol Buffer Contract (`proto/inference.proto`)

```protobuf
syntax = "proto3";

package inference;

service InferenceService {
  rpc StreamGenerate(GenerateRequest) returns (stream GenerateResponse);
}

message GenerateRequest {
  string prompt = 1;
  int32 max_tokens = 2;
  float temperature = 3;
}

message GenerateResponse {
  string token = 1;
  bool is_final = 2;
}
```

---

## Gemma 4 Architectural Novelties Inside `src/gemma4.rs`

1. **Per-Layer Embeddings (PLE):** Injects a 256-dimensional token identity + context projection dynamically at every layer:
   $$\text{PLE}_l = \text{Norm}\left(\text{Proj}(x) \cdot \frac{1}{\sqrt{d_{\text{model}}}} + \text{Embed}_{\text{PLE}}(w) \cdot \sqrt{d_{\text{ple}}}\right) \cdot \frac{1}{\sqrt{2}}$$
2. **Upper-Layer KV-Sharing:** Layers 15..34 share the Key and Value cache from lower layers, reducing memory bandwidth pressure.
3. **Proportional RoPE:** Full-attention layers use `global_head_dim = 512` with `partial_rotary_factor = 0.25` (128 rotated channels, 384 pass-through).
4. **Quad-RMSNorm Blocks:** 4 RMSNorms per decoder layer + unit RMSNorm on Value vectors.

---

## Documentation and Deep-Dives

Complete architectural guides and systems deep-dives are located in [`docs/`](docs/):
- **[Top 10 Rust Concepts in this Project](docs/RUST_CONCEPTS.md)**
- **[Top 10 Go Concepts in this Project](docs/GO_CONCEPTS.md)**
- **[Neural Architecture & Deep Learning Internals](docs/NEURAL_ARCHITECTURE_INTERNALS.md)**
- **[Multimodal Vision & Audio Architecture](docs/MULTIMODAL_ARCHITECTURE.md)**
- **[GCP Deployment Guide](docs/guides/GCP_SETUP.md)**
- **[Nebius Deployment Guide](docs/guides/NEBIUS_SETUP.md)**

---

## Repository Layout

```
.
├── Cargo.toml                  # Rust manifest with Candle, Tokio, Tonic, Prost
├── build.rs                    # Tonic-build compiling proto/inference.proto
├── proto/
│   └── inference.proto         # gRPC streaming service definition
├── go-gateway/
│   ├── go.mod                  # Go module definition
│   ├── main.go                 # HTTP/2 SSE API Gateway + gRPC Client
│   └── proto/                  # Generated Go protobuf stubs
├── src/
│   ├── lib.rs                  # Library root exposing gemma4, vision, and multimodal modules
│   ├── main.rs                 # Persistent gRPC inference worker & server
│   ├── gemma4.rs               # Pure Rust implementation of Gemma 4
│   ├── vision.rs               # Vision Transformer (ViT) & Patch Embedder
│   ├── multimodal.rs           # Multimodal conditional generation engine
│   └── bin/
│       ├── cli.rs              # Standalone single-shot text CLI
│       └── multimodal_cli.rs   # Standalone multimodal vision CLI
├── docs/
│   ├── README.md               # Master documentation index
│   ├── RUST_CONCEPTS.md        # Rust systems patterns
│   ├── GO_CONCEPTS.md          # Go concurrency patterns
│   ├── NEURAL_ARCHITECTURE_INTERNALS.md # Transformer mathematical breakdown
│   ├── MULTIMODAL_ARCHITECTURE.md       # Vision-language pipeline
│   └── guides/
│       ├── GCP_SETUP.md        # Google Cloud Platform guide
│       └── NEBIUS_SETUP.md     # Nebius Cloud guide
├── models/
│   └── gemma-4-e2b/            # Model weights (Safetensors), tokenizer.json, config.json
├── BUILDERS_LOG.md             # Detailed engineering troubleshooting log
├── SKYPILOT_GUIDE.md           # SkyPilot multi-cloud orchestrator guide
├── SLIDES.html                 # Presentation slide deck
├── SLIDES.md                   # Presentation slide source
└── README.md                   # System architecture documentation
```

---

## License
Apache-2.0 / MIT.
Gemma 4 model weights are subject to Google's Gemma Terms of Use.
