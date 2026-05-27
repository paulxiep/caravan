# Caravan

An application-definition compiler.

A caravan is a group of services. Sometimes they travel together in one process; sometimes they split apart and head to separate destinations. Which shape they take is one yaml line per target, and the source code does not change between formations.

Today, picking between a monolith and a set of microservices is a quarters-long architectural commitment: it locks in tooling, oncall structure, and deploy topology, and reversing the decision means rewriting code. Caravan makes that decision reversible. **Monolith or microservices is a yaml decision, not a code change.**

An application is a graph of components connected through the `caravan-rpc` SDK at each inter-component **seam**. Caravan projects that graph onto any point in three orthogonal dimensions with source code unchanged:

- **Packaging**: which services share a process. Each seam dispatches one of three ways per target: in-process (`inproc`), as its own container (a compose service or Fargate task), or as an AWS Lambda function.
- **Placement**: where each service runs (local docker-compose, cloud long-running, cloud function, cloud batch).
- **Composition**: what each resource is bound to (local OSS engine, cloud managed service, existing cloud resource by ID). Mixing is first-class: local services can talk to real cloud resources in the same run.

A yaml `target:` names a point in (packaging × placement × composition). A repo declares many: `dev`, `hybrid-dev`, `staging`, `prod`, `pr-preview`. `caravan compile --target=<name>` emits auditable Terraform/HCL (cloud) or `docker-compose.override.generated.yaml` (local) into `<output_dir>/<target>/generated/` (default `caravan-out/`, yaml-overridable); `caravan up --target=<name>` applies it. Emit and apply are separate commands, so the HCL artifact is genuinely reviewable rather than buried inside a one-shot deploy.

## Why this matters

**Microservices are easy to deploy and miserable to debug.** A request that crosses three services means three sets of logs, three debuggers, and mocks for everything you are not iterating on right now. Caravan lets you collapse those seams back into a single in-process program for local work: set a breakpoint, step through the whole request end to end, run the unit tests you had before any of this got split up. When you are done, flip one yaml line per target and the same source code redeploys as the separate services again. Phase 1 close on `code-rag` measured this: the same `/chat` query returned **20/20 byte-identical results** between `dev-monolith` (all-inproc) and `dev-split-light` (Embedder as HTTP peer), same source code on both sides.

**No more premature microservices, no more painful scaling rewrites.** Split too early and you burn runway on three deploy pipelines, three observability stacks, and three oncall rotations before you have proven the product. Stay monolithic too long and scaling one hot path means rewriting the whole thing into services. Caravan removes the bet: start as one process, split a seam the day it earns its own deployment, recombine if you were wrong. Same source code on both sides of the flip, on two real applications today.

**Local and cloud share a single command surface.** `caravan compile` writes Terraform/HCL or a docker-compose override that you can read and review before anything applies. `caravan up` and `caravan down` then take over for both compose and AWS; you do not type `tofu` or `docker compose` directly. The emit/apply split is real: the artifact on disk between the two commands is auditable. This was proven end to end on AWS `ap-southeast-1` at Phase 2 close.

**Backend swaps are orthogonal to packaging.** `invoice-parse` has a `dev-rabbitmq-flip` target that swaps its queue from Redis Streams to RabbitMQ via one yaml line; the same Python `MessageQueue` interface routes on URL scheme. You can do the same for your own resources without touching application code.

## How to adopt

The arc is the same one `code-rag` and `invoice-parse` followed. Both were existing repos before Caravan; they got converted to use the SDK in milestones B0 / B0p (hand-wired one seam) through M5 / M6 (full SDK across every seam).

