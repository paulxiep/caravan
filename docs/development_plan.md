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

### M3 — Promote Python SDK from hand-typed to compiler-emitted (2–3 sessions)

**Demo.** invoice-parse's compose-bootstrap override file is now produced by `caravan compile` rather than hand-edited. Two seams in invoice-parse (LLMExtraction from B0 + a second seam declared) flip per-seam between `inproc` and `container` via yaml.

**Prereqs.** M1; M2 (proves the per-seam env var contract end-to-end in another language first).

**Work.** Promote `caravan-rpc` Python from B0's 0.1.0 to compiler-emitted use. Compiler phase-2 language detection (`pyproject.toml` vs `Cargo.toml` in entry path). Manifest patching for `requirements.txt` (D9 — append-line semantics) — Caravan auto-adds `caravan-rpc>=0.1.0` to user manifests during compile.

**Acceptance.** invoice-parse runs end-to-end with two seams declared, mix-and-match per target. No env var → unchanged behavior (inertness preserved).

**Parallelizable with M4.**

### M4 — Composition swap, compose-only resource emitters (Phase 1, 2–3 sessions)

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

### M5 — code-rag full Caravan target (3–4 sessions)

**Demo.** code-rag declares all four seams (Embedder, Reranker, VectorReader, LlmClient) via SDK. A target sets each seam's mode independently — e.g., `Embedder: container, Reranker: container, VectorReader: inproc, LlmClient: inproc`. `curl /query` returns identical results regardless of which seams are split.

**Prereqs.** M2; M4 (compose-only `search` resource emitter — cloud variant deferred to M4-cloud).

**Work** (per [../../code-rag/docs/caravan-readiness.md](../../code-rag/docs/caravan-readiness.md)):
- Promote Embedder/Reranker to traits behind `#[wagon]` (most of this is done by B0p).
- Split `VectorStore` → `VectorReader` + `VectorWriter` + separate `call_edges` resource.
- Extract `code-rag-core` crate to break `code-rag-mcp`'s transitive dep on `code-rag-chat`.
- Caravan-generated compose references existing [../../code-rag/dockerfile/Dockerfile](../../code-rag/dockerfile/Dockerfile) — do not restructure.

**Acceptance.** code-rag's existing test suite + all 5 deployment surfaces still work (per pre-change-state verification commands). Zero source-code edits between targets.

**Design pressure into SDK.** `Mutex<Embedder>` interior mutability forces `#[wagon]` to handle `Arc<Mutex<dyn T>>` patterns. Likely SDK addition: documented `provide_shared(...)` variant.

### M6 — invoice-parse full Caravan target (3–4 sessions)

**Demo.** invoice-parse with all three seams declared (LLMExtraction from B0, OCRText, OCRLayout) + the queue/blob/db.sql/cache resources migrated from bespoke adapter pattern to Caravan composition. yaml flips per-seam modes independently. **Compose-only — cloud composition flips are M4-cloud / Phase 2.**

**Prereqs.** M3; M4 (compose-only).

**Work** (per [../../invoice-parse/docs/caravan-readiness.md](../../invoice-parse/docs/caravan-readiness.md)):
- Declare `OCRText` + `OCRLayout` interfaces; wrap existing PaddleOCR adapters via `provide()`.
- Migrate existing `BlobStore` / `Queue` adapter ABCs onto Caravan `bucket` / `queue` resource bindings.
- Document FFI boundary at `libs/shared-rs` as not-an-SDK-seam.
- Forces `queue` (Redis ↔ SQS) and `db.sql` (Postgres) resource emitters to ship.

**Acceptance.** All 4 deployment surfaces still work. Per-seam mode mixes possible without source edits.

### M9 — Phase 1 close: 8 testability conditions pass on both repos via compose (2 sessions)

**Demo.** CI E2E suite exercises multiple per-seam-mode combinations on both repos across compose + local-run targets; all 8 conditions in [poc_yaml_spec.md](poc_yaml_spec.md) §Testability green. Pre-change-state verification commands pass for both repos. **No AWS yet.**

**Prereqs.** M5; M6.

