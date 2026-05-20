//! Wire-format codec for Caravan RPC v1.
//!
//! Matches the Python SDK byte-for-byte (see
//! `caravan/rpc/python/src/caravan_rpc/_codec.py` and the same project's
//! `serve.py`). Any divergence here breaks cross-language interop tested at
//! M9.
//!
//! Request:  `{"args": [...], "kwargs": {...}}`
//! Success:  `{"ok": true,  "result": <value>}`
//! Failure:  `{"ok": false, "error": {"code": "<str>", "message": "<str>"}}`
//!
//! Rust callers populate `args` only; `kwargs` is always `{}` (Rust has no
//! kwargs at the language level). The field is present in the JSON for wire
//! compatibility with Python peers.

use serde::{Deserialize, Serialize};
use serde_json::{Value, json};

use crate::errors::{RpcRemoteError, RpcTransportError};

/// Wire-version header value. Bumping this requires lockstep across all 4
/// SDKs and every deployed peer (see `docs/poc_rpc_sdk.md` §2.5).
pub const WIRE_VERSION: &str = "1";

/// HTTP path prefix for Caravan RPC. Full path is
/// `/_caravan/rpc/<interface>/<method>`.
pub const PATH_PREFIX: &str = "/_caravan/rpc/";

/// Header names referenced by the wire contract.
pub const HEADER_WIRE_VERSION: &str = "X-Caravan-Rpc-Version";
pub const HEADER_AUTHORIZATION: &str = "Authorization";

/// Request envelope for a peer call.
#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct Request {
    pub args: Vec<Value>,
    /// Always `{}` for Rust senders. Kept for cross-language wire parity.
    #[serde(default)]
    pub kwargs: serde_json::Map<String, Value>,
}

impl Request {
    /// Build a request envelope from a slice of JSON-serialized positional
    /// arguments. Used by macro-generated client adapters.
    pub fn from_args(args: Vec<Value>) -> Self {
        Self {
            args,
            kwargs: serde_json::Map::new(),
        }
    }

    /// Encode to a JSON byte vector. `serde_json` does not sort map keys by
    /// default; this should not matter at the protocol level (Python's
    /// `json.dumps` is also key-insertion-ordered), but golden-capture tests
    /// must canonicalize before comparing across languages.
    pub fn to_json_bytes(&self) -> Vec<u8> {
        serde_json::to_vec(self).expect("Request serialization is infallible")
    }
}

/// Response envelope returned by a peer. `tag = "ok"` would be the
/// `serde(tag)` approach but the wire uses a bool, so we hand-roll the split.
#[derive(Debug, Clone)]
pub enum Response {
    Ok(Value),
    Err { code: String, message: String },
}

impl Response {
    /// Parse a peer's JSON response. Returns `RpcTransportError` if the body
    /// is not a valid envelope; `RpcRemoteError` (logical failure) is mapped
    /// into [`Response::Err`] so callers can pattern-match.
    pub fn from_json_bytes(body: &[u8]) -> Result<Self, RpcTransportError> {
        let v: Value = serde_json::from_slice(body)
            .map_err(|e| RpcTransportError::DecodeJson(e.to_string()))?;
        let ok = v
            .get("ok")
            .and_then(Value::as_bool)
            .ok_or(RpcTransportError::DecodeEnvelope("ok"))?;
        if ok {
            let result = v
                .get("result")
                .cloned()
                .ok_or(RpcTransportError::DecodeEnvelope("result"))?;
            Ok(Response::Ok(result))
        } else {
            let err = v
                .get("error")
                .ok_or(RpcTransportError::DecodeEnvelope("error"))?;
            let code = err
                .get("code")
                .and_then(Value::as_str)
                .ok_or(RpcTransportError::DecodeEnvelope("error.code"))?
                .to_string();
            let message = err
                .get("message")
                .and_then(Value::as_str)
                .ok_or(RpcTransportError::DecodeEnvelope("error.message"))?
                .to_string();
            Ok(Response::Err { code, message })
        }
    }

    /// Build a success envelope.
    pub fn ok(result: Value) -> Self {
        Response::Ok(result)
    }

