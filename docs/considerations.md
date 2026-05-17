# Considerations — from thesis + v4 to "where to start designing"

Scoping memo. Inputs are [`thesis.md`](thesis.md) and [`supeux_abstraction_v4.md`](supeux_abstraction_v4.md) (the two canonical living documents; earlier abstraction docs and the architecture brainstorm have been deleted).

Output is meant to be read once, used to align, then either edited (to push back) or used to seed the next concrete design doc.

## Status (2026-05-17)

Items **A, B, C, D, E, F, G, H, I, J, K** dispositioned in the original IR pass. Items **Q, R, S, T** dispositioned by the PoC scoping pass (see §2.4 below). See per-item resolution lines inline; long-form artifacts in [`ir.md`](ir.md) (typed IR + yaml schema + pipeline + RPC contract + mapping audit), [`hcl_walkthrough.md`](hcl_walkthrough.md) (HCL primer + worked emit sample), and the three PoC docs: [`poc_yaml_spec.md`](poc_yaml_spec.md), [`poc_rpc_sdk.md`](poc_rpc_sdk.md), [`poc_groups_to_code.md`](poc_groups_to_code.md). Items L–P remain deferred per §2.3.

**PoC narrowing supersedes parts of items A and J**: the PoC collapses the `Module` + `Bundle` two-layer split into a single **container-per-target** concept; modules-as-yaml-primitives are dropped in favor of source-tree paths and code-scan-discovered SDK seams (`@interface` / `provide` / `client`). The full IR in [`ir.md`](ir.md) retains the original two-layer structure for v1+; the PoC is a strict projection. See §2.4.

Headline outcomes from the dispositions:
- **Modules are first-class** (IR primitive renamed from `service:` to `module:`); `service` retired as an IR primitive to avoid overload with cloud-service (S3, RDS).
- **Bundles** introduced as the packaging-arithmetic primitive (1..N modules → 1 deploy unit).
- **`composition:`** is a per-resource field, per-target overrideable — collapses the mixed-composition story into one uniform field.
- **`cloud_only`** is the 9th `ResourceKind` (not a top-level section).
- **Cron** lives under each module's `triggers:` list; the top-level `triggers:` block in v4 §6 is retired.
- **Inter-module RPC**: supeux ships `supeux-rpc-<lang>` SDKs (Go, Python, Rust, TS); HTTP/JSON wire v1, runtime reflection v1, codegen v2.
- **Compiler pipeline** is five typed phases (Lex → Parse → Normalize → Resolve → Emit).
- **Tier 1 env-var table** owned by repo as `internal/tier1/tier1.yaml`, embedded via `//go:embed`.
- **Per-cloud init** is AWS-only in v1; GCP/Azure named for v2.x.

The dispositions edit v4 §3 / §6 inline (primitives table, yaml schema, env-var injection table). Thesis is unchanged in its load-bearing principles; one mention of `service.shape:` is renamed to `module.shape:`.

---

## 0. What "IR" means in this memo

**IR = intermediate representation.** It's the typed, in-memory data structure the compiler operates on between *parsing the user's yaml* and *emitting HCL / docker-compose*. Same idea as a compiler's AST, just one layer up: not the syntax tree of the yaml file, but the normalized, validated model of what the user *meant*.

Concretely for supeux, the IR is roughly:

```
parse yaml  →  raw struct (phase 1–2)
            →  validate + cross-ref resolve (phase 2–3)
            →  normalized IR (phase 3)         ← this is "the IR"
            →  per-provider resolution (phase 4)
            →  HCL / compose files (phase 5)
```

The IR is what `supeux spec` dumps, what golden-file tests pin against, and what the HCL emitter consumes. Designing it well matters because every CLI verb, every emitter, every test, and every error message ultimately reads or writes IR. v4 talks a lot about *what supeux ships*; it is thin on *what the IR is as a typed object*. That gap is the reason this memo exists.

---

