# AWS → TypeScript Stack Mapping & Emulation Quality

> ⚠️ **HISTORICAL — pre-SDK research notes; TypeScript SDK is namespace-reserved only.** Current SDK namespace at [`../rpc/typescript/`](../rpc/typescript/) is a 0.0.1 placeholder; TypeScript is out of PoC scope per [`development_plan.md`](development_plan.md). Snapshot 2026-05-16; do not assume any specific row is current.

> **Snapshot date: 2026-05-16.** References `aws_service_groups.md` for AWS-side detail and `mapping_typescript_to_aws.md` for the reverse direction.
> **Scope**: TypeScript / JavaScript ecosystem. "Wire-compatible" means *the official `@aws-sdk/client-*` package (or relevant driver) talks to a local container via an `endpoint` config option or DSN swap without code changes*. Python and Rust mirrors live in `mapping_aws_to_python.md` / `mapping_aws_to_rust.md`.
> **Framing**: TypeScript ecosystem evidence feeding into `thesis.md` (conceptual home) and `caravan_abstraction_v3.md` (long-form derivation; supersedes v2). The emulation-quality bands below are **orthogonal to v3's T0/T1/T2 service tiers** — see the note after the bands table.

This file answers: *"I picked an AWS service. What container do I run alongside my TypeScript app so the same code talks to it without knowing the difference?"*

## Emulation-quality bands

| Band | Meaning |
|---|---|
| **wire-compatible** | Same TS package (`@aws-sdk/client-*` or driver) talks to local container via env-driven `endpoint` / DSN. Behavior matches production for ~95% of common operations. |
| **behavior-compatible** | Same TS package, different connection setup. The engine is real (real Postgres, real Redis) so behavior is honest, but the AWS-specific bits (IAM, snapshots, performance insights) are absent. |
| **partial** | Local container speaks the same wire protocol but lacks features. Most happy-path code works; specific operations error or return wrong shapes. |
| **none viable** | No local container meaningfully reproduces the AWS service's behavior. Either abstract behind a community library at your code boundary (v3 Tier 1), or test against AWS directly. |

Two local-container columns per service:
- **OSS option**: the engine itself (e.g., `postgres:16`, `redis:7`, `minio/minio`).
- **LocalStack option**: `localstack/localstack` (Community = free; Pro = paid). Where Community covers the service it's listed; Pro-only services are flagged.

**The TypeScript idiom for cloud↔local switching** is the AWS SDK v3 client-config pattern — every `@aws-sdk/client-*` constructor accepts an `endpoint` option:

```ts
import { S3Client } from "@aws-sdk/client-s3";
const s3 = new S3Client({
  endpoint: process.env.S3_ENDPOINT_URL,     // undefined → real S3; http://minio:9000 → local
  forcePathStyle: !!process.env.S3_ENDPOINT_URL,
});
```

Closer in shape to boto3 than to the Rust type-state Builder — one option-bag, one constructor.

**Runtime note**: snippets assume **Node 22** unless otherwise stated. **Bun** (`oven/bun:1`) works as a drop-in for most of these (it implements the Node API surface that `@aws-sdk/*` and the drivers below need); Lambda support for Bun is community/experimental (`bun-lambda` custom runtime). **Deno** is container-baseline-only — `deno compile` produces a static binary that runs under Fargate fine, but Lambda runtime support is niche.

### Emulation quality vs v3 service tier

The two axes describe different things:

| | Same wire API? | Local emulator faithful? |
|---|---|---|
| **Emulation quality** | not measured here directly | wire-compatible / behavior-compatible / partial / none viable |
| **v3 T0/T1/T2 tier** | T0 = yes (env-var swap is enough); T1 = no (need a community library to bridge); T2 = no AND no OSS engine | not measured |

Loose correspondence: most **wire-compatible** + most **behavior-compatible** entries below are **T0**. **partial** entries split — some are still T0 with a few caveats (DocumentDB ≈ Mongo for happy paths), others become **T1** when a community library (Vercel AI SDK for Bedrock, `jose` for Cognito token-verify, `nodemailer` for SES, `@xenova/transformers` for Whisper-shaped STT) is what unifies the code. **none viable** is always **T2** — caravan marks `cloud_only:` and the user picks one of v3 §4's four patterns (skip / hit-real / engine-swap / stub).

---

# Web stack core

## 1. Compute — Function (Lambda)

| AWS | OSS option | LocalStack | Quality | Notes |
|---|---|---|---|---|
| Lambda (Node) | `public.ecr.aws/lambda/nodejs:22` runtime image | `localstack` Community — Lambda v2 | partial | LocalStack invokes via local Docker. IAM, layers, VPC ENIs stubbed; cold-start timing differs. Good for happy-path; do NOT rely on for performance or timeout testing. |
| Lambda (Bun custom runtime) | `oven/bun:1` + `bun-lambda` adapter on `provided.al2023` | none | partial | Bun-on-Lambda is community/experimental; ecosystem coverage uneven. Use Node 22 unless you have a specific reason. |
| Lambda SnapStart | none | none | none viable | Node cold-starts are 100–500 ms typical; SnapStart helps the JVM more than Node. AWS-internal snapshot mechanics anyway. |
| Lambda@Edge | none | none | none viable | CDN-edge invocation has no local counterpart. Edge runtime is JS-only — Node 20 subset; no `@aws-sdk/*` available at the edge. |
| Lambda Function URL | run handler under Hono / Express / Fastify Lambda adapter | `localstack` Lambda | partial | Function URL itself doesn't matter locally; just invoke the handler. |

