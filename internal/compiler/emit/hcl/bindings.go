package hcl

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/paulxiep/caravan/internal/compiler"
	"github.com/paulxiep/caravan/internal/compiler/emit"
)

// EnvBinding is one container-env entry's full wiring spec on a Fargate
// or Lambda task. The HCL emit consumes a list per host entry to
// populate task-def environment + Lambda env blocks; the sidecar
// manifest writer translates the same list into the JSON `caravan up`
// reads to resolve TF variable values from its host env at apply time.
//
// Exactly one of Literal or VarName is set per binding:
//
//   - Literal: inline value, copied straight into the task-def env. Used
//     for base compose `environment:` entries with non-interpolation
//     literal values (e.g. `INVOICE_PARSE_CONFIG: /app/config/docker.yaml`).
//   - VarName: caravan emits `variable "<VarName>" {}` in main.tf and
//     the task-def env references `var.<VarName>`. Used for declared
//     secrets (sensitive=true), env_file passthroughs, and `${VAR}`
//     compose interpolations.
type EnvBinding struct {
	Key       string // env var name as the container reads it
	Literal   string // inline value (when set; VarName empty)
	VarName   string // tofu variable name (when set; Literal empty)
	EnvName   string // host env var caravan up resolves into TF_VAR_<VarName>
	Sensitive bool   // sensitive=true on the tofu variable
	Source    string // debug tag — "secret" / "env_file" / "environment"
}

// ComputeBindings produces the per-entry binding list for one Fargate
// target. For each container-mode entry it walks:
//
//  1. Declared secrets via entry.Uses → secret bindings (Sensitive=true).
//  2. Base compose's `env_file:` for the entry's same-named service →
//     one TF-variable binding per .env key.
//  3. Base compose's `environment:` block for the same service →
//     literals inlined; ${VAR} interpolations as TF variables.
//
// Caravan-owned env names (CARAVAN_*, resource endpoint env names from
// rp.ResourceEnvVars, declared-secret env names) are filtered so the
// existing pipelines stay authoritative. Net result: user-app config
// flows through to Fargate/Lambda; caravan-derived values aren't shadowed.
func ComputeBindings(
	rp *compiler.ResolvedPlan,
	target *compiler.Target,
	baseServiceEnvs map[string]*emit.BaseComposeServiceEnv,
	composeDir string,
) (map[string][]EnvBinding, error) {
	out := map[string][]EnvBinding{}
	if rp == nil || rp.Plan == nil || target == nil {
		return out, nil
	}
	for entryName, mode := range target.Entries {
		if mode != compiler.EntryContainer {
			continue
		}
		entry := rp.Plan.Entries[entryName]
		if entry == nil {
			continue
		}
		bindings, err := bindingsForEntry(rp, entry, baseServiceEnvs, composeDir)
		if err != nil {
			return nil, fmt.Errorf("entry %q: %w", entryName, err)
		}
		out[entryName] = bindings
	}
	return out, nil
}

