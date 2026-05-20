# Caravan Rust coding standard

> A prescriptive guide to writing Rust code that adopts Caravan in *hours* rather than *days*. Companion to [`poc_rpc_sdk.md`](poc_rpc_sdk.md) (the wire contract) and [`../PoC-B0p.md`](../PoC-B0p.md) (the empirical adoption record for code-rag).
>
> Rust first because B0p surfaced the steepest friction on the Rust side. Companion docs for Python, TypeScript, and Go will follow once the Rust shape stabilizes.
>
> The original draft was reviewed for Caravan-favoring bias by an impartial session; the scope tags (`[Universal Rust]` / `[Dyn-compat]` / `[Caravan-specific]`) and several of the framing corrections — most notably the R3 rewrite (away from "god struct" pejorative) and the R5 softening (free functions taking `&impl Trait` remain idiomatic Rust) — come from that review.

## Why this exists

Caravan is a [structure tool](../PoC-positioning.md#2-comparative-positioning-in-depth) — like Airflow, Spring, NestJS, or an ORM — meaning it asks for modular code at the inter-component boundary. **This doc is the prescriptive version of "modular enough for Caravan" in Rust.**

Codebases that follow these rules convert to Caravan with minimal restructuring: declare the seam interfaces with `#[wagon]`, register impls with `provide()`, swap call sites to `client::<dyn T>()`. Done in hours.

Codebases that violate the rules pay the conversion cost during the Caravan-adoption branch (this is what happened in B0p). The work is finite and one-time, but it's also tech debt that any DI refactor would have done — Caravan just forces the timing. (*DI = dependency injection*: the pattern where an object's dependencies are passed in from outside rather than constructed inside it; Spring / NestJS / Dagger / Wire are the well-known frameworks. Caravan's `provide()` / `client::<dyn T>()` is a DI container with the added job of routing the dependency to inproc / HTTP / Lambda dispatch.)

**Calibration target**: a codebase scoring ≥ 80 on the [readiness scorecard](#caravan-readiness-scorecard) should convert in under a day. A codebase scoring ≥ 95 should convert in under three hours.

## The ten rules

Each rule has: the rule, an antipattern, the correct pattern, and a one-line "why."

**Provenance:** rules R1–R5 + R8–R9 are directly motivated by B0p's diff in code-rag (each rule fixes a concrete friction source that surfaced during the conversion). Rules R6, R7, and R10 are *preemptive* for M2 — code-rag happened to satisfy them already, but they would fail loudly under the first HTTP/Lambda dispatch attempt. Treat the preemptive rules as cheap insurance.

**Scope tags.** Each rule is tagged with where it draws its authority — so readers can tell which rules are universal Rust hygiene vs. constraints that only bite if you adopt Caravan (or any other `dyn`-trait-based dispatch system):

- **[Universal Rust]** — mainstream Rust API guidance; applies regardless of Caravan. Cross-check with [`rust-api-guidelines`](https://rust-lang.github.io/api-guidelines/).
- **[Dyn-compat]** — required for any trait used as `dyn Trait`, Caravan-adopting or not. See the Rust Reference's [dyn compatibility](https://doc.rust-lang.org/reference/items/traits.html#dyn-compatibility) chapter.
- **[Caravan-specific]** — driven by Caravan's registry/dispatch model; a non-Caravan Rust codebase can ignore these without harm.

### R1. Cross-component dependencies behind `dyn Trait`, not concrete structs &nbsp;`[Caravan-specific]`

**Antipattern**
```rust
struct AppState {
    embedder: Embedder,           // concrete type
    store:    VectorStore,        // concrete type
}
```

**Correct**
```rust
struct AppState {
    embedder: Arc<dyn Embedder>,
    store:    Arc<dyn VectorReader>,
}
// or — better — drop seams from AppState entirely and look up via `client::<dyn T>()`.
```

**Why.** Mainstream Rust prefers generics (`fn handler<E: Embedder>(e: &E)`) when there's one binding per binary — monomorphization, no vtable cost. Reach for `Arc<dyn Embedder>` when you need runtime polymorphism, heterogeneous collections, or to keep generics from infecting every call site. Caravan's registry uses the trait-object form because the same dispatch path serves inproc and HTTP/Lambda transports.

---

### R2. Seam methods are `&self` only &nbsp;`[Universal Rust]`

**Antipattern**
```rust
pub trait Embedder {
    fn embed(&mut self, text: &str) -> Result<Vec<f32>, _>;
}
```

**Correct**
```rust
pub trait Embedder: Send + Sync {
    fn embed(&self, text: &str) -> Result<Vec<f32>, _>;
}
// Interior mutability moves into the impl:
struct FastEmbedImpl { model: Mutex<TextEmbedding> }
impl Embedder for FastEmbedImpl {
    fn embed(&self, text: &str) -> Result<Vec<f32>, _> {
        let mut guard = self.model.lock().map_err(/* ... */)?;
        guard.embed(text)
    }
}
```

**Why.** `client::<dyn T>() -> Arc<T>` returns shared references. `&mut self` isn't compatible. Across the wire (M2 HTTP / M7 Lambda) every request is independent anyway — the lock is purely an inproc optimization.

---

### R3. Shared-state struct carries trait objects, not concrete services &nbsp;`[Caravan-specific]`

**Antipattern**
```rust
struct AppState {
    embedder: Mutex<Embedder>,
    reranker: Option<Mutex<Reranker>>,
    store:    VectorStore,
    llm:      LlmClient,
    classifier: IntentClassifier,
    config:   EngineConfig,
}
```

**Correct**
```rust
// Seam deps live in caravan_rpc's registry; AppState carries only derived/runtime state.
struct AppState {
    classifier: IntentClassifier,
    config:     EngineConfig,
}

async fn handler(State(state): State<Arc<AppState>>) -> Response {
    let embedder = caravan_rpc::client::<dyn Embedder>();
    let result = embedder.embed(&query)?;
    // ...
}
```

**Why.** Mainstream Rust does *not* call a fat `AppState { db, cache, config }` a god struct — every major web framework's own examples look like that. The actual problem for Caravan adoption is **concrete types in the shared-state struct**, not the field count: each concrete seam dep has to either move to the registry or be threaded as `Arc<dyn T>` so it stays swappable. Caravan's `client::<dyn T>()` lookup is one solution; a passed `Context` object is another (both supported at M2).

---

### R4. `!Sync` library backing types are hidden inside the impl &nbsp;`[Universal Rust]`

**Antipattern**
```rust
// fastembed::TextEmbedding is !Sync. Surfacing it at the AppState boundary
// forces every caller to lock.
struct AppState { embedder: Mutex<TextEmbedding> }
```

**Correct**
```rust
struct FastEmbedImpl { model: Mutex<TextEmbedding> }   // !Sync hidden inside

impl Embedder for FastEmbedImpl {
    fn embed(&self, text: &str) -> Result<Vec<f32>, _> {
        let mut guard = self.model.lock()?;
        guard.embed(text)
    }
}
```

**Why.** Callers shouldn't know that a particular backend is `!Sync`. Wrap once, expose a `&self`-only trait. M2 will provide a [`MutexProtected<T>`](#what-caravan-provides-to-meet-you-halfway) helper that turns this pattern into a one-liner.

---

### R5. Seam logic lives in trait methods, not free functions taking trait refs &nbsp;`[Caravan-specific]`

**Antipattern**
```rust
pub mod generator {
    pub async fn generate(prompt: &str, client: &LlmClient) -> Result<String, _> { /* ... */ }
}
// Callers: generator::generate(&prompt, &state.llm).await?
```

**Correct**
```rust
#[wagon]
#[async_trait]
pub trait LlmClient: Send + Sync {
    async fn generate(&self, prompt: &str) -> Result<String, _>;
}
// Callers: caravan_rpc::client::<dyn LlmClient>().generate(&prompt).await?
```

**Why.** Free functions taking `&impl Trait` are *idiomatic* Rust outside this doc — `std::io::copy<R: Read, W: Write>(...)` is the archetype, and you shouldn't refactor them away wholesale. The constraint is narrower: if a particular function is meant to be a Caravan **seam** (swappable via `client::<dyn T>()`), the registry can only look up trait methods, so pull *that* function into the trait. Non-seam helpers can stay where they are.

---

### R6. Wire-friendly types in seam method signatures &nbsp;`[Caravan-specific]`

**Antipattern**
```rust
trait Embedder {
    fn embed_batch<'a>(&self, texts: &'a [&'a str]) -> Vec<Vec<f32>>;   // borrowed lifetimes
    fn raw_bytes(&self) -> &Vec<u8>;                                    // returns borrowed
}
```

**Correct**
```rust
trait Embedder {
    fn embed_batch(&self, texts: &[&str]) -> Vec<Vec<f32>>;             // &[&str] is fine; deref-coercible
    fn snapshot(&self) -> Vec<u8>;                                       // owned return
}
```

Generally: types in seam method signatures must be `Serialize + DeserializeOwned` once you cross the wire. Owned `Vec<…>`, `String`, primitives, structs that implement Serde — fine. Borrowed references with lifetimes that don't survive serialization — not fine.

**Scope note.** This applies to traits that may dispatch over the wire (Caravan seams at M2+). For purely in-process traits, borrowed inputs (`&str`, `&[T]`, `Cow<'_, str>`) remain idiomatic and are *encouraged* by the [API Guidelines](https://rust-lang.github.io/api-guidelines/flexibility.html#caller-decides-where-to-place-data-c-caller-control) (C-CALLER-CONTROL). Don't strip borrows from traits that aren't seams.

**Why.** Inproc mode tolerates anything; HTTP/Lambda mode requires JSON-round-trippable types. Designing for the HTTP case from day one means no signature change at M2.

---

### R7. No `impl Trait` return types in seam methods &nbsp;`[Dyn-compat]`

**Antipattern**
```rust
trait VectorReader {
    fn search(&self) -> impl Iterator<Item = Chunk>;   // not dyn-compatible
}
```

**Correct**
```rust
trait VectorReader {
    fn search(&self) -> Vec<Chunk>;
    // or, if iteration matters:
    fn search(&self) -> Box<dyn Iterator<Item = Chunk> + Send>;
}
```

**Why.** `impl Trait` returns mean the concrete type is opaque at the type level — which kills `dyn Trait` compatibility. Either return a concrete type (`Vec<…>`, `String`) or an explicit boxed trait object. (If a trait is *never* used as `dyn`, return-position impl Trait in traits is stable since Rust 1.75 and often the cleaner choice — this rule only binds dyn-dispatched seams.)

---

### R8. `async-trait` for async seam methods (until native async-fn-in-dyn-trait stabilizes) &nbsp;`[Dyn-compat]`

**Correct**
```rust
#[wagon]
#[async_trait::async_trait]
pub trait VectorReader: Send + Sync {
    async fn search(&self, query: &[f32], limit: usize) -> Result<Vec<Chunk>, _>;
}
```

Pin to the latest `0.1.x`. Note the precise state of the language: `async fn` in traits (AFIT) has been **stable since Rust 1.75 (Dec 2023)** — the remaining gap is *dyn-compatibility* for such traits, which is still WIP. Until that ships, `#[async_trait]` is the only safe path for any trait you want to use as `dyn`.

**Why.** Without `async-trait`, `dyn VectorReader` doesn't compile when the trait has `async fn` methods. The macro emits the necessary `Pin<Box<dyn Future<...> + Send>>` boilerplate. Once dyn-compatible native `async fn` traits stabilize, switch over; treat that as a per-crate semver-minor bump.

---

### R9. Concrete, serializable, recoverable error types on seam methods &nbsp;`[Universal Rust]`

**Antipattern**
```rust
trait LlmClient {
    async fn generate(&self, prompt: &str) -> anyhow::Result<String>;   // anyhow::Error doesn't serialize cleanly
}
```

**Correct**
```rust
#[derive(thiserror::Error, Debug, serde::Serialize, serde::Deserialize)]
pub enum LlmError {
    #[error("LLM generation failed: {0}")]
    Generation(String),
    #[error("LLM rate-limited: retry after {retry_after_secs}s")]
    RateLimited { retry_after_secs: u32 },
}

trait LlmClient {
    async fn generate(&self, prompt: &str) -> Result<String, LlmError>;
}
```

The chat-side / engine-side may still `#[from]`-convert `LlmError` into a broader `EngineError` for handler use. Just don't put `anyhow::Error` *in the seam signature*.

**When an upstream error type doesn't derive Serde** (e.g. `anyhow::Error`, `lancedb::Error`, `arrow_schema::ArrowError`): replace the `#[from] UpstreamError` variant payload with `String` and write an explicit `From` impl that logs the full chain via `tracing::warn!(error = format!("{e:#}"), ...)` and captures `e.to_string()` in the variant. This follows the reqwest / AWS-SDK / gRPC `Status` convention — full diagnostic on the producing side, top-level message on the wire. The `?`-operator continues to work at call sites because the explicit `From` impl preserves the conversion.

**Why.** Seam errors cross the wire in HTTP/Lambda modes. `anyhow::Error` is type-erased — can't be matched on, can't be reliably serialized. Domain-specific error enums survive transit, let callers branch on `match`, and stay greppable. This matches the conventional Rust split: **`thiserror` for library errors, `anyhow` for application top-level**; seams are library boundaries, so they fall on the `thiserror` side.

---

### R10. No generic methods on seam traits &nbsp;`[Dyn-compat]`

**Antipattern**
```rust
trait Cache {
    fn get<K: Hash + Eq, V: DeserializeOwned>(&self, key: K) -> Option<V>;
}
```

**Correct**
```rust
trait Cache {
    fn get_bytes(&self, key: &str) -> Option<Vec<u8>>;
    // or specialize per concrete type set actually used:
    fn get_chunk(&self, key: &str) -> Option<Chunk>;
}
```

Same rule applies to generic trait parameters (`trait Foo<T>`) — those also break `dyn` compatibility. Keep seams monomorphic.

**Why.** Generic methods require monomorphization, which is impossible behind a trait object. The trait object has *one* vtable; it can't carry a different vtable per call-site type instantiation. The standard-library escape hatch when you need both is to split a `dyn`-friendly core trait from a generic extension trait (the `Read` / `ReadExt` pattern) — apply the same shape to seams if you have to.

---

## Pre-Caravan checklist

Before starting a Caravan-conversion branch, audit your codebase against R1–R10. For each rule, count violations:

```
R1 violations: ___  (each concrete cross-component dep in AppState or fn signatures)
R2 violations: ___  (each &mut self method on a candidate seam trait)
R3 violations: ___  (concrete seam-dep fields in shared state that would move to the registry)
R4 violations: ___  (!Sync types exposed at the boundary instead of inside the impl)
R5 violations: ___  (module-level functions that should be methods on a seam trait)
R6 violations: ___  (signatures with non-Serde-friendly types)
R7 violations: ___  (`impl Trait` returns on candidate seam methods)
R8 violations: ___  (async methods without async_trait wrapper)
R9 violations: ___  (seam errors using anyhow / Box<dyn Error>)
R10 violations: ___ (generic methods on candidate seam traits)
```

Total violations predict adoption effort. Calibration from code-rag's B0p:

| Total violations | Predicted adoption effort | Example |
|---|---|---|
| 0 – 5 | A few hours | Already-DI-shaped Rust codebase |
| 6 – 15 | 1 day (one session) | Light AppState refactor + a couple of trait extractions |
| 16 – 30 | 2–3 days (B0p territory) | code-rag started here |
| 30 + | 5+ days; consider a DI refactor before Caravan adoption | Many concrete seam deps in shared state + `&mut self` trait shapes + free-function-heavy across component boundaries |

## Caravan-readiness scorecard

A complementary view: a 0–100 score, with violations weighted by how blocking they are.

```
Start at 100.
Subtract for each violation:
  - R1 (concrete dep in AppState):           -3 each
  - R2 (&mut self on seam trait method):     -5 each
  - R3 (concrete seam-dep field in shared state):  -2 each
  - R4 (!Sync type at boundary):             -3 each
  - R5 (module-level fn that's a seam):      -2 each
  - R6 (non-Serde signature):                -4 each
  - R7 (impl Trait return on seam):          -3 each
  - R8 (async seam without async_trait):     -2 each
  - R9 (anyhow in seam signature):           -2 each
  - R10 (generic method on seam trait):      -5 each
Clamp to [0, 100].
```

**code-rag pre-B0p calibration**: 4 concrete cross-component deps in AppState (R1: -12), 3 `&mut self` methods on candidate seam traits — `embed_one`, `embed_batch`, `rerank` (R2: -15), 4 concrete seam-dep fields in shared state that move to the registry — `embedder`, `reranker`, `store`, `llm` (R3: -8), 2 `!Sync` exposures — fastembed's `TextEmbedding` and `TextRerank` (R4: -6), 1 module-level function that should be a seam method — `engine::generator::generate` (R5: -2), 0 others. Score = 100 - 43 = **57**. Adoption took 3 sessions, which matches the "16–30 total violations" band in the checklist above. The published readiness rating (HIGH ~80%) was *forward-looking* (predicted M5 outcome); the scorecard above measures *current shape*.

Score interpretation:
- **90+**: Caravan-ready. Convert in hours.
- **75–89**: Lightly Caravan-shaped. One session.
- **50–74**: Standard mid-size Rust codebase. 2–3 sessions (B0p band).
- **< 50**: Significant DI refactor recommended before Caravan adoption.

---

## What Caravan provides to meet you halfway

User-side rules are half the friction-reduction effort. The other half lives in the SDK design — Caravan should meet idiomatic Rust where it is, not demand contortions. The following SDK design knobs are open for the M2 design gate.

### M2-1. `#[wagon]` attribute macro (current default)

Status: shipped in B0p as identity macro; M2 will add codegen.

```rust
#[wagon]
pub trait Embedder: Send + Sync {
    fn embed(&self, text: &str) -> Result<Vec<f32>, EmbedError>;
}
```

Trade-off considered: function-like `caravan::seam!(Embedder);` as an alternative. The function-like form is more "grep for which traits are seams" but the attribute form is more idiomatic-feeling for traits.

**M2 disposition**: keep attribute as the default per [`poc_rpc_sdk.md`](poc_rpc_sdk.md).

### M2-2. `#[wagon]` on impl block (escape hatch for foreign traits)

Status: candidate; not yet implemented.

```rust
// External crate's trait — you can't edit it.
use external_crate::Embedder;

struct FastEmbedImpl { /* ... */ }

#[caravan::wagon_impl]
impl Embedder for FastEmbedImpl { /* ... */ }
```

Lets users wrap traits defined outside their crate without having to fork the upstream. The downside is loss of "grep the trait declaration to discover seams" — but the upside is real adoption flexibility.

**M2 disposition**: ship as escape hatch. Attribute-on-trait remains the default; attribute-on-impl is for foreign-trait cases.

### M2-3. Context-passing as alternative to global registry

Status: candidate; would change adoption ergonomics significantly.

```rust
// Current global form (kept):
let embedder = caravan_rpc::client::<dyn Embedder>();

// New context form:
async fn handler(
    State(ctx): State<Arc<CaravanContext>>,
    /* ... */
) {
    let embedder = ctx.client::<dyn Embedder>();
    embedder.embed(text)?;
}
```

Pro: no global mutable state; easier to test (each test builds its own context); composes naturally with axum's `State<>` extractor; idiomatic Rust.

Con: requires threading a context everywhere — but codebases already thread `State<Arc<AppState>>` everywhere, so this is no marginal change.

**M2 disposition**: ship both. Default the Rust documentation to context-passing as the recommended pattern; keep the global as a convenience for cross-cutting code. The Python SDK keeps the global as default (matches Python's typical pattern).

### M2-4. `MutexProtected<T>` helper for `!Sync` libraries

Status: candidate; concretely solves the fastembed-style case.

```rust
// Before (manual):
struct FastEmbedImpl { model: Mutex<TextEmbedding> }
impl Embedder for FastEmbedImpl {
    fn embed(&self, text: &str) -> Result<Vec<f32>, EmbedError> {
        let mut guard = self.model.lock().map_err(|_| EmbedError::Poisoned)?;
        guard.embed(text).map_err(|e| EmbedError::Embed(e.to_string()))
    }
}

// After (with helper):
type FastEmbedImpl = caravan_rpc::MutexProtected<TextEmbedding>;

#[caravan::auto_seam(Embedder)]
impl FastEmbedImpl { /* the helper auto-generates the &self wrappers */ }
```

The helper captures the recurring pattern: backing type has `&mut self` methods, we want a `&self` trait interface, Mutex bridges. Document it as the canonical answer to R4.

**M2 disposition**: ship as opt-in helper. Documentation directly references R4.

### M2-5. `wrap_struct!` for trait-impl delegation boilerplate

Status: candidate; nice-to-have, not load-bearing.

```rust
// Before (manual — what B0p did for VectorReader on VectorStore):
#[async_trait]
impl VectorReader for VectorStore {
    async fn search_code(&self, q: &[f32], limit: usize) -> Result<Vec<(CodeChunk, f32)>, StoreError> {
        VectorStore::search_code(self, q, limit).await
    }
    // ... 14 more methods, all identical-shape delegations ...
}

// After (with macro):
caravan::wrap_struct!(
    VectorStore impl VectorReader {
        search_code, search_code_signatures, search_readme,
        search_crates, search_module_docs, search_folders, search_files,
        hybrid_search_code, hybrid_search_readme, hybrid_search_crates,
        hybrid_search_module_docs, hybrid_search_folders, hybrid_search_files,
        list_projects, get_chunks_by_ids, get_all_edges, get_callers, get_callees,
    }
);
```

Useful when method signatures match exactly. Falls down when they diverge — then hand-write the impl.

**M2 disposition**: defer to post-M2. Nice ergonomic win but not blocking.

### M2-6. Clippy / lint helpers for rule violations

Status: candidate; Phase 3+ work.

A `cargo-caravan-lint` tool (or a clippy plugin) that scans user code against R1–R10 and reports. Would automate the [pre-Caravan checklist](#pre-caravan-checklist).

**M2 disposition**: defer to Phase 3+. Excellent adoption-pitch artifact ("Caravan tells you what needs to change") but not core SDK work.

---

## Out of scope (general Rust hygiene not covered here)

R1–R10 are about *shape* — what your seams look like from the outside so Caravan can dispatch them. They are not a replacement for general Rust hygiene. The following antipatterns matter for any Rust codebase and several can bite Caravan seams in particular, but they live in the broader [`rust-api-guidelines`](https://rust-lang.github.io/api-guidelines/) and are not enumerated here:

- **Holding a `MutexGuard` across `.await`.** Marks the future `!Send`, breaks tokio's multi-threaded scheduler. Relevant to async seam impls that use `std::sync::Mutex` (R4's pattern works only because the guard is dropped before any `.await`).
- **`std::sync::Mutex` vs `tokio::sync::Mutex` in async code.** Std mutex is fine if held briefly without an `.await` inside; tokio's mutex is required when the guard must cross `.await`.
- **`Box<dyn Error>` in library APIs.** Same family as R9 but broader — any type-erased error at a library boundary loses match-ability.
- **`unwrap()` / `expect()` / `panic!()` in seam / library code.** Seams are library boundaries; surface failures as `Result`, not panics.
- **Re-exporting concrete types from a public boundary.** Forces SemVer churn on downstream crates — the crate-API analogue of R1.

If a Caravan-adopting codebase wants a full lint pass, run [`clippy`](https://doc.rust-lang.org/clippy/) with the `clippy::pedantic` and `clippy::nursery` groups on top of these ten rules.

## What this doc is NOT

- Not a general Rust style guide. For broader API design, see [`rust-api-guidelines`](https://rust-lang.github.io/api-guidelines/).
- Not a replacement for [`poc_rpc_sdk.md`](poc_rpc_sdk.md). That doc defines the wire contract; this doc defines how to shape user code so the wire contract is comfortable to live with.
- Not a critique of any specific crate. fastembed's `&mut self` API is fine for its use case; R4 just describes how to interface with it from Caravan.
- Not authoritative on scope. [`thesis.md`](thesis.md) and [`development_plan.md`](development_plan.md) define what Caravan is and where it's going. This doc is downstream of both.

## Companion docs to come

- `python_caravan_style.md` — once B0 closes, the Python rule set will be derived from invoice-parse evidence (gentler than Rust; mostly about ABC + factory patterns).
- `typescript_caravan_style.md` — Phase 2+ work, after Caravan TypeScript SDK lands.
- `go_caravan_style.md` — Phase 2+ work, after Caravan Go SDK lands.

Rust first because B0p produced the steepest evidence. Other languages follow once this format is validated by use.
