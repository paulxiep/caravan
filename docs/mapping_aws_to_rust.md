# AWS → Rust Stack Mapping & Emulation Quality

> ⚠️ **HISTORICAL — pre-SDK research notes.** Current SDK contract lives at [`../rpc/rust/`](../rpc/rust/) (caravan-rpc + caravan-rpc-macros at 0.1.0). Authoritative docs are [`thesis.md`](thesis.md) and [`development_plan.md`](development_plan.md). Snapshot 2026-05-16; do not assume any specific row is current.

> **Snapshot date: 2026-05-16.** References `aws_service_groups.md` for AWS-side detail and `mapping_rust_to_aws.md` for the reverse direction.
> **Scope**: Rust ecosystem. Pragmatic Rust-first — services with no mature Rust crate are flagged but not detailed. "Wire-compatible" means *the official `aws-sdk-rust` crate (or relevant driver) talks to a local container via builder-time endpoint / DSN override without code changes*.

This file answers: *"I picked an AWS service. What container do I run alongside my Rust app so the same code talks to it without knowing the difference?"*

## Emulation-quality bands

| Band | Meaning |
|---|---|
| **wire-compatible** | Same Rust crate (aws-sdk-* or driver) talks to local container via env-driven endpoint/DSN. Behavior matches production for ~95% of common operations. |
| **behavior-compatible** | Same Rust crate, different connection setup. The engine is real (real Postgres, real Redis) so behavior is honest, but the AWS-specific bits (IAM, snapshots, performance insights) are absent. |
| **partial** | Local container speaks the same wire protocol but lacks features. Most happy-path code works; specific operations error or return wrong shapes. |
| **none viable** | No local container meaningfully reproduces the AWS service's behavior. Either abstract behind a trait at your code boundary, or test against AWS directly. |

Two local-container columns per service:
- **OSS option**: the engine itself (e.g., `postgres:16`, `redis:7`, `minio/minio`).
- **LocalStack option**: `localstack/localstack` (Community = free; Pro = paid). Where Community covers the service it's listed; Pro-only services are flagged.

**The Rust idiom for cloud↔local switching** is the AWS SDK builder pattern:

```rust
use aws_config::{BehaviorVersion, defaults};
use aws_sdk_s3 as s3;
let mut cfg = defaults(BehaviorVersion::latest());
if let Ok(url) = std::env::var("S3_ENDPOINT_URL") {
    cfg = cfg.endpoint_url(url);
}
let s3 = s3::Client::new(&cfg.load().await);
```

Type-state builders make the swap a few extra lines vs boto3's `endpoint_url=` kwarg, but mechanically the same env-driven flip.

---

# Web stack core

## 1. Compute — Function (Lambda)

| AWS | OSS option | LocalStack | Quality | Notes |
|---|---|---|---|---|
| Lambda | `public.ecr.aws/lambda/provided.al2023` runtime image + cargo-lambda for cross-compile | `localstack` Community — Lambda v2 | partial | Rust Lambda runtime went GA Nov 2025 (`lambda_runtime` crate). LocalStack invokes via local Docker; IAM, layers, VPC ENIs stubbed; cold-start timing differs. |
| Lambda SnapStart | none | none | none viable | Rust cold-starts are already low (~10–50 ms); SnapStart less relevant. AWS-internal snapshot mechanics anyway. |
| Lambda@Edge | none | none | none viable | CDN-edge invocation has no local counterpart. Also: Lambda@Edge does not support custom Rust runtime as of 2026; it requires Node/Python. |
| Lambda Function URL | run handler under `lambda_http` + axum adapter | `localstack` Lambda | partial | `lambda_http` adapts Lambda events to `http::Request`; the same axum router works both as a Lambda handler and as a standalone server. |

**Rust idiom for local dev**: with `lambda_http`, write an axum router and use `cargo lambda watch` to serve it locally over HTTP. The same crate compiled with `cargo lambda build` produces the Lambda binary. One handler, two deployment targets.

```rust
use lambda_http::{run, service_fn, Error, Request, Response, Body};
async fn handler(req: Request) -> Result<Response<Body>, Error> {
    Ok(Response::new("hi".into()))
}
#[tokio::main]
async fn main() -> Result<(), Error> { run(service_fn(handler)).await }
```

---

## 2. Compute — Container

