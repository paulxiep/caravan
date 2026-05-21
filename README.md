# Caravan

An application-definition compiler that sits between application code and infrastructure-as-code. A caravan is your application as a graph of units that travels together and splits where deployment demands. One yaml describes entries, the `caravan-rpc` SDK seams in the code, and the bound cloud resources; `caravan compile --target=<name>` emits auditable Terraform/HCL (cloud) or `docker-compose.override.generated.yaml` (local) into `<output_dir>/<target>/generated/` (default `caravan-out/`, yaml-overridable); `caravan up --target=<name>` applies it. Emit/apply are separate so the HCL artifact is genuinely reviewable, not buried in a one-shot deploy.

An application is a graph of components connected through the `caravan-rpc` SDK at each inter-component **seam**. caravan projects that graph onto any point in three orthogonal dimensions with source code unchanged:

- **Packaging** — how source seams become deploy units (modular monolith / multi-container / multi-service). Per target, each seam dispatches as `inproc`, `container` (compose service / Fargate task), or `lambda`.
- **Placement** — where processes run (local docker-compose / cloud long-running / cloud function / cloud batch).
- **Composition** — what each resource is bound to (local OSS engine / cloud managed service / existing cloud resource by ID). Mixing is first-class — local services can talk to real cloud resources in the same run.

A yaml `target:` names a point in (packaging × placement × composition). A repo declares many — `dev`, `hybrid-dev`, `staging`, `prod`, `pr-preview` — and `caravan up --target=<name>` flips between them. Same source code everywhere.

## Usage

```
any-parent-folder/
├── caravan/                ← this repo (Go compiler + per-language SDKs)
├── code-rag/               ← reference target (Rust, M5)
├── invoice-parse/          ← reference target (Python + Rust, M6)
└── ...
```

In a user repo with a `caravan.yaml`:

```bash
caravan check                          # phases 1-2 (parse + schema)
caravan spec --target=<name>           # phases 1-3, JSON ResolvedPlan to stdout
caravan compile --target=<name>        # phases 1-4 + write artifacts
```

The compiler writes per-target artifacts to `<output_dir>/<target>/generated/`. Yaml `output_dir:` (default `caravan-out/`) lets a repo override the write root — `output_dir: infra` in the reference repos keeps the pre-Caravan layout.

## Development Roadmap

- [Development Plan](docs/development_plan.md) — live milestone tracker with descope ladders.
- [Thesis](docs/thesis.md) — load-bearing scoping doc.

| Milestone | Date | Focus |
|-----------|------|-------|
| **Phase A** | 2026-05-19 | SDK name squat across PyPI / crates.io / npm / Go |
| **B0** | 2026-05-20 | Hand-wired Python SDK on invoice-parse (LLMExtraction seam) |
| **B0p** | 2026-05-20 | Rust SDK stub on code-rag (Embedder seam) |
| **M0** | 2026-05-20 | Compiler IR — parse + normalize + resolve phases |
| **M1** | 2026-05-20 | Compose override emit (byte-identical to B0 hand-edit on invoice-parse) |
| **M2** | 2026-05-20 | Rust SDK with `#[wagon]` codegen + axum server + `run_or_serve` |
| **M3** | 2026-05-21 | Python compiler-emitted manifest patches (requirements.txt) |
| **M4** | 2026-05-21 | Compose resource catalog (Postgres, Redis, MinIO, RabbitMQ, OpenSearch) |
| **M5** | 2026-05-21 | code-rag full Caravan — 4 seams × 4 targets, per-seam mode flips independent |
| **M6** | 2026-05-21 | invoice-parse full Caravan — 3 seams × 4 targets, RabbitMQ composition flip |
| **M9 (Phase 1 close)** | 2026-05-21 | Multi-unit deployment, cross-target parity proven on both repos |

**Phase 2** (M4-cloud → M7 → M9-cloud — AWS coverage) gate opens after Phase 1 publish + rewire.

## Architecture

| Package | Single Responsibility |
|---------|----------------------|
| `cmd/caravan` | CLI — `check`, `spec`, `compile` subcommands |
| `internal/compiler` | Parse + normalize + resolve phases over caravan.yaml |
| `internal/compiler/emit` | Phase-5 emitters — compose override, manifest patches, resource containers |
| `rpc/python` | `caravan-rpc` Python SDK (`@wagon` / `provide` / `client` / `caravan_rpc.serve`) |
| `rpc/rust/caravan-rpc` | `caravan-rpc` Rust crate (sync + async client/server adapters, `run_or_serve`) |
| `rpc/rust/caravan-rpc-macros` | `#[wagon]` proc-macro emitting HTTP adapters |
| `rpc/typescript`, `rpc/go` | Namespace reservations (out of PoC scope) |

