# supeux Abstraction Recommendation

> **Snapshot date: 2026-05-16.** Synthesizes `aws_service_groups.md`, `mapping_python_to_aws.md`, `mapping_aws_to_python.md`, `python_api_diffs.md`. Final input to PoC scoping; supersedes the earlier IfC-survey strawman where they disagree.

This file collapses the per-pair difficulty analysis into:
1. A revised PoC primitive scope (built up from File 4's difficulty bands rather than the original strawman).
2. A cloud-only list and how supeux signals it.
3. A concrete shape for the yaml switch (GHA + IaC + docker-compose layers).
4. Risk gotchas readers must know before trusting the abstraction.

---

## 1. Where the prior recommendation needs amending

The IfC-survey strawman proposed 5 primitives for the first iteration:
- `function` · `bucket` · `topic+sub` · `cron` · `secret`

After Files 1–4, this is **almost right**, with two small revisions:

| Primitive | Verdict | Note |
|---|---|---|
| `function` | **Keep** | Lambda↔FastAPI is *Moderate* (File 4). supeux ships a `handler` decorator + Mangum-style wrapper for both directions. |
| `bucket` | **Keep** | S3↔MinIO is *Trivial* — endpoint URL env var. The cleanest primitive in the set. |
| `topic+sub` | **Keep, with split** | SNS↔localstack is *Trivial*; SQS↔ElasticMQ is *Trivial*. Treat `topic` and `queue` as separate primitives — they're distinct AWS services and supeux users routinely want a queue without a topic. Suggest **`topic` + `queue`** as two primitives. |
| `cron` | **Keep** | EventBridge Scheduler is *Moderate*. supeux generates a `@supeux.cron("0 2 * * *")` decorator that emits the AWS schedule rule and a local APScheduler container only if explicitly enabled. |
| `secret` | **Keep** | SSM Parameter Store + Secrets Manager are both *Trivial*. supeux ships `secret("name")` that resolves at runtime against either AWS or env vars locally. |

**Recommended addition**: **`kv`** (DynamoDB). Difficulty *Trivial*; nearly every Python web app needs a session store, idempotency key store, or feature-flag store; DDB-local is one of the best-supported emulators in the ecosystem. Adding it costs supeux ~2 days of implementation and unlocks a huge class of apps. The original 5-primitive scope omitted this and would force users to roll their own.

**Recommended addition**: **`db.sql`** (Postgres). Difficulty *Trivial*; ~70% of Python apps need a relational DB. Aurora/RDS↔postgres container is just a DSN swap. Even if supeux doesn't manage the connection (let SQLAlchemy do that), it needs to know the resource exists so it can provision the cluster + inject `DATABASE_URL`.

**Revised PoC primitive list (7)**:
- `function` (Lambda or Fargate task — supeux picks based on workload hint)
- `bucket` (S3 / MinIO)
- `queue` (SQS / ElasticMQ)
- `topic` (SNS / localstack)
- `kv` (DynamoDB / dynamodb-local)
- `db.sql` (RDS or Aurora Postgres / postgres container)
- `cron` (EventBridge Scheduler / APScheduler if enabled)
- `secret` (SSM Parameter Store + Secrets Manager / env vars)

That's 7 primitives. Still small enough to ship in weeks, large enough to express the majority of Python web/data apps.

---

## 2. Cloud-only marking and the `cloud_only` IR flag

From File 4's Intractable bucket (~15 services), supeux must support declaring a resource and refusing to bind it locally. Proposal:

```python
# Example: supeux IR (Python decorator style)
@supeux.resource(
    type="bedrock.llm",
    model="anthropic.claude-opus-4-7-20260416-v1:0",
    cloud_only=True,
)
class LLM: ...

@supeux.function(uses=[LLM])
def summarize(event):
    resp = LLM.invoke(messages=event["messages"])
    return resp
```

Behavior:
- `supeux up --target=aws` → provisions Bedrock invocation permissions, injects the boto3 Bedrock client wrapper.
- `supeux up --target=local` → **errors loudly**: "Resource LLM is `cloud_only=True`; pass `--allow-cloud-only=LLM` to use the real AWS service from local dev, or implement a `LocalLLM` adapter."

This makes the cloud-only/local-switchable split explicit at the IR level. Users don't accidentally ship something that "works locally" because it was secretly mocked.

**The 15-ish cloud-only services** (from File 3's "none viable" + File 4's "Intractable"):
- CloudFront, Lambda@Edge, CloudFront Functions, Global Accelerator
- S3 Express One Zone, S3 Vectors, S3 Object Lambda, S3 Select
- Aurora DSQL, DAX, Neptune Analytics, Kendra
- SNS Mobile Push (APNs/FCM), CloudWatch Synthetics / RUM / Application Signals
- IAM enforcement (LocalStack stubs the API, not the enforcement)
- Bedrock Knowledge Bases / Agents / Guardrails
- IoT Device Management / Defender / Analytics / SiteWise / TwinMaker / FleetWise
- SageMaker JumpStart / Canvas
- Forecast (deprecated), Personalize

For each, supeux ships a `cloud_only=True` resource type. No local backend. Documented loudly.

---

## 3. Yaml / switch shape

The user's brief said the switch should be yaml combining three layers: GitHub Actions, IaC, docker-compose. The recommended shape:

```yaml
# supeux.yaml — top-level config in repo root
name: my-app
default_target: local           # used when `supeux up` is run with no --target

targets:
  local:
    runtime: docker-compose
    overrides:
      function: container       # all functions become long-lived containers
      cron: none                # don't run scheduled jobs in local dev by default
  aws-staging:
    runtime: aws
    region: us-east-1
    account_id: "111122223333"
    overrides:
      function: lambda          # all functions deploy as Lambda
      db.sql: aurora-serverless-v2-paused  # auto-pause on inactivity
    ci:
      workflow: .github/workflows/deploy-staging.yml
      on:
        push: { branches: [main] }
  aws-prod:
    runtime: aws
    region: us-east-1
    account_id: "999988887777"
    overrides:
      function: lambda
      db.sql: aurora-serverless-v2
      bucket.policy: versioning+lifecycle
    ci:
      workflow: .github/workflows/deploy-prod.yml
      on:
        workflow_dispatch: {}  # manual promotion only

resources:
  - type: db.sql
    name: app
    engine: postgres
    version: "16"
  - type: bucket
    name: uploads
  - type: queue
    name: jobs
  - type: kv
    name: sessions
    primary_key: [pk, sk]
  - type: function
    name: api
    handler: app.api:handler
    uses: [app, uploads, jobs, sessions]
  - type: function
    name: worker
    handler: app.worker:handler
    uses: [app, jobs, uploads]
    trigger: { queue: jobs }
  - type: cron
    name: nightly_cleanup
    schedule: "0 2 * * *"
    target: worker
  - type: secret
    name: stripe_key
```

What each layer absorbs:
- **The yaml itself** = the IR. supeux parses this into a protobuf spec.
- **docker-compose layer** = generated from the `local` target: docker-compose.yaml with postgres, minio, ElasticMQ, dynamodb-local, redis (if used), the FastAPI wrapper for each `function`. No GHA, no Terraform.
- **IaC layer** = generated from `aws-staging` / `aws-prod`: a Pulumi program (in-process — no CLI subprocess) that creates the RDS cluster, S3 bucket, SQS queue, DynamoDB table, Lambda functions, IAM roles + policies (auto-derived from `uses:`).
- **GHA layer** = generated workflow files (`.github/workflows/deploy-staging.yml`) that run `supeux up --target=aws-staging` on the listed trigger. Just enough to bootstrap CI; users can edit by hand if they need more.

**Switch granularity**: a single CLI flag (`--target=`) flips environments. The yaml decides what each environment maps to. No code change.

**Where overrides go**: most users will not customize individual resources per target. Sensible defaults per `runtime` cover 90% of cases. The `overrides:` block exists for the 10% (e.g., "use Aurora Serverless v2 with auto-pause in staging, not in prod").

---

## 4. Risk list — divergence gotchas in "easy" mappings

The Trivial-band pairs (~22) are not 100% identical. Each carries a known divergence; users must be told.

| Pair | Gotcha | Mitigation |
|---|---|---|
| S3 ↔ minio | Strong-read-after-write semantics under concurrent writes differ. minio has been known to return stale reads in degraded modes. | Document "S3 is strongly consistent since Dec 2020; minio's consistency model is per-version. For dev, fine; for prod assumptions, lean on S3 docs." |
| S3 ↔ minio | Lifecycle policies use different DSLs; supeux only emits to one side. | supeux generates S3 lifecycle XML for AWS; emits the equivalent minio command for local — but flagged as best-effort. |
| DynamoDB ↔ dynamodb-local | Streams partially supported; TTL deletes happen on best-effort timing. Time-to-live in production deletes within 48 hr; locally instant. | Don't write code that depends on TTL timing for correctness. |
| SQS ↔ ElasticMQ | ElasticMQ doesn't enforce per-account throttle quotas. Code that handles `ThrottlingException` won't see it locally. | If you have throttle-handling logic, chaos-test it in staging. |
| SQS FIFO ↔ ElasticMQ FIFO | Dedup window precision differs by ms. | Don't rely on the exact 5-min window in tests. |
| Postgres (RDS/Aurora) ↔ postgres container | Aurora-specific extensions (`aurora_compute_plan`, etc.) don't exist in vanilla Postgres. Conversely, vanilla Postgres extensions Aurora hasn't approved (e.g., niche FDWs) will fail in Aurora. | Pin to the intersection. supeux warns at IR validation if a referenced extension isn't on Aurora's supported list. |
| RDS minor-version auto-upgrades | Maintenance windows trigger version bumps that can break pinned `psycopg` extension versions. | Use Aurora (broader compatibility window) or disable auto-minor-upgrade and own the cadence. |
| DocumentDB ↔ mongo | DocumentDB lacks `$lookup` outer-join semantics, change-stream resumability quirks, ~30% of aggregation operators. | Test critical aggregations against DocumentDB in CI, not just local mongo. |
| ElastiCache cluster-mode ↔ single redis | Cross-slot pipelines fail on cluster, work on single. | Use `redis-cluster` container locally if your code uses cluster-mode in prod. |
| OpenSearch ↔ opensearch image | UltraWarm tier behaviors don't reproduce; ML plugins (k-NN, learning-to-rank) versions may differ. | Pin OpenSearch versions to match. |
| Kinesis ↔ localstack | KCL workers need DynamoDB locally too; coordination behavior differs at scale. | Test the producer locally; test consumer at scale against real Kinesis. |
| SES ↔ mailhog | SES throttles based on reputation + warmup; mailhog never throttles. | Don't load-test through SES sandbox; request prod access first. |
| Step Functions ↔ aws-stepfunctions-local | Distributed Map state, Express semantics, and intrinsic-function library have version drift with the local container. | Pin to a known good local container version; flag in supeux output when ASL uses a feature the pinned local doesn't support. |
| IoT Core MQTT ↔ mosquitto | IoT Core requires mTLS device certs; mosquitto accepts plaintext. Real IAM authorization at the broker doesn't reproduce. | Run mosquitto with TLS in CI to at least catch handshake bugs. |

---

## 5. Concrete first-iteration scope (revised from the IfC-survey strawman)

Hard constraints to keep the PoC shippable:
- **7 primitives only**: `function`, `bucket`, `queue`, `topic`, `kv`, `db.sql`, `cron`, `secret` (yes, 8 — I lied; `secret` slipped in. Treat as 7 + 1 because secrets are first-class everywhere).
- **1 language SDK**: Python.
- **1 cloud**: AWS (with cloud-agnostic primitive names — `bucket` not `s3`).
- **1 local backend**: docker-compose with the well-supported emulator containers (`minio`, `elasticmq`, `dynamodb-local`, `postgres:16`, `redis:7`, `localstack` Community for SNS + secrets + SSM).
- **1 IaC bridge**: Pulumi in-process (no CLI subprocess, no separate state backend setup).
- **State**: user's own S3 bucket; passphrase in SSM.

What is explicitly **not in scope** for v1:
- TypeScript / Go / Rust SDKs.
- GCP / Azure providers.
- Cognito or any auth primitive (use `cloud_only=True` if needed; cleanest path is to keep auth out of the IR for now).
- Step Functions / Bedrock / SageMaker / API Gateway WebSocket (cloud-only for v1; users wire boto3 directly).
- Live debugging proxy (SST-style).
- Multi-region.
- A Console UI.

**Verification checklist** (when v1 is built):
- [ ] `supeux up --target=local` brings up the full local stack via docker-compose. App runs against it.
- [ ] `supeux up --target=aws-staging` provisions equivalent AWS resources via Pulumi. Same app code runs.
- [ ] Switching `--target` between runs is fast (Pulumi state cached locally; docker-compose is incremental).
- [ ] IAM policies on AWS are auto-derived from `uses:` declarations. No manual policy editing required.
- [ ] `supeux spec --json` prints the IR.
- [ ] A reference Python app exists in `/examples` exercising all 7+1 primitives.
- [ ] Cloud-only resources error usefully when the user tries `--target=local`.

---

## 6. One-page summary

> The IfC survey settled on a 5-primitive PoC. After auditing 35 AWS service groups and ~50 AWS↔Python local pairs, **the right PoC scope is 7+1 primitives**: `function`, `bucket`, `queue`, `topic`, `kv`, `db.sql`, `cron`, `secret`. Adding `kv` and `db.sql` is cheap (both are Trivial-band, single env-var swap) and dramatically increases the fraction of real Python apps that can be expressed.
>
> ~22 AWS↔local pairs are **Trivial** (env-driven `endpoint_url` or DSN swap) — these are supeux's bread and butter. ~12 are **Moderate** (need a thin adapter). ~5 are **Hard** (Cognito, Step Functions multi-service, API Gateway WebSocket, fan-out durability) — supeux defines Protocol interfaces and users pick the impl. ~15 are **Intractable** (CloudFront, Bedrock, SageMaker, IoT Defender, etc.) — supeux marks them `cloud_only=True` and refuses local binding.
>
> The yaml switch lives at `supeux.yaml`; a `--target=` CLI flag flips between `local` (docker-compose generated), `aws-staging` and `aws-prod` (Pulumi in-process). GHA workflows are generated for CI. No code changes per environment — only env vars injected by the supeux runtime.
>
> The 15-pair "Intractable" list is the *honest scope boundary* of supeux. Trying to abstract any of them would produce false positives — code that works locally and fails in production. Treat the list as a feature of the abstraction, not a limitation.