## 1. Thesis goal, restated

> **An application is a graph of modules connected by interfaces. One yaml projects that graph onto any point in three orthogonal dimensions, with the source code unchanged.**
>
> Dimensions:
> - **Packaging** — N modules → 1 process / N co-located containers / N independently-deployed units.
> - **Placement** — local (compose) / cloud long-running (Fargate, App Runner) / cloud function (Lambda) / cloud batch.
> - **Composition** — local OSS engine / cloud managed service / pre-existing cloud resource referenced by ID. **Mixing is first-class** (local processes can talk to real S3 / real Bedrock).
>
> A `target:` names a point in (packaging × placement × composition). `supeux up --target=<name>` flips between targets. Same source code.
>
> The compiler is two-step on purpose: `supeux compile` emits a reviewable HCL/compose artifact on disk; `supeux up` applies it. This is what makes "auditable IaC artifacts" load-bearing rather than aspirational.

That sentence is the load-bearing core. Everything in v4 is downstream and may shift; the thesis goal itself does not.

---

## 2. Design decisions

Items A–K were originally ambiguous calls from v4 / thesis; resolutions below are current state as of 2026-05-17 and have been written into [`supeux_abstraction_v4.md`](supeux_abstraction_v4.md), [`ir.md`](ir.md), and [`hcl_walkthrough.md`](hcl_walkthrough.md). Items L–P (§2.3) remain deferred.

### 2.1 IR-shaping decisions

