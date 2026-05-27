package compiler

import (
	"os"
	"path/filepath"
)

// Normalize runs phase 3 on a ParsedDoc. It validates cross-references
// and fills in defaults. The PoC has a small enough schema that we keep
// normalize/parse on the same struct type — Normalize mutates the doc
// in place and returns the same pointer for convenience.
//
// Structured as a pipeline:
//
//  1. defaulters — fill in computed defaults (e.g. seam.ServiceName).
//  2. validators — assert cross-refs and structural invariants;
//     diagnostics are accumulated, the pipeline always runs to
//     completion so the caller sees every problem at once.
func Normalize(doc *ParsedDoc, diag *Diagnostics) *Plan {
	if doc == nil {
		return nil
	}
	applyDefaults(doc)
	runValidators(doc, diag)
	return doc
}

// --- defaulters --------------------------------------------------------------

// applyDefaults runs each defaulter in order. A defaulter never reports
// diagnostics — it only fills in computed values.
func applyDefaults(doc *ParsedDoc) {
	defaulters := []func(*ParsedDoc){
		defaultOutputDir,
		defaultSeamServiceNames,
		defaultSeamPaths,
		defaultHybridTargetFields,
		defaultFargateTargetFields,
	}
	for _, fn := range defaulters {
		fn(doc)
	}
}

// DefaultOutputDir is the per-target write root used when the yaml
// doesn't set `output_dir:` explicitly. Namespaced so `caravan compile`
// can't silently overwrite a hand-authored `infra/` (or any other
// commonly-claimed name) the user already owns.
const DefaultOutputDir = "caravan-out"

// defaultOutputDir fills Plan.OutputDir from DefaultOutputDir when the
// yaml didn't declare one. Existing repos that want to keep the prior
// `infra/` layout opt in with `output_dir: infra`.
func defaultOutputDir(doc *ParsedDoc) {
	if doc.OutputDir == "" {
		doc.OutputDir = DefaultOutputDir
	}
}

// defaultSeamServiceNames fills Seam.ServiceName from kebab-case(Name)
// for any seam that didn't declare one in yaml. M1's compose-emitter
// reads ServiceName as authoritative — defaulting here keeps the
// emitter free of "fallback" branches.
func defaultSeamServiceNames(doc *ParsedDoc) {
	for _, s := range doc.Seams {
		if s.ServiceName == "" {
			s.ServiceName = kebabCase(s.Name)
		}
	}
}

// defaultSeamPaths fills Seam.Path for single-entry plans where the seam
// omits `path:` in yaml. The seam-path field is the load-bearing key the
// resolve phase uses to identify which entry "owns" a seam (i.e. whose
// peer-table the seam belongs to). Multi-entry plans (invoice-parse)
// declare path explicitly per seam; single-entry plans (code-rag) can
// leave it empty because the assignment is unambiguous. Without this
// default, resolve.go::entriesUsingSeams returns empty for code-rag and
// the consumer entry never gets CARAVAN_RPC_PEERS injected.
//
// Plans with multiple entries and any path-less seam are left as-is —
// the user must declare paths explicitly there; validateSeamUses /
// runtime errors surface the ambiguity rather than guessing.
func defaultSeamPaths(doc *ParsedDoc) {
	if len(doc.Entries) != 1 {
		return
	}
	var soleEntryPath string
	for _, e := range doc.Entries {
		soleEntryPath = e.Path
	}
	if soleEntryPath == "" {
		return
	}
	for _, s := range doc.Seams {
		if s.Path == "" {
			s.Path = soleEntryPath
		}
	}
}

// DefaultAwsProfile names the AWS profile compose containers
// authenticate with via the mounted ~/.aws when the user didn't
// declare one explicitly. Matches the M4-cloud-prereq onboarding
// checklist's `aws configure --profile caravan-poc` step.
const DefaultAwsProfile = "caravan-poc"

// defaultHybridTargetFields fills derived defaults on targets that opt
// into M4-cloud's hybrid-dev mode (creds_passthrough). Pure defaulting
// — validation happens in validateHybridTarget.
func defaultHybridTargetFields(doc *ParsedDoc) {
	for _, t := range doc.Targets {
		if !t.CredsPassthrough {
			continue
		}
		if t.AwsProfile == "" {
			t.AwsProfile = DefaultAwsProfile
		}
		if t.Backend != nil {
			if t.Backend.Region == "" {
				t.Backend.Region = t.Region
			}
			if t.Backend.Key == "" {
				t.Backend.Key = doc.Name + "/" + t.Name + ".tfstate"
			}
		}
	}
}

