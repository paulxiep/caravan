# TypeScript API Diffs: AWS ↔ Local Container

> ⚠️ **HISTORICAL — pre-SDK research notes; TypeScript SDK is namespace-reserved only.** Current SDK namespace at [`../rpc/typescript/`](../rpc/typescript/) is a 0.0.1 placeholder; TypeScript is out of PoC scope per [`development_plan.md`](development_plan.md). Snapshot 2026-05-16; do not assume any specific row is current.

> **Snapshot date: 2026-05-16.** References `aws_service_groups.md`, `mapping_typescript_to_aws.md`, `mapping_aws_to_typescript.md`.
> **Framing**: TypeScript ecosystem evidence feeding into `thesis.md` (conceptual home) and `caravan_abstraction_v3.md` (long-form derivation; supersedes v2). The difficulty bands below map onto v3's T0/T1/T2 service tiers — see the row at the bottom of the bands table.

For each AWS↔local pair surfaced in the mapping files, this file shows the exact TypeScript code change required to switch between them and assigns a **difficulty band**:

| Band | Meaning | What caravan does | v3 tier |
|---|---|---|---|
| **Trivial** | One env var (endpoint URL or DSN) controls the switch. Same imports, same calls. | Sets env vars at deploy. Done. | **T0** |
| **Moderate** | Same library, but a few config keys or call shapes differ; or a small adapter (framework Lambda wrapper, env-driven branches) closes the gap. | Documents the adapter shape. Usually no library needed. | **T0** (mostly); occasionally T1 |
| **Hard** | Different wire APIs cloud vs local; a structural abstraction is required. | **Uses the recommended TS community library** — Vercel AI SDK for LLMs, `jose` for token verification, `nodemailer` for email, `@xenova/transformers` for Whisper-shaped STT. caravan **does not ship** a runtime adapter library; see v3 §4. | **T1** |
| **Intractable** | No realistic local equivalent. Don't try to emulate — false positives hide bugs. | Marks `cloud_only:` in the yaml IR (v3 §6, §8). User picks one of v3's four patterns per service: **skip** (feature-flag off locally), **hit-real** (mounted creds; pay real $$), **engine-swap** (DAX→DDB-local, S3 Vectors→hnswlib, etc.), or **stub**. | **T2** |

Snippets are ≤15 lines each and assume `process.env` is populated by the caravan runtime / docker-compose / GHA matrix. **Runtime**: snippets assume Node 22; flagged when Bun differs.

---

# Trivial — env-driven `endpoint` or DSN swap

These are the wins. A single env var flips cloud↔local with no code change.

## S3 ↔ MinIO

```ts
import { S3Client, PutObjectCommand } from "@aws-sdk/client-s3";
const s3 = new S3Client({
  endpoint: process.env.S3_ENDPOINT_URL,              // undefined → real S3; http://minio:9000 → local
  forcePathStyle: !!process.env.S3_ENDPOINT_URL,      // required for minio; AWS rejects it
});
await s3.send(new PutObjectCommand({ Bucket: "my-bucket", Key: "hello.txt", Body: "hi" }));
```
**Verdict: Trivial.** Same `forcePathStyle` gotcha as Rust. Caveats: minio doesn't do storage-class tiering or strong-read-after-write under partial failures. For 95% of code, same.

## DynamoDB ↔ dynamodb-local

```ts
import { DynamoDBClient } from "@aws-sdk/client-dynamodb";
import { DynamoDBDocumentClient, PutCommand } from "@aws-sdk/lib-dynamodb";
const base = new DynamoDBClient({
  endpoint: process.env.DYNAMODB_ENDPOINT_URL,        // http://dynamodb:8000 locally
  region: process.env.AWS_REGION ?? "us-east-1",
});
const ddb = DynamoDBDocumentClient.from(base);
await ddb.send(new PutCommand({
  TableName: "items",
  Item: { pk: "u#1", sk: "profile", name: "Alice" },
}));
```
**Verdict: Trivial.** `@aws-sdk/lib-dynamodb`'s Document client removes attribute-type wrapping (`{ S: "..."}`) — closest to boto3's `resource("dynamodb")`. Streams + TTL deletes are partial in dynamodb-local; transactions, conditional writes are solid.

## SQS ↔ ElasticMQ / localstack

```ts
import { SQSClient, SendMessageCommand, ReceiveMessageCommand } from "@aws-sdk/client-sqs";
const sqs = new SQSClient({ endpoint: process.env.SQS_ENDPOINT_URL });
const QueueUrl = process.env.QUEUE_URL!;
// https://sqs... in AWS; http://elasticmq:9324/000000000000/queue locally
await sqs.send(new SendMessageCommand({ QueueUrl, MessageBody: "job#42" }));
const { Messages } = await sqs.send(new ReceiveMessageCommand({ QueueUrl, WaitTimeSeconds: 20 }));
```
**Verdict: Trivial.** ElasticMQ implements long-poll, DLQ wiring, FIFO dedup. Make the queue URL itself an env var, not a constructed string.

## SNS ↔ localstack

