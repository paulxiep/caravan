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

// BaseComposeBuild captures the `services.<X>.build` subset caravan
// reuses when emitting Fargate Lambda build-overrides. We only carry
// the fields the Lambda override needs: context, dockerfile, and any
// additional_contexts (other build keys — target, args — are
// caravan-emitted from yaml IR, not copied from the user's compose).
type BaseComposeBuild struct {
	Context            string
	Dockerfile         string
	AdditionalContexts map[string]string
}

// BaseComposeBuilds reads the base compose and returns the `build:`
// section for each service as a BaseComposeBuild. Used by the Fargate
// Lambda build-override emitter (compose_fargate_build.go) so the
// emitted Lambda service inherits the host entry's context +
// additional_contexts verbatim — keeps "user owns the Dockerfile"
// while letting caravan override the multi-stage `target:` and image
// tag for the Lambda peer.
//
// Missing or unparseable base compose → empty map + nil error
// (caller falls back to no-op or its own defaults).
func BaseComposeBuilds(dir string) (map[string]*BaseComposeBuild, error) {
	path := DiscoverBaseCompose(dir)
	if path == "" {
		return map[string]*BaseComposeBuild{}, nil
	}
	body, err := os.ReadFile(path)
	if err != nil {
		return map[string]*BaseComposeBuild{}, fmt.Errorf("read base compose %s: %w", path, err)
	}
	return parseComposeBuilds(body)
}

func parseComposeBuilds(body []byte) (map[string]*BaseComposeBuild, error) {
	var doc yaml.Node
	if err := yaml.Unmarshal(body, &doc); err != nil {
		return map[string]*BaseComposeBuild{}, fmt.Errorf("parse base compose builds: %w", err)
	}
	out := map[string]*BaseComposeBuild{}
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
			name, svc := v.Content[j], v.Content[j+1]
			if name.Kind != yaml.ScalarNode || svc.Kind != yaml.MappingNode {
				continue
			}
			b := extractBuildBlock(svc)
			if b != nil {
				out[name.Value] = b
			}
		}
	}
	return out, nil
}

// extractBuildBlock pulls `build.{context,dockerfile,additional_contexts}`
// out of a service's mapping. Returns nil when the service has no
// `build:` (image-only services like redis/postgres).
func extractBuildBlock(svc *yaml.Node) *BaseComposeBuild {
	for i := 0; i+1 < len(svc.Content); i += 2 {
		k, v := svc.Content[i], svc.Content[i+1]
		if k.Value != "build" {
			continue
		}
		// Two shapes: string ("./path" — context-only shorthand) or mapping.
		switch v.Kind {
		case yaml.ScalarNode:
			return &BaseComposeBuild{Context: v.Value}
		case yaml.MappingNode:
			b := &BaseComposeBuild{}
			for j := 0; j+1 < len(v.Content); j += 2 {
				bk, bv := v.Content[j], v.Content[j+1]
				switch bk.Value {
				case "context":
					if bv.Kind == yaml.ScalarNode {
						b.Context = bv.Value
					}
				case "dockerfile":
					if bv.Kind == yaml.ScalarNode {
						b.Dockerfile = bv.Value
					}
				case "additional_contexts":
					b.AdditionalContexts = extractAdditionalContexts(bv)
				}
			}
			return b
		}
	}
	return nil
}

// BaseComposeServiceEnv captures the env-related subset of one base-
// compose service: the `env_file:` list (file paths the user expects
// docker compose to load at compose-up time) and the `environment:`
// block (literal or interpolated values inlined per service). M9-cloud
// uses this to passthrough user-app env vars (e.g. invoice-parse's
// OCR_DET_MODEL, INVOICE_PARSE_CONFIG) into Fargate task defs + Lambda
// env, since neither runtime has a shell layer that would honor
// compose's env_file / environment blocks at task-launch time.
type BaseComposeServiceEnv struct {
	// EnvFile is the list of file paths declared under the service's
	// `env_file:` key. Paths are relative to the compose file's
	// directory (compose's default behavior). Caller resolves them
	// against that directory when reading.
	EnvFile []string
	// Environment is the service's `environment:` block. Values may be
	// literal strings or contain ${VAR} interpolations that compose
	// would resolve at compose-up time from its process env.
	Environment map[string]string
}

