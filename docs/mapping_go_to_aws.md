# Go Stack → AWS Mapping

> ⚠️ **HISTORICAL — pre-SDK research notes; Go SDK is namespace-reserved only.** Current SDK namespace at [`../rpc/go/`](../rpc/go/) is a 0.0.1 placeholder; Go is out of PoC scope per [`development_plan.md`](development_plan.md). (Note: the compiler itself is written in Go, but caravan-rpc-go is not a PoC deliverable.) Snapshot 2026-05-16; do not assume any specific row is current.

> **Snapshot date: 2026-05-16.** AWS prices reference `aws_service_groups.md`.
> **Scope**: Go ecosystem (Go 1.22+). Python, Rust, and TypeScript mirrors live in `mapping_python_to_aws.md` / `mapping_rust_to_aws.md` / `mapping_typescript_to_aws.md`.
> **Framing**: this file is Go ecosystem evidence feeding into `thesis.md` (conceptual home) and `caravan_abstraction_v4.md` (long-form derivation; supersedes v3). The Cheapest/Production/Premium tier labels below are the **operator's intuition**; they map onto v4 §6's explicit yaml `tier:` vocabulary (`db.sql tier: dev | prod-small | prod | premium | global`, `bucket class: standard | intelligent | …`, etc.) — that mapping is shown inline per row and rolled up in the closing summary table.

Question this file answers: *"My Go app and its docker-compose dependencies — what does each piece become on AWS?"*

Each row lists three tiers — **Cheapest fit** (PoC, hobby, dev/staging), **Production fit** (typical "real app" choice), **Premium fit** (when scale, latency, or compliance dominate). Tier choice depends on traffic, budget, and how much you trust AWS-specific lock-in. Where multiple AWS services are mentioned, see `aws_service_groups.md` for cost/latency detail; for cloud↔local code-diffs see `go_api_diffs.md`.

**Build / runtime note**: snippets assume Go 1.22+. Default build is `CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -ldflags="-s -w"` producing a static binary that runs in `FROM scratch` or `gcr.io/distroless/static-debian12`. For Graviton arm64 Fargate, cross-compile with `GOARCH=arm64`. CGO-requiring deps (`gocv`, `mattn/go-sqlite3`, `gosseract`, `whisper.cpp` bindings) need a glibc or musl base instead of scratch — flagged per row.

---

## Datastores

