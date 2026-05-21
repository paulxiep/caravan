package emit

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/paulxiep/caravan/internal/compiler"
)

// caravanRpcSpec is the version spec Caravan injects into user
// manifests. We use `>=0.1.0.dev0` (not `>=0.1.0`) because the in-tree
// vendored wheel that invoice-parse installs from is
// `caravan_rpc-0.1.0.dev0-py3-none-any.whl` — per PEP 440 a `dev0`
// release sorts strictly *below* the corresponding release version, so
// `>=0.1.0` would reject the vendored wheel. The dev0 spec accepts both
// the current dev wheel and the future M9 PyPI 0.1.0 release.
const caravanRpcSpec = "caravan-rpc>=0.1.0.dev0"

// caravanRpcDistribution is the PEP-503 normalized distribution name
// we recognize in user manifests (case-insensitive; `_` and `-` are
// equivalent).
const caravanRpcDistribution = "caravan-rpc"

// ManifestPatch describes one line Caravan injects into a build-context
// manifest. M3 contributes one patch (`caravan-rpc>=0.1.0`); M4-cloud,
// M5, and M6 plug in more (tier-1 LLM provider feature flags, resource
// SDKs) without rewriting the file-write path.
type ManifestPatch struct {
	// Distribution is the PEP-503-normalized distribution name (e.g.
	// "caravan-rpc"). Used to detect pre-existing user-side declarations
	// for conflict reporting.
	Distribution string
	// Line is the verbatim line Caravan writes when the distribution is
	// absent. Includes the version specifier (e.g. "caravan-rpc>=0.1.0").
	Line string
	// Reason is a short human-readable explanation that becomes a
	// comment above the appended line in the emitted manifest. Useful
	// during debugging.
	Reason string
}

// EmitManifestPatches writes per-target build-context manifest files
// for each Python entry in the plan. Output path mirrors the in-repo
// layout under a `build-context/` subtree so future per-target build-
// context files (TS package.json, Cargo.toml patches) co-exist without
// name collisions:
//
//	<outDir>/build-context/<entry.Path>/requirements.txt
//
// User's on-disk manifest at <userRepoRoot>/<entry.Path>/requirements.txt
// is read but never modified (poc_yaml_spec.md §"Manifest patching").
//
// Per-language behavior:
//
//   - Python: append-line semantics on requirements.txt. Errors on
//     version mismatch per D9 (development_plan.md decision gate).
//   - Rust:   no-op for M3. Rust's manifest-patch path is its own
//             milestone story (M2 didn't need it because the SDK was
//             added via path-dep in code-rag).
//   - Other:  skipped silently; emit_test will fail before this matters.
//
// Returns the list of written paths so the CLI can log them alongside
// the compose-override path.
func EmitManifestPatches(rp *compiler.ResolvedPlan, outDir, userRepoRoot string) ([]string, error) {
	if rp == nil || rp.Plan == nil {
		return nil, fmt.Errorf("nil ResolvedPlan")
	}
	patches := []ManifestPatch{
		{
			Distribution: caravanRpcDistribution,
			Line:         caravanRpcSpec,
			Reason:       "caravan-rpc SDK — managed by `caravan compile`; do not hand-edit.",
		},
	}

	var wrote []string
	for _, entryName := range sortedKeys(rp.Plan.Entries) {
		entry := rp.Plan.Entries[entryName]
		if entry == nil {
			continue
		}
		switch entry.Language {
		case compiler.LanguagePython:
			path, err := emitPythonManifest(entry, patches, outDir, userRepoRoot)
			if err != nil {
				return wrote, fmt.Errorf("entries.%s: %w", entry.Name, err)
			}
			if path != "" {
				wrote = append(wrote, path)
			}
		case compiler.LanguageRust, compiler.LanguageUnknown, "":
			// Rust manifest patching is deferred. Empty Language means
			// the entry's path doesn't resolve to a manifest on disk —
			// the validateEntryLanguages warning is the user-facing
			// signal; no patch is written.
			continue
		}
	}
	return wrote, nil
}

// emitPythonManifest reads the user's requirements.txt at
// <userRepoRoot>/<entry.Path>/requirements.txt, applies the patches,
// and writes the result under outDir/build-context/<entry.Path>.
// Returns the emitted-file path on success.
func emitPythonManifest(entry *compiler.Entry, patches []ManifestPatch, outDir, userRepoRoot string) (string, error) {
	if entry.Path == "" {
		return "", nil
	}
	sourcePath := filepath.Join(userRepoRoot, entry.Path, "requirements.txt")

	originalBody := ""
	if data, err := os.ReadFile(sourcePath); err == nil {
		originalBody = string(data)
	} else if !os.IsNotExist(err) {
		return "", fmt.Errorf("read %s: %w", sourcePath, err)
	}

	patched, err := applyPythonPatches(originalBody, patches)
	if err != nil {
		return "", fmt.Errorf("patch %s: %w", filepath.Join(entry.Path, "requirements.txt"), err)
	}

	outPath := filepath.Join(outDir, "build-context", entry.Path, "requirements.txt")
	if err := os.MkdirAll(filepath.Dir(outPath), 0o755); err != nil {
		return "", fmt.Errorf("mkdir %s: %w", filepath.Dir(outPath), err)
	}
	if err := os.WriteFile(outPath, []byte(patched), 0o644); err != nil {
		return "", fmt.Errorf("write %s: %w", outPath, err)
	}
	return outPath, nil
}

