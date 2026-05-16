# Python API Diffs: AWS ↔ Local Container

> **Snapshot date: 2026-05-16.** References `aws_service_groups.md`, `mapping_python_to_aws.md`, `mapping_aws_to_python.md`.
> **Framing**: Python ecosystem evidence feeding into `thesis.md` (conceptual home) and `supeux_abstraction_v2.md` (long-form derivation). The difficulty bands below map onto v2's T0/T1/T2 service tiers — see the row at the bottom of the bands table.

For each AWS↔local pair surfaced in the mapping files, this file shows the exact Python code change required to switch between them and assigns a **difficulty band**:

| Band | Meaning | What supeux does | v2 tier |
|---|---|---|---|
| **Trivial** | One env var (endpoint URL or DSN) controls the switch. Same imports, same calls. | Sets env vars at deploy. Done. | **T0** |
| **Moderate** | Same library, but a few config keys or call shapes differ; or a small adapter (`Mangum`, env-driven branches) closes the gap. | Documents the adapter shape. Usually no library needed. | **T0** (mostly); occasionally T1 |
| **Hard** | Different wire APIs cloud vs local; a structural abstraction is required. | **Uses the recommended Python community library** — `litellm` for LLMs, `authlib`/`python-jose` for token verification, `smtplib` for email, `openai-whisper` for STT, `opencv-python`+`ultralytics` for vision. supeux **does not ship** a runtime adapter library; see v2 §4. | **T1** |
| **Intractable** | No realistic local equivalent. Don't try to emulate — false positives hide bugs. | Marks `cloud_only:` in the yaml IR (v2 §6, §8). User picks one of v2's four patterns per service: **skip** (feature-flag off locally), **hit-real** (mounted creds; pay real $$), **engine-swap** (DAX→DDB-local, S3 Vectors→FAISS, etc.), or **stub**. | **T2** |

Snippets are ≤15 lines each and assume `os.environ` is populated by the supeux runtime / docker-compose / GHA matrix.

---

# Trivial — env-driven `endpoint_url` or DSN swap

These are the wins. A single env var flips cloud↔local with no code change.

## S3 ↔ MinIO

```python
import boto3, os
s3 = boto3.client(
    "s3",
    endpoint_url=os.environ.get("S3_ENDPOINT_URL"),  # None → real S3; http://minio:9000 → local
)
s3.put_object(Bucket="my-bucket", Key="hello.txt", Body=b"hi")
```
**Verdict: Trivial.** Caveats: minio doesn't do storage-class tiering or strong-read-after-write under partial failures. For 95% of code, same.

## DynamoDB ↔ dynamodb-local

```python
import boto3, os
ddb = boto3.resource(
    "dynamodb",
    endpoint_url=os.environ.get("DYNAMODB_ENDPOINT_URL"),  # http://dynamodb:8000 locally
    region_name=os.environ.get("AWS_DEFAULT_REGION", "us-east-1"),
)
table = ddb.Table("items")
table.put_item(Item={"pk": "u#1", "sk": "profile", "name": "Alice"})
```
**Verdict: Trivial.** Streams + TTL deletes are partial in dynamodb-local; transactions, conditional writes are solid.

## SQS ↔ ElasticMQ / localstack

```python
import boto3, os
sqs = boto3.client("sqs", endpoint_url=os.environ.get("SQS_ENDPOINT_URL"))
q_url = os.environ["QUEUE_URL"]  # https://sqs... in AWS; http://elasticmq:9324/000000000000/queue locally
sqs.send_message(QueueUrl=q_url, MessageBody="job#42")
resp = sqs.receive_message(QueueUrl=q_url, WaitTimeSeconds=20)
```
**Verdict: Trivial.** ElasticMQ implements long-poll, DLQ wiring, FIFO dedup. The queue URL itself differs — make it an env var, not a constructed string.

## SNS ↔ localstack

```python
import boto3, os
sns = boto3.client("sns", endpoint_url=os.environ.get("SNS_ENDPOINT_URL"))
sns.publish(TopicArn=os.environ["TOPIC_ARN"], Message="event")
```
**Verdict: Trivial.** ARN format differs locally (`arn:aws:sns:us-east-1:000000000000:topic-name`) — pass it as env var.