### postgres
- **Local**: `postgres:16-alpine`. Go clients: `database/sql` + `lib/pq` (pure-Go, canonical baseline), `jackc/pgx/v5` (modern, faster, native + database/sql interfaces via `stdlib`). ORMs: `gorm` (decorator/struct-tag ORM), `ent` (Facebook's typed graph-shaped ORM), `bun` (lightweight typed query builder), `uptrace/bun`. Codegen: `sqlc` (SQL → typed Go funcs).
- **Cheapest fit**: RDS Postgres db.t4g.micro (~$12/mo) — single-AZ.
- **Production fit**: Aurora Postgres Serverless v2 (auto-pause for dev/staging; 0.5–8 ACU for prod). Drop-in driver compatibility.
- **Premium fit**: Aurora Postgres provisioned multi-AZ with I/O-Optimized cluster config + 1–3 read replicas. Aurora DSQL for active-active multi-region.
- **Decision criterion**: under 100k req/day → RDS micro is fine. Above 1 RPS sustained, Aurora Serverless v2 wins on the operational story. Premium only when you can defend the cost.
- **v4 yaml**: `db.sql tier: dev` (RDS micro) · `prod-small` (Aurora Serverless v2) · `prod` (Aurora provisioned multi-AZ) · `premium` (multi-AZ + read replicas + I/O-Optimized) · `global` (Aurora Global / DSQL). Tier 0 — DSN swap via `DATABASE_URL`.
- **Gotcha (Go-specific, `pgx`)**: `pgx` exposes two interfaces — the native `pgx.Conn` / `pgxpool.Pool` (faster, exposes `LISTEN/NOTIFY`, `COPY`, batch protocol) and `database/sql`-compatible via `import _ "github.com/jackc/pgx/v5/stdlib"` + `sql.Open("pgx", dsn)`. The native interface locks the call sites to pgx; the `database/sql` adapter keeps swap-ability. Pick one per project; mixing both within one repo is confusing.
- **Gotcha (Go-specific, `sqlc`)**: `sqlc generate` runs at *build time* and needs the schema files in the repo to be in sync with what the SQL queries assume. Parallel to Rust's `sqlx::query!` macro. CI must run `sqlc generate` after any migration is added; many teams check in the generated code so production builds don't need `sqlc` available.
- **Gotcha (Go-specific, `gorm`)**: `db.AutoMigrate(&User{})` is a development-loop shortcut, not a production migration tool. Use `golang-migrate/migrate`, `pressly/goose`, or `atlasgo/atlas` for managed migrations in cloud targets. caravan doesn't choose for you; document it in the reference apps.

### mysql / mariadb
- **Local**: `mysql:8`, `mariadb:11`. Go: `go-sql-driver/mysql` (canonical pure-Go), `gorm`, `ent`, `bun`. ORMs work with the same DSN.
- **Cheapest fit**: RDS MySQL db.t4g.micro (~$12/mo).
- **Production fit**: Aurora MySQL Serverless v2 (MariaDB code mostly works against Aurora MySQL — check stored proc syntax).
- **Premium fit**: Aurora MySQL with global database + cross-region read replicas.
- **Decision criterion**: Same as postgres. MariaDB-specific features (JSON path expressions, certain spatial functions) may not survive Aurora MySQL — verify per workload.
- **v4 yaml**: `db.sql engine: mysql tier: dev | prod-small | prod | premium | global`. Tier 0 — DSN swap via `DATABASE_URL`.

### mongodb
- **Local**: `mongo:7`. Go: `mongo-driver` (the official driver, `go.mongodb.org/mongo-driver`).
- **Cheapest fit**: DocumentDB db.t4g.medium (~$72/mo) — cheapest viable Mongo-compatible.
- **Production fit**: DocumentDB multi-AZ with 1+ replicas.
- **Premium fit**: MongoDB Atlas on AWS (run via AWS Marketplace) — full Mongo API + Atlas features (Search, Vector Search, Triggers). Often the right call when you actually depend on Mongo aggregations.
- **Decision criterion**: DocumentDB advertises Mongo wire protocol but lacks ~30% of aggregation operators (esp. `$lookup` semantics, `$facet`, `$bucket`). If your code uses modern Mongo features, Atlas-on-AWS over DocumentDB.

### redis (as cache)
- **Local**: `redis:7-alpine`. Go: `redis/go-redis/v9` (canonical, cluster-aware via `redis.NewClusterClient`). Older `gomodule/redigo` is still around but `go-redis` is the standard.
- **Cheapest fit**: ElastiCache Redis cache.t4g.micro (~$12/mo).
- **Production fit**: ElastiCache Redis Serverless (auto-scale, no capacity planning) or ElastiCache cluster-mode-enabled with reader nodes.
- **Premium fit**: MemoryDB for Redis (durable Redis as primary store) — costs ~6× ElastiCache but eliminates a separate DB tier.
- **Decision criterion**: 99% of "I need Redis" cases want ElastiCache Serverless. MemoryDB only when you've explicitly chosen Redis as system of record.
- **v4 yaml**: deferred to v1.x — see v4 §6 tier table (`cache: tier: dev | prod-small | prod-cluster | serverless | memorydb`). Tier 0 — DSN swap via `REDIS_URL`.

### redis (as pub/sub or queue)
- **Local**: same image. Go: `go-redis` `.Subscribe()` / `.XAdd()` for Streams, or `hibiken/asynq` (Redis-backed durable job queue — the Go equivalent of TS's BullMQ).
- **Cheapest fit**: ElastiCache Redis (keep pub/sub working as-is, asynq + Redis broker).
- **Production fit**: Replace pub/sub with SNS + SQS fan-out, or replace stream with Kinesis Data Streams. Different code; better at-least-once semantics and decoupled scaling. asynq's SQS-backend story is less mature than BullMQ's — most teams swap to raw `aws-sdk-go-v2/service/sqs` when migrating.
- **Premium fit**: EventBridge with content-based routing for the pub/sub layer.
- **Decision criterion**: Redis pub/sub is fire-and-forget (drops if no subscriber). SQS/SNS is durable. For anything you care about losing, do not keep Redis pub/sub in AWS.
- **v4 yaml**: when migrating, declare `topic:` (→ SNS) for fan-out and `queue:` (→ SQS, ElasticMQ locally) for point-to-point. Both Tier 0 once you've moved off Redis pub/sub.

### memcached
- **Local**: `memcached:1`. Go: `bradfitz/gomemcache` (canonical; passive maintenance but stable).
- **Cheapest fit**: ElastiCache Memcached cache.t4g.micro (~$12/mo).
- **Production fit / Premium fit**: ElastiCache Redis Serverless. Memcached has no compelling advantage over Redis in 2026. Switch.
- **Decision criterion**: there isn't one — pick Redis.

### minio (S3-compatible)
- **Local**: `minio/minio`. Go: `aws-sdk-go-v2/service/s3` with `o.BaseEndpoint = aws.String(os.Getenv("S3_ENDPOINT_URL"))` and `o.UsePathStyle = true`. The `minio/minio-go` client is also viable when you specifically want minio admin APIs.
- **Cheapest fit**: S3 Standard. Free tier covers 5 GB.
- **Production fit**: S3 Standard + Intelligent-Tiering for unknown-pattern data.
- **Premium fit**: S3 + CloudFront for read-heavy + S3 Replication cross-region for DR.
- **Decision criterion**: minio is the closest-to-trivial AWS migration in this whole file — same `aws-sdk-go-v2/service/s3` code, env-driven `BaseEndpoint`. The one trap is per-object behavior under concurrent writes (minio's eventual consistency model differs from S3 in failure modes).
- **v4 yaml**: `bucket class: standard | intelligent | standard-ia | one-zone-ia | glacier-instant | glacier-flexible | glacier-deep-archive`; `lifecycle:` for transitions; `variant: standard | express-one-zone | vectors` for the rare typed-different cases. Tier 0 — `S3_ENDPOINT_URL` + `UsePathStyle` swap.
- **Edge cases moving to v4 cloud_only**: S3 + CloudFront for "Production fit" reads becomes `static_site` primitive (v1.2 per v4 §10); CloudFront standalone is `cloud_only: cloudfront`.

### opensearch
- **Local**: `opensearchproject/opensearch:2` (or `elasticsearch:8`). Go: `opensearch-project/opensearch-go/v3` (official). `elastic/go-elasticsearch/v8` is Elastic's own; differs post-fork.
- **Cheapest fit**: OpenSearch Service t3.small.search single-node (~$73/mo) — viable for dev; not for prod.
- **Production fit**: OpenSearch Service provisioned 3-node cluster (r6g.large.search × 3 ≈ $400/mo) with dedicated master nodes for stability above 10 data nodes.
- **Premium fit**: OpenSearch Serverless if your workload is genuinely spiky and the $1k/mo floor doesn't sting.
- **Decision criterion**: OpenSearch's API is a fork of Elasticsearch 7.10 — modern Elasticsearch ≥8 code (especially using x-pack security or vector search APIs that differ) may need shims. `opensearch-go/v3` is straightforward; `go-elasticsearch/v8` will refuse to connect to OpenSearch without `MAJOR_VERSION_MISMATCH` overrides.

### qdrant / weaviate / chroma (dedicated vector DBs)
- **Local**: `qdrant/qdrant`, `cr.weaviate.io/semitechnologies/weaviate`, `chromadb/chroma`. Go clients: `qdrant/go-client` (official), `weaviate/weaviate-go-client/v5`. Chroma's Go client is community-maintained.
- **Cheapest fit**: Aurora Postgres pgvector. <10M vectors fits easily on a db.t4g.medium.
- **Production fit**: OpenSearch Service with the k-NN plugin (use HNSW). Best when you already run OpenSearch.
- **Premium fit**: S3 Vectors for tens-of-billions cold storage. Or run the dedicated vector DB on EKS/ECS via the vendor's own AWS Marketplace AMI / Helm chart (Qdrant Cloud, Weaviate Cloud — both have AWS-native managed offerings).
- **Decision criterion**: if you're <10M vectors and your team already runs Postgres, pgvector is the cheapest abstraction shrinkage. Above 100M vectors or strict <50 ms latency SLOs, dedicated managed (Pinecone, Qdrant Cloud) beats AWS-native today.

### pgvector (local)
- **Local**: `pgvector/pgvector:pg16`. Go: `pgx` + `pgvector/pgvector-go` (works with `pgx`, `database/sql`, `gorm`).
- **Cheapest / Production fit**: Aurora Postgres has pgvector extension built-in. Same SQL, same Go code — set `CREATE EXTENSION vector;`.
- **Premium fit**: Aurora Postgres Optimized Reads instances (caches working set in NVMe; helps HNSW search latency).
- **Decision criterion**: easiest AWS port in the vector category. If your vector layer is pgvector locally, keep it pgvector in AWS until it stops scaling.

### sqlite (single-process embedded)
- **Local**: file-on-disk. Go: `mattn/go-sqlite3` (CGO, mature, fastest) or `modernc.org/sqlite` (pure-Go, slower, works in scratch).
- **Cheapest fit**: not a server. For sqlite-on-AWS, options are EFS-backed file (`db.sqlite` on a Fargate-mounted EFS) or Litestream-replicated to S3 — both niche.
- **Production fit**: migrate to RDS Postgres. sqlite is wrong for prod multi-replica services anyway.
- **Decision criterion**: sqlite is great for embedded/CLI tools and dev. Doesn't belong in a horizontally-scaled service; caravan's `db.sql` primitive doesn't target it.

---

## Messaging

### rabbitmq
- **Local**: `rabbitmq:3-management`. Go: `rabbitmq/amqp091-go` (the canonical AMQP 0.9.1 library, official maintained fork of `streadway/amqp`).
- **Cheapest fit**: Amazon MQ for RabbitMQ mq.t3.micro single-instance (~$15/mo).
- **Production fit**: Amazon MQ multi-AZ cluster (3-node) — keeps your AMQP semantics intact.
- **Premium fit**: Same. Cluster size up.
- **Alternative path (cheaper but code changes)**: Move to SQS Standard (replace `amqp091-go` consumer with `aws-sdk-go-v2/service/sqs` polling) + SNS for fan-out. Often the right call if RabbitMQ features used were just "queue with workers."
- **Decision criterion**: If you rely on AMQP-specific features (priority queues, dead-letter exchanges with topic routing, JMS-like selectors) → Amazon MQ. If you used RabbitMQ as a generic queue → switch to SQS, save money and ops effort.
- **v4 yaml**: when migrating to SQS, declare `queue kind: standard | fifo`. Tier 0 — `SQS_ENDPOINT_URL` swap (ElasticMQ locally). Sticking with AMQP keeps `amqp091-go` Tier 0 against the rabbitmq container ↔ Amazon MQ (DSN swap).

### kafka
- **Local**: `bitnami/kafka:3.7` or `confluentinc/cp-kafka:7`. Go: `segmentio/kafka-go` (preferred pure-Go, easy API), `confluentinc/confluent-kafka-go/v2` (librdkafka FFI, more features, MSK-IAM via signer), `IBM/sarama` (older, mature, still widely used).
- **Cheapest fit**: MSK Serverless ($540/mo cluster floor — there's no "tiny" Kafka in AWS).
- **Production fit**: MSK provisioned `kafka.m7g.large` × 3 brokers (≈$500/mo brokers + storage).
- **Premium fit**: MSK provisioned with tiered storage + MSK Connect for source/sink connectors. Confluent Cloud on AWS Marketplace for full Kafka + Schema Registry + ksqlDB.
- **Cheaper non-Kafka alternative**: Kinesis Data Streams on-demand via `aws-sdk-go-v2/service/kinesis`. Different client library, no consumer-group rebalancing, but cheap at low-throughput.
- **Decision criterion**: Kafka is the most expensive messaging primitive to run in AWS. If you can live without consumer groups + exactly-once semantics, Kinesis is 5–10× cheaper at low volume. If you can't, MSK is the price.
- **Go advantage (MSK-IAM)**: `aws/aws-msk-iam-sasl-signer-go` is mature and battle-tested — both `segmentio/kafka-go` and `confluent-kafka-go` integrate cleanly. Go's MSK-IAM story is on par with Java's; meaningfully cleaner than TS (`kafkajs` SigV4 gap forces librdkafka FFI fallback) and slightly cleaner than Rust (the Rust signer is less battle-tested at scale).

### nats
- **Local**: `nats:2-alpine`. Go: `nats-io/nats.go` (the official client; NATS itself is written in Go, so Go has the best-supported client).
- **Cheapest fit**: Run NATS yourself on ECS Fargate (1 vCPU, 2 GB) ~$30/mo. Or migrate to SNS + SQS.
- **Production fit**: NATS on EKS or 3-node EC2 fleet (NATS clustering is straightforward).
- **Premium fit**: Synadia Cloud on AWS Marketplace if you need NATS-specific JetStream semantics managed.
- **Decision criterion**: NATS has no AWS-managed equivalent. Either self-host or migrate the abstraction (most NATS-as-pubsub uses translate cleanly to SNS+SQS). Go is NATS's home language — clients and tooling are first-class.

---

## Go app processes

### Go 1.22+ (canonical)
- **Local**: `golang:1.22-alpine` build stage → `gcr.io/distroless/static-debian12` or `FROM scratch` runtime. Lambda: `public.ecr.aws/lambda/provided:al2023` container image with the static `bootstrap` binary, or use `aws-lambda-go` directly with the managed `provided.al2023` runtime.
- **Cheapest fit**: Lambda + Function URL or API Gateway via `aws-lambda-go-api-proxy`. Free tier covers 1M req/mo.
- **Production fit**: ECS Fargate task behind ALB.
- **Premium fit**: App Runner for managed Fargate; ECS on EC2 + Spot for cost optimization.
- **Decision criterion**: Lambda cold-start for Go is **10–50 ms** (warm-start <5 ms) — the lowest of the four first-class languages. Static binary means no per-cold-start interpreter/runtime init. For request/response APIs without long-lived state, Lambda wins on ops + cost; for Go specifically the cold-start objection that drives Python+Node teams off Lambda barely applies. For websockets / SSE / heavy startup work / lifespan-style init, Fargate keeps the long-lived process model.
- **Build step**: `CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -ldflags="-s -w" -o bootstrap` (for Lambda Graviton) or `GOARCH=amd64` for x86. Single static binary; no bundler step, no dependency tree at runtime. Multi-stage Docker copies the binary into `scratch` or `distroless/static`.

### HTTP frameworks (all three covered per design decision)

For each: same Cheapest=Lambda+adapter / Production=Fargate behind ALB / Premium=App Runner pattern. The api_diffs file shows the canonical "one container, two shapes" snippet for each. All three use `aws-lambda-go-api-proxy` (sub-package per router) — the Go parallel to TS's `serverless-http` / `hono/aws-lambda` / `@fastify/aws-lambda`.

#### chi
- **Local**: `go-chi/chi/v5` + `aws-lambda-go-api-proxy/chi` (Lambda packaging).
- **Cheapest fit**: Lambda + Function URL via `chiadapter.New(r).ProxyWithContext`.
- **Production fit**: ECS Fargate behind ALB.
- **Decision criterion**: closest Go analogue to TS's Hono or Rust's `axum + lambda_http` — idiomatic, minimal, std-lib-shaped `http.Handler`. Recommended modern default; pick gin if hiring breadth dominates.

#### gin
- **Local**: `gin-gonic/gin` + `aws-lambda-go-api-proxy/gin`.
- **Cheapest fit**: Lambda via `ginadapter`.
- **Production fit**: ECS Fargate behind ALB.
- **Decision criterion**: biggest hiring pool, most-tutorials-online, slightly opinionated middleware system. Conservative default.

#### echo
- **Local**: `labstack/echo/v4` + `aws-lambda-go-api-proxy/echo`.
- **Cheapest fit**: Lambda via `echoadapter`.
- **Production fit**: ECS Fargate behind ALB.
- **Decision criterion**: performant, mature middleware ecosystem, first-party-feeling Lambda story via the adapter. Middle pick between chi (idiomatic minimal) and gin (opinionated convenient).

#### fiber
- **Local**: `gofiber/fiber/v2` + `aws-lambda-go-api-proxy/fiber`.
- **Cheapest fit**: Lambda via `fiberadapter`.
- **Production fit**: ECS Fargate behind ALB.
- **Decision criterion**: fasthttp-based (non-`net/http`); fastest in raw throughput but locks you out of the broader `net/http` middleware ecosystem. Pick only if you've measured `net/http` as a bottleneck.

#### stdlib `net/http` ServeMux
- **Local**: `net/http` (stdlib, Go 1.22+ has pattern-matching routes). Lambda: `aws-lambda-go-api-proxy/httpadapter`.
- **Cheapest fit**: Lambda via `httpadapter`.
- **Production fit**: ECS Fargate behind ALB.
- **Decision criterion**: zero dependencies. Go 1.22+ added pattern-matching routes (`mux.HandleFunc("GET /users/{id}", ...)`) that make `net/http` viable for many cases that previously required chi. The minimalist option.

### Workers / background jobs

#### asynq (Redis-backed)
- **Local**: separate Go container running `hibiken/asynq` Worker with Redis broker.
- **Cheapest fit**: asynq + ElastiCache Redis. Worker container on Fargate.
- **Production fit**: asynq + ElastiCache Redis Serverless. Or migrate to SQS-based pattern via raw `aws-sdk-go-v2/service/sqs` `ReceiveMessage` long-poll.
- **Premium fit**: Replace asynq + queue entirely with Step Functions (if workflows are DAG-shaped) or Lambda triggered from SQS (if tasks are short).
- **Decision criterion**: asynq is the canonical Go background-job pattern (parallel to BullMQ for TS). For SQS, raw SDK is the proven path; asynq doesn't have a battle-tested SQS adapter.
- **v4 yaml**: `service` + `trigger: { queue: jobs }`. Tier 0.

#### river (Postgres-backed)
- **Local**: `riverqueue/river` with the Postgres you already have. Worker container on Fargate.
- **Cheapest fit**: river + RDS Postgres (no separate broker needed — uses `LISTEN/NOTIFY` + a jobs table).
- **Production fit**: Same; or migrate to SQS as above.
- **Decision criterion**: river is newer (GA 2024) and avoids the "Redis is a separate critical dependency" cost. If you already run Postgres and want one fewer infra component, river > asynq.

#### robfig/cron (in-process scheduler)
- **Local**: in-process scheduler inside a long-running Go container.
- **Cheapest fit**: EventBridge Scheduler → Lambda (one schedule per job).
- **Production fit**: Same. EventBridge Scheduler is the canonical replacement.
- **Premium fit**: Step Functions Standard for jobs that need durable orchestration.
- **Decision criterion**: in-process schedulers fight multi-instance deployments (cron-fires-twice problem). EventBridge Scheduler is strictly better in AWS.

#### cron-in-container
- **Local**: a container with `cron` running.
- **Cheapest fit**: EventBridge Scheduler → Lambda or → ECS RunTask.
- **Production fit**: Same.
- **Decision criterion**: Use EventBridge Scheduler. No exceptions.

### gRPC services
- **Local**: `grpc-ecosystem/grpc-go` + protobuf codegen via `buf`. Containerized like any other Go service.
- **Cheapest fit**: ECS Fargate behind an NLB (gRPC over HTTP/2; ALB supports HTTP/2 only since 2020 and gRPC since 2020 — check your account).
- **Production fit**: ECS Fargate + NLB or ALB with HTTP/2 + grpc-web for browser clients.
- **Decision criterion**: gRPC is Go's strongest superpower (Go is grpc-go's home). For internal service-to-service traffic, prefer gRPC over JSON+HTTP. For external/public APIs, REST or GraphQL is friendlier.

---

## Adjacent infrastructure

### nginx / traefik (reverse proxy)
- **Local**: `nginx:alpine` or `traefik:v3`. Routes paths, handles TLS, serves static.
- **Cheapest fit**: API Gateway HTTP (paths via routes, TLS via ACM, static via S3+CloudFront).
- **Production fit**: ALB (path + host routing, TLS via ACM) + CloudFront for static.
- **Premium fit**: Add AWS WAF + Shield Advanced.
- **Decision criterion**: ALB is the closest semantic equivalent for service-fronting. API Gateway when your routes are Lambda-backed; ALB when they're Fargate/EC2.

### keycloak
- **Local**: `quay.io/keycloak/keycloak:24`. Go: `coreos/go-oidc/v3` for OIDC client flows; **`golang-jwt/jwt/v5` + `MicahParks/keyfunc/v3`** for token verification.
- **Cheapest fit**: Cognito User Pools (first 10k MAU free).
- **Production fit**: Cognito User Pools + Identity Pools for federated AWS-resource access. Or self-host Keycloak on Fargate + RDS Postgres if your team has Keycloak conviction.
- **Premium fit**: Auth0 / Okta / WorkOS on AWS Marketplace.
- **Decision criterion**: Cognito's UX (hosted UI quirks, custom-attribute friction, password-reset flows) loses to Keycloak on flexibility. Cognito wins on AWS-IAM integration and price at small scale. For >50k users with complex flows (org SSO, branded UI), most teams end up on Auth0/WorkOS or self-host Keycloak.
- **v4 framing (Tier 1)**: per v4 §4, the canonical pattern is *token verification* both sides via the same community library combo — **`golang-jwt` + `keyfunc`** + a JWKS URL env var. Cognito's JWKS lives at `https://cognito-idp.<region>.amazonaws.com/<pool_id>/.well-known/jwks.json`; Keycloak / dev issuer exposes its own well-known JWKS endpoint. Same `jwt.Parse(token, jwks.Keyfunc, ...)` call both sides; no caravan-shipped library involved. Cognito's *user lifecycle* (sign-up, MFA, hosted UI, custom attributes) remains `cloud_only` per v4 §8.
- **Code change**: where you previously used Keycloak Admin REST API, the equivalent is `aws-sdk-go-v2/service/cognitoidentityprovider` `Admin*` operations — these don't have a portable abstraction and only run cloud-side anyway. For request-time auth, the `golang-jwt` + `keyfunc` JWKS pattern is the supported path.

### vault (Hashicorp)
- **Local**: `hashicorp/vault:1.16`. Go: `hashicorp/vault/api` (official client).
- **Cheapest fit**: SSM Parameter Store Standard (free!) for static config. Secrets Manager for rotating secrets.
- **Production fit**: SSM Parameter Store + Secrets Manager + KMS-encrypted application data.
- **Premium fit**: HCP Vault Dedicated on AWS Marketplace if you actually use Vault dynamic secrets / PKI / transit features.
- **Decision criterion**: 90% of "vault" usage in startups is just secret storage — SSM Parameter Store covers it for free. Vault is worth keeping only if you use dynamic database credentials, PKI, or KV v2's leasing.
- **Code change**: `vault.Logical().Read("secret/data/x")` → `aws-sdk-go-v2/service/ssm` `GetParameter` or `aws-sdk-go-v2/service/secretsmanager` `GetSecretValue`. Significant rewrite.

### mailhog / maildev / mailpit (dev SMTP catcher)
- **Local**: `mailhog/mailhog`, `axllent/mailpit`, or `maildev/maildev`. Go: two paths. (a) **`net/smtp`** (Go stdlib — minimalist; you build the MIME envelope yourself or use small helpers). (b) **`go-gomail/gomail`** (community wrapper, MIME-friendly, attachments). Both point at port 1025 (mailhog) or whatever the catcher exposes.
- **Cheapest fit / Production fit**: SES — either via SMTP (point `net/smtp` or `gomail` at the SES SMTP endpoint with SES SMTP credentials) or via `aws-sdk-go-v2/service/sesv2` `SendEmail`.
- **Premium fit**: SES with Virtual Deliverability Manager + dedicated IP pool.
- **Decision criterion**: SES is the right call. For local dev keep mailhog and switch via env-driven SMTP host or `aws-sdk-go-v2` endpoint. SES requires production access approval (sandbox-to-prod) — non-trivial paperwork; budget a few days.
- **v4 framing (Tier 1, both options shown)**: per v4 §4, two named community options.
  - **`net/smtp`** (stdlib, no dependency): the most Go-idiomatic path. Same env-driven SMTP config works against SES SMTP endpoint and mailhog. Verbose MIME-construction is the cost.
  - **`gomail`**: community wrapper that handles MIME, attachments, HTML alternatives. Closest parallel to TS's `nodemailer` / Python's `smtplib` / Rust's `lettre`.
  - The `aws-sdk-go-v2/service/sesv2` path is an alternative when you need SES-specific features (templates, configuration sets); pick one approach per call site.

### prometheus / grafana
- **Local**: `prom/prometheus` + `grafana/grafana`. Go: `prometheus/client_golang` (the canonical Prometheus client; Prometheus itself is written in Go).
- **Cheapest fit**: CloudWatch Metrics (custom). Free tier 10 metrics. Use the embedded metric format (EMF) from logs to avoid PutMetricData costs.
- **Production fit**: Amazon Managed Prometheus (AMP) + Amazon Managed Grafana (AMG). Keeps the same query language (PromQL) and dashboards.
- **Premium fit**: AMP/AMG + extended retention + workspace federation.
- **Decision criterion**: If your team already lives in Prometheus/Grafana, AMP/AMG is the low-friction port. If you're starting fresh, CloudWatch Metrics is fine for <50 custom metrics; gets expensive past 1k.

### loki / jaeger / tempo (logs and traces)
- **Local**: `grafana/loki`, `jaegertracing/all-in-one`, `grafana/tempo`. Go: `log/slog` (Go 1.21+ stdlib structured logger — the modern default), `rs/zerolog` (zero-allocation), `uber-go/zap` (older, fast); `go.opentelemetry.io/otel/sdk/trace` + `otlptracegrpc` exporter.
- **Cheapest fit**: CloudWatch Logs (logs); X-Ray (traces) via OTel exporter.
- **Production fit**: CloudWatch Logs + X-Ray + Application Signals for service maps. Or push to Datadog/Honeycomb/Grafana Cloud via OTel collector if budget allows.
- **Premium fit**: AWS Distro for OpenTelemetry → AMP (metrics) + CloudWatch Logs + X-Ray.
- **Decision criterion**: OpenTelemetry is the abstraction that survives a vendor swap — instrument with OTel, choose backend separately. CloudWatch Logs gets expensive fast (>$50/GB ingested at scale).

---

## AI / LLM

This section reflects the Tier 1 pair classification in v4 §4 — `litellm`'s Go analogue is two options: `langchaingo` (broader provider coverage) or `eino` (CloudWeGo/ByteDance, newer typed-chain DSL).

### LLM provider abstraction (Bedrock + Ollama + others)
- **Local**: `ollama/ollama` (single-binary local LLM host, OpenAI-compatible HTTP API) or `vllm/vllm-openai` for GPU-backed serving. Go: two options shown.
  - (a) **`tmc/langchaingo`** — broader provider catalog (Bedrock, Ollama, OpenAI, Anthropic-direct, Cohere, Vertex, many others). Mirrors the Python LangChain abstraction.
  - (b) **`cloudwego/eino`** — newer (GA 2024), opinionated chain DSL, fewer providers, cleaner Go-idiomatic types.
- **Cheapest fit**: Ollama locally for dev; **Bedrock on-demand** for prod (Haiku ~$1/$5 per M tokens, Sonnet ~$3/$15, Opus ~$5/$25 — see `aws_service_groups.md` §29).
- **Production fit**: Bedrock on-demand + **Bedrock Provisioned Throughput** when sustained spend exceeds ~$5k/mo and predictability matters.
- **Premium fit**: Mixed routing — cheap models for cheap tasks, Opus for hard tasks; budget-aware fallbacks; spend limits per model. Both `langchaingo` and `eino` support routing patterns.
- **v4 framing (Tier 1)**: both libraries are named in v4 §4. Either provides a single API surface across Bedrock and Ollama — env-driven provider/model selects the backend.
  ```go
  // langchaingo path
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
- **Decision criterion**: `langchaingo` for breadth + Python-LangChain code-porting; `eino` for typed-graph composition and Go idiomaticity. Both maintained as of 2026.
- **v4 yaml**: `cloud_only: llm: { type: bedrock.llm, model: "anthropic.claude-opus-4-7-..." }` for the *provisioning marker* (IAM perms, throughput config). User code talks to `langchaingo` or `eino`; caravan just ensures the cloud-side identity has the right Bedrock policies attached and the model ID env var is injected.
- **Out of scope for either abstraction (remain `cloud_only` T2)**: Bedrock Knowledge Bases, Bedrock Agents, Bedrock Guardrails — AWS-orchestration services with no OSS equivalent. Either hit real AWS from local dev (mixed mode per v4 §4) or skip locally and test cloud-side.

### Vision / OCR (Rekognition + Textract)
- **Local**: ONNX Runtime Go bindings (`yalue/onnxruntime_go`) for CLIP / DETR / YOLO; `hybridgroup/gocv` (OpenCV bindings, CGO); `otiai10/gosseract` (Tesseract bindings, CGO) for OCR.
- **Cheapest fit**: Rekognition off-the-shelf APIs ($1 per 1k images for Labels, $1.50–$50 per 1k pages for Textract).
- **Production fit / Premium fit**: SageMaker hosting a fine-tuned model (when off-the-shelf accuracy isn't enough; Python-shaped training, cross-language seam).
- **v4 framing (Tier 1)**: vision is genuinely Tier 1 — same task, different model behind the API. No single Go community library hides the gap the way `langchaingo` / `eino` do for LLMs; the pattern is to wrap behind a small interface if you need to swap, or accept that local tests run a different model than prod. Note: CGO-requiring ML libs (`gocv`, `gosseract`) don't run in `FROM scratch` or `provided.al2023` static-binary Lambda — use a glibc/musl base image.

### Speech (Polly TTS, Transcribe STT)
- **Local**: `ggerganov/whisper.cpp` Go bindings (`whisper.cpp/bindings/go`, CGO) for STT. TTS: no first-class Go option; cross-language to Coqui-TTS / piper Python service.
- **Cheapest / Production fit**: Transcribe ($0.024/min batch), Polly Neural ($16 / M chars).
- **v4 framing (Tier 1)**: `whisper.cpp` Go bindings are the named Go option for STT (parallel to TS's `@xenova/transformers` Whisper.js). Output formats differ between Whisper.cpp and Transcribe (Whisper returns segments + text; Transcribe returns rich items); normalize at the boundary. CGO required — same trade-off as the vision libs.

---

## Summary table

| Local component (Go) | Cheapest fit | Production fit | v4 yaml / tier vocab | Tier |
|---|---|---|---|---|
| postgres (`pgx` / `database/sql` / `gorm` / `sqlc` / `ent` / `bun`) | RDS Postgres micro | Aurora Postgres Serverless v2 | `db.sql tier: dev | prod-small | prod | premium | global` | T0 |
| mysql/mariadb (`go-sql-driver/mysql`) | RDS MySQL micro | Aurora MySQL Serverless v2 | `db.sql engine: mysql tier: …` | T0 |
| mongodb (`mongo-driver`) | DocumentDB t4g.medium | DocumentDB cluster or Atlas-on-AWS | not a v4 primitive (use `cloud_only` or escape hatch) | T0 happy-path; partial overall |
| redis cache (`go-redis`) | ElastiCache micro | ElastiCache Serverless | `cache tier: …` (v1.x in v4 §6) | T0 |
| redis pubsub | ElastiCache | SNS+SQS (rewrite) | migrate to `topic:` + `queue:` | T0 after migration |
| memcached (`gomemcache`) | ElastiCache Memcached | Switch to Redis | (use `cache` primitive) | T0 |
| minio (`aws-sdk-go-v2/service/s3`) | S3 | S3 + Intelligent-Tiering | `bucket class: standard | intelligent | …` | T0 |
| opensearch (`opensearch-go/v3`) | OpenSearch t3.small | OpenSearch r6g cluster | not in v1 PoC; use `terraform-module` escape | T0 |
| pgvector (`pgvector-go`) | Aurora Postgres | Aurora Postgres | `db.sql` with `extensions: [vector]` | T0 |
| qdrant/weaviate/chroma | pgvector | OpenSearch k-NN or vendor cloud | not a v4 primitive | T1 if hand-rolled abstraction |
| sqlite (`mattn/go-sqlite3` / `modernc.org/sqlite`) | not a server | migrate to RDS Postgres | not a v4 primitive | n/a |
| rabbitmq (`amqp091-go`) | Amazon MQ micro | Amazon MQ cluster | `queue kind: standard | fifo` (after SQS migration) or DSN swap to Amazon MQ | T0 |
| kafka (`kafka-go` / `confluent-kafka-go`) | MSK Serverless ($540 floor) | MSK provisioned | not in v1; `terraform-module` or `cloud_only` | T0 wire / T0 IAM (Go signer mature) |
| nats (`nats.go`) | Self-host on Fargate | Self-host on EKS or migrate to SNS+SQS | no v4 primitive — self-host as a `service` | n/a |
| chi + `aws-lambda-go-api-proxy/chi` | Lambda + Function URL | Fargate behind ALB | `service shape: function | long-running` | one container, two shapes (v4 §3) |
| gin + `aws-lambda-go-api-proxy/gin` | Lambda + plugin | Fargate behind ALB | `service shape: function | long-running` | same |
| echo + `aws-lambda-go-api-proxy/echo` | Lambda + plugin | Fargate behind ALB | `service shape: function | long-running` | same |
| fiber + `aws-lambda-go-api-proxy/fiber` | Lambda + plugin | Fargate behind ALB | `service shape: function | long-running` | same |
| stdlib `net/http` + `httpadapter` | Lambda + plugin | Fargate behind ALB | `service shape: function | long-running` | same |
| asynq worker | Fargate + ElastiCache Redis | Fargate + SQS (raw `aws-sdk-go-v2/service/sqs`) | `service` + `trigger: { queue: jobs }` | T0 |
| river worker | Fargate + RDS Postgres | Same or migrate to SQS | `service` + Postgres-backed queue | T0 |
| robfig/cron (in-process) | EventBridge Scheduler → Lambda | Same | `triggers: <name>: { schedule: "0 2 * * *", target: worker }` | n/a (cron is a trigger attribute) |
| cron container | EventBridge Scheduler → Lambda or RunTask | Same | `triggers:` schedule | n/a |
| gRPC service (`grpc-go`) | Fargate + NLB | Fargate + NLB or ALB-HTTP/2 | `service expose: { port: …, protocol: grpc }` | n/a |
| nginx/traefik | API Gateway HTTP | ALB | `service expose: { port: …, public: true }` (ALB auto-derived) | n/a |
| keycloak | Cognito (first 10k MAU free) | Cognito or self-host Keycloak | Cognito user-lifecycle is `cloud_only`; token verify via `golang-jwt` + `keyfunc` JWKS | **T1 (`golang-jwt` + `keyfunc`)** |
| vault | SSM Parameter Store (free) | SSM + Secrets Manager + KMS | `secret:` primitive | T0 |
| mailhog/mailpit | SES | SES + dedicated IP | no primitive; `net/smtp` or `gomail` is the abstraction | **T1 (`net/smtp` or `gomail`)** |
| prometheus | CloudWatch Metrics (EMF) | Amazon Managed Prometheus | not in v1 PoC | n/a |
| grafana | CloudWatch dashboards | Amazon Managed Grafana | not in v1 PoC | n/a |
| loki | CloudWatch Logs | CloudWatch Logs | stdout JSON via `log/slog` (collected by runtime) | T0 |
| jaeger/tempo | X-Ray | X-Ray + Application Signals | OTel exporter env var | T0 (OTel) |
| **LLM (Bedrock/Ollama)** | Ollama locally + Bedrock Haiku in cloud | Bedrock Sonnet | `cloud_only: { type: bedrock.llm, model: ... }`; `langchaingo` or `eino` in code | **T1 (`langchaingo` or `eino`)** |
| **Vision (Rekognition/Textract)** | Rekognition off-the-shelf | SageMaker fine-tuned (cross-lang) | not in v1; small wrapper if you need swap | T1 |
| **Speech STT (Transcribe)** | Transcribe | same | not in v1; `whisper.cpp` Go bindings locally (CGO) | **T1 (`whisper.cpp/bindings/go`)** |
| **Speech TTS (Polly)** | Polly Neural | same | no first-class Go TTS; cross-language to Python | partial / cross-lang |

**Tier legend**: T0 = same wire API both sides, env-var swap (v4 §4). T1 = different wire APIs, community library bridges (`langchaingo` / `eino`, `golang-jwt` + `keyfunc`, `net/smtp` / `gomail`, `whisper.cpp` bindings). T2 = no local equivalent, `cloud_only:` in IR. See `go_api_diffs.md` for code snippets per pair and `caravan_abstraction_v4.md` §4 for the canonical T0/T1/T2 derivation.

---

See `mapping_aws_to_go.md` for the reverse direction (which container plays the AWS role in dev) and `go_api_diffs.md` for the per-pair Go code diff. Conceptual home: `thesis.md`. Long-form derivation: `caravan_abstraction_v4.md` (supersedes v3).
