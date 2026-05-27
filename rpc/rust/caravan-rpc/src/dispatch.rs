//! HTTP client dispatchers used by macro-generated client adapters.
//!
//! Four entry points: sync/async × HTTP/Lambda. The proc-macro picks
//! sync-vs-async based on the seam trait's method shape; the HTTP-vs-Lambda
//! split is decided at call time from the `PeerEntry` variant the client
//! adapter was constructed with. All four speak the Wire-version-1 protocol
//! in `codec.rs`.
//!
//! Auth split:
//! * HTTP mode: bearer-token via `CARAVAN_RPC_SHARED_SECRET`.
//! * Lambda mode (M7): SigV4 with the `lambda` service name, signed against
//!   the credentials resolved by the AWS default provider chain (env vars
//!   on Lambda, ECS container credentials endpoint on Fargate, etc).
//!
//! Gated by the `client` cargo feature. WASM consumers (`code-rag-engine`)
//! compile with `default-features = false` and so do not pull in reqwest
//! or aws-sigv4.

#![cfg(feature = "client")]

use std::sync::OnceLock;
use std::time::SystemTime;

use serde_json::Value;

use crate::codec::{
    HEADER_AUTHORIZATION, HEADER_WIRE_VERSION, Request, Response, WIRE_VERSION, path_for,
};
use crate::errors::{RpcError, RpcTransportError};
use crate::peers::{PeerEntry, shared_secret};

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

/// Unified sync entry point: dispatches based on the `PeerEntry` variant.
/// HTTP → bearer-auth path; Lambda → SigV4 path. Inproc shouldn't reach
/// here (the macro-generated client only constructs over Http or Lambda).
pub fn dispatch_sync_by_peer(
    peer: &PeerEntry,
    interface: &str,
    method: &str,
    args: Vec<Value>,
) -> Result<Value, RpcError> {
    match peer {
        PeerEntry::Http { url } => dispatch_sync(url, interface, method, args),
        PeerEntry::Lambda { function_url } => {
            dispatch_sync_sigv4(function_url, interface, method, args)
        }
        PeerEntry::Inproc => Err(RpcTransportError::Http(
            "caravan-rpc: inproc peer reached HTTP dispatch (internal bug)".to_string(),
        )
        .into()),
    }
}

/// Unified async entry point. See [`dispatch_sync_by_peer`].
pub async fn dispatch_async_by_peer(
    peer: &PeerEntry,
    interface: &str,
    method: &str,
    args: Vec<Value>,
) -> Result<Value, RpcError> {
    match peer {
        PeerEntry::Http { url } => dispatch_async(url, interface, method, args).await,
        PeerEntry::Lambda { function_url } => {
            dispatch_async_sigv4(function_url, interface, method, args).await
        }
        PeerEntry::Inproc => Err(RpcTransportError::Http(
            "caravan-rpc: inproc peer reached HTTP dispatch (internal bug)".to_string(),
        )
        .into()),
    }
}

/// Extract the AWS region from a Function URL of the form
/// `https://<id>.lambda-url.<region>.on.aws/`. Returns None if the URL
/// doesn't match the lambda-url shape.
fn region_from_function_url(function_url: &str) -> Option<String> {
    let after_scheme = function_url.split("://").nth(1)?;
    let host = after_scheme.split('/').next()?;
    // Expect `<id>.lambda-url.<region>.on.aws`.
    let parts: Vec<&str> = host.split('.').collect();
    if parts.len() >= 5 && parts[1] == "lambda-url" && parts[3] == "on" && parts[4] == "aws" {
        Some(parts[2].to_string())
    } else {
        None
    }
}

/// Process-global cache of the SDK config. Loaded once on first SigV4
/// dispatch via a fresh single-thread tokio runtime (no requirement that
/// the caller be inside tokio). The cached config's credential provider
/// internally handles refresh of expiring temporary credentials.
fn aws_config() -> &'static aws_config::SdkConfig {
    static CONFIG: OnceLock<aws_config::SdkConfig> = OnceLock::new();
    CONFIG.get_or_init(|| {
        let rt = tokio::runtime::Builder::new_current_thread()
            .enable_all()
            .build()
            .expect("caravan-rpc: build current-thread runtime for aws_config init");
        rt.block_on(async {
            aws_config::defaults(aws_config::BehaviorVersion::latest())
                .load()
                .await
        })
    })
}

/// Resolve fresh AWS credentials via the default provider chain. Blocking;
/// uses a single-thread runtime so callers don't need to be inside tokio.
fn resolve_credentials() -> Result<aws_credential_types::Credentials, RpcTransportError> {
    use aws_credential_types::provider::ProvideCredentials;
    let cfg = aws_config();
    let provider = cfg
        .credentials_provider()
        .ok_or_else(|| RpcTransportError::Http("aws credentials provider missing".into()))?;
    let rt = tokio::runtime::Builder::new_current_thread()
        .enable_all()
        .build()
        .map_err(|e| RpcTransportError::Http(format!("build runtime: {e}")))?;
    rt.block_on(provider.provide_credentials())
        .map_err(|e| RpcTransportError::Http(format!("resolve aws credentials: {e}")))
}

