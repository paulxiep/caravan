# AWS → Python Stack Mapping & Emulation Quality

> ⚠️ **HISTORICAL — pre-SDK research notes.** Current SDK contract lives at [`../rpc/python/`](../rpc/python/) (in-tree at 0.1.0). Authoritative docs are [`thesis.md`](thesis.md) and [`development_plan.md`](development_plan.md). Snapshot 2026-05-16; do not assume any specific row is current.

> **Snapshot date: 2026-05-16.** References `aws_service_groups.md` for AWS-side detail and `mapping_python_to_aws.md` for the reverse direction.
> **Scope**: Python ecosystem. "Wire-compatible" means *boto3 with `endpoint_url` (or a connection-string swap) works without code changes*. Rust mirror lives in `mapping_aws_to_rust.md`.
> **Framing**: Python ecosystem evidence feeding into `thesis.md` (conceptual home) and `caravan_abstraction_v2.md` (long-form derivation). The emulation-quality bands below are **orthogonal to v2's T0/T1/T2 service tiers** — see the note after the bands table.

This file answers: *"I picked an AWS service. What container do I run alongside my Python app so the same code talks to it without knowing the difference?"*

## Emulation-quality bands

| Band | Meaning |
|---|---|
| **wire-compatible** | Same Python client (boto3 or driver) talks to local container via env-driven endpoint/DSN. Behavior matches production for ~95% of common operations. |
| **behavior-compatible** | Same Python client, different connection setup. The engine is real (real Postgres, real Redis) so behavior is honest, but the AWS-specific bits (IAM, snapshots, performance insights) are absent. |
| **partial** | Local container speaks the same wire protocol but lacks features. Most happy-path code works; specific operations error or return wrong shapes. |
| **none viable** | No local container meaningfully reproduces the AWS service's behavior. Either abstract behind a community library at your code boundary (v2 Tier 1), or test against AWS directly. |

Two local-container columns per service:
- **OSS option**: the engine itself (e.g., `postgres:16`, `redis:7`, `minio/minio`).
- **LocalStack option**: `localstack/localstack` (Community = free; Pro = paid). Where Community covers the service it's listed; Pro-only services are flagged.

### Emulation quality vs v2 service tier

The two axes describe different things:

| | Same wire API? | Local emulator faithful? |
|---|---|---|
| **Emulation quality** | not measured here directly | wire-compatible / behavior-compatible / partial / none viable |
| **v2 T0/T1/T2 tier** | T0 = yes (env-var swap is enough); T1 = no (need a community library to bridge); T2 = no AND no OSS engine | not measured |

Loose correspondence: most **wire-compatible** + most **behavior-compatible** entries below are **T0**. **partial** entries split — some are still T0 with a few caveats (DocumentDB ≈ Mongo for happy paths), others become **T1** when a community library (`litellm` for Bedrock, `authlib` for Cognito token-verify, `smtplib` for SES, `openai-whisper` for Transcribe) is what unifies the code. **none viable** is always **T2** — caravan marks `cloud_only:` and the user picks one of v2 §4's four patterns (skip / hit-real / engine-swap / stub).

---

# Web stack core

## 1. Compute — Function (Lambda)

| AWS | OSS option | LocalStack | Quality | Notes |
|---|---|---|---|---|
| Lambda | `public.ecr.aws/lambda/python:3.12` runtime image (run handler in container) | `localstack` Community — Lambda v2 | partial | LocalStack invokes via local Docker. IAM, layers, VPC ENIs are stubbed; cold-start timing differs. Good for happy-path; do NOT rely on for performance or timeout testing. |
| Lambda SnapStart | none | none | none viable | Snapshot/restore mechanics are AWS-internal. |
| Lambda@Edge | none | none | none viable | CDN-edge invocation has no local counterpart. |
| Lambda Function URL | run handler in FastAPI/Flask wrapper | `localstack` Lambda | partial | Function URL itself doesn't matter locally; just invoke the handler. |

