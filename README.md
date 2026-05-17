# Supeux

*Pronounced "super", without the 'r' sound. A portmanteau of **super**, **souple** (French: "supple, flexible"), and **peux** (French: "can") - super-charged flexibility and capability behind a simple yaml interface.*

An application-definition compiler that sits between application code and infrastructure-as-code. Write one yaml describing the module graph and its bound cloud resources; `supeux compile --target=<name>` emits auditable Terraform/HCL (cloud) or `docker-compose.generated.yaml` (local) into `infra/<target>/generated/`, and `supeux up --target=<name>` applies the emitted spec. The emit/apply split is by design — auditable HCL means HCL on disk between the two commands, not buried in a one-shot deploy.

An application is a graph of **modules** connected by interfaces. supeux lets one yaml project that graph onto any point in three orthogonal dimensions, with the source code unchanged.

The three dimensions:

- **Packaging** - how modules become processes (modular monolith / multi-container / multi-service).
- **Placement** - where processes run (local docker-compose / cloud long-running / cloud function / cloud batch).
- **Composition** - what each resource is bound to (local OSS engine / cloud managed service / existing cloud resource by ID). Mixing is first-class - local services can talk to real cloud resources in the same run.

A yaml `target:` names a point in (packaging × placement × composition). A repo declares many - `dev`, `hybrid-dev`, `staging`, `prod`, `pr-preview` - and `supeux up --target=<name>` flips between them. Same source code everywhere.

This repo is **scoping-stage**: thesis + evidence catalogs, no implementation yet.

## Scoping documents

- [Thesis](https://github.com/paulxiep/supeux/blob/main/docs/thesis.md) - primary scoping doc. Read first.
- [IR data model + yaml schema + pipeline](https://github.com/paulxiep/supeux/blob/main/docs/ir.md) - typed IR sketch, the yaml the user writes, compiler phase signatures, RPC env-var contract, mapping unambiguity audit.
- [HCL primer + worked emit sample](https://github.com/paulxiep/supeux/blob/main/docs/hcl_walkthrough.md) - learn HCL syntax through a fully annotated `staging-fargate` emit + `dev-local` compose + `hybrid-dev` delta.
- [Considerations](https://github.com/paulxiep/supeux/blob/main/docs/considerations.md) - ambiguity catalogue + dispositions (resolved 2026-05-17 for items A-K).
- [Abstraction v4 derivation](https://github.com/paulxiep/supeux/blob/main/docs/supeux_abstraction_v4.md) - current long-form derivation that produced the thesis (four-language re-derivation: primitive shapes, yaml schema, IaC choice, gotchas). Supersedes v3.
- [AWS service groups](https://github.com/paulxiep/supeux/blob/main/docs/aws_service_groups.md) - cost / latency / scale catalog of AWS services.
- [GCP service groups](https://github.com/paulxiep/supeux/blob/main/docs/gcp_service_groups.md) - GCP companion to the AWS catalog (cost / latency / scale, GCP-native role groupings).
- [Azure service groups](https://github.com/paulxiep/supeux/blob/main/docs/azure_service_groups.md) - Azure companion to the AWS catalog (cost / latency / scale, Azure-native role groupings including Service Bus, Cosmos DB, Microsoft Fabric, Logic Apps).
- [Cloud providers](https://github.com/paulxiep/supeux/blob/main/docs/cloud_providers.md) - cross-provider primitive mapping (AWS / GCP / Azure) and divergences from the AWS baseline.
- Python ecosystem evidence: [mapping AWS→Python](https://github.com/paulxiep/supeux/blob/main/docs/mapping_aws_to_python.md) · [mapping Python→AWS](https://github.com/paulxiep/supeux/blob/main/docs/mapping_python_to_aws.md) · [Python API diffs](https://github.com/paulxiep/supeux/blob/main/docs/python_api_diffs.md).
- Rust ecosystem evidence: [mapping AWS→Rust](https://github.com/paulxiep/supeux/blob/main/docs/mapping_aws_to_rust.md) · [mapping Rust→AWS](https://github.com/paulxiep/supeux/blob/main/docs/mapping_rust_to_aws.md) · [Rust API diffs](https://github.com/paulxiep/supeux/blob/main/docs/rust_api_diffs.md).
- TypeScript ecosystem evidence: [mapping AWS→TypeScript](https://github.com/paulxiep/supeux/blob/main/docs/mapping_aws_to_typescript.md) · [mapping TypeScript→AWS](https://github.com/paulxiep/supeux/blob/main/docs/mapping_typescript_to_aws.md) · [TypeScript API diffs](https://github.com/paulxiep/supeux/blob/main/docs/typescript_api_diffs.md).
- Go ecosystem evidence: [mapping AWS→Go](https://github.com/paulxiep/supeux/blob/main/docs/mapping_aws_to_go.md) · [mapping Go→AWS](https://github.com/paulxiep/supeux/blob/main/docs/mapping_go_to_aws.md) · [Go API diffs](https://github.com/paulxiep/supeux/blob/main/docs/go_api_diffs.md).
- [Abstraction v3](https://github.com/paulxiep/supeux/blob/main/docs/supeux_abstraction_v3.md) - prior long-form derivation, Python + Rust + TypeScript. Historical record.
- [Abstraction v2](https://github.com/paulxiep/supeux/blob/main/docs/supeux_abstraction_v2.md) - prior long-form derivation, Python + Rust only. Historical record.
- [Abstraction v1](https://github.com/paulxiep/supeux/blob/main/docs/supeux_abstraction_v1.md) - prior framing built around a Python SDK. Historical record.

## Status

Pre-implementation. The thesis is load-bearing; everything in the companion docs is current evaluation that may shift. Implementation roadmap will land once v1 scope is locked.