/// Sign a request body for a Lambda Function URL POST and return the
/// (header-name, header-value) pairs that should be added to the outbound
/// request. Includes `host`, `x-amz-date`, `x-amz-security-token` (if
/// session creds), `x-amz-content-sha256`, and `Authorization`.
fn sigv4_sign(
    function_url: &str,
    region: &str,
    body: &[u8],
    creds: &aws_credential_types::Credentials,
) -> Result<Vec<(String, String)>, RpcTransportError> {
    use aws_sigv4::http_request::{SignableBody, SignableRequest, SigningSettings, sign};
    use aws_sigv4::sign::v4;

    let identity = creds.clone().into();
    let signing_settings = SigningSettings::default();
    let signing_params: aws_sigv4::http_request::SigningParams = v4::SigningParams::builder()
        .identity(&identity)
        .region(region)
        .name("lambda")
        .time(SystemTime::now())
        .settings(signing_settings)
        .build()
        .map_err(|e| RpcTransportError::Http(format!("sigv4 params: {e}")))?
        .into();

    let signable = SignableRequest::new(
        "POST",
        function_url,
        std::iter::empty(),
        SignableBody::Bytes(body),
    )
    .map_err(|e| RpcTransportError::Http(format!("sigv4 signable: {e}")))?;

    let (instructions, _signature) = sign(signable, &signing_params)
        .map_err(|e| RpcTransportError::Http(format!("sigv4 sign: {e}")))?
        .into_parts();
    let (signing_headers, _params) = instructions.into_parts();
    Ok(signing_headers
        .into_iter()
        .map(|h| (h.name().to_string(), h.value().to_string()))
        .collect())
}

/// Synchronous Lambda dispatcher (M7). Signs the POST with SigV4 and
/// targets the Function URL directly. Mirrors `dispatch_sync` for the
/// HTTP path: same Wire-v1 envelope, same 200/non-200 handling.
pub fn dispatch_sync_sigv4(
    function_url: &str,
    interface: &str,
    method: &str,
    args: Vec<Value>,
) -> Result<Value, RpcError> {
    let request = Request::from_args(args);
    let body = request.to_json_bytes();
    let url = join_url(function_url, interface, method);
    let region = region_from_function_url(function_url)
        .ok_or_else(|| RpcTransportError::Http(format!("can't extract region from {url}")))?;

    let creds = resolve_credentials()?;
    let signed_headers = sigv4_sign(&url, &region, &body, &creds)?;

    let client = reqwest::blocking::Client::new();
    let mut req = client
        .post(&url)
        .header("Content-Type", "application/json")
        .header(HEADER_WIRE_VERSION, WIRE_VERSION)
        .body(body);
    for (name, value) in signed_headers {
        req = req.header(name, value);
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

/// Asynchronous Lambda dispatcher (M7). Async sibling of
/// [`dispatch_sync_sigv4`].
pub async fn dispatch_async_sigv4(
    function_url: &str,
    interface: &str,
    method: &str,
    args: Vec<Value>,
) -> Result<Value, RpcError> {
    let request = Request::from_args(args);
    let body = request.to_json_bytes();
    let url = join_url(function_url, interface, method);
    let region = region_from_function_url(function_url)
        .ok_or_else(|| RpcTransportError::Http(format!("can't extract region from {url}")))?;

    // Resolve creds via the default chain. tokio::task::spawn_blocking
    // keeps the sync block_on dance off the async executor thread.
    let creds = tokio::task::spawn_blocking(resolve_credentials)
        .await
        .map_err(|e| RpcTransportError::Http(format!("join creds task: {e}")))??;
    let signed_headers = sigv4_sign(&url, &region, &body, &creds)?;

    let client = reqwest::Client::new();
    let mut req = client
        .post(&url)
        .header("Content-Type", "application/json")
        .header(HEADER_WIRE_VERSION, WIRE_VERSION)
        .body(body);
    for (name, value) in signed_headers {
        req = req.header(name, value);
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

    #[test]
    fn region_extracted_from_function_url() {
        assert_eq!(
            region_from_function_url("https://abc123.lambda-url.us-east-1.on.aws/").as_deref(),
            Some("us-east-1")
        );
        assert_eq!(
            region_from_function_url(
                "https://abc123.lambda-url.ap-southeast-2.on.aws/_caravan/rpc/X/m"
            )
            .as_deref(),
            Some("ap-southeast-2")
        );
        assert_eq!(
            region_from_function_url("https://example.com/").as_deref(),
            None
        );
    }
}
