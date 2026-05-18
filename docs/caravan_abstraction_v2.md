# caravan Abstraction Recommendation — v2 (first-principles)

> **Snapshot date: 2026-05-16.** Derived independently from the business requirements below, drawing evidence from `aws_service_groups.md`, `mapping_aws_to_python.md`, `mapping_python_to_aws.md`, `python_api_diffs.md`, `mapping_aws_to_rust.md`, `mapping_rust_to_aws.md`, `rust_api_diffs.md`.
>
> **Read `thesis.md` first** for the crystallized framing. This file is the long-form derivation that produced it — primitive shapes, yaml schema, IaC choice, roadmap, gotchas. The thesis is load-bearing; specifics in this file are current evaluation and may shift.
>
> This file does **not** build on `caravan_abstraction_v1.md`. Where they disagree, this file states the disagreement explicitly. v1 stays as historical record.

---

## 1. Why a v2 exists

v1 was synthesized from an IfC competitive survey (strawman) plus the AWS↔Python mappings. Its conclusions: 7+1 primitives, Python decorator SDK (`@caravan.function`, `@caravan.cron`), Pulumi in-process, AWS-only, Python-only.

This session reframes the design around the user's actual business requirements:

1. **Same code deploys each component to cloud or local containers** based on simple switches (and cloud configuration when targeting cloud).
2. **The switch is yaml**, combining three layers: GitHub Actions, IaC, docker-compose, as relevant.
3. **Assumption**: the user's application is written as SoC modules that can be extracted into containers on local.
4. **Rust ecosystem realities should inform** the conclusion — not just be data-gathered.

Two of those facts make v1's framing wrong, not merely incomplete:

- **(3) Containers-first.** If user code is already containerizable, the primary deployment artifact is a container, not a Python handler. v1 made `function` (Lambda-shaped) the top compute primitive; v2 makes `service` (container-shaped) the top compute primitive, with `function` demoted to a *shape* of `service`.
- **(4) Rust signal.** Pulumi has no first-party Rust SDK. CDK has no Rust binding. CDKtf was deprecated in 2026. v1's "Pulumi in-process" was a Python-centric leak; v2 emits Terraform / OpenTofu HCL — language-neutral, reviewable diffs.

Those two shifts cascade through everything else.

---

## 2. End-state vision

Before scoping v1, the end shape caravan is trying to reach. Every v1 / v1.1 / future decision should track toward this.

caravan, fully realized, is a **containers-first deploy tool** that lets a team write SoC-modular services in any language and deploy them to a cloud (AWS first; GCP/Azure reachable by later HCL-provider work) via one yaml manifest. No SDK, no runtime coupling, no language lock-in.

### What the user writes

- **Containers**, one per service. Inside, user code uses the language's normal AWS SDK / driver libraries with `endpoint_url` / DSN env-var-driven configuration. Lambda-shaped services wrap themselves with the language's idiomatic adapter (`lambda_http` in Rust, `Mangum` in Python, etc.) — that wrapper is user code, not caravan code.
- **One `caravan.yaml`** declaring services, resources, triggers, secrets, targets.
- **Optional**: hand-written `.tf` files alongside generated ones, for AWS features caravan hasn't wrapped. caravan never overwrites them.

### What caravan generates

| Target runtime | Generated artifacts |
|---|---|
| `docker-compose` (local) | `docker-compose.generated.yaml` with the user's service containers + OSS dependency containers (postgres, minio, elasticmq, dynamodb-local, redis, opensearch, localstack-SNS, etc.) wired together. |
| `aws` (cloud) | `infra/<target>/*.tf` covering compute (Fargate/App Runner/Lambda container-image, per `shape:`), networking (VPC/subnets/SGs auto-derived from `uses:` graph), stateful resources (RDS/Aurora, S3, SQS, SNS, DynamoDB, ElastiCache, OpenSearch, …), IAM (task roles + execution roles + policies auto-derived from `uses:`), triggers (Function URLs, ALB, SQS event source mappings, EventBridge Scheduler rules, S3 events), observability (CloudWatch log groups; X-Ray sampling configs), secrets (SSM + Secrets Manager + KMS — never plaintext). |
| CI | `.github/workflows/deploy-<target>.yml` with build / test / deploy stages + PR-preview support. Users edit by hand after generation for non-standard pipelines. |

### CLI surface (end-state)

- `caravan init` — one-time state backend bootstrap (S3 bucket + DynamoDB lock table) per AWS account
- `caravan up --target=<name>` — apply target
- `caravan down --target=<name>` — tear down
- `caravan diff --target=<name>` — preview changes (`tofu plan`, pretty-printed)
- `caravan spec [--json|--graph]` — inspect IR as text, JSON, or graphviz
- `caravan spec --check` — best-effort lint of yaml against source code (env-var usage)
- `caravan logs <service> [--target=<name>] [--follow]` — stream logs (docker logs locally; CloudWatch in cloud) through one CLI
- `caravan exec <service> [--target=<name>] -- <cmd>` — run a command inside the running container (`docker exec` locally; `aws ecs execute-command` in cloud)
- `caravan preview --pr=<n>` — spin per-PR target stack from a target template
- `caravan generate workflow` — refresh CI files from current yaml

### End-state primitive coverage

- **All 8 primitives** generally available: `service`, `bucket`, `queue`, `topic`, `kv`, `db.sql`, `secret`, `static_site`.
- **All 3 `service` shapes** supported: `long-running` (Fargate / App Runner), `function` (Lambda container-image), `batch` (one-off task on Fargate / AWS Batch).
- **All 5 trigger types** supported: `http`, `queue`, `topic`, `cron`, `bucket-event`.
- **~20 cloud-only resource types** declared via `cloud_only: type: <name>` syntax, each with documented "this is the AWS SDK call you make in your code" guidance. Examples: `bedrock.llm`, `cloudfront`, `cognito.user-pool`, `stepfunctions.workflow`, `sagemaker.endpoint`.

