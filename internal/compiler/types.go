package compiler

// Span is a source position in the yaml input. Captured from yaml.Node.
type Span struct {
	File string `json:"file,omitempty"`
	Line int    `json:"line,omitempty"`
	Col  int    `json:"col,omitempty"`
}

// Plan is the normalized in-memory IR of a caravan.yaml. Produced by
// phase 3 (Normalize). Targets, resources, entries, and seams are
// keyed by name; iteration order is alphabetical via sortedKeys() in
// resolve.go to keep ResolvedPlan output deterministic.
//
// PoC-narrowed shape: collapses docs/ir.md's Module + Bundle two-layer
// model into entries (top-level deploy units) + seams (synchronous
// abstraction boundaries) + per-target dispatch overrides.
type Plan struct {
	Name          string               `json:"name"`
	DefaultTarget string               `json:"default_target,omitempty"`
	// OutputDir is the per-target write root. `caravan compile
	// --target=<t>` writes its artifacts under <OutputDir>/<t>/generated/.
	// Yaml field: `output_dir`. Defaults at phase 3 (Normalize) to
	// "caravan-out" so the generated tree is namespaced and unlikely to
	// collide with a hand-authored `infra/` the user already owns.
	// Existing repos that want to keep the previous layout set
	// `output_dir: infra` explicitly.
	OutputDir     string               `json:"output_dir,omitempty"`
	Entries       map[string]*Entry    `json:"entries,omitempty"`
	Seams         map[string]*Seam     `json:"seams,omitempty"`
	Resources     map[string]*Resource `json:"resources,omitempty"`
	Secrets       map[string]*Secret   `json:"secrets,omitempty"`
	Targets       map[string]*Target   `json:"targets,omitempty"`
	// PatchedManifests lists per-target build-context manifest files
	// (e.g. requirements.txt) written by phase-5 emit/manifest.go. Empty
	// until EmitManifestPatches runs. Surfaced so the CLI can log what
	// it wrote alongside the compose override.
	PatchedManifests []string `json:"patched_manifests,omitempty"`
}

// Entry is a top-level deploy unit — a service the user runs.
type Entry struct {
	Name       string `json:"name"`
	Path       string `json:"path"`
	Dockerfile string `json:"dockerfile,omitempty"`
	// RuntimeTarget names the multi-stage Dockerfile stage to use as
	// the runtime image (compose `build.target:`). When the user's
	// Dockerfile has multiple targets (e.g. code-rag's `chat` /
	// `raptor`), this disambiguates which one Caravan should reuse —
	// both for the consumer service itself and for Rust peer services
	// that share the same image via `caravan_rpc::run_or_serve`.
	//
	// Optional; default behavior (empty) lets Docker pick the
	// Dockerfile's final stage.
	RuntimeTarget string `json:"runtime_target,omitempty"`
	// EnvFile is the compose `env_file:` value that gets injected into
	// the entry's container AND inherited by any Rust peer services
	// that reuse the entry's image. Same binary → same env-var needs
	// at startup, so peers pick up whatever the consumer entry uses by
	// default. Per-seam `seam.env_file` overrides for edge cases.
	//
	// Path is taken verbatim and interpreted by compose relative to
	// the project directory (where `docker compose` was invoked).
	EnvFile  string    `json:"env_file,omitempty"`
	Triggers []Trigger `json:"triggers,omitempty"`
	Uses     []string  `json:"uses,omitempty"`
	// Language is detected at phase-3 (Normalize) from the manifest
	// files present in Path: Cargo.toml → rust, pyproject.toml or
	// requirements.txt → python. Empty until validateEntryLanguages
	// runs; warns rather than errors when Path doesn't exist on disk
	// (test fixtures use synthetic paths).
	Language Language `json:"language,omitempty"`
	Span     Span     `json:"-"`
}

// Seam is a same-language synchronous abstraction boundary inside an
// entry's code. Per-target it may dispatch as inproc (no peer service)
// or container/lambda (peer service + HTTP/SigV4 dispatch).
type Seam struct {
	Name string `json:"name"`
	Path string `json:"path"`
	// Dockerfile is the image the peer service uses when this seam
	// dispatches as a container. Often the same as the consuming
	// entry's Dockerfile (the image already carries the impl code).
	Dockerfile string `json:"dockerfile,omitempty"`
	Uses       []string `json:"uses,omitempty"`
	// Impl is the impl-class reference the M1 compose-emitter passes
	// to the peer service's serve command. Python shape: "module:Class".
	// Other languages: TBD (M2+). Required when any target sets this
	// seam's dispatch to container or lambda; ignored otherwise.
	Impl string `json:"impl,omitempty"`
	// ServiceName, when set, overrides the default kebab-cased seam
	// name used as the peer service's compose service name. Default:
	// kebab-case of the seam name (LLMExtraction → llm-extraction).
	ServiceName string `json:"service_name,omitempty"`
	// EnvFile, when set, becomes the peer service's `env_file:`
	// directive in the emitted compose override. Path is taken
	// verbatim — interpreted by compose relative to the override
	// file's directory. invoice-parse's LLMExtraction sets this to
	// `../.env` so the peer inherits GEMINI_API_KEY; code-rag's
	// Embedder leaves it unset (no envvar deps).
	EnvFile string `json:"env_file,omitempty"`
	Span    Span   `json:"-"`
}