**TS idiom for local dev**: per v3 §3 / §9, Lambda is one `shape:` of the `service` primitive, not a separate primitive. Containers-first means the same image deploys two ways — wrap your Hono/Express/Fastify app with the framework's Lambda adapter and branch on the `AWS_LAMBDA_RUNTIME_API` env var (present only inside Lambda) to switch between "export the handler" and "listen on a port".

Three canonical adapter idioms (all three covered in the api_diffs file):

```ts
// Hono — closest analogue to Rust's lambda_http; runs on Node/Bun/Deno/CF Workers
import { Hono } from "hono";
import { handle } from "hono/aws-lambda";
import { serve } from "@hono/node-server";
const app = new Hono().get("/hi", (c) => c.json({ msg: "hi" }));
export const handler = handle(app);
if (!process.env.AWS_LAMBDA_RUNTIME_API) serve({ fetch: app.fetch, port: 8080 });
```

```ts
// Express + serverless-http — biggest hiring pool, conservative pick
import express from "express";
import serverless from "serverless-http";
const app = express();
app.get("/hi", (_req, res) => res.json({ msg: "hi" }));
export const handler = serverless(app);
if (!process.env.AWS_LAMBDA_RUNTIME_API) app.listen(8080);
```

```ts
// Fastify + @fastify/aws-lambda — performant, mature plugin
import Fastify from "fastify";
import awsLambdaFastify from "@fastify/aws-lambda";
const app = Fastify();
app.get("/hi", async () => ({ msg: "hi" }));
export const handler = awsLambdaFastify(app);
if (!process.env.AWS_LAMBDA_RUNTIME_API) app.listen({ port: 8080, host: "0.0.0.0" });
```

Same container image, two `shape:` values; caravan generates `aws_lambda_function` Terraform vs `aws_ecs_service` Terraform around the same image. The user wraps the handler ABI in their framework's idiomatic adapter — that wrapper is user code, not caravan code.

---

## 2. Compute — Container

| AWS | OSS option | LocalStack | Quality | Notes |
|---|---|---|---|---|
| ECS / EKS / Fargate / App Runner | `docker compose up` | `localstack` Pro — ECS | wire-compatible (run-the-container sense) | The container itself is identical; only the orchestrator differs. A Node 22 multi-stage build to `node:22-alpine` is ~80–120 MB; Bun single-binary builds are ~50 MB. |

**TS idiom**: container image is the unit of portability. For local AWS creds, `@aws-sdk/credential-providers` reads from `~/.aws/credentials` mounted into the container (`-v ~/.aws:/root/.aws:ro`). No code changes between local and AWS for the container itself.

---

## 3. Compute — VM / Batch

| AWS | OSS option | LocalStack | Quality | Notes |
|---|---|---|---|---|
| EC2 | docker container approximation | `localstack` Community — EC2 API only | partial | Local EC2 is a category mistake — emulate the *workload*, not the VM. |
| AWS Batch | docker-compose service + a queue | `localstack` Pro — Batch | partial | Batch's job dispatching is mocked. Hand-roll a local job queue if you need parity. |
| Lightsail | docker container | `localstack` Pro — Lightsail | partial | Same as EC2. |

---

## 4. Storage — Object (S3)

| AWS | OSS option | LocalStack | Quality | Notes |
|---|---|---|---|---|
| S3 Standard / IA / Glacier | `minio/minio` or `adobe/s3mock` | `localstack` Community — S3 | **wire-compatible** | `@aws-sdk/client-s3` with `{ endpoint, forcePathStyle: true }` works. Storage classes are no-ops on minio. |
| S3 Intelligent-Tiering | minio | localstack | partial | Tiering behavior is mocked / absent. |
| S3 Express One Zone | none | none | none viable | Directory-bucket semantics + 10× perf are AWS-specific. |
| S3 lifecycle / replication | minio (has its own lifecycle DSL) | localstack | partial | Different DSL on minio. |
| S3 Object Lambda | none | partial | partial | LocalStack stubs the API; can't reproduce edge-routing. |

**TS idiom**:
```ts
import { S3Client, PutObjectCommand } from "@aws-sdk/client-s3";
const s3 = new S3Client({
  endpoint: process.env.S3_ENDPOINT_URL,
  forcePathStyle: !!process.env.S3_ENDPOINT_URL,
});
await s3.send(new PutObjectCommand({ Bucket: "my-bucket", Key: "hello.txt", Body: "hi" }));
```
This is the gold-standard cloud↔local pattern in TS, same as boto3's. `forcePathStyle: true` is the minio-specific gotcha (same as Rust's `force_path_style(true)`).

---

## 5. Storage — File

