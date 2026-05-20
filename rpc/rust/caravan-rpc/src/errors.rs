//! Caravan RPC error types.
//!
//! Mirrors the Python SDK's `RpcRemoteError` / `RpcTransportError` split.
//! Wire-version 1 keeps the error envelope simple: `{ok: false, error: {code,
//! message}}` for logical failures; transport-level failures (timeout, 5xx,
//! malformed body) become `RpcTransportError`.

use thiserror::Error;

/// Logical failure returned by a peer in the `{ok: false, error: {...}}`
/// envelope. The `code` is the originating exception class name (Python) or
/// the Rust error's `Debug` discriminant; `message` is the user-visible
/// message.
#[derive(Debug, Error)]
#[error("remote error [{code}]: {message}")]
pub struct RpcRemoteError {
    pub code: String,
    pub message: String,
}

/// Transport-level failure: connection refused, timeout, 5xx without an
/// envelope body, malformed JSON, missing fields. Anything that prevents
/// extracting a logical-failure envelope from the peer.
#[derive(Debug, Error)]
pub enum RpcTransportError {
    #[error("http request failed: {0}")]
    Http(String),
    #[error("response body was not valid JSON: {0}")]
    DecodeJson(String),
    #[error("response envelope missing required field: {0}")]
    DecodeEnvelope(&'static str),
    #[error("response wire version {got:?} != expected {expected:?}")]
    BadWireVersion { got: String, expected: &'static str },
    #[error("peer returned HTTP {status} without a JSON envelope (body: {body:?})")]
    BadStatus { status: u16, body: String },
}

/// Top-level error returned from dispatcher helpers. Either the peer rejected
/// the call logically, or the transport layer failed.
#[derive(Debug, Error)]
pub enum RpcError {
    #[error(transparent)]
    Remote(#[from] RpcRemoteError),
    #[error(transparent)]
    Transport(#[from] RpcTransportError),
}
