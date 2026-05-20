//! Verify the proc-macro's async-trait path. The trait shape mirrors
//! code-rag's `LlmClient` — `#[async_trait]` + `async fn` + `&str` arg
//! + `Result<String, Err>` return.

#![cfg(all(feature = "client", feature = "server"))]

use std::sync::Arc;
use std::time::Duration;

use async_trait::async_trait;
use serde::{Deserialize, Serialize};

use caravan_rpc::wagon;

#[derive(Debug, Clone, Serialize, Deserialize, PartialEq, Eq)]
pub enum DemoLlmError {
    Empty,
    RateLimited,
}

#[wagon]
#[async_trait]
pub trait DemoLlmClient: Send + Sync {
    async fn generate(&self, prompt: &str) -> Result<String, DemoLlmError>;
}

struct EchoLlm;

#[async_trait]
impl DemoLlmClient for EchoLlm {
    async fn generate(&self, prompt: &str) -> Result<String, DemoLlmError> {
        if prompt.is_empty() {
            return Err(DemoLlmError::Empty);
        }
        Ok(format!("echo: {prompt}"))
    }
}

async fn spawn_server(impl_arc: Arc<dyn DemoLlmClient>) -> u16 {
    let listener = tokio::net::TcpListener::bind("127.0.0.1:0")
        .await
        .expect("bind");
    let port = listener.local_addr().expect("addr").port();
    let router = build_demo_llm_client_router(impl_arc);
    tokio::spawn(async move {
        axum::serve(listener, router).await.expect("serve");
    });
    tokio::time::sleep(Duration::from_millis(50)).await;
    port
}

#[tokio::test(flavor = "multi_thread", worker_threads = 2)]
async fn async_trait_generate_roundtrip() {
    let server_impl: Arc<dyn DemoLlmClient> = Arc::new(EchoLlm);
    let port = spawn_server(server_impl).await;
    let base_url = format!("http://127.0.0.1:{port}");

    let client = DemoLlmClientHttpClient::new(base_url);
    let result = client.generate("hi").await;
    assert_eq!(result, Ok("echo: hi".to_string()));
}

#[tokio::test(flavor = "multi_thread", worker_threads = 2)]
async fn async_trait_err_arm_roundtrip() {
    let server_impl: Arc<dyn DemoLlmClient> = Arc::new(EchoLlm);
    let port = spawn_server(server_impl).await;
    let base_url = format!("http://127.0.0.1:{port}");

    let client = DemoLlmClientHttpClient::new(base_url);
    let result = client.generate("").await;
    assert_eq!(result, Err(DemoLlmError::Empty));
}