| AWS | OSS option | LocalStack | Quality | Notes |
|---|---|---|---|---|
| ECS / EKS / Fargate / App Runner | `docker compose up` | `localstack` Pro — ECS | wire-compatible (run-the-container sense) | Container itself is identical; orchestrator differs. A statically-linked Rust binary in a `FROM scratch` image is ~5–15 MB — fast pulls on Fargate. |

**Rust idiom**: build a single static binary, `FROM scratch` or `FROM gcr.io/distroless/cc-debian12`. No language-runtime-specific concerns at the image layer.

---

## 3. Compute — VM / Batch

| AWS | OSS option | LocalStack | Quality | Notes |
|---|---|---|---|---|
| EC2 | docker container approximation | `localstack` Community — EC2 API only | partial | Local EC2 is a category mistake — emulate the *workload*. |
| AWS Batch | docker-compose service + a queue | `localstack` Pro — Batch | partial | Batch's job dispatching is mocked. Rust batch jobs run identically as Fargate tasks; the orchestrator differs. |
| Lightsail | docker container | `localstack` Pro — Lightsail | partial | Same as EC2. |

---

## 4. Storage — Object (S3)

| AWS | OSS option | LocalStack | Quality | Notes |
|---|---|---|---|---|
| S3 Standard / IA / Glacier | `minio/minio` or `adobe/s3mock` | `localstack` Community — S3 | **wire-compatible** | `aws-sdk-s3` with `.endpoint_url(...)` and `.force_path_style(true)` works against minio. Storage classes are no-ops on minio. |
| S3 Intelligent-Tiering | minio | localstack | partial | Tiering behavior is mocked / absent. |
| S3 Express One Zone | none | none | none viable | Directory-bucket semantics + 10× perf are AWS-specific. |
| S3 lifecycle / replication | minio (has its own lifecycle DSL) | localstack | partial | Different DSL on minio. |
| S3 Object Lambda | none | partial | partial | LocalStack stubs the API; can't reproduce edge-routing. |

**Rust idiom**:
```rust
use aws_sdk_s3::{Client, config::Builder};
let conf = aws_config::defaults(BehaviorVersion::latest()).load().await;
let mut b = Builder::from(&conf);
if let Ok(url) = std::env::var("S3_ENDPOINT_URL") { b = b.endpoint_url(url).force_path_style(true); }
let s3 = Client::from_conf(b.build());
```
`force_path_style(true)` is the minio-specific gotcha — without it, the SDK does virtual-host-style addressing that minio doesn't accept. Set unconditionally when the endpoint env is set.

**Alternative**: the `object_store` crate (from Apache Arrow) abstracts S3/GCS/Azure/local-file behind a single trait. Useful for multi-cloud; weaker than aws-sdk-s3 for advanced S3 features.

---

## 5. Storage — File

| AWS | OSS option | LocalStack | Quality | Notes |
|---|---|---|---|---|
| EFS | docker volume + `nfs-ganesha` for true NFS, or bind-mount | none | partial | Rust `std::fs` and `tokio::fs` work transparently against bind-mounted volumes. NFS-specific lock contention won't show up locally. |
| FSx Lustre / Windows / ONTAP / OpenZFS | None matched | none | none viable | Filesystem-specific. |

---

## 6. Storage — Block (EBS)

| AWS | OSS option | LocalStack | Quality | Notes |
|---|---|---|---|---|
| EBS gp3 / io2 / st1 / sc1 | docker volume | `localstack` Pro — EBS API | partial | Locally, every "disk" is your laptop's SSD. Performance bands are AWS-specific. |
| Instance Store | tmpfs | none | partial | Ephemeral nature is the only locally-reproducible property. |

---

## 7. Database — RDBMS

