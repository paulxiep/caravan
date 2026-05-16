# Azure Service Role Groups — Scale, Latency, Cost

> **Snapshot date: 2026-05-16. Prices in `eastus` USD unless noted. Re-verify on the Azure Pricing Calculator before quoting in a decision doc.**
>
> **Sources**: numbers come from training data through Jan 2026 plus targeted web verification (2026-05-16) for the most volatile services: Azure OpenAI per-token pricing (Foundry Models direct + GPT-4o / o3 / o4-mini families verified against `azure.microsoft.com/en-us/pricing/details/azure-openai/` and `learn.microsoft.com/azure/foundry/foundry-models`), Cosmos DB serverless RU rate and autoscale 50% premium, Container Apps Consumption per-second vCPU/GiB pricing and idle-rate split, Microsoft Fabric F-SKU per-CU-hour pricing, AI Search Standard tier + semantic ranker per-1k-query pricing, Front Door Standard vs Premium per-GB egress and per-10k-request pricing, Postgres Flexible Server burstable + general-purpose SKU rates, Azure SQL Database serverless vCore-second rate ($0.000145/vCore-s ≈ $0.5218/vCore-hr) and auto-pause behavior. Older / stable services (general VMs, Blob Hot, Azure Files Standard, Service Bus Standard) are not web-re-verified for this snapshot.

This is the Azure counterpart to [`aws_service_groups.md`](aws_service_groups.md). It is referenced by `cloud_providers.md` for tier-by-tier evidence of Azure primitive coverage and by the future Azure analogues of the per-language mapping docs.

## What a "role group" is

A **role group** is a slot in a typical application's architecture (e.g., "the place I put a key-value store") that several Azure services can plausibly fill. The point of grouping is that within a group the *interface to your code* is similar, while **scale ceiling, latency, and cost shape** differ enough that the choice matters.

Each group section has:
1. A per-service comparison table on three axes:
   - **Scale ceiling** — soft/hard limit per region / account / namespace. The point at which you'd be forced to re-shard or migrate.
   - **Latency band** — p50 / p99 for a typical read or write. "Single-digit ms" means ≤10 ms p50.
   - **Cost shape** — what you pay *per* (per-request, per-hour, per-GB-month, per-RU, per-CU-hour) with a representative number.
2. A "when to pick which" note that captures the actual decision criteria.

Groups are listed in dependency order — compute first, then storage, then data services that need both.

Every per-service table below also carries a `Tier` column with one of `T0` / `T1` / `T2`. The rollup section that follows lifts those tags into three flat lists for at-a-glance reading; the Tier 2 deep-dive near the end of this file then sub-groups Tier 2 and judges each truly-niche entry on whether it earns a `cloud_only:` yaml shortcut or should be reached via the `terraform-module` escape hatch.

**Hybrid-with-AWS structure note.** The four outer sections (Web Stack Core / Data-Analytics / ML-AI / IoT-Edge) parallel [`aws_service_groups.md`](aws_service_groups.md) one-for-one. *Inside* each section, the per-Azure group layout reorganizes where Azure naturally groups things differently — most notably (a) Service Bus fuses Queue+Topic into one group, (b) Cosmos DB is one group with API rows instead of separate KV / Document / Graph / Table groups, (c) Microsoft Fabric is its own group spanning warehouse+lakehouse+BI, (d) Logic Apps gets its own row alongside Durable Functions, and (e) App Configuration, Azure Communication Services, Azure Arc earn dedicated treatment without AWS analogue. The five AWS Tier-1 hard pairs (LLM / JWT / email / STT / image) map to Azure-equivalent services in the same shape.

---

# At-a-glance tier rollup

The `T0 / T1 / T2` framing is load-bearing in `thesis.md` ("Service tiers" under Current evaluation) and `supeux_abstraction_v2.md` §4. Recap:

- **T0** — same wire API both sides; endpoint-URL or DSN env-var swap in user code suffices. No abstraction library required. Container-shaped compute primitives also sit here (one image, runs locally as docker-compose service or in cloud as Container Apps / App Service / AKS / Functions container image).
- **T1** — different wire APIs cloud vs local; a structural abstraction layer is required (per the thesis stable design principle). Mature community libraries cover the well-known pairs (rig-core / litellm for LLMs; jsonwebtoken + JWKS for token verify; lettre / smtplib for email; whisper crates for STT; OpenCV / yolov8 for image analysis).
- **T2** — no OSS engine reproduces the service locally. `cloud_only:` provisioning marker. The Tier 2 deep-dive below sub-groups these, splits common from truly-niche, and per-niche-service decides between `yaml-registry`, `hand-tf`, and `skip`.

## T0 services (~26)

Compute primitives (run-the-container is the wire compat): Container Apps (Consumption + Dedicated workload profiles), AKS (managed Kubernetes + AKS Automatic), App Service (Web App for Containers — Basic / Standard / Premium / Isolated v2), Azure Functions (Consumption + Premium + FlexConsumption + Dedicated, container-image deployment), Azure VMs (D / E / F series + Spot + Reserved Instances + Savings Plans), Azure Batch, VM Scale Sets, Azure Container Instances (ACI). Data plane (endpoint / DSN swap): Blob Storage (Hot + Cool + Cold + Archive + Premium Block + Premium Page; ADLS gen2 layered on top), Azure Files (Standard transaction-optimized / hot / cool, Premium SSD), Managed Disks (Premium SSD v2, Premium SSD, Standard SSD, Standard HDD), Local SSD / Temporary Disk, Postgres Flexible Server (burstable / general purpose / memory-optimized + Flexible Server with HA), MySQL Flexible Server, Azure SQL Database (DTU + vCore provisioned + vCore serverless + Hyperscale + Azure SQL Managed Instance General Purpose / Business Critical), Cosmos DB (SQL API + MongoDB 4.x/5.x/6.x compat + Cassandra API + Table API — provisioned + autoscale; serverless is T2 at the multi-region tier), Azure Cache for Redis (Basic / Standard / Premium), Azure Managed Redis (preview / GA), Service Bus (Standard + Premium with queues + topics), Storage Queues, Event Hubs (Basic + Standard + Premium + Dedicated), Event Grid Custom Topics, Application Gateway v2 (WAF v2), Azure Load Balancer (Basic + Standard), Azure DNS (public + private + Traffic Manager via Standard), Azure Key Vault (Standard + Premium HSM-backed), Azure App Configuration (Free + Standard), Logic Apps Standard (workflows run on App Service plan), Azure Data Factory + Synapse Pipelines (mapping data flows + integration runtimes), Azure Monitor Logs (stdout/Container Insights ingestion), Application Insights (via OpenTelemetry SDK), AI Search (Free + Basic + Standard S1/S2/S3 — Lucene API on top of OSS Elasticsearch-like engine), HDInsight (real Hadoop / Spark / Kafka / HBase / Interactive Query), Synapse Spark pools, Azure Databricks (Standard + Premium workspaces — Spark wire-compat), Container Registry (Basic + Standard + Premium).

## T1 services (~5 hard pairs)

Each pair requires a structural abstraction; community libraries cover all of them today:

- **LLM** (Azure OpenAI — GPT-4o / GPT-4.1 / o1 / o3 / o4-mini / Phi / Mistral via Foundry Models direct + Provisioned Throughput Units) ↔ Ollama / vLLM. Bridge: rig-core (Rust), litellm (Python), langchaingo / eino (Go), Vercel AI SDK (TS). Azure OpenAI's API surface is *near*-OpenAI compatible (deployment-name-instead-of-model-name is the main wire diff) — the same client library used against the OpenAI API or local Ollama (which exposes the OpenAI surface) works with one env var flip.
- **Token verification** (Entra ID / Entra External ID JWKS endpoint) ↔ local JWT issuer. Bridge: jsonwebtoken (Rust), authlib / python-jose / msal-python (Python), golang-jwt (Go), jose (TS).
- **Email** (Azure Communication Services Email REST or SMTP submission) ↔ MailHog / Mailpit SMTP catcher. Bridge: lettre (Rust), smtplib (Python), gomail (Go), nodemailer (TS) — or `@azure/communication-email` and analogues on the cloud side.
- **Speech-to-text** (Azure AI Speech batch + real-time + custom speech) ↔ Whisper. Bridge: whisper-rs (Rust), openai-whisper (Python), similar elsewhere.
- **Image analysis / OCR** (Azure AI Vision — image analysis, dense captions, smart-crop, face; Azure AI Document Intelligence formerly Form Recognizer) ↔ OpenCV / YOLOv8 / Tesseract + layoutparser. Bridge: per-language community libraries.

Also AI Speech TTS Neural voices ↔ coqui-ai / piper; AI Language sentiment/entities ↔ spaCy; Translator ↔ argos-translate sit at the T1 edge — covered by community libraries, classified here when the user actually swaps the implementation per environment.

## T2 services (~30)

Every service tagged `T2` in the role-group tables below. Sub-grouped, split common-vs-niche, and judged for yaml-registry fit in the **Tier 2 deep-dive** section near the end of this file.

Headline list (canonical bucket in parens; full breakdown in the deep-dive):

- **Edge / CDN**: Front Door Standard / Premium (with WAF), Azure CDN from Microsoft / Akamai (Verizon SKU retired), Azure Static Web Apps preview environments + linked APIs, Application Gateway WAF custom rules / managed rule sets.
- **Multi-region coordination**: Cosmos DB multi-region writes (multi-master), Cosmos DB Strong consistency across regions, Azure SQL Database geo-replication / Hyperscale named replicas, Service Bus Geo-Disaster Recovery pairing, Traffic Manager profile + nested.
- **Managed ML / AI orchestration**: Azure OpenAI On Your Data (managed RAG), AI Foundry Agent Service, Azure OpenAI Assistants API, Content Safety, Azure ML pipelines + endpoints (no-code orchestration), Personalizer (retired track), Azure Bot Service.
- **IoT vertical**: IoT Central, Azure IoT Operations, Defender for IoT, Digital Twins, IoT Hub Device Provisioning Service (DPS), Azure Time Series Insights (retired track).
- **Specialty storage variants**: Azure NetApp Files, Azure Container Storage, Azure Data Lake Storage Gen2 hierarchical namespace, Premium Blob page blobs for VMs, Azure Elastic SAN, Cosmos DB integrated cache (dedicated gateway), Cosmos DB Vector Search (preview-to-GA), Cosmos DB Continuous Backup PITR.
- **Identity / lifecycle / observability / messaging lock-in**: Entra ID Conditional Access / PIM / Identity Protection, Entra External ID (B2C) custom policies, Workforce SSO admin, Defender for Cloud, Azure Monitor Smart Detection, Azure Monitor alert rules + action groups (triggering), Activity Log, Notification Hubs, ACS Calling / SMS / Chat advanced features, ACS Number Lookup.
- **API edge / data / BI orchestration**: API Management (Developer / Basic / Standard / Premium / Consumption / Standard v2 / Premium v2), Azure Functions Bindings (managed input/output bindings — Azure-specific glue), Microsoft Fabric (all F-SKUs as one orchestration surface), Power BI Premium Per User / Premium capacity (subsumed by Fabric F64+), Purview Data Map / Catalog, Microsoft Purview DLP, Data Factory mapping data flows, Synapse Dedicated SQL Pool, Azure Stream Analytics.
- **Deprecated / end-of-life**: Azure CDN from Verizon (retired 2025), Azure Cognitive Services Personalizer (retired), Time Series Insights (retired), Maps Creator (retired), Azure Database for MariaDB (retiring 2024-09), Azure Notification Hubs Free tier (retired track).

See **§Tier 2 deep-dive** below for each truly-niche entry's use case and practicality verdict (`yaml-registry` / `hand-tf` / `skip`).

---

# Web Stack Core (22 groups)

## 1. Compute — Function

| Service | Scale ceiling | Latency | Cost shape | Tier |
|---|---|---|---|---|
| **Azure Functions Consumption** | 200 instances default; ~100 concurrent per instance for HTTP triggers | Cold start 1–3 s (.NET / Python); warm <50 ms | First 1M req + 400k GB-s free; $0.20 / M req + $0.000016 / GB-s | T0 |
| **Azure Functions Premium (EP1/EP2/EP3)** | Pre-warmed instances; up to 100 per plan | Always-warm <50 ms | $0.173 / vCPU-hr + $0.0123 / GB-hr (EP1: 1 vCPU / 3.5 GB ≈ $0.219/hr) | T0 |
| **Azure Functions FlexConsumption** | Per-second autoscale; up to 1k instances | Cold start 200–600 ms (faster than v1 Consumption) | $0.0000162 / GB-s on-demand + $0.078 / vCPU-hr always-ready | T0 |
| **Azure Functions Dedicated (App Service plan)** | Bounded by App Service plan; manual scale | Always-warm <50 ms | App Service plan hourly only | T0 |
| **Container Apps Jobs** | Up to 1k replicas; event/schedule/manual triggers | Container-start 5–30 s; can warm via min-replicas | Per-second vCPU + GiB at Container Apps Consumption rate | T0 |

**When to pick which**: Functions Consumption for spiky low-volume HTTP / queue triggers (free tier covers most hobby workloads). FlexConsumption when Consumption's cold-start hurts and you want per-second billing without paying for always-on Premium instances — best of both. Premium when you need VNet integration, longer-than-10-min runs, or pre-warmed instances for consistent <50 ms cold response. Dedicated when you're already paying for an App Service plan and want to colocate function code there. Container Apps Jobs (not classical "function") when the trigger fits Container Apps event sources (Service Bus, Event Hub, Cron, manual) and you want full Dockerfile control.

**Hard limits worth knowing**: Consumption tier 10-min max execution (5-min default), 1.5 GB memory cap, no VNet integration; cold-start ~2 s in .NET / Python, ~500 ms in Node. FlexConsumption 60-min max, 2 GB / 4 GB instance sizes, VNet-integrated. Premium 60-min max, up to 14 GB memory, full VNet.