- **A. Modules are first-class; `service` retired as IR primitive.** User-code unit is `module:` in yaml/IR (replacing v4's `services:`). Packaging arithmetic lives in a new top-level `bundles:` primitive (1..N modules → 1 deploy unit). `service` is dropped from the IR vocabulary because it overloaded with cloud-service (S3, RDS); the user's distinction between *user-code component* (now `module`) and *cloud provider primitive* (`resource`) is what drove the rename. Thesis unchanged — it already spoke `module` natively. The packaging dimension (modular monolith / multi-container / multi-deploy) now has a typed home via `bundles:`. v4 §3 primitives table and §6 yaml edited.

- **B. Inter-component RPC ships as `supeux-rpc-<lang>` per-language SDKs.** Four libraries (Go, Python, Rust, TypeScript) — a deliberate reversal of v4 §4's "supeux ships zero code libraries" call, scoped to the RPC plumbing layer only (Tier 1 cross-vendor abstraction libraries — rig / litellm / Vercel AI SDK / langchaingo — remain community-curated, though supeux now patches them into the build context per target — see item R). No community library covers "switch between in-process and remote dispatch based on packaging," so supeux owns that contract. SDK surface: `@interface` declaration, `provide(impl)` for the provider, `client(Interface) → proxy` for callers (PoC drops the `target_module` arg; phase 2 enforces single-provider-per-interface). The compiler injects `SUPEUX_RPC_PEERS` JSON per container at phase 4 — `{Interface: {mode: inproc|http|lambda, url?}}` — and the proxy dispatches in-process or over wire based on that table. Wire format: HTTP/JSON v1 (Function URL + ALB + compose all speak it natively without a sidecar; gRPC reconsidered at v2). Lambda peers reached via Function URL with `AuthType: AWS_IAM`; HTTP mode uses bearer-auth via `SUPEUX_RPC_SHARED_SECRET`. Runtime reflection v1 for Python/TS, codegen for Rust/Go. Libraries live in the compiler monorepo at `/sdk/<lang>/`.

- **C. Composition is a per-resource field, per-target overrideable.** Every resource declares `composition: oss-local | cloud-managed | by-id`. Targets override per-resource via `targets.<name>.composition.<resource>:`. `by-id` carries `by_id: { aws: "arn:..." }` for referencing pre-existing cloud resources. Targets may declare `default_composition:` as sugar for the all-cloud / all-local case. Thesis dimension 3 ("mixing is first-class") now has yaml syntax: pin one resource to cloud while everything else stays local by overriding the single resource in that target's `composition:` block. v4 §6 yaml edited.

- **D. Cron lives under each module's `triggers:` list.** Top-level `triggers:` section dropped from v4 §6 — that block contradicted v4 §3's "cron is a property of a module." Form: `modules.<name>.triggers: - cron: { schedule: "0 2 * * *", name: nightly_cleanup, timezone: UTC }`. Trade-off: a cron fanning out to two modules requires two trigger entries (one per consumer) — accepted as more honest than indirection.

- **E. `function`-shape locally is user-code; supeux is silent.** Modules whose bundle has `shape: function` run as long-lived servers locally with no supeux wrapping. User code already branches on `AWS_LAMBDA_RUNTIME_API` via the language's idiomatic adapter (`lambda_http` / `Mangum` / `hono/aws-lambda` / `aws-lambda-go-api-proxy`). supeux's only job per the shape is choosing Lambda Terraform vs Fargate Terraform — so `shape: function` is a real IR axis (drives emission) without being a local-runtime injection. Local port mapping follows `expose.port:` if declared, otherwise emitter convention (`127.0.0.1:8081+` in stable order, mapping table printed by `supeux up`).

- **F. Networking is auto-derived with conservative defaults + sibling-`.tf` escape hatch.** Defaults: VPC `10.0.0.0/16`; 2 AZs; `/24` public subnets (`10.0.0.0/24`, `10.0.1.0/24`) + `/24` private subnets (`10.0.10.0/24`, `10.0.11.0/24`); 1 NAT in the first public subnet; Fargate tasks in private subnets; ALB in public subnets if any module has `expose.public: true`; one SG per bundle (no ingress except via ALB or self) + one SG per cloud resource (ingress restricted to bundle SGs whose modules use that resource). VPC endpoints **not** auto-emitted — cost-tuning lives in user-written sibling `.tf`. Hand-written `.tf` files alongside `generated/` are preserved at apply (the v1 escape hatch). Future yaml fields `network: { cidr, az_count, nat: ha|single }` ship when user pressure justifies; v1 holds these hardcoded.

### 2.2 Compiler-architecture decisions

- **G. Compiler pipeline is five typed phases.** (1) Lex: `[]byte → RawYAML` (parse with source spans). (2) Parse: `RawYAML → ParsedDoc` (struct mapping, per-field validation, tagged-union dispatch). (3) Normalize: `ParsedDoc → Plan` (resolve cross-refs to typed pointers; apply defaults; flatten composition fields — `Plan` is the IR golden-file format `supeux spec` exposes). (4) Resolve: `Plan × TargetName → ResolvedPlan` (per-target overrides; env-var injection; IAM derivation; networking derivation). (5) Emit: `ResolvedPlan → []HCLFile` / `[]ComposeFile` (pure projection via `hclwrite`). CLI verbs: `check` runs 1–2; `spec` runs 1–3; `compile` runs 1–5; `diff` / `up` read phase-5 output; `up --regenerate` re-runs 1–5 then applies. The 3/4 boundary keeps `spec` cloud-agnostic and makes target-swap cheap.

- **H. Tier 1 env-var-name table lives in `internal/tier1/tier1.yaml`, embedded via `//go:embed`.** Structured data file (not a markdown table). Shape: `pairs.<pair>.libraries.<lang>: { name, env: [env-var-name...] }`, with `[ {name, env}, ... ]` for two-canonical-options rows (Go LLM, Go email). `supeux check` at phase 2 grep-scans module source files for matches, narrowed by the module's declared `language:`. Miss = lint warning, not error (users may read env vars via config indirection). Adding a Tier 1 pair = PR to `tier1.yaml` + companion rows in the four `mapping_*_to_aws.md` docs. Version field tracks breaking schema changes.

- **I. `supeux init` is AWS-only in v1; GCP/Azure named for v2.x.** v1: `supeux init` provisions S3 + DynamoDB lock, writes `backend.tf`. Other clouds error with "v1 supports AWS only; for GCP/Azure write `backend.tf` manually — see docs/state-backend.md". v2.x: `supeux init --cloud=gcp` → GCS bucket with object versioning + object-generation locks; `supeux init --cloud=azure` → Storage Account + Blob Container with blob-lease locks. Per-cloud init ships with the corresponding HCL-provider emission milestone in v4 §10 — same v2.x lane.

- **J. Modular monolith emits as single-image multi-entrypoint via the `bundles:` primitive.** A bundle `monolith: { modules: [api, worker, report], shape: long_running, image: single }` emits ONE container image (built from repo root, all module code baked in) + ONE `aws_ecs_task_definition` (or `aws_lambda_function`) with ONE container. A thin `supeux-entrypoint` shim reads `--modules=a,b,c` CLI arg and dispatches each module as an in-process worker. `SUPEUX_RPC_PEERS` marks every sibling as `mode: inproc`. Single image (vs per-module images) is the load-bearing choice: multi-container in one deploy unit is already the middle packaging band (multiple bundles, same target placement); modular-monolith must collapse harder. Trade-off: cross-language monoliths (Python + Rust + Go) produce fat images (~400 MB) — mitigated by `docker buildx --cache-from`; v1.1 adds `image: multi-stage` if it bites.

- **K. `cloud_only` is the 9th `ResourceKind`.** Lives inside `resources:` (not as a top-level section). Yaml: `resources: llm: { type: cloud_only, cloud_only: { type: bedrock.llm, model: ... } }`. Uniform `Resource` walker in phase 3 / phase 5 — no special-casing. `Composition` is always `cloud-managed` for `cloud_only` (emitter refuses `oss-local`). Cloud-only-specific fields live in a free-form `cloud_only:` map validated per-`type` in phase 2 via a registry. Trade-off: free-form `params` is less type-safe than the eight built-in primitives — validated at the registry boundary, accepted because per-`type` shape varies (bedrock.llm vs cloudfront vs dax).

### 2.4 PoC scoping dispositions (added during PoC narrowing pass)

These items emerged when scoping the PoC and were dispositioned in the rewrites of [`poc_yaml_spec.md`](poc_yaml_spec.md), [`poc_rpc_sdk.md`](poc_rpc_sdk.md), and [`poc_groups_to_code.md`](poc_groups_to_code.md). They partially supersede items A (modules-as-yaml-primitive) and J (modular monolith via `bundles:`) above.

- **Q. Module + Bundle collapse to entries + seams + per-target dispatch.** The PoC yaml drops the `modules:` and `bundles:` top-level blocks. Three new top-level constructs replace them: `entries:` (root deploy units with their own Dockerfile, triggers, and resource `uses:`), `seams:` (named SDK interfaces with a path to the provider's sub-crate and its own focused Dockerfile), and per-target `seams: { <Name>: inproc | container | lambda }` decisions. Containers are derived from per-target seam decisions, not declared. The `interfaces:` yaml block also drops — `@interface` / `provide` / `client` declarations live in code, and supeux's phase 2 cross-checks the yaml `seams:` block against code scans. Net IR delta vs item A: three new top-level constructs (entries, seams, target.seams), two yaml blocks deleted (modules, bundles), packaging-arithmetic is per-seam per-target. The full IR retains the Module + Bundle structures for v1+.

- **R. Manifest patching: supeux owns the build artifact, user owns the Dockerfile.** Supeux patches the user's package manifest (`Cargo.toml` / `requirements.txt` / `package.json` / `go.mod`) per container per target with two categories of supeux-managed deps: (1) the `supeux-rpc-<lang>` SDK (always), and (2) Tier 1 hard-pair provider selection — the `llm` group's Bedrock-vs-Ollama provider package chosen per target's composition. User's on-disk manifest is untouched; the patched copy lives only in the per-target build context. User's Dockerfile is required and unmodified by supeux — algorithmic Dockerfile generation is rejected (too much per-language variance: base images, build steps, CGO flags, GPU runtimes). The Dockerfile contract: `COPY` the manifest from build context, don't inline dep additions.

- **S. Tier 0 vs Tier 1 distinction surfaced.** Of the 10 PoC basic resource groups (compute, object_store, kv, queue, sql, secret, cache, stream, search, llm), nine are Tier 0 (same client library both sides; only an env var changes per target) and one is Tier 1 (`llm`; cloud and local use different libraries; supeux's manifest patching from item R is what makes the source-unchanged claim hold). The thesis-level "abstraction libraries for hard pairs" principle now has an operational mechanism (manifest patching) rather than just a documentation pointer.

- **T. One supeux.yaml = one VPC.** Each yaml emits one network boundary (one VPC for cloud targets; one docker-compose network for local). Multi-VPC apps require multiple yamls; supeux does not coordinate across yamls, repos, or git remotes. The supeux-rpc SDK only routes within the single VPC scope. Frames the "one repo, one supeux.yaml" stance from a security/networking POV — what you get with one yaml is one network identity.

### 2.3 Deferred (lower-impact, not dispositioned)

These items remain open. They don't block IR or emitter design; revisit when implementation surfaces concrete pressure.

- **L.** v4 §9 names the v1 cloud target `aws-dev`; §6 yaml example uses `staging` / `prod`. Pick one canonical example name for the docs and the CLI golden tests.
- **M.** v4 §6 shows `secrets: stripe_key: {}` with empty body. Where does the value come from? Resolver model (env var? SSM ParameterStore? Secrets Manager? user supplies at `supeux up`?) is unspecified. (Note: v4 §6 yaml in this update uses `{ from: ssm, path: ... }` as a working assumption, but the full `from:` enum and resolution semantics need a dedicated pass.)
- **N.** Multi-file yaml: v4 §6 says "one `supeux.yaml`." Compose has `include:`; Terraform has modules. No statement on whether a big monorepo can split its supeux definition. Reasonable to defer, but it should be a stated deferral.
- **O.** "Multi-container in one deploy unit" (thesis dim 1, middle band) is operationally underspecified — is that an ECS task with sidecars, a compose service group, a Lambda function with Lambda extensions? Each maps to different Terraform. (The A/J dispositions cover modular-monolith and multi-bundle-same-placement; the multi-container-in-one-task middle case is still open.)
- **P.** Schema validation strategy (JSON Schema vs hand-written validator vs language-native struct unmarshal) isn't picked. Affects how `supeux check` and `supeux spec` are built.

---

## 3. High-level proposal: where to start designing

> **Status (2026-05-17):** steps 1–4 below are executed — IR data model + yaml schema in [`ir.md`](ir.md), worked HCL emit sample in [`hcl_walkthrough.md`](hcl_walkthrough.md), pipeline phases pinned in §2.2 G, inter-module RPC dispositioned in §2.1 B. Steps 5–6 (HCL emitter implementation, CLI surface) await coding. The section is preserved as the original sequencing argument.

The recommendation is to **resist starting with code, the CLI surface, or the HCL emitter**, and instead lock the IR's data model and the compiler pipeline first. v4 is rich on *what supeux ships* and thin on *what the IR actually is as a typed object*. The latter is the spine; everything else slots into it.

### Suggested order

1. **Lock the IR data model.**
   The first artifact should be a short doc that defines the IR as a typed shape (think Go structs / Rust enums / JSON Schema, pick later). Resolve A, B, D, J, K at this layer. Outcome: "what is the IR a value of?" has a single answer.

2. **Prove the three thesis dimensions are orthogonal in that IR.**
   Write three toy yamls — one toggling packaging only, one toggling placement only, one toggling composition only — and confirm each is expressible. If composition's "mixed" / "by-ID" leg or packaging's "monolith" leg can't be expressed, the IR is genuinely missing axes. This is a one-evening test that catches structural gaps cheaply.

3. **Pin the compiler pipeline (phases 1–5).**
   Each phase becomes a typed transform: input shape → output shape. Resolve G. This defines the spine of the compiler and the test seams. It also clarifies which CLI verbs read which intermediate (e.g., `spec` reads phase-3 IR, `compile` writes phase-5 HCL).

4. **Scope inter-module communication separately.**
   B is large enough to deserve its own scoping memo. Don't fold it into the IR doc — the answer (community RPC library choice per language, transport, addressing, auth) drives schema fields but is its own design question. Output: which library, what addressing, what env-var convention; whether the IR needs an `interfaces:` section or whether `uses:` already covers it.

5. **Only then design the HCL emitter.**
   [`compiler-language.md`](compiler-language.md) already justifies Go + `hclwrite` + `tfexec` — that decision can stand. But the emitter's input shape *is* the IR; thrashing the IR after the emitter exists is the expensive way to do this. Networking auto-derivation rules (F) get pinned here, by going one primitive at a time and writing the emitted HCL by hand first.

6. **CLI surface last.**
   The verb set (`init | compile | diff | up | down | spec | check`) is skin over the pipeline. Designing the verbs before the pipeline is designed inverts the dependency.

### Things to consider while doing the above

The recommendations below survive the dispositions in §2 — they're either implementation discipline that doesn't expire, or watch-list items for the future.

- **Don't design v1's four reference apps in parallel with the compiler.** They are *verification*, not design input. Build the compiler against a single hand-written yaml, then port the four reference apps. Otherwise the four apps will smuggle requirements into the IR that v4 never sanctioned.
- **Resist letting the CLI's `--target=` flag become a god-flag.** It selects packaging + placement + composition simultaneously. Fine for v1 ergonomics but bears watching — if users ever want to flip one dimension without the others, the IR needs to expose them separately.
- **The phase-3 IR (post-normalization, pre-emit) is the load-bearing test seam.** It's what `supeux spec` exposes and what golden-file tests should pin. Pin `Plan` JSON goldens before phase-5 HCL goldens.

The following recommendations have been folded into §2 dispositions and need no separate consideration: *pick the modules-vs-services stance* (resolved per A), *treat `cloud_only:` as a forcing function for IR uniformity* (resolved per K), *treat composition as an axis of every resource* (resolved per C), *the Tier 1 env-var-name table should be a versioned data file* (resolved per H).

### What this memo did and did not do

- Did: catalogue the v4 / thesis ambiguities (§2 originals); disposition A–K with reasoning (§2 current); seed the typed IR data model, yaml schema, compiler-phase signatures, and HCL emit sample now living in [`ir.md`](ir.md) + [`hcl_walkthrough.md`](hcl_walkthrough.md); pick the modules-first-class stance (A) and the supeux-authored RPC-SDK stance (B).
- Did not: propose CLI verb details beyond the phase mapping in G; pick a v1 cloud-target canonical name (L); pick the schema-validation strategy (P); disposition the other deferred items in §2.3.

---

## 4. Verification (how to know this memo did its job)

- The reader can read §1 and confirm the thesis goal is stated as they hold it.
- The reader can read §2 and either accept the resolutions or flag specific dispositions to revisit (in which case the linked [`ir.md`](ir.md) and [`hcl_walkthrough.md`](hcl_walkthrough.md) need matching edits).
- The reader can read §3 and say "yes, the IR/pipeline-first sequencing was right" or push back with a different ordering for the remaining unimplemented work (HCL emitter, CLI).
- Originally items **A** (modules vs services), **B** (RPC story), and **C** (mixed composition syntax) were named as the three to disposition before any IR design starts. **Done (2026-05-17): all three dispositioned (see §2.1). The IR data model is now committed in [`ir.md`](ir.md).**