| AWS | OSS option | LocalStack | Quality | Notes |
|---|---|---|---|---|
| RDS Postgres | `postgres:16` | `localstack` Pro — RDS API | **behavior-compatible** | `sqlx` / `tokio-postgres` / `sea-orm` / `diesel` all DSN-driven. |
| Aurora Postgres / Aurora Serverless v2 | `postgres:16` | localstack Pro | behavior-compatible | Aurora-specific features (read replicas, ACU scaling) are AWS-only and invisible to code. |
| RDS MySQL / Aurora MySQL | `mysql:8` | localstack Pro | behavior-compatible | Same crates: `sqlx`, `mysql_async`, `diesel`. |
| RDS MariaDB | `mariadb:11` | localstack Pro | behavior-compatible | Same. |
| RDS for SQL Server | `mcr.microsoft.com/mssql/server:2022-latest` | localstack Pro | behavior-compatible | `tiberius` is the standard async crate. |
| RDS for Oracle | `gvenzl/oracle-xe:21` | localstack Pro | partial | Rust Oracle crates (`oracle`) require Oracle Instant Client; awkward toolchain. |
| Aurora DSQL (multi-region) | none | none | none viable | Active-active is a coordination problem; no local equivalent. |

**Rust idiom**:
```rust
use sqlx::postgres::PgPoolOptions;
let url = std::env::var("DATABASE_URL")?;
let pool = PgPoolOptions::new().max_connections(8).connect(&url).await?;
```
Same code, DSN swap.

**Rust-specific gotcha**: `sqlx` macro-form (`sqlx::query!`) verifies queries at compile time and requires a live database during `cargo build`, or pre-cached query metadata via `cargo sqlx prepare`. Plan CI for this — usually a `docker-compose up postgres` before `cargo build`.

---

## 8. Database — KV / Document NoSQL

| AWS | OSS option | LocalStack | Quality | Notes |
|---|---|---|---|---|
| DynamoDB | `amazon/dynamodb-local` (official) | `localstack` Community — DynamoDB | **wire-compatible** | `aws-sdk-dynamodb` with `.endpoint_url("http://dynamodb:8000")`. Streams partial; transactions + conditional expressions solid. |
| DynamoDB Global Tables | dynamodb-local × 2 with manual replication | none | partial | Replication is the whole point and doesn't exist locally. |
| DAX | none | none | none viable | Microsecond cache layer; AWS-only. |
| DocumentDB | `mongo:7` | localstack Pro | partial | `mongodb` crate works both sides; DocumentDB ≠ real Mongo — same false-positive risk as Python. |
| Keyspaces | `cassandra:5` | none | partial | `scylla` or `cdrs-tokio` crates; Cassandra ≠ Keyspaces in some ops. |

**Rust idiom**:
```rust
use aws_sdk_dynamodb::Client;
let conf = aws_config::defaults(BehaviorVersion::latest()).load().await;
let mut b = aws_sdk_dynamodb::config::Builder::from(&conf);
if let Ok(url) = std::env::var("DYNAMODB_ENDPOINT_URL") { b = b.endpoint_url(url); }
let ddb = Client::from_conf(b.build());
```
For typed item ↔ struct mapping, pair with `serde_dynamo`.

---

## 9. Database — Cache

| AWS | OSS option | LocalStack | Quality | Notes |
|---|---|---|---|---|
| ElastiCache Redis (all flavors) | `redis:7-alpine` | localstack Pro | **behavior-compatible** | `redis-rs` (sync + async via `tokio` feature), `fred` (high-throughput), `deadpool-redis` (pooling). DSN swap. |
| ElastiCache Serverless | `redis:7-alpine` | localstack Pro | behavior-compatible | Serverless behavior invisible to code. |
| ElastiCache Memcached | `memcached:1.6` | localstack Pro | behavior-compatible | `memcache` or `async-memcached`. |
| MemoryDB for Redis | `redis:7-alpine` | none | behavior-compatible | MemoryDB's durability doesn't reproduce locally. |

**Rust idiom**:
```rust
use redis::AsyncCommands;
let client = redis::Client::open(std::env::var("REDIS_URL")?)?;
let mut conn = client.get_async_connection().await?;
let _: () = conn.set_ex("session:abc", "user-123", 3600).await?;
```

---

## 10. Database — Time-series / Graph

| AWS | OSS option | LocalStack | Quality | Notes |
|---|---|---|---|---|
| Timestream for LiveAnalytics | `influxdb:2` (closest) | none | none viable | Timestream's SQL surface is proprietary; Rust crate (`aws-sdk-timestreamwrite` / `aws-sdk-timestreamquery`) only talks to AWS. |
| Timestream for InfluxDB | `influxdb:2` | none | wire-compatible | `influxdb2` Rust crate works both sides. URL + token swap. |
| Neptune | `tinkerpop/gremlin-server` (Gremlin) or `neo4j` (Cypher) | none | partial | Rust Gremlin clients (`gremlin-client`) and Cypher (`neo4rs`) are developing; pick one engine. |
| Neptune Analytics | none | none | none viable | In-memory engine is AWS-only. |