## Kinesis Data Streams ↔ localstack

```python
import boto3, os, json
k = boto3.client("kinesis", endpoint_url=os.environ.get("KINESIS_ENDPOINT_URL"))
k.put_record(
    StreamName="events", PartitionKey="user-1",
    Data=json.dumps({"type": "click"}).encode(),
)
```
**Verdict: Trivial.** KCL consumer (`amazon-kinesis-client`) needs separate shard-coordination back-end (DynamoDB) and is fussier locally — for consumer code, prefer raw `get_records` in tests.

## Secrets Manager / SSM Parameter Store ↔ localstack

```python
import boto3, os
ssm = boto3.client("ssm", endpoint_url=os.environ.get("SSM_ENDPOINT_URL"))
val = ssm.get_parameter(Name="/app/db/password", WithDecryption=True)["Parameter"]["Value"]
```
**Verdict: Trivial.** Same for `boto3.client("secretsmanager")`. KMS-decryption is software-only locally; if your secrets aren't encrypted in the local environment, that's fine for dev.

## CloudWatch Logs ↔ localstack (or just stdout)

```python
import logging, sys
logging.basicConfig(level=logging.INFO, stream=sys.stdout)
log = logging.getLogger(__name__)
log.info("processed order %s", order_id)
```
**Verdict: Trivial.** Best practice: write logs to stdout, let the runtime capture them (Lambda → CloudWatch automatic; Fargate → awslogs driver; locally → docker logs). Almost no app code should call `boto3.client("logs").put_log_events` directly.

## Step Functions ↔ aws-stepfunctions-local

```python
import boto3, os
sf = boto3.client("stepfunctions", endpoint_url=os.environ.get("STEPFUNCTIONS_ENDPOINT_URL"))
sf.start_execution(
    stateMachineArn=os.environ["STATE_MACHINE_ARN"],
    input='{"orderId": "o-123"}',
)
```
**Verdict: Trivial.** ASL state machine definitions deploy identically. AWS provides the official local container (`amazon/aws-stepfunctions-local`). The catch: tasks that target real AWS services (Lambda, DynamoDB) need their own endpoint overrides, which the local container supports via env vars (`AWS_STEPFUNCTIONS_LAMBDA_ENDPOINT`, etc.).

## RDS / Aurora Postgres ↔ postgres container

```python
import os
from sqlalchemy import create_engine
engine = create_engine(os.environ["DATABASE_URL"])
# AWS:   postgresql+psycopg://app:****@aurora-cluster.cluster-xyz.us-east-1.rds.amazonaws.com:5432/app
# Local: postgresql+psycopg://app:dev@postgres:5432/app
```
**Verdict: Trivial.** Same `psycopg`, same SQL. Aurora-specific features (read replicas, Optimized Reads) are runtime — not visible to your code.

## pgvector (Aurora) ↔ pgvector container

```python
from pgvector.psycopg import register_vector
import psycopg, os
conn = psycopg.connect(os.environ["DATABASE_URL"])
register_vector(conn)
conn.execute("CREATE EXTENSION IF NOT EXISTS vector;")
conn.execute("INSERT INTO docs (id, embedding) VALUES (%s, %s)", (1, [0.1] * 1536))
```
**Verdict: Trivial.** Same extension, same syntax. The cleanest cloud port in the vector category.

## RDS / Aurora MySQL ↔ mysql container

```python
import os
from sqlalchemy import create_engine
engine = create_engine(os.environ["DATABASE_URL"])
# AWS:   mysql+pymysql://app:****@aurora-mysql.cluster-xyz.us-east-1.rds.amazonaws.com:3306/app
# Local: mysql+pymysql://app:dev@mysql:3306/app
```
**Verdict: Trivial.**

## ElastiCache Redis ↔ redis container

