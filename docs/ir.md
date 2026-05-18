# IR data model + yaml schema + compiler pipeline

> Long-form reference for the IR (intermediate representation) introduced by the 2026-05-17 dispositions in [`considerations.md`](considerations.md). v4 §3 / §6 carry the load-bearing primitive list and yaml shape; this file adds the typed data model, the compiler-pipeline phase signatures, the env-var injection contract for inter-component RPC, and the mapping unambiguity audit that motivates each yaml field.
>
> Read order: [thesis.md](thesis.md) → [caravan_abstraction_v4.md](caravan_abstraction_v4.md) §3 + §6 → this file → [hcl_walkthrough.md](hcl_walkthrough.md) for a worked emit sample.
>
> **PoC narrowing (current direction)**: the PoC collapses this file's `Module` + `Bundle` two-layer split into a three-piece yaml shape — `entries:` (root deploy units, declared once) + `seams:` (SDK interface declarations, dispatched per target via `inproc | container | lambda`) + `targets:`. Containers are derived from per-target seam decisions, not declared. The yaml `interfaces:` block is gone in favor of yaml's `seams:` block + code-scan cross-check. See [poc_yaml_spec.md](poc_yaml_spec.md) for the narrowed yaml shape, [poc_rpc_sdk.md](poc_rpc_sdk.md) for the SDK contract surface, and [poc_groups_to_code.md](poc_groups_to_code.md) for the 10-group resource catalog. The structures below remain canonical for v1+; the PoC is a strict projection.

---

## 1. IR data model (Go-flavored sketch)

Post-phase-3 normalized IR, one `Plan` value per `caravan.yaml`:

```go
type Plan struct {
    Name           string
    DefaultTarget  string
    Modules        map[string]*Module
    Resources      map[string]*Resource
    Bundles        map[string]*Bundle
    Targets        map[string]*Target
    Secrets        map[string]*Secret
    Interfaces     map[string]*Interface       // optional, only if RPC SDK in use
    SourceSpans    map[string]Span             // for diagnostics
}

type Module struct {
    Name      string
    Kind      ModuleKind                       // http | worker | cron | batch | adapter
    Build     *BuildSpec
    Language  string                           // python | rust | typescript | go | container
    Uses      []Ref                            // -> Resource | Module | Secret (heterogeneous)
    Provides  []InterfaceRef                   // optional
    Triggers  []Trigger
    Env       map[string]string
    Expose    *ExposeSpec                      // for kind=http
}

type Trigger struct {
    Kind    TriggerKind                        // http | queue | topic | cron | bucket_event
    HTTP    *HTTPTrigger
    Queue   *QueueTrigger                      // -> Resource name
    Cron    *CronTrigger
    Topic   *TopicTrigger
    Bucket  *BucketEventTrigger
}

type Resource struct {
    Name        string
    Kind        ResourceKind                   // bucket|queue|topic|kv|db_sql|secret|static_site|cloud_only
    Tier        string                         // tier vocabulary per kind
    Variant     string                         // e.g. s3-express-one-zone
    Composition CompositionMode                // oss-local | cloud-managed | by-id
    ByID        *ByIDRef                       // iff Composition == ByID
    CloudOnly   *CloudOnlySpec                 // iff Kind == cloud_only
    Lifecycle   *LifecycleSpec
}

type Bundle struct {
    Name     string
    Modules  []string                          // >= 1
    Shape    BundleShape                       // long_running | function | batch
    Image    ImageStrategy                     // single | multi-stage
    CPU      string
    Memory   string
    Replicas *int
}

type Target struct {
    Name        string
    Runtime     RuntimeKind                    // docker-compose | aws | gcp | azure
    Region      string
    AccountID   string
    DefaultComposition CompositionMode         // sugar for all-cloud / all-local
    Placement   map[string]PlacementSpec       // Bundle.Name -> {on: fargate|lambda|apprunner|compose|batch}
    Composition map[string]CompositionOverride // Resource.Name -> {Mode, ByID?, Tier?}
    Vars        map[string]string
    CI          *CIBlock
}
```

