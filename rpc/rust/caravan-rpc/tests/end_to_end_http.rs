//! End-to-end Caravan RPC roundtrip: hand-coded client + server adapters
//! for a tiny trait, real axum + tokio + reqwest in the loop.
//!
//! Proves the Session-1 codec + dispatcher + Session-2 server runtime
//! work together before the proc-macro generalizes the adapter codegen
//! in Session 3. The shape of the hand-coded code here is intentionally
//! close to what `#[wagon]` will emit, so the macro design has a working
//! reference.

#![cfg(all(feature = "client", feature = "server"))]

use std::sync::Arc;
use std::time::Duration;

use serde_json::{Value, json};

use caravan_rpc::codec::Response;
use caravan_rpc::dispatch::dispatch_sync;
use caravan_rpc::server::{MethodHandler, RpcRouter};

// ------------------------- Hand-coded "TestEmbedder" -------------------------

/// Test trait mirroring code-rag's Embedder shape but using owned types so
/// the hand-coded adapter doesn't need to grapple with `&str` → owned
/// conversion (that's part of Session 3's proc-macro work).
pub trait TestEmbedder: Send + Sync {
    fn embed(&self, text: String) -> Vec<f32>;
    fn dimension(&self) -> usize;
}

/// Deterministic test impl: maps each byte to a float; vector dim is 4.
struct EchoEmbedder;

impl TestEmbedder for EchoEmbedder {
    fn embed(&self, text: String) -> Vec<f32> {
        let mut v: Vec<f32> = text.bytes().map(|b| b as f32).collect();
        v.resize(4, 0.0);
        v
    }
    fn dimension(&self) -> usize {
        4
    }
}

/// Hand-coded client adapter. The proc-macro at Session 3 will generate
/// the equivalent. Calls cross the wire via `dispatch_sync`.
struct TestEmbedderHttpClient {
    base_url: String,
}

impl TestEmbedder for TestEmbedderHttpClient {
    fn embed(&self, text: String) -> Vec<f32> {
        let args = vec![json!(text)];
        let result =
            dispatch_sync(&self.base_url, "TestEmbedder", "embed", args).expect("rpc embed");
        serde_json::from_value(result).expect("decode result")
    }
    fn dimension(&self) -> usize {
        let result = dispatch_sync(&self.base_url, "TestEmbedder", "dimension", vec![])
            .expect("rpc dimension");
        serde_json::from_value(result).expect("decode dim")
    }
}

/// Hand-coded server-adapter builder. The macro at Session 3 will emit
/// this verbatim per `#[wagon]` trait.
fn build_test_embedder_router(impl_arc: Arc<dyn TestEmbedder>) -> axum::Router {
    let embed_handler: MethodHandler = {
        let impl_arc = impl_arc.clone();
        Arc::new(move |body: axum::body::Bytes| {
            let impl_arc = impl_arc.clone();
            Box::pin(async move {
                let env: caravan_rpc::codec::Request = match serde_json::from_slice(&body) {
                    Ok(e) => e,
                    Err(e) => {
                        return Response::err("BadJSON", e.to_string());
                    }
                };
                let text: String = match env.args.first().and_then(|v| v.as_str()) {
                    Some(s) => s.to_owned(),
                    None => {
                        return Response::err("BadArgs", "expected args[0] = string");
                    }
                };
                let result = impl_arc.embed(text);
                match serde_json::to_value(result) {
                    Ok(v) => Response::ok(v),
                    Err(e) => Response::err("EncodeError", e.to_string()),
                }
            })
        })
    };

    let dim_handler: MethodHandler = {
        let impl_arc = impl_arc.clone();
        Arc::new(move |_body: axum::body::Bytes| {
            let impl_arc = impl_arc.clone();
            Box::pin(async move {
                let result = impl_arc.dimension();
                Response::ok(Value::from(result))
            })
        })
    };

    RpcRouter::new("TestEmbedder")
        .add_method("embed", embed_handler)
        .add_method("dimension", dim_handler)
        .into_axum_router(None)
}

// ------------------------------ Test harness --------------------------------

async fn spawn_server(impl_arc: Arc<dyn TestEmbedder>) -> u16 {
    let listener = tokio::net::TcpListener::bind("127.0.0.1:0")
        .await
        .expect("bind");
    let port = listener.local_addr().expect("addr").port();
    let router = build_test_embedder_router(impl_arc);
    tokio::spawn(async move {
        axum::serve(listener, router).await.expect("serve");
    });
    // Give axum a tick to start accepting connections. ~1ms is usually
    // enough but 50ms keeps the test stable on slow CI.
    tokio::time::sleep(Duration::from_millis(50)).await;
    port
}

// ----------------------------------- Tests ----------------------------------

#[tokio::test(flavor = "multi_thread", worker_threads = 2)]
async fn embed_roundtrip_returns_byte_floats() {
    let server_impl: Arc<dyn TestEmbedder> = Arc::new(EchoEmbedder);
    let port = spawn_server(server_impl).await;
    let base_url = format!("http://127.0.0.1:{port}");

    // dispatch_sync is sync (uses reqwest::blocking) — call from a blocking
    // task so we don't block this test's executor thread.
    let result = tokio::task::spawn_blocking(move || {
        let client = TestEmbedderHttpClient { base_url };
        client.embed("abc".to_string())
    })
    .await
    .expect("blocking task");

    // 'a'=97, 'b'=98, 'c'=99, then padded to dim=4 with 0.
    assert_eq!(result, vec![97.0, 98.0, 99.0, 0.0]);
}

