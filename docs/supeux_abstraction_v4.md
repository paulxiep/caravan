# supeux Abstraction Recommendation — v4 (four-language re-derivation)

> **Snapshot date: 2026-05-16.** Derived independently from the business requirements below, drawing evidence from `aws_service_groups.md`, `mapping_aws_to_python.md`, `mapping_python_to_aws.md`, `python_api_diffs.md`, `mapping_aws_to_rust.md`, `mapping_rust_to_aws.md`, `rust_api_diffs.md`, `mapping_aws_to_typescript.md`, `mapping_typescript_to_aws.md`, `typescript_api_diffs.md`, **and the newly added `mapping_aws_to_go.md`, `mapping_go_to_aws.md`, `go_api_diffs.md`**.
>
> **Read `thesis.md` first** for the crystallized framing. This file is the long-form derivation that produced it — primitive shapes, yaml schema, IaC choice, roadmap, gotchas. The thesis is load-bearing; specifics in this file are current evaluation and may shift.
>
> **This file supersedes `supeux_abstraction_v3.md` as canonical.** v3 stays as historical record (mirroring how v3 superseded v2 and v2 superseded v1). v4 does **not** build on v3; where they disagree, this file states the disagreement explicitly. The conceptual core (`thesis.md`) is unchanged by v4; v4 confirms the design by re-deriving it with Go evidence folded in.

---

## 1. Why a v4 exists

v3 was synthesized from AWS↔Python, AWS↔Rust, and AWS↔TypeScript mappings. Its conclusions: 8 primitives, no deploy-time SDK, Terraform/OpenTofu HCL emission, Tier 0/1/2 service classification, ~5-primitive v1 PoC, AWS-only first, three reference apps in v1 (Python + Rust + TS).

This session re-derives the design with **four first-class languages** instead of three. Adding Go evidence is the only change in inputs. Three things move:

1. **Tier 0/1/2 headcounts are now cross-validated against four independent languages.** Go's counts (~22 T0 / ~5 T1 / ~15 T2) sit at parity with Python (~22 T0 / ~5 T1 / ~15 T2) and TypeScript (~22 T0 / ~5 T1 / ~15 T2), slightly above Rust (~18 T0 / ~3 T1 / ~18 T2 — Rust's tighter T0 count reflects `aws-sdk-rust`'s narrower endpoint-override coverage vs `aws-sdk-go-v2`'s consistent per-client `BaseEndpoint`). The four-way spread tightens v3's three-way claim; the design holds across four language families.
2. **The "supeux ships no runtime adapter library" call (v3 §4) is reinforced further.** Four languages now have mature Tier 1 community libraries — `litellm` (Python), `rig` (Rust), Vercel AI SDK (TS), and **`langchaingo` / `eino`** (Go) — without any supeux-authored code in user space. Same for token verification (`authlib` / `jsonwebtoken` / `jose` / **`golang-jwt` + `keyfunc`**) and email (`smtplib` / `lettre` / `nodemailer` / **`net/smtp` / `gomail`**). The pattern generalizes harder: when four independent ecosystems converge on different-named libraries that solve the same Tier 1 pair, supeux's job is curation, not implementation. Go's Tier 1 surface adds a small wrinkle: two named community options per pair for LLM (`langchaingo` for breadth, `eino` for typed-chain DSL) and email (`net/smtp` for stdlib minimalism, `gomail` for MIME wrapping). v4 names both and documents the decision criterion.
3. **Go's role is doubly load-bearing.** Per `thesis.md:63`, supeux's own CLI is implemented in Go and Pulumi-Go-as-CLI-internal is the documented next move if HCL expressiveness ever binds. With Go now a first-class user-code language as well, Go sits at both ends of supeux: the language users write applications in and the language the deploy tool itself is written in. The IaC tooling landscape for Go (cdktf-go sunset Dec 10, 2025; Pulumi-Go available but imperative; AWS CDK Go in preview emits CloudFormation) confirms the same conclusion as the other three languages — no first-party HCL-emitting-from-Go toolchain exists, so supeux's HCL-emission posture serves Go users at parity. v4 §7d records the per-Go observations driving this.

Nothing in `thesis.md` needs to change in its load-bearing principles. A small set of *current-evaluation* edits to thesis are surfaced in §14 for user decision; the user has dispositioned all of them — see §14 for details.

---

## 2. End-state vision

Before scoping v1, the end shape supeux is trying to reach. Every v1 / v1.1 / future decision should track toward this.

supeux, fully realized, is a **containers-first deploy tool** that lets a team write SoC-modular services in any language and deploy them to a cloud (AWS first; GCP/Azure reachable by later HCL-provider work) via one yaml manifest. No SDK, no runtime coupling, no language lock-in.

### What the user writes

- **Containers**, one per service. Inside, user code uses the language's normal AWS SDK / driver libraries with `endpoint` / DSN env-var-driven configuration. Lambda-shaped services wrap themselves with the language's idiomatic adapter (`lambda_http` in Rust, `Mangum` in Python, `hono/aws-lambda` / `serverless-http` / `@fastify/aws-lambda` in TS, `aws-lambda-go-api-proxy` in Go) — that wrapper is user code, not supeux code.
- **One `supeux.yaml`** declaring services, resources, triggers, secrets, targets.
- **Optional**: hand-written `.tf` files alongside generated ones, for AWS features supeux hasn't wrapped. supeux never overwrites them.

### What supeux generates

| Target runtime | Generated artifacts |
|---|---|
| `docker-compose` (local) | `docker-compose.generated.yaml` with the user's service containers + OSS dependency containers (postgres, minio, elasticmq, dynamodb-local, redis, opensearch, localstack-SNS, etc.) wired together. |
| `aws` (cloud) | `infra/<target>/*.tf` covering compute (Fargate/App Runner/Lambda container-image, per `shape:`), networking (VPC/subnets/SGs auto-derived from `uses:` graph), stateful resources, IAM (auto-derived from `uses:`), triggers (Function URLs, ALB, SQS event source mappings, EventBridge Scheduler rules, S3 events), observability (CloudWatch log groups; X-Ray sampling), secrets (SSM + Secrets Manager + KMS — never plaintext). |
| CI | `.github/workflows/deploy-<target>.yml` with build / test / deploy stages + PR-preview support. Users edit by hand after generation. |

### CLI surface (end-state)

The flow is **two-step on purpose**: emission produces an artifact on disk that the user can read, hand-correct, and version-control; apply runs that artifact via `tofu apply` (cloud) or `docker compose up` (local). This is what makes "auditable IaC artifacts" actually auditable — the HCL exists as a reviewable file *between* emit and apply, not as transient internal state. `supeux up` does **not** silently re-emit; it consumes the previously emitted spec.

- `supeux init` — one-time state backend bootstrap (S3 bucket + DynamoDB lock table) per AWS account
- **`supeux compile --target=<name>`** — phases 1–5: parse yaml → validate → normalize IR → resolve to provider → emit HCL files (cloud target) or `docker-compose.generated.yaml` (local target) into `infra/<target>/generated/`. **No cloud calls.** Output is meant to be read, optionally hand-corrected via sibling `.tf` files, and committed to git. The verb matches the "application-definition compiler" framing in `README.md` and `thesis.md`.
- `supeux up --target=<name>` — apply the already-emitted spec. For cloud targets: `tofu init && tofu apply` on `infra/<target>/`. For local: `docker compose up` on `docker-compose.generated.yaml`. Refuses if no spec has been emitted for the target. May optionally re-run `supeux compile` first via `--regenerate`, but the default is "apply what's on disk."
- `supeux down --target=<name>` — tear down (`tofu destroy` / `docker compose down`)
- `supeux diff --target=<name>` — preview changes (`tofu plan` against the emitted spec, pretty-printed). Useful gate between `compile` and `up`.
- `supeux spec [--json|--graph]` — inspect IR (phases 1–3 only) as text, JSON, or graphviz. **Distinct from `supeux compile`**: `spec` is a noun-shaped read-only inspector that dumps the cloud-agnostic IR; `compile` is the verb that produces the per-target HCL/compose projection on disk.
- `supeux check` — phases 1–2 only: yaml syntax, cross-refs, env-var usage. Fast; no cloud calls; no emission.
- `supeux logs <service> [--target=<name>] [--follow]` — stream logs through one CLI
- `supeux exec <service> [--target=<name>] -- <cmd>` — run a command inside the running container
- `supeux preview --pr=<n>` — spin per-PR target stack from a target template (still emits → reviews → applies under the hood)
- `supeux generate workflow` — refresh CI files from current yaml

