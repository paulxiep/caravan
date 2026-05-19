# caravan-rpc (Rust) — Pre-release placeholder

Runtime SDK for the [Caravan](https://github.com/paulxiep/caravan) application-definition compiler.

**Status: 0.0.1 pre-release placeholder.** This release reserves the crates.io name. The functional SDK lands at 0.1.0.

## What is Caravan?

Caravan is an application-definition compiler. The same source code deploys across **packaging** (inproc / container / lambda) × **placement** (compose / Fargate / Lambda / Batch) × **composition** (oss-local / cloud-managed) axes by yaml-line changes alone — no source-code edits.

Read the [thesis](https://github.com/paulxiep/caravan/blob/main/docs/thesis.md) and the [PoC RPC SDK spec](https://github.com/paulxiep/caravan/blob/main/docs/poc_rpc_sdk.md).

## When 0.1.0 ships

```rust
use caravan_rpc::{wagon, provide, client};

#[wagon]
#[async_trait::async_trait]
pub trait Embedder: Send + Sync {
    async fn embed(&self, text: String) -> Vec<f32>;
}

// provider side
struct FastEmbedImpl { /* ... */ }

#[async_trait::async_trait]
impl Embedder for FastEmbedImpl {
    async fn embed(&self, text: String) -> Vec<f32> { /* ... */ }
}

fn register() {
    provide::<dyn Embedder>(Arc::new(FastEmbedImpl::new()));
}

// caller side — dispatches inproc / http / lambda per CARAVAN_RPC_PEERS
async fn use_embedder() {
    let embedder = client::<dyn Embedder>();
    let v = embedder.embed("hello".into()).await;
}
```

The 0.0.1 placeholder exposes `wagon` / `provide` / `client` as no-op functions; `client()` panics.

## Roadmap

- **0.0.1** (this release): reserve crates.io name, build-clean stubs.
- **0.1.0**: functional runtime with `#[wagon]` proc-macro, axum HTTP server adapter, Lambda Function URL client adapter, `CARAVAN_RPC_PEERS` env-var dispatch. See [development_plan.md](https://github.com/paulxiep/caravan/blob/main/docs/development_plan.md) milestone M2.

## License

Apache-2.0. See [LICENSE](LICENSE).