// bindingsForEntry computes the binding list for one entry. See
// ComputeBindings for the resolution order.
func bindingsForEntry(
	rp *compiler.ResolvedPlan,
	entry *compiler.Entry,
	baseServiceEnvs map[string]*emit.BaseComposeServiceEnv,
	composeDir string,
) ([]EnvBinding, error) {
	bindings := []EnvBinding{}
	declaredSecretKeys := map[string]bool{}
	seenKeys := map[string]bool{}

	// 1. Declared secrets (from caravan.yaml's `secrets:` block, referenced
	//    via entry.Uses). Sensitive=true → tofu variable with sensitive
	//    flag; value resolved from host env at apply time.
	for _, useName := range entry.Uses {
		secret := rp.Plan.Secrets[useName]
		if secret == nil || secret.From != "env" {
			continue
		}
		envName := secret.Path
		if envName == "" {
			envName = secret.Name
		}
		declaredSecretKeys[envName] = true
		bindings = append(bindings, EnvBinding{
			Key:       envName,
			VarName:   secretVarName(secret.Name),
			EnvName:   envName,
			Sensitive: true,
			Source:    "secret",
		})
		seenKeys[envName] = true
	}

	// 2. + 3. Base compose passthrough — env_file keys + environment block.
	serviceEnv := baseServiceEnvs[entry.Name]
	if serviceEnv != nil {
		envFileKeys, err := loadEnvFileKeys(serviceEnv.EnvFile, composeDir)
		if err != nil {
			return nil, err
		}
		merged := map[string]string{}
		for k := range envFileKeys {
			// env_file passthrough = host env lookup at apply time
			merged[k] = "${" + k + "}"
		}
		for k, v := range serviceEnv.Environment {
			// environment block overlays env_file (matches compose's merge order)
			merged[k] = v
		}

		resourceEnvNames := caravanResourceEnvNamesFor(rp, entry.Name)
		mergedKeys := make([]string, 0, len(merged))
		for k := range merged {
			mergedKeys = append(mergedKeys, k)
		}
		sortStrings(mergedKeys)

		for _, k := range mergedKeys {
			v := merged[k]
			if isCaravanOwnedEnv(k, resourceEnvNames, declaredSecretKeys) {
				continue
			}
			if seenKeys[k] {
				continue
			}
			seenKeys[k] = true

			source := "environment"
			if _, inEnvBlock := serviceEnv.Environment[k]; !inEnvBlock && envFileKeys[k] {
				source = "env_file"
			}

			if interpolatedVar := bareInterpolation(v); interpolatedVar != "" {
				bindings = append(bindings, EnvBinding{
					Key:       k,
					VarName:   terraformLocalName(interpolatedVar),
					EnvName:   interpolatedVar,
					Sensitive: false,
					Source:    source,
				})
				continue
			}
			bindings = append(bindings, EnvBinding{
				Key:     k,
				Literal: v,
				Source:  source,
			})
		}
	}

	sortBindingsByKey(bindings)
	return bindings, nil
}

// loadEnvFileKeys reads every env_file path declared on a service and
// returns the union of keys. Values are discarded — only the key set
// matters for the passthrough scan (values come from caravan up's host
// env at apply time, not from the developer's .env contents at compile
// time). Missing files contribute nothing.
func loadEnvFileKeys(paths []string, composeDir string) (map[string]bool, error) {
	out := map[string]bool{}
	for _, p := range paths {
		full := p
		if !filepath.IsAbs(full) && composeDir != "" {
			full = filepath.Join(composeDir, p)
		}
		entries, err := emit.LoadDotEnvFile(full)
		if err != nil {
			return nil, fmt.Errorf("load env_file %s: %w", full, err)
		}
		for k := range entries {
			out[k] = true
		}
	}
	return out, nil
}

// caravanResourceEnvNamesFor returns the set of env var names caravan
// itself emits for an entry (DATABASE_URL, S3_BUCKET, etc.). Drives the
// passthrough filter so user-side env declarations don't shadow
// caravan-derived endpoint values.
func caravanResourceEnvNamesFor(rp *compiler.ResolvedPlan, entryName string) map[string]bool {
	out := map[string]bool{}
	if rp == nil || rp.ResourceEnvVars == nil {
		return out
	}
	if vars, ok := rp.ResourceEnvVars[entryName]; ok {
		for k := range vars {
			out[k] = true
		}
	}
	return out
}

// isCaravanOwnedEnv reports whether the env name k is already owned by
// a caravan pipeline (RPC-internal env vars, resource endpoints, or
// declared secrets). True → skip in passthrough emit.
func isCaravanOwnedEnv(k string, resourceEnvNames, declaredSecretKeys map[string]bool) bool {
	if strings.HasPrefix(k, "CARAVAN_") {
		return true
	}
	if resourceEnvNames[k] {
		return true
	}
	if declaredSecretKeys[k] {
		return true
	}
	return false
}

