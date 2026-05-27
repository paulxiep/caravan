# Caravan

An application-definition compiler.

A caravan is a group of services. Sometimes they travel together in one process; sometimes they split apart and head to separate destinations. Which shape they take is one yaml line per target, and the source code does not change between formations.

Today, picking between a monolith and a set of microservices is a quarters-long architectural commitment: it locks in tooling, oncall structure, and deploy topology, and reversing the decision means rewriting code. Caravan makes that decision reversible. **Monolith or microservices is a yaml decision, not a code change.**

An application is a graph of components connected through the `caravan-rpc` SDK at each inter-component **seam**. Caravan projects that graph onto any point in three orthogonal dimensions with source code unchanged:

- **Packaging**: which services share a process. Per target, each seam dispatches as `inproc`, `container` (compose service / Fargate task), or `lambda`.
- **Placement**: where each service runs (local docker-compose, cloud long-running, cloud function, cloud batch).
- **Composition**: what each resource is bound to (local OSS engine, cloud managed service, existing cloud resource by ID). Mixing is first-class: local services can talk to real cloud resources in the same run.

A yaml `target:` names a point in (packaging × placement × composition). A repo declares many: `dev`, `hybrid-dev`, `staging`, `prod`, `pr-preview`. `caravan compile --target=<name>` emits auditable Terraform/HCL (cloud) or `docker-compose.override.generated.yaml` (local) into `<output_dir>/<target>/generated/` (default `caravan-out/`, yaml-overridable); `caravan up --target=<name>` applies it. Emit and apply are separate commands, so the HCL artifact is genuinely reviewable rather than buried inside a one-shot deploy.

### Direction this primitive opens up

One-line framing: think of caravan as Airflow for cloud architecture, where a declared graph plus a runtime view promote tier/cost decisions from senior-architect tribal knowledge into a queryable surface.

Once one yaml owns the deploy graph and the SDK sees every cross-component call, decisions that today live in senior-architect heads start to look more like data-lookup problems. The resource model already declares explicit tiering (`db.sql: tier: prod-small`, `bucket: class: standard`); the per-cloud mapping tables ([docs/aws_service_groups.md](docs/aws_service_groups.md), [docs/gcp_service_groups.md](docs/gcp_service_groups.md), [docs/azure_service_groups.md](docs/azure_service_groups.md)) already exist as inputs. The horizon is per-seam cost attribution, what-if simulation across targets, and a structured catalogue that replaces memorising cloud-service costs, latencies, throughputs, and limits with a query. The dev plan ([docs/development_plan.md](docs/development_plan.md)) is authoritative for what is being built; this section names the direction the primitive makes coherent, not committed roadmap.

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

The compiler writes per-target artifacts to `<output_dir>/<target>/generated/`. Yaml `output_dir:` (default `caravan-out/`) lets a repo override the write root; `output_dir: infra` in the reference repos keeps the pre-Caravan layout.

## Development Roadmap

- [Development Plan](docs/development_plan.md): live milestone tracker with descope ladders.
- [Thesis](docs/thesis.md): load-bearing scoping doc.

| Milestone | Date | Focus |
|-----------|------|-------|
| **Phase A** | 2026-05-19 | SDK name squat across PyPI / crates.io / npm / Go |
| **B0** | 2026-05-20 | Hand-wired Python SDK on invoice-parse (LLMExtraction seam) |
| **B0p** | 2026-05-20 | Rust SDK stub on code-rag (Embedder seam) |
| **M0** | 2026-05-20 | Compiler IR (parse + normalize + resolve phases) |
| **M1** | 2026-05-20 | Compose override emit (byte-identical to B0 hand-edit on invoice-parse) |
| **M2** | 2026-05-20 | Rust SDK with `#[wagon]` codegen + axum server + `run_or_serve` |
| **M3** | 2026-05-21 | Python compiler-emitted manifest patches (requirements.txt) |
| **M4** | 2026-05-21 | Compose resource catalog (Postgres, Redis, MinIO, RabbitMQ, OpenSearch) |
| **M5** | 2026-05-21 | code-rag full Caravan: 4 seams × 4 targets, per-seam mode flips independent |
| **M6** | 2026-05-21 | invoice-parse full Caravan: 3 seams × 4 targets, RabbitMQ composition flip |
| **M9 (Phase 1 close)** | 2026-05-21 | Multi-unit deployment, cross-target parity proven on both repos |
| **M4-cloud / M7** | 2026-05-26 | HCL emit + cloud-managed resources (M4-cloud); Lambda placement axis (M7) |
| **M9-cloud (Phase 2 close)** | 2026-05-27 | Real AWS apply on both repos × prod-mixed + prod-monolith; env_file passthrough; `caravan up`/`down` replaces tofu + docker-compose |