**Phase-3 invariants** (checked at the end of normalization):

- Every `Module.Uses` ref resolves to one of `Resources[x]`, `Modules[x]`, or `Secrets[x]`.
- Every `Bundle.Modules[]` entry resolves to `Modules[x]`.
- Every `Target.Placement` key resolves to `Bundles[x]`.
- Every `Target.Composition` key resolves to `Resources[x]`.
- `Resource.ByID` is non-nil iff `Resource.Composition == ByID`.
- `Resource.CloudOnly` is non-nil iff `Resource.Kind == cloud_only`, and that resource's `Composition` is always `cloud-managed`.

**Sum-type counts** (driving [compiler-language.md](compiler-language.md) §4.1):
- `ResourceKind`: 9 (`bucket`, `queue`, `topic`, `kv`, `db_sql`, `secret`, `static_site`, `cloud_only`, and the placeholder for future provider-specific kinds)
- `ModuleKind`: 5 (`http`, `worker`, `cron`, `batch`, `adapter`)
- `BundleShape`: 3 (`long_running`, `function`, `batch`)
- `TriggerKind`: 5 (`http`, `queue`, `topic`, `cron`, `bucket_event`)
- `CompositionMode`: 3 (`oss-local`, `cloud-managed`, `by-id`)
- `RuntimeKind`: 4 (`docker-compose`, `aws`, `gcp`, `azure`)

---

## 2. Yaml schema (worked example)

This is the user-facing yaml that maps 1:1 onto §1 after normalization. ~70 lines, exercises every disposition from `considerations.md` §2.

```yaml
name: shoplet
default_target: dev-local

modules:
  api:
    kind: http
    build: ./modules/api
    language: go
    expose: { port: 8080, public: true }
    uses: [uploads, jobs, app_db, sessions, stripe_key, worker]   # heterogeneous: resources, modules, secrets

  worker:
    kind: worker
    build: ./modules/worker
    language: rust
    uses: [app_db, uploads, jobs]
    triggers:
      - queue: { from: jobs }                                     # consumer of `jobs`
      - cron:  { schedule: "0 2 * * *", name: nightly_cleanup, timezone: UTC }

  report:
    kind: batch
    build: ./modules/report
    language: python
    uses: [app_db, archives]

resources:
  uploads:    { type: bucket,    class: standard,             composition: cloud-managed, lifecycle: keep-90d }
  archives:   { type: bucket,    class: glacier-deep-archive, composition: cloud-managed }
  jobs:       { type: queue,     kind: standard,              composition: oss-local }
  app_db:     { type: db.sql,    engine: postgres, version: "16", tier: prod-small, composition: oss-local }
  sessions:   { type: kv,        primary_key: [pk, sk],       capacity_mode: on-demand, composition: cloud-managed }
  legacy_bus: { type: queue,     composition: by-id,
                by_id: { aws: "arn:aws:sqs:us-east-1:123456789012:legacy" } }
  llm:        { type: cloud_only,
                cloud_only: { type: bedrock.llm,
                              model: "anthropic.claude-opus-4-7-20260416-v1:0" } }

secrets:
  stripe_key: { from: ssm, path: /shoplet/stripe }

bundles:
  monolith:  { modules: [api, worker, report], shape: long_running, image: single, cpu: "1024", memory: "2048" }
  api_svc:   { modules: [api],                 shape: long_running }
  worker_fn: { modules: [worker],              shape: function }
  report_b:  { modules: [report],              shape: batch }

targets:

  dev-local:
    runtime: docker-compose
    placement:   { monolith: { on: compose } }
    # resources keep declared composition

  hybrid-dev:                                                     # mixed composition: real S3 + real Bedrock from local
    runtime: docker-compose
    placement:   { monolith: { on: compose } }
    composition:
      uploads:  { mode: cloud-managed }
      app_db:   { mode: oss-local }

  staging-fargate:
    runtime: aws
    region: us-east-1
    account_id: "111122223333"
    default_composition: cloud-managed
    placement:
      api_svc:   { on: fargate, size: "0.5vCPU/1GB" }
      worker_fn: { on: lambda,  size: "1024MB" }
      report_b:  { on: batch }
    composition:
      app_db: { mode: cloud-managed, tier: dev }

  prod:
    runtime: aws
    region: us-east-1
    account_id: "999988887777"
    default_composition: cloud-managed
    placement:
      api_svc:   { on: fargate, size: "1vCPU/2GB", scale: { min: 2, max: 20 } }
      worker_fn: { on: lambda,  size: "2048MB" }
      report_b:  { on: batch }
    composition:
      app_db:  { mode: cloud-managed, tier: premium }
      uploads: { mode: cloud-managed, lifecycle: versioning+archival }
```

