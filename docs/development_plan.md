# Caravan PoC — Development Plan

Multi-session, milestone-driven plan from today's scoping-complete + code-empty state to a functioning PoC validated on two real test repos (code-rag, invoice-parse). The plan resolves a chicken-and-egg between building Caravan and adapting test repos by **bootstrapping on a real seam in invoice-parse before any compiler code lands** (milestone B0), then automating the proven shape via M0–M9.

---

## Framing

### Caravan today

Scoping-complete, code-empty. The CLI stub at [../cmd/caravan/main.go](../cmd/caravan/main.go) prints "not implemented yet." The v0.1 implementation language is **Go**. Authoritative docs in [./](./): `thesis.md`, `poc_rpc_sdk.md`, `poc_yaml_spec.md`, `poc_groups_to_code.md`, `ir.md`, `open_decisions.md`.

The compiler has five phases per [ir.md](ir.md): Lex → Parse → Normalize → Resolve → Emit. Output: HCL + docker-compose + per-target manifest patches + `CARAVAN_RPC_PEERS` env var.

The structural contract is the `caravan-rpc` SDK at seams: `@wagon` + `provide(X, impl)` + `client(X)`.

### Four organizing principles

1. **The thesis is one specific claim** — *yaml-line changes alone* flip a seam's dispatch mode without source-code edits. Everything before that claim is demoable is plumbing; everything after extends it.
2. **The test repos are design pressure, not the destination.** code-rag and invoice-parse are coordinated co-development partners on `caravan-conversion` branches; their conversion drives SDK ergonomics before AWS shape gets locked in.
3. **Bootstrap before compile.** B0 hand-wires one real seam in invoice-parse to prove the SDK contract by example. Only then does compiler work begin (M0+), automating what's already been proven.
4. **Test inproc + container first; cloud after Phase 1 passes.** All thesis-proving milestones (B0 → M9 Phase-1) run entirely on docker-compose + local-run — the inproc-vs-container dispatch flip is what the thesis is about, and compose proves it. AWS surface (HCL emitter, Lambda, Batch, real S3/RDS/etc.) is **Phase 2** — start only after Phase 1's 8-condition matrix is green on both repos. This reduces blast radius during design-pressure milestones: no AWS bill, no Terraform state, no IAM debugging while SDK and compiler shape is still being learned.

### Seam ≠ deploy boundary (load-bearing clarification)

A seam is an interface declared in source via the SDK. **Each seam carries its own per-target dispatch config — `inproc`, `container`, or `lambda` — set independently of every other seam.** A target's yaml might mark `EmbedderInterface: container`, `RerankerInterface: inproc`, `LlmInterface: lambda` simultaneously. Source code is identical across targets; the SDK reads `CARAVAN_RPC_PEERS` at runtime and dispatches each seam per its declared mode.

Implications:
- "dev-monolith" / "dev-split" are conventional target names but not enforced deployment shapes. Real targets mix modes per-seam freely.
- A seam declared in source but unset in a target's yaml inherits a default (`inproc` per [poc_rpc_sdk.md](poc_rpc_sdk.md)).
- "Flip a seam" means flipping **one seam's** entry in the target's `seams:` map — not the whole application's topology.

### SDK no-config inertness (non-negotiable property)

When `CARAVAN_RPC_PEERS` is unset, `client(X).method()` MUST short-circuit to the locally-registered `provide()` impl with no dispatch overhead and no error. A developer running `python -m invoice_processing.worker` or `cargo run -p code-rag-chat` against SDK-wrapped code gets exactly the same behavior they did before wrapping — no env var, no docker, no compiler, no crash. The SDK library is inert by default. Only when a target's compile output sets `CARAVAN_RPC_PEERS` does dispatch become non-trivial.

This makes local-run a first-class deployment surface alongside compose / Fargate / Lambda / Batch.

### Confirmed PoC scope

- **Languages**: Rust + Python (compiler in Go).
- **Cloud**: AWS only.
- **Placements**: docker-compose (local) + local-run (host process, no orchestrator) + Fargate + Lambda + Batch.
- **Seam identification**: user-declared via SDK (no auto-scanner). SDK wrappers in test repos are candidates the user manually refactors into caravan-rpc seams.

**Note on TypeScript and Go SDK directories.** `caravan/rpc/typescript/` and `caravan/rpc/go/` exist at 0.0.1 to claim npm and Go-proxy names; they are not in PoC scope. v1+ work resurrects them. Until then, treat them as namespace reservations.

---

## Pre-existing deployment inventory

Each pre-existing surface is a runtime mode the SDK + compiler must continue to support after Caravan-conversion. Validated against the pre-change-state docs in each repo.

### code-rag — 5 deployment surfaces

1. **Docker compose, chat target** — [../../code-rag/docker-compose.yaml](../../code-rag/docker-compose.yaml). Build target `chat` from single multi-stage [../../code-rag/dockerfile/Dockerfile](../../code-rag/dockerfile/Dockerfile). Build context is parent of the repo (`context: ..`).
2. **Docker compose, ingest target** — [../../code-rag/docker-compose-ingest.yaml](../../code-rag/docker-compose-ingest.yaml), one-shot, target `raptor` from same Dockerfile.
3. **Local non-container run** — `cargo run --release` for code-rag-chat (Axum HTTP), code-raptor (ingest CLI), harness (quality eval), and code-rag-mcp (MCP stdio). Direct host execution.
4. **GitHub Actions: gh-pages** ([../../code-rag/.github/workflows/gh-pages.yml](../../code-rag/.github/workflows/gh-pages.yml)) — workflow ingests target repos, exports static JSON, builds WASM standalone via trunk, deploys to GitHub Pages. **Runs the engine entirely in-browser.** Caravan-conversion must not break this WASM compile path.
5. **GitHub Actions: release** ([../../code-rag/.github/workflows/release.yml](../../code-rag/.github/workflows/release.yml)) — cross-platform binary build for `code-rag-mcp`, zipped + draft GH Release. End-user install: download → edit yaml → double-click → MCP stdio via Claude Code's `.mcp.json`. **Single-binary entry kind; Caravan must keep this entry independently buildable.**

### invoice-parse — 4 deployment surfaces

1. **Docker compose, `--profile app`** — [../../invoice-parse/infra/docker-compose.yaml](../../invoice-parse/infra/docker-compose.yaml). Runs Postgres 18, Redis 8, model-init one-shot, processing, output, dashboard.
2. **Docker compose, `--profile ingest`** — same compose file, one-shot enqueue.
3. **Local non-container run** — README documents host execution: `python -m invoice_processing.cli`, `python -m invoice_processing.worker`, `cargo run --manifest-path services/ingestion/Cargo.toml`, `cargo run --manifest-path services/output/Cargo.toml`, `streamlit run`. Postgres + Redis still in docker; app processes local.
4. **GitHub Actions: pages** ([../../invoice-parse/.github/workflows/pages.yml](../../invoice-parse/.github/workflows/pages.yml)) — TypeScript/Vite app under `demo/`. Runs the invoice parsing pipeline (paddleocr via npm, @google/genai, wasm-xlsxwriter) **entirely in browser**. Out of Caravan scope but constrains engine browser-compatibility.

### Implications

- **Caravan-generated artifacts live in `infra/<target>/generated/`** per [ir.md](ir.md). Existing compose / Dockerfiles stay as hand-authored ground truth. Validation: "does Caravan-generated compose produce the same end-state as hand-authored?"
- **invoice-parse's compose is closer to what Caravan emits** — per-service Dockerfiles, standard build contexts, profile-based topology toggling. Smaller migration cost.
- **code-rag's deployment is further from Caravan-shape** — single Dockerfile, parent-context build. Don't restructure for Caravan; reference the existing Dockerfile from generated compose instead.

---

## Milestones

Each milestone has: demoable result, prerequisites, implementation work, acceptance criteria, time estimate, and risks. All acceptance criteria additionally require the pre-change-state verification commands (per the corresponding `pre-change-state.md`) to still pass.

### Phase A — Squat names (COMPLETE — 2026-05-19)

Pre-condition for all subsequent milestones. All four tier-1 SDK packages are published as 0.0.1 placeholders:

| Registry | Status |
|---|---|
| PyPI `caravan-rpc` | ✅ 0.0.1 |
| crates.io `caravan-rpc` | ✅ 0.0.1 |
| npm `caravan-rpc` | ✅ 0.0.1 |
| Go `github.com/paulxiep/caravan/rpc/go` | ✅ v0.0.1 |

Source-of-truth at `caravan/rpc/<lang>/`. Workflows at `.github/workflows/publish-{py,rust,ts,go}-sdk.yml`. The 0.0.1 stubs are deliberately inert: `wagon()` is identity, `provide()` is a no-op, `client()` raises NotImplementedError. They reserve the name and let SDK-wrapped code import-clean.

**Version cadence**: 0.0.1 = squat placeholder (done). 0.1.0 = first functional release (lands at B0 for Python, M2 for Rust). 0.2.0+ = post-PoC iterations.

### B0 — Hand-wired bootstrap on invoice-parse LLMExtraction ⬅ THESIS-PROVING (3–5 sessions)

The chicken-and-egg breaker. No compiler. No proc-macros. We hand-author a minimal Python SDK, refactor one real seam to use it, and demonstrate the inproc/http flip on a live invoice pipeline by hand-editing `CARAVAN_RPC_PEERS`.

**Why invoice-parse and LLMExtraction:**
- Python iterates faster than Rust (runtime reflection via `inspect.signature`; no proc-macros to design).
- `LLMExtractor` in [../../invoice-parse/services/processing/invoice_processing/extraction.py](../../invoice-parse/services/processing/invoice_processing/extraction.py) is **already abstracted via ABC + factory** — closest seam to caravan-ready in either repo.
- LLM calls are stateless, network-bound, pure-ish — textbook seam.
- invoice-parse's existing per-service Dockerfile + profile-based compose already mirrors what Caravan emits.

