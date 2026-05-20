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

	deployUnits := collectDeployUnits(plan, target)
	peerTable := buildPeerTable(plan, target, diag)
	stringified := marshalPeers(peerTable)

	rp := &ResolvedPlan{
		Plan:       plan,
		TargetName: targetName,
		PeerTables: map[string]map[string]PeerEntry{},
		EnvVars:    map[string]map[string]string{},
	}
	for _, du := range deployUnits {
		// Every deploy unit sees the same peer table at M0 (single-
		// application PoC; no cross-application routing yet).
		rp.PeerTables[du] = peerTable
		rp.EnvVars[du] = map[string]string{
			"CARAVAN_RPC_PEERS": stringified,
		}
		// TODO(M4-cloud): IAM grants per deploy unit.
		// TODO(M7): CARAVAN_RPC_SHARED_SECRET injection per yaml.
	}
	return rp
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
		return builder(seam, diag)
	}
	panic(fmt.Sprintf("unhandled SeamDispatchMode: %q", mode))
}

// peerBuilders maps a dispatch mode to the function that produces its
// PeerEntry. Adding a new mode is one entry here + one constant in
// kinds.go + one case in the mode's `IsValid`.
var peerBuilders = map[SeamDispatchMode]func(*Seam, *Diagnostics) PeerEntry{
	SeamInproc:    buildInprocPeer,
	SeamContainer: buildContainerPeer,
	SeamLambda:    buildLambdaPeer,
}

func buildInprocPeer(_ *Seam, _ *Diagnostics) PeerEntry {
	return PeerEntry{Mode: "inproc"}
}

func buildContainerPeer(seam *Seam, _ *Diagnostics) PeerEntry {
	host := seam.ServiceName
	if host == "" {
		host = kebabCase(seam.Name) // defaulted in Normalize; defensive
	}
	return PeerEntry{
		Mode: "http",
		URL:  fmt.Sprintf("http://%s:8080", host),
	}
}

func buildLambdaPeer(seam *Seam, diag *Diagnostics) PeerEntry {
	// TODO(M7): real Function URL from emitted AWS resources.
	diag.Warn(seam.Span, "lambda dispatch lands at M7; emitting placeholder function_url")
	return PeerEntry{
		Mode:        "lambda",
		FunctionURL: "TODO_LAMBDA_URL",
	}
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
