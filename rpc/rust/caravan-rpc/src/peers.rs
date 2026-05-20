//! Parse and look up the `CARAVAN_RPC_PEERS` env var.
//!
//! See `docs/poc_rpc_sdk.md` §3 for the env-var contract.
//!
//! Shape:
//! ```json
//! {
//!   "Embedder":   { "mode": "inproc" },
//!   "Billing":    { "mode": "http",   "url": "http://billing:8080" },
//!   "FraudCheck": { "mode": "lambda", "function_url": "https://.../" }
//! }
//! ```
//!
//! The parsed table is cached in a `OnceLock` keyed off the process-start
//! value of the env var. If the var changes after first parse, the cache is
//! stale (intentional — peer table is set once per deploy unit).

use std::cell::RefCell;
use std::collections::HashMap;
use std::sync::OnceLock;

use serde::Deserialize;

/// Per-interface dispatch mode + endpoint.
#[derive(Debug, Clone, PartialEq, Eq)]
pub enum PeerEntry {
    /// Dispatch directly into the local inproc registry. The deploy unit
    /// hosts the impl; `client::<T>()` returns the registered `Arc<T>`.
    Inproc,
    /// Dispatch over HTTP to a peer in another container (compose service,
    /// Fargate task) at `url`. Bearer auth via `CARAVAN_RPC_SHARED_SECRET`.
    Http { url: String },
    /// Dispatch over HTTPS to a Lambda Function URL with SigV4 auth. M7
    /// scope; the SDK still parses this entry but the client adapter
    /// rejects it until M7 lands.
    Lambda { function_url: String },
}

/// One entry as it appears in the JSON env var. `serde` decodes either
/// `{"mode": "inproc"}` or `{"mode": "http", "url": "..."}` etc. depending
/// on the `mode` tag.
#[derive(Debug, Deserialize)]
#[serde(tag = "mode", rename_all = "lowercase")]
enum RawEntry {
    Inproc,
    Http { url: String },
    Lambda { function_url: String },
}

impl From<RawEntry> for PeerEntry {
    fn from(raw: RawEntry) -> Self {
        match raw {
            RawEntry::Inproc => PeerEntry::Inproc,
            RawEntry::Http { url } => PeerEntry::Http { url },
            RawEntry::Lambda { function_url } => PeerEntry::Lambda { function_url },
        }
    }
}

/// Parse a JSON string into the peer table. Returns an empty map when input
/// is empty or `null`. Surfaces parse errors as `serde_json::Error` so
/// callers can wrap into a transport error.
pub fn parse(raw: &str) -> Result<HashMap<String, PeerEntry>, serde_json::Error> {
    let trimmed = raw.trim();
    if trimmed.is_empty() || trimmed == "null" {
        return Ok(HashMap::new());
    }
    let parsed: HashMap<String, RawEntry> = serde_json::from_str(trimmed)?;
    Ok(parsed.into_iter().map(|(k, v)| (k, v.into())).collect())
}

/// Process-global cache of the parsed peer table. Read once at first
/// access; subsequent calls are lock-free.
fn cached_table() -> &'static HashMap<String, PeerEntry> {
    static TABLE: OnceLock<HashMap<String, PeerEntry>> = OnceLock::new();
    TABLE.get_or_init(|| match std::env::var("CARAVAN_RPC_PEERS") {
        Ok(raw) => parse(&raw).unwrap_or_else(|e| {
            // Bad peer-table JSON is a deploy-time bug, not a runtime
            // fall-through. Loud-fail with a clear pointer so the compiler
            // emitter (M2 phase 4) can be debugged from the running process.
            panic!(
                "CARAVAN_RPC_PEERS is not valid JSON ({e}). \
                 Expected shape: {{\"InterfaceName\":{{\"mode\":\"inproc|http|lambda\",...}}}}"
            )
        }),
        Err(_) => HashMap::new(),
    })
}

thread_local! {
    /// Per-thread peer-table override. When `Some(table)`, [`peer_for`]
    /// reads from this map instead of the env-derived cached table.
    /// Intended exclusively for tests — production code never sets this.
    ///
    /// Edition 2024 made `std::env::set_var` unsafe, so tests can't
    /// mutate process env without `unsafe` blocks. This override seam
    /// gives tests a safe-fn path that's also better than the global env
    /// approach (per-thread → tests run in parallel without locking).
    static PEER_TABLE_OVERRIDE: RefCell<Option<HashMap<String, PeerEntry>>> = const { RefCell::new(None) };
}

