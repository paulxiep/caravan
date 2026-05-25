# Rust Stack → AWS Mapping

> ⚠️ **HISTORICAL — pre-SDK research notes.** Current SDK contract lives at [`../rpc/rust/`](../rpc/rust/) (caravan-rpc + caravan-rpc-macros at 0.1.0). Authoritative docs are [`thesis.md`](thesis.md) and [`development_plan.md`](development_plan.md). Snapshot 2026-05-16; do not assume any specific row is current.

> **Snapshot date: 2026-05-16.** AWS prices reference `aws_service_groups.md`.
> **Scope**: Rust ecosystem only. Pragmatic Rust-first — components without a mature Rust client are out of scope for this round (e.g., Spark-based ETL stays Python).

Question this file answers: *"My Rust app and its docker-compose dependencies — what does each piece become on AWS?"*

Each row lists three tiers — **Cheapest fit** (PoC, hobby, dev/staging), **Production fit** (typical "real app" choice), **Premium fit** (when scale, latency, or compliance dominate). Tier choice depends on traffic, budget, and how much you trust AWS-specific lock-in. Where multiple AWS services are mentioned, see `aws_service_groups.md` for cost/latency detail.

The Rust-side equivalent of "boto3 with `endpoint_url`" is the `aws-sdk-rust` builder pattern: rebuild the client config with `.endpoint_url(...)` when the env var is set. All entries below assume this pattern is wired once at startup.

---

## Datastores

### postgres
- **Local**: `postgres:16-alpine`. Rust clients: `sqlx` (preferred async), `tokio-postgres` (raw async), `sea-orm` (async ORM), `diesel` (sync ORM, async-friendly via `diesel-async`).
- **Cheapest fit**: RDS Postgres db.t4g.micro (~$12/mo) — single-AZ.
- **Production fit**: Aurora Postgres Serverless v2 (auto-pause for dev/staging; 0.5–8 ACU for prod). Same DSN, same SQL, same Rust crate.
- **Premium fit**: Aurora Postgres provisioned multi-AZ with I/O-Optimized cluster + 1–3 read replicas. Aurora DSQL for active-active multi-region.
- **Decision criterion**: under 100k req/day → RDS micro is fine. Above 1 RPS sustained, Aurora Serverless v2 wins. Premium only when you can defend the cost.
- **Gotcha (Rust-specific)**: `sqlx::query!` macros verify at compile time and need a live database (or pre-cached `sqlx prepare` metadata) during `cargo build`. CI must `docker-compose up -d postgres` before building. `tokio-postgres` and `sea-orm` don't have this constraint.

### mysql / mariadb
- **Local**: `mysql:8`, `mariadb:11`. Rust: `sqlx`, `mysql_async`, `diesel` (with `mysql` feature).
- **Cheapest fit**: RDS MySQL db.t4g.micro (~$12/mo).
- **Production fit**: Aurora MySQL Serverless v2.
- **Premium fit**: Aurora MySQL with global database + cross-region read replicas.
- **Decision criterion**: Same as postgres.

### mongodb
- **Local**: `mongo:7`. Rust: `mongodb` (official, async).
- **Cheapest fit**: DocumentDB db.t4g.medium (~$72/mo) — cheapest viable Mongo-compatible.
- **Production fit**: DocumentDB multi-AZ with 1+ replicas.
- **Premium fit**: MongoDB Atlas on AWS Marketplace — full Mongo API + Atlas features.
- **Decision criterion**: DocumentDB lacks ~30% of aggregation operators. If your code uses modern Mongo features, Atlas-on-AWS over DocumentDB.

### redis (as cache)
- **Local**: `redis:7-alpine`. Rust: `redis-rs` (with `tokio-comp` feature), `fred` (high-throughput), `deadpool-redis` (pooling).
- **Cheapest fit**: ElastiCache Redis cache.t4g.micro (~$12/mo).
- **Production fit**: ElastiCache Redis Serverless or cluster-mode-enabled with reader nodes.
- **Premium fit**: MemoryDB for Redis (durable Redis as primary store).
- **Decision criterion**: ElastiCache Serverless is the default. MemoryDB only when you've explicitly chosen Redis as system of record.

