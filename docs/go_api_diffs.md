# Go API Diffs: AWS ↔ Local Container

> **Snapshot date: 2026-05-16.** References `aws_service_groups.md`, `mapping_go_to_aws.md`, `mapping_aws_to_go.md`.
> **Framing**: Go ecosystem evidence feeding into `thesis.md` (conceptual home) and `supeux_abstraction_v4.md` (long-form derivation; supersedes v3). The difficulty bands below map onto v4's T0/T1/T2 service tiers — see the row at the bottom of the bands table.

For each AWS↔local pair surfaced in the mapping files, this file shows the exact Go code change required to switch between them and assigns a **difficulty band**:

| Band | Meaning | What supeux does | v4 tier |
|---|---|---|---|
| **Trivial** | One env var (endpoint URL or DSN) controls the switch. Same imports, same calls. | Sets env vars at deploy. Done. | **T0** |
| **Moderate** | Same library, but a few config keys or call shapes differ; or a small adapter (framework Lambda wrapper, env-driven branches) closes the gap. | Documents the adapter shape. Usually no library needed. | **T0** (mostly); occasionally T1 |
| **Hard** | Different wire APIs cloud vs local; a structural abstraction is required. | **Uses the recommended Go community library** — `langchaingo` *or* `eino` for LLMs, `golang-jwt` + `keyfunc` for token verification, `net/smtp` *or* `gomail` for email, `whisper.cpp` Go bindings for Whisper-shaped STT. supeux **does not ship** a runtime adapter library; see v4 §4. | **T1** |
| **Intractable** | No realistic local equivalent. Don't try to emulate — false positives hide bugs. | Marks `cloud_only:` in the yaml IR (v4 §6, §8). User picks one of v4's four patterns per service: **skip** (feature-flag off locally), **hit-real** (mounted creds; pay real $$), **engine-swap** (DAX→DDB-local, S3 Vectors→hnsw, etc.), or **stub**. | **T2** |

Snippets are ≤15 lines each and assume `os.Getenv` is populated by the supeux runtime / docker-compose / GHA matrix. **Build context**: snippets assume `CGO_ENABLED=0 GOOS=linux go build` static binaries unless CGO is explicitly required (flagged per snippet).

Imports are abbreviated in some snippets (typical Go conventions: `aws.String`, `aws.ToString`, `ctx`); full import blocks shown only when load-bearing.

---

# Trivial — env-driven `BaseEndpoint` or DSN swap

These are the wins. A single env var flips cloud↔local with no code change.

## S3 ↔ MinIO

```go
import (
    "github.com/aws/aws-sdk-go-v2/aws"
    "github.com/aws/aws-sdk-go-v2/service/s3"
)

client := s3.NewFromConfig(cfg, func(o *s3.Options) {
    if ep := os.Getenv("S3_ENDPOINT_URL"); ep != "" {
        o.BaseEndpoint = aws.String(ep)  // http://minio:9000 locally
        o.UsePathStyle = true            // required for minio; AWS rejects it
    }
})
_, _ = client.PutObject(ctx, &s3.PutObjectInput{
    Bucket: aws.String("my-bucket"),
    Key:    aws.String("hello.txt"),
    Body:   strings.NewReader("hi"),
})
```
**Verdict: Trivial.** Same `UsePathStyle` gotcha as Rust's `force_path_style(true)` and TS's `forcePathStyle: true`. Caveats: minio doesn't do storage-class tiering or strong-read-after-write under partial failures. For 95% of code, same.

## DynamoDB ↔ dynamodb-local

```go
import (
    "github.com/aws/aws-sdk-go-v2/feature/dynamodb/attributevalue"
    "github.com/aws/aws-sdk-go-v2/service/dynamodb"
)

client := dynamodb.NewFromConfig(cfg, func(o *dynamodb.Options) {
    if ep := os.Getenv("DYNAMODB_ENDPOINT_URL"); ep != "" {
        o.BaseEndpoint = aws.String(ep)  // http://dynamodb:8000 locally
    }
})
item, _ := attributevalue.MarshalMap(struct{ PK, SK, Name string }{"u#1", "profile", "Alice"})
_, _ = client.PutItem(ctx, &dynamodb.PutItemInput{
    TableName: aws.String("items"),
    Item:      item,
})
```
**Verdict: Trivial.** `attributevalue.MarshalMap` removes the raw `map[string]types.AttributeValue{"S": "..."}` wrapping — closest to TS's `@aws-sdk/lib-dynamodb` Document client and boto3's `resource("dynamodb")`. Streams + TTL deletes are partial in dynamodb-local; transactions, conditional writes are solid.

## SQS ↔ ElasticMQ / localstack

```go
import "github.com/aws/aws-sdk-go-v2/service/sqs"

client := sqs.NewFromConfig(cfg, func(o *sqs.Options) {
    if ep := os.Getenv("SQS_ENDPOINT_URL"); ep != "" {
        o.BaseEndpoint = aws.String(ep)
    }
})
queueURL := os.Getenv("QUEUE_URL")
// https://sqs... in AWS; http://elasticmq:9324/000000000000/queue locally
_, _ = client.SendMessage(ctx, &sqs.SendMessageInput{
    QueueUrl:    aws.String(queueURL),
    MessageBody: aws.String("job#42"),
})
out, _ := client.ReceiveMessage(ctx, &sqs.ReceiveMessageInput{
    QueueUrl:        aws.String(queueURL),
    WaitTimeSeconds: 20,
})
```
**Verdict: Trivial.** ElasticMQ implements long-poll, DLQ wiring, FIFO dedup. Make the queue URL itself an env var, not a constructed string.

## SNS ↔ localstack

```go
import "github.com/aws/aws-sdk-go-v2/service/sns"

client := sns.NewFromConfig(cfg, func(o *sns.Options) {
    if ep := os.Getenv("SNS_ENDPOINT_URL"); ep != "" {
        o.BaseEndpoint = aws.String(ep)
    }
})
_, _ = client.Publish(ctx, &sns.PublishInput{
    TopicArn: aws.String(os.Getenv("TOPIC_ARN")),
    Message:  aws.String("event"),
})
```
**Verdict: Trivial.** ARN format differs locally (`arn:aws:sns:us-east-1:000000000000:topic-name`) — pass it as env var.

## Kinesis Data Streams ↔ localstack

