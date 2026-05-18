# GCP Service Role Groups — Scale, Latency, Cost

> **Snapshot date: 2026-05-16. Prices in us-central1 USD unless noted. Re-verify on the GCP Pricing Calculator before quoting in a decision doc.**
>
> **Sources**: numbers come from training data through Jan 2026 plus targeted web verification (2026-05-16) for the most volatile services: Vertex AI Gemini 2.5 (Pro / Flash / Flash-Lite / Nano) per-token pricing, Anthropic Claude on Vertex per-token (cross-checked against Bedrock pricing in `aws_service_groups.md` — Anthropic's published prices are uniform across providers; Vertex applies no markup), BigQuery Editions slot pricing (Standard / Enterprise / Enterprise Plus, post-2024 model change), AlloyDB Standard vs Columnar Engine, Spanner per-PU pricing (post the 100-PU minimum change), Cloud Run Gen2 CPU/memory rates, Memorystore for Valkey (newer SKU). Older / stable services (Compute Engine, GCS, Cloud SQL, Pub/Sub, Persistent Disk, Filestore) are not web-re-verified for this snapshot.

This is the GCP companion to `aws_service_groups.md`. The same role-group framing applies, with GCP-native section numbering where GCP's service shape doesn't match AWS's. The mapping table below tells you where to find each AWS group in this file.

## What a "role group" is

A **role group** is a slot in a typical application's architecture (e.g., "the place I put a key-value store") that several GCP services can plausibly fill. The point of grouping is that within a group the *interface to your code* is similar, while **scale ceiling, latency, and cost shape** differ enough that the choice matters.

Each group section has:
1. A per-service comparison table on three axes:
   - **Scale ceiling** — soft/hard limit per partition / table / region. The point at which you'd be forced to re-shard or migrate.
   - **Latency band** — p50 / p99 for a typical read or write. "Single-digit ms" means ≤10 ms p50.
   - **Cost shape** — what you pay *per* (per-request, per-hour, per-GB-month, per-vCPU-hour) with a representative number.
2. A "when to pick which" note that captures the actual decision criteria.

Groups are listed in dependency order — compute first, then storage, then data services that need both.

Every per-service table below also carries a `Tier` column with one of `T0` / `T1` / `T2`. The rollup section that follows lifts those tags into three flat lists for at-a-glance reading; the Tier 2 deep-dive near the end of this file then sub-groups Tier 2 and judges each truly-niche entry on whether it earns a `cloud_only:` yaml shortcut or should be reached via the `terraform-module` escape hatch.

---

## Mapping to `aws_service_groups.md`

Three fusions; the rest line up 1:1.

| AWS file group(s) | GCP file group | Reason |
|---|---|---|
| 1–11 | 1–11 | Direct mapping (Compute, Storage, Database). Cloud Run shows up in all three Compute groups because the `service.shape:` distinction (function / long-running / batch) is still load-bearing for codegen even though one resource type underlies all three. |
| 12 (Queue) + 13 (Pub/Sub & Event Bus) | **12** (Pub/Sub & Event Bus) | GCP Pub/Sub *is* a single service that does both. Cloud Tasks and Eventarc live here too. |
| 14 (Streaming) | 13 | Renumbered after the 12-fusion. |
| 15–22 (API edge → Observability) | 14–21 | Direct mapping, shifted by 1. |
| 23 (Warehouse) + 24 (Query-on-storage) | **22** (BigQuery) | BigQuery is one engine; external tables + BigLake are first-class on the same surface. |
| 25–27 (ETL / Big-data / BI) | 23–25 | Direct mapping. |
| 28–32 (ML Training → NLP) | 26–30 | Direct mapping (all under the Vertex AI umbrella). |
| 33 (Forecasting / Personalization) | **31** (Forecasting / Personalization / Search) | Vertex AI Search is a first-class GCP-native surface; included here, not as a separate group. |
| 34 (IoT Device Gateway) + 36 (IoT Analytics) | **32** (IoT data flow — gap notice) | Cloud IoT Core deprecated 2023; the recommended pattern is Pub/Sub → Cloud Run / Dataflow → BigQuery, which already lives in groups 12 and 22. |
| 35 (IoT Edge Runtime) | 33 | Direct mapping. |

Net: 36 AWS groups → 33 GCP groups.

---

# At-a-glance tier rollup

The `T0 / T1 / T2` framing is load-bearing in `thesis.md` ("Service tiers" under Current evaluation) and `caravan_abstraction_v2.md` §4. Recap:

- **T0** — same wire API both sides; endpoint-URL or DSN env-var swap in user code suffices. No abstraction library required. Container-shaped compute primitives also sit here (one image, runs locally as docker-compose service or in cloud as Cloud Run / GKE / GCE).
- **T1** — different wire APIs cloud vs local; a structural abstraction layer is required (per the thesis stable design principle). Mature community libraries cover the well-known pairs (rig-core / litellm for LLMs including Gemini and Anthropic-on-Vertex; jsonwebtoken + JWKS for Identity Platform token verify; lettre / smtplib for SMTP via SendGrid or self-managed; whisper crates for STT; OpenCV / yolov8 for image analysis).
- **T2** — no OSS engine reproduces the service locally. `cloud_only:` provisioning marker. The Tier 2 deep-dive below sub-groups these, splits common from truly-niche, and per-niche-service decides between `yaml-registry`, `hand-tf`, and `skip`.

**GCP's T0 share is relatively larger than AWS's** because Google ships first-party emulators for several major services: `firestore-emulator` (Native + Datastore modes), `spanner-emulator` (regional features), `pubsub-emulator`, `bigtable-emulator`, `datastore-emulator`. `fake-gcs-server` is community but widely used. Where AWS users need DynamoDB-Local (Amazon-published) plus stitched-together community alternatives for the rest, GCP users have a more uniform local-emulator story for the data plane.

## T0 services (~28)

Compute primitives (run-the-container is the wire compat): Cloud Run (all three CPU billing modes), Cloud Run Jobs, Cloud Functions Gen2 (Cloud Run-backed), Cloud Functions Gen1 (legacy), GKE Standard / Autopilot, Compute Engine (on-demand / Spot / Sustained-Use / Committed-Use 1y/3y / Sole-Tenant), GCP Batch. Data plane (endpoint / DSN swap): GCS Standard + classes (Nearline / Coldline / Archive / Autoclass — via `fake-gcs-server`), Filestore Basic HDD / SSD / Enterprise / Zonal (via bind mount), Persistent Disk pd-balanced / pd-ssd / pd-standard / pd-extreme, Hyperdisk Balanced / Throughput, Local SSD, Cloud SQL Postgres / MySQL / SQL Server (real engines locally), AlloyDB Postgres (postgres-protocol-compat — columnar engine excluded; T2), Spanner regional (via `spanner-emulator`), Firestore Native + Datastore mode (via `firestore-emulator`), Bigtable (via `bigtable-emulator`), Memorystore for Redis / Memcached / Valkey (real engines locally), Pub/Sub Standard + Lite-deprecated (via `pubsub-emulator`), Cloud Tasks (via community `cloud-tasks-emulator`), Cloud Load Balancing for HTTP (locally use nginx / traefik), Cloud DNS public + private (coredns or `/etc/hosts` locally), Secret Manager (env vars locally), Cloud KMS (software keys; HSM is GCP-only), Cloud Scheduler (cron locally), Cloud Composer 2/3 (real Airflow), Cloud Logging (stdout / log driver), Cloud Monitoring custom metrics (via Managed Service for Prometheus → Prometheus locally), Dataproc (real Spark / Hadoop), Dataflow batch mode (Apache Beam DirectRunner — streaming reproducibility is partial, flagged in §23), Vertex AI Training (Vertex AI SDK has `local-mode`), GCE Sole-Tenant Nodes.

## T1 services (~6 hard pairs)

Each pair requires a structural abstraction; community libraries cover all of them today:

- **LLM** (Vertex AI Model Garden — Gemini 2.5 Pro / Flash / Flash-Lite / Nano + Anthropic Claude on Vertex + Llama / Mistral / Gemma) ↔ Ollama / vLLM. Bridge: rig-core (Rust), litellm (Python), langchaingo / eino (Go), Vercel AI SDK (TS). Same library set as Bedrock.
- **Token verification** (Identity Platform JWKS, Firebase Auth JWKS) ↔ local JWT issuer. Bridge: jsonwebtoken (Rust), authlib / python-jose (Python), golang-jwt (Go), jose (TS).
- **Email** (no native GCP service — SendGrid via Marketplace / Mailgun / cross-cloud SES) ↔ MailHog / Mailpit SMTP catcher. Bridge: lettre (Rust), smtplib (Python), gomail (Go), nodemailer (TS). The "cloud side" is provider-specific; the local side is uniform.
- **Speech-to-text** (Cloud Speech-to-Text + Chirp foundation) ↔ Whisper. Bridge: whisper-rs (Rust), openai-whisper (Python), similar elsewhere.
- **Image analysis / OCR** (Cloud Vision API + Video Intelligence + Document AI) ↔ OpenCV / YOLOv8 / Tesseract + layoutparser. Bridge: per-language community libraries.
- **NLP** (Natural Language API + Translation API) ↔ spaCy + argos-translate. Bridge: per-language libs.

Also Text-to-Speech (Polly analog: Standard + Neural2 + Studio voices) ↔ coqui-ai / piper sits at the T1 edge.

## T2 services (~22)

Every service tagged `T2` in the role-group tables below. Sub-grouped, split common-vs-niche, and judged for yaml-registry fit in the **Tier 2 deep-dive** section near the end of this file.

Headline list (canonical bucket in parens; full breakdown in the deep-dive):

- **Edge / CDN**: Cloud CDN, Media CDN, Google Cloud Armor.
- **Multi-region coordination**: Spanner multi-region, BigQuery cross-region replication, Cloud Load Balancing Global tier.
- **Managed ML / AI orchestration**: Vertex AI Vector Search, Vertex AI Search, Vertex AI Agent Builder, Vertex AI Pipelines, Recommendations AI / Discovery AI Retail, Vertex AI Model Garden orchestration, Document AI custom models.
- **Specialty storage variants**: AlloyDB Columnar Engine, BigLake, Datastream, Dataplex governance, Parallelstore, NetApp Volumes, Hyperdisk Storage Pools, Bigtable App Profiles, BigQuery (engine itself).
- **Identity / lifecycle / observability lock-in**: Identity Platform (user *lifecycle* admin), Firebase Authentication (lifecycle), IAM enforcement, Identity-Aware Proxy, Workforce Identity Federation, Access Transparency / Access Approval, Cloud Trace visualization, Cloud Profiler dashboards, Error Reporting dashboards, Application Performance Insights, Audit Logs (data access events).
- **API edge / orchestration lock-in**: API Gateway, Apigee X / Hybrid, Eventarc, Workflows execution engine, Pub/Sub Lite (deprecated), Cloud Composer GCP-native operators.
- **Data / BI lock-in**: Looker, Looker Studio, Looker Studio Pro, BigQuery (engine), BigLake, Dataplex, Datastream.
- **IoT vertical**: Cloud IoT Core (**deprecated 2023**), GDC Edge, Coral Edge TPU.
- **Deprecated / end-of-life**: Cloud IoT Core, Pub/Sub Lite, Runtime Config, Cloud Debugger, Datastore mode migration path, Recommendations AI legacy (non-Retail).

See **§Tier 2 deep-dive** below for each truly-niche entry's use case and practicality verdict (`yaml-registry` / `hand-tf` / `skip`).

---

# Web Stack Core (21 groups)

## 1. Compute — Function

| Service | Scale ceiling | Latency | Cost shape | Tier |
|---|---|---|---|---|
| **Cloud Functions Gen2** | 1,000 concurrent / function default; up to 1k+ by request | Cold start 1–3 s (container pull); warm <50 ms | $0.40 / M req + $0.000024 / vCPU-s + $0.0000025 / GiB-s | T0 |
| **Cloud Functions Gen1** (legacy) | 1,000 concurrent / function | Cold start 0.5–2 s | $0.40 / M req + $0.0000100 / GHz-s + $0.0000025 / GB-s | T0 |
| **Cloud Run (min-instances=0)** | 1,000 instances / service default (raisable to 10k+) | Cold start 200 ms–2 s | Same as Functions Gen2 | T0 |
| **Cloud Run (min-instances≥1)** | Same | No cold start (idle CPU billed) | Idle CPU: $0.0000027 / vCPU-s + $0.0000025 / GiB-s | T0 |

**When to pick which**: Default to **Cloud Run with min-instances=0** for new function-shape workloads — Functions Gen2 is now a thin wrapper on Cloud Run, and Cloud Run gives you the same scale-to-zero economics with more flexibility (custom containers, larger images, longer requests). Use Functions Gen1 only for code that *already* targets it and you can't easily migrate. Use min-instances≥1 when cold starts are user-visible and the idle-CPU bill is worth it (~$70/mo per warmed-instance for 1 vCPU + 512 MiB). Cloud Run's cold start is faster than Lambda's — pulling a container from Artifact Registry into a pre-warmed POP, no MicroVM boot.

**Hard limits worth knowing**: 60-min max request (HTTP), 3,600 s for Cloud Run Jobs; 32 GiB max memory + 8 vCPU; 32 MiB max request payload; container image up to several GiB; no `/tmp` filesystem (use in-memory `/tmp` against memory cap).

---

## 2. Compute — Container

| Service | Scale ceiling | Latency | Cost shape | Tier |
|---|---|---|---|---|
| **Cloud Run** (request-billed) | 1,000 instances / service default | Warm 1–10 ms; cold 200 ms–2 s | $0.40 / M req + $0.000024 / vCPU-s + $0.0000025 / GiB-s (CPU only during request) | T0 |
| **Cloud Run** (CPU-always-on) | Same | Warm 1–10 ms | $0.0000180 / vCPU-s + $0.0000020 / GiB-s | T0 |
| **Cloud Run Jobs** | 10,000 tasks / job; 10 parallel default | Task start 5–30 s | Per-vCPU-s + per-GiB-s + $0.00002 / task | T0 |
| **GKE Standard** | 15,000 nodes / cluster; 110 pods / node default | App-dependent | $0.10 / cluster-hr (control plane) + node compute | T0 |
| **GKE Autopilot** | Same upper bound; per-pod billing | App-dependent | $0.0445 / vCPU-hr + $0.0049 / GiB-hr (pod resources, no node mgmt) | T0 |

**When to pick which**: **Cloud Run** for "I have a Dockerfile, give me a URL" — most managed, least configurable, scale-to-zero by default. CPU-always-on mode (instance-based billing) for long-running services with steady traffic where the request-billed mode would charge for idle CPU anyway; break-even at ~25% sustained request load. **Cloud Run Jobs** for batch / one-shot tasks (replaces "spin up Cloud Run, exit after work, hope it scales down"). **GKE Autopilot** when you need real Kubernetes (Helm charts, k8s-native operators, multi-cloud portability) but don't want to manage nodes — Google bills for pod resources only. **GKE Standard** when you need control-plane access (custom CNI, GPU node pools, mixed instance types). EKS-equivalent on GCP is GKE Standard; Fargate-equivalent is Cloud Run; AWS App Runner-equivalent is also Cloud Run.

**Pricing gotcha**: GKE control plane bills $0.10/hr per cluster ($72/mo) — the per-cluster fee was removed for zonal clusters in early 2020 then re-added for all clusters in 2020. Cloud Run has a free tier (2M req + 360k vCPU-s + 180k GiB-s per month); Cloud Functions has its own free tier overlapping.

---

## 3. Compute — VM / Batch

| Service | Scale ceiling | Latency | Cost shape | Tier |
|---|---|---|---|---|
| **Compute Engine (on-demand)** | Per-instance-family quotas (vCPU-based); 24-vCPU default region quota, raisable | Boot 30–60 s | $0.067 / hr (e2-standard-2) → $30+ / hr (large GPU) | T0 |
| **Compute Engine Spot VMs** | Same | Same | 60–91% off on-demand; 30-sec preemption notice | T0 |
| **Sustained Use Discounts** | Automatic | — | Up to 30% off for >25% monthly utilization; no commitment | T0 |
| **Committed Use Discounts 1y / 3y** (resource) | Same | Same | 37% off 1y / 55% off 3y (resource-based, region-locked) | T0 |
| **Committed Use Discounts 1y / 3y** (spend-based, flexible) | Same | Same | 20% off 1y / 46% off 3y (flexible CUD, family-and-region-flexible) | T0 |
| **GCP Batch** | Manages GCE / Spot fleets for jobs | Job pickup 30 s–2 min | Underlying GCE cost only (Batch orchestration free) | T0 |
| **Sole-Tenant Nodes** | Dedicated hardware for compliance | Same | Per-node-hour + 10% premium | T0 |

**When to pick which**: Compute Engine on-demand for fresh experiments. **Spot VMs** for fault-tolerant batch + dev environments (CI runners, training jobs that checkpoint). **Sustained Use Discounts** are automatic — no opt-in needed; you just need to leave instances running. **Committed Use Discounts** once your steady-state baseline is provably stable (>50% utilization for >12 months) — flexible CUDs are usually the better deal than resource-based CUDs because they survive instance-family changes. **GCP Batch** for "I have a queue of jobs, run them" — the cheapest way to get fan-out without orchestration code. Sole-Tenant Nodes only for compliance (HIPAA, regulated finance) where shared tenancy is unacceptable.

---

## 4. Storage — Object

| Service | Scale ceiling | Latency | Cost shape (per GB-month / per 10k req) | Tier |
|---|---|---|---|---|
| **GCS Standard (regional)** | Unlimited; 5 TiB / object; ~5,000 RPS per bucket initially, auto-scales | First-byte 100–200 ms | $0.020 / GB-mo; $0.05 / 10k Class A (write/list); $0.004 / 10k Class B (read) | T0 |
| **GCS Standard (multi-region)** | Same | Same | $0.026 / GB-mo (us multi-region); $0.05 / 10k Class A | T0 |
| **GCS Standard (dual-region)** | Same; replicated across two regions | Same | $0.046 / GB-mo (us-east1 + us-west1) + reduced cross-region egress | T0 |
| **GCS Nearline** | Same; 30-day min storage duration | Same as Standard | $0.010 / GB-mo + $0.01 / GB retrieval | T0 |
| **GCS Coldline** | Same; 90-day min | Same | $0.004 / GB-mo + $0.02 / GB retrieval | T0 |
| **GCS Archive** | Same; 365-day min | Same | $0.0012 / GB-mo + $0.05 / GB retrieval (cheapest cloud storage on earth, tied with S3 Deep Archive) | T0 |
| **GCS Autoclass** | Same | Same | $0.020 / GB-mo Standard tier + per-object monitoring $0.0025 / 1k objects; auto-tiers to Nearline/Coldline/Archive after access-pattern observation | T0 |
| **Turbo Replication** (dual-region option) | RPO 15-min | Same | Adds ~$0.022 / GB-mo to dual-region storage | T0 |

**When to pick which**: **GCS Standard** for hot data <30 days. **Autoclass** when you can't predict access patterns and have >128 KiB objects — GCP's equivalent of S3 Intelligent-Tiering, generally cheaper. **Nearline** for known-cold backup-type data accessed monthly. **Coldline** for quarterly access. **Archive** for "I will probably never read this but legal says keep it 7 years." **Dual-region** when you need 99.95% SLA and survive single-region failures without paying full multi-region; **Multi-region** when you want highest availability and lowest read latency for globally-distributed readers. Turbo Replication for dual-region buckets that need a tight RPO.

**Hidden costs**: cross-region replication ($0.02/GB egress within continent), versioning (stores every version), and `LIST` operations are Class A (10× the cost of `GET`). Lifecycle transitions cost per-object — don't transition a billion tiny files. GCS has no per-prefix RPS ceiling like S3's, but newly-created buckets warm up over ~20 minutes to peak throughput.

---

## 5. Storage — File

| Service | Scale ceiling | Latency | Cost shape | Tier |
|---|---|---|---|---|
| **Filestore Basic HDD** | 1 TiB–64 TiB | ms (single-digit at scale) | $0.20 / GB-mo | T0 |
| **Filestore Basic SSD** | Same | sub-ms | $0.30 / GB-mo | T0 |
| **Filestore Enterprise** | 1–10 TiB; regional (multi-zone) | sub-ms | $0.30 / GB-mo + IOPS provisioning | T0 |
| **Filestore Zonal** (high-scale SSD) | Up to 100 TiB; single-zone | sub-ms | $0.35 / GB-mo (SSD); $0.20 / GB-mo (HDD) | T0 |
| **Parallelstore** | Up to 100 TiB; PB at higher tier; Lustre-protocol | sub-ms; >1 GB/s per client | $0.14 / GB-mo (provisioned throughput tied) | T2 |
| **NetApp Volumes** (GCP-native NetApp) | Per-account quotas; SnapMirror to on-prem | sub-ms | $0.20 / GB-mo (Premium); per-throughput-tier | T2 |

**When to pick which**: **Filestore Basic** for "Linux containers / GKE pods need shared NFS." **Filestore Enterprise** when you need multi-zone availability for shared file storage (NFS v3/v4.1). **Filestore Zonal** when you need high IOPS in one zone (HPC scratch, large-scale ML feature stores). **Parallelstore** when you're running ML training or HPC that needs >1 GB/s per client (the FSx Lustre analog on GCP, GA 2024). **NetApp Volumes** if you need NetApp-specific features (SnapMirror to on-prem, multi-protocol NFS+SMB). For Windows-AD-joined SMB shares specifically, NetApp Volumes or Compute-Engine-hosted Active Directory + SMB; no first-class Filestore-for-SMB. Never use Filestore as a database substitute — it's slow for many small files.

---

## 6. Storage — Block

| Service | Scale ceiling | Latency | Cost shape | Tier |
|---|---|---|---|---|
| **Persistent Disk pd-balanced** | 64 TiB / volume; up to 80k IOPS at scale | sub-ms | $0.10 / GB-mo (includes baseline IOPS) | T0 |
| **Persistent Disk pd-ssd** | 64 TiB / volume; up to 100k IOPS | sub-ms | $0.17 / GB-mo | T0 |
| **Persistent Disk pd-standard** (HDD) | 64 TiB / volume; throughput-oriented | ms (sequential good) | $0.04 / GB-mo | T0 |
| **Persistent Disk pd-extreme** | 64 TiB / volume; up to 120k IOPS provisioned separately | sub-ms | $0.125 / GB-mo + $0.065 / provisioned-IOPS-mo | T0 |
| **Hyperdisk Balanced** | 64 TiB / volume; capacity, IOPS, throughput independently provisioned | sub-ms | $0.10 / GB-mo + $0.005 / IOPS-mo + $0.04 / MiBps-mo | T0 |
| **Hyperdisk Throughput** | 32 TiB; capacity + throughput; HDD-like | ms | $0.04 / GB-mo + $0.04 / MiBps-mo | T0 |
| **Hyperdisk Extreme** | 64 TiB; capacity + high IOPS | sub-ms | $0.125 / GB-mo + $0.0072 / IOPS-mo | T0 |
| **Hyperdisk Storage Pools** | Shared capacity across volumes in a pool | sub-ms | Pool-level provisioning; can be cheaper for fleet usage | T2 |
| **Local SSD (NVMe)** | Per-instance (e.g., 24 × 375 GiB on certain n2 instances) | μs | Bundled with instance type | T0 |

**When to pick which**: **pd-balanced** by default — the modern default for boot disks and general workloads, baseline IOPS included in the GB price (no IOPS-provisioning surprise). **pd-ssd** when you need sustained higher IOPS without paying for provisioned IOPS separately. **Hyperdisk Balanced** for write-heavy OLTP that needs independent capacity / IOPS / throughput tuning — replaces pd-extreme in most scenarios (more flexible). **Hyperdisk Extreme** only for the highest IOPS workloads (>100k sustained). **pd-standard / Hyperdisk Throughput** for log / data archive volumes. **Local SSD** for "shard data, instance is replaceable" patterns (Cassandra, Redis-on-disk) — data dies with the instance.

---

## 7. Database — RDBMS

| Service | Scale ceiling | Latency | Cost shape | Tier |
|---|---|---|---|---|
| **Cloud SQL Postgres / MySQL / SQL Server** | 64 TiB storage; vertical-only to db-n2-highmem-128 (128 vCPU, 864 GB RAM) | sub-ms intra-zone; +1–2 ms multi-zone HA | $0.0413 / hr (db-perf-optimized-N-2) → $20+ / hr (large HA); + $0.17 / GB-mo (SSD) | T0 |
| **Cloud SQL Enterprise vs Enterprise Plus** | Same; Plus adds data cache + faster failover | Enterprise Plus shaves 30–50% off p99 reads | Plus tier ~30% premium on instance hourly | T0 |
| **AlloyDB Postgres** (standard) | 128 TiB; up to 20 read pool nodes; cluster auto-fails | sub-ms intra-zone; <60 s failover | $0.27 / vCPU-hr + $0.0353 / GiB-hr + $0.30 / GB-mo storage | T0 |
| **AlloyDB Columnar Engine** | Same data; columnar acceleration for analytics queries | 100× faster analytics; OLTP unchanged | Adds ~30% to instance hourly when enabled | T2 |
| **AlloyDB Omni** | Self-hosted AlloyDB binaries on any infra | sub-ms (your infra) | $0.0395 / vCPU-hr software fee (BYO compute) | T0 |
| **Spanner (regional)** | Unlimited; horizontally-scalable SQL | <10 ms reads; <30 ms writes | $0.90 / PU-hr (1 node = 1,000 PUs); $0.30 / GB-mo storage; **100-PU minimum** post-2023 change | T0 |
| **Spanner (multi-region)** | Multi-region active-active; virtually unlimited | <10 ms regional; cross-region writes coordinated | $3.00 / PU-hr (multi-region tier); $0.50 / GB-mo storage | T2 |

**When to pick which**: **Cloud SQL** for "I just want managed Postgres at reasonable cost." **Cloud SQL Enterprise Plus** when you need faster failover (~5s vs ~60s) and data-cache acceleration; pay the 30% premium only if those matter. **AlloyDB** for high-throughput Postgres apps that need >20 read pool nodes or columnar analytics on OLTP data — the Aurora Postgres analog on GCP, with first-party columnar engine. **AlloyDB Omni** if you want AlloyDB performance on your own infra (on-prem, AWS, edge). **Spanner regional** when single-region horizontal-scale SQL is the requirement (multi-tenant SaaS where one schema, many shards) — but the 100-PU minimum ($648/mo floor) makes it expensive for small workloads. **Spanner multi-region** is the multi-region active-active SQL gold standard, the only first-party offering for that shape on any cloud — the Aurora DSQL analog, with a much longer track record. Use only if cross-region active-active is a hard requirement.

**Common pitfall**: Spanner's per-PU pricing changed in 2023 from per-node to per-PU with a 100-PU floor — single-PU pricing for dev no longer exists; use `spanner-emulator` for dev/test instead of provisioning real instances.

---

## 8. Database — KV / Document NoSQL

| Service | Scale ceiling | Latency | Cost shape | Tier |
|---|---|---|---|---|
| **Firestore Native (regional)** | Unlimited; 1 MiB / document; auto-scales | single-digit ms p99 | $0.18 / 100k reads + $0.18 / 100k writes + $0.020 / 100k deletes + $0.18 / GB-mo (region-dependent) | T0 |
| **Firestore Native (multi-region)** | Same | <100 ms cross-region | ~50% premium on operation pricing | T0 |
| **Firestore in Datastore mode** | Same engine, legacy Datastore API | single-digit ms p99 | Same as Native | T0 |
| **Bigtable (SSD)** | Petabyte tables; row-keyed only, no secondary indexes | single-digit ms p99 | $0.65 / node-hr + $0.17 / GB-mo (SSD) | T0 |
| **Bigtable (HDD)** | Same | ms (sequential good) | $0.51 / node-hr + $0.026 / GB-mo (HDD) | T0 |
| **Bigtable (replicated multi-cluster)** | Multi-region active-active | sub-second cross-region | 2× per-cluster cost + cross-region transfer | T0 |
| **Datastore** (legacy) | Migrate to Firestore Native mode | single-digit ms | Same as Firestore Native | T0 |

**When to pick which**: **Firestore Native** for new document-shape workloads — automatic auto-scaling, no capacity planning, first-class real-time listeners (a thing DynamoDB lacks). **Firestore multi-region** when you need active-active geo-replication without the Bigtable complexity. **Bigtable** when you have row-key-ordered access patterns at petabyte scale (time-series telemetry, ad-tech, fraud detection) — but accept that *there are no secondary indexes*; design the row key carefully. **Bigtable multi-cluster** with app profiles for active-active failover (the Cassandra-like pattern). **Datastore mode** only if you have legacy Datastore code — migrate to Firestore Native for new builds.

**Trap**: Firestore charges per-document-read even if the document is in a query result — a 1000-doc query is 1000 reads. Pricing surprises come from `whereEquals` queries that look small but return many docs. Bigtable doesn't have a free tier; the smallest production cluster (3 nodes × $0.65/hr ≈ $1,400/mo) is the entry price.

---

## 9. Database — Cache

| Service | Scale ceiling | Latency | Cost shape | Tier |
|---|---|---|---|---|
| **Memorystore for Redis (Basic)** | Single-zone; up to 300 GB | sub-ms | $0.049 / GB-hr (M1 capacity tier) → cheaper at scale tiers | T0 |
| **Memorystore for Redis (Standard)** | Same; with replica for HA | sub-ms | $0.054 / GB-hr | T0 |
| **Memorystore for Redis Cluster** | Up to 250 TB; sharded with replication | sub-ms | $0.054 / GB-hr per shard | T0 |
| **Memorystore for Memcached** | Up to 5 TB per cluster | sub-ms | $0.0250 / GB-hr | T0 |
| **Memorystore for Valkey** (newer 2024 SKU) | Same shape as Redis Cluster; Valkey 7.x | sub-ms | Similar to Redis pricing | T0 |

**When to pick which**: **Memorystore for Redis Standard** for typical Redis-as-a-cache workloads with HA. **Memorystore for Redis Cluster** for sustained large-cache use cases (>300 GB). **Memorystore for Valkey** — the Redis fork created after Redis Labs changed licensing in 2024; community-driven, fully OSS, mostly compatible. Pick Valkey for new builds in 2026 unless you have Redis-Enterprise-specific feature lock-in. Memcached almost never the right choice in 2026 — Redis/Valkey do everything Memcached does and more.

**Note**: GCP has no first-party Redis-with-durability analog (MemoryDB on AWS). Use Memorystore Redis with snapshotting + cross-region snapshot replication as a workaround, or use Firestore for durable KV with sub-10ms latency.

---

## 10. Database — Time-series / Graph

| Service | Scale ceiling | Latency | Cost shape | Tier |
|---|---|---|---|---|
| **BigQuery (partitioned / clustered time-series tables)** | Petabyte scale; ingestion via streaming inserts | seconds query latency typical | $6.25 / TiB scanned (on-demand) + $0.020 / GB-mo storage (active) | T2 (engine) |
| **Bigtable (time-series row-key pattern)** | Petabyte; high-RPS writes | ms | See group 8 pricing | T0 |
| **Spanner Graph** (2024 announce; SQL + graph queries) | Spanner data limits | <10 ms | Same as Spanner (group 7) | T0/T2 (regional/multi-region split) |
| **No dedicated graph DB** | — | — | — | — |

**When to pick which**: **BigQuery** for analytical time-series queries (aggregate dashboards, hourly rollups, ad-hoc analysis over metrics). Partition by ingestion time, cluster by entity ID. **Bigtable** for high-RPS write-heavy time-series with low-latency reads (IoT telemetry, observability metric storage, ad-tech bid history) — the InfluxDB / Cassandra time-series pattern, but row-keyed. **Spanner Graph** for SQL + graph traversal queries on the same data (fraud rings, knowledge graphs at SQL scale). **Note**: GCP has no first-party Neptune-equivalent (dedicated graph database). For pure-graph workloads, run open-source Neo4j on GCE or use Spanner Graph if SQL is acceptable.

---

## 11. Database — Search / Vector

| Service | Scale ceiling | Latency | Cost shape | Tier |
|---|---|---|---|---|
| **Vertex AI Vector Search** (formerly Matching Engine) | Billions of vectors; ScaNN-backed ANN | 10–50 ms vector query | Per-deployed-index node-hr + per-query + per-GB stored | T2 |
| **Vertex AI Search** (RAG / enterprise search) | Up to ~10M docs Enterprise; fully-managed | sub-second | $4 / 1k queries (Search Standard) → $12 / 1k (Search Enterprise) + per-GB indexed | T2 |
| **AlloyDB pgvector + ScaNN index** | Same as AlloyDB Postgres | 10–100 ms ANN query | Same as AlloyDB Postgres | T0 |
| **Cloud SQL pgvector** | Same as Cloud SQL Postgres | 10–100 ms ANN | Same as Cloud SQL | T0 |
| **BigQuery Vector Search** | Embeddings stored in BigQuery, queryable via SQL | seconds query (BQ engine) | $6.25 / TiB scanned + storage; vector index in BQ is free | T2 (engine) |

**When to pick which**: **AlloyDB pgvector + ScaNN** when you already run Postgres-shaped data and your vector count fits in a single AlloyDB cluster (<100M vectors) — saves an entire system, gets ScaNN's ANN performance with relational queries. **Cloud SQL pgvector** for smaller-scale (<10M vectors) Postgres-native vector storage. **Vertex AI Vector Search** when you need a dedicated ANN service at scale (>100M vectors, low-latency queries from many concurrent clients) — the gold standard for ANN on GCP, ScaNN-backed (the original ScaNN paper is Google's). **Vertex AI Search** for "I have docs, give me RAG over them" — managed retrieval + ranking + answer synthesis; the Bedrock Knowledge Bases analog. **BigQuery Vector Search** when your vectors live alongside analytical data and you tolerate seconds-latency queries (batch-shaped RAG, analytics over embeddings) — cheapest cold vector storage when you already use BigQuery.