---

## 2. Compute — Container

| Service | Scale ceiling | Latency | Cost shape | Tier |
|---|---|---|---|---|
| **Container Apps (Consumption)** | Scale 0–1,000 replicas per app; environment soft cap 100 apps | Cold start 1–5 s scale-from-zero; warm <50 ms | Active: $0.000024 / vCPU-s + $0.000003 / GiB-s; idle: $0.000008 / vCPU-s + $0.000001 / GiB-s; first 180k vCPU-s + 360k GiB-s + 2M req / sub-month free | T0 |
| **Container Apps (Dedicated workload profiles)** | Pre-provisioned D4–D32 / E4–E32 nodes per profile; dedicate by env | <1 s scale-up within profile | Per-node hourly (e.g., D4: ~$0.20/hr) + small per-environment management fee | T0 |
| **AKS (managed K8s)** | 5k nodes / cluster; 250 pods / node | App-dependent | Control plane: free (Standard tier $0.10/hr); + node compute (VM SKUs) | T0 |
| **AKS Automatic** | Opinionated AKS with Microsoft-managed node defaults | Same | Same as AKS Standard + Microsoft-managed config overhead | T0 |
| **App Service — Web App for Containers (Standard S1)** | Up to 10 instances / plan; auto-scale by metric | Warm <50 ms; cold ~3 s on Linux | $0.075 / hr (S1: 1.75 GB / 1 vCPU) → $1.20 / hr (P3v3) | T0 |
| **App Service — Isolated v2** | Dedicated ASE v3, scale to 100 instances | Warm <50 ms; private | $1.10 / hr (I1v2) → $4.40 / hr (I3v2); + ASE management | T0 |
| **Azure Container Instances (ACI)** | Per-container; no orchestration | <30 s start; no scale-down to zero | $0.0000133 / vCPU-s + $0.00000147 / GiB-s | T0 |

**When to pick which**: Container Apps for almost all greenfield "I have a Dockerfile, give me a URL" — Microsoft's clear north-star compute primitive (KEDA scaling + Dapr + revisions + traffic splitting built-in). Consumption profile for scale-to-zero apps; Dedicated workload profiles when you need GPU SKUs, larger memory, or sustained load > break-even point (~50% utilization vs Consumption). AKS only if you need real Kubernetes (Helm charts you can't rewrite, Istio, k8s-native operators) or multi-cloud portability; otherwise it's strictly more operational overhead than Container Apps for the same workload. App Service for Containers when you want the App Service developer surface (deployment slots, easy custom domains, App Service Auth) and don't need scale-to-zero. ACI for one-shot containers (CI runners, ETL bursts) — but Container Apps Jobs beats it on price + ergonomics in 2026.

**Pricing gotcha**: Container Apps Consumption bills *active and idle* vCPU/GiB seconds at different rates — idle is ~33% of active. "Idle" means replica is alive but not handling requests. The 5-min minimum keep-alive after last request means short-burst workloads pay idle rates for a 5-min tail.

---

## 3. Compute — VM / Batch

| Service | Scale ceiling | Latency | Cost shape | Tier |
|---|---|---|---|---|
| **Azure VMs (pay-as-you-go)** | Per-family vCPU quotas; easily 10k+ vCPUs by request | Boot 30 s–2 min | $0.0496 / hr (D2as v5) → $30+ / hr (large GPU like ND H100 v5) | T0 |
| **Azure VMs Spot** | Same; uses excess capacity | Same | Up to 90% off PAYG; eviction with 30-s warning | T0 |
| **Azure Reserved Instances (1y / 3y)** | Same | Same | 40–60% off PAYG for 1y; 60–72% for 3y | T0 |
| **Azure Savings Plan for Compute** | Same | Same | Similar to RI but flexible across SKU / region; covers Functions Premium + Container Apps Dedicated + App Service + Container Instances | T0 |
| **VM Scale Sets (uniform + flexible)** | 1,000 VMs / VMSS (Flexible); 600 (Uniform) | Boot 30 s–2 min | Underlying VM cost only | T0 |
| **Azure Batch** | Manages VM pools for jobs; 10k+ cores by quota | Job pickup 30 s–2 min | Underlying VM cost + free orchestration | T0 |
| **Dev Box / Microsoft Dev Box** | Developer cloud workstations | Boot 30s | $1.20 / hr (8 vCPU / 32 GB) — bundle pricing | T2 |

**When to pick which**: VMs PAYG for fresh experiments. VM Spot for fault-tolerant batch + dev (CI runners, training jobs that checkpoint; 30-s eviction is shorter than EC2's 2-min — design for it). 3-year Savings Plan for Compute once steady baseline is provably stable (>50% utilization for >12 months) — strictly better than RI because it covers Functions Premium / Container Apps Dedicated / App Service too. VM Scale Sets for "horizontally scaled VMs with one config" — most container workloads should be on Container Apps or AKS, not raw VMSS. Batch for "I have a queue of jobs, run them" — cheapest way to fan-out without orchestration code; integrates with Spot.

---

## 4. Storage — Object

| Service | Scale ceiling | Latency | Cost shape (per GB-month / per 10k tx) | Tier |
|---|---|---|---|---|
| **Blob Storage — Hot (LRS)** | 500 TiB / account default (raise to 5 PiB); 5 TB / blob; 20k req/s per account | First-byte 50–150 ms | $0.0184 / GB; $0.065 write / $0.005 read per 10k tx | T0 |
| **Blob Storage — Hot (GRS / GZRS)** | Same | Same + secondary lag (RA-GRS reads <15-min stale) | $0.0368 / GB (GRS); $0.046 / GB (GZRS) | T0 |
| **Blob Storage — Cool** | Same | Same as Hot for first byte | $0.01 / GB; $0.10 write / $0.01 read per 10k tx + retrieval | T0 |
| **Blob Storage — Cold** | Same; min 90-day retention | Same | $0.0036 / GB; $0.18 write / $0.10 read per 10k tx + retrieval | T0 |
| **Blob Storage — Archive** | Same; min 180-day retention | 1–15 hr rehydrate (Standard); <1 hr (High priority for blobs <10 GB) | $0.00099 / GB; $0.10 write / $5.00 read per 10k tx + rehydration | T0 |
| **Blob Storage — Premium Block Blob** | High IOPS object workloads | <10 ms p99 | $0.15 / GB; $0.018 write / $0.018 read per 10k tx | T2 |
| **Blob Storage — Premium Page Blob** | For VM disks (legacy path) | <10 ms | Tied to disk pricing | T0 |
| **Azure Data Lake Storage Gen2 (HNS on Blob)** | Same as Blob; HNS adds directory namespace | Same as Blob; +1–5 ms for hierarchical ops | Blob pricing + small hierarchical-namespace surcharge per tx | T2 |

**When to pick which**: Hot for active data <30 days. Cool for known-cold backup-type data that you read maybe quarterly. Cold (introduced 2023) for data older than 90 days but still readable cheaply — slots between Cool and Archive. Archive for compliance archives you'd rarely touch — 1–15 hr rehydrate is the cost. Premium Block Blob for high-RPS analytics / ML scratch workloads bottlenecked by Standard's per-account RPS ceiling (the equivalent of S3 Express One Zone — 7× pricier per GB, sub-10ms p99). ADLS Gen2 (Hot Blob with hierarchical namespace + ACLs) whenever you'll point Spark / Synapse / Databricks at the storage — the directory semantics matter for big-data engines.

**Hidden costs**: GRS / GZRS replication doubles per-GB cost. RA-GRS adds read-access secondary at +25% over GRS. Lifecycle policy transitions and Archive rehydration are per-tx — don't transition a billion tiny blobs. *Egress to internet* across the whole account is a unified bill — first 100 GB / month free, then $0.0875 / GB Zone 1 declining at volume.

---

## 5. Storage — File

| Service | Scale ceiling | Latency | Cost shape | Tier |
|---|---|---|---|---|
| **Azure Files Standard (transaction-optimized)** | 100 TiB / share (LRS); up to 100k IOPS | 5–10 ms ZRS | $0.06 / GB-mo + $0.015 / 10k tx (write) + $0.005 / 10k tx (read) | T0 |
| **Azure Files Standard (hot)** | Same | Same | $0.0255 / GB-mo + $0.015 / 10k tx (write) + tx pricing | T0 |
| **Azure Files Standard (cool)** | Same | Same | $0.015 / GB-mo + higher tx pricing | T0 |
| **Azure Files Premium (provisioned IOPS)** | 100 TiB / share; provisioned IOPS scale with GB | sub-ms | $0.16 / GB-mo (provisioned, includes baseline IOPS + bandwidth) | T0 |
| **Azure NetApp Files (Standard / Premium / Ultra service levels)** | 100 TiB / volume; up to 1k volumes / account | sub-ms (Premium / Ultra) | $0.000202 / GiB-hr (Standard) → $0.000403 (Premium) → $0.000538 (Ultra); +capacity-pool floor | T2 |
| **Azure Files NFS v4.1** | Premium tier only | sub-ms | Same as Files Premium | T0 |

**When to pick which**: Azure Files Standard for "Linux containers / Functions need shared POSIX storage" — SMB or NFS depending on tier. Files Premium when you need <10 ms p99 sustained, or NFS with predictable IOPS. Azure NetApp Files for HPC / SAP HANA / Oracle workloads needing >1 GB/s per client or NetApp-specific features (snapshots, cross-region replication, dual-protocol SMB+NFS). NetApp Files comes with a 4 TiB capacity-pool floor (~$300/mo Standard) — don't reach for it unless the workload justifies it.

---

## 6. Storage — Block

| Service | Scale ceiling | Latency | Cost shape | Tier |
|---|---|---|---|---|
| **Managed Disks — Premium SSD v2** | 64 TiB / disk; IOPS + throughput billed independently | sub-ms | $0.077 / GB-mo + IOPS (free first 3k, then $0.005 / IOPS-mo) + throughput | T0 |
| **Managed Disks — Premium SSD (v1)** | 32 TiB / disk; per-size IOPS coupling (P30 = 5k IOPS) | sub-ms | $0.135 / GB-mo (P10 / 128 GB ≈ $19/mo); discrete sizes | T0 |
| **Managed Disks — Standard SSD** | 32 TiB; lower IOPS | low-ms | $0.075 / GB-mo (E10 ≈ $9.60/mo) | T0 |
| **Managed Disks — Standard HDD** | 32 TiB; HDD | ms | $0.04 / GB-mo (S10 ≈ $5.12/mo) | T0 |
| **Managed Disks — Ultra Disk** | 64 TiB / disk; 160k IOPS / 4 GB/s | sub-ms; lowest tail | $0.119 / GB-mo + $0.058 / IOPS-mo + $0.000625 / MB/s-mo + VM enablement fee | T0 |
| **Local SSD / Temporary Disk (instance-attached)** | Per-VM SKU (e.g., L80s v3: 8 × 1.92 TB NVMe) | μs | Bundled with VM cost | T0 |
| **Azure Elastic SAN** | 100 TB / SAN; iSCSI multi-attach | <1 ms intra-region | Per-base-TiB + IOPS + throughput; capacity-floor-priced | T2 |

**When to pick which**: Premium SSD v2 by default — strictly better than v1 (provision IOPS / throughput independently of size; no discrete tier sizes). v1 only if a tool / template expects discrete P-sizes. Standard SSD for boot disks of dev VMs or low-IO workloads. Standard HDD for backups / cold mounted volumes. Ultra Disk only for write-heavy OLTP that genuinely needs >20k IOPS sustained — bills high enable-fee per VM. Local NVMe for Cassandra / Redis-style "shard data, instance replaceable" — data dies with the VM.

---

## 7. Database — RDBMS

| Service | Scale ceiling | Latency | Cost shape | Tier |
|---|---|---|---|---|
| **Azure Database for Postgres Flexible Server (Burstable)** | 16 TiB storage; B1ms (1 vCPU / 2 GB) → B16ms | sub-ms intra-AZ | $0.014 / hr (B1ms) → $0.221 / hr (B16ms); + storage | T0 |
| **Postgres Flexible Server (General Purpose)** | 16 TiB; D2ds_v5 (2 vCPU / 8 GB) → D96ds_v5 | sub-ms; HA across zones <100 ms failover | $0.157 / hr (D2ds_v5) → $7.50+ / hr (D96); + storage | T0 |
| **Postgres Flexible Server (Memory Optimized)** | Same; E2ds_v5 → E96ds_v5 (8× memory / vCPU vs GP) | sub-ms | $0.197 / hr (E2ds_v5) → $9.50+ / hr | T0 |
| **MySQL Flexible Server** | Same SKU shape as Postgres FS | Same | Similar pricing per SKU | T0 |
| **Azure SQL Database (Single, vCore General Purpose Gen5)** | 4 TB storage; 2–80 vCores | <10 ms intra-region | $0.255 / vCore-hr provisioned (2 vCores ≈ $370/mo) + storage | T0 |
| **Azure SQL Database (vCore Business Critical)** | 4 TB; AlwaysOn + in-memory + local SSD | sub-ms | $0.686 / vCore-hr + storage; ~2.7× GP | T0 |
| **Azure SQL Database (vCore Hyperscale)** | 100 TB; up to 4 read replicas; fast restore | low-ms; scale read replicas in <1 min | $0.347 / vCore-hr + $0.115 / GB-mo Hyperscale storage | T0 |
| **Azure SQL Database (vCore Serverless)** | Same as GP; auto-pause to 0 after 60-min idle | Resume in 1–60 s; min vCore configurable | $0.5218 / vCore-hr active (≈ $0.000145 / vCore-s); $0 compute when paused (storage only) | T0 |
| **Azure SQL Managed Instance (General Purpose / Business Critical)** | 16 TB; near-100% SQL Server surface (CLR, SQL Agent, cross-DB queries) | <10 ms | $0.347 / vCore-hr (GP) → $0.954 / vCore-hr (BC) + storage; ~$1k/mo floor | T0 |
| **Azure Database for MariaDB** | (retiring 2024-09; migrate to MySQL FS) | — | n/a | T2 (deprecated) |

**When to pick which**: **Postgres Flexible Server is the cross-cloud-portable default** — same Postgres surface as AWS RDS / GCP Cloud SQL; `db.sql: tier: …` maps here per `cloud_providers.md:73`. Azure SQL DB is reachable via `db.sql: variant: azure-sql` for users who want T-SQL / SSMS / partitioned tables / system-versioned temporal tables. Azure SQL Serverless for dev / staging / spiky workloads — strictly better than provisioned when sustained compute < ~25% (auto-pause is the kicker). Azure SQL Hyperscale for OLTP needing >4 TB or fast read-replica scale. SQL Managed Instance only when migrating an existing on-prem SQL Server with CLR / Service Broker / cross-DB joins — heavy ($1k/mo+ floor) for greenfield.

**Common pitfall**: Azure SQL Serverless never goes below the *min vCore* you configure (default 0.5). If your queries hit it every <60 min, it never pauses and you pay 24/7 — exactly the workload where provisioned GP would be cheaper. Validate the auto-pause is actually firing in production via metrics.

---

## 8. Database — NoSQL (Cosmos DB, all APIs)

| Service | Scale ceiling | Latency | Cost shape | Tier |
|---|---|---|---|---|
| **Cosmos DB — SQL API (provisioned RU/s)** | 1M RU/s per database soft (more by request); 10 GB / logical partition | <10 ms p99 read; <15 ms write (single-region) | $0.008 / 100 RU/s-hr (manual provisioned); + $0.25 / GB-mo | T0 |
| **Cosmos DB — SQL API (autoscale RU/s)** | Same; scales 10–100% of max | <10 ms p99 | $0.012 / 100 RU/s-hr (autoscale; 50% premium over manual) + storage | T0 |
| **Cosmos DB — SQL API (serverless)** | 5,000 RU/s burst ceiling; 1 TB / container | <10 ms p99 | $0.25 / M RU consumed + storage | T2 |
| **Cosmos DB — MongoDB API (vCore-based + RU-based)** | vCore-based: M30 / M40 / M50 / M60 / M80 instances; RU-based same as SQL API | <10 ms | vCore-based: $0.232 / vCore-hr; RU-based: as SQL API | T0 |
| **Cosmos DB — Cassandra API** | Same RU/s shape | <10 ms | Same as SQL API RU pricing | T0 |
| **Cosmos DB — Table API** | DynamoDB-shaped via RU model | <10 ms | Same as SQL API RU pricing | T0 |
| **Cosmos DB — Gremlin (graph) API** | Same RU model | <50 ms typical graph traversal | Same as SQL API RU pricing | T2 |
| **Cosmos DB — Integrated Cache (dedicated gateway)** | Adds read-cache layer in front of container | <2 ms cache-hit | $0.0464 / hr per dedicated gateway node + Cosmos pricing | T2 |
| **Cosmos DB — Multi-region writes (multi-master)** | Multi-region active-active across all enabled regions | sub-second cross-region replication | Provisioned RU × N regions enabled (2× for 2 regions, etc.) | T2 |

**When to pick which**: **SQL API by default** — closest to DynamoDB's item/partition model and the surface `cloud_providers.md:72` maps `kv` to. Autoscale RU/s for traffic with >2× peak-to-trough ratio (Cosmos's break-even); manual provisioned when traffic is predictable and you want the cheapest per-RU rate. Serverless only for true bursty low-volume workloads — 5k RU/s ceiling means it caps fast. MongoDB vCore-based when migrating an existing MongoDB Atlas / self-hosted cluster — strictly cheaper than MongoDB-via-RU for sustained workloads but loses Cosmos-native multi-master. Cassandra / Table / Gremlin APIs only when you have existing code in those wire formats — almost never the right greenfield choice over SQL API.

