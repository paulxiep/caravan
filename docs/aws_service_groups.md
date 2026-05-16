# AWS Service Role Groups — Scale, Latency, Cost

> **Snapshot date: 2026-05-16. Prices in us-east-1 USD unless noted. Re-verify on the AWS Pricing Calculator before quoting in a decision doc.**
>
> **Sources**: numbers come from training data through Jan 2026 plus targeted web verification (2026-05-16) for the most volatile services: Bedrock per-token pricing (Claude Opus/Sonnet/Haiku verified against `aws.amazon.com/bedrock/pricing` and `platform.claude.com`), S3 Vectors (verified against the late-2025 GA blog post), Aurora Serverless v2 Standard vs I/O-Optimized ACU rates, OpenSearch Serverless OCU minimums, MSK Serverless components, Lambda x86 vs ARM duration pricing, DynamoDB on-demand post-Nov-2024 50% reduction. Older / stable services (EC2, S3, RDS, SQS) are not web-re-verified for this snapshot.

This is the master catalog. Files 2–5 reference these groups by name.

## What a "role group" is

A **role group** is a slot in a typical application's architecture (e.g., "the place I put a key-value store") that several AWS services can plausibly fill. The point of grouping is that within a group the *interface to your code* is similar, while **scale ceiling, latency, and cost shape** differ enough that the choice matters.

Each group section has:
1. A per-service comparison table on three axes:
   - **Scale ceiling** — soft/hard limit per partition / table / region. The point at which you'd be forced to re-shard or migrate.
   - **Latency band** — p50 / p99 for a typical read or write. "Single-digit ms" means ≤10 ms p50.
   - **Cost shape** — what you pay *per* (per-request, per-hour, per-GB-month, per-ACU-hour) with a representative number.
2. A "when to pick which" note that captures the actual decision criteria.

Groups are listed in dependency order — compute first, then storage, then data services that need both.

Every per-service table below also carries a `Tier` column with one of `T0` / `T1` / `T2`. The rollup section that follows lifts those tags into three flat lists for at-a-glance reading; the Tier 2 deep-dive near the end of this file then sub-groups Tier 2 and judges each truly-niche entry on whether it earns a `cloud_only:` yaml shortcut or should be reached via the `terraform-module` escape hatch.

---

# At-a-glance tier rollup

The `T0 / T1 / T2` framing is load-bearing in `thesis.md` ("Service tiers" under Current evaluation) and `supeux_abstraction_v2.md` §4. Recap:

- **T0** — same wire API both sides; endpoint-URL or DSN env-var swap in user code suffices. No abstraction library required. Container-shaped compute primitives also sit here (one image, runs locally as docker-compose service or in cloud as Fargate / App Runner / Lambda).
- **T1** — different wire APIs cloud vs local; a structural abstraction layer is required (per the thesis stable design principle). Mature community libraries cover the well-known pairs (rig-core / litellm for LLMs; jsonwebtoken + JWKS for token verify; lettre / smtplib for email; whisper crates for STT; OpenCV / yolov8 for image analysis).
- **T2** — no OSS engine reproduces the service locally. `cloud_only:` provisioning marker. The Tier 2 deep-dive below sub-groups these, splits common from truly-niche, and per-niche-service decides between `yaml-registry`, `hand-tf`, and `skip`.

## T0 services (~22)

Compute primitives (run-the-container is the wire compat): Lambda standard runtime, ECS on EC2 / Fargate / Fargate Spot, EKS / EKS Fargate, App Runner, EC2 (on-demand / Spot / Reserved / Savings Plans), AWS Batch, Lightsail. Data plane (endpoint / DSN swap): S3 Standard + storage classes (Intelligent-Tiering, Standard-IA, One Zone-IA, Glacier Instant / Flexible / Deep Archive), EFS (via bind mount), EBS gp3 / gp2 / io2 / st1 / sc1, Instance Store, RDS Postgres / MySQL / MariaDB / SQL Server / Oracle, Aurora Postgres / MySQL (provisioned / Serverless v2 / Auto-Pause / I/O-Optimized), DynamoDB (provisioned + on-demand), DocumentDB (with false-positive caveat), Keyspaces, ElastiCache Redis (all flavors), ElastiCache Memcached, MemoryDB, Timestream for InfluxDB, OpenSearch Service (provisioned / Serverless / k-NN), Aurora pgvector, SQS (Standard + FIFO + Extended Client), Amazon MQ (RabbitMQ + ActiveMQ), SNS Standard topic, Kinesis Data Streams (provisioned + on-demand), Kinesis Firehose, MSK (provisioned / Serverless / Connect), ALB / NLB / GWLB (locally use nginx / traefik or route directly), Route 53 public + private + health checks (coredns or /etc/hosts locally), Verified Permissions (Cedar — the engine *is* OSS), Secrets Manager, SSM Parameter Store (Standard + Advanced), AppConfig, KMS (software keys), Step Functions Standard + Express (aws-stepfunctions-local), EventBridge default bus + Scheduler, MWAA (real Airflow), CloudWatch Logs (stdout / Live Tail / Insights), CloudWatch Metrics (custom + EMF), IoT Core MQTT + HTTPS / WS (mosquitto), Greengrass V2, FreeRTOS, Redshift (postgres wire), Athena (Trino / Presto), Glue Jobs (Spark) + Data Catalog (Hive Metastore), EMR (on EC2 / Serverless / on EKS / Studio).

## T1 services (~5 hard pairs)

Each pair requires a structural abstraction; community libraries cover all of them today:

- **LLM** (Bedrock — Claude / Llama / Mistral / Cohere / Nova / Titan + Provisioned Throughput) ↔ Ollama / vLLM. Bridge: rig-core (Rust), litellm (Python), langchaingo / eino (Go), Vercel AI SDK (TS).
- **Token verification** (Cognito User Pool JWKS) ↔ local JWT issuer. Bridge: jsonwebtoken (Rust), authlib / python-jose (Python), golang-jwt (Go), jose (TS).
- **Email** (SES API or SMTP submission) ↔ MailHog / Mailpit SMTP catcher. Bridge: lettre (Rust), smtplib (Python), gomail (Go), nodemailer (TS) — or aws-sdk-ses* on the cloud side.
- **Speech-to-text** (Transcribe / Transcribe Medical / Call Analytics) ↔ Whisper. Bridge: whisper-rs (Rust), openai-whisper (Python), similar elsewhere.
- **Image analysis / OCR** (Rekognition Image + Video + Custom Labels + Faces; Textract) ↔ OpenCV / YOLOv8 / Tesseract + layoutparser. Bridge: per-language community libraries.

Also Polly (TTS) ↔ coqui-ai / piper; Comprehend ↔ spaCy; Translate ↔ argos-translate sit at the T1 edge — covered by community libraries, classified here when the user actually swaps the implementation per environment.

## T2 services (~30)

Every service tagged `T2` in the role-group tables below. Sub-grouped, split common-vs-niche, and judged for yaml-registry fit in the **Tier 2 deep-dive** section near the end of this file.

Headline list (canonical bucket in parens; full breakdown in the deep-dive):

- **Edge / CDN**: CloudFront, CloudFront Functions, Lambda@Edge, Global Accelerator.
- **Multi-region coordination**: Aurora DSQL, DynamoDB Global Tables, Step Functions Distributed Map.
- **Managed ML / AI orchestration**: Bedrock Knowledge Bases, Bedrock Agents, Bedrock Guardrails, SageMaker JumpStart, SageMaker Canvas, Forecast, Personalize, Kendra.
- **IoT vertical**: IoT Device Management, IoT Device Defender, IoT Analytics, IoT SiteWise, IoT TwinMaker, IoT FleetWise, IoT Events, IoT Rules Engine, IoT ExpressLink.
- **Specialty storage variants**: S3 Express One Zone, S3 Vectors, S3 Object Lambda, S3 Select, DAX, Neptune Analytics, Timestream LiveAnalytics, FSx (Lustre / Windows / ONTAP / OpenZFS), Redshift Spectrum, Aurora zero-ETL → Redshift.
- **Identity / lifecycle / observability lock-in**: Cognito User Pools (user *lifecycle* admin), Cognito Identity Pools, Cognito Hosted UI, IAM enforcement, IAM Identity Center, CloudWatch RUM, CloudWatch Synthetics, CloudWatch Application Signals, CloudWatch Alarms (triggering), CloudTrail, X-Ray (visualization), SNS Mobile Push, SNS SMS, SES Virtual Deliverability Manager, Pinpoint / End User Messaging.
- **API edge / orchestration lock-in**: API Gateway REST + HTTP + WebSocket, AppSync, EventBridge Pipes, Route 53 Resolver.
- **Data / BI lock-in**: AWS Clean Rooms, Glue Crawlers, Glue DataBrew, Lake Formation, QuickSight (Enterprise + Q + Generative + Embedded), S3 Select.
- **Deprecated / end-of-life**: SWF, CloudSearch, Forecast.

See **§Tier 2 deep-dive** below for each truly-niche entry's use case and practicality verdict (`yaml-registry` / `hand-tf` / `skip`).

---

# Web Stack Core (22 groups)

## 1. Compute — Function (Lambda)

| Service | Scale ceiling | Latency | Cost shape | Tier |
|---|---|---|---|---|
| **Lambda (x86)** | 1,000 concurrent / region default; up to 100k+ by request | Cold start 100–500 ms; warm 1–10 ms invocation overhead | $0.20 / M req + $0.0000166667 / GB-s | T0 |
| **Lambda (arm64 / Graviton2)** | Same as x86 | ~20% better than x86 perf | Same request price; $0.0000133334 / GB-s duration (20% off duration only) | T0 |
| **Lambda SnapStart (Java/.NET/Python)** | Same | Cold start 100–300 ms (down from 5–10 s on Java) | Same as Lambda + caching surcharge | T2 |
| **Lambda@Edge** | 10k concurrent / region; runs at 600+ POPs | Origin-region latency + 50 ms typical | $0.60 / 1M req + $0.00005001 / GB-s | T2 |
| **Lambda Function URL** | Same as Lambda; bypasses API Gateway | 1 ms less than via APIGW | Free (Lambda cost only) | T0 |

**When to pick which**: Default to Lambda arm64 — 20% cheaper, same code in most cases. Use SnapStart only if Java/.NET cold starts are a real problem (Python cold starts are usually fine without it). Lambda@Edge only for genuine edge work (rewrite headers, A/B at CDN); 3× the cost and 4 KB payload limit. Function URL when you don't need APIGW features (custom domain via CloudFront, throttling, auth) — saves $3.50 / 1M req.

**Hard limits worth knowing**: 15-min max execution, 10 GB max memory, 250 MB unzipped package (10 GB via container image), 6 MB sync payload, 256 KB async payload, /tmp 512 MB default (up to 10 GB configurable).

---

## 2. Compute — Container

