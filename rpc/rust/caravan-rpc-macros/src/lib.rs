//! Procedural macros for the Caravan RPC SDK.
//!
//! The single attribute macro `#[wagon]` marks a trait as a seam interface.
//! Behaviour depends on the trait shape:
//!
//! * **Sync trait** (no `#[async_trait]`, no `async fn`) — args may be
//!   owned (`String`, `Vec<T>`) or borrowed in the narrow set the macro
//!   knows how to lower (`&str` → `String`, `&[T]` → `Vec<T>`, `&[&str]`
//!   → `Vec<String>`). Emits `<Trait>HttpClient` + `impl <Trait> for
//!   <Trait>HttpClient` (calls go over HTTP via `dispatch::dispatch_sync`)
//!   + `build_<trait_snake>_router(impl_arc)` axum router builder.
//!
//! * **Anything else** (async-trait, async fn, exotic arg types) → expand
//!   to the trait unchanged (identity behaviour, same as the B0p macro).
//!   Async-trait support lands later in M2 Session 4.
//!
//! Code-rag's traits at Session 4-narrow:
//! * Embedder (sync + `&str` + `&[&str]`) → full codegen, suitable for
//!   `dev-split-light`'s `Embedder: container` mode flip.
//! * Reranker (sync + `&str` + `Vec<String>`) → would be full codegen
//!   except for the third-party `fastembed::RerankResult` return type
//!   lacking serde; left identity until M5.
//! * LlmClient + VectorReader (`#[async_trait]`) → identity until Session
//!   4-async lands.

#![forbid(unsafe_code)]

use proc_macro::TokenStream;
use proc_macro2::TokenStream as TokenStream2;
use quote::{format_ident, quote};
use syn::{FnArg, ItemTrait, ReturnType, TraitItem, TraitItemFn, Type, parse_macro_input};

/// Result of analyzing a method arg's type — what owned form to decode it
/// as on the server, and how to re-borrow when calling the impl method.
struct ArgLowering {
    /// Owned form used as the local variable type in the server handler.
    owned_ty: TokenStream2,
    /// Expression used when calling the impl method. Either `name` for
    /// pass-by-value, `&name` for `&T`, or a custom expression like
    /// `&borrowed` for the `&[&str]` case.
    call_expr: TokenStream2,
    /// Optional extra binding emitted *before* the impl call. Used by
    /// `&[&str]` to build `Vec<&str>` from the decoded `Vec<String>`.
    extra_binding: Option<TokenStream2>,
}

/// Mark a trait as a Caravan RPC seam interface.
///
/// Accepts one optional argument:
/// * `#[wagon]` — default: full HTTP codegen if the trait shape is
///   supported (sync, supported arg/return types). Otherwise identity.
/// * `#[wagon(identity)]` — explicit opt-out: emit identity regardless.
///   Use for traits whose types aren't yet wire-ready (e.g.,
///   third-party non-serde return types like `fastembed::RerankResult`).
///   Transitional — the goal is to remove this flag once all wagon
///   traits in the project are wire-ready.
#[proc_macro_attribute]
pub fn wagon(attrs: TokenStream, item: TokenStream) -> TokenStream {
    let item_clone = item.clone();

    // Parse opt-out attribute: `#[wagon(identity)]`.
    let attrs2: TokenStream2 = attrs.into();
    let identity_opt_out = attrs2.to_string().trim() == "identity";

    if identity_opt_out {
        return item_clone;
    }

    let parsed = parse_macro_input!(item as ItemTrait);

    let Some(mode) = classify_trait(&parsed) else {
        // Fallback: identity expansion. The trait is emitted unchanged.
        return item_clone;
    };

    match expand_trait(&parsed, mode) {
        Ok(ts) => ts.into(),
        Err(e) => e.to_compile_error().into(),
    }
}

/// Trait shape recognized by the macro for full HTTP codegen.
#[derive(Clone, Copy, PartialEq, Eq)]
enum TraitMode {
    Sync,
    Async,
}

