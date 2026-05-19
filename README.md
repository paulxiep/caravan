# Caravan

An application-definition compiler that sits between application code and infrastructure-as-code. A caravan is your application as a graph of units that travels together and splits where deployment demands. Write one yaml describing the entry points, the SDK seams in the code, and the bound cloud resources; `caravan compile --target=<name>` emits auditable Terraform/HCL (cloud) or `docker-compose.generated.yaml` (local) into `infra/<target>/generated/`, and `caravan up --target=<name>` applies the emitted spec. The emit/apply split is by design — auditable HCL means HCL on disk between the two commands, not buried in a one-shot deploy.

An application is a graph of components connected through the `caravan-rpc` SDK at each inter-component **seam**. caravan lets one yaml project that graph onto any point in three orthogonal dimensions, with the source code unchanged.

The three dimensions:

- **Packaging** - how source seams become deploy units (modular monolith / multi-container / multi-service). Per target, each seam dispatches as `inproc` (no new deploy unit), `container` (compose service / Fargate task), or `lambda` (separate Lambda function).
- **Placement** - where processes run (local docker-compose / cloud long-running / cloud function / cloud batch).
- **Composition** - what each resource is bound to (local OSS engine / cloud managed service / existing cloud resource by ID). Mixing is first-class - local services can talk to real cloud resources in the same run.

A yaml `target:` names a point in (packaging × placement × composition). A repo declares many - `dev`, `hybrid-dev`, `staging`, `prod`, `pr-preview` - and `caravan up --target=<name>` flips between them. Same source code everywhere.

**Current state**: scoping-complete; PoC bootstrap in progress. **Phase A** (claim SDK package names across PyPI / crates.io / npm / Go) is done — see Implementation roadmap below. **Phase B** (functional SDK + test-repo conversion) starts at B0. **Phase 2** (AWS coverage) is deferred until Phase 1 (docker-compose validation) is green on both test repos.

## Scoping documents

### PoC scope (latest)

The PoC narrows the full thesis into a testable subset built around three load-bearing pieces: an RPC SDK (the control-plane primitive that makes packaging yaml-only), a 10-group resource catalog (the data-plane), and a yaml spec that binds them with a worked example proving the thesis end-to-end. The PoC docs supersede module / bundle vocabulary used in older docs — see each PoC doc's own "vocabulary change" note.