---

## 11. Database — Search / Vector

| AWS | OSS option | LocalStack | Quality | Notes |
|---|---|---|---|---|
| OpenSearch Service | `opensearchproject/opensearch:2` | localstack Pro | **behavior-compatible** | `opensearch` crate is the official Rust client; URL swap. Auto-tuning + UltraWarm are AWS-only. |
| OpenSearch Serverless | `opensearchproject/opensearch:2` | none | behavior-compatible | Auto-scale invisible to code. |
| OpenSearch k-NN | opensearch image w/ knn plugin | none | behavior-compatible | Plugin built-in to 2.x image. |
| Aurora pgvector | `pgvector/pgvector:pg16` | none | wire-compatible | `pgvector` Rust crate integrates with `sqlx` / `tokio-postgres` / `diesel`. Same SQL. |
| S3 Vectors | minio + `instant-distance` / `hnsw_rs` (in-process ANN) | none | none viable | S3 Vectors API is proprietary; no Rust wrapper. |
| Kendra | none | none | none viable | Bag-of-features too proprietary. |

---

## 12. Messaging — Queue

| AWS | OSS option | LocalStack | Quality | Notes |
|---|---|---|---|---|
| SQS Standard | `softwaremill/elasticmq-native` | `localstack` Community — SQS | **wire-compatible** | `aws-sdk-sqs` with `.endpoint_url("http://elasticmq:9324")`. Long-poll, DLQ wiring, FIFO dedup all work. |
| SQS FIFO | ElasticMQ supports it | localstack | wire-compatible | Same. |
| Amazon MQ — RabbitMQ | `rabbitmq:3-management` | none | **behavior-compatible** | `lapin` (the canonical async AMQP crate). Real RabbitMQ. |
| Amazon MQ — ActiveMQ | `apache/activemq-classic` | none | behavior-compatible | STOMP via `tokio-stomp` or `stomp-rs`. Mature-but-niche. |

**Rust idiom**:
```rust
let conf = aws_config::defaults(BehaviorVersion::latest()).load().await;
let mut b = aws_sdk_sqs::config::Builder::from(&conf);
if let Ok(url) = std::env::var("SQS_ENDPOINT_URL") { b = b.endpoint_url(url); }
let sqs = aws_sdk_sqs::Client::from_conf(b.build());
sqs.send_message().queue_url(&std::env::var("QUEUE_URL")?).message_body("job#42").send().await?;
```

---

## 13. Messaging — Pub/Sub & Event Bus

| AWS | OSS option | LocalStack | Quality | Notes |
|---|---|---|---|---|
| SNS Standard topic | `localstack` Community — SNS | localstack | wire-compatible | `aws-sdk-sns` with `.endpoint_url(...)`. |
| SNS FIFO topic | localstack | localstack | partial | FIFO semantics partial in LocalStack. |
| SNS Mobile Push (APNs/FCM) | none | localstack Pro | none viable | Real APNs/FCM needed. |
| EventBridge default bus | localstack Community — EventBridge | localstack | partial | `aws-sdk-eventbridge`. Rules + targets work for happy paths; schema registry, archive, replay partial. |
| EventBridge Pipes | localstack Pro | none | partial | Filter + target wiring stubbed. |
| EventBridge Scheduler | localstack Pro | localstack Pro | partial | Schedules fire; precision differs. |

---

## 14. Messaging — Streaming

| AWS | OSS option | LocalStack | Quality | Notes |
|---|---|---|---|---|
| Kinesis Data Streams | `localstack` Community — Kinesis | localstack | **wire-compatible** | `aws-sdk-kinesis` with `.endpoint_url(...)`. KCL has no Rust port — shard coordination is awkward at scale. |
| Kinesis Firehose | localstack Pro | localstack Pro | partial | Destination delivery needs separate emulators. |
| MSK | `bitnami/kafka:3.7` | none | **behavior-compatible** | `rdkafka` (librdkafka FFI) — mature, production-grade. `rskafka` is pure-Rust but lighter on features. Bootstrap-server env swap. |
| MSK Serverless | `bitnami/kafka:3.7` | none | behavior-compatible | Invisible to client. |
| MSK Connect | run Kafka Connect container | none | behavior-compatible | Real Kafka Connect; AWS lifecycle absent. |