```python
import os, redis
r = redis.Redis.from_url(os.environ["REDIS_URL"])
# AWS:   redis://master.cache-cluster.xyz.cache.amazonaws.com:6379/0  (or rediss:// for TLS)
# Local: redis://redis:6379/0
r.set("session:abc", "user-123", ex=3600)
```
**Verdict: Trivial.** Cluster-mode-enabled ElastiCache requires `redis.cluster.RedisCluster` — different client. If you depend on cluster mode, run `bitnami/redis-cluster` locally.

## DocumentDB ↔ mongo container

```python
import os
from pymongo import MongoClient
client = MongoClient(os.environ["MONGO_URL"])
db = client["app"]
db.users.insert_one({"name": "Alice"})
```
**Verdict: Trivial for happy path, partial in general.** DocumentDB is wire-compatible with Mongo but lacks ~30% of aggregation operators (esp. `$lookup` semantics, change-stream resumability quirks). If your code uses modern aggregations, run real Mongo locally and you'll get *false positives* (works local, fails AWS). Test critical paths against DocumentDB in CI.

## OpenSearch Service ↔ opensearch container

```python
import os
from opensearchpy import OpenSearch
os_client = OpenSearch(
    hosts=[os.environ["OPENSEARCH_URL"]],  # https://...es.amazonaws.com:443 vs http://opensearch:9200
    http_auth=(os.environ.get("OS_USER"), os.environ.get("OS_PASS")),
    use_ssl=os.environ.get("OS_USE_SSL", "true").lower() == "true",
    verify_certs=True,
)
os_client.index(index="docs", body={"title": "hello"})
```
**Verdict: Trivial.** Same `opensearch-py` library. Use the OpenSearch image (not Elasticsearch) — the post-fork divergence on `elasticsearch-py` ≥8 is hostile.

## MSK ↔ kafka container

```python
import os
from confluent_kafka import Producer
p = Producer({"bootstrap.servers": os.environ["KAFKA_BOOTSTRAP"]})
# AWS:   b-1.msk-cluster...kafka.us-east-1.amazonaws.com:9094 (TLS) or :9098 (IAM)
# Local: kafka:9092
p.produce("topic", value=b"hello")
p.flush()
```
**Verdict: Trivial for SASL_SSL or plaintext modes; moderate for IAM auth.** If you use MSK with IAM SASL, you need `aws-msk-iam-sasl-signer-python` and an additional `sasl.mechanisms=AWS_MSK_IAM` config — your local kafka container won't use this. Make the auth config env-driven.

## Amazon MQ RabbitMQ ↔ rabbitmq container

```python
import os, pika
conn = pika.BlockingConnection(pika.URLParameters(os.environ["RABBITMQ_URL"]))
# AWS:   amqps://user:****@b-xyz.mq.us-east-1.amazonaws.com:5671
# Local: amqp://guest:guest@rabbitmq:5672/
ch = conn.channel()
ch.queue_declare(queue="jobs")
ch.basic_publish(exchange="", routing_key="jobs", body=b"job-1")
```
**Verdict: Trivial.** Real RabbitMQ both sides; only TLS differs.

## IoT Core MQTT ↔ mosquitto container

```python
import os, paho.mqtt.client as mqtt
c = mqtt.Client()
if os.environ.get("MQTT_TLS") == "true":
    c.tls_set()  # AWS IoT Core requires mTLS with device certs
c.connect(os.environ["MQTT_HOST"], int(os.environ.get("MQTT_PORT", 1883)))
c.publish("telemetry/sensor1", b'{"temp": 22.5}')
```
**Verdict: Trivial wire; moderate auth.** mosquitto can be configured with or without TLS; IoT Core mandates mTLS with X.509. Real cert provisioning is the auth-shaped seam. For dev, run mosquitto without TLS and gate the `tls_set` call on env.

## SES ↔ mailhog (via SMTP)

```python
import os, smtplib
from email.message import EmailMessage
msg = EmailMessage()
msg["Subject"], msg["From"], msg["To"] = "Hi", "noreply@app.com", "user@example.com"
msg.set_content("hello")
with smtplib.SMTP(os.environ["SMTP_HOST"], int(os.environ["SMTP_PORT"])) as s:
    if os.environ.get("SMTP_USER"): s.starttls(); s.login(os.environ["SMTP_USER"], os.environ["SMTP_PASS"])
    s.send_message(msg)
```
**Verdict: Trivial.** AWS SES has SMTP endpoint credentials. Locally, mailhog accepts on `mailhog:1025` no-auth. Make TLS + auth env-driven.

