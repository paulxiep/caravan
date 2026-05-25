# AWS → Go Stack Mapping & Emulation Quality

> ⚠️ **HISTORICAL — pre-SDK research notes; Go SDK is namespace-reserved only.** Current SDK namespace at [`../rpc/go/`](../rpc/go/) is a 0.0.1 placeholder; Go is out of PoC scope per [`development_plan.md`](development_plan.md). (Note: the compiler itself is written in Go, but caravan-rpc-go is not a PoC deliverable.) Snapshot 2026-05-16; do not assume any specific row is current.

> **Snapshot date: 2026-05-16.** References `aws_service_groups.md` for AWS-side detail and `mapping_go_to_aws.md` for the reverse direction.
> **Scope**: Go ecosystem (Go 1.22+). "Wire-compatible" means *the official `aws-sdk-go-v2/service/*` package (or relevant driver) talks to a local container via a `BaseEndpoint` setting or DSN swap without code changes*. Python, Rust, and TypeScript mirrors live in `mapping_aws_to_python.md` / `mapping_aws_to_rust.md` / `mapping_aws_to_typescript.md`.
> **Framing**: Go ecosystem evidence feeding into `thesis.md` (conceptual home) and `caravan_abstraction_v4.md` (long-form derivation; supersedes v3). The emulation-quality bands below are **orthogonal to v4's T0/T1/T2 service tiers** — see the note after the bands table.

This file answers: *"I picked an AWS service. What container do I run alongside my Go app so the same code talks to it without knowing the difference?"*

## Emulation-quality bands

| Band | Meaning |
|---|---|
| **wire-compatible** | Same Go package (`aws-sdk-go-v2/service/*` or driver) talks to local container via env-driven `BaseEndpoint` / DSN. Behavior matches production for ~95% of common operations. |
| **behavior-compatible** | Same Go package, different connection setup. The engine is real (real Postgres, real Redis) so behavior is honest, but the AWS-specific bits (IAM, snapshots, performance insights) are absent. |
| **partial** | Local container speaks the same wire protocol but lacks features. Most happy-path code works; specific operations error or return wrong shapes. |
| **none viable** | No local container meaningfully reproduces the AWS service's behavior. Either abstract behind a community library at your code boundary (v4 Tier 1), or test against AWS directly. |

Two local-container columns per service:
- **OSS option**: the engine itself (e.g., `postgres:16`, `redis:7`, `minio/minio`).
- **LocalStack option**: `localstack/localstack` (Community = free; Pro = paid). Where Community covers the service it's listed; Pro-only services are flagged.

**The Go idiom for cloud↔local switching** is the AWS SDK Go v2 per-client `BaseEndpoint` option — every `aws-sdk-go-v2/service/*` `NewFromConfig` accepts an `Options` func to override the endpoint:

```go
import (
    "context"
    "os"
    "github.com/aws/aws-sdk-go-v2/aws"
    "github.com/aws/aws-sdk-go-v2/config"
    "github.com/aws/aws-sdk-go-v2/service/s3"
)

cfg, _ := config.LoadDefaultConfig(context.TODO())
client := s3.NewFromConfig(cfg, func(o *s3.Options) {
    if ep := os.Getenv("S3_ENDPOINT_URL"); ep != "" {
        o.BaseEndpoint = aws.String(ep)
        o.UsePathStyle = true
    }
})
```

`BaseEndpoint` shipped in `aws-sdk-go-v2` v1.16 (~2023) and replaced the older `EndpointResolverWithOptions` / `EndpointResolverV2` machinery for the common case. Pattern is the same shape as Python's `endpoint_url=` kwarg and TS's `endpoint` option — one field on the per-client `Options`, no global resolver.

**Runtime note**: snippets assume **Go 1.22+** unless otherwise stated. Lambda Go support uses the `provided.al2023` custom-runtime base image with `aws-lambda-go` (the official ABI library); cold-starts are ~10–50 ms — the fastest of the four first-class languages. Static binaries via `CGO_ENABLED=0 go build` are the default; CGO-requiring deps (`gocv` for OpenCV, `mattn/go-sqlite3`) need a glibc/musl base instead of scratch.

### Emulation quality vs v4 service tier

The two axes describe different things:

| | Same wire API? | Local emulator faithful? |
|---|---|---|
| **Emulation quality** | not measured here directly | wire-compatible / behavior-compatible / partial / none viable |
| **v4 T0/T1/T2 tier** | T0 = yes (env-var swap is enough); T1 = no (need a community library to bridge); T2 = no AND no OSS engine | not measured |

Loose correspondence: most **wire-compatible** + most **behavior-compatible** entries below are **T0**. **partial** entries split — some are still T0 with a few caveats (DocumentDB ≈ Mongo for happy paths), others become **T1** when a community library (`langchaingo` / `eino` for Bedrock, `golang-jwt` + `MicahParks/keyfunc` for Cognito token-verify, `net/smtp` / `gomail` for SES) is what unifies the code. **none viable** is always **T2** — caravan marks `cloud_only:` and the user picks one of v4 §4's four patterns (skip / hit-real / engine-swap / stub).

---

# Web stack core

## 1. Compute — Function (Lambda)

| AWS | OSS option | LocalStack | Quality | Notes |
|---|---|---|---|---|
| Lambda (Go custom runtime, `provided.al2023`) | `public.ecr.aws/lambda/provided:al2023` base image | `localstack` Community — Lambda v2 | partial | Go is compiled to a single static binary placed at `/var/runtime/bootstrap`. LocalStack invokes via local Docker. IAM, layers, VPC ENIs stubbed; cold-start timing differs. Good for happy-path; do NOT rely on for performance or timeout testing. |
| Lambda SnapStart | none | none | none viable | Go cold-starts are already 10–50 ms; SnapStart helps the JVM, not Go. AWS-internal snapshot mechanics anyway. |
| Lambda@Edge | none | none | none viable | CDN-edge invocation has no local counterpart. Edge runtime is Node-only — no Go support at the edge. |
| Lambda Function URL | run handler under chi / gin / echo Lambda adapter | `localstack` Lambda | partial | Function URL itself doesn't matter locally; just invoke the handler. |