**Rust-specific friction**: MSK with IAM SASL requires `aws-msk-iam-sasl-signer` (a Rust crate exists but less mature than the Java/Python equivalents). For most workloads, prefer SCRAM-SHA-512 or mTLS auth on MSK.

---

## 15. API / Web Edge

| AWS | OSS option | LocalStack | Quality | Notes |
|---|---|---|---|---|
| API Gateway REST | `localstack` Community — API Gateway | localstack | partial | `aws-sdk-apigateway` only manages config. For local dev, run your `axum` / `actix-web` router directly. |
| API Gateway HTTP | localstack | localstack | partial | Use `lambda_http` to share handler code between Lambda+APIGW HTTP and a standalone axum binary. |
| API Gateway WebSocket | localstack Pro | localstack Pro | partial | Stateful-connection model is APIGW-specific (connections stored in DDB; push via REST). axum websockets are per-process. No shared abstraction. |
| AppSync (GraphQL) | localstack Pro | localstack Pro | partial | Rust GraphQL: `async-graphql` server-side. AppSync resolvers + subscriptions need adapters. |
| ALB / NLB / GWLB | `nginx` or `traefik` for L7 | localstack Pro | partial | LB doesn't matter; route to your container. |

---

## 16. CDN

| AWS | OSS option | LocalStack | Quality | Notes |
|---|---|---|---|---|
| CloudFront | none | localstack Pro | none viable | CDN behavior (POP routing) unobservable locally. |
| CloudFront Functions / Lambda@Edge | none | none | none viable | Edge runtime is JS-only as of 2026; Rust isn't a target. |
| Global Accelerator | none | none | none viable | Anycast IP layer unobservable locally. |

---

## 17. DNS

| AWS | OSS option | LocalStack | Quality | Notes |
|---|---|---|---|---|
| Route 53 | `coredns/coredns` or `/etc/hosts` | localstack Community — Route 53 | partial | `aws-sdk-route53` config only; resolution itself is system DNS. |
| Route 53 Resolver | coredns | localstack Pro | partial | VPC-resolver behavior absent. |

---

## 18. Identity / Auth

| AWS | OSS option | LocalStack | Quality | Notes |
|---|---|---|---|---|
| Cognito User Pools | `quay.io/keycloak/keycloak:24` (closest realm) or a local JWT issuer | localstack Pro — Cognito | partial | Most teams put auth behind a trait; ship `CognitoVerifier` (prod) + `LocalJwtVerifier` (dev). Don't try to fake Cognito at the SDK level. |
| Cognito Identity Pools | none | localstack Pro | partial | Returning AWS temp creds is AWS-only. |
| IAM | `localstack` Community — IAM | localstack | partial | Policies parse; enforcement doesn't happen in LocalStack Community. |
| IAM Identity Center | none | none | none viable | Enterprise SSO over org-level IAM. |
| Verified Permissions (Cedar) | `cedar-policy` (Rust crate — Cedar is written in Rust!) | none | behavior-compatible | Cedar is a Rust project; the engine is the same OSS code AWS runs. AWS service adds storage + management. |

**Rust idiom** for token verification:
```rust
pub trait TokenVerifier: Send + Sync {
    fn verify(&self, token: &str) -> Result<Claims, AuthError>;
}
// CognitoVerifier: fetch JWKS, verify RS256.
// LocalJwtVerifier: HS256 with shared dev secret.
```
Use `jsonwebtoken` for the JWT primitives.

---

## 19. Secrets / Config

| AWS | OSS option | LocalStack | Quality | Notes |
|---|---|---|---|---|
| Secrets Manager | `localstack` Community — Secrets Manager | localstack | **wire-compatible** | `aws-sdk-secretsmanager` with `.endpoint_url(...)`. Rotation lambdas are the only AWS-specific bit. |
| SSM Parameter Store | localstack Community — SSM | localstack | **wire-compatible** | `aws-sdk-ssm`. |
| AppConfig | localstack Pro | localstack Pro | partial | `aws-sdk-appconfigdata`. Staged rollouts + validators don't reproduce. |
| KMS | localstack Community — KMS | localstack | partial | `aws-sdk-kms`. Software keys only locally; HSM-backed is AWS-only. |