- [PoC inter-process RPC SDK](https://github.com/paulxiep/caravan/blob/main/docs/poc_rpc_sdk.md) - the `caravan-rpc` SDK. Why it exists, wire contract, env-var contract, per-language surface (Python / Rust / TypeScript / Go).
- [PoC basic groups → 4-language code mapping](https://github.com/paulxiep/caravan/blob/main/docs/poc_groups_to_code.md) - 10 basic resource groups picked from the AWS catalog, mapped to cloud SDK call + local OSS call + env-var swap (or build-time dep selection for the Tier-1 `llm` group) per language.
- [PoC yaml spec + worked example + testability plan](https://github.com/paulxiep/caravan/blob/main/docs/poc_yaml_spec.md) - the entries + seams + per-target dispatch yaml shape, the `smart-query` worked example with monolith / split / cloud / local target variants, end-to-end testability conditions.

### Full thesis + upstream IR (canonical reference)

- [Thesis](https://github.com/paulxiep/caravan/blob/main/docs/thesis.md) - primary scoping doc. Some text on user-restructuring and runtime libraries is being revised to reflect the PoC's SDK-as-structural-contract decision; read alongside [poc_rpc_sdk.md §1](https://github.com/paulxiep/caravan/blob/main/docs/poc_rpc_sdk.md).
- [IR data model + yaml schema + pipeline](https://github.com/paulxiep/caravan/blob/main/docs/ir.md) - typed IR sketch, the yaml the user writes, compiler phase signatures, RPC env-var contract, mapping unambiguity audit. The PoC collapses the IR's Module + Bundle two-layer split into the entries + seams + per-target dispatch shape (see [poc_yaml_spec.md](https://github.com/paulxiep/caravan/blob/main/docs/poc_yaml_spec.md)).
- [HCL primer + worked emit sample](https://github.com/paulxiep/caravan/blob/main/docs/hcl_walkthrough.md) - learn HCL syntax through a fully annotated `staging-fargate` emit + `dev-local` compose + `hybrid-dev` delta.
- [Considerations](https://github.com/paulxiep/caravan/blob/main/docs/considerations.md) - ambiguity catalogue + dispositions (resolved 2026-05-17 for items A-K).
- [Abstraction v4 derivation](https://github.com/paulxiep/caravan/blob/main/docs/caravan_abstraction_v4.md) - long-form derivation behind the thesis (four-language re-derivation: primitive shapes, yaml schema, IaC choice, gotchas). Supersedes v3.
- [AWS service groups](https://github.com/paulxiep/caravan/blob/main/docs/aws_service_groups.md) - cost / latency / scale catalog of AWS services.
- [GCP service groups](https://github.com/paulxiep/caravan/blob/main/docs/gcp_service_groups.md) - GCP companion to the AWS catalog (cost / latency / scale, GCP-native role groupings).
- [Azure service groups](https://github.com/paulxiep/caravan/blob/main/docs/azure_service_groups.md) - Azure companion to the AWS catalog (cost / latency / scale, Azure-native role groupings including Service Bus, Cosmos DB, Microsoft Fabric, Logic Apps).
- [Cloud providers](https://github.com/paulxiep/caravan/blob/main/docs/cloud_providers.md) - cross-provider primitive mapping (AWS / GCP / Azure) and divergences from the AWS baseline.
- Python ecosystem evidence: [mapping AWS→Python](https://github.com/paulxiep/caravan/blob/main/docs/mapping_aws_to_python.md) · [mapping Python→AWS](https://github.com/paulxiep/caravan/blob/main/docs/mapping_python_to_aws.md) · [Python API diffs](https://github.com/paulxiep/caravan/blob/main/docs/python_api_diffs.md).
- Rust ecosystem evidence: [mapping AWS→Rust](https://github.com/paulxiep/caravan/blob/main/docs/mapping_aws_to_rust.md) · [mapping Rust→AWS](https://github.com/paulxiep/caravan/blob/main/docs/mapping_rust_to_aws.md) · [Rust API diffs](https://github.com/paulxiep/caravan/blob/main/docs/rust_api_diffs.md).
- TypeScript ecosystem evidence: [mapping AWS→TypeScript](https://github.com/paulxiep/caravan/blob/main/docs/mapping_aws_to_typescript.md) · [mapping TypeScript→AWS](https://github.com/paulxiep/caravan/blob/main/docs/mapping_typescript_to_aws.md) · [TypeScript API diffs](https://github.com/paulxiep/caravan/blob/main/docs/typescript_api_diffs.md).
- Go ecosystem evidence: [mapping AWS→Go](https://github.com/paulxiep/caravan/blob/main/docs/mapping_aws_to_go.md) · [mapping Go→AWS](https://github.com/paulxiep/caravan/blob/main/docs/mapping_go_to_aws.md) · [Go API diffs](https://github.com/paulxiep/caravan/blob/main/docs/go_api_diffs.md).
- [Abstraction v3](https://github.com/paulxiep/caravan/blob/main/docs/caravan_abstraction_v3.md) - prior long-form derivation, Python + Rust + TypeScript. Historical record.
- [Abstraction v2](https://github.com/paulxiep/caravan/blob/main/docs/caravan_abstraction_v2.md) - prior long-form derivation, Python + Rust only. Historical record.
- [Abstraction v1](https://github.com/paulxiep/caravan/blob/main/docs/caravan_abstraction_v1.md) - prior framing built around a Python SDK. Historical record.

## Implementation roadmap

- [Development plan](https://github.com/paulxiep/caravan/blob/main/docs/development_plan.md) — milestones B0 → M9 split into **Phase 1** (docker-compose + local-run, thesis-proving) and **Phase 2** (AWS coverage, deferred). Phase 1 alone proves the thesis on real code.
- [SDK source of truth](https://github.com/paulxiep/caravan/tree/main/rpc) — `caravan/rpc/<lang>/` for Python, Rust, TypeScript, Go. Currently 0.0.1 placeholders that import-clean but don't dispatch; replaced by functional 0.1.0 at B0 (Python) and M2 (Rust). TypeScript and Go directories exist as namespace reservations; out of PoC scope.
- **Published placeholders** (Phase A — 2026-05-19):
  - PyPI: [`caravan-rpc` 0.0.1](https://pypi.org/project/caravan-rpc/)
  - crates.io: [`caravan-rpc` 0.0.1](https://crates.io/crates/caravan-rpc)
  - npm: [`caravan-rpc` 0.0.1](https://www.npmjs.com/package/caravan-rpc)
  - Go: [`github.com/paulxiep/caravan/rpc/go` v0.0.1](https://pkg.go.dev/github.com/paulxiep/caravan/rpc/go)
- **Test repos** (real-world design pressure for B0 / M5 / M6):
  - [code-rag](https://github.com/paulxiep/code-rag) — 6-crate Rust workspace, RAG over code (M5 target; readiness rated HIGH ~80%).
  - [invoice-parse](https://github.com/paulxiep/invoice-parse) — Python+Rust polyglot, OCR + LLM extraction (B0 bootstrap + M6 target; readiness rated HIGH ~85%).

## Status

Phase A (squat) complete. Phase B (functional SDK on real test repos) is the active phase per [`docs/development_plan.md`](https://github.com/paulxiep/caravan/blob/main/docs/development_plan.md). The thesis is load-bearing; some evidence catalogs in `docs/` may still shift as Phase B surfaces ergonomics issues.
