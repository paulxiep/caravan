//! Procedural macros for the Caravan RPC SDK.
//!
//! At B0p this crate exports a single attribute macro, [`wagon`], as an
//! **identity** — it returns the annotated item unchanged. The visual surface
//! exists so user code can declare seam traits with the same `#[wagon]` shape
//! the functional SDK (M2) will eventually expand. When the proc-macro grows
//! up at M2 it will emit server + client adapter code alongside the trait
//! without any source change at the call site.
//!
//! Users should depend on `caravan-rpc` rather than this crate directly;
//! `caravan-rpc` re-exports `wagon` so a single `use caravan_rpc::wagon;`
//! suffices.

#![forbid(unsafe_code)]

use proc_macro::TokenStream;

/// Mark a trait as a Caravan RPC seam interface.
///
/// At present this attribute is the identity — it returns its input unchanged.
/// The annotation serves as a stable visual marker so seam declarations match
/// the M2 SDK surface byte-for-byte; the proc-macro will gain a real
/// implementation once the wire codec for Rust is designed.
#[proc_macro_attribute]
pub fn wagon(_attrs: TokenStream, item: TokenStream) -> TokenStream {
    item
}
