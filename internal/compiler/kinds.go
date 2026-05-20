// Package compiler implements the caravan yaml-to-IR compiler pipeline.
//
// The pipeline has five phases per docs/ir.md:
//
//	Lex       []byte → RawYAML        (yaml.Node tree + source spans)
//	Parse     RawYAML → ParsedDoc     (typed structs, schema validation)
//	Normalize ParsedDoc → Plan        (cross-refs + defaults applied)
//	Resolve   Plan × TargetName       (compute CARAVAN_RPC_PEERS per deploy unit)
//	          → ResolvedPlan
//	Emit      ResolvedPlan → files    (docker-compose.yaml etc.)
//
// At M0 we land Lex → Resolve and stub Emit. M1 fills the Emit phase for
// the docker-compose override target. The IR struct shape here is the
// PoC's flatter `entries+seams` model (docs/poc_yaml_spec.md), not the
// fuller Module+Bundle two-layer model in docs/ir.md §1 — that's
// reserved for v1+.
package compiler

// ResourceKind is the value of `type:` in a resources.X block.
type ResourceKind string

const (
	ResourceQueue  ResourceKind = "queue"
	ResourceDBSQL  ResourceKind = "db.sql"
	ResourceBucket ResourceKind = "bucket"
	ResourceCache  ResourceKind = "cache"
	ResourceKV     ResourceKind = "kv"
	ResourceStream ResourceKind = "stream"
	ResourceSearch ResourceKind = "search"
	ResourceLLM    ResourceKind = "llm"
)

// allResourceKinds is the canonical set used for validation. Update
// this whenever a new ResourceKind constant is added.
var allResourceKinds = []ResourceKind{
	ResourceQueue, ResourceDBSQL, ResourceBucket, ResourceCache,
	ResourceKV, ResourceStream, ResourceSearch, ResourceLLM,
}

// IsValid reports whether k names a known resource kind.
func (k ResourceKind) IsValid() bool {
	for _, v := range allResourceKinds {
		if k == v {
			return true
		}
	}
	return false
}

// TriggerKind names the shape of an entry's trigger entry.
type TriggerKind string

const (
	TriggerHTTP   TriggerKind = "http"
	TriggerQueue  TriggerKind = "queue"
	TriggerCron   TriggerKind = "cron"
	TriggerStream TriggerKind = "stream"
)

var allTriggerKinds = []TriggerKind{
	TriggerHTTP, TriggerQueue, TriggerCron, TriggerStream,
}

// IsValid reports whether k names a known trigger kind.
func (k TriggerKind) IsValid() bool {
	for _, v := range allTriggerKinds {
		if k == v {
			return true
		}
	}
	return false
}

// RuntimeKind is the `runtime:` value on a target.
type RuntimeKind string

const (
	RuntimeDockerCompose RuntimeKind = "docker-compose"
	RuntimeAWS           RuntimeKind = "aws"
)

// IsValid reports whether r names a known runtime.
func (r RuntimeKind) IsValid() bool {
	return r == RuntimeDockerCompose || r == RuntimeAWS
}

// CompositionMode is the `composition:` value on a resource (or
// `default_composition:` on a target).
type CompositionMode string

const (
	CompositionOSSLocal     CompositionMode = "oss-local"
	CompositionCloudManaged CompositionMode = "cloud-managed"
	CompositionByID         CompositionMode = "by-id"
)

// IsValid reports whether c names a known composition mode.
func (c CompositionMode) IsValid() bool {
	return c == CompositionOSSLocal || c == CompositionCloudManaged || c == CompositionByID
}

// EntryDispatchMode is the per-target value of targets.X.entries[name].
type EntryDispatchMode string

const (
	EntryContainer EntryDispatchMode = "container"
	EntryLambda    EntryDispatchMode = "lambda"
	EntryBatch     EntryDispatchMode = "batch"
)

// IsValid reports whether m names a known entry dispatch mode.
func (m EntryDispatchMode) IsValid() bool {
	return m == EntryContainer || m == EntryLambda || m == EntryBatch
}

// SeamDispatchMode is the per-target value of targets.X.seams[name].
type SeamDispatchMode string

const (
	SeamInproc    SeamDispatchMode = "inproc"
	SeamContainer SeamDispatchMode = "container"
	SeamLambda    SeamDispatchMode = "lambda"
)

// IsValid reports whether m names a known seam dispatch mode.
func (m SeamDispatchMode) IsValid() bool {
	return m == SeamInproc || m == SeamContainer || m == SeamLambda
}