**Python idiom for local dev**: per v2 §3 / §9, Lambda is one `shape:` of the `service` primitive, not a separate primitive. Containers-first means the same image deploys two ways — wrap your FastAPI/Flask app with `Mangum(app)` and use the env var `AWS_LAMBDA_RUNTIME_API` (present only inside Lambda) to branch between "run the handler under Mangum" and "serve over a port". Same code, two `shape:` values; caravan generates `aws_lambda_function` Terraform vs `aws_ecs_service` Terraform around the same image. The user wraps the handler ABI in idiomatic Python (`Mangum`) — that wrapper is user code, not caravan code.

---

## 2. Compute — Container

| AWS | OSS option | LocalStack | Quality | Notes |
|---|---|---|---|---|
| ECS / EKS / Fargate / App Runner | `docker compose up` | `localstack` Pro — ECS | wire-compatible (run-the-container sense) | The container itself is identical; only the orchestrator differs. Use docker-compose for local; treat ECS task definitions as deploy-time concern. |

**Python idiom**: container image is the unit of portability. No code changes between local and AWS for the container itself.

---

## 3. Compute — VM / Batch

| AWS | OSS option | LocalStack | Quality | Notes |
|---|---|---|---|---|
| EC2 | Vagrant / Multipass / docker container approximation | `localstack` Community — EC2 API only | partial | Local EC2 is a category mistake — emulate the *workload*, not the VM. |
| AWS Batch | docker-compose service + a queue | `localstack` Pro — Batch | partial | Batch's job dispatching is mocked. Hand-roll a local job queue if you need parity. |
| Lightsail | docker container | `localstack` Pro — Lightsail | partial | Same as EC2. |

---

## 4. Storage — Object (S3)

