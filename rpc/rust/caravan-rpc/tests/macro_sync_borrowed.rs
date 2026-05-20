//! Verify the Session-4-narrow proc-macro lowers borrowed args (`&str`,
//! `&[&str]`) correctly. The trait shape is byte-for-byte the same as
//! code-rag's `Embedder` (sync, `&str` / `&[&str]` / no-arg methods,
//! `Result<…, BorrowedEmbError>` returns).

#![cfg(all(feature = "client", feature = "server"))]

use std::sync::Arc;
use std::time::Duration;

use serde::{Deserialize, Serialize};

use caravan_rpc::wagon;

#[derive(Debug, Clone, Serialize, Deserialize, PartialEq, Eq)]
pub enum BorrowedEmbError {
    Empty,
    BatchTooLarge(usize),
}

#[wagon]
pub trait BorrowedEmbedder: Send + Sync {
    fn embed_one(&self, text: &str) -> Result<Vec<f32>, BorrowedEmbError>;
    fn embed_batch(&self, texts: &[&str]) -> Result<Vec<Vec<f32>>, BorrowedEmbError>;
    fn dimension(&self) -> usize;
}

struct ByteEmbedder;

impl BorrowedEmbedder for ByteEmbedder {
    fn embed_one(&self, text: &str) -> Result<Vec<f32>, BorrowedEmbError> {
        if text.is_empty() {
            return Err(BorrowedEmbError::Empty);
        }
        let mut v: Vec<f32> = text.bytes().map(|b| b as f32).collect();
        v.resize(4, 0.0);
        Ok(v)
    }
    fn embed_batch(&self, texts: &[&str]) -> Result<Vec<Vec<f32>>, BorrowedEmbError> {
        if texts.len() > 10 {
            return Err(BorrowedEmbError::BatchTooLarge(texts.len()));
        }
        texts.iter().map(|t| self.embed_one(t)).collect()
    }
    fn dimension(&self) -> usize {
        4
    }
}

async fn spawn_server(impl_arc: Arc<dyn BorrowedEmbedder>) -> u16 {
    let listener = tokio::net::TcpListener::bind("127.0.0.1:0")
        .await
        .expect("bind");
    let port = listener.local_addr().expect("addr").port();
    let router = build_borrowed_embedder_router(impl_arc);
    tokio::spawn(async move {
        axum::serve(listener, router).await.expect("serve");
    });
    tokio::time::sleep(Duration::from_millis(50)).await;
    port
}

#[tokio::test(flavor = "multi_thread", worker_threads = 2)]
async fn borrowed_str_arg_roundtrip() {
    let server_impl: Arc<dyn BorrowedEmbedder> = Arc::new(ByteEmbedder);
    let port = spawn_server(server_impl).await;
    let base_url = format!("http://127.0.0.1:{port}");

    let result = tokio::task::spawn_blocking(move || {
        let client = BorrowedEmbedderHttpClient::new(base_url);
        client.embed_one("hi")
    })
    .await
    .expect("blocking");

    assert_eq!(result, Ok(vec![104.0, 105.0, 0.0, 0.0]));
}

#[tokio::test(flavor = "multi_thread", worker_threads = 2)]
async fn borrowed_slice_of_str_roundtrip() {
    let server_impl: Arc<dyn BorrowedEmbedder> = Arc::new(ByteEmbedder);
    let port = spawn_server(server_impl).await;
    let base_url = format!("http://127.0.0.1:{port}");

    let result = tokio::task::spawn_blocking(move || {
        let client = BorrowedEmbedderHttpClient::new(base_url);
        client.embed_batch(&["a", "bb"])
    })
    .await
    .expect("blocking");

    assert_eq!(
        result,
        Ok(vec![vec![97.0, 0.0, 0.0, 0.0], vec![98.0, 98.0, 0.0, 0.0]])
    );
}

#[tokio::test(flavor = "multi_thread", worker_threads = 2)]
async fn no_arg_method_roundtrip() {
    let server_impl: Arc<dyn BorrowedEmbedder> = Arc::new(ByteEmbedder);
    let port = spawn_server(server_impl).await;
    let base_url = format!("http://127.0.0.1:{port}");

    let result = tokio::task::spawn_blocking(move || {
        let client = BorrowedEmbedderHttpClient::new(base_url);
        client.dimension()
    })
    .await
    .expect("blocking");

    assert_eq!(result, 4);
}

#[tokio::test(flavor = "multi_thread", worker_threads = 2)]
async fn err_arm_from_borrowed_method() {
    let server_impl: Arc<dyn BorrowedEmbedder> = Arc::new(ByteEmbedder);
    let port = spawn_server(server_impl).await;
    let base_url = format!("http://127.0.0.1:{port}");

    let result = tokio::task::spawn_blocking(move || {
        let client = BorrowedEmbedderHttpClient::new(base_url);
        client.embed_one("")
    })
    .await
    .expect("blocking");

    assert_eq!(result, Err(BorrowedEmbError::Empty));
}