---

## 12. Messaging — Pub/Sub & Event Bus

*(GCP-native fusion of the AWS file's groups 12 + 13. Pub/Sub is a single service that does both queue and topic semantics; Cloud Tasks is the work-queue specialist; Eventarc is the event routing layer.)*

| Service | Scale ceiling | Latency | Cost shape | Tier |
|---|---|---|---|---|
| **Pub/Sub topic + single subscription** (queue semantics) | Unlimited throughput | 100 ms typical | $40 / TiB throughput (in or out) + $0.40 / GB subscription storage | T0 |
| **Pub/Sub topic + N subscriptions** (fan-out) | Unlimited throughput | 100 ms typical | Same per-TiB pricing per subscription | T0 |
| **Pub/Sub ordered delivery** | Per-ordering-key sequential | 100 ms+ (ordering adds latency) | Same as Pub/Sub | T0 |
| **Pub/Sub Lite** (**deprecated 2024**) | Replaced by native Pub/Sub | — | Was cheaper for high-throughput; EOL | T2 (deprecated) |
| **Cloud Tasks** | 500 RPS / queue default; raisable to 100k | <100 ms enqueue | $0.40 / M req (first 1M / mo free) | T0 |
| **Eventarc** | Triggers from 130+ GCP sources | <500 ms | $0.50 / M events (custom + 3rd-party); GCP-native events free | T2 |
| **Pub/Sub schema registry** | Per-topic JSON / Avro / Protobuf schema | — | Free (validates publish) | T0 |

**When to pick which**: **Pub/Sub** for almost every greenfield messaging need — one service covers fan-out (multiple subscriptions per topic), queueing (single subscription), and topic-shaped pub/sub. **Cloud Tasks** when you need rate-limited HTTP-target work-queue semantics (Cloud Run handlers, deduplication, per-task scheduling) — different shape from Pub/Sub (push to HTTP endpoint with retries, not pub/sub broadcast). **Eventarc** when the routing is content-based (filter expressions on CloudEvents source/type) or you want managed GCP-resource-event triggers (GCS object create → Cloud Run) — Eventarc sits on top of Pub/Sub but adds CloudEvents normalization and event-source SDK. **Ordered delivery** on Pub/Sub only when per-ordering-key sequential delivery is required — costs latency, only worth it when actually needed.

**Often-missed**: Pub/Sub Lite was Google's cheaper Kafka-style alternative — deprecated 2024 with a migration path to native Pub/Sub (which got cheaper). For new builds, use native Pub/Sub. For Kafka semantics specifically (consumer groups with rebalancing, exactly-once via transactions), use Confluent Cloud on GCP via marketplace (group 13).

---

## 13. Messaging — Streaming

| Service | Scale ceiling | Latency | Cost shape | Tier |
|---|---|---|---|---|
| **Pub/Sub** (stream ingest) | Unlimited throughput | <500 ms p99 | $40 / TiB throughput + storage | T0 |
| **Pub/Sub Lite** (**deprecated 2024**) | EOL | — | — | T2 (deprecated) |
| **Confluent Cloud on GCP** (Kafka via Marketplace) | Cluster-sized; petabyte | <10 ms | Confluent partner pricing (separate billing) | T0 |
| **Dataflow** (Apache Beam streaming consumer) | Auto-scales workers | App-dependent | $0.069 / vCPU-hr (streaming Shuffle worker) + $0.004 / GiB-hr | T1 (DirectRunner local; Dataflow streaming features differ) |

**When to pick which**: **Pub/Sub** for native GCP pub/sub-shape streaming — simpler than Kafka partition planning. **Confluent Cloud on GCP** when you need real Kafka semantics (consumer groups with rebalancing, exactly-once via transactions, broad Kafka ecosystem tooling) — but it's a separate Confluent bill on top of GCP. **Dataflow** as the consumer-side streaming compute (Apache Beam, Python/Java/Go SDKs) — equivalent role to Kinesis Data Analytics / MSK + Flink on AWS.

**Pricing note**: Pub/Sub's $40/TiB is throughput-based (counts both in and out separately) — easier to reason about than Kinesis's shard-based provisioned model. For high-throughput Kafka-like workloads at >100 TiB/mo, Confluent Cloud on GCP may be cheaper than Pub/Sub depending on partition count.

---

## 14. API / Web Edge

| Service | Scale ceiling | Latency | Cost shape | Tier |
|---|---|---|---|---|
| **API Gateway** | 1k RPS / API default (raisable) | +5–10 ms overhead | $3.00 / M API calls (first 2M / mo free) | T2 |
| **Apigee X** | Enterprise-scale; multi-region | depends on routing | Eval tier ~$500 / mo; Standard ~$14k+ / yr; Enterprise much more | T2 |
| **Apigee Hybrid** | Self-hosted runtime + GCP control plane | depends | Per-environment + per-runtime | T2 |
| **Global External HTTP(S) LB** | Global anycast; millions of req/s | <10 ms LB overhead | $0.025 / forwarding-rule-hr + $0.008 / GB ingress | T0 |
| **Regional External HTTP(S) LB** | Per-region; same protocol | <10 ms | Same as Global | T0 |
| **Internal HTTP(S) LB** | VPC-internal | <10 ms | Same as External | T0 |
| **Network LB (TCP/UDP passthrough)** | Per-region; millions of req/s | <1 ms LB overhead | $0.025 / forwarding-rule-hr | T0 |
| **Internal Network LB** | VPC-internal L4 | <1 ms | Same as Network LB | T0 |

**When to pick which**: **API Gateway** for managed-API surface on top of Cloud Run / Cloud Functions / GKE backends — OpenAPI-driven config, simpler than Apigee. **Apigee X** for enterprise API management (developer portals, monetization, complex routing, OAuth flows, quota management) — heavy and expensive but covers the API-product use case. **Global External HTTP(S) LB** for almost any Cloud Run / GKE service that needs a public anycast IP — the SSL termination + multi-region routing + Cloud CDN integration point. **Regional LB** when you don't need anycast (single-region apps, cheaper IP allocation). **Network LB** for TCP/UDP services (gRPC at L4, MQTT, gaming UDP).

**Often-overlooked**: A Global External HTTP(S) LB with Cloud Armor + Cloud CDN attached is GCP's full-stack web edge — equivalent to ALB + WAF + CloudFront stitched together on AWS. The pricing is per-forwarding-rule, not per-LCU; predictable.

---

## 15. CDN

| Service | Scale ceiling | Latency | Cost shape | Tier |
|---|---|---|---|---|
| **Cloud CDN** | Global; tens of TB/sec aggregate | <50 ms edge fetch | $0.08 / GB cache egress (NA/EU) + $0.0075 / 10k cache fills | T2 |
| **Media CDN** | Anycast premium for video / large-scale streaming | <30 ms | Premium per-GB egress pricing (negotiated for high-volume) | T2 |
| **Google Cloud Armor** (adjacent — WAF / DDoS) | Per-LB attached | <1 ms | $5 / policy-mo + $0.75 / M req inspected | T2 |

**When to pick which**: **Cloud CDN** for static-asset caching tied to an HTTP(S) LB origin — the CloudFront analog. Cheap and integrated. **Media CDN** for large-scale video streaming (live + VOD, sports / news / OTT) — separate POPs, optimized for video, sold separately. **Cloud Armor** for WAF + DDoS protection attached to an HTTP(S) LB — Managed Protection Plus tier adds Adaptive Protection (ML-based DDoS detection).

**GCP gap**: there's no Lambda@Edge / CloudFront Functions equivalent on GCP today. Cloud Run is regional, not edge-located. Teams needing edge compute often reach for Cloudflare Workers as a third-party complement to Cloud CDN.

---

## 16. DNS

| Service | Scale ceiling | Latency | Cost shape | Tier |
|---|---|---|---|---|
| **Cloud DNS (public zones)** | Unlimited records / zone | global anycast resolver, <30 ms | $0.20 / zone-mo (first 25); + $0.40 / M queries | T0 |
| **Cloud DNS (private zones)** | VPC-scoped | <10 ms intra-VPC | Same pricing | T0 |
| **Cloud DNS Forwarding** | VPC ↔ on-prem DNS | <10 ms | + per-query | T2 |
| **Cloud DNS Policies** | Per-VPC inbound / outbound policy | — | + per-policy + per-query | T2 |

**When to pick which**: **Cloud DNS public zones** for almost all GCP-hosted apps. **Cloud DNS private zones** for service-discovery within VPC. **Forwarding / Policies** only for hybrid cloud needing on-prem DNS resolution.

**Note**: GCP doesn't have a Route 53 ALIAS-equivalent that's free for AWS-internal targets. Anycast routing across regions is available via Global External HTTP(S) LB + Cloud DNS together.

---

## 17. Identity / Auth

| Service | Scale ceiling | Latency | Cost shape | Tier |
|---|---|---|---|---|
| **Identity Platform** (GCIP) | Unlimited MAUs per project; multi-tenant capable | <100 ms typical | First 50k MAU free; $0.0055 / MAU (50k–100k); cheaper above | T2 (lifecycle); T1 for JWKS token verify via community libs |
| **Firebase Authentication** (consumer SKU) | Same engine, consumer pricing | <100 ms | First 50k MAU free; similar tiering | T2 (lifecycle); T1 token verify |
| **Cloud IAM** | Unlimited identities; 1.5k roles / project; org-scoped | API <100 ms | Free (the resource) | T2 (enforcement is GCP-only) |
| **Workforce Identity Federation** | Enterprise federation into GCP | <500 ms | Free | T2 |
| **Workload Identity Federation** | Workload-side credential exchange (no service-account keys) | <100 ms | Free | T2 |
| **Identity-Aware Proxy** (IAP) | Per-LB attached; zero-trust gating | <100 ms LB overhead | $0/hr (included with LB); per-request inspection counted | T2 |

**When to pick which**: **Identity Platform** for B2C apps and "I need users to sign up with email / Google / social IdPs" — the Cognito User Pools analog. Better DX than Cognito (cleaner SDK, real-time auth state). **Firebase Authentication** is the same engine, packaged for Firebase apps; pick the GCIP SKU for production enterprise. **Cloud IAM** for service-to-service auth via service accounts and Workload Identity Federation (preferred over downloading service-account JSON keys). **IAP** for "I want SSO-gated access to internal apps without VPN" — attach to an HTTP(S) LB, get OAuth-gated access to backend Cloud Run / GKE / GCE workloads.

**Common pitfall**: Identity Platform's free tier covers most small apps (50k MAU). At scale, the MAU pricing is friendlier than Cognito's per-MAU at the same band, but verify against current pricing.

---

## 18. Secrets / Config

| Service | Scale ceiling | Latency | Cost shape | Tier |
|---|---|---|---|---|
| **Secret Manager** | Per-secret quotas; 65 KiB / version | <100 ms cached | $0.06 / secret-version-mo + $0.03 / 10k access calls | T0 |
| **Cloud KMS** (software keys) | Unlimited keys (soft); per-keyring | <10 ms | $0.06 / key-mo + $0.03 / 10k cryptographic ops | T0 |
| **Cloud HSM** (FIPS 140-2 Level 3) | Same | <10 ms | $1.00 / key-mo + $0.03 / 10k ops | T2 (HSM hardware is GCP-only) |
| **Cloud EKM** (External Key Manager) | Keys hosted externally | +external KMS latency | $3.00 / key-mo + $0.03 / 10k ops | T2 (external KMS integration) |
| **Runtime Config** (**deprecated**) | — | — | Was free | T2 (deprecated) |
| **Config Connector** | Kubernetes CRDs that provision GCP resources | — | Free (component of GKE Config Management) | T0 |

**When to pick which**: **Secret Manager** for all secrets (no free Parameter Store analog like AWS has). The $0.06/version pricing means rotating secrets monthly is cheap; not a barrier. **Cloud KMS software keys** for envelope-encrypting application data (GCS, Cloud SQL, app-level secrets). **Cloud HSM** for FIPS 140-2 Level 3 compliance (regulated workloads). **Cloud EKM** when key material *must* live outside Google (regulated workloads with foreign-key requirements).

**Cost note**: GCP has no SSM Parameter Store Standard tier (free) equivalent. Every secret costs $0.06/version-mo. For env-var-shaped non-secret config, use environment variables directly on Cloud Run / GKE (free), not Secret Manager.

---

## 19. Workflow / Scheduling

| Service | Scale ceiling | Latency | Cost shape | Tier |
|---|---|---|---|---|
| **Workflows** (serverless orchestration) | 100 concurrent workflows / project default | sub-second transitions | $0.01 / 1k internal steps + $0.025 / 1k external steps (first 5k free) | T2 (no canonical local engine) |
| **Cloud Scheduler** | Up to 5k jobs / project | sub-sec | $0.10 / job-mo (first 3 jobs free) | T0 |
| **Cloud Composer 2** (managed Airflow) | Per-environment size | depends on DAGs | Small env ~$300–400 / mo; larger envs scale | T0 |
| **Cloud Composer 3** (newer; serverless-ish) | Same | Same | Per-vCPU + per-GiB + storage | T0 |
| **Cloud Tasks** (delayed dispatch) | See group 12 | <100 ms | See group 12 | T0 |

**When to pick which**: **Workflows** for serverless orchestration of HTTP services (Cloud Run choreography, simple multi-step API calls) — the Step Functions Express analog. Limited compared to Step Functions (no Distributed Map, smaller ASL-like surface); YAML DSL. **Cloud Scheduler** for cron + one-shot schedules (Pub/Sub target → Cloud Run handler). **Cloud Composer** when your team already lives in Airflow DAGs (data engineering teams primarily). **Cloud Tasks** when you need delayed dispatch with retries (a workflow primitive, not a full workflow engine).

**GCP gap**: no Step Functions Standard-equivalent for long-running (multi-day) workflows with built-in checkpointing. Workflows tops out around minutes-to-hours. For long-running orchestration, teams build on Cloud Tasks + Firestore state, or run Temporal on GKE.

---

## 20. Email / Notifications

| Service | Scale ceiling | Latency | Cost shape | Tier |
|---|---|---|---|---|
| **NO native first-party email service.** | — | — | — | — |
| **SendGrid via Marketplace** (Twilio-owned) | Per-plan | seconds | Free tier 100/day; $20/mo for 50k emails (Essentials); scales up | T1 (lettre/smtplib bridge via SMTP) |
| **Mailgun via Marketplace** | Per-plan | seconds | Pay-as-you-go from $1/1k emails | T1 |
| **Self-managed SMTP on GCE** | Per-instance | seconds | GCE cost only; deliverability is on you | T1 |
| **AWS SES from GCP** (cross-cloud) | Per-account quota | seconds | $0.10/1k emails (out) + cross-cloud egress | T1 |
| **Firebase Cloud Messaging** (FCM) — mobile push | iOS/Android/Web push | varies by APNs/FCM | Free for unlimited messages | T2 |
| **Twilio for SMS** (no native GCP SMS) | Per-account spend | seconds | Twilio per-message pricing | T1 |

**When to pick which**: GCP has **no first-party email service**. For transactional email (signups, receipts), use **SendGrid via Marketplace** as the closest equivalent to SES — bills through your GCP account, no separate vendor onboarding. For higher volume or marketing email, **Mailgun** or self-managed on GCE. **FCM** is the de facto choice for mobile push (free + cross-platform); AWS SNS Mobile Push has no native GCP equivalent. **SMS** goes to Twilio (most common) or other partners — no native GCP SMS service.

**Architecture note**: in user code, abstract email behind a `lettre` / `smtplib` SMTP client interface; provider swap is then a config change (SMTP creds + endpoint). This is the same T1 pattern as SES vs MailHog locally — the cloud target is just "whoever has SMTP."

---

## 21. Observability

| Service | Scale ceiling | Latency | Cost shape | Tier |
|---|---|---|---|---|
| **Cloud Logging** | Unlimited | seconds ingestion | First 50 GiB / project / mo free; $0.50 / GiB ingested + $0.01 / GiB-mo storage (default 30d retention) | T0 (via stdout / log driver) |
| **Cloud Logging Log Analytics** (BQ-backed) | Per-bucket; query via BigQuery | seconds | $5 / TiB scanned + $0.020 / GiB-mo storage | T0 |
| **Log Router** (sinks) | Free routing to GCS / Pub/Sub / BQ | — | Free; only destinations bill | T0 |
| **Cloud Monitoring** (custom metrics) | 1-sec resolution available | 1-min standard | First 150 MiB / mo free; $0.2580 / MiB after | T0 (EMF analog: Managed Service for Prometheus) |
| **Managed Service for Prometheus** | Petabyte scale | sub-sec | $0.36 / M samples ingested; $0.06 / M samples queried | T0 (Prometheus locally) |
| **Cloud Trace** | Distributed tracing | — | First 2.5M spans / mo free; $0.20 / M after | T1 (OTel → Jaeger local) |
| **Cloud Profiler** | Continuous profiling | — | Free | T2 (visualization is GCP-only) |
| **Error Reporting** | Automatic error aggregation | — | Free | T2 (UI is GCP-only) |
| **Application Performance Insights** | Auto-instrument APM | — | Per-service-mo + per-signal | T2 |
| **Cloud Debugger** (**deprecated 2023**) | — | — | — | T2 (deprecated) |
| **Audit Logs** | Admin + Data Access events | — | First 50 GiB free; admin logs always free; data access logs counted | T2 (triggering doesn't reproduce locally) |
| **Access Transparency / Access Approval** | Google-side access logging | — | Free (Premium support) | T2 |

**When to pick which**: **Cloud Logging** is the default destination — stdout from Cloud Run / GKE flows here automatically. The 50 GiB/project free tier is generous; many apps stay free-tier. **Log Analytics** when you need ad-hoc SQL queries over logs (uses BigQuery engine, $5/TiB scanned — cheaper than CloudWatch Logs Insights). **Managed Service for Prometheus** for new monitoring code — first-class Prometheus query API, scales further than self-hosted. **Cloud Trace** for basic distributed tracing — fine for HTTP request flows, less depth than Datadog/Honeycomb. **Audit Logs** for compliance (always-on for admin events).

**Cost trap**: Cloud Logging at $0.50/GiB is high-margin (like CloudWatch Logs). Filter aggressively at the source — `gcloud logging` exclusion filters drop logs before billing.

---

# Data / Analytics (4 groups)

## 22. Analytics — BigQuery (warehouse + query-on-storage)

*(GCP-native fusion of the AWS file's groups 23 + 24. BigQuery is one engine; external tables, BigLake, and native Iceberg are first-class on the same surface.)*

| Service | Scale ceiling | Latency | Cost shape | Tier |
|---|---|---|---|---|
| **BigQuery on-demand** (pay-per-query) | Petabyte scale; auto-scales slots transparently | sub-sec to seconds | $6.25 / TiB scanned (free 1 TiB / mo) | T2 (engine) |
| **BigQuery Editions — Standard** (autoscaling) | Same | Same | $0.04 / slot-hr; min 100 slots autoscaling | T2 |
| **BigQuery Editions — Enterprise** | Same; supports Capacity Commitments | Same | $0.06 / slot-hr; min 100 slots autoscaling | T2 |
| **BigQuery Editions — Enterprise Plus** | Same; cross-region disaster recovery | Same | $0.10 / slot-hr; min 100 slots autoscaling | T2 |
| **BigQuery Reservations** (capacity commitments) | Same | Same | 1y commit ~20% off; 3y commit ~40% off Editions hourly | T2 |
| **BigQuery Storage** (Active) | — | — | $0.020 / GiB-mo (logical) or $0.010 / GiB-mo (physical, newer) | T2 |
| **BigQuery Storage** (Long-term, >90d untouched) | — | — | $0.010 / GiB-mo (logical) or $0.005 / GiB-mo (physical) | T2 |
| **BigQuery Omni** (cross-cloud query) | Read S3 / ADLS from BigQuery | seconds | Per-TiB scanned + per-region transfer | T2 |
| **BigQuery external tables** (federated query) | Reads GCS / Bigtable / Drive | seconds | $6.25 / TiB scanned + source-system I/O | T2 |
| **BigLake** (open-format tables) | Iceberg / Hudi / Delta on GCS | seconds | $6.25 / TiB scanned + BigLake metadata fee | T2 |
| **BigQuery for Apache Iceberg** (native) | Iceberg-native BigQuery tables | seconds | Same as BigQuery storage + Iceberg metadata | T2 |
| **Dataplex** (zones + governance routing) | Data lake metadata + governance | — | $0.02 / hr lakeID + per-scan + per-quality-job | T2 |

**When to pick which**: **BigQuery on-demand** for ad-hoc analytics on small-to-medium data — the cheapest way for "I have a few TiB and run queries occasionally." **Editions Standard** when sustained slot usage justifies a baseline reservation. **Enterprise / Enterprise Plus** for production workloads needing query-level priority (Plus adds cross-region replication for DR). **BigQuery Reservations** with 1y/3y commitments once your slot baseline is provably stable. **External tables / BigLake** to query S3/GCS data without ingest — Iceberg support is first-class in 2026. **BigQuery Omni** if you have data in AWS / Azure and want to query it from a single BigQuery surface without copying.

**Trap**: BigQuery on-demand at $6.25/TiB scanned (was $5 pre-July-2023) can dominate the bill if you `SELECT *` instead of selecting columns (BigQuery is columnar; you only pay for columns scanned). Always project columns; partition + cluster tables; use BI Engine for hot dashboards.

---

## 23. Analytics — ETL / Catalog

| Service | Scale ceiling | Latency | Cost shape | Tier |
|---|---|---|---|---|
| **Dataflow** (Apache Beam batch) | Auto-scales workers; petabyte scale | minutes startup | $0.056 / vCPU-hr (batch worker) + $0.004 / GiB-hr + storage | T1 (DirectRunner locally; streaming features diverge) |
| **Dataflow** (Apache Beam streaming) | Same | sub-second | $0.069 / vCPU-hr (streaming Shuffle worker) + $0.004 / GiB-hr | T1 |
| **Cloud Data Fusion** (CDAP-based, UI) | Per-instance | depends | $1.80 / hr (Basic) → $4.20 / hr (Enterprise) + per-pipeline | T2 |
| **Datastream** (CDC source → BQ / GCS / SQL targets) | Per-stream | seconds-minute lag | $0.36 / GiB processed + $0.024 / GiB stored | T2 |
| **Dataplex** (catalog + governance, supersedes Data Catalog) | Org-wide metadata | — | See group 22 | T2 |
| **Storage Transfer Service** | Petabyte-scale transfer (S3 / Azure / on-prem / GCS) | depends | Per-GiB depending on source | T0 |

**When to pick which**: **Dataflow** for traditional batch ETL with Apache Beam (Python/Java SDKs) — Spark-equivalent capability with Google's auto-scaling model. **Cloud Data Fusion** for UI-driven ETL (data analysts, no-code) — CDAP under the hood. **Datastream** for CDC from operational databases (Cloud SQL, AlloyDB, Oracle, MySQL on-prem) into BigQuery / GCS — Aurora zero-ETL analog. **Dataplex** for org-wide data catalog + quality / governance (the Glue Data Catalog + Lake Formation analog combined into one product). **Storage Transfer Service** for one-time or scheduled bulk transfers from other clouds or on-prem.

---

## 24. Analytics — Big-data Compute

| Service | Scale ceiling | Latency | Cost shape | Tier |
|---|---|---|---|---|
| **Dataproc on Compute Engine** | Thousands of nodes; full Hadoop/Spark/HBase/Flink/Presto | minutes startup | $0.01 / vCPU-hr Dataproc surcharge + GCE cost | T0 |
| **Dataproc Serverless** (for Spark) | Auto-scales workers | sub-minute first job | $0.06 / vCPU-hr base + $0.0067 / GiB-hr | T0 |
| **Dataproc on GKE** | Run Spark on existing GKE | depends | $0.01 / vCPU-hr Dataproc surcharge + GKE cost | T0 |
| **Dataproc Metastore** (Hive metastore as-a-service) | Per-instance | <100 ms | $0.40 / hr (Developer) → $0.79 / hr (Enterprise) | T0 |

**When to pick which**: **Dataproc Serverless** for new Spark workloads — no cluster sizing, pay-per-job. **Dataproc on Compute Engine** when you want long-lived clusters with custom bootstrap actions or non-Spark workloads (HBase, Presto, Flink). **Dataproc on GKE** when your org runs everything on Kubernetes. **Dataproc Metastore** to centralize Hive metadata across multiple Dataproc + BigQuery + Dataflow consumers.

---

## 25. Analytics — BI

| Service | Scale ceiling | Latency | Cost shape | Tier |
|---|---|---|---|---|
| **Looker** (full BI platform) | Per-user dashboards; semantic layer (LookML) | sub-second cached | License-based; ~$5k/mo Standard → $50k+/mo Enterprise (tiered by viewers + developers) | T2 |
| **Looker Studio** (free; Data Studio successor) | Per-user dashboards; no semantic layer | sub-second cached | Free | T2 |
| **Looker Studio Pro** | Adds workspace + sharing controls | Same | $9 / user / mo + per-asset pricing | T2 |

**When to pick which**: **Looker** when you need a semantic modeling layer (LookML) and enterprise governance — Google's flagship BI tool, comparable to Tableau / PowerBI in capabilities. **Looker Studio** (free) for ad-hoc dashboards over BigQuery / Sheets / generic sources — the Data Studio rebrand. **Looker Studio Pro** when free-tier Looker Studio falls short on collaboration / sharing controls but Looker proper is overkill.

**Note**: Looker and Looker Studio are *different products* despite the shared name — Looker is the post-Google-acquisition flagship; Looker Studio is the rebranded Data Studio (free tier). They share branding, not architecture.

---

# ML / AI (6 groups)

## 26. ML — Training / Serving Platform

| Service | Scale ceiling | Latency | Cost shape | Tier |
|---|---|---|---|---|
| **Vertex AI Training** (on-demand) | Per-job instance fleet (1–100s) | minutes startup | $0.07 / hr (n1-standard-4) → $40+ / hr (a3-highgpu-8g with H100) | T0 (SDK `local-mode`) |
| **Vertex AI Training** (Spot/preemptible) | Same | Same + checkpoint discipline | Up to 70% off | T0 |
| **Vertex AI Online Prediction** | Per-endpoint auto-scaling | <100 ms typical | Per-instance hourly + per-prediction | T0 |
| **Vertex AI Batch Prediction** | Run on a dataset, save to GCS | minutes | Per-instance | T0 |
| **Vertex AI Workbench** | Managed JupyterLab instances | varies | Per-instance hourly | T0 (Jupyter OSS base) |
| **Vertex AI Pipelines** (managed Kubeflow) | Per-pipeline run | seconds startup | $0.03 / pipeline-run + per-step compute | T2 |
| **Vertex AI Experiments** | Experiment tracking | — | Free (storage in GCS) | T0 |
| **Vertex AI Model Registry** | Centralized model registry | — | Free (storage in GCS) | T0 |

**When to pick which**: **Vertex AI Training** for managed model training — Spot tier when your training is checkpoint-friendly. **Vertex AI Online Prediction** for steady inference traffic. **Vertex AI Batch Prediction** for "run model on this whole dataset and save results." **Vertex AI Pipelines** when you need MLOps orchestration (training → validation → deployment) — managed Kubeflow Pipelines, the closest analog to SageMaker Pipelines. **Vertex AI Workbench** for the IDE / notebook experience.

**Note**: GCP doesn't have a SageMaker Serverless Inference equivalent (the < 200-concurrent-invocations-per-endpoint model). Cloud Run with min-instances=0 + a model loaded at startup is the GCP pattern for serverless inference.

---

## 27. ML — Foundation Models (Vertex AI Model Garden)

| Service | Scale ceiling | Latency | Cost shape | Tier |
|---|---|---|---|---|
| **Vertex AI — Gemini 2.5 Pro** | Per-project quota; on-demand or provisioned | 1–5 sec to first token | $1.25 / M input (≤200K context) → $2.50 / M input (>200K); $10 / M output (≤200K) → $15 / M output (>200K) | T1 (rig-core / litellm bridge) |
| **Vertex AI — Gemini 2.5 Flash** | Same | <1 sec to first | $0.30 / M input + $2.50 / M output | T1 |
| **Vertex AI — Gemini 2.5 Flash-Lite** | Same | <1 sec | $0.10 / M input + $0.40 / M output | T1 |
| **Vertex AI — Gemini 2.5 Nano** (on-device + cloud) | Same | <1 sec | Cheaper still (cloud); on-device free | T1 |
| **Vertex AI — Anthropic Claude Opus 4.7** | Per-project quota | 1–5 sec to first | $5 / M input + $25 / M output (mirrors Bedrock; Anthropic prices are uniform across providers) | T1 |
| **Vertex AI — Anthropic Claude Sonnet 4.6** | Same | <1 sec to first | $3 / M input + $15 / M output | T1 |
| **Vertex AI — Anthropic Claude Haiku 4.5** | Same | <1 sec | $1 / M input + $5 / M output | T1 |
| **Vertex AI — Llama / Mistral / Gemma / DeepSeek** | Per-model quotas | varies | Per-model token pricing | T1 |
| **Vertex AI — Imagen** (image gen) | Per-project quota | seconds | Per-image (Imagen 3 ≈ $0.04 / image) | T1 (Stable Diffusion bridge locally) |
| **Vertex AI — Veo** (video gen) | Per-project quota | minutes | Per-second-of-video | T2 |
| **Vertex AI — Lyria** (music gen) | Per-project quota | seconds-minutes | Per-track | T2 |
| **Vertex AI Provisioned Throughput** | Reserved capacity | predictable | Per-model-hour commitment | T1 |
| **Vertex AI Agent Builder** | Orchestrated tool-use over models | adds 1–3 sec | Per-invocation + underlying model tokens | T2 |

**When to pick which**: **Vertex AI Model Garden** when you want managed LLMs on GCP without operating GPUs. **Gemini family** for first-party models — Flash for cheap/fast, Pro for balanced, Pro >200K context for long-doc reasoning. **Anthropic Claude on Vertex** for Anthropic's reasoning quality (Haiku for cheap, Sonnet for balanced, Opus for hardest reasoning) — pricing is uniform with direct Anthropic and Bedrock. **Imagen / Veo / Lyria** for multimodal generation (image / video / music). **Provisioned Throughput** when you're spending >$5k/mo on on-demand for predictability + lower per-token cost. **Agent Builder** for "managed agentic orchestration" (Bedrock Agents analog) — but loses flexibility vs DIY rig-core / litellm agent loops.

**Pricing volatile**: Token prices change quarterly; verify on the Vertex AI pricing page before committing. The Anthropic-on-Vertex prices match Bedrock to the dollar in current snapshots — Anthropic's published prices are uniform across providers as of 2026.

---

## 28. ML — Vision

| Service | Scale ceiling | Latency | Cost shape | Tier |
|---|---|---|---|---|
| **Cloud Vision API** | Per-image API | <500 ms typical | $1.50 / 1k images (Labels) → $3.00 / 1k (Object Localization) | T1 (OpenCV / YOLOv8 bridge) |
| **Video Intelligence API** | Streaming or stored | seconds | $0.10 / minute of video | T1 |
| **Document AI** (general OCR + form extraction) | Per-page OCR + structure | seconds | $1.50 / 1k pages (general OCR); $30+ / 1k (custom processors) | T1 (tesseract + layoutparser) |
| **Vertex AI Vision** (industrial cv) | Streaming live video; pre-built models for retail / mfg | seconds | Per-stream-hour + per-prediction | T2 (custom models are managed only) |

**When to pick which**: **Cloud Vision API** when off-the-shelf vision suffices (content moderation, simple object detection). **Document AI** for PDF/scan extraction — comparable to Textract; the custom-processor route is well-supported. **Video Intelligence** for video moderation / object tracking. **Vertex AI Vision** for streaming-video industrial cv (retail analytics, manufacturing quality) — newer, vertical-shaped product.

---

## 29. ML — Speech

| Service | Scale ceiling | Latency | Cost shape | Tier |
|---|---|---|---|---|
| **Speech-to-Text Standard** | Async batch + streaming | seconds (streaming); minutes (batch) | $0.024 / minute (Standard) → $0.014 / minute (Long Audio) | T1 (whisper bridge) |
| **Speech-to-Text Chirp** (foundation model) | Same, higher quality | similar | $0.024 / minute | T1 |
| **Text-to-Speech Standard** | Standard voices | <500 ms | $4 / M chars | T1 (piper / coqui bridge) |
| **Text-to-Speech Neural2** | Higher-quality voices | <500 ms | $16 / M chars | T1 |
| **Text-to-Speech Studio** voices | Cinema-quality voices | seconds | $160 / M chars | T1 |
| **Speaker Diarization** | Multi-speaker | seconds | Included with STT | T1 |

**When to pick which**: **Speech-to-Text Standard** for call-center / transcription. **Speech-to-Text Chirp** (Google's universal speech foundation model) for non-English or accent-heavy content — usually beats Whisper on non-English. **Text-to-Speech Neural2** for accessible TTS in webapps — quality plateau. **Studio voices** for production audiobook / podcast / video voiceover.

---

## 30. ML — NLP

| Service | Scale ceiling | Latency | Cost shape | Tier |
|---|---|---|---|---|
| **Natural Language API** | Sentiment, entities, syntax, classification | <500 ms | $1 / 1k units (1 unit = 1k chars) | T1 (spaCy bridge) |
| **Translation API Basic** | 100+ language pairs | <500 ms | $20 / M chars | T1 (argos-translate bridge) |
| **Translation API Advanced** | Glossary support + batch | <500 ms (online) | $20 / M chars + per-glossary | T1 |
| **Translation API AutoML Custom** | Train on your bilingual pairs | <500 ms | $45 / hr training + $80 / M chars (custom) | T1 |
| **Healthcare NLP API** | Medical entity extraction | seconds | $0.01 / 100 chars | T1 |

**When to pick which**: **Natural Language API** for off-the-shelf NLP on GCP; for serious NLP, fine-tune a Gemini model or use Vertex AI with a prompt. **Translation API** is fine for most translation; specialized providers (DeepL) often beat it on European languages.

---

## 31. ML — Forecasting / Personalization / Search

| Service | Scale ceiling | Latency | Cost shape | Tier |
|---|---|---|---|---|
| **Vertex AI Forecast** (BQML AutoML time-series) | Per-dataset | seconds | $5 / M predictions + training cost | T2 |
| **Vertex AI Search** (Bedrock Knowledge Bases analog) | Up to ~10M docs Enterprise | sub-second | $4 / 1k queries (Standard) → $12 / 1k (Enterprise) + per-GB indexed | T2 |
| **Recommendations AI / Discovery AI Retail** | Per-domain recommender | <100 ms | $0.27 / TPS-hr + training cost | T2 |

**When to pick which**: **Vertex AI Forecast** for time-series forecasting via BQML AutoML — no separate dataset / recipe abstractions like AWS Forecast had (which is deprecated). **Vertex AI Search** as the "RAG in a box" path — covers retrieval + ranking + answer synthesis; the Bedrock Knowledge Bases analog. **Discovery AI Retail** (the Recommendations AI rebrand for retail use cases) for retail recommender shortcuts (clickstream → recs) — non-trivial setup; a simple collaborative-filter on Cloud SQL Postgres often wins for small catalogs.

---

# IoT / Edge (2 groups)

## 32. IoT — Data flow (gap notice)

*(GCP-native fusion of the AWS file's groups 34 + 36. **Cloud IoT Core was deprecated by Google in August 2023 and shut down.** GCP has no first-party device gateway today. This section documents the recommended pattern and partner alternatives.)*

| Service | Scale ceiling | Latency | Cost shape | Tier |
|---|---|---|---|---|
| **Cloud IoT Core** (**deprecated 2023**) | — | — | EOL — do not use | T2 (deprecated) |
| **Pub/Sub + Cloud Run pattern** (recommended) | Unlimited devices | <500 ms | See groups 1, 12 | T0 |
| **Pub/Sub → Dataflow → BigQuery** (analytics pipeline) | Unlimited | sub-sec to seconds | See groups 12, 22, 23 | T0/T1 |
| **ClearBlade IoT Core** (third-party; took over Google IoT Core surface) | Per-license | <500 ms | ClearBlade per-device pricing | T0 (MQTT bridge) |
| **EMQX or HiveMQ on GCE** | Self-hosted | <500 ms | Compute Engine cost | T0 (real MQTT broker locally) |

**When to pick which**: GCP's official recommendation for new IoT builds is to send device telemetry **directly to Pub/Sub** (via mqtt-to-pubsub bridges or HTTPS) and process downstream with Cloud Run / Dataflow → BigQuery. **ClearBlade IoT Core** is the third-party that took over the Google IoT Core surface for teams that need a turnkey managed MQTT broker — drop-in replacement requiring config-only changes. **EMQX or HiveMQ on GCE** for self-hosted MQTT with full control.

**GCP gap (be honest)**: The IoT Core deprecation also killed first-party fleet management, device defender / anomaly detection, IoT Analytics, SiteWise, TwinMaker, and FleetWise equivalents. GCP has **no analog** for any of the AWS IoT vertical services (groups 34–36 on the AWS file beyond the basic gateway). Teams running large IoT fleets typically reach for partner products (ClearBlade, EMQX Enterprise, Particle, Losant) layered on top of Pub/Sub + BigQuery.

---

## 33. IoT — Edge Runtime

| Service | Scale ceiling | Latency | Cost shape | Tier |
|---|---|---|---|---|
| **Google Distributed Cloud Edge** (GDC Edge) | Edge cluster + connected back to GCP control plane | local; depends on device | Per-node hardware + per-rack subscription | T2 |
| **GDC Air-gapped** | Disconnected edge (regulated / defense) | local | Per-rack subscription | T2 |
| **Anthos on bare-metal** (legacy → GDC) | Self-hosted GKE on bare metal | local | Per-vCPU subscription | T2 |
| **Coral Edge TPU** (hardware) | Per-device TPU inference | μs–ms | Hardware purchase | T2 (hardware) |

**When to pick which**: **GDC Edge** for industrial edge deployments (factory floor, telco MEC, retail) where workloads run on-prem but control plane stays in GCP — the GCP analog to AWS Outposts or Azure Stack Edge. **GDC Air-gapped** when "no internet connectivity" is a hard requirement (defense, regulated finance, sensitive workloads). **Coral Edge TPU** for embedded ML inference on devices.

**Note**: there's no Greengrass-style "Lambda at the edge" first-party service on GCP. GDC Edge runs full GKE; you deploy containers. For microcontroller-class devices (FreeRTOS analog), GCP relies on partner stacks; not a first-party product.

---

# Tier 2 deep-dive

## What "Tier 2" means in caravan terms

Tier 2 means no OSS container reproduces the service locally. For the user's **runtime** story this is already solved — user container code calls the GCP SDK directly with mounted application-default credentials, which works identically whether the container runs locally (docker bind-mount of `~/.config/gcloud` or `GOOGLE_APPLICATION_CREDENTIALS` env var) or on Cloud Run / GKE (service account auto-injected via Workload Identity). No caravan runtime abstraction is needed.

The only open question per service is the **provisioning** story: does its config shape fit a short `cloud_only:` yaml block (worth a registry slot), or is the config big / dynamic enough that hand-written `.tf` via the `terraform-module` escape hatch (`caravan_abstraction_v2.md` §2) is more honest? The sub-grouping below splits Tier 2 by *why* it's niche; the per-service paragraphs that follow judge each truly-niche entry on one of three verdicts:

- **`yaml-registry`** — config fits a short `cloud_only:` block (model ID + a few knobs). Ship a registry entry.
- **`hand-tf`** — config is too large / dynamic for short yaml (entity graphs, workflow bodies, data pipelines). Document the SDK call pattern, route to `resources.<name>: { type: terraform-module, source: ./modules/foo }`.
- **`skip`** — deprecated, end-of-life, or so narrow that even hand-tf documentation isn't worth shipping.

## Tier 2 sub-groups (by why-niche)

The seven buckets are oriented around what makes the service unreachable from a local container.

1. **Edge runtime / CDN** — Cloud CDN, Media CDN, Cloud Armor. Edge POP routing and anycast are the value-add; no local equivalent.
2. **Multi-region coordination** — Spanner multi-region, BigQuery cross-region replication. Active-active or cross-region fan-out is a coordination property that single-node OSS can't reproduce.
3. **Managed ML / AI orchestration** — Vertex AI Search, Vertex AI Agent Builder, Vertex AI Vector Search, Vertex AI Pipelines, Recommendations AI / Discovery AI Retail, Document AI custom processors. Orchestration and bundled-model marketplaces are the proprietary value; the underlying inference is already T1 via community libraries.
4. **Specialty storage variants** — AlloyDB Columnar Engine, BigLake, Datastream, Dataplex, Parallelstore, NetApp Volumes, Hyperdisk Storage Pools, BigQuery (engine itself). Same primitive shape with a proprietary performance or query characteristic Google hasn't open-sourced.
5. **Identity / lifecycle / observability lock-in** — Identity Platform user lifecycle, Firebase Auth lifecycle, IAM enforcement, Identity-Aware Proxy, Workforce Identity Federation, Cloud Trace visualization, Cloud Profiler dashboards, Error Reporting, Application Performance Insights, Audit Logs (data access), Access Transparency. Telemetry and lifecycle admin are by nature cloud-only.
6. **API edge / data / BI orchestration** — API Gateway, Apigee X / Hybrid, Eventarc, Workflows execution engine, Looker, Looker Studio, Looker Studio Pro, Cloud Composer GCP-native operators. Orchestration / dashboarding — the GCP-managed plane *is* the value.
7. **Deprecated / end-of-life** — Cloud IoT Core, Pub/Sub Lite, Runtime Config, Cloud Debugger, Recommendations AI legacy (non-Retail), Datastore mode migration path. Should not be a target for new builds.

## Common vs niche within Tier 2

**Common Tier 2** — services most modern GCP apps touch at some point. Earn a yaml-registry slot or a primitive wrapper even when their config shape is non-trivial. Their "when to pick which" notes already live in the per-role-group sections above; deep-dive treatment is not repeated here:

- API Gateway — universal API surface
- Identity Platform — universal auth
- Cloud CDN — universal CDN
- Vertex AI Search — RAG is mainstream in 2026
- Spanner multi-region — anyone going multi-region
- Cloud Audit Logs / IAM enforcement / IAP — universal supporting infra
- BigQuery (engine) — universal warehouse
- Eventarc — common in Cloud Run + Pub/Sub deployments

**Truly-niche Tier 2** — the entries deep-dived below. Narrow-vertical, low-adoption, deprecated, or outside what caravan's expected audience touches by default.

## Per-niche-service paragraphs and practicality verdicts

For each entry: 2–3 sentences on **who reaches for it** + **trigger scenario** + **why a generic alternative doesn't suffice**, then a single **Practicality:** verdict.

### Edge runtime / CDN — niche

#### Media CDN

Anycast premium CDN for large-scale video streaming (live + VOD) — separate POP fabric, optimized for video, sold to media customers (sports streaming, OTT). For non-video CDN, Cloud CDN is the default.

**Practicality: `hand-tf`** — config is video-pipeline-shaped (origin shielding, edge cache policies, signed URLs), not app-shaped.

#### Google Cloud Armor

WAF + DDoS attached to an HTTP(S) LB; Adaptive Protection adds ML-based detection. Reached when running public-facing endpoints needing OWASP rule enforcement.

**Practicality: `yaml-registry`** — config is a small policy list (managed rule sets + custom rules); fits a `cloud_only:` block.

### Multi-region coordination — niche

#### BigQuery cross-region replication (Enterprise Plus)

Cross-region DR for BigQuery via Enterprise Plus tier — automatic replication of datasets across regions. Reached by enterprises with cross-region BCP requirements on analytics.

**Practicality: `yaml-registry`** — config is a short flag (source region + DR region + dataset filter).

### Managed ML / AI orchestration — niche

#### Vertex AI Vector Search

Managed ANN service (ScaNN-backed) for billions of vectors with low-latency queries. Reached by teams operating at >100M vector scale where AlloyDB pgvector + ScaNN can't keep up.

**Practicality: `yaml-registry`** — config is small: index name + dimension + distance + ScaNN config + endpoint deployment.

#### Vertex AI Agent Builder

Managed agent orchestration: tool definitions, retrieval over Vertex AI Search, conversational state. Reached by teams building agentic apps who want managed orchestration instead of writing the agent loop in code.

**Practicality: `hand-tf`** — agent config + tool schemas are app-specific and lengthy.

#### Vertex AI Pipelines

Managed Kubeflow Pipelines for MLOps. Pipeline DAGs are user-authored Python.

**Practicality: `hand-tf`** — pipeline DAG bodies are the value; per-app.

#### Recommendations AI / Discovery AI Retail

Recommender-system-as-a-service: catalog + user events + serving config. Reached by retail / media teams without ML talent. For small catalogs (<100k items), a Cloud SQL collaborative-filter often wins.

**Practicality: `hand-tf`** — catalog schema + serving config + event ingestion is application-shaped.

#### Document AI (custom processors)

Train custom document extractors for specific forms (invoices, leases, claims). Reached by document-heavy enterprises with proprietary form templates.

**Practicality: `hand-tf`** — processor schema + label config + training datasets are bespoke per use case.

### Specialty storage variants — niche

#### AlloyDB Columnar Engine

Adds columnar acceleration to AlloyDB Postgres for analytical queries; OLTP unchanged. Reached when you want OLTP + light OLAP without a separate warehouse.

**Practicality: `yaml-registry`** — small shape: enable flag + memory size on an existing AlloyDB cluster.

#### BigLake

Open-format table layer over GCS (Iceberg / Hudi / Delta) queryable from BigQuery and Spark uniformly. Reached by lakehouse-style architectures.

**Practicality: `hand-tf`** — table-level config + IAM + metadata service wiring is per-deployment.

#### Datastream

CDC source → BQ / GCS / SQL targets, seconds-minute lag. Reached when you want OLTP + OLAP without a separate ETL pipeline.

**Practicality: `yaml-registry`** — small shape: source DB + target dataset + replication filter (Aurora zero-ETL analog).

#### Dataplex

Data lake catalog + governance + quality (replaces Data Catalog + adds Lake Formation-style features). Reached by data-platform teams running shared data lakes.

**Practicality: `hand-tf`** — zone definitions + asset registrations + quality rules are org-shaped.

#### Parallelstore

Lustre-protocol high-throughput filesystem for HPC / ML training, GA 2024. Reached when you need >1 GB/s per client.

**Practicality: `yaml-registry`** — small shape: capacity + throughput tier + region.

#### NetApp Volumes

GCP-native NetApp managed storage with SnapMirror, multi-protocol NFS+SMB. Reached when the NetApp feature set is the requirement (lift-and-shift from on-prem NetApp).

**Practicality: `hand-tf`** — config is NetApp-shaped (service level + capacity pool + volume + SnapMirror); per-deployment.

#### Hyperdisk Storage Pools

Shared capacity across multiple Hyperdisk volumes in a pool — better economics for fleet usage. Reached by orgs managing many disks at scale.

**Practicality: `yaml-registry`** — small shape: pool capacity + IOPS + throughput targets.

#### Bigtable App Profiles

Routing policies for multi-cluster Bigtable replication (single-cluster vs multi-cluster, with failover semantics). Reached by teams operating multi-region Bigtable.

**Practicality: `yaml-registry`** — small shape: profile name + routing policy + cluster targets.

### Identity / lifecycle / observability lock-in — niche

(Common Tier 2 entries — Identity Platform user lifecycle, IAM enforcement, Audit Logs, IAP — are covered in their role-group sections above. The niche-only entries below.)

#### Firebase Authentication (consumer SKU lifecycle)

Same engine as Identity Platform, packaged for Firebase consumer apps. Reached by apps already in the Firebase ecosystem.

**Practicality: `hand-tf`** — same shape as Identity Platform user lifecycle.

#### Workforce Identity Federation

Org-level workforce SSO via external IdP federation (Okta, Azure AD, etc.) for human users accessing GCP. Reached by enterprises consolidating identity into one IdP.

**Practicality: `skip`** — thesis explicitly lists "multi-account governance" as out of scope.

#### Application Performance Insights

Auto-instrumented APM over Java / Go / Node services. Reached when you want GCP-native APM without Datadog.

**Practicality: `yaml-registry`** — enable flag per service + SLO definitions. Small shape.

#### Access Transparency / Access Approval

Google-side access logging + per-event approval workflow for regulated workloads. Reached by financial services / healthcare / government.

**Practicality: `skip`** — per-org subscription tier, not per-app provisioned.

#### Cloud Profiler

Continuous CPU / heap profiling exported to a Google-hosted UI. Profiling agent is OSS; dashboard is GCP-only.

**Practicality: `yaml-registry`** — enable flag per service.

#### Cloud Trace (visualization side)

OTel spans flow to a Google-hosted UI for trace exploration. The export is portable (OTel); the trace explorer UI isn't.

**Practicality: `yaml-registry`** — enable flag + sampling rate.

#### Error Reporting

Automatic error aggregation from logs into a managed UI. Logs are portable; the aggregation UI isn't.

**Practicality: `yaml-registry`** — enable flag.

### API edge / data / BI orchestration — niche

(API Gateway, Eventarc, and Looker / Looker Studio are covered as common Tier 2 in their role-group sections.)

#### Apigee X / Apigee Hybrid

Enterprise API management (developer portals, monetization, OAuth flows, quota management, complex routing). Reached by enterprises with API-as-product strategies. For internal APIs, API Gateway is the lower-cost choice.

**Practicality: `hand-tf`** — Apigee config is API-product-shaped (API products + developer portals + flow hooks); per-app.

#### Workflows (execution engine)

GCP-managed serverless orchestration with YAML DSL. The YAML itself is portable in shape (looks like Step Functions ASL) but the execution engine is proprietary.

**Practicality: `hand-tf`** — the workflow YAML body is the value; per-app.

#### Cloud Composer GCP-native operators

GCP-specific Airflow operators (BigQuery, Dataflow, Cloud Functions, Cloud Run triggers). Reached by data engineering teams orchestrating GCP-native pipelines via Airflow.

**Practicality: `skip`** — operator selection is per-DAG; not a provisioning concern.

### Deprecated / end-of-life

#### Cloud IoT Core

Pre-2023 managed MQTT gateway. **Deprecated 2023** — Google recommends Pub/Sub directly or third-party (ClearBlade).

**Practicality: `skip`** — deprecated; no new builds.

#### Pub/Sub Lite

Cheaper Kafka-style alternative to Pub/Sub. **Deprecated 2024** — migration path to native Pub/Sub.

**Practicality: `skip`** — deprecated.

#### Runtime Config

Pre-Secret-Manager config service. **Deprecated** — use Secret Manager.

**Practicality: `skip`** — deprecated.

#### Cloud Debugger

Live production debugger. **Deprecated 2023** — Cloud Profiler + Error Reporting cover the use case.

**Practicality: `skip`** — deprecated.

#### Datastore mode (migration path)

Legacy API surface that runs on the Firestore engine. Migration to Firestore Native mode is recommended.

**Practicality: `skip`** — migration concern; not a new-build target.

## Runtime story for niche Tier 2 services (recap)

Regardless of the per-service verdict above, the user's **runtime code path** for any niche Tier 2 service is unchanged: call the GCP SDK from inside the service container with mounted application-default credentials. This works identically whether the container runs locally (docker bind-mount of `~/.config/gcloud` or `GOOGLE_APPLICATION_CREDENTIALS` env var) or on Cloud Run / GKE (service account auto-injected via Workload Identity Federation). The verdict only determines whether caravan ships an opinionated yaml shortcut for the **provisioning** side, or sends the user to the v2 §2 escape hatch:

```yaml
resources:
  my_niche_thing:
    type: terraform-module
    source: ./modules/my_niche_thing
    inputs: { … }
```

with the resulting resource ID injected as an env var into services that `uses:` it. No new primitive is invented; this analysis only earmarks which Tier 2 services route through `cloud_only:` versus `terraform-module`.

---

# Cross-cutting patterns observed

Seven patterns recur across groups and matter for File 5's recommendation:

1. **Cloud Run unifies function, long-running, and batch shapes.** AWS splits these across Lambda (function), Fargate / App Runner (long-running), and Batch (batch). GCP's Cloud Run is one resource type with shape-driven knobs (min-instances, concurrency, request-deadline, Cloud Run Jobs for batch). caravan's `service.shape:` distinction still drives codegen differences but the underlying resource is the same — *cleaner than AWS*, not messier.
2. **Pub/Sub fuses queue and topic.** What AWS splits across SQS + SNS + EventBridge, GCP collapses into Pub/Sub (with Cloud Tasks for HTTP work-queue specifics and Eventarc for content-based routing on top). caravan still emits both `queue` and `topic` as IR primitives; codegen handles the fusion.
3. **BigQuery is the single warehouse + lake + ad-hoc-query axis.** AWS splits across Redshift (warehouse), Athena (ad-hoc), and Glue (catalog). BigQuery covers all three in one engine — external tables, BigLake (Iceberg / Hudi / Delta), Dataplex catalog integration, BigQuery Omni (cross-cloud). Reduces the surface area users have to learn.
4. **Vertex AI is the single ML umbrella.** AWS splits across Bedrock (foundation models), SageMaker (training / serving / pipelines), Rekognition / Textract / Comprehend / Transcribe / Polly (purpose-built APIs). Vertex AI Model Garden + Vertex AI Vision / Speech / NLP + Vertex AI Training all live under one console. Same model lineup (Gemini, Claude on Vertex, Llama, Mistral) but managed uniformly.
5. **First-party emulators expand T0 share.** Google ships `firestore-emulator`, `spanner-emulator`, `pubsub-emulator`, `bigtable-emulator`, `datastore-emulator`. AWS users stitch together community alternatives + DynamoDB-Local. GCP's local-emulator story is more uniform for the data plane.
6. **Two notable first-party gaps to call out**: (a) **no native email service** — SendGrid via Marketplace is the closest equivalent to SES; (b) **no first-party IoT gateway** after Cloud IoT Core's 2023 deprecation — Pub/Sub direct or third-party (ClearBlade) fills the gap. Edge compute is also a relative gap (no Lambda@Edge equivalent; Cloud Run is regional).
7. **The Tier 2 niche set bifurcates by provisioning shape, not runtime difficulty** — same conclusion as the AWS file. All Tier 2 services are runtime-solved by mounted creds + GCP SDK from inside the user's container (works identically local / Cloud Run / GKE via Workload Identity). The only design question per service is whether its HCL fits a short `cloud_only:` yaml shortcut (`yaml-registry`) or is better left to the `terraform-module` escape hatch (`hand-tf`) — or omitted entirely (`skip`). See **§Tier 2 deep-dive** above.
