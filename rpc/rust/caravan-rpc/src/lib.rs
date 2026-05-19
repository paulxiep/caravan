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
//! # B0p scope
//!
//! This release supports the **inproc** dispatch mode only: the local impl
//! registered via [`provide`] is returned as an `Arc<dyn T>` by [`client`].
//! HTTP (M2) and Lambda (M7) dispatch panic with a `TODO` message — the seam
//! declarations and call sites are forward-compatible, but the wire plumbing
//! is not yet implemented.
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

use std::any::{Any, TypeId};
use std::collections::HashMap;
use std::sync::{Arc, OnceLock, RwLock};

pub use caravan_rpc_macros::wagon;

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

/// Return the registered `Arc<dyn T>` for trait object `T`.
///
/// **No-config inertness**: when `CARAVAN_RPC_PEERS` is unset, this is a
/// direct registry lookup — zero wrapping, zero overhead beyond an `Arc`
/// clone.
///
/// Panics if no impl is registered for `T`. Use [`try_client`] to handle
/// optional seams (e.g. a reranker that may be disabled in config).
///
/// At B0p, any non-inproc dispatch mode in `CARAVAN_RPC_PEERS` causes a
/// panic — the wire plumbing for `http` lands at M2 and `lambda` at M7.
pub fn client<T: ?Sized + Send + Sync + 'static>() -> Arc<T> {
    panic_if_non_inproc_dispatch_configured();
    try_client::<T>().unwrap_or_else(|| {
        panic!(
            "no impl registered for type {}; call provide::<{}>(Arc::new(impl)) at startup",
            std::any::type_name::<T>(),
            std::any::type_name::<T>()
        )
    })
}

/// Return the registered `Arc<dyn T>` for trait object `T`, or `None` if no
/// impl has been registered.
///
/// Use this for seams that are conditionally enabled at runtime (e.g. an
/// optional reranker). For seams that must always be present, prefer the
/// panicking [`client`] for a clearer error message at startup.
pub fn try_client<T: ?Sized + Send + Sync + 'static>() -> Option<Arc<T>> {
    panic_if_non_inproc_dispatch_configured();
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

/// At B0p the only supported dispatch is inproc. If `CARAVAN_RPC_PEERS` is
/// set and contains an `http` or `lambda` mode entry, abort with a clear
/// pointer at the milestone that will land the dispatch path.
///
/// A full peer-table parse + per-interface mode lookup is M2 work; this
/// coarse string check is sufficient to fail loudly without committing to
/// the JSON schema before then.
fn panic_if_non_inproc_dispatch_configured() {
    let Ok(raw) = std::env::var("CARAVAN_RPC_PEERS") else {
        return;
    };
    if raw.contains("\"http\"") {
        panic!(
            "caravan-rpc {VERSION} stub: HTTP dispatch lands at M2 (see caravan/docs/development_plan.md). \
             CARAVAN_RPC_PEERS = {raw:?}"
        );
    }
    if raw.contains("\"lambda\"") {
        panic!(
            "caravan-rpc {VERSION} stub: Lambda dispatch lands at M7 (see caravan/docs/development_plan.md). \
             CARAVAN_RPC_PEERS = {raw:?}"
        );
    }
}

/// Reset the registry. Intended for test isolation only; production code
/// should `provide()` once and leave the registry alone for the process
/// lifetime.
#[doc(hidden)]
pub fn __clear_registry_for_tests() {
    let mut g = registry().write().expect("caravan-rpc registry poisoned");
    g.clear();
}