## Timestream for InfluxDB ↔ influxdb container

```python
import os
from influxdb_client import InfluxDBClient, Point
client = InfluxDBClient(url=os.environ["INFLUX_URL"], token=os.environ["INFLUX_TOKEN"], org=os.environ["INFLUX_ORG"])
client.write_api().write(bucket="metrics", record=Point("cpu").field("usage", 42.0))
```
**Verdict: Trivial.** Same `influxdb-client`; URL + token swap.

---

# Moderate — same library, configuration / behavior differs

## FastAPI app: one container, two `shape:` values (Lambda or Fargate)

Per v2 §3 / §9, Lambda is one `shape:` of the `service` primitive — not a separate primitive. The user writes a FastAPI app once and uses `Mangum` to bridge the handler ABI. supeux generates `aws_lambda_function` Terraform around the same container image when `shape: function`, or `aws_ecs_service` Terraform when `shape: long-running`. No supeux SDK, no decorator — the user's container handles the ABI in their own idiom.

```python
from fastapi import FastAPI
from mangum import Mangum
import os

app = FastAPI()

@app.get("/hi")
async def hi(name: str): return {"message": f"hi {name}"}

# Branch on the Lambda-runtime env var (set only inside Lambda).
if os.environ.get("AWS_LAMBDA_RUNTIME_API"):
    handler = Mangum(app)         # → Lambda container-image
else:
    import uvicorn
    uvicorn.run(app, host="0.0.0.0", port=8080)  # → Fargate / App Runner / local docker-compose
```

**Verdict: Moderate (T0 in v2's tier system).** The seam is one `if` statement; the same container image deploys both ways. supeux's only job is to inject env vars the same way it does for any other service. v2 §9's "Why Lambda fits v1 (it nearly didn't)" walks through this: removing the decorator SDK is what made Lambda inclusion cheap — supeux emits `aws_lambda_function` + `aws_lambda_function_url` HCL, the user's Python container handles the rest.

**Constraints inherited from Lambda regardless of supeux**: websockets need API Gateway WebSocket (separate primitive, deferred to v1.1+; see v2 §8 / §11); streaming responses need Lambda Function URLs with response streaming on; lifespan startup runs per cold-start. None of these are supeux concerns — they're Lambda properties.

## Celery worker — SQS broker (cloud) vs Redis broker (local)

```python
# tasks.py — single code path, switched via env
from celery import Celery
import os

broker_url = os.environ["CELERY_BROKER_URL"]
# AWS:   sqs://aws_access_key:aws_secret@   (uses default region from env)
# Local: redis://redis:6379/0

celery = Celery("app", broker=broker_url, broker_transport_options={"region": "us-east-1"})

@celery.task
def process_order(order_id: int):
    ...
```
**Verdict: Moderate.** Celery handles broker abstraction at config-time. But SQS-as-broker drops features: no priority, no fanout, polling-only consume, visibility timeout != ack timing. If your tasks rely on broker fanout (`task_routes` to queues with bindings), the Redis-broker dev environment will quietly do something Redis-specific that SQS doesn't replicate.

## EventBridge Scheduler (cloud) vs in-process scheduler (local dev)

```python
# Cloud: defined at deploy time via IaC; fires SQS msg / Lambda invocation
# Local: simulate via separate scheduler container

# scheduler.py (local only — don't run in prod)
import schedule, time, requests
schedule.every().day.at("02:00").do(lambda: requests.post("http://app:8000/jobs/nightly"))
while True:
    schedule.run_pending(); time.sleep(30)
```
**Verdict: Moderate.** The handler code is the same (it receives an event); only the trigger differs. supeux should generate the EventBridge Scheduler rule from a `@supeux.cron("0 2 * * *")` decorator and skip generating the local-side scheduler container by default — most dev sessions don't need cron firing.

