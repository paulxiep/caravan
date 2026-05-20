package emit

import (
	"bytes"
	"fmt"
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

// EmitComposeOverride builds the docker-compose override yaml for one
// resolved target. It's layered on top of the user's hand-authored
// docker-compose.yaml — adds CARAVAN_RPC_PEERS to consumer entries and
// spawns a peer service per container-mode seam.
//
// The output is yaml encoded via yaml.Node for stable key order
// (golden-file tests would flake on Go map randomization otherwise).
func EmitComposeOverride(rp *compiler.ResolvedPlan) ([]byte, error) {
	if rp == nil || rp.Plan == nil {
		return nil, fmt.Errorf("nil ResolvedPlan")
	}
	target := rp.Plan.Targets[rp.TargetName]
	if target == nil {
		return nil, fmt.Errorf("target %q not in plan", rp.TargetName)
	}

	services := []composeService{}

	// 1. Consumer entries: inject CARAVAN_RPC_PEERS + the shared secret.
	for _, entryName := range sortedKeys(target.Entries) {
		envVars := rp.EnvVars[entryName]
		if len(envVars) == 0 {
			continue
		}
		services = append(services, buildConsumerOverride(entryName, envVars))
	}

	// 2. Container-mode seams: emit a peer service per seam.
	for _, seamName := range sortedKeys(target.Seams) {
		mode := target.Seams[seamName]
		if mode != compiler.SeamContainer {
			continue
		}
		seam := rp.Plan.Seams[seamName]
		if seam == nil {
			return nil, fmt.Errorf("targets.%s.seams references unknown seam %q (should have been caught in Normalize)", target.Name, seamName)
		}
		svc, err := buildSeamPeerService(seam)
		if err != nil {
			return nil, err
		}
		services = append(services, svc)
	}

	return renderCompose(target.Name, services)
}

// --- service builders -------------------------------------------------------

// composeService is one entry's worth of docker-compose `services:`
// content. We use this intermediate shape (vs raw yaml.Node trees)
// because the field order is fixed across all services — easier to
// keep deterministic.
type composeService struct {
	Name        string
	Build       *composeBuild
	EnvFile     []string
	Environment []composeEnvKV
	Command     []string
	DependsOn   []composeDependsOn
	Profiles    []string
}

type composeBuild struct {
	Context    string
	Dockerfile string
}

type composeEnvKV struct {
	Key, Value string
}

type composeDependsOn struct {
	Service   string
	Condition string
}

// buildConsumerOverride emits the env-only override block that
// injects CARAVAN_RPC_PEERS + shared-secret into a consumer entry,
// plus a depends_on edge to each peer service it talks to.
func buildConsumerOverride(entryName string, envVars map[string]string) composeService {
	svc := composeService{
		Name: entryName,
		Environment: []composeEnvKV{
			{Key: "CARAVAN_RPC_PEERS", Value: envVars["CARAVAN_RPC_PEERS"]},
			{Key: "CARAVAN_RPC_SHARED_SECRET", Value: sharedSecretPlaceholder},
		},
	}
	// depends_on: every peer service named in the peer-table URLs.
	for _, peerHost := range peerHostsFromEnv(envVars["CARAVAN_RPC_PEERS"]) {
		svc.DependsOn = append(svc.DependsOn, composeDependsOn{
			Service:   peerHost,
			Condition: "service_started",
		})
	}
	return svc
}

// buildSeamPeerService emits the new service for a container-mode seam.
func buildSeamPeerService(seam *compiler.Seam) (composeService, error) {
	lang := detectLanguage(seam)
	emitter, ok := SeamServerCommands[lang]
	if !ok {
		return composeService{}, fmt.Errorf("seam %q: no SeamServerCommand for language %q (impl=%q)", seam.Name, lang, seam.Impl)
	}
	cmd, err := emitter.Command(seam, seamServerPort)
	if err != nil {
		return composeService{}, fmt.Errorf("seam %q: %w", seam.Name, err)
	}
	return composeService{
		Name: seam.ServiceName,
		Build: &composeBuild{
			Context:    "..",
			Dockerfile: seam.Dockerfile,
		},
		EnvFile: []string{"../.env"},
		Environment: []composeEnvKV{
			{Key: "CARAVAN_RPC_SHARED_SECRET", Value: sharedSecretPlaceholder},
		},
		Command:  cmd,
		Profiles: []string{"app"},
	}, nil
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
func renderCompose(targetName string, services []composeService) ([]byte, error) {
	doc := &yaml.Node{Kind: yaml.DocumentNode}
	doc.HeadComment = composeHeaderComment(targetName)

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

func composeHeaderComment(targetName string) string {
	return fmt.Sprintf(
		" Generated by `caravan compile --target=%s`. Do not edit by hand.\n"+
			" Layer this override atop the hand-authored docker-compose.yaml:\n"+
			"   docker compose \\\n"+
			"     -f infra/docker-compose.yaml \\\n"+
			"     -f infra/%s/generated/docker-compose.override.generated.yaml \\\n"+
			"     --profile app up",
		targetName, targetName,
	)
}

// serviceNode builds the yaml.Node tree for one composeService. Field
// order: build → env_file → environment → command → depends_on →
// profiles. Matches B0's hand-edited override for diff-friendliness.
func serviceNode(svc composeService) *yaml.Node {
	out := &yaml.Node{Kind: yaml.MappingNode}
	add := func(key string, val *yaml.Node) {
		out.Content = append(out.Content, scalarNode(key), val)
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

