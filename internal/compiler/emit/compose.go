package emit

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/paulxiep/caravan/internal/compiler"
)

// Per-seam TCP port for HTTP-mode peer services. PoC default; per-seam
// overrides land later if needed.
const seamServerPort = 8080

// Shared-secret placeholder injected into both the consumer entry's
// env and the peer service's env. Matches B0's hand-edit value.
// M7 replaces this with a compiler-derived secret (D7 in the dev plan).
const sharedSecretPlaceholder = "dev-secret-placeholder"

// AppProfile is the docker-compose `profiles:` value caravan emit attaches
// to every entry, peer-service, and resource container (compose.go,
// resources.go). `caravan up` / `caravan down` activates it with
// `--profile <AppProfile>` (cmd/caravan/up.go's buildComposeArgs).
// Exported so producer (emit) and consumer (cmd) reference a single
// source of truth — flipping this constant moves both sides in lockstep.
const AppProfile = "app"

// EmitComposeOverride builds the docker-compose override yaml for one
// resolved target. It's layered on top of the user's hand-authored
// docker-compose.yaml — adds CARAVAN_RPC_PEERS to consumer entries and
// spawns a peer service per container-mode seam.
//
// `userRepoName` is the base name of the user's repository (e.g.
// "code-rag" or "invoice-parse"); the Rust peer-service `build:`
// directive uses `<userRepoName>/infra/...` as the dockerfile path
// because the compose build context is the parent of the user repo.
// When set to "" the prefix is omitted (back-compat for callers that
// don't need the prefix, e.g. invoice-parse's Python path).
//
// `baseComposeServices` is the set of service names already declared
// in the user's hand-authored compose. M4 uses it to skip emitting
// resource containers (Postgres, Redis, MinIO, RabbitMQ, OpenSearch)
// when the same name already exists — resource env vars still flow
// into the consumer regardless. Pass nil / empty map when no
// collision detection is desired (emit-everything mode).
//
// The output is yaml encoded via yaml.Node for stable key order
// (golden-file tests would flake on Go map randomization otherwise).
func EmitComposeOverride(rp *compiler.ResolvedPlan, userRepoName string, baseComposeServices map[string]bool) ([]byte, error) {
	if rp == nil || rp.Plan == nil {
		return nil, fmt.Errorf("nil ResolvedPlan")
	}
	target := rp.Plan.Targets[rp.TargetName]
	if target == nil {
		return nil, fmt.Errorf("target %q not in plan", rp.TargetName)
	}

	acc := newComposeAccumulator()

	// 1. Consumer entries: inject CARAVAN_RPC_PEERS + the shared secret
	//    via the accumulator's seam-source band.
	for _, entryName := range sortedKeys(target.Entries) {
		envVars := rp.EnvVars[entryName]
		if len(envVars) == 0 {
			continue
		}
		if err := addConsumerSeamEnv(acc, entryName, envVars); err != nil {
			return nil, err
		}
	}

	// 2. Resource env vars (M4): fold endpoint URLs / credentials into
	//    consumers that declared `uses:` for a resolved resource.
	//    Tagged envSourceResource so the accumulator flushes them
	//    before the seam-source band.
	if err := emitResourceEnvVars(acc, rp); err != nil {
		return nil, err
	}

	// 3. Container-mode seams: emit a peer service per seam.
	for _, seamName := range sortedKeys(target.Seams) {
		mode := target.Seams[seamName]
		if mode != compiler.SeamContainer {
			continue
		}
		seam := rp.Plan.Seams[seamName]
		if seam == nil {
			return nil, fmt.Errorf("targets.%s.seams references unknown seam %q (should have been caught in Normalize)", target.Name, seamName)
		}
		svc, err := buildSeamPeerService(seam, rp, userRepoName)
		if err != nil {
			return nil, err
		}
		acc.AddService(svc.Name, svc)
	}

	// 4. Resource containers (M4): emit OSS-local containers for each
	//    resolved resource — but only when the base compose doesn't
	//    already publish a same-named service.
	if err := emitResources(acc, rp, baseComposeServices); err != nil {
		return nil, err
	}

	// 5. M4-cloud: when the target opts into hybrid-dev mode, mount the
	//    host's `~/.aws` into every consumer + peer service (so local
	//    containers can authenticate to real AWS via the developer's
	//    profile) and inject AWS_REGION + AWS_PROFILE env vars. Skipped
	//    for non-hybrid targets — the existing oss-local Phase-1 flow.
	if target.CredsPassthrough {
		if err := emitCredsPassthrough(acc, target); err != nil {
			return nil, err
		}
	}

	return acc.Render(target.Name, rp.Plan.OutputDir)
}

