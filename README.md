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

- [Thesis](docs/thesis.md) - primary scoping doc. Read first.
- [Abstraction v4 derivation](docs/supeux_abstraction_v4.md) - current long-form derivation that produced the thesis (four-language re-derivation: primitive shapes, yaml schema, IaC choice, gotchas). Supersedes v3.
- [AWS service groups](docs/aws_service_groups.md) - cost / latency / scale catalog of AWS services.
- [GCP service groups](docs/gcp_service_groups.md) - GCP companion to the AWS catalog (cost / latency / scale, GCP-native role groupings).
- [Azure service groups](docs/azure_service_groups.md) - Azure companion to the AWS catalog (cost / latency / scale, Azure-native role groupings including Service Bus, Cosmos DB, Microsoft Fabric, Logic Apps).
- [Cloud providers](docs/cloud_providers.md) - cross-provider primitive mapping (AWS / GCP / Azure) and divergences from the AWS baseline.
- Python ecosystem evidence: [mapping AWS→Python](docs/mapping_aws_to_python.md) · [mapping Python→AWS](docs/mapping_python_to_aws.md) · [Python API diffs](docs/python_api_diffs.md).
- Rust ecosystem evidence: [mapping AWS→Rust](docs/mapping_aws_to_rust.md) · [mapping Rust→AWS](docs/mapping_rust_to_aws.md) · [Rust API diffs](docs/rust_api_diffs.md).
- TypeScript ecosystem evidence: [mapping AWS→TypeScript](docs/mapping_aws_to_typescript.md) · [mapping TypeScript→AWS](docs/mapping_typescript_to_aws.md) · [TypeScript API diffs](docs/typescript_api_diffs.md).
- Go ecosystem evidence: [mapping AWS→Go](docs/mapping_aws_to_go.md) · [mapping Go→AWS](docs/mapping_go_to_aws.md) · [Go API diffs](docs/go_api_diffs.md).
- [Abstraction v3](docs/supeux_abstraction_v3.md) - prior long-form derivation, Python + Rust + TypeScript. Historical record.
- [Abstraction v2](docs/supeux_abstraction_v2.md) - prior long-form derivation, Python + Rust only. Historical record.
- [Abstraction v1](docs/supeux_abstraction_v1.md) - prior framing built around a Python SDK. Historical record.

## Status

Pre-implementation. The thesis is load-bearing; everything in the companion docs is current evaluation that may shift. Implementation roadmap will land once v1 scope is locked.