1. **Find one seam to start with.** A seam is anywhere one component calls another over a boundary you would consider splitting: an embedder, an LLM client, an OCR layer, a queue producer. You do not have to find them all. One is enough.
2. **Wrap it with the three-point SDK contract.** `@wagon` declares the seam interface, `provide` registers your concrete impl, `client` is what the caller invokes. Same shape in Python and Rust. The code on either side of the seam keeps its existing structure; the SDK sits at the boundary.
3. **Write a `caravan.yaml`.** Declare your entries (runnable units), the seams between them, the resources they use, and one or more `target:` blocks naming a point in (packaging × placement × composition). `dev-monolith` keeps everything in-process locally; `prod-mixed` runs some seams as Fargate peers and others as Lambdas, against real RDS and S3.
4. **`caravan compile` and `caravan up`.** Compile writes the artifacts under `caravan-out/<target>/generated/` so you can review them. Up applies. Flip the target, run the same two commands, get the other shape.

### Minimal example

Install the SDK:

```bash
pip install caravan-rpc
# Rust: cargo add caravan-rpc caravan-rpc-macros
```

Declare the seam and wire it up. The interface is shared between caller and impl; `provide` registers the impl; `client(Interface)` is the dispatch handle.

```python
from caravan_rpc import wagon, provide, client

@wagon
class LLMExtraction:
    def extract(self, raw_ocr=None, table_extraction=None) -> dict: ...

class GeminiExtractor:
    def extract(self, raw_ocr=None, table_extraction=None):
        return {"vendor": "gemini", "total": 42.0}

provide(LLMExtraction, GeminiExtractor())

result = client(LLMExtraction).extract(raw_ocr=raw_ocr_obj)
```

Declare two targets: one with the seam in-process, one with it split out as a container peer. Same source, both targets coexist in the same yaml.

```yaml
name: my-app
default_target: dev-monolith

entries:
  processing:
    path: services/processing
    dockerfile: services/processing/Dockerfile

seams:
  LLMExtraction:
    path: services/processing
    dockerfile: services/processing/Dockerfile
    impl: my_app.extraction:GeminiExtractor

targets:
  dev-monolith:
    runtime: docker-compose
    entries: { processing: container }
    # seams omitted, LLMExtraction stays inproc

  dev-split:
    runtime: docker-compose
    entries: { processing: container }
    seams: { LLMExtraction: container }
```

Compile, review, run:

```bash
caravan check                           # parse + schema
caravan compile --target=dev-split      # writes artifacts to caravan-out/
caravan up --target=dev-split
```

Switch packaging without touching code:

```bash
caravan down --target=dev-split
caravan up --target=dev-monolith
```