```go
import "github.com/aws/aws-sdk-go-v2/service/kinesis"

client := kinesis.NewFromConfig(cfg, func(o *kinesis.Options) {
    if ep := os.Getenv("KINESIS_ENDPOINT_URL"); ep != "" {
        o.BaseEndpoint = aws.String(ep)
    }
})
data, _ := json.Marshal(map[string]string{"type": "click"})
_, _ = client.PutRecord(ctx, &kinesis.PutRecordInput{
    StreamName:   aws.String("events"),
    PartitionKey: aws.String("user-1"),
    Data:         data,
})
```
**Verdict: Trivial for producer.** Go has an official KCL port (`aws/amazon-kinesis-client-go`) but consumer-coordination behavior is hard to test locally. For consumers, prefer Lambda triggered by Kinesis or raw `GetRecords` against shards.

## Secrets Manager / SSM Parameter Store ↔ localstack

```go
import "github.com/aws/aws-sdk-go-v2/service/ssm"

client := ssm.NewFromConfig(cfg, func(o *ssm.Options) {
    if ep := os.Getenv("SSM_ENDPOINT_URL"); ep != "" {
        o.BaseEndpoint = aws.String(ep)
    }
})
out, _ := client.GetParameter(ctx, &ssm.GetParameterInput{
    Name:           aws.String("/app/db/password"),
    WithDecryption: aws.Bool(true),
})
password := aws.ToString(out.Parameter.Value)
```
**Verdict: Trivial.** Same for `aws-sdk-go-v2/service/secretsmanager`. KMS-decryption is software-only locally.

## CloudWatch Logs ↔ stdout

```go
import "log/slog"

logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
logger.Info("processed order", "order_id", orderID)
```
**Verdict: Trivial.** Best practice: emit JSON to stdout via `log/slog` (Go 1.21+ stdlib structured logger). Lambda → CloudWatch automatic; Fargate → awslogs driver; locally → `docker logs`. Almost no Go app should call `aws-sdk-go-v2/service/cloudwatchlogs` `PutLogEvents` directly. Alternatives: `rs/zerolog` (zero-allocation), `uber-go/zap` (older, fast).

## Step Functions ↔ aws-stepfunctions-local

```go
import "github.com/aws/aws-sdk-go-v2/service/sfn"

client := sfn.NewFromConfig(cfg, func(o *sfn.Options) {
    if ep := os.Getenv("STEPFUNCTIONS_ENDPOINT_URL"); ep != "" {
        o.BaseEndpoint = aws.String(ep)
    }
})
input, _ := json.Marshal(map[string]string{"orderId": "o-123"})
_, _ = client.StartExecution(ctx, &sfn.StartExecutionInput{
    StateMachineArn: aws.String(os.Getenv("STATE_MACHINE_ARN")),
    Input:           aws.String(string(input)),
})
```
**Verdict: Trivial.** ASL state machine definitions deploy identically. AWS provides the official local container (`amazon/aws-stepfunctions-local`). Tasks targeting real AWS services need their own endpoint overrides via env vars — the local container supports them.

## RDS / Aurora Postgres ↔ postgres container

```go
import (
    "database/sql"
    _ "github.com/jackc/pgx/v5/stdlib"
)

db, _ := sql.Open("pgx", os.Getenv("DATABASE_URL"))
// AWS:   postgres://app:****@aurora-cluster.cluster-xyz.us-east-1.rds.amazonaws.com:5432/app
// Local: postgres://app:dev@postgres:5432/app
_, _ = db.ExecContext(ctx, "INSERT INTO users (name) VALUES ($1)", "Alice")
```
**Verdict: Trivial.** Same `pgx`, same SQL. Aurora-specific features (read replicas, Optimized Reads) are runtime — not visible to your code.
**Go gotcha (`sqlc`)**: `sqlc generate` runs at build time against schema files. CI must run codegen after migrations; many teams commit generated code.
**Go gotcha (`gorm` AutoMigrate)**: works locally but use `golang-migrate/migrate` or `pressly/goose` for production migrations — `AutoMigrate` silently diverges from managed migration history.

## pgvector (Aurora) ↔ pgvector container

```go
import (
    "database/sql"
    _ "github.com/jackc/pgx/v5/stdlib"
    "github.com/pgvector/pgvector-go"
)

db, _ := sql.Open("pgx", os.Getenv("DATABASE_URL"))
_, _ = db.ExecContext(ctx, "CREATE EXTENSION IF NOT EXISTS vector")
embedding := pgvector.NewVector(make([]float32, 1536))
_, _ = db.ExecContext(ctx, "INSERT INTO docs (id, embedding) VALUES ($1, $2)", 1, embedding)
```
**Verdict: Trivial.** Same extension, same syntax. `pgvector-go` provides typed integrations with `pgx`, `database/sql`, `gorm`.

## RDS / Aurora MySQL ↔ mysql container

```go
import (
    "database/sql"
    _ "github.com/go-sql-driver/mysql"
)

db, _ := sql.Open("mysql", os.Getenv("DATABASE_URL"))
// AWS:   mysql://app:****@aurora-mysql.cluster-xyz.us-east-1.rds.amazonaws.com:3306/app
// Local: mysql://app:dev@mysql:3306/app
_, _ = db.ExecContext(ctx, "INSERT INTO users (name) VALUES (?)", "Alice")
```
**Verdict: Trivial.**

## ElastiCache Redis ↔ redis container

```go
import "github.com/redis/go-redis/v9"

opt, _ := redis.ParseURL(os.Getenv("REDIS_URL"))
// AWS:   redis://master.cache-cluster.xyz.cache.amazonaws.com:6379/0  (or rediss:// for TLS)
// Local: redis://redis:6379/0
rdb := redis.NewClient(opt)
_ = rdb.Set(ctx, "session:abc", "user-123", time.Hour).Err()
```
**Verdict: Trivial.** Cluster-mode-enabled ElastiCache requires `redis.NewClusterClient(&redis.ClusterOptions{Addrs: []string{...}})` — different constructor. If you depend on cluster mode, run `bitnami/redis-cluster` locally.

## DocumentDB ↔ mongo container

