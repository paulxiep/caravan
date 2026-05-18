# Rust API Diffs: AWS ↔ Local Container

> **Snapshot date: 2026-05-16.** References `aws_service_groups.md`, `mapping_rust_to_aws.md`, `mapping_aws_to_rust.md`.

For each AWS↔local pair surfaced in the mapping files, this file shows the exact Rust code change required to switch between them and assigns a **difficulty band**:

| Band | Meaning | What caravan must do |
|---|---|---|
| **Trivial** | One env var (endpoint URL or DSN) controls the switch. Same crate, same calls; type-state builder rebuild is the only verbosity tax vs Python. | Set env vars at deploy. Done. |
| **Moderate** | Same crate, but a few config keys or call shapes differ; or a thin wrapper / adapter hides the difference. | Generate a tiny config / adapter module. |
| **Hard** | Different paradigms (handler vs server, broker mental model). A real trait abstraction is needed. | Define a `Trait`; ship a cloud impl + local impl. |
| **Intractable** | No realistic local equivalent. Don't try. | Mark `cloud_only: true` in the IR; refuse local binding. |

Snippets are ≤15 lines each and assume `std::env` is populated by the caravan runtime / docker-compose / GHA matrix.

---

# Trivial — env-driven `endpoint_url` or DSN swap

These are the wins. A single env var flips cloud↔local with no code change beyond rebuilding the AWS SDK config.

## S3 ↔ MinIO

