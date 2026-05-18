# TypeScript Stack → AWS Mapping

> **Snapshot date: 2026-05-16.** AWS prices reference `aws_service_groups.md`.
> **Scope**: TypeScript / JavaScript ecosystem (Node 22 + Bun, with Deno as container-baseline). Python and Rust mirrors live in `mapping_python_to_aws.md` / `mapping_rust_to_aws.md`.
> **Framing**: this file is TypeScript ecosystem evidence feeding into `thesis.md` (conceptual home) and `caravan_abstraction_v3.md` (long-form derivation; supersedes v2). The Cheapest/Production/Premium tier labels below are the **operator's intuition**; they map onto v3 §6's explicit yaml `tier:` vocabulary (`db.sql tier: dev | prod-small | prod | premium | global`, `bucket class: standard | intelligent | …`, etc.) — that mapping is shown inline per row and rolled up in the closing summary table.

Question this file answers: *"My TS app and its docker-compose dependencies — what does each piece become on AWS?"*

Each row lists three tiers — **Cheapest fit** (PoC, hobby, dev/staging), **Production fit** (typical "real app" choice), **Premium fit** (when scale, latency, or compliance dominate). Tier choice depends on traffic, budget, and how much you trust AWS-specific lock-in. Where multiple AWS services are mentioned, see `aws_service_groups.md` for cost/latency detail; for cloud↔local code-diffs see `typescript_api_diffs.md`.

**Runtime note**: snippets assume Node 22 unless flagged. **Bun** (`oven/bun:1`) is a drop-in for most of these; Lambda support is community/experimental (`bun-lambda` custom runtime). **Deno** is container-baseline-only — works on Fargate via `deno compile`; Lambda support niche.

---

## Datastores