**Work:**
1. **Functional `caravan-rpc` Python SDK (0.0.1 → 0.1.0).** Replace the no-op stubs at `caravan/rpc/python/src/caravan_rpc/__init__.py` with a hand-typed functional implementation: `@wagon` decorator (preserves 0.0.1's identity-when-unset behavior, plus method-call interception when env var is set), `provide(X, impl)` registry, `client(X)` dispatcher that reads `CARAVAN_RPC_PEERS`. aiohttp or stdlib HTTP server adapter. `requests` or `httpx` client adapter. **No-config-inertness baked in**: when env var is unset, `client(X).method()` is a direct function call on the registered impl. Bump version to 0.1.0 in `pyproject.toml`; publish via the existing `publish-py-sdk.yml` workflow.
2. **invoice-parse `caravan-conversion` branch**: refactor `LLMExtractor` ABC into `@wagon LLMExtraction`; replace factory with `provide(LLMExtraction, GeminiExtractor())`. Call sites in [../../invoice-parse/services/processing/invoice_processing/worker.py](../../invoice-parse/services/processing/invoice_processing/worker.py) change from `extractor.extract(...)` to `client(LLMExtraction).extract(...)`.
3. **Hand-edited compose override** at `invoice-parse/infra/docker-compose.caravan-bootstrap.yaml` injecting `CARAVAN_RPC_PEERS` env var into the `processing` service and adding an optional `llm-extractor` sidecar for HTTP-mode tests.

**Acceptance — six criteria in increasing strictness:**
1. **Inertness, local-run, no env var**: `python -m invoice_processing.worker` runs against existing local Postgres/Redis exactly as before. No env var set. Same Excel output. Same logs.
2. **Inertness, compose, no env var**: `docker compose --profile app up` against the existing compose file succeeds with no Caravan env var injected.
3. **Inproc with env var**: `CARAVAN_RPC_PEERS={"LLMExtraction":{"mode":"inproc"}}` set; same outcome as #1/#2; dispatch path logs INPROC.
4. **HTTP with env var, local**: `CARAVAN_RPC_PEERS={"LLMExtraction":{"mode":"http","url":"http://localhost:8080"}}` + separate `python -m caravan_rpc.serve LLMExtraction` bound to localhost:8080. Worker runs end-to-end producing identical Excel output. Logs show HTTP dispatch.
5. **HTTP with env var, compose**: override compose adds `llm-extractor` sidecar running `provide()`. Worker runs end-to-end, identical Excel, HTTP dispatch in logs.
6. **`git diff -- services/processing/invoice_processing/` is empty between #1, #3, #4, #5.** Source code identical; env var alone decides behavior.

**B0 is the lived spec for the SDK.** Once it works, every later milestone is "automate this proven shape." If B0 fails, the thesis is wrong before we burn compiler effort.

**Progress (B0 — IN PROGRESS):**

SDK (`caravan/rpc/python/`):
- [x] `@wagon` decorator + tests — 2026-05-19 (9/9 tests pass; real LLMExtraction-shape covered: `Optional[X] | None = None` defaults, `Signature.bind(**kwargs)`, string annotations under `from __future__ import annotations`)
- [x] inproc registry + `provide()` — 2026-05-19 (6/6 tests pass; thread-safe via `Lock`; `provide()` rejects non-`@wagon` interfaces; `lookup()` raises clear `LookupError` naming the missing interface)
- [x] `pydantic.TypeAdapter` codec — 2026-05-19 (12/12 tests pass; unified codec for dataclasses + Pydantic models + Optional defaults + primitives + unannotated passthrough; string annotations resolved via `typing.get_type_hints` with module globalns; full JSON wire roundtrip verified; adapters cached per `(interface, method)`)
- [x] `client(I)` proxy — 2026-05-19 (16/16 tests pass; **no-config inertness verified**: env-unset → `client(I).method == impl.method` (`MethodType` equality, same `__self__`); HTTP dispatch via stdlib `urllib.request` exercising encode→POST→decode; `Authorization: Bearer` from `CARAVAN_RPC_SHARED_SECRET`; `X-Caravan-Rpc-Version: 1`; remote-error envelope → `RpcRemoteError`; transport failures → `RpcTransportError`; lambda mode raises `NotImplementedError` deferred to M7; strict seam-method semantics — non-`@wagon` methods raise `AttributeError`)
- [x] `python -m caravan_rpc.serve` CLI — 2026-05-19 (12/12 tests pass incl. real-HTTP end-to-end roundtrip on ephemeral port: encode→POST→decode→Pydantic reconstruct; bearer-auth enforcement; wire-version header check; remote exception → `RpcRemoteError`; `--impl module:Class` resolution defaults `--interface-module` to the impl's module so invoice-parse's same-file interface+impl layout works without extra flags)
- [x] **SDK smoke against real invoice-parse types** — 2026-05-19. In invoice-parse `.venv`: codec `_adapters_for(LLMExtraction, 'extract')` builds `TypeAdapter(Union[RawOcrOutput, NoneType])` (dataclass + Optional), `TypeAdapter(Union[TableExtractionOutput, NoneType])`, and `TypeAdapter(InvoiceExtraction)` (Pydantic BaseModel). No type-resolution errors. `_resolve_interface_and_impl('LLMExtraction', 'invoice_processing.extraction:GeminiExtractor', None)` resolves both correctly via the same-module default. `client(LLMExtraction).extract` returns the bound method of the registered `GeminiExtractor` — inertness verified end-to-end on the real refactor.

**Publish flow (gated at Phase 2 close — M9-cloud):**

The 0.0.1 PyPI placeholder already reserves the name. The 0.1.0 first-functional release waits until **M9-cloud (Phase 2 close)** — by then the SDK contract has been validated against compose AND Fargate AND Lambda, with both Python and Rust SDKs exercised. Through the entire B0 → M9-cloud run, test repos pull caravan-rpc via local-editable install (or git URL pinning a SHA).

- [ ] **Local-editable install** through B0 → M9-cloud — `pip install -e <caravan>/rpc/python` (or `pip install "caravan-rpc @ git+https://github.com/paulxiep/caravan.git@<sha>#subdirectory=rpc/python"` for CI/cross-machine). SDK version stays at `0.1.0.dev0`.
- [ ] **TestPyPI publish at 0.1.0rc1** — smoke after M9-cloud close: prove the wheel installs cleanly in a fresh venv.
- [ ] **PyPI publish at 0.1.0** — once rc1 smoke passes. By this point the SDK has been driven by hand-wired (B0), compiler-emitted (M1, M3), Rust interop (M2), and AWS placements (M7, M4-cloud). Wire-version-1 ABI is genuinely frozen at this point.

invoice-parse `caravan-conversion` branch:
- [x] `caravan-rpc` installed in `.venv` via editable path (`pip install -e ../caravan/rpc/python`). No PyPI version pin. — 2026-05-19
- [x] `extraction.py` refactored — 2026-05-19. `LLMExtractor` ABC + `create_extractor` factory removed. New `@wagon class LLMExtraction:` interface (method-signature-only). `GeminiExtractor` / `ClaudeExtractor` / `OpenAIExtractor` dropped ABC inheritance, became plain classes.
- [x] `worker.py` call site refactored — 2026-05-19. Imports: `LLMExtractor` + `create_extractor` dropped, `LLMExtraction` + `GeminiExtractor` added, `caravan_rpc.{client, provide}` added. `run_pipeline` no longer takes `extractor` param. Call site at line 184 swapped to `client(LLMExtraction).extract(...)`. `run_worker()` calls `provide(LLMExtraction, GeminiExtractor())` once at startup before the queue loop.
- [x] `cli.py` call site refactored — 2026-05-19. `--provider` flag still works; chosen impl is registered via `provide()` before `client(LLMExtraction).extract(...)`. `create_extractor` factory removed.
- [x] `infra/docker-compose.caravan-bootstrap.yaml` written — 2026-05-19. Override file injects `CARAVAN_RPC_PEERS` into `processing` and spawns an `llm-extractor` peer service reusing the processing image with overridden `command:` running `python -m caravan_rpc.serve`.
- [x] `Dockerfile` + wheel-vendoring setup — 2026-05-19. caravan-rpc gets installed in the image from a vendored wheel at `services/processing/vendor/caravan_rpc-*.whl`, built locally via `infra/rebuild-caravan-rpc-wheel.sh`. The wheel is gitignored (build artifact); the script rebuilds it after any caravan-rpc source change. When caravan-rpc lands on PyPI post-M9-cloud, this swaps to a plain `pip install caravan-rpc==<ver>`.

Six-criteria acceptance gate (extraction.json SHA-256 identical across runs):
- [x] #1 Local-run, no env — 2026-05-19. cli.py against `invoices/sample_invoice.pdf`. `extraction.json` SHA = `3f3c0097226fec3d22d55c00a8a0c436b8bcfe9ad7aab13f33b2bde1364f2bf7`. Vendor=myAgency Ltd, 2766 CZK, 7 line items, 100% confidence. (Local venv needs `paddlex[ocr]` + `OCR_DET_MODEL=PP-OCRv5_mobile_det` + `OCR_REC_MODEL=en_PP-OCRv5_mobile_rec`; auto-downloads models on first run.)
- [x] #2 Compose, no env — 2026-05-19. `docker compose -f infra/docker-compose.yaml --profile app up -d`, ingest enqueues sample_invoice.pdf as job `e70aa72d-...`, processing worker handles it inproc (env unset). Postgres `extraction_data` is **semantically identical** to #1 (deep dict equality). Confirms inertness path works inside container too.
- [x] #3 Inproc with env — 2026-05-19. `CARAVAN_RPC_PEERS={"LLMExtraction":{"mode":"inproc"}}`. Same SHA as #1. Inproc-explicit path goes through the same inertness branch as no-env.
- [x] #4 HTTP local (two-process) — 2026-05-19. `caravan_rpc.serve` background-spawned on port 8080; cli pointed at it. Same SHA as #1. HTTP encode→POST→decode preserved every byte through `pydantic.TypeAdapter` for the real `RawOcrOutput` / `TableExtractionOutput` / `InvoiceExtraction` types.
- [x] #5 HTTP compose (override file) — 2026-05-19. `docker compose -f infra/docker-compose.yaml -f infra/docker-compose.caravan-bootstrap.yaml --profile app up -d` brings up `llm-extractor` peer service (same image, command `python -m caravan_rpc.serve --interface LLMExtraction --impl invoice_processing.extraction:GeminiExtractor --port 8080`). `processing` runs with `CARAVAN_RPC_PEERS={"LLMExtraction":{"mode":"http","url":"http://llm-extractor:8080"}}` injected. PDF job `4b31b715-...`'s `extraction_data` semantically identical to #1. 17 successful POST `/_caravan/rpc/LLMExtraction/extract` 200 responses in llm-extractor logs (one per ingested invoice). End-to-end HTTP dispatch through container DNS confirmed.
- [x] #6 Source identical across runs — 2026-05-19. Verified via mtime check: source files last edited 22:00–22:01, all runs started ≥22:35. `git diff` between the working-tree state at any two run times is empty (no edits between criterion runs).

Key decisions ratified during planning:
- **Wire serialization:** `pydantic.TypeAdapter` built from `@wagon` method type hints, cached at decoration time. Pydantic v2 is a hard SDK dep.
- **HTTP-mode topology:** separate compose service `llm-extractor` reusing the `processing` image with overridden `command:`. Not a K8s-style "sidecar" — an independent peer service. Same shape generalizes to M7-Fargate (sibling Task Definition) and M7-Lambda (Function URL with `caravan_rpc.lambda_handler` entry).
- **HTTP server library:** stdlib `http.server` ThreadingHTTPServer (no extra deps).
- **`provide()` invocation site:** worker/CLI startup, not module-import time.
- **Bedrock/LLM provider swap:** out of B0 scope (that's resource composition at M4-cloud, orthogonal to seam dispatch).

### B0p — code-rag stub track (parallelizable with B0; not blocking)

**Parallelization note.** This is the one milestone in the plan explicitly designed for concurrent execution — separate language (Rust), separate repo (code-rag), separate branch (`caravan-conversion`), no API surface dependency on B0's Python output beyond the shared spec. If you have multiple sessions / people, **run B0 and B0p concurrently**. If solo, either ordering works: parallel (context-switch cost, but both branches stay alive) or sequential B0 → B0p (let Python's SDK lessons inform the Rust trait surface, slightly lower churn risk). All other milestones in the plan are strictly sequential.

While B0 lands the Python contract, code-rag's `caravan-conversion` branch progresses against a hand-typed Rust stub.

**Work:**
- **Reuse the published `caravan-rpc` 0.0.1 placeholder as the stub.** Its `wagon` is currently an identity function (not yet an attribute macro) and its `provide`/`client` are no-ops. For B0p we extend 0.0.1's surface with hand-typed Rust trait shapes (declaration-only, no functional dispatch) so code-rag can declare its seams against a stable API. This may require a 0.0.2 placeholder bump if the trait surface changes before M2.
- code-rag `caravan-conversion` branch: depend on `caravan-rpc = "0.0.x"` (whichever placeholder version carries the trait surface). Declare Embedder + Reranker + VectorReader + LlmClient as `#[wagon]`-typed traits via hand-typed code. Swap `state.x.method()` → `client(X).method()` at all call sites. Since `wagon` is identity and `client()` is unused in local-run (env var unset), all existing code-rag commands keep working.
- Validate the trait surface against code-rag's `Mutex<Embedder>` interior-mutability case **before** M2 lands functional proc-macro support.

**Acceptance:**
- All code-rag local-run commands work unchanged (no env var, no crash).
- Existing `docker compose up` works unchanged.
- `cargo test` passes.
- `trunk build --release --features standalone` still produces WASM artifact.
- When real `caravan-rpc` lands at M2, code-rag swaps stub for real — drop-in.

**Progress (B0p — IN PROGRESS):**

SDK (`caravan/rpc/rust/`):
- [x] Workspace restructure: `caravan-rpc/` + new sibling `caravan-rpc-macros/` (proc-macro = true) — 2026-05-20 (single-crate placeholder → two-crate workspace; `LICENSE` / `README.md` / `src/lib.rs` moved under `caravan-rpc/`; root `Cargo.toml` becomes a workspace manifest)
- [x] Identity `#[wagon]` attribute macro in `caravan-rpc-macros` — 2026-05-20 (returns input unchanged; M2 will replace with adapter codegen; visual surface in code-rag matches the M2 target byte-for-byte)
- [x] Functional inproc registry — 2026-05-20 (`TypeId`-keyed `RwLock<HashMap<TypeId, Box<dyn Any + Send + Sync>>>`; `provide::<T: ?Sized + Send + Sync + 'static>(Arc<T>)`; `client::<T>() -> Arc<T>` + `try_client::<T>() -> Option<Arc<T>>` + `is_provided::<T>()`; no-config inertness verified — env-unset → straight registry lookup with zero overhead beyond `Arc::clone`)
- [x] Loud-fail guards for non-inproc dispatch — 2026-05-20 (`CARAVAN_RPC_PEERS` containing `"http"` panics naming M2; `"lambda"` panics naming M7; coarse string match avoids committing to the JSON schema before M2)
- [x] Registry integration tests — 2026-05-20 (9/9 pass: `provide`/`client` roundtrip, last-write-wins re-register, panic when no impl, `try_client`+`is_provided`, inproc-mode env behaves like unset, http-mode env panics with M2 pointer, lambda-mode env panics with M7 pointer, distinct traits keyed independently by `TypeId`, `Arc` clone semantics — `EnvVarGuard` serializes env-touching tests; unique marker traits keep registry-only tests parallel-safe)
- [x] Publish workflow updated for two-crate workspace — 2026-05-20 (`publish-rust-sdk.yml` publishes `caravan-rpc-macros` first, sleeps 60s for crates.io indexing, then publishes `caravan-rpc`; defaults to dry-run)
- [x] `cargo clippy --workspace --all-targets` clean — 2026-05-20
- [x] `cargo fmt --all` clean — 2026-05-20

**Publish flow (gated at PR/MR merge):**

The on-crates.io 0.0.1 placeholder is unchanged. Local crates remain at `0.0.1` through B0p iteration per user direction ("we should only bump publish on full PR/MR"). The coordinated `0.0.2` bump + publish for both `caravan-rpc` and `caravan-rpc-macros` lands at PR-merge time.

- [ ] **Local-path-dep** consumed by code-rag through B0p — `caravan-rpc = { path = "../caravan/rpc/rust/caravan-rpc" }`
- [ ] **0.0.2 bump + crates.io publish** at PR-merge (both crates in lockstep; macros first, runtime second)

code-rag `caravan-conversion` branch:
- [x] `caravan-rpc` added via workspace path-dep across `code-rag-chat` (root), `code-rag-store`, `code-rag-mcp`, `code-raptor`. **Not** added to `code-rag-engine` / `code-rag-ui` (those compile to `wasm32-unknown-unknown`). `async-trait = "0.1"` added as direct dep where needed (0.1.89 resolved). — 2026-05-20
- [x] Seam declarations in `crates/code-rag-store/src/seams.rs`: `Embedder`, `Reranker`, `VectorReader`, `LlmClient` — all `#[wagon]`, `&self` only (Mutex moves into impls), `Reranker::rerank` keeps owned `Vec<String>` for the cross-encoder, `VectorReader` covers all 13 read methods + call-graph reads, `LlmError` defined locally so the trait can live alongside the others. — 2026-05-20
- [x] **B3a Embedder:** `Embedder` struct → `FastEmbedImpl`; interior `std::sync::Mutex<TextEmbedding>`; `EmbedError::Poisoned` variant; impl `seams::Embedder`. Call sites swap to `&dyn Embedder` parameter; AppState's `tokio::sync::Mutex<Embedder>` becomes `Arc<dyn Embedder>`. — 2026-05-20
- [x] **B3b Reranker:** `Reranker` struct → `MsMarcoRerankerImpl`; interior `std::sync::Mutex<TextRerank>`; `RerankError::Poisoned` variant; impl `seams::Reranker`. AppState's `Option<tokio::sync::Mutex<Reranker>>` becomes `Option<Arc<dyn Reranker>>`. — 2026-05-20
- [x] **B3c VectorReader:** `impl crate::seams::VectorReader for VectorStore` with all 16 read methods delegating via UFCS to inherent methods of the same name; writes stay on the concrete `VectorStore`. — 2026-05-20
- [x] **B3d LlmClient:** `LlmClient` struct → `RigGeminiImpl`; module-level free `async fn generate` absorbed into trait method; chat-side `EngineError` gains `#[from] LlmError`; `ApiError::From<LlmError>` added. — 2026-05-20
- [x] **B4 + B5:** AppState stripped to `{ classifier, config }`; seam impls constructed in `from_config` and registered via `caravan_rpc::provide::<dyn I>(...)`; **every call site swapped to `caravan_rpc::client::<dyn I>().method(...)`** in `src/api/handlers.rs`, `src/engine/retriever.rs`, `src/harness/runner.rs`, `crates/code-rag-mcp/src/main.rs`; `retrieve()` and `runner::run_all()` signatures take `&dyn …`. — 2026-05-20

Verification gate:
- [x] `cargo check --workspace` clean (caravan + code-rag) — 2026-05-20
- [x] `cargo test --workspace` green (all test suites, no regression vs baseline) — 2026-05-20
- [x] `cargo clippy --workspace --all-targets` (caravan SDK clean; code-rag has only pre-existing warnings unrelated to B0p) — 2026-05-20
- [x] `cargo fmt --all` clean (both repos) — 2026-05-20
- [x] `cargo build --release -p code-rag-chat` succeeds — 2026-05-20
- [x] `cargo build --release -p code-rag-mcp` succeeds independently — 2026-05-20
- [x] `cargo run --release -p code-rag-chat -- --health` exits 0 with **no env var** (no-config inertness confirmed) — 2026-05-20
- [ ] `trunk build --release --features standalone` re-run (engine/UI not consumers of `caravan-rpc`, so unchanged — defer unless WASM bundle changes)
- [ ] `docker compose build` re-run

Key decisions ratified during B0p:
- **`#[wagon]` ships now as identity proc-macro** (separate `caravan-rpc-macros` crate). Visual surface in code-rag matches M2 target byte-for-byte; no source-file churn at M2.
- **Full code-rag refactor** (concrete structs → traits, Mutex moves inside impls, every call site swapped). The dev plan flagged the `Mutex<Embedder>` interior-mutability case as the load-bearing thing B0p must validate; that's now exercised end-to-end.
- **Local path-dep** during iteration; **no version bumps mid-iteration** (per user direction). 0.0.2 bump + crates.io publish are gated to the PR/MR merge.
- **Optional seam handling:** `try_client::<T>() -> Option<Arc<T>>` SDK helper (cleaner than a sentinel `NoopReranker` impl). Used for Reranker, which the chat target may run without.
- **VectorWriter / call-edges resource split deferred to M5** per dev plan. B0p's `VectorReader` covers reads only; writes stay on the concrete `VectorStore`.

See [../PoC-B0p.md](../PoC-B0p.md) for a full milestone write-up.

### M0 — Compiler parses yaml and writes a file (2 sessions)

**Demo.** `caravan compile --target=dev` reads `caravan.yaml`, prints normalized Plan as JSON, writes placeholder `infra/dev/generated/main.tf` + `docker-compose.generated.yaml`. `caravan spec --json` round-trips.

**Prereqs.** B0 acceptance #1–#3 passing.

**Work.** Compiler phases 1–3 in Go per [ir.md](ir.md). Structs for `Plan` / `Entry` / `Seam` / `Resource` / `Target`. `gopkg.in/yaml.v3` parser. Cross-ref resolver. Diagnostic infrastructure with source spans. The bootstrap yaml for invoice-parse from B0 becomes the worked-example fixture.

**Acceptance.** Bootstrap yaml parses. Phase-2 errors on unknown ref + duplicate provider + missing manifest. Golden-file tests. `caravan spec --json` matches the hand-authored env vars from B0.

**Risk.** Tagged-union dispatch on `type:` is fiddly in Go. Mitigation: build `TestExhaustiveSwitch_<Kind>` CI helpers from day one.

**Decision gate before M0.** Ratify Go for v0.1 compiler (recommended; already aligned with stub CLI).

**Go conventions — what to commit.** `go.mod` (module declaration; analog of `Cargo.toml` / `package.json`) and `go.sum` (content-hash lock; analog of `Cargo.lock` / `package-lock.json`) **both go in the commit**. caravan is a binary, so committing `go.sum` is mandatory for reproducible builds; for pure libraries the old "don't commit go.sum" advice has been superseded — commit it either way. The pattern matches B0p's Rust `Cargo.toml` (committed) + `Cargo.lock` (gitignored for libraries per [the Rust SDK gitignore note](../.gitignore)), with the caveat that **Go's default is the opposite of Rust's library convention** — go.sum is committed even for libraries.

**Progress (M0 — DONE):**

Compiler scaffold (`caravan/internal/compiler/`):
- [x] `internal/compiler/kinds.go` — enums for `ResourceKind`, `TriggerKind`, `RuntimeKind`, `CompositionMode`, `EntryDispatchMode`, `SeamDispatchMode` with `IsValid()` helpers — 2026-05-20
- [x] `internal/compiler/types.go` — IR structs (`Plan`, `Entry`, `Seam`, `Resource`, `Secret`, `Target`, `Trigger`, `ResolvedPlan`, `PeerEntry`, `Span`) per PoC's flatter entries+seams shape — 2026-05-20
- [x] `internal/compiler/diag.go` — `Diagnostics` collector with `Error()` / `Warn()` + `WriteTo()` in `file:line:col: severity: message` form — 2026-05-20
- [x] `internal/compiler/lex.go` — Phase 1: file → `yaml.Node` tree via `gopkg.in/yaml.v3`, source spans preserved — 2026-05-20
- [x] `internal/compiler/traverse.go` — declarative yaml-stepping helpers (`forEachKV`, `forEachItem`, `dispatchFields`, `mappedItems`) — 2026-05-20
- [x] `internal/compiler/parse.go` — Phase 2: schema validation via per-field `fieldMap`s; tagged-union dispatch (`triggerParsers` map) on trigger kind and resource type; generic `parseEnumMap[T]` for the target sub-maps — 2026-05-20
- [x] `internal/compiler/normalize.go` — Phase 3 as named pipelines: `applyDefaults` (seam `service_name` kebab-case fallback) → `runValidators` (7 cross-ref + invariant checks, all run unconditionally so diagnostics surface together) — 2026-05-20
- [x] `internal/compiler/resolve.go` — Phase 4 (narrowed): per-mode `peerBuilders` map + named helpers; deterministic alphabetic ordering. No IAM / networking / secret resolution (deferred to M4-cloud / M7) — 2026-05-20
- [x] `internal/compiler/compile.go` — top-level `CompileFile` (phases 1–3) and `CompileFileForTarget` (phases 1–4) — 2026-05-20
- [x] `cmd/caravan/main.go` — subcommand router (`check`, `spec`, `compile`, `--version`) using stdlib `flag` — 2026-05-20
- [x] `go.sum` ready to commit (committed alongside `go.mod` per Go convention) — 2026-05-20

Test infrastructure (`caravan/internal/compiler/testdata/`):
- [x] `invoice-parse-bootstrap.yaml` — copy of `invoice-parse/caravan.yaml` — 2026-05-20
- [x] `invoice-parse-bootstrap.dev-bootstrap.spec.json` + `.dev-inproc.spec.json` — golden outputs, refreshed via `go test -update` — 2026-05-20
- [x] `TestSpecJSON` (2 subtests): golden-file match for both targets — 2026-05-20
- [x] `TestSpecMatchesB0HandEdit`: pins `EnvVars.processing.CARAVAN_RPC_PEERS` to B0's exact string — 2026-05-20
- [x] `TestNormalizeErrors` (5 subtests): unknown `uses:` ref, duplicate seam, unknown resource type, container seam without `impl:`, missing top-level `name:` — 2026-05-20

invoice-parse `caravan-conversion` branch:
- [x] `invoice-parse/caravan.yaml` authored at repo root with `LLMExtraction` seam carrying `impl:` + `service_name:` fields — 2026-05-20

End-to-end acceptance:
- [x] `caravan check` from invoice-parse working dir exits 0 — 2026-05-20
- [x] `caravan spec --target=dev-bootstrap` emits `EnvVars.processing.CARAVAN_RPC_PEERS = {"LLMExtraction":{"mode":"http","url":"http://llm-extractor:8080"}}` — byte-for-byte match with B0's hand-edited override file — 2026-05-20
- [x] `caravan compile --target=dev-bootstrap` writes placeholder `infra/dev-bootstrap/generated/{main.tf, docker-compose.generated.yaml}` (real emission lands at M1) — 2026-05-20
- [x] `go test ./...` green (8/8 tests pass) — 2026-05-20

### M1 — Compiler emits docker-compose override, runs invoice-parse (2 sessions)

**Demo.** `caravan compile --target=dev-bootstrap` against the B0 yaml writes `infra/dev-bootstrap/generated/docker-compose.override.generated.yaml`, a compose **override** layered atop the hand-authored base. Semantically equivalent to the hand-edited `infra/docker-compose.caravan-bootstrap.yaml` from B0 — same `CARAVAN_RPC_PEERS` injection on `processing`, same `llm-extractor` peer service. `docker compose -f base -f generated up` runs identically to the hand-edited B0 path.

**Prereqs.** M0; B0.

**Scope: override-only.** The earlier ambition of emitting a full-compose alongside is **deferred to M6**. Reason: invoice-parse's resources (postgres, redis, blob storage) aren't yet caravan-declared, and the `model-init` service has bespoke inline Python that doesn't fit the yaml schema. M1 stays focused on the delta-override path; full-compose reconstruction lands when M6 brings invoice-parse's other resources under caravan declaration.

**Work.** Phase-5 compose emitter (`internal/compiler/emit/compose.go`). One emitter: `EmitComposeOverride(*ResolvedPlan) []byte`. Per-language seam-server `command:` emission via a pluggable interface (M1 ships Python; M2 plugs in Rust). Yaml-spec extensions: `seams.X.impl: <module:Class>` and optional `seams.X.service_name`.

**Acceptance.** Generated override produces the same extraction (semantic JSON equality) as B0's criterion #5 — sample_invoice.pdf run through `docker compose -f infra/docker-compose.yaml -f infra/dev-bootstrap/generated/docker-compose.override.generated.yaml --profile app up` matches `.b0-runs/c1/extraction.json`. 17 successful POSTs through the emitted `llm-extractor` service (one per ingested invoice).

**Progress (M1 — DONE):**

Compose emit (`caravan/internal/compiler/emit/`):
- [x] `internal/compiler/emit/seam_server.go` — pluggable `SeamServerCommand` interface; `SeamServerCommands` map keyed by `Language`. Python implementation ships at M1 (`python -m caravan_rpc.serve --interface NAME --impl module:Class --port N`). `detectLanguage` heuristic reads `seam.Impl` shape. Rust (`LanguageRust`) is enumerated but its emitter lands at M2. — 2026-05-20
- [x] `internal/compiler/emit/compose.go` — `EmitComposeOverride(*ResolvedPlan) []byte`. Builds the override yaml via `yaml.Node` for stable key order. `buildConsumerOverride` injects `CARAVAN_RPC_PEERS` + `CARAVAN_RPC_SHARED_SECRET` + `depends_on` edges to peer services. `buildSeamPeerService` dispatches via `SeamServerCommands[lang]`. Command-arg items use `DoubleQuotedStyle` to satisfy docker compose v2's schema (`command.N must be a string`). — 2026-05-20
- [x] `cmd/caravan/main.go` — `compile --target=X` writes real `docker-compose.override.generated.yaml` for `runtime: docker-compose` targets (HCL still placeholder until M4-cloud). — 2026-05-20
- [x] `internal/compiler/testdata/dev-bootstrap.override.golden.yaml` — golden file, refreshed via `go test -update`. — 2026-05-20
- [x] `internal/compiler/emit/compose_test.go` — `TestEmitComposeOverride` (golden-file diff) + `TestEmitComposeMatchesB0Shape` (load-bearing substring assertions). — 2026-05-20

End-to-end acceptance:
- [x] `go test ./...` green (10/10 across `internal/compiler` + `internal/compiler/emit`). — 2026-05-20
- [x] `docker compose -f infra/docker-compose.yaml -f infra/dev-bootstrap/generated/docker-compose.override.generated.yaml config --quiet` passes (schema-valid). — 2026-05-20
- [x] M1 generated override + base compose → sample_invoice.pdf processing → postgres `extraction_data` **IDENTICAL** (deep dict equality) to `.b0-runs/c1/extraction.json` from B0. Confirms the M1-emitted compose dispatches LLMExtraction through the peer service end-to-end producing byte-equivalent outputs to the hand-edited B0 path. — 2026-05-20
- [x] 15 successful `POST /_caravan/rpc/LLMExtraction/extract` 200s in `llm-extractor` logs during the test batch (15 of 17 enqueued jobs completed within the poll window; sample_invoice.pdf delivered cleanly). — 2026-05-20

### M2 — Rust SDK, code-rag flips one seam ⬅ CRITICAL THESIS PROOF (4–6 sessions)

**Demo.** code-rag's Axum HTTP server runs under `caravan compile --target=dev-monolith` with Embedder going through real `caravan-rpc`. yaml line changes `Embedder: container`, recompile + recompose. Same `curl /query`, same response, but `docker logs` shows the embedder responding via HTTP. `git diff -- crates/` is empty.

**Prereqs.** M1; B0p (code-rag stub track already has SDK-wrapped call sites).

**Work — three components in parallel:**
1. **`caravan-rpc` SDK**. `#[wagon]` proc-macro emits trait + server-adapter + client-adapter. `provide::<dyn T>()` registry. `client::<dyn T>()` dispatcher reads `CARAVAN_RPC_PEERS`. axum HTTP server adapter on `/_caravan/rpc/<iface>/<method>`. **No-config inertness**: identical to Python — unset env var → direct call on registered impl. Start with hand-written adapters; lift to proc-macro after runtime is stable.
2. **Compiler phase 4** (peer-table computation): per target, per deploy unit, compute `CARAVAN_RPC_PEERS` JSON — one entry per seam, each carrying that seam's mode independently. Phase 5 injects as `environment:`.
3. **code-rag dependency bump**: bump `caravan-rpc` from 0.0.x (placeholder, declaration-only trait surface) to 0.1.0 (functional with `#[wagon]` proc-macro + axum HTTP adapter + dispatcher). Same crate, new version. code-rag's source unchanged at call sites — the trait surface stays compatible.

**Acceptance.**
- PoC testability conditions 5/6/7/8 from [poc_yaml_spec.md](poc_yaml_spec.md) §Testability pass when toggling `Embedder` between `inproc` and `container`.
- Wire format matches [poc_rpc_sdk.md](poc_rpc_sdk.md) §2 byte-for-byte (golden HTTP capture).
- No-config-inertness: `cargo run -p code-rag-chat` against code-rag's existing local config works with no Caravan env var set.
- WASM standalone still builds: `trunk build --release --features standalone` succeeds.
- Single-binary entry: `cargo build --release -p code-rag-mcp` produces a stdio-mode binary that runs unchanged.

**Risks.**
- Rust proc-macro for `#[wagon]` — generic erasure (`dyn T`), `async_trait`, JSON arg encoding. Budget for a redesign loop.
- "Both deploy units carry the embedder code in monolith mode, but only one runs it" — handle local-impl-inert correctly per [poc_rpc_sdk.md](poc_rpc_sdk.md) §3.
- Bearer-secret strategy: lock to compiler-emitted hex for PoC; defer SSM persistence to v0.2.

**Decision gates before M2.** Pin Rust SDK HTTP server (axum). Pin shared-secret strategy (compiler-emitted hex).

**M2 plan ratified 2026-05-20** (see `caravan/.claude/plans/understand-caravan-thesis-read-tranquil-brooks.md` or equivalent plan file):

Key decisions:
- All 4 seams in scope (Embedder, Reranker, VectorReader, LlmClient). VectorReader's 16 methods + tuple returns + Option args drive worst-case proc-macro design.
- Proc-macro handles **both sync and async traits** — sync uses `reqwest::blocking`, async uses async reqwest. Trait signatures in code-rag's `seams.rs` stay unchanged.
- Peer service via **compiler-emitted synthetic Rust crate** per container-mode seam (`infra/<target>/generated/peers/<seam-kebab>/{Cargo.toml, src/main.rs, Dockerfile}`). User code stays at three-point contract (`#[wagon]` / `provide` / `client`).
- Three targets to exercise per-seam mixability (user direction): `dev-monolith` (all inproc), `dev-split-light` (`Embedder: container`), `dev-split-mixed` (`Embedder` + `LlmClient` both `container`). All-4-container intentionally not a target (no extra proof, heavy infra).
- Wire format byte-for-byte with Python (`preserve_order` on serde_json).
- **One wagon = one peer service group** invariant: N call sites → 1 peer service per seam, never per call site. Compiler already enforces this for Python at M1; Rust path inherits.

**Progress (M2 — IN PROGRESS):**

Session 1 — SDK runtime scaffold (DONE 2026-05-20):
- [x] [Cargo.toml](../rpc/rust/caravan-rpc/Cargo.toml) bumped to `0.1.0`; deps added (`serde`, `serde_json` with `preserve_order`, `reqwest` blocking+async, `axum`, `tokio`, `thiserror`); feature gates `default = ["client", "server"]` so `code-rag-engine`'s WASM build continues to work via `default-features = false`.
- [x] [src/errors.rs](../rpc/rust/caravan-rpc/src/errors.rs) — `RpcRemoteError`, `RpcTransportError`, `RpcError` mirror the Python error split.
- [x] [src/codec.rs](../rpc/rust/caravan-rpc/src/codec.rs) — `Request{args,kwargs}` / `Response::{Ok,Err}` envelope + `path_for(iface, method)` helper. 8 unit tests; `preserve_order` keeps map field order Python-compatible byte-for-byte.
- [x] [src/peers.rs](../rpc/rust/caravan-rpc/src/peers.rs) — `PeerEntry::{Inproc, Http{url}, Lambda{function_url}}` + `parse(json) -> HashMap<String, PeerEntry>` + cached `peer_for(name)`. 7 unit tests; rejects unknown modes and missing URL fields. Bad JSON in env var loud-fails at first lookup.
- [x] [src/dispatch.rs](../rpc/rust/caravan-rpc/src/dispatch.rs) — `dispatch_sync(base_url, iface, method, args) -> Result<Value, RpcError>` + `dispatch_async(...)` — the HTTP client helpers the proc-macro will call from generated client adapters. Gated behind `client` feature. 4 unit tests cover URL joining, bearer header format, status checks (incl. body truncation).
- [x] [src/lib.rs](../rpc/rust/caravan-rpc/src/lib.rs) — re-exports new modules; removed the pre-M2 HTTP-mode panic (we are at M2 now); kept Lambda-mode panic with M7 pointer. `client::<T>()` retains B0p inproc behavior until Session 3 wires the proc-macro adapter discovery.
- [x] [tests/registry.rs](../rpc/rust/caravan-rpc/tests/registry.rs) updated — `http_mode_env_falls_back_to_local_impl_until_session_3` replaces the pre-M2 panic test; all 9 integration tests green.
- [x] `cargo test --workspace` clean (32 tests across lib + integration). `cargo clippy --workspace --all-targets -- -D warnings` clean. `cargo fmt --all` clean. `cargo check --workspace` from code-rag clean (downstream still builds). `cargo build --target wasm32-unknown-unknown --no-default-features -p caravan-rpc` succeeds — feature gating works.

Session 2 — axum server + first hand-written adapter (DONE 2026-05-20):
- [x] [src/server.rs](../rpc/rust/caravan-rpc/src/server.rs) — `MethodHandler` type alias (`Arc<dyn Fn(Bytes) -> Pin<Box<dyn Future<Output = Response>>>`); `RpcRouter::{new, add_method, into_axum_router}` builder; central axum POST handler that validates `X-Caravan-Rpc-Version`, optional `Authorization: Bearer`, interface-name match, method existence, then invokes the per-method handler. `serve_forever(addr, router)` for the synthetic peer crate's main.rs at Session 5.
- [x] [tests/end_to_end_http.rs](../rpc/rust/caravan-rpc/tests/end_to_end_http.rs) — hand-coded `TestEmbedder` trait + `EchoEmbedder` impl + `TestEmbedderHttpClient` client adapter + `build_test_embedder_router` server adapter. The hand-written shape mirrors what the Session 3 proc-macro will emit, so the macro has a working reference. 5 tests cover: embed roundtrip (97/98/99/0 float bytes), zero-arg `dimension()` roundtrip, unknown-method → 404 + envelope, missing wire-version header → 400, bearer-auth-enforced 401/200 with secret configured.
- [x] All 37 tests green (23 lib + 9 registry + 5 end-to-end); `cargo clippy --workspace --all-targets -- -D warnings` clean; `cargo fmt --all` clean.
- [x] Verified: sync trait method `client.embed(text)` called via `tokio::task::spawn_blocking` from inside a tokio test runtime works correctly — confirms the "sync trait + reqwest::blocking" design from M2 plan §Concerns #1.

Session 3 — Proc-macro v1 (sync + owned-args path) (DONE 2026-05-20):
- [x] [caravan-rpc-macros/Cargo.toml](../rpc/rust/caravan-rpc-macros/Cargo.toml) deps `syn 2 (features=full)`, `quote 1`, `proc-macro2 1`.
- [x] [caravan-rpc-macros/src/lib.rs](../rpc/rust/caravan-rpc-macros/src/lib.rs) — `#[wagon]` parses `syn::ItemTrait`; conservative shape gate (`is_sync_owned_trait`) detects async (`async_trait` attr OR `async fn`) and borrowed-arg types (`&T`, `&[T]`, recursing through generic args). Eligible traits emit `<Trait>HttpClient` + `impl Trait for <Trait>HttpClient` (via `dispatch::dispatch_sync`) + `build_<trait_snake>_router(impl_arc) -> axum::Router` (one route per method, decodes args via `serde_json::from_value`, calls impl, encodes return).
- [x] Wire-error model: macro-generated client adapter serializes the impl's full `Result<T, E>` over the wire (uses serde's default Result encoding). Server side wraps Ok value in `Response::ok`; client side deserializes Result<T,E> from `result` field. Diverges from Python's `{ok: false, error}` envelope for user errors — Rust uses in-band Result encoding because Rust error types are typically not constructable from `(code, message)` strings. Documented; cross-language interop tested at M9.
- [x] All 4 code-rag traits hit the identity fallback at Session 3 — Embedder has `&str`, Reranker has `&str`, LlmClient/VectorReader have `#[async_trait]`. `cargo check --workspace` from code-rag still clean — zero source changes in code-rag.
- [x] [tests/macro_sync_owned.rs](../rpc/rust/caravan-rpc/tests/macro_sync_owned.rs) — synthetic `DemoEmbedder` trait (owned `String` args, `Result<Vec<f32>, DemoError>` returns). 4 tests: embed roundtrip, batch roundtrip, zero-arg `dimension`, and `Err(DemoError::Empty)` passing through wire encoding.
- [x] 41 total tests green (23 lib + 5 e2e + 4 macro + 9 registry). Clippy + fmt clean.