## X-Ray tracing (cloud) vs Jaeger (local) via OpenTelemetry

```python
from opentelemetry import trace
from opentelemetry.sdk.trace import TracerProvider
from opentelemetry.sdk.trace.export import BatchSpanProcessor
from opentelemetry.exporter.otlp.proto.grpc.trace_exporter import OTLPSpanExporter
import os

trace.set_tracer_provider(TracerProvider())
trace.get_tracer_provider().add_span_processor(
    BatchSpanProcessor(OTLPSpanExporter(endpoint=os.environ["OTEL_EXPORTER_OTLP_ENDPOINT"]))
)
# AWS:   ADOT collector → X-Ray
# Local: localhost:4317 → jaeger
```
**Verdict: Moderate.** OpenTelemetry is the abstraction. Code is identical; only the OTLP endpoint and the exporter target differ. This is the strongest pattern in observability — use it.

## AppConfig (cloud) vs env vars / file (local)

```python
import os, json
def get_feature_flags():
    if endpoint := os.environ.get("APPCONFIG_ENDPOINT"):
        import boto3
        client = boto3.client("appconfigdata", endpoint_url=endpoint)
        # boilerplate: start_configuration_session, get_latest_configuration, parse
        ...
    return json.loads(os.environ.get("FEATURE_FLAGS", "{}"))
```
**Verdict: Moderate.** AppConfig's value is staged rollouts + validators. Locally, env-driven JSON is fine — supeux just generates a `feature_flags.py` adapter that picks based on env.

---

# Hard — different paradigms, needs a real abstraction

## Cognito (cloud) vs local OIDC issuer — token verification via `authlib`

Per v2 §4, this is a Tier 1 pair where a mature community library already provides the abstraction. The Python idiom is `authlib` (or `python-jose`) verifying against a JWKS URL both sides — env-driven URL is the entire seam. No `AuthService` Protocol with two impls; one code path, one library.

```python
from authlib.jose import jwt, JsonWebKey
import httpx, os

# JWKS_URL points at Cognito or local issuer
# Cloud:  https://cognito-idp.<region>.amazonaws.com/<pool_id>/.well-known/jwks.json
# Local:  http://keycloak:8080/realms/dev/protocol/openid-connect/certs
_jwks = JsonWebKey.import_key_set(httpx.get(os.environ["JWKS_URL"]).json())

def verify_token(token: str) -> dict:
    claims = jwt.decode(token, _jwks)
    claims.validate()                       # exp, iat, iss, aud
    return dict(claims)                     # sub, email, custom: attrs, ...
```

**Verdict: Hard band → v2 Tier 1.** Cognito's *token issuance* surface (JWKS-served RS256) is a well-defined standard; `authlib`/`python-jose` hide the cloud↔local difference behind one JWKS URL env var. Cognito's *user lifecycle* (sign-up confirmation, MFA flows, custom attribute admin, hosted UI) has no portable abstraction and stays cloud-only — that's the right answer per v2 §8: don't try to fake admin paths; either skip in local dev or hit real Cognito via mounted creds. **supeux does not ship `AuthService` / `CognitoAuth` / `LocalJWTAuth`** — that was the v1 prescription; v2 §4 explicitly defers to authlib.

## API Gateway WebSocket (cloud) vs FastAPI websockets (local)

```python
# Cloud (Lambda + APIGW WebSocket)
def on_connect(event, context):
    connection_id = event["requestContext"]["connectionId"]
    # store in DynamoDB
    return {"statusCode": 200}

def on_message(event, context):
    apigw = boto3.client("apigatewaymanagementapi", endpoint_url=f"https://{event['requestContext']['domainName']}/{event['requestContext']['stage']}")
    apigw.post_to_connection(ConnectionId=event["requestContext"]["connectionId"], Data=b"hello")
    return {"statusCode": 200}

# Local (FastAPI)
from fastapi import FastAPI, WebSocket
app = FastAPI()
@app.websocket("/ws")
async def ws_endpoint(ws: WebSocket):
    await ws.accept()
    while True:
        msg = await ws.receive_text()
        await ws.send_text(f"echo: {msg}")
```
**Verdict: Hard.** API Gateway WebSocket inverts the connection model: connections are *stored* (in DynamoDB), and you push to them via REST (`post_to_connection`). FastAPI websockets are stateful per-process. There is no shared abstraction; supeux must pick one model and document the trade-off. For real-time apps, ECS Fargate + FastAPI websockets is the saner cloud target.