| AWS | OSS option | LocalStack | Quality | Notes |
|---|---|---|---|---|
| EFS | docker volume + `nfs-ganesha` for true NFS, or bind-mount | none | partial | Node `fs.promises` and Bun's `Bun.file` work transparently against bind-mounted volumes. NFS-specific lock contention won't show up locally. |
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
| RDS Postgres | `postgres:16` | `localstack` Pro — RDS API | **behavior-compatible** | Same drivers: `pg` (node-postgres), `postgres` (porsager/postgres), `prisma`, `drizzle-orm`, `kysely`, `typeorm`. DSN swap. |
| Aurora Postgres / Aurora Serverless v2 | `postgres:16` | localstack Pro | behavior-compatible | Aurora-specific features (read replicas, ACU scaling, Optimized Reads) are AWS-only and irrelevant for local correctness tests. |
| RDS MySQL / Aurora MySQL | `mysql:8` | localstack Pro | behavior-compatible | Drivers: `mysql2` (preferred async), `prisma`, `drizzle`, `typeorm`. |
| RDS MariaDB | `mariadb:11` | localstack Pro | behavior-compatible | Same. |
| RDS for SQL Server | `mcr.microsoft.com/mssql/server:2022-latest` | localstack Pro | behavior-compatible | `mssql` npm. License limitations on local image. |
| RDS for Oracle | `gvenzl/oracle-xe:21` | localstack Pro | partial | `oracledb` npm requires Oracle Instant Client; awkward toolchain. |
| Aurora DSQL (multi-region) | none | none | none viable | Active-active is a coordination problem; no local equivalent. |

**TS idiom**:
```ts
import { Pool } from "pg";
const pool = new Pool({ connectionString: process.env.DATABASE_URL });
// AWS:   postgres://app:****@aurora-cluster.cluster-xyz.us-east-1.rds.amazonaws.com:5432/app
// Local: postgres://app:dev@postgres:5432/app
```
Same drivers, DSN swap. Cleanest pattern after S3.

**TS-specific gotcha (Prisma)**: Prisma's migration engine is a per-platform binary. Multi-arch Docker builds (e.g., Fargate on Graviton arm64 + dev on x86 macs) need the right `binaryTargets` in `schema.prisma`: `["native", "linux-musl-arm64-openssl-3.0.x", "linux-musl-openssl-3.0.x"]`. Missing this is the most common "works locally, breaks in Fargate" failure mode for Prisma users.

---

## 8. Database — KV / Document NoSQL

| AWS | OSS option | LocalStack | Quality | Notes |
|---|---|---|---|---|
| DynamoDB | `amazon/dynamodb-local` (official) | `localstack` Community — DynamoDB | **wire-compatible** | `@aws-sdk/client-dynamodb` + `@aws-sdk/lib-dynamodb` (Document client, recommended) with `{ endpoint }`. Streams partial; transactions + conditional expressions solid. |
| DynamoDB Global Tables | dynamodb-local × 2 with manual replication | none | partial | Replication is the whole point and doesn't exist locally. |
| DAX | none | none | none viable | DAX-specific client; no OSS equivalent. Test against vanilla DynamoDB locally. |
| DocumentDB | `mongo:7` | localstack Pro | partial | `mongodb` npm. DocumentDB ≠ real Mongo; testing against real Mongo will reveal *false positives*. |
| Keyspaces | `cassandra:5` | none | partial | `cassandra-driver` npm; Cassandra ≠ Keyspaces in some ops. |

**TS idiom**:
```ts
import { DynamoDBClient } from "@aws-sdk/client-dynamodb";
import { DynamoDBDocumentClient, PutCommand } from "@aws-sdk/lib-dynamodb";
const base = new DynamoDBClient({ endpoint: process.env.DYNAMODB_ENDPOINT_URL });
const ddb = DynamoDBDocumentClient.from(base);
await ddb.send(new PutCommand({ TableName: "items", Item: { pk: "u#1", sk: "profile", name: "Alice" } }));
```
`@aws-sdk/lib-dynamodb` removes the boto3-style `{ S: "..."}` attribute-type wrapping — closest to the boto3 `resource("dynamodb")` ergonomics.

---

## 9. Database — Cache

| AWS | OSS option | LocalStack | Quality | Notes |
|---|---|---|---|---|
| ElastiCache Redis (all flavors) | `redis:7-alpine` | localstack Pro | **behavior-compatible** | `ioredis` (preferred, cluster-aware) or `redis` (node-redis v4+). DSN swap. |
| ElastiCache Serverless | `redis:7-alpine` | localstack Pro | behavior-compatible | Serverless behavior invisible to client. |
| ElastiCache Memcached | `memcached:1.6` | localstack Pro | behavior-compatible | `memjs` npm (preferred async). |
| MemoryDB for Redis | `redis:7-alpine` | none | behavior-compatible | MemoryDB's durability doesn't reproduce locally. |

**TS idiom**:
```ts
import Redis from "ioredis";
const r = new Redis(process.env.REDIS_URL!);  // redis://master.cache-cluster.xyz... or redis://redis:6379/0
await r.set("session:abc", "user-123", "EX", 3600);
```
Cluster-mode-enabled ElastiCache: use `new Redis.Cluster([...])` instead — different constructor; if you depend on cluster mode, run `bitnami/redis-cluster` locally.

---

## 10. Database — Time-series / Graph

| AWS | OSS option | LocalStack | Quality | Notes |
|---|---|---|---|---|
| Timestream for LiveAnalytics | `influxdb:2` (closest behavior) | none | none viable | Timestream's SQL surface and tiering are proprietary. |
| Timestream for InfluxDB | `influxdb:2` | none | wire-compatible | `@influxdata/influxdb-client`. URL + token swap. |
| Neptune | `tinkerpop/gremlin-server` (Gremlin) or `neo4j` (Cypher path) | none | partial | `gremlin` npm or `neo4j-driver`. Neptune supports Gremlin + SPARQL + openCypher; pick one locally. |
| Neptune Analytics | none | none | none viable | In-memory engine is AWS-only. |

