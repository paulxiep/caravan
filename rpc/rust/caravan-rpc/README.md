# caravan-rpc (Rust)

Runtime SDK for the [Caravan](https://github.com/paulxiep/caravan) application-definition compiler.

**Status: 0.1.1.** Functional runtime with `#[wagon]` proc-macro, axum HTTP server adapter, Lambda Function URL client + server adapters, `CARAVAN_RPC_PEERS` env-var dispatch, peer-mode self-call guard, and Caravan-shipped resource adapters (BlobStore, MessageQueue) for S3 / Redis / RabbitMQ / SQS.

## What is Caravan?

Caravan is an application-definition compiler. The same source code deploys across **packaging** (inproc / container / lambda) × **placement** (compose / Fargate / Lambda) × **composition** (oss-local / cloud-managed) axes by yaml-line changes alone — no source-code edits.

Read the [thesis](https://github.com/paulxiep/caravan/blob/main/docs/thesis.md) and the [PoC RPC SDK spec](https://github.com/paulxiep/caravan/blob/main/docs/poc_rpc_sdk.md).

## Install

```toml
[dependencies]
caravan-rpc = "0.1.1"
```

Optional feature gates (off by default for WASM-friendly builds):

```toml
caravan-rpc = { version = "0.1.1", features = ["client", "server"] }
# Resource-adapter extras (each pulls in its backend libs only when enabled):
# resources-aws    — boto-like S3BlobStore + SqsQueue via aws-sdk-rust
# resources-redis  — RedisStreamQueue via redis crate
# resources-rabbit — RabbitMQQueue via lapin
# resources-all    — convenience meta-feature
```

## Three-point structural contract

User code interacts with Caravan through three SDK entry points; everything else is compiler-managed:

```rust
use std::sync::Arc;
use caravan_rpc::{wagon, provide, client};

// 1. `#[wagon]` declares a trait as a Caravan seam — a synchronous
//    abstraction boundary that yaml can flip between inproc / HTTP /
//    Lambda dispatch per target.
#[wagon]
#[async_trait::async_trait]
pub trait Embedder: Send + Sync {
    async fn embed(&self, text: String) -> Result<Vec<f32>, EmbedError>;
}

// 2. `provide` registers a concrete impl at process startup.
struct FastEmbedImpl { /* ... */ }

#[async_trait::async_trait]
impl Embedder for FastEmbedImpl {
    async fn embed(&self, text: String) -> Result<Vec<f32>, EmbedError> { /* ... */ }
}

fn register(state: &mut AppState) {
    provide::<dyn Embedder>(Arc::new(FastEmbedImpl::new()));
}

// 3. `client` dispatches a call — inproc, HTTP, or Lambda per the
//    `CARAVAN_RPC_PEERS` env var the compiler emits per target.
async fn lookup(query: &str) {
    let embedder = client::<dyn Embedder>();
    let v = embedder.embed(query.into()).await;
}
```

## `run_or_serve` — fourth contract point for entry mains

Peer containers reuse the consumer entry's image; `CARAVAN_RPC_ROLE=peer-<Interface>` (injected by Caravan) tells the SDK to serve that interface instead of running the user app:

```rust
#[tokio::main]
async fn main() -> Result<(), Box<dyn std::error::Error>> {
    let state = AppState::from_config(...).await?;  // calls provide() inside
    caravan_rpc::run_or_serve(|| async move {
        // The user's normal app startup. Only runs when CARAVAN_RPC_ROLE
        // is unset — i.e., this process is the consumer, not a peer.
        run_chat_server(state).await
    }).await?;
    Ok(())
}
```

When `CARAVAN_RPC_ROLE=peer-Embedder` is set, `run_or_serve` discovers the macro-emitted server adapter for `Embedder` from `inventory`, builds the axum router, and serves on `0.0.0.0:8080`. When `AWS_LAMBDA_RUNTIME_API` is also set (AWS Lambda runtime), the same router is handed to `lambda_http::run` instead of binding TCP.

## Dispatch modes

`CARAVAN_RPC_PEERS` is a per-deploy-unit JSON map the compiler emits:

```json
{
  "Embedder":         {"mode": "http",   "url": "http://embedder:8080"},
  "Reranker":         {"mode": "inproc"},
  "ValidateExtraction": {"mode": "lambda", "function_url": "https://...lambda-url.ap-southeast-1.on.aws/"}
}
```

- `inproc` → `client::<dyn T>()` returns the registered local impl directly (zero overhead).
- `http`  → returns an `<Trait>HttpClient` that POSTs to `/_caravan/rpc/<iface>/<method>` with a Bearer token.
- `lambda` → SigV4-signed POST to the Lambda Function URL.

Peer-mode self-call guard: when `CARAVAN_RPC_ROLE=peer-<T>` matches the served interface, `try_client::<dyn T>()` bypasses the HTTP factory and returns the local impl — peer containers share the consumer's `CARAVAN_RPC_PEERS`, so without this guard the macro-emitted router would loop back over HTTP to itself.

## Resource adapters

Caravan-shipped impls of common resource seams (gated by feature flags so users only pull in the backends they need):

```rust
use caravan_rpc::resources::{auto_register_resources, BlobStore};
use caravan_rpc::client;

fn main() -> anyhow::Result<()> {
    let fallback = std::fs::read_to_string("config/app.yaml")?;
    let cfg: serde_yaml::Value = serde_yaml::from_str(&fallback)?;
    caravan_rpc::resources::auto_register_resources(Some(&cfg))?;

    // Use the registered impl through the normal client() path.
    let blob = client::<dyn BlobStore>();
    blob.put("input.pdf", &bytes)?;
    Ok(())
}
```

Backend selection is driven by explicit Caravan-emitted markers:

- `CARAVAN_BLOB_BACKEND=s3` + `S3_BUCKET` set → `S3BlobStore` (real AWS or MinIO via `S3_ENDPOINT_URL`).
- `CARAVAN_BLOB_BACKEND=local-fs` → `LocalFsBlobStore` rooted at `LOCAL_FS_BLOB_PATH` (or yaml fallback `blob_storage.base_path`).
- Marker unset → consult `yaml_fallback`. Non-caravan local-dev path.

`CARAVAN_BLOB_BACKEND=s3` with no `S3_BUCKET` loud-fails at startup (catches the "user forgot to populate `.env.hybrid` from `tofu output`" footgun).

## Versions

- **0.1.1**: peer-mode self-call guard; `CARAVAN_BLOB_BACKEND` explicit marker; `try_client::<T>()` skips the HTTP factory when serving as `peer-<T>`. SDK version bumped alongside Python 0.1.1 for matching wire-protocol semantics.
- **0.1.0**: first functional release. `#[wagon]` proc-macro, axum HTTP adapter, Lambda Function URL client + server (M7), resource adapters (M4-cloud), `run_or_serve` entry contract (M2 Path B).
- **0.0.x**: crates.io name reservation placeholders.

See [development_plan.md](https://github.com/paulxiep/caravan/blob/main/docs/development_plan.md) for the full milestone history.

## License

Apache-2.0. See [LICENSE](LICENSE).