## Step Functions Standard (cloud) vs Celery chain / Prefect (local)

```python
# Cloud (ASL JSON)
{
  "StartAt": "Validate",
  "States": {
    "Validate": {"Type": "Task", "Resource": "arn:aws:lambda:...:validate", "Next": "Charge"},
    "Charge":   {"Type": "Task", "Resource": "arn:aws:lambda:...:charge",   "Next": "Notify"},
    "Notify":   {"Type": "Task", "Resource": "arn:aws:lambda:...:notify",   "End": true}
  }
}

# Local (Celery chain)
from celery import chain
chain(validate.s(order), charge.s(), notify.s()).apply_async()
```
**Verdict: Hard.** Step Functions has durable state, retry policy DSL, parallel branches, human approval steps. Celery has chains/groups but persistence and observability are weaker. Either:
- (a) supeux defines workflows in a DSL and emits ASL for cloud / Celery code for local, **or**
- (b) supeux only supports workflows on cloud and documents "no local equivalent — test against AWS."

(b) is what I'd recommend — synthesizing two backends doubles the surface area for limited benefit.

## SQS + Lambda fan-out (cloud) vs FastAPI background tasks (local)

```python
# Cloud: producer puts on SQS, Lambda consumer fires per message
sqs.send_message(QueueUrl=..., MessageBody=json.dumps({"order": 42}))

def lambda_consumer(event, context):
    for record in event["Records"]:
        process(json.loads(record["body"]))

# Local: FastAPI BackgroundTasks (in-process, lossy)
from fastapi import BackgroundTasks
@app.post("/orders")
async def create_order(order: Order, bg: BackgroundTasks):
    bg.add_task(process, order)
```
**Verdict: Hard if you care about local-vs-cloud durability.** BackgroundTasks die with the process. supeux's option is to run a local Celery/RQ worker + SQS-emulator (ElasticMQ), keeping the *queue* abstraction honest both sides. That's the recommended pattern.

---

# Intractable — no realistic local equivalent

For these, supeux must mark `cloud_only: true` and refuse to bind locally. Trying to emulate is worse than not — false positives hide bugs.

## Bedrock LLM (cloud) vs Ollama (local) — `litellm` is the abstraction

**Reclassified from Intractable (v1) to Hard / Tier 1 (v2).** v2 §4 names `litellm` as the canonical Python community library for the LLM Tier 1 pair. One API call, env-driven model string selects Bedrock or Ollama (or OpenAI, Anthropic-direct, Cohere, Vertex, …):

```python
import litellm, os

reply = litellm.completion(
    model=os.environ.get("LLM_MODEL", "ollama/llama3.1"),
    # cloud:  bedrock/anthropic.claude-opus-4-7-20260416-v1:0
    # cloud:  bedrock/anthropic.claude-sonnet-4-6-...
    # local:  ollama/llama3.1
    messages=[{"role": "user", "content": "hi"}],
)
text = reply.choices[0].message.content
```

**Verdict: Hard band → v2 Tier 1.** litellm handles the per-provider request/response shaping; user code is unchanged across deployments. **supeux does not ship `LLMClient` / `BedrockLLM` / `OllamaLLM`** — that was the v1 prescription; v2 §4 explicitly defers to litellm. **Output equivalence is not promised** — Claude Opus 4.7 and Llama 3.1 are different models; local tests are plumbing-level, real Bedrock tests are output-quality.

**Still T2 / cloud-only**: Bedrock **Knowledge Bases**, **Agents**, and **Guardrails**. litellm doesn't bridge these — they are AWS-orchestration services with no OSS equivalent. Per v2 §4, user picks "skip in local" or "hit real AWS via mounted creds" patterns per service.

## SageMaker training / inference