// Trigger is the shape that drives an entry's lifecycle. PoC supports
// http (long-running HTTP server), queue (consumer loop), cron, stream.
type Trigger struct {
	Kind   TriggerKind   `json:"kind"`
	HTTP   *HTTPTrigger  `json:"http,omitempty"`
	Queue  *QueueTrigger `json:"queue,omitempty"`
	Cron   *CronTrigger  `json:"cron,omitempty"`
	Stream *StreamTrigger `json:"stream,omitempty"`
	Span   Span          `json:"-"`
}

// HTTPTrigger declares an http endpoint on the entry.
type HTTPTrigger struct {
	Path   string `json:"path,omitempty"`
	Port   int    `json:"port,omitempty"`
	Public bool   `json:"public,omitempty"`
}

// QueueTrigger ties the entry to a queue resource.
type QueueTrigger struct {
	From string `json:"from"`
}

// CronTrigger fires the entry on a schedule.
type CronTrigger struct {
	Schedule string `json:"schedule"`
	Timezone string `json:"timezone,omitempty"`
}

// StreamTrigger ties the entry to a stream resource.
type StreamTrigger struct {
	From string `json:"from"`
}

// Resource is a data-plane primitive (queue, db, bucket, etc.).
type Resource struct {
	Name        string          `json:"name"`
	Type        ResourceKind    `json:"type"`
	Composition CompositionMode `json:"composition,omitempty"`
	// Variant picks the concrete OSS-local container choice for this
	// resource (e.g. queue → redis-streams vs rabbitmq). Empty means
	// "use the default variant for this resource type", which the
	// emit/resources.go catalog resolves at phase 5.
	Variant ResourceVariant `json:"kind,omitempty"`
	// User, Password, DBName carry explicit credentials / DB name for
	// resource kinds that need them (db.sql today; future bucket etc.).
	// Empty falls through to caravan-managed defaults; declaring them
	// in yaml makes the IR the single source of truth so neither the
	// user nor the compiler has to "guess" each other's defaults when a
	// hand-authored compose ships the engine container.
	User     string `json:"user,omitempty"`
	Password string `json:"password,omitempty"`
	DBName   string `json:"dbname,omitempty"`
	// Extra carries type-specific fields beyond type / composition /
	// kind (e.g. llm.task=chat). Kept as a raw map at the IR level;
	// the emitter validates per-type.
	Extra map[string]any `json:"extra,omitempty"`
	Span  Span           `json:"-"`
}

// Secret is a user-declared secret reference. PoC supports `from: env`
// for B0/M0/M1; ssm/secrets-manager land at M7.
type Secret struct {
	Name string `json:"name"`
	From string `json:"from"`
	Path string `json:"path,omitempty"`
	Span Span   `json:"-"`
}