#[tokio::test(flavor = "multi_thread", worker_threads = 2)]
async fn dimension_roundtrip_zero_args() {
    let server_impl: Arc<dyn TestEmbedder> = Arc::new(EchoEmbedder);
    let port = spawn_server(server_impl).await;
    let base_url = format!("http://127.0.0.1:{port}");

    let result = tokio::task::spawn_blocking(move || {
        let client = TestEmbedderHttpClient { base_url };
        client.dimension()
    })
    .await
    .expect("blocking task");

    assert_eq!(result, 4);
}

#[tokio::test(flavor = "multi_thread", worker_threads = 2)]
async fn bad_method_returns_envelope_error() {
    let server_impl: Arc<dyn TestEmbedder> = Arc::new(EchoEmbedder);
    let port = spawn_server(server_impl).await;
    let base_url = format!("http://127.0.0.1:{port}");

    let err = tokio::task::spawn_blocking(move || {
        dispatch_sync(&base_url, "TestEmbedder", "unknown_method", vec![])
    })
    .await
    .expect("blocking task")
    .expect_err("unknown method should fail");

    // The server returns HTTP 404 + envelope body. The dispatcher's
    // status-check kicks in before envelope decoding for non-200s, so we
    // get a transport error (BadStatus). This matches the wire contract:
    // unknown methods are transport-level "route not found", not logical
    // failures.
    match err {
        caravan_rpc::RpcError::Transport(caravan_rpc::RpcTransportError::BadStatus {
            status,
            body,
        }) => {
            assert_eq!(status, 404);
            assert!(body.contains("UnknownMethod"), "body was: {body}");
        }
        other => panic!("unexpected error variant: {other:?}"),
    }
}

#[tokio::test(flavor = "multi_thread", worker_threads = 2)]
async fn missing_wire_version_header_rejected() {
    let server_impl: Arc<dyn TestEmbedder> = Arc::new(EchoEmbedder);
    let port = spawn_server(server_impl).await;
    let base_url = format!("http://127.0.0.1:{port}");

    // Raw reqwest call without the X-Caravan-Rpc-Version header.
    // Important: extract status+body inside spawn_blocking so the response
    // (which carries a tokio runtime handle) is dropped on a blocking
    // thread, not in this async test's executor.
    let (status, body) = tokio::task::spawn_blocking(move || {
        let url = format!("{base_url}/_caravan/rpc/TestEmbedder/embed");
        let r = reqwest::blocking::Client::new()
            .post(&url)
            .header("Content-Type", "application/json")
            .body(r#"{"args":["x"],"kwargs":{}}"#)
            .send()
            .expect("send");
        let status = r.status().as_u16();
        let body = r.text().expect("text");
        (status, body)
    })
    .await
    .expect("blocking task");

    assert_eq!(status, 400);
    assert!(body.contains("BadVersion"), "got: {body}");
}

#[tokio::test(flavor = "multi_thread", worker_threads = 2)]
async fn bearer_auth_enforced_when_secret_configured() {
    let server_impl: Arc<dyn TestEmbedder> = Arc::new(EchoEmbedder);
    // Build server with explicit secret. Stand up by hand so we can set
    // the secret on the router (spawn_server uses None).
    let listener = tokio::net::TcpListener::bind("127.0.0.1:0")
        .await
        .expect("bind");
    let port = listener.local_addr().expect("addr").port();
    let secret = "test-secret-hex".to_string();
    let router = {
        let dim_handler: MethodHandler = {
            let impl_arc = server_impl.clone();
            Arc::new(move |_| {
                let impl_arc = impl_arc.clone();
                Box::pin(async move { Response::ok(Value::from(impl_arc.dimension())) })
            })
        };
        RpcRouter::new("TestEmbedder")
            .add_method("dimension", dim_handler)
            .into_axum_router(Some(secret.clone()))
    };
    tokio::spawn(async move {
        axum::serve(listener, router).await.expect("serve");
    });
    tokio::time::sleep(Duration::from_millis(50)).await;
    let base_url = format!("http://127.0.0.1:{port}");

    // Without auth header → 401.
    let url = format!("{base_url}/_caravan/rpc/TestEmbedder/dimension");
    let no_auth_status = tokio::task::spawn_blocking({
        let url = url.clone();
        move || {
            let r = reqwest::blocking::Client::new()
                .post(&url)
                .header("Content-Type", "application/json")
                .header("X-Caravan-Rpc-Version", "1")
                .body(r#"{"args":[],"kwargs":{}}"#)
                .send()
                .expect("send");
            r.status().as_u16()
        }
    })
    .await
    .expect("blocking task");
    assert_eq!(no_auth_status, 401);

    // With correct bearer → 200.
    let (with_auth_status, body) = tokio::task::spawn_blocking({
        let url = url.clone();
        let secret = secret.clone();
        move || {
            let r = reqwest::blocking::Client::new()
                .post(&url)
                .header("Content-Type", "application/json")
                .header("X-Caravan-Rpc-Version", "1")
                .header("Authorization", format!("Bearer {secret}"))
                .body(r#"{"args":[],"kwargs":{}}"#)
                .send()
                .expect("send");
            let status = r.status().as_u16();
            let body = r.text().expect("text");
            (status, body)
        }
    })
    .await
    .expect("blocking task");
    assert_eq!(with_auth_status, 200);
    assert_eq!(body, r#"{"ok":true,"result":4}"#);
}
