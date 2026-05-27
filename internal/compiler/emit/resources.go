package emit

import (
	"fmt"

	"github.com/paulxiep/caravan/internal/compiler"
)

// Resource container catalog (M4 Phase 1, compose-only). One entry per
// (ResourceKind, ResourceVariant) combination that caravan emits as an
// OSS-local container. Each entry builds a `composeService` ready for
// `acc.AddService(name, svc)`.
//
// Service names match the variant name (`minio`, `postgres`, `redis`,
// `rabbitmq`, `opensearch`). The names are also the compose-network
// DNS hostnames used by resource_endpoints.go's endpoint URLs — single
// source of truth across the catalog and the env-var table.
//
// Cloud variants (cloud-managed / by-id) emit no container; that path
// lands at M4-cloud (Phase 2) and goes through HCL instead. Phase 1
// no-ops when rr.Composition != oss-local.

// resourceCatalogKey is a (Type, Variant) tuple used as a map key.
type resourceCatalogKey struct {
	Type    compiler.ResourceKind
	Variant compiler.ResourceVariant
}

// resourceBuilder returns the composeService for one resolved
// resource. The variant is the resolved one (defaults already
// applied), so the builder can assume non-empty. The resolved
// resource is passed so builders can read yaml-declared credentials
// (rr.User / rr.Password / rr.DBName) — same values that thread into
// the endpoint env vars (DATABASE_URL etc.) so container creds and
// consumer DSNs stay in lockstep.
type resourceBuilder func(rr *compiler.ResolvedResource) composeService

// resourceCatalog maps (Type, Variant) to its container-builder.
// Empty result (no entry) means "no container emitted for this
// resource at Phase 1" — kv, stream, llm fall here; cloud-managed
// + by-id compositions also short-circuit before lookup.
var resourceCatalog = map[resourceCatalogKey]resourceBuilder{
	{compiler.ResourceBucket, compiler.VariantMinIO}:       buildMinIOService,
	{compiler.ResourceDBSQL, compiler.VariantPostgres}:     buildPostgresService,
	{compiler.ResourceCache, compiler.VariantRedis}:        buildRedisService,
	{compiler.ResourceQueue, compiler.VariantRedisStreams}: buildRedisService, // shared container
	{compiler.ResourceQueue, compiler.VariantRabbitMQ}:     buildRabbitMQService,
	{compiler.ResourceSearch, compiler.VariantOpenSearch}:  buildOpenSearchService,
}

// variantEngineName maps each (Type, Variant) to its compose-service
// engine name — the hostname that lands in the endpoint URL. Multiple
// resources can share an engine (e.g. `cache:redis` and
// `queue:redis-streams` both point at the same `redis` container).
// Must stay in lockstep with resource_endpoints.go's URL hostnames.
var variantEngineName = map[resourceCatalogKey]string{
	{compiler.ResourceBucket, compiler.VariantMinIO}:       "minio",
	{compiler.ResourceDBSQL, compiler.VariantPostgres}:     "postgres",
	{compiler.ResourceCache, compiler.VariantRedis}:        "redis",
	{compiler.ResourceQueue, compiler.VariantRedisStreams}: "redis",
	{compiler.ResourceQueue, compiler.VariantRabbitMQ}:     "rabbitmq",
	{compiler.ResourceSearch, compiler.VariantOpenSearch}:  "opensearch",
}

// resourceServiceNameFor returns the compose service name a resolved
// resource would publish to (e.g. `minio`, `redis`, `rabbitmq`).
// Empty string when no Phase-1 container ships for this combination.
// Used by both the emitter (to call acc.AddService) and the collision
// detector (base_compose_scan.go) to decide whether the user's
// hand-authored compose already provides this service.
func resourceServiceNameFor(rr *compiler.ResolvedResource) string {
	if rr == nil || rr.Composition != compiler.CompositionOSSLocal {
		return ""
	}
	return variantEngineName[resourceCatalogKey{rr.Type, rr.Variant}]
}

// buildResourceService returns the composeService for a resolved
// resource. Returns (composeService{}, false) when the resource has no
// Phase-1 container (cloud composition, unsupported kind, etc.).
func buildResourceService(rr *compiler.ResolvedResource) (composeService, bool) {
	if rr == nil || rr.Composition != compiler.CompositionOSSLocal {
		return composeService{}, false
	}
	builder, ok := resourceCatalog[resourceCatalogKey{rr.Type, rr.Variant}]
	if !ok {
		return composeService{}, false
	}
	svc := builder(rr)
	svc.Name = resourceServiceNameFor(rr)
	return svc, true
}

// --- per-variant builders ---------------------------------------------------
//
// PoC-grade defaults: hardcoded credentials, single-replica, no
// persistence volumes beyond what the OSS images default to. M4-cloud
// (Phase 2) substitutes managed-service references; persistence /
// scaling tuning is post-PoC.

