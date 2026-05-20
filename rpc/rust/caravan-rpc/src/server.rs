//! Axum-based HTTP server for hosting `@wagon` impls as peer services.
//!
//! Gated by the `server` cargo feature. Used by:
//! 1. macro-generated `build_<trait>_router(impl)` functions (lands Session 3);
//! 2. the compiler-emitted synthetic peer crate's `main.rs` (lands Session 5).
//!
//! The router exposes one POST route per method at
//! `/_caravan/rpc/<interface>/<method>`. Headers are validated centrally;
//! per-method args decoding and impl dispatch is owned by the
//! `MethodHandler` callback (the macro will fill these in).
//!
//! Wire spec: see [`crate::codec`].

#![cfg(feature = "server")]

use std::collections::HashMap;
use std::future::Future;
use std::pin::Pin;
use std::sync::Arc;

use axum::Router;
use axum::body::Bytes;
use axum::extract::{Path, State};
use axum::http::{HeaderMap, StatusCode};
use axum::response::{IntoResponse, Response as AxumResponse};
use axum::routing::post;

use crate::codec::{
    HEADER_AUTHORIZATION, HEADER_WIRE_VERSION, PATH_PREFIX, Response, WIRE_VERSION,
};

/// Handler callback for a single (interface, method) pair. Owns three
/// things: decoding the JSON `Request` envelope into the method's typed
/// args; invoking the registered impl (sync via direct call, async via
/// `.await`); encoding the return value into a `Response::Ok` envelope.
///
/// Returns a `Response` enum (`Ok` / `Err`) — the SDK's wrapper converts
/// that into the HTTP response. The macro-generated code (Session 3+)
/// produces one of these per trait-method pair.
pub type MethodHandler =
    Arc<dyn Fn(Bytes) -> Pin<Box<dyn Future<Output = Response> + Send>> + Send + Sync + 'static>;

/// Builder for an interface's server-side router. The macro will call
/// [`RpcRouter::new`] + [`RpcRouter::add_method`] N times then
/// [`RpcRouter::into_axum_router`] once.
pub struct RpcRouter {
    interface: String,
    methods: HashMap<String, MethodHandler>,
}

impl RpcRouter {
    /// Start a new router scoped to one interface name. Multiple
    /// `RpcRouter`s can be `.merge()`d into a single axum router when a
    /// peer hosts multiple seams (post-PoC; PoC peer hosts one seam).
    pub fn new(interface: impl Into<String>) -> Self {
        Self {
            interface: interface.into(),
            methods: HashMap::new(),
        }
    }

    /// Register one method's handler. The handler is invoked when a POST
    /// arrives at `/_caravan/rpc/<this-interface>/<method>` (after header
    /// validation).
    pub fn add_method(mut self, method: impl Into<String>, handler: MethodHandler) -> Self {
        self.methods.insert(method.into(), handler);
        self
    }

    /// Lower into an axum [`Router`]. `secret` is enforced as the bearer
    /// token when `Some`; when `None`, requests without `Authorization`
    /// are accepted (dev mode, matches Python).
    pub fn into_axum_router(self, secret: Option<String>) -> Router {
        let state = Arc::new(RouterState {
            interface: self.interface,
            methods: self.methods,
            secret,
        });
        let route_path = format!("{PATH_PREFIX}{{interface}}/{{method}}");
        Router::new()
            .route(&route_path, post(rpc_handler))
            .with_state(state)
    }
}

/// Shared state for the single axum POST handler. Holds the interface name
/// (for mismatch validation), method map, and optional shared secret.
struct RouterState {
    interface: String,
    methods: HashMap<String, MethodHandler>,
    secret: Option<String>,
}

/// Axum POST handler. Validates headers, looks up the per-method handler,
/// invokes it, and serializes the result back over HTTP. Logical errors
/// (`Response::Err`) come back as HTTP 500 + envelope body per the wire
/// spec; transport-level rejections (bad version, bad auth, unknown
/// route) are HTTP 4xx with an envelope.
async fn rpc_handler(
    State(state): State<Arc<RouterState>>,
    Path((interface, method)): Path<(String, String)>,
    headers: HeaderMap,
    body: Bytes,
) -> AxumResponse {
    // 1. Wire version check.
    let version = headers
        .get(HEADER_WIRE_VERSION)
        .and_then(|v| v.to_str().ok())
        .unwrap_or("");
    if version != WIRE_VERSION {
        return reply(
            StatusCode::BAD_REQUEST,
            Response::err(
                "BadVersion",
                format!("expected {HEADER_WIRE_VERSION}: {WIRE_VERSION}; got {version:?}"),
            ),
        );
    }

    // 2. Bearer auth (skipped when secret is unset — dev mode).
    if let Some(expected) = state.secret.as_ref() {
        let auth = headers
            .get(HEADER_AUTHORIZATION)
            .and_then(|v| v.to_str().ok())
            .unwrap_or("");
        if !auth.starts_with("Bearer ") || &auth[7..] != expected.as_str() {
            return reply(
                StatusCode::UNAUTHORIZED,
                Response::err("Unauthorized", "missing or invalid Bearer token"),
            );
        }
    }

    // 3. Interface name match.
    if interface != state.interface {
        return reply(
            StatusCode::NOT_FOUND,
            Response::err(
                "InterfaceMismatch",
                format!("server hosts {:?}, got {interface:?}", state.interface),
            ),
        );
    }

    // 4. Method lookup.
    let Some(handler) = state.methods.get(&method).cloned() else {
        return reply(
            StatusCode::NOT_FOUND,
            Response::err(
                "UnknownMethod",
                format!("{} has no method {method:?}", state.interface),
            ),
        );
    };

    // 5. Invoke handler. Errors thrown by the impl are translated into
    //    `Response::Err` by the handler closure (macro-generated code at
    //    Session 3+); we only see a `Response` enum here.
    let result = handler(body).await;
    let status = match &result {
        Response::Ok(_) => StatusCode::OK,
        Response::Err { .. } => StatusCode::INTERNAL_SERVER_ERROR,
    };
    reply(status, result)
}

fn reply(status: StatusCode, envelope: Response) -> AxumResponse {
    let body = envelope.to_json_bytes();
    let mut resp = (status, body).into_response();
    // `from_static` is a const fn — no need for LazyLock/OnceLock indirection.
    resp.headers_mut().insert(
        HEADER_WIRE_VERSION,
        axum::http::HeaderValue::from_static(WIRE_VERSION),
    );
    resp.headers_mut().insert(
        axum::http::header::CONTENT_TYPE,
        axum::http::HeaderValue::from_static("application/json"),
    );
    resp
}

/// Bind to `addr` and serve `router` forever. Used by the compiler-emitted
/// synthetic peer crate; meant to be called from `#[tokio::main]`.
pub async fn serve_forever(addr: std::net::SocketAddr, router: Router) -> std::io::Result<()> {
    let listener = tokio::net::TcpListener::bind(addr).await?;
    axum::serve(listener, router).await?;
    Ok(())
}