### End-state language coverage

- **First-class** (have reference apps + per-language docs): Python, Rust, Go, TypeScript/Node.
- **Container baseline** (work because they're containers, no special support needed): Java, Ruby, .NET, anything with a Dockerfile.
- Per-language guidance docs (`mapping_aws_to_<lang>.md`, `<lang>_api_diffs.md`) explain the `endpoint_url` / DSN / Lambda-adapter idiom and recommend the mature community library per Tier 1 hard pair (rig-core / litellm / langchaingo / Vercel AI SDK for LLMs; jwt libs for token verify; etc.). The caravan deploy CLI itself ships zero language-specific code; `caravan-adapters-*` packages may exist for proven gaps but are optional and standalone.

### End-state cloud coverage

- **AWS**: full coverage of declared primitives + the cloud-only registry.
- **GCP**: same primitive names map to Cloud Run / GCS / Pub/Sub (queue+topic) / Cloud SQL / Firestore / Secret Manager. Reachable by adding GCP-provider HCL templates after AWS coverage stabilizes.
- **Azure**: same primitives map to Container Apps / Blob / Service Bus / Postgres Flexible Server / Cosmos / Key Vault.
- The IR primitives are **cloud-agnostic by name** (`bucket`, not `s3`) — schema doesn't break when GCP/Azure are added.

### End-state observability (no extra wiring)

- Services emit logs to stdout in JSON; caravan wires runtime collection (awslogs driver / Lambda automatic / docker logs).
- OTel traces on by default; `OTEL_EXPORTER_OTLP_ENDPOINT` env var points at ADOT (cloud) or Jaeger (local).
- Metrics via CloudWatch EMF in logs for basic counter/timer cases; Prometheus sidecar opt-in for advanced.

### End-state extension model (escape hatches)

- Hand-written `.tf` files in `infra/<target>/` are preserved and merged with generated ones; caravan never deletes user HCL.
- `resources: <name>: { type: terraform-module, source: "./modules/foo" }` wraps arbitrary HCL modules into the caravan IR for `uses:` and env-var injection.
- This matters because caravan can't (and shouldn't try to) wrap every AWS feature. The escape hatch keeps caravan useful even when its built-in primitives don't cover something.

### Currently out of scope (revisitable as demand justifies)

These are scope calls reading the current landscape, not permanent commitments. The thesis (modules × packaging × placement × composition) constrains what's load-bearing; everything below is tradeoff.

- **Serverless-framework UX bias.** Lambda is one *shape* of `service`, not the gravity center. If the function shape becomes the dominant user pattern, the UX can lean into it more.
- **Deploy-time SDK.** The current design has yaml as the source of truth — no `import caravan` driving deploys. If specific ergonomics gaps emerge that yaml + env-vars can't close, this is revisitable.
- **Per-language adapter libraries.** Community libraries (rig-core, litellm, etc.) cover most Tier 1 hard pairs today. `caravan-adapters-*` may ship for proven gaps; not currently a priority.
- **Live debugger / hot-reload proxy.** Containers + IDE debugger + volume-mount-for-source already work; no current plan to reinvent.
- **Multi-account governance layer** (Control Tower, AFT, AWS Organizations). Different product.
- **Kubernetes target.** Managed runtimes (Fargate / App Runner / Lambda) are the current default lane; EKS is reachable but unprioritized.
- **Console UI / hosted SaaS.** Out of scope today. Could become a separate product later.

---

## 3. First-principle derivation of primitives

Starting fresh: **what must the IR express, given (a) the user has containerizable SoC modules and (b) the cloud/local switch is yaml?**

The IR must name:
- The user's own runnable units (each becomes a container on local, an ECS/Fargate/App Runner/Lambda task on AWS).
- The stateful dependencies those units talk to (databases, queues, buckets) — these have an OSS engine locally and a managed AWS service in cloud.
- The triggers that wake those units (HTTP requests, queue messages, schedules).
- The secrets/config the units consume.
- Edge concerns the user may need (static asset hosting).
- A way to flag resources that exist only in cloud (Bedrock, CloudFront, etc.).

That yields **eight primitives**, derived purely from the requirement, not from carried-over assumptions:

| Primitive | What it is | Cloud backing | Local backing | vs v1 |
|---|---|---|---|---|
| `service` | A runnable container with optional HTTP / queue trigger | App Runner / ECS Fargate / Lambda-container | docker-compose service | **New top primitive.** v1 had `function`; v2 collapses `function` into a *shape* of `service` (`shape: function` ⇒ Lambda-shaped deployment when handler-ABI-compatible). |
| `bucket` | Object store | S3 | MinIO | Unchanged from v1. |
| `queue` | Durable point-to-point queue | SQS | ElasticMQ | Unchanged from v1. |
| `topic` | Pub/sub fan-out | SNS | LocalStack-SNS | Unchanged from v1. |
| `kv` | Key-value store | DynamoDB | dynamodb-local | Unchanged from v1. |
| `db.sql` | Relational DB | RDS / Aurora Postgres or MySQL | postgres:16 / mysql:8 | Unchanged from v1. |
| `secret` | Secret / config value | SSM + Secrets Manager | env vars (with optional dev `.env` file) | Unchanged from v1. |
| `static_site` | SPA / static asset hosting | S3 + CloudFront | nginx container | **New.** Surfaces naturally from container-first thinking; v1 buried CloudFront in `cloud_only`. |

**Demoted from primitive to trigger attribute**: `cron`. A scheduled invocation is a property of a `service`, not a thing of its own. `cron` lives under `triggers:` and emits EventBridge Scheduler rules in cloud, optionally a `tokio-cron-scheduler`/APScheduler sidecar locally.