**Work.** E2E test harness — per-target compose-up + curl + log scrape. Golden compose files.

**This is the Phase 1 finish line.** The thesis ("yaml flips dispatch mode without source edits") is empirically proven on real code (code-rag + invoice-parse) via compose. If the project ends here, it has already justified its existence — Phase 2 is AWS coverage, not thesis proof.

---

## Phase 2 — Cloud (start only after M9 Phase 1 close is green)

Cloud milestones land after Phase 1 is fully validated. Strongly descope-able as a group: if compose-only validation is sufficient evidence for your audience (PoC review, design doc, hiring portfolio), Phase 2 is skippable. Estimate: ~10 sessions if all three Phase-2 milestones land.

### M4-cloud — HCL emit + AWS resource provisioning (Phase 2, 3–4 sessions)

**Demo.** Same code from M4, with `composition.uploads: cloud-managed`. `caravan compile --target=prod` emits HCL via `hclwrite`; `tofu plan` is human-reviewable; `tofu apply` provisions `aws_s3_bucket` + IAM policy. Same Rust/Python code that talked to MinIO in M4 now talks to real S3.

**Prereqs.** M9 (Phase 1 close).

**Work.** First HCL emitter via `hclwrite`. HCL-side resource emitters for the groups M5/M6 needed: `bucket` (`aws_s3_bucket`), `search` (`aws_opensearch_domain` or referenced ARN), `db.sql` (`aws_rds_instance`), `cache` (`aws_elasticache_cluster`), `queue` (`aws_sqs_queue`). IAM grants computed per `uses:` declarations.

**Risk.** Terraform state backend bootstrapping. Mitigate by separating `caravan compile` (HCL on disk; gates this milestone) from `caravan up` (tofu apply; manual on first run).

### M7 — Lambda dispatch works for one seam (Phase 2, 3–4 sessions)

**Demo.** `caravan compile --target=prod-mixed && caravan up` on either repo, where yaml sets one seam to `lambda`. The caller still uses `client(X)`; SDK detects lambda from `CARAVAN_RPC_PEERS` and SigV4-signs a POST to the Lambda Function URL.

**Prereqs.** M4-cloud.

**Work.**
- SDK: `lambda` dispatch mode in Rust client (SigV4 via `aws-sigv4` crate). Server side: detect `AWS_LAMBDA_RUNTIME_API` and register Lambda handler instead of axum.
- Phase 5: `aws_lambda_function` + Function URL (`AuthType: AWS_IAM`) + per-caller `lambda:InvokeFunctionUrl` IAM grant.

**Risk.** SigV4 signing edge cases; cold-start tax may make tests flaky. Do it by hand once (curl + aws-vault) before automating.

**Decision gates before M7.** Pin `caravan up` workflow (wrap `tofu apply -auto-approve` vs print-only). Pin Fargate-Fargate RPC mechanism (Cloud Map recommended).

**Descope.** Fargate-only if M5/M6 ran long; skip Lambda dispatch entirely.

### M8 — Batch placement works for one entry (Phase 2, 2–3 sessions, strong descope candidate)

**Demo.** `entries.code-raptor: batch` in a code-rag target; `caravan up` provisions AWS Batch; the job ingests one repo and writes embeddings to S3.

**Prereqs.** M7.

**Why descopable.** Batch is the heaviest emitter for the thinnest demo. code-raptor can run as a one-off `docker run` and still demonstrate ingestion.

### M9-cloud — Phase 2 close: same 8 conditions across AWS targets (2 sessions)

**Demo.** Re-run M9's testability matrix against AWS targets (Fargate, Lambda). All 8 conditions green across compose AND cloud per-seam-mode combinations. The full thesis claim — "yaml flips dispatch across packaging × placement × composition" — is now empirically proven on both compose and AWS.

**Prereqs.** M7; M4-cloud.

**Work.** Extend M9's E2E harness to drive `caravan up` to AWS. Golden HCL files. AWS cost-budget guard in CI.

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

                              M4-cloud (HCL emit + AWS resources)
                                       ↓
                              M7 (Lambda dispatch)
                                       ↓
                              M8 (Batch — strong descope candidate)
                                       ↓
                          M9-cloud (8 conditions across AWS)