```rust
use aws_config::BehaviorVersion;
use aws_sdk_s3::{Client, config::Builder};
let base = aws_config::defaults(BehaviorVersion::latest()).load().await;
let mut b = Builder::from(&base);
if let Ok(url) = std::env::var("S3_ENDPOINT_URL") {
    b = b.endpoint_url(url).force_path_style(true);
}
let s3 = Client::from_conf(b.build());
s3.put_object().bucket("my-bucket").key("hello.txt")
    .body(b"hi".to_vec().into()).send().await?;
```
**Verdict: Trivial.** Two specific Rust-side gotchas vs Python: (1) the type-state Builder must be rebuilt (Python's kwarg style is denser), (2) `force_path_style(true)` is required for minio. Set both conditionally on the env var.

## DynamoDB ↔ dynamodb-local

```rust
use aws_config::BehaviorVersion;
use aws_sdk_dynamodb::Client;
let base = aws_config::defaults(BehaviorVersion::latest()).load().await;
let mut b = aws_sdk_dynamodb::config::Builder::from(&base);
if let Ok(url) = std::env::var("DYNAMODB_ENDPOINT_URL") { b = b.endpoint_url(url); }
let ddb = Client::from_conf(b.build());
ddb.put_item().table_name("items")
    .item("pk", "u#1".into()).item("sk", "profile".into())
    .item("name", "Alice".into()).send().await?;
```
**Verdict: Trivial.** Streams + TTL deletes are partial in dynamodb-local; transactions, conditional writes are solid. Pair with `serde_dynamo` if you want struct ↔ item mapping.

## SQS ↔ ElasticMQ / localstack

```rust
use aws_config::BehaviorVersion;
let base = aws_config::defaults(BehaviorVersion::latest()).load().await;
let mut b = aws_sdk_sqs::config::Builder::from(&base);
if let Ok(url) = std::env::var("SQS_ENDPOINT_URL") { b = b.endpoint_url(url); }
let sqs = aws_sdk_sqs::Client::from_conf(b.build());
let q = std::env::var("QUEUE_URL")?;
sqs.send_message().queue_url(&q).message_body("job#42").send().await?;
let resp = sqs.receive_message().queue_url(&q).wait_time_seconds(20).send().await?;
```
**Verdict: Trivial.** Same wire compat as Python. Make the queue URL itself an env var, not a constructed string.

## SNS ↔ localstack

```rust
let base = aws_config::defaults(BehaviorVersion::latest()).load().await;
let mut b = aws_sdk_sns::config::Builder::from(&base);
if let Ok(url) = std::env::var("SNS_ENDPOINT_URL") { b = b.endpoint_url(url); }
let sns = aws_sdk_sns::Client::from_conf(b.build());
sns.publish().topic_arn(std::env::var("TOPIC_ARN")?).message("event").send().await?;
```
**Verdict: Trivial.** ARN format differs locally (`arn:aws:sns:us-east-1:000000000000:topic-name`) — pass it as env var.

## Kinesis Data Streams ↔ localstack

```rust
let base = aws_config::defaults(BehaviorVersion::latest()).load().await;
let mut b = aws_sdk_kinesis::config::Builder::from(&base);
if let Ok(url) = std::env::var("KINESIS_ENDPOINT_URL") { b = b.endpoint_url(url); }
let k = aws_sdk_kinesis::Client::from_conf(b.build());
let body = serde_json::to_vec(&serde_json::json!({"type": "click"}))?;
k.put_record().stream_name("events").partition_key("user-1")
    .data(body.into()).send().await?;
```
**Verdict: Trivial for producer.** No mature Rust port of KCL exists, so high-throughput consumer code is harder. For consumers, prefer raw `get_records` against shards or push to Lambda triggered by Kinesis.

## Secrets Manager / SSM Parameter Store ↔ localstack

```rust
let base = aws_config::defaults(BehaviorVersion::latest()).load().await;
let mut b = aws_sdk_ssm::config::Builder::from(&base);
if let Ok(url) = std::env::var("SSM_ENDPOINT_URL") { b = b.endpoint_url(url); }
let ssm = aws_sdk_ssm::Client::from_conf(b.build());
let val = ssm.get_parameter().name("/app/db/password").with_decryption(true)
    .send().await?.parameter.and_then(|p| p.value).unwrap_or_default();
```
**Verdict: Trivial.** Same for `aws_sdk_secretsmanager`. KMS decryption is software-only locally.

## CloudWatch Logs ↔ stdout

```rust
use tracing_subscriber::{fmt, EnvFilter};
fmt().with_env_filter(EnvFilter::from_default_env())
     .with_target(false).json().init();
tracing::info!(order_id = %order_id, "processed order");
```
**Verdict: Trivial.** Best practice: emit JSON to stdout via `tracing`. Lambda → CloudWatch automatic; Fargate → awslogs driver; locally → `docker logs`. Almost no Rust app should call `aws-sdk-cloudwatchlogs::put_log_events` directly.

## Step Functions ↔ aws-stepfunctions-local

```rust
let base = aws_config::defaults(BehaviorVersion::latest()).load().await;
let mut b = aws_sdk_sfn::config::Builder::from(&base);
if let Ok(url) = std::env::var("STEPFUNCTIONS_ENDPOINT_URL") { b = b.endpoint_url(url); }
let sf = aws_sdk_sfn::Client::from_conf(b.build());
sf.start_execution()
    .state_machine_arn(std::env::var("STATE_MACHINE_ARN")?)
    .input(r#"{"orderId":"o-123"}"#).send().await?;
```
**Verdict: Trivial.** ASL state machine definitions deploy identically. The local container supports endpoint env vars for downstream service calls (`AWS_STEPFUNCTIONS_LAMBDA_ENDPOINT`, etc.).

## RDS / Aurora Postgres ↔ postgres container

```rust
use sqlx::postgres::PgPoolOptions;
let url = std::env::var("DATABASE_URL")?;
// AWS:   postgres://app:****@aurora-cluster.cluster-xyz.us-east-1.rds.amazonaws.com:5432/app
// Local: postgres://app:dev@postgres:5432/app
let pool = PgPoolOptions::new().max_connections(8).connect(&url).await?;
sqlx::query("INSERT INTO users (name) VALUES ($1)").bind("Alice").execute(&pool).await?;
```
**Verdict: Trivial.** Same `sqlx`, same SQL, DSN swap. Aurora-specific features (read replicas, Optimized Reads) are runtime — invisible to code.
**Rust gotcha**: `sqlx::query!` (macro form) requires a live DB during `cargo build`. Either keep the macro and run `docker-compose up -d postgres` before build, or use `sqlx-cli prepare` to cache metadata for offline build.

## pgvector (Aurora) ↔ pgvector container

```rust
use sqlx::postgres::PgPoolOptions;
use pgvector::Vector;
let pool = PgPoolOptions::new().connect(&std::env::var("DATABASE_URL")?).await?;
sqlx::query("CREATE EXTENSION IF NOT EXISTS vector;").execute(&pool).await?;
let v: Vector = vec![0.1_f32; 1536].into();
sqlx::query("INSERT INTO docs (id, embedding) VALUES ($1, $2)")
    .bind(1_i32).bind(v).execute(&pool).await?;
```
**Verdict: Trivial.** Same extension, same syntax. The `pgvector` crate provides the `Vector` type with `sqlx` / `tokio-postgres` / `diesel` integrations.

## RDS / Aurora MySQL ↔ mysql container

```rust
use sqlx::mysql::MySqlPoolOptions;
let url = std::env::var("DATABASE_URL")?;
// AWS:   mysql://app:****@aurora-mysql.cluster-xyz.us-east-1.rds.amazonaws.com:3306/app
// Local: mysql://app:dev@mysql:3306/app
let pool = MySqlPoolOptions::new().max_connections(8).connect(&url).await?;
```
**Verdict: Trivial.**

## ElastiCache Redis ↔ redis container

```rust
use redis::AsyncCommands;
let client = redis::Client::open(std::env::var("REDIS_URL")?)?;
// AWS:   redis://master.cache-cluster.xyz.cache.amazonaws.com:6379/0  (or rediss:// for TLS)
// Local: redis://redis:6379/0
let mut conn = client.get_async_connection().await?;
let _: () = conn.set_ex("session:abc", "user-123", 3600).await?;
```
**Verdict: Trivial.** Cluster-mode-enabled ElastiCache requires `redis::cluster::ClusterClient` — different constructor. If you use cluster mode, run `bitnami/redis-cluster` locally.

## DocumentDB ↔ mongo container

```rust
use mongodb::{Client, bson::doc};
let client = Client::with_uri_str(std::env::var("MONGO_URL")?).await?;
let coll = client.database("app").collection::<mongodb::bson::Document>("users");
coll.insert_one(doc! {"name": "Alice"}, None).await?;
```
**Verdict: Trivial for happy path, partial in general.** DocumentDB lacks ~30% of aggregation operators (`$lookup` semantics, `$facet`, `$bucket`). Real Mongo locally gives false positives. Test critical aggregations against DocumentDB in CI.

## OpenSearch Service ↔ opensearch container

```rust
use opensearch::{OpenSearch, http::{Url, transport::Transport}};
let url = Url::parse(&std::env::var("OPENSEARCH_URL")?)?;
let transport = Transport::single_node(url.as_str())?;
let os = OpenSearch::new(transport);
os.index(opensearch::IndexParts::IndexId("docs", "1"))
    .body(serde_json::json!({"title":"hello"})).send().await?;
```
**Verdict: Trivial.** Same `opensearch` crate. Use the OpenSearch image (not Elasticsearch).

## MSK ↔ kafka container

```rust
use rdkafka::{producer::{FutureProducer, FutureRecord}, ClientConfig};
let producer: FutureProducer = ClientConfig::new()
    .set("bootstrap.servers", &std::env::var("KAFKA_BOOTSTRAP")?)
    .create()?;
// AWS:   b-1.msk-cluster...kafka.us-east-1.amazonaws.com:9094 (TLS) or :9098 (IAM)
// Local: kafka:9092
producer.send(FutureRecord::to("topic").payload(b"hello").key("k"),
              std::time::Duration::from_secs(5)).await.map_err(|(e,_)| e)?;
```
**Verdict: Trivial for SASL_SSL or plaintext modes; Moderate for IAM auth.** MSK with IAM SASL needs `aws-msk-iam-sasl-signer-rust`; less mature than Java/Python equivalents. Prefer SCRAM-SHA-512 or mTLS for Rust workloads on MSK until this matures.

## Amazon MQ RabbitMQ ↔ rabbitmq container

```rust
use lapin::{Connection, ConnectionProperties, options::*, BasicProperties};
let conn = Connection::connect(&std::env::var("RABBITMQ_URL")?, ConnectionProperties::default()).await?;
// AWS:   amqps://user:****@b-xyz.mq.us-east-1.amazonaws.com:5671
// Local: amqp://guest:guest@rabbitmq:5672/
let ch = conn.create_channel().await?;
ch.queue_declare("jobs", QueueDeclareOptions::default(), Default::default()).await?;
ch.basic_publish("", "jobs", BasicPublishOptions::default(),
    b"job-1", BasicProperties::default()).await?;
```
**Verdict: Trivial.** Real RabbitMQ both sides; only TLS differs.

## IoT Core MQTT ↔ mosquitto container

```rust
use rumqttc::{MqttOptions, AsyncClient, QoS, Transport, TlsConfiguration};
let mut opts = MqttOptions::new("client-1", &std::env::var("MQTT_HOST")?,
                                std::env::var("MQTT_PORT")?.parse()?);
if std::env::var("MQTT_TLS").as_deref() == Ok("true") {
    opts.set_transport(Transport::tls_with_config(TlsConfiguration::default().into()));
}
let (client, _eventloop) = AsyncClient::new(opts, 10);
client.publish("telemetry/sensor1", QoS::AtLeastOnce, false, r#"{"temp":22.5}"#).await?;
```
**Verdict: Trivial wire; Moderate auth.** mosquitto accepts plaintext; IoT Core mandates mTLS with X.509 device certs. Provision certs out-of-band; gate `Transport::tls_with_config` on env.

## SES ↔ mailhog (via SMTP)

```rust
use lettre::{Message, SmtpTransport, Transport, transport::smtp::authentication::Credentials};
let msg = Message::builder()
    .from("noreply@app.com".parse()?).to("user@example.com".parse()?)
    .subject("Hi").body("hello".to_string())?;
let host = std::env::var("SMTP_HOST")?;
let port: u16 = std::env::var("SMTP_PORT")?.parse()?;
let mut b = SmtpTransport::builder_dangerous(&host).port(port);
if let (Ok(u), Ok(p)) = (std::env::var("SMTP_USER"), std::env::var("SMTP_PASS")) {
    b = b.credentials(Credentials::new(u, p));
}
b.build().send(&msg)?;
```
**Verdict: Trivial.** SES has SMTP credentials. Locally, mailhog accepts on `mailhog:1025` no-auth. `lettre` handles both via env-driven config.

## Timestream for InfluxDB ↔ influxdb container

```rust
use influxdb2::{Client, models::DataPoint};
let client = Client::new(std::env::var("INFLUX_URL")?,
                         std::env::var("INFLUX_ORG")?,
                         std::env::var("INFLUX_TOKEN")?);
client.write("metrics", futures::stream::iter(vec![
    DataPoint::builder("cpu").field("usage", 42.0_f64).build()?
])).await?;
```
**Verdict: Trivial.** Same `influxdb2` crate; URL + token swap.

## Verified Permissions ↔ Cedar (Rust crate)

```rust
use cedar_policy::{Authorizer, Context, Decision, Entities, PolicySet, Request};
let policies: PolicySet = std::env::var("CEDAR_POLICIES")?.parse()?;
let entities: Entities = Entities::empty();
let req = Request::new(/* principal, action, resource */ None, None, None, Context::empty(), None)?;
let decision = Authorizer::new().is_authorized(&req, &policies, &entities).decision();
assert_eq!(decision, Decision::Allow);
```
**Verdict: Trivial.** Cedar is itself a Rust project. The OSS engine (`cedar-policy` crate) is the same code AWS runs server-side. Verified Permissions adds storage + lifecycle; the *decision* is identical locally.

---

# Moderate — same crate, configuration / behavior differs

## Lambda handler vs axum server (one codebase via `lambda_http`)

```rust
use axum::{Router, routing::get, extract::Query};
use lambda_http::{run, Error};
use serde::Deserialize;

#[derive(Deserialize)] struct P { name: String }
async fn hi(Query(p): Query<P>) -> String { format!("hi {}", p.name) }

fn app() -> Router { Router::new().route("/hi", get(hi)) }

#[tokio::main]
async fn main() -> Result<(), Error> {
    if std::env::var("AWS_LAMBDA_RUNTIME_API").is_ok() {
        run(app()).await         // → Lambda
    } else {
        let listener = tokio::net::TcpListener::bind("0.0.0.0:8080").await?;
        axum::serve(listener, app()).await?; Ok(()) // → standalone
    }
}
```
**Verdict: Moderate.** The cleanest "one codebase, two deployment shapes" story in AWS today. The if/else dispatch on `AWS_LAMBDA_RUNTIME_API` is the entire seam. Streaming responses require Lambda Function URLs; websockets via Lambda mean API Gateway WebSocket (see Hard section).

## Apalis worker — SQS backend (cloud) vs Redis backend (local)

```rust
use apalis::prelude::*;
#[derive(serde::Serialize, serde::Deserialize, Clone)]
struct ProcessOrder { order_id: u64 }
async fn process(job: ProcessOrder, _ctx: Context) -> Result<(), apalis::Error> { Ok(()) }

#[tokio::main]
async fn main() -> Result<(), Box<dyn std::error::Error>> {
    match std::env::var("APALIS_BACKEND")?.as_str() {
        "sqs" => {
            let storage = apalis_sqs::SqsStorage::new(&std::env::var("QUEUE_URL")?).await?;
            Monitor::new().register(WorkerBuilder::new("orders").backend(storage).build_fn(process))
                .run().await?;
        }
        _ => {
            let storage = apalis_redis::RedisStorage::connect(std::env::var("REDIS_URL")?).await?;
            Monitor::new().register(WorkerBuilder::new("orders").backend(storage).build_fn(process))
                .run().await?;
        }
    }
    Ok(())
}
```
**Verdict: Moderate.** Same job code; backend swap at startup. SQS-as-backend drops features: no priority, polling-only consume, visibility timeout != ack timing. If your tasks rely on Redis pub/sub fan-out, the local-dev experience will quietly do something SQS doesn't replicate.

## EventBridge Scheduler (cloud) vs tokio-cron-scheduler (local)

```rust
// Cloud: defined at deploy time; fires SQS msg / Lambda invocation. Handler is the same Rust binary.
// Local (only when env enables it):
use tokio_cron_scheduler::{Job, JobScheduler};
let sched = JobScheduler::new().await?;
sched.add(Job::new_async("0 0 2 * * *", |_uuid, _l| Box::pin(async {
    reqwest::Client::new().post("http://app:8080/jobs/nightly").send().await.ok();
}))?).await?;
sched.start().await?;
```
**Verdict: Moderate.** Handler code is the same; only the trigger differs. caravan should generate the EventBridge Scheduler rule from a yaml `triggers:` declaration and skip the local scheduler container unless explicitly enabled — most dev sessions don't need cron firing.

## X-Ray tracing (cloud) vs Jaeger (local) via OpenTelemetry

```rust
use opentelemetry_otlp::WithExportConfig;
use opentelemetry::trace::TracerProvider as _;
use tracing_subscriber::layer::SubscriberExt;

let endpoint = std::env::var("OTEL_EXPORTER_OTLP_ENDPOINT")
    .unwrap_or_else(|_| "http://jaeger:4317".into());
let tracer = opentelemetry_otlp::new_pipeline().tracing()
    .with_exporter(opentelemetry_otlp::new_exporter().tonic().with_endpoint(endpoint))
    .install_batch(opentelemetry_sdk::runtime::Tokio)?;
let subscriber = tracing_subscriber::registry()
    .with(tracing_opentelemetry::layer().with_tracer(tracer));
tracing::subscriber::set_global_default(subscriber)?;
// AWS:   ADOT collector → X-Ray  (OTEL_EXPORTER_OTLP_ENDPOINT=http://otel-collector:4317)
// Local: jaeger                  (OTEL_EXPORTER_OTLP_ENDPOINT=http://jaeger:4317)
```
**Verdict: Moderate.** Same `tracing` instrumentation; only the OTLP endpoint env flips. Strongest abstraction pattern in Rust observability.

## AppConfig (cloud) vs env / file (local)

```rust
async fn feature_flags() -> serde_json::Value {
    if let Ok(_) = std::env::var("APPCONFIG_ENDPOINT") {
        // boilerplate: start_configuration_session, get_latest_configuration, parse
        let base = aws_config::defaults(BehaviorVersion::latest()).load().await;
        let cfg = aws_sdk_appconfigdata::Client::new(&base);
        // ...
        serde_json::json!({})
    } else {
        serde_json::from_str(&std::env::var("FEATURE_FLAGS").unwrap_or_else(|_| "{}".into())).unwrap()
    }
}
```
**Verdict: Moderate.** AppConfig's value is staged rollouts + validators. Locally, env-driven JSON is fine.

## Bedrock (cloud) vs ollama-rs (local)

```rust
#[async_trait::async_trait]
trait LlmClient: Send + Sync {
    async fn complete(&self, prompt: &str) -> Result<String, Box<dyn std::error::Error>>;
}
// BedrockClient using aws_sdk_bedrockruntime::Client and invoke_model
// OllamaClient using ollama_rs::Ollama::generate
fn build_llm() -> Box<dyn LlmClient> {
    match std::env::var("LLM_BACKEND").as_deref() {
        Ok("bedrock") => Box::new(BedrockClient::new()),
        _ => Box::new(OllamaClient::new()),
    }
}
```
**Verdict: Moderate for plumbing, Intractable for output equivalence.** Trait abstraction works for code paths; the *model* differs (Claude vs Llama vs Mistral), so output quality and exact tokens differ. Local is for plumbing-level tests; real Bedrock for output-quality tests.

---

# Hard — different paradigms, needs a real abstraction

## Cognito (cloud) vs local JWT issuer

```rust
use jsonwebtoken::{decode, DecodingKey, Validation, Algorithm};
use serde::Deserialize;

#[derive(Deserialize, Debug)]
pub struct Claims { pub sub: String, pub email: Option<String> }

#[async_trait::async_trait]
pub trait TokenVerifier: Send + Sync {
    async fn verify(&self, token: &str) -> Result<Claims, AuthError>;
}

pub struct CognitoVerifier { /* JWKS cache, user_pool_id, region */ }
#[async_trait::async_trait]
impl TokenVerifier for CognitoVerifier { /* fetch JWKS, RS256 verify */ async fn verify(&self, _t: &str) -> Result<Claims, AuthError> { todo!() } }

pub struct LocalJwtVerifier { secret: String }
#[async_trait::async_trait]
impl TokenVerifier for LocalJwtVerifier {
    async fn verify(&self, t: &str) -> Result<Claims, AuthError> {
        let key = DecodingKey::from_secret(self.secret.as_bytes());
        Ok(decode::<Claims>(t, &key, &Validation::new(Algorithm::HS256))?.claims)
    }
}
```
**Verdict: Hard.** Cognito's user lifecycle (sign-up, MFA, custom attributes, hosted UI) has no local equivalent. The right abstraction is *token verification*, not user management. caravan ships the `TokenVerifier` trait + `CognitoVerifier` + `LocalJwtVerifier`; user wires which based on env.

## API Gateway WebSocket (cloud) vs axum websockets (local)

```rust
// Cloud (Lambda + APIGW WebSocket)
async fn on_connect(event: lambda_runtime::LambdaEvent<serde_json::Value>) -> Result<serde_json::Value, lambda_runtime::Error> {
    let conn_id = event.payload["requestContext"]["connectionId"].as_str().unwrap();
    // store conn_id in DynamoDB
    Ok(serde_json::json!({"statusCode": 200}))
}
async fn push(conn_id: &str, domain: &str, stage: &str, body: &[u8]) -> Result<(), Box<dyn std::error::Error>> {
    let endpoint = format!("https://{}/{}", domain, stage);
    let base = aws_config::defaults(BehaviorVersion::latest()).load().await;
    let mgmt = aws_sdk_apigatewaymanagement::Client::from_conf(
        aws_sdk_apigatewaymanagement::config::Builder::from(&base).endpoint_url(endpoint).build());
    mgmt.post_to_connection().connection_id(conn_id).data(body.to_vec().into()).send().await?;
    Ok(())
}

// Local (axum)
use axum::extract::ws::{WebSocket, WebSocketUpgrade, Message};
use axum::response::Response;
async fn ws_handler(ws: WebSocketUpgrade) -> Response {
    ws.on_upgrade(|mut socket: WebSocket| async move {
        while let Some(Ok(Message::Text(s))) = socket.recv().await {
            let _ = socket.send(Message::Text(format!("echo: {s}"))).await;
        }
    })
}
```
**Verdict: Hard.** Connection model inverts: APIGW WebSocket *stores* connections (DDB) and pushes via REST; axum websockets are stateful per-process. No shared abstraction. caravan must pick one model — for real-time apps, Fargate + axum websockets is the saner cloud target.

## Step Functions Standard (cloud) vs Apalis chain / DAG (local)

```rust
// Cloud (ASL JSON, deployed via Terraform):
// {"StartAt":"Validate","States":{
//   "Validate":{"Type":"Task","Resource":"arn:aws:lambda:...:validate","Next":"Charge"},
//   "Charge":{"Type":"Task","Resource":"arn:aws:lambda:...:charge","Next":"Notify"},
//   "Notify":{"Type":"Task","Resource":"arn:aws:lambda:...:notify","End":true}}}

// Local (apalis chain — manual)
async fn run_chain(order: Order) -> anyhow::Result<()> {
    let v = validate(order).await?;
    let c = charge(v).await?;
    notify(c).await?;
    Ok(())
}
```
**Verdict: Hard.** Step Functions has durable state, retry-policy DSL, parallel branches, human-approval steps. Apalis chains exist but persistence/observability is weaker. Recommendation: caravan only supports workflows on cloud and documents "no local equivalent — test against AWS or against `aws-stepfunctions-local`."

## SQS + Lambda fan-out (cloud) vs in-process tasks (local)

```rust
// Cloud producer
sqs.send_message().queue_url(&q).message_body(&serde_json::to_string(&order)?).send().await?;
// Cloud consumer (Lambda triggered by SQS)
async fn lambda_consumer(event: lambda_runtime::LambdaEvent<aws_lambda_events::sqs::SqsEvent>)
    -> Result<(), lambda_runtime::Error> {
    for record in event.payload.records {
        if let Some(body) = record.body { process(serde_json::from_str(&body)?).await?; }
    }
    Ok(())
}

// Local: in-process spawn (lossy — dies with the process)
tokio::spawn(async move { process(order).await });
```
**Verdict: Hard if you care about local-vs-cloud durability.** `tokio::spawn` is fire-and-forget per-process. The honest local equivalent is to run an `apalis` worker + ElasticMQ — keeping the *queue* abstraction honest on both sides. That's the recommended pattern.

---

# Intractable — no realistic local equivalent

For these, caravan must mark `cloud_only: true` and refuse to bind locally. Trying to emulate is worse — false positives hide bugs.

## SageMaker training/inference, Bedrock LLM, Bedrock Knowledge Bases / Agents / Guardrails

```rust
// Cloud
let bedrock = aws_sdk_bedrockruntime::Client::new(&aws_config::defaults(BehaviorVersion::latest()).load().await);
let resp = bedrock.invoke_model()
    .model_id("anthropic.claude-opus-4-7-20260416-v1:0")
    .body(serde_json::to_vec(&serde_json::json!({"messages":[{"role":"user","content":"hi"}], "max_tokens":100}))?.into())
    .send().await?;

// Local "equivalent" — different model, different API
use ollama_rs::{Ollama, generation::chat::ChatMessageRequest};
let ollama = Ollama::default();
let resp = ollama.send_chat_messages(ChatMessageRequest::new("llama3.1".into(), vec![/* msgs */])).await?;
```
**Verdict: Intractable.** Outputs are not comparable. Knowledge Bases / Agents / Guardrails are orchestration value; no OSS equivalent. Wrap behind a trait for plumbing tests; accept the model differs.

## CloudFront / Lambda@Edge / Global Accelerator / CloudFront Functions

**Verdict: Intractable.** Edge runtime is JS-only as of 2026; Rust isn't a target at the edge regardless. CDN behavior (POP routing, edge caching) unobservable locally. Test routing logic as pure functions; trust the CDN to invoke correctly.

## S3 Express One Zone, S3 Vectors, Aurora DSQL, DAX, Neptune Analytics, IAM enforcement

**Verdict: Intractable.** Each has properties (single-AZ ultra-low-latency, ANN-on-S3, multi-region active-active SQL, microsecond DDB cache, in-memory graph analytics, real IAM evaluation) that require AWS to demonstrate.

## CloudWatch Synthetics / RUM / Application Signals, IoT Device Defender / Analytics / SiteWise / TwinMaker / FleetWise

**Verdict: Intractable.** Observability or domain-specific products built around AWS-managed data flows. For local dev: skip and use OTel + raw logs.

## Step Functions Distributed Map, SNS Mobile Push, Forecast / Personalize, SageMaker JumpStart / Canvas

**Verdict: Intractable.** Parallel-children behavior / real APNs+FCM dispatch / managed-model marketplace / no-code UI — all AWS-internal.

---

# Per-group difficulty summary

| Group | AWS service | Local pair | Difficulty |
|---|---|---|---|
| Compute — Function | Lambda | axum via `lambda_http` | Moderate |
| Compute — Container | ECS/Fargate/App Runner | docker-compose | Trivial |
| Compute — VM | EC2 | docker container | N/A (don't abstract) |
| Storage — Object | S3 | minio | **Trivial** |
| Storage — Object | S3 Express One Zone | (none) | Intractable |
| Storage — Object | S3 Vectors | (none) | Intractable |
| Storage — File | EFS | docker volume | Moderate (no perf parity) |
| Storage — Block | EBS | docker volume | N/A |
| DB — RDBMS | RDS/Aurora Postgres | postgres | **Trivial** |
| DB — RDBMS | RDS/Aurora MySQL | mysql | **Trivial** |
| DB — RDBMS | Aurora DSQL | (none) | Intractable |
| DB — KV | DynamoDB | dynamodb-local | **Trivial** |
| DB — KV | DAX | (none) | Intractable |
| DB — Document | DocumentDB | mongo | Trivial happy-path; partial overall |
| DB — Cache | ElastiCache Redis | redis | **Trivial** |
| DB — Cache | ElastiCache Memcached | memcached | Trivial |
| DB — Cache | MemoryDB | redis (no durability) | Moderate |
| DB — Search | OpenSearch | opensearch | **Trivial** |
| DB — Vector | Aurora pgvector | pgvector | **Trivial** |
| DB — Vector | OpenSearch k-NN | opensearch w/ knn | **Trivial** |
| DB — Time-series | Timestream LiveAnalytics | (none) | Intractable wire |
| DB — Time-series | Timestream for InfluxDB | influxdb | **Trivial** |
| DB — Graph | Neptune | tinkerpop/neo4j | Partial |
| Messaging — Queue | SQS | ElasticMQ | **Trivial** |
| Messaging — Queue | Amazon MQ RabbitMQ | rabbitmq (`lapin`) | **Trivial** |
| Messaging — PubSub | SNS | localstack | **Trivial** |
| Messaging — Event Bus | EventBridge default | localstack (partial) | Moderate |
| Messaging — Stream | Kinesis | localstack | **Trivial** producer; Hard consumer (no Rust KCL) |
| Messaging — Stream | MSK | kafka (`rdkafka`) | **Trivial** for SCRAM/plaintext; Moderate for IAM |
| API edge | API Gateway HTTP | axum via `lambda_http` | Moderate |
| API edge | API Gateway WebSocket | axum websockets | **Hard** |
| API edge | AppSync | `async-graphql` server | Hard / Intractable |
| API edge | ALB | run container directly | N/A |
| CDN | CloudFront | (none) | Intractable |
| CDN | Lambda@Edge / CloudFront Functions | (none — JS only) | Intractable |
| DNS | Route 53 | /etc/hosts / coredns | Partial |
| Auth | Cognito | Keycloak / LocalJwtVerifier | **Hard** |
| Auth | IAM | (none; LocalStack stubs) | Intractable enforcement |
| Auth | Verified Permissions (Cedar) | `cedar-policy` crate | **Trivial** |
| Secrets | Secrets Manager | localstack | **Trivial** |
| Secrets | SSM Parameter Store | localstack | **Trivial** |
| Secrets | KMS | localstack | Moderate (software keys only) |
| Workflow | Step Functions Standard | aws-stepfunctions-local | **Trivial** within ASL; Hard for multi-service workflows |
| Workflow | Step Functions Express | (partial local) | Hard |
| Workflow | EventBridge Scheduler | tokio-cron-scheduler / apalis | Moderate |
| Workflow | MWAA | (Python-only) | Cross-language |
| Email | SES | mailhog (SMTP via `lettre`) | **Trivial** |
| Email | SNS SMS | (none — inspect-only) | Intractable |
| Observability | CloudWatch Logs | stdout (`tracing`) | **Trivial** |
| Observability | CloudWatch Metrics | prometheus / EMF | Moderate |
| Observability | X-Ray | jaeger via OTel | Moderate |
| Observability | RUM / Synthetics / AppSignals | (none) | Intractable |
| Analytics — Warehouse | Redshift | clickhouse / postgres | Partial |
| Analytics — Query | Athena | trino (`prusto`) | Partial |
| Analytics — ETL | Glue | spark container (Python-shaped) | Cross-language |
| Analytics — Big-data | EMR | spark (Python-shaped) | Cross-language |
| ML — Training | SageMaker | Python script | Cross-language |
| ML — LLM | Bedrock | ollama-rs (different model) | Moderate plumbing; Intractable outputs |
| ML — Vision/Speech/NLP | Rekognition/Polly/Transcribe/Comprehend | candle/whisper-rs (different models) | Partial |
| IoT — Gateway | IoT Core MQTT | mosquitto (`rumqttc`) | **Trivial** wire; Moderate auth |
| IoT — Edge | Greengrass | greengrass-runtime (Rust components since 2024) | Trivial |
| IoT — Analytics | IoT Analytics / SiteWise / etc | (none) | Intractable |

**Headcount**:
- **Trivial**: ~18 pairs — caravan's bread and butter for Rust.
- **Moderate**: ~7 — adapter modules (`lambda_http` for Lambda↔axum; apalis-sqs vs apalis-redis; OTel; LlmClient trait plumbing).
- **Hard**: ~3 — protocol traits (Cognito, WebSocket, multi-service Step Functions).
- **Intractable**: ~18 — `cloud_only: true`.
- **Cross-language seams**: ~4 (MWAA, Glue, EMR, SageMaker training) — Rust users orchestrate them via APIs but write the training/ETL code in Python.

**vs Python (~22 Trivial / ~12 Moderate / ~5 Hard / ~15 Intractable)**: Rust ecosystem is tighter, not weaker. Some pairs collapse from Moderate→Trivial (one binary deploys two shapes via `lambda_http`); some grow to Intractable for ecosystem reasons (Lambda@Edge has no Rust runtime, KCL has no Rust port). Net result: Rust is a first-class target for the *containers-first* abstraction shape.

See `caravan_abstraction_v2.md` for how these difficulty bands translate into v2 PoC scope, IR primitives, and the yaml switch shape.
