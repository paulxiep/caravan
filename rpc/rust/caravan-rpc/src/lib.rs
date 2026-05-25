//! Runtime SDK for the [Caravan](https://github.com/paulxiep/caravan)
//! application-definition compiler.
//!
//! A user declares a seam-interface trait, marks it with [`wagon`], registers a
//! concrete implementation via [`provide`], and dispatches through [`client`].
//! Dispatch mode (inproc / http / lambda) is read from the
//! `CARAVAN_RPC_PEERS` env var at the call site; when the env var is unset,
//! `client::<dyn I>()` returns the registered `Arc<dyn I>` directly with no
//! overhead (no-config inertness).
//!
//! See <https://github.com/paulxiep/caravan/blob/main/docs/poc_rpc_sdk.md> for
//! the wire contract and per-language surface.
//!
//! # M2 status
//!
//! 0.1.0 ships the runtime building blocks: codec (`codec`), peer-table
//! parsing (`peers`), error types (`errors`), HTTP client dispatchers
//! (`dispatch`, behind the `client` feature). The proc-macro that turns
//! `#[wagon]` into server + client adapters lands in M2 Session 3+. Until
//! then, `client::<dyn T>()` returns the inproc-registered impl regardless of
//! peer-table mode — switching to HTTP happens only after the proc-macro
//! wires per-trait HTTP adapter discovery.
//!
//! Lambda mode panics with an M7 pointer (forward-compat marker).
//!
//! ```ignore
//! use std::sync::Arc;
//! use caravan_rpc::{wagon, provide, client};
//!
//! #[wagon]
//! pub trait Embedder: Send + Sync {
//!     fn embed(&self, text: &str) -> Vec<f32>;
//! }
//!
//! struct InMemoryEmbedder;
//! impl Embedder for InMemoryEmbedder {
//!     fn embed(&self, _text: &str) -> Vec<f32> { vec![0.0; 8] }
//! }
//!
//! // startup
//! provide::<dyn Embedder>(Arc::new(InMemoryEmbedder));
//!
//! // call site
//! let v = client::<dyn Embedder>().embed("hello");
//! assert_eq!(v.len(), 8);
//! ```

#![forbid(unsafe_code)]

// `#[wagon]`-generated code emits `::caravan_rpc::...` paths. Inside this
// crate we re-alias the crate's own name so the macro works on traits
// declared internally (e.g. the `resources::BlobStore` / `MessageQueue`
// seams shipped from caravan-rpc itself).
extern crate self as caravan_rpc;

use std::any::{Any, TypeId};
use std::collections::HashMap;
use std::sync::{Arc, OnceLock, RwLock};

pub use caravan_rpc_macros::wagon;

pub mod codec;
pub mod errors;
pub mod peers;
pub mod resources;

#[cfg(feature = "client")]
pub mod dispatch;

#[cfg(feature = "server")]
pub mod server;

pub use errors::{RpcError, RpcRemoteError, RpcTransportError};
pub use peers::{PeerEntry, peer_for};
pub use resources::{
    BlobError, BlobStore, LocalFsBlobStore, MessageQueue, QueueError, auto_register_resources,
};
#[cfg(feature = "resources-aws")]
pub use resources::{S3BlobStore, SqsQueue};
#[cfg(feature = "resources-redis")]
pub use resources::RedisStreamQueue;
#[cfg(feature = "resources-rabbit")]
pub use resources::RabbitMQQueue;

/// Internal re-exports for use by `#[wagon]`-generated code only. Lets the
/// user's crate depend solely on `caravan-rpc`; the macro reaches in here
/// rather than spelling `::serde_json::...` / `::axum::...` (which would
/// require the user to add those crates to their own Cargo.toml).
///
/// Not a stable public API — names may change without notice.
#[doc(hidden)]
pub mod __macro_support {
    pub use async_trait;
    #[cfg(feature = "server")]
    pub use axum;
    pub use inventory;
    pub use serde_json;
}

/// Factory entry for a `#[wagon]` trait's HTTP client adapter.
///
/// Macro-generated code submits one of these per full-codegen trait via
/// `inventory::submit!` so `client::<dyn T>()` can discover the
/// trait-specific HttpClient constructor at runtime, indexed by `TypeId`.
///
/// `construct` returns the HttpClient wrapped as `Arc<dyn T>` then erased
/// into `Box<dyn Any + Send + Sync>` (because `Arc<dyn T>: Any` for
/// `T: 'static`). The SDK downcasts back to `Arc<T>` in `client::<T>()`.
pub struct HttpAdapterFactory {
    pub interface_name: &'static str,
    pub type_id_fn: fn() -> std::any::TypeId,
    pub construct: fn(url: String) -> Box<dyn Any + Send + Sync>,
}

inventory::collect!(HttpAdapterFactory);

