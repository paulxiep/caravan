# Caravan PoC — Development Plan

Multi-session, milestone-driven plan from today's scoping-complete + code-empty state to a functioning PoC validated on two real test repos (code-rag, invoice-parse). The plan resolves a chicken-and-egg between building Caravan and adapting test repos by **bootstrapping on a real seam in invoice-parse before any compiler code lands** (milestone B0), then automating the proven shape via M0–M9.

---

## Framing

### Caravan today

Scoping-complete, code-empty. The CLI stub at [../cmd/caravan/main.go](../cmd/caravan/main.go) prints "not implemented yet." The v0.1 implementation language is **Go**. Authoritative docs in [./](./): `thesis.md`, `poc_rpc_sdk.md`, `poc_yaml_spec.md`, `poc_groups_to_code.md`, `ir.md`, `open_decisions.md`.

The compiler has five phases per [ir.md](ir.md): Lex → Parse → Normalize → Resolve → Emit. Output: HCL + docker-compose + per-target manifest patches + `CARAVAN_RPC_PEERS` env var.

The structural contract is the `caravan-rpc` SDK at seams: `@wagon` + `provide(X, impl)` + `client(X)`.

### Three organizing principles

1. **The thesis is one specific claim** — *yaml-line changes alone* flip a seam's dispatch mode without source-code edits. Everything before that claim is demoable is plumbing; everything after extends it.
2. **The test repos are design pressure, not the destination.** code-rag and invoice-parse are coordinated co-development partners on `caravan-conversion` branches; their conversion drives SDK ergonomics before AWS shape gets locked in.
3. **Bootstrap before compile.** B0 hand-wires one real seam in invoice-parse to prove the SDK contract by example. Only then does compiler work begin (M0+), automating what's already been proven.

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

### B0 — Hand-wired bootstrap on invoice-parse LLMExtraction ⬅ THESIS-PROVING (3–5 sessions)

The chicken-and-egg breaker. No compiler. No proc-macros. We hand-author a minimal Python SDK, refactor one real seam to use it, and demonstrate the inproc/http flip on a live invoice pipeline by hand-editing `CARAVAN_RPC_PEERS`.

**Why invoice-parse and LLMExtraction:**
- Python iterates faster than Rust (runtime reflection via `inspect.signature`; no proc-macros to design).
- `LLMExtractor` in [../../invoice-parse/services/processing/invoice_processing/extraction.py](../../invoice-parse/services/processing/invoice_processing/extraction.py) is **already abstracted via ABC + factory** — closest seam to caravan-ready in either repo.
- LLM calls are stateless, network-bound, pure-ish — textbook seam.
- invoice-parse's existing per-service Dockerfile + profile-based compose already mirrors what Caravan emits.

**Work:**
1. **`caravan-rpc-py` package** (in caravan repo, hand-typed — not generated): `@wagon` decorator, `provide(X, impl)` registry, `client(X)` dispatcher that reads `CARAVAN_RPC_PEERS` env var. aiohttp or stdlib HTTP server adapter. `requests` or `httpx` client adapter. **No-config-inertness baked in**: when env var is unset, `client(X).method()` is a direct function call on the registered impl.
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

### B0p — Parallel: code-rag stub track (concurrent with B0; not blocking)

While B0 lands the Python contract, code-rag's `caravan-conversion` branch progresses against a hand-typed Rust stub.

**Work:**
- Caravan repo: add `caravan-rpc-stub` crate with hand-typed Rust trait shapes + a stub dispatcher matching the spec. Not a real proc-macro; hand-written impls only.
- code-rag `caravan-conversion` branch: introduce `caravan-rpc-stub` dependency. Declare Embedder + Reranker + VectorReader + LlmClient as `@wagon`-typed traits via hand-typed code. Swap `state.x.method()` → `client(X).method()` at all call sites.
- Validate the Rust SDK shape against code-rag's `Mutex<Embedder>` interior-mutability case **before** the proc-macro lands at M2.

**Acceptance:**
- All code-rag local-run commands work unchanged (no env var, no crash).
- Existing `docker compose up` works unchanged.
- `cargo test` passes.
- `trunk build --release --features standalone` still produces WASM artifact.
- When real `caravan-rpc` lands at M2, code-rag swaps stub for real — drop-in.

### M0 — Compiler parses yaml and writes a file (2 sessions)

