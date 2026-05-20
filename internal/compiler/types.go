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
	Entries       map[string]*Entry    `json:"entries,omitempty"`
	Seams         map[string]*Seam     `json:"seams,omitempty"`
	Resources     map[string]*Resource `json:"resources,omitempty"`
	Secrets       map[string]*Secret   `json:"secrets,omitempty"`
	Targets       map[string]*Target   `json:"targets,omitempty"`
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
	Span     Span      `json:"-"`
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
	// Extra carries type-specific fields (e.g. llm.task=chat). Kept
	// as a raw map at the IR level; the emitter validates per-type.
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
	// Composition overrides per resource (resource-name → mode).
	Composition map[string]CompositionMode `json:"composition,omitempty"`
	Span        Span                       `json:"-"`
}

// ResolvedPlan is produced by phase 4 (Resolve). It carries the input
// Plan + computed env vars per deploy unit. M0 emits CARAVAN_RPC_PEERS
// only; IAM, networking, and secret resolution are deferred (M4-cloud / M7).
type ResolvedPlan struct {
	Plan       *Plan                        `json:"plan"`
	TargetName string                       `json:"target"`
	EnvVars    map[string]map[string]string `json:"env_vars"`
	// PeerTables is the per-deploy-unit CARAVAN_RPC_PEERS *map* before
	// JSON-stringification. Useful for callers (emitter, tests) that
	// want structured access; EnvVars contains the stringified form
	// that gets injected at runtime.
	PeerTables map[string]map[string]PeerEntry `json:"peer_tables"`
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