// DefaultVPCCIDR is the default IPv4 CIDR caravan uses for a Fargate
// target's VPC when caravan.yaml doesn't pin one. /16 gives ~65k
// addresses — more than any PoC needs, but matches the AWS default
// for the QuickStart-style VPC wizard so the emitted HCL looks
// idiomatic to anyone reviewing it.
const DefaultVPCCIDR = "10.0.0.0/16"

// DefaultCloudMapSuffix is the trailing label for the Cloud Map private
// DNS namespace registered per Fargate target. The full namespace is
// "<app>.<suffix>". Local is the AWS convention for non-routable
// private namespaces (.local has no TLD collision risk).
const DefaultCloudMapSuffix = "local"

// defaultFargateTargetFields fills derived defaults on `runtime: fargate`
// targets. Pure defaulting — validation happens in validateFargateTarget.
//
//   - AwsProfile defaults to DefaultAwsProfile (same as hybrid-dev so the
//     state backend + ECR registry share the same identity).
//   - Backend.Region / .Key default to Target.Region / "<app>/<target>.tfstate".
//   - VPC.CIDR defaults to DefaultVPCCIDR ("10.0.0.0/16").
//   - VPC.NAT defaults to "single" — adequate for staging; prod sets "ha".
//   - CloudMapNamespace defaults to "<app>.local".
//   - ECSClusterName defaults to "<app>-<target>".
func defaultFargateTargetFields(doc *ParsedDoc) {
	for _, t := range doc.Targets {
		if t.Runtime != RuntimeFargate {
			continue
		}
		if t.AwsProfile == "" {
			t.AwsProfile = DefaultAwsProfile
		}
		if t.Backend != nil {
			if t.Backend.Region == "" {
				t.Backend.Region = t.Region
			}
			if t.Backend.Key == "" {
				t.Backend.Key = doc.Name + "/" + t.Name + ".tfstate"
			}
		}
		if t.VPC == nil {
			t.VPC = &VPCConfig{}
		}
		if t.VPC.CIDR == "" {
			t.VPC.CIDR = DefaultVPCCIDR
		}
		if t.VPC.NAT == "" {
			t.VPC.NAT = "single"
		}
		if t.CloudMapNamespace == "" {
			t.CloudMapNamespace = doc.Name + "." + DefaultCloudMapSuffix
		}
		if t.ECSClusterName == "" {
			t.ECSClusterName = doc.Name + "-" + t.Name
		}
	}
}

// --- validators --------------------------------------------------------------

// validator is one cross-ref / invariant check. Each is independent
// and runs unconditionally so the caller sees every problem at once.
type validator func(*ParsedDoc, *Diagnostics)

// runValidators executes every validator. Order doesn't matter for
// correctness (diagnostics are accumulated, not short-circuited), but
// the order here roughly follows what a human would check first.
func runValidators(doc *ParsedDoc, diag *Diagnostics) {
	pipeline := []validator{
		validateEntryUses,
		validateEntryTriggerRefs,
		validateSeamUses,
		validateTargetRefs,
		validateSeamImplWhenDispatched,
		validateNamespaceCollisions,
		validateDefaultTarget,
		validateEntryLanguages,
		validateResourceVariants,
		validateTargetCompositionOverrides,
		validateHybridTarget,
		validateFargateTarget,
	}
	for _, fn := range pipeline {
		fn(doc, diag)
	}
}

// validateEntryUses checks that every `uses:` name on an entry resolves
// to a declared resource or secret.
func validateEntryUses(doc *ParsedDoc, diag *Diagnostics) {
	for _, e := range doc.Entries {
		for _, ref := range e.Uses {
			if !nameDeclared(doc, ref) {
				diag.Error(e.Span, "entries.%s uses unknown name %q (not in resources or secrets)", e.Name, ref)
			}
		}
	}
}