// Target is a (packaging × placement × composition) point. Each
// target overrides the global defaults per-entry, per-seam, per-resource.
type Target struct {
	Name               string                       `json:"name"`
	Runtime            RuntimeKind                  `json:"runtime"`
	DefaultComposition CompositionMode              `json:"default_composition,omitempty"`
	Region             string                       `json:"region,omitempty"`
	Entries            map[string]EntryDispatchMode `json:"entries,omitempty"`
	Seams              map[string]SeamDispatchMode  `json:"seams,omitempty"`
	// Composition overrides per resource (resource-name → override).
	// Yaml accepts both scalar (`oss-local`) and object (`{ mode:
	// oss-local, kind: rabbitmq }`) form; both parse into the same
	// CompositionOverride. Empty fields fall through to the resource's
	// own declaration / target default / variant default at resolve.
	Composition map[string]*CompositionOverride `json:"composition,omitempty"`
	// CredsPassthrough enables M4-cloud's hybrid-dev mode: when true the
	// compose emitter mounts the developer's `~/.aws` into each
	// app-profile service and injects AWS_REGION/AWS_PROFILE; the HCL
	// emitter writes Terraform for cloud-managed resources alongside the
	// compose override. Yaml field: `creds_passthrough:`. False outside
	// M4-cloud targets.
	CredsPassthrough bool `json:"creds_passthrough,omitempty"`
	// AwsProfile names the local AWS profile the compose containers
	// authenticate with via the mounted ~/.aws. Yaml field:
	// `aws_profile:`. Defaults at phase 3 to "caravan-poc" per the
	// M4-cloud-prereq onboarding checklist when CredsPassthrough is set.
	AwsProfile string `json:"aws_profile,omitempty"`
	// Backend pins the Terraform state backend names for HCL emit. The
	// values live in caravan.yaml (not env vars or env-time discovery)
	// so the IR stays self-contained. Required when CredsPassthrough is
	// set; the M4-cloud-prereq onboarding checklist provides the values
	// (state bucket + DynamoDB lock table).
	Backend *BackendConfig `json:"backend,omitempty"`
	// VPC carries the network shape M4b Fargate targets emit (VPC + 2-AZ
	// subnets + IGW + single/HA NAT). Required for `runtime: fargate`.
	// Nil on docker-compose targets (no network emission).
	VPC *VPCConfig `json:"vpc,omitempty"`
	// CloudMapNamespace is the private DNS namespace registered for
	// Fargate-Fargate service discovery (D11 → Cloud Map). Defaults to
	// "<app>.local" at phase 3 when empty on a Fargate target. Ignored
	// on non-Fargate targets.
	CloudMapNamespace string `json:"cloud_map_namespace,omitempty"`
	// ECSClusterName overrides the default ECS cluster name on a Fargate
	// target. Defaults at phase 3 to "<app>-<target>" when empty.
	ECSClusterName string `json:"ecs_cluster_name,omitempty"`
	Span           Span   `json:"-"`
}

// EmitsHCL reports whether this target produces HCL output. True for
// any AWS-producing target: hybrid-dev (M4-cloud — compose containers
// authenticate via creds_passthrough), Fargate (M4b — ECS task
// placement), and Lambda (M7 — function placement). Stays false for
// pure docker-compose targets (no AWS shape at all).
//
// The signal is Backend != nil: every AWS-producing target requires a
// remote tofu state backend (validators enforce this upstream), and no
// pure-compose target carries one. Adding a new placement runtime means
// adding a validator that requires Backend — the EmitsHCL predicate
// itself stays unchanged.
func (t *Target) EmitsHCL() bool {
	if t == nil {
		return false
	}
	return t.Backend != nil
}

// VPCConfig pins the VPC shape M4b emits for a Fargate target. Single
// NAT is the v1 default; HA NAT (one per AZ) is flagged for v1.1 via
// `nat: ha`.
type VPCConfig struct {
	// CIDR is the VPC's IPv4 CIDR block. Defaults at phase 3 to
	// "10.0.0.0/16" when empty.
	CIDR string `json:"cidr,omitempty"`
	// NAT controls NAT gateway redundancy: "single" (one NAT, one AZ
	// public subnet) or "ha" (one per AZ). Defaults at phase 3 to
	// "single" — sufficient for staging targets; prod should set "ha".
	NAT  string `json:"nat,omitempty"`
	Span Span   `json:"-"`
}

// BackendConfig pins the S3+DynamoDB Terraform state backend for one
// cloud target. Used by HCL emit's backend.tf. The bucket and lock
// table are created out-of-band in M4-cloud-prereq; caravan reads
// these names from the user's caravan.yaml.
type BackendConfig struct {
	// Bucket is the S3 bucket holding the .tfstate file.
	Bucket string `json:"bucket"`
	// LockTable is the DynamoDB table backing tofu's state lock.
	LockTable string `json:"lock_table"`
	// Region is the AWS region the bucket + lock table live in. May
	// differ from the target's Region (state can live in one region
	// while resources land in another). Defaults to Target.Region at
	// emit time when empty.
	Region string `json:"region,omitempty"`
	// Key is the per-target state-file path inside the bucket. Defaults
	// to "<app>/<target>.tfstate" at phase 3 when empty.
	Key  string `json:"key,omitempty"`
	Span Span   `json:"-"`
}

// CompositionOverride is one target's per-resource override. Either
// or both fields may be empty; resolve.go layers it on top of the
// resource's own Composition + Variant fields.
type CompositionOverride struct {
	Mode    CompositionMode `json:"mode,omitempty"`
	Variant ResourceVariant `json:"kind,omitempty"`
	Span    Span            `json:"-"`
}

