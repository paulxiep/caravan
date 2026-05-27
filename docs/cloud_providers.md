# Cloud Providers — primitive mapping and divergences

> ℹ️ **REFERENCE DATA — snapshot 2026-05-16; GCP / Azure are out of PoC scope.** Useful evidence for the thesis's "cloud-agnostic primitives" claim, but PoC is AWS-only per [`development_plan.md`](development_plan.md). Re-verify provider specifics before quoting in any current decision.

> **Snapshot date: 2026-05-16.** Read `thesis.md` first. This doc is evidence behind the thesis's commitment to cloud-agnostic primitives — that the caravan IR survives contact with non-AWS providers.

## Why this doc exists

The thesis commits to **cloud-agnostic primitives by name** (`bucket`, not `s3`). That commitment is cheap to write down and expensive to validate; this doc validates it.

The eight primitives — `service`, `bucket`, `queue`, `topic`, `kv`, `db.sql`, `secret`, `static_site` — plus the three `service.shape` values (`long-running`, `function`, `batch`) and the five trigger types (`http`, `queue`, `topic`, `cron`, `bucket-event`) need to land on every major cloud without renaming the IR. If a primitive doesn't translate, the schema is leaking AWS assumptions and needs revision **now**, before users build yaml around it.

This doc:
1. Lists the providers in scope.
2. Maps each primitive to its concrete service per provider.
3. Calls out the *divergences* — where a provider's surface area doesn't line up with AWS's.
4. Records the implications for the IR.

It does **not** repeat the per-provider cost / latency / scale catalogs — those live in [`aws_service_groups.md`](aws_service_groups.md), [`gcp_service_groups.md`](gcp_service_groups.md), and [`azure_service_groups.md`](azure_service_groups.md).

## Providers in scope

| Provider | Status | Why |
|---|---|---|
| AWS | Baseline | Implementation starts here. `aws_service_groups.md` is the canonical catalog. |
| GCP | First-class target post-AWS | Largest non-AWS share among target users. `gcp_service_groups.md` is the companion catalog (Cloud Run / GCS / Pub/Sub / Cloud SQL / Firestore / Secret Manager). |
| Azure | First-class target post-AWS | Enterprise dominant. `azure_service_groups.md` is the companion catalog (Container Apps / Blob / Service Bus / Postgres Flexible Server / Cosmos / Key Vault). |

**Out of primary mapping** (worth a one-liner each, not full coverage):

- **Cloudflare** — edge-first. No `service.shape: long-running` analog (Workers are function-shaped only); R2 covers `bucket`; Queues cover `queue`; D1 covers a narrow `db.sql` slice. Reachable only if caravan gains a function-only deploy mode and accepts shape downgrades.
- **Oracle Cloud (OCI)** — primitive coverage exists (Functions / Object Storage / Streaming / ATP / Vault) but no Tier-1 user demand in scope. Reachable by adding HCL provider templates; no schema risk.
- **DigitalOcean** — subset of AWS primitives at smaller scale. App Platform = `service.shape: long-running`; Spaces = `bucket`; Managed Postgres = `db.sql`. No `function` shape, no managed `kv`. Reachable for small-scale targets; not a primary lane.

## Primitive mapping

| Primitive | AWS | GCP | Azure |
|---|---|---|---|
| `service` (long-running) | Fargate / App Runner | Cloud Run | Container Apps |
| `service` (function) | Lambda (container image) | Cloud Functions gen2 / Cloud Run | Azure Functions / Container Apps |
| `service` (batch) | AWS Batch / Fargate one-off | Cloud Run Jobs / GCP Batch | Container Apps Jobs / Azure Batch |
| `bucket` | S3 | GCS (Cloud Storage) | Blob Storage |
| `queue` | SQS | Pub/Sub (subscription side) | Service Bus Queue / Storage Queue |
| `topic` | SNS | Pub/Sub (topic side) | Service Bus Topic / Event Grid |
| `kv` | DynamoDB | Firestore (Native mode) / Bigtable | Cosmos DB (SQL API) |
| `db.sql` | RDS / Aurora | Cloud SQL / AlloyDB | Postgres Flexible Server / Azure SQL DB |
| `secret` | Secrets Manager / SSM Parameter Store | Secret Manager | Key Vault |
| `static_site` | S3 + CloudFront | GCS + Cloud CDN | Blob + Front Door / Static Web Apps |

### Trigger mapping

| Trigger | AWS | GCP | Azure |
|---|---|---|---|
| `http` | Function URL / ALB / API Gateway | Cloud Run URL / API Gateway | Container Apps ingress / APIM |
| `queue` | SQS event source mapping | Pub/Sub push to Cloud Run | Service Bus trigger |
| `topic` | SNS → Lambda subscription | Pub/Sub push | Event Grid subscription |
| `cron` | EventBridge Scheduler | Cloud Scheduler | Logic Apps / Azure Functions Timer |
| `bucket-event` | S3 event notifications | GCS Pub/Sub notifications | Event Grid (Blob source) |

## What each provider does differently

### GCP