**Common pitfall**: Cosmos DB *hot partition* — a logical partition can sustain 10k RU/s. If your partition key concentrates on one value, you throttle even at low total RPS. The 100k RU/s burst at the *physical* partition layer doesn't help if logical partitions are skewed. Pick a high-cardinality partition key from day one — *much* harder to change later than in DynamoDB (Cosmos requires a data-migration to repartition).

**Trap**: turning on *multi-region writes* multiplies your bill by region count and forces last-writer-wins conflict resolution (or custom merge procedure in JS — rarely worth it). Only enable if cross-region write latency is a genuine requirement.

---

## 9. Database — Cache

| Service | Scale ceiling | Latency | Cost shape | Tier |
|---|---|---|---|---|
| **Azure Cache for Redis (Basic C0–C6)** | 53 GB / instance; no SLA | sub-ms | $0.022 / hr (C0 / 250 MB) → $0.694 / hr (C6 / 53 GB) — single-node, no replication | T0 |
| **Azure Cache for Redis (Standard C0–C6)** | Same per node; primary + replica HA | sub-ms; 99.9% SLA | 2× Basic per tier | T0 |
| **Azure Cache for Redis (Premium P1–P5)** | 530 GB / shard via cluster mode; persistence, VNet | sub-ms | $0.412 / hr (P1 / 6 GB) → $9.84 / hr (P5 / 530 GB); + cluster shards | T0 |
| **Azure Cache for Redis Enterprise (E10–E1000)** | Redis Inc.-built tier; Active-Active geo-replication; modules (RedisSearch, RedisTimeSeries, RedisBloom, RedisJSON) | sub-ms; lowest tail | $0.81 / hr (E10) → $54+ / hr (E1000); per-shard pricing | T0 |
| **Azure Cache for Redis Enterprise Flash (F300–F1500)** | NVMe-backed tier for cost-per-GB | low-ms (some cold reads from SSD) | $0.81 / hr (F300) → $25+ / hr — cheaper per GB than Enterprise | T0 |
| **Azure Managed Redis (GA 2024)** | Newer SKU consolidating Standard / Premium / Enterprise into 4-vCPU node multiples | sub-ms; 99.99% SLA | Per-vCPU-node-hour pricing; mid-tier between Standard and Enterprise | T0 |

**When to pick which**: Azure Managed Redis for new apps in 2026 — Microsoft's clear successor to Cache for Redis Standard / Premium with a cleaner pricing model. Cache for Redis Premium for existing apps needing VNet + persistence without the Enterprise license cost. Enterprise / Enterprise Flash for Active-Active geo-replication (multi-region writes) or modules (RedisSearch is genuinely useful for hybrid full-text + vector). Cache for Redis Basic *only* for dev — no SLA means it can go down for patching without warning. Memcached has no Azure equivalent — use Redis with eviction policy `allkeys-lru` for memcached-like behavior.

---

## 10. Database — Time-series / Graph

| Service | Scale ceiling | Latency | Cost shape | Tier |
|---|---|---|---|---|
| **Azure Data Explorer (ADX / Kusto)** | Up to PB scale per cluster; clusters of D11–D14 / L-series | <100 ms KQL query typical | $0.10 / hr (Dev D11 / 2 vCore) → $5+ / hr (production); + storage | T0 (kusto-locally-via-Lite is read-only — runtime T0 via stdout-then-ADX) |
| **Cosmos DB Gremlin API (graph)** | Same RU shape | <50 ms typical traversal | Same as Cosmos SQL API RU pricing | T2 |
| **Azure SQL Hyperscale w/ Graph extensions** | 100 TB; SQL graph node/edge tables | low-ms | Same as Azure SQL Hyperscale | T0 |
| **Azure Time Series Insights** | (retired 2025-03; migrate to Data Explorer or Fabric Real-Time Intelligence) | — | n/a | T2 (deprecated) |

**When to pick which**: ADX (now also surfaced as *Fabric Real-Time Intelligence* in Microsoft Fabric) for high-volume telemetry / logs / IoT data where you want KQL queries with sub-second latency. Cosmos Gremlin for property graphs — knowledge graphs, recommendation graphs, fraud rings. Azure SQL graph extensions when the graph is small (<100M edges) and you already have SQL infra — node/edge tables are SQL-relational with graph query syntax. Time Series Insights is gone — migrate.

---

## 11. Database — Search / Vector

| Service | Scale ceiling | Latency | Cost shape | Tier |
|---|---|---|---|---|
| **Azure AI Search (Basic)** | 2 GB index; 3 SU max | <100 ms search | $0.10 / hr (Basic / 1 SU) — ≈ $73/mo | T0 |
| **Azure AI Search (Standard S1 / S2 / S3)** | S1: 25 GB / 12 indexes; S2: 100 GB / 12 indexes; S3: 200 GB / 50 indexes; up to 36 SU | <100 ms | $0.336 / hr (S1 / 1 SU ≈ $245/mo) → $1.34 / hr (S3 / 1 SU) | T0 |
| **Azure AI Search (Storage-Optimized L1 / L2)** | L1: 1 TB / index; L2: 2 TB / index | <200 ms | $2.91 / hr (L1) → $5.82 / hr (L2) — for huge indexes | T0 |
| **Azure AI Search — Semantic ranker (standard plan)** | Up to 36 SU | adds 200–500 ms vs base | $1.00 / 1k queries above free tier (1k semantic queries / mo free) | T2 |
| **Azure AI Search — Vector search** | Bundled in any AI Search tier | 10–100 ms ANN query | Bundled in SU pricing (memory-hungry — Standard S2+ recommended) | T0 |
| **Postgres pgvector (on Flexible Server)** | Same as Flexible Server | 10–100 ms ANN | Same as Postgres Flexible Server | T0 |
| **Cosmos DB Vector Search (SQL API / MongoDB API)** | Vector indexing on Cosmos containers | 10–100 ms | Bundled with Cosmos RU consumption | T2 |

**When to pick which**: AI Search Basic / S1 for app-search with optional vector — the cheapest path to "find these documents fast" without a separate vector store. pgvector when you already run Postgres Flexible Server and your vector count is <10M — saves an entire system. Cosmos DB vector search when you already have a Cosmos container and want hybrid filter+vector queries from the same record. AI Search Storage-Optimized only for >100 GB indexes — the per-SU price jumps but per-GB is cheaper. Semantic ranker is genuinely better than BM25+vector for relevance but it bills per 1k queries on top of SU — measure the relevance lift before turning it on for high-RPS endpoints.

---

## 12. Messaging — Service Bus (Queue + Topic)

> **Hybrid divergence from AWS**: AWS splits queue and topic across SQS and SNS. Azure Service Bus genuinely sells both shapes from one product surface (queues, topics with up to 2k subscriptions per topic, sessions, transactions, dead-lettering, scheduled messages). For `cloud_providers.md` `queue` and `topic` primitives, Service Bus is the unified target. Storage Queues, Event Grid, and Web PubSub split into separate adjacent groups below.

| Service | Scale ceiling | Latency | Cost shape | Tier |
|---|---|---|---|---|
| **Service Bus Standard (queue or topic)** | 80 GB / queue or topic; 1k subscriptions / topic; soft RPS scales with billable operations | 10–100 ms | $0.0135 / hr base + $0.80 / M operations (each op = up to 64 KB message) | T0 |
| **Service Bus Premium (messaging units)** | Reserved capacity 1 / 2 / 4 / 8 / 16 MU per namespace; no shared multi-tenant noise; 100 GB / queue | <10 ms p99 | $0.917 / MU-hr (1 MU ≈ $670/mo) — predictable; no per-operation charge | T0 |
| **Service Bus (sessions / FIFO)** | FIFO via session-id; ordered delivery per session | Same as base tier | Same as base tier; no premium | T0 |
| **Service Bus (transactions / send-receive-ack)** | Cross-queue transactional batches | adds 10–30 ms | Same as base tier; transactional operation counts | T0 |

**When to pick which**: Standard for almost every greenfield queue+topic need — cheapest, scales fine to mid-volume. Premium when (a) you need predictable sub-10ms latency under load (Standard's multi-tenant noisy-neighbor matters at >1k msg/s), (b) you need >80 GB / queue, (c) you need VNet integration, or (d) you need Geo-Disaster Recovery pairing (Premium-only feature). Service Bus Sessions when you need FIFO *within a session* (e.g., one order's events) but not globally — exactly the SQS FIFO message-group semantics, with no global RPS cap.

**Pricing gotcha**: Standard tier's "$0.80 / M operations" hides that *every send, every receive, every renew-lock* is a separate operation. A naive consumer (peek-lock + receive + complete) is 3 operations per message — so $2.40 / M messages, not $0.80. Premium is often cheaper than Standard for sustained workloads above ~500 msg/s. Use the Service Bus pricing calculator with realistic ops-per-message math before picking Standard for "high volume."

---

## 13. Messaging — Storage Queue + Event Grid + Web PubSub

> **Hybrid divergence from AWS**: AWS bundles all event-routing under SNS / EventBridge. Azure splits it: Storage Queues (cheap), Event Grid (HTTP fan-out router), Event Hubs (streaming — see next group), Web PubSub / SignalR Service (WebSocket fan-out to clients). Different roles, different prices.

| Service | Scale ceiling | Latency | Cost shape | Tier |
|---|---|---|---|---|
| **Storage Queue** | 500 TB / storage account; 2k msg/s per queue; 64 KB max msg | 10–100 ms | $0.045 / 10k tx + Blob-style storage cost (~$0.02 / GB-mo) | T0 |
| **Event Grid (Custom Topics + System Topics)** | 5,000 events/sec per topic default; unlimited subscribers | <500 ms delivery | $0.60 / M operations (publish, delivery attempt, advanced filter) | T0 |
| **Event Grid Domains** | Aggregate custom topics; multi-tenant fan-out | Same | Same operation pricing | T2 |
| **Event Grid Namespace (pull delivery + MQTT broker)** | 5k connections / namespace; CloudEvents over HTTP / MQTT v3.1.1 / v5 | <500 ms | Per-operation + per-connection-minute | T0 (MQTT layer is OSS-compatible) |
| **Azure Web PubSub** | 1M concurrent WebSocket connections per resource | <100 ms message fan-out | $1.00 / unit-day (1 unit = 100k connections cap, 100 RPS) | T2 |
| **SignalR Service** | Similar; ASP.NET SignalR fan-out | <100 ms | Per-unit-day | T2 |