```ts
import { SNSClient, PublishCommand } from "@aws-sdk/client-sns";
const sns = new SNSClient({ endpoint: process.env.SNS_ENDPOINT_URL });
await sns.send(new PublishCommand({
  TopicArn: process.env.TOPIC_ARN!,
  Message: "event",
}));
```
**Verdict: Trivial.** ARN format differs locally (`arn:aws:sns:us-east-1:000000000000:topic-name`) — pass it as env var.

## Kinesis Data Streams ↔ localstack

```ts
import { KinesisClient, PutRecordCommand } from "@aws-sdk/client-kinesis";
const k = new KinesisClient({ endpoint: process.env.KINESIS_ENDPOINT_URL });
await k.send(new PutRecordCommand({
  StreamName: "events",
  PartitionKey: "user-1",
  Data: new TextEncoder().encode(JSON.stringify({ type: "click" })),
}));
```
**Verdict: Trivial for producer.** No native TS KCL port; high-throughput consumer code is harder. For consumers, prefer Lambda triggered by Kinesis or raw `GetRecords` against shards.

## Secrets Manager / SSM Parameter Store ↔ localstack

```ts
import { SSMClient, GetParameterCommand } from "@aws-sdk/client-ssm";
const ssm = new SSMClient({ endpoint: process.env.SSM_ENDPOINT_URL });
const out = await ssm.send(new GetParameterCommand({
  Name: "/app/db/password",
  WithDecryption: true,
}));
const password = out.Parameter?.Value;
```
**Verdict: Trivial.** Same for `@aws-sdk/client-secrets-manager`. KMS-decryption is software-only locally.

## CloudWatch Logs ↔ stdout

```ts
import pino from "pino";
const log = pino({ level: "info" });
log.info({ orderId }, "processed order");
```
**Verdict: Trivial.** Best practice: emit JSON to stdout via `pino` (or `winston`). Lambda → CloudWatch automatic; Fargate → awslogs driver; locally → `docker logs`. Almost no TS app should call `@aws-sdk/client-cloudwatch-logs` `PutLogEvents` directly.

## Step Functions ↔ aws-stepfunctions-local

```ts
import { SFNClient, StartExecutionCommand } from "@aws-sdk/client-sfn";
const sf = new SFNClient({ endpoint: process.env.STEPFUNCTIONS_ENDPOINT_URL });
await sf.send(new StartExecutionCommand({
  stateMachineArn: process.env.STATE_MACHINE_ARN!,
  input: JSON.stringify({ orderId: "o-123" }),
}));
```
**Verdict: Trivial.** ASL state machine definitions deploy identically. AWS provides the official local container (`amazon/aws-stepfunctions-local`). Tasks targeting real AWS services (Lambda, DynamoDB) need their own endpoint overrides via env vars (`AWS_STEPFUNCTIONS_LAMBDA_ENDPOINT`, etc.) — the local container supports them.

## RDS / Aurora Postgres ↔ postgres container

```ts
import { Pool } from "pg";
const pool = new Pool({ connectionString: process.env.DATABASE_URL });
// AWS:   postgres://app:****@aurora-cluster.cluster-xyz.us-east-1.rds.amazonaws.com:5432/app
// Local: postgres://app:dev@postgres:5432/app
await pool.query("INSERT INTO users (name) VALUES ($1)", ["Alice"]);
```
**Verdict: Trivial.** Same `pg`, same SQL. Aurora-specific features (read replicas, Optimized Reads) are runtime — not visible to your code.
**TS gotcha (Prisma)**: `binaryTargets` in `schema.prisma` must include the Fargate arch (`linux-musl-arm64-openssl-3.0.x` for Graviton; `linux-musl-openssl-3.0.x` for x86). Missing this is the canonical "works locally, breaks in Fargate" Prisma failure.

## pgvector (Aurora) ↔ pgvector container

```ts
import { Pool } from "pg";
import pgvector from "pgvector/pg";
const pool = new Pool({ connectionString: process.env.DATABASE_URL });
const client = await pool.connect();
await pgvector.registerType(client);
await client.query("CREATE EXTENSION IF NOT EXISTS vector;");
const embedding = new Array(1536).fill(0.1);
await client.query("INSERT INTO docs (id, embedding) VALUES ($1, $2)", [1, pgvector.toSql(embedding)]);
client.release();
```
**Verdict: Trivial.** Same extension, same syntax. The `pgvector` npm package provides typed integrations with `pg`, `drizzle`, `kysely`, `prisma`.

## RDS / Aurora MySQL ↔ mysql container

```ts
import mysql from "mysql2/promise";
const pool = mysql.createPool({ uri: process.env.DATABASE_URL });
// AWS:   mysql://app:****@aurora-mysql.cluster-xyz.us-east-1.rds.amazonaws.com:3306/app
// Local: mysql://app:dev@mysql:3306/app
await pool.execute("INSERT INTO users (name) VALUES (?)", ["Alice"]);
```
**Verdict: Trivial.**

## ElastiCache Redis ↔ redis container

```ts
import Redis from "ioredis";
const r = new Redis(process.env.REDIS_URL!);
// AWS:   redis://master.cache-cluster.xyz.cache.amazonaws.com:6379/0  (or rediss:// for TLS)
// Local: redis://redis:6379/0
await r.set("session:abc", "user-123", "EX", 3600);
```
**Verdict: Trivial.** Cluster-mode-enabled ElastiCache requires `new Redis.Cluster([...])` — different constructor. If you depend on cluster mode, run `bitnami/redis-cluster` locally.

