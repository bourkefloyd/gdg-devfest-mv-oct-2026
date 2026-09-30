# Codelab: Oxidizing Gemma — Zero-Python High-Performance Foundation Model Runtimes

Welcome to the **Oxidizing Gemma Codelab**. In this hands-on workshop, you will build a production-grade, zero-Python foundation model inference engine for **Google Gemma 4** using **Rust (Candle)**, **Go (High-Concurrency Gateway)**, **gRPC (Streaming Protocols)**, and **SkyPilot (Multi-Cloud Orchestration)**.

---

## The Curriculum

* **[Module 0: Mission & Architecture Overview](./00-introduction.md)**
  * The philosophy of zero-Python inference, the Prompting Paradox, and the 3-tier system architecture.
* **[Module 1: Toolchain Setup & Zero-Copy Weight Memory-Mapping](./01-environment-and-weights.md)**
  * Installing dependencies (Rust, Go, Protoc, CUDA/Metal), downloading Gemma 4 weights, and Safetensors `mmap` internals.
* **[Module 2: Gemma 4 Neural Decoder & Per-Layer Embeddings (PLE)](./02-architecture-and-per-layer-embeddings.md)**
  * Implementing Gemma 4 RMSNorm (+1 unit scaling), Rotary Position Embeddings (RoPE), GeGLU MLP, and Per-Layer Embeddings.
* **[Module 3: Upper-Layer KV Cache Sharing & Memory Arithmetic](./03-kv-cache-and-upper-layer-sharing.md)**
  * Heterogeneous attention (sliding window vs global), KV caching mechanics, and the math behind upper-layer KV sharing.
* **[Module 4: High-Throughput gRPC Streaming Engine](./04-high-throughput-grpc-engine.md)**
  * Compiling Protobuf definitions with `tonic-build`, implementing async token generation in Tokio, and streaming via gRPC.
* **[Module 5: High-Concurrency Go API Gateway & SSE](./05-go-api-gateway-and-sse.md)**
  * Building the Go reverse proxy, gRPC connection pooling, and OpenAI-compatible Server-Sent Events (SSE) `/v1/chat/completions`.
* **[Module 6: Multimodal Vision & Video Engine](./06-multimodal-vision-and-video.md)**
  * Building the 16-layer Vision Transformer (ViT), 2D row/column positional tables, multimodal projection, and temporal video frame processing.
* **[Module 7: Multi-Cloud Deployment & Hardware Benchmarking](./07-cloud-orchestration-skypilot.md)**
  * Provisioning on Google Cloud (L4/A100) and Nebius Cloud (H100) using SkyPilot, and analyzing real-world token throughput.

---

## Interactive Web Codelab

You can also view this entire curriculum as an interactive web app at **[`codelab/index.html`](./index.html)**.
