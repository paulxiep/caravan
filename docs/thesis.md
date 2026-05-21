# caravan thesis

> Stable framing for the project, written so future sessions don't re-derive it. The thesis is load-bearing; everything after it is current evaluation that may shift.

## The pain this thesis dissolves

Today, choosing between a monolith and a set of microservices is one of the largest irreversible decisions a team takes. Senior architects and CTOs gate it because reversing it means rewriting code, redoing oncall, and re-wiring deploy. The thesis below is that this decision should not be irreversible at all: with the right structural primitive, it becomes a yaml line, set per target, reversible at any time, with source code unchanged.

## Thesis

An application is a graph of **modules** connected by interfaces. caravan lets one yaml project that graph onto any point in three orthogonal dimensions, with the source code unchanged.

### The three dimensions

1. **Packaging: how source seams become deploy units.**
   - Modular monolith (N seams → 1 process)
   - Multi-container (N seams → N containers in one deploy unit, co-located)
   - Multi-service (N seams → N independently deployed units)

   Driven by yaml. The user wraps inter-component call sites in the **caravan-rpc SDK** (`@wagon` / `provide` / `client`); each such site is a *seam*, a candidate split point. Per target, yaml decides per-seam: `inproc` (no new deploy unit), `container` (new compose service / Fargate task), or `lambda` (new Lambda function). Containers are *derived* from these decisions, not declared. Caravan does the packaging arithmetic, computing per-deploy-unit `CARAVAN_RPC_PEERS` tables the SDK reads at runtime to route each call.

2. **Placement: where processes run.**
   - Local (docker-compose)
   - Cloud long-running (Fargate / App Runner)
   - Cloud function (Lambda)
   - Cloud batch

   Driven by yaml `targets:` × `bundle.shape:` (where a bundle is a packaging unit of 1..N modules; see the A and J dispositions in `considerations.md`).

3. **Composition: what each resource is bound to.**
   - Local OSS engine (minio, postgres, dynamodb-local, …)
   - Cloud managed service (S3, RDS, DynamoDB, …)
   - Existing cloud resource referenced by ID from another deploy

   **Mixing is first-class.** Local processes can talk to real cloud resources in the same run.

These dimensions are orthogonal. A yaml `target:` names a point in (packaging × placement × composition). A repo can declare many such targets: `dev`, `hybrid-dev`, `staging`, `prod`, `pr-preview`. `caravan up --target=<name>` flips between them. Same source code everywhere.

### Why this matters

The same modular codebase, four lives, zero rewrites:

- **Local monolith** for fast inner loop with hot reload via volume mounts.
- **Multi-container on staging Fargate** for cheap integration tests with realistic process boundaries.
- **Multi-service on prod Fargate** for fault isolation and independent scaling.
- **Local processes pointing at real cloud queues + real Bedrock** during incident debugging, so production state is preserved while you iterate.

Every transition is a yaml edit.

The word *modular* in "modular codebase" carries weight: caravan asks user code to express its inter-component dependencies through the `caravan-rpc` SDK rather than concrete cross-imports. A codebase that already follows this shape converts in hours; one that doesn't pays a one-time refactor cost that is independently valuable (the same shape good DI demands). See [`rust_caravan_style.md`](rust_caravan_style.md) for the per-language prescription (Rust first; other languages follow).

## Direction this primitive enables

> Direction, not roadmap. The development plan ([`development_plan.md`](development_plan.md)) is authoritative for what is being built. This section names the horizon the primitive opens up, so future sessions can tell which expansions are coherent with the thesis and which are scope creep.

**One-line framing.** Think of caravan as Airflow for cloud architecture: a declared graph and a runtime view, with tier/cost decisions promoted from senior-architect tribal knowledge into a queryable surface. Airflow is the closest existing reference; the parallel is the declared-graph + runtime-observability + structured-catalogue shape, not the scheduling part. A better analogy may emerge as the direction matures.

The same primitive that makes monolith ↔ microservices a yaml line also collects two data assets nothing else has in one place: the compile-time deploy graph (every deploy unit, every resource binding, every seam-mode decision per target) and the runtime per-seam dispatch frequency (the SDK already sees every cross-component call). Together these make the second pain in the original framing tractable.

**The second pain.** Today, qualifying as a Solutions Architect gates behind memorising cloud-service costs, latencies, throughputs, and limits, plus the heuristics for trading them off. Most of that is a data-lookup problem dressed up as expertise. The resource model already declares explicit tiering (`db.sql: tier: prod-small`, `bucket: class: standard`, `kv: capacity_mode: on-demand`); the per-cloud mapping tables in [`aws_service_groups.md`](aws_service_groups.md), [`gcp_service_groups.md`](gcp_service_groups.md), and [`azure_service_groups.md`](azure_service_groups.md) are already the lookup tables. What is missing is the wiring that turns "what does this seam cost on Fargate vs. inproc" or "is RDS `prod-small` enough for this `db.sql`" into a query against the deploy graph rather than a senior-architect role-play.

