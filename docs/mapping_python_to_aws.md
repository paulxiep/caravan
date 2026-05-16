# Python Stack → AWS Mapping

> **Snapshot date: 2026-05-16.** AWS prices reference `aws_service_groups.md`.
> **Scope**: Python ecosystem only. Other language ecosystems (Go, Java, Ruby) are out of scope for this round; Rust is covered in the parallel `mapping_rust_to_aws.md`.
> **Framing**: this file is Python ecosystem evidence feeding into `thesis.md` (conceptual home) and `supeux_abstraction_v2.md` (long-form derivation). The Cheapest/Production/Premium tier labels below are the **operator's intuition**; they map onto v2 §6's explicit yaml `tier:` vocabulary (`db.sql tier: dev | prod-small | prod | premium | global`, `bucket class: standard | intelligent | …`, etc.) — that mapping is shown inline per row and rolled up in the closing summary table.

Question this file answers: *"My Python app and its docker-compose dependencies — what does each piece become on AWS?"*

Each row lists three tiers — **Cheapest fit** (PoC, hobby, dev/staging), **Production fit** (typical "real app" choice), **Premium fit** (when scale, latency, or compliance dominate). Tier choice depends on traffic, budget, and how much you trust AWS-specific lock-in. Where multiple AWS services are mentioned, see `aws_service_groups.md` for cost/latency detail; for cloud↔local code-diffs see `python_api_diffs.md`.

---

## Datastores

### postgres
- **Local**: `postgres:16-alpine`. Python clients: `psycopg[binary]`, `asyncpg`, `sqlalchemy`.
- **Cheapest fit**: RDS Postgres db.t4g.micro (~$12/mo) — single-AZ.
- **Production fit**: Aurora Postgres Serverless v2 (auto-pause for dev/staging; 0.5–8 ACU for prod). Drop-in driver compatibility.
- **Premium fit**: Aurora Postgres provisioned multi-AZ with I/O-Optimized cluster config + 1–3 read replicas. Aurora DSQL for active-active multi-region.
- **Decision criterion**: under 100k req/day → RDS micro is fine. Above 1 RPS sustained, Aurora Serverless v2 wins on the operational story. Premium only when you can defend the cost.
- **v2 yaml**: `db.sql tier: dev` (RDS micro) · `prod-small` (Aurora Serverless v2) · `prod` (Aurora provisioned multi-AZ) · `premium` (multi-AZ + read replicas + I/O-Optimized) · `global` (Aurora Global / DSQL). Tier 0 — DSN swap via `DATABASE_URL`.
- **Gotcha**: RDS minor-version auto-upgrades during maintenance windows can break `psycopg` if you've pinned a specific extension version. Use Aurora to dodge most of this.

### mysql / mariadb
- **Local**: `mysql:8`, `mariadb:11`. Python: `pymysql`, `mysqlclient`, `aiomysql`, `sqlalchemy`.
- **Cheapest fit**: RDS MySQL db.t4g.micro (~$12/mo).
- **Production fit**: Aurora MySQL Serverless v2 (MariaDB code mostly works against Aurora MySQL — check stored proc syntax).
- **Premium fit**: Aurora MySQL with global database + cross-region read replicas.
- **Decision criterion**: Same as postgres. MariaDB-specific features (JSON path expressions, certain spatial functions) may not survive Aurora MySQL — verify per workload.
- **v2 yaml**: `db.sql engine: mysql tier: dev | prod-small | prod | premium | global`. Tier 0 — DSN swap via `DATABASE_URL`.

### mongodb
- **Local**: `mongo:7`. Python: `pymongo`, `motor` (async), `beanie`, `mongoengine`.
- **Cheapest fit**: DocumentDB db.t4g.medium (~$72/mo) — cheapest viable Mongo-compatible.
- **Production fit**: DocumentDB multi-AZ with 1+ replicas.
- **Premium fit**: MongoDB Atlas on AWS (run via AWS Marketplace) — full Mongo API + Atlas features (Search, Vector Search, Triggers). Often the right call when you actually depend on Mongo aggregations.
- **Decision criterion**: DocumentDB advertises Mongo wire protocol but lacks ~30% of aggregation operators (esp. `$lookup` semantics, `$facet`, `$bucket`). If your code uses modern Mongo features, Atlas-on-AWS over DocumentDB.