**When to pick which**: Storage Queue when "I just need a cheap queue" and don't need Service Bus features (sessions, transactions, dead-lettering, topics) — strictly cheaper than Service Bus Standard for high-volume simple workloads. Event Grid for content-based event routing across Azure services (Blob created → Function, Resource Manager event → Logic App, etc.) — analogue of EventBridge. Event Grid Namespace when you need MQTT broker semantics with managed TLS / auth (alternative to running mosquitto + auth proxy). Web PubSub for WebSocket fan-out (chat, real-time dashboards) — usually cheaper than API Management WebSocket + Functions backend.

**Often-missed**: Event Grid → Service Bus Queue is the canonical pattern for *durable fan-out* — Event Grid does the routing, Service Bus durably queues per consumer. Mirror of SNS → SQS fan-out.

---

## 14. Messaging — Streaming

| Service | Scale ceiling | Latency | Cost shape | Tier |
|---|---|---|---|---|
| **Event Hubs Basic** | 1 throughput unit (1 MB/s in, 2 MB/s out) / partition; 32 partitions / namespace | <100 ms | $0.015 / TU-hr + $0.028 / M events | T0 |
| **Event Hubs Standard** | Up to 40 TUs by request; 1 MB/s in per TU | <100 ms | $0.030 / TU-hr + $0.028 / M events | T0 |
| **Event Hubs Premium (1–16 PUs)** | 1 PU = ~5 MB/s + 1k partitions; isolation | <50 ms | $1.16 / PU-hr (1 PU ≈ $850/mo) — no per-event charge | T0 |
| **Event Hubs Dedicated (Capacity Units)** | 100+ MB/s ingress; single-tenant cluster | <50 ms | ~$6,500 / CU-mo committed pricing | T0 |
| **Event Hubs Capture → Blob/ADLS** | Built-in archival to Blob | Continuous | Bundled with TU/PU | T0 |
| **Event Hubs for Apache Kafka (Standard tier feature)** | Kafka protocol surface on Event Hubs | <100 ms | Same as Event Hubs Standard tiers | T0 (Kafka wire-compat) |
| **HDInsight Kafka cluster (managed Kafka on VMs)** | Cluster-sized; petabyte scale | <10 ms | Per-node VM cost + HDInsight markup | T0 |

**When to pick which**: Event Hubs Standard with the *Kafka protocol surface* for almost all new streaming pipelines — your code uses native Kafka clients (librdkafka, kafkajs, sarama), the wire is Kafka, but you don't operate brokers. Event Hubs Premium for >1 MB/s sustained per partition or when you need network isolation. Event Hubs Dedicated when you've measured >100 MB/s and predictable cost matters more than elasticity. HDInsight Kafka only when you need real Kafka semantics that Event Hubs' Kafka surface doesn't cover (exact-once via transactions, KRaft mode, broker-side compaction tuning) — and accept managing brokers. Use Event Hubs Capture for cheap-archival to Blob → Synapse/Fabric.

---

## 15. API / Web Edge

| Service | Scale ceiling | Latency | Cost shape | Tier |
|---|---|---|---|---|
| **Application Gateway v2 (WAF v2)** | 100 RPS / capacity unit baseline; autoscale 0–125 CUs | <10 ms LB overhead | $0.0072 / hr fixed + $0.008 / CU-hr; WAF: +$0.443 / hr fixed + $0.0144 / CU-hr | T0 |
| **Azure Load Balancer (Standard)** | Millions of flows/sec; L4 TCP/UDP | <1 ms LB overhead | $0.025 / hr / rule + $0.005 / GB processed | T0 |
| **Azure Load Balancer (Basic)** | (Retiring 2025-09 — migrate to Standard) | <1 ms | Free | T0 (deprecated) |
| **API Management (Consumption)** | 50k req/sec / region soft | adds 20–50 ms overhead | $4.20 / M operations (no fixed cost) | T2 |
| **API Management (Standard v2)** | 100M calls/mo baseline; predictable | adds 10–30 ms | $1.50 / hr + tiered overage | T2 |
| **API Management (Premium v2)** | Higher quotas + zonal HA + VNet | adds 10–30 ms | $4.20 / hr + tiered overage; min ~$3k/mo | T2 |
| **API Management (Developer)** | 1 unit only; no SLA | adds 10–30 ms | $0.07 / hr (≈ $50/mo) | T2 |