| Service | Scale ceiling | Latency | Cost shape | Tier |
|---|---|---|---|---|
| **ECS on EC2** | Bounded by EC2 fleet; thousands of tasks per cluster | Warm — depends on app | EC2 hourly + free orchestration | T0 |
| **ECS on Fargate** | 1,000 tasks / service default; 5,000 per region soft | Task start 30–60 s | $0.04048 / vCPU-hr + $0.004445 / GB-hr | T0 |
| **ECS on Fargate Spot** | Same | Same | ~70% off Fargate; can be reclaimed with 2-min warning | T0 |
| **EKS (managed K8s)** | 100 nodes / cluster default (request more); 750 pods / node | App-dependent | $0.10 / cluster-hr + node compute | T0 |
| **EKS on Fargate** | Same as ECS Fargate | Same | Same as ECS Fargate | T0 |
| **App Runner** | Auto-scales 1–25 instances default | Warm 1–10 ms; cold ~1 s | $0.064 / vCPU-hr + $0.007 / GB-hr (active); $0.007 / GB-hr (idle) | T0 |

**When to pick which**: App Runner for "I have a Dockerfile, give me a URL" — most managed, least configurable. ECS Fargate for batch and long-running services where you want container semantics without managing nodes. ECS on EC2 when you have steady high utilization (Fargate is ~30% premium over EC2 baseline). EKS only if you need real Kubernetes (Helm charts you can't rewrite, Istio, k8s-native operators) or multi-cloud portability. EKS Fargate combines worst of both pricing models — avoid unless mandated.

**Pricing gotcha**: ECS/EKS Fargate bill per-second after a 1-min minimum. Cold-start economics matter for spiky workloads.

---

## 3. Compute — VM / Batch

| Service | Scale ceiling | Latency | Cost shape | Tier |
|---|---|---|---|---|
| **EC2 on-demand** | Per-instance-family quotas (vCPU-based); easily 10k+ vCPUs by request | Boot 30 s–2 min | $0.0416 / hr (t3.medium) → $30+ / hr (large GPU) | T0 |
| **EC2 Spot** | Same; uses spare capacity | Same | Up to 90% off on-demand; 2-min interruption warning | T0 |
| **EC2 Reserved (1y / 3y)** | Same | Same | 40–60% off on-demand for 1y; 60–75% for 3y | T0 |
| **EC2 Savings Plans** | Same | Same | Similar to RI but more flexible (compute SP covers Fargate + Lambda too) | T0 |
| **AWS Batch** | Manages EC2/Fargate/Spot fleets for jobs | Job pickup 30 s–2 min | Underlying compute cost only (Batch orchestration free) | T0 |
| **Lightsail** | Fixed bundles, max 1 TB attached storage | Same as EC2 | $3.50 / mo (512 MB) → $160 / mo (32 GB) — includes transfer | T0 |

**When to pick which**: EC2 on-demand for fresh experiments. EC2 Spot for fault-tolerant batch + dev environments (CI runners, training jobs that checkpoint). 3-year Savings Plans once your steady-state baseline is provably stable (>50% utilization for >12 months). Batch for "I have a queue of jobs, run them" — the cheapest way to get fan-out without orchestration code. Lightsail for hobby/portfolio sites where the predictable bundle pricing wins on simplicity.

---

## 4. Storage — Object

| Service | Scale ceiling | Latency | Cost shape (per GB-month / per 1k req) | Tier |
|---|---|---|---|---|
| **S3 Standard** | Unlimited; 5 TB / object; 3,500 PUT or 5,500 GET / sec per prefix | First-byte 100–200 ms | $0.023 / GB; $0.005 PUT / $0.0004 GET | T0 |
| **S3 Intelligent-Tiering** | Same | Same as Standard for active tier; +50 ms for IA tiers | $0.023 / GB + $0.0025 / 1k obj monitoring | T0 |
| **S3 Standard-IA** | Same | Same as Standard | $0.0125 / GB; $0.01 PUT / $0.001 GET + $0.01 / GB retrieval | T0 |
| **S3 One Zone-IA** | Same; single AZ | Same | $0.01 / GB (20% cheaper than S-IA) | T0 |
| **S3 Glacier Instant Retrieval** | Same | Same as Standard (ms!) | $0.004 / GB + $0.03 / GB retrieval | T0 |
| **S3 Glacier Flexible Retrieval** | Same | Minutes to hours retrieval | $0.0036 / GB + retrieval fee | T0 |
| **S3 Glacier Deep Archive** | Same | 12 hr retrieval | $0.00099 / GB + retrieval fee (cheapest cloud storage on earth) | T0 |
| **S3 Express One Zone (Directory bucket)** | High RPS within one AZ | <10 ms p99 (10× faster than Standard) | $0.16 / GB (7× pricier) + cheaper requests | T2 |

**When to pick which**: Standard for hot data <30 days. Intelligent-Tiering when you can't predict access patterns and have >128 KB objects. S-IA for known-cold backup-type data. Glacier IR for compliance archives you'd rarely touch but need fast when you do. Deep Archive for "I will probably never read this but legal says keep it 7 years." Express One Zone for high-RPS ML training data or analytics shuffle — only worth it if you're hitting Standard's per-prefix throughput ceiling.

**Hidden costs**: cross-region replication ($0.02 / GB + transfer), versioning (stores every version), and `LIST` operations are 12.5× the cost of `GET`. Lifecycle transitions cost per-object — don't transition a billion tiny files.

---

## 5. Storage — File

| Service | Scale ceiling | Latency | Cost shape | Tier |
|---|---|---|---|---|
| **EFS Standard** | Petabytes; thousands of concurrent NFS clients | ms (single-digit on bursting; sub-ms with provisioned) | $0.30 / GB-mo + throughput | T0 |
| **EFS IA** | Same | ~50 ms first byte | $0.025 / GB-mo | T0 |
| **EFS One Zone** | Same; single AZ | Same | $0.16 / GB-mo (47% cheaper) | T0 |
| **FSx for Lustre** | TB/sec throughput at scale | sub-ms | $0.145 / GB-mo (persistent SSD) | T2 |
| **FSx for Windows File Server (SMB)** | Multi-AZ; AD-integrated | sub-ms | $0.13 / GB-mo (SSD multi-AZ) | T2 |
| **FSx for NetApp ONTAP** | Multi-protocol (NFS+SMB+iSCSI), snapshots, dedup | sub-ms | $0.144 / GB-mo (SSD) + $0.024 / GB-mo (capacity pool) | T2 |
| **FSx for OpenZFS** | NFS; ZFS snapshots/clones | sub-ms | $0.090 / GB-mo (SSD) | T2 |

**When to pick which**: EFS for "Linux containers / Lambda need shared POSIX storage." FSx Lustre when you're running ML training or HPC that needs >1 GB/s per client. FSx Windows for Windows file shares (AD-joined SMB). FSx ONTAP if you need NetApp-specific features (SnapMirror to on-prem). FSx OpenZFS for the cheapest "real filesystem" with cheap clones. Never use EFS as a database substitute — it's slow for many small files.

---

## 6. Storage — Block

| Service | Scale ceiling | Latency | Cost shape | Tier |
|---|---|---|---|---|
| **EBS gp3** | 16 TB / volume; 16,000 IOPS baseline (provisionable to 16k–64k) | sub-ms | $0.08 / GB-mo + provisioned IOPS/throughput | T0 |
| **EBS gp2** | 16 TB / volume; IOPS = 3 × GB (max 16k) | sub-ms | $0.10 / GB-mo (legacy — prefer gp3) | T0 |
| **EBS io2 Block Express** | 64 TB / volume; 256,000 IOPS / 4 GB/s | sub-ms; 99.999% durability | $0.125 / GB-mo + $0.065 / IOPS-mo (high) | T0 |
| **EBS st1 (throughput HDD)** | 16 TB; 500 MB/s burst | ms (sequential good, random bad) | $0.045 / GB-mo | T0 |
| **EBS sc1 (cold HDD)** | 16 TB; 250 MB/s burst | ms | $0.015 / GB-mo | T0 |
| **Instance Store (NVMe)** | Per-instance (e.g., i4i.32xlarge: 8 × 3.75 TB) | μs | Bundled with instance | T0 |

**When to pick which**: gp3 by default — explicit IOPS provisioning beats gp2's "3 IOPS per GB" coupling. io2 BX only for write-heavy OLTP that genuinely needs >16k IOPS sustained. st1/sc1 for log/data archive volumes. Instance store for Cassandra/Redis-style "shard data, instance is replaceable" patterns — data dies with the instance.

---

## 7. Database — RDBMS

| Service | Scale ceiling | Latency | Cost shape | Tier |
|---|---|---|---|---|
| **RDS Postgres / MySQL / MariaDB** | 64 TiB storage; vertical-only to db.x2iedn.32xlarge (128 vCPU, 4 TB RAM) | sub-ms intra-AZ; +1–2 ms multi-AZ | $0.017 / hr (db.t4g.micro) → $20+ / hr (large multi-AZ); + storage | T0 |
| **RDS for SQL Server / Oracle** | Same as above | Same | + license (≈3–5× community engine cost) | T0 |
| **Aurora Postgres / MySQL** | 128 TiB; 15 read replicas; cluster auto-fails | sub-ms intra-AZ; <100 ms failover | $0.10 / ACU-hr (Aurora) or $0.029 / hr (db.t4g.medium provisioned) + $0.10 / GB-mo + $0.20 / 1M I/O | T0 |
| **Aurora Serverless v2 (Standard)** | Same as Aurora; scales 0.5–256 ACUs | ms (no cold start; min 0.5 ACU) | $0.12 / ACU-hr (1 ACU ≈ 2 GB RAM); + I/O charges | T0 |
| **Aurora Serverless v2 (I/O-Optimized)** | Same | ms | $0.156 / ACU-hr (30% premium); **zero I/O charges** — break-even ~25% I/O-spend ratio | T0 |
| **Aurora Serverless v2 Auto-Pause** | Scales to 0 after inactivity | Resume in ~15 s | $0 idle; $0.12 / ACU-hr active | T0 |
| **Aurora DSQL (multi-region active-active)** | Multi-region, virtually unlimited | <10 ms regional read; cross-region writes coordinated | Per-DPU + storage; positioned premium | T2 |

**When to pick which**: RDS for "I just want managed Postgres at reasonable cost." Aurora provisioned for high-throughput single-region apps that need read replicas + faster failover. Aurora Serverless v2 for spiky workloads — better than provisioned when sustained ACU < provisioned cost equivalent. Aurora Serverless v2 auto-pause for dev/test envs that idle. Aurora DSQL only if multi-region active-active is a hard requirement (regulated industries, latency-sensitive global apps) — it's expensive and SQL-feature-limited.

**Common pitfall**: Aurora I/O cost ($0.20/M) can dominate the bill for OLTP. Aurora I/O-Optimized cluster config trades higher ACU cost for free I/O — break-even ~25% I/O-spend ratio.

---

## 8. Database — KV / Document NoSQL

| Service | Scale ceiling | Latency | Cost shape | Tier |
|---|---|---|---|---|
| **DynamoDB (provisioned)** | 40k RCU/WCU per table soft; partition-key based; petabyte tables exist | single-digit ms p99 | $0.00013 / RCU-hr + $0.00065 / WCU-hr + $0.25 / GB-mo | T0 |
| **DynamoDB (on-demand)** | Auto-scales; ~2× sustained traffic spike absorbed | single-digit ms p99 | $0.125 / M reads + $0.625 / M writes (eventually-consistent reads half-price) | T0 |
| **DynamoDB Global Tables** | Multi-region active-active | sub-second cross-region replication | 2× on-demand cost (per region) + cross-region transfer | T2 |
| **DAX (DynamoDB cache)** | In front of DynamoDB | microseconds (cache hit) | $0.04 / hr (dax.t3.small) → expensive at scale | T2 |
| **DocumentDB (MongoDB-compatible)** | 64 TiB; up to 15 replicas | single-digit ms | $0.10 / instance-hr (db.t3.medium) + $0.10 / GB-mo + I/O | T0 |
| **Keyspaces (Cassandra-compatible)** | Petabyte tables; serverless | single-digit ms | $1.45 / M writes + $0.29 / M reads + $0.30 / GB-mo (on-demand) | T0 |

**When to pick which**: DynamoDB on-demand for unpredictable traffic and most greenfield apps — pricing simpler, no capacity planning. DynamoDB provisioned for steady high-throughput where you've measured RCU/WCU and can buy reserved capacity (up to 77% off). DAX only if you've measured single-digit-ms isn't enough and you can absorb its single-AZ-per-shard fragility. DocumentDB if you have a MongoDB-shaped app and don't want to self-host (note: not 100% MongoDB API compatible, especially aggregations). Keyspaces if you have existing Cassandra code — almost never the right greenfield choice.

**Trap**: DynamoDB "hot partition" — a single partition can only sustain 1,000 WCU. If your access pattern concentrates on one key, you'll throttle even at low total RPS.

---

## 9. Database — Cache

| Service | Scale ceiling | Latency | Cost shape | Tier |
|---|---|---|---|---|
| **ElastiCache for Redis (cluster mode disabled)** | 500 GB / shard; replicas for read scale | sub-ms | $0.017 / hr (cache.t4g.micro) → $5+ / hr (r7g.16xlarge) | T0 |
| **ElastiCache for Redis (cluster mode enabled)** | 500 nodes × 500 GB = 250 TB | sub-ms | Same per-node pricing; sharding is free | T0 |
| **ElastiCache Serverless (Redis)** | Auto-scales; max 5 TB per cache | sub-ms | $0.125 / GB-hr stored + $0.0034 / M ECPUs | T0 |
| **ElastiCache for Memcached** | 40 nodes / cluster | sub-ms | Same per-node pricing | T0 |
| **MemoryDB for Redis** | Durable Redis (multi-AZ transactional log); 500 GB / shard | sub-ms write; ms-level read | $0.108 / hr (db.t4g.small) — ~6× ElastiCache equivalent | T0 |
| **DAX** | DynamoDB-specific (see group 8) | μs | $0.04 / hr (dax.t3.small) | T2 |

**When to pick which**: ElastiCache Serverless for new apps — no capacity planning. ElastiCache provisioned cluster-mode-enabled when sustained usage and you can pre-shard. MemoryDB when you need Redis as a *primary* durable store (not just a cache) — replaces "Redis + write-behind-to-DB" pattern. Memcached almost never the right choice in 2026 — Redis does everything Memcached does and more.

---

## 10. Database — Time-series / Graph

| Service | Scale ceiling | Latency | Cost shape | Tier |
|---|---|---|---|---|
| **Timestream for LiveAnalytics** | Trillions of events; auto-tiers memory→magnetic | ms write; SQL query latency varies | $0.50 / M writes + $0.036 / GB-hr memory + $0.03 / GB-mo magnetic + $0.01 / GB scanned | T2 |
| **Timestream for InfluxDB** | Up to 16 TB / instance | ms | $0.27 / hr (db.influx.medium) — instance-based | T0 |
| **Neptune** | 64 TiB; 15 read replicas | ms | $0.10 / hr (db.t4g.medium) + $0.10 / GB-mo + I/O | T0 |
| **Neptune Serverless** | Same data limits; scales 1–128 NCUs | ms | $0.1608 / NCU-hr | T0 |
| **Neptune Analytics** | In-memory graph queries on large graphs | seconds for analytics queries | $0.48 / m-NCU-hr | T2 |

**When to pick which**: Timestream LiveAnalytics for high-volume IoT/observability metrics where you want managed and SQL. Timestream InfluxDB if you have existing InfluxDB code/dashboards. Neptune for "I have a graph problem" (knowledge graphs, recommendations, fraud rings). Neptune Analytics for *analytical* graph queries (centrality, paths) — different engine than transactional Neptune.

---

## 11. Database — Search / Vector

| Service | Scale ceiling | Latency | Cost shape | Tier |
|---|---|---|---|---|
| **OpenSearch Service (provisioned)** | 3 PB; max ~200 nodes / domain | ms search latency typical | $0.10 / hr (t3.small.search) → $4+ / hr (r6gd.16xl); + EBS | T0 |
| **OpenSearch Serverless** | Auto-scales OCU; data + indexing pools | ms | $0.24 / OCU-hr; prod min 2 OCUs ≈ $350/mo (1 indexing + 1 search); dev/test no-redundancy 1 OCU ≈ $175/mo | T0 |
| **OpenSearch — k-NN / vector** | Same as OpenSearch | 10–100 ms vector query | Same as OpenSearch + memory-hungry | T0 |
| **Aurora pgvector** | Same as Aurora Postgres | 10–100 ms ANN query | Same as Aurora Postgres | T0 |
| **S3 Vectors** | GA late-2025; 14 regions; tens of billions of vectors per index | 100–500 ms query | $0.06 / GB-mo storage + $0.20 / GB PUT + $2.50 / M queries + per-TB scan charge (declines past 100k vectors) | T2 |
| **Kendra** | Up to 5M docs (Enterprise); fully-managed enterprise search | sub-second | $1.125 / hr (Developer) → $7+ / hr (Enterprise) | T2 |
| **CloudSearch** | Legacy; not for new builds | ms | $0.10 / hr — deprecated track | T2 |

**When to pick which**: OpenSearch provisioned when you have steady traffic and want full control. OpenSearch Serverless if you'll regret capacity planning more than the ~$350/mo prod floor — but small/steady workloads should NOT use Serverless (provisioned t3.small is $73/mo). pgvector when you already run Postgres and your vector count is <10M — saves an entire system. S3 Vectors for "I have billions of vectors, query latency is not interactive" (batch RAG indexing, embedding archives) — cheapest cold vector storage by far. Kendra only for budget-insensitive enterprise search with permissioned content — almost always overkill.

---

## 12. Messaging — Queue

| Service | Scale ceiling | Latency | Cost shape | Tier |
|---|---|---|---|---|
| **SQS Standard** | Unlimited throughput; at-least-once | 10–100 ms typical | $0.40 / M req (free tier 1M/mo); first 256 KB | T0 |
| **SQS FIFO** | 3,000 msg/sec batched, 300 unbatched per message group | 10–100 ms | $0.50 / M req | T0 |
| **SQS Extended Client (large payloads)** | Up to 2 GB via S3 indirection | +S3 round trip | SQS + S3 standard | T0 |
| **Amazon MQ — RabbitMQ** | 100k msg/sec depending on broker size | sub-ms intra-AZ | $0.08 / hr (mq.t3.micro) → $1+ / hr; + storage | T0 |
| **Amazon MQ — ActiveMQ** | Similar | sub-ms | Same as MQ RabbitMQ | T0 |

**When to pick which**: SQS Standard for almost every greenfield queue need — cheapest, most scalable, simplest. SQS FIFO when you genuinely need ordering or exactly-once (and accept the 3k msg/sec/group limit). Amazon MQ only when migrating *existing* RabbitMQ or ActiveMQ code that uses AMQP/JMS features SQS lacks (priorities, topic exchanges with bindings, JMS message selectors).

---

## 13. Messaging — Pub/Sub & Event Bus

| Service | Scale ceiling | Latency | Cost shape | Tier |
|---|---|---|---|---|
| **SNS Standard topic** | Unlimited throughput | <100 ms | $0.50 / M publishes + per-protocol delivery (e.g., $2.00 / 100k SMS, $0.50 / M HTTP) | T0 |
| **SNS FIFO topic** | 300 publishes/sec/message-group | <100 ms | $0.30 / M publishes + $0.017 / GB | T0 |
| **SNS Mobile Push** | iOS/Android/Web push | varies by APNs/FCM | $0.50 / M publishes + $0.50 / M deliveries | T2 |
| **EventBridge default bus** | 10k events/sec soft | <500 ms | $1.00 / M events (custom + 3rd-party); AWS events free | T0 |
| **EventBridge Pipes** | Source → filter → target | <500 ms | $0.40 / M events processed + $0.20 / M for enrichment | T2 |
| **EventBridge Scheduler** | 1M schedules / account; cron + one-time | second-precision | $1.00 / M invocations | T0 |

**When to pick which**: SNS for fan-out to a small set of known subscribers (Lambda + SQS + HTTP webhook). EventBridge when the routing is content-based (rule patterns), you want SaaS event sources, or you want a schema registry. EventBridge Pipes for source-to-target wiring without writing a Lambda (e.g., DynamoDB Stream → filter → SQS). EventBridge Scheduler always over old-style EventBridge Rules for cron — explicit, more limits, better dead-letter.

**Often-missed**: SNS-to-SQS *fan-out* is the canonical pattern when you have N consumers needing the same event with per-consumer retry — single SNS topic, one SQS queue per consumer.

---

## 14. Messaging — Streaming

| Service | Scale ceiling | Latency | Cost shape | Tier |
|---|---|---|---|---|
| **Kinesis Data Streams (provisioned)** | 1 MB/s in, 2 MB/s out per shard | <70 ms p99 | $0.015 / shard-hr + $0.014 / M PUT | T0 |
| **Kinesis Data Streams (on-demand)** | Auto-scales to 200 MB/s in default | <70 ms | $0.04 / hr base + $0.04 / GB ingested + $0.013 / GB egress | T0 |
| **Kinesis Firehose** | 5 MB/s per shard delivery to S3/Redshift/OpenSearch | seconds to minutes (buffer-flush) | $0.029 / GB ingested + format conversion | T0 |
| **MSK (managed Kafka) provisioned** | Cluster-sized; petabyte scale | <10 ms | $0.21 / hr (kafka.m7g.large) → $4+ / hr; + storage | T0 |
| **MSK Serverless** | Auto-scales partitions | <10 ms | $0.75 / cluster-hr + $0.0015 / partition-hr + $0.10 / GB in + $0.10 / GB out + $0.10 / GB-mo storage | T0 |
| **MSK Connect** | Kafka Connect managed | depends on connector | $0.11 / MCU-hr | T0 |

**When to pick which**: Kinesis Data Streams on-demand for new AWS-native pipelines — simpler than partition planning. Firehose specifically when the destination is S3/Redshift/OpenSearch and you can tolerate buffer-flush latency (>5 sec). MSK when you need real Kafka semantics (consumer groups with re-balancing, exactly-once via transactions, broad Kafka ecosystem tooling) — but it's significantly pricier than Kinesis. MSK Serverless for variable-throughput Kafka workloads; the $540/mo cluster floor is steep.

---

## 15. API / Web Edge

| Service | Scale ceiling | Latency | Cost shape | Tier |
|---|---|---|---|---|
| **API Gateway REST** | 10k RPS / account default | +20–50 ms overhead | $3.50 / M req + data transfer | T2 |
| **API Gateway HTTP** | 10k RPS / account default | +5–10 ms overhead | $1.00 / M req (≤300M); cheaper above | T2 |
| **API Gateway WebSocket** | 500 new conns/sec/account; 100k concurrent default | <100 ms | $1.00 / M messages + $0.25 / M connection-mins | T2 |
| **AppSync (GraphQL)** | 2k req/sec/API default | depends on resolvers | $4.00 / M queries + $2.00 / M real-time updates | T2 |
| **ALB (Application LB)** | Auto-scales; HTTP/HTTPS L7 | <10 ms LB overhead | $0.0225 / hr + $0.008 / LCU-hr | T0 |
| **NLB (Network LB)** | Millions of req/sec; TCP/UDP L4 | <1 ms LB overhead | $0.0225 / hr + $0.006 / NLCU-hr | T0 |
| **GWLB (Gateway LB)** | For 3rd-party firewalls / NVAs | <1 ms | $0.0125 / hr + $0.004 / GLCU-hr | T0 |

**When to pick which**: API Gateway HTTP for almost all new REST APIs — 70% cheaper than REST, fewer features but enough for most. API Gateway REST only if you need request validators, SDK generation, edge-optimized endpoints, or AWS WAF tight integration (now available on HTTP too). API Gateway WebSocket for chat / real-time apps with Lambda backends. AppSync if your team has GraphQL conviction. ALB for Fargate/ECS/EC2 HTTP services. NLB for gRPC, MQTT, or anything not HTTP.

**Often-overlooked**: An ALB with a Lambda target is often cheaper than API Gateway HTTP above ~30 RPS sustained (ALB hourly is fixed, APIGW HTTP is per-request).

---

## 16. CDN

| Service | Scale ceiling | Latency | Cost shape | Tier |
|---|---|---|---|---|
| **CloudFront** | Global; tens of TB/sec aggregate | <50 ms edge fetch | $0.085 / GB (first 10 TB to N. America); $0.0075 / 10k HTTPS req | T2 |
| **CloudFront Functions** | 10 ms max execution; viewer request/response | <1 ms | $0.10 / M invocations | T2 |
| **Lambda@Edge** | 5 sec viewer / 30 sec origin; full Node/Python | 50 ms typical | $0.60 / M req + $0.00005001 / GB-s | T2 |
| **Global Accelerator** | Anycast IPs over AWS backbone | shaves 10–30% of origin RTT | $0.025 / hr endpoint + $0.015 / GB inbound | T2 |

**When to pick which**: CloudFront for static assets and HTTP origin caching. CloudFront Functions for trivial header/URL rewrites (cheap, fast). Lambda@Edge when you need real logic at the edge (A/B routing, dynamic auth) — much pricier. Global Accelerator for TCP/UDP workloads that need anycast IPs (gaming, VoIP, multi-region failover). For static-asset CDN purposes, CloudFront's free outbound to internet on first 1 TB/mo is the best bargain in cloud.

---

## 17. DNS

| Service | Scale ceiling | Latency | Cost shape | Tier |
|---|---|---|---|---|
| **Route 53 hosted zones (public)** | Unlimited records / zone | global anycast resolver, <30 ms | $0.50 / zone-mo (first 25); + $0.40 / M queries | T0 |
| **Route 53 hosted zones (private)** | VPC-scoped | <10 ms intra-VPC | $0.50 / zone-mo; + $0.40 / M queries | T0 |
| **Route 53 Resolver (inbound/outbound)** | For VPC ↔ on-prem DNS | <10 ms | $0.125 / ENI-hr | T2 |
| **Route 53 health checks** | Per endpoint | 30-sec or 10-sec interval | $0.50 / check-mo (basic) → $2.00 (with HTTPS + string match) | T0 |

**When to pick which**: Route 53 for almost all AWS-hosted apps. Standard records via aliases to AWS resources (ALB, CloudFront, S3) are *free* (no query charge for ALIAS resolved within AWS). Private zones for service-discovery within VPC. Cloudflare or other DNS only if you have non-AWS reasons (DDoS mitigation, app-layer features Cloudflare offers).

---

## 18. Identity / Auth

| Service | Scale ceiling | Latency | Cost shape | Tier |
|---|---|---|---|---|
| **Cognito User Pools** | 40M MAUs supported per pool | <100 ms typical | First 10k MAU free; $0.0055 / MAU (10k–100k); cheaper above | T2 (lifecycle); T1 for JWKS token verify via community libs |
| **Cognito Identity Pools** | Same | <100 ms | Free (only AWS API cost for the resulting credentials) | T2 |
| **Cognito Hosted UI** | Same | <500 ms initial | Same as User Pools | T2 |
| **IAM** | Unlimited users; 5k roles / account; 10k policies | API <100 ms | Free (the resource) | T2 (enforcement is AWS-only) |
| **IAM Identity Center (SSO)** | Enterprise SSO over IAM | <500 ms | Free | T2 |
| **Verified Permissions (Cedar)** | Authorization (not auth*entication*) | ms | $0.00015 / authz request + $0.10 / 1k policies stored | T0 (Cedar engine is OSS) |

**When to pick which**: Cognito for B2C apps and "I need users to sign up with email/google/etc." IAM Identity Center for workforce SSO. Verified Permissions when authz is non-trivial and rule-based (multi-tenant SaaS, complex sharing). Cognito has rough edges (no built-in MFA recovery UX, awkward custom attribute story) — many teams bail to Auth0 or self-hosted Keycloak.

---

## 19. Secrets / Config

| Service | Scale ceiling | Latency | Cost shape | Tier |
|---|---|---|---|---|
| **Secrets Manager** | 500k secrets / region; 64 KB / secret | <100 ms cached | $0.40 / secret-mo + $0.05 / 10k API calls | T0 |
| **SSM Parameter Store (Standard)** | 10k parameters / account; 4 KB / param | <100 ms | Free | T0 |
| **SSM Parameter Store (Advanced)** | 100k parameters; 8 KB / param | <100 ms | $0.05 / parameter-mo + $0.05 / 10k API | T0 |
| **AppConfig** | Config delivery with deploy strategies | <100 ms via SDK cache | $0.0008 / get + $0.0002 / replication + freeform cost | T0 |
| **KMS** | Unlimited keys (soft); 1k aliases | <10 ms | $1 / key-mo + $0.03 / 10k encrypts | T0 (software keys; HSM is AWS-only) |

**When to pick which**: SSM Parameter Store Standard for env-var-like config (free!). Secrets Manager when you need automatic rotation (RDS creds, third-party API keys with rotation lambdas). AppConfig for feature flags and validated/staged config rollouts. KMS for envelope-encrypting application data (S3, EBS, application-level secrets).

**Common mistake**: putting every config value in Secrets Manager. At 500 secrets, that's $200/mo for what Parameter Store does for free.

---

## 20. Workflow / Scheduling

| Service | Scale ceiling | Latency | Cost shape | Tier |
|---|---|---|---|---|
| **Step Functions Standard** | 25k open executions / account; 1 yr max | sub-second transitions | $0.025 / 1k state transitions | T0 |
| **Step Functions Express** | 100k starts/sec; 5 min max | <100 ms | $1.00 / M starts + $0.00001667 / GB-s + $0.00007 / req-s | T0 |
| **Step Functions Distributed Map** | Up to 10k concurrent child executions | depends | Standard or Express pricing × children | T2 |
| **EventBridge Scheduler** | 1M schedules; 100 per second per group | 1-sec precision | $1.00 / M invocations (free tier 14M) | T0 |
| **MWAA (Managed Airflow)** | Per environment size | depends on DAGs | $0.49 / hr (mw1.small) → $4+ / hr (large) + storage | T0 |
| **SWF (legacy)** | Don't pick for new work | — | Per workflow execution | T2 |

**When to pick which**: Step Functions Express for high-volume short workflows (HTTP request handlers, simple choreography); Standard for long-running, low-throughput (data pipelines, human approval, multi-day jobs). EventBridge Scheduler for cron and one-shot schedules — replaces both EventBridge Rules and CloudWatch Events. MWAA when your team already lives in Airflow DAGs and porting to Step Functions isn't viable.

---

## 21. Email / Notifications

| Service | Scale ceiling | Latency | Cost shape | Tier |
|---|---|---|---|---|
| **SES** | Per-account sending quota (starts at 200/day sandbox; production review unlocks) | seconds (deliverability driven) | $0.10 / 1k emails (out) + $0.12 / GB attachments + $0.10 / 1k receive | T1 (lettre/smtplib bridge; or T0 via SMTP submission) |
| **SES Virtual Deliverability Manager** | Adds engagement tracking, reputation | — | $1,250 / mo subscription + per-message fees | T2 |
| **SNS SMS** | Per-account per-region spend limit | 5–30 sec | $0.00645 / SMS to US (varies wildly by country) | T2 |
| **SNS Mobile Push (APNs/FCM)** | Per-platform service limits | <5 sec | $0.50 / M publishes + $0.50 / M deliveries | T2 |
| **Pinpoint** | Campaign orchestration over SES/SNS/Connect | varies | $1.20 / 1k segments + underlying SES/SNS cost | T2 |
| **End User Messaging Push / SMS** | Newer split of Pinpoint into focused services | — | similar to Pinpoint legacy | T2 |

**When to pick which**: SES for transactional email (signups, receipts). Pinpoint for campaign-style messaging across channels. SNS SMS for transactional SMS only — for marketing SMS use Pinpoint or 3rd-party (Twilio, MessageBird) for better deliverability.

---

## 22. Observability

| Service | Scale ceiling | Latency | Cost shape | Tier |
|---|---|---|---|---|
| **CloudWatch Logs** | Unlimited | seconds-to-minutes ingestion | $0.50 / GB ingested + $0.03 / GB-mo storage | T0 (via stdout / awslogs driver) |
| **CloudWatch Logs Live Tail** | Per-stream | sub-second | $0.01 / minute / stream | T0 |
| **CloudWatch Logs Insights** | Query language over log data | seconds | $0.005 / GB scanned | T0 |
| **CloudWatch Metrics — Custom** | 1-sec resolution available | 1-min standard | $0.30 / metric-mo (first 10k); $0.10 / 1k API calls | T0 (EMF or direct API) |
| **CloudWatch Alarms** | Per metric/composite | — | $0.10 / alarm-mo (standard); $0.30 / high-res | T2 (triggering doesn't reproduce locally) |
| **CloudWatch Synthetics** | Per canary | minute-scale runs | $0.0012 / canary run | T2 |
| **CloudWatch RUM** | Real-user monitoring | — | $1.00 / 100k events | T2 |
| **X-Ray** | Distributed tracing | — | $5.00 / 1M traces recorded + $0.50 / 1M retrieved | T1 (OTel → Jaeger local / ADOT cloud) |
| **CloudTrail (management events)** | Unlimited | minutes | First trail free (management); $2.00 / 100k events (data events) | T2 |
| **CloudWatch Application Signals** | Auto-instrument for APM | — | $0.0035 / minute / signal | T2 |

**When to pick which**: CloudWatch Logs is the default destination — but it gets expensive fast at >100 GB/mo ingestion. Many teams pipe to S3 (via Firehose) for cheap retention and use Athena to query. X-Ray is fine for basic distributed tracing but lacks the depth of Datadog/Honeycomb at the cost of being free-tier-friendly. CloudTrail management events are *free* for the first trail — turn it on always.

**Cost trap**: CloudWatch Logs at $0.50/GB ingested is one of the highest-margin AWS services. Filter aggressively before sending.

---

# Data / Analytics (5 groups)

## 23. Analytics — Warehouse

| Service | Scale ceiling | Latency | Cost shape | Tier |
|---|---|---|---|---|
| **Redshift (provisioned)** | Up to 128 RA3.16xlarge nodes; PB scale | sub-sec to seconds query | $0.85 / RA3.large.hr → $13.04 / RA3.16xlarge.hr; + RMS storage $0.024 / GB-mo | T0 (postgres wire; DISTKEY/SORTKEY won't reproduce) |
| **Redshift Serverless** | Auto-scales RPUs (8–512); same data limits | sub-sec to seconds | $0.36 / RPU-hr + storage | T0 |
| **Redshift Spectrum** | Query S3 from Redshift | seconds (S3-bound) | $5.00 / TB scanned | T2 |
| **Aurora zero-ETL → Redshift** | Auto-replicates Aurora → Redshift | seconds-minute lag | Aurora + Redshift, no extra ETL fee | T2 |

**When to pick which**: Redshift Serverless for new analytics workloads with unpredictable usage — but the 8 RPU min ($0.36 × 8 × 720 = $2,074 / mo at 100% util) is steep. Provisioned RA3 when steady usage > 4 hours/day. Spectrum to query S3 data alongside Redshift tables without ingest.

---

## 24. Analytics — Query-on-storage

| Service | Scale ceiling | Latency | Cost shape | Tier |
|---|---|---|---|---|
| **Athena (SQL on S3, on-demand)** | Per-query | seconds to minutes | $5.00 / TB scanned (free 10 MB minimum per query) | T0 (Trino / Presto OSS) |
| **Athena Provisioned Capacity** | Reserved DPUs for steady use | — | $0.30 / DPU-hr (min 24 DPUs, $216/day floor) | T0 |
| **Athena for Apache Spark** | Notebooks on S3 | — | $0.35 / DPU-hr | T0 |
| **S3 Select** | Subset object scan | seconds | $0.002 / GB scanned + $0.0007 / GB returned | T2 |
| **AWS Clean Rooms** | Multi-party SQL on shared data | — | $0.93 / CRPU-hr | T2 |

**When to pick which**: Athena on-demand for ad-hoc analytics on S3 data — the cheapest way to query existing S3 data lakes (especially with Parquet + partitioning). Athena Provisioned for steady reporting workloads with predictable DPU usage. S3 Select for filtering individual large objects without downloading. Always partition + Parquet to keep scan costs sane.

---

## 25. Analytics — ETL / Catalog

| Service | Scale ceiling | Latency | Cost shape | Tier |
|---|---|---|---|---|
| **Glue Jobs (Spark)** | DPU-based; scales horizontally | minutes (job startup) | $0.44 / DPU-hr (min 1 min) | T0 (bitnami/spark locally) |
| **Glue Jobs (Python shell)** | Single-DPU jobs | seconds startup | $0.44 / DPU-hr | T0 |
| **Glue Crawlers** | Schema discovery on S3 | minutes | $0.44 / DPU-hr | T2 |
| **Glue Data Catalog** | Hive metastore | <100 ms | First 1M objects free; $1.00 / 100k objects after | T0 (apache/hive metastore) |
| **Glue DataBrew** | No-code data prep | minutes | $0.48 / interactive node-hr; $0.40 / DataBrew job node-hr | T2 |
| **Lake Formation** | Adds AuthZ/governance on top of Glue catalog | — | Free (uses underlying Glue/S3) | T2 |

**When to pick which**: Glue for traditional batch ETL with PySpark — pricier than self-managed Spark but no infra to babysit. DataBrew for analyst self-serve. Lake Formation when your data lake needs row/column-level security and tag-based access control. Glue Catalog is the de-facto metastore — Athena, Redshift Spectrum, EMR all use it.

---

## 26. Analytics — Big-data Compute

| Service | Scale ceiling | Latency | Cost shape | Tier |
|---|---|---|---|---|
| **EMR on EC2** | Thousands of nodes; full Hadoop/Spark/HBase/Presto | minutes startup | EC2 cost + $0.27 / EC2-vCPU-hr EMR markup (varies) | T0 |
| **EMR Serverless** | Auto-scales workers | sub-minute first job | $0.052624 / vCPU-hr + $0.00578 / GB-hr | T0 |
| **EMR on EKS** | Run Spark on existing EKS | depends | $0.01265 / vCPU-hr EMR markup + EKS cost | T0 |
| **EMR Studio** | JupyterLab on EMR | — | Free (compute via clusters) | T0 |

**When to pick which**: EMR Serverless for new Spark workloads — no cluster sizing. EMR on EC2 when you want long-lived clusters with custom bootstrap actions or non-Spark workloads (HBase, Presto, Flink). EMR on EKS when your org runs everything on Kubernetes.

---

## 27. Analytics — BI

| Service | Scale ceiling | Latency | Cost shape | Tier |
|---|---|---|---|---|
| **QuickSight Enterprise** | Per-user dashboards | sub-second cached | $18 / author-mo + $0.30 / reader-session (capped at $5/mo/reader) or $5/reader-mo | T2 |
| **QuickSight Q (NLQ)** | Natural-language Q&A | — | +$250 / mo / author | T2 |
| **QuickSight Generative BI** | LLM-based authoring | — | Bundled with Enterprise + Q | T2 |
| **QuickSight Embedded** | Embed dashboards | — | Same as Enterprise per session/user | T2 |

**When to pick which**: QuickSight when AWS-only and you want pay-per-session for occasional viewers. Tableau / Looker / Power BI still dominate the BI category — QuickSight is "good enough" but rarely a category winner.

---

# ML / AI (6 groups)

## 28. ML — Training / Serving Platform

| Service | Scale ceiling | Latency | Cost shape | Tier |
|---|---|---|---|---|
| **SageMaker Training (on-demand)** | Per-job instance fleet (1–100s) | minutes startup | $0.06 / hr (ml.t3.medium) → $40+ / hr (ml.p5.48xlarge) | T0 (local-mode SDK) |
| **SageMaker Training (Spot)** | Same | Same + checkpoint discipline | Up to 90% off | T0 |
| **SageMaker Real-Time Inference** | Per-endpoint auto-scaling | <100 ms typical | Per-instance hourly + data | T0 |
| **SageMaker Serverless Inference** | 200 concurrent invocations max | 100 ms warm; cold start seconds | $0.0000200 / GB-s memory + $0.20 / M requests | T0 |
| **SageMaker Async Inference** | Up to 1 hr per inference; queued | — | Per-instance + storage | T0 |
| **SageMaker Batch Transform** | Run on a dataset, save to S3 | minutes | Per-instance | T0 |
| **SageMaker Studio** | IDE for the above | varies | Per-user + underlying compute | T0 (Jupyter OSS base) |
| **SageMaker JumpStart / Canvas** | Pre-trained models / no-code | varies | Per-instance + per-model | T2 |

**When to pick which**: SageMaker Serverless Inference for spiky inference loads <200 concurrent. Real-time inference for steady traffic. Async inference for batch-like inference where you tolerate seconds-to-minutes. Batch Transform for "run on this whole dataset and save results." Spot training for fault-tolerant runs with checkpointing.

---

## 29. ML — Foundation Models (Bedrock)

| Service | Scale ceiling | Latency | Cost shape | Tier |
|---|---|---|---|---|
| **Bedrock — Claude Opus 4.7** | Per-account quota; on-demand or provisioned | 1–5 sec to first token | $5 / M input + $25 / M output (web-verified 2026-05; unchanged from Opus 4.6; Opus 4.7's new tokenizer can emit up to 35% more tokens per request) | T1 (rig-core / litellm bridge) |
| **Bedrock — Claude Sonnet 4.6** | Same | <1 sec to first | $3 / M input + $15 / M output | T1 |
| **Bedrock — Claude Haiku 4.5** | Same | <1 sec | $1 / M input + $5 / M output | T1 |
| **Bedrock — Llama / Mistral / Cohere / Amazon Nova / Titan** | Per-model quotas | varies | Per-model token pricing | T1 |
| **Bedrock Provisioned Throughput** | Reserved capacity | predictable | Per-model-hour commitment (hours to months) | T1 |
| **Bedrock Agents** | Orchestrated tool-use over models | adds 1–3 sec | Per-invocation + underlying model tokens | T2 |
| **Bedrock Knowledge Bases (RAG)** | Vector store + retrieval | depends on vector store | Per-query + vector store cost | T2 |
| **Bedrock Guardrails** | Filter input/output | <500 ms | $0.75 / 1k text units (input or output) | T2 |

**When to pick which**: Bedrock when you want managed LLMs in AWS without operating GPUs. Claude family for highest quality (Haiku for cheap/fast, Sonnet for balanced, Opus for hardest reasoning). Provisioned throughput when you're spending >$5k/mo on on-demand for predictability + lower per-token cost. Knowledge Bases as the "RAG in a box" path — but loses flexibility vs DIY pgvector + Bedrock models.

**Pricing volatile**: Token prices change quarterly; verify on the Bedrock pricing page before committing.

---

## 30. ML — Vision

| Service | Scale ceiling | Latency | Cost shape | Tier |
|---|---|---|---|---|
| **Rekognition Image** | Per-image API | <500 ms typical | $1.00 / 1k images (Labels) | T1 (OpenCV / YOLOv8 bridge) |
| **Rekognition Video** | Streaming or stored | seconds | $0.10 / minute of video | T1 |
| **Rekognition Custom Labels** | Train on your own labels | — | $1.00 / inference hour + training | T1 |
| **Textract** | Per-page OCR + structure | seconds | $1.50 / 1k pages (text) → $50 / 1k pages (forms+tables) | T1 (tesseract + layoutparser) |
| **Rekognition Faces** | Face match / detection | <500 ms | $1.00 / 1k images | T1 |

**When to pick which**: Rekognition when "good enough" off-the-shelf vision suffices (content moderation, simple object detection). Textract for PDF/scan extraction — significantly better than Tesseract on real-world forms. For anything custom, SageMaker + a model from JumpStart wins.

---

## 31. ML — Speech

| Service | Scale ceiling | Latency | Cost shape | Tier |
|---|---|---|---|---|
| **Polly (TTS)** | Standard + Neural + Long-form + Generative voices | <500 ms for short | $4 / M chars (Standard) → $30 / M chars (Generative) | T1 (coqui-ai / piper bridge) |
| **Transcribe (STT)** | Async batch + streaming | seconds (streaming); minutes (batch) | $0.024 / minute (batch) → $0.014 / minute high volume | T1 (whisper bridge) |
| **Transcribe Medical** | HIPAA-tuned | same | $0.075 / minute | T1 |
| **Transcribe Call Analytics** | Call-center features | — | $0.03 / minute | T1 |

**When to pick which**: Polly for accessible TTS in a webapp — Neural voices are good enough for most use cases. Transcribe is fine for English call-center; for podcasting / non-English / nuance, OpenAI Whisper or Deepgram often beat it.

---

## 32. ML — NLP

| Service | Scale ceiling | Latency | Cost shape | Tier |
|---|---|---|---|---|
| **Comprehend** | Sentiment, entities, key phrases, classification | <500 ms | $0.0001 / unit (1 unit = 100 chars); $0.50 / TR custom | T1 (spaCy bridge) |
| **Comprehend Medical** | Medical entity extraction | same | $0.01 / 100 chars | T1 |
| **Translate** | 75+ language pairs | <500 ms | $15 / M chars (real-time); $60 / M chars (Active Custom Translation) | T1 (argos-translate bridge) |
| **Translate (Batch)** | Async over S3 | — | $15 / M chars | T1 |

**When to pick which**: Comprehend for off-the-shelf NLP on AWS; for serious NLP, fine-tune a small model via SageMaker or use Bedrock with a prompt. Translate is fine for most translation; specialized providers (DeepL) often beat it on European languages.

---

## 33. ML — Forecasting / Personalization

| Service | Scale ceiling | Latency | Cost shape | Tier |
|---|---|---|---|---|
| **Forecast** | (deprecated — replaced by SageMaker Canvas time-series) | — | Last billed at $0.088 / 1k forecasts | T2 (deprecated) |
| **Personalize** | Per-domain recommender | <100 ms | $0.05 / TPS-hr + $0.24 / training-hr + $0.067 / GB ingested | T2 |

**When to pick which**: Forecast is end-of-life — use SageMaker Canvas or SageMaker DeepAR. Personalize for recommender system shortcuts (clickstream → recs) — non-trivial setup; a simple collaborative-filter on Postgres often wins for small catalogs.

---

# IoT / Edge (3 groups)

## 34. IoT — Device Gateway

| Service | Scale ceiling | Latency | Cost shape | Tier |
|---|---|---|---|---|
| **IoT Core (MQTT)** | Billions of devices | <100 ms | $0.30 / 1M minutes connection + $1.00 / 1M messages (publish/deliver) | T0 (mosquitto) |
| **IoT Core (HTTPS / WS)** | Same | <500 ms | $1.00 / 1M messages | T0 |
| **IoT Device Management** | Fleet provisioning, jobs, indexing | — | $0.0042 / device-mo (per indexed device) + $0.25 / 1k jobs | T2 |
| **IoT Device Defender** | Anomaly detection on telemetry | — | $0.0011 / device-mo | T2 |
| **IoT Rules Engine** | Route messages from IoT Core to other AWS | <500 ms | $0.15 / 1M rule activations | T2 |

**When to pick which**: IoT Core for MQTT at scale with TLS device certs. For <10k devices and simple HTTP telemetry, a plain ALB+Lambda+Kinesis often beats IoT Core on price and simplicity.

---

## 35. IoT — Edge Runtime

| Service | Scale ceiling | Latency | Cost shape | Tier |
|---|---|---|---|---|
| **Greengrass V2** | Edge runtime that runs Lambda + ML on devices | local; depends on device | $0.16 / device-mo (first 10k) → cheaper above | T0 (AWS-provided local runtime) |
| **FreeRTOS** | RTOS for microcontrollers | local | Free OSS; AWS provides extensions | T0 |
| **IoT ExpressLink** | Connectivity module SDK | local | Hardware-vendor pricing | T2 |

**When to pick which**: Greengrass for industrial edge (run inference at the device, sync intermittently). FreeRTOS for embedded — Free but tied to AWS ecosystem for the cloud bits.

---

## 36. IoT — Analytics

| Service | Scale ceiling | Latency | Cost shape | Tier |
|---|---|---|---|---|
| **IoT Analytics** | Time-series store + analytics for device data | seconds | $0.50 / 1M messages + $0.10 / GB stored + $0.36 / hr container query | T2 |
| **IoT SiteWise** | Industrial OT data modeling | — | $0.0010 / data point ingested + $0.07 / GB-mo + per-attribute | T2 |
| **IoT Events** | Detector models on streams | sub-sec | $0.30 / 1M evaluations | T2 |
| **IoT TwinMaker** | Digital twins | — | $0.25 / 1k queries + $0.25 / 1k authz | T2 |
| **IoT FleetWise** | Vehicle telemetry collection | — | $0.05 / vehicle-mo + per-signal pricing | T2 |

**When to pick which**: IoT Analytics is mostly superseded by Kinesis Firehose → S3 → Athena for cost-effective IoT analytics. SiteWise specifically when modeling industrial assets with OPC-UA. FleetWise for connected vehicles.

---

# Tier 2 deep-dive

## What "Tier 2" means in supeux terms

Tier 2 means no OSS container reproduces the service locally. For the user's **runtime** story this is already solved — user container code calls the AWS SDK directly with mounted IAM creds, which works identically whether the container runs locally (docker `~/.aws` bind-mount or `AWS_PROFILE` env var) or on Fargate / Lambda (task role / execution role auto-injected). No supeux runtime abstraction is needed.

The only open question per service is the **provisioning** story: does its config shape fit a short `cloud_only:` yaml block (worth a registry slot), or is the config big / dynamic enough that hand-written `.tf` via the `terraform-module` escape hatch (`supeux_abstraction_v2.md` §2) is more honest? The sub-grouping below splits Tier 2 by *why* it's niche; the per-service paragraphs that follow judge each truly-niche entry on one of three verdicts:

- **`yaml-registry`** — config fits a short `cloud_only:` block (model ID + a few knobs). Ship a registry entry.
- **`hand-tf`** — config is too large / dynamic for short yaml (entity graphs, ASL bodies, recipe configs). Document the SDK call pattern, route to `resources.<name>: { type: terraform-module, source: ./modules/foo }`.
- **`skip`** — deprecated, end-of-life, or so narrow that even hand-tf documentation isn't worth shipping.

## Tier 2 sub-groups (by why-niche)

The eight buckets are oriented around what makes the service unreachable from a local container, not which v2 §4 pattern fits (skip / hit-real / engine-swap / stub) — that pattern dimension is complementary, and the per-service paragraphs footnote the relevant pattern where useful.

1. **Edge runtime / CDN** — CloudFront, CloudFront Functions, Lambda@Edge, Global Accelerator. Edge POP routing and anycast are the value-add; no local equivalent.
2. **Multi-region coordination** — Aurora DSQL, DynamoDB Global Tables, Step Functions Distributed Map. Active-active or cross-region fan-out is a coordination property that single-node OSS can't reproduce.
3. **Managed ML / AI orchestration** — Bedrock Knowledge Bases, Bedrock Agents, Bedrock Guardrails, SageMaker JumpStart, SageMaker Canvas, Forecast, Personalize, Kendra. Orchestration and bundled-model marketplaces are the proprietary value; the underlying inference is already T1 via community libraries (rig-core / litellm).
4. **IoT vertical** — IoT Device Management, IoT Device Defender, IoT Analytics, IoT SiteWise, IoT TwinMaker, IoT FleetWise, IoT Events, IoT Rules Engine, IoT ExpressLink. Vertical-specific data models (CAN-bus, OPC-UA, digital-twin entity graphs) sit above the MQTT layer (T0 via mosquitto).
5. **Specialty storage variants** — S3 Express One Zone, S3 Vectors, S3 Object Lambda, S3 Select, DAX, Neptune Analytics, Timestream LiveAnalytics, FSx (Lustre / Windows / ONTAP / OpenZFS). Same primitive shape with a proprietary performance or query characteristic AWS hasn't open-sourced.
6. **Identity / lifecycle / observability / messaging lock-in** — Cognito User Pools (user *lifecycle* admin), Cognito Identity Pools, Cognito Hosted UI, IAM enforcement, IAM Identity Center, CloudWatch RUM, CloudWatch Synthetics, CloudWatch Application Signals, CloudWatch Alarms (triggering), CloudTrail, SNS Mobile Push, SNS SMS, SES VDM, Pinpoint / End User Messaging. Telemetry from real browsers / devices, lifecycle admin, and AWS-side enforcement are by nature cloud-only.
7. **API edge / data / BI orchestration** — API Gateway (REST / HTTP / WebSocket), AppSync, EventBridge Pipes, Route 53 Resolver, AWS Clean Rooms, Glue Crawlers, Glue DataBrew, Lake Formation, QuickSight (Enterprise / Q / Generative / Embedded), Redshift Spectrum, Aurora zero-ETL → Redshift. Orchestration / dashboarding / multi-party — the AWS-managed plane *is* the value.
8. **Deprecated / end-of-life** — SWF, CloudSearch, Forecast. Should not be a target for new builds.

## Common vs niche within Tier 2

**Common Tier 2** — services most modern cloud apps touch at some point. Earn a yaml-registry slot or a primitive wrapper even when their config shape is non-trivial, because the audience is broad enough to justify it. Their "when to pick which" notes already live in the per-role-group sections above; deep-dive treatment is not repeated here:

- API Gateway (REST / HTTP / WebSocket) — universal API surface
- Cognito User Pools — universal auth
- CloudFront — universal CDN
- Bedrock Knowledge Bases — RAG is mainstream in 2026
- DynamoDB Global Tables — anyone going multi-region
- Lambda@Edge — common in CDN-heavy apps (covered below as niche by some lenses; included here as common in supeux's expected audience)
- CloudWatch Alarms / CloudTrail / IAM enforcement / Cognito Identity Pools — universal supporting infra
- AppSync — common in GraphQL-shop deployments
- Step Functions Standard (single state machine) — common workflow surface

**Truly-niche Tier 2** — the entries deep-dived below. Narrow-vertical, low-adoption, deprecated, or otherwise outside what supeux's expected audience touches by default.

## Per-niche-service paragraphs and practicality verdicts

For each entry: 2–3 sentences on **who reaches for it** + **trigger scenario** + **why a generic alternative doesn't suffice**, then a single **Practicality:** verdict.

### Edge runtime / CDN — niche

#### Global Accelerator

For latency-sensitive TCP / UDP workloads (gaming, VoIP, multi-region failover) that benefit from anycast IPs over the AWS backbone. Reached by teams that have measured >100 ms RTT improvement from anycast over direct origin routing and have client-IP-stable user pools. For pure HTTP origins, CloudFront usually wins.

**Practicality: `yaml-registry`** — config shape is small (endpoint groups + traffic dials).

#### Lambda@Edge

CloudFront viewer / origin event hooks for dynamic header rewrites, A/B routing, request-time auth checks, JWT verification at the POP. Reached when CloudFront Functions (10 ms cap, JS-only, no network) isn't enough but a full origin round-trip is too slow. Language-locked to Node / Python (Rust isn't supported at the edge in 2026).

**Practicality: `hand-tf`** — config is a Lambda + viewer / origin event-type binding on a specific CloudFront distribution; per-app and tightly coupled to the distribution config.

#### CloudFront Functions

Sub-ms JS at the viewer hook for trivial header / URL rewrites and cache-key tweaks. Cheap and faster than Lambda@Edge, but the JS code body is per-app and inline.

**Practicality: `hand-tf`** — the function body is inline JS code, which fits hand-tf more honestly than a yaml string.

### Multi-region coordination — niche

#### Aurora DSQL

Multi-region active-active Postgres-flavored SQL for apps that genuinely need cross-region writes (regulated industries with data-residency + multi-region availability requirements). Premium-priced and SQL-feature-limited compared to vanilla Aurora; most teams shouldn't reach for it.

**Practicality: `yaml-registry`** — already covered by `tier: global` on the `db.sql` primitive per `supeux_abstraction_v2.md` §6. No new shape needed.

#### Step Functions Distributed Map

ASL state machine extension that fans out across up to 10k concurrent child executions over an S3-or-DynamoDB-sourced dataset. Used for batch document processing, multi-file ETL, large evaluation runs — when Step Functions Standard's per-execution parallelism isn't enough.

**Practicality: `hand-tf`** — the ASL body that includes a Distributed Map step is the user's per-app workflow definition. Step Functions overall is already a hand-tf case for multi-state workflows; Distributed Map is the same.

### Managed ML / AI orchestration — niche

#### Bedrock Knowledge Bases (RAG)

Bundled vector store + ingestion + retrieval pipeline for "I have docs, give me RAG over them" use cases. Reached by teams that don't want to operate a vector store and embedding pipeline themselves; trades flexibility (chunk size, hybrid search, custom retrievers) for managed-ness. Re-classified here as both common (because RAG is mainstream) and shape-heavy.

**Practicality: `hand-tf`** — vector store choice + data source connectors + chunking strategy + embedding model + retrieval config is a big, application-specific shape.

#### Bedrock Agents

Tool-use orchestration over Bedrock models: action groups, tool schemas, collaborator agents, prompt templates. Reached by teams building agentic apps who want managed orchestration instead of writing the agent loop in code.

**Practicality: `hand-tf`** — action group definitions and tool schemas are app-specific and lengthy.

#### Bedrock Guardrails

Input / output content filtering with category thresholds, denied topics, sensitive-info redaction, prompt-attack detection. Reached when running user-facing LLM apps where content safety is a release blocker.

**Practicality: `yaml-registry`** — config is a short list (filter strengths per category + denied-topic strings + redaction rules). Fits a `cloud_only:` block cleanly.

#### SageMaker JumpStart

Foundation-model marketplace inside SageMaker — pick a pretrained model, click deploy, get a SageMaker endpoint. Reached by ML teams that want to evaluate non-Bedrock models (e.g., specific open-weight checkpoints) without operating GPUs.

**Practicality: `hand-tf`** — model-marketplace selection + instance config + autoscaling config is heavyweight.

#### SageMaker Canvas

No-code ML — clickable model training over CSV-shaped datasets. Reached by analysts and BAs in regulated industries who can't write Python. Typically provisioned once per org by an admin.

**Practicality: `hand-tf`** — UI-driven; the underlying domain + user profile config is admin-shaped, not app-shaped.

#### Personalize

Recommender-system-as-a-service: dataset groups (user / item / interaction events), recipes (USER_PERSONALIZATION, SIMS, etc.), event trackers, campaigns. Reached by media / e-commerce teams without ML talent. For small catalogs (<100k items), a Postgres collaborative-filter usually wins.

**Practicality: `hand-tf`** — dataset group + schema + recipe + campaign + event tracker config is application-shaped and heavyweight.

#### Forecast

Time-series forecasting service. **Deprecated** — AWS recommends SageMaker Canvas time-series for new work.

**Practicality: `skip`** — deprecated; no new builds.

#### Kendra

Enterprise document search with NLP, custom connectors (SharePoint, Salesforce, Box, S3), ACL-aware results, FAQ extraction. Reached by enterprises with sprawling permissioned content stores. For most modern needs, RAG over the same content via Bedrock KB or OpenSearch + pgvector wins on cost and flexibility.

**Practicality: `hand-tf`** — data source connectors + index field mappings + ACL config = lots of shape per deployment.

### IoT vertical — niche

#### IoT FleetWise

CAN-bus and ECU signal collection from connected vehicles with edge-side data reduction and decoder manifests. Reached by automotive OEMs and fleet operators where per-signal sampling rules matter more than raw cost. IoT Core + Kinesis works as a generic alternative but requires building the decoder + reduction layer yourself.

**Practicality: `hand-tf`** — signal catalog + decoder manifest + collection scheme is vehicle-specific.

#### IoT SiteWise

Industrial OT data modeling: asset hierarchies, OPC-UA gateway, time-series storage, calculated properties. Reached by manufacturing / energy / utility ops teams modeling physical-asset topologies. Generic alternatives (IoT Core + Timestream) don't preserve the asset-hierarchy semantics.

**Practicality: `hand-tf`** — asset model + property definitions + transforms = bespoke per industrial deployment.

#### IoT TwinMaker

Digital-twin platform: entities, components, scene compositions, Grafana plugin. Reached by industrial digital-twin builders modeling 3D scenes wired to live SiteWise / IoT data.

**Practicality: `hand-tf`** — entity graphs and scene definitions are inherently large and bespoke.

#### IoT Device Management

Fleet provisioning, job orchestration over devices, fleet indexing, device groups. Reached when a team operates >1k production devices and needs OTA update orchestration + indexed search. Smaller fleets do fine with plain MQTT + a thin homemade jobs system.

**Practicality: `hand-tf`** — fleet indexing schema + job templates per device class are fleet-specific.

#### IoT Device Defender

Audit checks (e.g., shared certs across devices, overly permissive policies) and detect profiles (anomaly detection on MQTT telemetry). Reached by security-sensitive IoT deployments needing continuous compliance posture.

**Practicality: `hand-tf`** — audit-check selections and detect-profile thresholds are deployment-specific.

#### IoT Analytics

Time-series store + analytics tailored for device data. Originally aimed to be the IoT-side warehouse but largely superseded by Kinesis Firehose → S3 → Athena, which is cheaper and more flexible. AWS itself deprioritizes.

**Practicality: `skip`** — recommend the Firehose → S3 → Athena pattern (already T0); not worth registry surface area.

#### IoT Events

Detector models on telemetry streams — state-machine-shaped "if temp > X for Y minutes, send alert". Reached by industrial deployments wanting managed event-pattern detection without writing Lambda code.

**Practicality: `hand-tf`** — detector model definitions are workflow-shaped, per-deployment.

#### IoT Rules Engine

SQL-shaped routing from IoT Core MQTT topics to other AWS services (Kinesis, Lambda, DynamoDB, S3). Near-mandatory once IoT Core is in use; the SQL is per-deployment.

**Practicality: `hand-tf`** — but worth documenting an opinionated example alongside the IoT Core T0 entry.

#### IoT ExpressLink

Connectivity module SDK for hardware-vendor microcontrollers (Espressif, Infineon, etc.) — abstracts the cellular / WiFi / TLS layer so device firmware can talk to IoT Core without a full TCP stack.

**Practicality: `skip`** — device-firmware concern, outside supeux's deploy-tool scope.

### Specialty storage variants — niche

#### S3 Express One Zone (Directory bucket)

Single-AZ, high-RPS S3 variant — <10 ms p99, 7× pricier per GB. Reached by ML training shuffle, analytics scratch, and other "lots of small reads per second per prefix" workloads bottlenecked by S3 Standard's per-prefix throughput ceiling.

**Practicality: `yaml-registry`** — already covered by `bucket: variant: express-one-zone` per `supeux_abstraction_v2.md` §6 vocabulary.

#### S3 Vectors

Native vector storage and ANN search in S3, GA late-2025. For "I have billions of vectors and query latency is non-interactive" (batch RAG indexing, embedding archives). 20×+ cheaper per GB than OpenSearch vector storage; 100–500 ms query latency vs OpenSearch's 10–100 ms.

**Practicality: `yaml-registry`** — small shape: bucket name + dimension + similarity metric + index type. Worth a `cloud_only:` slot.

#### S3 Object Lambda

Per-request object transformation Lambdas (redaction, format conversion, image resize on read). Reached when you want multiple representations of the same object without storing them. The transformation Lambda body is the value.

**Practicality: `hand-tf`** — config is the Lambda's transformation logic, which is per-app.

#### S3 Select

In-object SQL-style scan to return a subset of a CSV / JSON / Parquet object. Pure runtime API; no provisioning required.

**Practicality: `skip`** — nothing to provision; pure SDK call from user code.

#### DAX

Microsecond DynamoDB cache layer — node cluster sitting in front of a DynamoDB table. Reached when you've measured single-digit-ms isn't enough and you can absorb DAX's single-AZ-per-shard fragility.

**Practicality: `yaml-registry`** — small shape: cluster name + node type + node count + target DynamoDB table.

#### Neptune Analytics

In-memory graph analytics engine separate from transactional Neptune — for centrality, pathfinding, community detection over loaded graph snapshots. Reached by fraud / recommendations / knowledge-graph teams running batch-shaped analytical graph queries.

**Practicality: `hand-tf`** — engine choice + import sources + workspace config is specialized.

#### Timestream for LiveAnalytics

Original Timestream — proprietary SQL surface over auto-tiered memory + magnetic storage for high-volume IoT / observability metrics. The InfluxDB-flavor variant (T0) is the more portable choice today; LiveAnalytics' lock-in is its proprietary SQL.

**Practicality: `hand-tf`** — table + retention + magnetic-store config is per-deployment, and the LiveAnalytics flavor isn't the recommended modern choice.

#### FSx (Lustre / Windows / ONTAP / OpenZFS)

Filesystem-specific managed storage: Lustre for HPC / ML training >1 GB/s per client; Windows for AD-joined SMB shares; ONTAP for NetApp-specific features (SnapMirror, multi-protocol); OpenZFS for cheap "real filesystem" with snapshots/clones. Reached when the filesystem flavor itself is the requirement.

**Practicality: `hand-tf`** — each flavor has bespoke per-use-case config; not enough audience overlap for a registry shortcut.

### Identity / lifecycle / observability / messaging lock-in — niche

(Common Tier 2 entries — Cognito User Pools, IAM enforcement, CloudTrail, CloudWatch Alarms — are covered in their role-group sections above. The niche-only entries below.)

#### Cognito Identity Pools

Federate identities (Cognito User Pool, Google, Facebook, SAML) into temporary AWS credentials for client-side SDK use. Reached by mobile / SPA apps that need to call AWS APIs directly from the client (e.g., S3 uploads from browser).

**Practicality: `hand-tf`** — federated identity mappings are tied to specific identity providers' setup; per-app.

#### Cognito Hosted UI

AWS-hosted sign-up / sign-in pages over Cognito User Pools. Useful for prototypes and apps that don't want to build their own auth UI; brand customization is limited.

**Practicality: `hand-tf`** — domain prefix + customization CSS + callback URLs are per-app config tied to the user pool definition.

#### IAM Identity Center (workforce SSO)

Organization-level workforce SSO over multiple AWS accounts, identity-source federation. Reached by AWS Organizations users for centralized account access management.

**Practicality: `skip`** — `thesis.md` explicitly lists "multi-account governance" as currently out of scope.

#### CloudWatch RUM

Real-user monitoring from the browser — page load times, JS errors, custom events. Reached by web teams who want native AWS frontend observability without paying Datadog / Sentry.

**Practicality: `yaml-registry`** — small shape: app monitor name + domain allow-list + sampling rate.

#### CloudWatch Synthetics

Browser-based canaries running Puppeteer / Selenium scripts on schedule. Reached for proactive uptime + golden-flow monitoring from outside the VPC.

**Practicality: `hand-tf`** — the canary's inline JS / Python script body is the value; fits hand-tf, not yaml.

#### CloudWatch Application Signals

Auto-instrumented APM (request latency, error rate, SLO tracking) over Java / Python / .NET / Node services. Reached when you want AWS-native APM without the Datadog tax.

**Practicality: `yaml-registry`** — enable flag per service + SLO definitions. Small shape.

#### SNS Mobile Push

Fan-out to APNs (iOS) / FCM (Android) / Baidu push endpoints. Reached by apps with native mobile clients sending notifications. Locally there's nowhere for a push to go.

**Practicality: `yaml-registry`** — small shape: platform application name + cert/key secret ref + platform type.

#### SNS SMS

Outbound SMS via SNS. Reached for transactional SMS only; marketing SMS goes to Twilio / Pinpoint for better deliverability. Local dev: real SMS doesn't go anywhere.

**Practicality: `hand-tf`** — config is account-level (sender ID, spend limits, originator registrations) and largely outside per-app yaml.

#### SES Virtual Deliverability Manager

Email-deliverability-as-a-service add-on over SES with engagement tracking and reputation analytics. Subscription is $1,250 / mo.

**Practicality: `skip`** — subscription tier, not a per-app provisioned resource.

#### Pinpoint / End User Messaging Push / SMS

Campaign-style orchestration over SES / SNS / mobile push with segments, journeys, A/B testing. Reached by marketing-shop apps needing email + SMS + push campaigns from one place.

**Practicality: `hand-tf`** — campaign / journey / segment config is rich and app-specific.

### API edge / data / BI orchestration — niche

(API Gateway REST / HTTP / WebSocket and AppSync are covered as common Tier 2 in their role-group sections.)

#### EventBridge Pipes

Source → filter → optional enrichment → target wiring for DynamoDB streams, Kinesis, SQS, etc., without writing a Lambda. Reached for simple ETL hops where a Lambda would be over-engineering.

**Practicality: `yaml-registry`** — small shape: source + filter expression + target + optional enrichment Lambda.

#### Route 53 Resolver (inbound / outbound)

VPC ↔ on-prem DNS bridging via ENIs. Reached by hybrid cloud deployments needing on-prem DNS resolution inside VPC or vice-versa.

**Practicality: `hand-tf`** — VPC + ENI + endpoint rule config is networking-shaped, not app-shaped.

#### AWS Clean Rooms

Multi-party SQL on shared datasets with cryptographic guarantees that raw rows don't leak. Reached by ad-tech and healthcare data-sharing arrangements.

**Practicality: `hand-tf`** — collaboration + analysis-rule config is inter-org and bespoke.

#### Glue Crawlers

Schema-discovery jobs that scan S3 and populate Glue Catalog. Reached by ETL pipelines where schemas evolve. For static-schema pipelines, hand-defining the schema is honest and avoids the crawler runtime cost.

**Practicality: `hand-tf`** — schedule + S3 path + classifier overrides per crawler.

#### Glue DataBrew

UI-driven no-code data prep — visual recipe builder over Glue. Reached by analysts.

**Practicality: `skip`** — UI-driven; users provision once and configure interactively.

#### Lake Formation

Permissions overlay on top of Glue Catalog with row / column / tag-based access control. Reached by data-platform teams running shared data lakes.

**Practicality: `hand-tf`** — permission policies + tag definitions are org-shaped, not app-shaped.

#### QuickSight (Enterprise / Q / Generative BI / Embedded)

AWS-managed BI tool. Reached by AWS-only orgs who want pay-per-session viewer pricing. Tableau / Looker / Power BI still dominate the BI category.

**Practicality: `hand-tf`** — datasource + analysis + dashboard config is BI-tool-shaped, not app-shaped.

#### Redshift Spectrum

Query S3 from Redshift via external tables. Reached when you have a Redshift cluster and want to extend queries to cold S3 data without ingest.

**Practicality: `hand-tf`** — external schema config tied to a specific Redshift cluster.

#### Aurora zero-ETL → Redshift

Auto-replicates Aurora data into Redshift for analytical queries with seconds-minute lag. Reached when you want OLTP + OLAP without a separate ETL pipeline.

**Practicality: `yaml-registry`** — small shape: source Aurora cluster + target Redshift cluster + replication filter.

### Deprecated / end-of-life

#### SWF (Simple Workflow Service)

Pre-Step-Functions workflow orchestration. **Deprecated** — AWS recommends Step Functions for new builds.

**Practicality: `skip`** — deprecated.

#### CloudSearch

Pre-OpenSearch managed search. **Deprecated** — AWS recommends OpenSearch.

**Practicality: `skip`** — deprecated.

### Lambda runtime corner cases

#### Lambda SnapStart

Snapshot-restore mechanism to cut Java / .NET / Python Lambda cold-starts. Reached when Java / .NET cold-starts (5–10 s) are user-visible. AWS-internal snapshot mechanics; for Python specifically, cold-starts are usually fine without it.

**Practicality: `yaml-registry`** — small shape: enable flag on a specific Lambda. Worth exposing as an `overrides:` knob on `service.shape: function`.

## Runtime story for niche Tier 2 services (recap)

Regardless of the per-service verdict above, the user's **runtime code path** for any niche Tier 2 service is unchanged: call the AWS SDK from inside the service container with mounted creds. This works identically whether the container runs locally (docker `~/.aws` bind-mount or `AWS_PROFILE` env var) or on Fargate / Lambda (task role / execution role auto-injected by AWS). The verdict only determines whether supeux ships an opinionated yaml shortcut for the **provisioning** side, or sends the user to the v2 §2 escape hatch:

```yaml
resources:
  my_niche_thing:
    type: terraform-module
    source: ./modules/my_niche_thing
    inputs: { … }
```

with the resulting ARN injected as an env var into services that `uses:` it. No new primitive is invented; this analysis only earmarks which Tier 2 services route through `cloud_only:` versus `terraform-module`.

---

# Cross-cutting patterns observed

Six patterns recur across groups and matter for File 5's recommendation:

1. **Most groups have a "serverless" variant that costs more per-unit but eliminates capacity planning.** Pattern: pick provisioned only when sustained utilization > ~50% of provisioned capacity.
2. **A handful of services dominate by cost-sensitivity:** Lambda, S3, DynamoDB, SQS, SNS, CloudWatch Logs, EC2, RDS/Aurora. These are also the most stable APIs — the obvious targets for cloud↔local abstraction.
3. **Cognito, Step Functions, EventBridge, AppSync, SageMaker, Bedrock have no realistic local-container parity.** They're either AWS-specific protocols or proprietary models. Local emulation is best-effort at best. See **§Tier 2 deep-dive** above for the full sub-grouping, common-vs-niche split, and per-niche-service practicality verdict.
4. **Many "managed" services are wire-compatible with OSS engines**: Postgres on RDS, MySQL on RDS, Mongo on DocumentDB (mostly), Redis on ElastiCache, Kafka on MSK, Elasticsearch-API on OpenSearch. This is the cheap abstraction zone.
5. **The cost shape predicts how supeux's "switch to cloud" yaml should behave**: per-request services (Lambda, DynamoDB on-demand, SQS) are friendly to cloud-by-default; per-hour services (RDS, ElastiCache, OpenSearch) are unfriendly to "spin up for CI" — supeux needs an *off-by-default* policy for those.
6. **The Tier 2 niche set bifurcates by provisioning shape, not runtime difficulty.** All Tier 2 services are runtime-solved by mounted creds + AWS SDK from inside the user's container (works identically local / Fargate / Lambda). The only design question per service is whether its HCL fits a short `cloud_only:` yaml shortcut (`yaml-registry`) or is better left to the `terraform-module` escape hatch (`hand-tf`) — or omitted entirely (`skip`). See **§Tier 2 deep-dive** above.

These feed directly into File 5's PoC scope recommendation.