**Demo.** `caravan compile --target=dev` reads `caravan.yaml`, prints normalized Plan as JSON, writes placeholder `infra/dev/generated/main.tf` + `docker-compose.generated.yaml`. `caravan spec --json` round-trips.

**Prereqs.** B0 acceptance #1–#3 passing.

**Work.** Compiler phases 1–3 in Go per [ir.md](ir.md). Structs for `Plan` / `Entry` / `Seam` / `Resource` / `Target`. `gopkg.in/yaml.v3` parser. Cross-ref resolver. Diagnostic infrastructure with source spans. The bootstrap yaml for invoice-parse from B0 becomes the worked-example fixture.

**Acceptance.** Bootstrap yaml parses. Phase-2 errors on unknown ref + duplicate provider + missing manifest. Golden-file tests. `caravan spec --json` matches the hand-authored env vars from B0.

**Risk.** Tagged-union dispatch on `type:` is fiddly in Go. Mitigation: build `TestExhaustiveSwitch_<Kind>` CI helpers from day one.

**Decision gate before M0.** Ratify Go for v0.1 compiler (recommended; already aligned with stub CLI).

### M1 — Compiler emits docker-compose, runs invoice-parse (2 sessions)

**Demo.** `caravan compile --target=dev-bootstrap` against the B0 yaml produces an `infra/dev-bootstrap/generated/docker-compose.generated.yaml` byte-equivalent (modulo formatting) to the hand-edited B0 override. `docker compose up` works identically.

**Prereqs.** M0; B0.

**Work.** Phase-5 compose emitter. Maps `entries.X.dockerfile` → compose `build:` block. Resource emitter for `db.sql` (Postgres) and `cache` (Redis) groups — emit equivalents of existing service definitions in invoice-parse compose.

**Acceptance.** Generated compose matches hand-authored ground truth. invoice-parse pipeline runs end-to-end through Caravan-generated infra.

### M2 — Rust SDK, code-rag flips one seam ⬅ CRITICAL THESIS PROOF (4–6 sessions)

**Demo.** code-rag's Axum HTTP server runs under `caravan compile --target=dev-monolith` with Embedder going through real `caravan-rpc`. yaml line changes `Embedder: container`, recompile + recompose. Same `curl /query`, same response, but `docker logs` shows the embedder responding via HTTP. `git diff -- crates/` is empty.

**Prereqs.** M1; B0p (code-rag stub track already has SDK-wrapped call sites).

**Work — three components in parallel:**
1. **`caravan-rpc` SDK**. `#[wagon]` proc-macro emits trait + server-adapter + client-adapter. `provide::<dyn T>()` registry. `client::<dyn T>()` dispatcher reads `CARAVAN_RPC_PEERS`. axum HTTP server adapter on `/_caravan/rpc/<iface>/<method>`. **No-config inertness**: identical to Python — unset env var → direct call on registered impl. Start with hand-written adapters; lift to proc-macro after runtime is stable.
2. **Compiler phase 4** (peer-table computation): per target, per deploy unit, compute `CARAVAN_RPC_PEERS` JSON — one entry per seam, each carrying that seam's mode independently. Phase 5 injects as `environment:`.
3. **code-rag swap-in**: replace `caravan-rpc-stub` dependency with real `caravan-rpc`. No source changes at call sites (stub had the same API).

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

**Work.** Formalize `caravan-rpc-py` from B0 as a versioned package. Compiler phase-2 language detection (`pyproject.toml` vs `Cargo.toml` in entry path). Manifest patching for `requirements.txt` (D9 — append-line semantics).

**Acceptance.** invoice-parse runs end-to-end with two seams declared, mix-and-match per target. No env var → unchanged behavior (inertness preserved).

**Parallelizable with M4.**

### M4 — Composition swap, bucket resource group (3–4 sessions)

**Demo.** `composition.uploads: oss-local` boots MinIO and sets `S3_ENDPOINT_URL`. Flip to `cloud-managed`, run against real S3. Same Rust code calls `s3.put_object` both times.

**Prereqs.** M2.

**Work.** First HCL emitter via `hclwrite`. Compose-side: MinIO + minio-init sidecar; inject endpoint env var. HCL-side (narrow): one resource group only — `bucket` — emitting `aws_s3_bucket` + IAM policy.

**Acceptance.** yaml-line diff is exactly one line; `tofu plan` is human-reviewable; both modes work.