fn lookup_http_factory<T: ?Sized + 'static>() -> Option<&'static HttpAdapterFactory> {
    let want = TypeId::of::<T>();
    inventory::iter::<HttpAdapterFactory>
        .into_iter()
        .find(|f| (f.type_id_fn)() == want)
}

/// Factory entry for a `#[wagon]` trait's server-side router. Mirrors
/// [`HttpAdapterFactory`] but for the server direction.
///
/// Macro-generated code submits one of these per full-codegen trait via
/// `inventory::submit!`. [`run_or_serve`] iterates this collection by
/// interface name to find the right router builder when starting in
/// peer mode.
///
/// `build_router_from_registry` is macro-emitted and does the
/// trait-erased work of: `try_client::<dyn Trait>()` for the impl,
/// then `build_<trait>_router(impl)` to produce the axum router.
#[cfg(feature = "server")]
pub struct HttpServerFactory {
    pub interface_name: &'static str,
    pub build_router_from_registry:
        fn() -> Result<crate::__macro_support::axum::Router, &'static str>,
}

#[cfg(feature = "server")]
inventory::collect!(HttpServerFactory);

/// Run the user's main, OR start a peer HTTP server, based on the
/// `CARAVAN_RPC_ROLE` env var.
///
/// **Inertness**: when the env var is unset or empty, this just awaits
/// `user_main` and returns — no overhead, no behavior change.
///
/// **Peer mode**: when `CARAVAN_RPC_ROLE=peer-<InterfaceName>`,
/// `user_main` is NOT called. Instead, the SDK:
///   1. Looks up the macro-emitted [`HttpServerFactory`] for the named
///      interface (via inventory).
///   2. Calls `build_router_from_registry` which (a) finds the
///      `provide()`-registered impl in the inproc registry and (b)
///      builds the axum router using the macro-generated
///      `build_<trait>_router(impl)`.
///   3. Binds on `CARAVAN_RPC_BIND_ADDR` (default `0.0.0.0:8080`) and
///      `serve_forever`s.
///
/// Caller contract: the user's setup code (including `provide()` calls
/// for all #[wagon] traits) must run BEFORE `run_or_serve` is awaited.
/// Typical pattern:
///
/// ```ignore
/// #[tokio::main]
/// async fn main() -> Result<()> {
///     let state = AppState::from_config(...).await?;  // calls provide() inside
///     caravan_rpc::run_or_serve(|| async move {
///         // user's normal app startup — only runs in non-peer mode.
///         run_chat_server(state).await
///     }).await
/// }
/// ```
#[cfg(feature = "server")]
pub async fn run_or_serve<F, Fut>(user_main: F) -> Result<(), RpcError>
where
    F: FnOnce() -> Fut,
    Fut: std::future::Future<Output = Result<(), RpcError>>,
{
    let role = std::env::var("CARAVAN_RPC_ROLE").unwrap_or_default();
    if let Some(iface_name) = role.strip_prefix("peer-") {
        let factory = inventory::iter::<HttpServerFactory>
            .into_iter()
            .find(|f| f.interface_name == iface_name)
            .unwrap_or_else(|| {
                panic!(
                    "caravan-rpc: CARAVAN_RPC_ROLE={role:?} but no HttpServerFactory \
                     registered for interface {iface_name:?}. Did you mark the trait \
                     with #[wagon] and have your impl crate compiled into this binary?"
                )
            });
        let router = (factory.build_router_from_registry)().unwrap_or_else(|msg| {
            panic!("caravan-rpc: peer {iface_name} failed to build router: {msg}")
        });
        let addr: std::net::SocketAddr = std::env::var("CARAVAN_RPC_BIND_ADDR")
            .unwrap_or_else(|_| "0.0.0.0:8080".to_string())
            .parse()
            .expect("CARAVAN_RPC_BIND_ADDR must parse as SocketAddr");
        eprintln!("caravan peer {iface_name} serving on {addr}");
        server::serve_forever(addr, router)
            .await
            .expect("serve_forever returned error");
        Ok(())
    } else {
        user_main().await
    }
}

/// Version of this crate.
pub const VERSION: &str = env!("CARGO_PKG_VERSION");

/// Process-global inproc registry mapping a seam trait's [`TypeId`] to its
/// `Arc<dyn T>` impl.
///
/// Stored as `Box<dyn Any + Send + Sync>` so we can key by any trait object's
/// `TypeId`. The stored value is always an `Arc<T>` (with `T: ?Sized`); the
/// downcast in [`client`] reconstructs that exact type.
type Registry = RwLock<HashMap<TypeId, Box<dyn Any + Send + Sync>>>;

fn registry() -> &'static Registry {
    static R: OnceLock<Registry> = OnceLock::new();
    R.get_or_init(|| RwLock::new(HashMap::new()))
}