// emitCredsPassthrough adds the `~/.aws` mount + AWS_REGION + AWS_PROFILE
// to every service already in the accumulator. M4-cloud only.
//
// Mount path: absolute, computed at emit time via os.UserHomeDir(). The
// generated compose file is a per-developer dev override (uncommitted),
// so encoding the absolute path keeps `${HOME}` / `~` ambiguities out of
// the runtime. Windows Docker Desktop accepts both forward and back
// slashes; we emit forward slashes for cross-platform yaml friendliness.
func emitCredsPassthrough(acc *composeAccumulator, target *compiler.Target) error {
	home, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("creds_passthrough: resolve home dir: %w", err)
	}
	awsHostDir := filepath.ToSlash(filepath.Join(home, ".aws"))
	mount := awsHostDir + ":/root/.aws:ro"

	// Iterate by name (acc.order) for determinism. We touch every
	// already-emitted service: consumer entries (added by addConsumerSeamEnv
	// / emitResourceEnvVars) and seam peer services (added by AddService
	// in step 3). Resource containers are skipped because cloud-managed
	// resources never emit a container at all (emit/resources.go:72).
	for _, name := range acc.order {
		// Mount via AddService merge — mergeService's volume branch
		// dedupes via set semantics.
		acc.AddService(name, composeService{Volumes: []string{mount}})

		// AWS_REGION and AWS_PROFILE land in the seam-source band so
		// they don't collide with the resource-source endpoint vars
		// (S3_BUCKET, DATABASE_URL, etc.). Both are reserved namespace
		// markers for cloud auth — not the CARAVAN_RPC_ prefix the
		// resource-source guard checks.
		if err := acc.AddEnv(name, "AWS_REGION", target.Region, envSourceSeam); err != nil {
			return err
		}
		if err := acc.AddEnv(name, "AWS_PROFILE", target.AwsProfile, envSourceSeam); err != nil {
			return err
		}
	}
	return nil
}

// --- service builders -------------------------------------------------------

// composeService is one entry's worth of docker-compose `services:`
// content. We use this intermediate shape (vs raw yaml.Node trees)
// because the field order is fixed across all services — easier to
// keep deterministic.
type composeService struct {
	Name string
	// Image is the pre-built image reference (e.g. "minio/minio:latest").
	// Mutually exclusive with Build at render time — resource containers
	// (M4) set Image; consumer overrides + seam peers (M1/M2) set Build.
	Image       string
	Build       *composeBuild
	EnvFile     []string
	Environment []composeEnvKV
	Command     []string
	DependsOn   []composeDependsOn
	Profiles    []string
	// Ports is a list of `"host:container"` mappings rendered under
	// compose's `ports:` key. Used by resource containers (M4) so the
	// user can reach Postgres / Redis / MinIO from the host.
	Ports []string
	// Volumes is the compose `volumes:` list. M4-cloud injects the
	// host's `~/.aws` here on every consumer + peer service when the
	// target sets `creds_passthrough: true`, so the containers can
	// authenticate to real AWS via the developer's profile.
	Volumes []string
}

type composeBuild struct {
	Context    string
	Dockerfile string
	Target     string // optional; selects a stage in a multi-stage Dockerfile
	// Args are docker build-args passed to BuildKit. Used to inject
	// CARAVAN_TARGET so multi-stage Dockerfiles whose stages reference
	// `infra/${CARAVAN_TARGET}/...` resolve cleanly — buildkit walks
	// every stage's COPY paths when computing cache keys, even stages
	// not selected by `target:`, so the path must exist regardless.
	Args []composeBuildArg
}

type composeBuildArg struct {
	Key, Value string
}

type composeEnvKV struct {
	Key, Value string
}

type composeDependsOn struct {
	Service   string
	Condition string
}