**Risk.** Terraform state backend bootstrapping. Mitigate by separating `caravan compile` (HCL on disk) from `caravan up` (tofu apply) — only the former gates the milestone.

**Decision gate before M4.** Pin manifest-patch conflict policy (error on version mismatch).

**Parallelizable with M3.**

### M5 — code-rag full Caravan target (3–4 sessions)

**Demo.** code-rag declares all four seams (Embedder, Reranker, VectorReader, LlmClient) via SDK. A target sets each seam's mode independently — e.g., `Embedder: container, Reranker: container, VectorReader: inproc, LlmClient: inproc`. `curl /query` returns identical results regardless of which seams are split.

**Prereqs.** M2; M4 (needs `search` resource group too).

**Work** (per [../../code-rag/docs/caravan-readiness.md](../../code-rag/docs/caravan-readiness.md)):
- Promote Embedder/Reranker to traits behind `#[wagon]` (most of this is done by B0p).
- Split `VectorStore` → `VectorReader` + `VectorWriter` + separate `call_edges` resource.
- Extract `code-rag-core` crate to break `code-rag-mcp`'s transitive dep on `code-rag-chat`.
- Caravan-generated compose references existing [../../code-rag/dockerfile/Dockerfile](../../code-rag/dockerfile/Dockerfile) — do not restructure.

**Acceptance.** code-rag's existing test suite + all 5 deployment surfaces still work (per pre-change-state verification commands). Zero source-code edits between targets.

**Design pressure into SDK.** `Mutex<Embedder>` interior mutability forces `#[wagon]` to handle `Arc<Mutex<dyn T>>` patterns. Likely SDK addition: documented `provide_shared(...)` variant.

### M6 — invoice-parse full Caravan target (3–4 sessions)

**Demo.** invoice-parse with all three seams declared (LLMExtraction from B0, OCRText, OCRLayout) + the queue/blob/db.sql/cache resources migrated from bespoke adapter pattern to Caravan composition. yaml flips per-seam modes independently.

**Prereqs.** M3; M4.

**Work** (per [../../invoice-parse/docs/caravan-readiness.md](../../invoice-parse/docs/caravan-readiness.md)):
- Declare `OCRText` + `OCRLayout` interfaces; wrap existing PaddleOCR adapters via `provide()`.
- Migrate existing `BlobStore` / `Queue` adapter ABCs onto Caravan `bucket` / `queue` resource bindings.
- Document FFI boundary at `libs/shared-rs` as not-an-SDK-seam.
- Forces `queue` (Redis ↔ SQS) and `db.sql` (Postgres) resource emitters to ship.

**Acceptance.** All 4 deployment surfaces still work. Per-seam mode mixes possible without source edits.

### M7 — Lambda dispatch works for one seam (3–4 sessions)

**Demo.** `caravan compile --target=prod-mixed && caravan up` on either repo, where yaml sets one seam to `lambda`. The caller still uses `client(X)`; SDK detects lambda from `CARAVAN_RPC_PEERS` and SigV4-signs a POST to the Lambda Function URL.

**Prereqs.** M2; M4.

**Work.**
- SDK: `lambda` dispatch mode in Rust client (SigV4 via `aws-sigv4` crate). Server side: detect `AWS_LAMBDA_RUNTIME_API` and register Lambda handler instead of axum.
- Phase 5: `aws_lambda_function` + Function URL (`AuthType: AWS_IAM`) + per-caller `lambda:InvokeFunctionUrl` IAM grant.

**Risk.** SigV4 signing edge cases; cold-start tax may make tests flaky. Do it by hand once (curl + aws-vault) before automating.

**Decision gates before M7.** Pin `caravan up` workflow (wrap `tofu apply -auto-approve` vs print-only). Pin Fargate-Fargate RPC mechanism (Cloud Map recommended).

**Descope.** Fargate-only if M5/M6 ran long.

### M8 — Batch placement works for one entry (2–3 sessions, strong descope candidate)

**Demo.** `entries.code-raptor: batch` in a code-rag target; `caravan up` provisions AWS Batch; the job ingests one repo and writes embeddings to S3.

**Prereqs.** M5; M7.

**Why descopable.** Batch is the heaviest emitter for the thinnest demo. code-raptor can run as a one-off `docker run` and still demonstrate ingestion.

### M9 — All 8 testability conditions pass on both repos (2 sessions)