// BaseComposeServiceEnvs reads the base compose and returns the env
// blocks (env_file + environment) for each service. Used by HCL emit
// to fold user-app env vars into Fargate task defs / Lambda env at
// compile time. Missing or unparseable compose → empty map + nil error
// (same fallback as BaseComposeServiceNames / BaseComposeBuilds).
func BaseComposeServiceEnvs(dir string) (map[string]*BaseComposeServiceEnv, error) {
	path := DiscoverBaseCompose(dir)
	if path == "" {
		return map[string]*BaseComposeServiceEnv{}, nil
	}
	body, err := os.ReadFile(path)
	if err != nil {
		return map[string]*BaseComposeServiceEnv{}, fmt.Errorf("read base compose %s: %w", path, err)
	}
	return parseComposeServiceEnvs(body)
}

// BaseComposeDir returns the directory containing the discovered base
// compose. Callers use it to resolve env_file paths (which are relative
// to the compose file's location per docker-compose semantics).
// Empty string when no compose was discovered.
func BaseComposeDir(dir string) string {
	path := DiscoverBaseCompose(dir)
	if path == "" {
		return ""
	}
	return filepath.Dir(path)
}

func parseComposeServiceEnvs(body []byte) (map[string]*BaseComposeServiceEnv, error) {
	var doc yaml.Node
	if err := yaml.Unmarshal(body, &doc); err != nil {
		return map[string]*BaseComposeServiceEnv{}, fmt.Errorf("parse base compose env: %w", err)
	}
	out := map[string]*BaseComposeServiceEnv{}
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
			name, svc := v.Content[j], v.Content[j+1]
			if name.Kind != yaml.ScalarNode || svc.Kind != yaml.MappingNode {
				continue
			}
			env := extractServiceEnv(svc)
			if env != nil {
				out[name.Value] = env
			}
		}
	}
	return out, nil
}

// extractServiceEnv pulls `env_file:` and `environment:` out of one
// service mapping. Returns nil when the service declares neither.
func extractServiceEnv(svc *yaml.Node) *BaseComposeServiceEnv {
	out := &BaseComposeServiceEnv{Environment: map[string]string{}}
	any := false
	for i := 0; i+1 < len(svc.Content); i += 2 {
		k, v := svc.Content[i], svc.Content[i+1]
		switch k.Value {
		case "env_file":
			out.EnvFile = extractEnvFileList(v)
			if len(out.EnvFile) > 0 {
				any = true
			}
		case "environment":
			extractEnvironmentBlock(v, out.Environment)
			if len(out.Environment) > 0 {
				any = true
			}
		}
	}
	if !any {
		return nil
	}
	return out
}

// extractEnvFileList handles env_file's two shapes: a single string,
// or a list of strings. Other shapes (mapping with required flag) are
// skipped — caravan's PoC scope only handles the common cases.
func extractEnvFileList(n *yaml.Node) []string {
	switch n.Kind {
	case yaml.ScalarNode:
		if n.Value != "" {
			return []string{n.Value}
		}
	case yaml.SequenceNode:
		out := make([]string, 0, len(n.Content))
		for _, item := range n.Content {
			if item.Kind == yaml.ScalarNode && item.Value != "" {
				out = append(out, item.Value)
			}
		}
		return out
	}
	return nil
}