// addConsumerSeamEnv contributes the seam-side env vars
// (CARAVAN_RPC_PEERS, CARAVAN_RPC_SHARED_SECRET) and depends_on edges
// for a consumer entry to the accumulator. M4 contributes resource-side
// env vars to the same consumer via acc.AddEnv(_, _, _, envSourceResource).
func addConsumerSeamEnv(acc *composeAccumulator, entryName string, envVars map[string]string) error {
	if err := acc.AddEnv(entryName, "CARAVAN_RPC_PEERS", envVars["CARAVAN_RPC_PEERS"], envSourceSeam); err != nil {
		return err
	}
	if err := acc.AddEnv(entryName, "CARAVAN_RPC_SHARED_SECRET", sharedSecretPlaceholder, envSourceSeam); err != nil {
		return err
	}
	// depends_on: every peer service named in the peer-table URLs.
	deps := composeService{}
	for _, peerHost := range peerHostsFromEnv(envVars["CARAVAN_RPC_PEERS"]) {
		deps.DependsOn = append(deps.DependsOn, composeDependsOn{
			Service:   peerHost,
			Condition: "service_started",
		})
	}
	if len(deps.DependsOn) > 0 {
		acc.AddService(entryName, deps)
	}
	return nil
}

// buildSeamPeerService emits the new service for a container-mode seam.
//
// Two shapes depending on language:
//   - Python: reuse the user's image (build context `..`, user's
//     Dockerfile) with an overridden `command:` running
//     `python -m caravan_rpc.serve --interface X --impl Y --port N`.
//   - Rust: build a fresh image from the caravan-generated synthetic
//     peer Dockerfile (one per seam, lives in
//     `infra/<target>/generated/peers/<service>/Dockerfile`). The
//     synthetic binary is its own entrypoint — no command override.
//
// `userRepoName` is the user repo's directory name; the Rust path
// prefixes it onto the dockerfile reference so docker compose can
// resolve from the build context (which is the user repo's parent).
// `rp` is needed by the Rust branch to pick the host entry whose
// image the peer reuses.
func buildSeamPeerService(seam *compiler.Seam, rp *compiler.ResolvedPlan, userRepoName string) (composeService, error) {
	lang := detectLanguage(seam)
	switch lang {
	case compiler.LanguagePython:
		return buildPythonPeerService(seam, rp)
	case compiler.LanguageRust:
		return buildRustPeerService(seam, rp, userRepoName), nil
	default:
		return composeService{}, fmt.Errorf("seam %q: unsupported impl language %q (impl=%q)", seam.Name, lang, seam.Impl)
	}
}

// buildPythonPeerService keeps the existing M1 shape: reuse user's
// image + command override invoking `python -m caravan_rpc.serve`.
//
// Build stage selection: when the host entry declares `runtime_target:`
// (e.g. invoice-parse's processing stage), the peer's compose `build.target`
// is set to that stage. Required when the user's Dockerfile is multi-stage
// (M7 added a `lambda-slim` stage to invoice-parse's processing Dockerfile);
// without an explicit target docker defaults to the LAST stage, which for
// lambda-slim attempts `COPY infra/prod-mixed/...` and fails on compose
// targets. Mirrors buildRustPeerService's hostEntry.RuntimeTarget usage.
func buildPythonPeerService(seam *compiler.Seam, rp *compiler.ResolvedPlan) (composeService, error) {
	emitter, ok := SeamServerCommands[compiler.LanguagePython]
	if !ok {
		return composeService{}, fmt.Errorf("seam %q: no SeamServerCommand for Python (internal bug)", seam.Name)
	}
	cmd, err := emitter.Command(seam, seamServerPort)
	if err != nil {
		return composeService{}, fmt.Errorf("seam %q: %w", seam.Name, err)
	}
	build := &composeBuild{
		Context:    "..",
		Dockerfile: seam.Dockerfile,
		// CARAVAN_TARGET is read by every multi-stage Dockerfile-author
		// who templates `infra/${CARAVAN_TARGET}/...` COPY paths. Inject
		// the current target name so all stages resolve their context
		// paths against the right per-target generated/ tree — buildkit
		// rejects cache-key computation when ANY stage references a
		// nonexistent path, even stages not selected by `target:`.
		Args: []composeBuildArg{{Key: "CARAVAN_TARGET", Value: rp.TargetName}},
	}
	// Multi-stage Dockerfile target selection: prefer the entry whose
	// `path:` matches the seam's `path:` (the entry that hosts this
	// seam's code), and use its `runtime_target:`. Falls back to
	// pickHostEntry's alphabetically-first container entry when no
	// path-match exists (single-entry repos via defaultSeamPaths).
	if hostEntry := pickSeamHostEntry(rp, seam); hostEntry != nil && hostEntry.RuntimeTarget != "" {
		build.Target = hostEntry.RuntimeTarget
	}
	svc := composeService{
		Name:  seam.ServiceName,
		Build: build,
		Environment: []composeEnvKV{
			{Key: "CARAVAN_RPC_SHARED_SECRET", Value: sharedSecretPlaceholder},
			{Key: "CARAVAN_RPC_PEERS", Value: rp.PeersJSON},
		},
		Command:  cmd,
		Profiles: []string{AppProfile},
	}
	// env_file resolution: per-seam override wins; otherwise inherit the
	// host entry's env_file. The peer reuses the host entry's image so it
	// has the same env-var shape at startup (model selection, API keys,
	// etc.) — without inheriting, peers crash on missing user-config env
	// vars that the entry takes for granted.
	if seam.EnvFile != "" {
		svc.EnvFile = []string{seam.EnvFile}
	} else if hostEntry := pickSeamHostEntry(rp, seam); hostEntry != nil && hostEntry.EnvFile != "" {
		svc.EnvFile = []string{hostEntry.EnvFile}
	}
	return svc, nil
}