**Demo.** CI E2E suite exercises multiple per-seam-mode combinations on both repos across compose + AWS targets; all 8 conditions in [poc_yaml_spec.md](poc_yaml_spec.md) §Testability green. Pre-change-state verification commands pass for both repos.

**Prereqs.** M5; M6; M7.

---

## Dependency diagram

```
                           B0 (Bootstrap on invoice-parse)
                              [thesis-proving, hand-wired]
                                       ↓
              ┌────────────────────────┼────────────────────────┐
              ↓                        ↓                        ↓
        B0p (code-rag stub)        M0 (compiler 1-3)    [SDK lived spec lands]
              │                        ↓
              │                  M1 (compose emit)
              │                        ↓
              └────────────────────────┤
                                       ↓
                                M2 (Rust SDK + code-rag flip)
                                       ↓
                          ┌────────────┴────────────┐
                          ↓                         ↓
                  M3 (Python SDK formalize)   M4 (composition swap)
                          │                         │
                          └────────────┬────────────┘
                                       ↓
                          ┌────────────┴────────────┐
                          ↓                         ↓
                  M5 (code-rag full)          M6 (invoice-parse full)
                          │                         │
                          └────────────┬────────────┘
                                       ↓
                                M7 (Lambda dispatch)
                                       ↓
                                M8 (Batch — descopable)
                                       ↓
                                M9 (testability matrix)
```

**Critical path:** B0 → M0 → M1 → M2 → M5 → M9 (or via M6).

**Parallelizable pairs:** B0 ∥ B0p (different repos, hand-typed contracts in lockstep). M3 ∥ M4. M5 ∥ M6. M7 ∥ M8.

---

## Recommended pacing

Approximately 30 focused sessions for the full PoC.

- Sessions 1–5: **B0 + B0p**. The thesis is proven by hand on real code before any compiler effort.
- Sessions 6–13: **M0 → M2**. Compiler automates the proven shape, lands Rust SDK.
- Sessions 14–22: **M3 → M6**. Resource composition, both repos fully Caravan-ified.
- Sessions 23–32: **M7 → M9**. AWS coverage + validation matrix.

If the project ends at session 5, **the thesis is already empirically proven on real code** — strongest descope-friendly state imaginable.

---

## Descope ladder (drop first first)

1. **M8 (Batch)**. Heaviest emitter for thinnest demo.
2. **M6 third seam (OCRLayout)**. Keep LLMExtraction (B0) + OCRText only.
3. **Lambda mode in M7**. Fargate only if heavy ML seams won't tolerate cold-start.
4. **Tier-1 manifest patching** (LLM provider feature flags) — hardcode `rig-core`'s `bedrock` feature for PoC.

**Cannot descope:** B0 (the lived spec proof). M2 (Rust SDK + thesis flip in a second language). At least one of M5/M6 (real codebase validates SDK). At least one of M3/M4 (orthogonality of language or composition).

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

- Conditions 1, 7, 8 (SDK exists + dispatch observable + zero source edits): end of B0 (hand-typed Python); confirmed by M2 (Rust); fully formal by M3.
- Conditions 2, 3, 4 (reference app + scan + manifest patch): end of M0; extended by M5/M6.
- Conditions 5, 6 (compose works + same response across modes): end of B0 (compose); end of M2 (Rust); end of M7 (AWS).
- All 8 on both repos across mixed per-seam-mode combinations: M9.

---

## Verification (how to know each milestone worked)

- **B0**: six-criteria acceptance above. Side-by-side terminals showing the four runs (local-no-env, compose-no-env, inproc-env, http-env) returning identical Excel.
- **B0p**: code-rag pre-change-state verification commands all pass on the stub branch.
- **M0–M1**: `go test ./...` + diff between Caravan-generated and hand-edited compose for invoice-parse.
- **M2**: side-by-side terminals for code-rag showing `docker logs` mode swap. `git diff` empty between target switches. WASM + MCP binary still build.
- **M3**: invoice-parse generated compose drives the B0 demo end-to-end.
- **M4**: `aws s3 ls` shows real bucket written to by the same code that wrote to MinIO.
- **M5 / M6**: pre-change-state verification commands pass on both repos under multiple per-seam-mode combinations.
- **M7**: CloudWatch logs from Lambda show dispatch lines.
- **M9**: CI matrix green.

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