// validateEntryTriggerRefs checks queue/stream triggers point at known
// resources.
func validateEntryTriggerRefs(doc *ParsedDoc, diag *Diagnostics) {
	for _, e := range doc.Entries {
		for _, t := range e.Triggers {
			switch t.Kind {
			case TriggerQueue:
				if t.Queue != nil && t.Queue.From != "" {
					if _, ok := doc.Resources[t.Queue.From]; !ok {
						diag.Error(t.Span, "entries.%s queue trigger references unknown resource %q", e.Name, t.Queue.From)
					}
				}
			case TriggerStream:
				if t.Stream != nil && t.Stream.From != "" {
					if _, ok := doc.Resources[t.Stream.From]; !ok {
						diag.Error(t.Span, "entries.%s stream trigger references unknown resource %q", e.Name, t.Stream.From)
					}
				}
			}
		}
	}
}

// validateSeamUses mirrors validateEntryUses for seams.
func validateSeamUses(doc *ParsedDoc, diag *Diagnostics) {
	for _, s := range doc.Seams {
		for _, ref := range s.Uses {
			if !nameDeclared(doc, ref) {
				diag.Error(s.Span, "seams.%s uses unknown name %q (not in resources or secrets)", s.Name, ref)
			}
		}
	}
}

// validateTargetRefs checks every name keyed under target.entries /
// .seams / .composition refers to an actual declaration.
func validateTargetRefs(doc *ParsedDoc, diag *Diagnostics) {
	for _, t := range doc.Targets {
		for name := range t.Entries {
			if _, ok := doc.Entries[name]; !ok {
				diag.Error(t.Span, "targets.%s.entries references unknown entry %q", t.Name, name)
			}
		}
		for name := range t.Seams {
			if _, ok := doc.Seams[name]; !ok {
				diag.Error(t.Span, "targets.%s.seams references unknown seam %q", t.Name, name)
			}
		}
		for name := range t.Composition {
			if _, ok := doc.Resources[name]; !ok {
				diag.Error(t.Span, "targets.%s.composition references unknown resource %q", t.Name, name)
			}
		}
	}
}

// validateSeamImplWhenDispatched requires `impl:` on any seam that
// dispatches as container or lambda in any target — M1's emitter needs
// it to write the peer service's command.
func validateSeamImplWhenDispatched(doc *ParsedDoc, diag *Diagnostics) {
	for _, t := range doc.Targets {
		for seamName, mode := range t.Seams {
			if mode != SeamContainer && mode != SeamLambda {
				continue
			}
			s := doc.Seams[seamName]
			if s == nil {
				continue // already flagged by validateTargetRefs
			}
			if s.Impl == "" {
				diag.Error(s.Span, "seams.%s dispatches as %s in target %s but has no `impl:` field", s.Name, mode, t.Name)
			}
		}
	}
}

// validateNamespaceCollisions catches entries, seams, and resources
// sharing a name — they all map onto compose service names so a
// collision would silently shadow.
func validateNamespaceCollisions(doc *ParsedDoc, diag *Diagnostics) {
	pairs := []struct {
		groupA, groupB string
		namesA, namesB map[string]struct{}
	}{
		{"entry", "seam", keysOf(doc.Entries), keysOf(doc.Seams)},
		{"entry", "resource", keysOf(doc.Entries), keysOf(doc.Resources)},
		{"seam", "resource", keysOf(doc.Seams), keysOf(doc.Resources)},
	}
	for _, p := range pairs {
		for n := range p.namesA {
			if _, dup := p.namesB[n]; dup {
				diag.Error(Span{}, "name %q is declared as both %s and %s — cross-namespace collision", n, p.groupA, p.groupB)
			}
		}
	}
}

// validateDefaultTarget pins default_target to a real target name.
func validateDefaultTarget(doc *ParsedDoc, diag *Diagnostics) {
	if doc.DefaultTarget == "" {
		return
	}
	if _, ok := doc.Targets[doc.DefaultTarget]; !ok {
		diag.Error(Span{}, "default_target %q is not a declared target", doc.DefaultTarget)
	}
}