// buildRustPeerService points at the caravan-generated synthetic peer
// Dockerfile. The build context is the parent of the user repo (so the
// peer crate + caravan-rpc + impl crate are all reachable from one
// COPY). The Dockerfile path is the caravan-generated one inside
// `infra/<target>/generated/peers/<service>/Dockerfile` — relative to
// the build context that's `<repo-name>/infra/<target>/...`.
func buildRustPeerService(seam *compiler.Seam, rp *compiler.ResolvedPlan, userRepoName string) composeService {
	// Path B (2026-05-21): the peer service reuses the consumer entry's
	// image as-is. The chat binary's `main()` is expected to wrap its
	// app startup in `caravan_rpc::run_or_serve`, which detours into
	// peer mode based on `CARAVAN_RPC_ROLE=peer-<Interface>`. No CMD
	// override, no separate binary, no workspace.members surgery.
	//
	// Resolve which entry hosts the peer: M2 PoC picks the first
	// container-mode entry in the target (typical case: single-entry
	// repos like code-rag). Multi-entry repos can override per-seam
	// via `seams.<X>.host_entry` post-PoC.
	hostEntry := pickHostEntry(rp)
	dockerfilePath := hostEntry.Dockerfile
	if dockerfilePath == "" {
		dockerfilePath = "Dockerfile"
	}
	build := &composeBuild{
		Context:    "..",
		Dockerfile: fmt.Sprintf("./%s/%s", userRepoName, dockerfilePath),
		Target:     hostEntry.RuntimeTarget,
	}

	role := "peer-" + seam.Name

	// Seam peer services share the unit's peer table so a peer can
	// dispatch outward to other seams in the same unit at runtime.
	// `rp.PeersJSON` is the single marshaled view set once in
	// Resolve(); peer services reference it directly.
	envs := []composeEnvKV{
		{Key: "CARAVAN_RPC_ROLE", Value: role},
		{Key: "CARAVAN_RPC_SHARED_SECRET", Value: sharedSecretPlaceholder},
		{Key: "CARAVAN_RPC_PEERS", Value: rp.PeersJSON},
	}

	svc := composeService{
		Name:        seam.ServiceName,
		Build:       build,
		Environment: envs,
		// No `command:` — the chat binary's default CMD handles the
		// role switch via `run_or_serve`.
		Profiles: []string{AppProfile},
	}
	// env_file resolution: per-seam override wins; otherwise inherit
	// from the host entry. The peer runs the SAME binary as the
	// consumer, so its startup env-var needs are the same (AppState
	// init calls provide() for every impl, each of which may read
	// env vars). Inheriting the entry's env_file avoids per-seam
	// declarations for the common case.
	switch {
	case seam.EnvFile != "":
		svc.EnvFile = []string{seam.EnvFile}
	case hostEntry.EnvFile != "":
		svc.EnvFile = []string{hostEntry.EnvFile}
	}
	return svc
}