## Current State

- **Compiler phases 1–4 functional** — `caravan compile` writes per-target compose overrides + Python manifest patches into `<output_dir>/<target>/generated/`
- **Three SDK contract**: `@wagon` (declare seam), `provide` (register impl), `client` (dispatch) — same shape across Python and Rust; macros emit HTTP client + server adapters from the wagon-marked trait
- **Per-seam mode flips empirically independent** — code-rag's `dev-split-mixed` exercises Embedder + Reranker simultaneously as container peers while VectorReader + LlmClient stay inproc
- **Same image, both roles** — peer services reuse the consumer's image with `CARAVAN_RPC_ROLE=peer-<Interface>`; `run_or_serve` detours into peer-serve mode based on the env var. No synthetic peer crate, no `workspace.members` surgery
- **Resource catalog (M4)** — Postgres, Redis, MinIO, RabbitMQ, OpenSearch containers emitted per OSS-local variant; collision detection skips duplicates when the user's hand-authored compose already publishes the same service name
- **Composition orthogonality** — invoice-parse's `dev-rabbitmq-flip` swaps queue from `redis-streams` to `rabbitmq` via yaml composition override; the same Python `MessageQueue` ABC routes on URL scheme
- **Multi-unit deployment** — entries without seams (e.g. queue-consumer Rust services) are first-class. Peer-table emission scopes to seam-owning units only; no-seam units don't carry spurious `CARAVAN_RPC_PEERS` + `depends_on` edges that would break `docker compose --profile <X>` runs
- **Explicit resource credentials** — yaml `resources.<name>.{user,password,dbname}` for kinds that need them (db.sql today). Same values feed `DATABASE_URL` + emitted container env so creds and DSNs stay in lockstep; user-authored postgres in a hand-compose can declare matching values
- **WASM-safe SDK** — Rust `caravan-rpc` feature-gates tokio / axum / reqwest behind `default-features = ["client", "server"]` so wasm32-unknown-unknown consumers (e.g. code-rag's engine crate compiling to a static demo) build with `default-features = false`

## Phase 1 close — empirical results (2026-05-21)

**code-rag** — same `/chat` query against `dev-monolith` (all-inproc) vs `dev-split-light` (Embedder as HTTP peer): **20/20 byte-identical `chunk_ids`**. Embedder peer logs `caravan peer Embedder serving on 0.0.0.0:8080` — the SDK detoured via `CARAVAN_RPC_ROLE`.

**invoice-parse** — 3-unit deployment (`ingest` + `processing` + `output`) declared in one caravan.yaml. On `dev-split-llm`: LLMExtraction dispatches via HTTP to a separate peer container, OCR seams stay inproc, same Python source. **End-to-end: 17 invoices enqueued → 8 HTTP dispatches to llm-extractor → 6 Excels delivered, 0 errors mid-run**.

Verbatim peer log: `[caravan_rpc.serve] serving LLMExtraction on http://0.0.0.0:8080` and `172.18.0.5 - "POST /_caravan/rpc/LLMExtraction/extract HTTP/1.1" 200 -`.

## Scoping documents

PoC scope (latest — supersedes module/bundle vocabulary in older docs):

- [PoC inter-process RPC SDK](docs/poc_rpc_sdk.md) — wire contract, env-var contract, per-language surface (Python / Rust / TypeScript / Go).
- [PoC basic groups → 4-language code mapping](docs/poc_groups_to_code.md) — 10 basic resource groups, mapped to cloud-SDK + local-OSS calls per language.
- [PoC yaml spec + worked example](docs/poc_yaml_spec.md) — entries + seams + per-target dispatch shape, end-to-end testability conditions.

Canonical reference:

- [Thesis](docs/thesis.md) — primary scoping doc; some text on user-restructuring is revised by [poc_rpc_sdk.md §1](docs/poc_rpc_sdk.md).
- [IR data model + pipeline](docs/ir.md) — typed IR sketch, yaml schema, compiler phase signatures.
- [HCL primer + worked emit sample](docs/hcl_walkthrough.md) — fully annotated `staging-fargate` + `dev-local` + `hybrid-dev` walkthrough.
- [Considerations](docs/considerations.md) — ambiguity catalogue + dispositions.
- [Abstraction v4](docs/caravan_abstraction_v4.md) — long-form derivation (4-language re-derivation; supersedes v3).
- Cloud catalogues: [AWS](docs/aws_service_groups.md) · [GCP](docs/gcp_service_groups.md) · [Azure](docs/azure_service_groups.md) · [Cloud providers cross-mapping](docs/cloud_providers.md).
- Per-language ecosystem evidence (mapping AWS↔language + API diffs): [Python](docs/mapping_aws_to_python.md) · [Rust](docs/mapping_aws_to_rust.md) · [TypeScript](docs/mapping_aws_to_typescript.md) · [Go](docs/mapping_aws_to_go.md).
- Historical: [Abstraction v3](docs/caravan_abstraction_v3.md) · [v2](docs/caravan_abstraction_v2.md) · [v1](docs/caravan_abstraction_v1.md).

## Test repos

Real-world design pressure for B0 / M5 / M6 / M9:

- [code-rag](https://github.com/paulxiep/code-rag) — 8-crate Rust workspace, RAG over code (M5; readiness rated HIGH ~80%).
- [invoice-parse](https://github.com/paulxiep/invoice-parse) — Python + Rust polyglot, OCR + LLM extraction (B0 + M6; readiness rated HIGH ~85%).

## Published placeholders (Phase A — 2026-05-19)

| Registry | Package | Version |
|----------|---------|---------|
| PyPI | [`caravan-rpc`](https://pypi.org/project/caravan-rpc/) | 0.0.1 |
| crates.io | [`caravan-rpc`](https://crates.io/crates/caravan-rpc) | 0.0.1 |
| npm | [`caravan-rpc`](https://www.npmjs.com/package/caravan-rpc) | 0.0.1 |
| Go | [`github.com/paulxiep/caravan/rpc/go`](https://pkg.go.dev/github.com/paulxiep/caravan/rpc/go) | v0.0.1 |

Lockstep `caravan-rpc` + `caravan-rpc-macros` 0.1.0 publish to crates.io + `caravan-rpc` 0.1.0 publish to PyPI is the final operational gate; until then, the functional 0.1.0 lives in this workspace and is consumed by test repos via local path (Rust) and a vendored wheel (Python).

---

### Keywords

- **Language:** `Go` (compiler) · `Rust` + `Python` (SDKs) · `TypeScript`, `Go` (reserved SDK namespaces)
- **Architecture & Patterns:** `Application-Definition Compiler` · `One yaml IR` · `Three-Dimensional Dispatch (packaging × placement × composition)` · `SDK-as-Structural-Contract` · `Seam (per-language synchronous abstraction)` · `Entry (top-level deploy unit)` · `Resource (data-plane primitive)` · `Per-Target Dispatch Override` · `Unit (entry + its seam peer containers)` · `Phase-5 Emit (compose / HCL / manifest patches)` · `Collision-Detected Resource Emission` · `Variant-Per-OSS-Engine Catalog` · `Path-Overlap Scoping` · `WASM-Safe Feature Gating` · `Multi-Unit Deployment` · `Composition Orthogonality`
- **RPC & SDK:** `caravan-rpc` · `@wagon (declare seam)` · `provide (register impl)` · `client (dispatch)` · `run_or_serve (peer detour)` · `CARAVAN_RPC_PEERS (env-var dispatch table)` · `CARAVAN_RPC_ROLE` · `Proc-Macro HTTP Adapter` · `Inventory Registration` · `Sync + Async Trait Support` · `JSON Wire Protocol` · `Inproc / HTTP / Lambda Modes`
- **IaC & Deploy:** `Docker Compose Emit` · `Override-Layer Pattern` · `Terraform / HCL (deferred to M4-cloud)` · `hclwrite` · `Auditable IaC Artifacts` · `Emit/Apply Split` · `Per-Target Build Context` · `Multi-Stage Dockerfile Reuse` · `Profile-Aware Compose` · `MinIO (S3 wire-compat)` · `RabbitMQ ↔ Redis Streams Composition Flip`
- **Compiler & Tooling:** `Go + gopkg.in/yaml.v3` · `Phased Pipeline (Lex → Parse → Normalize → Resolve → Emit)` · `ResolvedPlan (per-target IR)` · `Golden-File Testing` · `Diagnostic Accumulation` · `Span Tracking` · `Manifest Patching (requirements.txt + Cargo.toml)` · `Vendored SDK Insertion`
- **Cloud (deferred to Phase 2):** `AWS` · `Fargate` · `App Runner` · `Lambda (Function URL + SigV4)` · `AWS Batch` · `RDS / Aurora` · `S3` · `OpenSearch` · `SQS` · `ElastiCache` · `Tier-1 Hard-Pair Abstraction (Bedrock ↔ Ollama / Cognito ↔ JWT / SES ↔ SMTP)`