### redis (as cache)
- **Local**: `redis:7-alpine`. Python: `redis-py`, `redis.asyncio`, `aiocache`.
- **Cheapest fit**: ElastiCache Redis cache.t4g.micro (~$12/mo).
- **Production fit**: ElastiCache Redis Serverless (auto-scale, no capacity planning) or ElastiCache cluster-mode-enabled with reader nodes.
- **Premium fit**: MemoryDB for Redis (durable Redis as primary store) — costs ~6× ElastiCache but eliminates a separate DB tier.
- **Decision criterion**: 99% of "I need Redis" cases want ElastiCache Serverless. MemoryDB only when you've explicitly chosen Redis as system of record.
- **v2 yaml**: deferred to v1.x — see v2 §6 tier table (`cache: tier: dev | prod-small | prod-cluster | serverless | memorydb`). Tier 0 — DSN swap via `REDIS_URL`.

### redis (as pub/sub or queue)
- **Local**: same image. Python: `redis-py` `.pubsub()` or `redis.Stream`.
- **Cheapest fit**: ElastiCache Redis (keep pub/sub working as-is).
- **Production fit**: Replace pub/sub with SNS + SQS fan-out, or replace stream with Kinesis Data Streams. Different code; better at-least-once semantics and decoupled scaling.
- **Premium fit**: EventBridge with content-based routing for the pub/sub layer.
- **Decision criterion**: Redis pub/sub is fire-and-forget (drops if no subscriber). SQS/SNS is durable. For anything you care about losing, do not keep Redis pub/sub in AWS.
- **v2 yaml**: when migrating, declare `topic:` (→ SNS, ElasticMQ-SNS locally) for fan-out and `queue:` (→ SQS, ElasticMQ locally) for point-to-point. Both Tier 0 once you've moved off Redis pub/sub.

### memcached
- **Local**: `memcached:1`. Python: `pymemcache`, `pylibmc`.
- **Cheapest fit**: ElastiCache Memcached cache.t4g.micro (~$12/mo).
- **Production fit / Premium fit**: ElastiCache Redis Serverless. Memcached has no compelling advantage over Redis in 2026. Switch.
- **Decision criterion**: there isn't one — pick Redis.