---

## 20. Workflow / Scheduling

| AWS | OSS option | LocalStack | Quality | Notes |
|---|---|---|---|---|
| Step Functions Standard | `amazon/aws-stepfunctions-local` (official) | localstack Pro | **wire-compatible** | `aws-sdk-sfn` with `.endpoint_url("http://stepfunctions-local:8083")`. ASL state machines deploy identically. |
| Step Functions Express | aws-stepfunctions-local | localstack Pro | partial | Express semantics not fully tested locally. |
| Step Functions Distributed Map | none | none | none viable | Distributed Map parallel-children behavior is AWS-only. |
| EventBridge Scheduler | localstack Pro | localstack Pro | partial | Schedules fire; cron precision differs. |
| MWAA (Managed Airflow) | `apache/airflow:2.10` | none | behavior-compatible | Airflow is Python-only — Rust ops invoke it via DAG-as-config; consider this a cross-language seam. |
| SWF | none | none | none viable | Legacy; no local. |

**Rust idiom** for in-process scheduling (local dev only): `tokio-cron-scheduler` or `apalis` (job queue with cron support).

---

## 21. Email / Notifications

| AWS | OSS option | LocalStack | Quality | Notes |
|---|---|---|---|---|
| SES | `mailhog/mailhog` or `axllent/mailpit` (SMTP catcher) | localstack Community — SES | partial | Two paths: `lettre` (SMTP — swap host via env, works both sides) or `aws-sdk-sesv2` (HTTP API — endpoint override). SMTP path is simpler. |
| SNS SMS | localstack Pro | localstack Pro | partial | Real SMS doesn't go anywhere locally. |
| SNS Mobile Push | none | localstack Pro | none viable | Real APNs/FCM needed. |
| Pinpoint | localstack Pro | localstack Pro | partial | Campaign orchestration partly stubbed. |

---

## 22. Observability

| AWS | OSS option | LocalStack | Quality | Notes |
|---|---|---|---|---|
| CloudWatch Logs | stdout (Fargate awslogs driver / Lambda automatic) | localstack | **wire-compatible** | Best practice: emit logs via `tracing` / `tracing-subscriber` to stdout. Runtime collects them. Direct `aws-sdk-cloudwatchlogs` calls are rare. |
| CloudWatch Metrics | embedded metric format (EMF) in logs, or `aws-sdk-cloudwatch` | localstack | partial | For metrics, `metrics` + `metrics-exporter-prometheus` to a Prom sidecar, OR EMF via `tracing` events. |
| CloudWatch Alarms | localstack Pro | localstack Pro | partial | Definitions accepted; triggering partial. |
| CloudWatch Synthetics / RUM | none | none | none viable | Real browser canaries + real-user telemetry. |
| X-Ray | `amazon/aws-xray-daemon` (UDP collector, no UI) | localstack Pro | partial | Standard pattern: instrument with `opentelemetry` + `tracing-opentelemetry`; OTLP exporter → ADOT → X-Ray (cloud) or → Jaeger (local). |
| CloudTrail | localstack Pro | localstack Pro | partial | Event logging partly stubbed. |
| Application Signals | none | none | none viable | Auto-instrument-magic is AWS-only. |

**Rust idiom** (OTel + tracing):
```rust
use opentelemetry_otlp::WithExportConfig;
let endpoint = std::env::var("OTEL_EXPORTER_OTLP_ENDPOINT").unwrap_or_else(|_| "http://jaeger:4317".into());
let tracer = opentelemetry_otlp::new_pipeline().tracing()
    .with_exporter(opentelemetry_otlp::new_exporter().tonic().with_endpoint(endpoint))
    .install_batch(opentelemetry_sdk::runtime::Tokio)?;
```

---

# Data / analytics

