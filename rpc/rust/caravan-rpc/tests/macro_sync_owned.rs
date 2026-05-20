//! Verify the Session-3 proc-macro emits a working sync HttpClient +
//! router for a trait whose args and returns are all owned. The trait
//! shape here mirrors what code-rag's Embedder would look like *if* it
//! used owned types; the real `&str` / `&[&str]` borrowed-arg support
//! lands in Session 4.

#![cfg(all(feature = "client", feature = "server"))]

use std::sync::Arc;
use std::time::Duration;

use serde::{Deserialize, Serialize};

use caravan_rpc::wagon;

// User error type — must be Serialize+Deserialize so the macro-generated
// adapter can carry Result<T, E> across the wire.
#[derive(Debug, Clone, Serialize, Deserialize, PartialEq, Eq)]
pub enum DemoError {
    Empty,
    TooLong(usize),
}

// Synthetic sync trait with fully-owned types. Matches the Session-3
// macro support window.
#[wagon]
pub trait DemoEmbedder: Send + Sync {
    fn embed(&self, text: String) -> Result<Vec<f32>, DemoError>;
    fn batch(&self, texts: Vec<String>) -> Result<Vec<Vec<f32>>, DemoError>;
    fn dimension(&self) -> usize;
}

// Test impl: byte-floats, padded to dim 4.
struct DemoImpl;

impl DemoEmbedder for DemoImpl {
    fn embed(&self, text: String) -> Result<Vec<f32>, DemoError> {
        if text.is_empty() {
            return Err(DemoError::Empty);
        }
        if text.len() > 100 {
            return Err(DemoError::TooLong(text.len()));
        }
        let mut v: Vec<f32> = text.bytes().map(|b| b as f32).collect();
        v.resize(4, 0.0);
        Ok(v)
    }
    fn batch(&self, texts: Vec<String>) -> Result<Vec<Vec<f32>>, DemoError> {
        texts.into_iter().map(|t| self.embed(t)).collect()
    }
    fn dimension(&self) -> usize {
        4
    }
}

async fn spawn_server(impl_arc: Arc<dyn DemoEmbedder>) -> u16 {
    let listener = tokio::net::TcpListener::bind("127.0.0.1:0")
        .await
        .expect("bind");
    let port = listener.local_addr().expect("addr").port();
    let router = build_demo_embedder_router(impl_arc);
    tokio::spawn(async move {
        axum::serve(listener, router).await.expect("serve");
    });
    tokio::time::sleep(Duration::from_millis(50)).await;
    port
}

#[tokio::test(flavor = "multi_thread", worker_threads = 2)]
async fn macro_generated_embed_roundtrip() {
    let server_impl: Arc<dyn DemoEmbedder> = Arc::new(DemoImpl);
    let port = spawn_server(server_impl).await;
    let base_url = format!("http://127.0.0.1:{port}");

    let result = tokio::task::spawn_blocking(move || {
        let client = DemoEmbedderHttpClient::new(base_url);
        client.embed("hi".to_string())
    })
    .await
    .expect("blocking task");

    // 'h'=104, 'i'=105, padded to dim 4.
    assert_eq!(result, Ok(vec![104.0, 105.0, 0.0, 0.0]));
}

#[tokio::test(flavor = "multi_thread", worker_threads = 2)]
async fn macro_generated_batch_roundtrip() {
    let server_impl: Arc<dyn DemoEmbedder> = Arc::new(DemoImpl);
    let port = spawn_server(server_impl).await;
    let base_url = format!("http://127.0.0.1:{port}");

    let result = tokio::task::spawn_blocking(move || {
        let client = DemoEmbedderHttpClient::new(base_url);
        client.batch(vec!["a".to_string(), "bb".to_string()])
    })
    .await
    .expect("blocking task");

    assert_eq!(
        result,
        Ok(vec![vec![97.0, 0.0, 0.0, 0.0], vec![98.0, 98.0, 0.0, 0.0],])
    );
}

#[tokio::test(flavor = "multi_thread", worker_threads = 2)]
async fn macro_generated_dimension_no_args_roundtrip() {
    let server_impl: Arc<dyn DemoEmbedder> = Arc::new(DemoImpl);
    let port = spawn_server(server_impl).await;
    let base_url = format!("http://127.0.0.1:{port}");

    let result = tokio::task::spawn_blocking(move || {
        let client = DemoEmbedderHttpClient::new(base_url);
        client.dimension()
    })
    .await
    .expect("blocking task");

    assert_eq!(result, 4);
}

#[tokio::test(flavor = "multi_thread", worker_threads = 2)]
async fn macro_generated_user_error_passes_through_result_encoding() {
    let server_impl: Arc<dyn DemoEmbedder> = Arc::new(DemoImpl);
    let port = spawn_server(server_impl).await;
    let base_url = format!("http://127.0.0.1:{port}");

    // Empty string → Err(DemoError::Empty). The macro-generated client
    // decodes the wire's Result<T,E> JSON via serde, so the Err arm comes
    // back faithfully (no transport-level failure).
    let result = tokio::task::spawn_blocking(move || {
        let client = DemoEmbedderHttpClient::new(base_url);
        client.embed("".to_string())
    })
    .await
    .expect("blocking task");

    assert_eq!(result, Err(DemoError::Empty));
}