// ResolvedPlan is produced by phase 4 (Resolve). It carries the input
// Plan + computed env vars per deploy unit. M0 emits CARAVAN_RPC_PEERS
// only; M4 (Phase 1) adds resource resolution + per-consumer endpoint
// env vars (DATABASE_URL, REDIS_URL, etc.). IAM, networking, and
// secret resolution stay deferred (M4-cloud / M7).
type ResolvedPlan struct {
	Plan       *Plan                        `json:"plan"`
	TargetName string                       `json:"target"`
	// EnvVars carries the SDK-control-plane env vars per deploy unit
	// (CARAVAN_RPC_PEERS, etc.). Source = seam dispatch.
	EnvVars map[string]map[string]string `json:"env_vars"`
	// PeersJSON is the single marshaled CARAVAN_RPC_PEERS view shared
	// across every member of every seam-owning unit (entries + their
	// peer containers). Same value that lands in EnvVars[<entry>]
	// for seam-using entries; surfaced as a top-level field so peer
	// service emit can read it without re-scanning.
	PeersJSON string `json:"peers_json,omitempty"`
	// PeerTables is the per-deploy-unit CARAVAN_RPC_PEERS *map* before
	// JSON-stringification. Useful for callers (emitter, tests) that
	// want structured access; EnvVars contains the stringified form
	// that gets injected at runtime.
	PeerTables map[string]map[string]PeerEntry `json:"peer_tables"`
	// ResolvedResources carries each resource's resolved Composition +
	// Variant for this target (per-target overrides merged in). Keyed
	// by resource name. Empty when the plan declares no resources.
	ResolvedResources map[string]*ResolvedResource `json:"resolved_resources,omitempty"`
	// ResourceEnvVars carries the resource-endpoint env vars to inject
	// per consumer entry (`DATABASE_URL`, `REDIS_URL`, `S3_ENDPOINT_URL`,
	// etc.). Keyed by consumer entry name → env var name → value.
	// Source = resource composition. The emit-phase compose accumulator
	// (post-M3 refactor) tags these with SourceResource so they merge
	// deterministically with SourceSeam (CARAVAN_RPC_*) keys.
	ResourceEnvVars map[string]map[string]string `json:"resource_env_vars,omitempty"`
	// IAMGrants carries the per-entry IAM permission set the HCL emitter
	// needs (M4-cloud). Keyed by entry name → list of statements;
	// statements are deduped + sorted within an entry. Populated only
	// for entries that consume cloud-managed resources; nil otherwise.
	IAMGrants map[string][]IAMStatement `json:"iam_grants,omitempty"`
}

// IAMStatement is one resource-action grant tied to a specific resolved
// resource. ResourceRef is the resource name from the Plan IR (not the
// cloud-side ARN — HCL emit derives the ARN from the resource block via
// `aws_s3_bucket.X.arn` references). Actions are sorted so byte-stable
// HCL output is trivial.
type IAMStatement struct {
	// ResourceRef names the resource (Plan IR key) this grant applies to.
	ResourceRef string `json:"resource_ref"`
	// ResourceKind is the resolved resource's type (bucket / queue /
	// search / etc.). Carried alongside ResourceRef so HCL emit can
	// pick the right ARN expression without re-looking-up the resource.
	ResourceKind ResourceKind `json:"resource_kind"`
	// Actions is the sorted, deduped list of AWS API actions granted
	// (e.g. ["s3:GetObject", "s3:PutObject"]).
	Actions []string `json:"actions"`
}

// ResolvedResource is one resource's per-target resolved shape. Carries
// the effective Composition + Variant after layering per-target overrides
// on top of the resource's own declaration and applying type-defaults.
type ResolvedResource struct {
	Name        string          `json:"name"`
	Type        ResourceKind    `json:"type"`
	Composition CompositionMode `json:"composition"`
	Variant     ResourceVariant `json:"variant"`
	// User, Password, DBName carry the resolved credentials threaded
	// from the user-declared Resource.{User,Password,DBName}. Empty
	// when not declared; the endpoint emitter falls back to caravan
	// defaults in that case.
	User     string `json:"user,omitempty"`
	Password string `json:"password,omitempty"`
	DBName   string `json:"dbname,omitempty"`
}

// PeerEntry is one row in CARAVAN_RPC_PEERS — interface-name keyed.
// Per docs/poc_rpc_sdk.md:
//
//	inproc:    {"mode": "inproc"}
//	container: {"mode": "http",   "url": "http://<host>:<port>"}
//	lambda:    {"mode": "lambda", "function_url": "https://..."}
type PeerEntry struct {
	Mode        string `json:"mode"`
	URL         string `json:"url,omitempty"`
	FunctionURL string `json:"function_url,omitempty"`
}