**Go idiom for local dev**: per v4 §3 / §9, Lambda is one `shape:` of the `service` primitive, not a separate primitive. Containers-first means the same image deploys two ways — wrap your chi/gin/echo router with `aws-lambda-go-api-proxy` and branch on the `AWS_LAMBDA_RUNTIME_API` env var (present only inside Lambda) to switch between "register a handler" and "listen on a port".

Three canonical adapter idioms (all three covered in the api_diffs file):

```go
// chi — idiomatic, minimal, closest analogue to Rust's lambda_http
package main

import (
    "net/http"
    "os"

    "github.com/aws/aws-lambda-go/lambda"
    "github.com/awslabs/aws-lambda-go-api-proxy/chi"
    chiRouter "github.com/go-chi/chi/v5"
)

func main() {
    r := chiRouter.NewRouter()
    r.Get("/hi", func(w http.ResponseWriter, _ *http.Request) {
        w.Write([]byte(`{"msg":"hi"}`))
    })
    if os.Getenv("AWS_LAMBDA_RUNTIME_API") != "" {
        lambda.Start(chiadapter.New(r).ProxyWithContext)
    } else {
        http.ListenAndServe(":8080", r)
    }
}
```

```go
// gin — biggest hiring pool, conservative pick
import (
    "github.com/aws/aws-lambda-go/lambda"
    "github.com/awslabs/aws-lambda-go-api-proxy/gin"
    "github.com/gin-gonic/gin"
)

func main() {
    r := gin.Default()
    r.GET("/hi", func(c *gin.Context) { c.JSON(200, gin.H{"msg": "hi"}) })
    if os.Getenv("AWS_LAMBDA_RUNTIME_API") != "" {
        lambda.Start(ginadapter.New(r).ProxyWithContext)
    } else {
        r.Run(":8080")
    }
}
```

```go
// echo — performant middle ground, mature middleware ecosystem
import (
    "github.com/aws/aws-lambda-go/lambda"
    "github.com/awslabs/aws-lambda-go-api-proxy/echo"
    "github.com/labstack/echo/v4"
)

func main() {
    e := echo.New()
    e.GET("/hi", func(c echo.Context) error { return c.JSON(200, map[string]string{"msg": "hi"}) })
    if os.Getenv("AWS_LAMBDA_RUNTIME_API") != "" {
        lambda.Start(echoadapter.New(e).ProxyWithContext)
    } else {
        e.Start(":8080")
    }
}
```

Same container image, two `shape:` values; caravan generates `aws_lambda_function` Terraform vs `aws_ecs_service` Terraform around the same image. The user wraps the handler ABI in `aws-lambda-go-api-proxy` — that wrapper is user code, not caravan code. `aws-lambda-go-api-proxy` is the closest Go parallel to TS's `serverless-http`: a single adapter library with sub-packages for the major routers (chi, gin, echo, fiber, gorilla/mux, `net/http`).

---

## 2. Compute — Container

| AWS | OSS option | LocalStack | Quality | Notes |
|---|---|---|---|---|
| ECS / EKS / Fargate / App Runner | `docker compose up` | `localstack` Pro — ECS | wire-compatible (run-the-container sense) | The container itself is identical; only the orchestrator differs. A Go multi-stage build from `golang:1.22-alpine` to `gcr.io/distroless/static-debian12` (or `scratch` with `CGO_ENABLED=0`) is ~5–20 MB — by far the smallest of the four first-class languages. |

**Go idiom**: container image is the unit of portability. For local AWS creds, the default `config.LoadDefaultConfig` chain reads from `~/.aws/credentials` mounted into the container (`-v ~/.aws:/root/.aws:ro`). No code changes between local and AWS for the container itself. Static binaries make scratch / distroless work — `FROM scratch` is genuinely viable for Go services in a way it isn't for Node / Python / Rust-with-glibc.

---

## 3. Compute — VM / Batch

| AWS | OSS option | LocalStack | Quality | Notes |
|---|---|---|---|---|
| EC2 | docker container approximation | `localstack` Community — EC2 API only | partial | Local EC2 is a category mistake — emulate the *workload*, not the VM. |
| AWS Batch | docker-compose service + a queue | `localstack` Pro — Batch | partial | Batch's job dispatching is mocked. Hand-roll a local job queue (`asynq` or `river`) if you need parity. |
| Lightsail | docker container | `localstack` Pro — Lightsail | partial | Same as EC2. |

---

## 4. Storage — Object (S3)

| AWS | OSS option | LocalStack | Quality | Notes |
|---|---|---|---|---|
| S3 Standard / IA / Glacier | `minio/minio` or `adobe/s3mock` | `localstack` Community — S3 | **wire-compatible** | `aws-sdk-go-v2/service/s3` with `o.BaseEndpoint` + `o.UsePathStyle = true` works. Storage classes are no-ops on minio. |
| S3 Intelligent-Tiering | minio | localstack | partial | Tiering behavior is mocked / absent. |
| S3 Express One Zone | none | none | none viable | Directory-bucket semantics + 10× perf are AWS-specific. |
| S3 lifecycle / replication | minio (has its own lifecycle DSL) | localstack | partial | Different DSL on minio. |
| S3 Object Lambda | none | partial | partial | LocalStack stubs the API; can't reproduce edge-routing. |

**Go idiom**:
```go
import (
    "github.com/aws/aws-sdk-go-v2/aws"
    "github.com/aws/aws-sdk-go-v2/config"
    "github.com/aws/aws-sdk-go-v2/service/s3"
)

cfg, _ := config.LoadDefaultConfig(ctx)
client := s3.NewFromConfig(cfg, func(o *s3.Options) {
    if ep := os.Getenv("S3_ENDPOINT_URL"); ep != "" {
        o.BaseEndpoint = aws.String(ep)
        o.UsePathStyle = true
    }
})
_, _ = client.PutObject(ctx, &s3.PutObjectInput{
    Bucket: aws.String("my-bucket"),
    Key:    aws.String("hello.txt"),
    Body:   strings.NewReader("hi"),
})
```
This is the gold-standard cloud↔local pattern in Go, same as boto3's. `UsePathStyle = true` is the minio-specific gotcha (same as Rust's `force_path_style(true)` and TS's `forcePathStyle: true`).

---

## 5. Storage — File