func buildMinIOService(_ *compiler.ResolvedResource) composeService {
	// MinIO is the S3 wire-compatible local engine. Credentials match
	// resource_endpoints.go::bucket → AWS_ACCESS_KEY_ID/SECRET. The
	// `server /data` command starts MinIO in single-node mode; the
	// `console-address :9001` flag exposes the web console for poking
	// during development. Port 9000 is the S3 API.
	return composeService{
		Image: "minio/minio:latest",
		Environment: []composeEnvKV{
			{Key: "MINIO_ROOT_USER", Value: "minioadmin"},
			{Key: "MINIO_ROOT_PASSWORD", Value: "minioadmin"},
		},
		Command:  []string{"server", "/data", "--console-address", ":9001"},
		Ports:    []string{"9000:9000", "9001:9001"},
		Profiles: []string{AppProfile},
	}
}

func buildPostgresService(rr *compiler.ResolvedResource) composeService {
	// Postgres credentials track yaml-declared `user/password/dbname`
	// on the resource (with caravan/caravan/caravan fallbacks). The
	// same values feed resource_endpoints.go::db.sql so the
	// container creds and the DATABASE_URL consumers see stay in
	// lockstep.
	user := firstNonEmpty(rr.User, "caravan")
	password := firstNonEmpty(rr.Password, "caravan")
	dbname := firstNonEmpty(rr.DBName, "caravan")
	return composeService{
		Image: "postgres:16-alpine",
		Environment: []composeEnvKV{
			{Key: "POSTGRES_USER", Value: user},
			{Key: "POSTGRES_PASSWORD", Value: password},
			{Key: "POSTGRES_DB", Value: dbname},
		},
		Ports:    []string{"5432:5432"},
		Profiles: []string{AppProfile},
	}
}

// firstNonEmpty mirrors the helper in compiler/resource_endpoints.go
// but lives in the emit package to avoid an extra import.
func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}

func buildRedisService(_ *compiler.ResolvedResource) composeService {
	// One redis container backs both `cache` (REDIS_URL) and `queue`
	// (QUEUE_URL=redis://... for the redis-streams variant). Service
	// name is "redis"; the catalog folds both keys into this builder.
	return composeService{
		Image:    "redis:7-alpine",
		Ports:    []string{"6379:6379"},
		Profiles: []string{AppProfile},
	}
}

func buildRabbitMQService(_ *compiler.ResolvedResource) composeService {
	// RabbitMQ AMQP broker for `queue` with `kind: rabbitmq`. The
	// `management` tag bundles the web UI on port 15672 — useful for
	// the M4 composition-flip demo. Credentials match
	// resource_endpoints.go::queue/rabbitmq → amqp://guest:guest@...
	return composeService{
		Image: "rabbitmq:3-management-alpine",
		Environment: []composeEnvKV{
			{Key: "RABBITMQ_DEFAULT_USER", Value: "guest"},
			{Key: "RABBITMQ_DEFAULT_PASS", Value: "guest"},
		},
		Ports:    []string{"5672:5672", "15672:15672"},
		Profiles: []string{AppProfile},
	}
}

func buildOpenSearchService(_ *compiler.ResolvedResource) composeService {
	// OpenSearch single-node for local dev. Security plugin disabled
	// (no TLS, no auth) to match resource_endpoints.go::search →
	// OPENSEARCH_URL=http://opensearch:9200 (plain HTTP).
	return composeService{
		Image: "opensearchproject/opensearch:2",
		Environment: []composeEnvKV{
			{Key: "discovery.type", Value: "single-node"},
			{Key: "plugins.security.disabled", Value: "true"},
			{Key: "OPENSEARCH_INITIAL_ADMIN_PASSWORD", Value: "Caravan!Dev1"},
		},
		Ports:    []string{"9200:9200"},
		Profiles: []string{AppProfile},
	}
}

// emitResources is the M4 phase-5 entry point — called from
// EmitComposeOverride. It walks the resolved resources, builds a
// container for each oss-local one that's not already named in the
// base compose (the `existing` set), and registers them in the
// accumulator. Returns nil iff every resource emit succeeded.
func emitResources(
	acc *composeAccumulator,
	rp *compiler.ResolvedPlan,
	existing map[string]bool,
) error {
	for _, name := range sortedKeys(rp.ResolvedResources) {
		rr := rp.ResolvedResources[name]
		svcName := resourceServiceNameFor(rr)
		if svcName == "" {
			continue // no Phase-1 container for this kind/variant
		}
		if existing[svcName] {
			continue // hand-authored compose already provides this service
		}
		svc, ok := buildResourceService(rr)
		if !ok {
			return fmt.Errorf("resources.%s: no container builder for (%s, %s)", name, rr.Type, rr.Variant)
		}
		acc.AddService(svcName, svc)
	}
	return nil
}

// emitResourceEnvVars folds rp.ResourceEnvVars into the accumulator,
// tagging each entry with envSourceResource so the band-ordering
// + CARAVAN_RPC_ namespace check fire correctly.
func emitResourceEnvVars(acc *composeAccumulator, rp *compiler.ResolvedPlan) error {
	for _, consumer := range sortedKeys(rp.ResourceEnvVars) {
		envs := rp.ResourceEnvVars[consumer]
		for _, key := range sortedKeys(envs) {
			if err := acc.AddEnv(consumer, key, envs[key], envSourceResource); err != nil {
				return err
			}
		}
	}
	return nil
}