### redis (as pub/sub or queue)
- **Local**: same image. Rust: `redis::aio::Connection.pubsub()` or use `apalis` for queue.
- **Cheapest fit**: ElastiCache Redis (keep pub/sub).
- **Production fit**: Replace with SNS + SQS fan-out (rewrite via `aws-sdk-sqs` / `aws-sdk-sns`), or `aws-sdk-kinesis` for streams.
- **Premium fit**: EventBridge with content-based routing.
- **Decision criterion**: Redis pub/sub is fire-and-forget; SQS/SNS is durable. For anything you care about losing, switch.

### memcached
- **Local**: `memcached:1`. Rust: `memcache` (sync) or `async-memcached`.
- **Cheapest fit**: ElastiCache Memcached.
- **Production fit / Premium fit**: ElastiCache Redis Serverless. Memcached has no compelling advantage in 2026.
- **Decision criterion**: pick Redis.

### minio (S3-compatible)
- **Local**: `minio/minio`. Rust: `aws-sdk-s3` with `.endpoint_url(...)` + `.force_path_style(true)`. Alternative: `object_store` (multi-backend abstraction).
- **Cheapest fit**: S3 Standard.
- **Production fit**: S3 Standard + Intelligent-Tiering.
- **Premium fit**: S3 + CloudFront for read-heavy + S3 Replication cross-region for DR.
- **Decision criterion**: cleanest migration in this whole file — endpoint URL swap and `force_path_style` toggle.
- **Gotcha (Rust-specific)**: `force_path_style(true)` is required for minio; without it the SDK does virtual-host-style addressing that minio rejects. Set whenever `S3_ENDPOINT_URL` is set.

### opensearch
- **Local**: `opensearchproject/opensearch:2`. Rust: `opensearch` (official Rust client, async).
- **Cheapest fit**: OpenSearch Service t3.small.search single-node (~$73/mo) — dev only.
- **Production fit**: OpenSearch Service provisioned 3-node cluster.
- **Premium fit**: OpenSearch Serverless for genuinely spiky workloads.
- **Decision criterion**: Same Rust crate, URL swap. The `elasticsearch` crate also exists but `opensearch` is the canonical post-fork choice.

### qdrant / weaviate / chroma (dedicated vector DBs)
- **Local**: `qdrant/qdrant`, `cr.weaviate.io/semitechnologies/weaviate`, `chromadb/chroma`. Rust: `qdrant-client` (official, mature), `weaviate-community` (community), `chromadb` (community, limited).
- **Cheapest fit**: Aurora Postgres pgvector. <10M vectors fits on db.t4g.medium.
- **Production fit**: OpenSearch Service with k-NN plugin (HNSW).
- **Premium fit**: S3 Vectors for cold storage, or run the vector DB on EKS via the vendor's marketplace AMI / Helm chart.
- **Decision criterion**: if <10M vectors and you already run Postgres, pgvector. Above 100M vectors or strict <50 ms latency SLOs, dedicated managed beats AWS-native.

### pgvector (local)
- **Local**: `pgvector/pgvector:pg16`. Rust: `pgvector` crate integrates with `sqlx` / `tokio-postgres` / `diesel`.
- **Cheapest / Production fit**: Aurora Postgres has pgvector built-in. Same SQL, same Rust code.
- **Premium fit**: Aurora Postgres Optimized Reads (NVMe cache for HNSW).
- **Decision criterion**: easiest AWS port in the vector category.

---

## Messaging

### rabbitmq
- **Local**: `rabbitmq:3-management`. Rust: `lapin` (canonical async AMQP, mature).
- **Cheapest fit**: Amazon MQ for RabbitMQ mq.t3.micro single-instance (~$15/mo).
- **Production fit**: Amazon MQ multi-AZ cluster — keeps AMQP semantics intact.
- **Premium fit**: Same. Cluster size up.
- **Alternative path (cheaper but code changes)**: Move to SQS via `aws-sdk-sqs`. Different crate, different consumer model (polling).
- **Decision criterion**: If you rely on AMQP-specific features (priority queues, DLX with topic routing) → Amazon MQ. If you used RabbitMQ as a generic queue → switch to SQS.

### kafka
- **Local**: `bitnami/kafka:3.7`. Rust: `rdkafka` (mature, librdkafka FFI — production choice) or `rskafka` (pure Rust, lighter features).
- **Cheapest fit**: MSK Serverless (~$540/mo cluster floor).
- **Production fit**: MSK provisioned `kafka.m7g.large` × 3 brokers.
- **Premium fit**: MSK provisioned with tiered storage + MSK Connect. Confluent Cloud on AWS Marketplace for full Kafka + Schema Registry + ksqlDB.
- **Cheaper alternative**: Kinesis Data Streams on-demand via `aws-sdk-kinesis` — different crate, no consumer-group rebalancing.
- **Decision criterion**: same as Python — Kafka is expensive in AWS. Drop to Kinesis if you can live without consumer groups.
- **Gotcha (Rust-specific)**: MSK with IAM SASL needs `aws-msk-iam-sasl-signer-rust`, which is less battle-tested than the Python/Java equivalents. Prefer SCRAM-SHA-512 or mTLS for MSK auth from Rust until further notice.

