package emit

import (
	"fmt"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

// Base-compose scan resolves which compose service names the user's
// hand-authored docker-compose.yaml already publishes. M4 uses this
// set to skip emitting resource containers (Postgres, Redis, MinIO,
// etc.) when the same name already exists in the base compose — the
// "already provided" case from the M4 plan. Env-var wiring still
// flows into the consumer; only the duplicate container emission is
// suppressed.
//
// Discovery rules (no yaml schema change; M4 stays within the closed
// SDK contract):
//
//  1. If the CLI passed `--base-compose=path`, use that path.
//  2. Else probe conventional locations relative to the caravan.yaml's
//     directory: `infra/docker-compose.yaml` (invoice-parse) → take
//     first hit; `docker-compose.yaml` (code-rag) → take if present;
//     `compose.yaml` → take if present.
//  3. If none exist, return an empty set + nil error. Emit-everything
//     mode is the safe fallback for new repos with no hand-authored
//     compose.

// BaseComposeServiceNames returns the set of service names declared
// in the user's hand-authored compose file. `dir` is the directory
// containing the caravan.yaml (where the user invoked `caravan
// compile`). Returns an empty map (never nil) when no compose file is
// found on the conventional paths — callers can index without nil
// checks.
//
// Parse errors return ("emit everything") behavior: if the user's
// compose isn't a syntactically valid yaml, we'd rather emit a fresh
// container than silently no-op and have the user wonder why their
// resource wasn't wired. A warning is the right signal but the
// emitter doesn't have a diagnostics handle at this layer; phase 5
// callers can check the error and log if they want.
func BaseComposeServiceNames(dir string) (map[string]bool, error) {
	path := DiscoverBaseCompose(dir)
	if path == "" {
		return map[string]bool{}, nil
	}
	body, err := os.ReadFile(path)
	if err != nil {
		// File existed at Stat time but we couldn't read it. Report.
		return map[string]bool{}, fmt.Errorf("read base compose %s: %w", path, err)
	}
	return parseComposeServiceNames(body)
}

// DiscoverBaseCompose probes the conventional base-compose locations
// in `dir` (where the user ran `caravan compile`). Returns the
// absolute path on first hit, empty string when none exist. The probe
// order matches the M4-plan convention: invoice-parse's
// `infra/docker-compose.yaml` first, then code-rag-style root-level
// `docker-compose.yaml` / `compose.yaml`.
func DiscoverBaseCompose(dir string) string {
	candidates := []string{
		filepath.Join(dir, "infra", "docker-compose.yaml"),
		filepath.Join(dir, "infra", "docker-compose.yml"),
		filepath.Join(dir, "docker-compose.yaml"),
		filepath.Join(dir, "docker-compose.yml"),
		filepath.Join(dir, "compose.yaml"),
		filepath.Join(dir, "compose.yml"),
	}
	for _, p := range candidates {
		if info, err := os.Stat(p); err == nil && !info.IsDir() {
			return p
		}
	}
	return ""
}

// parseComposeServiceNames extracts service names from a compose yaml.
// Only the `services:` top-level key is parsed; everything else (
// version, networks, volumes, etc.) is ignored.
func parseComposeServiceNames(body []byte) (map[string]bool, error) {
	var doc yaml.Node
	if err := yaml.Unmarshal(body, &doc); err != nil {
		return map[string]bool{}, fmt.Errorf("parse base compose: %w", err)
	}
	out := map[string]bool{}
	root := docRoot(&doc)
	if root == nil || root.Kind != yaml.MappingNode {
		return out, nil
	}
	for i := 0; i+1 < len(root.Content); i += 2 {
		k, v := root.Content[i], root.Content[i+1]
		if k.Value != "services" || v.Kind != yaml.MappingNode {
			continue
		}
		for j := 0; j+1 < len(v.Content); j += 2 {
			name := v.Content[j]
			if name.Kind == yaml.ScalarNode {
				out[name.Value] = true
			}
		}
	}
	return out, nil
}

// docRoot drills past the document node to the underlying mapping.
// Mirrors compiler.docRoot but lives in this package to avoid pulling
// the unexported helper across package boundaries.
func docRoot(n *yaml.Node) *yaml.Node {
	if n == nil {
		return nil
	}
	if n.Kind == yaml.DocumentNode && len(n.Content) > 0 {
		return n.Content[0]
	}
	return n
}