/// Look up the dispatch mode for an interface name. Returns `None` when
/// the (override or env-derived) table is empty OR when the interface is
/// absent from it (both cases mean inproc per the SDK contract — caller
/// treats `None` as equivalent to `Some(PeerEntry::Inproc)`).
///
/// If a per-thread override is set (see [`__set_table_override_for_tests`]),
/// the override takes priority over the env-derived cached table.
pub fn peer_for(interface: &str) -> Option<PeerEntry> {
    let from_override =
        PEER_TABLE_OVERRIDE.with(|cell| cell.borrow().as_ref().map(|t| t.get(interface).cloned()));
    match from_override {
        Some(maybe_entry) => maybe_entry,
        None => cached_table().get(interface).cloned(),
    }
}

/// Test-only seam: replace the per-thread peer-table override with `table`.
/// Subsequent [`peer_for`] calls on this thread read from it.
///
/// Not exported as part of the stable public API (`#[doc(hidden)]`).
#[doc(hidden)]
pub fn __set_table_override_for_tests(table: HashMap<String, PeerEntry>) {
    PEER_TABLE_OVERRIDE.with(|cell| {
        *cell.borrow_mut() = Some(table);
    });
}

/// Test-only seam: clear the per-thread override so [`peer_for`] falls
/// back to the env-derived table again.
#[doc(hidden)]
pub fn __clear_table_override_for_tests() {
    PEER_TABLE_OVERRIDE.with(|cell| {
        *cell.borrow_mut() = None;
    });
}

/// Helper used by the dispatcher when constructing HTTP requests. Reads
/// `CARAVAN_RPC_SHARED_SECRET` lazily at call time (not cached — the
/// secret may rotate, and the env-var read is cheap).
pub fn shared_secret() -> Option<String> {
    std::env::var("CARAVAN_RPC_SHARED_SECRET").ok()
}

/// Test-only helper. Re-reads the env var into a fresh table and returns it
/// without affecting the process-global cache. Used by unit tests that
/// vary `CARAVAN_RPC_PEERS` per test case.
#[doc(hidden)]
pub fn parse_from_env() -> HashMap<String, PeerEntry> {
    match std::env::var("CARAVAN_RPC_PEERS") {
        Ok(raw) => parse(&raw).unwrap_or_default(),
        Err(_) => HashMap::new(),
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn parse_empty_string_yields_empty_map() {
        let t = parse("").unwrap();
        assert!(t.is_empty());
    }

    #[test]
    fn parse_null_yields_empty_map() {
        let t = parse("null").unwrap();
        assert!(t.is_empty());
    }

    #[test]
    fn parse_inproc_entry() {
        let raw = r#"{"Embedder":{"mode":"inproc"}}"#;
        let t = parse(raw).unwrap();
        assert_eq!(t.get("Embedder"), Some(&PeerEntry::Inproc));
    }

    #[test]
    fn parse_http_entry() {
        let raw = r#"{"Embedder":{"mode":"http","url":"http://embedder:8080"}}"#;
        let t = parse(raw).unwrap();
        assert_eq!(
            t.get("Embedder"),
            Some(&PeerEntry::Http {
                url: "http://embedder:8080".into()
            })
        );
    }

    #[test]
    fn parse_lambda_entry() {
        let raw = r#"{"FraudCheck":{"mode":"lambda","function_url":"https://x.lambda-url.us-east-1.on.aws/"}}"#;
        let t = parse(raw).unwrap();
        assert_eq!(
            t.get("FraudCheck"),
            Some(&PeerEntry::Lambda {
                function_url: "https://x.lambda-url.us-east-1.on.aws/".into()
            })
        );
    }

    #[test]
    fn parse_mixed_modes() {
        let raw = r#"{
            "Embedder": {"mode": "inproc"},
            "Billing": {"mode": "http", "url": "http://billing:8080"},
            "FraudCheck": {"mode": "lambda", "function_url": "https://abc.lambda-url.us-east-1.on.aws/"}
        }"#;
        let t = parse(raw).unwrap();
        assert_eq!(t.len(), 3);
        assert!(matches!(t.get("Embedder"), Some(PeerEntry::Inproc)));
        assert!(matches!(t.get("Billing"), Some(PeerEntry::Http { .. })));
        assert!(matches!(
            t.get("FraudCheck"),
            Some(PeerEntry::Lambda { .. })
        ));
    }

    #[test]
    fn parse_rejects_unknown_mode() {
        let raw = r#"{"X":{"mode":"telepathy"}}"#;
        assert!(parse(raw).is_err());
    }

    #[test]
    fn parse_rejects_http_missing_url() {
        let raw = r#"{"X":{"mode":"http"}}"#;
        assert!(parse(raw).is_err());
    }
}
