package compiler

// The (ResourceKind, ResourceVariant) catalog. Lives in the compiler
// package (not emit/) so normalize can validate the surface without
// pulling in emit's container-builder code. The same table is the
// authoritative input to emit/resources.go's container catalog —
// adding a new variant means an entry here AND a builder over there.
//
// M4 (Phase 1) ships one OSS-local default per resource type, plus
// `rabbitmq` as the second `queue` variant to exercise composition
// orthogonality on compose.

// variantTable enumerates which variants are valid for each resource
// kind. First entry per kind is the default applied when the user
// leaves `kind:` empty.
var variantTable = map[ResourceKind][]ResourceVariant{
	ResourceQueue:  {VariantRedisStreams, VariantRabbitMQ},
	ResourceDBSQL:  {VariantPostgres},
	ResourceBucket: {VariantMinIO},
	ResourceCache:  {VariantRedis},
	ResourceSearch: {VariantOpenSearch},
	// kv, stream, llm: no Phase-1 OSS-local variant ships. Resolve
	// leaves Variant empty for these; emit no-ops them.
}

// ValidVariantsFor returns the set of legal variants for a resource
// kind. The first element is the default. Returns nil for resource
// kinds with no Phase-1 OSS-local variant.
func ValidVariantsFor(kind ResourceKind) []ResourceVariant {
	return variantTable[kind]
}

// DefaultVariantFor returns the default variant for a resource kind,
// or empty if none ships at Phase 1.
func DefaultVariantFor(kind ResourceKind) ResourceVariant {
	if vs := variantTable[kind]; len(vs) > 0 {
		return vs[0]
	}
	return ""
}

// IsValidVariant reports whether v is a legal variant for kind. Empty
// v is always valid (resolved to the default at phase 4).
func IsValidVariant(kind ResourceKind, v ResourceVariant) bool {
	if v == "" {
		return true
	}
	for _, allowed := range variantTable[kind] {
		if allowed == v {
			return true
		}
	}
	return false
}