## DocumentDB ↔ mongo container

```ts
import { MongoClient } from "mongodb";
const client = new MongoClient(process.env.MONGO_URL!);
await client.connect();
await client.db("app").collection("users").insertOne({ name: "Alice" });
```
**Verdict: Trivial for happy path, partial in general.** DocumentDB is wire-compatible with Mongo but lacks ~30% of aggregation operators (esp. `$lookup` semantics, change-stream resumability quirks). If your code uses modern aggregations, real Mongo locally gives *false positives*. Test critical paths against DocumentDB in CI.

## OpenSearch Service ↔ opensearch container

```ts
import { Client } from "@opensearch-project/opensearch";
const client = new Client({
  node: process.env.OPENSEARCH_URL!,                  // https://...es.amazonaws.com:443 vs http://opensearch:9200
  auth: process.env.OS_USER ? { username: process.env.OS_USER, password: process.env.OS_PASS! } : undefined,
  ssl: { rejectUnauthorized: (process.env.OS_USE_SSL ?? "true") === "true" },
});
await client.index({ index: "docs", body: { title: "hello" } });
```
**Verdict: Trivial.** Same `@opensearch-project/opensearch` library. Use the OpenSearch image (not Elasticsearch) — the post-fork divergence on `@elastic/elasticsearch` ≥8 is hostile.

## MSK ↔ kafka container

```ts
import { Kafka } from "kafkajs";
const kafka = new Kafka({
  clientId: "app",
  brokers: process.env.KAFKA_BOOTSTRAP!.split(","),
  // AWS:   b-1.msk-cluster...kafka.us-east-1.amazonaws.com:9094 (TLS) or :9098 (IAM)
  // Local: kafka:9092
});
const producer = kafka.producer();
await producer.connect();
await producer.send({ topic: "events", messages: [{ value: "hello" }] });
```
**Verdict: Trivial for SASL_SSL or plaintext modes; Moderate for IAM auth.** `kafkajs` doesn't natively sign SigV4 for MSK-IAM — use `confluent-kafka-javascript` (librdkafka FFI, 2024+ GA) for MSK-IAM, or prefer SCRAM-SHA-512 / mTLS. Same Rust/Python posture.

## Amazon MQ RabbitMQ ↔ rabbitmq container

```ts
import amqp from "amqplib";
const conn = await amqp.connect(process.env.RABBITMQ_URL!);
// AWS:   amqps://user:****@b-xyz.mq.us-east-1.amazonaws.com:5671
// Local: amqp://guest:guest@rabbitmq:5672/
const ch = await conn.createChannel();
await ch.assertQueue("jobs");
ch.sendToQueue("jobs", Buffer.from("job-1"));
```
**Verdict: Trivial.** Real RabbitMQ both sides; only TLS differs.

## IoT Core MQTT ↔ mosquitto container

```ts
import mqtt from "mqtt";
const c = mqtt.connect({
  host: process.env.MQTT_HOST!,
  port: parseInt(process.env.MQTT_PORT ?? "1883"),
  protocol: process.env.MQTT_TLS === "true" ? "mqtts" : "mqtt",
  // AWS IoT Core requires mTLS with X.509 device certs:
  ...(process.env.MQTT_TLS === "true" && { key: process.env.MQTT_KEY, cert: process.env.MQTT_CERT }),
});
c.on("connect", () => c.publish("telemetry/sensor1", JSON.stringify({ temp: 22.5 })));
```
**Verdict: Trivial wire; Moderate auth.** mosquitto can be configured with or without TLS; IoT Core mandates mTLS with X.509. Real cert provisioning is the auth-shaped seam.

## SES ↔ mailhog (via SMTP using `nodemailer`)