| AWS | OSS option | LocalStack | Quality | Notes |
|---|---|---|---|---|
| S3 Standard / IA / Glacier | `minio/minio` or `adobe/s3mock` | `localstack` Community — S3 | **wire-compatible** | boto3 with `endpoint_url=http://minio:9000` works. Storage classes are no-ops on minio (it doesn't actually tier). |
| S3 Intelligent-Tiering | minio | localstack | partial | Tiering behavior is mocked / absent. |
| S3 Express One Zone | none | none | none viable | Directory-bucket semantics + 10× perf are AWS-specific. |
| S3 lifecycle / replication | minio (has its own lifecycle DSL) | localstack | partial | Different DSL on minio; LocalStack supports config but not async behavior. |
| S3 Object Lambda | none | partial | partial | LocalStack stubs the API; can't reproduce edge-routing. |

**Python idiom**: 
```python
import boto3
s3 = boto3.client("s3", endpoint_url=os.environ.get("S3_ENDPOINT_URL"))  # None in prod, http://minio:9000 in dev
```
This is the gold-standard cloud↔local pattern. Most code is unchanged.

---

## 5. Storage — File

| AWS | OSS option | LocalStack | Quality | Notes |
|---|---|---|---|---|
| EFS | docker volume + `nfs-ganesha` for true NFS, or just bind-mount | none | partial | If your code does `open("/mnt/efs/...", "w")`, a bind-mount works for behavior parity. NFS-specific issues (lock contention, write coalescing) won't show up locally. |
| FSx Lustre / Windows / ONTAP / OpenZFS | None matched | none | none viable | These are filesystem-specific. Emulate with bind mounts for "files appear" testing; cannot reproduce performance characteristics. |

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
| RDS Postgres | `postgres:16` | `localstack` Pro — RDS API | **behavior-compatible** | Same `psycopg`/`sqlalchemy` code. Use DSN env var to switch. |
| Aurora Postgres / Aurora Serverless v2 | `postgres:16` | localstack Pro | behavior-compatible | Aurora-specific features (read replicas, ACU scaling, Optimized Reads) are AWS-only and irrelevant for local correctness tests. |
| RDS MySQL / Aurora MySQL | `mysql:8` | localstack Pro | behavior-compatible | Same with `pymysql` / `mysqlclient`. |
| RDS MariaDB | `mariadb:11` | localstack Pro | behavior-compatible | Same. |
| RDS for SQL Server | `mcr.microsoft.com/mssql/server:2022-latest` | localstack Pro | behavior-compatible | Same with `pyodbc` / `pymssql`. License limitations on local image. |
| RDS for Oracle | `gvenzl/oracle-xe:21` | localstack Pro | partial | Oracle XE has feature/storage limits vs full Oracle. |
| Aurora DSQL (multi-region) | none | none | none viable | Active-active is a coordination problem; no local equivalent. |

**Python idiom**:
```python
db_url = os.environ["DATABASE_URL"]  # postgres://app:pass@aurora.../db OR postgres://app:pass@postgres:5432/db
engine = create_engine(db_url)
```
Same code, DSN swap. This is the second cleanest cloud↔local pattern after S3.

---

## 8. Database — KV / Document NoSQL

| AWS | OSS option | LocalStack | Quality | Notes |
|---|---|---|---|---|
| DynamoDB | `amazon/dynamodb-local` (official) | `localstack` Community — DynamoDB | **wire-compatible** | boto3 with `endpoint_url=http://dynamodb:8000` works. Streams partially supported; transactions, conditional expressions all good. |
| DynamoDB Global Tables | dynamodb-local × 2 with manual replication | none | partial | Replication is the whole point and it doesn't exist locally. |
| DAX | none | none | none viable | DAX-specific client; no OSS equivalent. Test against vanilla DynamoDB locally. |
| DocumentDB | `mongo:7` | localstack Pro | partial | DocumentDB ≠ real Mongo; testing against real Mongo will reveal *false positives* (your code works locally but fails on DocumentDB). |
| Keyspaces | `cassandra:5` | none | partial | Cassandra ≠ Keyspaces in some ops. |

**Python idiom**: 
```python
import boto3
ddb = boto3.resource("dynamodb", endpoint_url=os.environ.get("DYNAMODB_ENDPOINT_URL"))
```
DynamoDB Local is one of the best-supported AWS-emulation containers. Trustworthy.

---

## 9. Database — Cache

| AWS | OSS option | LocalStack | Quality | Notes |
|---|---|---|---|---|
| ElastiCache Redis (all flavors) | `redis:7-alpine` | localstack Pro | **behavior-compatible** | Same `redis-py`. DSN swap. ElastiCache cluster mode = use redis-cluster locally if you depend on hash-slot semantics. |
| ElastiCache Serverless | `redis:7-alpine` | localstack Pro | behavior-compatible | Serverless behavior (auto-scale, ECPU cost) is invisible to code. |
| ElastiCache Memcached | `memcached:1.6` | localstack Pro | behavior-compatible | Same `pymemcache`. |
| MemoryDB for Redis | `redis:7-alpine` | none | behavior-compatible | MemoryDB's durability (multi-AZ transactional log) doesn't reproduce locally. |

**Python idiom**:
```python
import redis
r = redis.Redis.from_url(os.environ["REDIS_URL"])  # redis://elasticache.../0 or redis://redis:6379/0
```

---

## 10. Database — Time-series / Graph

| AWS | OSS option | LocalStack | Quality | Notes |
|---|---|---|---|---|
| Timestream for LiveAnalytics | `influxdb:2` (closest behavior) | none | none viable | Timestream's SQL surface and tiering are proprietary. |
| Timestream for InfluxDB | `influxdb:2` | none | wire-compatible | Same `influxdb-client`. Same DSN swap. |
| Neptune | `tinkerpop/gremlin-server` (Gremlin) or `neo4j` (Cypher path) | none | partial | Neptune supports Gremlin + SPARQL + openCypher. Locally pick one. |
| Neptune Analytics | none | none | none viable | In-memory engine is AWS-only. |

---

## 11. Database — Search / Vector

| AWS | OSS option | LocalStack | Quality | Notes |
|---|---|---|---|---|
| OpenSearch Service | `opensearchproject/opensearch:2` | localstack Pro | **behavior-compatible** | Same `opensearch-py`. Auto-tuning + UltraWarm are AWS-only. |
| OpenSearch Serverless | `opensearchproject/opensearch:2` | none | behavior-compatible | Auto-scale invisible to code; OCU billing model gone. |
| OpenSearch k-NN | opensearch image with knn plugin (built-in 2.x) | none | behavior-compatible | Same. |
| Aurora pgvector | `pgvector/pgvector:pg16` | none | wire-compatible | Same SQL, same `psycopg` code. |
| S3 Vectors | minio + manual ANN index (e.g. `faiss`) | none | none viable | S3 Vectors API is proprietary; no community shim. |
| Kendra | none | none | none viable | Bag-of-features (NLP search, doc connectors, FAQs) too proprietary. |

---

## 12. Messaging — Queue

| AWS | OSS option | LocalStack | Quality | Notes |
|---|---|---|---|---|
| SQS Standard | `softwaremill/elasticmq-native` (great choice) | `localstack` Community — SQS | **wire-compatible** | ElasticMQ implements SQS API faithfully including long-poll, message attributes, DLQ wiring. |
| SQS FIFO | ElasticMQ supports it | localstack | wire-compatible | Dedup/deduplication-id semantics replicated; check edge cases. |
| Amazon MQ — RabbitMQ | `rabbitmq:3-management` | none | **behavior-compatible** | Real RabbitMQ. Same `pika`/`aio-pika`. |
| Amazon MQ — ActiveMQ | `apache/activemq-classic` | none | behavior-compatible | Same `stomp.py`. |

**Python idiom**:
```python
import boto3
sqs = boto3.client("sqs", endpoint_url=os.environ.get("SQS_ENDPOINT_URL"))
# queue url path differs locally: http://elasticmq:9324/000000000000/queue
```

---

## 13. Messaging — Pub/Sub & Event Bus

| AWS | OSS option | LocalStack | Quality | Notes |
|---|---|---|---|---|
| SNS Standard topic | `localstack` Community — SNS (no good standalone OSS) | localstack | wire-compatible | LocalStack's SNS is solid and well-tested. |
| SNS FIFO topic | localstack | localstack | partial | FIFO semantics partially implemented. |
| SNS Mobile Push (APNs/FCM) | none | localstack Pro | none viable | Real APNs/FCM destination needed for behavior parity. |
| EventBridge default bus | localstack Community — EventBridge | localstack | partial | Rules + targets work for happy paths. Schema registry, archive, replay partial. |
| EventBridge Pipes | localstack Pro | none | partial | Filter + target wiring stubbed. |
| EventBridge Scheduler | localstack Pro | localstack Pro | partial | Schedules fire; precision and group limits differ. |

**Python idiom**: For SNS, same as SQS — boto3 with `endpoint_url`. For EventBridge, prefer testing the consumer (Lambda + event) rather than the bus itself.

---

## 14. Messaging — Streaming

| AWS | OSS option | LocalStack | Quality | Notes |
|---|---|---|---|---|
| Kinesis Data Streams | `localstack` Community — Kinesis | localstack | **wire-compatible** | boto3 with `endpoint_url` works. KCL workers more complex (test against AWS). |
| Kinesis Firehose | localstack Pro | localstack Pro | partial | Destination delivery (S3/Redshift/OpenSearch) needs separate emulators wired together. |
| MSK | `bitnami/kafka:3.7` (real Kafka) | none | **behavior-compatible** | Same `kafka-python` / `confluent-kafka`. Bootstrap-server env swap. |
| MSK Serverless | `bitnami/kafka:3.7` | none | behavior-compatible | Serverless invisible to client. |
| MSK Connect | run Kafka Connect container | none | behavior-compatible | Real Kafka Connect; AWS-managed lifecycle absent. |

---

## 15. API / Web Edge

| AWS | OSS option | LocalStack | Quality | Notes |
|---|---|---|---|---|
| API Gateway REST | `localstack` Community — API Gateway | localstack | partial | Resources/methods/integrations stubbed. Many request/response transformation features absent. |
| API Gateway HTTP | localstack | localstack | partial | Simpler than REST, better LocalStack coverage. |
| API Gateway WebSocket | localstack Pro | localstack Pro | partial | WS connection management partly emulated. |
| AppSync (GraphQL) | localstack Pro | localstack Pro | partial | Resolvers run; subscriptions partial. |
| ALB / NLB / GWLB | `nginx` or `traefik` for L7; `haproxy` for L4 | localstack Pro | partial | LB itself doesn't matter; route the traffic to your container directly. |

**Python idiom**: don't emulate the LB. Run your FastAPI/Flask container on a port and `curl` it directly.

---

## 16. CDN

| AWS | OSS option | LocalStack | Quality | Notes |
|---|---|---|---|---|
| CloudFront | none | localstack Pro | none viable | CDN behavior (POP routing, edge caching) is the point and is unobservable locally. |
| CloudFront Functions / Lambda@Edge | run handler in FastAPI route | none | none viable | Edge-runtime semantics unique. |
| Global Accelerator | none | none | none viable | Anycast IP layer unobservable locally. |

---

## 17. DNS

| AWS | OSS option | LocalStack | Quality | Notes |
|---|---|---|---|---|
| Route 53 | `coredns/coredns` or `/etc/hosts` for tiny cases | localstack Community — Route 53 | partial | Records resolve; health checks, traffic policies partial. |
| Route 53 Resolver | coredns | localstack Pro | partial | VPC-resolver behavior absent. |

---

## 18. Identity / Auth

| AWS | OSS option | LocalStack | Quality | Notes |
|---|---|---|---|---|
| Cognito User Pools (token verification) | `quay.io/keycloak/keycloak:24` realm (or any local OIDC issuer) | localstack Pro — Cognito | partial | **Tier 1**: use `authlib` or `python-jose` to verify JWTs against a JWKS URL both sides. Cognito exposes `https://cognito-idp.<region>.amazonaws.com/<pool_id>/.well-known/jwks.json`; local dev issuer exposes its own well-known. Same `verify(token)` call. See v2 §4. |
| Cognito User Pools (user lifecycle: sign-up, MFA, hosted UI, custom attrs) | none viable locally | localstack Pro | none viable | T2 / `cloud_only`. User-management admin APIs (`boto3.client('cognito-idp').admin_*`) only run cloud-side. |
| Cognito Identity Pools | none | localstack Pro | partial | Returning AWS temp credentials is AWS-only. T2. |
| IAM | `localstack` Community — IAM | localstack | partial | Policies parse; *enforcement* doesn't happen in LocalStack Community. T2 for runtime enforcement. |
| IAM Identity Center | none | none | none viable | Enterprise SSO. T2. |
| Verified Permissions (Cedar) | `cedar-policy` OSS engine | none | behavior-compatible | Cedar is OSS; you can run the policy engine locally. AWS service adds storage + management. T0 for the decision call. |

**Python idiom (Tier 1)**: per v2 §4, the right shape is to verify tokens with a community library and let env vars point at the right JWKS URL — *not* hand-roll a Protocol with two impls. The Cognito vs local-dev split lives in the JWKS URL env var, not in the code path.

```python
from authlib.jose import jwt, JsonWebKey
import httpx, os
jwks = JsonWebKey.import_key_set(httpx.get(os.environ["JWKS_URL"]).json())
claims = jwt.decode(token, jwks)
claims.validate()  # exp, iat, iss, aud
```

User-management admin actions (creating users, resetting passwords, custom attribute writes) are inherently `cloud_only` — there is no portable abstraction; the local-dev experience for those is "skip in dev" or "hit real Cognito via mounted creds".

---

## 19. Secrets / Config

| AWS | OSS option | LocalStack | Quality | Notes |
|---|---|---|---|---|
| Secrets Manager | `localstack` Community — Secrets Manager | localstack | **wire-compatible** | boto3 with `endpoint_url`. Rotation lambdas are the only AWS-specific bit. |
| SSM Parameter Store | localstack Community — SSM | localstack | **wire-compatible** | Same. |
| AppConfig | localstack Pro | localstack Pro | partial | Get-config works; deployment strategies partial. |
| KMS | localstack Community — KMS | localstack | partial | Encrypt/decrypt work with software keys; HSM-backed keys + key policies are real-AWS-only. |

**Python idiom**:
```python
ssm = boto3.client("ssm", endpoint_url=os.environ.get("SSM_ENDPOINT_URL"))
db_password = ssm.get_parameter(Name="/app/db/password", WithDecryption=True)["Parameter"]["Value"]
```

---

## 20. Workflow / Scheduling

| AWS | OSS option | LocalStack | Quality | Notes |
|---|---|---|---|---|
| Step Functions Standard | `amazon/aws-stepfunctions-local` (official, free) | localstack Pro | **wire-compatible** | Official local container for ASL state machines. Surprisingly good. |
| Step Functions Express | aws-stepfunctions-local | localstack Pro | partial | Express semantics not fully tested in local container. |
| Step Functions Distributed Map | none | none | none viable | Distributed Map's parallel-children behavior is AWS-only. |
| EventBridge Scheduler | localstack Pro | localstack Pro | partial | Schedules fire; cron precision differs. |
| MWAA (Managed Airflow) | `apache/airflow:2.10` (real Airflow) | none | behavior-compatible | Real Airflow with same DAG code. AWS-managed plugins (IAM, CloudWatch logs) need stubs. |
| SWF | none | none | none viable | Legacy; no local. |

---

## 21. Email / Notifications

| AWS | OSS option | LocalStack | Quality | Notes |
|---|---|---|---|---|
| SES | `mailhog/mailhog` or `axllent/mailpit` (SMTP catcher) | localstack Community — SES | partial | **Tier 1**: `smtplib` (stdlib) is the abstraction — points at SES SMTP endpoint in cloud, mailhog in dev. `boto3.client('sesv2').send_email(...)` is the alternative when you need SES-specific features (templates, configuration sets). Pick one path per call site. See v2 §4. |
| SNS SMS | localstack Pro | localstack Pro | partial | Real SMS doesn't go anywhere. Inspect what was attempted. |
| SNS Mobile Push | none | localstack Pro | none viable | Real APNs/FCM dispatch is the point. |
| Pinpoint / End User Messaging | localstack Pro | localstack Pro | partial | Campaign orchestration partly stubbed. |

---

## 22. Observability

| AWS | OSS option | LocalStack | Quality | Notes |
|---|---|---|---|---|
| CloudWatch Logs | localstack Community — CloudWatch Logs | localstack | wire-compatible | Logs API works; metric filters partial. |
| CloudWatch Metrics | localstack Community | localstack | partial | PutMetricData works; alarms partial. |
| CloudWatch Alarms | localstack Pro | localstack Pro | partial | Definitions accepted; triggering partial. |
| CloudWatch Synthetics | none | none | none viable | Real browser canaries against real endpoints. |
| CloudWatch RUM | none | none | none viable | Real-user telemetry from real browsers. |
| X-Ray | `amazon/aws-xray-daemon` for local sampling/UDP collection (no UI) | localstack Pro — X-Ray | partial | Daemon catches segments locally; visualizing requires AWS console. Use `jaeger` for local trace UI via OTel. |
| CloudTrail | localstack Pro | localstack Pro | partial | Event logging partly stubbed. |
| Application Signals | none | none | none viable | Auto-instrument-magic is AWS-only. |

---

# Data / analytics

| AWS | OSS option | LocalStack | Quality | Notes |
|---|---|---|---|---|
| Redshift | `postgres:16` (very different perf) or `clickhouse/clickhouse-server` | localstack Pro — Redshift API | partial | Schema-level compatibility w/ Postgres for happy-path queries. Performance + Redshift-specific syntax (DISTKEY/SORTKEY) won't reproduce. |
| Redshift Serverless | same | localstack Pro | partial | Same. |
| Athena | `prestodb/presto` or `trinodb/trino` | localstack Pro — Athena | partial | Trino is the OSS engine Athena is built on. Same Presto SQL dialect for most queries. |
| S3 Select | minio (no S3 Select equivalent) | localstack | none viable | Object-scan-with-SQL is AWS-only. |
| Glue Jobs (Spark) | `bitnami/spark:3.5` | localstack Pro | partial | Real Spark works. Glue's specific DynamicFrame API + bookmarks won't. |
| Glue Crawlers | none | localstack Pro | partial | Schema discovery is best emulated with hand-defined schemas locally. |
| Glue Data Catalog | `apache/hive:3` Metastore | localstack Pro | partial | Hive Metastore is the protocol Glue Catalog implements. |
| Glue DataBrew | none | none | none viable | UI-driven; no local equivalent. |
| Lake Formation | none | none | none viable | Permissions overlay is AWS-only. |
| EMR | `bitnami/spark:3.5` (Spark only) | localstack Pro | partial | Run Spark locally; EMR's cluster lifecycle absent. |
| EMR Serverless | bitnami/spark | localstack Pro | partial | Same. |
| QuickSight | `metabase/metabase`, `superset` | none | none viable | Specific dashboard authoring won't port. |

---

# ML / AI

| AWS | OSS option | LocalStack | Quality | Notes |
|---|---|---|---|---|
| SageMaker Training | `python:3.12` + your training script | localstack Pro — SageMaker | partial | Local-mode SageMaker SDK can run training inside a container that mimics SageMaker contract (paths under `/opt/ml/...`). Half-decent. |
| SageMaker Real-Time Inference | run model in `python:3.12` + FastAPI | localstack Pro | partial | Same — the model serving contract is straightforward. |
| SageMaker Serverless Inference | same | none | partial | Same. |
| SageMaker Batch Transform | run script over S3-pointed input | localstack Pro | partial | Same. |
| SageMaker Studio | `jupyter/scipy-notebook` | none | partial | Jupyter is the OSS layer; studio adds AWS integrations. |
| SageMaker JumpStart / Canvas | none | none | none viable | Bundled model marketplace + no-code UI. |
| Bedrock — Claude/Llama/Mistral/Nova/Titan | **`ollama/ollama`** (local LLM serving) or `vllm/vllm-openai` | localstack Pro — Bedrock | partial | **Tier 1**: `litellm` is the named Python community library in v2 §4 — one API surface across Bedrock + Ollama + OpenAI + Anthropic-direct + Cohere + ~100 others. Env-driven model string (`bedrock/anthropic.claude-opus-4-7-...` vs `ollama/llama3.1`) selects the backend; no Protocol-with-two-impls needed. |
| Bedrock Knowledge Bases (RAG) | OpenSearch + your own retrieval | none | none viable | T2. Orchestration is the value-add; no community lib bridges it. Either hit real AWS from local (v2 §4 "hit-real" pattern) or skip in local dev. |
| Bedrock Agents | none | localstack Pro | none viable | T2. Tool-use orchestration is proprietary. |
| Bedrock Guardrails | OSS filters (`presidio`, `nemo-guardrails`) — different philosophy | none | none viable | T2. |
| Rekognition (Image/Video) | OpenCV / `torchvision` / `yolov8` | localstack Pro | partial | Tasks (object detect, face match) exist in OSS but ML model is different. |
| Textract | `tesseract-ocr/tesseract` + `layoutparser` | localstack Pro | partial | OCR works; forms/tables much weaker. |
| Polly (TTS) | `coqui-ai/TTS` or `piper` | none | partial | Voice quality and SSML support differ. |
| Transcribe (STT) | `openai/whisper` (open model) | none | partial | Output format differs. |
| Comprehend | `spacy` + custom models | none | partial | Sentiment/NER doable locally. |
| Translate | `argos-translate` or `meta-llm-translate` | none | partial | Quality gap on production languages. |
| Forecast / Personalize | none (Forecast deprecated; Personalize requires AWS data pipeline) | none | none viable | — |

**Python idiom for Bedrock (Tier 1)**: per v2 §4, use `litellm` directly — it ships with provider routers for Bedrock, Ollama, OpenAI, Anthropic-direct, Cohere, Vertex, and others under one `litellm.completion(model=..., messages=...)` call. Env-driven model strings select the backend:

```python
import litellm, os
reply = litellm.completion(
    model=os.environ.get("LLM_MODEL", "ollama/llama3.1"),
    messages=[{"role": "user", "content": "hi"}],
)
```

The previously common pattern of hand-rolling an `LLMClient` Protocol with `BedrockLLM` + `OllamaLLM` impls is the v1 prescription — v2 §4 explicitly states that caravan does not ship runtime adapter libraries when mature community libraries (litellm here) already cover the abstraction. Bedrock Knowledge Bases / Agents / Guardrails remain `cloud_only` (T2) — litellm doesn't bridge those.

---

# IoT / edge

| AWS | OSS option | LocalStack | Quality | Notes |
|---|---|---|---|---|
| IoT Core (MQTT) | `eclipse-mosquitto:2` | localstack Pro — IoT | behavior-compatible | Mosquitto is real MQTT. Same `paho-mqtt` Python client. AWS-specific bits (thing shadow, jobs) absent. |
| IoT Core (HTTPS / WS) | `eclipse-mosquitto:2` with WS listener | localstack Pro | partial | Same. |
| IoT Device Management | none | localstack Pro | none viable | Fleet operations specific to AWS. |
| IoT Device Defender | none | none | none viable | Anomaly detection on AWS-side data flow. |
| IoT Rules Engine | none | localstack Pro | partial | Rules tie IoT Core → other AWS services; needs ecosystem stubs. |
| Greengrass V2 | run `greengrass-runtime` in container (AWS provides) | none | behavior-compatible | Official local runtime. |
| FreeRTOS | run on local hardware or QEMU | none | wire-compatible | OSS RTOS; runs anywhere. |
| IoT Analytics / SiteWise / Events / TwinMaker / FleetWise | none | localstack Pro (some) | none viable | Domain-specific. |

---

# Summary: emulation-quality scoreboard

| Quality band | Count | Examples |
|---|---|---|
| **wire-compatible** | ~12 | S3, DynamoDB, SQS, SNS, Kinesis Data Streams, Secrets Manager, SSM Parameter Store, CloudWatch Logs, Step Functions Standard, Aurora pgvector, Influx-flavored Timestream, FreeRTOS |
| **behavior-compatible** | ~10 | RDS/Aurora (Postgres/MySQL/MariaDB/SQL Server), ElastiCache (Redis/Memcached), MemoryDB, OpenSearch, Amazon MQ (RabbitMQ/ActiveMQ), MSK, MWAA, IoT Core MQTT, Greengrass, Verified Permissions |
| **partial** | ~25 | Lambda, API Gateway, EventBridge, Cognito, KMS, Athena, Glue, X-Ray, SageMaker training/inference, Rekognition/Textract/Polly/Transcribe, Bedrock |
| **none viable** | ~15 | Lambda@Edge, CloudFront, Global Accelerator, IAM enforcement, IAM Identity Center, S3 Vectors, S3 Express One Zone, S3 Select, S3 Object Lambda, Aurora DSQL, DAX, Neptune Analytics, Kendra, Bedrock Knowledge Bases / Agents / Guardrails, CloudWatch Synthetics/RUM/AppSignals, CloudFront Functions, SNS Mobile Push, IoT Device Management/Defender, IoT Analytics+ family, Forecast, Personalize, SageMaker JumpStart/Canvas |

**Implications for caravan** (developed in `caravan_abstraction_v2.md`):
- The ~22 wire-or-behavior-compatible services map to **v2 Tier 0** — caravan's job is env-var injection (endpoint URL or DSN). No abstraction library, no runtime SDK. This is v2's "containers-first" bread and butter.
- The ~25 partial services split. Those with a mature Python community library (Cognito token verify → `authlib`/`python-jose`; SES → `smtplib`; Bedrock LLM core → `litellm`; Transcribe → `openai-whisper`; Rekognition/Textract → opencv-python/ultralytics/tesseract) are **v2 Tier 1** — caravan documents which library to import; the abstraction lives in user code via that library. Those without a clean community bridge (advanced API Gateway features, EventBridge schema registry, etc.) stay close to cloud-only.
- The ~15 none-viable services are **v2 Tier 2** — `cloud_only:` in the IR. caravan refuses to generate a local stand-in; user picks one of v2 §4's four patterns (skip / hit-real / engine-swap / stub) per service.

See `python_api_diffs.md` for the actual code-diff per pair. Conceptual home: `thesis.md`. Long-form derivation of T0/T1/T2 and the v1 PoC scope: `caravan_abstraction_v2.md`.
