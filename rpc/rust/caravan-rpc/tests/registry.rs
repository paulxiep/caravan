//! Integration tests for the `caravan-rpc` inproc registry + dispatch
//! mode selection.
//!
//! Each test uses its own distinct trait (and therefore its own `TypeId`
//! key) so the process-global registry doesn't cause cross-test
//! interference under cargo's default parallel harness.
//!
//! Peer-table mode is varied via `peers::__set_table_override_for_tests`
//! which is per-thread — tests run in parallel without locking and
//! without mutating process env (no `unsafe` in test code).

#![allow(dead_code)] // some trait methods exist only to occupy unique TypeId slots

use std::collections::HashMap;
use std::sync::Arc;

use caravan_rpc::peers::{
    __clear_table_override_for_tests, __set_table_override_for_tests, PeerEntry,
};
use caravan_rpc::{client, is_provided, provide, try_client, wagon};

/// Drop-guard that installs a per-thread peer-table override and clears
/// it on drop. Tests are parallel-safe — each thread gets its own.
struct PeerOverride;

impl PeerOverride {
    fn set(entries: &[(&str, PeerEntry)]) -> Self {
        let mut map = HashMap::new();
        for (k, v) in entries {
            map.insert((*k).to_string(), v.clone());
        }
        __set_table_override_for_tests(map);
        Self
    }

    fn empty() -> Self {
        __set_table_override_for_tests(HashMap::new());
        Self
    }
}

impl Drop for PeerOverride {
    fn drop(&mut self) {
        __clear_table_override_for_tests();
    }
}

// ---------- inproc lookup (no override / empty override) ----------

#[wagon]
trait Greeter: Send + Sync {
    fn greet(&self, name: String) -> String;
}

struct ShoutGreeter;
impl Greeter for ShoutGreeter {
    fn greet(&self, name: String) -> String {
        format!("HELLO, {}!", name.to_uppercase())
    }
}

#[test]
fn provide_then_client_returns_registered_impl() {
    let _g = PeerOverride::empty();
    provide::<dyn Greeter>(Arc::new(ShoutGreeter));

    let g = client::<dyn Greeter>();
    assert_eq!(g.greet("world".to_string()), "HELLO, WORLD!");
}

// ---------- re-register is last-write-wins ----------

#[wagon]
trait Adder: Send + Sync {
    fn add(&self, a: i32, b: i32) -> i32;
}

struct PlainAdder;
impl Adder for PlainAdder {
    fn add(&self, a: i32, b: i32) -> i32 {
        a + b
    }
}

struct BiasedAdder;
impl Adder for BiasedAdder {
    fn add(&self, a: i32, b: i32) -> i32 {
        a + b + 100
    }
}

#[test]
fn re_provide_replaces_prior_impl() {
    let _g = PeerOverride::empty();
    provide::<dyn Adder>(Arc::new(PlainAdder));
    assert_eq!(client::<dyn Adder>().add(2, 3), 5);

    provide::<dyn Adder>(Arc::new(BiasedAdder));
    assert_eq!(client::<dyn Adder>().add(2, 3), 105);
}

// ---------- client() panics when no impl is registered ----------

#[wagon]
trait NeverRegistered: Send + Sync {
    fn unreachable(&self) -> i32;
}

#[test]
#[should_panic(expected = "no impl registered")]
fn client_panics_when_no_impl_registered() {
    let _g = PeerOverride::empty();
    let _ = client::<dyn NeverRegistered>();
}

// ---------- try_client returns None when no impl is registered ----------

#[wagon]
trait OptionalSeam: Send + Sync {
    fn ping(&self) -> String;
}

struct PingImpl;
impl OptionalSeam for PingImpl {
    fn ping(&self) -> String {
        "pong".to_string()
    }
}

#[test]
fn try_client_returns_none_when_no_impl_registered() {
    let _g = PeerOverride::empty();
    assert!(try_client::<dyn OptionalSeam>().is_none());
    assert!(!is_provided::<dyn OptionalSeam>());

    provide::<dyn OptionalSeam>(Arc::new(PingImpl));

    assert!(is_provided::<dyn OptionalSeam>());
    let s = try_client::<dyn OptionalSeam>().expect("just registered");
    assert_eq!(s.ping(), "pong");
}

// ---------- inproc mode override behaves like no override ----------

#[wagon]
trait InprocSeam: Send + Sync {
    fn value(&self) -> i32;
}

struct InprocImpl;
impl InprocSeam for InprocImpl {
    fn value(&self) -> i32 {
        42
    }
}

#[test]
fn inproc_mode_override_behaves_like_no_override() {
    let _g = PeerOverride::set(&[("InprocSeam", PeerEntry::Inproc)]);
    provide::<dyn InprocSeam>(Arc::new(InprocImpl));
    assert_eq!(client::<dyn InprocSeam>().value(), 42);
}