### minio (S3-compatible)
- **Local**: `minio/minio` or `bitnami/minio`. Python: `boto3` with `endpoint_url=http://minio:9000`.
- **Cheapest fit**: S3 Standard. Free tier covers 5 GB.
- **Production fit**: S3 Standard + Intelligent-Tiering for unknown-pattern data.
- **Premium fit**: S3 + CloudFront for read-heavy + S3 Replication cross-region for DR.
- **Decision criterion**: minio is the closest-to-trivial AWS migration in this whole file — same boto3 code, env-driven endpoint. The one trap is per-object behavior under concurrent writes (minio's eventual consistency model differs from S3 in failure modes).
- **v2 yaml**: `bucket class: standard | intelligent | standard-ia | one-zone-ia | glacier-instant | glacier-flexible | glacier-deep-archive`; `lifecycle:` for transitions; `variant: standard | express-one-zone | vectors` for the rare typed-different cases. Tier 0 — `S3_ENDPOINT_URL` swap.
- **Edge cases moving to v2 cloud_only**: S3 + CloudFront for "Production fit" reads becomes `static_site` primitive (v1.2 per v2 §10); CloudFront standalone is `cloud_only: cloudfront`.

### elasticsearch / opensearch
- **Local**: `opensearchproject/opensearch:2` (or `elasticsearch:8`). Python: `opensearch-py`, `elasticsearch-py`.
- **Cheapest fit**: OpenSearch Service t3.small.search single-node (~$73/mo) — viable for dev; not for prod.
- **Production fit**: OpenSearch Service provisioned 3-node cluster (r6g.large.search × 3 ≈ $400/mo) with dedicated master nodes for stability above 10 data nodes.
- **Premium fit**: OpenSearch Serverless if your workload is genuinely spiky and the $1k/mo floor doesn't sting.
- **Decision criterion**: OpenSearch's API is a fork of Elasticsearch 7.10 — modern Elasticsearch ≥8 code (especially using x-pack security or vector search APIs that differ) may need shims. `opensearch-py` is straightforward; `elasticsearch-py` ≥8 will refuse to connect to OpenSearch unless you downgrade to `elasticsearch-py==7.13.4` or set `compatibility-mode`.

### qdrant / weaviate / chroma (dedicated vector DBs)
- **Local**: `qdrant/qdrant`, `cr.weaviate.io/semitechnologies/weaviate`, `chromadb/chroma`. Python clients: `qdrant-client`, `weaviate-client`, `chromadb`.
- **Cheapest fit**: Aurora Postgres pgvector. <10M vectors fits easily on a db.t4g.medium.
- **Production fit**: OpenSearch Service with the k-NN plugin (use HNSW). Best when you already run OpenSearch.
- **Premium fit**: S3 Vectors for tens-of-billions cold storage. Or run the dedicated vector DB on EKS/ECS via the vendor's own AWS Marketplace AMI / Helm chart (Qdrant Cloud, Weaviate Cloud — both have AWS-native managed offerings).
- **Decision criterion**: if you're <10M vectors and your team already runs Postgres, pgvector is the cheapest abstraction shrinkage. Above 100M vectors or strict <50 ms latency SLOs, dedicated managed (Pinecone, Qdrant Cloud) beats AWS-native today. S3 Vectors is for archival-shape workloads.

### pgvector (local)
- **Local**: `pgvector/pgvector:pg16`. Python: `psycopg` + `pgvector[psycopg]`.
- **Cheapest / Production fit**: Aurora Postgres has pgvector extension built-in. Same SQL, same Python code — set `CREATE EXTENSION vector;`.
- **Premium fit**: Aurora Postgres Optimized Reads instances (caches working set in NVMe; helps HNSW search latency).
- **Decision criterion**: easiest AWS port in the vector category. If your vector layer is pgvector locally, keep it pgvector in AWS until it stops scaling.

---

## Messaging

### rabbitmq
- **Local**: `rabbitmq:3-management`. Python: `pika`, `aio-pika`, `celery[redis,sqs,rabbitmq]`.
- **Cheapest fit**: Amazon MQ for RabbitMQ mq.t3.micro single-instance (~$15/mo).
- **Production fit**: Amazon MQ multi-AZ cluster (3-node) — keeps your AMQP semantics intact.
- **Premium fit**: Same. Cluster size up.
- **Alternative path (cheaper but code changes)**: Move to SQS Standard (replace `pika` consumer with `boto3.client('sqs')` polling) + SNS for fan-out. Often the right call if RabbitMQ features used were just "queue with workers."
- **Decision criterion**: If you rely on AMQP-specific features (priority queues, dead-letter exchanges with topic routing, JMS-like selectors) → Amazon MQ. If you used RabbitMQ as a generic queue → switch to SQS, save money and ops effort.
- **v2 yaml**: when migrating to SQS, declare `queue kind: standard | fifo`. Tier 0 — `SQS_ENDPOINT_URL` swap (ElasticMQ locally). Sticking with AMQP keeps `pika` Tier 0 against the rabbitmq container ↔ Amazon MQ (DSN swap).

### kafka
- **Local**: `bitnami/kafka:3.7` or `confluentinc/cp-kafka:7`. Python: `kafka-python`, `confluent-kafka`, `aiokafka`, `faust-streaming`.
- **Cheapest fit**: MSK Serverless ($540/mo cluster floor — there's no "tiny" Kafka in AWS).
- **Production fit**: MSK provisioned `kafka.m7g.large` × 3 brokers (≈$500/mo brokers + storage).
- **Premium fit**: MSK provisioned with tiered storage + MSK Connect for source/sink connectors. Confluent Cloud on AWS Marketplace for full Kafka + Schema Registry + ksqlDB.
- **Cheaper non-Kafka alternative**: Kinesis Data Streams on-demand. Different client library (`boto3` instead of `confluent-kafka`), no consumer-group rebalancing, but cheap at low-throughput.
- **Decision criterion**: Kafka is the most expensive messaging primitive to run in AWS. If you can live without consumer groups + exactly-once semantics, Kinesis is 5–10× cheaper at low volume. If you can't, MSK is the price.

### nats
- **Local**: `nats:2-alpine`. Python: `nats-py`.
- **Cheapest fit**: Run NATS yourself on ECS Fargate (1 vCPU, 2 GB) ~$30/mo. Or migrate to SNS + SQS.
- **Production fit**: NATS on EKS or 3-node EC2 fleet (NATS clustering is straightforward).
- **Premium fit**: Synadia Cloud on AWS Marketplace if you need NATS-specific JetStream semantics managed.
- **Decision criterion**: NATS has no AWS-managed equivalent. Either self-host or migrate the abstraction (most NATS-as-pubsub uses translate cleanly to SNS+SQS).

---

## Python app processes

### fastapi
- **Local**: `python:3.12-slim` + `uvicorn[standard] app:app`. Often behind `nginx`.
- **Cheapest fit**: Lambda + API Gateway HTTP via Mangum adapter, or Lambda Function URL. Free tier covers 1M req/mo.
- **Production fit**: ECS Fargate task behind ALB — keeps the long-lived event-loop semantics, websockets, and SSE working naturally.
- **Premium fit**: App Runner if you want managed Fargate; ECS on EC2 + Spot for cost optimization.
- **Decision criterion**: FastAPI's value is async + websockets + streaming responses. Lambda kills the first two. Use Lambda only for stateless request/response APIs; otherwise ECS Fargate.
- **Code shape**: `Mangum(app)` wrap is one line. Background tasks, long-lived websockets, lifespan startup hooks don't survive the move to Lambda.

### flask
- **Local**: `python:3.12-slim` + `gunicorn -w 4 app:app`.
- **Cheapest fit**: Lambda + API Gateway HTTP via `aws-wsgi` or Mangum. Flask is wsgi → trivial wrap.
- **Production fit**: ECS Fargate behind ALB; App Runner for simpler setup.
- **Premium fit**: Same as Production.
- **Decision criterion**: Flask is the *easiest* Python web framework to put on Lambda — no async, no lifespan. Default to Lambda for new small Flask apps; default to Fargate for >50 req/sec sustained.

### django
- **Local**: `python:3.12` + `gunicorn` or `uvicorn` (for ASGI). Usually paired with postgres + redis + celery.
- **Cheapest fit**: Single Fargate task + RDS Postgres micro. ~$30/mo.
- **Production fit**: Fargate auto-scaling group behind ALB + RDS Aurora Postgres + ElastiCache Redis + Celery on Fargate workers + S3 for media + CloudFront.
- **Premium fit**: Same with multi-AZ everywhere + read replicas + Aurora Serverless v2.
- **Decision criterion**: Django on Lambda is technically possible (`zappa`, `serverless-wsgi`) but the ecosystem (django-admin, migrations, signals at process start) fights it. Default to Fargate. Move to Lambda only for truly stateless API-only Django.

### gunicorn / uvicorn
- **Local**: process manager inside a Python container.
- **Cheapest fit / Production fit**: Doesn't map directly — gunicorn is a process inside whatever container/Lambda your framework lives in. The "AWS equivalent" is the runtime (Lambda runtime, App Runner managed nginx).
- **Decision criterion**: don't try to abstract this. Pick the framework target (above) and the process manager is determined.

### celery worker + celery beat
- **Local**: `python:3.12` container running `celery -A app worker -l info` and `celery beat`. Broker = redis or rabbitmq.
- **Cheapest fit**: Keep Celery as the worker abstraction. Broker = ElastiCache Redis or Amazon MQ for RabbitMQ. Worker container on Fargate. Beat scheduler container on Fargate (single instance — don't HA beat).
- **Production fit**: Same but switch broker to SQS via `celery[sqs]`. Workers on Fargate; replace `celery beat` with EventBridge Scheduler → SQS messages.
- **Premium fit**: Replace celery worker + queue entirely with Step Functions (if workflows are DAG-shaped) or Lambda triggered from SQS (if tasks are short).
- **Decision criterion**: Celery + SQS is the standard "Django background jobs in AWS" pattern. Beat → EventBridge Scheduler is a clean win (no risk of duplicate beat instances scheduling twice).

### rq (Redis Queue)
- **Local**: `python:3.12` + `rq worker`. Broker = redis.
- **Cheapest fit**: ElastiCache Redis + RQ workers on Fargate.
- **Production fit**: Same, or migrate to SQS-based pattern (RQ doesn't have an SQS adapter — switch to Celery or write workers against `boto3 sqs.receive_message`).
- **Decision criterion**: RQ → SQS is "rewrite the worker loop"; if your queue volume is small, just keep RQ + ElastiCache.

### dramatiq
- **Local**: `python:3.12` + `dramatiq -p 4 tasks`. Broker = rabbitmq or redis.
- **Cheapest fit**: Keep dramatiq + ElastiCache Redis (cheap broker).
- **Production fit**: Dramatiq + Amazon MQ RabbitMQ (Dramatiq's RabbitMQ broker is the most-tested).
- **Decision criterion**: Dramatiq has no SQS broker out of the box. If you want SQS, switch to Celery.

### apscheduler
- **Local**: in-process scheduler inside a long-running Python container.
- **Cheapest fit**: EventBridge Scheduler → Lambda (one schedule per job).
- **Production fit**: Same. EventBridge Scheduler is the canonical replacement.
- **Premium fit**: Step Functions Standard for jobs that need durable orchestration.
- **Decision criterion**: APScheduler in-process is fragile in multi-instance deployments (cron-fires-twice problem). EventBridge Scheduler is strictly better in AWS.

### cron-in-container
- **Local**: a container with `cron` running.
- **Cheapest fit**: EventBridge Scheduler → Lambda or → ECS RunTask.
- **Production fit**: Same.
- **Decision criterion**: Use EventBridge Scheduler. No exceptions.

---

## Adjacent infrastructure

### nginx / traefik (reverse proxy)
- **Local**: `nginx:alpine` or `traefik:v3`. Routes paths, handles TLS, serves static.
- **Cheapest fit**: API Gateway HTTP (paths via routes, TLS via ACM, static via S3+CloudFront).
- **Production fit**: ALB (path + host routing, TLS via ACM) + CloudFront for static.
- **Premium fit**: Add AWS WAF + Shield Advanced.
- **Decision criterion**: ALB is the closest semantic equivalent for service-fronting. API Gateway when your routes are Lambda-backed; ALB when they're Fargate/EC2.

### keycloak
- **Local**: `quay.io/keycloak/keycloak:24`. Python: `python-keycloak` for admin flows, **`authlib`** or **`python-jose`** for token verification.
- **Cheapest fit**: Cognito User Pools (first 10k MAU free).
- **Production fit**: Cognito User Pools + Identity Pools for federated AWS-resource access. Or self-host Keycloak on Fargate + RDS Postgres if your team has Keycloak conviction.
- **Premium fit**: Auth0 / Okta / WorkOS on AWS Marketplace.
- **Decision criterion**: Cognito's UX (hosted UI quirks, custom-attribute friction, password-reset flows) loses to Keycloak on flexibility. Cognito wins on AWS-IAM integration and price at small scale. For >50k users with complex flows (org SSO, branded UI), most teams end up on Auth0/WorkOS or self-host Keycloak.
- **v2 framing (Tier 1)**: per v2 §4, the canonical pattern is *token verification* both sides via the same community library — `authlib` (or `python-jose`) + a JWKS URL env var. Cognito's JWKS lives at `https://cognito-idp.<region>.amazonaws.com/<pool_id>/.well-known/jwks.json`; Keycloak / dev issuer exposes its own well-known JWKS endpoint. Same `verify(token)` call both sides; no supeux-shipped library involved. Cognito's *user lifecycle* (sign-up, MFA, hosted UI, custom attributes) remains `cloud_only` per v2 §8.
- **Code change**: where you previously used `python-keycloak` admin APIs, the equivalent is `boto3.client('cognito-idp')` admin actions — these don't have a portable abstraction and only run cloud-side anyway. For request-time auth, the authlib JWKS pattern is the supported path.

### vault (Hashicorp)
- **Local**: `hashicorp/vault:1.16`. Python: `hvac`.
- **Cheapest fit**: SSM Parameter Store Standard (free!) for static config. Secrets Manager for rotating secrets.
- **Production fit**: SSM Parameter Store + Secrets Manager + KMS-encrypted application data.
- **Premium fit**: HCP Vault Dedicated on AWS Marketplace if you actually use Vault dynamic secrets / PKI / transit features.
- **Decision criterion**: 90% of "vault" usage in startups is just secret storage — SSM Parameter Store covers it for free. Vault is worth keeping only if you use dynamic database credentials, PKI, or KV v2's leasing.
- **Code change**: `hvac.Client().secrets.kv.read_secret(...)` → `boto3.client('ssm').get_parameter(...)` or `boto3.client('secretsmanager').get_secret_value(...)`. Significant rewrite.

### mailhog / maildev (dev SMTP catcher)
- **Local**: `mailhog/mailhog` or `maildev/maildev`. Python: **`smtplib`** (stdlib) pointing at port 1025.
- **Cheapest fit / Production fit**: SES — either via SMTP (point `smtplib` at the SES SMTP endpoint with SES SMTP credentials) or via `boto3.client('sesv2').send_email(...)`.
- **Premium fit**: SES with Virtual Deliverability Manager + dedicated IP pool.
- **Decision criterion**: SES is the right call. For local dev keep mailhog and switch via env-driven SMTP host or boto3 endpoint. SES requires production access approval (sandbox-to-prod) — non-trivial paperwork; budget a few days.
- **v2 framing (Tier 1)**: `smtplib` IS the abstraction — it works both sides without any wrapper. Env-driven `SMTP_HOST` / `SMTP_PORT` / `SMTP_USER` / `SMTP_PASS` is the entire seam. The `boto3.client('sesv2')` path is an alternative when you need SES-specific features (templates, configuration sets); pick one approach per code path. (Rust mirror uses `lettre` for the same role.)

### prometheus / grafana
- **Local**: `prom/prometheus` + `grafana/grafana`. Python: `prometheus-client`.
- **Cheapest fit**: CloudWatch Metrics (custom). Free tier 10 metrics. Use the embedded metric format from logs to avoid PutMetricData costs.
- **Production fit**: Amazon Managed Prometheus (AMP) + Amazon Managed Grafana (AMG). Keeps the same query language (PromQL) and dashboards.
- **Premium fit**: AMP/AMG + extended retention + workspace federation.
- **Decision criterion**: If your team already lives in Prometheus/Grafana, AMP/AMG is the low-friction port. If you're starting fresh, CloudWatch Metrics is fine for <50 custom metrics; gets expensive past 1k.
- **Code change**: `prometheus_client.Counter("x").inc()` keeps working under AMP via `remote_write`. Grafana dashboards port directly.

### loki / jaeger / tempo (logs and traces)
- **Local**: `grafana/loki`, `jaegertracing/all-in-one`, `grafana/tempo`. Python: `python-logging-loki`, `opentelemetry-*`.
- **Cheapest fit**: CloudWatch Logs (logs); X-Ray (traces) via OTel exporter.
- **Production fit**: CloudWatch Logs + X-Ray + Application Signals for service maps. Or push to Datadog/Honeycomb/Grafana Cloud via OTel collector if budget allows.
- **Premium fit**: AWS Distro for OpenTelemetry → AMP (metrics) + CloudWatch Logs + X-Ray.
- **Decision criterion**: OpenTelemetry is the abstraction that survives a vendor swap — instrument with OTel, choose backend separately. CloudWatch Logs gets expensive fast (>$50/GB ingested at scale).

---

## AI / LLM

This section was absent in the v1-era version of this doc because v1 framed LLMs as Intractable / cloud-only. v2 §4 reclassifies LLM access as a **Tier 1 pair** with a clear community-library answer.

### LLM provider abstraction (Bedrock + Ollama + others)
- **Local**: `ollama/ollama` (single-binary local LLM host, OpenAI-compatible HTTP API) or `vllm/vllm-openai` for GPU-backed serving. Python: **`litellm`**.
- **Cheapest fit**: Ollama locally for dev; **Bedrock on-demand** for prod (Haiku ~$1/$5 per M tokens, Sonnet ~$3/$15, Opus ~$5/$25 — see `aws_service_groups.md` §29).
- **Production fit**: Bedrock on-demand + **Bedrock Provisioned Throughput** when sustained spend exceeds ~$5k/mo and predictability matters.
- **Premium fit**: Mixed routing via `litellm` — cheap models for cheap tasks, Opus for hard tasks; budget-aware fallbacks; spend limits per model.
- **v2 framing (Tier 1)**: `litellm` is the Python community library named in v2 §4. It provides a single API surface across Bedrock, Ollama, OpenAI, Anthropic direct, Cohere, Vertex, and ~100 other providers — env-driven model strings select the backend.
  ```python
  import litellm, os
  reply = litellm.completion(
      model=os.environ.get("LLM_MODEL", "ollama/llama3.1"),  # "bedrock/anthropic.claude-opus-4-7-..." in cloud
      messages=[{"role": "user", "content": "hi"}],
  )
  ```
- **v2 yaml**: `cloud_only: llm: { type: bedrock.llm, model: "anthropic.claude-opus-4-7-..." }` for the *provisioning marker* (IAM perms, throughput config). User code talks to `litellm`; supeux just ensures the cloud-side identity has the right Bedrock policies attached and the model ID env var is injected.
- **Out of scope for the litellm abstraction (remain `cloud_only` T2)**: Bedrock Knowledge Bases, Bedrock Agents, Bedrock Guardrails — these are AWS-orchestration services with no OSS equivalent. Either hit real AWS from local dev (mixed mode per v2 §4) or skip locally and test cloud-side.

### Vision / OCR (Rekognition + Textract)
- **Local**: `opencv-python` + `ultralytics` (YOLOv8 for detection / segmentation), `tesseract` + `layoutparser` for OCR.
- **Cheapest fit**: Rekognition off-the-shelf APIs ($1 per 1k images for Labels, $1.50–$50 per 1k pages for Textract).
- **Production fit / Premium fit**: SageMaker hosting a fine-tuned model (when off-the-shelf accuracy isn't enough).
- **v2 framing (Tier 1)**: vision is genuinely Tier 1 — same task, different model behind the API. No single community library hides the gap the way `litellm` does for LLMs; the pattern is to wrap behind a small Protocol if you need to swap, or accept that local tests run a different model than prod.

### Speech (Polly TTS, Transcribe STT)
- **Local**: **`openai-whisper`** for STT; `coqui-ai/TTS` or `piper` for TTS.
- **Cheapest / Production fit**: Transcribe ($0.024/min batch), Polly Neural ($16 / M chars).
- **v2 framing (Tier 1)**: whisper is the named community library in v2 §4 for STT. Output formats differ between Whisper and Transcribe (whisper returns segments + text; Transcribe returns rich items); normalize at the boundary.

---

## Summary table

| Local component (Python) | Cheapest fit | Production fit | v2 yaml / tier vocab | Tier |
|---|---|---|---|---|
| postgres | RDS Postgres micro | Aurora Postgres Serverless v2 | `db.sql tier: dev | prod-small | prod | premium | global` | T0 |
| mysql/mariadb | RDS MySQL micro | Aurora MySQL Serverless v2 | `db.sql engine: mysql tier: …` | T0 |
| mongodb | DocumentDB t4g.medium | DocumentDB cluster or Atlas-on-AWS | not a v2 primitive (use `cloud_only` or escape hatch) | T0 happy-path; partial overall |
| redis (cache) | ElastiCache micro | ElastiCache Serverless | `cache tier: …` (v1.x in v2 §6) | T0 |
| redis (pubsub) | ElastiCache | SNS+SQS (rewrite) | migrate to `topic:` + `queue:` | T0 after migration |
| memcached | ElastiCache Memcached | Switch to Redis | (use `cache` primitive) | T0 |
| minio | S3 | S3 + Intelligent-Tiering | `bucket class: standard | intelligent | …` | T0 |
| opensearch | OpenSearch t3.small | OpenSearch r6g cluster | not in v1 PoC; use `terraform-module` escape | T0 |
| pgvector | Aurora Postgres | Aurora Postgres | `db.sql` with `extensions: [vector]` | T0 |
| qdrant/weaviate/chroma | pgvector | OpenSearch k-NN or vendor cloud | not a v2 primitive | T1 if hand-rolled abstraction |
| rabbitmq | Amazon MQ micro | Amazon MQ cluster | `queue kind: standard | fifo` (after SQS migration) or DSN swap to Amazon MQ | T0 |
| kafka | MSK Serverless ($540 floor) | MSK provisioned | not in v1; `terraform-module` or `cloud_only` | T0 wire / Moderate IAM |
| nats | Self-host on Fargate | Self-host on EKS or migrate to SNS+SQS | no v2 primitive — self-host as a `service` | n/a |
| fastapi | Lambda + Mangum | Fargate behind ALB | `service shape: function | long-running` | one container, two shapes (v2 §3) |
| flask | Lambda + aws-wsgi | Fargate or App Runner | `service shape: function | long-running` | same |
| django | Single Fargate | Fargate + Aurora + ElastiCache + S3 | `service shape: long-running` | same |
| celery worker | Fargate + ElastiCache Redis | Fargate + SQS broker (`celery[sqs]`) | `service` + `trigger: { queue: jobs }` | T0 |
| celery beat | Fargate single instance | EventBridge Scheduler → SQS | `triggers: <name>: { schedule: "0 2 * * *", target: worker }` | n/a (cron is a trigger attribute) |
| rq | Fargate + ElastiCache | Same | `service` + Redis-backed queue | T0 |
| dramatiq | Fargate + ElastiCache | Fargate + Amazon MQ | `service` + AMQP-backed queue | T0 |
| apscheduler | EventBridge Scheduler → Lambda | Same | `triggers:` schedule | n/a |
| cron | EventBridge Scheduler → Lambda or RunTask | Same | `triggers:` schedule | n/a |
| nginx/traefik | API Gateway HTTP | ALB | `service expose: { port: …, public: true }` (ALB auto-derived) | n/a |
| keycloak | Cognito (first 10k MAU free) | Cognito or self-host Keycloak | Cognito user-lifecycle is `cloud_only`; token verify via `authlib` JWKS | **T1 (authlib)** |
| vault | SSM Parameter Store (free) | SSM + Secrets Manager + KMS | `secret:` primitive | T0 |
| mailhog | SES | SES + dedicated IP | no primitive; `smtplib` is the abstraction | **T1 (smtplib)** |
| prometheus | CloudWatch Metrics | Amazon Managed Prometheus | not in v1 PoC | n/a |
| grafana | CloudWatch dashboards | Amazon Managed Grafana | not in v1 PoC | n/a |
| loki | CloudWatch Logs | CloudWatch Logs | stdout JSON (collected by runtime) | T0 |
| jaeger/tempo | X-Ray | X-Ray + Application Signals | OTel exporter env var | T0 (OTel) |
| **LLM (Bedrock/Ollama)** | Ollama locally + Bedrock Haiku in cloud | Bedrock Sonnet | `cloud_only: { type: bedrock.llm, model: ... }`; `litellm` in code | **T1 (litellm)** |
| **Vision (Rekognition/Textract)** | Rekognition off-the-shelf | SageMaker fine-tuned | not in v1; small wrapper if you need swap | T1 |
| **Speech (Transcribe/Polly)** | Transcribe + Polly Neural | same | not in v1; whisper / piper locally | T1 (whisper) |

**Tier legend**: T0 = same wire API both sides, env-var swap (v2 §4). T1 = different wire APIs, community library bridges (`litellm`, `authlib`, `smtplib`, `openai-whisper`, etc.). T2 = no local equivalent, `cloud_only:` in IR. See `python_api_diffs.md` for code snippets per pair and `supeux_abstraction_v2.md` §4 for the canonical T0/T1/T2 derivation.

---

See `mapping_aws_to_python.md` for the reverse direction (which container plays the AWS role in dev) and `python_api_diffs.md` for the per-pair Python code diff. Conceptual home: `thesis.md`. Long-form derivation: `supeux_abstraction_v2.md`.
