# PoC basic groups → 4-language code mapping

> A PoC-narrowed subset of the [12 per-language mapping docs](mapping_aws_to_python.md). Picks 10 basic groups from the [~36 AWS service role groups](aws_service_groups.md), defaults each to its cheapest-to-provision on-demand auto-scale option, and shows the cloud SDK call + local OSS call + env-var swap for each of [Python](mapping_aws_to_python.md), [Rust](mapping_aws_to_rust.md), [TypeScript](mapping_aws_to_typescript.md), and [Go](mapping_aws_to_go.md).
>
> **Data-plane vs control-plane.** This doc covers the **data-plane** — how user code calls cloud resources (S3, DynamoDB, SQS, …) with the same source in cloud and local environments. The **control-plane** — how user code calls *other parts of itself* across deploy units — is covered by [poc_rpc_sdk.md](poc_rpc_sdk.md) (the `caravan-rpc` SDK + seam dispatch). Both are required for the PoC to demonstrate the thesis end-to-end.
>
> Read order: [thesis.md](thesis.md) → [poc_rpc_sdk.md](poc_rpc_sdk.md) (control-plane) → this file (data-plane) → [poc_yaml_spec.md](poc_yaml_spec.md) (the entries + seams + targets yaml that exercises both).

## Scope

**6 base groups** (CRUD-app baseline) + **4 driven by sibling-repo evidence** ([code-rag](../../code-rag/), [quant-trading-gym](../../quant-trading-gym/)) = **10 basic groups**.