**Semantic conventions baked into the schema:**

- `uses:` is the union of references; producer vs consumer roles on a queue are narrowed by the consumer's `triggers: - queue: { from: X }`. The module named in the queue trigger gets `receive+delete` IAM; every other module that has the queue in `uses:` gets `send`.
- `default_composition` on a target is sugar: any resource not in the target's `composition:` block inherits the target's default (overriding the resource's declared default).
- `bundles:` is optional. If absent, caravan auto-bundles one bundle per module (named after the module). Explicit `bundles:` is required only for the modular-monolith case or when a target references a bundle name. Phase-2 warning if `placement:` references an unknown bundle.

---

## 3. Compiler pipeline — five typed phases

| Phase | Name | Signature | Responsibility |
|---|---|---|---|
| 1 | Lex | `[]byte → RawYAML` | Parse YAML AST with source spans (line, col, length). |
| 2 | Parse | `RawYAML → ParsedDoc` | Map YAML onto Go structs; per-field schema validation; tagged-union dispatch on `type:`/`kind:`. Diagnostics carry source spans. |
| 3 | Normalize | `ParsedDoc → Plan` | Resolve cross-refs (`uses:` strings → typed pointers); apply defaults; flatten composition fields. `caravan spec` reads here. **Plan is the IR golden-file format.** |
| 4 | Resolve | `Plan × TargetName → ResolvedPlan` | Apply per-target composition/placement overrides; compute env-var injection (`CARAVAN_RPC_PEERS` JSON, endpoint URLs, table names); compute IAM derivation from `uses:` graph; networking derivation. |
| 5 | Emit | `ResolvedPlan → []HCLFile` (or `[]ComposeFile`) | Pure projection via `hclwrite` / native struct→yaml. No back-references into Plan. |

CLI verb → phases:

| Verb | Phases | Output |
|---|---|---|
| `caravan check` | 1–2 | stderr diagnostics; exit code |
| `caravan spec [--json\|--graph]` | 1–3 | stdout Plan JSON / graphviz / text |
| `caravan compile --target=X` | 1–5 | files in `infra/X/generated/` |
| `caravan diff --target=X` | (reads phase-5 output) + `tofu plan` | pretty-printed diff |
| `caravan up --target=X` | (reads phase-5 output) + `tofu apply` / `compose up` | apply |
| `caravan up --target=X --regenerate` | 1–5 + apply | one-shot |

**Why this split:** phase 3 is the cloud-agnostic IR. Phase 4 is where it becomes target-specific. Phase 5 is pure emission. Tests pin phase-3 as JSON goldens, phase-5 as `.tf.golden` files. The 3/4 boundary keeps `spec` cloud-agnostic and makes target-swap cheap.

---

## 4. Inter-module RPC — `caravan-rpc-<lang>` SDK contract

Per `considerations.md` item B disposition. Four libraries: `caravan-rpc-go`, `caravan-rpc-python`, `caravan-rpc-rust`, `caravan-rpc-typescript`. Same wire contract; idiomatic surface per language.

Per-language surface (Python shown; others mirror):

```python
from caravan_rpc import interface, provide, client

@interface
class JobRunner:
    def submit(self, job_id: str, payload: dict) -> str: ...

# In the worker source — owner registers an implementation.
class WorkerJobRunner(JobRunner):
    def submit(self, job_id, payload): ...

provide(JobRunner, WorkerJobRunner())

# In the api source — caller gets a proxy. Same call site regardless of packaging.
runner = client(JobRunner)                        # PoC: no target_module arg; caravan finds the provider by interface name (one provider per interface enforced at phase 2)
txn_id = runner.submit("job-42", {"customer_id": 17})
```

The original IR surface required `target_module="worker"`. The PoC drops that argument because phase 2 enforces one provider per interface (single-provider routing). Multi-provider routing (load-balanced fan-out, geographic) is a v1 expansion that may reintroduce the explicit peer-name argument.

**Env-var contract** — compiler injects per-container at phase 4:

| Env var | Value | Meaning |
|---|---|---|
| `CARAVAN_RPC_SELF` | `worker` | The container this process executes as (yaml container-name) |
| `CARAVAN_RPC_PEERS` | JSON: `{"JobRunner": {"mode":"inproc"}, "Billing": {"mode":"http","url":"http://billing:8080"}, "Fraud": {"mode":"lambda","function_url":"https://abc.lambda-url.us-east-1.on.aws/"}}` | Per-interface dispatch table |
| `CARAVAN_RPC_SHARED_SECRET` | random per-deploy hex | Bearer auth for `mode: http` calls |

Original IR additionally listed `CARAVAN_RPC_BUNDLE`. The PoC drops it because the container concept subsumes the bundle (one container = one deploy unit; `SELF` alone identifies it). PEERS keys are now **interface names** (not peer container names), reflecting the single-provider-per-interface PoC constraint.

**Phase 4 computation** — for every module M in the current bundle, for every peer P that M `uses:`:
- M and P in same bundle → `mode: inproc`
- M and P in different bundles, both long-running → `mode: http` with peer bundle's internal ALB URL
- P is a Lambda → `mode: lambda` with Function URL (`AuthType: AWS_IAM`)

**Wire format: HTTP/JSON v1.** Function URL + ALB + compose all natively speak it without sidecar; all four languages have mature HTTP+JSON stacks; gRPC would require ALB-HTTP/2 or NLB+sidecar (too much surface for v1). gRPC reconsidered at v2 if profiling demands.

**Codegen vs reflection:** runtime reflection in v1, codegen in v2. Reflection suffices for the four reference apps; codegen needs `caravan gen-rpc` CLI verb and per-language emitters. Risk: renamed method in B silently breaks A until runtime; mitigated by integration tests across bundles and the v1.1 codegen step.

**Optional `interfaces:` yaml section** (not required v1):

```yaml
interfaces:
  JobRunner:
    provider: worker
    methods:
      - submit(job_id: string, payload: object) -> string
```

When present, phase 2 cross-checks `provides:` declarations and pre-wires IAM grants on Function URL ARNs.

**Library home:** monorepo with `/sdk/<lang>/` directories, language-native package publishing on tag (`caravan-rpc-py-v0.1.0`, etc.).

---

## 5. Tier 1 env-var-name registry — `internal/tier1/tier1.yaml`

Per `considerations.md` item H disposition. Structured data file in the compiler repo, embedded via `//go:embed`. Shape:

```yaml
version: 1
pairs:
  llm:
    libraries:
      python:     { name: litellm,     env: [LLM_MODEL, OPENAI_API_KEY, AWS_REGION] }
      rust:       { name: rig-core,    env: [LLM_BACKEND, AWS_REGION] }
      typescript: { name: ai,          env: [LLM_BACKEND, LLM_MODEL] }
      go:
        - { name: langchaingo, env: [LLM_BACKEND, LLM_MODEL] }
        - { name: eino,        env: [LLM_BACKEND, LLM_MODEL] }
  jwt:
    libraries:
      python:     { name: authlib, env: [COGNITO_JWKS_URL, COGNITO_AUDIENCE] }
      ...
```

`caravan check` (phase 2) grep-scans each module's source files for at least one matching env-var name from the library row keyed by `module.language:`. Miss = lint warning (not error) because users may read env vars via config indirection. Adding a Tier 1 pair = PR to `tier1.yaml` + companion rows in the four `mapping_*_to_aws.md` docs.

---

## 6. Mapping unambiguity audit

Where yaml→emit projection requires a choice not fully determined by the yaml. Each gap closed by an explicit default, a yaml field, or a documented convention.

### 6a. Cloud projection (HCL)

| Gap | Closure |
|---|---|
| VPC CIDR | Hardcoded `10.0.0.0/16` convention; future `network: { cidr }` field |
| AZ count | Default `2`; future `network: { az_count }` (v1: 2 only) |
| NAT count | Default `1`; future `network: { nat: ha \| single }` |
| ALB scheme | Inferred: any module with `expose.public:true` → public ALB in public subnets |
| Multiple public modules | One shared ALB per target in v1; path-routing yaml field deferred to v1.1 |
| ECS task CPU/memory defaults | Hardcoded 512/1024; override via `bundles.<name>: { cpu, memory }` |
| RDS instance class per tier | Caravan-owned tier→instance mapping table in code, versioned with compiler |
| SQS producer vs consumer IAM | **Convention**: module in `triggers: - queue: { from: X }` is consumer (receive+delete); any other module with `X` in `uses:` is producer (send) |
| Image build context | Convention: `docker build .` from repo root (NOT subdir) so dispatcher has all module code; future `image: registry/foo:tag` skips ECR |
| Image dispatcher contract | CLI arg `--module=X` (single) / `--modules=a,b,c` (fused); documented per-language in caravan guide |
| Image tag | Default `latest` (staging) / `${git_sha}` (prod); future `image_tag:` field |
| CloudWatch log retention | Default 14 days; future `bundles.<name>: { log_retention_days }` |
| `expose.port` vs ALB listener port | Convention: ALB always `80`, forwards to module's `expose.port` |
| HTTPS listener | Out of scope v1; future `expose: { tls: true, cert_arn: ... }` |
| Worker shape disambig | Convention: `placement.<bundle>.on: fargate` → long-running poller; `on: lambda` → event source mapping |
| Lambda concurrency | Default unreserved; future `placement.<bundle>: { concurrency: { reserved: int } }` |
| ALB vs Function URL for HTTP triggers on function-shape bundles | Default Function URL; future `triggers.http: { via: function_url \| alb }` |
| `db_sql.version` | Required at phase 2; refuse yaml if absent |
| `secrets.from` enum | Restricted to `{ssm, secrets-manager, env, file}`; phase 2 errors on unknown |

### 6b. Local projection (compose)

| Gap | Closure |
|---|---|
| Compose service names | Convention: resource name = compose service name |
| Local credentials | Convention: hardcoded `app:app` / `minioadmin:minioadmin` — dev-only |
| Host port collisions | Emitter assigns `127.0.0.1:8081, 8082, …` in stable order; prints mapping table |
| Postgres volume strategy | Default named volume `<name>_pgdata`; future `local: { volume: ephemeral \| named: X }` |
| Hot reload | Disabled by default; future `dev: { hot_reload: true }` mounts module source |
| `composition: by-id` from compose | Phase 4 error unless target has `creds_passthrough: true` (mounts `~/.aws`) |
| ElasticMQ queue init | ElasticMQ auto-creates on first send; verify with explicit config if relied on |
| Bucket init | One-shot `mc mb` per bucket via `minio_init` sidecar |

### 6c. Hybrid (cloud resources without compute in cloud)

| Gap | Closure |
|---|---|
| Resource names in local env vars | Convention: `<app>-<resource>-<target>` naming; `caravan refresh` if drift |
| AWS creds source | Convention: mount `~/.aws:/root/.aws:ro` + `AWS_PROFILE=<target-name>`; future `target.aws_profile:` override |
| Cloud-side IAM for hybrid | Convention: no IAM emitted; human's profile carries perms (debugging posture) |
| db_sql cloud + compute local | Requires VPN / RDS Data API / public RDS; out of scope v1.1 |

Net: ~30 gaps, all closeable. Most via a single yaml field or a documented default. None require re-architecting.

---

## 7. Risk list

1. **Single-image multi-language bundle bloats fast.** Python + Rust + Go modular monolith easily hits ~400 MB. Cold-start tax on Lambda; pull latency on ECS. Mitigation: `docker buildx --cache-from`; v1.1 `image: multi-stage`.
2. **`CARAVAN_RPC_*` env-var contract is a hidden ABI.** Once shipped, breaking it forces lockstep upgrades across four SDKs and every deployed Lambda. Mitigation: freeze contract at v1 ship; semver SDK packages.
3. **`composition: by-id` Terraform import semantics are subtle.** `terraform import` is required to bring an existing ARN under state; emission bugs silently re-create. Mitigation: golden-file tests covering `by-id` paths; phase 4 emits explicit `import` blocks.
4. **Reflection-based RPC has no compile-time signature check across modules.** Renamed method in B silently breaks A until runtime call fails. Mitigation: v1.1 codegen; integration tests across bundles in v1 CI.
5. **Phase 4 is the largest single function** — networking, IAM, RPC peer table, composition overrides all collide. Risk of accidental complexity. Mitigation: sub-divide phase 4 by emitter concern in code organization.

---

## 8. Open for user (small decisions blocking nothing in particular)

- **`bundles:` auto-bundling** when section is absent. Proposed: silent fallback (one bundle per module, named after the module). Confirm vs require explicit `bundles:` always.
- **Inter-module Lambda auth.** Proposed: Function URL with `AuthType: AWS_IAM`. Confirm vs direct `lambda:Invoke`.
- **`caravan-rpc-*` library home.** Proposed: monorepo with `/sdk/<lang>/`. Confirm vs four separate repos.
- **Resource-name drift handling.** Proposed: convention `<app>-<resource>-<target>` + `caravan refresh` if drift. Accept vs Terraform-output read at compose-generation time.

---

## 9. Verification

Before code exists:
- [ ] Read §2 yaml; confirm all three thesis dimensions (packaging × placement × composition) are expressible.
- [ ] Walk the §2 yaml through `staging-fargate`; confirm the HCL in [hcl_walkthrough.md](hcl_walkthrough.md) follows without judgment calls outside the §6 audit.
- [ ] Walk the same yaml through `dev-local`; confirm the compose in [hcl_walkthrough.md](hcl_walkthrough.md) follows.

After v1 PoC code exists (extends v4 §9 checklist):
- [ ] `caravan check` flags a queue with both `uses:` and `triggers:` referencing it from the same module as duplicate.
- [ ] `caravan spec --json` emits a Plan with `Modules`, `Bundles`, `Resources`, `Targets` as separate top-level maps.
- [ ] `caravan compile --target=dev-local` produces the compose file in [hcl_walkthrough.md](hcl_walkthrough.md) (golden-file test).
- [ ] `caravan compile --target=staging-fargate` produces the HCL files in [hcl_walkthrough.md](hcl_walkthrough.md); `terraform fmt -check` passes.
- [ ] `caravan compile --target=hybrid-dev` produces both an `infra/hybrid-dev/generated/main.tf` (cloud resources only) and `docker-compose.generated.yaml` (no minio/elasticmq, AWS creds mount, real cloud env vars).
- [ ] All four reference apps link against `caravan-rpc-<lang>`, declare `JobRunner` (or equivalent), and pass `api → worker` calls both in-process (`dev-local`) and cross-bundle (`staging-fargate` → Function URL).
- [ ] `CARAVAN_RPC_PEERS` env var correctly computed per bundle per target; integration test confirms dispatch mode (inproc / http / lambda).
- [ ] Switching strategies (`dev-local` → `staging-fargate` → `hybrid-dev`) on the §2 yaml works without source-code edits in any reference app.