```go
import (
    "go.mongodb.org/mongo-driver/mongo"
    "go.mongodb.org/mongo-driver/mongo/options"
)

client, _ := mongo.Connect(ctx, options.Client().ApplyURI(os.Getenv("MONGO_URL")))
defer client.Disconnect(ctx)
_, _ = client.Database("app").Collection("users").InsertOne(ctx, map[string]string{"name": "Alice"})
```
**Verdict: Trivial for happy path, partial in general.** DocumentDB is wire-compatible with Mongo but lacks ~30% of aggregation operators (esp. `$lookup` semantics, change-stream resumability quirks). If your code uses modern aggregations, real Mongo locally gives *false positives*. Test critical paths against DocumentDB in CI.

## OpenSearch Service ↔ opensearch container

```go
import (
    opensearch "github.com/opensearch-project/opensearch-go/v3"
    "github.com/opensearch-project/opensearch-go/v3/opensearchapi"
)

client, _ := opensearchapi.NewClient(opensearchapi.Config{
    Client: opensearch.Config{
        Addresses: []string{os.Getenv("OPENSEARCH_URL")},  // https://...es.amazonaws.com:443 vs http://opensearch:9200
        Username:  os.Getenv("OS_USER"),
        Password:  os.Getenv("OS_PASS"),
    },
})
_, _ = client.Index(ctx, opensearchapi.IndexReq{Index: "docs", Body: strings.NewReader(`{"title":"hello"}`)})
```
**Verdict: Trivial.** Same `opensearch-go/v3` library. Use the OpenSearch image (not Elasticsearch) — the post-fork divergence on `go-elasticsearch/v8` is hostile.

## MSK ↔ kafka container

```go
import "github.com/segmentio/kafka-go"

w := &kafka.Writer{
    Addr:  kafka.TCP(strings.Split(os.Getenv("KAFKA_BOOTSTRAP"), ",")...),
    Topic: "events",
    // AWS:   b-1.msk-cluster...kafka.us-east-1.amazonaws.com:9094 (TLS) or :9098 (IAM)
    // Local: kafka:9092
}
_ = w.WriteMessages(ctx, kafka.Message{Value: []byte("hello")})
```
**Verdict: Trivial for SASL_SSL or plaintext; Trivial for IAM auth too (Go advantage).** Go's `aws/aws-msk-iam-sasl-signer-go` is mature; both `segmentio/kafka-go` and `confluentinc/confluent-kafka-go` integrate cleanly with the IAM signer. Cleaner than TS's `kafkajs` SigV4 gap.

## Amazon MQ RabbitMQ ↔ rabbitmq container

```go
import amqp "github.com/rabbitmq/amqp091-go"

conn, _ := amqp.Dial(os.Getenv("RABBITMQ_URL"))
// AWS:   amqps://user:****@b-xyz.mq.us-east-1.amazonaws.com:5671
// Local: amqp://guest:guest@rabbitmq:5672/
defer conn.Close()
ch, _ := conn.Channel()
_, _ = ch.QueueDeclare("jobs", true, false, false, false, nil)
_ = ch.Publish("", "jobs", false, false, amqp.Publishing{Body: []byte("job-1")})
```
**Verdict: Trivial.** Real RabbitMQ both sides; only TLS differs.

## IoT Core MQTT ↔ mosquitto container

```go
import mqtt "github.com/eclipse/paho.mqtt.golang"

opts := mqtt.NewClientOptions().AddBroker(os.Getenv("MQTT_BROKER"))
// IoT Core requires mTLS with X.509 device certs:
if os.Getenv("MQTT_TLS") == "true" {
    opts.SetTLSConfig(loadTLSConfig(os.Getenv("MQTT_KEY"), os.Getenv("MQTT_CERT")))
}
client := mqtt.NewClient(opts)
client.Connect().Wait()
client.Publish("telemetry/sensor1", 0, false, `{"temp":22.5}`)
```
**Verdict: Trivial wire; Moderate auth.** mosquitto can be configured with or without TLS; IoT Core mandates mTLS with X.509. Real cert provisioning is the auth-shaped seam. For AWS-specific bits (thing shadow, jobs) use `aws/aws-iot-device-sdk-go` instead.

## SES ↔ mailhog (via SMTP using `net/smtp`)

```go
import "net/smtp"

auth := smtp.PlainAuth("", os.Getenv("SMTP_USER"), os.Getenv("SMTP_PASS"), os.Getenv("SMTP_HOST"))
msg := []byte("To: user@example.com\r\nSubject: Hi\r\n\r\nhello")
_ = smtp.SendMail(
    os.Getenv("SMTP_HOST")+":"+os.Getenv("SMTP_PORT"),
    auth,
    "noreply@app.com", []string{"user@example.com"}, msg,
)
```
**Verdict: Trivial — and `net/smtp` is one of two v4 Tier 1 options.** AWS SES has SMTP endpoint credentials. Locally, mailhog accepts on `mailhog:1025` no-auth. Same env-driven config both sides. (Mechanically Trivial; classified Tier 1 because the *abstraction* — smtp-vs-`aws-sdk-go-v2/service/sesv2` — is what hides cloud↔local without it.)

## SES ↔ mailhog (via SMTP using `gomail`)

```go
import "gopkg.in/gomail.v2"

m := gomail.NewMessage()
m.SetHeader("From", "noreply@app.com")
m.SetHeader("To", "user@example.com")
m.SetHeader("Subject", "Hi")
m.SetBody("text/plain", "hello")
port, _ := strconv.Atoi(os.Getenv("SMTP_PORT"))
d := gomail.NewDialer(os.Getenv("SMTP_HOST"), port, os.Getenv("SMTP_USER"), os.Getenv("SMTP_PASS"))
_ = d.DialAndSend(m)
```
**Verdict: Trivial — and `gomail` is the other v4 Tier 1 option.** Community wrapper handling MIME, attachments, HTML alternatives — closest parallel to TS's `nodemailer` / Python's `smtplib` / Rust's `lettre`. Pick `net/smtp` for minimalism + zero deps; pick `gomail` for MIME/attachments. Both are valid Tier 1 paths.

## Timestream for InfluxDB ↔ influxdb container

```go
import influxdb2 "github.com/influxdata/influxdb-client-go/v2"

client := influxdb2.NewClient(os.Getenv("INFLUX_URL"), os.Getenv("INFLUX_TOKEN"))
defer client.Close()
writeAPI := client.WriteAPIBlocking(os.Getenv("INFLUX_ORG"), "metrics")
p := influxdb2.NewPointWithMeasurement("cpu").AddField("usage", 42.0)
_ = writeAPI.WritePoint(ctx, p)
```
**Verdict: Trivial.** Same `influxdb-client-go`; URL + token swap.