// pickSeamHostEntry returns the entry that hosts the seam's code in this
// target. Resolution order:
//
//  1. Path-match: the alphabetically-first container-mode entry whose
//     `path:` equals the seam's `path:`. This is the right choice for
//     multi-entry repos where each entry has its own runtime_target
//     (invoice-parse: seams.X.path = services/processing → entries.processing).
//  2. Fallback: pickHostEntry's first-container-entry heuristic. Sufficient
//     for single-entry repos where seam.Path was defaulted to the entry's
//     path by defaultSeamPaths (code-rag).
//
// Returns nil only when neither resolution succeeds — callers treat that
// as "no runtime_target available" and use the Dockerfile's default
// (last) stage.
func pickSeamHostEntry(rp *compiler.ResolvedPlan, seam *compiler.Seam) *compiler.Entry {
	if rp == nil || rp.Plan == nil || seam == nil {
		return nil
	}
	target := rp.Plan.Targets[rp.TargetName]
	if target == nil {
		return nil
	}
	if seam.Path != "" {
		for _, entryName := range sortedKeys(target.Entries) {
			if target.Entries[entryName] != compiler.EntryContainer {
				continue
			}
			entry := rp.Plan.Entries[entryName]
			if entry != nil && entry.Path == seam.Path {
				return entry
			}
		}
	}
	return pickHostEntry(rp)
}

// pickHostEntry returns the entry whose image the Rust peers reuse.
// M2 PoC: the first (and usually only) container-mode entry in the
// target. Multi-entry repos can override post-PoC.
func pickHostEntry(rp *compiler.ResolvedPlan) *compiler.Entry {
	target := rp.Plan.Targets[rp.TargetName]
	for _, entryName := range sortedKeys(target.Entries) {
		if target.Entries[entryName] != compiler.EntryContainer {
			continue
		}
		if entry := rp.Plan.Entries[entryName]; entry != nil {
			return entry
		}
	}
	// No container entry — return a zero Entry; callers handle empty
	// Dockerfile/RuntimeTarget by using defaults.
	return &compiler.Entry{}
}

// peerHostsFromEnv extracts the host names from a CARAVAN_RPC_PEERS
// JSON string. Returns sorted, deduplicated, with only http-mode peers.
func peerHostsFromEnv(envValue string) []string {
	// Minimal targeted parse — the env string is well-formed JSON by
	// construction (resolve.go's marshalPeers), so a substring scan is
	// safe enough for M1. If we ever care about robustness, swap to
	// json.Unmarshal into map[string]compiler.PeerEntry.
	if envValue == "" {
		return nil
	}
	const urlMarker = `"url":"http://`
	seen := map[string]struct{}{}
	rest := envValue
	for {
		idx := strings.Index(rest, urlMarker)
		if idx < 0 {
			break
		}
		rest = rest[idx+len(urlMarker):]
		end := strings.Index(rest, ":")
		if end < 0 {
			break
		}
		host := rest[:end]
		seen[host] = struct{}{}
		rest = rest[end:]
	}
	out := make([]string, 0, len(seen))
	for h := range seen {
		out = append(out, h)
	}
	return sortedStrings(out)
}

// --- rendering --------------------------------------------------------------

// renderCompose serializes a list of composeService into yaml using
// yaml.Node for stable key order. The header comment is conservative
// — it labels the file as generated and warns against hand-editing.
func renderCompose(targetName, outputDir string, services []composeService) ([]byte, error) {
	doc := &yaml.Node{Kind: yaml.DocumentNode}
	doc.HeadComment = composeHeaderComment(targetName, outputDir)

	root := &yaml.Node{Kind: yaml.MappingNode}
	doc.Content = []*yaml.Node{root}

	servicesNode := &yaml.Node{Kind: yaml.MappingNode}
	for _, svc := range services {
		servicesNode.Content = append(servicesNode.Content,
			scalarNode(svc.Name),
			serviceNode(svc),
		)
	}
	root.Content = append(root.Content, scalarNode("services"), servicesNode)

	var buf bytes.Buffer
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2)
	if err := enc.Encode(doc); err != nil {
		return nil, fmt.Errorf("encode compose yaml: %w", err)
	}
	if err := enc.Close(); err != nil {
		return nil, fmt.Errorf("close yaml encoder: %w", err)
	}
	return buf.Bytes(), nil
}

func composeHeaderComment(targetName, outputDir string) string {
	return fmt.Sprintf(
		" Generated by `caravan compile --target=%s`. Do not edit by hand.\n"+
			" Layer this override atop the hand-authored docker-compose.yaml:\n"+
			"   docker compose \\\n"+
			"     -f infra/docker-compose.yaml \\\n"+
			"     -f %s/%s/generated/docker-compose.override.generated.yaml \\\n"+
			"     --profile app up",
		targetName, outputDir, targetName,
	)
}