**What this opens up, named so future sessions don't re-derive it.**

- *Per-seam cost attribution.* Cross the compile-time deploy graph with runtime per-seam call counts and the per-cloud cost tables; output a $-figure per seam per target. Generic FinOps tools cannot do this because they work at resource granularity, not component granularity.
- *What-if simulation across targets.* Given a historical dispatch log and the emitted HCL for two targets, attribute the cost delta to specific seam-mode changes before any deploy.
- *Structured catalogue replacing rote SA knowledge.* The catalogues are already structured per cloud; the missing piece is the query layer that lets a caravan.yaml resolve "I need a `bucket` at `class: standard` in `eu-west-1`" into the cheapest provider/region/tier combination meeting the declared constraints.

None of this is committed in the dev plan; surfacing it here means a future Phase 3 expansion in this direction is in line with the thesis, not a pivot.

## Stable design principles

These follow directly from the thesis and are unlikely to shift:

- **One yaml is the IR.** Projections (docker-compose, Terraform/HCL, GHA) are generated from it; never the reverse.
- **Cloud-agnostic primitives by name** (`bucket`, not `s3`). Schema doesn't break when non-AWS providers are added.
- **Auditable IaC artifacts.** Generated HCL is reviewable in CI; no opaque deploy step.
- **One structural contract: the caravan-rpc SDK at inter-component seams.** Within that single contract, user code is whatever the user wrote; caravan composes the deploy graph (per-target container partitioning + IaC emission) without further restructuring. The earlier framing "user code already containerizable; caravan never asks the user to restructure" is partially superseded: the SDK contract IS one structural ask; everything else holds. See `poc_rpc_sdk.md` §1 for the without-SDK anti-pattern this contract prevents, and `rust_caravan_style.md` for the per-language adoption prescription (ten rules + readiness scorecard; Rust first).
- **Caravan owns build artifacts per target.** User owns the Dockerfile per container. Caravan patches the user's package manifest (`Cargo.toml` / `requirements.txt` / `package.json` / `go.mod`) in the per-target build context with two categories of caravan-managed deps: the RPC SDK (always) and Tier-1 hard-pair provider selection (the `llm` group's Bedrock vs Ollama provider, etc.). User's on-disk manifest is untouched.
- **One caravan.yaml = one VPC.** Each yaml emits one network boundary (one VPC for cloud, one compose network for local). Multi-VPC apps use multiple yamls; caravan does not coordinate across them.
- **Abstraction libraries are structural for hard pairs.** Where a cloud service and its local counterpart speak different wire APIs (Bedrock ↔ Ollama, Cognito ↔ local JWT, SES ↔ SMTP), keeping user code the same across deployments requires an abstraction layer with cloud + local impls. The layer can live in a community library, in a caravan-authored library, or in user code, but it must exist. This follows from the thesis (same code, different deployments); it isn't a tradeoff. *Who authors it* per pair is a separate scope call, in Current evaluation below.
- **Resource tiering is explicit, not inferred.** Scale, latency, throughput, durability, and cost are deliberate human choices, not derivable from code or usage. Each resource primitive exposes a small vocabulary the user declares directly (`db.sql: tier: dev | prod-small | prod | premium | global`; `bucket: class: standard | intelligent | glacier-instant | …`; `kv: capacity_mode: on-demand | provisioned`; plus a `variant:` field for the rare cases where the resource type itself differs, e.g. S3 Express One Zone vs S3 Vectors vs S3 standard). caravan maps user-declared tiers to cloud-specific resources via mapping tables (`aws_service_groups.md` today; GCP / Azure tables later); it never guesses scale from traffic patterns or code shape.

## Current evaluation (may shift)

Reading the landscape and tradeoffs as of 2026-05-16. These are tradeoff calls, not commitments:

- **Cloud coverage**: AWS first. GCP and Azure reachable by adding HCL provider templates once AWS coverage stabilizes.
- **Language coverage**: Python, Rust, TypeScript, and Go first-class. Container baseline (any language with a Dockerfile) is free.
- **IaC backend**: emit Terraform / OpenTofu HCL today. Driven by language-neutrality, reviewable diffs, and no per-language SDK coupling (notably no Pulumi-Rust SDK). If HCL expressiveness becomes the binding constraint, Pulumi-Go-as-CLI-internal is the next move.
- **Service tiers**:
  - **Tier 0** (~18–22 services): same wire API both sides. Endpoint-URL or DSN env-var swap is enough. No abstraction library required.
  - **Tier 1** (~3–5 hard pairs): different wire APIs both sides. An abstraction library is *structurally required* (see Stable design principles); the current scope call is *who authors it*. Today, mature community libraries cover the well-known pairs: rig-core (Rust), litellm (Python), Vercel AI SDK (TypeScript), and `langchaingo` / `eino` (Go) for LLM providers including Bedrock + Ollama; jsonwebtoken / authlib / `jose` / `golang-jwt` + `keyfunc` for Cognito vs local JWT verify; lettre / smtplib / nodemailer / `net/smtp` or `gomail` for SES vs SMTP catchers. caravan documents the right community library per language; `caravan-adapters-*` may ship for pairs where no good community option exists.
  - **Tier 2** (~15–20 services): no OSS engine reproduces the service locally. `cloud_only:` IR flag is a **provisioning marker** (don't generate a local stand-in), not a runtime guarantee. On a local target, user code chooses per service: feature-flag-skip (CloudFront, RUM, Mobile Push), hit real AWS via mounted creds (Bedrock KB / Agents), swap to a divergent engine (DAX → DDB-local, S3 Express → MinIO, Aurora DSQL → Postgres), or stub the call. caravan provisions the cloud infra; the pattern choice is user judgment.
- **Deploy-time decorators driving deploys** (v1's `@caravan.function` style): not in the current design. yaml is the source of truth; this avoids per-language deploy tooling. Distinct from Tier 1 runtime adapters above. Revisitable if a real ergonomics gap shows up that yaml + env-vars can't close.
- **Currently out of scope** (each could be a real product on its own): Kubernetes target, live debugging proxy, console UI, multi-account governance, hosted SaaS. Could change if demand justifies.

## Positioning (orientation, not commitment)

- **Application-definition compiler.** caravan sits between application code and infrastructure-as-code. Write one yaml describing the module graph and its bound cloud resources. Run `caravan compile --target=<name>` to emit auditable Terraform/HCL (cloud) or `docker-compose.generated.yaml` (local) into `infra/<target>/generated/`; review or hand-correct as needed; then `caravan up --target=<name>` applies the emitted spec via `tofu apply` (cloud) or `docker compose up` (local). Emit and apply are separate commands so the HCL artifact is genuinely reviewable, not a transient byproduct.
- **Not Encore-shaped.** Encore generates RPC stubs from code structure via codegen. caravan requires user code to call across component boundaries through the caravan-rpc SDK; caravan handles only the packaging arithmetic, computing per-container `CARAVAN_RPC_PEERS` dispatch tables the SDK reads at runtime. The SDK is request/response HTTP/JSON v1; user keeps whatever web framework they want for external entry.
- **Not Pulumi-shaped.** Pulumi makes IaC an imperative SDK in user language. caravan emits HCL artifacts the user reads and reviews.
- **Not Kubernetes-shaped.** Managed runtimes (Fargate, App Runner, Lambda) are the default lane; EKS is reachable but unprioritized.
- **One caravan-authored runtime library in user code: `caravan-rpc`.** Everything else (AWS SDKs, Tier-1 abstraction libraries like litellm / rig-core / Vercel AI SDK / langchaingo, web frameworks) is what the user would have imported anyway; caravan just patches the dep into the per-target build context so the user doesn't have to add it manually. The RPC SDK is the one runtime piece because the packaging dimension is unverifiable without a runtime that abstracts inproc vs HTTP vs Lambda dispatch. Everything else stays deploy tooling.

## Companion documents

- `mapping_aws_to_python.md`, `mapping_python_to_aws.md`, `python_api_diffs.md`: Python ecosystem evidence.
- `mapping_aws_to_rust.md`, `mapping_rust_to_aws.md`, `rust_api_diffs.md`: Rust ecosystem evidence.
- `mapping_aws_to_typescript.md`, `mapping_typescript_to_aws.md`, `typescript_api_diffs.md`: TypeScript ecosystem evidence.
- `mapping_aws_to_go.md`, `mapping_go_to_aws.md`, `go_api_diffs.md`: Go ecosystem evidence.
- `aws_service_groups.md`: cost / latency / scale catalog of AWS services.
- `cloud_providers.md`: cross-provider primitive mapping (AWS / GCP / Azure) and divergences from the AWS baseline.
- `caravan_abstraction_v4.md`: long-form derivation that produced this thesis (four-language re-derivation; supersedes v3). Detail, scope, gotchas live there.
- `caravan_abstraction_v3.md`: prior long-form derivation, Python + Rust + TypeScript. Historical record.
- `caravan_abstraction_v2.md`: prior long-form derivation, Python + Rust only. Historical record.
- `caravan_abstraction_v1.md`: prior framing built around a Python SDK. Historical record.