/// Classify a trait. Returns `Some(Sync)` if every method is sync and
/// types lower correctly; `Some(Async)` if `#[async_trait]` is present
/// (every method then expected to be `async fn`) and types lower
/// correctly; `None` otherwise (identity fallback).
fn classify_trait(item: &ItemTrait) -> Option<TraitMode> {
    let has_async_trait_attr = item.attrs.iter().any(|a| a.path().is_ident("async_trait"));

    let mut all_methods_async = true;
    let mut any_method_async = false;

    for trait_item in &item.items {
        let TraitItem::Fn(m) = trait_item else {
            continue;
        };

        if m.sig.asyncness.is_some() {
            any_method_async = true;
        } else {
            all_methods_async = false;
        }

        // Every arg type must be lowerable.
        for input in &m.sig.inputs {
            let FnArg::Typed(pat_type) = input else {
                continue;
            };
            let pat = quote! { __dummy };
            lower_arg_type(&pat_type.ty, &pat)?;
        }

        // No borrowed types in return.
        if let ReturnType::Type(_, ty) = &m.sig.output
            && contains_reference(ty)
        {
            return None;
        }
    }

    // Decide sync vs async. The wire dispatcher (`dispatch_sync` vs
    // `dispatch_async`) is selected per the whole trait — mixing sync
    // and async methods in one #[wagon] trait isn't supported.
    if has_async_trait_attr || any_method_async {
        if !all_methods_async {
            // Mixed shape — bail to identity.
            return None;
        }
        Some(TraitMode::Async)
    } else {
        Some(TraitMode::Sync)
    }
}