// serviceNode builds the yaml.Node tree for one composeService. Field
// order: image → build → env_file → environment → command → ports →
// depends_on → profiles. Matches B0's hand-edited override for
// diff-friendliness in the consumer / seam-peer cases (which omit
// image + ports); image and ports are added before / after the
// existing band for resource containers (M4).
func serviceNode(svc composeService) *yaml.Node {
	out := &yaml.Node{Kind: yaml.MappingNode}
	add := func(key string, val *yaml.Node) {
		out.Content = append(out.Content, scalarNode(key), val)
	}
	if svc.Image != "" {
		add("image", scalarNode(svc.Image))
	}
	if svc.Build != nil {
		add("build", buildNode(svc.Build))
	}
	if len(svc.EnvFile) > 0 {
		add("env_file", stringListNode(svc.EnvFile))
	}
	if len(svc.Environment) > 0 {
		add("environment", envNode(svc.Environment))
	}
	if len(svc.Command) > 0 {
		// docker compose schema: each command-arg must be a string.
		// yaml.v3 emits numeric-looking strings unquoted (`8080` not
		// `"8080"`), which compose rejects. Force quoted style here.
		add("command", quotedListNode(svc.Command))
	}
	if len(svc.Ports) > 0 {
		// Force quoted style on port mappings — the `5432:5432` shape
		// looks numeric to the yaml emitter without it.
		add("ports", quotedListNode(svc.Ports))
	}
	if len(svc.Volumes) > 0 {
		// Volume mounts also use the `host:container` shape — same
		// quoting concern as ports.
		add("volumes", quotedListNode(svc.Volumes))
	}
	if len(svc.DependsOn) > 0 {
		add("depends_on", dependsOnNode(svc.DependsOn))
	}
	if len(svc.Profiles) > 0 {
		add("profiles", stringListNode(svc.Profiles))
	}
	return out
}

func buildNode(b *composeBuild) *yaml.Node {
	out := &yaml.Node{Kind: yaml.MappingNode}
	out.Content = []*yaml.Node{
		scalarNode("context"), scalarNode(b.Context),
		scalarNode("dockerfile"), scalarNode(b.Dockerfile),
	}
	if b.Target != "" {
		out.Content = append(out.Content, scalarNode("target"), scalarNode(b.Target))
	}
	if len(b.Args) > 0 {
		argsNode := &yaml.Node{Kind: yaml.MappingNode}
		for _, a := range b.Args {
			argsNode.Content = append(argsNode.Content, scalarNode(a.Key), scalarNode(a.Value))
		}
		out.Content = append(out.Content, scalarNode("args"), argsNode)
	}
	return out
}

func envNode(env []composeEnvKV) *yaml.Node {
	out := &yaml.Node{Kind: yaml.MappingNode}
	for _, kv := range env {
		out.Content = append(out.Content, scalarNode(kv.Key), scalarNode(kv.Value))
	}
	return out
}

func stringListNode(items []string) *yaml.Node {
	out := &yaml.Node{Kind: yaml.SequenceNode}
	for _, s := range items {
		out.Content = append(out.Content, scalarNode(s))
	}
	return out
}

// quotedListNode is stringListNode that forces DoubleQuotedStyle on
// every item. Used for command: argv arrays where docker compose's
// schema requires every entry to be a string.
func quotedListNode(items []string) *yaml.Node {
	out := &yaml.Node{Kind: yaml.SequenceNode}
	for _, s := range items {
		out.Content = append(out.Content, &yaml.Node{
			Kind:  yaml.ScalarNode,
			Style: yaml.DoubleQuotedStyle,
			Value: s,
		})
	}
	return out
}

func dependsOnNode(deps []composeDependsOn) *yaml.Node {
	out := &yaml.Node{Kind: yaml.MappingNode}
	for _, d := range deps {
		inner := &yaml.Node{Kind: yaml.MappingNode}
		inner.Content = []*yaml.Node{
			scalarNode("condition"), scalarNode(d.Condition),
		}
		out.Content = append(out.Content, scalarNode(d.Service), inner)
	}
	return out
}

func scalarNode(s string) *yaml.Node {
	return &yaml.Node{Kind: yaml.ScalarNode, Value: s}
}

// --- helpers ----------------------------------------------------------------

func sortedKeys[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return sortedStrings(out)
}

func sortedStrings(s []string) []string {
	// Avoid pulling in the sort package overhead repeatedly; tiny lists.
	for i := 1; i < len(s); i++ {
		for j := i; j > 0 && s[j-1] > s[j]; j-- {
			s[j-1], s[j] = s[j], s[j-1]
		}
	}
	return s
}