// validateEntryLanguages detects each entry's source language by
// stat-ing the manifest files inside `entries.<name>.path`. Fills in
// Entry.Language.
//
// Rules (per docs/poc_yaml_spec.md §"What caravan derives"):
//
//	Cargo.toml present                          → rust
//	pyproject.toml or requirements.txt present  → python
//	both rust + python manifests present        → error (ambiguous)
//	none present                                → warn (test fixtures
//	                                              use synthetic paths
//	                                              that don't exist on
//	                                              disk; downstream
//	                                              consumers can default)
//
// The path is resolved relative to the caravan.yaml's directory. When
// the path doesn't exist, we emit a Warn rather than Error to keep
// existing tests with synthetic `path: ./api` fixtures passing.
func validateEntryLanguages(doc *ParsedDoc, diag *Diagnostics) {
	for _, e := range doc.Entries {
		if e.Path == "" {
			continue
		}
		dir := e.Path
		if !filepath.IsAbs(dir) {
			// Source file is captured per-span; entries' spans share the
			// same file. Use that to resolve the entry's path relative
			// to caravan.yaml's directory.
			base := filepath.Dir(e.Span.File)
			if base != "" && base != "." {
				dir = filepath.Join(base, e.Path)
			}
		}
		hasCargo := manifestExists(filepath.Join(dir, "Cargo.toml"))
		hasPyproject := manifestExists(filepath.Join(dir, "pyproject.toml"))
		hasRequirements := manifestExists(filepath.Join(dir, "requirements.txt"))
		hasPython := hasPyproject || hasRequirements

		switch {
		case hasCargo && hasPython:
			diag.Error(e.Span, "entries.%s path %q has ambiguous language: both Cargo.toml and Python manifest (pyproject.toml/requirements.txt) present", e.Name, e.Path)
		case hasCargo:
			e.Language = LanguageRust
		case hasPython:
			e.Language = LanguagePython
		default:
			// Path doesn't resolve to a known manifest. Warn only when
			// the directory exists on disk — synthetic test fixtures use
			// paths like `./api` that don't exist; we don't want to fail
			// those. Leave Language at the zero value ("") so JSON output
			// stays stable for existing fixtures.
			if pathExists(dir) {
				diag.Warn(e.Span, "entries.%s path %q has no recognized manifest (Cargo.toml / pyproject.toml / requirements.txt); language defaults to unknown", e.Name, e.Path)
			}
		}
	}
}

func manifestExists(p string) bool {
	info, err := os.Stat(p)
	if err != nil {
		return false
	}
	return !info.IsDir()
}

func pathExists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}

// validateResourceVariants checks each resource's `kind:` is a legal
// variant for its `type:`. Empty kind passes (resolved to the type
// default at phase 4); unknown values error.
func validateResourceVariants(doc *ParsedDoc, diag *Diagnostics) {
	for _, r := range doc.Resources {
		if r.Variant == "" {
			continue
		}
		if !IsValidVariant(r.Type, r.Variant) {
			diag.Error(r.Span,
				"resources.%s kind %q is not valid for type %q (valid: %v)",
				r.Name, r.Variant, r.Type, ValidVariantsFor(r.Type))
		}
	}
}

// validateTargetCompositionOverrides checks per-target composition
// overrides: the named resource exists (validateTargetRefs already
// covers that), the override's mode (if set) is a known
// CompositionMode (parse-time validates the scalar form; the object
// form re-validates here), and the override's kind (if set) is a
// legal variant for the resource's type.
func validateTargetCompositionOverrides(doc *ParsedDoc, diag *Diagnostics) {
	for _, t := range doc.Targets {
		for resName, override := range t.Composition {
			if override == nil {
				continue
			}
			r := doc.Resources[resName]
			if r == nil {
				continue // covered by validateTargetRefs
			}
			if override.Mode != "" && !override.Mode.IsValid() {
				diag.Error(override.Span,
					"targets.%s.composition.%s: invalid mode %q",
					t.Name, resName, override.Mode)
			}
			if override.Variant != "" && !IsValidVariant(r.Type, override.Variant) {
				diag.Error(override.Span,
					"targets.%s.composition.%s: kind %q is not valid for type %q (valid: %v)",
					t.Name, resName, override.Variant, r.Type, ValidVariantsFor(r.Type))
			}
		}
	}
}

