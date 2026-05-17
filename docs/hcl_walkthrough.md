# HCL primer + worked supeux emit sample

> Companion to [poc_yaml_spec.md](poc_yaml_spec.md) (PoC) / [ir.md](ir.md) (full IR). Designed for a yaml-fluent reader who has never seen HCL — learn the syntax by reading what `supeux compile` would actually emit for the `smart-query` worked example. Two target projections shown: `prod-split` (HCL), `dev-monolith` (docker-compose).
>
> Annotations are the point of this document. The full files are illustrative; treat each comment as the load-bearing content and the surrounding HCL as the example.

---

## 1. HCL syntax primer

HCL is what Terraform / OpenTofu consumes. Mental model: yaml is data; HCL is data + references + a tiny expression language. Same nested-key feel; different punctuation.

**Blocks** — top-level units. Shape: `type "label1" "label2" { ... }`. For `resource` blocks, label1 is the provider-defined type, label2 is your local name.

```hcl
resource "aws_s3_bucket" "uploads" {       # type=aws_s3_bucket, local name=uploads
  bucket = "smart-query-uploads-prod"      # key = value (note: = not :)
  tags   = { Env = "prod" }                # nested object literal
}
```

**Attributes** — `key = expression` inside a block. Strings in `"..."`; numbers, booleans, lists `[...]`, objects `{...}` look like JSON.

**References are first-class** — there are no string-interpolated IDs. Define `aws_s3_bucket.uploads`, and anywhere else `aws_s3_bucket.uploads.arn` reads that resource's ARN attribute, automatically wiring a dependency edge in Terraform's graph.

```hcl
resource "aws_iam_role_policy" "api_uploads" {
  role   = aws_iam_role.api.id                            # reference, not a string
  policy = data.aws_iam_policy_document.uploads.json
}
```

String interpolation when needed: `"arn:aws:s3:::${aws_s3_bucket.uploads.id}/*"`.

**Heredocs** — multi-line strings (JSON policies, user-data scripts). `<<-` strips leading whitespace:

```hcl
policy = <<-EOF
  { "Statement": [{"Effect": "Allow", "Action": "s3:GetObject"}] }
EOF
```

**Variables** — inputs. `variable "x" { ... }` declares, `var.x` reads:

```hcl
variable "region" { type = string; default = "us-east-1" }
provider "aws" { region = var.region }
```

**`for_each`** — loops a block over a map. `each.key` and `each.value` reference the current pair:

```hcl
resource "aws_sqs_queue" "q" {
  for_each = { jobs = "standard", events = "fifo" }
  name     = "${each.key}-prod"
}
# Reference one: aws_sqs_queue.q["jobs"].arn
```

**`dynamic`** — generates repeated nested blocks (e.g. multiple `ingress {}` inside one SG):

```hcl
dynamic "ingress" {
  for_each = var.ports
  content {
    from_port = ingress.value
    to_port   = ingress.value
    protocol  = "tcp"
  }
}
```

**Modules** — reusable HCL bundles (`module "x" { source = "..."; ... }`). Supeux's v1 emit is flat files (no `module {}` blocks) for legibility.

`jsonencode(...)` and `tomap(...)` are common helpers — they convert HCL values to JSON strings / typed maps for fields that demand them (IAM policies, Lambda environment).

That's enough to read everything below.

---

## 2. Source yaml — recap

