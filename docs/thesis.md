# supeux — Thesis

> Stable framing for the project, written so future sessions don't re-derive it. The thesis is load-bearing; everything after it is current evaluation that may shift.

## Thesis

An application is a graph of **modules** connected by interfaces. supeux lets one yaml project that graph onto any point in three orthogonal dimensions, with the source code unchanged.

### The three dimensions

1. **Packaging — how modules become processes.**
   - Modular monolith (N modules → 1 process)
   - Multi-container (N modules → N containers in one deploy unit, co-located)
   - Multi-service (N modules → N independently deployed units)

   Driven by yaml. The user's modular discipline + a community RPC library handle the inter-module abstraction; supeux does the packaging arithmetic.

2. **Placement — where processes run.**
   - Local (docker-compose)
   - Cloud long-running (Fargate / App Runner)
   - Cloud function (Lambda)
   - Cloud batch

   Driven by yaml `targets:` × `service.shape:`.

3. **Composition — what each resource is bound to.**
   - Local OSS engine (minio, postgres, dynamodb-local, …)
   - Cloud managed service (S3, RDS, DynamoDB, …)
   - Existing cloud resource referenced by ID from another deploy

   **Mixing is first-class.** Local services can talk to real cloud resources in the same run.

These dimensions are orthogonal. A yaml `target:` names a point in (packaging × placement × composition). A repo can declare many such targets — `dev`, `hybrid-dev`, `staging`, `prod`, `pr-preview` — and `supeux up --target=<name>` flips between them. Same source code everywhere.

### Why this matters

The same modular codebase, four lives, zero rewrites:

- **Local monolith** for fast inner loop with hot reload via volume mounts.
- **Multi-container on staging Fargate** for cheap integration tests with realistic process boundaries.
- **Multi-service on prod Fargate** for fault isolation and independent scaling.
- **Local processes pointing at real cloud queues + real Bedrock** during incident debugging, so production state is preserved while you iterate.

Every transition is a yaml edit.

## Stable design principles

These follow directly from the thesis and are unlikely to shift:

- **One yaml is the IR.** Projections (docker-compose, Terraform/HCL, GHA) are generated from it; never the reverse.
- **Cloud-agnostic primitives by name** (`bucket`, not `s3`). Schema doesn't break when non-AWS providers are added.
- **Auditable IaC artifacts.** Generated HCL is reviewable in CI; no opaque deploy step.
- **SoC-containers assumed.** User code is already containerizable; supeux deploys it, never asks the user to restructure it.
- **Abstraction libraries are structural for hard pairs.** Where a cloud service and its local counterpart speak different wire APIs (Bedrock ↔ Ollama, Cognito ↔ local JWT, SES ↔ SMTP), keeping user code the same across deployments requires an abstraction layer with cloud + local impls. The layer can live in a community library, in a supeux-authored library, or in user code — but it must exist. This follows from the thesis (same code, different deployments); it isn't a tradeoff. *Who authors it* per pair is a separate scope call, in Current evaluation below.
- **Resource tiering is explicit, not inferred.** Scale, latency, throughput, durability, and cost are deliberate human choices, not derivable from code or usage. Each resource primitive exposes a small vocabulary the user declares directly (`db.sql: tier: dev | prod-small | prod | premium | global`; `bucket: class: standard | intelligent | glacier-instant | …`; `kv: capacity_mode: on-demand | provisioned`; plus a `variant:` field for the rare cases where the resource type itself differs — S3 Express One Zone vs S3 Vectors vs S3 standard). supeux maps user-declared tiers to cloud-specific resources via mapping tables (`aws_service_groups.md` today; GCP / Azure tables later); it never guesses scale from traffic patterns or code shape.

## Current evaluation (may shift)

Reading the landscape and tradeoffs as of 2026-05-16. These are tradeoff calls, not commitments:

- **Cloud coverage**: AWS first. GCP and Azure reachable by adding HCL provider templates once AWS coverage stabilizes.
- **Language coverage**: Python, Rust, TypeScript, and Go first-class. Container baseline (any language with a Dockerfile) is free.
- **IaC backend**: emit Terraform / OpenTofu HCL today. Driven by language-neutrality, reviewable diffs, and no per-language SDK coupling (notably no Pulumi-Rust SDK). If HCL expressiveness becomes the binding constraint, Pulumi-Go-as-CLI-internal is the next move.
- **Service tiers**:
  - **Tier 0** (~18–22 services): same wire API both sides. Endpoint-URL or DSN env-var swap is enough. No abstraction library required.
  - **Tier 1** (~3–5 hard pairs): different wire APIs both sides. An abstraction library is *structurally required* (see Stable design principles); the current scope call is *who authors it*. Today, mature community libraries cover the well-known pairs — rig-core (Rust), litellm (Python), Vercel AI SDK (TypeScript), and `langchaingo` / `eino` (Go) for LLM providers including Bedrock + Ollama; jsonwebtoken / authlib / `jose` / `golang-jwt` + `keyfunc` for Cognito vs local JWT verify; lettre / smtplib / nodemailer / `net/smtp` or `gomail` for SES vs SMTP catchers. supeux documents the right community library per language; `supeux-adapters-*` may ship for pairs where no good community option exists.
  - **Tier 2** (~15–20 services): no OSS engine reproduces the service locally. `cloud_only:` IR flag is a **provisioning marker** (don't generate a local stand-in), not a runtime guarantee. On a local target, user code chooses per service: feature-flag-skip (CloudFront, RUM, Mobile Push), hit real AWS via mounted creds (Bedrock KB / Agents), swap to a divergent engine (DAX → DDB-local, S3 Express → MinIO, Aurora DSQL → Postgres), or stub the call. supeux provisions the cloud infra; the pattern choice is user judgment.
- **Deploy-time decorators driving deploys** (v1's `@supeux.function` style): not in the current design. yaml is the source of truth; this avoids per-language deploy tooling. Distinct from Tier 1 runtime adapters above. Revisitable if a real ergonomics gap shows up that yaml + env-vars can't close.
- **Currently out of scope** (each could be a real product on its own): Kubernetes target, live debugging proxy, console UI, multi-account governance, hosted SaaS. Could change if demand justifies.

## Positioning (orientation, not commitment)

- **Application-definition compiler.** supeux sits between application code and infrastructure-as-code. Write one yaml describing the module graph and its bound cloud resources. Run `supeux compile --target=<name>` to emit auditable Terraform/HCL (cloud) or `docker-compose.generated.yaml` (local) into `infra/<target>/generated/`; review or hand-correct as needed; then `supeux up --target=<name>` applies the emitted spec via `tofu apply` (cloud) or `docker compose up` (local). Emit and apply are separate commands so the HCL artifact is genuinely reviewable, not a transient byproduct.
- **Not Encore-shaped.** Encore generates RPC stubs from code structure via codegen. supeux requires modular code with both in-process and remote impls (via community RPC libraries) and handles only the packaging arithmetic.
- **Not Pulumi-shaped.** Pulumi makes IaC an imperative SDK in user language. supeux emits HCL artifacts the user reads and reviews.
- **Not Kubernetes-shaped.** Managed runtimes (Fargate, App Runner, Lambda) are the default lane; EKS is reachable but unprioritized.
- **Not a runtime library in user code.** supeux is deploy tooling. User code reads env vars and uses whatever AWS SDK / community library it would have used anyway. (A small per-language adapter library *could* exist for proven Tier-1 gaps — see Current evaluation above.)

## Companion documents

- `mapping_aws_to_python.md`, `mapping_python_to_aws.md`, `python_api_diffs.md` — Python ecosystem evidence.
- `mapping_aws_to_rust.md`, `mapping_rust_to_aws.md`, `rust_api_diffs.md` — Rust ecosystem evidence.
- `mapping_aws_to_typescript.md`, `mapping_typescript_to_aws.md`, `typescript_api_diffs.md` — TypeScript ecosystem evidence.
- `mapping_aws_to_go.md`, `mapping_go_to_aws.md`, `go_api_diffs.md` — Go ecosystem evidence.
- `aws_service_groups.md` — cost / latency / scale catalog of AWS services.
- `cloud_providers.md` — cross-provider primitive mapping (AWS / GCP / Azure) and divergences from the AWS baseline.
- `supeux_abstraction_v4.md` — long-form derivation that produced this thesis (four-language re-derivation; supersedes v3). Detail, scope, gotchas live there.
- `supeux_abstraction_v3.md` — prior long-form derivation, Python + Rust + TypeScript. Historical record.
- `supeux_abstraction_v2.md` — prior long-form derivation, Python + Rust only. Historical record.
- `supeux_abstraction_v1.md` — prior framing built around a Python SDK. Historical record.
