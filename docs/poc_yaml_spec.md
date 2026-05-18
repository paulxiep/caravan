# PoC yaml spec — entries + seams + per-target dispatch

> Restructured to a "seam-first" model: the SDK call sites (interfaces) are the atomic unit of caravan's vocabulary. Yaml declares the user's entry points + the seams that exist + the per-target dispatch decision for each seam. Containers are *derived* from those decisions, not declared.
>
> Read order: [thesis.md](thesis.md) → [poc_rpc_sdk.md](poc_rpc_sdk.md) → [poc_groups_to_code.md](poc_groups_to_code.md) → this file.

## Model — first principles

If caravan didn't exist, user code uses direct function calls; the whole app compiles into one binary; one container; monolith. That's the baseline.

Caravan's power is that the user can wrap any internal function call site in the SDK (`@interface` / `provide(...)` / `client::<X>()` — see [poc_rpc_sdk.md](poc_rpc_sdk.md)) and that call site becomes a **seam**: a candidate split point. Per seam, per target, yaml decides:

- `inproc` — provider stays in the entry's binary. No new deploy unit.
- `container` — provider becomes its own container (compose service in dev; Fargate in cloud).
- `lambda` — provider becomes its own Lambda function. Cloud only.

So the user picks the deploy topology by writing yaml decisions per seam per target. Same source, different lives.

**Two yaml top-level constructs map to deploy units**:

| Construct | What it is | Has its own |
|---|---|---|
| **entry** | A user-defined deploy root: an HTTP endpoint, a queue worker, a cron job. The user writes a Dockerfile for the monolith case (entry's binary + all the code its workspace pulls in). | Source path, Dockerfile, triggers, resource-usage list |
| **seam** | An `@interface` declared in code. *May* be split off into its own deploy unit per target; otherwise stays inproc inside the entry. | Source path (sub-crate), Dockerfile (focused build), resource-usage list |

When a seam stays `inproc`, no new deploy unit. When a seam is `container` or `lambda`, caravan builds the seam's focused Dockerfile against its sub-crate and emits a separate deploy unit.

## Scope: one caravan.yaml = one VPC

One repo, one caravan.yaml, one VPC. All entries + all split-off seams share that network boundary. Multi-VPC apps require multiple yamls. Caravan does not coordinate across yamls.

## What the yaml specifies

| Block | What it declares |
|---|---|
| `name`, `default_target` | Project name and the target used when none is specified. |
| `resources:` | External cloud resources (one of the 10 [basic groups](poc_groups_to_code.md)). |
| `secrets:` | Credentials. |
| `entries:` | Root deploy units. Per entry: source path, Dockerfile, triggers, what resources/secrets it uses. |
| `seams:` | SDK seams found (or to be found) in code. Per seam: source path of the sub-crate that hosts the provider, Dockerfile for the focused build, what resources/secrets the provider uses. |
| `targets:` | Per target: `runtime` + `composition` + per-entry deploy choice + per-seam dispatch decision. |

## Schema

```yaml
name: <string>
default_target: <target-name>

resources:
  <resource-name>:
    type: bucket | kv | queue | db.sql | cache | stream | search | llm
    composition: oss-local | cloud-managed
    # group-specific extension fields (see poc_groups_to_code.md):
    #   bucket   : class:, variant:
    #   kv       : capacity_mode:, primary_key:
    #   queue    : kind: (standard|fifo)
    #   db.sql   : engine:, version:, tier:, extensions:
    #   cache    : engine: (redis|valkey|memcached), kind:
    #   stream   : kind: (kinesis|msk-serverless)
    #   search   : kind:
    #   llm      : task: (chat|embedding|rerank), provider:

secrets:
  <secret-name>:
    from: ssm | secrets-manager | env
    path: <string>

entries:
  <entry-name>:
    path:       <path>                                 # workspace root / main crate dir
    dockerfile: <path>                                 # required; user-owned; builds the monolith binary
    triggers:
      - http:   { path: <string>, port: <int>, public?: <bool> }
      - queue:  { from: <queue-resource-name> }
      - cron:   { schedule: <cron-expr>, timezone?: <tz> }
      - stream: { from: <stream-resource-name> }
    uses:       [<resource-or-secret-name>...]         # data-plane only (resources + secrets)

seams:                                                 # optional; caravan can also discover by scanning entries' source
  <seam-name>:                                         # name must match `@interface <Name>` in code
    path:       <path>                                 # sub-crate that hosts the provider
    dockerfile: <path>                                 # focused build, used only when this seam is split
    uses:       [<resource-or-secret-name>...]         # what the provider's code touches

targets:
  <target-name>:
    runtime:             docker-compose | aws
    default_composition: oss-local | cloud-managed
    region?:             <aws-region>                  # default from $AWS_REGION
    composition?:                                      # per-resource override
      <resource-name>: { mode: oss-local | cloud-managed }
    entries:                                           # per-entry deploy choice
      <entry-name>: lambda | container | batch
    seams?:                                            # per-seam dispatch; default inproc if unmentioned
      <seam-name>: inproc | container | lambda
```

## What caravan derives (instead of asking)

| Inferred | From |
|---|---|
| Each entry's language | Detect by manifest presence in `entries.<name>.path`: `Cargo.toml` → rust, `pyproject.toml`/`requirements.txt` → python, `package.json` → typescript, `go.mod` → go. Phase-2 error on coexistence or absence. |
| Each seam's provider location | If `seams:` block exists, declared. If absent, caravan scans each entry's source + reachable deps for `provide(X, ...)` calls and infers (X's sub-crate = the file's containing package). Mismatch between yaml and code = phase-2 error. |
| Inter-process RPC peer table | Per target: for every seam whose target decision is `container` or `lambda`, mark that seam as external in all entries' `CARAVAN_RPC_PEERS`. Default = `inproc` if unmentioned. |
| Entry's `on:` deployment mapping | From `entries.<name>` value × `target.runtime`: `lambda × aws → lambda`; `container × aws → fargate`; `container × docker-compose → compose service`; `batch × aws → batch`. |
| Region | `$AWS_REGION` / `$AWS_DEFAULT_REGION` env var; explicit yaml value wins. |

## Dockerfile ownership

**Caravan does NOT generate Dockerfiles.** The user provides one per buildable unit:
- Per **entry**: monolith Dockerfile that builds the entry's full binary (workspace / dep graph included). Always used.
- Per **seam**: focused Dockerfile that builds just the sub-crate's provider binary. Used only in targets where this seam is split.

Algorithmic Dockerfile generation is rejected: too much per-language variance (base images, build steps, CGO flags, GPU runtimes, custom toolchains). User's Dockerfile is the contract surface.

The user's Dockerfile contract:
- `COPY` the manifest (`Cargo.toml` / `requirements.txt` / `package.json` / `go.mod`) from the build context root.
- Don't inline dep additions (`RUN cargo add ...` / `RUN pip install <pkg>`); caravan's patched manifest is the source of truth.
- Multi-stage builds are fine; the manifest goes into whichever stage installs deps.

## Manifest patching

Caravan patches the manifest in each build context per target with two categories of caravan-managed deps:

1. **`caravan-rpc-<lang>` SDK** (always; user doesn't add it themselves).
2. **Tier-1 hard-pair provider selection** for the `llm` group: which `rig-core` Cargo feature, `litellm[...]` extra, `@ai-sdk/...` npm peer, or Go build tag is included based on the resource's composition (see [poc_groups_to_code.md §10](poc_groups_to_code.md#10-llm)).

Per-language mechanism:

| Language | Mechanism | Example |
|---|---|---|
| Rust | Patch `[dependencies]` table, modify features | adds `caravan-rpc = "1.0"`; cloud llm: `rig-core = { features = ["bedrock"] }` |
| Python | Append/modify `requirements.txt` lines | adds `caravan-rpc==1.0`; cloud llm: `litellm[bedrock]>=1.0` |
| TypeScript | Merge into `package.json` `dependencies` | adds `"@caravan/rpc": "^1.0"`; cloud llm: `"@ai-sdk/amazon-bedrock": "^0.x"` |
| Go | Append `require` + `// +build` tagged source | adds `github.com/<org>/caravan-rpc-go` |

User's on-disk manifest is untouched; the patched copy lives only in the per-target build context.

## Worked example — `smart-query`

User code (one crate; everything in `./api`, the Embedder sub-crate also exists at `./embedder` for the split case):

```
smart-query/
├── caravan.yaml
├── Cargo.toml                       ← workspace: members = ["api", "embedder"]
├── api/
│   ├── Cargo.toml                   ← depends on embedder (path-dep)
│   ├── Dockerfile                   ← builds the monolith binary
│   └── src/                         ← HTTP handler + client::<Embedder>() + (when monolith) provide(Embedder, ...)
└── embedder/
    ├── Cargo.toml
    ├── Dockerfile                   ← focused build; only the embedder provider
    └── src/                         ← Embedder trait + EmbedderImpl + provide()
```

Yaml:

```yaml
name: smart-query
default_target: dev-monolith

resources:
  vector_index: { type: search, composition: cloud-managed }
  chat_llm:     { type: llm,    composition: cloud-managed, task: chat }
  embed_llm:    { type: llm,    composition: cloud-managed, task: embedding }

secrets:
  gemini_key:   { from: ssm, path: /smart-query/gemini }

entries:
  api:
    path:       ./api
    dockerfile: ./api/Dockerfile
    triggers:
      - http: { path: /query, port: 8080, public: true }
    uses: [vector_index, chat_llm, gemini_key]

seams:
  Embedder:
    path:       ./embedder
    dockerfile: ./embedder/Dockerfile
    uses:       [embed_llm]

targets:

  dev-monolith:
    runtime: docker-compose
    default_composition: oss-local
    entries: { api: container }
    # seams: omitted → Embedder is inproc; embedder code runs inside api's container

  dev-split:
    runtime: docker-compose
    default_composition: oss-local
    entries: { api: container }
    seams:   { Embedder: container }   # Embedder spawns its own compose service

  prod-monolith:
    runtime: aws
    default_composition: cloud-managed
    entries: { api: lambda }

  prod-split:
    runtime: aws
    default_composition: cloud-managed
    entries: { api: lambda }
    seams:   { Embedder: lambda }      # Embedder spawns its own Lambda
```

### Per-target topology

| Target | Deploy units | api's `CARAVAN_RPC_PEERS` (Embedder entry) |
|---|---|---|
| dev-monolith | 1 compose service (`api`) + opensearch + ollama | `{ Embedder: { mode: inproc } }` |
| dev-split | 2 compose services (`api` + `embedder`) + opensearch + ollama | `{ Embedder: { mode: http, url: http://embedder:8080 } }` |
| prod-monolith | 1 Lambda (`smart-query-api`) | `{ Embedder: { mode: inproc } }` |
| prod-split | 2 Lambdas (`smart-query-api` + `smart-query-embedder`) | `{ Embedder: { mode: lambda, function_url: ... } }` |

User's Rust source in `./api/src/lib.rs` calls `let e = client::<Embedder>(); e.embed(...)` — same line in all four targets. SDK reads `CARAVAN_RPC_PEERS` at runtime to pick dispatch mode.

### What gets built per target

- **dev-monolith** / **prod-monolith**: only `./api/Dockerfile`. The api binary includes both api + embedder code; runtime SDK dispatches inproc.
- **dev-split** / **prod-split**: `./api/Dockerfile` + `./embedder/Dockerfile`. Two images. api's binary still contains embedder source (Cargo workspace pulls it in), but the runtime peer table marks Embedder as external — the local `provide(Embedder, ...)` becomes inert.

The "extra dead code in api's binary" cost is accepted for PoC (no per-target Cargo features needed). Optimization via feature flags is v1+.

## Extensibility — three ≤3-line diffs

### 1. Swap api compute Lambda → Fargate (in prod-split)

```diff
 prod-split:
   entries:
-    api: lambda
+    api: container
```

Caravan emits `aws_ecs_service` instead of `aws_lambda_function`. The seam decision (`Embedder: lambda`) stays — embedder still its own Lambda, called from api's Fargate task.

### 2. Add a provisioned-capacity kv resource

```diff
 resources:
+  sessions: { type: kv, composition: cloud-managed, capacity_mode: provisioned, read_capacity: 100, write_capacity: 50 }
```

Then add `sessions` to `entries.api.uses`.

### 3. Swap search OpenSearch → pgvector

```diff
 resources:
-  vector_index: { type: search, composition: cloud-managed }
+  vector_index: { type: db.sql, composition: cloud-managed, engine: postgres, version: "16", extensions: [pgvector] }
```

User-code change accompanies (different client library — see [poc_groups_to_code.md §9](poc_groups_to_code.md#9-search)). Yaml wiring is one line.

## Testability — end-to-end definition

The PoC is testable end-to-end when **all** of the following hold:

1. **`caravan-rpc` SDK exists in all 4 languages** (Python, Rust, TypeScript, Go), each with unit tests proving inproc and http dispatch modes work against a controlled peer table.
2. **A reference app exists** matching the worked example above (Rust source for `./api` + `./embedder`).
3. **The caravan compiler** scans each entry's source + reachable deps, builds the seam → provider-location map, and emits the correct `CARAVAN_RPC_PEERS` per deploy unit per target.
4. **Manifest patching works**: in each target's build context, the patched `Cargo.toml` contains `caravan-rpc = "1.0"` and the right `rig-core = { features = [...] }` per composition. User's source `Cargo.toml` is unchanged on disk.
5. **`docker compose up`** succeeds on `dev-monolith` and `dev-split`.
6. **Same external endpoint, same response**: `curl -X POST http://localhost:8080/query -d '{"text":"hello"}'` returns the same payload (modulo timestamps / request IDs) on both `dev-monolith` and `dev-split`.
7. **Dispatch mode is observable in embedder logs**: `dev-monolith` shows `[caravan-rpc] Embedder.embed via INPROC`; `dev-split` shows `[caravan-rpc] Embedder.embed via HTTP from api`. **Load-bearing verification.**
8. **No source-code edit between targets.** `git diff -- api/src/ embedder/src/` shows zero lines.

### Implementation order (out of this doc's scope)

1. `caravan-rpc-{python, rust, typescript, go}` libraries.
2. Caravan compiler — phases 1–5 per [ir.md §3](ir.md#L200); phase 4 must compute `CARAVAN_RPC_PEERS` per deploy unit + emit manifest patches per target.
3. Compose + HCL emitters.
4. Reference app `smart-query`.
5. E2E test harness.

## Two-sided truth model

The yaml is the source of truth for the **deploy graph**: which entries exist, which seams exist, what resources they bind, how seams dispatch per target. The yaml is NOT the source of truth for **interface shapes**: those live in code, declared via the SDK's per-language mechanism (`@interface` / `#[interface]` / `defineInterface` / `interface + go:generate`). The optional `seams:` yaml block names the seams and points at their source; it does not redeclare method signatures.

## Out of PoC scope

The following are deliberately cut from PoC. All are recoverable from [ir.md](ir.md) and the full per-language mapping docs.

**Container-level fields not in PoC**: explicit `cpu:`, `memory:`, `replicas:`, `image:` (phase-4 defaults apply); per-target `env:` overrides (use `secret:` resources instead).

**Resource-level fields not in PoC**: `lifecycle:`, `variant:` (each group's PoC default fixes); `composition: by-id` (v1 hybrid-debug feature).

**Target-level fields not in PoC**: `vars:`, `ci:`, `account_id:`.

**Resource types not in PoC**: `topic` (use `queue` with multiple consumers); `static_site`; `cloud_only`. See [poc_groups_to_code.md "Out of PoC scope"](poc_groups_to_code.md#out-of-poc-scope).

**Trigger types not in PoC**: `topic`, `bucket_event`.

**Seam features not in PoC**: chained seams (a seam that calls another seam — splits compose to chains of deploy units); multi-provider seams (load-balanced or geographically-routed); seam-level Cargo feature gating (PoC accepts that the entry's binary carries dead provider code when seam is split — size cost only).

**RPC features deferred** (per [poc_rpc_sdk.md §7](poc_rpc_sdk.md#7-out-of-poc-scope)): streaming RPC, bidirectional RPC, runtime-validated codegen for Python/TS, IDL files, retry/circuit-breaker policies, trace propagation.

## What changed vs prior revisions (now in `poc_yaml_spec_old.md`)

Removed:
- The per-target `containers:` map that listed sources + dockerfile + uses + triggers for each container.
- The implicit "container is the unit of yaml" framing.
- Conflated `uses:` field that mixed cloud resources and SDK interfaces.

Added:
- `entries:` (top-level) — root deploy units defined once, referenced per target.
- `seams:` (top-level) — SDK interfaces declared once, dispatch decided per target.
- Per-target `seams: { <name>: inproc | container | lambda }` map — the per-seam, per-target packaging decision.

The thesis's three orthogonal dimensions remain expressed:
- **Packaging** → seam decisions (`inproc` vs split).
- **Placement** → `target.runtime` + entry deploy choice.
- **Composition** → `target.default_composition` + per-resource overrides.

## IR delta from [ir.md](ir.md)

The IR's `Module` + `Bundle` two-layer split collapses into the entries + seams shape above. The IR's `Interface` struct is now derived from yaml `seams:` + code scan (cross-checked, not redeclared). 4 new ResourceKinds (`cache`, `stream`, `search`, `llm`) still apply.

Upstream IR docs are not in sync yet — they retain the original Module + Bundle structure for v1+. See [`ir.md` top-of-file](ir.md) PoC-narrowing note.