- **Pub/Sub conflates queue and topic.** A Pub/Sub *topic* is the publisher side; a *subscription* is the consumer side. There is no separate "queue" service — what AWS splits into SNS+SQS, GCP fuses. caravan still emits both `queue` and `topic` as IR primitives; codegen maps both onto Pub/Sub (a `queue` becomes a topic + one subscription with no fan-out; a `topic` becomes a topic with N subscriptions). The user-facing IR doesn't bend.
- **Cloud Run blurs `long-running` and `function`.** Cloud Run scales to zero and bills per-request like Lambda, but also runs long-lived containers like Fargate. The `service.shape` distinction still drives codegen differences (concurrency settings, min-instances, request-deadline) but the underlying resource type is the same. This is cleaner than AWS, not messier.
- **Firestore's secondary-index story is weaker than DynamoDB's GSIs.** Firestore (Native mode) supports composite indexes but they're declared up-front per query, not materialized as independent tables. For `kv` workloads that rely heavily on GSIs, Bigtable is the alternative — but Bigtable is row-keyed only, no secondary indexes at all. Neither is a drop-in for DynamoDB's pattern.
- **No first-class Aurora DSQL equivalent.** AlloyDB is GCP's Postgres-with-analytics offering, but the multi-region serverless Postgres shape that DSQL targets has no GCP analog today.
- **Cloud Functions gen2 is built on Cloud Run.** Treating `service.shape: function` as "deploy to Cloud Run with function-style settings" is the cleanest mapping; the standalone Cloud Functions surface is largely a thin wrapper now.

### Azure

- **Service Bus and Event Grid split queue/topic differently from AWS.** Service Bus has both queues and topics with ordered, sessioned, transactional semantics — closer to SQS+SNS fused with enterprise messaging features. Event Grid is a separate fan-out event router that doesn't queue. caravan maps `queue` → Service Bus Queue (or Storage Queue for cheap workloads) and `topic` → Service Bus Topic; Event Grid surfaces only for `bucket-event` triggers.
- **Container Apps Jobs covers both function and batch shapes.** Azure Functions exists but for non-trigger-bound function-shape compute, Container Apps Jobs is the more uniform target. The `function` vs `batch` distinction in the IR still drives different codegen (event-triggered vs one-shot scheduled).
- **Cosmos DB is multi-API.** Cosmos exposes SQL, MongoDB, Cassandra, Gremlin, and Table APIs over the same engine. The primitive mapping uses the **SQL API** for `kv` parity — it's the closest to DynamoDB's item/partition model. Users wanting Mongo-shaped access pick that API in the yaml at codegen time, not in the IR.
- **Postgres Flexible Server vs Azure SQL DB.** `db.sql: tier: …` maps to Postgres Flexible Server by default (cross-cloud Postgres parity); Azure SQL DB is reachable via `variant:` for users who want T-SQL.

### AWS (for contrast)

- **Only provider with a clean queue/topic split.** SQS and SNS are independent services. This is the IR's native shape; GCP and Azure both bend.
- **DynamoDB has LSI/GSI + the strongest single-digit-ms p99 story.** Firestore and Cosmos have indexes but the pattern differs. The IR's `kv` primitive is shaped by DynamoDB's affordances — secondary access patterns are first-class.
- **Aurora DSQL covers a niche no other provider matches.** Multi-region serverless Postgres with strong consistency. The IR exposes this as `db.sql: tier: global`; on GCP/Azure that tier currently has no clean target.
- **Lambda has the longest cold-start tail among managed function offerings.** Cloud Run and Container Apps both warm-start container images faster. The `function` shape's cold-start behavior is provider-specific; users picking `function` on AWS accept the cold-start, on GCP/Azure get a better tail by default.

## Implications for the IR schema

The mapping survives. Specifically:

- **Primitive names stay AWS-agnostic.** Already true in the thesis: `bucket`, `queue`, `topic`, `kv`, `db.sql`. No rename needed.
- **`queue` and `topic` stay separate primitives** even though GCP fuses them into Pub/Sub. caravan codegen handles the fusion; users keep two-primitive thinking. Collapsing them in the IR would penalize the cleanest mapping (AWS, Azure) to flatter the messiest one (GCP).
- **`service.shape: function` vs `long-running` stays meaningful** even where the provider (Cloud Run, Container Apps) blurs them. caravan emits provider-appropriate resource types per shape — same yaml, different HCL.
- **`variant:` is the escape hatch for intra-primitive divergence.** When a primitive name covers multiple non-equivalent resource types (S3 Express vs S3 Vectors vs S3 Standard on AWS; Postgres Flexible Server vs Azure SQL DB on Azure; Firestore vs Bigtable on GCP), `variant:` names the specific choice. This already exists in the thesis (line 55) for AWS; it generalizes cleanly.
- **Tier vocabulary is provider-translated via per-provider mapping tables.** `db.sql: tier: dev | prod-small | prod | premium | global` maps via [`aws_service_groups.md`](aws_service_groups.md), [`gcp_service_groups.md`](gcp_service_groups.md), and [`azure_service_groups.md`](azure_service_groups.md) — same structure (tier → concrete instance class + cost + latency band), different rows per provider.
- **`cloud_only:` IR flag is provider-scoped.** A resource that's `cloud_only` on AWS (Bedrock Agents, CloudFront, RUM) may have a local-runnable analog on another provider or no analog at all. The flag travels with the target, not the primitive.

## Out of scope (for now)

This doc covers primitive **naming and mapping**. What it deliberately doesn't cover:

- **Per-language Tier 1 hard-pair libraries enumerated per provider.** Each of `aws_service_groups.md` / `gcp_service_groups.md` / `azure_service_groups.md` lists the Tier-1 hard pairs (LLM, JWT, email, STT, image OCR) and the bridge libraries that cover them; deeper per-language enumeration (rig-core / litellm / jsonwebtoken / lettre etc. vs Vertex AI / Firebase Auth and vs Azure OpenAI / Entra ID equivalents) is downstream language-mapping work.
- **Multi-cloud deployments in one target.** Out of v1 scope. A `caravan.yaml` target picks one cloud; the IR not breaking across clouds is enough for now.
- **Hybrid composition across clouds.** Already covered conceptually (thesis "Composition" dimension); concrete `existing_resource:` references across clouds are deferred behind single-cloud GA.
