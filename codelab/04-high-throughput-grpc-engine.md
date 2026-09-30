# Module 4: High-Throughput gRPC Streaming Engine

In this module, you will build a high-performance **gRPC streaming server** in Rust using **Tonic** and **Tokio**. This service accepts inference requests and streams generated tokens back over an HTTP/2 multiplexed stream with sub-millisecond per-token latency.

---

## 1. Defining the gRPC Protocol (`inference.proto`)

Open [`proto/inference.proto`](../proto/inference.proto) to inspect the service definition:

```protobuf
syntax = "proto3";

package gemma;
option go_package = "go-gateway/proto;proto";

service InferenceService {
  rpc StreamGenerate (GenerateRequest) returns (stream GenerateResponse);
}

message GenerateRequest {
  string prompt = 1;
  int32 max_tokens = 2;
  float temperature = 3;
  float top_p = 4;
}

message GenerateResponse {
  string text = 1;
  bool is_finished = 2;
  int32 finish_reason = 3; // 0 = None, 1 = Stop Token, 2 = Max Tokens
}
```

---

## 2. Compiling Protobufs in `build.rs`

In Rust, [`build.rs`](../build.rs) automatically compiles the `.proto` schema into type-safe Rust code at compile time using `tonic-build`:

```rust
fn main() -> Result<(), Box<dyn std::error::Error>> {
    tonic_build::configure()
        .build_server(true)
        .build_client(false)
        .compile_protos(&["proto/inference.proto"], &["proto"])?;
    Ok(())
}
```

---

## 3. Implementing the Asynchronous Streaming RPC

In [`src/main.rs`](../src/main.rs), we implement `InferenceService` using Tokio channels (`mpsc::channel`):

```rust
use tokio::sync::mpsc;
use tokio_stream::wrappers::ReceiverStream;
use tonic::{Request, Response, Status};

pub struct GemmaInferenceServer {
    model: std::sync::Arc<tokio::sync::Mutex<Gemma4Model>>,
    tokenizer: std::sync::Arc<tokenizers::Tokenizer>,
    device: candle_core::Device,
}

#[tonic::async_trait]
impl inference_service_server::InferenceService for GemmaInferenceServer {
    type StreamGenerateStream = ReceiverStream<Result<GenerateResponse, Status>>;

    async fn stream_generate(
        &self,
        request: Request<GenerateRequest>,
    ) -> Result<Response<Self::StreamGenerateStream>, Status> {
        let req = request.into_inner();
        let (tx, rx) = mpsc::channel(128);

        let model = self.model.clone();
        let tokenizer = self.tokenizer.clone();
        let device = self.device.clone();

        // Spawn inference loop on blocking worker thread
        tokio::task::spawn_blocking(move || {
            let mut model = model.blocking_lock();
            let mut logits_processor = LogitsProcessor::new(42, Some(req.temperature as f64), Some(req.top_p as f64));
            
            // Encode prompt & execute prefill...
            let tokens = tokenizer.encode(req.prompt.as_str(), true).unwrap();
            let prompt_ids = tokens.get_ids();
            
            // Decode loop: send each token to the gRPC stream channel
            for step in 0..req.max_tokens {
                let token_text = tokenizer.decode(&[next_token], false).unwrap_or_default();
                
                let resp = GenerateResponse {
                    text: token_text,
                    is_finished: false,
                    finish_reason: 0,
                };
                
                if tx.blocking_send(Ok(resp)).is_err() {
                    break; // Client disconnected
                }
                
                // Decode next step...
            }
        });

        Ok(Response::new(ReceiverStream::new(rx)))
    }
}
```

---

## 4. Running the Rust gRPC Inference Server

Launch the gRPC server:

```bash
# On Apple Silicon:
cargo run --release --bin gemma_hello

# On Linux CUDA:
cargo run --release --no-default-features --features cuda --bin gemma_hello
```

**Expected Output:**
```
1. Loading Gemma 4 configuration from "models/gemma-4-e2b/config.json"
2. Initializing compute device
   Active hardware device: Metal (Precision: F16)
3. Loading tokenizer from "models/gemma-4-e2b/tokenizer.json"
4. Memory-mapping 1 safetensors file(s)
5. Loading model weights into device memory
   Model initialized and warm in 1.45s

Gemma 4 gRPC Worker listening on http://0.0.0.0:50051
```

Next, let's build the frontend HTTP gateway in **[Module 5: High-Concurrency Go API Gateway & SSE](./05-go-api-gateway-and-sse.md)**.