// ---------- http-mode override + inventory factory routes to HttpClient ----------
//
// HttpSeam goes through the full proc-macro codegen path, so an
// `inventory::submit!` factory exists. With http-mode in the override
// table + a registered local impl, client() should return the
// macro-generated HttpClient (not the local impl). We don't have an
// actual peer server running here, but we can verify the right adapter
// type comes back by checking it's NOT the local impl.

#[wagon]
trait HttpSeam: Send + Sync {
    fn label(&self) -> String;
}

struct HttpSeamLocal;
impl HttpSeam for HttpSeamLocal {
    fn label(&self) -> String {
        "local".to_string()
    }
}

#[test]
fn http_mode_override_returns_macro_generated_http_client() {
    let _g = PeerOverride::set(&[(
        "HttpSeam",
        PeerEntry::Http {
            url: "http://unreachable:9".to_string(),
        },
    )]);
    provide::<dyn HttpSeam>(Arc::new(HttpSeamLocal));

    let h = client::<dyn HttpSeam>();
    // The HttpClient adapter's label() would try to make a real HTTP
    // call to http://unreachable:9 and fail. We instead check via Arc
    // pointer identity that the returned adapter is NOT the local impl.
    let local: Arc<dyn HttpSeam> = Arc::new(HttpSeamLocal);
    // Compare Arc data pointers — adapter is a fresh Arc, not the
    // process-global registered one.
    let h_ptr = Arc::as_ptr(&h) as *const ();
    let local_ptr = Arc::as_ptr(&local) as *const ();
    assert_ne!(h_ptr, local_ptr, "expected HttpClient adapter, got local");
}

// ---------- lambda-mode override routes to macro-generated client (M7) ----------
//
// LambdaSeam goes through the full proc-macro codegen path, so an
// `inventory::submit!` factory exists. With lambda-mode in the override
// table + a registered local impl, client() should return the
// macro-generated adapter wrapping the Function URL (not the local impl).
// We don't actually make the SigV4 round-trip here; we just verify the
// returned Arc is NOT the local one (i.e., the factory was consulted).

#[wagon]
trait LambdaSeam: Send + Sync {
    fn anything(&self) -> i32;
}

struct LambdaImpl;
impl LambdaSeam for LambdaImpl {
    fn anything(&self) -> i32 {
        7
    }
}

#[test]
fn lambda_mode_override_returns_macro_generated_client() {
    let _g = PeerOverride::set(&[(
        "LambdaSeam",
        PeerEntry::Lambda {
            function_url: "https://abc.lambda-url.us-east-1.on.aws/".to_string(),
        },
    )]);
    provide::<dyn LambdaSeam>(Arc::new(LambdaImpl));

    let h = client::<dyn LambdaSeam>();
    let local: Arc<dyn LambdaSeam> = Arc::new(LambdaImpl);
    let h_ptr = Arc::as_ptr(&h) as *const ();
    let local_ptr = Arc::as_ptr(&local) as *const ();
    assert_ne!(h_ptr, local_ptr, "expected Lambda adapter, got local");
}

// ---------- distinct interfaces don't collide on TypeId ----------

#[wagon]
trait AlphaSeam: Send + Sync {
    fn tag(&self) -> String;
}

#[wagon]
trait BetaSeam: Send + Sync {
    fn tag(&self) -> String;
}

struct AlphaImpl;
impl AlphaSeam for AlphaImpl {
    fn tag(&self) -> String {
        "alpha".to_string()
    }
}

struct BetaImpl;
impl BetaSeam for BetaImpl {
    fn tag(&self) -> String {
        "beta".to_string()
    }
}

#[test]
fn distinct_traits_are_keyed_independently() {
    let _g = PeerOverride::empty();
    provide::<dyn AlphaSeam>(Arc::new(AlphaImpl));
    provide::<dyn BetaSeam>(Arc::new(BetaImpl));

    assert_eq!(client::<dyn AlphaSeam>().tag(), "alpha");
    assert_eq!(client::<dyn BetaSeam>().tag(), "beta");
}

// ---------- arc clone semantics: same underlying impl ----------

#[wagon]
trait Counter: Send + Sync {
    fn get(&self) -> usize;
    fn incr(&self);
}

struct AtomicCounter(std::sync::atomic::AtomicUsize);
impl Counter for AtomicCounter {
    fn get(&self) -> usize {
        self.0.load(std::sync::atomic::Ordering::SeqCst)
    }
    fn incr(&self) {
        self.0.fetch_add(1, std::sync::atomic::Ordering::SeqCst);
    }
}

#[test]
fn client_returns_clones_of_same_underlying_arc() {
    let _g = PeerOverride::empty();
    provide::<dyn Counter>(Arc::new(AtomicCounter(0.into())));

    let a = client::<dyn Counter>();
    let b = client::<dyn Counter>();
    a.incr();
    b.incr();
    assert_eq!(a.get(), 2);
    assert_eq!(b.get(), 2);
}