**Auto-derived, not user-facing**: `network` (VPC, subnets, security groups). v2 derives these from the `uses:` graph; the user never writes them.

---

## 4. SDK strategy — two separate questions, often confused

The largest departure from v1, and the most important framing to get right. There are two distinct SDK questions, and they answer to different rules:

| SDK kind | Example | Status |
|---|---|---|
| **Deploy-time SDK** (decorators driving deploys) | v1's `@caravan.function` / `@caravan.cron` reading Python imports to emit Pulumi resources | **Genuinely optional. Current call: not shipped.** yaml is the source of truth; this avoids Python-runtime coupling and per-language deploy tooling. Revisitable if a specific ergonomics gap shows up that yaml + env-vars can't close. |
| **Runtime adapter library** (abstraction for hard pairs) | `LlmClient` trait with both `BedrockLlm` and `OllamaLlm` impls so user code stays the same | **Structurally required wherever cloud and local wire APIs differ.** Not a tradeoff: if Bedrock's `invoke_model` and Ollama's `generate` are different functions, user code cannot be identical without an abstraction layer. The only scope call is *who writes that layer* — community, caravan, or the user. |

Conflating these two led to the original v2 framing's "no SDK ever" overstatement. The runtime adapter SDK is not optional for Tier 1 pairs; only its authorship is.

### Three tiers, with this distinction baked in

**Tier 0 — Same wire API both sides (~18 Rust / ~22 Python services)**: endpoint-URL or DSN env-var swap. No abstraction library required. User reads `S3_ENDPOINT_URL`, `DATABASE_URL`, etc., uses the language's native AWS SDK / driver, code is unchanged across deployments.

```python
S3_ENDPOINT_URL = os.environ.get("S3_ENDPOINT_URL")  # None → real S3; http://minio:9000 locally
```

```rust
let s3_endpoint = std::env::var("S3_ENDPOINT_URL").ok();
```

**Tier 1 — Different wire APIs (~3–5 hard pairs)**: abstraction library required. The library defines a trait/Protocol and ships both cloud and local impls. User code talks to the trait; env var selects impl at startup.

The current authorship call: **rely on mature community libraries where they exist; caravan ships `caravan-adapters-*` only for proven gaps.**

| Pair | Rust | Python | Go | TypeScript |
|---|---|---|---|---|
| LLM (Bedrock + Ollama + others) | `rig` / `rig-core` | `litellm` | `langchaingo` / `eino` | Vercel AI SDK |
| Token verification (Cognito JWKS + local JWT) | `jsonwebtoken` + JWKS cache | `authlib` / `python-jose` | `golang-jwt` | `jose` |
| Email (SES API + SMTP catcher) | `lettre` / `aws-sdk-sesv2` | `smtplib` / `boto3` | `gomail` / `aws-sdk-go-v2` | `nodemailer` / `@aws-sdk/client-ses` |
| Speech-to-text (Transcribe + Whisper) | `whisper-rs` + `aws-sdk-transcribe` | `openai-whisper` + `boto3` | similar | similar |
| Image analysis (Rekognition + OSS vision) | OpenCV + `aws-sdk-rekognition` | `opencv-python` / ultralytics + `boto3` | similar | similar |

For LLMs specifically, the canonical Tier 1 case, community libraries already provide the abstraction:

```rust
use rig::{providers::{bedrock, ollama}, completion::Prompt};
let model = match std::env::var("LLM_BACKEND").as_deref() {
    Ok("bedrock") => bedrock::Client::from_env().completion_model("anthropic.claude-opus-4-7"),
    _             => ollama::Client::from_env().completion_model("llama3.1"),
};
let reply = model.prompt("hello").send().await?;
```

```python
import litellm, os
reply = litellm.completion(
    model=os.environ.get("LLM_MODEL", "ollama/llama3.1"),  # "bedrock/anthropic.claude-opus-4-7-..." in cloud
    messages=[{"role": "user", "content": "hello"}],
)
```

If a v1.1+ landscape survey finds a Tier 1 pair where no community library covers cloud + local under one abstraction, caravan ships `caravan-adapters-<lang>` for that pair. The default isn't "caravan writes everything" or "users write everything" — it's "use the best existing answer per pair."

**Tier 2 — No-local-stand-in services (~15–20)**: "cloud-only" is short for *no OSS engine reproduces this service locally*. The `cloud_only:` IR flag is a **provisioning marker** — it tells caravan not to generate a local docker-compose stand-in. It doesn't mean code crashes on a local target; what happens depends on which of four patterns the user picks per service:

| Pattern | Typical services | Local-target behavior |
|---|---|---|
| Skip in local (feature-flag off) | CloudFront, CloudWatch RUM / Synthetics, SNS Mobile Push, Step Functions Distributed Map, IoT Defender | Code paths gated behind a config check; no-op or short-circuit on local. Most common. |
| Hit real AWS from local (mixed mode, requires mounted creds) | Bedrock Knowledge Bases / Agents when iterating against real models | Local container has AWS creds; SDK calls reach real cloud. This is the mix-composition dimension from the thesis. Costs real $$. |
| Swap to a different engine (accept divergence) | DAX → vanilla DDB-local; S3 Express → MinIO; Aurora DSQL → vanilla Postgres; Neptune Analytics → Neo4j community | Same client crate / library, different engine. Loses the AWS-specific characteristic; tests still run. Documented per pair in `api_diffs` files as "partial" or "Intractable wire". |
| Stub via a small adapter | When response shape matters but the service is unavailable locally | User wraps the cloud call behind a trait/Protocol and returns canned data on local. Same structural answer as Tier 1, but no community library exists because the service has no local impl to wrap. |

caravan's job for Tier 2 is: (a) provision the cloud resource via Terraform, (b) inject AWS creds into local containers when the user opts into mixed mode, (c) document which pattern fits which service in the `api_diffs` files. *Which pattern to pick* is user judgment per service.