/// Decide how to decode a method arg from the wire and how to pass it to
/// the impl method. Returns `None` for shapes the Session-4-narrow macro
/// doesn't support (e.g., `&CustomStruct`, function pointers).
///
/// Supported lowerings:
/// * `&str` → decode `String`, call `&name`
/// * `&[&str]` → decode `Vec<String>`, then build `Vec<&str>`, call `&name_ref`
/// * `&[T]` (T owned) → decode `Vec<T>`, call `&name`
/// * Otherwise (owned T) → decode `T`, call `name`
fn lower_arg_type(ty: &Type, name: &TokenStream2) -> Option<ArgLowering> {
    if let Type::Reference(r) = ty {
        let inner = &*r.elem;
        // `&str` case.
        if is_str_path(inner) {
            return Some(ArgLowering {
                owned_ty: quote! { ::std::string::String },
                call_expr: quote! { &#name },
                extra_binding: None,
            });
        }
        // `&[T]` case — the referenced type is a slice.
        if let Type::Slice(slice) = inner {
            // Special-case `&[&str]` → decode Vec<String>, build Vec<&str>.
            if let Type::Reference(inner_ref) = &*slice.elem
                && is_str_path(&inner_ref.elem)
            {
                let borrowed_ident =
                    format_ident!("__caravan_{}_borrowed", name.to_string().replace(' ', ""));
                return Some(ArgLowering {
                    owned_ty: quote! { ::std::vec::Vec<::std::string::String> },
                    call_expr: quote! { &#borrowed_ident },
                    extra_binding: Some(quote! {
                        let #borrowed_ident: ::std::vec::Vec<&str> =
                            #name.iter().map(::std::string::String::as_str).collect();
                    }),
                });
            }
            // `&[T]` where T isn't itself a reference. The owned form is
            // `Vec<T>`, and we pass it via `&name` (deref coerces to `&[T]`).
            let elem_ty = &slice.elem;
            if !contains_reference(elem_ty) {
                return Some(ArgLowering {
                    owned_ty: quote! { ::std::vec::Vec<#elem_ty> },
                    call_expr: quote! { &#name },
                    extra_binding: None,
                });
            }
            return None;
        }
        return None;
    }
    // Owned type — no borrow logic needed. The owned form is the type
    // itself; the call expression is just the name.
    if contains_reference(ty) {
        // e.g., `Vec<&str>` as an owned-looking type that nevertheless
        // borrows — can't deserialize without lifetimes.
        return None;
    }
    Some(ArgLowering {
        owned_ty: quote! { #ty },
        call_expr: quote! { #name },
        extra_binding: None,
    })
}

/// Whether a type is `str` (the unsized variant of `&str`).
fn is_str_path(ty: &Type) -> bool {
    if let Type::Path(p) = ty
        && p.qself.is_none()
        && let Some(last) = p.path.segments.last()
    {
        return last.ident == "str";
    }
    false
}

/// Recursively check whether a type contains a reference (`&T`, `&mut T`).
/// We only descend into generic arguments via `Type::Path` since that's the
/// common case (`Result<&str, _>`, `Vec<&[u8]>`, etc.); other oddball types
/// (function pointers, trait objects with explicit lifetimes) are rare in
/// seam trait signatures and not worth handling at Session 3.
fn contains_reference(ty: &Type) -> bool {
    match ty {
        Type::Reference(_) => true,
        Type::Slice(_) => true,
        Type::Array(arr) => contains_reference(&arr.elem),
        Type::Tuple(t) => t.elems.iter().any(contains_reference),
        Type::Path(path) => {
            for segment in &path.path.segments {
                if let syn::PathArguments::AngleBracketed(args) = &segment.arguments {
                    for arg in &args.args {
                        if let syn::GenericArgument::Type(inner) = arg
                            && contains_reference(inner)
                        {
                            return true;
                        }
                    }
                }
            }
            false
        }
        Type::Paren(p) => contains_reference(&p.elem),
        Type::Group(g) => contains_reference(&g.elem),
        _ => false,
    }
}

/// Expand a wagon trait into trait + HttpClient + router builder.
/// Behavior varies by `mode`:
/// * Sync — `impl Trait for <Trait>HttpClient { fn ... }` using `dispatch_sync`.
/// * Async — `#[async_trait] impl Trait for <Trait>HttpClient { async fn ... }` using `dispatch_async`.
fn expand_trait(item: &ItemTrait, mode: TraitMode) -> syn::Result<TokenStream2> {
    let trait_ident = &item.ident;
    let vis = &item.vis;
    let interface_str = trait_ident.to_string();
    let client_struct = format_ident!("{}HttpClient", trait_ident);
    let router_fn = format_ident!("build_{}_router", to_snake_case(&interface_str));

    let mut client_methods: Vec<TokenStream2> = Vec::new();
    let mut handler_bindings: Vec<TokenStream2> = Vec::new();
    let mut router_chain: Vec<TokenStream2> = Vec::new();

    for trait_item in &item.items {
        let TraitItem::Fn(m) = trait_item else {
            continue;
        };
        client_methods.push(emit_client_method(m, &interface_str, mode)?);
        let (binding, method_str) = emit_server_handler(m, trait_ident, mode)?;
        handler_bindings.push(binding);
        let handler_ident = format_ident!("__caravan_handler_{}", method_str);
        router_chain.push(quote! { .add_method(#method_str, #handler_ident) });
    }

    // For async traits the impl needs `#[async_trait::async_trait]` so
    // each `async fn` becomes a regular `fn -> Pin<Box<...>>`. We pull
    // the macro from the `__macro_support` re-export so the user
    // doesn't need an explicit `async-trait` dep.
    let async_trait_attr = match mode {
        TraitMode::Sync => quote! {},
        TraitMode::Async => quote! { #[::caravan_rpc::__macro_support::async_trait::async_trait] },
    };

    let out = quote! {
        // Original trait, emitted unchanged.
        #item

        // HTTP-client adapter: dispatches each method call over the wire.
        #vis struct #client_struct {
            base_url: ::std::string::String,
        }

        impl #client_struct {
            #vis fn new(base_url: impl ::std::convert::Into<::std::string::String>) -> Self {
                Self { base_url: base_url.into() }
            }
        }

        #async_trait_attr
        impl #trait_ident for #client_struct {
            #(#client_methods)*
        }

        // Builder: wraps a registered impl into an axum Router for the peer
        // service to serve. Reads CARAVAN_RPC_SHARED_SECRET at call time so
        // the bearer-auth check matches what the client side sends.
        #vis fn #router_fn(
            impl_arc: ::std::sync::Arc<dyn #trait_ident>,
        ) -> ::caravan_rpc::__macro_support::axum::Router {
            #(#handler_bindings)*
            ::caravan_rpc::server::RpcRouter::new(#interface_str)
                #(#router_chain)*
                .into_axum_router(::caravan_rpc::peers::shared_secret())
        }

        // Inventory registration: lets `caravan_rpc::client::<dyn Trait>()`
        // discover this trait's HttpClient constructor at runtime when the
        // peer table marks the interface as http-mode.
        ::caravan_rpc::__macro_support::inventory::submit! {
            ::caravan_rpc::HttpAdapterFactory {
                interface_name: #interface_str,
                type_id_fn: || ::std::any::TypeId::of::<dyn #trait_ident>(),
                construct: |__url: ::std::string::String|
                    -> ::std::boxed::Box<dyn ::std::any::Any + ::std::marker::Send + ::std::marker::Sync> {
                    let __adapter: ::std::sync::Arc<dyn #trait_ident> =
                        ::std::sync::Arc::new(#client_struct::new(__url));
                    ::std::boxed::Box::new(__adapter)
                },
            }
        }

        // Server-side inventory registration: lets
        // `caravan_rpc::run_or_serve` discover this trait's server router
        // builder at runtime when CARAVAN_RPC_ROLE=peer-<Trait> is set.
        // The closure does the trait-erased work: registry lookup + router
        // build with the macro-emitted `build_<trait>_router`.
        ::caravan_rpc::__macro_support::inventory::submit! {
            ::caravan_rpc::HttpServerFactory {
                interface_name: #interface_str,
                build_router_from_registry: || {
                    let __impl = ::caravan_rpc::try_client::<dyn #trait_ident>()
                        .ok_or("no provide() call for this trait before run_or_serve")?;
                    Ok(#router_fn(__impl))
                },
            }
        }
    };

    Ok(out)
}

/// Emit one method body for the HttpClient's `impl Trait for` block.
/// Body shape depends on `mode`: sync uses blocking `dispatch_sync`,
/// async uses `dispatch_async(...).await`.
fn emit_client_method(
    m: &TraitItemFn,
    interface: &str,
    mode: TraitMode,
) -> syn::Result<TokenStream2> {
    let sig = &m.sig;
    let method_str = sig.ident.to_string();
    let mut arg_serializations: Vec<TokenStream2> = Vec::new();

    for input in &sig.inputs {
        if let FnArg::Typed(pat_type) = input {
            let pat = &pat_type.pat;
            arg_serializations.push(quote! {
                ::caravan_rpc::__macro_support::serde_json::to_value(&#pat).expect("caravan-rpc: arg serialize")
            });
        }
    }

    let dispatch_call = match mode {
        TraitMode::Sync => quote! {
            ::caravan_rpc::dispatch::dispatch_sync(
                &self.base_url, #interface, #method_str, __args
            ).expect("caravan-rpc: dispatch_sync")
        },
        TraitMode::Async => quote! {
            ::caravan_rpc::dispatch::dispatch_async(
                &self.base_url, #interface, #method_str, __args
            ).await.expect("caravan-rpc: dispatch_async")
        },
    };

    let body = quote! {
        let __args: ::std::vec::Vec<::caravan_rpc::__macro_support::serde_json::Value> = vec![ #(#arg_serializations),* ];
        let __v = #dispatch_call;
        ::caravan_rpc::__macro_support::serde_json::from_value(__v).expect("caravan-rpc: deserialize return")
    };

    let block: syn::Block = syn::parse2(quote! { { #body } })?;
    let mut m = m.clone();
    m.default = Some(block);
    m.semi_token = None;
    Ok(quote! { #m })
}

/// Emit one MethodHandler binding for the server-side router builder.
/// Returns the `let __caravan_handler_<method> = ...;` token stream and
/// the method name (as the string used in path routing + .add_method).
fn emit_server_handler(
    m: &TraitItemFn,
    trait_ident: &syn::Ident,
    mode: TraitMode,
) -> syn::Result<(TokenStream2, String)> {
    let sig = &m.sig;
    let method_ident = &sig.ident;
    let method_str = method_ident.to_string();
    let handler_ident = format_ident!("__caravan_handler_{}", method_str);

    // For each typed arg, emit a decode block (decoding into the OWNED
    // form, even if the trait method takes a borrowed type) plus a call
    // expression that re-borrows where needed. `lower_arg_type` owns this
    // translation; we just call it here.
    let mut decode_blocks: Vec<TokenStream2> = Vec::new();
    let mut call_args: Vec<TokenStream2> = Vec::new();
    let mut idx: usize = 0;
    for input in &sig.inputs {
        if let FnArg::Typed(pat_type) = input {
            let pat = &pat_type.pat;
            let pat_tokens = quote! { #pat };
            let arg_name = pat_tokens.to_string();
            let lowering =
                lower_arg_type(&pat_type.ty, &pat_tokens).expect("is_sync_owned_trait gates this");
            let owned_ty = &lowering.owned_ty;
            let idx_lit = idx;
            let extra = lowering.extra_binding.unwrap_or_default();
            decode_blocks.push(quote! {
                let #pat: #owned_ty = match __env.args.get(#idx_lit) {
                    ::std::option::Option::Some(__val) => {
                        match ::caravan_rpc::__macro_support::serde_json::from_value(__val.clone()) {
                            ::std::result::Result::Ok(__t) => __t,
                            ::std::result::Result::Err(__e) => {
                                return ::caravan_rpc::codec::Response::err(
                                    format!("BadArg({})", #arg_name),
                                    __e.to_string(),
                                );
                            }
                        }
                    }
                    ::std::option::Option::None => {
                        return ::caravan_rpc::codec::Response::err(
                            format!("MissingArg({})", #arg_name),
                            format!("expected args[{}]", #idx_lit),
                        );
                    }
                };
                #extra
            });
            call_args.push(lowering.call_expr);
            idx += 1;
        }
    }

    let impl_call = match mode {
        TraitMode::Sync => quote! {
            <dyn #trait_ident>::#method_ident(&*__impl_arc #(, #call_args)*)
        },
        TraitMode::Async => quote! {
            <dyn #trait_ident>::#method_ident(&*__impl_arc #(, #call_args)*).await
        },
    };

    let body = quote! {
        let #handler_ident: ::caravan_rpc::server::MethodHandler = {
            let __impl_arc = impl_arc.clone();
            ::std::sync::Arc::new(move |__body: ::caravan_rpc::__macro_support::axum::body::Bytes| {
                let __impl_arc = __impl_arc.clone();
                ::std::boxed::Box::pin(async move {
                    let __env: ::caravan_rpc::codec::Request = match ::caravan_rpc::__macro_support::serde_json::from_slice(&__body) {
                        ::std::result::Result::Ok(__e) => __e,
                        ::std::result::Result::Err(__e) => {
                            return ::caravan_rpc::codec::Response::err(
                                "BadJSON",
                                __e.to_string(),
                            );
                        }
                    };
                    #(#decode_blocks)*
                    let __result = #impl_call;
                    match ::caravan_rpc::__macro_support::serde_json::to_value(&__result) {
                        ::std::result::Result::Ok(__v) => ::caravan_rpc::codec::Response::ok(__v),
                        ::std::result::Result::Err(__e) => ::caravan_rpc::codec::Response::err(
                            "EncodeError",
                            __e.to_string(),
                        ),
                    }
                })
            })
        };
    };

    Ok((body, method_str))
}

/// Convert PascalCase / CamelCase to snake_case for the router builder
/// function name (e.g. `Embedder` → `embedder`, `VectorReader` →
/// `vector_reader`).
fn to_snake_case(s: &str) -> String {
    let mut out = String::with_capacity(s.len() + 4);
    for (i, ch) in s.chars().enumerate() {
        if ch.is_uppercase() {
            if i > 0 {
                out.push('_');
            }
            for low in ch.to_lowercase() {
                out.push(low);
            }
        } else {
            out.push(ch);
        }
    }
    out
}