The full versions are in the converted repos: see `caravan.yaml` and the SDK call sites in [code-rag](https://github.com/paulxiep/code-rag) and [invoice-parse](https://github.com/paulxiep/invoice-parse).

## Usage

```
any-parent-folder/
├── caravan/                ← this repo (Go compiler + per-language SDKs)
├── code-rag/               ← converted target (Rust, M5)
├── invoice-parse/          ← converted target (Python + Rust, M6)
└── ...
```

In a user repo with a `caravan.yaml`:

```bash
caravan check                          # phases 1-2 (parse + schema)
caravan spec --target=<name>           # phases 1-3, JSON ResolvedPlan to stdout
caravan compile --target=<name>        # phases 1-4 + write artifacts
caravan up --target=<name>             # build, emit if needed, apply
caravan down --target=<name>           # tear down
```

The compiler writes per-target artifacts to `<output_dir>/<target>/generated/`. Yaml `output_dir:` (default `caravan-out/`) lets a repo override the write root; `output_dir: infra` in the converted repos keeps the pre-Caravan layout.

## Architecture

Caravan splits into a Go compiler, per-language SDKs, and emitters that turn the IR into compose overrides or HCL.

| Package | Single Responsibility |
|---------|----------------------|
| `cmd/caravan` | CLI: `check`, `spec`, `compile`, `up`, `down` subcommands |
| `internal/compiler` | Parse + normalize + resolve phases over caravan.yaml |
| `internal/compiler/emit` | Phase-5 emitters: compose override, manifest patches, resource containers |
| `rpc/python` | `caravan-rpc` Python SDK (`@wagon` / `provide` / `client` / `caravan_rpc.serve`) |
| `rpc/rust/caravan-rpc` | `caravan-rpc` Rust crate (sync + async client/server adapters, `run_or_serve`) |
| `rpc/rust/caravan-rpc-macros` | `#[wagon]` proc-macro emitting HTTP adapters |
| `rpc/typescript`, `rpc/go` | Namespace reservations (out of PoC scope) |

## Current state

- **`caravan up` and `caravan down` are the only commands you type.** Compile writes per-target compose overrides, HCL artifacts (`main.tf`, `backend.tf`, `versions.tf`, `iam.tf`), the `caravan.tfwiring.json` sidecar manifest, and manifest patches (`requirements.txt`, `Cargo.toml`) into `<output_dir>/<target>/generated/`. Up runs `tofu init/apply` for Fargate or Lambda targets and `docker compose up` for compose targets. The emit/apply split keeps the HCL reviewable between the two commands.
- **The SDK contract is three calls and stays closed.** `@wagon` declares the seam, `provide` registers an impl, `client` dispatches. Same shape in Python and Rust. The Rust macro emits HTTP client and server adapters from the wagon-marked trait.
- **Same image, both roles.** Peer services reuse the consumer's image with `CARAVAN_RPC_ROLE=peer-<Interface>`; `run_or_serve` detours into peer-serve mode based on the env var. No synthetic peer crate, no `workspace.members` surgery.
- **Per-seam mode flips are independent.** `code-rag`'s `dev-split-mixed` runs Embedder and Reranker as container peers while VectorReader and LlmClient stay inproc. The cloud equivalent `prod-mixed` flips one seam to Lambda with the others on Fargate.
- **Resource catalog and composition orthogonality.** Postgres, Redis, MinIO, RabbitMQ, and OpenSearch ship as OSS-local containers; M4-cloud overlays cloud-managed (real S3, RDS, SQS, ElastiCache) onto the same keys. `invoice-parse`'s `dev-rabbitmq-flip` swaps queue from `redis-streams` to `rabbitmq` via yaml; the Python `MessageQueue` ABC routes on URL scheme.
- **Lambda placement axis.** Any seam can flip to `mode: lambda`, getting an AWS Lambda function (container-image source, AuthType=AWS_IAM Function URL, SigV4-signed dispatch from the caller). Image is built from a slim Dockerfile stage opt-in via per-seam `image_target:`.
- **Multi-unit deployments are first-class.** Entries without seams (e.g. queue-consumer Rust services) deploy alongside seam-owning units; peer-table emission scopes to seam-owning units only, so no-seam units do not carry spurious `CARAVAN_RPC_PEERS` or `depends_on` edges.

Additional capabilities (covered in the docs): WASM-safe SDK feature gating, env passthrough from base compose into Terraform variables, and explicit resource credentials reconciled with emitted container env so DSNs and creds stay in lockstep.

## Phase 1 close, empirical results (2026-05-21)

**`code-rag`**: same `/chat` query against `dev-monolith` (all-inproc) vs `dev-split-light` (Embedder as HTTP peer) returns **20/20 byte-identical `chunk_ids`**. Embedder peer logs `caravan peer Embedder serving on 0.0.0.0:8080`; the SDK detoured via `CARAVAN_RPC_ROLE`.

**`invoice-parse`**: 3-unit deployment (`ingest` + `processing` + `output`) declared in one caravan.yaml. On `dev-split-llm`, LLMExtraction dispatches via HTTP to a separate peer container, OCR seams stay inproc, same Python source. **End-to-end: 17 invoices enqueued → 8 HTTP dispatches to llm-extractor → 6 Excels delivered, 0 errors mid-run.**

Verbatim peer log: `[caravan_rpc.serve] serving LLMExtraction on http://0.0.0.0:8080` and `172.18.0.5 - "POST /_caravan/rpc/LLMExtraction/extract HTTP/1.1" 200 -`.

## Phase 2 close, empirical results (2026-05-27)

Phase 2 closes the thesis on AWS: same source code, different yaml target, same `caravan up` / `down` command.

**M4-cloud** (cloud-managed resource composition): caravan emits VPC + private subnets + ECS cluster + Cloud Map private DNS namespace + per-resource AWS blocks (S3 / RDS / SQS / ElastiCache / OpenSearch) + per-target IAM roles with resource-scoped grants. Hybrid-dev (compose runtime + creds_passthrough to real AWS) HCL emit verified clean; not gated for closure per the 2026-05-27 scope pivot.

**M7** (Lambda placement axis): per-seam `mode: lambda` flips emit `aws_lambda_function` (container-image source, slim stage from `image_target:`), `aws_lambda_function_url` (AuthType=AWS_IAM), shared execution role. Caller-side: caravan-rpc dispatches via SigV4-signed POST when the peer-table marks the seam `lambda` mode.

**M9-cloud closure, cloud applies (ap-southeast-1, account 351090596944):**

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

## Development roadmap

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

## Next: path to MVP

Phase 2 closure proves the thesis on the two converted repos (`code-rag` and `invoice-parse`, both pre-existing before Caravan, both brought into the SDK across B0 / B0p through M5 / M6) with Python + Rust SDKs against AWS. MVP is the next horizon: the steps that take Caravan from "thesis proven" to "usable by someone other than the author":

1. **More converted repos, more variation.** The two converted repos (`code-rag` Rust, `invoice-parse` Python + Rust) prove the SDK contract works on real applications without compiler / SDK accommodations, but both happen to be ML/AI workloads with similar seam shapes. MVP needs more conversions spanning different domains (web apps, data pipelines, event-driven workers), different scales, and different seam patterns. Each new conversion is two tests in one: whether the three-point contract (`@wagon` / `provide` / `client`) stays minimal or starts accreting affordances, and how painful the conversion itself is, which is the adoption UX signal MVP actually needs.
2. **More language support.** Python + Rust ship fully (`#[wagon]` proc-macro + axum + `run_or_serve` on Rust; `@wagon` decorator + FastAPI-equivalent + `lambda_handler` on Python). TypeScript and Go namespaces are squatted on PyPI / npm / crates.io / pkg.go.dev but the SDKs are stubs. MVP needs at least one of TypeScript or Go live, to verify the contract is not an accidental Python+Rust shape.
3. **Cloud-optimization-smart.** The resource model already declares explicit tiering (`db.sql: tier: prod-small`) and the per-cloud catalog tables ([AWS](docs/aws_service_groups.md) · [GCP](docs/gcp_service_groups.md) · [Azure](docs/azure_service_groups.md)) are inputs. MVP turns these into a queryable surface: per-seam cost attribution, latency estimates, what-if simulation across targets. Picking between `prod-mixed` and `prod-monolith` should be a `caravan plan --compare` view, not architect tribal knowledge.
4. **UX refinement.** Current CLI is functional but PoC-shape: error messages assume the developer is also the compiler-author, the README is the only onboarding path, no `caravan init`, no inline help for `caravan.yaml` schema, no `--review-only` apply gate. MVP polishes the surface a non-author would touch first.

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

## Converted repos

Two real applications brought into Caravan via the SDK. Both pre-existed the compiler; the conversion was incremental, starting with one hand-wired seam at B0 / B0p and reaching full SDK coverage at M5 / M6.

- [code-rag](https://github.com/paulxiep/code-rag): 8-crate Rust workspace, RAG over code. Brought in at M5 (4 seams × 4 targets); readiness rated HIGH ~80%.
- [invoice-parse](https://github.com/paulxiep/invoice-parse): Python + Rust polyglot, OCR + LLM extraction. Brought in across B0 + M6 (3 seams × 4 targets, RabbitMQ composition flip); readiness rated HIGH ~85%.

## Published packages

| Registry | Package | Version |
|----------|---------|---------|
| PyPI | [`caravan-rpc`](https://pypi.org/project/caravan-rpc/) | 0.1.1 |
| crates.io | [`caravan-rpc`](https://crates.io/crates/caravan-rpc) | 0.1.1 |
| crates.io | [`caravan-rpc-macros`](https://crates.io/crates/caravan-rpc-macros) | 0.1.1 |

0.1.0 shipped at Phase 1 close (2026-05-21). 0.1.1 ships at Phase 2 close: the version Phase 2 ran against on real AWS. Deltas: SDK self-call guard via `CARAVAN_RPC_ROLE` (peer containers reusing the consumer image no longer loop back over HTTP); pydantic `TypeAdapter` configured with `ConfigDict(ser_json_bytes="base64", val_json_bytes="base64")` so binary payloads (PDFs, images) cross the wire intact; `CARAVAN_BLOB_BACKEND` backend-marker validation (`s3` requires `S3_BUCKET`, loud-fails on missing env; `local-fs` skips MinIO even when `S3_ENDPOINT_URL` is set).

---

### Keywords

- **Language:** `Go` (compiler) · `Rust` + `Python` (SDKs) · `TypeScript`, `Go` (reserved SDK namespaces)
- **Architecture & Patterns:** `Application-Definition Compiler` · `One yaml IR` · `Three-Dimensional Dispatch (packaging × placement × composition)` · `SDK-as-Structural-Contract` · `Seam (per-language synchronous abstraction)` · `Entry (top-level deploy unit)` · `Resource (data-plane primitive)` · `Per-Target Dispatch Override` · `Unit (entry + its seam peer containers)` · `Phase-5 Emit (compose / HCL / manifest patches)` · `Collision-Detected Resource Emission` · `Variant-Per-OSS-Engine Catalog` · `Path-Overlap Scoping` · `WASM-Safe Feature Gating` · `Multi-Unit Deployment` · `Composition Orthogonality`
- **RPC & SDK:** `caravan-rpc` · `@wagon (declare seam)` · `provide (register impl)` · `client (dispatch)` · `run_or_serve (peer detour)` · `CARAVAN_RPC_PEERS (env-var dispatch table)` · `CARAVAN_RPC_ROLE` · `Proc-Macro HTTP Adapter` · `Inventory Registration` · `Sync + Async Trait Support` · `JSON Wire Protocol` · `Inproc / HTTP / Lambda Modes`
- **IaC & Deploy:** `Docker Compose Emit` · `Override-Layer Pattern` · `Terraform / HCL (deferred to M4-cloud)` · `hclwrite` · `Auditable IaC Artifacts` · `Emit/Apply Split` · `Per-Target Build Context` · `Multi-Stage Dockerfile Reuse` · `Profile-Aware Compose` · `MinIO (S3 wire-compat)` · `RabbitMQ ↔ Redis Streams Composition Flip`
- **Compiler & Tooling:** `Go + gopkg.in/yaml.v3` · `Phased Pipeline (Lex → Parse → Normalize → Resolve → Emit)` · `ResolvedPlan (per-target IR)` · `Golden-File Testing` · `Diagnostic Accumulation` · `Span Tracking` · `Manifest Patching (requirements.txt + Cargo.toml)` · `Vendored SDK Insertion`
- **Cloud (deferred to Phase 2):** `AWS` · `Fargate` · `App Runner` · `Lambda (Function URL + SigV4)` · `AWS Batch` · `RDS / Aurora` · `S3` · `OpenSearch` · `SQS` · `ElastiCache` · `Tier-1 Hard-Pair Abstraction (Bedrock ↔ Ollama / Cognito ↔ JWT / SES ↔ SMTP)`