---

## 11. Database — Search / Vector

| AWS | OSS option | LocalStack | Quality | Notes |
|---|---|---|---|---|
| OpenSearch Service | `opensearchproject/opensearch:2` | localstack Pro | **behavior-compatible** | `@opensearch-project/opensearch` (official). URL swap. Auto-tuning + UltraWarm are AWS-only. |
| OpenSearch Serverless | `opensearchproject/opensearch:2` | none | behavior-compatible | Auto-scale invisible to code; OCU billing model gone. |
| OpenSearch k-NN | opensearch image with knn plugin (built-in 2.x) | none | behavior-compatible | Same. |
| Aurora pgvector | `pgvector/pgvector:pg16` | none | wire-compatible | `pgvector` npm integrates with `pg` / `drizzle` / `kysely`. Same SQL. |
| S3 Vectors | minio + manual ANN index (e.g. `hnswlib-node`) | none | none viable | S3 Vectors API is proprietary; no community shim. |
| Kendra | none | none | none viable | Bag-of-features (NLP search, doc connectors, FAQs) too proprietary. |

---

## 12. Messaging — Queue

| AWS | OSS option | LocalStack | Quality | Notes |
|---|---|---|---|---|
| SQS Standard | `softwaremill/elasticmq-native` | `localstack` Community — SQS | **wire-compatible** | `@aws-sdk/client-sqs` with `{ endpoint }`. Long-poll, DLQ wiring, FIFO dedup all work. |
| SQS FIFO | ElasticMQ supports it | localstack | wire-compatible | Dedup/deduplication-id semantics replicated; check edge cases. |
| Amazon MQ — RabbitMQ | `rabbitmq:3-management` | none | **behavior-compatible** | `amqplib` (canonical). Real RabbitMQ. |
| Amazon MQ — ActiveMQ | `apache/activemq-classic` | none | behavior-compatible | `stompit` npm (STOMP). Mature-but-niche. |

**TS idiom**:
```ts
import { SQSClient, SendMessageCommand } from "@aws-sdk/client-sqs";
const sqs = new SQSClient({ endpoint: process.env.SQS_ENDPOINT_URL });
await sqs.send(new SendMessageCommand({
  QueueUrl: process.env.QUEUE_URL!,
  MessageBody: "job#42",
}));
```

---

## 13. Messaging — Pub/Sub & Event Bus

| AWS | OSS option | LocalStack | Quality | Notes |
|---|---|---|---|---|
| SNS Standard topic | `localstack` Community — SNS (no good standalone OSS) | localstack | wire-compatible | `@aws-sdk/client-sns`. LocalStack's SNS is solid and well-tested. |
| SNS FIFO topic | localstack | localstack | partial | FIFO semantics partially implemented. |
| SNS Mobile Push (APNs/FCM) | none | localstack Pro | none viable | Real APNs/FCM destination needed for behavior parity. |
| EventBridge default bus | localstack Community — EventBridge | localstack | partial | `@aws-sdk/client-eventbridge`. Rules + targets work for happy paths. Schema registry, archive, replay partial. |
| EventBridge Pipes | localstack Pro | none | partial | Filter + target wiring stubbed. |
| EventBridge Scheduler | localstack Pro | localstack Pro | partial | `@aws-sdk/client-scheduler`. Schedules fire; precision and group limits differ. |

---

## 14. Messaging — Streaming

| AWS | OSS option | LocalStack | Quality | Notes |
|---|---|---|---|---|
| Kinesis Data Streams | `localstack` Community — Kinesis | localstack | **wire-compatible** | `@aws-sdk/client-kinesis` with `{ endpoint }`. KCL workers more complex (no native TS port). |
| Kinesis Firehose | localstack Pro | localstack Pro | partial | Destination delivery (S3/Redshift/OpenSearch) needs separate emulators wired together. |
| MSK | `bitnami/kafka:3.7` (real Kafka) | none | **behavior-compatible** | `kafkajs` (preferred pure-JS) or `confluent-kafka-javascript` (librdkafka FFI). Bootstrap-server env swap. |
| MSK Serverless | `bitnami/kafka:3.7` | none | behavior-compatible | Serverless invisible to client. |
| MSK Connect | run Kafka Connect container | none | behavior-compatible | Real Kafka Connect; AWS-managed lifecycle absent. |

**TS-specific friction**: `kafkajs` SASL/IAM support for MSK has been less mature than the Java/Python equivalents; the maintainer-recommended path for MSK-IAM is `confluent-kafka-javascript` (released GA in 2024) or sticking to SCRAM-SHA-512 / mTLS auth.

---

## 15. API / Web Edge