// applyPythonPatches applies each ManifestPatch to a requirements.txt
// body. Idempotent on identical inputs.
//
// Per-patch semantics:
//
//   - Distribution absent from the body → append `# <Reason>` comment
//     then the patch Line.
//   - Distribution present with a spec that exactly matches the patch
//     line (after whitespace normalization) → no-op.
//   - Distribution present with any other spec → error (D9).
func applyPythonPatches(body string, patches []ManifestPatch) (string, error) {
	lines := strings.Split(body, "\n")
	out := body
	for _, p := range patches {
		state, existingLine := findRequirementLine(lines, p.Distribution)
		switch state {
		case requirementAbsent:
			out = appendRequirement(out, p)
		case requirementCompatible:
			// no-op; user (or previous compile) already has the right line
			_ = existingLine
		case requirementConflict:
			return "", fmt.Errorf("manifest patch conflict: requirements.txt declares %q but compiler requires %q (D9 — error on version mismatch)", strings.TrimSpace(existingLine), p.Line)
		}
	}
	return out, nil
}

// requirementState describes how a distribution appears in a
// requirements.txt body relative to a target patch line.
type requirementState int

const (
	requirementAbsent requirementState = iota
	requirementCompatible
	requirementConflict
)

// findRequirementLine scans `lines` for an entry whose PEP-503-
// normalized distribution name matches `distribution`. Returns the
// state and the raw line on a hit.
//
// Compatibility rule: the existing entry must canonicalize to the same
// distribution + the same version-specifier portion the patch line
// uses. We match by re-canonicalizing both sides (lowercase, normalize
// `_` ↔ `-`, strip whitespace). Any other variant is a conflict.
func findRequirementLine(lines []string, distribution string) (requirementState, string) {
	want := canonicalizeDistribution(distribution)
	for _, raw := range lines {
		line := stripPipComment(raw)
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		name, spec := splitRequirement(line)
		if canonicalizeDistribution(name) != want {
			continue
		}
		// Found the distribution. The compatibility rule for M3 is
		// exact-spec match (case/whitespace-insensitive) against the
		// caravanRpcSpec value (after the distribution name). The
		// caller controls which spec it considers compatible.
		switch strings.TrimSpace(spec) {
		case "", ">=0.1.0", ">=0.1.0.dev0":
			return requirementCompatible, raw
		default:
			return requirementConflict, raw
		}
	}
	return requirementAbsent, ""
}

// splitRequirement separates a PEP-508 requirement line into
// (distribution-name, specifier). Specifier may be empty when the
// line is just the distribution name. Extras and markers are dropped
// from the name portion but preserved in the specifier portion for
// reporting.
func splitRequirement(line string) (name, spec string) {
	cutset := "=<>!~;[ \t#"
	idx := strings.IndexAny(line, cutset)
	if idx < 0 {
		return strings.TrimSpace(line), ""
	}
	return strings.TrimSpace(line[:idx]), strings.TrimSpace(line[idx:])
}

// stripPipComment removes a trailing `# ...` comment from a pip
// requirements line. Pip allows comments anywhere on the line; we
// only need the leading non-comment content.
func stripPipComment(line string) string {
	if i := strings.Index(line, "#"); i >= 0 {
		return line[:i]
	}
	return line
}

// canonicalizeDistribution applies PEP-503 normalization: lowercase,
// replace runs of `[-_.]` with a single `-`.
func canonicalizeDistribution(name string) string {
	out := strings.ToLower(strings.TrimSpace(name))
	out = strings.ReplaceAll(out, "_", "-")
	out = strings.ReplaceAll(out, ".", "-")
	for strings.Contains(out, "--") {
		out = strings.ReplaceAll(out, "--", "-")
	}
	return out
}

// appendRequirement appends a `# <Reason>` comment then the patch line
// to body, inserting a leading newline only when body is non-empty and
// doesn't already end with one. Final output always ends with a single
// trailing newline.
func appendRequirement(body string, p ManifestPatch) string {
	var sb strings.Builder
	sb.WriteString(body)
	if len(body) > 0 && !strings.HasSuffix(body, "\n") {
		sb.WriteString("\n")
	}
	if p.Reason != "" {
		sb.WriteString("# ")
		sb.WriteString(p.Reason)
		sb.WriteString("\n")
	}
	sb.WriteString(p.Line)
	sb.WriteString("\n")
	return sb.String()
}