Session 4-narrow — Borrowed args + inventory dispatcher (DONE 2026-05-20):
- [x] Borrowed-arg lowering via `ArgLowering` — `&str` → decode `String`, pass `&name`; `&[&str]` → decode `Vec<String>`, build `Vec<&str>`, pass `&borrowed`; `&[T]` → decode `Vec<T>`, pass `&name`. Embedder's `embed_one(&self, text: &str)` + `embed_batch(&self, texts: &[&str])` now hit full codegen.
- [x] [tests/macro_sync_borrowed.rs](../rpc/rust/caravan-rpc/tests/macro_sync_borrowed.rs) — 4 tests against `BorrowedEmbedder` trait byte-for-byte mirroring code-rag's Embedder: `&str` arg, `&[&str]` arg, no-arg, Err arm pass-through.
- [x] [HttpAdapterFactory](../rpc/rust/caravan-rpc/src/lib.rs) — `inventory::submit!` from macro emits one per full-codegen trait; `client::<dyn T>()` looks up by `TypeId`, routes to `<Trait>HttpClient` when peer table says http.
- [x] `__macro_support` module re-exports `serde_json`, `inventory`, `async_trait`, `axum` so user crate only declares `caravan-rpc` as a dep.
- [x] **Edition + MSRV bumped** to `edition = "2024"`, `rust-version = "1.95"` (matched installed rustc; user direction 2026-05-20). Squat-era placeholder values (1.75 / 2021) replaced.
- [x] **Edition 2024 made `env::set_var` unsafe — refactored tests to thread-local override** (`peers::__set_table_override_for_tests`) instead of mutating process env. Zero `unsafe` in tests; parallel-safe (per-thread); cleaner than the old `ENV_LOCK`-serialized approach. Removed the broad `panic_if_lambda_dispatch_configured` — Lambda panic now fires inline in `try_client` when an inventory factory exists AND peer table says Lambda, so it only triggers for full-codegen traits.
- [x] [tests/registry.rs](../rpc/rust/caravan-rpc/tests/registry.rs) rewritten — `PeerOverride::set([...])` drop-guard replaces `EnvVarGuard`. New test `http_mode_override_returns_macro_generated_http_client` directly verifies the inventory + peer-table wiring: with HTTP override and registered local impl, `client::<dyn HttpSeam>()` returns the HttpClient adapter (Arc-pointer-distinct from the local impl).
- [x] **Transitional `#[wagon(identity)]` opt-out** — applied to code-rag's Reranker because `fastembed::RerankResult` lacks serde. `#[wagon]` defaults to full codegen; `#[wagon(identity)]` forces identity expansion. Reranker stays identity until M5 where a `RerankResultWire` shim will land.
- [x] code-rag verification: `cargo check --workspace` clean. Embedder hits full codegen (sync + borrowed lowerable), Reranker hits identity via explicit `#[wagon(identity)]`, LlmClient + VectorReader hit identity via `#[async_trait]` detection. Inventory entry present for Embedder.
- [x] All 45 tests green (23 lib + 5 e2e + 4 owned-macro + 4 borrowed-macro + 9 registry). Clippy `-D warnings` clean. Fmt clean.