| AWS | OSS option | LocalStack | Quality | Notes |
|---|---|---|---|---|
| API Gateway REST | `localstack` Community — API Gateway | localstack | partial | Resources/methods/integrations stubbed. Many request/response transformation features absent. |
| API Gateway HTTP | localstack | localstack | partial | Use Hono/Express/Fastify Lambda adapter to share handler code between Lambda+APIGW HTTP and a standalone server. `@aws-lambda-powertools/*` packages help with logging/tracing/metrics inside Lambda. |
| API Gateway WebSocket | localstack Pro | localstack Pro | partial | Stateful-connection model is APIGW-specific (connections stored in DDB; push via `@aws-sdk/client-apigatewaymanagementapi`). `ws` / `socket.io` per-process model is different. |
| AppSync (GraphQL) | localstack Pro | localstack Pro | partial | Server-side: `apollo-server` or `graphql-yoga` for local. AppSync resolvers + subscriptions need adapters. |
| ALB / NLB / GWLB | `nginx` or `traefik` for L7; `haproxy` for L4 | localstack Pro | partial | LB itself doesn't matter; route the traffic to your container directly. |

**TS idiom**: don't emulate the LB. Run your Hono/Express/Fastify container on a port and `curl` it directly.

---

## 16. CDN

| AWS | OSS option | LocalStack | Quality | Notes |
|---|---|---|---|---|
| CloudFront | none | localstack Pro | none viable | CDN behavior (POP routing, edge caching) is the point and is unobservable locally. |
| CloudFront Functions / Lambda@Edge | (none worth) | none | none viable | Edge runtime is a JS subset — no Node API, no `fs`/`net`/`@aws-sdk/*`, 1MB code limit, 10MB memory. Test routing as a pure function; don't try to emulate. |
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
| Cognito User Pools (token verification) | `quay.io/keycloak/keycloak:24` realm (or any local OIDC issuer) | localstack Pro — Cognito | partial | **Tier 1**: use **`jose`** to verify JWTs against a JWKS URL both sides. Cognito exposes `https://cognito-idp.<region>.amazonaws.com/<pool_id>/.well-known/jwks.json`; local dev issuer exposes its own well-known. One `jwtVerify(token, jwks)` call. See v3 §4. |
| Cognito User Pools (user lifecycle: sign-up, MFA, hosted UI, custom attrs) | none viable locally | localstack Pro | none viable | T2 / `cloud_only`. User-management admin APIs (`@aws-sdk/client-cognito-identity-provider` `admin*` commands) only run cloud-side. |
| Cognito Identity Pools | none | localstack Pro | partial | Returning AWS temp credentials is AWS-only. T2. |
| IAM | `localstack` Community — IAM | localstack | partial | Policies parse; *enforcement* doesn't happen in LocalStack Community. T2 for runtime enforcement. |
| IAM Identity Center | none | none | none viable | Enterprise SSO. T2. |
| Verified Permissions (Cedar) | `@cedar-policy/cedar-wasm` (wasm wrapper of the Rust engine) | none | behavior-compatible | Cedar is OSS; the wasm package wraps the canonical Rust engine. AWS service adds storage + management. T0 for the decision call. |

**TS idiom (Tier 1)**: per v3 §4, verify tokens with `jose` and let env vars point at the right JWKS URL — *not* hand-roll an interface with two impls. The Cognito vs local-dev split lives in the JWKS URL env var, not in the code path.

```ts
import { createRemoteJWKSet, jwtVerify } from "jose";
const jwks = createRemoteJWKSet(new URL(process.env.JWKS_URL!));
// Cloud:  https://cognito-idp.<region>.amazonaws.com/<pool_id>/.well-known/jwks.json
// Local:  http://keycloak:8080/realms/dev/protocol/openid-connect/certs
const { payload } = await jwtVerify(token, jwks, { issuer, audience });
```

User-management admin actions (creating users, resetting passwords, custom attribute writes) are inherently `cloud_only` — there is no portable abstraction; the local-dev experience for those is "skip in dev" or "hit real Cognito via mounted creds".

---

## 19. Secrets / Config

| AWS | OSS option | LocalStack | Quality | Notes |
|---|---|---|---|---|
| Secrets Manager | `localstack` Community — Secrets Manager | localstack | **wire-compatible** | `@aws-sdk/client-secrets-manager` with `{ endpoint }`. Rotation lambdas are the only AWS-specific bit. |
| SSM Parameter Store | localstack Community — SSM | localstack | **wire-compatible** | `@aws-sdk/client-ssm`. |
| AppConfig | localstack Pro | localstack Pro | partial | `@aws-sdk/client-appconfigdata`. Get-config works; deployment strategies partial. |
| KMS | localstack Community — KMS | localstack | partial | `@aws-sdk/client-kms`. Encrypt/decrypt work with software keys; HSM-backed keys + key policies are real-AWS-only. |

**TS idiom**:
```ts
import { SSMClient, GetParameterCommand } from "@aws-sdk/client-ssm";
const ssm = new SSMClient({ endpoint: process.env.SSM_ENDPOINT_URL });
const out = await ssm.send(new GetParameterCommand({ Name: "/app/db/password", WithDecryption: true }));
const password = out.Parameter?.Value;
```

---

## 20. Workflow / Scheduling

| AWS | OSS option | LocalStack | Quality | Notes |
|---|---|---|---|---|
| Step Functions Standard | `amazon/aws-stepfunctions-local` (official, free) | localstack Pro | **wire-compatible** | `@aws-sdk/client-sfn` with `{ endpoint: "http://stepfunctions-local:8083" }`. Surprisingly good. |
| Step Functions Express | aws-stepfunctions-local | localstack Pro | partial | Express semantics not fully tested in local container. |
| Step Functions Distributed Map | none | none | none viable | Distributed Map's parallel-children behavior is AWS-only. |
| EventBridge Scheduler | localstack Pro | localstack Pro | partial | Schedules fire; cron precision differs. |
| MWAA (Managed Airflow) | `apache/airflow:2.10` (real Airflow) | none | behavior-compatible | Airflow is Python-only — TS ops invoke DAGs via API; cross-language seam. |
| SWF | none | none | none viable | Legacy; no local. |