```

**Phase 1 critical path:** B0 → M0 → M1 → M2 → M5 → M9 (or via M6). Phase 1 alone proves the thesis.

**Phase 2 critical path:** M9 (gate) → M4-cloud → M7 → M9-cloud.

**Parallelizable pairs:** B0 ∥ B0p (different repo + language; only milestone explicitly parallel). M3 ∥ M4. M5 ∥ M6.

---

## Recommended pacing

Approximately 30 focused sessions split across two phases.

**Phase 1 — docker-compose proof (~22 sessions):**
- Sessions 1–5: **B0 (+ B0p concurrent if capacity)**. Thesis proven by hand on real code before any compiler effort.
- Sessions 6–13: **M0 → M2**. Compiler automates proven shape; Rust SDK lands.
- Sessions 14–20: **M3 → M6**. Resource composition (compose-only), both repos fully Caravan-ified.
- Sessions 21–22: **M9**. Phase 1 closes — 8 conditions green on both repos via compose.

**Phase 2 — AWS (~10 sessions, optional):**
- Sessions 23–26: **M4-cloud**. HCL emitter + AWS resource emitters.
- Sessions 27–30: **M7**. Lambda dispatch on one seam.
- Sessions 31–32: **M8**. Batch placement (strong descope candidate; can skip entirely).
- Sessions 33–34: **M9-cloud**. Phase 2 close — same 8 conditions across AWS targets.

**If the project ends at session 22 (Phase 1 done), the thesis is empirically proven** on real code via compose. Phase 2 is AWS coverage, not thesis proof. If it ends at session 5 (B0 done), the thesis is hand-proven on one real seam — already meaningful evidence.

---

## Descope ladder (drop first first)

1. **All of Phase 2 (M4-cloud, M7, M8, M9-cloud)**. Phase 1 alone proves the thesis on real code. Skip Phase 2 if compose-only validation suffices for your audience (PoC review, design doc, portfolio).
2. **M8 (Batch)** if Phase 2 is happening. Heaviest emitter for thinnest demo.
3. **M6 third seam (OCRLayout)**. Keep LLMExtraction (B0) + OCRText only.
4. **Lambda mode in M7**. Fargate only if heavy ML seams won't tolerate cold-start.
5. **Tier-1 manifest patching** (LLM provider feature flags) — hardcode `rig-core`'s `bedrock` feature for PoC.

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
| M7 | `caravan up` workflow (D10) | Wrap `tofu apply -auto-approve` |
| M7 | Fargate-Fargate RPC mechanism (D11) | Cloud Map (cheaper than ALB) |

---

## 8 PoC testability conditions — validation timeline

From [poc_yaml_spec.md](poc_yaml_spec.md) §Testability.

**Phase 1 (compose-only) progression:**
- Conditions 1, 7, 8 (SDK exists + dispatch observable + zero source edits): end of B0 (hand-typed Python); confirmed by M2 (Rust); fully formal by M3.
- Conditions 2, 3, 4 (reference app + scan + manifest patch): end of M0; extended by M5/M6.
- Conditions 5, 6 (compose works + same response across modes): end of B0 (compose); end of M2 (Rust).
- **All 8 on both repos across mixed per-seam-mode combinations via compose: M9 (Phase 1 close).**

**Phase 2 (AWS) extension:**
- Conditions 5, 6 re-validated against AWS (Lambda + Fargate) targets at M9-cloud.
- **All 8 across compose × AWS per-seam-mode combinations: M9-cloud (Phase 2 close).**

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
- **M4-cloud**: `aws s3 ls` shows real bucket written to by the same code that wrote to MinIO at M4. `tofu plan` reviewable.
- **M7**: CloudWatch logs from Lambda show dispatch lines.
- **M8**: AWS Batch job ingests one repo and writes embeddings to S3 successfully.
- **M9-cloud (Phase 2 close)**: CI matrix green on compose AND AWS for both repos.

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