```ts
import nodemailer from "nodemailer";
const transport = nodemailer.createTransport({
  host: process.env.SMTP_HOST,
  port: parseInt(process.env.SMTP_PORT ?? "25"),
  secure: process.env.SMTP_SECURE === "true",
  auth: process.env.SMTP_USER ? { user: process.env.SMTP_USER, pass: process.env.SMTP_PASS! } : undefined,
});
await transport.sendMail({
  from: "noreply@app.com", to: "user@example.com",
  subject: "Hi", text: "hello",
});
```
**Verdict: Trivial — and `nodemailer` is the v3 Tier 1 lib.** AWS SES has SMTP endpoint credentials. Locally, mailhog accepts on `mailhog:1025` no-auth. Same env-driven config both sides. (Mechanically Trivial; classified Tier 1 because `nodemailer` itself is the abstraction layer that hides cloud↔local — without it you'd need separate SMTP-vs-`@aws-sdk/client-ses` branches.)

## Timestream for InfluxDB ↔ influxdb container

```ts
import { InfluxDB, Point } from "@influxdata/influxdb-client";
const client = new InfluxDB({ url: process.env.INFLUX_URL!, token: process.env.INFLUX_TOKEN! });
const writeApi = client.getWriteApi(process.env.INFLUX_ORG!, "metrics");
writeApi.writePoint(new Point("cpu").floatField("usage", 42.0));
await writeApi.close();
```
**Verdict: Trivial.** Same `@influxdata/influxdb-client`; URL + token swap.

## Verified Permissions ↔ Cedar (wasm)

```ts
import { Authorizer } from "@cedar-policy/cedar-wasm";
const authorizer = new Authorizer();
const decision = authorizer.isAuthorized({
  principal: 'User::"alice"',
  action: 'Action::"view"',
  resource: 'Doc::"d1"',
  context: {},
  policies: process.env.CEDAR_POLICIES!,
  entities: "[]",
});
// decision.decision === "Allow" | "Deny"
```
**Verdict: Trivial.** `@cedar-policy/cedar-wasm` wraps the canonical Rust Cedar engine — the same code AWS Verified Permissions runs server-side. Verified Permissions adds storage + lifecycle; the *decision* is identical locally.

---

# Moderate — same library, configuration / behavior differs

## "One container, two shapes" — Hono (recommended modern default)

Per v3 §3 / §9, Lambda is one `shape:` of the `service` primitive — not a separate primitive. caravan generates `aws_lambda_function` Terraform around the same container image when `shape: function`, or `aws_ecs_service` Terraform when `shape: long-running`. The user's container handles the ABI in framework-idiomatic code.

```ts
// src/index.ts — runs as Lambda OR standalone server from one binary
import { Hono } from "hono";
import { handle } from "hono/aws-lambda";
import { serve } from "@hono/node-server";

const app = new Hono().get("/hi", (c) => c.json({ message: `hi ${c.req.query("name")}` }));

export const handler = handle(app);   // → Lambda container-image entry point

if (!process.env.AWS_LAMBDA_RUNTIME_API) {
  serve({ fetch: app.fetch, port: 8080 });   // → Fargate / App Runner / local docker-compose
}
```
**Verdict: Moderate (T0 in v3's tier system).** The seam is one `if` statement; the same container deploys both ways. Hono's `hono/aws-lambda` is the TS closest analogue to Rust's `lambda_http` — runs on Node, Bun, Deno, and CloudFlare Workers from one source. caravan's only job is to inject env vars the same way it does for any other service.

## "One container, two shapes" — Express + `serverless-http`

```ts
import express from "express";
import serverless from "serverless-http";

const app = express();
app.get("/hi", (req, res) => res.json({ message: `hi ${req.query.name}` }));

export const handler = serverless(app);

if (!process.env.AWS_LAMBDA_RUNTIME_API) {
  app.listen(8080);
}
```
**Verdict: Moderate.** Biggest hiring pool, npm-ecosystem-mature, slower cold-start than Hono / Fastify. `@vendia/serverless-express` is the AWS-blessed alternative wrapper with the same shape.

## "One container, two shapes" — Fastify + `@fastify/aws-lambda`

```ts
import Fastify from "fastify";
import awsLambdaFastify from "@fastify/aws-lambda";

const app = Fastify({ logger: true });
app.get("/hi", async (req) => ({ message: `hi ${(req.query as { name?: string }).name ?? ""}` }));

export const handler = awsLambdaFastify(app);

if (!process.env.AWS_LAMBDA_RUNTIME_API) {
  app.listen({ port: 8080, host: "0.0.0.0" });
}
```
**Verdict: Moderate.** Performant (~2× Express throughput), mature plugin ecosystem, first-party Lambda story. Middle pick between Express and Hono.

**Constraints inherited from Lambda regardless of framework**: websockets need API Gateway WebSocket (separate primitive, deferred to v1.1+); streaming responses need Lambda Function URLs with response streaming on; per-cold-start startup runs the entire `import` graph. None are caravan concerns — they're Lambda properties.

## BullMQ worker — Redis backend (local) vs SQS backend (cloud-leaning)

```ts
// worker.ts — backend swap at startup
import { Worker } from "bullmq";

const connection = process.env.REDIS_URL
  ? { url: process.env.REDIS_URL }
  : undefined;   // SQS-backed BullMQ via bullmq-sqs is community/experimental

new Worker(
  "orders",
  async (job) => { await processOrder(job.data); },
  { connection: connection! },
);
```

For SQS-first patterns, drop BullMQ and use raw SDK:

```ts
import { SQSClient, ReceiveMessageCommand, DeleteMessageCommand } from "@aws-sdk/client-sqs";
const sqs = new SQSClient({ endpoint: process.env.SQS_ENDPOINT_URL });
while (true) {
  const { Messages = [] } = await sqs.send(new ReceiveMessageCommand({
    QueueUrl: process.env.QUEUE_URL!, WaitTimeSeconds: 20, MaxNumberOfMessages: 10,
  }));
  for (const m of Messages) {
    await processOrder(JSON.parse(m.Body!));
    await sqs.send(new DeleteMessageCommand({ QueueUrl: process.env.QUEUE_URL!, ReceiptHandle: m.ReceiptHandle! }));
  }
}
```
**Verdict: Moderate.** BullMQ + Redis is the canonical TS pattern. `bullmq-sqs` exists but doesn't yet have BullMQ-Redis feature parity (rate limits, repeat jobs, flows). For production SQS workloads, raw `@aws-sdk/client-sqs` long-poll is the proven path.

## EventBridge Scheduler (cloud) vs `node-cron` (local dev)

```ts
// scheduler.ts — local only; don't run in prod (cron-fires-twice in multi-instance deploys)
import cron from "node-cron";
import fetch from "node-fetch";
cron.schedule("0 2 * * *", async () => {
  await fetch("http://app:8080/jobs/nightly", { method: "POST" });
});
```
**Verdict: Moderate.** Handler code is the same; only the trigger differs. caravan generates an EventBridge Scheduler rule from a yaml `triggers:` declaration and skips the local-side scheduler container by default — most dev sessions don't need cron firing.

## X-Ray tracing (cloud) vs Jaeger (local) via OpenTelemetry

```ts
import { NodeSDK } from "@opentelemetry/sdk-node";
import { OTLPTraceExporter } from "@opentelemetry/exporter-trace-otlp-grpc";
import { getNodeAutoInstrumentations } from "@opentelemetry/auto-instrumentations-node";

const sdk = new NodeSDK({
  traceExporter: new OTLPTraceExporter({
    url: process.env.OTEL_EXPORTER_OTLP_ENDPOINT ?? "http://jaeger:4317",
  }),
  instrumentations: [getNodeAutoInstrumentations()],
});
sdk.start();
// AWS:   ADOT collector → X-Ray
// Local: jaeger
```
**Verdict: Moderate.** OpenTelemetry is the abstraction. Code is identical; only the OTLP endpoint and exporter target differ. Strongest observability pattern in TS.

## AppConfig (cloud) vs env vars / file (local)

```ts
import { AppConfigDataClient, StartConfigurationSessionCommand, GetLatestConfigurationCommand } from "@aws-sdk/client-appconfigdata";

async function getFeatureFlags(): Promise<Record<string, unknown>> {
  if (process.env.APPCONFIG_ENDPOINT) {
    const client = new AppConfigDataClient({ endpoint: process.env.APPCONFIG_ENDPOINT });
    // ...start session, get latest, parse
    return {};
  }
  return JSON.parse(process.env.FEATURE_FLAGS ?? "{}");
}
```
**Verdict: Moderate.** AppConfig's value is staged rollouts + validators. Locally, env-driven JSON is fine.

## Bedrock (cloud) vs Ollama (local) — Vercel AI SDK is the abstraction

**Reclassified from Intractable (v1) to Hard / Tier 1 (v3).** v3 §4 names the Vercel AI SDK as the canonical TS community library. One API call, env-driven provider/model selects the backend:

```ts
import { generateText } from "ai";
import { bedrock } from "@ai-sdk/amazon-bedrock";
import { ollama } from "ollama-ai-provider";

const provider = process.env.LLM_BACKEND === "bedrock" ? bedrock : ollama;
const model = provider(process.env.LLM_MODEL ?? "llama3.1");
//   cloud:  bedrock("anthropic.claude-opus-4-7-20260416-v1:0")
//   local:  ollama("llama3.1")

const { text } = await generateText({ model, prompt: "hi" });
```
**Verdict: Moderate plumbing — Hard band → v3 Tier 1.** The AI SDK handles per-provider request/response shaping; user code is unchanged across deployments. **caravan does not ship `LlmClient` / `BedrockLLM` / `OllamaLLM`** — that was the v1 prescription; v3 §4 explicitly defers to the Vercel AI SDK. **Output equivalence is not promised** — Claude Opus 4.7 and Llama 3.1 are different models; local tests are plumbing-level, real Bedrock tests are output-quality.

**Still T2 / cloud-only**: Bedrock **Knowledge Bases**, **Agents**, and **Guardrails**. The AI SDK doesn't bridge these — they are AWS-orchestration services with no OSS equivalent.

---

# Hard — different paradigms, needs a real abstraction

## Cognito (cloud) vs local OIDC issuer — token verification via `jose`

Per v3 §4, this is a Tier 1 pair where a mature community library already provides the abstraction. The TS idiom is `jose` verifying against a JWKS URL both sides — env-driven URL is the entire seam. No `TokenVerifier` interface with two impls; one code path, one library.

```ts
import { createRemoteJWKSet, jwtVerify, type JWTPayload } from "jose";

const jwks = createRemoteJWKSet(new URL(process.env.JWKS_URL!));
// Cloud:  https://cognito-idp.<region>.amazonaws.com/<pool_id>/.well-known/jwks.json
// Local:  http://keycloak:8080/realms/dev/protocol/openid-connect/certs

export async function verifyToken(token: string): Promise<JWTPayload> {
  const { payload } = await jwtVerify(token, jwks, {
    issuer: process.env.JWT_ISSUER,
    audience: process.env.JWT_AUDIENCE,
  });
  return payload;
}
```

**Verdict: Hard band → v3 Tier 1.** Cognito's *token issuance* surface (JWKS-served RS256) is a well-defined standard; `jose` hides the cloud↔local difference behind one JWKS URL env var. Cognito's *user lifecycle* (sign-up confirmation, MFA flows, custom attribute admin, hosted UI) has no portable abstraction and stays cloud-only per v3 §8: don't fake admin paths; either skip in local dev or hit real Cognito via mounted creds. **caravan does not ship `TokenVerifier` / `CognitoVerifier` / `LocalJwtVerifier`** — v3 §4 explicitly defers to `jose`.

## API Gateway WebSocket (cloud) vs `ws` / `socket.io` (local)

```ts
// Cloud (Lambda + APIGW WebSocket)
import { ApiGatewayManagementApiClient, PostToConnectionCommand } from "@aws-sdk/client-apigatewaymanagementapi";

export const onConnect = async (event: any) => {
  const connectionId = event.requestContext.connectionId;
  // store connectionId in DynamoDB
  return { statusCode: 200 };
};

export const onMessage = async (event: any) => {
  const { domainName, stage, connectionId } = event.requestContext;
  const mgmt = new ApiGatewayManagementApiClient({ endpoint: `https://${domainName}/${stage}` });
  await mgmt.send(new PostToConnectionCommand({ ConnectionId: connectionId, Data: "hello" }));
  return { statusCode: 200 };
};

// Local (Hono + ws)
import { WebSocketServer } from "ws";
const wss = new WebSocketServer({ port: 8081 });
wss.on("connection", (ws) => {
  ws.on("message", (data) => ws.send(`echo: ${data}`));
});
```
**Verdict: Hard.** API Gateway WebSocket inverts the connection model: connections are *stored* (in DynamoDB), and you push to them via REST (`PostToConnection`). `ws` / `socket.io` are stateful per-process. There is no shared abstraction; caravan picks one model and documents the trade-off. For real-time apps, ECS Fargate + `ws` is the saner cloud target.

## Step Functions Standard (cloud) vs BullMQ flows / pure async (local)

```ts
// Cloud (ASL JSON, deployed via Terraform):
// {"StartAt":"Validate","States":{
//   "Validate":{"Type":"Task","Resource":"arn:aws:lambda:...:validate","Next":"Charge"},
//   "Charge":{"Type":"Task","Resource":"arn:aws:lambda:...:charge","Next":"Notify"},
//   "Notify":{"Type":"Task","Resource":"arn:aws:lambda:...:notify","End":true}}}

// Local (BullMQ flow — manual)
import { FlowProducer } from "bullmq";
const flow = new FlowProducer({ connection: { url: process.env.REDIS_URL! } });
await flow.add({
  name: "orderFlow",
  queueName: "orders",
  children: [
    { name: "validate", queueName: "orders", data: { order } },
    { name: "charge",   queueName: "orders", data: { order } },
    { name: "notify",   queueName: "orders", data: { order } },
  ],
});
```
**Verdict: Hard.** Step Functions has durable state, retry policy DSL, parallel branches, human approval steps. BullMQ flows exist but persistence/observability are weaker. Either:
- (a) caravan defines workflows in a DSL and emits ASL for cloud / BullMQ for local, **or**
- (b) caravan only supports workflows on cloud and documents "no local equivalent — test against `aws-stepfunctions-local`."

(b) is the recommendation — synthesizing two backends doubles the surface area for limited benefit.

## SQS + Lambda fan-out (cloud) vs in-process tasks (local)

```ts
// Cloud producer
import { SQSClient, SendMessageCommand } from "@aws-sdk/client-sqs";
const sqs = new SQSClient({});
await sqs.send(new SendMessageCommand({
  QueueUrl: process.env.QUEUE_URL!,
  MessageBody: JSON.stringify({ order: 42 }),
}));

// Cloud consumer (Lambda triggered by SQS)
import type { SQSHandler } from "aws-lambda";
export const handler: SQSHandler = async (event) => {
  for (const record of event.Records) {
    await processOrder(JSON.parse(record.body));
  }
};

// Local: setImmediate (lossy — dies with the process)
setImmediate(async () => { await processOrder(order); });
```
**Verdict: Hard if you care about local-vs-cloud durability.** `setImmediate` / `process.nextTick` / floating promises all die with the process. caravan's option is to run a local BullMQ worker + ElasticMQ — keeping the *queue* abstraction honest both sides. That's the recommended pattern.

---

# Intractable — no realistic local equivalent

For these, caravan must mark `cloud_only: true` and refuse to bind locally. Trying to emulate is worse than not — false positives hide bugs.

## SageMaker training / inference

```ts
// Cloud
import { SageMakerRuntimeClient, InvokeEndpointCommand } from "@aws-sdk/client-sagemaker-runtime";
const sm = new SageMakerRuntimeClient({});
const resp = await sm.send(new InvokeEndpointCommand({
  EndpointName: process.env.SAGEMAKER_ENDPOINT!,
  Body: new Uint8Array([/* ... */]),
}));

// Local "equivalent" — run the model in a Node container that mimics SageMaker contract
// (paths under /opt/ml/...). Behavior is approximate. Most teams cross-language to Python.
```
**Verdict: Intractable (T2) for the SageMaker-managed surface.** If your inference is a Node container behind an endpoint, the *model serving* portion is straightforwardly portable; the SageMaker-platform features (auto-scaling endpoint variants, A/B traffic splits, model monitoring) are not. Treat the platform as cloud-only.

## CloudFront / Lambda@Edge / Global Accelerator / CloudFront Functions

```ts
// CloudFront Functions — JS subset; no fs/net/aws-sdk
function handler(event: any) {
  const req = event.request;
  if (req.uri === "/old") req.uri = "/new";
  return req;
}
```
**Verdict: Intractable.** Edge runtime is a JS subset — no Node API, no `@aws-sdk/*`, 1MB code limit, 10MB memory. Test routing logic in isolation as a pure function; trust the CDN to invoke it correctly. No emulation worth maintaining.

## S3 Express One Zone, S3 Vectors, Aurora DSQL, DAX, Neptune Analytics, IAM enforcement

**Verdict: Intractable.** Each has properties (single-AZ ultra-low-latency, ANN-on-S3, multi-region active-active SQL, microsecond DDB cache, in-memory graph analytics, real IAM evaluation) that require AWS to demonstrate. Document; don't fake.

## CloudWatch Synthetics / RUM / Application Signals, IoT Device Defender / Analytics / SiteWise / TwinMaker / FleetWise

**Verdict: Intractable.** Observability or domain-specific products built around AWS-managed data flows. For local dev: skip and use OTel + raw logs.

## Step Functions Distributed Map, SNS Mobile Push, Forecast / Personalize, SageMaker JumpStart / Canvas

**Verdict: Intractable.** Parallel-children behavior / real APNs+FCM dispatch / managed-model marketplace / no-code UI — all AWS-internal.

---

# Per-group difficulty summary

| Group | AWS service | Local pair | Difficulty | v3 tier |
|---|---|---|---|---|
| Compute — Function | Lambda (container-image, `shape: function`) | same container, Hono/Express/Fastify Lambda adapter | Moderate | **T0** (one container, two shapes — v3 §3) |
| Compute — Container | ECS/Fargate/App Runner | docker-compose | Trivial | **T0** |
| Compute — VM | EC2 | docker container | N/A (don't abstract) | n/a |
| Storage — Object | S3 | minio | **Trivial** | **T0** |
| Storage — Object | S3 Express One Zone | (none) | Intractable | **T2** (engine-swap → MinIO) |
| Storage — Object | S3 Vectors | (none) | Intractable | **T2** (engine-swap → hnswlib / pgvector) |
| Storage — File | EFS | docker volume | Moderate (no perf parity) | T0 |
| Storage — Block | EBS | docker volume | N/A | n/a |
| DB — RDBMS | RDS/Aurora Postgres | postgres | **Trivial** | **T0** |
| DB — RDBMS | RDS/Aurora MySQL | mysql | **Trivial** | **T0** |
| DB — RDBMS | Aurora DSQL | (none) | Intractable | **T2** (engine-swap → Postgres) |
| DB — KV | DynamoDB | dynamodb-local | **Trivial** | **T0** |
| DB — KV | DAX | (none) | Intractable | **T2** (engine-swap → DDB-local; accept no cache) |
| DB — Document | DocumentDB | mongo | Trivial happy-path; partial overall | T0 happy-path |
| DB — Cache | ElastiCache Redis | redis | **Trivial** | **T0** |
| DB — Cache | ElastiCache Memcached | memcached | Trivial | T0 |
| DB — Cache | MemoryDB | redis (no durability) | Moderate | T0 |
| DB — Search | OpenSearch | opensearch | **Trivial** | **T0** |
| DB — Vector | Aurora pgvector | pgvector | **Trivial** | **T0** |
| DB — Vector | OpenSearch k-NN | opensearch w/ knn | **Trivial** | **T0** |
| DB — Time-series | Timestream LiveAnalytics | (none — use Influx) | Intractable wire | T2 |
| DB — Time-series | Timestream for InfluxDB | influxdb | **Trivial** | **T0** |
| DB — Graph | Neptune | tinkerpop/neo4j | Partial | T1 if you abstract |
| Messaging — Queue | SQS | ElasticMQ | **Trivial** | **T0** |
| Messaging — Queue | Amazon MQ RabbitMQ | rabbitmq (`amqplib`) | **Trivial** | **T0** |
| Messaging — PubSub | SNS | localstack | **Trivial** | **T0** |
| Messaging — Event Bus | EventBridge default | localstack (partial) | Moderate | T0 with caveats |
| Messaging — Stream | Kinesis | localstack | **Trivial** producer; Moderate consumer (no native TS KCL) | T0 / T1 |
| Messaging — Stream | MSK | kafka (`kafkajs`/`confluent-kafka-javascript`) | **Trivial** for plaintext/SASL_SSL; Moderate for IAM | T0 / Moderate IAM |
| API edge | API Gateway HTTP | Hono/Express/Fastify via Lambda adapter | Moderate | T0 (deferred to v1.3 per v3 §10) |
| API edge | API Gateway WebSocket | `ws` / `socket.io` | **Hard** | **T2** in v3 (`cloud_only`) |
| API edge | AppSync | apollo-server / graphql-yoga | Hard / Intractable | T2 |
| API edge | ALB | run container behind nginx | N/A (don't abstract) | n/a (auto-derived from `service expose:`) |
| CDN | CloudFront | (none) | Intractable | **T2** (skip pattern in local; `static_site` primitive in v1.2) |
| CDN | Lambda@Edge / CloudFront Functions | (none — JS subset) | Intractable | **T2** |
| DNS | Route 53 | /etc/hosts / coredns | Partial | T1 |
| Auth | Cognito (token verify) | local OIDC issuer | **Hard** in band; **T1 via `jose`** | **T1** |
| Auth | Cognito (user lifecycle) | (none) | Intractable | **T2** in v3 |
| Auth | IAM | (none; LocalStack stubs) | Intractable enforcement | **T2** |
| Auth | Verified Permissions (Cedar) | `@cedar-policy/cedar-wasm` | Trivial | **T0** |
| Secrets | Secrets Manager | localstack | **Trivial** | **T0** |
| Secrets | SSM Parameter Store | localstack | **Trivial** | **T0** |
| Secrets | KMS | localstack | Moderate (software keys only) | T0 |
| Workflow | Step Functions Standard (single-service) | aws-stepfunctions-local | **Trivial** within ASL | T0 |
| Workflow | Step Functions Standard (multi-service workflows) | (partial) | Hard | **T2** in v3 |
| Workflow | Step Functions Express | (partial local) | Hard | T2 |
| Workflow | EventBridge Scheduler | node-cron / node-schedule | Moderate | T0 (in v3 `cron` is a trigger attribute, not a primitive) |
| Workflow | MWAA | apache/airflow (Python-shaped) | Cross-language | T0 once orchestrated |
| Email | SES | mailhog (SMTP via **`nodemailer`**) | **Trivial** | **T1 — `nodemailer`** is the abstraction |
| Email | SNS SMS | (none — inspect-only) | Intractable | T2 |
| Email | SNS Mobile Push | (none) | Intractable | **T2** |
| Observability | CloudWatch Logs | stdout / docker logs (`pino`) | **Trivial** | **T0** |
| Observability | CloudWatch Metrics | EMF via `@aws-lambda-powertools/metrics` | Moderate | T0 |
| Observability | X-Ray | jaeger via OTel | Moderate | T0 (OTel is the abstraction) |
| Observability | RUM / Synthetics / AppSignals | (none) | Intractable | T2 |
| Analytics — Warehouse | Redshift | clickhouse / postgres | Partial | T1 |
| Analytics — Query | Athena | trino | Partial | T1 |
| Analytics — ETL | Glue | spark container (Python-shaped) | Cross-language | T1 |
| Analytics — Big-data | EMR | spark container | Cross-language | T1 |
| ML — Training | SageMaker training | Python script | Cross-language | T1 |
| ML — Inference (model-as-container) | SageMaker endpoint | TS + `onnxruntime-node` | Moderate | T0 once containerized |
| ML — LLM | Bedrock | ollama via **Vercel AI SDK** | Hard band; **T1 via Vercel AI SDK** | **T1** |
| ML — LLM orchestration | Bedrock KB / Agents / Guardrails | (none) | Intractable | **T2** |
| ML — Vision | Rekognition / Textract | `@xenova/transformers` / `tesseract.js` | Partial — outputs differ | **T1** |
| ML — Speech STT | Transcribe | **`@xenova/transformers`** (Whisper.js) | Partial — outputs differ | **T1** |
| ML — Speech TTS | Polly | (no first-class TS TTS) | Cross-language | T1 |
| IoT — Gateway | IoT Core MQTT | mosquitto (`mqtt` npm) | **Trivial** wire; Moderate auth | T0 wire |
| IoT — Edge | Greengrass | greengrass-runtime (JS components since 2.0) | Trivial | T0 |
| IoT — Analytics | IoT Analytics / SiteWise / etc | (none) | Intractable | **T2** |

**Headcount (per v3's tier semantics)**:
- **T0**: ~22 pairs — env-var swap is enough; no abstraction library required. caravan's bread and butter.
- **T1**: ~5 pairs — community libraries cover them (**Vercel AI SDK**, **`jose`**, **`nodemailer`**, **`@xenova/transformers`**, optionally an interface around vision SDKs). caravan **does not ship** a runtime adapter library; v3 §4 documents which library per pair.
- **T2**: ~15 pairs — `cloud_only:` in the IR. User picks one of v3 §4's four patterns: skip / hit-real / engine-swap / stub.

The remaining ~12 entries are Moderate-band T0s where a small adapter (framework Lambda wrapper, OTel exporter env var, BullMQ backend swap) closes the gap without needing a community library.

**vs Python (~22 T0 / ~5 T1 / ~15 T2) and Rust (~18 T0 / ~3 T1 / ~18 T2)**: TypeScript headcount sits closest to Python because the `@aws-sdk/client-*` package family mirrors boto3's coverage breadth — every AWS service has a dedicated TS client. A few cells differ at the margins: `kafkajs` is less mature for MSK-IAM than Python's signer; Vercel AI SDK has the richest provider router across the three languages (more providers than `rig` or `litellm`); `@xenova/transformers` is the most ergonomic Whisper-shaped option of the three (no Python dependency required). Net result: TS is a first-class target for the *containers-first* abstraction shape, with the most mature Tier 1 community library landscape of the three languages.

See `caravan_abstraction_v3.md` for how these tiers translate into v1 PoC scope, IR primitives, and the yaml switch shape. Conceptual home: `thesis.md`.