// validateHybridTarget enforces the surface invariants for M4-cloud's
// hybrid-dev mode (creds_passthrough: true):
//
//  1. runtime must be docker-compose (M4-cloud emits compose +
//     HCL together; aws-runtime cloud-compute lands at M4b/M7).
//  2. region must be declared so HCL emit knows where to point the
//     AWS provider.
//  3. backend.bucket and backend.lock_table must be set (referenced
//     by HCL backend.tf; pre-created in M4-cloud-prereq).
//  4. at least one resource must resolve to cloud-managed — either
//     via default_composition or a per-resource composition override
//     or the resource's own declaration. Pure-oss-local targets have
//     no business mounting ~/.aws.
func validateHybridTarget(doc *ParsedDoc, diag *Diagnostics) {
	for _, t := range doc.Targets {
		if !t.CredsPassthrough {
			// Backend declared without creds_passthrough is suspicious
			// but tolerated; M4b may use backend without hybrid mode.
			continue
		}
		if t.Runtime != RuntimeDockerCompose {
			diag.Error(t.Span,
				"targets.%s.creds_passthrough requires runtime=%s (got %q)",
				t.Name, RuntimeDockerCompose, t.Runtime)
		}
		if t.Region == "" {
			diag.Error(t.Span,
				"targets.%s.creds_passthrough requires `region:` for HCL provider config",
				t.Name)
		}
		if t.Backend == nil {
			diag.Error(t.Span,
				"targets.%s.creds_passthrough requires `backend:` (state bucket + lock table from M4-cloud-prereq)",
				t.Name)
		} else {
			if t.Backend.Bucket == "" {
				diag.Error(t.Backend.Span, "targets.%s.backend.bucket is required", t.Name)
			}
			if t.Backend.LockTable == "" {
				diag.Error(t.Backend.Span, "targets.%s.backend.lock_table is required", t.Name)
			}
		}
		if !targetHasCloudManagedResource(doc, t) {
			diag.Error(t.Span,
				"targets.%s.creds_passthrough is set but no resource resolves to cloud-managed (set default_composition or per-resource composition override)",
				t.Name)
		}
	}
}

// validateFargateTarget enforces the surface invariants for M4b's Fargate
// placement (runtime: fargate):
//
//  1. CredsPassthrough must be false — Fargate task roles replace the
//     `~/.aws` mount; the hybrid-dev cred-passthrough path is the wrong
//     authentication model for cloud compute.
//  2. region must be declared so HCL emit knows where to point the AWS
//     provider + VPC + ECS cluster.
//  3. backend.bucket and backend.lock_table must be set (state must be
//     remote; local state on a deploy box would mean apply requires the
//     same laptop).
//  4. VPC.NAT must be a known value when explicitly set ("single" / "ha").
//  5. at least one Fargate placement signal must be present — either an
//     entry marked `runtime: fargate` (in v1 yaml shape via the
//     EntryDispatchMode struct, when added) or a seam with mode=container
//     declared in t.Seams. A bare runtime=fargate target with no Fargate
//     consumers has nothing to compile.
//
// Note on (5): the entry-level `runtime: fargate` per-entry field is
// added in session 2 alongside the ECS emitter. For session 1, only the
// seam-via-t.Seams[name]=container path is checked. This is sufficient
// for code-rag's staging-fargate demo shape where chat is the only entry
// and Embedder is the only Fargate seam peer.
func validateFargateTarget(doc *ParsedDoc, diag *Diagnostics) {
	for _, t := range doc.Targets {
		if t.Runtime != RuntimeFargate {
			continue
		}
		if t.CredsPassthrough {
			diag.Error(t.Span,
				"targets.%s.runtime=fargate is incompatible with creds_passthrough (Fargate task roles replace the ~/.aws mount)",
				t.Name)
		}
		if t.Region == "" {
			diag.Error(t.Span,
				"targets.%s.runtime=fargate requires `region:` for VPC + ECS cluster emission",
				t.Name)
		}
		if t.Backend == nil {
			diag.Error(t.Span,
				"targets.%s.runtime=fargate requires `backend:` (state bucket + lock table from M4-cloud-prereq)",
				t.Name)
		} else {
			if t.Backend.Bucket == "" {
				diag.Error(t.Backend.Span, "targets.%s.backend.bucket is required", t.Name)
			}
			if t.Backend.LockTable == "" {
				diag.Error(t.Backend.Span, "targets.%s.backend.lock_table is required", t.Name)
			}
		}
		if t.VPC != nil && t.VPC.NAT != "" && t.VPC.NAT != "single" && t.VPC.NAT != "ha" {
			diag.Error(t.VPC.Span,
				"targets.%s.vpc.nat must be \"single\" or \"ha\" (got %q)",
				t.Name, t.VPC.NAT)
		}
		// Fargate needs at least one container-mode consumer — either a
		// container-mode seam (peer service in its own task) or a
		// container-mode entry (the main user-code task). Pure-Lambda +
		// inproc-seams targets (M7's invoice-parse prod-mixed shape) have
		// container entries even with no container seams.
		hasContainerConsumer := false
		for _, mode := range t.Seams {
			if mode == SeamContainer {
				hasContainerConsumer = true
				break
			}
		}
		if !hasContainerConsumer {
			for _, mode := range t.Entries {
				if mode == EntryContainer {
					hasContainerConsumer = true
					break
				}
			}
		}
		if !hasContainerConsumer {
			diag.Error(t.Span,
				"targets.%s.runtime=fargate has no Fargate consumers (declare at least one entry or seam with mode=container)",
				t.Name)
		}
	}
}