| AWS | OSS option | LocalStack | Quality | Notes |
|---|---|---|---|---|
| Redshift | `postgres:16` (very different perf) or `clickhouse/clickhouse-server` (closer columnar) | localstack Pro — Redshift API | partial | Use `sqlx` or `tokio-postgres` (Redshift speaks Postgres wire protocol). DISTKEY/SORTKEY syntax won't reproduce. |
| Redshift Serverless | same | localstack Pro | partial | Same. |
| Athena | `prestodb/presto` or `trinodb/trino` | localstack Pro — Athena | partial | `prusto` is a Rust Trino/Presto client. Same SQL dialect for most queries. |
| S3 Select | minio (no S3 Select equivalent) | localstack | none viable | Object-scan-with-SQL is AWS-only. |
| Glue Jobs (Spark) | `bitnami/spark:3.5` (Spark is JVM) | localstack Pro | partial | No native Rust Spark. Use `datafusion` (Rust SQL engine) for local prototyping; deploy to Glue via PySpark. Cross-language seam. |
| Glue Crawlers | none | localstack Pro | partial | Schema discovery best emulated with hand-defined schemas. |
| Glue Data Catalog | `apache/hive:3` Metastore | localstack Pro | partial | Hive Metastore is the protocol Glue Catalog implements. |
| Glue DataBrew | none | none | none viable | UI-driven. |
| Lake Formation | none | none | none viable | Permissions overlay is AWS-only. |
| EMR | `bitnami/spark:3.5` (Spark only) | localstack Pro | partial | Same as Glue Jobs — cross-language. |
| EMR Serverless | bitnami/spark | localstack Pro | partial | Same. |
| QuickSight | `metabase/metabase`, `superset` | none | none viable | Dashboard authoring won't port. |

**Rust idiom note**: Rust has world-class single-node OLAP via `datafusion` (Apache Arrow project) and `polars`. For team-scale analytics, those still ship to JVM-based services.

---

# ML / AI

| AWS | OSS option | LocalStack | Quality | Notes |
|---|---|---|---|---|
| SageMaker Training | `python:3.12` + your training script | localstack Pro | partial | Rust ML training is niche (`candle`, `burn`); most production training is Python on SageMaker. Cross-language seam. |
| SageMaker Real-Time Inference | run model in Rust `axum` + `candle` or `tract` | localstack Pro | partial | If you serve inference in Rust, the SageMaker contract (`/opt/ml/...`) can be mimicked locally. |
| SageMaker Serverless Inference | same | none | partial | Same. |
| SageMaker Batch Transform | run script over S3-pointed input | localstack Pro | partial | Same. |
| SageMaker Studio | none | none | none viable | Jupyter is Python-centric; Rust kernels (`evcxr_jupyter`) exist but Studio integrations don't follow. |
| SageMaker JumpStart / Canvas | none | none | none viable | Bundled model marketplace + no-code UI. |
| Bedrock — Claude/Llama/etc | `ollama/ollama` (local LLM) or `vllm/vllm-openai` | localstack Pro | partial | `aws-sdk-bedrockruntime` for cloud; `ollama-rs` for local. APIs differ per model. Consider wrapping behind a `LlmClient` trait. |
| Bedrock Knowledge Bases (RAG) | OpenSearch + your own retrieval | none | none viable | Orchestration is the value-add. |
| Bedrock Agents | none | localstack Pro | none viable | Tool-use orchestration is proprietary. |
| Bedrock Guardrails | `presidio` (Python) or custom Rust filters | none | none viable | Different filter philosophy. |
| Rekognition / Polly / Transcribe / Comprehend / Translate | various | localstack Pro | partial | Rust ML inference works (`candle`, `tract`, `whisper-rs`, `tts-rs`); models differ from AWS-managed. |
| Forecast / Personalize | none (Forecast deprecated) | none | none viable | — |

**Rust idiom for LLMs**:
```rust
pub trait LlmClient: Send + Sync {
    async fn complete(&self, prompt: &str) -> Result<String, LlmError>;
}
// BedrockClient (prod), OllamaClient (dev), RecordedClient (tests).
```

---

# IoT / edge