### nats
- **Local**: `nats:2-alpine`. Rust: `async-nats` (official, mature).
- **Cheapest fit**: Run NATS yourself on ECS Fargate (~$30/mo). Or migrate to SNS + SQS.
- **Production fit**: NATS on EKS or 3-node EC2 fleet (NATS clustering is straightforward).
- **Premium fit**: Synadia Cloud on AWS Marketplace if you need managed JetStream.
- **Decision criterion**: NATS has no AWS-managed equivalent. Self-host or migrate the abstraction.

---

## Rust app processes

### axum
- **Local**: `rust:1.84-slim` or static `FROM scratch` image. Listens via `tokio::net::TcpListener` + `axum::serve`. Often behind `nginx` for TLS.
- **Cheapest fit**: Lambda + API Gateway HTTP via `lambda_http` (axum router → Lambda binary). Free tier covers 1M req/mo.
- **Production fit**: ECS Fargate task behind ALB — keeps long-lived connections, websockets, SSE working naturally. A small Rust binary (~10 MB) starts in <1 s, so Fargate scale-out is responsive.
- **Premium fit**: App Runner for managed Fargate; ECS on EC2 + Spot for cost optimization.
- **Decision criterion**: axum + `lambda_http` is the cleanest "one codebase, both deployment shapes" story in the AWS ecosystem. Lambda kills long-lived websockets and lifespan-style startup; Fargate keeps them. Pick per workload, not per framework.

### actix-web
- **Local**: same image story. Actix's actor model needs a Tokio runtime (provided by `actix-rt`).
- **Cheapest fit**: Fargate behind ALB. Actix-web on Lambda is awkward — no first-class `lambda_http` adapter as of 2026.
- **Production fit**: Same.
- **Premium fit**: Same.
- **Decision criterion**: Actix-web is the fastest framework in synthetic benchmarks; choose for raw RPS-per-core. For Lambda compatibility, axum is the pragmatic default.

### rocket / warp / poem / salvo
- **Local**: container.
- **Cheapest / Production fit**: Fargate behind ALB. None have first-class Lambda adapters.
- **Decision criterion**: pick framework on ergonomics; for Lambda-shaped workloads use axum.

### apalis (background worker)
- **Local**: separate Rust binary running `apalis::Worker` with a Redis or Postgres backend.
- **Cheapest fit**: Apalis + ElastiCache Redis (or RDS Postgres) on Fargate.
- **Production fit**: Apalis with SQS backend (`apalis-sqs` extension) — replaces Redis broker with SQS. Workers on Fargate.
- **Premium fit**: Replace apalis entirely with Step Functions (DAG workflows) or Lambda triggered from SQS (short tasks).
- **Decision criterion**: Apalis + SQS is the modern "Rust background jobs in AWS" pattern. Equivalent to "Celery + SQS" for Python.

### faktory-rs / sidekiq-rs
- **Local**: separate worker binary; broker = Faktory or Sidekiq instance.
- **Cheapest fit / Production fit**: Niche; if you're using Faktory/Sidekiq specifically, self-host the broker on Fargate or migrate to apalis + SQS.
- **Decision criterion**: usually you want apalis instead — first-class Rust ecosystem fit.

### tokio-cron-scheduler / apalis cron
- **Local**: in-process scheduler inside a long-running Rust container.
- **Cheapest fit**: EventBridge Scheduler → Lambda (one schedule per job) via `aws-sdk-scheduler` for deploy.
- **Production fit**: Same.
- **Premium fit**: Step Functions Standard for jobs needing durable orchestration.
- **Decision criterion**: in-process schedulers fight multi-instance deployments. EventBridge Scheduler is strictly better in AWS.

### cron-in-container
- **Local**: a container with `cron` running a `cargo run` binary.
- **Cheapest fit / Production fit**: EventBridge Scheduler → Lambda or → ECS RunTask.
- **Decision criterion**: never use cron container in AWS.