| # | Group | yaml `type:` | PoC default | OSS local | Demanded by |
|---|---|---|---|---|---|
| 1 | [compute](#1-compute) | — (handled by `entries:` + `seams:`) | Lambda arm64 | docker-compose service | all |
| 2 | [object_store](#2-object_store) | `bucket` | S3 Standard | `minio/minio` | all |
| 3 | [kv](#3-kv) | `kv` | DynamoDB on-demand | `amazon/dynamodb-local` | all |
| 4 | [queue](#4-queue) | `queue` | SQS Standard | `softwaremill/elasticmq-native` | all |
| 5 | [sql](#5-sql) | `db.sql` | Aurora Serverless v2 Postgres | `postgres:16` | all |
| 6 | [secret](#6-secret) | `secret` | SSM Parameter Store | env-var injection | all |
| 7 | [cache](#7-cache) | `cache` *(new)* | ElastiCache Serverless Redis | `redis:7-alpine` | code-rag + qtg |
| 8 | [stream](#8-stream) | `stream` *(new)* | Kinesis Data Streams on-demand | `redpandadata/redpanda` | qtg |
| 9 | [search](#9-search) | `search` *(new)* | OpenSearch Serverless (BM25 + k-NN) | `opensearchproject/opensearch:2` | code-rag |
| 10 | [llm](#10-llm) | `llm` *(new, Tier-1 hard pair)* | Bedrock | Ollama / FastEmbed | code-rag |

**Tier 0 vs Tier 1.** Nine of these groups (compute, object_store, kv, queue, sql, secret, cache, stream, search) are **Tier 0** — same client library both sides; only an `endpoint_url` / DSN env var changes between cloud and local. One group, **`llm`, is Tier 1** — cloud (Bedrock SDK) and local (Ollama / FastEmbed) speak different wire APIs, so a community abstraction library (rig-core / litellm / Vercel AI SDK / langchaingo) bridges them, and caravan selects which provider compiles into the binary per target via [manifest patching](poc_yaml_spec.md#manifest-patching). Tier 1 is the only group where the user's package manifest differs across targets.

**Thesis reconciliation.** Defaulting compute to Lambda is PoC pragmatism, not a thesis claim. Lambda needs zero VPC/ALB/cluster provisioning; switching an entry to `container` (Fargate) is one yaml line — see [poc_yaml_spec.md "Extensibility"](poc_yaml_spec.md#extensibility--three-3-line-diffs).

**Conventions in every section below.**
- The PoC default is the "expensive on-demand auto-scale" choice within the group (pay-per-request, scale-to-zero).
- For Tier 0 groups, the env-var pattern is one variable per resource (e.g. `S3_ENDPOINT_URL`) that is `None`/unset in production and points at the OSS container in dev.
- Each row links back to the canonical per-language mapping doc — those carry the full Quality / LocalStack / FIFO / cluster-mode notes the PoC drops.

---

## 1. compute

The "compute" group is implicit in caravan's yaml — there's no `type: compute` resource. Compute appears in two places:

- **`entries:`** — root deploy units (one or more per yaml). Each entry has a Dockerfile, triggers (HTTP / queue / cron), and a deploy-mode choice per target (`lambda | container | batch`).
- **`seams:`** — SDK seams that *may* be split off into their own deploy units per target (`inproc | container | lambda`).

So one yaml emits 1..N deploy units, depending on entries + per-target seam decisions. PoC default per target: each entry as `lambda` (zero infra to provision); split seams as `lambda` for cloud / `container` for compose. See [poc_yaml_spec.md "Worked example"](poc_yaml_spec.md#worked-example--smart-query).

Per-language: each entry's Dockerfile builds the monolith binary. A thin per-language adapter switches between "register a Lambda handler" and "listen on a port" based on `AWS_LAMBDA_RUNTIME_API` (only set inside Lambda).

| Lang | Cloud call (Lambda) | Local call (docker-compose) | Env-var swap |
|---|---|---|---|
| **Python** | `Mangum(app)` wraps FastAPI/Flask → `handler` ([mapping_aws_to_python.md:46](mapping_aws_to_python.md#L37)) | `uvicorn app:app --host 0.0.0.0` | `AWS_LAMBDA_RUNTIME_API` (auto-set inside Lambda) |
| **Rust** | `lambda_http::run(service_fn(handler))` + cargo-lambda ([mapping_aws_to_rust.md:48-57](mapping_aws_to_rust.md#L48-L57)) | `cargo lambda watch` or plain axum binary | same |
| **TypeScript** | `import { handle } from "hono/aws-lambda"; export const handler = handle(app)` ([mapping_aws_to_typescript.md:66-72](mapping_aws_to_typescript.md#L66-L72)) | `serve({ fetch: app.fetch, port: 8080 })` | same |
| **Go** | `lambda.Start(chiadapter.New(r).ProxyWithContext)` ([mapping_aws_to_go.md:74-97](mapping_aws_to_go.md#L74-L97)) | `http.ListenAndServe(":8080", r)` | same |

**Extension port.** Change `entries.<name>: lambda` → `container` in a target → caravan emits `aws_ecs_service` (cloud) or `compose service` (local) instead of Lambda. User's main() branch already handles both via the env-var check; the Dockerfile is unchanged.

**External HTTP vs inter-seam RPC** *(critical scope boundary)*. The per-language frameworks above (FastAPI, axum, Hono, chi) handle **external HTTP entry** — user requests arriving from outside the caravan-managed graph. For **calls between caravan-managed code units**, user code does NOT write its own HTTP plumbing — it uses [poc_rpc_sdk.md](poc_rpc_sdk.md)'s `client::<Interface>()` instead. The SDK dispatches that call as in-process, container, or Lambda Function URL invoke depending on yaml-driven seam decisions. Hand-rolling HTTP for inter-seam calls breaks the thesis's "same source, three lives" promise.

---

## 2. object_store

PoC default: **S3 Standard**. Endpoint-URL swap is the cleanest cloud↔local pattern in all four languages. Path-style addressing (`force_path_style` / `forcePathStyle` / `UsePathStyle`) is the universal MinIO gotcha — set it unconditionally when the endpoint env var is present.

| Lang | Cloud call | Local call (MinIO) | Env-var swap |
|---|---|---|---|
| **Python** | `boto3.client("s3", endpoint_url=os.environ.get("S3_ENDPOINT_URL"))` ([mapping_aws_to_python.md:81-84](mapping_aws_to_python.md#L81-L84)) | same code, env var set to `http://minio:9000` | `S3_ENDPOINT_URL` |
| **Rust** | `aws_sdk_s3::Client::from_conf(b.endpoint_url(url).force_path_style(true))` ([mapping_aws_to_rust.md:91-99](mapping_aws_to_rust.md#L91-L99)) | same code | same |
| **TypeScript** | `new S3Client({ endpoint, forcePathStyle: true })` ([mapping_aws_to_typescript.md:130-137](mapping_aws_to_typescript.md#L130-L137)) | same code | same |
| **Go** | `s3.NewFromConfig(cfg, func(o *s3.Options){ o.BaseEndpoint = aws.String(ep); o.UsePathStyle = true })` ([mapping_aws_to_go.md:172-192](mapping_aws_to_go.md#L172-L192)) | same code | same |

**Extension port.** Add `class:` to the yaml resource → `intelligent | glacier-deep-archive | standard-ia`. For S3 Express One Zone, add `variant: express-one-zone` (Tier 2 — no local stand-in).

---

## 3. kv

PoC default: **DynamoDB on-demand** (pay-per-request, no capacity planning). `dynamodb-local` is among the highest-fidelity AWS emulators; trustworthy for transactions, conditional expressions, queries.

| Lang | Cloud call | Local call (dynamodb-local) | Env-var swap |
|---|---|---|---|
| **Python** | `boto3.resource("dynamodb", endpoint_url=os.environ.get("DYNAMODB_ENDPOINT_URL"))` ([mapping_aws_to_python.md:138-142](mapping_aws_to_python.md#L138-L142)) | same code, env set to `http://dynamodb:8000` | `DYNAMODB_ENDPOINT_URL` |
| **Rust** | `aws_sdk_dynamodb::Client::from_conf(b.endpoint_url(url))` + `serde_dynamo` ([mapping_aws_to_rust.md:157-165](mapping_aws_to_rust.md#L157-L165)) | same code | same |
| **TypeScript** | `DynamoDBDocumentClient.from(new DynamoDBClient({ endpoint }))` ([mapping_aws_to_typescript.md:195-202](mapping_aws_to_typescript.md#L195-L202)) | same code | same |
| **Go** | `dynamodb.NewFromConfig(cfg, func(o *dynamodb.Options){ o.BaseEndpoint = aws.String(ep) })` + `attributevalue.MarshalMap` ([mapping_aws_to_go.md:257-281](mapping_aws_to_go.md#L257-L281)) | same code | same |

**Extension port.** Add `capacity_mode: provisioned` for predictable workloads; `kind: documentdb` to swap to DocumentDB. DAX (microsecond cache layer) is Tier-2 — use `cache` (group 7) instead for PoC.

---

## 4. queue

PoC default: **SQS Standard**. ElasticMQ implements the SQS wire protocol faithfully (long-poll, message attributes, DLQ wiring, FIFO dedup).

| Lang | Cloud call | Local call (ElasticMQ) | Env-var swap |
|---|---|---|---|
| **Python** | `boto3.client("sqs", endpoint_url=os.environ.get("SQS_ENDPOINT_URL"))` ([mapping_aws_to_python.md:197-202](mapping_aws_to_python.md#L197-L202)) | same code, env set to `http://elasticmq:9324` | `SQS_ENDPOINT_URL` + `QUEUE_URL` |
| **Rust** | `aws_sdk_sqs::Client::from_conf(b.endpoint_url(url))` ([mapping_aws_to_rust.md:222-228](mapping_aws_to_rust.md#L222-L228)) | same code | same |
| **TypeScript** | `new SQSClient({ endpoint })` ([mapping_aws_to_typescript.md:260-266](mapping_aws_to_typescript.md#L260-L266)) | same code | same |
| **Go** | `sqs.NewFromConfig(cfg, func(o *sqs.Options){ o.BaseEndpoint = aws.String(ep) })` ([mapping_aws_to_go.md:339-355](mapping_aws_to_go.md#L339-L355)) | same code | same |

**Extension port.** `kind: fifo` for ordered/deduplicated queues. To swap to RabbitMQ (Amazon MQ), the wire protocol changes — that's a different `kind:` value and a different client library (`pika` / `lapin` / `amqplib` / `amqp091-go`).

---

## 5. sql

PoC default: **Aurora Serverless v2 Postgres** (ACU scales to ~0.5 minimum, auto-scales up). Same `postgres:16` container locally. DSN-driven — second cleanest pattern after S3.

| Lang | Cloud call | Local call (postgres:16) | Env-var swap |
|---|---|---|---|
| **Python** | `create_engine(os.environ["DATABASE_URL"])` (SQLAlchemy / `psycopg`) ([mapping_aws_to_python.md:119-124](mapping_aws_to_python.md#L119-L124)) | same code, DSN points at compose-network host | `DATABASE_URL` |
| **Rust** | `PgPoolOptions::new().connect(&std::env::var("DATABASE_URL")?).await?` (`sqlx`) ([mapping_aws_to_rust.md:135-141](mapping_aws_to_rust.md#L135-L141)) | same code | same |
| **TypeScript** | `new Pool({ connectionString: process.env.DATABASE_URL })` (`pg`) ([mapping_aws_to_typescript.md:172-179](mapping_aws_to_typescript.md#L172-L179)) | same code | same |
| **Go** | `sql.Open("pgx", os.Getenv("DATABASE_URL"))` (`pgx/stdlib`) ([mapping_aws_to_go.md:227-239](mapping_aws_to_go.md#L227-L239)) | same code | same |

**Extension port.** `engine: mysql` swaps to MySQL / Aurora MySQL (drivers change per language). `tier: dev|prod|premium|global` is the caravan-owned scale knob; the time-series gap (`timeseries` group folded in) is covered by adding `extensions: [timescaledb]`. The vector-search gap is covered by `extensions: [pgvector]` — see group 9.

---

## 6. secret

PoC default: **SSM Parameter Store** (free tier; pay-per-call beyond). LocalStack covers Parameter Store and Secrets Manager wire-compatibly; the dev story can also be plain env vars when there's nothing sensitive.

| Lang | Cloud call | Local call (LocalStack / env) | Env-var swap |
|---|---|---|---|
| **Python** | `boto3.client("ssm", endpoint_url=os.environ.get("SSM_ENDPOINT_URL")).get_parameter(Name=...)` ([mapping_aws_to_python.md:301-304](mapping_aws_to_python.md#L301-L304)) | same code, or read `os.environ` directly | `SSM_ENDPOINT_URL` |
| **Rust** | `aws_sdk_ssm::Client::from_conf(b.endpoint_url(url))` ([mapping_aws_to_rust.md:317](mapping_aws_to_rust.md#L317)) | same code | same |
| **TypeScript** | `new SSMClient({ endpoint }).send(new GetParameterCommand({ Name, WithDecryption: true }))` ([mapping_aws_to_typescript.md:366-371](mapping_aws_to_typescript.md#L366-L371)) | same code | same |
| **Go** | `ssm.NewFromConfig(cfg, func(o *ssm.Options){ o.BaseEndpoint = aws.String(ep) }).GetParameter(...)` ([mapping_aws_to_go.md:461-477](mapping_aws_to_go.md#L461-L477)) | same code | same |

**Extension port.** `from: secrets-manager` for rotated secrets / cross-account share; `from: env` skips the SDK entirely (dev convenience). Caravan phase-4 injects the cloud-side resource ARN and the IAM read permission automatically.

---

## 7. cache

PoC default: **ElastiCache Serverless Redis** (pay-per-request ECPUs; auto-scales). Behavior-compatible against `redis:7-alpine` locally. Both [code-rag](../../code-rag/) (embedding/reranker output cache) and [quant-trading-gym](../../quant-trading-gym/) (hot feature cache) benefit.

| Lang | Cloud call | Local call (redis:7) | Env-var swap |
|---|---|---|---|
| **Python** | `redis.Redis.from_url(os.environ["REDIS_URL"])` ([mapping_aws_to_python.md:156-160](mapping_aws_to_python.md#L156-L160)) | same code | `REDIS_URL` |
| **Rust** | `redis::Client::open(std::env::var("REDIS_URL")?)?.get_async_connection().await?` ([mapping_aws_to_rust.md:178-184](mapping_aws_to_rust.md#L178-L184)) | same code | same |
| **TypeScript** | `new Redis(process.env.REDIS_URL!)` (`ioredis`) ([mapping_aws_to_typescript.md:216-221](mapping_aws_to_typescript.md#L216-L221)) | same code | same |
| **Go** | `redis.NewClient(redis.ParseURL(os.Getenv("REDIS_URL")))` (`go-redis/v9`) ([mapping_aws_to_go.md:294-301](mapping_aws_to_go.md#L294-L301)) | same code | same |

**Extension port.** `engine: valkey | memcached` swaps engines (memcached needs a different client library per language — `pymemcache` / `memcache` / `memjs` / `gomemcache`). `kind: memorydb` for durable Redis with multi-AZ transactional log. `kind: cluster` for hash-slot semantics — run `bitnami/redis-cluster` locally.

---

## 8. stream

PoC default: **Kinesis Data Streams on-demand** (per-shard scaling without capacity provisioning). Wire-compatible via LocalStack Kinesis. Distinguishes from `queue` by replay, partitioned ordering, and multiple independent consumers. Required by [quant-trading-gym](../../quant-trading-gym/) for trade/tick event broadcast.

| Lang | Cloud call | Local call (LocalStack) | Env-var swap |
|---|---|---|---|
| **Python** | `boto3.client("kinesis", endpoint_url=os.environ.get("KINESIS_ENDPOINT_URL"))` ([mapping_aws_to_python.md:225](mapping_aws_to_python.md#L221)) | same code, env to LocalStack endpoint | `KINESIS_ENDPOINT_URL` |
| **Rust** | `aws_sdk_kinesis::Client::from_conf(b.endpoint_url(url))` ([mapping_aws_to_rust.md:249](mapping_aws_to_rust.md#L249)) | same code | same |
| **TypeScript** | `new KinesisClient({ endpoint })` ([mapping_aws_to_typescript.md:288](mapping_aws_to_typescript.md#L288)) | same code | same |
| **Go** | `kinesis.NewFromConfig(cfg, func(o *kinesis.Options){ o.BaseEndpoint = aws.String(ep) })` ([mapping_aws_to_go.md:376](mapping_aws_to_go.md#L376)) | same code | same |

**Extension port.** `kind: msk-serverless` swaps the wire protocol to Kafka — client libraries change (`kafka-python` / `rdkafka` / `kafkajs` / `segmentio/kafka-go`). For Kinesis Firehose (destination delivery) and KCL-style consumer coordination, see the per-language mapping docs.

---

## 9. search

PoC default: **OpenSearch Serverless** with vector + BM25 in one collection (OCU auto-scales). Local container is `opensearchproject/opensearch:2` (k-NN plugin built in). Required by [code-rag](../../code-rag/) — the current LanceDB usage refactors into this. Provider-neutral name keeps the door open for vector-store-only providers.

| Lang | Cloud call | Local call (opensearch:2) | Env-var swap |
|---|---|---|---|
| **Python** | `opensearchpy.OpenSearch(hosts=[os.environ["OPENSEARCH_URL"]])` ([mapping_aws_to_python.md:179](mapping_aws_to_python.md#L179)) | same code | `OPENSEARCH_URL` |
| **Rust** | `opensearch::OpenSearch::new(Transport::single_node(&os.url)?)` ([mapping_aws_to_rust.md:203](mapping_aws_to_rust.md#L203)) | same code | same |
| **TypeScript** | `new Client({ node: process.env.OPENSEARCH_URL })` (`@opensearch-project/opensearch`) ([mapping_aws_to_typescript.md:241](mapping_aws_to_typescript.md#L241)) | same code | same |
| **Go** | `opensearch.NewClient(opensearch.Config{Addresses: []string{os.Getenv("OPENSEARCH_URL")}})` (`opensearch-go/v3`) ([mapping_aws_to_go.md:321](mapping_aws_to_go.md#L321)) | same code | same |

**Extension port.** Folds into `sql` with `extensions: [pgvector]` for small-scale dense vectors (single ANN index, no separate operational story). `kind: provisioned` swaps to non-serverless OpenSearch (cheaper at sustained load, capacity-planned). Kendra and S3 Vectors are Tier-2 — no local stand-in.

---

## 10. llm

PoC default: **Bedrock** (cloud) ↔ **Ollama** (chat) / FastEmbed (embedding) / local cross-encoder (rerank). **Tier-1 hard pair** per the [thesis](thesis.md#L50) — cloud and local speak different wire APIs, so an abstraction library is structurally required. Required by [code-rag](../../code-rag/) (currently Gemini via rig-core; refactors to Bedrock).

**This is the only group where caravan's manifest patching matters at compile time.** For the other 9 groups, the user installs one client library (boto3, aws-sdk-go, etc.) and only an env var changes per target. For `llm`, the **deps themselves differ per target** — Bedrock provider for cloud, Ollama provider for local. Caravan picks which provider compiles in by patching the user's package manifest per target. See [poc_yaml_spec.md "Manifest patching"](poc_yaml_spec.md#manifest-patching).

| Lang | Abstraction library | Cloud manifest patch | Local manifest patch |
|---|---|---|---|
| **Python** | `litellm` | `litellm[bedrock]>=1.0` added to `requirements.txt` | `litellm[ollama]>=1.0` |
| **Rust** | `rig-core` | `rig-core = { version = "0.x", features = ["bedrock"] }` in `Cargo.toml` | `rig-core = { features = ["ollama"] }` |
| **TypeScript** | Vercel AI SDK (`ai`) | `"@ai-sdk/amazon-bedrock": "^0.x"` added to `package.json` | `"ollama-ai-provider": "^0.x"` |
| **Go** | `langchaingo` or `eino` | build tag `cloud` → bedrock sub-package compiled in | build tag `local` → ollama sub-package |

User code stays identical across targets (calls the abstraction library's surface):

| Lang | User code (unchanged across targets) | Ref |
|---|---|---|
| **Python** | `litellm.completion(model=os.environ["LLM_MODEL"], messages=...)` | [mapping_aws_to_python.md:389-396](mapping_aws_to_python.md#L389-L396) |
| **Rust** | `rig::providers::auto_from_env().agent(...).prompt(...).await` | [mapping_aws_to_rust.md:410-415](mapping_aws_to_rust.md#L410-L415) |
| **TypeScript** | `generateText({ model: <provider>(LLM_MODEL), prompt })` | [mapping_aws_to_typescript.md:473-484](mapping_aws_to_typescript.md#L473-L484) |
| **Go** | `llms.GenerateFromSinglePrompt(ctx, llm, prompt)` (langchaingo) | [mapping_aws_to_go.md:584-614](mapping_aws_to_go.md#L584-L614) |

`LLM_MODEL` env var (also caravan-injected) picks the specific model — e.g., `bedrock/anthropic.claude-opus-4-7-...` for cloud, `ollama/llama3.1` for local. The library knows how to route based on the model string + which provider package is compiled in.

**Embedding / rerank.** Bedrock covers embeddings (Titan, Cohere Embed) under the same SDK; for the local side, embeddings need a separate path (FastEmbed via FFI for Python/Rust; `@xenova/transformers` for TypeScript; ONNX runtime for Go). Express in yaml as `llm: { task: embedding }` — caravan picks the right Bedrock model ID / local container image based on `task:`.

**Extension port.** `provider: openai | gemini | anthropic-api | vertex` — selects a different cloud manifest patch (different provider package). User code unchanged. Bedrock Knowledge Bases / Agents / Guardrails remain Tier-2 (`cloud_only`) — no community library bridges those.

---

## Out of PoC scope

The following are deliberately cut from PoC. All are recoverable from [ir.md](ir.md) and the full per-language mapping docs.

**Vocabulary change since older drafts**: the original IR had `modules:` (code units) + `bundles:` (packaging groups). The PoC collapses these into the entries + seams shape in [poc_yaml_spec.md](poc_yaml_spec.md). The `interfaces:` top-level yaml block is no longer needed for interface declarations — interfaces live in code (declared via `@wagon` per language); yaml's optional `seams:` block names them and points at the provider's source.

**Resource-level fields not in PoC**: `lifecycle:`, `variant:` (each group's PoC default fixes the variant); `composition: by-id` (v1 hybrid-debug feature).

**Resource types not in PoC**: `topic` (use `queue` with multiple consumers, or fold into `stream`); `static_site` (deferred per [caravan_abstraction_v4.md](caravan_abstraction_v4.md)); `cloud_only` (the Tier-2 escape hatch — handled per-call-site as documented in each `mapping_aws_to_<lang>.md` Bedrock Knowledge Bases / Agents / Guardrails rows).

**Trigger types not in PoC**: `topic`, `bucket_event` (use `queue` + S3-event-to-SQS wiring done by hand for PoC; auto-derive in v1.1).

**Subsystems not in PoC**: `creds_passthrough` for hybrid-debug; per-target shared base-image declarations; per-target Cargo-feature gating of seam code (PoC accepts that an entry's binary carries the inproc-impl code even when the seam is split — size cost, no correctness issue).

**Groups folded rather than promoted**: `feature_store` → `kv`; `model_registry` → `object_store` + `sql`/`kv` for metadata; `timeseries` → `sql` with `extensions: [timescaledb]`; `scheduler` → existing per-entry `triggers.cron:` (not a resource); `ml_inference` for embedding/rerank → `llm` group with `task:` field.

When the PoC graduates to v1, open [ir.md](ir.md), [aws_service_groups.md](aws_service_groups.md), and the four per-language [mapping_aws_to_<lang>.md](mapping_aws_to_python.md) docs — every dropped field is fully specified there.

---

See [poc_rpc_sdk.md](poc_rpc_sdk.md) for the control-plane (inter-seam RPC) companion, and [poc_yaml_spec.md](poc_yaml_spec.md) for the entries + seams + targets yaml that exercises both docs end-to-end.