/// Register `impl_` as the inproc provider for trait object `T`.
///
/// Call once per process at startup (worker entry, CLI `main()`) before any
/// `client::<dyn T>()` call. Re-registering an interface replaces the prior
/// impl (last-write-wins) — intentional for test isolation; production code
/// should call `provide` once per interface.
///
/// ```ignore
/// provide::<dyn Embedder>(Arc::new(FastEmbedImpl::new()?));
/// ```
pub fn provide<T: ?Sized + Send + Sync + 'static>(impl_: Arc<T>) {
    let mut g = registry().write().expect("caravan-rpc registry poisoned");
    g.insert(
        TypeId::of::<T>(),
        Box::new(impl_) as Box<dyn Any + Send + Sync>,
    );
}

/// Return an `Arc<dyn T>` to dispatch through.
///
/// Behavior depends on `CARAVAN_RPC_PEERS[interface]`:
/// * Unset or `inproc` → the locally `provide()`-ed impl (zero-overhead).
/// * `http` AND the trait was full-codegen-expanded by `#[wagon]` (so an
///   inventory factory exists) → an `Arc<<Trait>HttpClient>` whose every
///   method call goes over the wire.
/// * `http` but no inventory factory (e.g., `#[wagon(identity)]` trait) →
///   falls back to the local impl. Logged once at startup so misconfigs
///   are visible. Documented limitation: identity-marked traits don't
///   honor mode flips.
/// * `lambda` → panic with M7 pointer.
///
/// Panics if no impl is registered AND no http factory exists for `T`.
/// Use [`try_client`] for optional seams.
pub fn client<T: ?Sized + Send + Sync + 'static>() -> Arc<T> {
    try_client::<T>().unwrap_or_else(|| {
        panic!(
            "no impl registered for type {}; call provide::<{}>(Arc::new(impl)) at startup",
            std::any::type_name::<T>(),
            std::any::type_name::<T>()
        )
    })
}

/// Return an `Arc<dyn T>` to dispatch through, or `None` if no impl is
/// available (neither locally `provide()`-ed nor wired via HTTP through
/// `#[wagon]`'s inventory factory).
///
/// Use this for seams that are conditionally enabled at runtime (e.g. an
/// optional reranker). For seams that must always be present, prefer the
/// panicking [`client`] for a clearer error message at startup.
///
/// Dispatch-mode selection mirrors [`client`]: HTTP mode + an inventory
/// factory → returns the macro-generated `<Trait>HttpClient`; otherwise
/// → returns the registered local impl (inproc).
pub fn try_client<T: ?Sized + Send + Sync + 'static>() -> Option<Arc<T>> {
    // 1. If an HTTP factory exists for T (i.e., the trait was full-
    //    codegen-expanded by `#[wagon]`), consult the peer table.
    if let Some(factory) = lookup_http_factory::<T>() {
        match peer_for(factory.interface_name) {
            Some(PeerEntry::Http { url }) => {
                let boxed = (factory.construct)(url);
                return Some(
                    *boxed
                        .downcast::<Arc<T>>()
                        .expect("caravan-rpc: HttpAdapterFactory.construct returned wrong type"),
                );
            }
            Some(PeerEntry::Lambda { .. }) => {
                panic!(
                    "caravan-rpc {VERSION}: Lambda dispatch for interface {:?} lands at M7 \
                     (see caravan/docs/development_plan.md).",
                    factory.interface_name
                );
            }
            // Inproc or absent → fall through to local registry lookup.
            _ => {}
        }
    }

    // 2. Local registry lookup. Covers inproc mode + identity-marked
    //    traits + http mode without factory + lambda without factory
    //    (the last two are correctness-acceptable per `client` docs).
    let g = registry().read().expect("caravan-rpc registry poisoned");
    g.get(&TypeId::of::<T>()).map(|entry| {
        entry
            .downcast_ref::<Arc<T>>()
            .expect("caravan-rpc registry type mismatch (internal bug)")
            .clone()
    })
}

/// Whether an impl has been registered for trait object `T`.
///
/// Slightly cheaper than [`try_client`] when the caller doesn't need the impl
/// itself (e.g. health checks). Subject to TOCTOU — prefer `try_client` in
/// dispatch paths.
pub fn is_provided<T: ?Sized + Send + Sync + 'static>() -> bool {
    let g = registry().read().expect("caravan-rpc registry poisoned");
    g.contains_key(&TypeId::of::<T>())
}

/// Reset the registry. Intended for test isolation only; production code
/// should `provide()` once and leave the registry alone for the process
/// lifetime.
#[doc(hidden)]
pub fn __clear_registry_for_tests() {
    let mut g = registry().write().expect("caravan-rpc registry poisoned");
    g.clear();
}