### End-state primitive coverage

- **All 8 primitives** generally available: `service`, `bucket`, `queue`, `topic`, `kv`, `db.sql`, `secret`, `static_site`.
- **All 3 `service` shapes** supported: `long-running` (Fargate / App Runner), `function` (Lambda container-image), `batch` (one-off task on Fargate / AWS Batch).
- **All 5 trigger types** supported: `http`, `queue`, `topic`, `cron`, `bucket-event`.
- **~20 cloud-only resource types** declared via `cloud_only: type: <name>` syntax.

### End-state language coverage

- **First-class** (have reference apps + per-language docs): **Python, Rust, TypeScript/Node, Go** — all four languages have full mapping/api_diffs evidence in v4. The set is closed.
- **Container baseline** (work because they're containers, no special support needed): Java, Ruby, .NET, anything with a Dockerfile.
- Per-language guidance docs (`mapping_aws_to_<lang>.md`, `<lang>_api_diffs.md`) explain the `endpoint` / DSN / Lambda-adapter idiom and recommend the mature community library per Tier 1 hard pair (rig / litellm / Vercel AI SDK / `langchaingo` or `eino` for LLMs; `jose` / `authlib` / `jsonwebtoken` / `golang-jwt` + `keyfunc` for token verify; `nodemailer` / `smtplib` / `lettre` / `net/smtp` or `gomail` for email). The supeux deploy CLI itself ships zero language-specific code; `supeux-adapters-*` packages may exist for proven gaps but are optional and standalone.

### End-state cloud coverage

- **AWS**: full coverage of declared primitives + the cloud-only registry.
- **GCP**: same primitive names map to Cloud Run / GCS / Pub/Sub (queue+topic) / Cloud SQL / Firestore / Secret Manager. Reachable by adding GCP-provider HCL templates after AWS coverage stabilizes.
- **Azure**: same primitives map to Container Apps / Blob / Service Bus / Postgres Flexible Server / Cosmos / Key Vault.
- The IR primitives are **cloud-agnostic by name** (`bucket`, not `s3`) — schema doesn't break when GCP/Azure are added.

### End-state observability (no extra wiring)

- Services emit logs to stdout in JSON; supeux wires runtime collection (awslogs driver / Lambda automatic / docker logs).
- OTel traces on by default; `OTEL_EXPORTER_OTLP_ENDPOINT` env var points at ADOT (cloud) or Jaeger (local).
- Metrics via CloudWatch EMF in logs for basic counter/timer cases; Prometheus sidecar opt-in for advanced.

### End-state extension model (escape hatches)

- Hand-written `.tf` files in `infra/<target>/` are preserved and merged with generated ones; supeux never deletes user HCL.
- `resources: <name>: { type: terraform-module, source: "./modules/foo" }` wraps arbitrary HCL modules into the supeux IR for `uses:` and env-var injection.
- This matters because supeux can't (and shouldn't try to) wrap every AWS feature. The escape hatch keeps supeux useful even when its built-in primitives don't cover something.

### Currently out of scope (revisitable as demand justifies)

- **Serverless-framework UX bias.** Lambda is one *shape* of `service`, not the gravity center.
- **Deploy-time SDK.** yaml is the source of truth — no `import supeux` driving deploys.
- **Per-language adapter libraries.** Community libraries (rig / litellm / Vercel AI SDK / langchaingo / eino / etc.) cover most Tier 1 hard pairs today. `supeux-adapters-*` may ship for proven gaps; not currently a priority.
- **Live debugger / hot-reload proxy.** Containers + IDE debugger + volume-mount-for-source already work.
- **Multi-account governance layer** (Control Tower, AFT, AWS Organizations). Different product.
- **Kubernetes target.** Managed runtimes (Fargate / App Runner / Lambda) are the current default lane.
- **Console UI / hosted SaaS.** Out of scope today.

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

That yields **eight primitives**, derived purely from the requirement. **Go evidence does not move this count** — `aws-sdk-go-v2/service/s3` → `bucket`; `aws-sdk-go-v2/service/sqs` → `queue`; `pgx`/`gorm` → `db.sql`; `aws-sdk-go-v2/service/dynamodb` → `kv`; etc. Same shape across all four first-class languages. The primitive set is language-agnostic by construction.

| Primitive | What it is | Cloud backing | Local backing |
|---|---|---|---|
| `service` | A runnable container with optional HTTP / queue trigger | App Runner / ECS Fargate / Lambda-container | docker-compose service |
| `bucket` | Object store | S3 | MinIO |
| `queue` | Durable point-to-point queue | SQS | ElasticMQ |
| `topic` | Pub/sub fan-out | SNS | LocalStack-SNS |
| `kv` | Key-value store | DynamoDB | dynamodb-local |
| `db.sql` | Relational DB | RDS / Aurora Postgres or MySQL | postgres:16 / mysql:8 |
| `secret` | Secret / config value | SSM + Secrets Manager | env vars (with optional dev `.env` file) |
| `static_site` | SPA / static asset hosting | S3 + CloudFront | nginx container |

**Demoted from primitive to trigger attribute**: `cron`. A scheduled invocation is a property of a `service`.

**Auto-derived, not user-facing**: `network` (VPC, subnets, security groups).

---

## 4. SDK strategy — two separate questions, often confused

There are two distinct SDK questions, and they answer to different rules:

| SDK kind | Example | Status |
|---|---|---|
| **Deploy-time SDK** (decorators driving deploys) | v1's `@supeux.function` / `@supeux.cron` reading Python imports to emit Pulumi resources | **Genuinely optional. Current call: not shipped.** yaml is the source of truth; this avoids language-runtime coupling and per-language deploy tooling. |
| **Runtime adapter library** (abstraction for hard pairs) | `LlmClient` trait with both `BedrockLlm` and `OllamaLlm` impls | **Structurally required wherever cloud and local wire APIs differ.** Not a tradeoff: if Bedrock's `invoke_model` and Ollama's `generate` are different functions, user code cannot be identical without an abstraction layer. The only scope call is *who writes that layer* — community, supeux, or the user. |

### Three tiers

**Tier 0 — Same wire API both sides (~18 Rust / ~22 Python / ~22 TypeScript / ~22 Go pairs)**: endpoint-URL or DSN env-var swap. No abstraction library required.

```python
S3_ENDPOINT_URL = os.environ.get("S3_ENDPOINT_URL")  # None → real S3; http://minio:9000 locally
```

```rust
let s3_endpoint = std::env::var("S3_ENDPOINT_URL").ok();
```

```ts
const s3 = new S3Client({
  endpoint: process.env.S3_ENDPOINT_URL,
  forcePathStyle: !!process.env.S3_ENDPOINT_URL,
});
```

```go
client := s3.NewFromConfig(cfg, func(o *s3.Options) {
    if ep := os.Getenv("S3_ENDPOINT_URL"); ep != "" {
        o.BaseEndpoint = aws.String(ep)
        o.UsePathStyle = true
    }
})
```

**Tier 1 — Different wire APIs (~3 Rust / ~5 Python / ~5 TypeScript / ~5 Go pairs)**: abstraction library required. The library defines an interface and ships both cloud and local impls. User code talks to the library; env var selects impl at startup. (The Rust column counts the v1-PoC-relevant trio — LLM, token-verify, email — that `mapping_aws_to_rust.md` flags as Tier 1 abstractions. STT and vision appear in the table below because community libraries *do* exist for them in all four languages, but the Rust mapping doc treats Rekognition / Transcribe / Polly as "different OSS models" rather than a single-abstraction Tier 1 pair, hence the ~3 versus ~5 spread.)

**The current authorship call: rely on mature community libraries where they exist; supeux ships `supeux-adapters-*` only for proven gaps.** Four independent ecosystems converging on different-named libraries that solve the same Tier 1 pair is overwhelming evidence that the per-language landscape is mature enough for supeux to curate rather than implement:

| Pair | Rust | Python | **Go** | TypeScript |
|---|---|---|---|---|
| LLM (Bedrock + Ollama + others) | `rig` / `rig-core` | `litellm` | **`langchaingo` *or* `eino`** | Vercel AI SDK (`ai` + `@ai-sdk/amazon-bedrock` + `ollama-ai-provider`) |
| Token verification (Cognito JWKS + local JWT) | `jsonwebtoken` + JWKS cache | `authlib` / `python-jose` | **`golang-jwt/jwt/v5` + `MicahParks/keyfunc/v3`** | `jose` |
| Email (SES API + SMTP catcher) | `lettre` / `aws-sdk-sesv2` | `smtplib` / `boto3` | **`net/smtp` *or* `go-gomail/gomail`** | `nodemailer` / `@aws-sdk/client-ses` |
| Speech-to-text (Transcribe + Whisper) | `whisper-rs` + `aws-sdk-transcribe` | `openai-whisper` + `boto3` | **`whisper.cpp/bindings/go`** (CGO) + `aws-sdk-go-v2/service/transcribe` | `@xenova/transformers` (Whisper.js) + `@aws-sdk/client-transcribe` |
| Image analysis (Rekognition + OSS vision) | OpenCV + `aws-sdk-rekognition` | `opencv-python` / ultralytics + `boto3` | **`onnxruntime_go` / `gocv` (CGO)** + `aws-sdk-go-v2/service/rekognition` | `onnxruntime-node` / `@xenova/transformers` + `@aws-sdk/client-rekognition` |

Two columns name **two community options** rather than one: Go's LLM and email rows. These reflect a genuine ergonomic split in the Go ecosystem — `langchaingo` for broader provider coverage (LangChain port) vs `eino` for typed-chain DSL (CloudWeGo); `net/smtp` for stdlib minimalism vs `gomail` for MIME-friendly wrapping. v4 names both rather than picking; the per-language mapping/api_diffs docs document the decision criterion. Other rows are one-canonical-lib per language.