**TS idiom** for in-process scheduling (local dev only): `node-cron` or `node-schedule` (simple); `bullmq` if you also need job persistence (cron + queue in one).

---

## 21. Email / Notifications

| AWS | OSS option | LocalStack | Quality | Notes |
|---|---|---|---|---|
| SES | `mailhog/mailhog` or `axllent/mailpit` (SMTP catcher) | localstack Community — SES | partial | **Tier 1**: **`nodemailer`** is the abstraction — points at SES SMTP endpoint in cloud, mailhog in dev. `@aws-sdk/client-ses` (or `client-sesv2`) is the alternative when you need SES-specific features (templates, configuration sets). Pick one path per call site. See v3 §4. |
| SNS SMS | localstack Pro | localstack Pro | partial | Real SMS doesn't go anywhere. Inspect what was attempted. |
| SNS Mobile Push | none | localstack Pro | none viable | Real APNs/FCM dispatch is the point. |
| Pinpoint / End User Messaging | localstack Pro | localstack Pro | partial | Campaign orchestration partly stubbed. |

---

## 22. Observability

| AWS | OSS option | LocalStack | Quality | Notes |
|---|---|---|---|---|
| CloudWatch Logs | stdout (Fargate awslogs driver / Lambda automatic) | localstack | wire-compatible | Best practice: emit JSON to stdout via `pino` or `winston`. Almost no TS app should call `@aws-sdk/client-cloudwatch-logs` `PutLogEvents` directly. |
| CloudWatch Metrics | `@aws-lambda-powertools/metrics` (EMF in logs), or `@aws-sdk/client-cloudwatch` | localstack | partial | EMF (embedded metric format) in logs avoids PutMetricData costs and JSON-shapes metrics for CloudWatch. |
| CloudWatch Alarms | localstack Pro | localstack Pro | partial | Definitions accepted; triggering partial. |
| CloudWatch Synthetics | none | none | none viable | Real browser canaries against real endpoints. |
| CloudWatch RUM | none | none | none viable | Real-user telemetry from real browsers. |
| X-Ray | `amazon/aws-xray-daemon` for local sampling/UDP collection (no UI) | localstack Pro — X-Ray | partial | Daemon catches segments locally; visualizing requires AWS console. Use `jaeger` for local trace UI via OTel. |
| CloudTrail | localstack Pro | localstack Pro | partial | Event logging partly stubbed. |
| Application Signals | none | none | none viable | Auto-instrument-magic is AWS-only. |

**TS idiom** (OTel + tracing):
```ts
import { NodeSDK } from "@opentelemetry/sdk-node";
import { OTLPTraceExporter } from "@opentelemetry/exporter-trace-otlp-grpc";
const sdk = new NodeSDK({
  traceExporter: new OTLPTraceExporter({
    url: process.env.OTEL_EXPORTER_OTLP_ENDPOINT ?? "http://jaeger:4317",
  }),
});
sdk.start();
// AWS:   ADOT collector → X-Ray
// Local: jaeger
```

---

# Data / analytics

| AWS | OSS option | LocalStack | Quality | Notes |
|---|---|---|---|---|
| Redshift | `postgres:16` (very different perf) or `clickhouse/clickhouse-server` | localstack Pro — Redshift API | partial | Redshift speaks Postgres wire protocol; use `pg`. DISTKEY/SORTKEY syntax won't reproduce. |
| Redshift Serverless | same | localstack Pro | partial | Same. |
| Athena | `prestodb/presto` or `trinodb/trino` | localstack Pro — Athena | partial | `presto-client` npm or query via REST. Trino is the OSS engine Athena is built on. |
| S3 Select | minio (no S3 Select equivalent) | localstack | none viable | Object-scan-with-SQL is AWS-only. |
| Glue Jobs (Spark) | `bitnami/spark:3.5` | localstack Pro | partial | No native TS Spark. Cross-language seam (Python/Scala for Spark; TS for orchestration). |
| Glue Crawlers | none | localstack Pro | partial | Schema discovery is best emulated with hand-defined schemas locally. |
| Glue Data Catalog | `apache/hive:3` Metastore | localstack Pro | partial | Hive Metastore is the protocol Glue Catalog implements. |
| Glue DataBrew | none | none | none viable | UI-driven; no local equivalent. |
| Lake Formation | none | none | none viable | Permissions overlay is AWS-only. |
| EMR | `bitnami/spark:3.5` (Spark only) | localstack Pro | partial | Cross-language seam. |
| EMR Serverless | bitnami/spark | localstack Pro | partial | Same. |
| QuickSight | `metabase/metabase`, `superset` | none | none viable | Specific dashboard authoring won't port. |

**TS analytics note**: for single-node OLAP in TS, `duckdb` (via `@duckdb/node-api`) gives a Postgres-shaped SQL surface with strong column-store performance. For team-scale analytics, those still ship to JVM-based services.

---

# ML / AI

