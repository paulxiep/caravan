package compiler

// Resource endpoint env-vars are the "URL/DSN-shaped" environment
// variables that consumer entries read at runtime to reach a resource
// (e.g. DATABASE_URL, REDIS_URL, S3_ENDPOINT_URL). The shape is per
// (ResourceKind, ResourceVariant); M4 (Phase 1) ships the compose-DNS
// values for each variant. Phase 2 / M4-cloud overlays cloud-managed
// values onto the same keys.
//
// Lives next to variants.go (not in internal/compiler/emit/) so the
// resolve phase can compute per-consumer env vars without depending
// on emit. The emit phase reads ResolvedPlan.ResourceEnvVars and
// dispatches each key/value into the M3 composeAccumulator with the
// SourceResource tag.

// firstNonEmpty returns the first non-empty string from its args, or
// "" if all are empty. Used to layer yaml-declared resource fields on
// top of caravan defaults.
func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}

// EndpointEnvVars returns the env-var map for one resolved resource.
// Keys are env var names (DATABASE_URL etc.); values are the compose-
// DNS endpoint strings. Returns empty map for resources whose variant
// has no Phase-1 OSS-local container (kv, stream, llm).
//
// Phase-1 values assume default ports and the convention that the
// resource container's compose service name matches its variant name
// (postgres, redis, rabbitmq, minio, opensearch). Phase 2 cloud
// overlays a different value-set for the same keys.
//
// Hardcoded credentials (minio root user, postgres password) are
// PoC-grade defaults matching the Phase-1 demo. M4-cloud / M7 swap
// them for real secret-store references.
func EndpointEnvVars(rr *ResolvedResource) map[string]string {
	if rr == nil {
		return nil
	}
	// Phase 1: only oss-local emits a wired endpoint. cloud-managed +
	// by-id flow through Phase 2 path (M4-cloud) and inject their own
	// values; M4 Phase 1 just returns empty for those.
	if rr.Composition != CompositionOSSLocal {
		return nil
	}
	switch rr.Type {
	case ResourceBucket:
		// S3 wire-API compat (boto3 reads S3_ENDPOINT_URL).
		return map[string]string{
			"S3_ENDPOINT_URL":       "http://minio:9000",
			"AWS_ACCESS_KEY_ID":     "minioadmin",
			"AWS_SECRET_ACCESS_KEY": "minioadmin",
			"AWS_REGION":            "us-east-1",
		}
	case ResourceDBSQL:
		user := firstNonEmpty(rr.User, "caravan")
		password := firstNonEmpty(rr.Password, "caravan")
		dbname := firstNonEmpty(rr.DBName, "caravan")
		return map[string]string{
			"DATABASE_URL": "postgresql://" + user + ":" + password + "@postgres:5432/" + dbname,
		}
	case ResourceCache:
		return map[string]string{
			"REDIS_URL": "redis://redis:6379",
		}
	case ResourceQueue:
		switch rr.Variant {
		case VariantRabbitMQ:
			return map[string]string{
				"QUEUE_URL": "amqp://guest:guest@rabbitmq:5672",
			}
		case VariantRedisStreams, "":
			// Default: redis-streams reuses the cache redis container.
			return map[string]string{
				"QUEUE_URL": "redis://redis:6379",
			}
		}
	case ResourceSearch:
		return map[string]string{
			"OPENSEARCH_URL": "http://opensearch:9200",
		}
	}
	return nil
}