### lambda_runtime / lambda_http
- **Local**: `cargo lambda watch` — runs the handler binary under a local Lambda emulator on port 9000.
- **Cheapest fit / Production fit**: Lambda. Use container-image Lambda (`provided.al2023` base + `cargo lambda build`) — Rust binaries are small (~10 MB) and cold-start fast (<50 ms typical, vs ~500 ms for Python).
- **Decision criterion**: Lambda's officially supported Rust runtime (GA Nov 2025). For HTTP routes, pair with `lambda_http` to share code between Lambda and standalone server deployments.

---

## Adjacent infrastructure

### nginx / traefik (reverse proxy)
- **Local**: `nginx:alpine` or `traefik:v3`.
- **Cheapest fit**: API Gateway HTTP (paths via routes, TLS via ACM, static via S3+CloudFront).
- **Production fit**: ALB (path + host routing, TLS via ACM) + CloudFront for static.
- **Premium fit**: Add AWS WAF + Shield Advanced.
- **Decision criterion**: ALB for service-fronting; API Gateway when routes are Lambda-backed.

### keycloak
- **Local**: `quay.io/keycloak/keycloak:24`. Rust: `openidconnect` crate for OIDC client; `jsonwebtoken` for token verification.
- **Cheapest fit**: Cognito User Pools (first 10k MAU free).
- **Production fit**: Cognito User Pools + Identity Pools. Or self-host Keycloak on Fargate + RDS Postgres.
- **Premium fit**: Auth0 / Okta / WorkOS on AWS Marketplace.
- **Decision criterion**: Cognito wins on price + IAM integration at small scale; loses to Keycloak on flexibility above 50k users.
- **Code change** (Rust): no first-party `aws-sdk-cognito` token *verification* helper — use `jsonwebtoken` + JWKS fetch directly. Auth lives behind a `TokenVerifier` trait with `CognitoVerifier` (prod) and `LocalJwtVerifier` (dev).

### vault (Hashicorp)
- **Local**: `hashicorp/vault:1.16`. Rust: `vaultrs` or `hashicorp_vault` crate.
- **Cheapest fit**: SSM Parameter Store (free for Standard tier) for static config; Secrets Manager for rotating secrets.
- **Production fit**: SSM Parameter Store + Secrets Manager + KMS.
- **Premium fit**: HCP Vault Dedicated on AWS Marketplace.
- **Decision criterion**: 90% of Vault use in startups is just secret storage — SSM covers it.
- **Code change**: `vaultrs` → `aws-sdk-ssm` / `aws-sdk-secretsmanager`. Significant rewrite.

### mailhog / maildev (dev SMTP catcher)
- **Local**: `mailhog/mailhog` or `axllent/mailpit`. Rust: `lettre` (canonical SMTP crate, async support via `tokio1` feature).
- **Cheapest fit / Production fit**: SES via SMTP (works with `lettre`) or `aws-sdk-sesv2` (HTTP API).
- **Premium fit**: SES with Virtual Deliverability Manager + dedicated IP pool.
- **Decision criterion**: SMTP path via `lettre` works identically against mailhog and SES; one env-driven host swap. SES requires production access approval (sandbox-to-prod) — non-trivial paperwork.

### prometheus / grafana
- **Local**: `prom/prometheus` + `grafana/grafana`. Rust: `metrics` + `metrics-exporter-prometheus`, or `prometheus-client` crate directly.
- **Cheapest fit**: CloudWatch Metrics (custom). Use EMF format from logs to avoid PutMetricData costs.
- **Production fit**: Amazon Managed Prometheus (AMP) + Amazon Managed Grafana (AMG). Same PromQL.
- **Premium fit**: AMP/AMG + extended retention + workspace federation.
- **Decision criterion**: If your team lives in PromQL, AMP/AMG is the low-friction port. CloudWatch Metrics is fine for <50 custom metrics.

### loki / jaeger / tempo (logs and traces)
- **Local**: `grafana/loki`, `jaegertracing/all-in-one`, `grafana/tempo`. Rust: `tracing` + `tracing-loki`, `tracing-opentelemetry` + `opentelemetry-otlp`.
- **Cheapest fit**: CloudWatch Logs (stdout via awslogs driver); X-Ray (traces) via OTel exporter.
- **Production fit**: CloudWatch Logs + X-Ray + Application Signals. Or push to Datadog/Honeycomb/Grafana Cloud via OTel collector.
- **Premium fit**: AWS Distro for OpenTelemetry → AMP (metrics) + CloudWatch Logs + X-Ray.
- **Decision criterion**: OpenTelemetry is the abstraction that survives a vendor swap. Same `tracing` code; only the OTLP endpoint flips.