// extractEnvironmentBlock handles environment's two shapes:
//   - mapping: { KEY: value, ... }
//   - sequence: ["KEY=value", "BARE_KEY", ...]
//
// Bare keys (sequence form, no `=`) declare passthrough of the host
// env var with the same name; we emit them as `KEY: ${KEY}` so the
// downstream passthrough logic treats them as a compose interpolation.
func extractEnvironmentBlock(n *yaml.Node, out map[string]string) {
	switch n.Kind {
	case yaml.MappingNode:
		for i := 0; i+1 < len(n.Content); i += 2 {
			k, v := n.Content[i], n.Content[i+1]
			if k.Kind != yaml.ScalarNode {
				continue
			}
			if v.Kind == yaml.ScalarNode {
				out[k.Value] = v.Value
			}
		}
	case yaml.SequenceNode:
		for _, item := range n.Content {
			if item.Kind != yaml.ScalarNode {
				continue
			}
			eq := -1
			for idx, c := range item.Value {
				if c == '=' {
					eq = idx
					break
				}
			}
			if eq < 0 {
				// Bare key — passthrough form. Treat as ${KEY}.
				out[item.Value] = "${" + item.Value + "}"
			} else if eq > 0 {
				out[item.Value[:eq]] = item.Value[eq+1:]
			}
		}
	}
}

// LoadDotEnvFile reads a docker-compose-style `KEY=VALUE` file and
// returns the parsed map. Comments (`#`-prefix) and blank lines are
// skipped. Optional surrounding quotes on values are stripped. Used
// by HCL emit to enumerate env_file keys for the passthrough scan —
// the values are NOT trusted (they could be stale dev secrets); only
// the key list matters. `caravan up` re-resolves values from its own
// host env at apply time.
//
// Missing file → empty map + nil error (the env_file may not exist in
// the developer's checkout; the corresponding TF variables will fall
// back to the host env at apply time).
func LoadDotEnvFile(path string) (map[string]string, error) {
	body, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return map[string]string{}, nil
		}
		return map[string]string{}, fmt.Errorf("read %s: %w", path, err)
	}
	out := map[string]string{}
	for _, line := range splitLines(string(body)) {
		line = trimSpace(line)
		if line == "" || line[0] == '#' {
			continue
		}
		eq := -1
		for i, c := range line {
			if c == '=' {
				eq = i
				break
			}
		}
		if eq <= 0 {
			continue
		}
		key := trimSpace(line[:eq])
		val := trimSpace(line[eq+1:])
		if n := len(val); n >= 2 {
			first, last := val[0], val[n-1]
			if (first == '"' && last == '"') || (first == '\'' && last == '\'') {
				val = val[1 : n-1]
			}
		}
		out[key] = val
	}
	return out, nil
}

// splitLines / trimSpace are tiny stdlib-free helpers — keeping the
// package's import surface unchanged.
func splitLines(s string) []string {
	out := []string{}
	start := 0
	for i, c := range s {
		if c == '\n' {
			line := s[start:i]
			if n := len(line); n > 0 && line[n-1] == '\r' {
				line = line[:n-1]
			}
			out = append(out, line)
			start = i + 1
		}
	}
	if start < len(s) {
		out = append(out, s[start:])
	}
	return out
}

func trimSpace(s string) string {
	start := 0
	for start < len(s) && (s[start] == ' ' || s[start] == '\t') {
		start++
	}
	end := len(s)
	for end > start && (s[end-1] == ' ' || s[end-1] == '\t' || s[end-1] == '\r') {
		end--
	}
	return s[start:end]
}

// extractAdditionalContexts handles both the mapping form
// (`additional_contexts: { name: path }`) and the sequence form
// (`additional_contexts: [name=path, ...]`).
func extractAdditionalContexts(n *yaml.Node) map[string]string {
	out := map[string]string{}
	switch n.Kind {
	case yaml.MappingNode:
		for i := 0; i+1 < len(n.Content); i += 2 {
			k, v := n.Content[i], n.Content[i+1]
			if k.Kind == yaml.ScalarNode && v.Kind == yaml.ScalarNode {
				out[k.Value] = v.Value
			}
		}
	case yaml.SequenceNode:
		for _, item := range n.Content {
			if item.Kind != yaml.ScalarNode {
				continue
			}
			eq := -1
			for idx, c := range item.Value {
				if c == '=' {
					eq = idx
					break
				}
			}
			if eq <= 0 || eq == len(item.Value)-1 {
				continue
			}
			out[item.Value[:eq]] = item.Value[eq+1:]
		}
	}
	return out
}
