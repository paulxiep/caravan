//! Integration tests for the `caravan-rpc` inproc registry + dispatch gating.
//!
//! Each test uses its own distinct trait (and therefore its own `TypeId` key)
//! so the process-global registry doesn't cause cross-test interference under
//! cargo's default parallel test harness. Tests that touch the
//! `CARAVAN_RPC_PEERS` env var serialize themselves via [`ENV_LOCK`].

#![allow(dead_code)] // some trait methods exist only to occupy unique TypeId slots

use std::sync::{Arc, Mutex, MutexGuard};

use caravan_rpc::{client, is_provided, provide, try_client, wagon};

/// Serializes tests that read or write `CARAVAN_RPC_PEERS`. Tests that only
/// touch the registry (no env interaction) can run in parallel because each
/// uses a unique trait type.
static ENV_LOCK: Mutex<()> = Mutex::new(());

struct EnvVarGuard<'a> {
    _lock: MutexGuard<'a, ()>,
    key: &'static str,
    prior: Option<String>,
}

impl<'a> EnvVarGuard<'a> {
    fn set(key: &'static str, value: &str) -> Self {
        let lock = ENV_LOCK.lock().unwrap_or_else(|p| p.into_inner());
        let prior = std::env::var(key).ok();
        // SAFETY: tests run single-threaded with respect to this env var by
        // way of ENV_LOCK; outside-process readers are not part of the test
        // contract.
        std::env::set_var(key, value);
        Self {
            _lock: lock,
            key,
            prior,
        }
    }

    fn unset(key: &'static str) -> Self {
        let lock = ENV_LOCK.lock().unwrap_or_else(|p| p.into_inner());
        let prior = std::env::var(key).ok();
        std::env::remove_var(key);
        Self {
            _lock: lock,
            key,
            prior,
        }
    }
}

impl Drop for EnvVarGuard<'_> {
    fn drop(&mut self) {
        match &self.prior {
            Some(v) => std::env::set_var(self.key, v),
            None => std::env::remove_var(self.key),
        }
    }
}

// ---------- inproc lookup (env-unset) ----------

#[wagon]
trait Greeter: Send + Sync {
    fn greet(&self, name: &str) -> String;
}

struct ShoutGreeter;
impl Greeter for ShoutGreeter {
    fn greet(&self, name: &str) -> String {
        format!("HELLO, {}!", name.to_uppercase())
    }
}

#[test]
fn provide_then_client_returns_registered_impl() {
    let _g = EnvVarGuard::unset("CARAVAN_RPC_PEERS");
    provide::<dyn Greeter>(Arc::new(ShoutGreeter));

    let g = client::<dyn Greeter>();
    assert_eq!(g.greet("world"), "HELLO, WORLD!");
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
    let _g = EnvVarGuard::unset("CARAVAN_RPC_PEERS");
    provide::<dyn Adder>(Arc::new(PlainAdder));
    assert_eq!(client::<dyn Adder>().add(2, 3), 5);

    provide::<dyn Adder>(Arc::new(BiasedAdder));
    assert_eq!(client::<dyn Adder>().add(2, 3), 105);
}

// ---------- client() panics when no impl is registered ----------

#[wagon]
trait NeverRegistered: Send + Sync {
    fn unreachable(&self);
}

#[test]
#[should_panic(expected = "no impl registered")]
fn client_panics_when_no_impl_registered() {
    let _g = EnvVarGuard::unset("CARAVAN_RPC_PEERS");
    let _ = client::<dyn NeverRegistered>();
}

// ---------- try_client returns None when no impl is registered ----------

#[wagon]
trait OptionalSeam: Send + Sync {
    fn ping(&self) -> &'static str;
}

struct PingImpl;
impl OptionalSeam for PingImpl {
    fn ping(&self) -> &'static str {
        "pong"
    }
}

#[test]
fn try_client_returns_none_when_no_impl_registered() {
    let _g = EnvVarGuard::unset("CARAVAN_RPC_PEERS");
    assert!(try_client::<dyn OptionalSeam>().is_none());
    assert!(!is_provided::<dyn OptionalSeam>());

    provide::<dyn OptionalSeam>(Arc::new(PingImpl));

    assert!(is_provided::<dyn OptionalSeam>());
    let s = try_client::<dyn OptionalSeam>().expect("just registered");
    assert_eq!(s.ping(), "pong");
}

// ---------- inproc mode in CARAVAN_RPC_PEERS behaves like env-unset ----------

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
fn inproc_mode_env_behaves_like_env_unset() {
    let _g = EnvVarGuard::set(
        "CARAVAN_RPC_PEERS",
        "{\"InprocSeam\":{\"mode\":\"inproc\"}}",
    );
    provide::<dyn InprocSeam>(Arc::new(InprocImpl));
    assert_eq!(client::<dyn InprocSeam>().value(), 42);
}

// ---------- http mode panics with M2 pointer ----------

#[wagon]
trait HttpSeam: Send + Sync {
    fn anything(&self);
}

struct HttpImpl;
impl HttpSeam for HttpImpl {
    fn anything(&self) {}
}

#[test]
#[should_panic(expected = "HTTP dispatch lands at M2")]
fn http_mode_env_panics_with_m2_pointer() {
    let _g = EnvVarGuard::set(
        "CARAVAN_RPC_PEERS",
        "{\"HttpSeam\":{\"mode\":\"http\",\"url\":\"http://peer:8080\"}}",
    );
    provide::<dyn HttpSeam>(Arc::new(HttpImpl));
    let _ = client::<dyn HttpSeam>();
}

// ---------- lambda mode panics with M7 pointer ----------

#[wagon]
trait LambdaSeam: Send + Sync {
    fn anything(&self);
}

struct LambdaImpl;
impl LambdaSeam for LambdaImpl {
    fn anything(&self) {}
}

#[test]
#[should_panic(expected = "Lambda dispatch lands at M7")]
fn lambda_mode_env_panics_with_m7_pointer() {
    let _g = EnvVarGuard::set(
        "CARAVAN_RPC_PEERS",
        "{\"LambdaSeam\":{\"mode\":\"lambda\",\"function_url\":\"https://x.lambda-url.us-east-1.on.aws/\"}}",
    );
    provide::<dyn LambdaSeam>(Arc::new(LambdaImpl));
    let _ = client::<dyn LambdaSeam>();
}

// ---------- distinct interfaces don't collide on TypeId ----------

#[wagon]
trait AlphaSeam: Send + Sync {
    fn tag(&self) -> &'static str;
}

#[wagon]
trait BetaSeam: Send + Sync {
    fn tag(&self) -> &'static str;
}

struct AlphaImpl;
impl AlphaSeam for AlphaImpl {
    fn tag(&self) -> &'static str {
        "alpha"
    }
}

struct BetaImpl;
impl BetaSeam for BetaImpl {
    fn tag(&self) -> &'static str {
        "beta"
    }
}

#[test]
fn distinct_traits_are_keyed_independently() {
    let _g = EnvVarGuard::unset("CARAVAN_RPC_PEERS");
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
    let _g = EnvVarGuard::unset("CARAVAN_RPC_PEERS");
    provide::<dyn Counter>(Arc::new(AtomicCounter(0.into())));

    let a = client::<dyn Counter>();
    let b = client::<dyn Counter>();
    a.incr();
    b.incr();
    assert_eq!(a.get(), 2);
    assert_eq!(b.get(), 2);
}
