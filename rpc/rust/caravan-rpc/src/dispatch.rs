//! HTTP client dispatchers used by macro-generated client adapters.
//!
//! Two entry points, sync and async, that the proc-macro selects between
//! based on the seam trait's method shape (sync `fn` vs `async fn`). Both
//! speak the Wire-version-1 protocol in `codec.rs`.
//!
//! Gated by the `client` cargo feature. WASM consumers (`code-rag-engine`)
//! compile with `default-features = false` and so do not pull in reqwest.

#![cfg(feature = "client")]

use serde_json::Value;

use crate::codec::{
    HEADER_AUTHORIZATION, HEADER_WIRE_VERSION, Request, Response, WIRE_VERSION, path_for,
};
use crate::errors::{RpcError, RpcTransportError};
use crate::peers::shared_secret;

/// Build the bearer-auth header value, if a shared secret is configured.
fn bearer(secret: Option<&str>) -> Option<String> {
    secret.map(|s| format!("Bearer {s}"))
}

/// Build the request URL by joining the peer base URL with the wire path.
/// Trailing slash on `base_url` is tolerated so compose-generated URLs and
/// hand-edited URLs both work.
fn join_url(base_url: &str, interface: &str, method: &str) -> String {
    let trimmed = base_url.trim_end_matches('/');
    let path = path_for(interface, method);
    format!("{trimmed}{path}")
}

/// Inspect a status code; return a transport error for anything that isn't
/// 200 OK. Logical errors (`{ok: false, error}`) still come over HTTP 200
/// per the wire contract — the peer separates transport from logic.
fn check_status(status: u16, body: &str) -> Result<(), RpcTransportError> {
    if status == 200 {
        Ok(())
    } else {
        Err(RpcTransportError::BadStatus {
            status,
            body: body.chars().take(512).collect(),
        })
    }
}

/// Synchronous HTTP dispatcher. Used by the macro for sync trait methods
/// (Embedder, Reranker in code-rag at B0p). Blocks the calling thread on
/// the HTTP roundtrip — when invoked from inside a tokio runtime, the
/// executor thread blocks (documented limitation per the M2 plan).
pub fn dispatch_sync(
    base_url: &str,
    interface: &str,
    method: &str,
    args: Vec<Value>,
) -> Result<Value, RpcError> {
    let request = Request::from_args(args);
    let body = request.to_json_bytes();
    let url = join_url(base_url, interface, method);

    let client = reqwest::blocking::Client::new();
    let mut req = client
        .post(&url)
        .header("Content-Type", "application/json")
        .header(HEADER_WIRE_VERSION, WIRE_VERSION)
        .body(body);
    if let Some(b) = bearer(shared_secret().as_deref()) {
        req = req.header(HEADER_AUTHORIZATION, b);
    }
    let resp = req
        .send()
        .map_err(|e| RpcTransportError::Http(e.to_string()))?;
    let status = resp.status().as_u16();
    let bytes = resp
        .bytes()
        .map_err(|e| RpcTransportError::Http(e.to_string()))?;
    let body_str = std::str::from_utf8(&bytes).unwrap_or("<non-utf8>");
    check_status(status, body_str)?;
    let envelope = Response::from_json_bytes(&bytes)?;
    envelope.into_result().map_err(Into::into)
}

/// Asynchronous HTTP dispatcher. Used by the macro for async / async-trait
/// trait methods (LlmClient, VectorReader in code-rag at B0p). Non-blocking
/// roundtrip via reqwest's async client.
pub async fn dispatch_async(
    base_url: &str,
    interface: &str,
    method: &str,
    args: Vec<Value>,
) -> Result<Value, RpcError> {
    let request = Request::from_args(args);
    let body = request.to_json_bytes();
    let url = join_url(base_url, interface, method);

    let client = reqwest::Client::new();
    let mut req = client
        .post(&url)
        .header("Content-Type", "application/json")
        .header(HEADER_WIRE_VERSION, WIRE_VERSION)
        .body(body);
    if let Some(b) = bearer(shared_secret().as_deref()) {
        req = req.header(HEADER_AUTHORIZATION, b);
    }
    let resp = req
        .send()
        .await
        .map_err(|e| RpcTransportError::Http(e.to_string()))?;
    let status = resp.status().as_u16();
    let bytes = resp
        .bytes()
        .await
        .map_err(|e| RpcTransportError::Http(e.to_string()))?;
    let body_str = std::str::from_utf8(&bytes).unwrap_or("<non-utf8>");
    check_status(status, body_str)?;
    let envelope = Response::from_json_bytes(&bytes)?;
    envelope.into_result().map_err(Into::into)
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn url_joining_trims_trailing_slash() {
        assert_eq!(
            join_url("http://embedder:8080/", "Embedder", "embed_one"),
            "http://embedder:8080/_caravan/rpc/Embedder/embed_one"
        );
        assert_eq!(
            join_url("http://embedder:8080", "Embedder", "embed_one"),
            "http://embedder:8080/_caravan/rpc/Embedder/embed_one"
        );
    }

    #[test]
    fn bearer_header_format() {
        assert_eq!(bearer(Some("hex123")).as_deref(), Some("Bearer hex123"));
        assert_eq!(bearer(None), None);
    }

    #[test]
    fn check_status_ok() {
        assert!(check_status(200, "ok").is_ok());
    }

    #[test]
    fn check_status_5xx_returns_bad_status() {
        let err = check_status(500, "boom").unwrap_err();
        match err {
            RpcTransportError::BadStatus { status, body } => {
                assert_eq!(status, 500);
                assert_eq!(body, "boom");
            }
            other => panic!("unexpected: {other:?}"),
        }
    }

    #[test]
    fn check_status_truncates_large_body() {
        let big_body = "x".repeat(2000);
        let err = check_status(500, &big_body).unwrap_err();
        match err {
            RpcTransportError::BadStatus { body, .. } => assert_eq!(body.len(), 512),
            other => panic!("unexpected: {other:?}"),
        }
    }
}