## Verified Permissions ↔ Cedar (native Go)

```go
import (
    cedar "github.com/cedar-policy/cedar-go"
    "github.com/cedar-policy/cedar-go/types"
)

ps, _ := cedar.NewPolicySetFromBytes("policies.cedar", []byte(os.Getenv("CEDAR_POLICIES")))
entities := types.Entities{}
req := types.Request{
    Principal: types.NewEntityUID("User", "alice"),
    Action:    types.NewEntityUID("Action", "view"),
    Resource:  types.NewEntityUID("Doc", "d1"),
}
decision, _ := ps.IsAuthorized(entities, req)
// decision.Decision == types.Allow / types.Deny
```
**Verdict: Trivial.** `cedar-go` is the **official native Go implementation** of Cedar — not a wasm wrapper. Same engine AWS Verified Permissions runs server-side. Cleaner deployment story than TS's `@cedar-policy/cedar-wasm`. Verified Permissions adds storage + lifecycle; the *decision* is identical locally.

## KMS encrypt/decrypt ↔ localstack

```go
import "github.com/aws/aws-sdk-go-v2/service/kms"

client := kms.NewFromConfig(cfg, func(o *kms.Options) {
    if ep := os.Getenv("KMS_ENDPOINT_URL"); ep != "" {
        o.BaseEndpoint = aws.String(ep)
    }
})
out, _ := client.Encrypt(ctx, &kms.EncryptInput{
    KeyId:     aws.String(os.Getenv("KMS_KEY_ID")),
    Plaintext: []byte("secret"),
})
ciphertext := out.CiphertextBlob
```
**Verdict: Trivial for encrypt/decrypt with software keys.** HSM-backed keys + KMS key policies are real-AWS-only — not testable locally.

---

# Moderate — same library, configuration / behavior differs

## "One container, two shapes" — chi (recommended modern default)

Per v4 §3 / §9, Lambda is one `shape:` of the `service` primitive — not a separate primitive. supeux generates `aws_lambda_function` Terraform around the same container image when `shape: function`, or `aws_ecs_service` Terraform when `shape: long-running`. The user's container handles the ABI in framework-idiomatic code.