// bareInterpolation returns the inner var name if v is a bare `${NAME}`
// compose interpolation, "" otherwise. Mirrors compute_fargate.go's
// isComposePassthrough but lifts the inner identifier so the caller
// can use it as a host env var name.
func bareInterpolation(v string) string {
	if len(v) < 4 || v[0] != '$' || v[1] != '{' || v[len(v)-1] != '}' {
		return ""
	}
	inner := v[2 : len(v)-1]
	for _, c := range inner {
		if c == '$' || c == '{' || c == '}' {
			return ""
		}
	}
	return inner
}

func sortBindingsByKey(s []EnvBinding) {
	for i := 1; i < len(s); i++ {
		for j := i; j > 0 && s[j-1].Key > s[j].Key; j-- {
			s[j-1], s[j] = s[j], s[j-1]
		}
	}
}

// WiringManifest is the sidecar JSON caravan writes alongside main.tf
// so `caravan up` can resolve TF variable values from host env without
// re-parsing the HCL textually (replaces the old string-grep approach
// in cmd/caravan/up.go's tofuSecretVars).
type WiringManifest struct {
	Bindings []ManifestBinding `json:"bindings"`
}

// ManifestBinding is one TF-variable binding caravan up reads from the
// sidecar JSON. Flat shape (vars-only, no per-entry view) so caravan up
// just iterates and resolves; multi-entry overlap is de-duped at write
// time.
type ManifestBinding struct {
	VarName   string `json:"var_name"`
	EnvName   string `json:"env_name"`
	Sensitive bool   `json:"sensitive"`
	Source    string `json:"source"`
}

// WriteWiringManifest emits the sidecar manifest to outDir. Takes the
// per-entry binding map and produces a flat de-duped list keyed by
// VarName. Returns the written path.
func WriteWiringManifest(outDir string, perEntryBindings map[string][]EnvBinding) (string, error) {
	dedupe := map[string]ManifestBinding{}
	for _, bindings := range perEntryBindings {
		for _, b := range bindings {
			if b.VarName == "" {
				continue
			}
			if _, exists := dedupe[b.VarName]; exists {
				continue
			}
			dedupe[b.VarName] = ManifestBinding{
				VarName:   b.VarName,
				EnvName:   b.EnvName,
				Sensitive: b.Sensitive,
				Source:    b.Source,
			}
		}
	}
	names := make([]string, 0, len(dedupe))
	for n := range dedupe {
		names = append(names, n)
	}
	sortStrings(names)
	m := WiringManifest{Bindings: make([]ManifestBinding, 0, len(names))}
	for _, n := range names {
		m.Bindings = append(m.Bindings, dedupe[n])
	}
	body, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return "", fmt.Errorf("marshal wiring manifest: %w", err)
	}
	body = append(body, '\n')
	path := filepath.Join(outDir, "caravan.tfwiring.json")
	if err := os.WriteFile(path, body, 0o644); err != nil {
		return "", fmt.Errorf("write wiring manifest: %w", err)
	}
	return path, nil
}

// dedupedVarBindings returns the per-target list of TF variables (var
// name + sensitive flag) the HCL emitter must declare. Order is sorted
// by VarName. Used by emitSecretVariables (now: emitVarBindings) to
// produce one `variable {}` per unique tofu variable across all entries.
func dedupedVarBindings(perEntryBindings map[string][]EnvBinding) []EnvBinding {
	dedupe := map[string]EnvBinding{}
	for _, bindings := range perEntryBindings {
		for _, b := range bindings {
			if b.VarName == "" {
				continue
			}
			if _, exists := dedupe[b.VarName]; exists {
				continue
			}
			dedupe[b.VarName] = b
		}
	}
	names := make([]string, 0, len(dedupe))
	for n := range dedupe {
		names = append(names, n)
	}
	sortStrings(names)
	out := make([]EnvBinding, 0, len(names))
	for _, n := range names {
		out = append(out, dedupe[n])
	}
	return out
}