**Phase 2 closed (2026-05-27)** — see [Phase 2 close, empirical results](#phase-2-close-empirical-results-2026-05-27) below.

## Architecture

| Package | Single Responsibility |
|---------|----------------------|
| `cmd/caravan` | CLI: `check`, `spec`, `compile` subcommands |
| `internal/compiler` | Parse + normalize + resolve phases over caravan.yaml |
| `internal/compiler/emit` | Phase-5 emitters: compose override, manifest patches, resource containers |
| `rpc/python` | `caravan-rpc` Python SDK (`@wagon` / `provide` / `client` / `caravan_rpc.serve`) |
| `rpc/rust/caravan-rpc` | `caravan-rpc` Rust crate (sync + async client/server adapters, `run_or_serve`) |
| `rpc/rust/caravan-rpc-macros` | `#[wagon]` proc-macro emitting HTTP adapters |
| `rpc/typescript`, `rpc/go` | Namespace reservations (out of PoC scope) |

## Current State

- **Compiler phases 1–5 functional**: `caravan compile` writes per-target compose overrides, HCL artifacts (`main.tf`/`backend.tf`/`versions.tf`/`iam.tf`), the `caravan.tfwiring.json` sidecar manifest, manifest patches (requirements.txt / Cargo.toml), and Lambda build overrides into `<output_dir>/<target>/generated/`.
- **`caravan up` / `caravan down` replace tofu + docker-compose** for every target. Single entry point: builds + pushes ECR images, runs `tofu init/apply` for Fargate / Lambda targets; runs `docker compose -f base -f override --profile app up -d --build` for compose targets. Users never type `tofu` or `docker compose` directly. HCL on-disk stays reviewable between `caravan compile` and `caravan up` (emit/apply split).
- **Three SDK contract**: `@wagon` (declare seam), `provide` (register impl), `client` (dispatch). Same shape across Python and Rust; macros emit HTTP client + server adapters from the wagon-marked trait. SDK contract is closed — new capabilities ship as compiler-emitted artifacts, never as new user-repo files/flags.
- **Per-seam mode flips empirically independent**: code-rag's `dev-split-mixed` exercises Embedder + Reranker simultaneously as container peers while VectorReader + LlmClient stay inproc. Cloud equivalent: `prod-mixed` flips one seam to Lambda while keeping others inproc on Fargate.
- **Same image, both roles**: peer services reuse the consumer's image with `CARAVAN_RPC_ROLE=peer-<Interface>`; `run_or_serve` detours into peer-serve mode based on the env var. No synthetic peer crate, no `workspace.members` surgery.
- **Resource catalog (M4)**: Postgres, Redis, MinIO, RabbitMQ, OpenSearch containers emitted per OSS-local variant; collision detection skips duplicates when the user's hand-authored compose already publishes the same service name. M4-cloud overlays cloud-managed (real S3 / RDS / SQS / ElastiCache) onto the same keys.
- **Composition orthogonality**: invoice-parse's `dev-rabbitmq-flip` swaps queue from `redis-streams` to `rabbitmq` via yaml composition override; the same Python `MessageQueue` ABC routes on URL scheme.
- **Lambda placement axis (M7)**: any seam can flip to `mode: lambda`, getting an AWS Lambda function (container image source, AuthType=AWS_IAM Function URL, SigV4-signed dispatch from caller). Image is built from a slim Dockerfile stage opt-in via per-seam `image_target:` (no `--target=slim` convention, no synthesized Dockerfile).
- **Multi-unit deployment**: entries without seams (e.g. queue-consumer Rust services) are first-class. Peer-table emission scopes to seam-owning units only; no-seam units don't carry spurious `CARAVAN_RPC_PEERS` + `depends_on` edges that would break `docker compose --profile <X>` runs.
- **Explicit resource credentials**: yaml `resources.<name>.{user,password,dbname}` for kinds that need them (db.sql today). Same values feed `DATABASE_URL` + emitted container env so creds and DSNs stay in lockstep; user-authored postgres in a hand-compose can declare matching values.
- **Env passthrough from base compose**: `caravan compile` scans each container-mode entry's `env_file:` + `environment:` block on the base compose service of the same name. `env_file:` keys and `${VAR}` interpolations become tofu variables (resolved by `caravan up` from host env); literal `environment:` values inline into the Fargate / Lambda task-def env. Zero caravan.yaml schema delta; the user's compose is the source of truth.
- **WASM-safe SDK**: Rust `caravan-rpc` feature-gates tokio / axum / reqwest behind `default-features = ["client", "server"]` so wasm32-unknown-unknown consumers (e.g. code-rag's engine crate compiling to a static demo) build with `default-features = false`.

## Phase 1 close, empirical results (2026-05-21)

**code-rag**: same `/chat` query against `dev-monolith` (all-inproc) vs `dev-split-light` (Embedder as HTTP peer) returns **20/20 byte-identical `chunk_ids`**. Embedder peer logs `caravan peer Embedder serving on 0.0.0.0:8080`; the SDK detoured via `CARAVAN_RPC_ROLE`.

**invoice-parse**: 3-unit deployment (`ingest` + `processing` + `output`) declared in one caravan.yaml. On `dev-split-llm`, LLMExtraction dispatches via HTTP to a separate peer container, OCR seams stay inproc, same Python source. **End-to-end: 17 invoices enqueued → 8 HTTP dispatches to llm-extractor → 6 Excels delivered, 0 errors mid-run.**

Verbatim peer log: `[caravan_rpc.serve] serving LLMExtraction on http://0.0.0.0:8080` and `172.18.0.5 - "POST /_caravan/rpc/LLMExtraction/extract HTTP/1.1" 200 -`.

## Phase 2 close, empirical results (2026-05-27)

Phase 2 closes the thesis on AWS: same source code, different yaml target, same `caravan up`/`down` command.

**M4-cloud** (cloud-managed resource composition): caravan emits VPC + private subnets + ECS cluster + Cloud Map private DNS namespace + per-resource AWS blocks (S3 / RDS / SQS / ElastiCache / OpenSearch) + per-target IAM roles with resource-scoped grants. Hybrid-dev (compose runtime + creds_passthrough to real AWS) HCL emit verified clean; not gated for closure per the 2026-05-27 scope pivot.

**M7** (Lambda placement axis): per-seam `mode: lambda` flips emit `aws_lambda_function` (container-image source, slim stage from `image_target:`), `aws_lambda_function_url` (AuthType=AWS_IAM), shared execution role. Caller-side: caravan-rpc dispatches via SigV4-signed POST when the peer-table marks the seam `lambda` mode.

**M9-cloud closure — cloud applies (ap-southeast-1, account 351090596944):**

| Target | Repo | Resources | Test result |
|---|---|---|---|
| `prod-mixed` | invoice-parse | 43 created → destroyed | Fargate processing+ingest+output tasks RUNNING; ValidateExtraction Lambda Function URL deployed; PaddleOCR loaded `PP-OCRv5_mobile_det` / `en_PP-OCRv5_mobile_rec` from env_file passthrough; S3+RDS+SQS cloud-managed; clean teardown |
| `prod-monolith` | invoice-parse | 39 created → destroyed | 3 Fargate tasks, all 4 seams (LLMExtraction / OCRText / OCRLayout / ValidateExtraction) inproc; RDS+S3+SQS cloud-managed; processing task RUNNING; clean teardown |
| `prod-monolith` | code-rag | 22 created → destroyed | 1 Fargate task (`code-rag-chat`), all 5 seams (Embedder / Reranker / VectorReader / LlmClient / IntentClassifier) inproc; no cloud-managed resources; task RUNNING; clean teardown |
| `staging-fargate` | code-rag | 26 created → destroyed | M4b first Fargate-with-peer shape preserved: chat + embedder Fargate tasks both RUNNING, embedder reachable via Cloud Map (`http://embedder.code-rag.local:8080`); pre-created CloudWatch log groups (no awslogs-create-group 403); clean teardown |

**Local-compose regression** (same session, post env_file passthrough implementation):

| Target | Repo | Test result |
|---|---|---|
| `dev-bootstrap` | invoice-parse | 7 containers up; processing loaded PaddleOCR with model names from `env_file: ../.env`; postgres/redis healthy; matches B0 baseline |
| `dev-rabbitmq-flip` | invoice-parse | Queue composition flip (redis-streams → rabbitmq) still works; all 7 services + rabbitmq up |

**Env passthrough fix** (deferred test #1 closed): caravan compile now produces `caravan.tfwiring.json` sidecar listing every TF variable binding (declared secrets + env_file keys + `${VAR}` interpolations). `caravan up` resolves each from host env (loads `.env` automatically) and passes via `TF_VAR_*`. Two-phase compile-then-up design preserved; brittle HCL string-grep removed.

**Thesis empirically proven**: same source code → yaml flip from `runtime: docker-compose` to `runtime: fargate` (and `mode: container` to `mode: lambda` per seam) brings both repos up on real AWS with `caravan up`. No tofu / docker-compose commands typed by the user. See [Development Plan](docs/development_plan.md) §M9-cloud for the full landing inventory.

## Scoping documents

PoC scope (latest; supersedes module/bundle vocabulary in older docs):

- [PoC inter-process RPC SDK](docs/poc_rpc_sdk.md): wire contract, env-var contract, per-language surface (Python / Rust / TypeScript / Go).
- [PoC basic groups → 4-language code mapping](docs/poc_groups_to_code.md): 10 basic resource groups, mapped to cloud-SDK + local-OSS calls per language.
- [PoC yaml spec + worked example](docs/poc_yaml_spec.md): entries + seams + per-target dispatch shape, end-to-end testability conditions.

Canonical reference:

- [Thesis](docs/thesis.md): primary scoping doc; some text on user-restructuring is revised by [poc_rpc_sdk.md §1](docs/poc_rpc_sdk.md).
- [IR data model + pipeline](docs/ir.md): typed IR sketch, yaml schema, compiler phase signatures.
- [HCL primer + worked emit sample](docs/hcl_walkthrough.md): fully annotated `staging-fargate` + `dev-local` + `hybrid-dev` walkthrough.
- [Considerations](docs/considerations.md): ambiguity catalogue + dispositions.
- [Abstraction v4](docs/caravan_abstraction_v4.md): long-form derivation (4-language re-derivation; supersedes v3).
- Cloud catalogues: [AWS](docs/aws_service_groups.md) · [GCP](docs/gcp_service_groups.md) · [Azure](docs/azure_service_groups.md) · [Cloud providers cross-mapping](docs/cloud_providers.md).
- Per-language ecosystem evidence (mapping AWS↔language + API diffs): [Python](docs/mapping_aws_to_python.md) · [Rust](docs/mapping_aws_to_rust.md) · [TypeScript](docs/mapping_aws_to_typescript.md) · [Go](docs/mapping_aws_to_go.md).
- Historical: [Abstraction v3](docs/caravan_abstraction_v3.md) · [v2](docs/caravan_abstraction_v2.md) · [v1](docs/caravan_abstraction_v1.md).

## Test repos

Real-world design pressure for B0 / M5 / M6 / M9:

- [code-rag](https://github.com/paulxiep/code-rag): 8-crate Rust workspace, RAG over code (M5; readiness rated HIGH ~80%).
- [invoice-parse](https://github.com/paulxiep/invoice-parse): Python + Rust polyglot, OCR + LLM extraction (B0 + M6; readiness rated HIGH ~85%).

## Published packages

| Registry | Package | Version |
|----------|---------|---------|
| PyPI | [`caravan-rpc`](https://pypi.org/project/caravan-rpc/) | 0.1.1 |
| crates.io | [`caravan-rpc`](https://crates.io/crates/caravan-rpc) | 0.1.1 |
| crates.io | [`caravan-rpc-macros`](https://crates.io/crates/caravan-rpc-macros) | 0.1.1 |

0.1.0 shipped at Phase 1 close (2026-05-21). 0.1.1 ships at Phase 2 close — the version Phase 2 ran against on real AWS. Deltas: SDK self-call guard via `CARAVAN_RPC_ROLE` (peer containers reusing the consumer image no longer loop back over HTTP); pydantic `TypeAdapter` configured with `ConfigDict(ser_json_bytes="base64", val_json_bytes="base64")` so binary payloads (PDFs, images) cross the wire intact; `CARAVAN_BLOB_BACKEND` backend-marker validation (`s3` requires `S3_BUCKET`, loud-fails on missing env; `local-fs` skips MinIO even when `S3_ENDPOINT_URL` is set).

---

### Keywords

- **Language:** `Go` (compiler) · `Rust` + `Python` (SDKs) · `TypeScript`, `Go` (reserved SDK namespaces)
- **Architecture & Patterns:** `Application-Definition Compiler` · `One yaml IR` · `Three-Dimensional Dispatch (packaging × placement × composition)` · `SDK-as-Structural-Contract` · `Seam (per-language synchronous abstraction)` · `Entry (top-level deploy unit)` · `Resource (data-plane primitive)` · `Per-Target Dispatch Override` · `Unit (entry + its seam peer containers)` · `Phase-5 Emit (compose / HCL / manifest patches)` · `Collision-Detected Resource Emission` · `Variant-Per-OSS-Engine Catalog` · `Path-Overlap Scoping` · `WASM-Safe Feature Gating` · `Multi-Unit Deployment` · `Composition Orthogonality`
- **RPC & SDK:** `caravan-rpc` · `@wagon (declare seam)` · `provide (register impl)` · `client (dispatch)` · `run_or_serve (peer detour)` · `CARAVAN_RPC_PEERS (env-var dispatch table)` · `CARAVAN_RPC_ROLE` · `Proc-Macro HTTP Adapter` · `Inventory Registration` · `Sync + Async Trait Support` · `JSON Wire Protocol` · `Inproc / HTTP / Lambda Modes`
- **IaC & Deploy:** `Docker Compose Emit` · `Override-Layer Pattern` · `Terraform / HCL (deferred to M4-cloud)` · `hclwrite` · `Auditable IaC Artifacts` · `Emit/Apply Split` · `Per-Target Build Context` · `Multi-Stage Dockerfile Reuse` · `Profile-Aware Compose` · `MinIO (S3 wire-compat)` · `RabbitMQ ↔ Redis Streams Composition Flip`
- **Compiler & Tooling:** `Go + gopkg.in/yaml.v3` · `Phased Pipeline (Lex → Parse → Normalize → Resolve → Emit)` · `ResolvedPlan (per-target IR)` · `Golden-File Testing` · `Diagnostic Accumulation` · `Span Tracking` · `Manifest Patching (requirements.txt + Cargo.toml)` · `Vendored SDK Insertion`
- **Cloud (deferred to Phase 2):** `AWS` · `Fargate` · `App Runner` · `Lambda (Function URL + SigV4)` · `AWS Batch` · `RDS / Aurora` · `S3` · `OpenSearch` · `SQS` · `ElastiCache` · `Tier-1 Hard-Pair Abstraction (Bedrock ↔ Ollama / Cognito ↔ JWT / SES ↔ SMTP)`