Session 4-async (DONE 2026-05-20):
- [x] `classify_trait` returns `TraitMode::{Sync, Async}` based on `#[async_trait]` attribute or `async fn` methods. Mixed-mode traits (some async, some sync) bail to identity. Same arg-lowering + return-no-borrow checks apply.
- [x] `expand_trait` emits `#[async_trait::async_trait]` on the impl block when async. `emit_client_method` uses `dispatch_async(...).await` for async; `emit_server_handler` awaits the impl call for async. `async-trait` crate re-exported via `__macro_support` so user crates don't need a direct dep.
- [x] [tests/macro_async.rs](../rpc/rust/caravan-rpc/tests/macro_async.rs) — `DemoLlmClient` (`#[async_trait]`, `async fn generate(&self, prompt: &str) -> Result<String, DemoLlmError>`). 2 tests: roundtrip + error-arm pass-through.
- [x] **All four code-rag traits now classify correctly:**
  - Embedder (sync + `&str` + `&[&str]`) → full codegen ✓
  - Reranker → identity via explicit `#[wagon(identity)]` (fastembed::RerankResult lacks serde; M5)
  - LlmClient (`#[async_trait]` + `&str` + serde-ready `String` / `LlmError`) → full codegen ✓
  - VectorReader (`#[async_trait]` + many serde-ready `code_rag_types::*Chunk` returns) → full codegen ✓
- [x] **LlmClient cannot be flipped to `container` at M2** despite full macro support — the `RigGeminiImpl` impl lives in the chat binary crate (`code-rag/src/engine/generator.rs`), not a library, so the synthetic peer crate can't import it. Move-to-library is an M3+ refactor.
- [x] 47 Rust tests green; clippy + fmt clean; code-rag `cargo check --workspace` clean.

Session 5 cleanup (DONE 2026-05-20):
- [x] [code-rag/.gitignore](../../code-rag/.gitignore) — added `/infra/` + `/caravan.exe` so caravan-generated artifacts + the local caravan binary copy don't get committed.

Session 5 — Compiler Rust path (DONE 2026-05-20):
- [x] [internal/compiler/emit/seam_server.go](../internal/compiler/emit/seam_server.go) `detectLanguage` now distinguishes Rust (`crate::Type`) from Python (`module.path:Class`) by checking for `::` first. `LanguageRust` is intentionally absent from `SeamServerCommands` map — Rust peers don't use the command-override pattern.
- [x] [internal/compiler/emit/rust_peer.go](../internal/compiler/emit/rust_peer.go) NEW — `EmitRustPeerCrate(seam, opts)` returns three file bodies (`Cargo.toml`, `src/main.rs`, `Dockerfile`). The synthetic `main.rs` constructs `<crate>::<Type>::new()`, calls `caravan_rpc::provide::<dyn <crate>::seams::<Iface>>(impl)`, builds the macro-generated `build_<snake>_router`, and `serve_forever`s. Cargo.toml has an empty `[workspace]` table so the peer is its own workspace (not absorbed into the user repo's workspace tree).
- [x] [internal/compiler/emit/compose.go](../internal/compiler/emit/compose.go) `buildSeamPeerService` branches per-language: Python keeps M1's command-override shape; Rust emits `build:` pointing at `infra/<target>/generated/peers/<svc>/Dockerfile` with no `command:` (the synthetic binary is its own entrypoint).
- [x] [cmd/caravan/main.go](../cmd/caravan/main.go) `writeRustPeerCrates` iterates the resolved target's container-mode Rust seams, computes path-resolution context (caravan-rpc + impl crate relative paths from the peer's Cargo.toml; in-Docker-context paths from the build root), calls `EmitRustPeerCrate`, and writes all three files plus a copy of the user repo's `Cargo.lock` into the peer dir.
- [x] [code-rag/caravan.yaml](../../code-rag/caravan.yaml) authored with 2 targets active for M2: `dev-monolith` (all seams inproc) + `dev-split-light` (`Embedder: container`). `dev-split-mixed` deferred until async-trait macro support lands.
- [x] **Cargo.lock-copy fix for transitive-dep determinism** — `cargo` in the synthetic peer's standalone workspace resolved `lance 1.0.1` (which has a `query depth limit overflow` bug in async block layout), while the user repo's workspace pinned `lance 1.0.0` via its lockfile. Root-cause fix: `writeRustPeerCrates` copies the user repo's `Cargo.lock` into the peer dir, so the peer inherits the user's tested version set. No `#![recursion_limit]` workaround in the synthetic main.rs.
- [x] `caravan compile --target=dev-monolith` writes the override (env-only, no peer services). `caravan compile --target=dev-split-light` writes the override + `peers/embedder/{Cargo.toml, src/main.rs, Dockerfile, Cargo.lock}`. Both generations succeed; emitted artifacts validated by hand-reading.
- [x] `go test ./...` clean; all 45 Rust tests clean.

Session 5 — Known caveat (Session 6 to verify):
- Local `cargo check` of the synthetic peer on Windows fails on a `cmake`/C-toolchain build script of a deep transitive dep (likely `lzma-sys` or similar via lancedb). Not a caravan issue — the Docker `rust:1.95` image has these tools by default. Verified at Session 6 via `docker compose build`.

Session 6 — Acceptance battery (DONE 2026-05-21):

**Architecture pivot: Option A** (user direction 2026-05-21). The synthetic-peer-with-its-own-Dockerfile approach was scrapped because it forced Caravan to pick rust + runtime image versions, fighting the "user owns Dockerfile" principle. Replaced with:

- Synthetic peer crate is a workspace member of the user repo (`workspace.members += "infra/peers/*"`). Cargo's existing `cargo build --workspace` in code-rag's Dockerfile compiles peer binaries alongside the chat binary.
- Compose's Rust-peer service reuses the user's chat image (same `dockerfile`, `target: chat`) with a `command:` override running `/app/caravan-peer-<svc>`. One image, multiple binaries, CMD-selected — matches Python's `llm-extractor` pattern (same image, command override) in spirit.
- Caravan-emitted files per Rust peer: just `Cargo.toml` + `src/main.rs` (no Dockerfile, no .dockerignore, no Cargo.lock copy).
- Peers live at `<user-repo>/infra/peers/<svc>/` — flat, one-level glob (Cargo doesn't expand multi-segment globs reliably). Caravan clears `infra/peers/` on each `caravan compile` so target switches yield only the current target's peers.

**code-rag changes (one-time M2 prep, three small structural edits in user repo):**
- [x] [code-rag/Cargo.toml](../../code-rag/Cargo.toml) — `workspace.members += "infra/peers/*"`.
- [x] [code-rag/dockerfile/Dockerfile](../../code-rag/dockerfile/Dockerfile):
  - Build stage: COPY caravan/rpc/rust/{caravan-rpc,caravan-rpc-macros} so the B0p path-dep resolves.
  - Bump base to `rust:1.95-slim` (was 1.92, below caravan-rpc's MSRV).
  - Dummy-source step adds `infra/peers/_caravan_marker/{Cargo.toml,src/main.rs}` — a no-op sentinel ensuring Cargo's `infra/peers/*` glob has ≥1 match (Cargo errors on zero-match globs).
  - COPY `code-rag/infra ./infra` after the real-source COPYs so peer crates appear before the final cargo build.
  - Post-build: collect `caravan-peer-*` binaries into `/caravan-bin/` via a shell loop (no-op-safe when no peers exist).
  - Chat target: `COPY --from=builder /caravan-bin/. ./` brings peer binaries into the chat image alongside `code-rag-chat`.
- [x] [code-rag/.gitignore](../../code-rag/.gitignore) — `/infra/` and `/caravan.exe` ignored.

**End-to-end acceptance (verified 2026-05-21):**
- [x] `caravan compile --target=dev-monolith` writes env-only override (no peer service); `caravan compile --target=dev-split-light` writes the override + `infra/peers/embedder/{Cargo.toml,src/main.rs}`.
- [x] `docker compose -f docker-compose.yaml -f infra/dev-monolith/generated/docker-compose.override.generated.yaml up -d --build` brings chat up; `curl POST /chat {"query":"how does caravan rpc work"}` returns 11 chunks with intent=`relationship`.
- [x] `docker compose ... -f infra/dev-split-light/generated/... --profile app up -d --build` brings chat + embedder peer up; same query returns 11 chunks with **byte-identical chunk_ids** in the same order, intent=`relationship`. Same source, yaml flip, different deployment, identical response.
- [x] **HTTP dispatch verified empirically**: `docker stop code-rag-embedder-1`; chat query panics with `caravan-rpc: dispatch_sync: Transport(Http("error sending request for url (http://embedder:8080/_caravan/rpc/Embedder/embed_one)"))` — proves the macro-generated client adapter is routing to the peer over the wire (not falling back to local FastEmbedImpl).
- [x] `git diff -- crates/ src/` empty between monolith and split-light runs — zero Rust source edits between target flips. The thesis claim ("yaml flips dispatch without source edits") empirically holds.
- [x] One wagon = one peer service group: 6 `client::<dyn Embedder>()` call sites in code-rag (handlers, retriever, runner, mcp) all dispatch to the single `embedder` compose service.

**Deferred to post-M2 close-out (M9 / merge):**
- [ ] WASM rebuild (`trunk build --release --features standalone`).
- [ ] Lockstep version bump of caravan-rpc + caravan-rpc-macros to 0.1.0 + crates.io publish.
- [ ] `dev-split-mixed` target with `LlmClient: container` once `RigGeminiImpl` moves out of the chat binary crate into a library (M3+ work — orthogonal to thesis proof).

### M2 Path B refactor (DONE 2026-05-21)

User flagged the M2-execute architecture as repo-hardcoded (`target: chat` literal, `_caravan_marker` sentinel in Dockerfile, workspace.members + `infra/peers/*` glob, peer-binary collection in Dockerfile, COPY `/caravan-bin/.` in chat target). Refactored to mirror Python's pattern: SDK is the peer entry; same binary, role-switched via env var.

- [x] **`caravan_rpc::run_or_serve(user_main)` added** (SDK contract point #4 alongside `#[wagon]` / `provide` / `client`). Reads `CARAVAN_RPC_ROLE`: unset → invokes `user_main` (inert pass-through); `peer-<Interface>` → inventory-discovers the macro-emitted server factory, builds the router, `serve_forever`s.
- [x] **`HttpServerFactory`** inventory entry — proc-macro emits one per `#[wagon]` trait. `build_router_from_registry` does `try_client::<dyn T>()` for the impl then calls macro-generated `build_<trait>_router(impl)`.
- [x] **Compose emit rewrite** ([compose.go::buildRustPeerService](../internal/compiler/emit/compose.go)) — Rust peers reuse the consumer entry's image (`target: chat` from `entries.<X>.runtime_target` yaml), no command override, no synthetic crate. `CARAVAN_RPC_ROLE=peer-<Iface>` env var is the entire dispatch trigger.
- [x] **YAML schema extensions**: `entries.<X>.runtime_target` (Dockerfile stage to reuse for peers) + `entries.<X>.env_file` (inherited by peer services that reuse the entry's image; per-seam `seam.env_file` overrides). Peers run the same binary as the consumer → same env-var startup needs → inheriting is the right default.
- [x] **rust_peer.go DELETED.** `writeRustPeerCrates` shrunk to a no-op that just clears any stale `infra/peers/` left by the M2-original architecture.
- [x] **code-rag changes reverted to minimum:**
  - [code-rag/Cargo.toml](../../code-rag/Cargo.toml) — `workspace.members` back to `["crates/*"]` (no `infra/peers/*` glob).
  - [code-rag/dockerfile/Dockerfile](../../code-rag/dockerfile/Dockerfile) — sentinel marker, COPY infra, peer-binary collection, COPY `/caravan-bin/.` ALL removed. Only the B0p caravan-rpc COPY survives (orthogonal path-dep prerequisite).
  - [code-rag/src/main.rs](../../code-rag/src/main.rs) — wrapped the chat-server launch in `caravan_rpc::run_or_serve(|| async move { … }).await`. One structural line added.
  - [code-rag/caravan.yaml](../../code-rag/caravan.yaml) — added `runtime_target: chat` + `env_file: .env` on the `code-rag-chat` entry.
- [x] **Acceptance re-verified 2026-05-21:**
  - `caravan compile --target=dev-split-light` emits compose override only — no synthetic crate files anywhere.
  - `docker compose build` produces the chat image; `up -d` brings both `code-rag-chat` and `code-rag-embedder-1` from the same image. Embedder peer logs `caravan peer Embedder serving on 0.0.0.0:8080` (proves `run_or_serve` detected the role and detoured).
  - `curl POST /chat {"query":"how does caravan rpc work"}` returns chunk_ids **byte-identical** to the monolith run + Path A run: `1d5fdac…, 633e1fe…, …, 1f3da8d…` (11 sources, same order, intent=`relationship`).
  - `docker stop code-rag-embedder-1` → chat panics with `caravan-rpc: dispatch_sync: Transport(Http("error sending request for url (http://embedder:8080/_caravan/rpc/Embedder/embed_one)"))`. HTTP dispatch is real, not local fallback.
  - `git diff -- crates/ src/` still empty between target flips (the `run_or_serve` wrap is a one-time B0p-style adoption, not a per-target change).
- [x] All Rust tests green (50 tests: 23 lib + 5 e2e + 4 owned + 4 borrowed + 9 registry + 2 async + 3 run_or_serve). Go tests clean.

**Net result:** caravan emit shrinks substantially (~250 lines of `rust_peer.go` deleted, simpler `buildRustPeerService`), user repo shrinks substantially (Dockerfile back to ~B0p shape, Cargo.toml back to one-glob-member). One new SDK contract point (`run_or_serve`) covers everything the synthetic peer crate used to do.

### M3 — Promote Python SDK from hand-typed to compiler-emitted (2–3 sessions, DONE 2026-05-21)

**Demo.** invoice-parse's compose-bootstrap override file is now produced by `caravan compile` rather than hand-edited. Two seams in invoice-parse (LLMExtraction from B0 + a second seam declared) flip per-seam between `inproc` and `container` via yaml.

**Prereqs.** M1; M2 (proves the per-seam env var contract end-to-end in another language first).

**Work.** Promote `caravan-rpc` Python from B0's 0.1.0 to compiler-emitted use. Compiler phase-2 language detection (`pyproject.toml` vs `Cargo.toml` in entry path). Manifest patching for `requirements.txt` (D9 — append-line semantics) — Caravan auto-adds `caravan-rpc>=0.1.0` to user manifests during compile.

**Acceptance.** invoice-parse runs end-to-end with two seams declared, mix-and-match per target. No env var → unchanged behavior (inertness preserved).

**Parallelizable with M4.**

**Progress (M3 — DONE):**

Compiler (`caravan/internal/compiler/`):
- [x] `internal/compiler/kinds.go` — `Language` type + constants (`LanguagePython`, `LanguageRust`, `LanguageTS`, `LanguageGo`, `LanguageUnknown`) relocated from `emit/seam_server.go` so Normalize and Emit share the surface. — 2026-05-21
- [x] `internal/compiler/types.go` — `Entry.Language` field (filled at phase-3 from manifest stat); `Plan.PatchedManifests []string` (paths emitted by EmitManifestPatches, surfaced for CLI logging). — 2026-05-21
- [x] `internal/compiler/normalize.go` — `validateEntryLanguages` validator: stats manifest files inside `entries.<name>.path`; `Cargo.toml` → rust, `pyproject.toml` / `requirements.txt` → python; coexistence = error, missing path = silent (synthetic test fixtures pass unchanged). — 2026-05-21
- [x] `internal/compiler/emit/accumulator.go` (NEW) — `composeAccumulator` with `AddService(name, svc)` / `AddEnv(consumer, key, value, source)` / `Render(targetName)`. Insertion-order preservation (keeps M1 golden byte-identical). Two-band env-var flush: `envSourceResource` first (alphabetic), `envSourceSeam` second (alphabetic), last-write-wins per key. `CARAVAN_RPC_` namespace reservation enforced — resource emit rejected if it writes a key with that prefix. — 2026-05-21
- [x] `internal/compiler/emit/compose.go` — `EmitComposeOverride` rewired through the accumulator. `buildConsumerOverride` replaced by `addConsumerSeamEnv(acc, ...)` that calls `acc.AddEnv(_, _, _, envSourceSeam)` for CARAVAN_RPC_PEERS + CARAVAN_RPC_SHARED_SECRET and `acc.AddService` for depends_on edges. M4's resource emit plugs into the same accumulator surface. — 2026-05-21
- [x] `internal/compiler/emit/manifest.go` (NEW) — `EmitManifestPatches(rp, outDir, userRepoRoot) ([]string, error)` writes per-target patched `requirements.txt` to `infra/<target>/generated/build-context/<entry.Path>/`. D9 error-on-version-mismatch policy implemented. Composable `[]ManifestPatch{Distribution, Spec, Reason}` API so M4-cloud / M5 / M6 can drop in additional patches without rewriting the file-write path. Python branch emits `caravan-rpc>=0.1.0.dev0` (PEP-440-compatible with the in-tree dev0 wheel; ties out via `--find-links /vendor/` until M9 PyPI publish). — 2026-05-21
- [x] `cmd/caravan/main.go` — `runCompile` wires `emit.EmitManifestPatches` after rust-peer emit; populates `Plan.PatchedManifests` for CLI surfacing. — 2026-05-21
- [x] 13 new tests, all green: `validateEntryLanguages` (6 cases: python/rust/coexistence/missing-path), accumulator (5 cases: insertion order, env band ordering, CARAVAN_RPC_ namespace rejection, last-write-wins, merge), manifest patcher (9 cases + 3 disk-write: absent/compatible/conflict, Python entry writes, Rust entry skipped, missing user manifest), two-seam fixture + mix-and-match (`TestEmitComposeOverride_TwoSeams` + `TestEmitComposeOverride_SeamMixAndMatch`). M1 golden byte-identical through the accumulator refactor. — 2026-05-21

invoice-parse — Python SDK + second seam:
- [x] `services/processing/invoice_processing/ocr.py` — `@wagon class OCRText` interface + `PaddleOCRTextImpl` concrete impl with eager PaddleOCR load in `__init__` (peer's TCP port only opens after model ready, avoids cold-start race with consumer dispatch). Existing `process_ocr` rewired to call `client(OCRText).extract_text(...)`. — 2026-05-21
- [x] `services/processing/invoice_processing/worker.py` — `provide(OCRText, PaddleOCRTextImpl())` alongside existing `provide(LLMExtraction, ...)`. — 2026-05-21
- [x] `caravan.yaml` — `seams.OCRText` block (impl=`invoice_processing.ocr:PaddleOCRTextImpl`, service_name=`ocr-text`). `dev-bootstrap` flips both seams to container; new `dev-split-llm` target (LLM container + OCRText inproc) demos per-seam mix-and-match; `dev-inproc` flips both to inproc. — 2026-05-21
- [x] `services/processing/Dockerfile` — rewired to consume the compiler-patched `requirements.txt` via `pip install -r ... --find-links /vendor/`. `ARG CARAVAN_TARGET=dev-bootstrap` for cross-target rebuilds. Vendored wheel preserved (PyPI release deferred to M9 phase 1 close). — 2026-05-21
- [x] Inertness verified: `provide(OCRText, FakeOCR())` + `client(OCRText).extract_text(...)` routes inproc with no env vars set. User's on-disk `requirements.txt` untouched after compile. — 2026-05-21

End-to-end smoke (compose run deferred to M9-phase-1 / PyPI publish):
- [x] `caravan compile --target=dev-bootstrap`: emits override with `processing` consumer + `llm-extractor` + `ocr-text` peer services; CARAVAN_RPC_PEERS carries both interface entries; depends_on covers both peers in alphabetic order; patched manifest written to `infra/dev-bootstrap/generated/build-context/services/processing/requirements.txt`. — 2026-05-21
- [x] `caravan compile --target=dev-split-llm`: only `llm-extractor` peer emitted; OCRText → `{"mode":"inproc"}` in CARAVAN_RPC_PEERS. — 2026-05-21
- [x] `caravan compile --target=dev-inproc`: no peer services; both seams → `{"mode":"inproc"}`. — 2026-05-21
- [ ] **Deferred to user local-test session**: full `docker compose up` end-to-end with real PaddleOCR model load + Gemini API key + PDF input. Tested locally until M9 phase 1 publishes `caravan-rpc` to PyPI; M9 closure reruns surfaces 1-4 acceptance.

### M4 — Composition swap, compose-only resource emitters (Phase 1, 2–3 sessions, DONE 2026-05-21)

**Demo.** `composition.uploads: oss-local` boots MinIO in compose and sets `S3_ENDPOINT_URL`. Same Rust/Python code that previously hit local FS now talks to MinIO via env-var-driven endpoint override. **No HCL, no AWS in this milestone** — the `cloud-managed` half lands as M4-cloud in Phase 2.

**Prereqs.** M2.

**Work.** Compose-side resource emitters for the groups M5/M6 actually need:
- `bucket` group → MinIO + minio-init sidecar
- `search` group → OpenSearch container (code-rag's LanceDB stays as-is for v0.1; cloud swap is Phase 2 concern)
- `db.sql` group → Postgres container
- `cache` group → Redis container
- `queue` group → Redis Streams or RabbitMQ container

Each emitter injects the appropriate endpoint env var into deploy units that declare `uses: <resource>`.

**Acceptance.** Generated compose includes the right resource containers + env-var wiring. User code calling resource SDKs (s3, redis, etc.) succeeds against the local containers. yaml-line composition flip works between two oss-local providers (e.g., Redis-local ↔ RabbitMQ-local) where applicable.

**Decision gate before M4.** Pin manifest-patch conflict policy (error on version mismatch).

**Parallelizable with M3.**

**Progress (M4 — DONE 2026-05-21):**

Compiler (`caravan/internal/compiler/`):
- [x] `kinds.go` — added `ResourceVariant` type + constants (`VariantRedisStreams`, `VariantRabbitMQ`, `VariantPostgres`, `VariantMinIO`, `VariantRedis`, `VariantOpenSearch`). — 2026-05-21
- [x] `variants.go` (new) — `variantTable` mapping each ResourceKind to its valid variants (first = default); `ValidVariantsFor`, `DefaultVariantFor`, `IsValidVariant`. Single source of truth for normalize + resolve + emit. — 2026-05-21
- [x] `resource_endpoints.go` (new) — `EndpointEnvVars(*ResolvedResource) map[string]string` per (Type, Variant): bucket→S3_ENDPOINT_URL+AWS_*, db.sql→DATABASE_URL, cache+queue:redis-streams→REDIS_URL/QUEUE_URL=redis://redis:6379, queue:rabbitmq→QUEUE_URL=amqp://guest:guest@rabbitmq:5672, search→OPENSEARCH_URL. — 2026-05-21
- [x] `types.go` — `Resource.Variant` (yaml `kind:`), `CompositionOverride{Mode, Variant}` replacing `map[string]CompositionMode` on `Target.Composition`, `ResolvedResource{Name, Type, Composition, Variant}`, `ResolvedPlan.{ResolvedResources, ResourceEnvVars}`. — 2026-05-21
- [x] `parse.go` — extended `parseResource` to pull `kind:`; `parseCompositionMap` accepts both scalar (`oss-local`) and object (`{ mode: oss-local, kind: rabbitmq }`) forms via `parseCompositionOverride`. Back-compat preserved for M0 fixtures. — 2026-05-21
- [x] `normalize.go` — `validateResourceVariants` (each resource's `kind:` must be legal for its type, empty allowed) + `validateTargetCompositionOverrides` (per-target overrides validate mode + kind against resource type). — 2026-05-21
- [x] `resolve.go` — `resolveResources(plan, target)` folds per-target overrides onto resource declarations + applies type defaults. `buildResourceEnvVars(plan, resolved)` walks each entry's `Uses[]` and computes per-consumer endpoint env vars. Both stored on `ResolvedPlan`. — 2026-05-21

Emit (`caravan/internal/compiler/emit/`):
- [x] `resources.go` (new) — `resourceCatalog` map `(Type, Variant) → resourceBuilder`; `variantEngineName` map for `compose-service hostname` resolution (cache:redis and queue:redis-streams share one `redis:` container); `buildMinIOService`, `buildPostgresService`, `buildRedisService`, `buildRabbitMQService`, `buildOpenSearchService`. `emitResources(acc, rp, existing)` walks resolved resources, skips collisions, calls `acc.AddService`. `emitResourceEnvVars(acc, rp)` folds resource env vars into consumers tagged `envSourceResource`. — 2026-05-21
- [x] `base_compose_scan.go` (new) — `BaseComposeServiceNames(dir)` + `DiscoverBaseCompose(dir)`. Convention-based discovery: `infra/docker-compose.yaml` → `docker-compose.yaml` → `compose.yaml`. Read failure non-fatal (warn + fall back to "emit everything"). — 2026-05-21
- [x] `compose.go` — `EmitComposeOverride` extended with `baseComposeServices map[string]bool` arg; pipeline now: (1) consumer seam-env, (2) resource env vars via `emitResourceEnvVars`, (3) container-mode seam peer services, (4) resource containers via `emitResources` with collision skip. `composeService` gained `Image` + `Ports` fields; `serviceNode` renders them. — 2026-05-21

CLI (`cmd/caravan/main.go`):
- [x] `caravan compile` calls `emit.BaseComposeServiceNames(cwd)` before `EmitComposeOverride` and threads the result through. Read failure surfaces as a warning, not a hard error. — 2026-05-21

Test infrastructure (`caravan/internal/compiler/testdata/`):
- [x] `invoice-parse-bootstrap.yaml` — added `dev-rabbitmq-flip` target with `composition: { invoice_queue: { mode: oss-local, kind: rabbitmq } }`. — 2026-05-21
- [x] `invoice-parse-bootstrap.dev-rabbitmq-flip.spec.json` — golden showing `resolved_resources.invoice_queue.variant: rabbitmq` + `resource_env_vars.processing.QUEUE_URL: amqp://...`. — 2026-05-21
- [x] `dev-bootstrap.override.golden.yaml` — regenerated with full M4 resource emit (minio + postgres + redis services, AWS_*/DATABASE_URL/REDIS_URL/S3_ENDPOINT_URL env vars on processing). — 2026-05-21
- [x] `TestSpecJSON` — added `invoice-parse-dev-rabbitmq-flip` case. — 2026-05-21
- [x] `TestEmitComposeRabbitMQFlip` — asserts QUEUE_URL flips amqp://, rabbitmq service emitted, no redis:// QUEUE_URL anywhere. — 2026-05-21
- [x] `TestEmitComposeBaseComposeCollision` — when base set declares postgres + redis, those services are NOT emitted but DATABASE_URL / QUEUE_URL still inject into consumers; minio (no collision) still emits. — 2026-05-21

invoice-parse `caravan-conversion` branch:
- [x] `invoice-parse/caravan.yaml` — added `dev-rabbitmq-flip` target. No source-code edits. — 2026-05-21

End-to-end acceptance (verified 2026-05-21):
- [x] `go test ./...` green (32 tests across compiler + emit; accumulator + manifest tests inherited from M3).
- [x] `caravan compile --target=dev-bootstrap` from invoice-parse working dir: emits override with minio service (no postgres / redis emission because they're in hand-authored `infra/docker-compose.yaml`); injects AWS_*, DATABASE_URL, QUEUE_URL=redis://, S3_ENDPOINT_URL into processing.
- [x] `caravan compile --target=dev-rabbitmq-flip`: emits new `rabbitmq:` service + flips processing's QUEUE_URL to `amqp://guest:guest@rabbitmq:5672`. Source diff between dev-bootstrap and dev-rabbitmq-flip runs is empty — the thesis claim on the composition dimension holds.
- [x] `caravan compile --target=dev-inproc`: still works; seam dispatch all inproc, resource env vars still wired.

Deferred to M5 / M6:
- [ ] code-rag-side resource declarations (LanceDB stays embedded at Phase 1 per dev plan; no Caravan resource declared yet).
- [ ] OpenSearch end-to-end demo on code-rag (deferred to M5+ when code-rag declares a `search` resource).
- [ ] Full invoice-parse migration onto Caravan-owned containers (M6 deletes hand-authored postgres/redis/blob services from invoice-parse's compose).

Key decisions ratified during M4:
- **Resource collision handling**: skip emission when same-named service exists in base compose; env vars still inject. Per user direction 2026-05-21.
- **Composition flip demo**: queue redis-streams ↔ rabbitmq is the only Phase-1-feasible composition flip (other groups have one OSS-local choice each). Demonstrated end-to-end on invoice-parse.
- **Schema extension**: `composition:` accepts both scalar (back-compat) and object form `{ mode, kind }`. No breaking change to M0/M1 yaml.
- **Manifest-patch conflict policy (D9)**: error on version mismatch. M3 owns the policy in `internal/compiler/emit/manifest.go`; M4 inherits without further edit.
- **Engine-name vs variant-name**: catalog `variantEngineName` map decouples user-facing variant ID from compose-service hostname. Allows shared engines (cache:redis + queue:redis-streams → one `redis:` container).

### M5 — code-rag full Caravan target (3–4 sessions, DONE 2026-05-21)

**Demo.** code-rag declares all four seams (Embedder, Reranker, VectorReader, LlmClient) via SDK. A target sets each seam's mode independently — e.g., `Embedder: container, Reranker: container, VectorReader: inproc, LlmClient: inproc`. `curl /query` returns identical results regardless of which seams are split.

**Prereqs.** M2; M4 (compose-only `search` resource emitter — cloud variant deferred to M4-cloud).

**Work** (per [../../code-rag/docs/caravan-readiness.md](../../code-rag/docs/caravan-readiness.md)):
- Promote Embedder/Reranker to traits behind `#[wagon]` (most of this is done by B0p).
- Split `VectorStore` → `VectorReader` + `VectorWriter` + separate `call_edges` resource.
- Extract `code-rag-core` crate to break `code-rag-mcp`'s transitive dep on `code-rag-chat`.
- Caravan-generated compose references existing [../../code-rag/dockerfile/Dockerfile](../../code-rag/dockerfile/Dockerfile) — do not restructure.

**Acceptance.** code-rag's existing test suite + all 5 deployment surfaces still work (per pre-change-state verification commands). Zero source-code edits between targets.

**Design pressure into SDK.** `Mutex<Embedder>` interior mutability forces `#[wagon]` to handle `Arc<Mutex<dyn T>>` patterns. Likely SDK addition: documented `provide_shared(...)` variant.

**Progress (M5 — DONE 2026-05-21):**

Decision ratified before execution:
- **`provide_shared(Arc<Mutex<dyn T>>)` not needed.** B0p already moved `Mutex<TextEmbedding>` interior to `FastEmbedImpl`; the outer trait object is `Arc<dyn Embedder>`, no Mutex at the seam boundary. The dev-plan hint reflected pre-B0p shape. SDK unchanged at M5. — 2026-05-21
- **`call_edges` split deferred** to Phase 2 (cloud-managed graph DB). `get_callers` / `get_callees` / `get_all_edges` stay on `VectorReader`; no separate `CallEdges` trait at M5. — 2026-05-21
- **`RigGeminiImpl` moves to a new `code-rag-llm` crate** (rather than `code-rag-store`) — clean separation; anticipates LLM concerns growing. — 2026-05-21

Session 1 — Reranker shim + Reranker full codegen (DONE 2026-05-21):
- [x] [crates/code-rag-store/src/seams.rs](../../code-rag/crates/code-rag-store/src/seams.rs) — replaced `pub use fastembed::RerankResult` with local `seams::RerankResult` (Debug/Clone/PartialEq/Serialize/Deserialize) + `From<fastembed::RerankResult>` conversion. Field shape identical (`document` / `score` / `index`) so call sites `rr.score` + `rr.index` in `src/engine/retriever.rs:189-191` keep compiling without source edits.
- [x] Reranker trait wagon attribute flipped from `#[wagon(identity)]` to `#[wagon]`. The proc-macro now emits the HTTP client + server adapters for it (sync + owned-args path).
- [x] [crates/code-rag-store/src/reranker.rs](../../code-rag/crates/code-rag-store/src/reranker.rs) — `MsMarcoRerankerImpl::rerank` converts `fastembed::RerankResult` → `seams::RerankResult` at the impl boundary via `.into_iter().map(seams::RerankResult::from).collect()`.
- [x] `cargo test --workspace --lib`: **126 passed**. `cargo clippy --workspace --all-targets`: clean.

Session 2 — VectorWriter split + code-rag-llm crate extraction (DONE 2026-05-21):
- [x] [crates/code-rag-store/src/seams.rs](../../code-rag/crates/code-rag-store/src/seams.rs) — added `VectorWriter` trait with `#[wagon(identity)] #[async_trait]`. 13 write methods: 6 upserts (CodeChunk + 5 chunk-types), 4 deletes (by-file, by-project, by-id, by-ids), `create_fts_indices`, `upsert_call_edges`, `delete_edges_by_project`. Identity wagon because writes are inproc-only by design (code-raptor ingest holds the concrete VectorStore; trait-typed wiring optional).
- [x] [crates/code-rag-store/src/vector_store.rs](../../code-rag/crates/code-rag-store/src/vector_store.rs) — `impl seams::VectorWriter for VectorStore` block (UFCS delegation to inherent methods, mirroring the existing `VectorReader` pattern).
- [x] **New `code-rag/crates/code-rag-llm/` crate.** Houses `RigGeminiImpl` (moved from `code-rag/src/engine/generator.rs`). Depends on `code-rag-store` (LlmClient trait + LlmError) + `rig-core` + `async-trait` + `anyhow`.
- [x] [code-rag/src/engine/mod.rs](../../code-rag/src/engine/mod.rs) — `pub mod generator` dropped; `RigGeminiImpl` re-exported from `code-rag-llm` for back-compat with existing `crate::engine::RigGeminiImpl` call sites.
- [x] [code-rag/Cargo.toml](../../code-rag/Cargo.toml) — added `code-rag-llm = { path = "crates/code-rag-llm" }`; dropped now-unused direct `rig-core = "0.27"` dep (root binary reaches it transitively through `code-rag-llm`).
- [x] `src/engine/generator.rs` deleted (no longer needed; `pub use code_rag_llm::RigGeminiImpl;` in `src/engine/mod.rs` covers it).

Session 3 — code-rag-core extraction + MCP de-coupling (DONE 2026-05-21):
- [x] **New `code-rag/crates/code-rag-core/` crate.** Carries the chat-side core that both `code-rag-chat` (root binary) and `code-rag-mcp` consume: `AppState` (state.rs), `SourceInfo` + `build_sources` (dto.rs), `retrieve` + `QueryContext` + `RetrievalResult` re-exports (retriever.rs, 746 lines moved verbatim), `EngineError` (errors.rs). Depends on code-rag-store + code-rag-engine + code-rag-types + code-rag-llm + caravan-rpc + tokio.
- [x] [code-rag/Cargo.toml](../../code-rag/Cargo.toml) — added `code-rag-core = { path = "crates/code-rag-core" }`.
- [x] [code-rag/src/api/state.rs](../../code-rag/src/api/state.rs) shrunk to `pub use code_rag_core::AppState;` re-export.
- [x] [code-rag/src/api/dto.rs](../../code-rag/src/api/dto.rs) — SourceInfo + build_sources + their tests moved to `code-rag-core::dto`; HTTP-only DTOs (ChatRequest/ChatResponse/HealthResponse/ProjectsResponse) stay; SourceInfo + build_sources re-exported for back-compat.
- [x] [code-rag/src/engine/retriever.rs](../../code-rag/src/engine/retriever.rs) shrunk to a 5-line re-export from `code_rag_core::retriever`.
- [x] [code-rag/src/engine/mod.rs](../../code-rag/src/engine/mod.rs) — `EngineError` now re-exported from `code-rag-core::errors`.
- [x] [crates/code-rag-mcp/Cargo.toml](../../code-rag/crates/code-rag-mcp/Cargo.toml) — `code-rag-chat = { path = "../.." }` REPLACED with `code-rag-core = { path = "../code-rag-core" }`. `cargo tree -p code-rag-mcp --depth 1` confirms no `code-rag-chat` in the dep tree.
- [x] [crates/code-rag-mcp/src/main.rs](../../code-rag/crates/code-rag-mcp/src/main.rs) — imports rewritten: `code_rag_chat::{api::{AppState, build_sources}, engine::{intent, retriever}}` → `code_rag_core::{AppState, build_sources, retriever}` + `code_rag_engine::intent`.

Session 4 — caravan.yaml + per-target compile verification (DONE 2026-05-21):
- [x] [code-rag/caravan.yaml](../../code-rag/caravan.yaml) — declared all 4 seams. Reranker impl: `code_rag_store::reranker::MsMarcoRerankerImpl`; VectorReader impl: `code_rag_store::vector_store::VectorStore`; LlmClient impl: `code_rag_llm::RigGeminiImpl`. Each seam has a `service_name` matching its peer hostname.
- [x] Added **2 new targets** (`dev-split-mixed` + `dev-split-heavy`) alongside the existing `dev-monolith` + `dev-split-light`. Mix-and-match coverage:
  - `dev-monolith`: all 4 inproc.
  - `dev-split-light`: Embedder container only.
  - `dev-split-mixed`: Embedder + Reranker container; VectorReader + LlmClient inproc.
  - `dev-split-heavy`: Embedder + Reranker + LlmClient container; VectorReader inproc (owns the embedded LanceDB filesystem, doesn't flip cleanly at Phase 1).
- [x] `caravan compile --target=<X>` for all 4 targets emits expected override:
  - dev-monolith: no peer services; `CARAVAN_RPC_PEERS` has all 4 seams as `inproc`.
  - dev-split-light: 1 peer service (`embedder`); `CARAVAN_RPC_PEERS.Embedder` http, rest inproc.
  - dev-split-mixed: 2 peer services (`embedder` + `reranker`); `CARAVAN_RPC_PEERS.{Embedder,Reranker}` http, rest inproc.
  - dev-split-heavy: 3 peer services (`embedder` + `reranker` + `llm-client`); only VectorReader inproc.
- [x] Each peer service has `CARAVAN_RPC_ROLE=peer-<Interface>` env var; `caravan_rpc::run_or_serve` detours into peer mode on this signal (no command override; same binary as the consumer).

Acceptance (cargo health, deferred external):
- [x] `cargo check --workspace`: clean.
- [x] `cargo test --workspace`: green (full workspace; lib + integration; no regressions).
- [x] `cargo clippy --workspace --all-targets`: clean (only docstring-formatting warnings; fixed).
- [x] `cargo build --release -p code-rag-chat`: success.
- [x] `cargo build --release -p code-rag-mcp`: success. **MCP no longer transitively depends on `code-rag-chat`** (verified via `cargo tree`).
- [ ] `trunk build --release --features standalone`: deferred (WASM standalone, requires `trunk` toolchain; engine/UI crates are not consumers of caravan-rpc per B0p, so no regression risk from M5 work).
- [ ] `docker compose build` of chat target: deferred (requires Docker daemon). M5 changes are all Rust-source-level; the existing Dockerfile + base compose are unchanged, so no regression risk.
- [ ] Manual `docker compose -f docker-compose.yaml -f infra/<target>/generated/docker-compose.override.generated.yaml up -d --build` per target + `curl POST /chat` byte-identical chunk_ids across all 4 targets: **acceptance gate for the M9 close**, not gated to M5 sessions.

Key decisions ratified during M5:
- **Reranker wire-shim pattern**: local newtype `RerankResult` in `seams.rs` with serde derives + `From<fastembed::RerankResult>` conversion. Same field names → zero source edits at call sites. Generalizable pattern for any future third-party-type-lacking-serde gap (M3 may port the pattern to Python at M6+ if needed).
- **VectorWriter trait scope**: 13 methods covering all writes. Identity wagon (no HTTP codegen) because writes are inproc-only by design. Code-raptor's ingest path holds the concrete `VectorStore`; trait exists for shape symmetry + future trait-typed wiring.
- **`code-rag-llm` crate** owns LLM impls (currently RigGeminiImpl; future providers slot in as sibling modules). `code-rag/Cargo.toml`'s direct `rig-core` dep removed — chat binary reaches `rig-core` transitively through `code-rag-llm`.
- **`code-rag-core` crate** owns the chat-side orchestrator. MCP imports directly; chat binary re-exports for back-compat. Workspace dep graph now: `code-rag-chat` + `code-rag-mcp` both → `code-rag-core` → `code-rag-store` + `code-rag-llm` + `code-rag-engine`. **MCP no longer transitively depends on chat.**

Deferred to M9 / post-Phase-1:
- [ ] WASM standalone build verification (`trunk build`).
- [ ] Docker compose up + curl chunk_id parity across all 4 targets.
- [ ] The `dev-split-heavy` `LlmClient: container` flip requires `GEMINI_API_KEY` to be present in the peer container's env (already inherited via `env_file: .env`).

### M6 — invoice-parse full Caravan target (3–4 sessions, DONE 2026-05-21)

**Demo.** invoice-parse with all three seams declared (LLMExtraction from B0, OCRText, OCRLayout) + the queue/blob/db.sql/cache resources migrated from bespoke adapter pattern to Caravan composition. yaml flips per-seam modes independently. **Compose-only — cloud composition flips are M4-cloud / Phase 2.**

**Prereqs.** M3; M4 (compose-only).

**Work** (per [../../invoice-parse/docs/caravan-readiness.md](../../invoice-parse/docs/caravan-readiness.md)):
- Declare `OCRText` + `OCRLayout` interfaces; wrap existing PaddleOCR adapters via `provide()`.
- Migrate existing `BlobStore` / `Queue` adapter ABCs onto Caravan `bucket` / `queue` resource bindings.
- Document FFI boundary at `libs/shared-rs` as not-an-SDK-seam.
- Forces `queue` (Redis ↔ SQS) and `db.sql` (Postgres) resource emitters to ship.

**Acceptance.** All 4 deployment surfaces still work. Per-seam mode mixes possible without source edits.

**Progress (M6 — DONE):**

Third seam (OCRLayout) on invoice-parse:
- [x] `services/processing/invoice_processing/table_extract.py` — added `@wagon class OCRLayout` interface. Refactored `PPStructureExtractor` to eager-load PPStructureV3 in `__init__` (peer's TCP port only opens after model ready) and take `(raw_ocr, file_bytes, filename)` instead of `(raw_ocr, images)` — wire-safe contract: impl regenerates images from bytes internally via `_bytes_to_images`. `SpatialClusterExtractor.extract` updated to the same signature (ignores `file_bytes`; works on raw_ocr coords). Dissolved `TableExtractor` ABC + `create_table_extractor` factory — both impls now stand alone and conceptually implement OCRLayout. — 2026-05-21
- [x] `services/processing/invoice_processing/worker.py` — `provide(OCRLayout, SpatialClusterExtractor())` (CPU-only default). Pipeline call site dispatches via `client(OCRLayout).extract(raw_ocr, pdf_bytes, filename)`. Run-pipeline signature dropped `table_extractor` param, added `filename`. — 2026-05-21
- [x] `services/processing/invoice_processing/cli.py` — `provide(OCRText, PaddleOCRTextImpl())` + `provide(OCRLayout, {SpatialCluster|PPStructure}Extractor())` (selected by `--table-method` arg, same shape as before but now via the seam). — 2026-05-21
- [x] `caravan.yaml` — `seams.OCRLayout` block (`impl: invoice_processing.table_extract:PPStructureExtractor`, `service_name: ocr-layout`). All four targets extended to include OCRLayout: `dev-bootstrap` (all 3 container), `dev-split-llm` (LLM container only), `dev-inproc` (all inproc), `dev-rabbitmq-flip` (all 3 container + queue=rabbitmq). — 2026-05-21
- [x] Inertness verified: `provide(OCRLayout, SpatialClusterExtractor())` + `client(OCRLayout).extract(...)` routes inproc with no env vars set; mock impl receives the call and returns its output. — 2026-05-21

Python adapter migration to Caravan resource bindings (`libs/shared-py/`):
- [x] `adapters/blob_store.py` — added `S3BlobStore` class (boto3-backed, `from_env()` reads `S3_ENDPOINT_URL` / `AWS_*` / `S3_BUCKET`). Talks to MinIO under compose (M4-injected env vars) and to real S3 in cloud (M4-cloud, future). Path-safety validation (UUID segments, traversal rejection) shared with `LocalFsBlobStore`. Best-effort `create_bucket` on construction (idempotent for MinIO; silent on AWS where bucket is out-of-band). — 2026-05-21
- [x] `adapters/queue.py` — added `RabbitMQQueue` class (pika-backed, sync, `from_url(amqp://...)` classmethod). Maps `MessageQueue` ABC to AMQP primitives: durable queue declare on first touch; `publish` → `basic_publish` with persistent delivery; `consume` → polling `basic_get` until count or `block_ms` elapses; `ack` → `basic_ack` on int delivery tag. `extend_visibility` is a no-op (RabbitMQ has no SQS-style visibility timeout). — 2026-05-21
- [x] `adapters/factory.py` — refactored to env-var-presence selection. `create_blob_store(config)`: `S3_ENDPOINT_URL` set → `S3BlobStore.from_env()`; else YAML config path. `create_queue(config)`: parses `QUEUE_URL`; scheme `amqp://` → `RabbitMQQueue`, `redis://` → `RedisStreamQueue`, else YAML config fallback. SQS rejected with explicit NotImplementedError (deferred to M4-cloud). — 2026-05-21
- [x] `config.py` — `DATABASE_URL` env var overrides `config.database.url` after YAML load. — 2026-05-21
- [x] `pyproject.toml` — added `boto3`, `pika`; dev-deps added `moto` for S3 testing. — 2026-05-21
- [x] `tests/python/test_blob_store.py` — `TestS3BlobStore` class (6 cases: put/get/exists/delete/path-traversal/UUID-validation + `from_env`) using `@moto.mock_aws`. — 2026-05-21
- [x] `tests/python/test_queue.py` — `TestRabbitMQQueueScheme` (4 cases: amqp/amqps accept, redis/https reject) + `TestRabbitMQQueueWireOps` (5 cases: publish-then-declare, consume-with-delivery-tag, consume-empty-on-timeout, ack-int-tag, extend-visibility-noop) using mocked pika channels. — 2026-05-21
- [x] `python -m pytest tests/python/test_blob_store.py tests/python/test_queue.py -m "not integration"`: **22 passed**, 4 RedisStreamQueue integration tests deselected (require live Redis; pre-existing, unrelated to M6). — 2026-05-21

Rust adapter migration to Caravan resource bindings (`libs/shared-rs/`):
- [x] `src/config.rs` — `DATABASE_URL` env var override of `config.database.url` after YAML load (parallels Python). — 2026-05-21
- [x] `src/adapters/blob_store.rs` — added `S3BlobStore` struct (aws-sdk-s3 client built once in `new()`; sync `BlobStore` trait methods bridge via `tokio::task::block_in_place + Handle::current().block_on(...)` — caller must be in tokio runtime, which `#[tokio::main]` provides). `from_env()` mirrors Python. `force_path_style: true` for MinIO compat. Path-safety validation matches LocalFsBlobStore + Python. — 2026-05-21
- [x] `src/adapters/queue.rs` — added `RabbitMQQueue` struct (lapin under the hood; same block_on bridge). Holds connection + channel in a `Mutex`; lazy connect on first call; durable queue declare on first touch. publish / consume / ack mapped to AMQP primitives; extend_visibility is a no-op. — 2026-05-21
- [x] `src/adapters/factory.rs` (new) — `create_blob_store(config)` + `create_queue(config)` mirror the Python factory's env-var-presence + YAML-fallback logic. URL scheme dispatch (`redis://` vs `amqp://`) for queue selection. — 2026-05-21
- [x] `src/adapters/mod.rs` — added `pub mod factory;`. — 2026-05-21
- [x] `services/ingestion/src/main.rs` + `services/output/src/main.rs` — replaced direct `LocalFsBlobStore::new` / `RedisStreamQueue::new` calls with `factory::create_blob_store(&config)` + `factory::create_queue(&config)`. Trait-object handles passed into ingest_file / run_worker as `&dyn BlobStore` / `&dyn MessageQueue`. — 2026-05-21
- [x] `Cargo.toml` — added `aws-config`, `aws-sdk-s3`, `lapin`, `url`. — 2026-05-21
- [x] `cargo build --manifest-path libs/shared-rs/Cargo.toml`: green (1m43s, ~150 deps including aws-sdk-s3 + lapin transitive). — 2026-05-21
- [x] `cargo build --manifest-path services/ingestion/Cargo.toml` + `services/output/Cargo.toml`: both green. Only pre-existing dead-code warnings in `services/output/src/delivery.rs` (unrelated to M6). — 2026-05-21

FFI boundary doc:
- [x] `libs/shared-rs/README.md` (new) — module overview + "Why shared-rs is not a Caravan seam" section explaining FFI vs RPC distinction. Future contributors should not wrap shared-rs functions with `#[wagon]`. Cross-references the env-var-presence selection contract. — 2026-05-21

End-to-end compile verification (all 4 targets):
- [x] `caravan compile --target=dev-bootstrap`: emits override with 3 peer services (`llm-extractor`, `ocr-text`, `ocr-layout`); `CARAVAN_RPC_PEERS` carries all 3 as `http` mode; depends_on covers all 3 in alphabetic order. Resource env vars (`AWS_*`, `DATABASE_URL`, `QUEUE_URL=redis://...`, `S3_ENDPOINT_URL`) injected on `processing` consumer in the resource band; CARAVAN_RPC_* in the seam band after. — 2026-05-21
- [x] `caravan compile --target=dev-split-llm`: only `llm-extractor` peer service emitted; OCRText + OCRLayout marked `{"mode":"inproc"}` in CARAVAN_RPC_PEERS. — 2026-05-21
- [x] `caravan compile --target=dev-inproc`: no peer services; all 3 seams `{"mode":"inproc"}`. — 2026-05-21
- [x] `caravan compile --target=dev-rabbitmq-flip`: 3 peer services + `rabbitmq:` resource container; `QUEUE_URL: amqp://guest:guest@rabbitmq:5672` (variant flip from redis-streams to rabbitmq via composition override). — 2026-05-21
- [x] `git diff -- services/processing/invoice_processing/ libs/shared-py/ libs/shared-rs/` between target compiles: **empty**. Source code identical across target flips — the load-bearing thesis claim ([poc_yaml_spec.md](poc_yaml_spec.md) §Testability conditions 7+8) holds at compile time.

Deferred to user local-test session (per "test local until M9 phase 1"):
- [ ] Full `docker compose --profile app up` end-to-end with real PaddleOCR / PPStructureV3 model load + MinIO + Postgres + Redis (and RabbitMQ for the flip target) + Gemini API key + sample invoice PDF/image input. Surfaces 1+2 of [pre-change-state.md](../../invoice-parse/docs/pre-change-state.md) verification.
- [ ] Surface 3 (local non-container) regression check: `python -m invoice_processing.cli invoices/sample_invoice.pdf` + `cargo run --manifest-path services/ingestion/Cargo.toml` + `cargo run --manifest-path services/output/Cargo.toml` with env vars unset (LocalFs + Redis YAML fallback path).
- [ ] Surface 4 (frontend `npm run build`) sanity — unchanged in scope, M6 didn't touch `demo/`.
- [ ] M9-phase-1 close: publish `caravan-rpc` to PyPI + `caravan-rpc-rs` to crates.io; retire the vendored wheel + path-dep workarounds. Re-run all 4 surfaces against the published SDKs.

Key M6 decisions:
- **Env-var presence selection** (not unified URL-driven adapter): preserves the no-Caravan local-dev path (LocalFs + Redis YAML fallback); adds boto3-backed S3 + pika/lapin RabbitMQ for the Caravan-compose path. Same env vars Caravan emits (M4) feed both Python and Rust adapter selection.
- **OCRLayout impl asymmetry**: worker's local `provide()` registers `SpatialClusterExtractor` (CPU-light); `caravan.yaml` `impl:` ref selects `PPStructureExtractor` for the container-mode peer where the heavy-model accuracy is worth the load cost. Both satisfy the same seam contract.
- **PPStructure / PaddleOCR cold-start**: both impls eager-load their models in `__init__`. Peer service's `caravan_rpc.serve` constructs the impl before binding the TCP port — no race with consumer dispatch.
- **Sync MessageQueue trait** preserved: lapin's async API bridged via `block_in_place + Handle::current().block_on()` inside trait methods. Works inside `#[tokio::main]` callers (ingestion + output). Same pattern as `aws-sdk-s3` calls.

### M9 — Phase 1 close: 8 testability conditions pass on both repos via compose (2 sessions)

**Demo.** CI E2E suite exercises multiple per-seam-mode combinations on both repos across compose + local-run targets; all 8 conditions in [poc_yaml_spec.md](poc_yaml_spec.md) §Testability green. Pre-change-state verification commands pass for both repos. **No AWS yet.**

**Prereqs.** M5; M6.

**Work.** E2E test harness — per-target compose-up + curl + log scrape. Golden compose files.

**This is the Phase 1 finish line.** The thesis ("yaml flips dispatch mode without source edits") is empirically proven on real code (code-rag + invoice-parse) via compose. If the project ends here, it has already justified its existence — Phase 2 is AWS coverage, not thesis proof.

---

## Phase 2 — Cloud (start only after M9 Phase 1 close is green)

Cloud milestones land after Phase 1 is fully validated. Strongly descope-able as a group: if compose-only validation is sufficient evidence for your audience (PoC review, design doc, hiring portfolio), Phase 2 is skippable. Estimate: ~13–14 sessions if all Phase-2 milestones land (M8 dropped per descope ladder).

**Re-scoped 2026-05-22** — Phase 2 split into hybrid-first / Fargate-first / Lambda-on-two-seams progression with an explicit AWS-onboarding prereq. Replaces the earlier monolithic M4-cloud + M7 + M8 + M9-cloud framing.

### M4-cloud-prereq — AWS account bootstrap (Phase 2, 1 session, no caravan code)

**Demo.** Verified-working AWS account state: `aws sts get-caller-identity --profile caravan-poc` returns the IAM user ARN; `tofu init` against the new state backend succeeds; AWS Budgets shows the spending cap.

**Prereqs.** M9 (Phase 1 close).

**Work** (AWS Console + shell, not in Caravan repo):
- IAM user with programmatic access + MFA; root locked with MFA only.
- AWS CLI install + `aws configure --profile caravan-poc`.
- Billing alarm at $20/mo; AWS Budgets hard cap at $50/mo. Critical because zero personal-AWS spend history means a config bug can run up cost (OpenSearch alone is ~$25/mo idle).
- S3 bucket + DynamoDB lock table for Terraform state backend (one-shot pre-create; chicken-and-egg-bootstrapped, doc-only).
- ECR repositories (one per image: `code-rag-chat`, `invoice-parse-processing`, `invoice-parse-processing-slim`, etc.).
- OpenTofu install.

**Output.** `docs/aws_onboarding_checklist.md` — operational checklist + state-backend bucket/table names + ECR registry URIs, committed once and treated as user-side ground truth.

**Why split out.** AWS-from-scratch is ~1 focused session; folding it into M4-cloud session 1 hides the cost and conflates ops with compiler work.

### M4-cloud — HCL emit + AWS resource provisioning (Phase 2, hybrid only, 3 sessions) — ✅ landed 2026-05-25

**Demo.** `caravan compile --target=hybrid-dev` emits HCL for the resource layer (S3 + RDS + SQS + ElastiCache); `tofu plan` is human-reviewable; `tofu apply` provisions them. Same Python/Rust code that talked to MinIO in M4 now talks to real S3 from a compose target with `creds_passthrough: true` (mounts `~/.aws`). **No cloud compute yet** — local processes, real cloud resources.

**Prereqs.** M4-cloud-prereq.

**Status — done (2026-05-25, `implement-cloud-poc` branch).** Closes the resolve.go IAM-grant TODO and lands the first cloud emitter end-to-end.

Caravan compiler additions:
- ✅ HCL emitter package `internal/compiler/emit/hcl/` (`hclwrite` + cty). Per-resource emitters for `bucket` → `aws_s3_bucket`, `db.sql` → `aws_db_instance`, `queue` → `aws_sqs_queue`, `cache` → `aws_elasticache_cluster`, `search` → `aws_opensearch_domain` (gated on actual `uses:` to dodge OpenSearch's 20+ min provisioning + ~$25/mo idle).
- ✅ Target IR extensions: kept `runtime: docker-compose` and added `creds_passthrough: bool` + `aws_profile` + `backend: { bucket, lock_table, region, key }` (replaces the dev-plan's tentative `docker-compose-cloud-hybrid` runtime — same outcome, cleaner IR; M4b reuses the cloud-managed composition without cred passthrough since Fargate task roles replace the `~/.aws` mount).
- ✅ IAM grant resolution (`resolve_iam.go`): per-entry statements derived from `uses:` (producer perms) and `triggers:` (consumer perms), unioned for entries doing both (invoice-parse `output`). Attached to the `caravan-poc` IAM user via `aws_iam_user_policy` (M4b/M7 swap this for Fargate/Lambda roles).
- ✅ Compose hybrid extensions: when `creds_passthrough: true`, mounts the developer's absolute `~/.aws` path into every app-profile service and injects `AWS_REGION` + `AWS_PROFILE`. Cloud-managed resources emit `${VAR}` passthroughs (S3_BUCKET, DATABASE_URL, QUEUE_URL, REDIS_URL, OPENSEARCH_URL) — the user runs `tofu output -json | jq -r '...' > .env.hybrid` once after `tofu apply`, then `docker compose --env-file .env.hybrid up`.
- ✅ Generated artifacts (per target): `versions.tf` (provider pin `aws ~> 5.0`, `required_version >= 1.6.0`), `backend.tf` (S3 + DynamoDB), `main.tf` (provider + SG + resources + outputs), `iam.tf`, `docker-compose.override.generated.yaml`, `.env.template`, `README.md` (3-step run instructions).
- ✅ Network reachability for VPC-only resources (RDS, ElastiCache): emits `data "http" "myip"` + a security group locked to laptop IP (dev-only; M4b introduces real VPC).

Caravan-rpc additions (resource adapters belong here, not in user repos):
- ✅ Caravan-shipped resource adapters under `caravan_rpc.resources` (Python: `rpc/python/src/caravan_rpc/resources/`; Rust: `rpc/rust/caravan-rpc/src/resources/`). Two `@wagon`-decorated seams + concrete impls:
  - `BlobStore` seam — `LocalFsBlobStore` (oss-local non-MinIO fallback), `S3BlobStore` (boto3 / aws-sdk-s3; serves MinIO with `S3_ENDPOINT_URL` set, real AWS without).
  - `MessageQueue` seam — `RedisStreamQueue`, `RabbitMQQueue`, `SqsQueue` (boto3 / aws-sdk-sqs).
- ✅ `auto_register_resources(yaml_fallback=...)` — one-line bootstrap user code calls at `main()`. Reads env vars (Caravan-injected) first, falls back to user-passed YAML config dict for non-Caravan local runs. User code never hand-writes a boto3 wrapper / SQS client / Redis client.
- ✅ Optional dep gates: `caravan-rpc[aws|redis|rabbit]` (Python extras), `caravan-rpc { features = ["resources-aws", "resources-redis", "resources-rabbit"] }` (Rust features). The seam traits load with no extras; impls demand their backend libs at construction time.

User-repo touch points (invoice-parse):
- ✅ caravan.yaml: `invoice_blobs: { type: bucket }` declared; added to all three entries' `uses:`. New `hybrid-dev` target with `creds_passthrough: true` + `region: ap-southeast-1` + `aws_profile: caravan-poc` + `backend: { ... }` pointing at the M4-cloud-prereq state bucket.
- ✅ Worker code (Python `services/processing/worker.py`, Rust `services/ingestion/main.rs` + `services/output/main.rs`): single `auto_register_resources(yaml_fallback=...)` call at startup; subsequent uses go through `client(BlobStore).put(...)` / `client(MessageQueue).publish(...)`. No adapter classes authored in invoice-parse — the structural ask remains exactly the three-point SDK contract.

End-to-end verification:
- ✅ All caravan tests: 28 Go tests (compiler / emit / hcl), 66 Python (caravan-rpc) + 2 skipped (boto3/redis not in dev venv), 54 Rust (caravan-rpc).
- ✅ `caravan compile --target=dev-bootstrap` (Phase-1 regression check): emits clean compose override, no leftover adapter references.
- ✅ `caravan compile --target=hybrid-dev`: emits HCL + compose override + env.template + README.
- ✅ Real-AWS apply/destroy loop (account `351090596944`, ap-southeast-1, 2026-05-25): `tofu init` → `tofu plan` (7 resources, human-reviewable, no surprises) → `tofu apply` (S3 + SG + SQS + 3× IAM + RDS, 4m56s for RDS) → AWS-CLI verification (S3 listable, SQS ARN returned, RDS `available` postgres 16.13, 3 user-policies attached to caravan-poc) → `tofu destroy` clean.

Deferred to later milestones:
- ⏸️ Live `docker compose up` against the provisioned AWS resources — the resource adapter wiring is unit-tested through `auto_register_resources` + `client(BlobStore)`, but the data-plane round-trip (PDF → ingest writes real S3 + SQS → processing reads real S3 + writes real RDS + publishes real SQS → output writes real S3) is left for a Phase-2 close-out session alongside M9-cloud.
- ⏸️ Cache + Search adapter impls in caravan-rpc (no user repo consumes cache or search yet; first real test lands at M9-cloud when code-rag enters Phase 2).
- ⏸️ DB-pool adapter in caravan-rpc (sqlalchemy engine / sqlx pool wrapper). Imposes ORM choice on users; design discussion pending. invoice-parse keeps direct sqlalchemy/sqlx for now.

**Risk (original).** OpenSearch domains are 20+ minutes to provision. Mitigated by gating emit on actual use; default tier `dev` (smallest instance). In practice, never triggered for invoice-parse.

### M4b — First Fargate placement (Phase 2, 3–4 sessions)

**Demo.** `caravan compile --target=staging-fargate && caravan up` runs invoice-parse's `processing` entry as a Fargate task. ECR push automated. Resources from M4-cloud still apply.

**Prereqs.** M4-cloud.

**Work.**
- VPC + subnets (2 AZs) + IGW + single NAT (flag `nat: ha` as v1.1).
- ECR repo + push automation: `caravan up` runs `docker build` + `aws ecr get-login-password | docker login` + tag + push **before** `tofu apply`.
- Fargate task def + service + internal ALB OR Cloud Map service discovery (per D11 gate — Cloud Map recommended).
- Decision gate: pin `caravan up` workflow (`tofu apply -auto-approve` wrap vs print-only — per D10).

**Acceptance.**
- invoice-parse `processing` runs on Fargate, consumes from SQS, writes to S3.
- Source code unchanged from compose target — yaml flip only.
- `aws ecs describe-tasks` shows the task in steady state.

**Risk.** ECR push timing vs Fargate task def creation (task def needs image URI). Mitigate by emit-then-apply with image push sandwiched: `caravan compile` → `caravan push-images` → `tofu apply`.

### M7 — Lambda dispatch on two seams, Rust + Python (Phase 2, 3–4 sessions)

**Demo.** Two seams flip to `lambda` mode across two languages:
- **Rust:** code-rag `IntentClassifier` carved from `code-rag-engine::intent`. Pure CPU, sub-second cold start.
- **Python:** invoice-parse `ValidateExtraction` carved from `validate_extraction()` at `services/processing/invoice_processing/validation.py:195`. Pure transforms, ~10MB slim image (no PaddleOCR/torch).

Same source, yaml flip per seam. SigV4-signed POST to Function URL with `AuthType: AWS_IAM`. Mirrors Phase 1's two-language per-seam-mode proof, now on Lambda.

**Prereqs.** M4b.

**Work — SDK side.**
- Rust SDK: `lambda` dispatch mode via `aws-sigv4` crate. Lambda runtime detection via `AWS_LAMBDA_RUNTIME_API`; register Lambda handler instead of axum (composes with existing `run_or_serve`).
- Python SDK: SigV4 via `botocore.auth.SigV4Auth`. Lambda detection via `AWS_LAMBDA_FUNCTION_NAME`; handler entry at `caravan_rpc.lambda_handler`.
- **Auth split**: `mode: lambda` uses SigV4 only (no bearer); `mode: http` keeps bearer. Emitter omits `CARAVAN_RPC_SHARED_SECRET` from Lambda env vars.

**Work — Compiler side.**
- HCL emit: `aws_lambda_function` (container-image source) + Function URL + per-caller `lambda:InvokeFunctionUrl` IAM grant.
- New compose target variant: `lambda-mixed` so consumers can run locally and dispatch into real Lambda for incremental testing.

**Work — code-rag refactor (IntentClassifier seam).**
- Add `#[wagon] pub trait IntentClassifier` in `crates/code-rag-store/src/seams.rs` (mirroring the 4 existing seams).
- Wrap existing `code_rag_engine::intent::IntentClassifier` struct in `LocalIntentClassifier` impl of the trait (non-WASM location — likely `code-rag-core::intent_local` or similar).
- Feature-gate so `code-rag-engine` WASM build (`standalone_api.rs`) keeps calling the concrete struct directly; only the server path goes through the seam.
- Flip server call sites: `src/api/handlers.rs:32-40` and `crates/code-rag-core/src/retriever.rs` (intent call sites identified in M7 prep exploration).
- Register via `caravan_rpc::provide::<dyn IntentClassifier>(...)` in `code-rag-core/src/state.rs`.

**Work — invoice-parse refactor (ValidateExtraction seam).**
- Wrap `validate_extraction()` in `@wagon class ValidateExtraction` at `services/processing/invoice_processing/validation.py:195`.
- Register `provide(ValidateExtraction, ValidateExtractionImpl())` in `worker.py` alongside existing provides.
- Flip call site in `worker.py:205` from `validate_extraction(...)` to `client(ValidateExtraction).validate(...)`.
- Add slim Docker build target in `services/processing/Dockerfile` excluding PaddleOCR/torch — ~10MB image footprint for the Lambda peer (vs 200MB+ for the OCR-bearing one).

**Acceptance.**
- `caravan compile --target=prod-mixed` emits two Lambda functions + IAM + Function URLs.
- code-rag chat query routes intent classification through Lambda; same chunk_ids as Fargate path (byte-identical).
- invoice-parse processing routes ValidateExtraction through Lambda; same ValidationResult as inproc path.
- Cold-start latency observable in CloudWatch logs <2s for both Lambda functions.
- `git diff -- crates/ src/ services/` empty between the Fargate-only and Lambda-mixed targets.

**Risk.** Per-seam slim image flavor may require a yaml schema extension (`seams.X.image_target:` for Dockerfile multi-stage selection). Mitigate via a `slim` build-target convention if the compiler shouldn't grow yaml surface.

**Decision gate before M7.** Shared-secret-vs-SigV4 strategy — SigV4-only for lambda mode; bearer stays for http mode (recommend).

### M8 — Batch placement (DESCOPED)

Dropped per descope ladder #2. AWS Batch is the heaviest emitter for the thinnest demo; code-raptor can run as a one-off `docker run` against real S3 if needed.

### M9-cloud — Phase 2 close: 8 conditions across compose × Fargate × Lambda (2 sessions)

**Demo.** Re-run M9's 8-condition testability matrix across compose × Fargate × Lambda for both repos. All 8 green across all cells. The full thesis claim — "yaml flips dispatch across packaging × placement × composition" — empirically proven on both compose and AWS.

**Prereqs.** M7.

**Work.**
- Extend M9 E2E harness to drive `caravan up` to AWS targets.
- Golden HCL files (`internal/compiler/emit/hcl/testdata/golden/<target>/*.tf`).
- AWS cost-budget guard in CI: pre-deploy check via AWS Budgets API; fail if monthly forecast exceeds threshold.
- Teardown automation: `caravan down --target=X` runs `tofu destroy` for sweep at end of CI runs. M4-cloud-prereq state backend persists; per-target resources don't.

---

## Dependency diagram

```
                  ═════════ Phase 1 — docker-compose + local-run ═════════

                       B0 (invoice-parse Python bootstrap)
                              [thesis-proving, hand-wired]
                       ─ ─ ─ ─ ─ ∥ ─ ─ ─ ─ ─
                       B0p (code-rag Rust stub)          [concurrent OK]
                                       ↓
                              M0 (compiler IR, parse+normalize)
                                       ↓
                              M1 (compose emit, runs invoice-parse)
                                       ↓
                              M2 (Rust SDK + code-rag flip)
                                       ↓
                          ┌────────────┴────────────┐
                          ↓                         ↓
                  M3 (Python compiler-emit)   M4 (compose composition swap)
                          │                         │
                          └────────────┬────────────┘
                                       ↓
                          ┌────────────┴────────────┐
                          ↓                         ↓
                  M5 (code-rag full)          M6 (invoice-parse full)
                          │                         │
                          └────────────┬────────────┘
                                       ↓
                  ═════ M9 — Phase 1 close (compose, both repos) ═════

                       [Phase 1 → Phase 2 gate: must be green first]

                  ════════════════ Phase 2 — AWS ═════════════════

                       M4-cloud-prereq (AWS account bootstrap)
                                       ↓
                       M4-cloud (HCL emit, hybrid only — local compute + real AWS resources)
                                       ↓
                              M4b (first Fargate placement)
                                       ↓
                       M7 (Lambda dispatch on TWO seams: Rust intent + Python validate)
                                       ↓
                       M9-cloud (8 conditions × 2 repos × compose+Fargate+Lambda)
```

**Phase 1 critical path:** B0 → M0 → M1 → M2 → M5 → M9 (or via M6). Phase 1 alone proves the thesis.

**Phase 2 critical path:** M9 (gate) → M4-cloud-prereq → M4-cloud → M4b → M7 → M9-cloud. (M8 dropped per descope ladder #2.)

**Parallelizable pairs:** B0 ∥ B0p (different repo + language; only milestone explicitly parallel). M3 ∥ M4. M5 ∥ M6.

---

## Recommended pacing

Approximately 30 focused sessions split across two phases.

**Phase 1 — docker-compose proof (~22 sessions):**
- Sessions 1–5: **B0 (+ B0p concurrent if capacity)**. Thesis proven by hand on real code before any compiler effort.
- Sessions 6–13: **M0 → M2**. Compiler automates proven shape; Rust SDK lands.
- Sessions 14–20: **M3 → M6**. Resource composition (compose-only), both repos fully Caravan-ified.
- Sessions 21–22: **M9**. Phase 1 closes — 8 conditions green on both repos via compose.

**Phase 2 — AWS (~13–14 sessions, optional):**
- Session 23: **M4-cloud-prereq**. AWS account bootstrap (no caravan code; doc + ops).
- Sessions 24–26: **M4-cloud (hybrid only)**. HCL emitter + resource emitters; local compute talks to real AWS.
- Sessions 27–30: **M4b**. First Fargate placement (VPC, ECR push, Cloud Map).
- Sessions 31–34: **M7**. Lambda dispatch on two seams (Rust IntentClassifier + Python ValidateExtraction); SigV4 in both SDKs.
- Sessions 35–36: **M9-cloud**. Phase 2 close — 8 conditions across compose × Fargate × Lambda for both repos.

**If the project ends at session 22 (Phase 1 done), the thesis is empirically proven** on real code via compose. Phase 2 is AWS coverage, not thesis proof. If it ends at session 5 (B0 done), the thesis is hand-proven on one real seam — already meaningful evidence.

---

## Descope ladder (drop first first)

1. **All of Phase 2 (M4-cloud-prereq, M4-cloud, M4b, M7, M9-cloud)**. Phase 1 alone proves the thesis on real code. Skip Phase 2 if compose-only validation suffices for your audience (PoC review, design doc, portfolio).
2. **M8 (Batch)** — already dropped from Phase 2 scope. Code-raptor can run as a one-off `docker run` against real S3 if needed.
3. **M6 third seam (OCRLayout)**. Keep LLMExtraction (B0) + OCRText only.
4. **M7 Python leg (ValidateExtraction)**. Rust IntentClassifier alone if Python Lambda image-flavor work runs long.
5. **M7 entirely** — Fargate only if heavy ML seams won't tolerate cold-start. Drops the Lambda dispatch demo but keeps Fargate × hybrid-resources coverage.
6. **Tier-1 manifest patching** (LLM provider feature flags) — hardcode `rig-core`'s `bedrock` feature for PoC.

**Cannot descope:** B0 (the lived spec proof). M2 (Rust SDK + thesis flip in a second language). At least one of M5/M6 (real codebase validates SDK). At least one of M3/M4 (orthogonality of language or composition). M9 Phase 1 close (otherwise nothing is verified).

---

## Decision gates (pause for user before stage)

From [open_decisions.md](open_decisions.md). Each must be ratified before its milestone lands.

| Before | Decision | Recommendation |
|---|---|---|
| B0 | Python HTTP server choice for caravan-rpc-py | stdlib `http.server` or aiohttp (simplest) |
| M0 | Compiler language | **Go** (confirmed) |
| M2 | Rust SDK HTTP server | axum |
| M2 | Shared-secret strategy (D7) | Compiler-emitted hex (option A) |
| M4 | Manifest-patch conflict policy (D9) | Error on version mismatch |
| M4-cloud-prereq | AWS region pin (state backend + ECR + resources) | `ap-southeast-1` (Singapore) — Bangkok-operator latency optimum; verify Bedrock model catalog before pin. User choice, ratified in checklist. |
| M4-cloud | Terraform state backend strategy | User-precreated S3+DynamoDB (doc-only one-shot; chicken-and-egg-bootstrapped) |
| M4-cloud | Storage permanence vocabulary | Already in IR — `composition: by-id` (pre-created) + `composition: cloud-managed` (HCL-emitted) |
| M4b | `caravan up` workflow (D10) | Wrap `tofu apply -auto-approve` after `caravan push-images` step |
| M4b | Fargate-Fargate RPC mechanism (D11) | Cloud Map (cheaper than ALB) |
| M7 | Auth split lambda vs http (D7 extension) | SigV4-only for `mode: lambda`; bearer for `mode: http`; emitter omits `CARAVAN_RPC_SHARED_SECRET` from Lambda env |
| M7 | Slim image flavor for Python Lambda | `slim` Dockerfile build-target convention (no yaml schema growth) |

---

## 8 PoC testability conditions — validation timeline

From [poc_yaml_spec.md](poc_yaml_spec.md) §Testability.

**Phase 1 (compose-only) progression:**
- Conditions 1, 7, 8 (SDK exists + dispatch observable + zero source edits): end of B0 (hand-typed Python); confirmed by M2 (Rust); fully formal by M3.
- Conditions 2, 3, 4 (reference app + scan + manifest patch): end of M0; extended by M5/M6.
- Conditions 5, 6 (compose works + same response across modes): end of B0 (compose); end of M2 (Rust).
- **All 8 on both repos across mixed per-seam-mode combinations via compose: M9 (Phase 1 close).**

**Phase 2 (AWS) extension:**
- Conditions 5, 6 re-validated against AWS resource layer at M4-cloud (hybrid: local compute → real S3/RDS/SQS).
- Conditions 5, 6 re-validated against Fargate at M4b.
- Conditions 5, 6 re-validated against Lambda (both Rust IntentClassifier and Python ValidateExtraction) at M7.
- **All 8 across compose × Fargate × Lambda per-seam-mode combinations: M9-cloud (Phase 2 close).**

---

## Verification (how to know each milestone worked)

**Phase 1:**
- **B0**: six-criteria acceptance above. Side-by-side terminals showing the four runs (local-no-env, compose-no-env, inproc-env, http-env) returning identical Excel.
- **B0p**: code-rag pre-change-state verification commands all pass on the stub branch.
- **M0–M1**: `go test ./...` + diff between Caravan-generated and hand-edited compose for invoice-parse.
- **M2**: side-by-side terminals for code-rag showing `docker logs` mode swap. `git diff` empty between target switches. WASM + MCP binary still build.
- **M3**: invoice-parse generated compose drives the B0 demo end-to-end.
- **M4**: generated compose includes MinIO sidecar; user code calling `s3.put_object` succeeds against MinIO endpoint. Same idea for `db.sql`, `cache`, `queue` containers.
- **M5 / M6**: pre-change-state verification commands pass on both repos under multiple per-seam-mode combinations (all `inproc`/`container` mixes via compose).
- **M9 (Phase 1 close)**: CI matrix green on compose for both repos. No AWS yet.

**Phase 2:**
- **M4-cloud-prereq**: `aws sts get-caller-identity --profile caravan-poc` returns the IAM user ARN; `tofu init` against the new state backend completes; AWS Budgets shows the spending cap.
- **M4-cloud**: `caravan compile --target=hybrid-dev` writes reviewable HCL; `tofu apply` provisions S3+RDS+SQS+cache; invoice-parse compose run writes a real invoice to real S3 and reads from RDS.
- **M4b**: `caravan up --target=staging-fargate` succeeds end-to-end including ECR push; `aws ecs describe-tasks` shows the task in steady state; source diff between hybrid-dev and staging-fargate runs is empty.
- **M7**: code-rag `/chat` against `prod-mixed` returns byte-identical chunk_ids to `dev-monolith`; invoice-parse PDF job processed through Fargate-→Lambda-→Fargate producing the expected Excel; CloudWatch shows IntentClassifier (Rust) and ValidateExtraction (Python) Lambda invocations.
- **M9-cloud (Phase 2 close)**: 8 conditions × 2 repos × (compose + Fargate + Lambda) matrix all green. AWS Budgets check passes; `tofu destroy` sweeps per-target resources at run end; state backend persists.

---

## Critical files

- [poc_rpc_sdk.md](poc_rpc_sdk.md) — SDK contract (B0, M2, M3, M7)
- [poc_yaml_spec.md](poc_yaml_spec.md) — yaml schema + 8 testability conditions (M0, M9)
- [poc_groups_to_code.md](poc_groups_to_code.md) — 10 resource groups (M4, M5, M6, M7)
- [ir.md](ir.md) — 5 compiler phases + env-var contract (every milestone)
- [../cmd/caravan/main.go](../cmd/caravan/main.go) — CLI stub; first real Go code lands here at M0
- [open_decisions.md](open_decisions.md) — decision gates listed above
- [../../code-rag/docs/caravan-readiness.md](../../code-rag/docs/caravan-readiness.md) — M5 design pressure inventory
- [../../code-rag/docs/pre-change-state.md](../../code-rag/docs/pre-change-state.md) — code-rag deployment baseline before SDK conversion (5 surfaces + verification commands)
- [../../invoice-parse/docs/caravan-readiness.md](../../invoice-parse/docs/caravan-readiness.md) — M6 design pressure inventory
- [../../invoice-parse/docs/pre-change-state.md](../../invoice-parse/docs/pre-change-state.md) — invoice-parse deployment baseline before SDK conversion (4 surfaces + verification commands)
