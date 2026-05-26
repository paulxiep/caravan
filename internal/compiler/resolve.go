package compiler

import (
	"encoding/json"
	"fmt"
	"sort"
)

// Resolve runs phase 4 on a normalized Plan for a given target.
//
// **Narrowed at M0** — produces only the per-deploy-unit
// CARAVAN_RPC_PEERS env var. IAM grant derivation, networking, secret
// resolution (beyond `from: env` passthrough) are deferred:
//
//	IAM         → M4-cloud
//	Networking  → M4-cloud (compose handles service-DNS implicitly)
//	Secret rotation / SigV4 → M7
//
// Returns a *ResolvedPlan plus diagnostics. The Plan field of the
// returned ResolvedPlan is the same pointer as the input — no copy.
func Resolve(plan *Plan, targetName string, diag *Diagnostics) *ResolvedPlan {
	if plan == nil {
		return nil
	}
	target, ok := plan.Targets[targetName]
	if !ok {
		diag.Error(Span{}, "unknown target %q (declared targets: %v)", targetName, sortedKeys(plan.Targets))
		return nil
	}

	peerTable := buildPeerTable(plan, target, diag)
	stringified := marshalPeers(peerTable)

	rp := &ResolvedPlan{
		Plan:       plan,
		TargetName: targetName,
		PeerTables: map[string]map[string]PeerEntry{},
		EnvVars:    map[string]map[string]string{},
		PeersJSON:  stringified,
	}
	// Caravan's "unit" = an entry's deploy bundle: the entry process
	// plus any seam peer containers spawned from seams whose code
	// module lives inside the entry (same `path:`). Every member of
	// a unit shares the peer table so any of them can dispatch
	// outward via the SDK. Entries that own no seams form
	// single-process units with no peer-table needs (e.g. a Rust
	// queue-consumer alongside a Python service with seams) — they
	// must NOT carry a peer table because their compose profiles
	// often exclude the peer services, and a spurious `depends_on`
	// edge from peer-table parsing would invalidate `docker compose
	// --profile <X>` on those units.
	for du := range entriesUsingSeams(plan) {
		rp.PeerTables[du] = peerTable
		rp.EnvVars[du] = map[string]string{
			"CARAVAN_RPC_PEERS": stringified,
		}
		// TODO(M7): CARAVAN_RPC_SHARED_SECRET injection per yaml.
	}
	// M4: resolve resource composition + variant per target, then
	// derive per-consumer endpoint env vars from each resource's
	// (Type, Variant). Empty when the plan declares no resources.
	rp.ResolvedResources = resolveResources(plan, target)
	rp.ResourceEnvVars = buildResourceEnvVars(plan, rp.ResolvedResources)
	// M4-cloud: derive IAM grants per entry from `uses:` + `triggers:`.
	// M7 also adds lambda:InvokeFunctionUrl per Lambda seam the entry
	// calls. Only populated for entries with at least one grant.
	rp.IAMGrants = resolveIAMGrants(plan, rp.ResolvedResources, target)
	return rp
}

// --- resource resolution (M4) -----------------------------------------------

// resolveResources walks plan.Resources and produces the per-target
// resolved shape. Composition layering:
//
//	target.Composition[name].Mode   > resource.Composition > target.DefaultComposition
//	target.Composition[name].Variant > resource.Variant     > DefaultVariantFor(Type)
//
// Output is a map keyed by resource name; values are pointers so the
// emit phase can mutate without disturbing the input plan. Returns
// nil when there are no resources (keeps ResolvedPlan JSON tidy).
func resolveResources(plan *Plan, target *Target) map[string]*ResolvedResource {
	if len(plan.Resources) == 0 {
		return nil
	}
	out := map[string]*ResolvedResource{}
	for _, name := range sortedKeys(plan.Resources) {
		r := plan.Resources[name]
		if r == nil {
			continue
		}
		out[name] = &ResolvedResource{
			Name:        r.Name,
			Type:        r.Type,
			Composition: resolveComposition(r, target),
			Variant:     resolveVariant(r, target),
			User:        r.User,
			Password:    r.Password,
			DBName:      r.DBName,
		}
	}
	return out
}