| AWS | OSS option | LocalStack | Quality | Notes |
|---|---|---|---|---|
| SageMaker Training | `python:3.12` + your training script | localstack Pro — SageMaker | partial | Rust/TS ML training is niche; most production training is Python on SageMaker. Cross-language seam. |
| SageMaker Real-Time Inference | run model in TS via `onnxruntime-node` or call out to a Python container | localstack Pro | partial | Same — the model serving contract is straightforward. |
| SageMaker Serverless Inference | same | none | partial | Same. |
| SageMaker Batch Transform | run script over S3-pointed input | localstack Pro | partial | Same. |
| SageMaker Studio | none | none | partial | Jupyter is Python-centric; TS notebook kernels exist but Studio integrations don't follow. |
| SageMaker JumpStart / Canvas | none | none | none viable | Bundled model marketplace + no-code UI. |
| Bedrock — Claude/Llama/Mistral/Nova/Titan | **`ollama/ollama`** (local LLM serving) or `vllm/vllm-openai` | localstack Pro — Bedrock | partial | **Tier 1**: **Vercel AI SDK** (`ai` + `@ai-sdk/amazon-bedrock` + `ollama-ai-provider`) is the named TS community library in v3 §4 — one API surface across Bedrock + Ollama + OpenAI + Anthropic-direct + Cohere + Vertex + many others. Env-driven model selects the backend. |
| Bedrock Knowledge Bases (RAG) | OpenSearch + your own retrieval | none | none viable | T2. Orchestration is the value-add; no community lib bridges it. Either hit real AWS from local (v3 §4 "hit-real" pattern) or skip in local dev. |
| Bedrock Agents | none | localstack Pro | none viable | T2. Tool-use orchestration is proprietary. |
| Bedrock Guardrails | OSS filters (custom or via `@xenova/transformers`) — different philosophy | none | none viable | T2. |
| Rekognition (Image/Video) | `onnxruntime-node` + ONNX-exported YOLO models, or `@xenova/transformers` for CLIP/DETR | localstack Pro | partial | Tasks (object detect, face match) exist in OSS but ML model is different. |
| Textract | `tesseract.js` + layout heuristics | localstack Pro | partial | OCR works; forms/tables much weaker. |
| Polly (TTS) | (no first-class TS TTS) — call out to Coqui-TTS / piper Python service | none | partial | Voice quality and SSML support differ. |
| Transcribe (STT) | **`@xenova/transformers`** (Whisper.js, ONNX in Node) | none | partial | Output format differs (Whisper returns segments + text; Transcribe returns rich items). |
| Comprehend | `@xenova/transformers` (sentiment / NER models) | none | partial | Sentiment/NER doable locally. |
| Translate | `@xenova/transformers` (NLLB or M2M100 models) | none | partial | Quality gap on production languages. |
| Forecast / Personalize | none (Forecast deprecated; Personalize requires AWS data pipeline) | none | none viable | — |

**TS idiom for Bedrock (Tier 1)**: per v3 §4, use the Vercel AI SDK directly — its provider router covers Bedrock, Ollama, OpenAI, Anthropic-direct, Cohere, Vertex, and others under one `generateText({ model, messages })` call. Env-driven model selects the backend:

```ts
import { generateText } from "ai";
import { bedrock } from "@ai-sdk/amazon-bedrock";
import { ollama } from "ollama-ai-provider";

const provider = process.env.LLM_BACKEND === "bedrock" ? bedrock : ollama;
const model = provider(process.env.LLM_MODEL ?? "llama3.1");
const { text } = await generateText({ model, prompt: "hi" });
```

The previously common pattern of hand-rolling an `LLMClient` interface with `BedrockLLM` + `OllamaLLM` impls is the v1-era prescription — v3 §4 explicitly states that caravan does not ship runtime adapter libraries when mature community libraries (Vercel AI SDK here) already cover the abstraction. Bedrock Knowledge Bases / Agents / Guardrails remain `cloud_only` (T2) — the AI SDK doesn't bridge those.

---

# IoT / edge

| AWS | OSS option | LocalStack | Quality | Notes |
|---|---|---|---|---|
| IoT Core (MQTT) | `eclipse-mosquitto:2` | localstack Pro — IoT | behavior-compatible | `mqtt` npm (pure-JS MQTT client). AWS-specific bits (thing shadow, jobs) absent; for those use `aws-iot-device-sdk-v2`. |
| IoT Core (HTTPS / WS) | `eclipse-mosquitto:2` with WS listener | localstack Pro | partial | `mqtt` supports WS. |
| IoT Device Management | none | localstack Pro | none viable | Fleet operations specific to AWS. |
| IoT Device Defender | none | none | none viable | Anomaly detection on AWS-side data flow. |
| IoT Rules Engine | none | localstack Pro | partial | Rules tie IoT Core → other AWS services; needs ecosystem stubs. |
| Greengrass V2 | run `greengrass-runtime` in container (AWS provides) | none | behavior-compatible | Official local runtime; JS components supported since 2.0. |
| FreeRTOS | run on local hardware or QEMU | none | wire-compatible | OSS RTOS; no JS at this layer — irrelevant for TS users. |
| IoT Analytics / SiteWise / Events / TwinMaker / FleetWise | none | localstack Pro (some) | none viable | Domain-specific. |

---

# Summary: emulation-quality scoreboard

