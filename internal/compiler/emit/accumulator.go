package emit

import (
	"fmt"
	"sort"
	"strings"
)

// composeAccumulator collects compose services + their env vars from
// multiple emit sources (M3 seam emit, M4 resource emit) into a single
// shape that renderCompose serializes.
//
// Two design goals:
//
//   - Multi-source contribution. M3 emits CARAVAN_RPC_* env vars onto
//     consumer entries plus peer services; M4 emits resource endpoint
//     env vars (DATABASE_URL, REDIS_URL, ...) onto the same consumers
//     plus resource containers (Postgres, Redis, MinIO, ...). The
//     accumulator merges both contributions into one services map.
//
//   - Deterministic, diff-stable output. Service iteration is in
//     insertion order (so M1 goldens stay byte-identical: consumer
//     first, peer second). Env vars per consumer flush in two bands:
//     resources first (source = envSourceResource) in alphabetic key
//     order, then seams (source = envSourceSeam) in alphabetic key
//     order. Last write per key wins.
//
// Precedence rule (enforced at AddEnv): resource emit MUST NOT write
// any env key prefixed CARAVAN_RPC_. That namespace is owned by seam
// emit; a resource shadowing it would be a bug in the catalog. Caller
// receives an error from AddEnv if violated.
type composeAccumulator struct {
	// services indexes the in-progress composeService per name. Pointer
	// values so AddEnv can mutate the service's Environment slice
	// after AddService set the rest of the fields.
	services map[string]*composeService

	// order preserves the insertion sequence so render output is
	// stable. M1's emit order: consumer entries first (alphabetic),
	// then container-mode seams (alphabetic).
	order []string

	// envSources tags each (consumer, key) with its source so Render
	// can flush in the documented band order.
	envSources map[string]map[string]envSource
}

// envSource tags the origin of an env var on a consumer service. Used
// only for ordering within a single consumer's environment block at
// Render time — the rendered file does not include source labels.
type envSource int

const (
	// envSourceResource: resource-emit injected this env var
	// (DATABASE_URL, REDIS_URL, S3_ENDPOINT_URL, etc.). Flushed first.
	envSourceResource envSource = iota
	// envSourceSeam: seam-emit injected this env var (CARAVAN_RPC_PEERS,
	// CARAVAN_RPC_SHARED_SECRET). Flushed second (layered on top).
	envSourceSeam
)

func newComposeAccumulator() *composeAccumulator {
	return &composeAccumulator{
		services:   map[string]*composeService{},
		envSources: map[string]map[string]envSource{},
	}
}

// AddService registers (or merges into) a compose service. Merge
// semantics:
//
//   - Build / Command: non-nil/non-empty values replace existing.
//   - EnvFile / Profiles / DependsOn / Environment: append.
//
// Environment merge happens at the slice level here for ergonomics
// (small composeService payloads that bundle env vars are fine); per-key
// dedup happens at AddEnv. Most callers should use AddEnv for env vars
// they own and AddService for build/command/profiles/depends_on shape.
func (a *composeAccumulator) AddService(name string, svc composeService) {
	if existing, ok := a.services[name]; ok {
		mergeService(existing, svc)
		return
	}
	cp := svc
	cp.Name = name
	a.services[name] = &cp
	a.order = append(a.order, name)
}

// AddEnv registers one env var on a consumer service. Creates the
// service entry if missing (so callers can AddEnv before AddService).
// Source tags it for deterministic merge order at Render time. Last
// write per key wins.
//
// Returns an error when source==envSourceResource and key has the
// CARAVAN_RPC_ prefix — that namespace is reserved for seam emit.
func (a *composeAccumulator) AddEnv(consumer, key, value string, source envSource) error {
	if source == envSourceResource && strings.HasPrefix(key, "CARAVAN_RPC_") {
		return fmt.Errorf("resource emit may not write env key %q on consumer %q: CARAVAN_RPC_ namespace is reserved for seam emit", key, consumer)
	}
	svc, ok := a.services[consumer]
	if !ok {
		svc = &composeService{Name: consumer}
		a.services[consumer] = svc
		a.order = append(a.order, consumer)
	}
	if a.envSources[consumer] == nil {
		a.envSources[consumer] = map[string]envSource{}
	}
	a.envSources[consumer][key] = source

	// Last-write-wins per key
	for i, kv := range svc.Environment {
		if kv.Key == key {
			svc.Environment[i].Value = value
			return nil
		}
	}
	svc.Environment = append(svc.Environment, composeEnvKV{Key: key, Value: value})
	return nil
}

// Render serializes the accumulator into compose yaml. Per-consumer env
// vars are flushed in band order (resource → seam), alphabetic within
// each band. Service iteration is in insertion order.
func (a *composeAccumulator) Render(targetName, outputDir string) ([]byte, error) {
	services := make([]composeService, 0, len(a.order))
	for _, name := range a.order {
		svc := a.services[name]
		if sources := a.envSources[name]; len(sources) > 0 && len(svc.Environment) > 1 {
			sort.SliceStable(svc.Environment, func(i, j int) bool {
				si := sources[svc.Environment[i].Key]
				sj := sources[svc.Environment[j].Key]
				if si != sj {
					return si < sj
				}
				return svc.Environment[i].Key < svc.Environment[j].Key
			})
		}
		services = append(services, *svc)
	}
	return renderCompose(targetName, outputDir, services)
}

// mergeService merges fields from src into dst. Build / Command
// replace; EnvFile / Profiles / DependsOn / Environment append.
func mergeService(dst *composeService, src composeService) {
	if src.Build != nil {
		dst.Build = src.Build
	}
	if len(src.Command) > 0 {
		dst.Command = src.Command
	}
	if len(src.EnvFile) > 0 {
		dst.EnvFile = append(dst.EnvFile, src.EnvFile...)
	}
	if len(src.Profiles) > 0 {
		dst.Profiles = mergeStringSet(dst.Profiles, src.Profiles)
	}
	dst.DependsOn = append(dst.DependsOn, src.DependsOn...)
	for _, kv := range src.Environment {
		// Per-key dedup: last write wins.
		found := false
		for i, existing := range dst.Environment {
			if existing.Key == kv.Key {
				dst.Environment[i].Value = kv.Value
				found = true
				break
			}
		}
		if !found {
			dst.Environment = append(dst.Environment, kv)
		}
	}
}

func mergeStringSet(a, b []string) []string {
	seen := make(map[string]struct{}, len(a))
	out := make([]string, 0, len(a)+len(b))
	for _, s := range a {
		if _, ok := seen[s]; !ok {
			seen[s] = struct{}{}
			out = append(out, s)
		}
	}
	for _, s := range b {
		if _, ok := seen[s]; !ok {
			seen[s] = struct{}{}
			out = append(out, s)
		}
	}
	return out
}