### shuttle.rs (alternative IaC path)
- **Local**: `cargo shuttle run` — Shuttle's CLI provisions local services declared via annotations on your `#[shuttle_runtime::main]` function.
- **Cheapest / Production fit**: Shuttle Cloud (AWS-backed since the 2026 relaunch) — `cargo shuttle deploy`.
- **Decision criterion**: Shuttle is the native-Rust "infrastructure-as-crates" path. If you want a single-vendor "ship my Rust binary plus infra" experience and don't need multi-cloud or auditable IaC, Shuttle is the simplest option. Not a fit if you need polyglot services, custom AWS resources Shuttle hasn't wrapped, or your security team requires Terraform-state visibility.

---

## Summary table

| Local component (Rust) | Cheapest fit | Production fit | Notes |
|---|---|---|---|
| postgres | RDS Postgres micro | Aurora Postgres Serverless v2 | DSN swap; sqlx compile-time check needs DB at build |
| mysql/mariadb | RDS MySQL micro | Aurora MySQL Serverless v2 | Same |
| mongodb | DocumentDB t4g.medium | DocumentDB or Atlas-on-AWS | Atlas if you use modern Mongo features |
| redis (cache) | ElastiCache micro | ElastiCache Serverless | `redis-rs` or `fred` |
| redis (pubsub) | ElastiCache | SNS+SQS (rewrite) | Durability differs |
| memcached | ElastiCache Memcached | Switch to Redis | No reason to stay |
| minio | S3 | S3 + Intelligent-Tiering | `endpoint_url` + `force_path_style(true)` |
| opensearch | OpenSearch t3.small | OpenSearch r6g cluster | `opensearch` crate |
| pgvector | Aurora Postgres | Aurora Postgres | Built-in extension |
| qdrant/weaviate/chroma | pgvector | OpenSearch k-NN or vendor cloud | qdrant-client is the most mature Rust client |
| rabbitmq | Amazon MQ micro | Amazon MQ cluster | `lapin`; or migrate to SQS |
| kafka | MSK Serverless ($540 floor) | MSK provisioned | `rdkafka`; SCRAM auth, IAM auth less mature in Rust |
| nats | Self-host on Fargate | Self-host on EKS or migrate to SNS+SQS | `async-nats` |
| axum | Lambda + lambda_http | Fargate behind ALB | One codebase, two shapes |
| actix-web | Fargate | Fargate | No first-class Lambda adapter |
| rocket/warp/poem/salvo | Fargate | Fargate | Same |
| apalis worker | Fargate + ElastiCache Redis | Fargate + SQS backend (apalis-sqs) | Standard Rust background-jobs pattern |
| faktory-rs / sidekiq-rs | Fargate + self-hosted broker | Same | Niche; prefer apalis |
| tokio-cron-scheduler | EventBridge Scheduler → Lambda | Same | Don't run in-process in AWS |
| cron container | EventBridge Scheduler → Lambda or RunTask | Same | Never use cron container |
| lambda_runtime / lambda_http | Lambda (container-image) | Lambda | GA Nov 2025; fast cold-start |
| nginx/traefik | API Gateway HTTP | ALB | ALB for service-fronting |
| keycloak | Cognito (first 10k MAU free) | Cognito or self-host Keycloak | `TokenVerifier` trait pattern |
| vault | SSM Parameter Store (free) | SSM + Secrets Manager + KMS | Significant code rewrite |
| mailhog | SES | SES + dedicated IP | `lettre` SMTP works both sides |
| prometheus | CloudWatch Metrics | Amazon Managed Prometheus | `metrics-exporter-prometheus` |
| grafana | CloudWatch dashboards | Amazon Managed Grafana | Dashboards port directly |
| loki | CloudWatch Logs | CloudWatch Logs | Or OTel → Grafana Cloud |
| jaeger/tempo | X-Ray | X-Ray + Application Signals | OTel via `tracing-opentelemetry` |
| shuttle.rs | Shuttle Cloud (AWS-backed) | Shuttle Cloud | Alternative IaC path; Rust-only |

See `mapping_aws_to_rust.md` for the reverse direction (which container plays the AWS role in dev), `rust_api_diffs.md` for the per-pair Rust code diff, and `caravan_abstraction_v2.md` for the synthesized PoC scope.