```python
# Cloud
import boto3
sm = boto3.client("sagemaker-runtime")
resp = sm.invoke_endpoint(EndpointName=os.environ["SAGEMAKER_ENDPOINT"], Body=b"...")

# Local "equivalent" — run the model script in a Python container that mimics SageMaker contract
# (paths under /opt/ml/...). SageMaker SDK local-mode does this; behavior is approximate.
```
**Verdict: Intractable (T2) for the SageMaker-managed surface.** If your inference is a Python container behind an endpoint, the *model serving* portion is straightforwardly portable; the SageMaker-platform features (auto-scaling endpoint variants, A/B traffic splits, model monitoring) are not. Treat the platform as cloud-only.

## CloudFront / Lambda@Edge / Global Accelerator

```python
# Cloud (CloudFront Function)
function handler(event) {
  const req = event.request;
  if (req.uri === "/old") req.uri = "/new";
  return req;
}
```
**Verdict: Intractable.** Edge runtime is single-AWS. The cleanest "local" approach is to test the routing logic in isolation as a pure function, and trust the CDN to invoke it correctly. No emulation worth maintaining.

## S3 Express One Zone, S3 Vectors, Aurora DSQL, DAX, Neptune Analytics, IAM enforcement

**Verdict: Intractable.** Each has properties (single-AZ ultra-low-latency, ANN-on-S3, multi-region active-active SQL, microsecond DDB cache, in-memory graph analytics, real IAM evaluation) that require AWS to demonstrate. Document; don't fake.

## CloudWatch Synthetics / RUM / Application Signals, IoT Device Defender / Analytics / SiteWise / TwinMaker / FleetWise

**Verdict: Intractable.** These are observability or domain-specific products built around AWS-managed data flows. The realistic alternative for local dev is to skip them entirely and rely on a different toolchain (OTel for tracing, raw logs for the rest).

---

# Per-group difficulty summary