### postgres
- **Local**: `postgres:16-alpine`. TS clients: `pg` (node-postgres, the canonical low-level driver), `postgres` (porsager/postgres, more ergonomic), `prisma` (ORM + migrations), `drizzle-orm` (typed SQL builder), `kysely` (typed query builder), `typeorm` (decorator ORM).
- **Cheapest fit**: RDS Postgres db.t4g.micro (~$12/mo) — single-AZ.
- **Production fit**: Aurora Postgres Serverless v2 (auto-pause for dev/staging; 0.5–8 ACU for prod). Drop-in driver compatibility.
- **Premium fit**: Aurora Postgres provisioned multi-AZ with I/O-Optimized cluster config + 1–3 read replicas. Aurora DSQL for active-active multi-region.
- **Decision criterion**: under 100k req/day → RDS micro is fine. Above 1 RPS sustained, Aurora Serverless v2 wins on the operational story. Premium only when you can defend the cost.
- **v3 yaml**: `db.sql tier: dev` (RDS micro) · `prod-small` (Aurora Serverless v2) · `prod` (Aurora provisioned multi-AZ) · `premium` (multi-AZ + read replicas + I/O-Optimized) · `global` (Aurora Global / DSQL). Tier 0 — DSN swap via `DATABASE_URL`.
- **Gotcha (TS-specific)**: Prisma's migration engine is a per-platform native binary. Multi-arch Docker builds need `binaryTargets = ["native", "linux-musl-arm64-openssl-3.0.x", "linux-musl-openssl-3.0.x"]` in `schema.prisma`. Missing the right target is the most common "works locally, breaks in Fargate" failure for Prisma users.
- **Gotcha (runtime)**: `pg` requires bundling adjustments under Bun (it relies on Node's `net`/`tls` modules — works, but cold-start is slower than `postgres` npm).

### mysql / mariadb
- **Local**: `mysql:8`, `mariadb:11`. TS: `mysql2` (preferred async, supports prepared statements), `prisma`, `drizzle`, `typeorm`.
- **Cheapest fit**: RDS MySQL db.t4g.micro (~$12/mo).
- **Production fit**: Aurora MySQL Serverless v2 (MariaDB code mostly works against Aurora MySQL — check stored proc syntax).
- **Premium fit**: Aurora MySQL with global database + cross-region read replicas.
- **Decision criterion**: Same as postgres. MariaDB-specific features (JSON path expressions, certain spatial functions) may not survive Aurora MySQL — verify per workload.
- **v3 yaml**: `db.sql engine: mysql tier: dev | prod-small | prod | premium | global`. Tier 0 — DSN swap via `DATABASE_URL`.

### mongodb
- **Local**: `mongo:7`. TS: `mongodb` (official driver), `mongoose` (ODM with schema validation).
- **Cheapest fit**: DocumentDB db.t4g.medium (~$72/mo) — cheapest viable Mongo-compatible.
- **Production fit**: DocumentDB multi-AZ with 1+ replicas.
- **Premium fit**: MongoDB Atlas on AWS (run via AWS Marketplace) — full Mongo API + Atlas features (Search, Vector Search, Triggers). Often the right call when you actually depend on Mongo aggregations.
- **Decision criterion**: DocumentDB advertises Mongo wire protocol but lacks ~30% of aggregation operators (esp. `$lookup` semantics, `$facet`, `$bucket`). If your code uses modern Mongo features, Atlas-on-AWS over DocumentDB.

### redis (as cache)
- **Local**: `redis:7-alpine`. TS: `ioredis` (cluster-aware, the production default), `redis` (node-redis v4+, official).
- **Cheapest fit**: ElastiCache Redis cache.t4g.micro (~$12/mo).
- **Production fit**: ElastiCache Redis Serverless (auto-scale, no capacity planning) or ElastiCache cluster-mode-enabled with reader nodes.
- **Premium fit**: MemoryDB for Redis (durable Redis as primary store) — costs ~6× ElastiCache but eliminates a separate DB tier.
- **Decision criterion**: 99% of "I need Redis" cases want ElastiCache Serverless. MemoryDB only when you've explicitly chosen Redis as system of record.
- **v3 yaml**: deferred to v1.x — see v3 §6 tier table (`cache: tier: dev | prod-small | prod-cluster | serverless | memorydb`). Tier 0 — DSN swap via `REDIS_URL`.

### redis (as pub/sub or queue)
- **Local**: same image. TS: `ioredis` `.subscribe()` / `.xadd()` for Streams, or `bullmq` (the standard TS queue library, Redis-backed).
- **Cheapest fit**: ElastiCache Redis (keep pub/sub working as-is, BullMQ + Redis broker).
- **Production fit**: Replace pub/sub with SNS + SQS fan-out, or replace stream with Kinesis Data Streams. Different code; better at-least-once semantics and decoupled scaling. BullMQ has an experimental SQS backend (`bullmq-sqs`) for keeping the queue abstraction stable.
- **Premium fit**: EventBridge with content-based routing for the pub/sub layer.
- **Decision criterion**: Redis pub/sub is fire-and-forget (drops if no subscriber). SQS/SNS is durable. For anything you care about losing, do not keep Redis pub/sub in AWS.
- **v3 yaml**: when migrating, declare `topic:` (→ SNS) for fan-out and `queue:` (→ SQS, ElasticMQ locally) for point-to-point. Both Tier 0 once you've moved off Redis pub/sub.

### memcached
- **Local**: `memcached:1`. TS: `memjs` (preferred async), `memcached` (older sync).
- **Cheapest fit**: ElastiCache Memcached cache.t4g.micro (~$12/mo).
- **Production fit / Premium fit**: ElastiCache Redis Serverless. Memcached has no compelling advantage over Redis in 2026. Switch.
- **Decision criterion**: there isn't one — pick Redis.

### minio (S3-compatible)
- **Local**: `minio/minio`. TS: `@aws-sdk/client-s3` with `{ endpoint: process.env.S3_ENDPOINT_URL, forcePathStyle: true }`.
- **Cheapest fit**: S3 Standard. Free tier covers 5 GB.
- **Production fit**: S3 Standard + Intelligent-Tiering for unknown-pattern data.
- **Premium fit**: S3 + CloudFront for read-heavy + S3 Replication cross-region for DR.
- **Decision criterion**: minio is the closest-to-trivial AWS migration in this whole file — same `@aws-sdk/client-s3` code, env-driven endpoint. The one trap is per-object behavior under concurrent writes (minio's eventual consistency model differs from S3 in failure modes).
- **v3 yaml**: `bucket class: standard | intelligent | standard-ia | one-zone-ia | glacier-instant | glacier-flexible | glacier-deep-archive`; `lifecycle:` for transitions; `variant: standard | express-one-zone | vectors` for the rare typed-different cases. Tier 0 — `S3_ENDPOINT_URL` + `forcePathStyle` swap.
- **Edge cases moving to v3 cloud_only**: S3 + CloudFront for "Production fit" reads becomes `static_site` primitive (v1.2 per v3 §10); CloudFront standalone is `cloud_only: cloudfront`.

### opensearch
- **Local**: `opensearchproject/opensearch:2` (or `elasticsearch:8`). TS: `@opensearch-project/opensearch` (official), `@elastic/elasticsearch` (Elastic's own; differs post-fork).
- **Cheapest fit**: OpenSearch Service t3.small.search single-node (~$73/mo) — viable for dev; not for prod.
- **Production fit**: OpenSearch Service provisioned 3-node cluster (r6g.large.search × 3 ≈ $400/mo) with dedicated master nodes for stability above 10 data nodes.
- **Premium fit**: OpenSearch Serverless if your workload is genuinely spiky and the $1k/mo floor doesn't sting.
- **Decision criterion**: OpenSearch's API is a fork of Elasticsearch 7.10 — modern Elasticsearch ≥8 code (especially using x-pack security or vector search APIs that differ) may need shims. `@opensearch-project/opensearch` is straightforward; `@elastic/elasticsearch` ≥8 will refuse to connect to OpenSearch without `MAJOR_VERSION_MISMATCH` overrides.

### qdrant / weaviate / chroma (dedicated vector DBs)
- **Local**: `qdrant/qdrant`, `cr.weaviate.io/semitechnologies/weaviate`, `chromadb/chroma`. TS clients: `@qdrant/js-client-rest`, `weaviate-ts-client`, `chromadb`.
- **Cheapest fit**: Aurora Postgres pgvector. <10M vectors fits easily on a db.t4g.medium.
- **Production fit**: OpenSearch Service with the k-NN plugin (use HNSW). Best when you already run OpenSearch.
- **Premium fit**: S3 Vectors for tens-of-billions cold storage. Or run the dedicated vector DB on EKS/ECS via the vendor's own AWS Marketplace AMI / Helm chart (Qdrant Cloud, Weaviate Cloud — both have AWS-native managed offerings).
- **Decision criterion**: if you're <10M vectors and your team already runs Postgres, pgvector is the cheapest abstraction shrinkage. Above 100M vectors or strict <50 ms latency SLOs, dedicated managed (Pinecone, Qdrant Cloud) beats AWS-native today.

### pgvector (local)
- **Local**: `pgvector/pgvector:pg16`. TS: `pg` + `pgvector` npm (works with `drizzle`, `kysely`, `prisma` via custom types).
- **Cheapest / Production fit**: Aurora Postgres has pgvector extension built-in. Same SQL, same TS code — set `CREATE EXTENSION vector;`.
- **Premium fit**: Aurora Postgres Optimized Reads instances (caches working set in NVMe; helps HNSW search latency).
- **Decision criterion**: easiest AWS port in the vector category. If your vector layer is pgvector locally, keep it pgvector in AWS until it stops scaling.

---

## Messaging

### rabbitmq
- **Local**: `rabbitmq:3-management`. TS: `amqplib` (canonical), `amqp-connection-manager` (auto-reconnect wrapper).
- **Cheapest fit**: Amazon MQ for RabbitMQ mq.t3.micro single-instance (~$15/mo).
- **Production fit**: Amazon MQ multi-AZ cluster (3-node) — keeps your AMQP semantics intact.
- **Premium fit**: Same. Cluster size up.
- **Alternative path (cheaper but code changes)**: Move to SQS Standard (replace `amqplib` consumer with `@aws-sdk/client-sqs` polling) + SNS for fan-out. Often the right call if RabbitMQ features used were just "queue with workers."
- **Decision criterion**: If you rely on AMQP-specific features (priority queues, dead-letter exchanges with topic routing, JMS-like selectors) → Amazon MQ. If you used RabbitMQ as a generic queue → switch to SQS, save money and ops effort.
- **v3 yaml**: when migrating to SQS, declare `queue kind: standard | fifo`. Tier 0 — `SQS_ENDPOINT_URL` swap (ElasticMQ locally). Sticking with AMQP keeps `amqplib` Tier 0 against the rabbitmq container ↔ Amazon MQ (DSN swap).

### kafka
- **Local**: `bitnami/kafka:3.7` or `confluentinc/cp-kafka:7`. TS: `kafkajs` (preferred pure-JS), `confluent-kafka-javascript` (librdkafka FFI, 2024+ GA).
- **Cheapest fit**: MSK Serverless ($540/mo cluster floor — there's no "tiny" Kafka in AWS).
- **Production fit**: MSK provisioned `kafka.m7g.large` × 3 brokers (≈$500/mo brokers + storage).
- **Premium fit**: MSK provisioned with tiered storage + MSK Connect for source/sink connectors. Confluent Cloud on AWS Marketplace for full Kafka + Schema Registry + ksqlDB.
- **Cheaper non-Kafka alternative**: Kinesis Data Streams on-demand via `@aws-sdk/client-kinesis`. Different client library, no consumer-group rebalancing, but cheap at low-throughput.
- **Decision criterion**: Kafka is the most expensive messaging primitive to run in AWS. If you can live without consumer groups + exactly-once semantics, Kinesis is 5–10× cheaper at low volume. If you can't, MSK is the price.
- **Gotcha (TS-specific)**: `kafkajs` doesn't natively sign SigV4 for MSK IAM auth. Use `confluent-kafka-javascript` (librdkafka FFI) for MSK-IAM, or prefer SCRAM-SHA-512 / mTLS. Same posture as Rust's `aws-msk-iam-sasl-signer-rust` recommendation.

### nats
- **Local**: `nats:2-alpine`. TS: `nats` (official).
- **Cheapest fit**: Run NATS yourself on ECS Fargate (1 vCPU, 2 GB) ~$30/mo. Or migrate to SNS + SQS.
- **Production fit**: NATS on EKS or 3-node EC2 fleet (NATS clustering is straightforward).
- **Premium fit**: Synadia Cloud on AWS Marketplace if you need NATS-specific JetStream semantics managed.
- **Decision criterion**: NATS has no AWS-managed equivalent. Either self-host or migrate the abstraction (most NATS-as-pubsub uses translate cleanly to SNS+SQS).

---

## TypeScript app processes

### Node 22 (canonical TS runtime)
- **Local**: `node:22-alpine` or `node:22-slim` for app containers. Lambda: `public.ecr.aws/lambda/nodejs:22` container image, or the managed `nodejs22.x` runtime.
- **Cheapest fit**: Lambda + Function URL or API Gateway via framework's Lambda adapter. Free tier covers 1M req/mo.
- **Production fit**: ECS Fargate task behind ALB.
- **Premium fit**: App Runner for managed Fargate; ECS on EC2 + Spot for cost optimization.
- **Decision criterion**: Lambda cold-start for Node is 100–500 ms typical (warm-start <10 ms). For request/response APIs without long-lived state, Lambda wins on ops + cost. For websockets / SSE / heavy startup work / lifespan-style init, Fargate keeps the long-lived event loop.
- **Build step**: TS → JS bundling is *user's responsibility* — use `esbuild`, `tsc`, `swc`, or `bun build` to emit `.js` before packaging. caravan does NOT bundle for you; Lambda Node runtime expects `.js`.

### Bun (opt-in runtime)
- **Local**: `oven/bun:1`. Lambda: custom runtime via `bun-lambda` adapter on `provided.al2023`.
- **Cheapest fit / Production fit**: ECS Fargate is the sweet spot — Bun's faster startup + smaller image (~50 MB single-binary) saves cold-start. Lambda support is community/experimental.
- **Premium fit**: Same Fargate path.
- **Decision criterion**: Bun for new projects where the team can absorb the ecosystem-coverage gap (some npm packages still expect Node-only APIs; `pg`'s underlying `net` module works but isn't as battle-tested under Bun). Default to Node 22 for v1; reach for Bun when cold-start matters more than ecosystem maturity.

### Deno (container-baseline only)
- **Local**: `denoland/deno:1`. Static binary via `deno compile`.
- **Cheapest fit / Production fit**: Fargate.
- **Decision criterion**: Deno's Lambda story is niche; the AWS SDK for Deno is community-maintained. caravan treats Deno as "any container with a Dockerfile" — works, but no per-runtime guidance in v1.

### HTTP frameworks (all three covered per design decision)

For each: same Cheapest=Lambda+adapter / Production=Fargate behind ALB / Premium=App Runner pattern. The api_diffs file shows the canonical "one container, two shapes" snippet for each.

#### Express
- **Local**: `express` + `serverless-http` (or `@vendia/serverless-express`) for Lambda packaging.
- **Cheapest fit**: Lambda + API Gateway HTTP via `serverless-http` wrapper.
- **Production fit**: ECS Fargate behind ALB.
- **Decision criterion**: biggest hiring pool, most npm-ecosystem-mature, slower cold-start than Hono / Fastify. Conservative default.

#### Fastify
- **Local**: `fastify` + `@fastify/aws-lambda` (official plugin).
- **Cheapest fit**: Lambda via `@fastify/aws-lambda`.
- **Production fit**: ECS Fargate behind ALB.
- **Decision criterion**: ~2× Express throughput; mature plugin ecosystem; first-party Lambda story.

#### Hono
- **Local**: `hono` + `hono/aws-lambda` (built-in) + `@hono/node-server` for Node listen.
- **Cheapest fit**: Lambda via `hono/aws-lambda`.
- **Production fit**: Fargate (Node 22 or Bun) behind ALB.
- **Premium fit**: Same; or CloudFlare Workers if you want edge deploys (out of AWS scope).
- **Decision criterion**: closest TS analogue to Rust's `axum + lambda_http` — sub-ms startup, runs on Node/Bun/Deno/CF Workers, smallest "one container, two shapes" surface. Recommended modern default; pick Express if hiring breadth dominates.

### BullMQ worker (background jobs)
- **Local**: separate Node container running `bullmq` Worker with Redis broker.
- **Cheapest fit**: BullMQ + ElastiCache Redis. Worker container on Fargate.
- **Production fit**: BullMQ + ElastiCache Redis Serverless. Or migrate to SQS-based pattern via `bullmq-sqs` (community; less battle-tested) — or rewrite consumers with raw `@aws-sdk/client-sqs` `receiveMessage` long-poll.
- **Premium fit**: Replace BullMQ + queue entirely with Step Functions (if workflows are DAG-shaped) or Lambda triggered from SQS (if tasks are short).
- **Decision criterion**: BullMQ + Redis is the most-used TS background job pattern. For SQS, the cleanest path is raw SDK rather than `bullmq-sqs` since the latter doesn't yet match BullMQ-Redis feature parity (rate limits, repeat jobs, flows).
- **v3 yaml**: `service` + `trigger: { queue: jobs }`. Tier 0.

### Agenda (Mongo-backed jobs)
- **Local**: `agenda` (worker library backed by MongoDB).
- **Cheapest fit**: Agenda + DocumentDB.
- **Production fit**: Same, or migrate to BullMQ + ElastiCache or SQS as above.
- **Decision criterion**: niche; if you specifically chose Agenda for Mongo affinity, keep it. New projects: pick BullMQ.

### node-cron / node-schedule
- **Local**: in-process scheduler inside a long-running Node container.
- **Cheapest fit**: EventBridge Scheduler → Lambda (one schedule per job).
- **Production fit**: Same. EventBridge Scheduler is the canonical replacement.
- **Premium fit**: Step Functions Standard for jobs that need durable orchestration.
- **Decision criterion**: in-process schedulers fight multi-instance deployments (cron-fires-twice problem). EventBridge Scheduler is strictly better in AWS.

### cron-in-container
- **Local**: a container with `cron` running.
- **Cheapest fit**: EventBridge Scheduler → Lambda or → ECS RunTask.
- **Production fit**: Same.
- **Decision criterion**: Use EventBridge Scheduler. No exceptions.

### AWS Lambda Powertools (TS)
- **Local**: not needed.
- **Cheapest / Production fit**: pair with any framework above when running on Lambda. `@aws-lambda-powertools/logger` for structured logging, `/tracer` for X-Ray, `/metrics` for EMF.
- **Decision criterion**: optional but ergonomic on Lambda. Not a caravan concern; user adds it themselves.

---

## Adjacent infrastructure

### nginx / traefik (reverse proxy)
- **Local**: `nginx:alpine` or `traefik:v3`. Routes paths, handles TLS, serves static.
- **Cheapest fit**: API Gateway HTTP (paths via routes, TLS via ACM, static via S3+CloudFront).
- **Production fit**: ALB (path + host routing, TLS via ACM) + CloudFront for static.
- **Premium fit**: Add AWS WAF + Shield Advanced.
- **Decision criterion**: ALB is the closest semantic equivalent for service-fronting. API Gateway when your routes are Lambda-backed; ALB when they're Fargate/EC2.

### keycloak
- **Local**: `quay.io/keycloak/keycloak:24`. TS: `openid-client` for OIDC client flows, **`jose`** for token verification.
- **Cheapest fit**: Cognito User Pools (first 10k MAU free).
- **Production fit**: Cognito User Pools + Identity Pools for federated AWS-resource access. Or self-host Keycloak on Fargate + RDS Postgres if your team has Keycloak conviction.
- **Premium fit**: Auth0 / Okta / WorkOS on AWS Marketplace.
- **Decision criterion**: Cognito's UX (hosted UI quirks, custom-attribute friction, password-reset flows) loses to Keycloak on flexibility. Cognito wins on AWS-IAM integration and price at small scale. For >50k users with complex flows (org SSO, branded UI), most teams end up on Auth0/WorkOS or self-host Keycloak.
- **v3 framing (Tier 1)**: per v3 §4, the canonical pattern is *token verification* both sides via the same community library — **`jose`** + a JWKS URL env var. Cognito's JWKS lives at `https://cognito-idp.<region>.amazonaws.com/<pool_id>/.well-known/jwks.json`; Keycloak / dev issuer exposes its own well-known JWKS endpoint. Same `jwtVerify(token, jwks)` call both sides; no caravan-shipped library involved. Cognito's *user lifecycle* (sign-up, MFA, hosted UI, custom attributes) remains `cloud_only` per v3 §8.
- **Code change**: where you previously used Keycloak Admin REST API, the equivalent is `@aws-sdk/client-cognito-identity-provider` admin actions — these don't have a portable abstraction and only run cloud-side anyway. For request-time auth, the `jose` JWKS pattern is the supported path.

### vault (Hashicorp)
- **Local**: `hashicorp/vault:1.16`. TS: `node-vault` (community), or the official `@hashicorp/vault-client` (when released).
- **Cheapest fit**: SSM Parameter Store Standard (free!) for static config. Secrets Manager for rotating secrets.
- **Production fit**: SSM Parameter Store + Secrets Manager + KMS-encrypted application data.
- **Premium fit**: HCP Vault Dedicated on AWS Marketplace if you actually use Vault dynamic secrets / PKI / transit features.
- **Decision criterion**: 90% of "vault" usage in startups is just secret storage — SSM Parameter Store covers it for free. Vault is worth keeping only if you use dynamic database credentials, PKI, or KV v2's leasing.
- **Code change**: `node-vault.read("secret/data/x")` → `@aws-sdk/client-ssm` `GetParameter` or `@aws-sdk/client-secrets-manager` `GetSecretValue`. Significant rewrite.

### mailhog / maildev (dev SMTP catcher)
- **Local**: `mailhog/mailhog` or `maildev/maildev`. TS: **`nodemailer`** pointing at port 1025.
- **Cheapest fit / Production fit**: SES — either via SMTP (point `nodemailer` at the SES SMTP endpoint with SES SMTP credentials) or via `@aws-sdk/client-sesv2` `SendEmailCommand`.
- **Premium fit**: SES with Virtual Deliverability Manager + dedicated IP pool.
- **Decision criterion**: SES is the right call. For local dev keep mailhog and switch via env-driven SMTP host or `@aws-sdk` endpoint. SES requires production access approval (sandbox-to-prod) — non-trivial paperwork; budget a few days.
- **v3 framing (Tier 1)**: `nodemailer` IS the abstraction — it works both sides without any wrapper. Env-driven `SMTP_HOST` / `SMTP_PORT` / `SMTP_USER` / `SMTP_PASS` is the entire seam. The `@aws-sdk/client-sesv2` path is an alternative when you need SES-specific features (templates, configuration sets); pick one approach per code path. (Python mirror uses `smtplib`; Rust mirror uses `lettre`.)

### prometheus / grafana
- **Local**: `prom/prometheus` + `grafana/grafana`. TS: `prom-client` (the canonical Prometheus client).
- **Cheapest fit**: CloudWatch Metrics (custom). Free tier 10 metrics. Use the embedded metric format (EMF) from logs to avoid PutMetricData costs — `@aws-lambda-powertools/metrics` emits EMF.
- **Production fit**: Amazon Managed Prometheus (AMP) + Amazon Managed Grafana (AMG). Keeps the same query language (PromQL) and dashboards.
- **Premium fit**: AMP/AMG + extended retention + workspace federation.
- **Decision criterion**: If your team already lives in Prometheus/Grafana, AMP/AMG is the low-friction port. If you're starting fresh, CloudWatch Metrics is fine for <50 custom metrics; gets expensive past 1k.

### loki / jaeger / tempo (logs and traces)
- **Local**: `grafana/loki`, `jaegertracing/all-in-one`, `grafana/tempo`. TS: `pino` (canonical structured logger), `winston` (older), `@opentelemetry/sdk-node` + `@opentelemetry/exporter-trace-otlp-grpc`.
- **Cheapest fit**: CloudWatch Logs (logs); X-Ray (traces) via OTel exporter.
- **Production fit**: CloudWatch Logs + X-Ray + Application Signals for service maps. Or push to Datadog/Honeycomb/Grafana Cloud via OTel collector if budget allows.
- **Premium fit**: AWS Distro for OpenTelemetry → AMP (metrics) + CloudWatch Logs + X-Ray.
- **Decision criterion**: OpenTelemetry is the abstraction that survives a vendor swap — instrument with OTel, choose backend separately. CloudWatch Logs gets expensive fast (>$50/GB ingested at scale).

---

## AI / LLM

This section reflects the Tier 1 pair classification in v3 §4 — `litellm`'s TS analogue is the Vercel AI SDK.

### LLM provider abstraction (Bedrock + Ollama + others)
- **Local**: `ollama/ollama` (single-binary local LLM host, OpenAI-compatible HTTP API) or `vllm/vllm-openai` for GPU-backed serving. TS: **Vercel AI SDK** (`ai` + `@ai-sdk/amazon-bedrock` + `ollama-ai-provider`).
- **Cheapest fit**: Ollama locally for dev; **Bedrock on-demand** for prod (Haiku ~$1/$5 per M tokens, Sonnet ~$3/$15, Opus ~$5/$25 — see `aws_service_groups.md` §29).
- **Production fit**: Bedrock on-demand + **Bedrock Provisioned Throughput** when sustained spend exceeds ~$5k/mo and predictability matters.
- **Premium fit**: Mixed routing — cheap models for cheap tasks, Opus for hard tasks; budget-aware fallbacks; spend limits per model. The AI SDK has middleware hooks for this.
- **v3 framing (Tier 1)**: Vercel AI SDK is the TS community library named in v3 §4. It provides a single API surface across Bedrock, Ollama, OpenAI, Anthropic direct, Cohere, Vertex, and many others — env-driven provider/model selects the backend.
  ```ts
  import { generateText } from "ai";
  import { bedrock } from "@ai-sdk/amazon-bedrock";
  import { ollama } from "ollama-ai-provider";
  const provider = process.env.LLM_BACKEND === "bedrock" ? bedrock : ollama;
  const { text } = await generateText({
    model: provider(process.env.LLM_MODEL ?? "llama3.1"),
    prompt: "hi",
  });
  ```
- **v3 yaml**: `cloud_only: llm: { type: bedrock.llm, model: "anthropic.claude-opus-4-7-..." }` for the *provisioning marker* (IAM perms, throughput config). User code talks to the Vercel AI SDK; caravan just ensures the cloud-side identity has the right Bedrock policies attached and the model ID env var is injected.
- **Out of scope for the Vercel AI SDK abstraction (remain `cloud_only` T2)**: Bedrock Knowledge Bases, Bedrock Agents, Bedrock Guardrails — AWS-orchestration services with no OSS equivalent. Either hit real AWS from local dev (mixed mode per v3 §4) or skip locally and test cloud-side.

### Vision / OCR (Rekognition + Textract)
- **Local**: `@xenova/transformers` for CLIP / DETR / sentiment models via ONNX; `tesseract.js` for OCR.
- **Cheapest fit**: Rekognition off-the-shelf APIs ($1 per 1k images for Labels, $1.50–$50 per 1k pages for Textract).
- **Production fit / Premium fit**: SageMaker hosting a fine-tuned model (when off-the-shelf accuracy isn't enough; Python-shaped training, cross-language seam).
- **v3 framing (Tier 1)**: vision is genuinely Tier 1 — same task, different model behind the API. No single TS community library hides the gap the way Vercel AI SDK does for LLMs; the pattern is to wrap behind a small interface if you need to swap, or accept that local tests run a different model than prod.

### Speech (Polly TTS, Transcribe STT)
- **Local**: **`@xenova/transformers`** for STT (Whisper.js, ONNX in Node). TTS: no first-class TS option; cross-language to Coqui-TTS / piper Python service.
- **Cheapest / Production fit**: Transcribe ($0.024/min batch), Polly Neural ($16 / M chars).
- **v3 framing (Tier 1)**: `@xenova/transformers` is the named TS community library in v3 §4 for STT. Output formats differ between Whisper.js and Transcribe (Whisper returns chunks + text; Transcribe returns rich items); normalize at the boundary.

---

## Summary table

| Local component (TS) | Cheapest fit | Production fit | v3 yaml / tier vocab | Tier |
|---|---|---|---|---|
| postgres (`pg` / `prisma` / `drizzle` / `kysely`) | RDS Postgres micro | Aurora Postgres Serverless v2 | `db.sql tier: dev | prod-small | prod | premium | global` | T0 |
| mysql/mariadb (`mysql2`) | RDS MySQL micro | Aurora MySQL Serverless v2 | `db.sql engine: mysql tier: …` | T0 |
| mongodb (`mongodb` / `mongoose`) | DocumentDB t4g.medium | DocumentDB cluster or Atlas-on-AWS | not a v3 primitive (use `cloud_only` or escape hatch) | T0 happy-path; partial overall |
| redis cache (`ioredis`) | ElastiCache micro | ElastiCache Serverless | `cache tier: …` (v1.x in v3 §6) | T0 |
| redis pubsub | ElastiCache | SNS+SQS (rewrite) | migrate to `topic:` + `queue:` | T0 after migration |
| memcached (`memjs`) | ElastiCache Memcached | Switch to Redis | (use `cache` primitive) | T0 |
| minio | S3 | S3 + Intelligent-Tiering | `bucket class: standard | intelligent | …` | T0 |
| opensearch (`@opensearch-project/opensearch`) | OpenSearch t3.small | OpenSearch r6g cluster | not in v1 PoC; use `terraform-module` escape | T0 |
| pgvector (`pgvector` npm) | Aurora Postgres | Aurora Postgres | `db.sql` with `extensions: [vector]` | T0 |
| qdrant/weaviate/chroma | pgvector | OpenSearch k-NN or vendor cloud | not a v3 primitive | T1 if hand-rolled abstraction |
| rabbitmq (`amqplib`) | Amazon MQ micro | Amazon MQ cluster | `queue kind: standard | fifo` (after SQS migration) or DSN swap to Amazon MQ | T0 |
| kafka (`kafkajs` / `confluent-kafka-javascript`) | MSK Serverless ($540 floor) | MSK provisioned | not in v1; `terraform-module` or `cloud_only` | T0 wire / Moderate IAM |
| nats (`nats`) | Self-host on Fargate | Self-host on EKS or migrate to SNS+SQS | no v3 primitive — self-host as a `service` | n/a |
| Hono | Lambda + `hono/aws-lambda` | Fargate behind ALB | `service shape: function | long-running` | one container, two shapes (v3 §3) |
| Express + `serverless-http` | Lambda + serverless-http | Fargate behind ALB | `service shape: function | long-running` | same |
| Fastify + `@fastify/aws-lambda` | Lambda + plugin | Fargate behind ALB | `service shape: function | long-running` | same |
| BullMQ worker | Fargate + ElastiCache Redis | Fargate + SQS (raw `@aws-sdk/client-sqs`) | `service` + `trigger: { queue: jobs }` | T0 |
| Agenda worker | Fargate + DocumentDB | Migrate to BullMQ | `service` + Mongo-backed queue | T0 |
| node-cron / node-schedule | EventBridge Scheduler → Lambda | Same | `triggers: <name>: { schedule: "0 2 * * *", target: worker }` | n/a (cron is a trigger attribute) |
| cron container | EventBridge Scheduler → Lambda or RunTask | Same | `triggers:` schedule | n/a |
| nginx/traefik | API Gateway HTTP | ALB | `service expose: { port: …, public: true }` (ALB auto-derived) | n/a |
| keycloak | Cognito (first 10k MAU free) | Cognito or self-host Keycloak | Cognito user-lifecycle is `cloud_only`; token verify via `jose` JWKS | **T1 (jose)** |
| vault | SSM Parameter Store (free) | SSM + Secrets Manager + KMS | `secret:` primitive | T0 |
| mailhog | SES | SES + dedicated IP | no primitive; `nodemailer` is the abstraction | **T1 (nodemailer)** |
| prometheus | CloudWatch Metrics (EMF) | Amazon Managed Prometheus | not in v1 PoC | n/a |
| grafana | CloudWatch dashboards | Amazon Managed Grafana | not in v1 PoC | n/a |
| loki | CloudWatch Logs | CloudWatch Logs | stdout JSON via `pino` (collected by runtime) | T0 |
| jaeger/tempo | X-Ray | X-Ray + Application Signals | OTel exporter env var | T0 (OTel) |
| **LLM (Bedrock/Ollama)** | Ollama locally + Bedrock Haiku in cloud | Bedrock Sonnet | `cloud_only: { type: bedrock.llm, model: ... }`; Vercel AI SDK in code | **T1 (Vercel AI SDK)** |
| **Vision (Rekognition/Textract)** | Rekognition off-the-shelf | SageMaker fine-tuned (cross-lang) | not in v1; small wrapper if you need swap | T1 |
| **Speech STT (Transcribe)** | Transcribe | same | not in v1; `@xenova/transformers` Whisper.js locally | **T1 (`@xenova/transformers`)** |
| **Speech TTS (Polly)** | Polly Neural | same | no first-class TS TTS; cross-language to Python | partial / cross-lang |

**Tier legend**: T0 = same wire API both sides, env-var swap (v3 §4). T1 = different wire APIs, community library bridges (Vercel AI SDK, `jose`, `nodemailer`, `@xenova/transformers`). T2 = no local equivalent, `cloud_only:` in IR. See `typescript_api_diffs.md` for code snippets per pair and `caravan_abstraction_v3.md` §4 for the canonical T0/T1/T2 derivation.

---

See `mapping_aws_to_typescript.md` for the reverse direction (which container plays the AWS role in dev) and `typescript_api_diffs.md` for the per-pair TS code diff. Conceptual home: `thesis.md`. Long-form derivation: `caravan_abstraction_v3.md` (supersedes v2).
