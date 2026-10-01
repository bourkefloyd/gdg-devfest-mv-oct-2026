---
cursor:
  subagentId: "bc-6bc9543c-c709-5508-a8ac-f9b6b31a4703"
---

# oxidizinggemma: local Mac setup audit (for a Linux Cloud Agent replica)

Audited read-only on Oct 1, 2026, on Bourke's Mac (`darwin/arm64`).

## Key finding: no project setup exists on this Mac yet

`~/projects/oxidizinggemma` did not exist until it was cloned today (~13:39) by this same worker. That checkout is a clean clone at `af8a6bb` with no untracked or ignored files: no `.env`, no `target/`, no `models/`, and no built gateway. Shell history (`~/.zsh_history`, `~/.bash_history`) has no `oxidizinggemma` entries. So there is no "done" local setup to copy. What follows is (a) what the repo itself requires and (b) which of those tools and assets already exist on the machine.

## Toolchains

| Need (per repo) | Repo-stated minimum | On this Mac |
|---|---|---|
| Rust (rustup, cargo) | 1.80+ stable, edition 2021, no `rust-toolchain` file | rustc/cargo 1.99.0, `stable-aarch64-apple-darwin`, in `~/.cargo/bin` (not on PATH in non-login shells) |
| Go | `go.mod` says `go 1.27.1` (README says 1.22+, but go.mod wins) | go 1.27.1 darwin/arm64 |
| protoc | needed by `build.rs` (`tonic-build` compiles `proto/inference.proto`) | libprotoc 36.2 (Homebrew) |
| ffmpeg | needed only by `multimodal_cli` for video frame extraction | ffmpeg 8.1 |
| python3 | used only by `stream.sh` for JSON encoding | 3.14.6 |
| CUDA 12.0+ with `nvcc` | Linux GPU builds only | n/a (Mac) |
| SkyPilot (`sky`), gcloud | only for the cloud deployment yamls | sky missing; gcloud 586.0.0 |

Node and uv are present but the project doesn't use them. No Python package manifest exists.

Linux system packages, taken from `sky-gcp.yaml`: `golang-go protobuf-compiler build-essential pkg-config libssl-dev`, plus `ffmpeg` and `curl`. Install Rust with rustup. Note that apt's `golang-go` is likely older than 1.27.1, so install Go from go.dev instead (or let `GOTOOLCHAIN=auto` download it).

## Model weights

- The code hardcodes the relative path `models/gemma-4-e2b/` (`src/main.rs:208`, `src/bin/cli.rs:41`, `src/bin/multimodal_cli.rs:153`). That directory must contain `config.json`, `tokenizer.json`, and `*.safetensors`. It is gitignored.
- Source: Hugging Face `google/gemma-4-E2B-it` (BUILDERS_LOG; the codelab says `google/gemma-4-e2b`). The repo is gated, so you need an HF token with the Gemma license accepted. The code does **not** download weights at runtime (hf-hub is a dependency but unused for loading). Download them ahead of time, for example: `hf download google/gemma-4-E2B-it --local-dir models/gemma-4-e2b`.
- Size: about 5 GB per BUILDERS_LOG ("full 5GB model weights"). This wasn't measured because no local copy exists.
- On this Mac: **not present** in the needed form. `~/.cache/huggingface/hub` (115 GB total) holds no `google/gemma-4-E2B-it`. It has `google/gemma-4-12b-it` (22 GB, the wrong model) and several MLX-quantized Gemma 4 variants (for example `FakeRockert543/gemma-4-e2b-it-MLX-8bit`, 8 GB). The MLX quantized formats won't load in this Candle engine. An HF token file exists at `~/.cache/huggingface/token`.

## Environment variables and secrets (names only)

- `HF_TOKEN`: in `.env.example`. It's needed to download the gated weights, not at runtime.
- `PORT` (default 8080) and `GRPC_WORKER_ADDR` (default `127.0.0.1:50051`): read by `go-gateway/main.go`.
- `GEMMA_HOST`: used by `stream.sh`. It defaults to a hardcoded public IP (`195.242.13.222:8080`), so set it to `127.0.0.1:8080` for local runs.
- `CUDA_HOME`, plus `PATH` including `/usr/local/cuda/bin`: needed for Linux CUDA builds.
- `dotenvy` loads `.env` in Rust.

## Build, run, and test commands (from the README and codelab; none were executed here)

Mac (the default `metal` feature):

- `cargo run --bin cli --release -- "Hello Gemma!"`: one-shot CLI
- `cargo run --release`: gRPC worker on :50051
- `cd go-gateway && go run main.go`: SSE gateway on :8080, `POST /v1/chat/completions`
- `cargo run --bin multimodal_cli --release -- photo.jpg "prompt"`: images or video (video requires ffmpeg)
- `./stream.sh "prompt"`: client

On Linux, add `--no-default-features --features cuda` to every cargo command. There are no Rust `#[test]`s and no Go `_test.go` files, so a build plus a CLI smoke run is the only verification.

## Mac-specific pieces that won't carry over

- **`default = ["metal"]` in Cargo.toml.** A plain `cargo build` on Linux will fail to compile Metal. Always pass `--no-default-features`, then pick a backend:
  - `--features cuda` needs an NVIDIA GPU, CUDA 12+, and nvcc.
  - With no features it builds a **CPU-only** binary. That's the realistic option for a GPU-less Cloud Agent VM. Check whether the code falls back to `Device::Cpu` when neither backend is compiled in (not verified). E2B inference on CPU will be slow but should work for smoke tests.
- The MLX weights in the Mac HF cache are Apple-only and irrelevant to this repo.
- Mac uses Homebrew for protoc, go, and ffmpeg; on Linux use apt or official tarballs.
- Disk: about 5 GB of weights plus a Candle/tonic release build (several GB in `target/`). `sky-gcp.yaml` provisions 150 GB.