| Group | AWS service | Local pair | Difficulty | v2 tier |
|---|---|---|---|---|
| Compute — Function | Lambda (container-image, `shape: function`) | same container, run under uvicorn | Moderate (Mangum branch) | **T0** (one container, two shapes — v2 §3) |
| Compute — Container | ECS/Fargate/App Runner | docker-compose | Trivial | **T0** |
| Compute — VM | EC2 | docker container | N/A (don't abstract) | n/a |
| Storage — Object | S3 | minio | **Trivial** | **T0** |
| Storage — Object | S3 Express One Zone | (none) | Intractable | **T2** (engine-swap → MinIO) |
| Storage — Object | S3 Vectors | (none) | Intractable | **T2** (engine-swap → FAISS / pgvector) |
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
| Messaging — Queue | Amazon MQ RabbitMQ | rabbitmq | **Trivial** | **T0** |
| Messaging — PubSub | SNS | localstack | **Trivial** | **T0** |
| Messaging — Event Bus | EventBridge default | localstack (partial) | Moderate | T0 with caveats |
| Messaging — Stream | Kinesis | localstack | **Trivial** producer; Moderate consumer (KCL) | T0 / T1 |
| Messaging — Stream | MSK | kafka container | **Trivial** for plaintext/SASL_SSL; Moderate for IAM auth | T0 / Moderate IAM |
| API edge | API Gateway HTTP | run handler directly via Mangum | Moderate | T0 (deferred to v1.3 per v2 §10) |
| API edge | API Gateway WebSocket | FastAPI websockets | **Hard** | **T2** in v2 (`cloud_only`; per v2 §8 newly added) |
| API edge | AppSync | (none worth) | Hard / Intractable | T2 |
| API edge | ALB | run container behind nginx | N/A (don't abstract) | n/a (auto-derived from `service expose:`) |
| CDN | CloudFront | (none) | Intractable | **T2** (skip pattern in local; `static_site` primitive in v1.2) |
| CDN | Lambda@Edge / CloudFront Functions | (none) | Intractable | **T2** |
| DNS | Route 53 | /etc/hosts / coredns | Partial | T1 |
| Auth | Cognito (token verify) | local OIDC issuer | **Hard** in band; **T1 via `authlib`** | **T1** |
| Auth | Cognito (user lifecycle: signup, MFA, hosted UI) | (none) | Intractable | **T2** in v2 (newly explicit; see v2 §8) |
| Auth | IAM | (none; LocalStack stubs) | Intractable enforcement | **T2** |
| Auth | Verified Permissions (Cedar) | cedar OSS | Trivial | **T0** |
| Secrets | Secrets Manager | localstack | **Trivial** | **T0** |
| Secrets | SSM Parameter Store | localstack | **Trivial** | **T0** |
| Secrets | KMS | localstack | Moderate (software keys only) | T0 |
| Workflow | Step Functions Standard (single-service) | aws-stepfunctions-local | **Trivial** within ASL | T0 |
| Workflow | Step Functions Standard (multi-service workflows) | (partial) | Hard | **T2** in v2 (newly explicit; see v2 §8) |
| Workflow | Step Functions Express | (partial local) | Hard | T2 |
| Workflow | EventBridge Scheduler | apscheduler / cron container | Moderate | T0 (in v2 `cron` is a trigger attribute, not a primitive) |
| Workflow | MWAA | apache/airflow | Trivial-ish | T0 |
| Email | SES | mailhog (SMTP via `smtplib`) | **Trivial** | **T1 — `smtplib`** is the abstraction |
| Email | SNS SMS | (none — inspect-only) | Intractable | T2 |
| Email | SNS Mobile Push | (none) | Intractable | **T2** |
| Observability | CloudWatch Logs | stdout / docker logs | **Trivial** | **T0** |
| Observability | CloudWatch Metrics | StatsD / prometheus / EMF | Moderate | T0 |
| Observability | X-Ray | jaeger via OTel | Moderate | T0 (OTel is the abstraction) |
| Observability | RUM / Synthetics / AppSignals | (none) | Intractable | T2 |
| Analytics — Warehouse | Redshift | clickhouse / postgres | Partial | T1 |
| Analytics — Query | Athena | trino | Partial | T1 |
| Analytics — ETL | Glue | spark container (Python-shaped) | Partial | T1 |
| Analytics — Big-data | EMR | spark container | Partial | T1 |
| ML — Training | SageMaker training | python script | Moderate (SDK local-mode) | T1 |
| ML — Inference (model-as-container) | SageMaker endpoint | FastAPI + model | Moderate | T0 once containerized |
| ML — LLM | Bedrock | ollama via **`litellm`** | Hard band; **T1 via litellm** | **T1** |
| ML — LLM orchestration | Bedrock KB / Agents / Guardrails | (none) | Intractable | **T2** |
| ML — Vision/Speech/NLP | Rekognition/Polly/Transcribe/Comprehend | OSS models (`openai-whisper`, `opencv-python`+`ultralytics`, `tesseract`) | Partial — outputs differ | **T1** (named libraries per v2 §4) |
| IoT — Gateway | IoT Core MQTT | mosquitto | **Trivial** wire; Moderate auth | T0 wire |
| IoT — Edge | Greengrass | greengrass-runtime container | Trivial | T0 |
| IoT — Analytics | IoT Analytics / SiteWise / etc | (none) | Intractable | **T2** |

**Headcount (per v2's tier semantics)**:
- **T0**: ~22 pairs — env-var swap is enough; no abstraction library required. supeux's bread and butter.
- **T1**: ~5 pairs — community libraries cover them (`litellm`, `authlib`/`python-jose`, `smtplib`, `openai-whisper`, `opencv-python`+`ultralytics`). supeux **does not ship** an SDK; v2 §4 documents which library per pair.
- **T2**: ~15 pairs — `cloud_only:` in the IR. User picks one of v2 §4's four patterns: skip / hit-real / engine-swap / stub.

The remaining ~12 entries are Moderate-band T0s where a small adapter (`Mangum`, OTel exporter env var, Celery's broker config) closes the gap without needing a community library.

See `supeux_abstraction_v2.md` for how these tiers translate into v1 PoC scope, IR primitives, and the yaml switch shape. Conceptual home: `thesis.md`.