| Quality band | Count | Examples |
|---|---|---|
| **wire-compatible** | ~12 | S3, DynamoDB, SQS, SNS, Kinesis (producer), Secrets Manager, SSM Parameter Store, CloudWatch Logs (via stdout), Step Functions Standard, Aurora pgvector, Influx-flavored Timestream |
| **behavior-compatible** | ~10 | RDS/Aurora (Postgres/MySQL/MariaDB/SQL Server), ElastiCache (Redis/Memcached), MemoryDB, OpenSearch, Amazon MQ (RabbitMQ/ActiveMQ), MSK, IoT Core MQTT, Greengrass, Verified Permissions (Cedar) |
| **partial** | ~25 | Lambda, API Gateway, EventBridge, Cognito, KMS, AppConfig, Athena (Trino), Glue (Spark cross-lang), X-Ray, SageMaker training/inference, Rekognition/Polly/Transcribe (different OSS models), Bedrock (different OSS models), CloudWatch Metrics |
| **none viable** | ~15 | Lambda@Edge, CloudFront, CloudFront Functions, Global Accelerator, IAM enforcement, IAM Identity Center, S3 Vectors, S3 Express One Zone, S3 Select, S3 Object Lambda, Aurora DSQL, DAX, Neptune Analytics, Kendra, Bedrock Knowledge Bases / Agents / Guardrails, CloudWatch Synthetics / RUM / Application Signals, SNS Mobile Push, IoT Device Management / Defender / Analytics / SiteWise / TwinMaker / FleetWise, Forecast, Personalize, SageMaker JumpStart / Canvas, Step Functions Distributed Map |

**TS-specific notes vs Python and Rust coverage**:
- Trivially-equivalent counts (~12 wire / ~10 behavior) are essentially identical to Python's and Rust's. The `@aws-sdk/client-*` packages' `endpoint` option is the universal Trivial-band enabler — same shape as boto3's `endpoint_url=` kwarg.
- A few cells are *less* mature on the TS side and worth flagging:
  - **MSK with IAM auth** — `kafkajs` doesn't natively sign with SigV4; use `confluent-kafka-javascript` (librdkafka FFI, 2024+) or prefer SCRAM-SHA-512 / mTLS.
  - **Polly TTS** — no first-class TS TTS; cross-language to Python (Coqui-TTS / piper).
  - **SageMaker training** — Python-shaped; TS users orchestrate cross-language.
- A few cells are *cleaner* on the TS side:
  - **Vercel AI SDK** is the most ergonomic Tier 1 LLM abstraction across the three languages — fewer lines than Rust's `rig` and richer per-provider features than `litellm`.
  - **`jose`** is the modern, audited JWT library — first-class JWKS support out of the box.
  - **`@xenova/transformers`** brings Whisper / CLIP / NLLB / sentiment models to plain Node via ONNX — no Python dependency.
  - **Hono** is the cleanest "one container, two shapes" framework — runs on Node, Bun, Deno, AND CloudFlare Workers; `hono/aws-lambda` is the closest analogue to Rust's `lambda_http`.

**Implications for caravan** (developed in `caravan_abstraction_v3.md`):
- The ~22 wire-or-behavior-compatible services map to **v3 Tier 0** — caravan's job is env-var injection (endpoint URL or DSN). No abstraction library, no runtime SDK.
- The ~25 partial services split. Those with a mature TS community library (Cognito token verify → `jose`; SES → `nodemailer`; Bedrock LLM core → Vercel AI SDK; Whisper-shaped STT → `@xenova/transformers`) are **v3 Tier 1** — caravan documents which library to import; the abstraction lives in user code via that library. Those without a clean community bridge (advanced API Gateway features, EventBridge schema registry, etc.) stay close to cloud-only.
- The ~15 none-viable services are **v3 Tier 2** — `cloud_only:` in the IR. caravan refuses to generate a local stand-in; user picks one of v3 §4's four patterns (skip / hit-real / engine-swap / stub) per service.

**TS-side IaC tooling context (anchors v3 §5 / §7b reasoning)**:
- **CDKtf** (`cdktf`, Cloud Development Kit for Terraform) was sunset and archived **December 10, 2025** by HashiCorp/IBM, citing "no product-market fit at scale" — see the [HashiCorp CDKtf page](https://developer.hashicorp.com/terraform/cdktf). As of 2026 the project is archived and read-only; no further updates ship. HashiCorp directs former CDKtf users to HCL, Pulumi, or AWS CDK.
- **Pulumi-TS** remains available and well-supported, but emits resources via imperative TS code rather than reviewable HCL artifacts — security/compliance teams typically prefer the latter for production deploys.
- **AWS CDK** (TS) emits CloudFormation, not Terraform/HCL; it ties users to AWS and to an opaque-by-default deploy artifact (synthesized CloudFormation is technically inspectable but isn't part of the workflow).
- **Net**: as of 2026 there is no first-party HCL-emitting-from-TS toolchain. caravan fills that gap polyglot-first by emitting HCL from yaml (see `caravan_abstraction_v3.md` §5, §7b observation 3). The user's TS code remains containerized application code; no `import caravan` or imperative IaC SDK is needed.

See `typescript_api_diffs.md` for the actual code-diff per pair. Conceptual home: `thesis.md`. Long-form derivation of T0/T1/T2 and the v1 PoC scope: `caravan_abstraction_v3.md` (supersedes v2).