| AWS | OSS option | LocalStack | Quality | Notes |
|---|---|---|---|---|
| EFS | docker volume + `nfs-ganesha` for true NFS, or bind-mount | none | partial | `os.Open` / `os.ReadFile` work transparently against bind-mounted volumes. NFS-specific lock contention won't show up locally. |
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
| RDS Postgres | `postgres:16` | `localstack` Pro — RDS API | **behavior-compatible** | Same drivers: `database/sql` + `lib/pq` (canonical pure-Go), `jackc/pgx/v5` (modern, faster, native interface or `database/sql` adapter via `stdlib`). ORMs: `gorm`, `ent`, `bun`. Codegen: `sqlc`. DSN swap. |
| Aurora Postgres / Aurora Serverless v2 | `postgres:16` | localstack Pro | behavior-compatible | Aurora-specific features (read replicas, ACU scaling, Optimized Reads) are AWS-only and irrelevant for local correctness tests. |
| RDS MySQL / Aurora MySQL | `mysql:8` | localstack Pro | behavior-compatible | Drivers: `go-sql-driver/mysql` (canonical), `gorm`, `ent`, `bun`. |
| RDS MariaDB | `mariadb:11` | localstack Pro | behavior-compatible | Same. |
| RDS for SQL Server | `mcr.microsoft.com/mssql/server:2022-latest` | localstack Pro | behavior-compatible | `microsoft/go-mssqldb`. License limitations on local image. |
| RDS for Oracle | `gvenzl/oracle-xe:21` | localstack Pro | partial | `godror/godror` requires Oracle Instant Client (CGO); awkward toolchain. Pure-Go alternative: `sijms/go-ora` (community). |
| Aurora DSQL (multi-region) | none | none | none viable | Active-active is a coordination problem; no local equivalent. |

**Go idiom**:
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
Same drivers, DSN swap. Cleanest pattern after S3. The `database/sql` interface keeps the door open to swap to `lib/pq` or any other driver; `pgx.Connect(ctx, dsn)` directly is faster and exposes Postgres-native features (`LISTEN/NOTIFY`, `COPY`, batch protocol) but locks the call sites to pgx.

**Go-specific gotcha (`sqlc`)**: `sqlc generate` reads schema files and emits typed query funcs from `.sql` files — parallel to Rust's `sqlx::query!` macro. Needs the schema definitions present in the repo (not a live DB); CI runs `sqlc generate` after migrations are added to the schema files. Failure mode: schema files out of sync with prod migration history.

**Go-specific gotcha (`gorm` auto-migration)**: `db.AutoMigrate(&User{})` works locally but is frowned-upon in production — explicit migration tools (`golang-migrate/migrate`, `pressly/goose`, `atlasgo/atlas`) are the path. The dual-mode trap: `AutoMigrate` silently diverges from a managed migration history.

---

## 8. Database — KV / Document NoSQL

| AWS | OSS option | LocalStack | Quality | Notes |
|---|---|---|---|---|
| DynamoDB | `amazon/dynamodb-local` (official) | `localstack` Community — DynamoDB | **wire-compatible** | `aws-sdk-go-v2/service/dynamodb` + `feature/dynamodb/attributevalue` (marshal/unmarshal helpers, recommended) with `o.BaseEndpoint`. Streams partial; transactions + conditional expressions solid. |
| DynamoDB Global Tables | dynamodb-local × 2 with manual replication | none | partial | Replication is the whole point and doesn't exist locally. |
| DAX | none | none | none viable | DAX-specific client; no OSS equivalent. Test against vanilla DynamoDB locally. |
| DocumentDB | `mongo:7` | localstack Pro | partial | `mongo-driver` (official). DocumentDB ≠ real Mongo; testing against real Mongo will reveal *false positives*. |
| Keyspaces | `cassandra:5` | none | partial | `gocql/gocql` (canonical); Cassandra ≠ Keyspaces in some ops. |

**Go idiom**:
```go
import (
    "github.com/aws/aws-sdk-go-v2/aws"
    "github.com/aws/aws-sdk-go-v2/feature/dynamodb/attributevalue"
    "github.com/aws/aws-sdk-go-v2/service/dynamodb"
    "github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
)

client := dynamodb.NewFromConfig(cfg, func(o *dynamodb.Options) {
    if ep := os.Getenv("DYNAMODB_ENDPOINT_URL"); ep != "" {
        o.BaseEndpoint = aws.String(ep)
    }
})

type User struct {
    PK, SK, Name string
}
item, _ := attributevalue.MarshalMap(User{PK: "u#1", SK: "profile", Name: "Alice"})
_, _ = client.PutItem(ctx, &dynamodb.PutItemInput{
    TableName: aws.String("items"),
    Item:      item,
})
```
`attributevalue.MarshalMap` removes the raw `map[string]types.AttributeValue{"S": "..."}` wrapping — closest to TS's `@aws-sdk/lib-dynamodb` Document client and boto3's `resource("dynamodb")` ergonomics.

---

## 9. Database — Cache