**When to pick which**: Application Gateway v2 for ALB-like HTTP load balancing + WAF in front of Container Apps / AKS — typically the right "front door inside one region" for a non-edge workload. Standard Load Balancer for L4 TCP/UDP (gaming, MQTT, gRPC at the LB tier). API Management Consumption for low-volume APIs where pay-per-call beats hourly — same pattern as API Gateway HTTP on AWS. API Management Standard v2 / Premium v2 for the full APIM developer-portal experience (subscriptions, products, OpenAPI publishing, policies) with predictable hourly billing. Developer tier *only* for non-prod (it's literally a single instance with no SLA). For pure CDN + global LB + WAF, see the CDN/Edge group below — Front Door often beats App Gateway + APIM for that role.

**Often-overlooked**: Container Apps' built-in ingress is genuinely production-grade — for many workloads you don't need Application Gateway in front at all. Reach for App Gateway only when you need WAF, multi-cert SNI, URL-based routing across multiple Container Apps environments, or session affinity that Container Apps revisions don't cover.

---

## 16. CDN / Edge

> **Hybrid divergence**: Azure Front Door and Azure CDN are merged into one group because they overlap in practice (Microsoft is steering everything toward Front Door; CDN from Verizon is already retired; CDN from Akamai is on retirement track too). Front Door is *also* a global load balancer with WAF — it cross-references the API/Web Edge group above for that role.

| Service | Scale ceiling | Latency | Cost shape | Tier |
|---|---|---|---|---|
| **Front Door Standard** | Global; tens of TB/s aggregate | <50 ms edge fetch | $35 / mo base + $0.083 / GB egress (first 10 TB Zone 1) + $0.009 / 10k requests; included WAF managed rules | T2 |
| **Front Door Premium** | Same; +bot manager + private link + advanced WAF | <50 ms | $330 / mo base + same egress + $0.022 / 10k requests; full WAF + bot management | T2 |
| **Azure CDN from Microsoft (Standard)** | Global | <50 ms | $0.081 / GB Zone 1 (first 10 TB); no fixed | T2 (deprecated — migrating to Front Door) |
| **Azure CDN from Akamai (Standard)** | Global | <50 ms | Same as Microsoft Standard | T2 (deprecated 2025-Q4) |
| **Azure Static Web Apps (Free + Standard)** | Bundles static hosting + linked Functions APIs + auth | <50 ms via Front Door internals | Free tier (100 GB/mo); $9 / app-mo Standard + bandwidth | T2 |
| **Front Door Rules Engine** | URL rewrites, header manipulation, redirects | sub-ms | Included in Front Door tier | T2 |

**When to pick which**: Front Door Standard for most workloads — global LB, CDN, WAF managed rules in one. Front Door Premium when you need bot management or private-link to backend origins (workloads in private VNets). Static Web Apps for SPA + small API combos (React / Vue / Next / Astro + a few Functions) — the Free tier covers portfolios and small projects, Standard adds custom auth + private endpoints. Migrate off Azure CDN (Microsoft / Akamai) by 2026 — Microsoft is consolidating onto Front Door.

**Trap**: Front Door Premium's $330 / mo fixed base is *steep* compared to AWS CloudFront's no-fixed-cost model. For low-traffic apps, Front Door Standard ($35 fixed) is the right tier; for >100 TB/mo traffic, the fixed cost is rounding error and Premium's bot manager + private link pay for themselves.

---

## 17. DNS

| Service | Scale ceiling | Latency | Cost shape | Tier |
|---|---|---|---|---|
| **Azure DNS (public zones)** | Unlimited records / zone | global anycast resolver, <30 ms | $0.50 / zone-mo (first 25); + $0.40 / M queries (first 1B); cheaper above | T0 |
| **Azure Private DNS** | VPC-scoped | <10 ms intra-VNet | $0.50 / zone-mo; + $0.40 / M queries | T0 |
| **Azure DNS Private Resolver** | VNet ↔ on-prem DNS bridge | <10 ms | $0.27 / endpoint-hr | T2 |
| **Traffic Manager** | DNS-level global LB with health checks | DNS-cached (TTL-bound) | $0.54 / M queries (first 1B) + $0.50 / endpoint-mo | T0 (DNS is global standard) |

**When to pick which**: Azure DNS for almost all Azure-hosted apps. Records via *alias records* to Azure resources (Front Door, Application Gateway, public IPs, Storage) resolve internally without extra query cost. Private DNS for service-discovery within VNet. Traffic Manager for DNS-level traffic routing — but Front Door does HTTP-level the same thing better in most cases (faster failover, no TTL caching pitfalls). Reach for Traffic Manager only when you need non-HTTP endpoint routing (e.g., game servers).

---

## 18. Identity / Auth

| Service | Scale ceiling | Latency | Cost shape | Tier |
|---|---|---|---|---|
| **Microsoft Entra ID (Free, included with subscription)** | Unlimited users; limited features (no Conditional Access, no PIM) | <100 ms | Free | T2 (lifecycle); T1 for JWKS token verify via community libs |
| **Microsoft Entra ID P1** | + Conditional Access, group-based licensing, password protection | <100 ms | $6 / user-mo | T2 |
| **Microsoft Entra ID P2** | + Identity Protection (risk-based CA), PIM, access reviews | <100 ms | $9 / user-mo | T2 |
| **Microsoft Entra External ID (formerly Azure AD B2C)** | 50k MAU free; 50M MAU supported per tenant | <100 ms | First 50k MAU free; $0.00325 / MAU (50k–100k); cheaper above | T2 (lifecycle); T1 for JWKS token verify |
| **Workload identities (managed identities)** | Per-resource user-assigned + system-assigned | <100 ms | Free | T2 (Azure-resource-bound) |

**When to pick which**: **Entra ID** for B2B / workforce identity — typically already in place via Microsoft 365 / E1+ licensing. **Entra External ID** for B2C apps and "I need users to sign up with email/google/etc." — free-tier covers most small apps. Managed Identities for *all* service-to-service auth in Azure — never bake credentials into code or Key Vault references when a managed identity will do.

**Common pitfall**: Entra External ID's free 50k MAU sounds generous but *all* sign-ins count (including failed). High-bot-traffic apps may blow past the free tier in pure noise — protect with Front Door Premium bot manager. Custom policies (the IEF / XML kind from old B2C) are still the only path to advanced UI flows but are a maintenance burden — keep flows simple.

---

## 19. Secrets / Config

> **Hybrid: App Configuration as a dedicated row**. AWS Parameter Store + AppConfig overlap; Azure splits them more clearly — Key Vault for secrets, App Configuration for feature flags + non-secret config. App Configuration earns its own row because its feature-flag-first model is genuinely richer than Parameter Store Advanced.

| Service | Scale ceiling | Latency | Cost shape | Tier |
|---|---|---|---|---|
| **Azure Key Vault (Standard)** | 25k transactions / 10 s per vault; 10 MB / secret | <100 ms cached | $0.03 / 10k transactions; $1 / cert / mo (managed certs) | T0 |
| **Azure Key Vault (Premium / HSM-backed)** | Same; FIPS 140-2 Level 2 HSM-protected keys | <100 ms | + $1 / key-mo (HSM keys); + per-op fee | T0 (software keys are T0; HSM is T2) |
| **Azure Managed HSM** | FIPS 140-3 Level 3 single-tenant HSM cluster | <100 ms | $4.27 / hr (≈ $3,100 / mo per cluster) | T2 |
| **App Configuration (Free)** | 1k requests / day; 10 MB | <100 ms via SDK cache | Free | T0 |
| **App Configuration (Standard)** | 30k requests / hr; 5 GB; private endpoint; geo-replication | <100 ms | $1.20 / day / replica + $0.60 / 100k requests above included | T0 |
| **Azure App Configuration Feature Manager** | Built-in feature-flag UI + .NET / Java / Python / JS SDKs | <100 ms via cache | Bundled with App Config Standard | T0 |

**When to pick which**: Key Vault for *all* secrets — connection strings, API keys, certs, SSH keys. Standard tier for almost everything; Premium only when compliance requires HSM-protected keys. Managed HSM for FedRAMP / High-compliance workloads needing single-tenant HSM. App Configuration Standard for centralized config + feature flags — strictly better than environment variables once you have >3 services sharing config or want to flip flags without redeploy. Free tier is fine for hobby / small apps but the 1k req/day cap surprises people; Standard's $36/mo floor is the entry point.

**Common mistake**: putting every config value in Key Vault. Key Vault is for *secrets*, not config — at 500 secrets, that's $0.15/mo + transaction costs and you've muddled which values rotate. App Configuration for non-secret config + Key Vault references for the secret subset is the right shape.

---

## 20. Workflow / Scheduling

> **Hybrid divergence**: Logic Apps gets its own row alongside Durable Functions because it's genuinely richer than EventBridge Scheduler / Step Functions — 800+ connectors, both designer-driven and code-driven workflows, two pricing models (Consumption / Standard) with different runtime characteristics.

| Service | Scale ceiling | Latency | Cost shape | Tier |
|---|---|---|---|---|
| **Logic Apps Standard (single-tenant, runs on App Service plan)** | Bounded by App Service plan; up to 100k runs/day per WS1 | <500 ms transitions | $200 / mo (WS1: 1 vCPU / 3.5 GB) → $800 / mo (WS3) — predictable | T0 |
| **Logic Apps Consumption (multi-tenant)** | 100M+ runs / mo per region | <500 ms | $0.000025 / action (built-in) + $0.000125 / connector action; data retention extra | T2 |
| **Durable Functions (Functions extension)** | Per-Function-host scale; orchestrations + activities + entities | <100 ms | Functions hosting cost only (no extra Durable fee) — Consumption / FlexConsumption / Premium / Dedicated | T0 (Durable runtime is OSS) |
| **Azure Functions Timer trigger** | 1M schedules; cron format | second-precision | Functions hosting cost only | T0 |
| **Azure Data Factory (pipelines)** | 1k concurrent activities default; 200 datasets / pipeline | Job pickup 30 s–2 min | $1 / 1k activity runs (mapping data flow: $0.193 / vCore-hr) | T0 (T2 for Mapping Data Flow visual surface) |
| **Synapse Pipelines** | Same shape as ADF (literally the same engine) | Same | Same as ADF | T0 |

**When to pick which**: **Logic Apps Standard** for production workflows with >100 runs/day — predictable hourly cost, single-tenant, can run on private endpoints / VNet. **Logic Apps Consumption** for sporadic workflows (<1 run/min) where pay-per-action wins. **Durable Functions** when you want code-as-workflow (C# / Python / JS / Java / PowerShell) instead of designer-as-workflow — strictly more flexible than Logic Apps but loses the connector library. **Timer triggers** for plain cron — analogue of EventBridge Scheduler. **Data Factory / Synapse Pipelines** for data-movement-shaped workflows (Copy activity from on-prem SQL → ADLS → Synapse transform) — overkill for HTTP / API orchestration, mandatory for any non-trivial ELT.

**Pricing trap**: Logic Apps Consumption charges per *action*, including ones that just evaluate a condition. A workflow with a 100-step loop is 100 actions per iteration — easy to surprise yourself at scale. Switch to Logic Apps Standard once you cross ~$50/mo on Consumption.

---

## 21. Email / Notifications

> **Hybrid divergence**: Azure Communication Services bundles Email + SMS + Chat + Calling + Number Lookup under one resource. AWS has SES + SNS SMS + Pinpoint as three separate services. The Azure mapping uses ACS Email for `email`, ACS SMS for `sms`, ACS Chat for in-app messaging, Notification Hubs for mobile push.

| Service | Scale ceiling | Latency | Cost shape | Tier |
|---|---|---|---|---|
| **Azure Communication Services — Email** | 100 emails/min sandbox (raise via Azure-managed domain or custom domain verification) | seconds | $0.00025 / email + $0.00012 / MB attachments | T1 (lettre/smtplib bridge or T0 via SMTP submission) |
| **Azure Communication Services — SMS** | Per-region per-number throughput | 5–30 s | $0.0075 / SMS US outbound (varies by country); + $1 / phone-number-mo | T2 |
| **Azure Communication Services — Chat** | Per-resource concurrent users | <500 ms | $0.40 / 1k chat messages | T2 |
| **Azure Communication Services — Calling (PSTN + VoIP)** | Per-resource concurrent calls | sub-second | Per-minute + per-number | T2 |
| **Notification Hubs (Free / Basic / Standard)** | Free: 1M push / mo; Standard: 10M+ active devices | <5 s push delivery | Free: $0; Basic: $10 / mo + tiered; Standard: $200 / mo + tiered | T2 |

**When to pick which**: ACS Email for transactional email (signups, receipts) — under $0.0003 / email is competitive with SES. ACS SMS for transactional SMS — for marketing SMS use a 3rd party (Twilio, MessageBird) for better deliverability. Notification Hubs for fan-out to APNs / FCM / Baidu push — Microsoft's equivalent of SNS Mobile Push. ACS Chat / Calling reach far beyond what AWS offers natively — used by teams building Teams-like or telehealth-like products who want managed signaling + media servers.

---

## 22. Observability

| Service | Scale ceiling | Latency | Cost shape | Tier |
|---|---|---|---|---|
| **Azure Monitor Logs (Log Analytics workspace, pay-as-you-go)** | Unlimited per workspace | seconds-to-minutes ingestion | $2.76 / GB ingested + $0.10 / GB-mo storage (first 31 days free) | T0 (via stdout / Container Insights / App Insights SDK) |
| **Azure Monitor Logs (Commitment Tiers 100 GB–5 TB/day)** | Same | Same | $1.50 / GB at 5 TB/day commitment (~45% discount) | T0 |
| **Azure Monitor Logs (Basic Logs)** | Per-table opt-in | seconds ingestion | $0.65 / GB ingested + $0.10 / GB-mo storage + $0.005 / GB queried | T0 |
| **Application Insights** | Per-app; same workspace backend | seconds | Same per-GB ingest as Log Analytics; +Live Metrics / Smart Detection | T0 (via OpenTelemetry → workspace) |
| **Azure Monitor Metrics (Custom)** | 1-min resolution standard; 1-sec for Premium | 1-min standard | First 175 MB / mo free; $0.258 / M time-series samples beyond | T0 (via OpenTelemetry / direct API) |
| **Alert Rules + Action Groups** | Per-rule | seconds | $0.10 / metric-rule-mo; $0.50 / log-rule-mo; first 1k SMS / mo free | T2 (triggering doesn't reproduce locally) |
| **Azure Workbooks** | Dashboard queries over workspaces | seconds | Free (queries cost via underlying workspace) | T2 |
| **Application Insights Smart Detection** | Auto-detect anomalies | minutes | Bundled with App Insights | T2 |
| **Network Watcher** | Per-region | minutes | $0.30 / GB Connection Monitor; $0.10 / hr packet capture | T2 |

**When to pick which**: Azure Monitor Logs / Log Analytics workspace as the default destination — but at $2.76 / GB it gets expensive fast at >100 GB/mo ingestion. Many teams pipe to a Storage account (via Diagnostic Settings) for cheap long-term retention and use Log Analytics for hot 31 days only. App Insights for distributed tracing + APM via OpenTelemetry — solid free-tier-friendly tracing. Alert Rules are *critical* for actually getting paged — but they're T2 because the triggering side (action groups, ITSM connectors, webhook to PagerDuty) is Azure-cloud-only.

**Cost trap**: Azure Monitor Logs at $2.76 / GB is one of the highest-margin Azure services. Use diagnostic settings to send only what you need (don't ingest all Activity Log if you don't query it); enable *Basic Logs* tier on tables you only need to keep for compliance.

---

# Data / Analytics (7 groups)

> **Hybrid divergence**: Microsoft Fabric is its own group (subsumes warehouse + lakehouse + BI + pipelines). Synapse Dedicated SQL and Synapse Serverless SQL remain separate groups because they're commonly used as point-solutions outside Fabric. Power BI gets its own group because it's the BI tool many shops use without touching the rest of the stack.

## 23. Analytics — Synapse Dedicated SQL Pool (legacy warehouse)

| Service | Scale ceiling | Latency | Cost shape | Tier |
|---|---|---|---|---|
| **Synapse Dedicated SQL Pool (DW100c–DW30000c)** | Up to 240 TB compressed; massively parallel processing | sub-sec to seconds query | $1.51 / hr (DW100c) → $452 / hr (DW30000c); + storage $122.88 / TB-mo | T2 |
| **Synapse Dedicated SQL Pool (paused state)** | Same data; compute offline | — | $0 compute; storage only | T2 |
| **Synapse Serverless SQL Pool** | Query ADLS / Blob / Cosmos DB via T-SQL; no infra | seconds-to-minutes | $5.00 / TB processed | T0 (queries OSS Parquet/CSV) |

**When to pick which**: **Microsoft is steering everyone toward Fabric Data Warehouse instead of Synapse Dedicated SQL Pools for new builds.** Dedicated Pools survive for migration scenarios + teams committed to the pre-Fabric Synapse workspace. Pause workloads when idle (>4 hr/day idle → pausing saves bigger fraction than RIs). **Synapse Serverless SQL** is the strongest single-component to keep — it's a T0 query layer over ADLS that beats Athena's pricing for some shapes ($5/TB scanned vs Athena's $5/TB but with broader Parquet/CSV/Delta support natively). Use Serverless SQL for ad-hoc analytics on data lakes you're not warehousing.

---

## 24. Analytics — Microsoft Fabric

| Service | Scale ceiling | Latency | Cost shape | Tier |
|---|---|---|---|---|
| **Fabric Capacity F2** | 2 CU; smallest production tier | seconds | $0.18 / CU-hr → $0.36 / hr (F2 ≈ $263 / mo) — `eastus` PAYG | T2 |
| **Fabric Capacity F4 / F8 / F16 / F32** | Linear scale | Same | $0.18 × N CU-hr | T2 |
| **Fabric Capacity F64** | 64 CU; threshold above which Power BI Pro licenses no longer required for viewers | <1 s for cached Power BI; seconds for warehouse | $0.18 × 64 = $11.52 / hr (F64 ≈ $8,400 / mo) | T2 |
| **Fabric Capacity F128 / F256 / F512 / F1024 / F2048** | Up to 2048 CU | Same | $0.18 × N CU-hr | T2 |
| **Fabric Reserved Instance (1y)** | Same capacity | Same | ~40% discount over PAYG | T2 |
| **Fabric workloads (included with capacity)** | Data Factory, Data Engineering (Spark notebooks), Data Warehouse, Lakehouse (OneLake on ADLS), Real-Time Intelligence (KQL), Data Science (notebooks + AutoML), Power BI Premium-equivalent | varies per workload | All bundled into CU consumption | T2 |
| **Fabric Mirroring (Cosmos DB / Azure SQL / Snowflake / on-prem)** | Free 1 TB / mo per Fabric capacity | Continuous near-real-time | Free up to limit; overage via OneLake storage | T2 |

**When to pick which**: Fabric F2 / F4 for *very small* shops or proof-of-concept — below F64 you still need Power BI Pro licenses for viewers (~$10 / user / mo), so total cost can equal F64 once you have ~70 viewers. **F64 is the major break-even** — at $8,400 / mo, you get unlimited Power BI report consumers for free. Reserved Instances at 40% off for steady production workloads. Fabric Mirroring (no-ETL replication from operational stores into OneLake) is one of the best features in 2026 — turn on first for any Cosmos/Azure SQL workload that needs analytics without standing up Data Factory pipelines.

**Trap**: Fabric capacity *bursts* by consuming "smoothing" budget — if you exceed your CU rate for too long, Microsoft throttles or charges autoscale overage. The CU model is opaque compared to "I provisioned N vCores" — measure actual workload CU consumption in a non-prod F2 before committing to capacity sizing.

---

## 25. Analytics — Databricks-on-Azure

| Service | Scale ceiling | Latency | Cost shape | Tier |
|---|---|---|---|---|
| **Azure Databricks Standard workspace** | Thousands of nodes; Spark / Delta Lake / MLflow | seconds startup with cluster pools | $0.40 / DBU-hr (Jobs Compute) → $0.55 / DBU-hr (All-Purpose); + VM cost | T0 (Spark wire compat; Delta Lake is OSS) |
| **Azure Databricks Premium workspace** | Same + role-based access, Unity Catalog, Photon | Same | $0.55 / DBU-hr Jobs → $0.95 / DBU-hr All-Purpose; + VM cost | T0 |
| **Azure Databricks SQL Warehouse (Serverless)** | Auto-scaling Photon-powered SQL warehouse | sub-second query startup | $0.70 / DBU-hr (Serverless SQL); + VM cost | T0 |
| **Databricks Model Serving** | Real-time inference endpoints | <100 ms | $0.07 / DBU-hr active inference (varies by size) | T0 |

**When to pick which**: Databricks Premium for any shop already on Databricks elsewhere — Unity Catalog is the multi-workspace governance story you want. Databricks SQL Warehouse Serverless for ad-hoc SQL on Delta Lake — strictly better than Synapse Dedicated SQL for new Delta-Lake-native workloads. Databricks-on-Azure vs Microsoft Fabric is the major analytics decision of 2026: Fabric for "all Microsoft, one capacity, OneLake-native" shops; Databricks for "Spark / Delta Lake first, multi-cloud, MLflow-heavy" shops.

---

## 26. Analytics — HDInsight (managed open-source clusters)

| Service | Scale ceiling | Latency | Cost shape | Tier |
|---|---|---|---|---|
| **HDInsight Hadoop / Hive cluster** | Per-cluster node count; A4–A8 small to D14–E64 large | minutes startup | VM cost + HDInsight per-core markup ($0.10–$0.30 / vCore-hr) | T0 (Hadoop is OSS) |
| **HDInsight Spark cluster** | Same | minutes | Same | T0 |
| **HDInsight Kafka cluster** | Same | minutes | Same | T0 |
| **HDInsight HBase cluster** | Same | minutes | Same | T0 |
| **HDInsight Interactive Query (LLAP)** | Per-cluster; LLAP daemons | seconds query | Same | T0 |

**When to pick which**: HDInsight is the legacy / OSS-first option — fully managed Hadoop / Spark / Kafka / HBase clusters on VMs. Reach for it when you need the *exact* OSS engine behavior (specific Hadoop version, Hive Metastore compatibility, HBase API). For greenfield Spark, Databricks-on-Azure or Synapse Spark Pools win on operator-experience. For Kafka, Event Hubs' Kafka protocol surface wins on operability. HDInsight survives mostly for shops migrating on-prem Hadoop to Azure without rewriting.

---

## 27. Analytics — ETL / Catalog

| Service | Scale ceiling | Latency | Cost shape | Tier |
|---|---|---|---|---|
| **Azure Data Factory (Copy activity + pipelines)** | 1k concurrent activities default | minutes (job startup) | $0.25 / 50-DIU-hr Copy + $1 / 1k activity runs | T0 |
| **Azure Data Factory Mapping Data Flows** | Spark-backed visual ETL | minutes | $0.193 / vCore-hr (compute) | T2 (visual surface) / T0 (underlying Spark) |
| **Synapse Pipelines** | Same engine as ADF | Same | Same | T0 / T2 |
| **Microsoft Purview Data Map (catalog + lineage)** | Per-capacity-unit | minutes for scans | $0.21 / capacity-unit-hr + $0.42 / scan-vCore-hr; min ~$300 / mo | T2 |
| **Microsoft Purview DLP / Data Governance** | Per-document | — | Bundled with Purview capacity-unit pricing | T2 |
| **Azure Stream Analytics** | Up to 192 SUs / job | sub-second | $0.11 / SU-hr (Standard); $0.22 (Dedicated) | T2 |

**When to pick which**: Data Factory for traditional batch ETL with Copy activities — pricier than self-managed Airflow + workers but no infra to babysit. Mapping Data Flows for visual ETL when your team prefers it over PySpark code. Purview when your data lake needs cross-source lineage tracking + classification + access policies — the Azure analogue of Lake Formation + Glue Catalog combined. Stream Analytics for SQL-shaped streaming queries over Event Hubs — but most shops moving to Fabric Real-Time Intelligence or Databricks Streaming for new workloads.

---

## 28. Analytics — Big-data Compute

| Service | Scale ceiling | Latency | Cost shape | Tier |
|---|---|---|---|---|
| **Synapse Spark Pools (Spark on demand)** | Auto-scale; up to 200 nodes | minutes startup | $0.169 / vCore-hr (Memory Optimized) | T0 |
| **HDInsight Spark / Hadoop (see above)** | Cluster-sized | minutes | VM + markup | T0 |
| **Azure Databricks (see above)** | Thousands of nodes | seconds with pools | $0.40 / DBU-hr + VM | T0 |
| **Microsoft Fabric Data Engineering** | Bundled with Fabric capacity | varies | Bundled with CU | T2 |
| **Azure Container Apps + Spark image** | Per-container | seconds | Container Apps Consumption rate | T0 |

**When to pick which**: Synapse Spark Pools for batch Spark workloads inside a Synapse workspace — strictly cheaper than Databricks for non-Photon workloads. Databricks when you need Photon, MLflow, or Unity Catalog. Fabric Data Engineering when you're already on a Fabric capacity. For light Spark jobs (data validation, small ETL), running Spark in a Container App is genuinely viable in 2026.

---

## 29. Analytics — BI

| Service | Scale ceiling | Latency | Cost shape | Tier |
|---|---|---|---|---|
| **Power BI Pro** | Per-user license | sub-second cached | $10 / user-mo | T2 |
| **Power BI Premium Per User (PPU)** | Per-user, premium features | Same | $20 / user-mo | T2 |
| **Power BI Premium (Capacity P1–P5)** | Per-capacity (subsumed by Fabric F-SKUs in 2026) | Same | Now via Fabric F64+ pricing | T2 (rolled into Fabric) |
| **Power BI Embedded (A1–A6 SKUs)** | Per-capacity for embedded scenarios | Same | $0.20 / hr (A1) → $24 / hr (A6) | T2 |

**When to pick which**: Power BI Pro for individual report authors and small teams. PPU when you need premium features (large models, paginated reports, AI insights) for <70 users — break-even with Premium capacity. Power BI Embedded for SaaS apps embedding Power BI in your own UI — A1 / A2 SKUs are the entry; size up as embed traffic grows. **For new workloads in 2026, evaluate Fabric F64 instead** — same Power BI Premium features included, plus the rest of Fabric.

---

# ML / AI (5 groups)

## 30. ML — Training / Serving Platform (Azure Machine Learning)

| Service | Scale ceiling | Latency | Cost shape | Tier |
|---|---|---|---|---|
| **Azure ML Compute Instance (managed dev VMs)** | Per-instance VM size | minutes startup | Underlying VM cost + small Azure ML markup | T0 (Jupyter / VS Code OSS base) |
| **Azure ML Compute Cluster (training)** | Per-job pool | minutes | Underlying VM cost (use Spot for 90% off) | T0 |
| **Azure ML Managed Online Endpoint (real-time inference)** | Per-endpoint instance fleet auto-scale | <100 ms p99 | Per-VM-hour underlying + small platform fee | T0 |
| **Azure ML Managed Online Endpoint (serverless)** | Auto-scale based on concurrent requests | 100–500 ms cold; <100 ms warm | $0.0000200 / GB-s + per-request | T0 |
| **Azure ML Batch Endpoint** | Run on a dataset, save outputs | minutes | Per-instance | T0 |
| **Azure ML Pipelines** | DAG orchestration over training/eval | varies | Compute used; orchestration free | T0 |
| **Azure ML Designer (no-code)** | Drag-drop pipeline builder | — | Compute used | T2 |
| **Azure AI Foundry / AI Studio** | UI for foundation models + agents + evaluations | — | Underlying model + compute costs | T2 |

**When to pick which**: Azure ML Compute Cluster + Spot for batch training jobs that checkpoint (90% off VM cost). Managed Online Endpoint Serverless for spiky inference loads — pay per GB-second of inference. Real-time endpoint with always-on instances for steady traffic. Batch Endpoint for "run on this whole dataset" workflows. ML Pipelines for the DAG orchestration of train→eval→register→deploy. AI Foundry is Microsoft's 2025/26 consolidation of the AI Studio + ML Studio + OpenAI deployment story into one workspace.

---

## 31. ML — Foundation Models (Azure OpenAI + Foundry Models)

| Service | Scale ceiling | Latency | Cost shape | Tier |
|---|---|---|---|---|
| **Azure OpenAI — GPT-4o** | Per-deployment TPM quota | <1 sec to first token | $2.50 / M input + $10 / M output (web-verified 2026-05) | T1 (rig-core / litellm bridge) |
| **Azure OpenAI — GPT-4o mini** | Same | <1 sec | $0.15 / M input + $0.60 / M output | T1 |
| **Azure OpenAI — o3** | Same; reasoning model | 5–30 sec depending on reasoning effort | $2.00 / M input + $8.00 / M output | T1 |
| **Azure OpenAI — o4-mini** | Same; cheap reasoning | 2–10 sec | $1.10 / M input + $4.40 / M output | T1 |
| **Azure OpenAI — GPT-4.1** | Same | <1 sec | $2.00 / M input + $8.00 / M output | T1 |
| **Azure OpenAI — Embeddings (text-embedding-3-small / large)** | Per-deployment | <100 ms | $0.020 / M tokens (small) / $0.130 / M tokens (large) | T1 |
| **Azure OpenAI — Provisioned Throughput Units (PTU)** | Reserved capacity per model | predictable | Per-PTU-hour committed pricing (hours / months / years) | T1 |
| **Foundry Models direct (Phi, Mistral, Cohere, Llama, others)** | Per-model quotas | varies | Per-model token pricing (typically cheaper than OpenAI tier) | T1 |
| **AI Foundry Agent Service (managed agent runtime)** | Orchestrated tool-use over Foundry models | adds 1–3 sec | Per-invocation + underlying model tokens | T2 |
| **Azure OpenAI On Your Data (managed RAG)** | Vector store + retrieval pipeline | depends on vector store | Per-query + AI Search + token costs | T2 |
| **Azure OpenAI Assistants API** | Tool-use API surface | varies | Per-token + per-tool-call | T2 |
| **Azure AI Content Safety** | Filter input/output for unsafe content | <500 ms | $0.75 / 1k text records (input or output) | T2 |

**When to pick which**: Azure OpenAI when you want Anthropic-quality LLMs *under Azure governance* (RBAC, private endpoints, data-residency commitments) — same model surface as OpenAI direct, slightly different deployment-name semantics. GPT-4o for most general-purpose work. GPT-4o mini for cheap & fast (Haiku-equivalent). o3 / o4-mini when reasoning matters (math, multi-step planning, agentic). PTU when spending >$5k / mo on PAYG for predictability and lower per-token cost. Foundry Models direct (Phi / Mistral / Llama / Cohere) for open-weight or third-party models without operating GPUs. AI Foundry Agent Service for managed agent loops with tool calls (analogue of Bedrock Agents).

**Pricing volatile**: Token prices change quarterly; verify on `azure.microsoft.com/en-us/pricing/details/azure-openai/` and the Foundry Models pricing page before committing. Microsoft also runs region-specific deals (e.g., Sweden Central often cheaper than East US for the same SKU).

---

## 32. ML — Vision

| Service | Scale ceiling | Latency | Cost shape | Tier |
|---|---|---|---|---|
| **Azure AI Vision — Image Analysis** | Per-image API | <500 ms typical | $1.00 / 1k transactions (S1 standard) | T1 (OpenCV / YOLOv8 bridge) |
| **Azure AI Vision — Dense Captions / Smart Crop** | Per-image | <500 ms | Bundled in S1 transaction | T1 |
| **Azure AI Vision — Face** | Face detection / verification / identification | <500 ms | $1.00 / 1k tx (S0) — gated by responsible-AI access review | T1 |
| **Azure AI Document Intelligence (formerly Form Recognizer)** | Per-page OCR + structure extraction | seconds | $1.50 / 1k pages (Read) → $50 / 1k pages (custom-trained models) | T1 (tesseract + layoutparser) |
| **Azure AI Vision — Spatial Analysis** | Real-time video analytics for retail / safety | seconds | $0.025 / hr / camera (Enterprise license) | T2 |
| **Custom Vision (legacy — superseded by AI Vision)** | Train classification / detection on your data | <500 ms | $0.40 / 1k tx + training hours | T2 (deprecated track) |

**When to pick which**: AI Vision Image Analysis when off-the-shelf vision suffices (content moderation, simple object detection, captions). AI Vision Face only when you've passed Microsoft's responsible-AI access review — gated access. Document Intelligence for PDF / scan extraction — significantly better than Tesseract on real-world forms; custom-trained models for high-volume domain-specific docs. For anything custom and high-volume, Azure ML + a model from Foundry or your own training wins.

---

## 33. ML — Speech

| Service | Scale ceiling | Latency | Cost shape | Tier |
|---|---|---|---|---|
| **Azure AI Speech — TTS (Neural voices)** | 100+ voices, 90+ languages | <500 ms for short utterances | $16 / 1M characters (Neural) → $30 / 1M (Neural HD) | T1 (coqui-ai / piper bridge) |
| **Azure AI Speech — Custom Neural Voice** | Train custom voice from samples | Same | Custom training fee + per-character usage | T1 |
| **Azure AI Speech — STT (real-time)** | Streaming + batch | sub-second streaming | $1.00 / hr of audio (Standard) | T1 (whisper bridge) |
| **Azure AI Speech — STT (batch)** | Async over Blob | minutes | $0.30 / hr of audio (Batch) | T1 |
| **Azure AI Speech — Speaker Recognition** | Speaker verification + identification | sub-second | $0.40 / 1k tx | T1 |
| **Azure AI Speech — Translation (speech translation)** | Real-time speech-to-text-to-translation | <1 s | $2.50 / hr of audio | T1 |
| **Azure AI Speech — Pronunciation Assessment** | Score pronunciation against reference | sub-second | $0.30 / 1k tx | T2 |

**When to pick which**: AI Speech TTS Neural voices for accessible TTS in webapps — good enough for most use cases. Neural HD voices when you need lifelike (audiobooks, virtual hosts). STT batch ($0.30/hr) is significantly cheaper than real-time STT ($1.00/hr) — use batch for podcast / archive transcription. For specialized accents / non-English / nuance, OpenAI Whisper or Deepgram often beat Azure STT.

---

## 34. ML — NLP / Translation

| Service | Scale ceiling | Latency | Cost shape | Tier |
|---|---|---|---|---|
| **Azure AI Language — Sentiment / Entities / Key Phrases / PII** | Per-document | <500 ms | $0.0001 / unit (1 unit = 1k chars); $2.00 / 1k records for advanced features | T1 (spaCy bridge) |
| **Azure AI Language — Conversational Language Understanding (CLU)** | Intent + entity extraction | <500 ms | $2.00 / 1k tx | T1 |
| **Azure AI Language — Question Answering** | Custom QA over docs | <500 ms | $10 / instance-mo + $0.40 / 1k tx | T2 |
| **Azure AI Language — Custom Text Classification** | Train classifier on your labels | <500 ms | $0.50 / 1k tx + training-instance-hours | T2 |
| **Azure AI Translator (text)** | 100+ language pairs | <500 ms | $10 / M chars (S1) → cheaper above | T1 (argos-translate bridge) |
| **Azure AI Translator (document)** | Async batch over docs | minutes | $15 / M chars | T1 |

**When to pick which**: AI Language for off-the-shelf NLP — sentiment / entities / key phrases / PII detection. CLU for intent classification (chatbots, query routers) — but for serious LLM-era apps, just prompt an LLM with the question and skip CLU. Translator is fine for most translation; specialized providers (DeepL) often beat it on European languages.

---

# IoT / Edge (3 groups)

## 35. IoT — Device Gateway

| Service | Scale ceiling | Latency | Cost shape | Tier |
|---|---|---|---|---|
| **IoT Hub (Free tier F1)** | 8k messages/day; 500 devices | <100 ms | Free | T0 (mosquitto / MQTT broker locally) |
| **IoT Hub Basic (B1–B3)** | 400k–300M messages/day | <100 ms | $0.025 / hr (B1) → $2.50 / hr (B3) | T0 |
| **IoT Hub Standard (S1–S3)** | 400k–300M messages/day; +device twins, methods, file upload | <100 ms | $0.034 / hr (S1) → $3.40 / hr (S3) | T0 |
| **IoT Hub Device Provisioning Service (DPS)** | 1M devices / DPS instance | seconds-to-minutes per provision | $0.123 / 1k registrations | T2 |
| **Event Grid MQTT broker (alternative for v3/v5)** | 5k connections / namespace | <500 ms | Per-operation + per-connection-minute | T0 (MQTT wire) |

**When to pick which**: IoT Hub for MQTT at scale with X.509 device certs or SAS tokens. Standard tier when you need device twins (synced state) / direct methods (RPC to device) / Cloud-to-Device commands. DPS for zero-touch enrollment at scale (factory-flashed devices that self-register on first boot). Event Grid MQTT broker is the newer / simpler option for pure pub-sub MQTT without IoT Hub features — strictly cheaper if you don't need twins / methods / file upload.

---

## 36. IoT — Edge Runtime

| Service | Scale ceiling | Latency | Cost shape | Tier |
|---|---|---|---|---|
| **Azure IoT Edge runtime** | Edge container runtime; modules sync from IoT Hub | local; depends on device | Free runtime; IoT Hub tier for cloud sync | T0 (runtime is OSS Moby) |
| **Azure IoT Operations** | Cloud-native edge platform (MQTT broker, OPC-UA, data flows) on AKS Edge / Arc | local | Per Arc-enabled-node fee | T2 |
| **Azure Sphere** | (deprecated end-of-life 2027-09 — migrate) | local | Per-device licensing | T2 (deprecated) |

**When to pick which**: IoT Edge runtime for "I need to run inference + buffer telemetry at the device" — supports Linux + Windows + ARM. Azure IoT Operations is Microsoft's 2024+ converged edge platform — adopt only if you're aligning on AKS Edge / Arc strategy. Azure Sphere is on retirement track — don't start new builds.

---

## 37. IoT — Analytics / Management

| Service | Scale ceiling | Latency | Cost shape | Tier |
|---|---|---|---|---|
| **IoT Central** | SaaS IoT solution accelerator; 5k devices baseline | <500 ms | $0.40 / device-mo (Standard 1) → $0.10 / device-mo (Standard 3) | T2 |
| **Azure Digital Twins** | Twin graph; query-able digital model of physical assets | <500 ms | $0.001 / API operation + per-query | T2 |
| **Defender for IoT** | OT / IoT security monitoring | minutes | $0.001 / device-hr (managed) | T2 |
| **Time Series Insights** | (retired 2025-03; migrate to Data Explorer or Fabric Real-Time Intelligence) | — | n/a | T2 (deprecated) |

**When to pick which**: IoT Central for "SaaS IoT in a box" — solution templates for connected products, fleet management, predictive maintenance. Mostly superseded by custom builds on IoT Hub + Container Apps + ADX for teams with engineering capacity. Digital Twins for industrial digital-twin modeling — domain-language for "this pump is connected to this tank" — analogue of AWS IoT TwinMaker. Defender for IoT for OT-network monitoring (passive packet inspection on industrial networks).

---

# Tier 2 deep-dive

## What "Tier 2" means in supeux terms

Tier 2 means no OSS container reproduces the service locally. For the user's **runtime** story this is already solved — user container code calls the Azure SDK directly with workload-identity (Managed Identity) or `DefaultAzureCredential` (which picks up `AZURE_*` env vars, az CLI auth, or service principal env vars locally), which works identically whether the container runs locally (`DefaultAzureCredential` finds CLI creds) or on Container Apps / Functions / VMs (Managed Identity auto-injected). No supeux runtime abstraction is needed.

The only open question per service is the **provisioning** story: does its config shape fit a short `cloud_only:` yaml block (worth a registry slot), or is the config big / dynamic enough that hand-written `.tf` via the `terraform-module` escape hatch (`supeux_abstraction_v2.md` §2) is more honest? The sub-grouping below splits Tier 2 by *why* it's niche; the per-service paragraphs that follow judge each truly-niche entry on one of three verdicts:

- **`yaml-registry`** — config fits a short `cloud_only:` block (model ID + a few knobs). Ship a registry entry.
- **`hand-tf`** — config is too large / dynamic for short yaml (entity graphs, custom IEF policies, recipe configs). Document the SDK call pattern, route to `resources.<name>: { type: terraform-module, source: ./modules/foo }`.
- **`skip`** — deprecated, end-of-life, or so narrow that even hand-tf documentation isn't worth shipping.

## Tier 2 sub-groups (by why-niche)

The eight buckets parallel the AWS file's bucketing, oriented around what makes the service unreachable from a local container, not which v2 §4 pattern fits (skip / hit-real / engine-swap / stub) — that pattern dimension is complementary.

1. **Edge / CDN / WAF** — Front Door Standard/Premium, Azure CDN Microsoft / Akamai (retiring), Static Web Apps preview environments, Application Gateway WAF custom rules. Edge POP routing + WAF rule engines are the value-add; no local equivalent.
2. **Multi-region coordination** — Cosmos DB multi-region writes, Cosmos DB Strong-Across-Regions consistency, Azure SQL geo-replication, Service Bus Geo-DR, Traffic Manager. Active-active or cross-region fan-out is a coordination property that single-node OSS can't reproduce.
3. **Managed ML / AI orchestration** — Azure OpenAI On Your Data, AI Foundry Agent Service, Azure OpenAI Assistants API, AI Content Safety, AI Studio + ML Designer no-code surfaces, Bot Service. Orchestration and bundled-model marketplaces are the proprietary value; the underlying inference is already T1 via community libraries.
4. **IoT vertical** — IoT Central, Azure IoT Operations, Defender for IoT, Digital Twins, IoT Hub DPS. Vertical-specific data models (twin graphs, OT-network protocols) sit above the MQTT layer (T0 via mosquitto or Event Grid MQTT).
5. **Specialty storage variants** — Premium Block Blob, ADLS Gen2 hierarchical namespace, Azure NetApp Files, Azure Elastic SAN, Azure Container Storage, Cosmos DB integrated cache (dedicated gateway), Cosmos DB vector search, Cosmos DB serverless. Same primitive shape with a proprietary performance or query characteristic Microsoft hasn't open-sourced.
6. **Identity / lifecycle / observability / messaging lock-in** — Entra ID Conditional Access / PIM / Identity Protection, Entra External ID custom IEF policies, Defender for Cloud, Monitor Smart Detection / Alert Rules (triggering), Activity Log, Notification Hubs, ACS Calling / SMS / Chat advanced features. Lifecycle admin and Microsoft-side enforcement are by nature cloud-only.
7. **API edge / data / BI orchestration** — API Management (all tiers), Functions Bindings (Azure-glue), Microsoft Fabric F-SKUs, Power BI Premium / Embedded, Purview, Synapse Dedicated SQL Pool, Stream Analytics, Mapping Data Flows visual surface. Orchestration / dashboarding — the Azure-managed plane *is* the value.
8. **Deprecated / end-of-life** — Azure CDN from Verizon (retired 2025), Azure CDN from Akamai (retiring 2025-Q4), Time Series Insights, Personalizer, Database for MariaDB (retired 2024-09), Azure Sphere (retiring 2027-09), Maps Creator. Should not be a target for new builds.

## Common vs niche within Tier 2

**Common Tier 2** — services most modern Azure apps touch at some point. Earn a yaml-registry slot or a primitive wrapper even when their config shape is non-trivial, because the audience is broad enough to justify it. Their "when to pick which" notes already live in the per-role-group sections above; deep-dive treatment is not repeated here:

- API Management (all tiers) — universal API surface
- Entra ID + Entra External ID — universal auth
- Front Door Standard — universal CDN + global LB + WAF
- Azure OpenAI On Your Data — RAG is mainstream in 2026
- Cosmos DB multi-region writes — anyone going multi-region
- Monitor Alert Rules / Activity Log / Defender for Cloud — universal supporting infra
- Static Web Apps — common in SPA-shop deployments
- Logic Apps Consumption (single workflow) — common workflow surface
- Microsoft Fabric F-SKUs — common analytics surface in Microsoft-aligned shops

**Truly-niche Tier 2** — the entries deep-dived below. Narrow-vertical, low-adoption, deprecated, or otherwise outside what supeux's expected audience touches by default.

## Per-niche-service paragraphs and practicality verdicts

For each entry: 2–3 sentences on **who reaches for it** + **trigger scenario** + **why a generic alternative doesn't suffice**, then a single **Practicality:** verdict.

### Edge / CDN / WAF — niche

#### Front Door Premium

Front Door Standard + bot manager + advanced WAF + private-link to backends. Reached when bot mitigation matters (auth endpoints, ticketing, login flooding) or when backends sit in private VNets without public endpoints. The $330/mo fixed floor means it's not the default.

**Practicality: `yaml-registry`** — config is a short list (origin groups, WAF rule mode, bot-manager toggle).

#### Application Gateway WAF custom rules

Bespoke per-app WAF rules layered on Application Gateway WAF v2. Reached when default Microsoft-managed rules generate false positives and you need exceptions per URL pattern or to write custom rate-limit rules.

**Practicality: `hand-tf`** — WAF custom rule bodies (match conditions + actions) are app-specific and verbose.

#### Static Web Apps preview environments

Free per-PR ephemeral preview environments tied to GitHub / Azure DevOps PRs. The runtime is the same as the production Static Web App; what's "T2" is the preview-environment lifecycle (auto-create on PR open, auto-destroy on close, custom domain per PR).

**Practicality: `yaml-registry`** — short shape: enable flag + branch pattern. Worth a `cloud_only:` slot.

### Multi-region coordination — niche

#### Cosmos DB multi-region writes (multi-master)

Active-active Cosmos across N enabled regions with last-writer-wins or custom JS merge procedures. Reached by globally distributed apps needing cross-region write latency (e.g., regulated industries with data-residency + multi-region availability). Multiplies RU cost by region count.

**Practicality: `yaml-registry`** — already covered by `kv: tier: global` per `supeux_abstraction_v2.md` §6 vocabulary; specify regions as a list.

#### Azure SQL geo-replication / failover groups

Async geo-replication of an Azure SQL Database to a secondary region with auto-failover. Reached by single-region apps needing DR target without cross-region writes.

**Practicality: `yaml-registry`** — short shape: secondary region name + auto-failover policy.

#### Service Bus Geo-Disaster Recovery pairing

Pair two Premium namespaces in different regions for metadata-only failover (entities replicate; messages don't until you use private-preview features). Reached by single-region apps needing DR for the messaging plane.

**Practicality: `hand-tf`** — namespace pairing + alias config + DNS cutover script per deployment; not enough audience.

#### Traffic Manager nested profiles

DNS-level global LB with nested profiles for hierarchical routing (e.g., performance-based across continents, then priority-based within continent). Reached only when Front Door's HTTP-level routing isn't enough (non-HTTP endpoints, game servers).

**Practicality: `hand-tf`** — endpoint definitions + routing method + nested-profile structure is networking-shaped.

### Managed ML / AI orchestration — niche

#### Azure OpenAI On Your Data

Bundled vector store (defaults to AI Search) + ingestion + retrieval pipeline for "I have docs, give me RAG over them" — analogue of Bedrock Knowledge Bases. Reached by teams that don't want to operate a vector store and embedding pipeline themselves; trades flexibility for managed-ness.

**Practicality: `hand-tf`** — vector store choice + data source connectors + chunking strategy + embedding model is a big, app-specific shape.

#### AI Foundry Agent Service

Tool-use orchestration over Foundry models: action groups, tool schemas, collaborator agents, prompt templates — analogue of Bedrock Agents. Reached by teams building agentic apps who want managed orchestration instead of writing the agent loop in code.

**Practicality: `hand-tf`** — agent + tool schemas + connection config is app-specific and lengthy.

#### Azure OpenAI Assistants API

Stateful conversation + tool use API surface tied to specific Azure OpenAI model deployments. Reached when an OpenAI-Assistants-shaped UX is desired without operating thread state yourself.

**Practicality: `hand-tf`** — assistant definitions + tool schemas + file IDs are per-app.

#### AI Content Safety

Input / output content filtering with category thresholds, denied terms, jailbreak detection, prompt-injection detection — analogue of Bedrock Guardrails. Reached when running user-facing LLM apps where content safety is a release blocker.

**Practicality: `yaml-registry`** — config is a short list (severity thresholds per category + denied-term lists + jailbreak-detection toggle). Fits a `cloud_only:` block cleanly.

#### Azure Bot Service

Hosted runtime for Bot Framework SDK bots with channel integration (Teams, Direct Line, Slack, Webchat). Reached by teams building Teams bots — almost no reason to use it otherwise in 2026 (LLM apps with custom webhooks beat it on flexibility).

**Practicality: `hand-tf`** — channel registrations + identity bindings are per-bot.

#### Azure ML Designer / Canvas (no-code surfaces)

Drag-drop pipeline builder over Azure ML. Reached by analysts and BAs in regulated industries who can't write Python.

**Practicality: `hand-tf`** — UI-driven; underlying workspace + compute config is admin-shaped.

#### Personalizer (deprecated)

Recommender-system-as-a-service. **Retired 2024-09** — Microsoft recommends moving to custom models on Azure ML.

**Practicality: `skip`** — retired; no new builds.

### IoT vertical — niche

#### IoT Central

SaaS IoT solution accelerator — pre-built dashboards, device groups, rules, jobs over IoT Hub. Reached by teams that want shrink-wrapped IoT solutions; mostly superseded by custom builds on IoT Hub + Container Apps + ADX for teams with engineering capacity.

**Practicality: `hand-tf`** — solution-template selection + customizations are per-deployment.

#### Azure IoT Operations

Microsoft's 2024+ converged edge platform: MQTT broker, OPC-UA connector, edge data flows, all on AKS Edge / Arc. Reached by industrial / manufacturing teams aligning on Arc / AKS Edge for OT-IT integration.

**Practicality: `hand-tf`** — extensive config (MQTT topics, OPC-UA tag mapping, data-flow pipelines) is industrial-deployment-specific.

#### Digital Twins

Twin graph + DTDL ontology + temporal-graph queries — domain modeling for "this pump connects to this tank." Reached by industrial digital-twin builders modeling physical asset topologies.

**Practicality: `hand-tf`** — twin models + relationship graphs are inherently large and bespoke.

#### Defender for IoT

Passive packet inspection on OT / industrial networks for anomaly detection (Modbus / Profinet / DNP3 / IEC-61850 protocols). Reached by manufacturing / utility / oil-and-gas security teams.

**Practicality: `hand-tf`** — sensor placement + protocol baselines + alert thresholds are deployment-specific.

#### IoT Hub Device Provisioning Service (DPS)

Zero-touch device enrollment via factory-flashed cert / TPM-based attestation. Reached at fleet scale (>1k devices) — smaller fleets do fine with manual provisioning via portal or script.

**Practicality: `yaml-registry`** — small shape: enrollment-group type + allocation policy + linked IoT Hub.

### Specialty storage variants — niche

#### Premium Block Blob

High-RPS object storage variant — sub-10ms p99, 7× pricier per GB than Standard Hot. Reached by ML training shuffle, analytics scratch, and other "lots of small reads per second" workloads. Analogue of S3 Express One Zone.

**Practicality: `yaml-registry`** — already covered by `bucket: variant: premium-block` per `supeux_abstraction_v2.md` §6.

#### ADLS Gen2 hierarchical namespace

Hot Blob with directory-namespace + ACL semantics — POSIX-shaped over object storage. Reached whenever Spark / Synapse / Databricks point at the storage.

**Practicality: `yaml-registry`** — short shape: enable HNS flag on bucket. Worth a `variant:` slot.

#### Azure NetApp Files

NetApp-specific managed file storage with snapshots, cross-region replication, dual-protocol SMB+NFS, Active Directory integration. Reached when the workload requires NetApp-specific features (SAP HANA, Oracle on Azure, HPC) or sub-ms p99 file IO at scale.

**Practicality: `hand-tf`** — capacity pool + volume + service-level + protocol config is bespoke per use-case.

#### Azure Elastic SAN

iSCSI SAN-shaped storage with multi-host attach + snapshots — analogue of EBS Multi-Attach. Reached by VMware / SQL clustering / shared-disk scenarios migrating from on-prem SANs.

**Practicality: `hand-tf`** — volume groups + iSCSI target config is networking + storage shaped.

#### Azure Container Storage

Persistent volume orchestrator for AKS / Container Apps that aggregates underlying storage (Premium SSD v2, Local NVMe, Elastic SAN) into Kubernetes-aware volumes. Reached by AKS workloads needing fast persistent volumes.

**Practicality: `hand-tf`** — storage-pool definitions + AKS extension config is K8s-specific.

#### Cosmos DB integrated cache (dedicated gateway)

In-front-of-Cosmos cache for hot read workloads — analogue of DAX. Reached when single-digit-ms isn't enough and you accept the per-gateway-node hourly cost.

**Practicality: `yaml-registry`** — small shape: gateway tier + node count + target container.

#### Cosmos DB Vector Search

Vector indexing on Cosmos containers (SQL API + MongoDB API). GA in 2025; native ANN over the same partitioned data. Reached for hybrid filter+vector queries from the same record (e.g., "find similar items where user_id = X").

**Practicality: `yaml-registry`** — small shape: vector dimension + similarity metric + index config. Worth a `cloud_only:` slot.

#### Cosmos DB serverless

Per-RU-consumed pricing with 5,000 RU/s ceiling. Reached for true bursty low-volume workloads. The ceiling makes it niche — most apps outgrow it.

**Practicality: `yaml-registry`** — already covered by `kv: capacity_mode: serverless`.

### Identity / lifecycle / observability / messaging lock-in — niche

(Common Tier 2 entries — Entra ID, Monitor Alert Rules, Defender for Cloud — are covered in their role-group sections above. The niche-only entries below.)

#### Entra ID Conditional Access policies

Risk-based access policies (e.g., require MFA from untrusted networks, block from specific countries, force compliant device). Reached by orgs with mature security postures; per-tenant config tied to specific user groups + applications.

**Practicality: `hand-tf`** — policy bodies (conditions + grant controls + session controls) are org-shaped, not app-shaped.

#### Entra ID PIM (Privileged Identity Management)

Just-in-time elevation for high-privilege role assignments with approval workflows. Reached by orgs implementing zero-standing-privilege for Azure subscription owners / Global Admins.

**Practicality: `skip`** — `thesis.md` explicitly lists "multi-account governance" as out of scope.

#### Entra External ID custom IEF policies

XML-based identity policies for advanced user journeys (custom claims, conditional MFA, federation with non-standard IDPs, custom UI). Reached by B2C apps with non-standard signup flows.

**Practicality: `hand-tf`** — custom IEF policy XML is heavyweight and per-app.

#### Application Insights Smart Detection

ML-based anomaly detection over App Insights telemetry — auto-detects request failure rate, dependency latency, exception rate anomalies. Reached when you want signals without writing alert rules.

**Practicality: `yaml-registry`** — small shape: enable flag + sensitivity per detector + action-group target.

#### Notification Hubs

Fan-out to APNs / FCM / Baidu / WNS push endpoints. Reached by apps with native mobile clients sending notifications. Locally there's nowhere for a push to go.

**Practicality: `yaml-registry`** — small shape: platform credentials (cert refs) + hub name + tier.

#### ACS SMS

Outbound SMS via Azure Communication Services. Reached for transactional SMS; marketing SMS goes to Twilio for better deliverability.

**Practicality: `hand-tf`** — number registrations + sender ID + opt-out compliance are account-level.

#### ACS Calling / Chat

PSTN + VoIP calling, in-app chat with media + presence. Reached by teams building Teams-like or telehealth-like products.

**Practicality: `hand-tf`** — call routing + identity bindings + media-stack config are app-specific.

### API edge / data / BI orchestration — niche

(API Management common tiers + Microsoft Fabric F-SKUs are covered as common Tier 2 in their role-group sections.)

#### API Management Premium v2 multi-region

Premium v2 multi-region deployment with auto-failover and active-active routing. Reached only by orgs needing globally distributed API surface with consistent developer-portal experience.

**Practicality: `hand-tf`** — region selection + routing policy + auth backend per region are deployment-specific.

#### Azure Functions Bindings (input/output bindings)

Declarative input/output bindings on Function triggers (e.g., automatically pull from Cosmos, write to Blob) — Azure-specific glue between Functions and other services. Reached for simple data-shuffle Functions where writing the SDK call yourself is over-engineering.

**Practicality: `yaml-registry`** — small shape: binding type + connection string ref + target. Worth covering inside the `service.shape: function` block.

#### Microsoft Purview Data Map / Catalog

Lineage tracking + classification + access policies over data sources (Synapse, ADLS, Power BI, SQL DB). Reached by data-platform teams running shared data lakes with multi-source provenance requirements.

**Practicality: `hand-tf`** — scan rules + classification rules + glossary terms are org-shaped.

#### Synapse Dedicated SQL Pool

Legacy MPP warehouse — being superseded by Fabric Data Warehouse for new builds. Survives for migration scenarios.

**Practicality: `hand-tf`** — distribution / partitioning / index / resource-class config is warehouse-specific.

#### Azure Stream Analytics

SQL-shaped streaming over Event Hubs / IoT Hub. Reached for declarative streaming where the alternative (Fabric Real-Time Intelligence / Databricks streaming) is overkill.

**Practicality: `hand-tf`** — query body is the value; per-app.

#### Data Factory Mapping Data Flows

Spark-backed visual ETL surface inside Data Factory. Reached by teams preferring visual ETL over PySpark code.

**Practicality: `hand-tf`** — flow definitions are pipeline-shaped, per-deployment.

#### Power BI Embedded

Power BI capacity SKUs (A1–A6) for embedding reports in your own SaaS UI. Reached by SaaS apps embedding BI in their customer-facing product.

**Practicality: `hand-tf`** — capacity SKU + workspace assignments + auth (RLS / embed tokens) are app-specific.

### Deprecated / end-of-life

#### Azure CDN from Verizon

**Retired 2025-Q1** — Microsoft recommends Front Door.

**Practicality: `skip`** — retired.

#### Azure CDN from Akamai

**Retiring 2025-Q4** — Microsoft recommends Front Door.

**Practicality: `skip`** — retiring.

#### Time Series Insights

**Retired 2025-03** — Microsoft recommends Azure Data Explorer or Fabric Real-Time Intelligence.

**Practicality: `skip`** — retired.

#### Azure Cognitive Services Personalizer

**Retired 2024-09** — Microsoft recommends custom recommender models on Azure ML.

**Practicality: `skip`** — retired.

#### Azure Database for MariaDB

**Retired 2024-09** — Microsoft recommends MySQL Flexible Server.

**Practicality: `skip`** — retired.

#### Azure Sphere

**Retiring 2027-09** — no successor product; Microsoft recommends migrating to other Azure IoT services.

**Practicality: `skip`** — retiring.

### Functions runtime corner cases

#### Azure Functions FlexConsumption

Per-second autoscale Functions hosting with VNet integration — strictly newer / better than v1 Consumption for most workloads. Listed here only because it's an Azure-specific runtime mode.

**Practicality: `yaml-registry`** — already covered as a `service.shape: function` `tier:` choice; small enable flag.

#### Azure Functions Premium pre-warmed instances

Always-warm instances on Premium plans to avoid cold starts. Reached when Premium's $200+/mo baseline is justified by cold-start sensitivity.

**Practicality: `yaml-registry`** — small shape: pre-warm count on a specific Function. Worth exposing as an `overrides:` knob on `service.shape: function`.

## Runtime story for niche Tier 2 services (recap)

Regardless of the per-service verdict above, the user's **runtime code path** for any niche Tier 2 service is unchanged: call the Azure SDK from inside the service container with `DefaultAzureCredential` (which auto-resolves via Managed Identity in cloud, CLI auth or service-principal env vars locally). This works identically whether the container runs locally (`az login` once, then `DefaultAzureCredential` picks up CLI creds) or on Container Apps / Functions / VMs (Managed Identity auto-injected by Azure). The verdict only determines whether supeux ships an opinionated yaml shortcut for the **provisioning** side, or sends the user to the v2 §2 escape hatch:

```yaml
resources:
  my_niche_thing:
    type: terraform-module
    source: ./modules/my_niche_thing
    inputs: { … }
```

with the resulting resource ID / connection string injected as an env var into services that `uses:` it. No new primitive is invented; this analysis only earmarks which Tier 2 services route through `cloud_only:` versus `terraform-module`.

---

# Cross-cutting patterns observed

Seven patterns recur across groups and matter for File 5's recommendation and `cloud_providers.md`'s divergence section:

1. **Most groups have a "serverless / consumption" variant that costs more per-unit but eliminates capacity planning.** Pattern: pick provisioned only when sustained utilization > ~50% of provisioned capacity. Examples: Functions Consumption vs Premium; Container Apps Consumption vs Dedicated; SQL Database Serverless vs Provisioned; Cosmos Serverless vs Provisioned RU/s.
2. **A handful of services dominate by cost-sensitivity:** Functions, Blob, Cosmos DB, Service Bus + Storage Queue, Monitor Logs, VMs, SQL DB / Postgres Flexible Server. These are also the most stable APIs — the obvious targets for cloud↔local abstraction. Same pattern as AWS (Lambda, S3, DynamoDB, SQS, etc.).
3. **Entra ID, Microsoft Fabric, Logic Apps, API Management, Azure ML, Azure OpenAI have no realistic local-container parity.** They're either Microsoft-specific protocols or proprietary models. Local emulation is best-effort at best. See **§Tier 2 deep-dive** above for the full sub-grouping, common-vs-niche split, and per-niche-service practicality verdict.
4. **Many "managed" services are wire-compatible with OSS engines**: Postgres on Flexible Server, MySQL on Flexible Server, Cosmos MongoDB API (with caveats), Redis on Cache for Redis, Kafka via Event Hubs' Kafka protocol surface, AI Search Lucene-shape, Spark on Synapse / Databricks / HDInsight, Hadoop on HDInsight. This is the cheap abstraction zone.
5. **The cost shape predicts how supeux's "switch to cloud" yaml should behave**: per-request services (Functions Consumption, Cosmos serverless, Storage Queue, Service Bus Standard) are friendly to cloud-by-default; per-hour services (Postgres FS provisioned, Cache for Redis, Container Apps Dedicated, AI Search S1+) are unfriendly to "spin up for CI" — supeux needs an *off-by-default* policy for those.
6. **The Tier 2 niche set bifurcates by provisioning shape, not runtime difficulty.** All Tier 2 services are runtime-solved by Managed Identity + Azure SDK from inside the user's container (works identically local / Container Apps / Functions). The only design question per service is whether its HCL fits a short `cloud_only:` yaml shortcut (`yaml-registry`) or is better left to the `terraform-module` escape hatch (`hand-tf`) — or omitted entirely (`skip`). See **§Tier 2 deep-dive** above.
7. **Azure-specific cross-cutting concerns absent from AWS framework**:
   - **Hybrid / Arc**. Azure Arc projects Azure governance + identity + Defender onto on-prem and other-cloud resources. supeux doesn't target hybrid clouds in v1 but should not assume the user's deploy targets are *only* Azure-managed VMs / Container Apps.
   - **Microsoft Fabric capacity (CU-based)** is genuinely different from per-resource billing — pay for an aggregate capacity, consume across many workloads. The IR doesn't model this today; future work if Fabric becomes a primary user target.
   - **Managed Identity / `DefaultAzureCredential`** is significantly cleaner than AWS's IAM role assumption story for local dev (no `~/.aws/credentials` files, just `az login`). supeux's local↔cloud auth story should lean into `DefaultAzureCredential` as the default pattern for Azure targets.

These feed directly into File 5's PoC scope recommendation and into `cloud_providers.md`'s "Implications for the IR schema" section.