// resolveComposition picks the effective composition for a resource
// given its declaration and the target's overrides.
func resolveComposition(r *Resource, target *Target) CompositionMode {
	if override, ok := target.Composition[r.Name]; ok && override != nil && override.Mode != "" {
		return override.Mode
	}
	if r.Composition != "" {
		return r.Composition
	}
	if target.DefaultComposition != "" {
		return target.DefaultComposition
	}
	return CompositionOSSLocal
}

// resolveVariant picks the effective variant for a resource given its
// declaration, the target's override, and the type's default.
func resolveVariant(r *Resource, target *Target) ResourceVariant {
	if override, ok := target.Composition[r.Name]; ok && override != nil && override.Variant != "" {
		return override.Variant
	}
	if r.Variant != "" {
		return r.Variant
	}
	return DefaultVariantFor(r.Type)
}

// buildResourceEnvVars walks each entry's Uses[] declarations and
// computes the per-consumer endpoint env vars. Empty when no consumer
// uses a resource that has Phase-1 OSS-local endpoints.
//
// Sort order: resources sorted alphabetically, env-var keys sorted
// alphabetically within each resource. The emit phase relies on this
// determinism for byte-stable compose output.
func buildResourceEnvVars(plan *Plan, resolved map[string]*ResolvedResource) map[string]map[string]string {
	if len(resolved) == 0 || len(plan.Entries) == 0 {
		return nil
	}
	out := map[string]map[string]string{}
	for _, entryName := range sortedKeys(plan.Entries) {
		e := plan.Entries[entryName]
		if e == nil || len(e.Uses) == 0 {
			continue
		}
		envs := map[string]string{}
		for _, refName := range e.Uses {
			rr := resolved[refName]
			if rr == nil {
				continue // not a resource (could be a secret); skip
			}
			for k, v := range EndpointEnvVars(rr) {
				envs[k] = v
			}
		}
		if len(envs) > 0 {
			out[entryName] = envs
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// entriesUsingSeams returns the set of entry names that own at least
// one seam — i.e. whose code module's path matches at least one
// seam.Path. Membership in this set scopes peer-table emission so
// no-seam entries (e.g. a Rust ingest CLI) don't carry CARAVAN_RPC_PEERS
// + spurious depends_on edges to peer services they never call.
func entriesUsingSeams(plan *Plan) map[string]bool {
	seamPaths := map[string]bool{}
	for _, s := range plan.Seams {
		seamPaths[s.Path] = true
	}
	out := map[string]bool{}
	for _, e := range plan.Entries {
		if e.Path != "" && seamPaths[e.Path] {
			out[e.Name] = true
		}
	}
	return out
}

// --- peer table construction ------------------------------------------------

// buildPeerTable returns the seam → PeerEntry table for this target.
// Keyed by seam name (the interface), independent of which deploy unit
// is consuming it.
func buildPeerTable(plan *Plan, target *Target, diag *Diagnostics) map[string]PeerEntry {
	peers := map[string]PeerEntry{}
	for _, seamName := range sortedKeys(plan.Seams) {
		peers[seamName] = peerEntryFor(plan.Seams[seamName], target, diag)
	}
	return peers
}

// peerEntryFor returns the PeerEntry that describes how to reach the
// given seam in the given target. Unset → inproc (the SDK's default
// per docs/poc_rpc_sdk.md).
func peerEntryFor(seam *Seam, target *Target, diag *Diagnostics) PeerEntry {
	mode := target.Seams[seam.Name]
	if mode == "" {
		mode = SeamInproc
	}
	if builder, ok := peerBuilders[mode]; ok {
		return builder(seam, target, diag)
	}
	panic(fmt.Sprintf("unhandled SeamDispatchMode: %q", mode))
}

// peerBuilders maps a dispatch mode to the function that produces its
// PeerEntry. Adding a new mode is one entry here + one constant in
// kinds.go + one case in the mode's `IsValid`.
//
// Builders take the target so they can vary the dispatch URL by runtime
// — e.g. `mode: container` produces a compose hostname on a
// docker-compose target and a Cloud Map FQDN on a Fargate target.
var peerBuilders = map[SeamDispatchMode]func(*Seam, *Target, *Diagnostics) PeerEntry{
	SeamInproc:    buildInprocPeer,
	SeamContainer: buildContainerPeer,
	SeamLambda:    buildLambdaPeer,
}

func buildInprocPeer(_ *Seam, _ *Target, _ *Diagnostics) PeerEntry {
	return PeerEntry{Mode: "inproc"}
}

// buildContainerPeer produces the URL for a `mode: container` seam.
// Compose targets: bare hostname (`http://embedder:8080`) — docker's
// embedded DNS resolves service names within the compose network.
// Fargate targets: Cloud Map FQDN (`http://embedder.code-rag.local:8080`)
// — ECS auto-registers the task in the target's private DNS namespace
// so other tasks resolve the name to the peer's private IP.
func buildContainerPeer(seam *Seam, target *Target, _ *Diagnostics) PeerEntry {
	host := seam.ServiceName
	if host == "" {
		host = kebabCase(seam.Name) // defaulted in Normalize; defensive
	}
	if target != nil && target.Runtime == RuntimeFargate {
		ns := target.CloudMapNamespace
		if ns != "" {
			host = host + "." + ns
		}
	}
	return PeerEntry{
		Mode: "http",
		URL:  fmt.Sprintf("http://%s:8080", host),
	}
}

func buildLambdaPeer(seam *Seam, _ *Target, _ *Diagnostics) PeerEntry {
	// The Function URL doesn't exist until tofu apply creates the Lambda.
	// Emit a Terraform interpolation reference that hclLiteralFromJSON
	// passes through to the HCL string literal; tofu apply substitutes the
	// real URL into CARAVAN_RPC_PEERS at deploy time. The local name must
	// match what compute_lambda.go emits (terraformLocalName-style).
	return PeerEntry{
		Mode:        "lambda",
		FunctionURL: "${aws_lambda_function_url." + terraformLocalName(seam.Name) + ".function_url}",
	}
}

// terraformLocalName must match emit/hcl/naming.go's terraformLocalName.
// Inlined here to avoid an internal/compiler → emit/hcl import cycle.
func terraformLocalName(s string) string {
	if s == "" {
		return ""
	}
	out := make([]byte, 0, len(s))
	for i, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '_':
			out = append(out, byte(r))
		case r >= 'A' && r <= 'Z':
			out = append(out, byte(r-'A'+'a'))
		default:
			if i > 0 {
				out = append(out, '_')
			}
		}
	}
	if len(out) > 0 && out[0] >= '0' && out[0] <= '9' {
		out = append([]byte{'r', '_'}, out...)
	}
	return string(out)
}

// --- deploy-unit collection -------------------------------------------------

// collectDeployUnits returns every deploy unit's name in this target —
// declared entries plus any seam dispatching as container or lambda.
//
// Naming:
//   - entries use their declared name verbatim;
//   - container/lambda seams use their resolved service_name (kebab-
//     case fallback, applied during Normalize).
//
// Output is sorted alphabetically so ResolvedPlan iteration is
// deterministic regardless of go-map iteration order.
func collectDeployUnits(plan *Plan, target *Target) []string {
	seen := map[string]struct{}{}
	add := func(name string) {
		if name == "" {
			return
		}
		seen[name] = struct{}{}
	}
	for entryName := range target.Entries {
		add(entryName)
	}
	for seamName, mode := range target.Seams {
		if mode == SeamInproc || mode == "" {
			continue
		}
		if seam := plan.Seams[seamName]; seam != nil {
			add(seam.ServiceName)
		}
	}
	out := make([]string, 0, len(seen))
	for k := range seen {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// --- serialization ----------------------------------------------------------

// marshalPeers serializes a peer table to the CARAVAN_RPC_PEERS JSON
// shape per docs/poc_rpc_sdk.md. Deterministic key order (alphabetic,
// via encoding/json's default behavior on map keys).
func marshalPeers(peers map[string]PeerEntry) string {
	if len(peers) == 0 {
		return "{}"
	}
	b, err := json.Marshal(peers)
	if err != nil {
		// PeerEntry has only safe types — Marshal can't fail.
		panic(fmt.Sprintf("marshalPeers: %v", err))
	}
	return string(b)
}

// sortedKeys returns the keys of a map in alphabetical order.
func sortedKeys[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