| AWS | OSS option | LocalStack | Quality | Notes |
|---|---|---|---|---|
| IoT Core (MQTT) | `eclipse-mosquitto:2` | localstack Pro — IoT | behavior-compatible | `rumqttc` is the leading async MQTT crate; `paho-mqtt` (FFI) for sync. Same client both sides. AWS-specific (thing shadow, jobs) absent. |
| IoT Core (HTTPS / WS) | `eclipse-mosquitto:2` with WS listener | localstack Pro | partial | `rumqttc` supports WS. |
| IoT Device Management | none | localstack Pro | none viable | Fleet operations AWS-specific. |
| IoT Device Defender | none | none | none viable | Anomaly detection on AWS-side data flow. |
| IoT Rules Engine | none | localstack Pro | partial | Rules tie IoT Core → other AWS services; needs ecosystem stubs. |
| Greengrass V2 | run `greengrass-runtime` in container (AWS provides) | none | behavior-compatible | Rust components are supported by Greengrass V2 since 2024. |
| FreeRTOS | run on local hardware or QEMU | none | wire-compatible | Rust-for-FreeRTOS is an emerging story (`embassy`, `rtic`); not specifically AWS-tied. |
| IoT Analytics / SiteWise / Events / TwinMaker / FleetWise | none | localstack Pro (some) | none viable | Domain-specific. |

---

# Summary: emulation-quality scoreboard

| Quality band | Count | Examples |
|---|---|---|
| **wire-compatible** | ~11 | S3, DynamoDB, SQS, SNS, Kinesis (producer), Secrets Manager, SSM Parameter Store, CloudWatch Logs (via stdout), Step Functions Standard, Aurora pgvector, Influx-flavored Timestream, FreeRTOS |
| **behavior-compatible** | ~10 | RDS/Aurora (Postgres/MySQL/MariaDB/SQL Server), ElastiCache (Redis/Memcached), MemoryDB, OpenSearch, Amazon MQ (RabbitMQ/ActiveMQ), MSK, IoT Core MQTT, Greengrass, Verified Permissions (Cedar) |
| **partial** | ~22 | Lambda, API Gateway, EventBridge, Cognito, KMS, AppConfig, Athena (Trino), Glue (Spark via cross-lang), X-Ray, SageMaker training/inference, Rekognition/Polly/Transcribe (different OSS models), Bedrock (different OSS models), CloudWatch Metrics |
| **none viable** | ~17 | Lambda@Edge, CloudFront, CloudFront Functions, Global Accelerator, IAM enforcement, IAM Identity Center, S3 Vectors, S3 Express One Zone, S3 Select, S3 Object Lambda, Aurora DSQL, DAX, Neptune Analytics, Kendra, Bedrock Knowledge Bases / Agents / Guardrails, CloudWatch Synthetics / RUM / Application Signals, CloudFront Functions, SNS Mobile Push, IoT Device Management / Defender / Analytics / SiteWise / TwinMaker / FleetWise, Forecast, Personalize, SageMaker JumpStart / Canvas, Step Functions Distributed Map |

**Rust-specific notes vs Python coverage**:
- Trivially-equivalent counts (~11 wire / ~10 behavior) are very close to Python's (~12 / ~10). The AWS SDK's `endpoint_url` builder is the universal Trivial-band enabler.
- A few cells are *less* mature on the Rust side and worth flagging:
  - **MSK with IAM auth** — Rust signer crate exists (`aws-msk-iam-sasl-signer`) but is less battle-tested than Python's.
  - **Oracle RDS** — Rust crates require Oracle Instant Client; awkward toolchain.
  - **MWAA / Glue / SageMaker training** — Python-shaped services. Rust users orchestrate them cross-language.
  - **Lambda@Edge / CloudFront Functions** — JS-only at the edge runtime, even in 2026.
- A few cells are *cleaner* on the Rust side:
  - **Verified Permissions / Cedar** — Cedar is itself written in Rust; the OSS engine is canonical.
  - **Lambda runtime** — `lambda_http` lets one axum app run as a Lambda binary or a standalone HTTP server.
  - **Edge / IoT** — Rust dominates embedded; FreeRTOS, Greengrass components, mosquitto all first-class.

**Implications for caravan** (developed in `caravan_abstraction_v2.md`):
- The ~21 wire-or-behavior-compatible services are the obvious abstraction targets — `endpoint_url` / DSN swap, no SDK needed.
- The ~22 partial services either need a thin trait abstraction at the user's code boundary (`LlmClient`, `TokenVerifier`) or honest cloud-only marking.
- The ~17 none-viable services are `cloud_only: true` in the IR — caravan refuses to bind them locally.

See `rust_api_diffs.md` for the actual code-diff per pair, `mapping_rust_to_aws.md` for the reverse direction (which container plays the AWS role in dev), and `caravan_abstraction_v2.md` for the synthesized PoC scope.