Examples grouped by typical pattern:

- **Skip-shaped**: CloudFront, Lambda@Edge, CloudFront Functions, Global Accelerator, CloudWatch Synthetics / RUM / Application Signals, SNS Mobile Push, IoT Device Management / Defender / Analytics / SiteWise / TwinMaker / FleetWise, Step Functions Distributed Map.
- **Hit-real-shaped**: Bedrock Knowledge Bases / Agents / Guardrails, SageMaker JumpStart / Canvas (the agentic/orchestration ones — local creds + connectivity work fine).
- **Engine-swap-shaped**: S3 Express One Zone, S3 Vectors, S3 Object Lambda, S3 Select, Aurora DSQL, DAX, Neptune Analytics, Kendra. These have a "partial" local cousin in the mapping docs.
- **Stub-shaped**: rarely needed; usually one of the above three suffices.

(IAM enforcement sits at a different level — it's a runtime guarantee that LocalStack can't reproduce. Treat as "real IAM is a production-only check.")

### Trade-offs and mitigations

**Trade-off accepted**: lose typed resource accessors of the SST `link:` flavor and compile-time IAM-policy inference. The mapping docs (`python_api_diffs.md`, `rust_api_diffs.md`) become *user-facing recipes* — including which community library to import for each Tier 1 pair.

**Mitigation for "env var typo at runtime" (Tier 0)**: `caravan spec --check` greps source files for env vars declared in `uses:`. Not a type system, but a useful safety net.

**Mitigation for Tier 1 wiring errors**: community libraries' `from_env()`-style constructors read canonical env-var names — caravan documents which names to use so its injected vars match what each language's library expects. This eliminates most of the typo class without caravan owning the trait surface.

**On caravan-authored libraries**: today the per-pair landscape points toward community-library sufficiency for Tier 1; caravan ships zero code libraries and curates guidance instead. If a v1.1+ survey finds pairs where no community option covers cloud + local under one abstraction, `caravan-adapters-<lang>` ships for those specific pairs. The principle: prove the gap before writing the library.

---

## 5. IaC strategy: emit Terraform / OpenTofu HCL

v1's "Pulumi in-process" assumed a Python-resident IR walker calling Pulumi's automation API. With v2's language-neutral IR, that approach is awkward — and the Rust evidence makes it strictly wrong.

**Options considered**:
- (a) Pulumi-Python regardless of user language. Couples the caravan runtime to Python even for Rust/Go/TS users. Awkward; deployable but Python becomes a hidden runtime dependency.
- (b) Pulumi-Go. Pulumi *does* have a Go SDK; caravan CLI written in Go, uses Pulumi automation API. Reasonable; caravan ships as a single Go binary. Still couples to Pulumi.
- (c) **Emit Terraform / OpenTofu HCL.** Language-neutral. Reviewable diffs in CI. OpenTofu's Apache-2.0 license matches the licensing posture caravan already targets. HCL is the dominant artifact security/compliance teams audit. State management is well-understood. No per-language SDK coupling for either caravan or the user.

**(c) wins.** v1's choice was a Python-centric leak; v2 corrects it.

**State backend**: opinionated — S3 bucket + DynamoDB lock table, created by a one-time `caravan init`. Users who need different state backends can edit the generated `backend.tf` (acceptable v1 friction).

**Apply mechanism**: `caravan up --target=aws-staging` wraps `tofu init && tofu apply -auto-approve` against the target's emitted directory. `caravan diff` runs `tofu plan` and pretty-prints.

---

## 6. Yaml shape

One `caravan.yaml` (the IR). Three projections, generated on demand:

- `docker-compose.generated.yaml` (when target's runtime = `docker-compose`)
- `infra/<target>/*.tf` (when target's runtime = `aws`)
- `.github/workflows/deploy-<target>.yml` (CI bootstrap; user can edit by hand after)

Schema (illustrative, not exhaustive):

```yaml
name: my-app
default_target: local

services:
  api:
    build: ./services/api          # or image: my-registry/api:tag
    shape: long-running            # long-running | function | batch
    expose: { port: 8080, public: true }
    uses: [uploads, jobs, app_db, sessions, stripe_key]
  worker:
    build: ./services/worker
    shape: long-running
    trigger: { queue: jobs }
    uses: [app_db, uploads, jobs]

resources:
  uploads:
    type: bucket
    class: standard            # standard | intelligent | glacier-instant | one-zone-ia | glacier-deep-archive
    lifecycle: keep-90d        # optional; transition / expiration rules
  archives:
    type: bucket
    class: glacier-deep-archive
  jobs:
    type: queue
    kind: standard             # standard | fifo
  app_db:
    type: db.sql
    engine: postgres
    version: "16"
    tier: prod-small           # dev | prod-small | prod | premium | global
  sessions:
    type: kv
    primary_key: [pk, sk]
    capacity_mode: on-demand   # on-demand | provisioned (with rcu/wcu)

triggers:
  nightly_cleanup:
    schedule: "0 2 * * *"
    target: worker

secrets:
  stripe_key: {}

cloud_only:
  llm: { type: bedrock.llm, model: "anthropic.claude-opus-4-7-20260416-v1:0" }

targets:
  local:   { runtime: docker-compose }
  staging:
    runtime: aws
    region: us-east-1
    account_id: "111122223333"
    overrides:
      app_db: { tier: dev }              # save $$ in staging
    ci:
      on: { push: { branches: [main] } }
  prod:
    runtime: aws
    region: us-east-1
    account_id: "999988887777"
    overrides:
      app_db: { tier: premium }
      uploads: { lifecycle: versioning+archival }
    ci:
      on: { workflow_dispatch: {} }
```

**Switching**: `caravan up --target=local` (or `--target=staging`, etc.). The CLI flag flips environments; the yaml decides what each environment maps to. **No code change** between environments — only env vars injected by the caravan runtime into containers / Lambda environment / etc.

**Resource tiering is explicit, not inferred.** Scale, latency, throughput, durability, and cost are deliberate user choices. caravan never guesses sizing from code shape or expected traffic. Each primitive has a small vocabulary:

| Primitive | Tier / class vocabulary |
|---|---|
| `db.sql` | `tier: dev` (RDS micro, single-AZ) · `prod-small` (Aurora Serverless v2) · `prod` (Aurora provisioned multi-AZ) · `premium` (Aurora multi-AZ + read replicas + I/O-Optimized) · `global` (Aurora Global / DSQL multi-region) |
| `bucket` | `class: standard · intelligent · standard-ia · one-zone-ia · glacier-instant · glacier-flexible · glacier-deep-archive`; `lifecycle:` for transition/expiration rules; `variant: standard · express-one-zone · vectors` for the rare cases where the bucket *type* differs |
| `kv` | `capacity_mode: on-demand · provisioned` (rcu/wcu when provisioned); `tier: standard · global-tables` |
| `queue` | `kind: standard · fifo` |
| `topic` | `kind: standard · fifo` |
| `cache` (v1.x) | `tier: dev · prod-small · prod-cluster · serverless · memorydb` |

The full tier-to-AWS-resource mapping (and per-tier cost/latency/scale characteristics) lives in `aws_service_groups.md`. When caravan adds GCP / Azure later, equivalent mapping tables ship alongside; the user-facing vocabulary stays cloud-agnostic.

**Where tier declarations live**: either at the resource base (the resource's default) or in a target `overrides:` (env-specific deviation). Cleanest convention: declare the prod intent at base; scale *down* in non-prod overrides. caravan requires an explicit tier on every resource declaration (no inferred default beyond "smallest valid tier" if the user opts in to that explicitly).

**Env-var injection contract**: for each resource a `service` `uses:`, caravan derives a canonical env var name and injects it. Documented and stable, so user code is portable:

| Resource type | Env var(s) |
|---|---|
| `bucket` | `<NAME>_BUCKET`, `S3_ENDPOINT_URL` (when local) |
| `queue` | `<NAME>_QUEUE_URL`, `SQS_ENDPOINT_URL` (when local) |
| `topic` | `<NAME>_TOPIC_ARN`, `SNS_ENDPOINT_URL` (when local) |
| `kv` | `<NAME>_TABLE`, `DYNAMODB_ENDPOINT_URL` (when local) |
| `db.sql` | `<NAME>_DATABASE_URL` |
| `secret` | `<NAME>` (the resolved value at runtime) |
| `static_site` | `<NAME>_BASE_URL` |

---

## 7. Rust-specific observations that justify departures from v1

These are the concrete facts from `mapping_aws_to_rust.md` / `mapping_rust_to_aws.md` / `rust_api_diffs.md` that drive v2's shape:

1. **No first-party Pulumi-Rust SDK** (and no CDK-Rust; CDKtf deprecated 2026). v1's Pulumi-in-process strategy excludes Rust users entirely. v2's Terraform emission is therefore *required*, not preferred.
2. **`aws-sdk-rust` supports `endpoint_url`** on every core service (S3, DynamoDB, SQS, SNS, Kinesis, Secrets Manager, SSM, Step Functions, CloudWatch Logs). The Trivial-band cardinality is ~18 pairs — nearly identical to Python's ~22. Cross-language Trivial coverage is real.
3. **`sqlx` + `tokio-postgres` + `sea-orm` are all DSN-driven**. `DATABASE_URL` env-var injection is universal. (Rust gotcha: `sqlx::query!` macros need a live DB at compile time; CI accounts for this with `docker-compose up postgres` before `cargo build`.)
4. **Lambda Rust runtime is GA (Nov 2025)** via `lambda_runtime` + `lambda_http`. axum routers deploy as Lambda container-image or standalone — one codebase, two shapes. This is the empirical proof that `shape: function` belongs in v1: the user-side wrapper exists in Rust (`lambda_http`) and Python (`Mangum`), and caravan's role is just to emit the right Terraform around the same container. The lane is genuinely different from `long-running` (handler ABI, cold-start, IAM-per-function), but those differences are all on the *user's* side of the contract, not caravan's.
5. **Async runtime convergence on Tokio**. AWS SDK, axum, sqlx, apalis, lapin, opensearch, rumqttc — all Tokio. A decorator-style deploy SDK in Rust would force this choice on users; the current no-deploy-SDK path sidesteps the argument. If caravan ever ships a Rust adapter library, requiring Tokio is consistent with where the ecosystem actually is.
6. **Cedar is a Rust project**. Verified Permissions uses the same OSS engine as the `cedar-policy` crate. This is a *strictly cleaner* abstraction story than v1's Cognito-shaped auth — and reinforces "let users wire stateless verification primitives themselves; only manage stateful infra."
7. **`object_store` exists** as a multi-cloud trait abstraction for S3/GCS/Azure/local-file. If caravan ever wants to extend beyond AWS (deferred for v1), the Rust ecosystem already has a pattern.
8. **Shuttle.rs is the native-Rust IaC competitor**. caravan v2 must position itself differently: containers-first, multi-language, Terraform-emitting. Shuttle is single-binary, Rust-only, opinionated platform. Different audience; no zero-sum competition.

---

## 8. Cloud-only list

By removing v1's decorator SDK, v2 loses the abstractions that previously hid certain AWS-only behaviors. The honest consequence is that several services move from "Hard but doable" in v1 to `cloud_only` in v2:

**Newly `cloud_only` in v2** (vs v1):
- **API Gateway** (REST + WebSocket) — without a function-handler SDK, there is no abstraction layer to bridge "Lambda-handler-receives-event" vs "axum-receives-Request". v1 documented adapters; v2 admits this is a per-language `lambda_http`-style concern outside caravan's scope.
- **Cognito user lifecycle** — `cloud_only` for sign-up / MFA / hosted UI / custom-attribute admin. Token *verification* stays trivial (any JWT lib).
- **Step Functions multi-service workflows** — `cloud_only`; ASL state machines deploy via Terraform but no local-side synth. Single-Lambda-task workflows can be tested against `aws-stepfunctions-local`.

**Retained from v1**:
- CloudFront, Lambda@Edge, CloudFront Functions, Global Accelerator
- S3 Express One Zone, S3 Vectors, S3 Object Lambda, S3 Select
- Aurora DSQL, DAX, Neptune Analytics, Kendra
- Bedrock Knowledge Bases / Agents / Guardrails
- SNS Mobile Push (APNs/FCM)
- CloudWatch Synthetics / RUM / Application Signals
- IAM enforcement (LocalStack stubs the API, not the enforcement)
- IoT Device Management / Defender / Analytics / SiteWise / TwinMaker / FleetWise
- SageMaker JumpStart / Canvas
- Forecast (deprecated) / Personalize
- Step Functions Distributed Map

**Net `cloud_only`**: ~20 services (v1 was ~15). This growth is correct, not a regression. Removing the SDK abstractions removes the things that were hiding lock-in. **The list is a feature: it tells users which AWS services lack honest local emulation, so they know where to draw the test-against-cloud boundary.**

IR shape for cloud-only resources:

```yaml
cloud_only:
  llm:
    type: bedrock.llm
    model: anthropic.claude-opus-4-7-20260416-v1:0
  cdn:
    type: cloudfront
    origins: [uploads]
```

Behavior:
- `caravan up --target=aws-*` → provisions via Terraform, injects boto3/aws-sdk-rust env config.
- `caravan up --target=local` → errors loudly: "Resource `llm` is cloud_only; pass `--allow-cloud-only=llm` to call the real AWS service from local dev, or omit it from local runs."

---

## 9. v1 PoC scope — first milestone toward §2

What ships first. Everything in §2 that isn't here is deferred to v1.1+, with the roadmap order below tracking the gap between v1 and the end-state vision.

Hard constraints to keep the v1 PoC shippable in weeks, not months:

- **5 primitives**: `service`, `bucket`, `queue`, `db.sql`, `secret`.
- **2 `service` shapes**: `long-running` (Fargate / App Runner) and `function` (Lambda container-image). Under v2's no-SDK design, the `function` shape is *not* a per-language handler abstraction — it's just a different Terraform deploy target for the same container the user already built. The user's code wraps itself in `lambda_http` (Rust) or `Mangum` (Python) inside the container; caravan generates the Lambda Terraform instead of the Fargate Terraform. Same image, different runtime config.
- **Triggers in v1**: `http` (Function URL for `function`-shape; ALB for `long-running`-shape) and `queue` (SQS event source mapping for `function`; long-poll consumer in user code for `long-running`).
- **Deferred to v1.1**: `topic`, `kv`, `static_site`, `cron` triggers, `service` `shape: batch`, API Gateway (use Function URLs in v1 — simpler, no APIGW route/method mapping layer).
- **Targets**: `local` (docker-compose; `function`-shape services run as long-lived FastAPI/axum servers locally) and `aws-dev` (Terraform → AWS).
- **Languages**: language-neutral. Ship two reference apps under `/examples`: one Python (FastAPI + Mangum), one Rust (axum + `lambda_http`). Each demonstrates a `long-running` service AND a `function`-shape service sharing handler code with its long-running sibling — proving the "one container, two shapes" claim end-to-end.
- **IaC**: emit OpenTofu HCL; wrap `tofu apply` in `caravan up`. State backend = S3 + DynamoDB lock, bootstrapped by `caravan init`.
- **CLI**: `caravan init | up | down | spec | diff` — no live-reload, no console UI, no debugger proxy.

Explicitly **not in scope** for v1:
- TypeScript / Go reference apps (the no-SDK design means these "just work" — confirm in v1.1).
- GCP / Azure providers.
- Cognito or any auth primitive (use `cloud_only` for now; the right v1.1 work is `TokenVerifier`-style protocol docs, not a primitive).
- API Gateway REST / WebSocket, AppSync, Step Functions, Bedrock, SageMaker (cloud-only for v1; users wire SDKs directly).
- Live debugging proxy.
- Multi-region.
- Console UI.

**Why Lambda fits v1 (it nearly didn't)**: in v1 (the prior doc), Lambda required a per-language decorator SDK to bridge handler ABIs — that was meaningful per-language work, which is why it was deferred. v2 deletes the SDK. With no SDK, Lambda is just a Terraform `aws_lambda_function` template pointing at the user's container image, plus an `aws_lambda_function_url` for HTTP triggers and an `aws_lambda_event_source_mapping` for queue triggers. The user's container already handles the handler ABI in their own language's idiom (`lambda_http`, `Mangum`). caravan's only job is to inject env vars the same way it does for Fargate. The whole "Lambda lane" collapses to a few hundred lines of HCL templates.

**Verification checklist** (when v1 is built):
- [ ] `caravan init` creates state backend (one-time per AWS account).
- [ ] `caravan up --target=local` brings up the full local stack via docker-compose. Both reference apps (Python + Rust) run against it — including their `function`-shape services running as long-lived servers locally.
- [ ] `caravan up --target=aws-dev` provisions equivalent AWS resources via Terraform. The same container images deploy as Fargate services AND as Lambda container-image functions; both share handler code.
- [ ] Switching `--target` between runs is fast (Terraform state cached locally; docker-compose is incremental).
- [ ] IAM policies on AWS are auto-derived from `uses:` declarations — for both Fargate task roles AND Lambda execution roles. No manual policy editing required.
- [ ] HTTP invocation works for `long-running` (via ALB or App Runner endpoint) and for `function` (via Lambda Function URL).
- [ ] Queue trigger works for `function` (SQS event source mapping) end-to-end.
- [ ] `caravan spec --json` prints the IR.
- [ ] `caravan spec --check` warns on env-var/uses mismatches.
- [ ] Both reference apps' container builds work without caravan installed — caravan is deploy tooling, not a build dependency.
- [ ] Cloud-only resources error usefully when the user tries `--target=local`.

---

## 10. Roadmap from v1 to the §2 end-state

Ordered by what unblocks the most user value first. Each milestone is independently shippable.

| Milestone | Adds | Why this order |
|---|---|---|
| **v1** | 5 primitives (`service`, `bucket`, `queue`, `db.sql`, `secret`), 2 shapes (`long-running`, `function`), 2 triggers (`http`, `queue`), AWS only, Python + Rust reference apps with Tier 1 community libraries (rig-core, litellm) wired in `/examples` | Validates the no-SDK + Terraform-emission + two-shapes thesis. Smallest set that proves the design holds across two languages and two compute shapes. |
| **v1.1** | `topic`, `kv` primitives; `cron` triggers; Go + TypeScript reference apps; `caravan logs` + `caravan exec`; **Tier 1 gap survey** — audit community libraries for each Tier 1 pair across all four languages; document recommendations | Closes the "all 8 stateful primitives" gap and confirms the no-SDK design generalizes. Decides whether `caravan-adapters-*` ships any code or stays as documentation only. |
| **v1.2** | `static_site` primitive (S3 + CloudFront / nginx); `bucket-event` trigger; `caravan preview --pr=N`; **`caravan-adapters-*` released for proven gaps only** (likely Cognito JWKS verify, maybe EmailSender) | First step into edge/CDN concerns; PR-preview deploys are the killer DX feature competitors charge for. Adapter library ships only where the v1.1 survey found a real gap. |
| **v1.3** | `shape: batch`; API Gateway HTTP routing layer; OTel + X-Ray default wiring | Closes the trigger/edge story (per-route mappings) and the observability story for cloud. |
| **v2** | `terraform-module` escape hatch primitive; cloud_only registry with documented SDK snippets; GHA workflow templates for non-trivial pipelines | Lets caravan be useful for AWS features caravan itself doesn't wrap — the long tail. |
| **v2.x** | GCP and Azure provider emission; same primitives | Validates the cloud-agnostic IR claim. Order between GCP and Azure decided by user demand. |
| **deferred indefinitely** | Console UI; live-reload debugging proxy; EKS target; multi-account governance; Kubernetes-shape services | Each is its own product. Stay in lane. |

The v1 set is the **smallest scope that exercises every novel design decision** in §3–§8 (no-SDK, two shapes, Terraform emission, env-var injection, cloud-agnostic IR). Subsequent milestones add coverage without re-deciding architecture.

---

## 11. Risks / honest scope boundary

- **No deploy-time SDK = no compile-time IAM/policy inference.** yaml mistakes (typos in `uses:`, missing resource declarations) and env-var typos in source surface at runtime. Mitigation: `caravan spec --check` greps source files for env-var references; warns on mismatches. For Tier 1 services, the recommended community library's typed API (rig-core's `bedrock::Client::from_env()`, litellm's typed model strings) provides the type safety caravan's deploy layer doesn't.
- **Lambda inclusion means the user must write the handler-ABI wrapper themselves.** caravan generates the Lambda Terraform and injects env vars, but the user's code has to be `lambda_http`-shaped (Rust) or `Mangum`-shaped (Python). For `long-running` services there's no such wrapper — you just listen on a port. The reference apps demonstrate both. This is the natural seam under no-SDK; documenting it clearly matters more than abstracting it.
- **Lambda cold starts vary per language.** Rust container-image Lambdas typically start in <100 ms; Python in 500–1500 ms; Java in seconds. caravan defaults are runtime-agnostic; users tune memory / provisioned concurrency / SnapStart via `overrides:` per target if needed. v1 doesn't ship cold-start tuning helpers.
- **Function URL only in v1; no API Gateway.** Function URLs handle the 90% case (HTTPS endpoint, IAM-or-none auth, no per-route mapping). API Gateway REST/HTTP/WebSocket routing layers are deferred to v1.1 — they're a separate complexity layer that doesn't share much with the container-shape concerns.
- **Terraform state management is opinionated.** Users wanting different backends edit `backend.tf`. Acceptable v1 friction; documented.
- **No live debugging proxy** (SST-style). Containers + ports + IDE debugger is the answer. Saves engineering effort; aligns with containers-first framing.
- **`cloud_only` list grew (~+5 from v1).** This is the *honest* scope boundary, not a regression. Removing the SDK removes the abstractions that previously hid Cognito/APIGW/Step-Functions lock-in. Users see the boundary instead of falling into it.
- **Risk that Terraform emission limits expressiveness vs Pulumi.** HCL has weaker programmability than Pulumi's TypeScript/Go SDKs. For the 5-primitive scope, this is fine. As cloud_only resource definitions accrete (Bedrock KB, Step Functions, etc.), HCL templates may grow ugly. If/when that hurts, evaluate Pulumi-Go-as-CLI-internal — but only after the friction is real.

---

## 12. Risk list — divergence gotchas in "easy" mappings

The Trivial-band pairs are not 100% identical between cloud and local. Each carries a known divergence; users must be told. These survive verbatim from v1:

| Pair | Gotcha | Mitigation |
|---|---|---|
| S3 ↔ minio | Strong-read-after-write semantics differ under concurrent writes / degraded modes. | Document; for prod assumptions lean on S3 docs, not local behavior. |
| S3 ↔ minio | Lifecycle policies use different DSLs. | caravan generates S3 lifecycle for AWS; emits best-effort minio command locally. |
| S3 ↔ minio (Rust-specific) | `aws-sdk-s3` requires `force_path_style(true)` against minio; AWS rejects it. | Set conditionally on `S3_ENDPOINT_URL` presence. |
| DynamoDB ↔ dynamodb-local | Streams partial; TTL deletes happen on best-effort timing. | Don't write code that depends on TTL timing for correctness. |
| SQS ↔ ElasticMQ | No per-account throttle quotas locally; `ThrottlingException` never fires in dev. | Chaos-test throttle handling in staging. |
| SQS FIFO ↔ ElasticMQ FIFO | Dedup window precision differs by ms. | Don't rely on exact 5-min window in tests. |
| Postgres (RDS/Aurora) ↔ postgres container | Aurora-specific extensions don't exist vanilla; vanilla extensions Aurora hasn't approved fail in Aurora. | caravan warns at IR validation if an extension isn't on Aurora's supported list. |
| Postgres (sqlx, Rust-specific) | `sqlx::query!` needs live DB during `cargo build`. | CI spins up postgres before build, or `sqlx prepare` for offline metadata. |
| RDS minor-version auto-upgrades | Maintenance windows break pinned driver-extension versions. | Use Aurora (broader compat) or disable auto-minor-upgrade. |
| DocumentDB ↔ mongo | DocumentDB lacks ~30% of aggregation operators. | Test critical aggregations against DocumentDB in CI, not just local mongo. |
| ElastiCache cluster-mode ↔ single redis | Cross-slot pipelines fail on cluster. | Use `redis-cluster` locally if your code uses cluster mode in prod. |
| OpenSearch ↔ opensearch image | UltraWarm tier behaviors don't reproduce; ML plugins version-drift. | Pin OpenSearch versions to match. |
| Kinesis ↔ localstack | No mature Rust KCL port; coordination behavior differs at scale. | Test producer locally; test consumer at scale against real Kinesis. |
| MSK with IAM auth (Rust-specific) | `aws-msk-iam-sasl-signer-rust` is less battle-tested than Java/Python. | Prefer SCRAM-SHA-512 or mTLS for MSK from Rust until further notice. |
| SES ↔ mailhog | SES throttles on reputation + warmup; mailhog never throttles. | Don't load-test through SES sandbox; request prod access first. |
| Step Functions ↔ aws-stepfunctions-local | Distributed Map, Express semantics, intrinsic-function library drift with local container version. | Pin local container version; caravan flags ASL features not supported by pinned version. |
| IoT Core MQTT ↔ mosquitto | IoT Core mandates mTLS; mosquitto accepts plaintext. | Run mosquitto with TLS in CI to catch handshake bugs. |

---

## 13. One-page summary

> **End-state vision (§2)**: caravan is a containers-first deploy tool. One yaml manifest, any language, any cloud (AWS first; GCP/Azure reachable later). User code is unmodified containers wired to env-var-driven endpoints; caravan generates docker-compose locally, Terraform/OpenTofu HCL for cloud, and GHA workflows for CI. No SDK, no runtime coupling, no language lock-in. 8 primitives (`service`, `bucket`, `queue`, `topic`, `kv`, `db.sql`, `secret`, `static_site`), 3 `service` shapes (`long-running`, `function`, `batch`), 5 trigger types, ~20 cloud-only resource types with documented escape-hatch guidance, hand-written-`.tf` extension model for the long tail.
>
> **Architectural shifts from v1** (§3–§5): Taking the SoC-containers assumption seriously collapses v1's `function` primitive into a *shape* of a more fundamental `service` primitive. The Python-SDK-with-decorators layer dissolves: yaml + env-var injection is sufficient for the ~18–22 Trivial pairs, which makes Rust/Go/TypeScript support free at the deploy layer. For the ~3–5 Tier 1 hard pairs (Bedrock + Ollama LLMs, Cognito + local JWT, etc.), caravan recommends mature community libraries (rig-core / litellm / etc.) rather than reinventing trait shapes. The Rust ecosystem's missing Pulumi SDK is the deciding factor for replacing v1's in-process Pulumi-Python with Terraform / OpenTofu HCL emission — language-neutral, reviewable diffs in CI, no per-language runtime coupling.
>
> **The IR** (§6): one `caravan.yaml` that projects to three artifacts: `docker-compose.generated.yaml` (local), `infra/<target>/*.tf` (cloud), and `.github/workflows/deploy-<target>.yml` (CI). Switching is a single `--target=` CLI flag. No code change between environments — only env vars injected by the caravan runtime.
>
> **v1 ships first** (§9, §10 roadmap): **5 primitives** (`service`, `bucket`, `queue`, `db.sql`, `secret`) with **two shapes** (`long-running` → Fargate/App Runner; `function` → Lambda container-image), **two triggers** (`http`, `queue`), **two targets** (local + aws-dev), **AWS only**, and **Python (FastAPI+Mangum) + Rust (axum+lambda_http) reference apps** that demonstrate the same handler code deploying as both Fargate and Lambda from the same container image. v1 is the *smallest scope that exercises every novel design decision* (no-SDK, two shapes, Terraform emission, env-var injection) — every subsequent milestone (v1.1 → v2.x) adds coverage without re-deciding architecture.
>
> **Honest boundary** (§8, §11): the `cloud_only` list grew from v1's ~15 to v2's ~20 — API Gateway, Cognito user-lifecycle, and Step Functions multi-service workflows joined it because removing the SDK removes the abstractions that were hiding those lock-ins. ~18 Rust pairs and ~22 Python pairs are Trivial; ~7 / ~12 are Moderate adapters; ~3 / ~5 are Hard protocol abstractions; ~18 / ~15 are cloud-only. Treat the cloud-only list as a feature, not a limitation.