    /// Build a failure envelope.
    pub fn err(code: impl Into<String>, message: impl Into<String>) -> Self {
        Response::Err {
            code: code.into(),
            message: message.into(),
        }
    }

    /// Serialize to JSON bytes (server-side use).
    pub fn to_json_bytes(&self) -> Vec<u8> {
        let v = match self {
            Response::Ok(result) => json!({"ok": true, "result": result}),
            Response::Err { code, message } => {
                json!({"ok": false, "error": {"code": code, "message": message}})
            }
        };
        serde_json::to_vec(&v).expect("Response serialization is infallible")
    }

    /// Unwrap into a logical-result `Result`. Server-side handlers and
    /// client-side dispatchers both want this shape after decoding.
    pub fn into_result(self) -> Result<Value, RpcRemoteError> {
        match self {
            Response::Ok(v) => Ok(v),
            Response::Err { code, message } => Err(RpcRemoteError { code, message }),
        }
    }
}

/// Construct the wire path for a method call.
pub fn path_for(interface: &str, method: &str) -> String {
    format!("{PATH_PREFIX}{interface}/{method}")
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn request_round_trip_empty_args() {
        let req = Request::from_args(vec![]);
        let bytes = req.to_json_bytes();
        // Same shape Python emits for a zero-arg call.
        assert_eq!(
            std::str::from_utf8(&bytes).unwrap(),
            r#"{"args":[],"kwargs":{}}"#
        );
    }

    #[test]
    fn request_with_string_arg() {
        let req = Request::from_args(vec![json!("hello")]);
        let bytes = req.to_json_bytes();
        assert_eq!(
            std::str::from_utf8(&bytes).unwrap(),
            r#"{"args":["hello"],"kwargs":{}}"#
        );
    }

    #[test]
    fn request_with_vec_of_floats() {
        let req = Request::from_args(vec![json!([1.0, 2.0, 3.0])]);
        let bytes = req.to_json_bytes();
        assert_eq!(
            std::str::from_utf8(&bytes).unwrap(),
            r#"{"args":[[1.0,2.0,3.0]],"kwargs":{}}"#
        );
    }

    #[test]
    fn response_ok_envelope() {
        let r = Response::ok(json!([1.0, 2.0]));
        let bytes = r.to_json_bytes();
        assert_eq!(
            std::str::from_utf8(&bytes).unwrap(),
            r#"{"ok":true,"result":[1.0,2.0]}"#
        );
    }

    #[test]
    fn response_err_envelope() {
        let r = Response::err("ValueError", "no good");
        let bytes = r.to_json_bytes();
        assert_eq!(
            std::str::from_utf8(&bytes).unwrap(),
            r#"{"ok":false,"error":{"code":"ValueError","message":"no good"}}"#
        );
    }

    #[test]
    fn response_decode_ok() {
        let body = br#"{"ok":true,"result":[1.0,2.0]}"#;
        let parsed = Response::from_json_bytes(body).expect("parses");
        match parsed {
            Response::Ok(v) => assert_eq!(v, json!([1.0, 2.0])),
            Response::Err { .. } => panic!("expected Ok"),
        }
    }

    #[test]
    fn response_decode_err() {
        let body = br#"{"ok":false,"error":{"code":"E","message":"m"}}"#;
        let parsed = Response::from_json_bytes(body).expect("parses");
        match parsed {
            Response::Err { code, message } => {
                assert_eq!(code, "E");
                assert_eq!(message, "m");
            }
            Response::Ok(_) => panic!("expected Err"),
        }
    }

    #[test]
    fn response_decode_missing_ok_field() {
        let body = br#"{"result":1}"#;
        let err = Response::from_json_bytes(body).expect_err("rejects");
        match err {
            RpcTransportError::DecodeEnvelope(f) => assert_eq!(f, "ok"),
            other => panic!("unexpected error variant: {other:?}"),
        }
    }

    #[test]
    fn response_decode_malformed_json() {
        let body = b"not json";
        let err = Response::from_json_bytes(body).expect_err("rejects");
        assert!(matches!(err, RpcTransportError::DecodeJson(_)));
    }

    #[test]
    fn path_construction() {
        assert_eq!(
            path_for("Embedder", "embed_one"),
            "/_caravan/rpc/Embedder/embed_one"
        );
    }
}