| AWS | OSS option | LocalStack | Quality | Notes |
|---|---|---|---|---|
| ElastiCache Redis (all flavors) | `redis:7-alpine` | localstack Pro | **behavior-compatible** | `redis/go-redis/v9` (canonical, the production default). DSN swap via `redis.ParseURL`. |
| ElastiCache Serverless | `redis:7-alpine` | localstack Pro | behavior-compatible | Serverless behavior invisible to client. |
| ElastiCache Memcached | `memcached:1.6` | localstack Pro | behavior-compatible | `bradfitz/gomemcache` (Brad Fitzpatrick's canonical client; passive maintenance but stable). |
| MemoryDB for Redis | `redis:7-alpine` | none | behavior-compatible | MemoryDB's durability doesn't reproduce locally. |

**Go idiom**:
```go
import "github.com/redis/go-redis/v9"

opt, _ := redis.ParseURL(os.Getenv("REDIS_URL"))  // redis://master.cache-cluster.xyz... or redis://redis:6379/0
rdb := redis.NewClient(opt)
_ = rdb.Set(ctx, "session:abc", "user-123", time.Hour).Err()
```
Cluster-mode-enabled ElastiCache: use `redis.NewClusterClient(&redis.ClusterOptions{Addrs: []string{...}})` instead — different constructor; if you depend on cluster mode, run `bitnami/redis-cluster` locally.

---

## 10. Database — Time-series / Graph

| AWS | OSS option | LocalStack | Quality | Notes |
|---|---|---|---|---|
| Timestream for LiveAnalytics | `influxdb:2` (closest behavior) | none | none viable | Timestream's SQL surface and tiering are proprietary. |
| Timestream for InfluxDB | `influxdb:2` | none | wire-compatible | `influxdata/influxdb-client-go/v2`. URL + token swap. |
| Neptune | `tinkerpop/gremlin-server` (Gremlin) or `neo4j` (Cypher path) | none | partial | `apache/tinkerpop/gremlin-go/v3` (Gremlin) or `neo4j/neo4j-go-driver/v5`. Neptune supports Gremlin + SPARQL + openCypher; pick one locally. |
| Neptune Analytics | none | none | none viable | In-memory engine is AWS-only. |

---

## 11. Database — Search / Vector

| AWS | OSS option | LocalStack | Quality | Notes |
|---|---|---|---|---|
| OpenSearch Service | `opensearchproject/opensearch:2` | localstack Pro | **behavior-compatible** | `opensearch-project/opensearch-go/v3` (official). URL swap. Auto-tuning + UltraWarm are AWS-only. |
| OpenSearch Serverless | `opensearchproject/opensearch:2` | none | behavior-compatible | Auto-scale invisible to code; OCU billing model gone. |
| OpenSearch k-NN | opensearch image with knn plugin (built-in 2.x) | none | behavior-compatible | Same. |
| Aurora pgvector | `pgvector/pgvector:pg16` | none | wire-compatible | `pgvector/pgvector-go` integrates with `pgx` / `database/sql` / `gorm`. Same SQL. |
| S3 Vectors | minio + manual ANN index (e.g. `viterin/vek` + custom HNSW) | none | none viable | S3 Vectors API is proprietary; no community shim. |
| Kendra | none | none | none viable | Bag-of-features (NLP search, doc connectors, FAQs) too proprietary. |

---

## 12. Messaging — Queue

| AWS | OSS option | LocalStack | Quality | Notes |
|---|---|---|---|---|
| SQS Standard | `softwaremill/elasticmq-native` | `localstack` Community — SQS | **wire-compatible** | `aws-sdk-go-v2/service/sqs` with `o.BaseEndpoint`. Long-poll, DLQ wiring, FIFO dedup all work. |
| SQS FIFO | ElasticMQ supports it | localstack | wire-compatible | Dedup/deduplication-id semantics replicated; check edge cases. |
| Amazon MQ — RabbitMQ | `rabbitmq:3-management` | none | **behavior-compatible** | `rabbitmq/amqp091-go` (canonical, the official maintained fork of streadway/amqp). Real RabbitMQ. |
| Amazon MQ — ActiveMQ | `apache/activemq-classic` | none | behavior-compatible | `go-stomp/stomp/v3` (STOMP). Mature-but-niche. |

**Go idiom**:
```go
import (
    "github.com/aws/aws-sdk-go-v2/aws"
    "github.com/aws/aws-sdk-go-v2/service/sqs"
)

client := sqs.NewFromConfig(cfg, func(o *sqs.Options) {
    if ep := os.Getenv("SQS_ENDPOINT_URL"); ep != "" {
        o.BaseEndpoint = aws.String(ep)
    }
})
_, _ = client.SendMessage(ctx, &sqs.SendMessageInput{
    QueueUrl:    aws.String(os.Getenv("QUEUE_URL")),
    MessageBody: aws.String("job#42"),
})
```

---

## 13. Messaging — Pub/Sub & Event Bus

| AWS | OSS option | LocalStack | Quality | Notes |
|---|---|---|---|---|
| SNS Standard topic | `localstack` Community — SNS (no good standalone OSS) | localstack | wire-compatible | `aws-sdk-go-v2/service/sns`. LocalStack's SNS is solid and well-tested. |
| SNS FIFO topic | localstack | localstack | partial | FIFO semantics partially implemented. |
| SNS Mobile Push (APNs/FCM) | none | localstack Pro | none viable | Real APNs/FCM destination needed for behavior parity. |
| EventBridge default bus | localstack Community — EventBridge | localstack | partial | `aws-sdk-go-v2/service/eventbridge`. Rules + targets work for happy paths. Schema registry, archive, replay partial. |
| EventBridge Pipes | localstack Pro | none | partial | Filter + target wiring stubbed. |
| EventBridge Scheduler | localstack Pro | localstack Pro | partial | `aws-sdk-go-v2/service/scheduler`. Schedules fire; precision and group limits differ. |

---

## 14. Messaging — Streaming

| AWS | OSS option | LocalStack | Quality | Notes |
|---|---|---|---|---|
| Kinesis Data Streams | `localstack` Community — Kinesis | localstack | **wire-compatible** | `aws-sdk-go-v2/service/kinesis` with `o.BaseEndpoint`. KCL has an official Go port (`aws/amazon-kinesis-client-go`) but consumer-coordination behavior is hard to test locally. |
| Kinesis Firehose | localstack Pro | localstack Pro | partial | Destination delivery (S3/Redshift/OpenSearch) needs separate emulators wired together. |
| MSK | `bitnami/kafka:3.7` (real Kafka) | none | **behavior-compatible** | `segmentio/kafka-go` (preferred pure-Go) or `confluentinc/confluent-kafka-go/v2` (librdkafka FFI). Bootstrap-server env swap. |
| MSK Serverless | `bitnami/kafka:3.7` | none | behavior-compatible | Serverless invisible to client. |
| MSK Connect | run Kafka Connect container | none | behavior-compatible | Real Kafka Connect; AWS-managed lifecycle absent. |

**Go advantage (MSK-IAM)**: `aws/aws-msk-iam-sasl-signer-go` is mature and battle-tested — Go's MSK-IAM story is cleaner than TS's (`kafkajs` doesn't natively sign SigV4; needs `confluent-kafka-javascript` FFI) and on par with Java's. Both `segmentio/kafka-go` and `confluentinc/confluent-kafka-go` integrate cleanly with the IAM signer.

---

## 15. API / Web Edge

| AWS | OSS option | LocalStack | Quality | Notes |
|---|---|---|---|---|
| API Gateway REST | `localstack` Community — API Gateway | localstack | partial | Resources/methods/integrations stubbed. Many request/response transformation features absent. |
| API Gateway HTTP | localstack | localstack | partial | Use chi/gin/echo + `aws-lambda-go-api-proxy` to share handler code between Lambda+APIGW HTTP and a standalone server. |
| API Gateway WebSocket | localstack Pro | localstack Pro | partial | Stateful-connection model is APIGW-specific (connections stored in DDB; push via `aws-sdk-go-v2/service/apigatewaymanagementapi`). `gorilla/websocket` per-process model is different. |
| AppSync (GraphQL) | localstack Pro | localstack Pro | partial | Server-side: `99designs/gqlgen` (codegen-first, the canonical Go GraphQL server) or `graphql-go/graphql`. AppSync resolvers + subscriptions need adapters. |
| ALB / NLB / GWLB | `nginx` or `traefik` for L7; `haproxy` for L4 | localstack Pro | partial | LB itself doesn't matter; route the traffic to your container directly. |

**Go idiom**: don't emulate the LB. Run your chi/gin/echo container on a port and `curl` it directly.

---

## 16. CDN

| AWS | OSS option | LocalStack | Quality | Notes |
|---|---|---|---|---|
| CloudFront | none | localstack Pro | none viable | CDN behavior (POP routing, edge caching) is the point and is unobservable locally. |
| CloudFront Functions / Lambda@Edge | (none worth) | none | none viable | Edge runtime is Node-only — no Go support, no `aws-sdk-go-v2`. Test routing as a pure function; don't try to emulate. |
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
| Cognito User Pools (token verification) | `quay.io/keycloak/keycloak:24` realm (or any local OIDC issuer) | localstack Pro — Cognito | partial | **Tier 1**: use **`golang-jwt/jwt/v5`** + **`MicahParks/keyfunc/v3`** to verify JWTs against a JWKS URL both sides. Cognito exposes `https://cognito-idp.<region>.amazonaws.com/<pool_id>/.well-known/jwks.json`; local dev issuer exposes its own well-known. `keyfunc` handles JWKS fetch/cache; `jwt.Parse` does verification. See v4 §4. |
| Cognito User Pools (user lifecycle: sign-up, MFA, hosted UI, custom attrs) | none viable locally | localstack Pro | none viable | T2 / `cloud_only`. User-management admin APIs (`aws-sdk-go-v2/service/cognitoidentityprovider` `Admin*` operations) only run cloud-side. |
| Cognito Identity Pools | none | localstack Pro | partial | Returning AWS temp credentials is AWS-only. T2. |
| IAM | `localstack` Community — IAM | localstack | partial | Policies parse; *enforcement* doesn't happen in LocalStack Community. T2 for runtime enforcement. |
| IAM Identity Center | none | none | none viable | Enterprise SSO. T2. |
| Verified Permissions (Cedar) | `cedar-policy/cedar-go` (official Go port, GA in 2024) | none | **behavior-compatible** | Cedar is OSS; `cedar-go` is the official native Go implementation — cleaner than TS's `@cedar-policy/cedar-wasm` (Rust-via-wasm) wrapper. AWS service adds storage + management. T0 for the decision call. |

**Go idiom (Tier 1)**: per v4 §4, verify tokens with `golang-jwt` + `keyfunc` and let env vars point at the right JWKS URL — *not* hand-roll an interface with two impls. The Cognito vs local-dev split lives in the JWKS URL env var, not in the code path.

```go
import (
    "github.com/MicahParks/keyfunc/v3"
    "github.com/golang-jwt/jwt/v5"
)

jwks, _ := keyfunc.NewDefault([]string{os.Getenv("JWKS_URL")})
// Cloud:  https://cognito-idp.<region>.amazonaws.com/<pool_id>/.well-known/jwks.json
// Local:  http://keycloak:8080/realms/dev/protocol/openid-connect/certs

token, err := jwt.Parse(rawToken, jwks.Keyfunc,
    jwt.WithIssuer(os.Getenv("JWT_ISSUER")),
    jwt.WithAudience(os.Getenv("JWT_AUDIENCE")),
)
```

User-management admin actions (creating users, resetting passwords, custom attribute writes) are inherently `cloud_only` — there is no portable abstraction; the local-dev experience for those is "skip in dev" or "hit real Cognito via mounted creds".

---

## 19. Secrets / Config

| AWS | OSS option | LocalStack | Quality | Notes |
|---|---|---|---|---|
| Secrets Manager | `localstack` Community — Secrets Manager | localstack | **wire-compatible** | `aws-sdk-go-v2/service/secretsmanager` with `o.BaseEndpoint`. Rotation lambdas are the only AWS-specific bit. |
| SSM Parameter Store | localstack Community — SSM | localstack | **wire-compatible** | `aws-sdk-go-v2/service/ssm`. |
| AppConfig | localstack Pro | localstack Pro | partial | `aws-sdk-go-v2/service/appconfigdata`. Get-config works; deployment strategies partial. |
| KMS | localstack Community — KMS | localstack | partial | `aws-sdk-go-v2/service/kms`. Encrypt/decrypt work with software keys; HSM-backed keys + key policies are real-AWS-only. |

**Go idiom**:
```go
import (
    "github.com/aws/aws-sdk-go-v2/aws"
    "github.com/aws/aws-sdk-go-v2/service/ssm"
)

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

---

## 20. Workflow / Scheduling

| AWS | OSS option | LocalStack | Quality | Notes |
|---|---|---|---|---|
| Step Functions Standard | `amazon/aws-stepfunctions-local` (official, free) | localstack Pro | **wire-compatible** | `aws-sdk-go-v2/service/sfn` with `o.BaseEndpoint`. Surprisingly good. |
| Step Functions Express | aws-stepfunctions-local | localstack Pro | partial | Express semantics not fully tested in local container. |
| Step Functions Distributed Map | none | none | none viable | Distributed Map's parallel-children behavior is AWS-only. |
| EventBridge Scheduler | localstack Pro | localstack Pro | partial | Schedules fire; cron precision differs. |
| MWAA (Managed Airflow) | `apache/airflow:2.10` (real Airflow) | none | behavior-compatible | Airflow is Python-only — Go ops invoke DAGs via API; cross-language seam. |
| SWF | none | none | none viable | Legacy; no local. |

**Go idiom** for in-process scheduling (local dev only): `robfig/cron/v3` (canonical Go cron) for simple cron expressions; `hibiken/asynq` (Redis-backed) or `riverqueue/river` (Postgres-backed) if you also need durable scheduled jobs.

---

## 21. Email / Notifications

| AWS | OSS option | LocalStack | Quality | Notes |
|---|---|---|---|---|
| SES | `mailhog/mailhog` or `axllent/mailpit` (SMTP catcher) | localstack Community — SES | partial | **Tier 1**: two paths. (a) **`net/smtp`** (Go stdlib, minimalist) — points at SES SMTP endpoint in cloud, mailhog in dev; same env-driven host/port. (b) **`go-gomail/gomail`** (community wrapper, MIME-friendly, attachment helpers — the closest parallel to `nodemailer` / `lettre` / `smtplib`). `aws-sdk-go-v2/service/sesv2` is the SES-direct alternative when you need SES-specific features (templates, configuration sets). Pick one path per call site. See v4 §4. |
| SNS SMS | localstack Pro | localstack Pro | partial | Real SMS doesn't go anywhere. Inspect what was attempted. |
| SNS Mobile Push | none | localstack Pro | none viable | Real APNs/FCM dispatch is the point. |
| Pinpoint / End User Messaging | localstack Pro | localstack Pro | partial | Campaign orchestration partly stubbed. |

---

## 22. Observability

| AWS | OSS option | LocalStack | Quality | Notes |
|---|---|---|---|---|
| CloudWatch Logs | stdout (Fargate awslogs driver / Lambda automatic) | localstack | wire-compatible | Best practice: emit JSON to stdout via `log/slog` (Go 1.21+ stdlib structured logger), `rs/zerolog`, or `uber-go/zap`. Almost no Go app should call `aws-sdk-go-v2/service/cloudwatchlogs` `PutLogEvents` directly. |
| CloudWatch Metrics | `aws/aws-sdk-go-v2/service/cloudwatch` or EMF in logs | localstack | partial | EMF (embedded metric format) in logs avoids PutMetricData costs and JSON-shapes metrics for CloudWatch. |
| CloudWatch Alarms | localstack Pro | localstack Pro | partial | Definitions accepted; triggering partial. |
| CloudWatch Synthetics | none | none | none viable | Real browser canaries against real endpoints. |
| CloudWatch RUM | none | none | none viable | Real-user telemetry from real browsers. |
| X-Ray | `amazon/aws-xray-daemon` for local sampling/UDP collection (no UI) | localstack Pro — X-Ray | partial | Daemon catches segments locally; visualizing requires AWS console. Use `jaeger` for local trace UI via OTel. |
| CloudTrail | localstack Pro | localstack Pro | partial | Event logging partly stubbed. |
| Application Signals | none | none | none viable | Auto-instrument-magic is AWS-only. |

**Go idiom** (OTel + tracing):
```go
import (
    "go.opentelemetry.io/otel"
    "go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
    "go.opentelemetry.io/otel/sdk/trace"
)

exporter, _ := otlptracegrpc.New(ctx,
    otlptracegrpc.WithEndpoint(getenv("OTEL_EXPORTER_OTLP_ENDPOINT", "jaeger:4317")),
    otlptracegrpc.WithInsecure(),
)
tp := trace.NewTracerProvider(trace.WithBatcher(exporter))
otel.SetTracerProvider(tp)
// AWS:   ADOT collector → X-Ray
// Local: jaeger
```

---

# Data / analytics

| AWS | OSS option | LocalStack | Quality | Notes |
|---|---|---|---|---|
| Redshift | `postgres:16` (very different perf) or `clickhouse/clickhouse-server` | localstack Pro — Redshift API | partial | Redshift speaks Postgres wire protocol; use `pgx`. DISTKEY/SORTKEY syntax won't reproduce. |
| Redshift Serverless | same | localstack Pro | partial | Same. |
| Athena | `prestodb/presto` or `trinodb/trino` | localstack Pro — Athena | partial | `aws-sdk-go-v2/service/athena` or REST via `trinodb/trino-go-client`. Trino is the OSS engine Athena is built on. |
| S3 Select | minio (no S3 Select equivalent) | localstack | none viable | Object-scan-with-SQL is AWS-only. |
| Glue Jobs (Spark) | `bitnami/spark:3.5` | localstack Pro | partial | No native Go Spark. Cross-language seam (Python/Scala for Spark; Go for orchestration). |
| Glue Crawlers | none | localstack Pro | partial | Schema discovery is best emulated with hand-defined schemas locally. |
| Glue Data Catalog | `apache/hive:3` Metastore | localstack Pro | partial | Hive Metastore is the protocol Glue Catalog implements. |
| Glue DataBrew | none | none | none viable | UI-driven; no local equivalent. |
| Lake Formation | none | none | none viable | Permissions overlay is AWS-only. |
| EMR | `bitnami/spark:3.5` (Spark only) | localstack Pro | partial | Cross-language seam. |
| EMR Serverless | bitnami/spark | localstack Pro | partial | Same. |
| QuickSight | `metabase/metabase`, `superset` | none | none viable | Specific dashboard authoring won't port. |

**Go analytics note**: for single-node OLAP in Go, `marcboeker/go-duckdb` (CGO) gives a Postgres-shaped SQL surface with strong column-store performance. For team-scale analytics, those still ship to JVM-based services. For OLTP→OLAP pipelines, `riverqueue/river` (Postgres-backed worker) is the Go-native way to stream rows into Redshift/ClickHouse.

---

# ML / AI

| AWS | OSS option | LocalStack | Quality | Notes |
|---|---|---|---|---|
| SageMaker Training | `python:3.12` + your training script | localstack Pro — SageMaker | partial | Go ML training is niche; most production training is Python on SageMaker. Cross-language seam. |
| SageMaker Real-Time Inference | run model in Go via ONNX Runtime Go bindings (`yalue/onnxruntime_go`) or call out to a Python container | localstack Pro | partial | Same — the model serving contract is straightforward. |
| SageMaker Serverless Inference | same | none | partial | Same. |
| SageMaker Batch Transform | run script over S3-pointed input | localstack Pro | partial | Same. |
| SageMaker Studio | none | none | partial | Jupyter is Python-centric; Go has `gophernotes` for Jupyter but Studio integrations don't follow. |
| SageMaker JumpStart / Canvas | none | none | none viable | Bundled model marketplace + no-code UI. |
| Bedrock — Claude/Llama/Mistral/Nova/Titan | **`ollama/ollama`** (local LLM serving) or `vllm/vllm-openai` | localstack Pro — Bedrock | partial | **Tier 1**: two options. (a) **`tmc/langchaingo`** — the broader-coverage option, ports the Python LangChain abstraction to Go; provider modules for Bedrock + Ollama + OpenAI + Anthropic + Cohere + many others. (b) **`cloudwego/eino`** — newer (CloudWeGo/ByteDance, GA 2024), opinionated chain DSL, fewer providers but cleaner type model. Both are named in v4 §4. Env-driven model selects the backend in either case. |
| Bedrock Knowledge Bases (RAG) | OpenSearch + your own retrieval | none | none viable | T2. Orchestration is the value-add; no community lib bridges it. Either hit real AWS from local (v4 §4 "hit-real" pattern) or skip in local dev. |
| Bedrock Agents | none | localstack Pro | none viable | T2. Tool-use orchestration is proprietary. |
| Bedrock Guardrails | OSS filters (custom or via small classifier models) — different philosophy | none | none viable | T2. |
| Rekognition (Image/Video) | ONNX Runtime Go bindings + YOLO/CLIP/DETR models, or `hybridgroup/gocv` (OpenCV Go bindings, CGO) | localstack Pro | partial | Tasks (object detect, face match) exist in OSS but ML model is different. Note: `gocv` requires CGO — won't work in scratch / `provided.al2023` static binaries; use a glibc/musl base. |
| Textract | OCR via `otiai10/gosseract` (Tesseract CGO bindings) | localstack Pro | partial | OCR works; forms/tables much weaker. |
| Polly (TTS) | (no first-class Go TTS) — call out to Coqui-TTS / piper Python service | none | partial | Voice quality and SSML support differ. |
| Transcribe (STT) | `ggerganov/whisper.cpp` Go bindings (`whisper.cpp/bindings/go`) | none | partial | Output format differs (Whisper returns segments + text; Transcribe returns rich items). CGO required. |
| Comprehend | small ONNX classifier models via `onnxruntime_go` | none | partial | Sentiment/NER doable locally. |
| Translate | small ONNX seq2seq models (NLLB / M2M100 via onnxruntime_go) | none | partial | Quality gap on production languages. |
| Forecast / Personalize | none (Forecast deprecated; Personalize requires AWS data pipeline) | none | none viable | — |

**Go idiom for Bedrock (Tier 1, both options shown)**: per v4 §4, use one of the named community libraries — `langchaingo` for broader provider coverage, or `eino` for ByteDance's newer typed-chain DSL.

```go
// Option A: langchaingo — broader provider router, mirrors Python LangChain
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
```

```go
// Option B: eino — CloudWeGo, opinionated chain DSL, fewer providers, cleaner types
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

**Decision criterion**: `langchaingo` if you want the largest provider catalog or are porting Python LangChain code; `eino` if you want a more Go-idiomatic typed-graph composition model and your provider needs are well-covered by its (smaller) set. Both are actively maintained as of 2026. The previously common pattern of hand-rolling an `LLMClient` interface with `BedrockLLM` + `OllamaLLM` impls is the v1-era prescription — v4 §4 explicitly states that caravan does not ship runtime adapter libraries when mature community libraries already cover the abstraction. Bedrock Knowledge Bases / Agents / Guardrails remain `cloud_only` (T2) — neither library bridges those.

---

# IoT / edge

| AWS | OSS option | LocalStack | Quality | Notes |
|---|---|---|---|---|
| IoT Core (MQTT) | `eclipse-mosquitto:2` | localstack Pro — IoT | behavior-compatible | `eclipse/paho.mqtt.golang` (canonical, pure-Go). AWS-specific bits (thing shadow, jobs) absent; for those use `aws/aws-iot-device-sdk-go` (newer) or hit `aws-sdk-go-v2/service/iotdataplane` directly. |
| IoT Core (HTTPS / WS) | `eclipse-mosquitto:2` with WS listener | localstack Pro | partial | `paho.mqtt.golang` supports WS. |
| IoT Device Management | none | localstack Pro | none viable | Fleet operations specific to AWS. |
| IoT Device Defender | none | none | none viable | Anomaly detection on AWS-side data flow. |
| IoT Rules Engine | none | localstack Pro | partial | Rules tie IoT Core → other AWS services; needs ecosystem stubs. |
| Greengrass V2 | run `greengrass-runtime` in container (AWS provides) | none | behavior-compatible | Official local runtime; Lambda Go runtime supported as a Greengrass component. |
| FreeRTOS | run on local hardware or QEMU | none | wire-compatible | OSS RTOS; no Go at this layer — irrelevant for Go users. |
| IoT Analytics / SiteWise / Events / TwinMaker / FleetWise | none | localstack Pro (some) | none viable | Domain-specific. |

---

# Summary: emulation-quality scoreboard

| Quality band | Count | Examples |
|---|---|---|
| **wire-compatible** | ~13 | S3, DynamoDB, SQS, SNS, Kinesis (producer), Secrets Manager, SSM Parameter Store, CloudWatch Logs (via stdout), Step Functions Standard, Aurora pgvector, Influx-flavored Timestream, Verified Permissions (cedar-go is native), KMS (encrypt/decrypt) |
| **behavior-compatible** | ~10 | RDS/Aurora (Postgres/MySQL/MariaDB/SQL Server), ElastiCache (Redis/Memcached), MemoryDB, OpenSearch, Amazon MQ (RabbitMQ/ActiveMQ), MSK (incl. IAM — Go's MSK-IAM is mature), IoT Core MQTT, Greengrass |
| **partial** | ~24 | Lambda, API Gateway, EventBridge, Cognito, AppConfig, Athena (Trino), Glue (Spark cross-lang), X-Ray, SageMaker training/inference, Rekognition/Polly/Transcribe (different OSS models), Bedrock (different OSS models), CloudWatch Metrics, Textract (CGO Tesseract), DocumentDB (Mongo-divergent) |
| **none viable** | ~15 | Lambda@Edge, CloudFront, CloudFront Functions, Global Accelerator, IAM enforcement, IAM Identity Center, S3 Vectors, S3 Express One Zone, S3 Select, S3 Object Lambda, Aurora DSQL, DAX, Neptune Analytics, Kendra, Bedrock Knowledge Bases / Agents / Guardrails, CloudWatch Synthetics / RUM / Application Signals, SNS Mobile Push, IoT Device Management / Defender / Analytics / SiteWise / TwinMaker / FleetWise, Forecast, Personalize, SageMaker JumpStart / Canvas, Step Functions Distributed Map |

**Go-specific notes vs Python, Rust, and TypeScript coverage**:
- Trivially-equivalent counts (~13 wire / ~10 behavior) are essentially identical to TS's and Python's, slightly above Rust's. The `aws-sdk-go-v2/service/*` packages' per-client `BaseEndpoint` option is the universal Trivial-band enabler — same shape as boto3's `endpoint_url=` kwarg and TS's `endpoint`. `BaseEndpoint` shipped in v1.16 (~2023) and is the modern path; older docs that show `EndpointResolverWithOptions` are pre-2023 and unnecessarily complex for the common case.
- A few cells are **cleaner on the Go side**:
  - **MSK with IAM auth** — `aws/aws-msk-iam-sasl-signer-go` is mature; both `segmentio/kafka-go` and `confluentinc/confluent-kafka-go` integrate. Go's MSK-IAM story is on par with Java's and meaningfully cleaner than TS's (`kafkajs` SigV4 gap requires FFI fallback).
  - **Verified Permissions** — `cedar-go` is a native Go implementation, not a wasm wrapper. Cleaner deployment story than TS's `@cedar-policy/cedar-wasm`.
  - **Lambda cold-start** — Go custom-runtime cold-starts of 10–50 ms are the **lowest of the four first-class languages** (vs Rust ~50–100 ms, TS ~100–500 ms, Python ~500–1500 ms). Pairs well with the smallest container image (~5–20 MB scratch / distroless).
  - **Static binaries** — `CGO_ENABLED=0 go build` enables `FROM scratch` images. No other first-class language deploys this cleanly.
- A few cells are **less mature on the Go side**:
  - **TTS** — no first-class Go TTS; cross-language to Python (Coqui-TTS / piper). Same gap as TS and Rust.
  - **SageMaker training** — Python-shaped; Go users orchestrate cross-language. Same gap.
  - **CGO-requiring ML libs** — `gocv` (OpenCV), `gosseract` (Tesseract), `whisper.cpp` Go bindings all need CGO + glibc/musl base images. Trade-off vs. the static-binary story; cleanly documented per-row.
- A few cells are **on par** with TS/Python:
  - **LLM provider abstraction** — `langchaingo` (LangChain port) and `eino` (CloudWeGo/ByteDance) are the named Tier 1 options. `langchaingo` is the broader-coverage analogue to `litellm` / Vercel AI SDK; `eino` is newer with a Go-idiomatic typed-chain DSL.
  - **Token verification** — `golang-jwt/jwt/v5` + `MicahParks/keyfunc/v3` for JWKS handling parallels `jose` (TS) / `authlib` (Python) / `jsonwebtoken` (Rust).
  - **Email** — two community paths: `net/smtp` (stdlib, minimalist) or `gomail` (community wrapper, MIME-friendly). Either covers cloud↔local with env-driven SMTP host.

**Implications for caravan** (developed in `caravan_abstraction_v4.md`):
- The ~23 wire-or-behavior-compatible services map to **v4 Tier 0** — caravan's job is env-var injection (endpoint URL or DSN). No abstraction library, no runtime SDK.
- The ~24 partial services split. Those with a mature Go community library (Cognito token verify → `golang-jwt` + `keyfunc`; SES → `net/smtp` or `gomail`; Bedrock LLM core → `langchaingo` or `eino`; Whisper-shaped STT → `whisper.cpp` Go bindings) are **v4 Tier 1** — caravan documents which library to import; the abstraction lives in user code via that library. Those without a clean community bridge (advanced API Gateway features, EventBridge schema registry, etc.) stay close to cloud-only.
- The ~15 none-viable services are **v4 Tier 2** — `cloud_only:` in the IR. caravan refuses to generate a local stand-in; user picks one of v4 §4's four patterns (skip / hit-real / engine-swap / stub) per service.

**Go-side IaC tooling context (anchors v4 §5 / §7d reasoning)**:
- **cdktf-go** (Cloud Development Kit for Terraform, Go binding) was sunset and archived **December 10, 2025** by HashiCorp/IBM along with the rest of CDKtf, citing "no product-market fit at scale" — see the [HashiCorp CDKtf page](https://developer.hashicorp.com/terraform/cdktf). As of 2026 the project is archived and read-only; no further updates ship. HashiCorp directs former CDKtf users to HCL, Pulumi, or AWS CDK.
- **Pulumi-Go** remains available and well-supported, and is the language for caravan's CLI internals per `thesis.md:63` ("If HCL expressiveness becomes the binding constraint, Pulumi-Go-as-CLI-internal is the next move"). It emits resources via imperative Go code rather than reviewable HCL artifacts — security/compliance teams typically prefer the latter for production deploys, which is why caravan emits HCL today.
- **AWS CDK** (Go preview) emits CloudFormation, not Terraform/HCL; it ties users to AWS and to an opaque-by-default deploy artifact.
- **Net**: as of 2026 there is no first-party HCL-emitting-from-Go toolchain. caravan fills that gap polyglot-first by emitting HCL from yaml — and caravan's CLI itself being Go (per `thesis.md:63`) makes Go's IaC story doubly load-bearing: Go is both a first-class user-code language and the CLI implementation language. See `caravan_abstraction_v4.md` §5, §7d.

See `go_api_diffs.md` for the actual code-diff per pair. Conceptual home: `thesis.md`. Long-form derivation of T0/T1/T2 and the v1 PoC scope: `caravan_abstraction_v4.md` (supersedes v3).
