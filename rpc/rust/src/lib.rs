//! Pre-release placeholder for the Caravan Rust SDK.
//!
//! Runtime SDK for the [Caravan](https://github.com/paulxiep/caravan)
//! application-definition compiler.
//!
//! The functional SDK lands at `0.1.0`. This `0.0.1` release reserves the
//! crates.io name and provides import-compatible no-op stubs so SDK-wrapped
//! code does not fail to build.
//!
//! See <https://github.com/paulxiep/caravan/blob/main/docs/poc_rpc_sdk.md>
//! for the SDK spec.

#![forbid(unsafe_code)]

/// Version of this placeholder crate.
pub const VERSION: &str = env!("CARGO_PKG_VERSION");

const PLACEHOLDER_MSG: &str =
    "caravan-rpc 0.0.1 is a pre-release placeholder. \
     The functional SDK lands at 0.1.0; see https://github.com/paulxiep/caravan.";

/// Pre-release no-op.
///
/// The real `#[wagon]` will be an attribute macro that synthesizes server +
/// client adapters for a trait. In `0.0.1` this function exists only so the
/// crate has a public symbol.
pub fn wagon() {}

/// Pre-release no-op.
///
/// The real `provide::<dyn T>(impl)` registers the impl for the trait in the
/// SDK's inproc registry. In `0.0.1` this function exists only as a stub.
pub fn provide() {}

/// Pre-release stub that panics.
///
/// The real `client::<dyn T>()` returns a dispatcher proxy reading
/// `CARAVAN_RPC_PEERS` from env. In `0.0.1` calling this panics — the SDK
/// is not yet functional.
pub fn client() -> ! {
    panic!("{}", PLACEHOLDER_MSG);
}