Reproduced from [poc_yaml_spec.md "Worked example"](poc_yaml_spec.md#worked-example--smart-query). Two crates: `./api` (entry; the HTTP-handling binary) and `./embedder` (a sub-crate that hosts the `Embedder` SDK seam's provider). The entry's Dockerfile builds the monolith binary; the seam's Dockerfile is a focused build used only when the seam is split per target.

```yaml
name: smart-query

resources:
  vector_index: { type: search, composition: cloud-managed }
  chat_llm:     { type: llm,    composition: cloud-managed, task: chat }
  embed_llm:    { type: llm,    composition: cloud-managed, task: embedding }

secrets:
  gemini_key:   { from: ssm, path: /smart-query/gemini }

entries:
  api:
    path:       ./api
    dockerfile: ./api/Dockerfile        # builds the monolith binary
    triggers:
      - http: { path: /query, port: 8080, public: true }
    uses: [vector_index, chat_llm, gemini_key]

seams:
  Embedder:
    path:       ./embedder              # sub-crate that hosts provide(Embedder, ...)
    dockerfile: ./embedder/Dockerfile   # focused build, used only when split
    uses:       [embed_llm]

targets:
  prod-split:
    runtime: aws
    default_composition: cloud-managed
    entries: { api: lambda }
    seams:   { Embedder: lambda }       # Embedder spawns its own Lambda

  dev-monolith:
    runtime: docker-compose
    default_composition: oss-local
    entries: { api: container }
    # seams: omitted → Embedder is inproc; provider compiled into api's binary
```

**Phase 2 source scan** cross-checks: `./embedder/src/lib.rs` must contain `provide(Embedder, ...)` (matching yaml's `seams.Embedder.path`); `./api/src/lib.rs` contains `client::<Embedder>(...)`. Phase 4 uses target's seam decisions to compute `SUPEUX_RPC_PEERS` per deploy unit.

**Per-target deploy units**:
- `prod-split` → 2 Lambdas (`smart-query-api` from `./api/Dockerfile`, `smart-query-embedder` from `./embedder/Dockerfile`).
- `dev-monolith` → 1 compose service (`api` from `./api/Dockerfile`; embedder code runs inproc inside).

---

## 3. Worked HCL for `prod-split`

Files supeux emits into `infra/prod-split/generated/`. Driven by the yaml above.

### 3a. `main.tf`

```hcl
# === Generated by supeux compile --target=prod-split. DO NOT EDIT. ===
# Hand-corrections belong in sibling .tf files (infra/prod-split/*.tf).

# ----- resources.vector_index (search / opensearch-serverless) -----
# composition: cloud-managed → OpenSearch Serverless collection.
resource "aws_opensearchserverless_security_policy" "vector_index_encryption" {
  name = "smart-query-vector-index-enc"
  type = "encryption"
  policy = jsonencode({
    Rules        = [{ ResourceType = "collection", Resource = ["collection/smart-query-vector-index"] }]
    AWSOwnedKey  = true
  })
}

resource "aws_opensearchserverless_security_policy" "vector_index_network" {
  name = "smart-query-vector-index-net"
  type = "network"
  policy = jsonencode([{
    Rules           = [{ ResourceType = "collection", Resource = ["collection/smart-query-vector-index"] }]
    AllowFromPublic = false
    SourceVPCEs     = [aws_opensearchserverless_vpc_endpoint.vector_index.id]
  }])
}

resource "aws_opensearchserverless_collection" "vector_index" {
  name = "smart-query-vector-index"
  type = "VECTORSEARCH"                                # k-NN + BM25 in one collection
  depends_on = [
    aws_opensearchserverless_security_policy.vector_index_encryption,
    aws_opensearchserverless_security_policy.vector_index_network,
  ]
}

# ----- resources.chat_llm + resources.embed_llm (llm / Bedrock) -----
# Tier 1 hard pair: NO infra resources to provision for the cloud side.
# Bedrock is a managed service that supeux just grants IAM access to.
# Local-side (dev-monolith) runs Ollama / FastEmbed as compose services — see §4.
# Manifest patching (Cargo.toml in build context per container) selects the bedrock
# provider feature for rig-core — see container blocks below.

# ----- secrets.gemini_key (ssm) -----
# Composition: cloud-managed → SSM Parameter Store. Value-set is user-managed
# (out-of-band aws ssm put-parameter); supeux only emits the IAM grant + env-var injection.
# No HCL resource emitted for the value itself.

# ----- ECR repos — one per container (each has its own Dockerfile + patched manifest) -----
resource "aws_ecr_repository" "api" {
  name                 = "smart-query-api"
  image_tag_mutability = "IMMUTABLE"
  image_scanning_configuration { scan_on_push = true }
}

resource "aws_ecr_repository" "embedder" {
  name                 = "smart-query-embedder"
  image_tag_mutability = "IMMUTABLE"
  image_scanning_configuration { scan_on_push = true }
}
# (Tag values are git-sha-based; supeux populates at build time. Compile-only step
#  doesn't pin a tag — that's filled by `supeux build` + `supeux up`.)

# ----- CloudWatch log groups (one per container) -----
# Lambda log groups follow /aws/lambda/<func> convention but supeux emits them
# explicitly so retention is yaml-controlled.
resource "aws_cloudwatch_log_group" "api"      { name = "/aws/lambda/smart-query-api";      retention_in_days = 14 }
resource "aws_cloudwatch_log_group" "embedder" { name = "/aws/lambda/smart-query-embedder"; retention_in_days = 14 }

# ----- containers.api (shape: function → Lambda, runtime: aws → on=lambda) -----
resource "aws_lambda_function" "api" {
  function_name = "smart-query-api"
  package_type  = "Image"
  image_uri     = "${aws_ecr_repository.api.repository_url}:latest"
  role          = aws_iam_role.api.arn
  timeout       = 30
  memory_size   = 1024
  architectures = ["arm64"]
  environment {
    variables = {
      # data-plane resource env vars (uses: [vector_index, chat_llm, gemini_key]):
      OPENSEARCH_URL                = aws_opensearchserverless_collection.vector_index.collection_endpoint
      LLM_BACKEND                   = "bedrock"        # chat_llm composition: cloud-managed → bedrock
      LLM_MODEL                     = "anthropic.claude-opus-4-7-20260416-v1:0"
      AWS_REGION                    = var.region
      GEMINI_KEY_SSM_PATH           = "/smart-query/gemini"
      # control-plane RPC env vars (Embedder provider = embedder container; mode: lambda):
      SUPEUX_RPC_SELF               = "api"
      SUPEUX_RPC_PEERS              = jsonencode({
        Embedder = {
          mode         = "lambda"
          function_url = aws_lambda_function_url.embedder.function_url
        }
      })
      SUPEUX_RPC_SHARED_SECRET      = random_password.rpc_secret.result
    }
  }
}

# Public HTTP entry for api (triggers.http.public: true) — Function URL with no auth.
resource "aws_lambda_function_url" "api" {
  function_name      = aws_lambda_function.api.function_name
  authorization_type = "NONE"                          # public: true
  cors { allow_origins = ["*"] }
}

# ----- containers.embedder (shape: function → Lambda) -----
resource "aws_lambda_function" "embedder" {
  function_name = "smart-query-embedder"
  package_type  = "Image"
  image_uri     = "${aws_ecr_repository.embedder.repository_url}:latest"
  role          = aws_iam_role.embedder.arn
  timeout       = 30
  memory_size   = 2048                                 # heavier (embedding model)
  architectures = ["arm64"]
  environment {
    variables = {
      LLM_BACKEND               = "bedrock"            # embed_llm composition: cloud-managed
      LLM_MODEL                 = "amazon.titan-embed-text-v2:0"
      AWS_REGION                = var.region
      SUPEUX_RPC_SELF           = "embedder"
      SUPEUX_RPC_PEERS          = jsonencode({})       # no outbound peers (provider only)
      SUPEUX_RPC_SHARED_SECRET  = random_password.rpc_secret.result
    }
  }
}

# Internal-only Function URL for embedder — AWS_IAM auth so only api's role can invoke.
resource "aws_lambda_function_url" "embedder" {
  function_name      = aws_lambda_function.embedder.function_name
  authorization_type = "AWS_IAM"                       # private; SigV4 from api
}

# Shared RPC secret — generated once per deploy, injected into both Lambdas.
resource "random_password" "rpc_secret" {
  length  = 48
  special = false
}
```

**What's notably different from the old IR's HCL**:
- **No `aws_ecs_cluster` / `aws_ecs_task_definition`** — both containers are Lambda (`shape: function`). Fargate would only appear if a container had `shape: long_running`.
- **No `aws_ecr_repository` per *bundle***; one per *container*. Each container has its own Dockerfile + patched manifest → its own image.
- **No `--module=api` dispatcher CLI arg.** Each container is its own binary (`./containers/api_only/main.rs` etc.) that imports the relevant library crates. No CLI dispatch needed.
- **`SUPEUX_RPC_PEERS` is keyed by interface name** (`Embedder`), not peer container name. Single provider per interface (PoC constraint).
- **No `SUPEUX_RPC_BUNDLE` env var** — collapsed into `SUPEUX_RPC_SELF` (the container name).

### 3b. `network.tf`

Conservative defaults per [ir.md §6a](ir.md#6a-cloud-projection-hcl): one VPC, two AZs, public + private `/24` subnets, one NAT. Lambdas in private subnets to reach OpenSearch via VPC endpoint.

```hcl
# === Generated by supeux compile. ===
data "aws_availability_zones" "available" { state = "available" }

resource "aws_vpc" "main" {
  cidr_block           = "10.0.0.0/16"
  enable_dns_hostnames = true
  enable_dns_support   = true
  tags = { Name = "smart-query-prod-split" }
}

resource "aws_subnet" "public" {
  for_each                = { for i, az in slice(data.aws_availability_zones.available.names, 0, 2) : i => az }
  vpc_id                  = aws_vpc.main.id
  cidr_block              = "10.0.${each.key}.0/24"
  availability_zone       = each.value
  map_public_ip_on_launch = true
}

resource "aws_subnet" "private" {
  for_each          = { for i, az in slice(data.aws_availability_zones.available.names, 0, 2) : i => az }
  vpc_id            = aws_vpc.main.id
  cidr_block        = "10.0.${each.key + 10}.0/24"
  availability_zone = each.value
}

resource "aws_internet_gateway" "main" { vpc_id = aws_vpc.main.id }

resource "aws_eip" "nat" { domain = "vpc" }
resource "aws_nat_gateway" "main" {
  allocation_id = aws_eip.nat.id
  subnet_id     = aws_subnet.public[0].id
}

# Route tables: public → IGW; private → NAT.
resource "aws_route_table" "public"  { vpc_id = aws_vpc.main.id }
resource "aws_route_table" "private" { vpc_id = aws_vpc.main.id }

resource "aws_route" "public_egress"  { route_table_id = aws_route_table.public.id;  destination_cidr_block = "0.0.0.0/0"; gateway_id     = aws_internet_gateway.main.id }
resource "aws_route" "private_egress" { route_table_id = aws_route_table.private.id; destination_cidr_block = "0.0.0.0/0"; nat_gateway_id = aws_nat_gateway.main.id }

resource "aws_route_table_association" "public"  { for_each = aws_subnet.public;  subnet_id = each.value.id; route_table_id = aws_route_table.public.id }
resource "aws_route_table_association" "private" { for_each = aws_subnet.private; subnet_id = each.value.id; route_table_id = aws_route_table.private.id }

# OpenSearch Serverless VPC endpoint — keeps vector_index reachable from Lambda private subnets without public egress.
resource "aws_opensearchserverless_vpc_endpoint" "vector_index" {
  name       = "smart-query-vector-index-vpce"
  vpc_id     = aws_vpc.main.id
  subnet_ids = [for s in aws_subnet.private : s.id]
}

# Lambda VPC config so the api + embedder Lambdas land in private subnets.
# (Applied via aws_lambda_function.vpc_config block in main.tf — omitted above for brevity.)
```

### 3c. `iam.tf`

```hcl
# === Generated by supeux compile. ===

# ----- Execution roles (Lambda boilerplate; one per container) -----
resource "aws_iam_role" "api" {
  name = "smart-query-api"
  assume_role_policy = jsonencode({
    Version = "2012-10-17"
    Statement = [{ Effect = "Allow", Principal = { Service = "lambda.amazonaws.com" }, Action = "sts:AssumeRole" }]
  })
}

resource "aws_iam_role" "embedder" {
  name = "smart-query-embedder"
  assume_role_policy = jsonencode({
    Version = "2012-10-17"
    Statement = [{ Effect = "Allow", Principal = { Service = "lambda.amazonaws.com" }, Action = "sts:AssumeRole" }]
  })
}

# Both roles get the Lambda basic execution managed policy (CloudWatch Logs write).
resource "aws_iam_role_policy_attachment" "api_basic" {
  role       = aws_iam_role.api.name
  policy_arn = "arn:aws:iam::aws:policy/service-role/AWSLambdaBasicExecutionRole"
}
resource "aws_iam_role_policy_attachment" "embedder_basic" {
  role       = aws_iam_role.embedder.name
  policy_arn = "arn:aws:iam::aws:policy/service-role/AWSLambdaBasicExecutionRole"
}

# VPC ENI permission (because Lambdas are in private subnets).
resource "aws_iam_role_policy_attachment" "api_vpc"      { role = aws_iam_role.api.name;      policy_arn = "arn:aws:iam::aws:policy/service-role/AWSLambdaVPCAccessExecutionRole" }
resource "aws_iam_role_policy_attachment" "embedder_vpc" { role = aws_iam_role.embedder.name; policy_arn = "arn:aws:iam::aws:policy/service-role/AWSLambdaVPCAccessExecutionRole" }

# ----- api role: derived from container.api.uses = [vector_index, chat_llm, gemini_key] -----
data "aws_iam_policy_document" "api" {
  # vector_index (search / OpenSearch Serverless)
  statement {
    effect    = "Allow"
    actions   = ["aoss:APIAccessAll"]
    resources = [aws_opensearchserverless_collection.vector_index.arn]
  }
  # chat_llm (llm / Bedrock InvokeModel for the specific model id)
  statement {
    effect    = "Allow"
    actions   = ["bedrock:InvokeModel", "bedrock:InvokeModelWithResponseStream"]
    resources = ["arn:aws:bedrock:${var.region}::foundation-model/anthropic.claude-opus-4-7-20260416-v1:0"]
  }
  # gemini_key (ssm GetParameter, KMS Decrypt if SecureString)
  statement {
    effect    = "Allow"
    actions   = ["ssm:GetParameter"]
    resources = ["arn:aws:ssm:${var.region}:${data.aws_caller_identity.current.account_id}:parameter/smart-query/gemini"]
  }
  # control-plane: api calls Embedder → invoke embedder's Function URL
  statement {
    effect    = "Allow"
    actions   = ["lambda:InvokeFunctionUrl"]
    resources = [aws_lambda_function.embedder.arn]
  }
}

resource "aws_iam_role_policy" "api" {
  role   = aws_iam_role.api.id
  policy = data.aws_iam_policy_document.api.json
}

# ----- embedder role: derived from container.embedder.uses = [embed_llm] -----
data "aws_iam_policy_document" "embedder" {
  statement {
    effect    = "Allow"
    actions   = ["bedrock:InvokeModel"]
    resources = ["arn:aws:bedrock:${var.region}::foundation-model/amazon.titan-embed-text-v2:0"]
  }
}

resource "aws_iam_role_policy" "embedder" {
  role   = aws_iam_role.embedder.id
  policy = data.aws_iam_policy_document.embedder.json
}

data "aws_caller_identity" "current" {}
```

**IAM derivation rules** in the new model:
- Each container's role gets policy statements for everything in its `uses:` list. Resource-type → action mapping is supeux-owned (e.g. `search` → `aoss:APIAccessAll`; `llm` → `bedrock:InvokeModel`).
- For control-plane RPC: scan finds `client::<Interface>` in container C → C's role gets `lambda:InvokeFunctionUrl` (or the appropriate cross-bundle network/IAM grant) on the container that provides `Interface`.
- No more bundle-vs-module distinction; everything is per-container.

---

## 4. Sample compose for `dev-monolith`

Files supeux emits into `infra/dev-monolith/generated/`. Driven by the `dev-monolith` target in the yaml above. Single container `monolith` packs both `./api` and `./embedder` source dirs.

```yaml
# === Generated by supeux compile --target=dev-monolith. DO NOT EDIT. ===
# Hand-corrections belong in sibling .yaml files merged at compose-up time.

version: "3.9"
name: smart-query-dev-monolith

services:

  # ----- vector_index (composition: oss-local → opensearch container) -----
  opensearch:
    image: opensearchproject/opensearch:2
    environment:
      - discovery.type=single-node
      - "OPENSEARCH_INITIAL_ADMIN_PASSWORD=dev-only-Password!1"
      - DISABLE_SECURITY_PLUGIN=true                   # dev-only
    ports:
      - "127.0.0.1:9200:9200"
    healthcheck:
      test: ["CMD", "curl", "-f", "http://localhost:9200/_cluster/health"]
      interval: 5s
      retries: 12

  # ----- chat_llm + embed_llm (composition: oss-local → ollama for chat; embed_llm uses local FastEmbed lib in-container) -----
  ollama:
    image: ollama/ollama:latest
    ports:
      - "127.0.0.1:11434:11434"
    volumes:
      - ollama_models:/root/.ollama
    # Models pulled on first request; supeux can optionally pre-pull via a one-shot sidecar.

  # ----- container `monolith` -----
  # Both api and embedder source dirs in one image. SUPEUX_RPC_PEERS routes Embedder
  # calls inproc (no HTTP) because provider and caller share the same process.
  monolith:
    build:
      context: .                                       # repo root — Dockerfile pulls from ./containers/monolith, ./api, ./embedder
      dockerfile: ./containers/monolith/Dockerfile
    ports:
      - "127.0.0.1:8080:8080"                          # api triggers.http: { port: 8080, public: true }
    environment:
      # data-plane resource env vars:
      OPENSEARCH_URL: "http://opensearch:9200"        # vector_index → local opensearch
      LLM_BACKEND: "ollama"                            # chat_llm composition: oss-local
      LLM_MODEL: "llama3.1"
      OLLAMA_URL: "http://ollama:11434"
      EMBED_BACKEND: "fastembed"                       # embed_llm composition: oss-local → in-process FastEmbed
      GEMINI_KEY: "${GEMINI_KEY:-dev-noop}"            # secrets.gemini_key composition: env (no LocalStack needed)
      # control-plane RPC env vars (Embedder provider = same container; mode: inproc):
      SUPEUX_RPC_SELF: "monolith"
      SUPEUX_RPC_PEERS: |
        { "Embedder": { "mode": "inproc" } }
      SUPEUX_RPC_SHARED_SECRET: "dev-only"
    depends_on:
      opensearch:
        condition: service_healthy
      ollama:
        condition: service_started

volumes:
  ollama_models:
```

**`dev-split` (not shown) would emit two compose services** (`api` and `embedder`) instead of one `monolith`, with the api's `SUPEUX_RPC_PEERS` set to `{"Embedder": {"mode": "http", "url": "http://embedder:8080"}}` — standard compose service-name hostname.

---

## 5. What's deferred from PoC

- **`hybrid-debug` target** (mixed composition — local processes pointing at real cloud resources): valid in the full IR, dropped from the PoC schema. Reintroduces with the `composition: by-id` field at v1.
- **Multi-language monolith** (TS api + Rust embedder in one container): heavyweight, risky per [ir.md §7 risk #1](ir.md#L362). The worked example sticks to same-language Rust for the monolith case.
- **Custom networking** (`network: { cidr, az_count }` yaml fields): hardcoded defaults shown here; user-override is a v1 feature.

---

## 6. Cross-references

- Source yaml shape and worked example: [poc_yaml_spec.md](poc_yaml_spec.md).
- RPC SDK control-plane (`@interface` / `provide` / `client`, env-var contract, dispatch modes): [poc_rpc_sdk.md](poc_rpc_sdk.md).
- Resource catalog (10 PoC basic groups, what supeux generates per group, per-language code patterns): [poc_groups_to_code.md](poc_groups_to_code.md).
- Full IR (canonical reference, retains the original Module + Bundle two-layer structure for v1+): [ir.md](ir.md).
- Cloud-projection mapping audit (~30 gaps with closure): [ir.md §6](ir.md#6-mapping-unambiguity-audit).
