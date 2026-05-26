package hcl

import (
	"github.com/hashicorp/hcl/v2/hclwrite"

	"github.com/paulxiep/caravan/internal/compiler"
)

// emitComputeForTarget dispatches to the per-runtime compute emitter.
// Called from renderMain after resources are emitted, before outputs.
// No-op for `docker-compose` targets — compose handles compute directly
// in its own emitter, not via HCL.
//
// Adding a new placement (M7's Lambda) means a new case here and a new
// per-placement file (e.g. compute_lambda.go). The iam.go principal
// dispatch already accepts the new principal kind; resolve.go's
// peerBuilders already accepts a new dispatch mode. The abstraction
// breakpoint for "add a placement" is small by design.
func emitComputeForTarget(body *hclwrite.Body, rp *compiler.ResolvedPlan, target *compiler.Target, outputs map[string]string) {
	switch target.Runtime {
	case compiler.RuntimeFargate:
		emitFargateCompute(body, rp, target, outputs)
		// case compiler.RuntimeLambda:
		//     emitLambdaCompute(body, rp, target, outputs)   // M7
	}
}

// fargateConsumers enumerates the (kind, name, serviceName) tuples that
// need a Fargate task on a `runtime: fargate` target. Two sources:
//
//   - Entries flagged `container` in t.Entries — the main user-code
//     deploy units. They run as Fargate tasks but typically aren't
//     dispatched-to via Cloud Map (callers are external HTTP).
//   - Seams flagged `container` in t.Seams — peer services the entry
//     dispatches to via caravan-rpc. They run as Fargate tasks AND
//     register a Cloud Map service so callers resolve their FQDN.
//
// Returned slice is stable (entries first, then seams, each in
// alphabetical order) so emitted HCL diffs are deterministic.
func fargateConsumers(rp *compiler.ResolvedPlan, target *compiler.Target) []fargateConsumer {
	out := make([]fargateConsumer, 0)

	entryNames := sortedKeysEntries(target)
	for _, name := range entryNames {
		if target.Entries[name] != compiler.EntryContainer {
			continue
		}
		e := rp.Plan.Entries[name]
		if e == nil {
			continue
		}
		out = append(out, fargateConsumer{
			Kind:        "entry",
			Name:        name,
			ServiceName: toDashed(name),
			NeedsCloudMap: false,
			Entry:       e,
		})
	}

	seamNames := sortedKeysSeams(target)
	for _, name := range seamNames {
		if target.Seams[name] != compiler.SeamContainer {
			continue
		}
		s := rp.Plan.Seams[name]
		if s == nil {
			continue
		}
		svc := s.ServiceName
		if svc == "" {
			svc = toDashed(name)
		}
		out = append(out, fargateConsumer{
			Kind:        "seam",
			Name:        name,
			ServiceName: svc,
			NeedsCloudMap: true,
			Seam:        s,
		})
	}
	return out
}

// fargateConsumer captures everything compute_fargate.go needs to emit
// one Fargate task definition + ECS service + (optional) Cloud Map
// service for a single entry or seam.
type fargateConsumer struct {
	Kind          string // "entry" or "seam"
	Name          string // plan IR name (entry/seam name)
	ServiceName   string // dashed name used for ECS service + Cloud Map record
	NeedsCloudMap bool
	Entry         *compiler.Entry
	Seam          *compiler.Seam
}

func sortedKeysEntries(t *compiler.Target) []string {
	out := make([]string, 0, len(t.Entries))
	for k := range t.Entries {
		out = append(out, k)
	}
	sortStrings(out)
	return out
}

func sortedKeysSeams(t *compiler.Target) []string {
	out := make([]string, 0, len(t.Seams))
	for k := range t.Seams {
		out = append(out, k)
	}
	sortStrings(out)
	return out
}

// sortStrings — local alias so this file doesn't pull in sort just for
// readability. The std import lives in compute_fargate.go already.
func sortStrings(s []string) {
	for i := 1; i < len(s); i++ {
		for j := i; j > 0 && s[j-1] > s[j]; j-- {
			s[j-1], s[j] = s[j], s[j-1]
		}
	}
}