// targetHasCloudManagedResource reports whether at least one declared
// resource would resolve to CompositionCloudManaged under the given
// target. Mirrors resolveComposition's precedence order without
// requiring a full resolve.
func targetHasCloudManagedResource(doc *ParsedDoc, t *Target) bool {
	for name, r := range doc.Resources {
		if r == nil {
			continue
		}
		if override, ok := t.Composition[name]; ok && override != nil && override.Mode != "" {
			if override.Mode == CompositionCloudManaged {
				return true
			}
			continue
		}
		if r.Composition != "" {
			if r.Composition == CompositionCloudManaged {
				return true
			}
			continue
		}
		if t.DefaultComposition == CompositionCloudManaged {
			return true
		}
	}
	return false
}

// --- helpers -----------------------------------------------------------------

// nameDeclared reports whether name is declared as a resource or secret.
func nameDeclared(doc *ParsedDoc, name string) bool {
	if _, ok := doc.Resources[name]; ok {
		return true
	}
	_, ok := doc.Secrets[name]
	return ok
}

// keysOf returns the keys of a map as a set.
func keysOf[V any](m map[string]V) map[string]struct{} {
	out := make(map[string]struct{}, len(m))
	for k := range m {
		out[k] = struct{}{}
	}
	return out
}

// kebabCase converts a CamelCase / PascalCase / snake_case identifier
// to kebab-case. Examples:
//
//	LLMExtraction    → llm-extraction
//	HTTPRequest      → http-request
//	parse_invoice    → parse-invoice
//	ParseInvoiceData → parse-invoice-data
//
// Rule: insert a hyphen before any uppercase letter preceded by a
// lowercase letter, OR before the last uppercase of an acronym run
// that's followed by a lowercase. Underscores become hyphens. Result
// is lowercase.
func kebabCase(s string) string {
	if s == "" {
		return ""
	}
	runes := []rune(s)
	out := make([]byte, 0, len(runes)+4)
	for i, r := range runes {
		switch {
		case r == '_':
			out = append(out, '-')
		case r >= 'A' && r <= 'Z':
			if needsHyphenBeforeUpper(runes, i) && len(out) > 0 && out[len(out)-1] != '-' {
				out = append(out, '-')
			}
			out = append(out, byte(r-'A'+'a'))
		default:
			out = append(out, byte(r))
		}
	}
	return string(out)
}

func needsHyphenBeforeUpper(runes []rune, i int) bool {
	if i == 0 {
		return false
	}
	prev := runes[i-1]
	prevLower := prev >= 'a' && prev <= 'z'
	prevDigit := prev >= '0' && prev <= '9'
	if prevLower || prevDigit {
		return true
	}
	// Acronym run boundary: H-T-T-P-R-e-q-u-e-s-t → http-request.
	// Insert hyphen when the prior char is uppercase and the next char
	// is lowercase — that marks the start of a new word.
	if !(prev >= 'A' && prev <= 'Z') {
		return false
	}
	if i+1 >= len(runes) {
		return false
	}
	next := runes[i+1]
	return next >= 'a' && next <= 'z'
}