```go
package main

import (
    "net/http"
    "os"

    "github.com/aws/aws-lambda-go/lambda"
    chiadapter "github.com/awslabs/aws-lambda-go-api-proxy/chi"
    "github.com/go-chi/chi/v5"
)

func main() {
    r := chi.NewRouter()
    r.Get("/hi", func(w http.ResponseWriter, req *http.Request) {
        w.Write([]byte(`{"msg":"hi ` + req.URL.Query().Get("name") + `"}`))
    })
    if os.Getenv("AWS_LAMBDA_RUNTIME_API") != "" {
        lambda.Start(chiadapter.New(r).ProxyWithContext)  // → Lambda container-image entry point
    } else {
        http.ListenAndServe(":8080", r)                    // → Fargate / App Runner / local docker-compose
    }
}
```
**Verdict: Moderate (T0 in v4's tier system).** The seam is one `if` statement; the same container deploys both ways. `aws-lambda-go-api-proxy` is the Go closest analogue to TS's `serverless-http` — a single adapter library with sub-packages per router. supeux's only job is to inject env vars the same way it does for any other service.

## "One container, two shapes" — gin

```go
import (
    "github.com/aws/aws-lambda-go/lambda"
    ginadapter "github.com/awslabs/aws-lambda-go-api-proxy/gin"
    "github.com/gin-gonic/gin"
)

func main() {
    r := gin.Default()
    r.GET("/hi", func(c *gin.Context) {
        c.JSON(200, gin.H{"msg": "hi " + c.Query("name")})
    })
    if os.Getenv("AWS_LAMBDA_RUNTIME_API") != "" {
        lambda.Start(ginadapter.New(r).ProxyWithContext)
    } else {
        _ = r.Run(":8080")
    }
}
```
**Verdict: Moderate.** Biggest hiring pool, most tutorials, slightly opinionated middleware system. Solid conservative pick.

## "One container, two shapes" — echo

```go
import (
    "github.com/aws/aws-lambda-go/lambda"
    echoadapter "github.com/awslabs/aws-lambda-go-api-proxy/echo"
    "github.com/labstack/echo/v4"
)

func main() {
    e := echo.New()
    e.GET("/hi", func(c echo.Context) error {
        return c.JSON(200, map[string]string{"msg": "hi " + c.QueryParam("name")})
    })
    if os.Getenv("AWS_LAMBDA_RUNTIME_API") != "" {
        lambda.Start(echoadapter.New(e).ProxyWithContext)
    } else {
        _ = e.Start(":8080")
    }
}
```
**Verdict: Moderate.** Performant, mature middleware ecosystem, first-party-feeling Lambda story via the adapter. Middle pick between chi (idiomatic minimal) and gin (opinionated convenient).

**Constraints inherited from Lambda regardless of framework**: websockets need API Gateway WebSocket (separate primitive, deferred to v1.1+); streaming responses need Lambda Function URLs with response streaming on; per-cold-start startup runs `init()` funcs + package-level vars. None are supeux concerns — they're Lambda properties.

**Go cold-start advantage**: Go custom-runtime cold-starts of 10–50 ms make Lambda viable for many APIs where Python's 500–1500 ms would push toward Fargate. The cold-start objection that drives Python+Node teams off Lambda barely applies to Go.

## asynq worker — Redis backend (local) vs SQS backend (cloud-leaning)

```go
// worker.go — backend swap at startup
import "github.com/hibiken/asynq"

if os.Getenv("REDIS_URL") != "" {
    opt, _ := asynq.ParseRedisURI(os.Getenv("REDIS_URL"))
    srv := asynq.NewServer(opt, asynq.Config{Concurrency: 10})
    _ = srv.Run(asynq.HandlerFunc(func(ctx context.Context, t *asynq.Task) error {
        return processOrder(t.Payload())
    }))
}
```

For SQS-first patterns, drop asynq and use raw SDK:

```go
import "github.com/aws/aws-sdk-go-v2/service/sqs"

client := sqs.NewFromConfig(cfg, func(o *sqs.Options) {
    if ep := os.Getenv("SQS_ENDPOINT_URL"); ep != "" { o.BaseEndpoint = aws.String(ep) }
})
queueURL := os.Getenv("QUEUE_URL")
for {
    out, _ := client.ReceiveMessage(ctx, &sqs.ReceiveMessageInput{
        QueueUrl: aws.String(queueURL), WaitTimeSeconds: 20, MaxNumberOfMessages: 10,
    })
    for _, m := range out.Messages {
        _ = processOrder([]byte(aws.ToString(m.Body)))
        _, _ = client.DeleteMessage(ctx, &sqs.DeleteMessageInput{
            QueueUrl: aws.String(queueURL), ReceiptHandle: m.ReceiptHandle,
        })
    }
}
```
**Verdict: Moderate.** asynq + Redis is the canonical Go pattern (parallel to BullMQ for TS). asynq doesn't have a battle-tested SQS backend; for production SQS workloads, raw `aws-sdk-go-v2/service/sqs` long-poll is the proven path. Alternatively, `riverqueue/river` (Postgres-backed) avoids the Redis dependency entirely.

## EventBridge Scheduler (cloud) vs `robfig/cron` (local dev)

```go
// scheduler.go — local only; don't run in prod (cron-fires-twice in multi-instance deploys)
import "github.com/robfig/cron/v3"

c := cron.New()
_, _ = c.AddFunc("0 2 * * *", func() {
    http.Post("http://app:8080/jobs/nightly", "application/json", nil)
})
c.Start()
select {}
```
**Verdict: Moderate.** Handler code is the same; only the trigger differs. supeux generates an EventBridge Scheduler rule from a yaml `triggers:` declaration and skips the local-side scheduler container by default — most dev sessions don't need cron firing.

## X-Ray tracing (cloud) vs Jaeger (local) via OpenTelemetry

```go
import (
    "go.opentelemetry.io/otel"
    "go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
    "go.opentelemetry.io/otel/sdk/trace"
)

endpoint := os.Getenv("OTEL_EXPORTER_OTLP_ENDPOINT")
if endpoint == "" { endpoint = "jaeger:4317" }
exporter, _ := otlptracegrpc.New(ctx,
    otlptracegrpc.WithEndpoint(endpoint),
    otlptracegrpc.WithInsecure(),
)
tp := trace.NewTracerProvider(trace.WithBatcher(exporter))
otel.SetTracerProvider(tp)
// AWS:   ADOT collector → X-Ray
// Local: jaeger
```
**Verdict: Moderate.** OpenTelemetry is the abstraction. Code is identical; only the OTLP endpoint and exporter target differ. Strongest observability pattern in Go.

## AppConfig (cloud) vs env vars / file (local)

```go
import "github.com/aws/aws-sdk-go-v2/service/appconfigdata"

func getFeatureFlags(ctx context.Context) (map[string]any, error) {
    if os.Getenv("APPCONFIG_ENDPOINT") != "" {
        c := appconfigdata.NewFromConfig(cfg, func(o *appconfigdata.Options) {
            o.BaseEndpoint = aws.String(os.Getenv("APPCONFIG_ENDPOINT"))
        })
        // ... StartConfigurationSession + GetLatestConfiguration + parse
        return nil, nil
    }
    var ff map[string]any
    _ = json.Unmarshal([]byte(os.Getenv("FEATURE_FLAGS")), &ff)
    return ff, nil
}
```
**Verdict: Moderate.** AppConfig's value is staged rollouts + validators. Locally, env-driven JSON is fine.

---

# Hard — different paradigms, needs a real abstraction

## Cognito (cloud) vs local OIDC issuer — token verification via `golang-jwt` + `keyfunc`

Per v4 §4, this is a Tier 1 pair where mature community libraries already provide the abstraction. The Go idiom is `golang-jwt` for token parsing/verification + `MicahParks/keyfunc` for JWKS fetch/cache — env-driven JWKS URL is the entire seam. No `TokenVerifier` interface with two impls; one code path, two libraries.

```go
import (
    "github.com/MicahParks/keyfunc/v3"
    "github.com/golang-jwt/jwt/v5"
)

jwks, _ := keyfunc.NewDefault([]string{os.Getenv("JWKS_URL")})
// Cloud:  https://cognito-idp.<region>.amazonaws.com/<pool_id>/.well-known/jwks.json
// Local:  http://keycloak:8080/realms/dev/protocol/openid-connect/certs

func verifyToken(raw string) (jwt.MapClaims, error) {
    tok, err := jwt.Parse(raw, jwks.Keyfunc,
        jwt.WithIssuer(os.Getenv("JWT_ISSUER")),
        jwt.WithAudience(os.Getenv("JWT_AUDIENCE")),
    )
    if err != nil || !tok.Valid {
        return nil, err
    }
    return tok.Claims.(jwt.MapClaims), nil
}
```

**Verdict: Hard band → v4 Tier 1.** Cognito's *token issuance* surface (JWKS-served RS256) is a well-defined standard; `golang-jwt` + `keyfunc` hide the cloud↔local difference behind one JWKS URL env var. Cognito's *user lifecycle* (sign-up confirmation, MFA flows, custom attribute admin, hosted UI) has no portable abstraction and stays cloud-only per v4 §8: don't fake admin paths; either skip in local dev or hit real Cognito via mounted creds. **supeux does not ship `TokenVerifier` / `CognitoVerifier` / `LocalJwtVerifier`** — v4 §4 explicitly defers to `golang-jwt` + `keyfunc`.

## API Gateway WebSocket (cloud) vs `gorilla/websocket` (local)

```go
// Cloud (Lambda + APIGW WebSocket)
import (
    "github.com/aws/aws-lambda-go/events"
    "github.com/aws/aws-sdk-go-v2/service/apigatewaymanagementapi"
)

func onConnect(ctx context.Context, e events.APIGatewayWebsocketProxyRequest) (events.APIGatewayProxyResponse, error) {
    // store e.RequestContext.ConnectionID in DynamoDB
    return events.APIGatewayProxyResponse{StatusCode: 200}, nil
}

func onMessage(ctx context.Context, e events.APIGatewayWebsocketProxyRequest) (events.APIGatewayProxyResponse, error) {
    rc := e.RequestContext
    mgmt := apigatewaymanagementapi.NewFromConfig(cfg, func(o *apigatewaymanagementapi.Options) {
        o.BaseEndpoint = aws.String("https://" + rc.DomainName + "/" + rc.Stage)
    })
    _, _ = mgmt.PostToConnection(ctx, &apigatewaymanagementapi.PostToConnectionInput{
        ConnectionId: aws.String(rc.ConnectionID), Data: []byte("hello"),
    })
    return events.APIGatewayProxyResponse{StatusCode: 200}, nil
}

// Local (chi + gorilla/websocket)
import "github.com/gorilla/websocket"

var upgrader = websocket.Upgrader{}
http.HandleFunc("/ws", func(w http.ResponseWriter, r *http.Request) {
    c, _ := upgrader.Upgrade(w, r, nil)
    defer c.Close()
    for {
        _, msg, _ := c.ReadMessage()
        _ = c.WriteMessage(websocket.TextMessage, []byte("echo: "+string(msg)))
    }
})
```
**Verdict: Hard.** API Gateway WebSocket inverts the connection model: connections are *stored* (in DynamoDB), and you push to them via REST (`PostToConnection`). `gorilla/websocket` is stateful per-process. There is no shared abstraction; supeux picks one model and documents the trade-off. For real-time apps, ECS Fargate + `gorilla/websocket` is the saner cloud target.

## Step Functions Standard (cloud) vs asynq/river flows (local)

```go
// Cloud (ASL JSON, deployed via Terraform):
// {"StartAt":"Validate","States":{
//   "Validate":{"Type":"Task","Resource":"arn:aws:lambda:...:validate","Next":"Charge"},
//   "Charge":{"Type":"Task","Resource":"arn:aws:lambda:...:charge","Next":"Notify"},
//   "Notify":{"Type":"Task","Resource":"arn:aws:lambda:...:notify","End":true}}}

// Local (asynq chained tasks — manual; no first-class "workflow" abstraction in Go OSS)
import "github.com/hibiken/asynq"

c := asynq.NewClient(asynq.RedisClientOpt{Addr: "redis:6379"})
// Each handler queues the next task in chain; durability is per-task, not per-workflow
_, _ = c.Enqueue(asynq.NewTask("order:validate", payload))
```
**Verdict: Hard.** Step Functions has durable state, retry policy DSL, parallel branches, human approval steps. Go OSS doesn't have an equivalent workflow engine at parity. Either:
- (a) supeux defines workflows in a DSL and emits ASL for cloud / asynq chain for local, **or**
- (b) supeux only supports workflows on cloud and documents "no local equivalent — test against `aws-stepfunctions-local`."

(b) is the recommendation — synthesizing two backends doubles the surface area for limited benefit.

## SQS + Lambda fan-out (cloud) vs in-process tasks (local)

```go
// Cloud producer
import "github.com/aws/aws-sdk-go-v2/service/sqs"

client := sqs.NewFromConfig(cfg)
_, _ = client.SendMessage(ctx, &sqs.SendMessageInput{
    QueueUrl:    aws.String(os.Getenv("QUEUE_URL")),
    MessageBody: aws.String(`{"order":42}`),
})

// Cloud consumer (Lambda triggered by SQS)
import "github.com/aws/aws-lambda-go/events"

func handler(ctx context.Context, e events.SQSEvent) error {
    for _, r := range e.Records {
        if err := processOrder([]byte(r.Body)); err != nil { return err }
    }
    return nil
}

// Local: goroutine (lossy — dies with the process)
go func() { _ = processOrder(order) }()
```
**Verdict: Hard if you care about local-vs-cloud durability.** Bare goroutines die with the process. supeux's option is to run a local `asynq` (Redis-backed) or `river` (Postgres-backed) worker + ElasticMQ — keeping the *queue* abstraction honest both sides. That's the recommended pattern.

## Bedrock (cloud) vs Ollama (local) — `langchaingo` or `eino` is the abstraction

**Reclassified from Intractable (v1) to Hard / Tier 1 (v4).** v4 §4 names two community options: `langchaingo` (broader provider coverage) or `eino` (newer typed-chain DSL). User picks one; env-driven provider/model selects the backend.

```go
// Option A: langchaingo — broader provider catalog, LangChain port
import (
    "github.com/tmc/langchaingo/llms"
    "github.com/tmc/langchaingo/llms/bedrock"
    "github.com/tmc/langchaingo/llms/ollama"
)

var llm llms.Model
if os.Getenv("LLM_BACKEND") == "bedrock" {
    llm, _ = bedrock.New(bedrock.WithModel(os.Getenv("LLM_MODEL")))
} else {
    llm, _ = ollama.New(ollama.WithModel(os.Getenv("LLM_MODEL")))
}
out, _ := llms.GenerateFromSinglePrompt(ctx, llm, "hi")
//   cloud:  bedrock model = "anthropic.claude-opus-4-7-20260416-v1:0"
//   local:  ollama  model = "llama3.1"
```

```go
// Option B: eino — CloudWeGo/ByteDance, opinionated chain DSL, cleaner types
import (
    "github.com/cloudwego/eino/components/model"
    eb "github.com/cloudwego/eino-ext/components/model/bedrock"
    eo "github.com/cloudwego/eino-ext/components/model/ollama"
)

var m model.ChatModel
if os.Getenv("LLM_BACKEND") == "bedrock" {
    m, _ = eb.NewChatModel(ctx, &eb.ChatModelConfig{Model: os.Getenv("LLM_MODEL")})
} else {
    m, _ = eo.NewChatModel(ctx, &eo.ChatModelConfig{Model: os.Getenv("LLM_MODEL")})
}
```
**Verdict: Moderate plumbing — Hard band → v4 Tier 1.** Both libraries handle per-provider request/response shaping; user code is unchanged across deployments. **supeux does not ship `LLMClient` / `BedrockLLM` / `OllamaLLM`** — that was the v1 prescription; v4 §4 explicitly defers to community libraries. **Output equivalence is not promised** — Claude Opus 4.7 and Llama 3.1 are different models; local tests are plumbing-level, real Bedrock tests are output-quality.

**Decision criterion (langchaingo vs eino)**: pick `langchaingo` for breadth (more providers, LangChain-ecosystem code-porting); pick `eino` for Go-idiomatic typed-graph composition and CloudWeGo's tooling. Both maintained as of 2026.

**Still T2 / cloud-only**: Bedrock **Knowledge Bases**, **Agents**, and **Guardrails**. Neither library bridges these — they are AWS-orchestration services with no OSS equivalent.

## Transcribe (cloud) vs Whisper.cpp Go bindings (local)

```go
// Cloud
import "github.com/aws/aws-sdk-go-v2/service/transcribe"

client := transcribe.NewFromConfig(cfg)
_, _ = client.StartTranscriptionJob(ctx, &transcribe.StartTranscriptionJobInput{
    TranscriptionJobName: aws.String("job-1"),
    Media:                &types.Media{MediaFileUri: aws.String("s3://bucket/audio.mp3")},
    LanguageCode:         types.LanguageCodeEnUs,
})

// Local — Whisper.cpp Go bindings (CGO required)
import whisper "github.com/ggerganov/whisper.cpp/bindings/go/pkg/whisper"

model, _ := whisper.New(os.Getenv("WHISPER_MODEL_PATH"))
ctx_, _ := model.NewContext()
_ = ctx_.Process(audioSamples, nil, nil)
for {
    seg, err := ctx_.NextSegment()
    if err != nil { break }
    fmt.Println(seg.Text)
}
```
**Verdict: Hard band → v4 Tier 1.** Output formats differ between Whisper.cpp and Transcribe; normalize at the boundary in user code. CGO required for Whisper.cpp bindings — won't build into a `FROM scratch` image; use a glibc/musl base.

---

# Intractable — no realistic local equivalent

For these, supeux must mark `cloud_only: true` and refuse to bind locally. Trying to emulate is worse than not — false positives hide bugs.

## SageMaker training / inference

```go
// Cloud
import "github.com/aws/aws-sdk-go-v2/service/sagemakerruntime"

sm := sagemakerruntime.NewFromConfig(cfg)
resp, _ := sm.InvokeEndpoint(ctx, &sagemakerruntime.InvokeEndpointInput{
    EndpointName: aws.String(os.Getenv("SAGEMAKER_ENDPOINT")),
    Body:         payload,
})

// Local "equivalent" — run the model in a Go container that mimics SageMaker contract
// (paths under /opt/ml/...). Behavior is approximate. Most teams cross-language to Python.
```
**Verdict: Intractable (T2) for the SageMaker-managed surface.** If your inference is a Go container behind an endpoint (e.g., using `onnxruntime_go`), the *model serving* portion is straightforwardly portable; the SageMaker-platform features (auto-scaling endpoint variants, A/B traffic splits, model monitoring) are not. Treat the platform as cloud-only.

## CloudFront / Lambda@Edge / Global Accelerator / CloudFront Functions

CloudFront Functions / Lambda@Edge are **Node-only** at the edge runtime — no Go support, no `aws-sdk-go-v2`. Test routing logic in isolation as a pure function in any language; trust the CDN to invoke it correctly. No emulation worth maintaining.

**Verdict: Intractable.** Edge runtime properties + AWS-internal mechanics. Document; don't fake.

## S3 Express One Zone, S3 Vectors, Aurora DSQL, DAX, Neptune Analytics, IAM enforcement

**Verdict: Intractable.** Each has properties (single-AZ ultra-low-latency, ANN-on-S3, multi-region active-active SQL, microsecond DDB cache, in-memory graph analytics, real IAM evaluation) that require AWS to demonstrate. Document; don't fake.

## CloudWatch Synthetics / RUM / Application Signals, IoT Device Defender / Analytics / SiteWise / TwinMaker / FleetWise

**Verdict: Intractable.** Observability or domain-specific products built around AWS-managed data flows. For local dev: skip and use OTel + raw logs.

## Step Functions Distributed Map, SNS Mobile Push, Forecast / Personalize, SageMaker JumpStart / Canvas

**Verdict: Intractable.** Parallel-children behavior / real APNs+FCM dispatch / managed-model marketplace / no-code UI — all AWS-internal.

---

# Per-group difficulty summary

| Group | AWS service | Local pair | Difficulty | v4 tier |
|---|---|---|---|---|
| Compute — Function | Lambda (container-image, `shape: function`) | same container, chi/gin/echo Lambda adapter | Moderate | **T0** (one container, two shapes — v4 §3) |
| Compute — Container | ECS/Fargate/App Runner | docker-compose | Trivial | **T0** |
| Compute — VM | EC2 | docker container | N/A (don't abstract) | n/a |
| Storage — Object | S3 | minio | **Trivial** | **T0** |
| Storage — Object | S3 Express One Zone | (none) | Intractable | **T2** (engine-swap → MinIO) |
| Storage — Object | S3 Vectors | (none) | Intractable | **T2** (engine-swap → custom HNSW / pgvector) |
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
| Messaging — Queue | Amazon MQ RabbitMQ | rabbitmq (`amqp091-go`) | **Trivial** | **T0** |
| Messaging — PubSub | SNS | localstack | **Trivial** | **T0** |
| Messaging — Event Bus | EventBridge default | localstack (partial) | Moderate | T0 with caveats |
| Messaging — Stream | Kinesis | localstack | **Trivial** producer; Moderate consumer (Go KCL port exists but coordination is local-hostile) | T0 / T1 |
| Messaging — Stream | MSK | kafka (`kafka-go`/`confluent-kafka-go`) | **Trivial** for plaintext/SASL_SSL; **Trivial for IAM too** (Go signer is mature) | T0 |
| API edge | API Gateway HTTP | chi/gin/echo via Lambda adapter | Moderate | T0 (deferred to v1.3 per v4 §10) |
| API edge | API Gateway WebSocket | `gorilla/websocket` | **Hard** | **T2** in v4 (`cloud_only`) |
| API edge | AppSync | `gqlgen` / `graphql-go` | Hard / Intractable | T2 |
| API edge | ALB | run container behind nginx | N/A (don't abstract) | n/a (auto-derived from `service expose:`) |
| CDN | CloudFront | (none) | Intractable | **T2** (skip pattern in local; `static_site` primitive in v1.2) |
| CDN | Lambda@Edge / CloudFront Functions | (none — Node-only edge) | Intractable | **T2** |
| DNS | Route 53 | /etc/hosts / coredns | Partial | T1 |
| Auth | Cognito (token verify) | local OIDC issuer | **Hard** in band; **T1 via `golang-jwt` + `keyfunc`** | **T1** |
| Auth | Cognito (user lifecycle) | (none) | Intractable | **T2** in v4 |
| Auth | IAM | (none; LocalStack stubs) | Intractable enforcement | **T2** |
| Auth | Verified Permissions (Cedar) | `cedar-go` (native Go) | Trivial | **T0** |
| Secrets | Secrets Manager | localstack | **Trivial** | **T0** |
| Secrets | SSM Parameter Store | localstack | **Trivial** | **T0** |
| Secrets | KMS | localstack | Trivial (software keys only) | T0 |
| Workflow | Step Functions Standard (single-service) | aws-stepfunctions-local | **Trivial** within ASL | T0 |
| Workflow | Step Functions Standard (multi-service workflows) | (partial) | Hard | **T2** in v4 |
| Workflow | Step Functions Express | (partial local) | Hard | T2 |
| Workflow | EventBridge Scheduler | robfig/cron | Moderate | T0 (in v4 `cron` is a trigger attribute, not a primitive) |
| Workflow | MWAA | apache/airflow (Python-shaped) | Cross-language | T0 once orchestrated |
| Email | SES | mailhog (SMTP via **`net/smtp`** or **`gomail`**) | **Trivial** | **T1 — `net/smtp` or `gomail`** is the abstraction |
| Email | SNS SMS | (none — inspect-only) | Intractable | T2 |
| Email | SNS Mobile Push | (none) | Intractable | **T2** |
| Observability | CloudWatch Logs | stdout / docker logs (`log/slog`) | **Trivial** | **T0** |
| Observability | CloudWatch Metrics | EMF via custom emitter | Moderate | T0 |
| Observability | X-Ray | jaeger via OTel | Moderate | T0 (OTel is the abstraction) |
| Observability | RUM / Synthetics / AppSignals | (none) | Intractable | T2 |
| Analytics — Warehouse | Redshift | clickhouse / postgres | Partial | T1 |
| Analytics — Query | Athena | trino (`trino-go-client`) | Partial | T1 |
| Analytics — ETL | Glue | spark container (Python-shaped) | Cross-language | T1 |
| Analytics — Big-data | EMR | spark container | Cross-language | T1 |
| ML — Training | SageMaker training | Python script | Cross-language | T1 |
| ML — Inference (model-as-container) | SageMaker endpoint | Go + `onnxruntime_go` | Moderate | T0 once containerized |
| ML — LLM | Bedrock | ollama via **`langchaingo`** or **`eino`** | Hard band; **T1 via `langchaingo` or `eino`** | **T1** |
| ML — LLM orchestration | Bedrock KB / Agents / Guardrails | (none) | Intractable | **T2** |
| ML — Vision | Rekognition / Textract | `onnxruntime_go` / `gosseract` (CGO) | Partial — outputs differ | **T1** |
| ML — Speech STT | Transcribe | **`whisper.cpp/bindings/go`** (CGO) | Partial — outputs differ | **T1** |
| ML — Speech TTS | Polly | (no first-class Go TTS) | Cross-language | T1 |
| IoT — Gateway | IoT Core MQTT | mosquitto (`paho.mqtt.golang`) | **Trivial** wire; Moderate auth | T0 wire |
| IoT — Edge | Greengrass | greengrass-runtime (Go components supported) | Trivial | T0 |
| IoT — Analytics | IoT Analytics / SiteWise / etc | (none) | Intractable | **T2** |

**Headcount (per v4's tier semantics)**:
- **T0**: ~22 pairs — env-var swap is enough; no abstraction library required. supeux's bread and butter.
- **T1**: ~5 pairs — community libraries cover them (**`langchaingo`** *or* **`eino`**, **`golang-jwt` + `keyfunc`**, **`net/smtp`** *or* **`gomail`**, **`whisper.cpp/bindings/go`**, optionally an interface around vision SDKs). supeux **does not ship** a runtime adapter library; v4 §4 documents which library per pair.
- **T2**: ~15 pairs — `cloud_only:` in the IR. User picks one of v4 §4's four patterns: skip / hit-real / engine-swap / stub.

The remaining ~12 entries are Moderate-band T0s where a small adapter (framework Lambda wrapper, OTel exporter env var, asynq backend swap) closes the gap without needing a community library.

**vs Python (~22 T0 / ~5 T1 / ~15 T2), Rust (~18 T0 / ~3 T1 / ~18 T2), and TypeScript (~22 T0 / ~5 T1 / ~15 T2)**: Go headcount sits at parity with Python and TS. A few cells differ at the margins, almost all in Go's favor:
- **MSK with IAM** is cleaner in Go than TS (`kafkajs` SigV4 gap) and on par with Java — `aws/aws-msk-iam-sasl-signer-go` is mature.
- **Verified Permissions** is cleaner in Go than TS — `cedar-go` is a native Go implementation, not a wasm wrapper.
- **Lambda cold-start** of 10–50 ms is the **lowest of the four first-class languages**, which expands the set of services for which Lambda is the right shape.
- **Static binaries** (`CGO_ENABLED=0` + `FROM scratch`) yield the **smallest container images** (~5–20 MB), the smallest of the four.
- A few cells are **less mature** in Go: TTS has no first-class library (cross-language to Python); CGO-requiring ML libs (`gocv`, `gosseract`, `whisper.cpp` bindings) break the static-binary story and force a glibc/musl base. Same gap as TS for TTS.
- For LLM abstraction, Go has **two named options** (`langchaingo` + `eino`) where the other three languages have one each — `langchaingo` is the broader-coverage analogue to `litellm` / Vercel AI SDK; `eino` is newer with a Go-idiomatic typed-chain DSL. v4 §4 names both.
- For email, Go has **two named options** (`net/smtp` + `gomail`) where TS/Python/Rust have one each — `net/smtp` (stdlib) is the most idiomatic-Go path; `gomail` parallels the others. v4 §4 names both.

Net result: Go is a first-class target for the *containers-first* abstraction shape, with the **best Lambda cold-start story, smallest images, cleanest MSK-IAM integration, and a native Cedar implementation** of the four languages. The two-option Tier 1 surface for LLM and email is the only ergonomic divergence from the one-canonical-library pattern in TS/Python/Rust — and v4 documents both.

See `supeux_abstraction_v4.md` for how these tiers translate into v1 PoC scope, IR primitives, and the yaml switch shape. Conceptual home: `thesis.md`.