For LLMs specifically, the canonical Tier 1 case, community libraries already provide the abstraction across all four languages:

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
    model=os.environ.get("LLM_MODEL", "ollama/llama3.1"),
    messages=[{"role": "user", "content": "hello"}],
)
```

```ts
import { generateText } from "ai";
import { bedrock } from "@ai-sdk/amazon-bedrock";
import { ollama } from "ollama-ai-provider";
const provider = process.env.LLM_BACKEND === "bedrock" ? bedrock : ollama;
const { text } = await generateText({
  model: provider(process.env.LLM_MODEL ?? "llama3.1"),
  prompt: "hello",
});
```

```go
import (
    "github.com/tmc/langchaingo/llms"
    "github.com/tmc/langchaingo/llms/bedrock"
    "github.com/tmc/langchaingo/llms/ollama"
)
var llm llms.Model
if os.Getenv("LLM_BACKEND") == "bedrock" {
    llm, _ = bedrock.New(bedrock.WithModel(os.Getenv("LLM_MODEL")))
} else {
    llm, _ = ollama.New(ollama.WithModel(os.Getenv("LLM_MODEL")))
}
out, _ := llms.GenerateFromSinglePrompt(ctx, llm, "hello")
```

If a v1.1+ landscape survey finds a Tier 1 pair where no community library covers cloud + local under one abstraction in any first-class language, supeux ships `supeux-adapters-<lang>` for that pair. The principle: prove the gap before writing the library.

**Tier 2 — No-local-stand-in services (~17 Rust / ~15 Python / ~15 TypeScript / ~15 Go pairs)**: "cloud-only" is short for *no OSS engine reproduces this service locally*. The `cloud_only:` IR flag is a **provisioning marker** — it tells supeux not to generate a local docker-compose stand-in. What happens depends on which of four patterns the user picks per service:

| Pattern | Typical services | Local-target behavior |
|---|---|---|
| Skip in local (feature-flag off) | CloudFront, CloudWatch RUM / Synthetics, SNS Mobile Push, Step Functions Distributed Map, IoT Defender | Code paths gated behind a config check; no-op or short-circuit on local. Most common. |
| Hit real AWS from local (mixed mode, requires mounted creds) | Bedrock Knowledge Bases / Agents when iterating against real models | Local container has AWS creds; SDK calls reach real cloud. Mix-composition dimension from the thesis. Costs real $$. |
| Swap to a different engine (accept divergence) | DAX → vanilla DDB-local; S3 Express → MinIO; Aurora DSQL → vanilla Postgres; Neptune Analytics → Neo4j community | Same client crate / library, different engine. Loses the AWS-specific characteristic; tests still run. |
| Stub via a small adapter | When response shape matters but the service is unavailable locally | User wraps the cloud call behind an interface and returns canned data on local. |

supeux's job for Tier 2 is: (a) provision the cloud resource via Terraform, (b) inject AWS creds into local containers when the user opts into mixed mode, (c) document which pattern fits which service. *Which pattern to pick* is user judgment per service.

### Trade-offs and mitigations

**Trade-off accepted**: lose typed resource accessors of the SST `link:` flavor and compile-time IAM-policy inference. The mapping docs (`python_api_diffs.md`, `rust_api_diffs.md`, `typescript_api_diffs.md`, `go_api_diffs.md`) become *user-facing recipes* — including which community library to import for each Tier 1 pair.

**Mitigation for "env var typo at runtime" (Tier 0)**: `supeux spec --check` greps source files for env vars declared in `uses:`. Not a type system, but a useful safety net.

**Mitigation for Tier 1 wiring errors**: community libraries' `from_env()`-style constructors (or env-driven model strings) read canonical env-var names — supeux documents which names to use so its injected vars match what each language's library expects.

**On supeux-authored libraries**: today the per-pair landscape points toward community-library sufficiency for Tier 1 *across four independent languages*; supeux ships zero code libraries and curates guidance instead. The four-language convergence is the strongest evidence yet that this call is right.

---

## 5. IaC strategy: emit Terraform / OpenTofu HCL

The v3 conclusion stands and is strengthened by Go evidence:

**Options considered**:
- (a) Pulumi-Python regardless of user language. Couples the supeux runtime to Python. Awkward.
- (b) Pulumi-Go. supeux CLI in Go, uses Pulumi automation API. Reasonable; couples to Pulumi.
- (c) **Emit Terraform / OpenTofu HCL.** Language-neutral. Reviewable diffs in CI. OpenTofu's Apache-2.0 license matches supeux's posture. HCL is the dominant artifact security/compliance teams audit. State management is well-understood. No per-language SDK coupling.

**(c) wins.** v1's choice was a Python-centric leak; v2 corrected it; v3 confirmed it across three languages; v4 confirms it across four.

**Why Go strengthens the call**: cdktf-go (the Go binding for CDKtf) was sunset and archived along with the rest of CDKtf **December 10, 2025** by HashiCorp/IBM (citing "no product-market fit at scale" — see the [HashiCorp CDKtf page](https://developer.hashicorp.com/terraform/cdktf) and `mapping_aws_to_go.md`). HashiCorp directs former cdktf-go users to HCL, Pulumi-Go, or AWS CDK Go. Pulumi-Go is mature and well-supported but security/compliance teams prefer reviewable HCL over imperative Go. AWS CDK Go (preview) emits CloudFormation, not Terraform/HCL, and ties users to AWS. As of 2026 there is no first-party HCL-emitting-from-Go toolchain — same conclusion as the other three languages.

**An additional Go-specific load-bearing fact**: per `thesis.md:63`, supeux's CLI is implemented in Go, and Pulumi-Go-as-CLI-internal is the documented next move if HCL expressiveness ever binds. With Go now a first-class user-code language as well (v4 §14.1), Go sits at both ends of supeux — user language and CLI language. The HCL-emission call is not "Go users need it because no alternative exists" (true) plus "Go is also what we'd reach for internally" (also true). The two facts compound rather than conflict: HCL emission today, Pulumi-Go internally tomorrow if needed, with the CLI written in the same language users write services in.

**State backend**: opinionated — S3 bucket + DynamoDB lock table, created by a one-time `supeux init`. Users who need different state backends edit `backend.tf` (acceptable v1 friction).

**Two-step emit-then-apply, not one-shot**: this is the load-bearing operational decision for "auditable IaC artifacts." The flow is:

1. **`supeux compile --target=aws-staging`** — runs phases 1–5. Emits HCL into `infra/aws-staging/generated/`. No `tofu` invocation, no cloud calls. The emitted files are a first-class artifact: meant to be read, optionally hand-corrected via sibling `.tf` files alongside `generated/`, and committed to git (or generated in CI and uploaded as a build artifact for review).
2. **(optional) review / hand-correct** — sibling `.tf` files in `infra/aws-staging/` are preserved and merged at apply time. Users can also edit the generated `.tf` directly *if they accept that re-running `supeux compile` will overwrite* — supeux puts a "do not edit; edit sibling .tf instead" header at the top of each generated file. The principle: changes can live in user-owned siblings forever; changes in `generated/` must be re-expressible in yaml or moved to a sibling.
3. **`supeux diff --target=aws-staging`** — runs `tofu plan` against the emitted spec. Pretty-prints the cloud diff. Optional gate before apply, especially in CI.
4. **`supeux up --target=aws-staging`** — runs `tofu init && tofu apply` against `infra/aws-staging/`. **Does not regenerate by default.** If the user wants emit + apply in one shot, `supeux up --regenerate` re-runs `supeux compile` first; this is opt-in, not the default, so that "did the HCL change?" is always a visible git-diff or CI artifact.

This separation is what makes the thesis principle "Auditable IaC artifacts. Generated HCL is reviewable in CI; no opaque deploy step" *operationally* true rather than aspirational. A one-shot `supeux up` that emits and applies in the same invocation would technically produce reviewable HCL on disk afterwards, but the review window is zero — apply already happened. The two-step flow gives the review window first-class status in the workflow.

The model is closest to:
- **Go**: `go build` (compile to static binary) → `./binary` (run). The binary is inspectable, version-controllable.
- **TypeScript**: `tsc` (compile to `.js`) → `node` (run `.js`). The `.js` is inspectable, debuggable, version-controllable.
- **Terraform itself**: writing `.tf` files (by hand) → `terraform plan` → `terraform apply`. supeux just adds an earlier step that writes the `.tf` from yaml.

Not closest to:
- **CDK** (`cdk deploy` synthesizes and applies in one step; synthesized CloudFormation is technically inspectable but isn't part of the workflow).
- **Pulumi** (`pulumi up` runs the program and applies; the resource plan exists only in memory + state).

---

## 6. Yaml shape

One `supeux.yaml` (the IR). Three projections, generated on demand:

- `docker-compose.generated.yaml` (when target's runtime = `docker-compose`)
- `infra/<target>/*.tf` (when target's runtime = `aws`)
- `.github/workflows/deploy-<target>.yml` (CI bootstrap)

Schema (illustrative):

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
    class: standard
    lifecycle: keep-90d
  archives:
    type: bucket
    class: glacier-deep-archive
  jobs:
    type: queue
    kind: standard
  app_db:
    type: db.sql
    engine: postgres
    version: "16"
    tier: prod-small
  sessions:
    type: kv
    primary_key: [pk, sk]
    capacity_mode: on-demand

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
      app_db: { tier: dev }
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

**Switching**: `supeux up --target=local` (or `--target=staging`, etc.). The CLI flag flips environments; the yaml decides what each environment maps to. **No code change** between environments — only env vars injected by the supeux runtime into containers / Lambda environment / etc. All four first-class languages read these env vars identically: `os.environ.get(...)` (Python), `std::env::var(...)` (Rust), `process.env.S3_ENDPOINT_URL` (TS), `os.Getenv("S3_ENDPOINT_URL")` (Go).

**Resource tiering is explicit, not inferred.** Each primitive has a small vocabulary:

| Primitive | Tier / class vocabulary |
|---|---|
| `db.sql` | `tier: dev` · `prod-small` · `prod` · `premium` · `global` |
| `bucket` | `class: standard · intelligent · standard-ia · one-zone-ia · glacier-instant · glacier-flexible · glacier-deep-archive`; `lifecycle:`; `variant: standard · express-one-zone · vectors` |
| `kv` | `capacity_mode: on-demand · provisioned`; `tier: standard · global-tables` |
| `queue` | `kind: standard · fifo` |
| `topic` | `kind: standard · fifo` |
| `cache` (v1.x) | `tier: dev · prod-small · prod-cluster · serverless · memorydb` |

**Env-var injection contract** — language-agnostic. For each resource a `service` `uses:`, supeux derives a canonical env var name and injects it:

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

## 7. Per-language observations that justify the design

### 7a. Rust-specific observations

These are the concrete facts from `mapping_aws_to_rust.md` / `mapping_rust_to_aws.md` / `rust_api_diffs.md` that drive v4's shape:

1. **No first-party Pulumi-Rust SDK** (and no CDK-Rust; CDKtf sunset Dec 10, 2025). Pulumi-in-process strategy excludes Rust users entirely. Terraform emission is therefore *required*, not preferred.
2. **`aws-sdk-rust` supports `endpoint_url`** on every core service. Trivial-band cardinality is ~18 pairs — narrower than the other three first-class languages (~22 each).
3. **`sqlx` + `tokio-postgres` + `sea-orm` are all DSN-driven**. `DATABASE_URL` env-var injection is universal.
4. **Lambda Rust runtime is GA (Nov 2025)** via `lambda_runtime` + `lambda_http`. axum routers deploy as Lambda container-image or standalone — one codebase, two shapes.
5. **Async runtime convergence on Tokio**. AWS SDK, axum, sqlx, apalis, lapin, opensearch, rumqttc — all Tokio.
6. **Cedar is a Rust project**. Verified Permissions uses the same OSS engine as the `cedar-policy` crate.
7. **`object_store` exists** as a multi-cloud trait abstraction for S3/GCS/Azure/local-file.
8. **Shuttle.rs is the native-Rust IaC competitor**. Different audience; no zero-sum competition.

### 7b. TypeScript-specific observations

These are the facts from `mapping_aws_to_typescript.md` / `mapping_typescript_to_aws.md` / `typescript_api_diffs.md` that confirmed the design in v3:

1. **`@aws-sdk/client-*` supports `endpoint`** on every client; modular packaging keeps cold-start small. Trivial-band cardinality ~22 pairs.
2. **Pulumi-TS exists** (unlike Pulumi-Rust), so TS users *could* go Pulumi-TS. supeux's HCL emission still wins for cross-language reviewability and security-team auditability.
3. **CDKtf was sunset and archived Dec 10, 2025** by HashiCorp/IBM, closing the language-native HCL-emitting alternative.
4. **Lambda Node 22 runtime is the mature default**; Bun support is community/experimental; Deno container-baseline-only.
5. **The "one container, two shapes" claim holds across three framework families.** Hono + `hono/aws-lambda`, Express + `serverless-http`, Fastify + `@fastify/aws-lambda` all branch on `process.env.AWS_LAMBDA_RUNTIME_API`.
6. **Cedar has a wasm wrapper** (`@cedar-policy/cedar-wasm`) — the same OSS engine as Verified Permissions, with parity to the Rust Cedar story.
7. **The TS ecosystem hosts ergonomic Tier 1 community libraries.** Vercel AI SDK has the richest provider router; `jose` is the modern audited JWT library; `nodemailer` has been the canonical SMTP library for a decade; `@xenova/transformers` brings Whisper / CLIP / NLLB / sentiment to plain Node via ONNX.
8. **SST is the native-TS IaC competitor**. Different audience; no zero-sum competition.
9. **TS source → JS bundle is user-side**. Lambda Node runtime expects `.js`; users bundle with esbuild / tsc / `bun build` before packaging.
10. **Prisma binary-target gotcha**. Multi-arch Docker builds need explicit `binaryTargets` in `schema.prisma`.

### 7c. Cross-language patterns

Four independent ecosystems agreeing on the same architectural answer is overwhelming evidence of design rightness:

- **Tier 0 cardinality is consistent**: 18–22 pairs across all four languages. Cross-language Trivial coverage is real, not a single-language artifact.
- **Tier 1 pairs map 1:1 across languages** with named community libraries in each (LLM / token-verify / email / STT / vision). No pair shows up as Tier 1 in one language and Tier 0/2 in another at the architectural level.
- **Tier 2 services are language-agnostic** — they're properties of the AWS service (no OSS engine, AWS-internal mechanics), not of the SDK ecosystem.
- **The "one container, two shapes" pattern holds** in every language with first-class Lambda support: `lambda_http` (Rust), `Mangum` (Python), `hono/aws-lambda` / `serverless-http` / `@fastify/aws-lambda` (TS), `aws-lambda-go-api-proxy` (Go).

### 7d. Go-specific observations

These are the new facts from `mapping_aws_to_go.md` / `mapping_go_to_aws.md` / `go_api_diffs.md` that confirm the design in v4:

1. **`aws-sdk-go-v2/service/*` supports per-client `BaseEndpoint`** since v1.16 (~2023). Same shape as Python's `endpoint_url=` and TS's `endpoint` option. Older docs showing `EndpointResolverWithOptions` / `EndpointResolverV2` are pre-2023 and unnecessarily complex for the common case. Trivial-band cardinality ~22 pairs — at parity with Python and TS, above Rust.
2. **Lambda Go runtime via `provided.al2023` is the mature default.** `aws-lambda-go` (the official `lambda.Start` ABI library) compiles to a single static binary placed at `/var/runtime/bootstrap`. Cold-starts are **10–50 ms** — the lowest of the four first-class languages (Rust ~50–100 ms, TS ~100–500 ms, Python ~500–1500 ms). The cold-start objection that drives Python+Node teams off Lambda barely applies to Go.
3. **Static binaries (`CGO_ENABLED=0`) make `FROM scratch` viable.** Container images of ~5–20 MB — the smallest of the four first-class languages. Trade-off: CGO-requiring deps (`gocv` OpenCV, `gosseract` Tesseract, `whisper.cpp` Go bindings) need a glibc/musl base instead of scratch. v4 §12 documents per-row.
4. **"One container, two shapes" holds across four router families.** chi + `aws-lambda-go-api-proxy/chi`, gin + `aws-lambda-go-api-proxy/gin`, echo + `aws-lambda-go-api-proxy/echo`, fiber + `aws-lambda-go-api-proxy/fiber`, plus stdlib `net/http` + `httpadapter` — all branch on `os.Getenv("AWS_LAMBDA_RUNTIME_API")` to deploy the same source as Lambda or standalone server. chi is the closest analogue to Rust's `lambda_http` and TS's Hono (idiomatic + minimal); gin has the largest hiring pool; echo is the performant middle ground. `aws-lambda-go-api-proxy` is the closest parallel to TS's `serverless-http` — a single adapter library with router-specific sub-packages.
5. **Cedar has a native Go implementation** (`cedar-policy/cedar-go`, GA 2024) — *not* a wasm wrapper. Cleaner than TS's `@cedar-policy/cedar-wasm`. AWS Verified Permissions runs the Rust engine server-side; `cedar-go` is the Go-native reimplementation maintained by the Cedar project, with API/policy-grammar parity. Go is one of two first-class languages (with Rust) with a native Cedar engine in-process.
6. **MSK-IAM is mature in Go.** `aws/aws-msk-iam-sasl-signer-go` is battle-tested; both `segmentio/kafka-go` and `confluentinc/confluent-kafka-go` integrate cleanly with the IAM signer. Go's MSK-IAM story is on par with Java's and meaningfully cleaner than TS's (`kafkajs` doesn't natively sign SigV4 — needs `confluent-kafka-javascript` librdkafka FFI fallback). Same posture as Python's mature signer.
7. **Pulumi-Go is mature, and Go is the CLI implementation language.** Per `thesis.md:63`, supeux's own CLI is in Go and Pulumi-Go-as-CLI-internal is the documented next move if HCL expressiveness ever binds. With Go now a first-class user-code language as well (v4 §14.1), Go sits at both ends of supeux. The IaC tooling landscape for Go (cdktf-go sunset Dec 10, 2025; AWS CDK Go in preview emits CloudFormation; Pulumi-Go imperative) leaves no first-party HCL-emitting-from-Go toolchain — same conclusion as the other three languages: supeux fills the gap polyglot-first.
8. **The Go ecosystem hosts two named Tier 1 options per pair for LLM and email.** v4 §4 names both `langchaingo` and `eino` for LLM (LangChain port vs CloudWeGo typed-chain DSL), and both `net/smtp` and `gomail` for email (stdlib vs community wrapper). Other Tier 1 pairs (token verify, STT, vision) name one canonical Go library. The two-option surface reflects genuine ergonomic splits in the Go ecosystem rather than maturity gaps — both options are production-grade.
9. **Go is NATS's home language.** NATS itself is written in Go; the `nats-io/nats.go` client is first-class. Where TS / Python / Rust treat NATS as community, Go users get the most polished NATS integration. (NATS isn't a v4 primitive — too niche — but worth noting for self-host-as-`service` patterns.)
10. **No bundling step.** Go compiles to a single static binary. Unlike TS's esbuild/tsc/`bun build` step before Lambda packaging, Go's `go build -o bootstrap` produces the deployable artifact directly. supeux's "we don't build your container" posture is even simpler for Go users.
11. **`sqlc` codegen requires live schema at build time** — parallel to Rust's `sqlx::query!` macro gotcha. CI must run `sqlc generate` after migrations are added to schema files; many teams commit the generated code so production builds don't need `sqlc` available. `gorm.AutoMigrate` is the dual-mode trap: works locally but production must use `golang-migrate/migrate` or `pressly/goose`.
12. **Graviton arm64 cross-compile is one flag.** `GOOS=linux GOARCH=arm64 go build` produces an arm64 binary on any host without emulation or multi-arch Docker buildx setup. Same source, two architectures. Unlike Prisma's `binaryTargets` per-arch concern in TS, Go users get this for free.

---

## 8. Cloud-only list

Unchanged from v3. Go evidence does not add or remove any `cloud_only` services; the list reflects AWS-side properties, not language-ecosystem properties.

**Cloud-only in v4**:
- API Gateway (REST + WebSocket) — handler-ABI abstraction varies per language; supeux admits this is a per-language `lambda_http`-style concern outside its scope.
- Cognito user lifecycle — `cloud_only` for sign-up / MFA / hosted UI / custom-attribute admin. Token *verification* stays Tier 1.
- Step Functions multi-service workflows — `cloud_only`; single-Lambda-task workflows can be tested against `aws-stepfunctions-local`.
- CloudFront, Lambda@Edge, CloudFront Functions, Global Accelerator.
- S3 Express One Zone, S3 Vectors, S3 Object Lambda, S3 Select.
- Aurora DSQL, DAX, Neptune Analytics, Kendra.
- Bedrock Knowledge Bases / Agents / Guardrails.
- SNS Mobile Push (APNs/FCM).
- CloudWatch Synthetics / RUM / Application Signals.
- IAM enforcement (LocalStack stubs the API, not the enforcement).
- IoT Device Management / Defender / Analytics / SiteWise / TwinMaker / FleetWise.
- SageMaker JumpStart / Canvas.
- Forecast (deprecated) / Personalize.
- Step Functions Distributed Map.

**Net `cloud_only`**: ~20 services — the list is a feature, not a limitation. It tells users which AWS services lack honest local emulation, so they know where to draw the test-against-cloud boundary.

IR shape unchanged:

```yaml
cloud_only:
  llm:
    type: bedrock.llm
    model: anthropic.claude-opus-4-7-20260416-v1:0
  cdn:
    type: cloudfront
    origins: [uploads]
```

---

## 9. v1 PoC scope — first milestone toward §2

What ships first. Everything in §2 that isn't here is deferred to v1.1+, with the roadmap order in §10 tracking the gap between v1 and the end-state vision.

Hard constraints to keep the v1 PoC shippable in weeks, not months:

- **5 primitives**: `service`, `bucket`, `queue`, `db.sql`, `secret`.
- **2 `service` shapes**: `long-running` (Fargate / App Runner) and `function` (Lambda container-image). Under v4's no-SDK design, the `function` shape is *not* a per-language handler abstraction — it's a different Terraform deploy target for the same container. The user's code wraps itself in `lambda_http` (Rust), `Mangum` (Python), `hono/aws-lambda` / `serverless-http` / `@fastify/aws-lambda` (TS), or `aws-lambda-go-api-proxy` (Go) inside the container; supeux generates the Lambda Terraform instead of the Fargate Terraform.
- **Triggers in v1**: `http` (Function URL for `function`-shape; ALB for `long-running`-shape) and `queue` (SQS event source mapping for `function`; long-poll consumer in user code for `long-running`).
- **Deferred to v1.1**: `topic`, `kv`, `static_site`, `cron` triggers, `service` `shape: batch`, API Gateway.
- **Targets**: `local` (docker-compose) and `aws-dev` (Terraform → AWS).
- **Languages**: language-neutral CLI. Reference apps: **Python (FastAPI + Mangum), Rust (axum + `lambda_http`), TypeScript (Hono + `hono/aws-lambda`), Go (chi + `aws-lambda-go-api-proxy/chi`)**. **Four reference apps in v1** validate the cross-language claim at full first-class scope out the gate (resolves §14.1 + §14.4). The cost is a fourth CI matrix entry plus Go-specific verification rows (Graviton arm64 cross-compile, `AWS_LAMBDA_RUNTIME_API` branch dispatch, `pgx` DSN swap, `BaseEndpoint` consistency across services); the benefit is "first-class" being deliverable-backed for all four languages from day one.
- **IaC**: emit OpenTofu HCL via `supeux compile`; `supeux up` runs `tofu apply` on the emitted spec. Emit and apply are separate commands by design (§5). State backend = S3 + DynamoDB lock.
- **CLI**: `supeux init | compile | diff | up | down | spec | check` — no live-reload, no console UI, no debugger proxy. The `compile` verb (HCL/compose emission) is distinct from `spec` (read-only IR dump).

Explicitly **not in scope** for v1:
- GCP / Azure providers.
- Cognito or any auth primitive (use `cloud_only` for now).
- API Gateway REST / WebSocket, AppSync, Step Functions, Bedrock, SageMaker (cloud-only for v1; users wire SDKs directly).
- Live debugging proxy.
- Multi-region.
- Console UI.

**Verification checklist** (when v1 is built):
- [ ] `supeux init` creates state backend.
- [ ] **`supeux compile --target=local`** emits `docker-compose.generated.yaml`; reviewable on disk before any container starts.
- [ ] **`supeux compile --target=aws-dev`** emits `infra/aws-dev/generated/*.tf`; reviewable on disk before any `tofu` invocation. Re-running `compile` overwrites only files in `generated/`; sibling `.tf` files (including `backend.tf`) are preserved.
- [ ] `supeux up --target=local` reads the emitted compose file and runs `docker compose up`. Reference apps run against it — including `function`-shape services running as long-lived servers locally.
- [ ] `supeux up --target=aws-dev` reads the emitted HCL and runs `tofu init && tofu apply`. The same container images deploy as Fargate services AND as Lambda container-image functions.
- [ ] `supeux up` errors clearly if no spec has been emitted for the target.
- [ ] `supeux up --regenerate` re-runs `supeux compile` then applies (opt-in one-shot flow).
- [ ] `supeux diff --target=aws-dev` runs `tofu plan` against the emitted spec; pretty-prints the diff.
- [ ] Switching `--target` between runs is fast (state cached locally; emitted HCL incremental).
- [ ] IAM policies on AWS are auto-derived from `uses:` declarations — for both Fargate task roles AND Lambda execution roles. Visible in the emitted `iam.tf` before apply.
- [ ] HTTP invocation works for `long-running` (ALB / App Runner) and `function` (Lambda Function URL).
- [ ] Queue trigger works for `function` (SQS event source mapping) end-to-end.
- [ ] `supeux spec --json` prints the IR (phases 1–3 only; distinct from `supeux compile`).
- [ ] `supeux check` warns on env-var/uses mismatches.
- [ ] Reference apps' container builds work without supeux installed.
- [ ] Cloud-only resources error usefully when the user tries `--target=local`.
- [ ] **TS reference app verification**: Prisma `binaryTargets` includes Fargate arch; `process.env.AWS_LAMBDA_RUNTIME_API` branch dispatches correctly between Lambda handler export and standalone listen.
- [ ] **Go reference app verification**: Graviton arm64 cross-compile via `GOOS=linux GOARCH=arm64 CGO_ENABLED=0 go build`; `os.Getenv("AWS_LAMBDA_RUNTIME_API")` branch dispatches between `lambda.Start(chiadapter.New(r).ProxyWithContext)` and `http.ListenAndServe(":8080", r)`; `pgx` (via `database/sql` adapter) works under `DATABASE_URL` DSN swap against postgres container and RDS Postgres; `aws-sdk-go-v2/service/{s3,sqs,secretsmanager}` `BaseEndpoint` swap works against minio / ElasticMQ / localstack and against real AWS; `FROM scratch` Lambda container image builds and invokes cleanly.

---

## 10. Roadmap from v1 to the §2 end-state

Ordered by what unblocks the most user value first. Each milestone is independently shippable.

| Milestone | Adds | Why this order |
|---|---|---|
| **v1** | 5 primitives, 2 shapes, 2 triggers, AWS only, **Python + Rust + TypeScript + Go** reference apps. All apps use Tier 1 community libraries (rig-core, litellm, Vercel AI SDK, `langchaingo` / `eino`) | Validates the no-SDK + Terraform-emission + two-shapes thesis across four languages out the gate. |
| **v1.1** | `topic`, `kv` primitives; `cron` triggers; `supeux logs` + `supeux exec`; **Tier 1 gap survey** across all four first-class languages | Closes the "all 8 stateful primitives" gap. Decides whether `supeux-adapters-*` ships any code. (Note: Go reference app no longer pending — landed in v1.) |
| **v1.2** | `static_site` primitive (S3 + CloudFront / nginx); `bucket-event` trigger; `supeux preview --pr=N`; `supeux-adapters-*` released for proven gaps only | First step into edge/CDN concerns; PR-preview deploys are the killer DX feature. |
| **v1.3** | `shape: batch`; API Gateway HTTP routing layer; OTel + X-Ray default wiring | Closes the trigger/edge story and the observability story for cloud. |
| **v2** | `terraform-module` escape hatch primitive; cloud_only registry with documented SDK snippets; GHA workflow templates for non-trivial pipelines | Lets supeux be useful for AWS features supeux itself doesn't wrap. |
| **v2.x** | GCP and Azure provider emission; same primitives | Validates the cloud-agnostic IR claim. |
| **deferred indefinitely** | Console UI; live-reload debugging proxy; EKS target; multi-account governance; Kubernetes-shape services | Each is its own product. |

---

## 11. Risks / honest scope boundary

- **No deploy-time SDK = no compile-time IAM/policy inference.** Mitigation: `supeux spec --check` greps source files for env-var references; warns on mismatches. For Tier 1 services, the recommended community library's typed API provides the type safety supeux's deploy layer doesn't.
- **Lambda inclusion means the user must write the handler-ABI wrapper themselves.** supeux generates the Lambda Terraform and injects env vars; the user's code has to be `lambda_http`-shaped (Rust), `Mangum`-shaped (Python), `hono/aws-lambda` / `serverless-http` / `@fastify/aws-lambda`-shaped (TS), or `aws-lambda-go-api-proxy`-shaped (Go). For `long-running` services there's no such wrapper. Reference apps demonstrate both. This is the natural seam under no-SDK.
- **Lambda cold starts vary per language.** Go container-image Lambdas start in 10–50 ms (lowest); Rust ~50–100 ms; TS (Node 22) ~100–500 ms; Python ~500–1500 ms; Java in seconds. supeux defaults are runtime-agnostic; users tune memory / provisioned concurrency / SnapStart via `overrides:` per target.
- **TS runtime variance.** Node 22 is the official Lambda runtime; Bun on Lambda is community/experimental; Deno is container-baseline-only. supeux defaults to Node 22; Bun and Deno work fine on Fargate as "any container with a Dockerfile."
- **Go CGO trade-off.** `CGO_ENABLED=0` enables the smallest images (`FROM scratch`) and the fastest cold-starts, but locks out CGO-requiring deps (`gocv` OpenCV, `gosseract` Tesseract, `whisper.cpp` Go bindings, `mattn/go-sqlite3`). Reference apps default to CGO-free; v4 §12 documents the trade-off per affected service.
- **Go `sqlc` build-time schema dependency** parallels Rust's `sqlx::query!` macro gotcha. CI must run `sqlc generate` after schema migrations; most teams commit the generated code.
- **Go `gorm.AutoMigrate` dual-mode trap.** Works for local dev; production migrations must use `golang-migrate/migrate` or `pressly/goose`. Documented in `mapping_go_to_aws.md`; reference apps default to explicit migration tooling.
- **Function URL only in v1; no API Gateway.** Function URLs handle the 90% case. API Gateway routing layers are deferred to v1.3.
- **Terraform state management is opinionated.** Users edit `backend.tf` for different backends. Documented.
- **No live debugging proxy.** Containers + ports + IDE debugger is the answer.
- **`cloud_only` list (~20).** Honest scope boundary, not a regression.
- **Risk that Terraform emission limits expressiveness vs Pulumi.** HCL has weaker programmability than Pulumi's TS/Go SDKs. For the 5-primitive scope, this is fine. If/when that hurts, evaluate Pulumi-Go-as-CLI-internal — Go's role as both user-code language and CLI implementation language (v4 §7d observation 7) makes this transition lower-friction than v3 assumed.

---

## 12. Risk list — divergence gotchas in "easy" mappings

The Trivial-band pairs are not 100% identical between cloud and local. Each carries a known divergence; users must be told. Inherits v3 entries and adds Go-specific rows at the bottom.

| Pair | Gotcha | Mitigation |
|---|---|---|
| S3 ↔ minio | Strong-read-after-write semantics differ under concurrent writes / degraded modes. | Document; for prod assumptions lean on S3 docs, not local behavior. |
| S3 ↔ minio | Lifecycle policies use different DSLs. | supeux generates S3 lifecycle for AWS; emits best-effort minio command locally. |
| S3 ↔ minio (Rust-specific) | `aws-sdk-s3` requires `force_path_style(true)` against minio; AWS rejects it. | Set conditionally on `S3_ENDPOINT_URL` presence. |
| S3 ↔ minio (TS-specific) | `@aws-sdk/client-s3` requires `forcePathStyle: true` against minio; AWS rejects it. | Set conditionally on `S3_ENDPOINT_URL` presence. |
| S3 ↔ minio (Go-specific) | `aws-sdk-go-v2/service/s3` requires `o.UsePathStyle = true` against minio; AWS rejects it. | Set conditionally on `S3_ENDPOINT_URL` presence (same pattern as Rust + TS). |
| DynamoDB ↔ dynamodb-local | Streams partial; TTL deletes happen on best-effort timing. | Don't write code that depends on TTL timing for correctness. |
| SQS ↔ ElasticMQ | No per-account throttle quotas locally; `ThrottlingException` never fires in dev. | Chaos-test throttle handling in staging. |
| SQS FIFO ↔ ElasticMQ FIFO | Dedup window precision differs by ms. | Don't rely on exact 5-min window in tests. |
| Postgres (RDS/Aurora) ↔ postgres container | Aurora-specific extensions don't exist vanilla; vanilla extensions Aurora hasn't approved fail in Aurora. | supeux warns at IR validation if an extension isn't on Aurora's supported list. |
| Postgres (`sqlx`, Rust-specific) | `sqlx::query!` needs live DB during `cargo build`. | CI spins up postgres before build, or `sqlx prepare` for offline metadata. |
| Postgres (Prisma, TS-specific) | Prisma migration-engine binary is per-platform; multi-arch Fargate builds need `binaryTargets` set correctly. | Set `binaryTargets = ["native", "linux-musl-arm64-openssl-3.0.x", "linux-musl-openssl-3.0.x"]` in `schema.prisma`. |
| Postgres (`sqlc`, Go-specific) | `sqlc generate` reads schema files at build time; codegen requires schema to match queries. | CI runs `sqlc generate` after migrations; commit the generated code to avoid prod-build dependency on sqlc. |
| Postgres (`gorm`, Go-specific) | `db.AutoMigrate(&User{})` silently diverges from managed migration history. | Use `golang-migrate/migrate`, `pressly/goose`, or `atlasgo/atlas` for production migrations. |
| Postgres (`pgx`, Go-specific) | `pgx` exposes two interfaces (native + `database/sql` adapter). Native is faster; adapter is portable. Mixing both within one repo is confusing. | Pick one per project. Reference app uses `database/sql` adapter for portability. |
| RDS minor-version auto-upgrades | Maintenance windows break pinned driver-extension versions. | Use Aurora (broader compat) or disable auto-minor-upgrade. |
| DocumentDB ↔ mongo | DocumentDB lacks ~30% of aggregation operators. | Test critical aggregations against DocumentDB in CI, not just local mongo. |
| ElastiCache cluster-mode ↔ single redis | Cross-slot pipelines fail on cluster. | Use `redis-cluster` locally if your code uses cluster mode in prod. |
| OpenSearch ↔ opensearch image | UltraWarm tier behaviors don't reproduce; ML plugins version-drift. | Pin OpenSearch versions to match. |
| Kinesis ↔ localstack | No mature TS KCL port; Go KCL port exists but coordination behavior is local-hostile. | Test producer locally; test consumer at scale against real Kinesis. |
| MSK with IAM auth (Rust-specific) | `aws-msk-iam-sasl-signer-rust` is less battle-tested than Java/Python/Go. | Prefer SCRAM-SHA-512 or mTLS for MSK from Rust. |
| MSK with IAM auth (TS-specific) | `kafkajs` doesn't sign SigV4 natively. | Use `confluent-kafka-javascript` (librdkafka FFI, 2024+ GA) for MSK-IAM, or prefer SCRAM-SHA-512 / mTLS. |
| MSK with IAM auth (Go-specific) | **No gotcha — `aws/aws-msk-iam-sasl-signer-go` is mature.** | Use `segmentio/kafka-go` or `confluentinc/confluent-kafka-go`; both integrate cleanly. Go is the cleanest first-class language for MSK-IAM. |
| SES ↔ mailhog | SES throttles on reputation + warmup; mailhog never throttles. | Don't load-test through SES sandbox; request prod access first. |
| Step Functions ↔ aws-stepfunctions-local | Distributed Map, Express semantics, intrinsic-function library drift with local container version. | Pin local container version; supeux flags ASL features not supported by pinned version. |
| IoT Core MQTT ↔ mosquitto | IoT Core mandates mTLS; mosquitto accepts plaintext. | Run mosquitto with TLS in CI to catch handshake bugs. |
| Bun ↔ Lambda (TS-specific) | Bun-on-Lambda is community/experimental; not officially supported by AWS. | Default to Node 22 container-image for Lambda; Bun is user-opt-in for Fargate. |
| TS source → JS bundle (TS-specific) | Lambda Node runtime expects `.js`; users must bundle (esbuild / tsc / `bun build`) before packaging. | Document the bundle step; supeux does NOT bundle for the user. |
| Go static binary vs CGO (Go-specific) | `CGO_ENABLED=0` is required for `FROM scratch` Lambda images and the smallest Fargate images; CGO-requiring deps (`gocv`, `gosseract`, `whisper.cpp` bindings, `mattn/go-sqlite3`) break the static-binary path. | Default reference app to `CGO_ENABLED=0`; for CGO-requiring deps switch to a glibc/musl base (`gcr.io/distroless/cc-debian12` or `golang:1.22` runtime). |
| Go Graviton arm64 (Go-specific) | Multi-arch Lambda images require building both architectures. | One-flag cross-compile: `GOOS=linux GOARCH=arm64 CGO_ENABLED=0 go build -o bootstrap`. No buildx emulation needed. |
| Go `aws-sdk-go-v2` endpoint resolution (Go-specific) | Pre-v1.16 (~2023) docs show `EndpointResolverWithOptions` / `EndpointResolverV2`; modern path is per-client `o.BaseEndpoint = aws.String(...)`. | Use `BaseEndpoint`; ignore the older resolver machinery unless multi-region custom routing is needed. |

---

## 13. One-page summary

> **End-state vision (§2)**: supeux is a containers-first deploy tool. One yaml manifest, any first-class language (Python, Rust, TypeScript, Go — all four with full mapping/api_diffs evidence in v4), any cloud (AWS first; GCP/Azure reachable later). User code is unmodified containers wired to env-var-driven endpoints; supeux generates docker-compose locally, Terraform/OpenTofu HCL for cloud, and GHA workflows for CI. No SDK, no runtime coupling, no language lock-in. 8 primitives (`service`, `bucket`, `queue`, `topic`, `kv`, `db.sql`, `secret`, `static_site`), 3 `service` shapes (`long-running`, `function`, `batch`), 5 trigger types, ~20 cloud-only resource types, hand-written-`.tf` extension model.
>
> **What v4 adds vs v3** (§1, §4, §7d, §12): Go evidence — four independent ecosystems agreeing on the same architectural answer. Go Tier 0/1/2 headcounts (~22 / ~5 / ~15) sit at parity with Python and TS; the design holds. Two named Tier 1 community options per Go pair for LLM (`langchaingo` *or* `eino`) and email (`net/smtp` *or* `gomail`); one canonical option for token verify (`golang-jwt` + `keyfunc`), STT (`whisper.cpp` Go bindings), and vision (`onnxruntime_go` / `gocv`). cdktf-go's Dec 10 2025 archive closes the language-native HCL-emit alternative for Go users — same posture as TS. Go's CLI-implementation role per `thesis.md:63` is now doubly load-bearing with Go as a first-class user-code language. Per-Go gotchas (Graviton cross-compile is trivial; CGO trade-off vs `FROM scratch`; `sqlc` build-time schema dependency; `gorm.AutoMigrate` dual-mode trap; `pgx` two-interface choice; `aws-sdk-go-v2` `BaseEndpoint` is the modern path) added to §12.
>
> **Architectural call from v3, confirmed by v4** (§3–§5): SoC-containers collapses `function` into a *shape* of `service`. Deploy-time-SDK dissolves (yaml + env-var injection is sufficient for the ~22 Trivial pairs in each first-class language). Tier 1 hard pairs use mature community libraries (rig / litellm / Vercel AI SDK / langchaingo or eino for LLMs; jsonwebtoken / authlib / jose / golang-jwt+keyfunc for token verify; lettre / smtplib / nodemailer / net/smtp or gomail for email). Terraform/OpenTofu HCL emission for IaC — language-neutral, reviewable diffs in CI, no per-language runtime coupling. Go cold-start of 10–50 ms is the lowest of the four first-class languages; Go static binaries yield the smallest container images; Cedar has a native Go implementation; MSK-IAM is mature in Go.
>
> **The IR** (§6): one `supeux.yaml` projecting to three artifacts: `docker-compose.generated.yaml` (local), `infra/<target>/*.tf` (cloud), `.github/workflows/deploy-<target>.yml` (CI). Switching is a single `--target=` flag. No code change between environments — only env vars injected by the supeux runtime. Go reads them identically: `os.Getenv("S3_ENDPOINT_URL")`, `os.Getenv("DATABASE_URL")`, etc.
>
> **Two-step flow, not one-shot** (§5): `supeux compile --target=<name>` emits HCL/compose into `infra/<target>/generated/` as a reviewable on-disk artifact; `supeux up --target=<name>` runs `tofu apply` (or `docker compose up`) against that artifact. Emit and apply are separate commands by design — that separation is what operationalizes the thesis principle "Auditable IaC artifacts." `supeux up --regenerate` covers the one-shot ergonomics opt-in.
>
> **v1 ships first** (§9, §10): **5 primitives** (`service`, `bucket`, `queue`, `db.sql`, `secret`) with **two shapes** (`long-running`, `function`), **two triggers** (`http`, `queue`), **two targets** (local + aws-dev), **AWS only**, and **Python + Rust + TypeScript + Go reference apps**. v1 is the *smallest scope that exercises every novel design decision* (no-SDK, two shapes, Terraform emission, env-var injection) across all four first-class languages from day one; every subsequent milestone adds coverage without re-deciding architecture.
>
> **Honest boundary** (§8, §11): the `cloud_only` list is ~20 services — API Gateway, Cognito user-lifecycle, Step Functions multi-service workflows joined it because removing the SDK removes the abstractions that were hiding those lock-ins. Treat the list as a feature, not a limitation. **Cross-language headcounts** (Trivial / Hard / Intractable): Python ~22 / ~5 / ~15; Rust ~18 / ~3 / ~18; TypeScript ~22 / ~5 / ~15; **Go ~22 / ~5 / ~15**. The design holds across four first-class languages.

---

## 14. Thesis edits proposed (decision items for user)

These were surfaced for the user's review per "If anything in thesis isn't clear or looks like it needs change, surface for user decision." **All sub-items have been dispositioned by user as of 2026-05-16** — see the per-sub-item disposition notes below. Thesis.md will be edited in line with §14.1, §14.2, and §14.3; §14.4 is reflected in §9 / §10 / §13 above; §14.5–§14.7 are NO-OPs (inherited from v3 dispositions). v4 itself is self-consistent with the dispositions.

### 14.1 [`thesis.md:62`](thesis.md) — Language coverage phrasing

**Disposition (2026-05-16): ACCEPTED — promote on evidence.** Thesis.md:62 reads "Python, Rust, TypeScript, and Go first-class. Container baseline (any language with a Dockerfile) is free." The evidence-based threshold (Go now has full mapping/api_diffs evidence at parity with Python, Rust, and TypeScript) is met; the deliverable-based threshold (v1 PoC ships four reference apps — see §14.4 disposition) is also met, removing both counter-arguments.

**Current (pre-edit)**: "Python, Rust, and TypeScript first-class; Go next. Container baseline (any language with a Dockerfile) is free."

**Applied**: "Python, Rust, TypeScript, and Go first-class. Container baseline (any language with a Dockerfile) is free."

**Rationale**: Go now has full mapping/api_diffs evidence at parity with Python, Rust, and TypeScript. The "first-class" / "next" distinction in thesis applies to *current evaluation*; promoting Go to first-class matches the evidence base. With §14.4 also accepted, the four-language first-class claim is deliverable-backed at v1 ship.

### 14.2 [`thesis.md:66`](thesis.md) — Tier 1 inline example libraries

**Disposition (2026-05-16): ACCEPTED — extend inline list to four languages.** Thesis.md:66 is expanded with Go libraries inline. The counter-argument (library names rot; inline list grows long) is acknowledged but outweighed by the readability benefit of four-language parity in the inline list. If a library name later rots, the inline mention is one edit away from the §4 table that already lists the full set.

**Current (pre-edit)**: "rig-core (Rust), litellm (Python), and Vercel AI SDK (TypeScript) for LLM providers including Bedrock + Ollama; jsonwebtoken / authlib / `jose` for Cognito vs local JWT verify; lettre / smtplib / nodemailer for SES vs SMTP catchers."

**Applied**: Go libraries woven into the inline list — `langchaingo` / `eino` joins rig-core/litellm/Vercel AI SDK for LLMs; `golang-jwt` + `keyfunc` joins jsonwebtoken/authlib/`jose` for token verify; `net/smtp` / `gomail` joins lettre/smtplib/nodemailer for SMTP. (Where Go has two named options per pair, both are shown inline; this is honest about the Go-ecosystem ergonomic split per §4.)

**Rationale**: thesis is currently load-bearing on three-language examples for Tier 1. Adding Go examples brings the inline list to parity with the §4 table in v4.

### 14.3 [`thesis.md:81-88`](thesis.md) — Companion documents list

**Disposition (2026-05-16): ACCEPTED.** Go evidence files added; v4 promoted to current long-form derivation; v3 demoted to historical record alongside v2 and v1.

**Current (pre-edit)** lists Python + Rust + TypeScript evidence files, `aws_service_groups.md`, `cloud_providers.md`, `supeux_abstraction_v1.md`, `supeux_abstraction_v2.md`, `supeux_abstraction_v3.md`.

**Applied**: added `mapping_aws_to_go.md`, `mapping_go_to_aws.md`, `go_api_diffs.md`. Added `supeux_abstraction_v4.md` as "long-form derivation that produced this thesis (four-language re-derivation; supersedes v3)". Demoted v3 to historical record line alongside v2 and v1.

### 14.4 v1 PoC reference-app set (mostly a v4 §9 question, surfaced here because it touches thesis evidence)

**Disposition (2026-05-16): ACCEPTED option B.** v1 ships four reference apps (Python + Rust + TypeScript + Go). The cross-language claim is validated by running code from day one across the full first-class set; the §14.1 "first-class" promotion becomes deliverable-backed in the same release. Cost (fourth CI matrix entry, Go-specific verification rows: Graviton cross-compile, `AWS_LAMBDA_RUNTIME_API` branch dispatch, `pgx` DSN swap, `BaseEndpoint` consistency, `FROM scratch` Lambda image) is accepted. v4 §9 and §10 updated to reflect; v4 §13 one-page summary updated.

**Current (pre-decision)** (v3 §9): v1 ships Python + Rust + TypeScript reference apps; Go deferred to v1.1.

**Option A (rejected)**: Python + Rust + TS in v1, Go in v1.1. Smaller v1 surface, fewer CI matrix entries.
**Option B (selected)**: ship four reference apps in v1. Go evidence proves the design holds across four languages; "first-class" in thesis (§14.1) becomes deliverable-backed for all four out the gate.

### 14.5 [`supeux_abstraction_v3.md:70`](supeux_abstraction_v3.md) end-state language coverage

**Disposition (2026-05-16): NO-OP.** v3 already says "First-class … Python, Rust, TypeScript/Node, Go" at the end-state level. Consistent with v4; v3 retained as historical record.

### 14.6 Bun/Deno positioning in thesis

**Disposition (2026-05-16): NO-OP.** Inherited from v3 §14.6. Thesis stays runtime-agnostic. Bun/Deno positioning is a TS-internal concern, captured in v3/v4 §7b / §11 / §12 and in `mapping_typescript_to_aws.md`. The gap is intentional.

### 14.7 Two-step emit-then-apply wording in thesis and README

**Disposition (2026-05-16): NO-OP (inherited from v3 §14.7).** Already resolved in v3; thesis.md:73 and README.md:5 already use the two-step `supeux compile` → `supeux up` framing. No further edit needed for v4.

---

*Conceptual home: `thesis.md`. Historical record: `supeux_abstraction_v1.md` (Python-decorator-SDK framing), `supeux_abstraction_v2.md` (containers-first re-derivation with Python + Rust), `supeux_abstraction_v3.md` (three-language re-derivation with Python + Rust + TypeScript). v4 supersedes v3 by re-deriving with four first-class languages and confirming the design holds.*
