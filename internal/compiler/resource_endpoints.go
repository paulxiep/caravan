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
//
// M4-cloud (Phase 2 hybrid): cloud-managed branches return
// `${VAR}` passthrough literals — compose interpolates them from the
// user's .env.hybrid (populated by `tofu output -json | jq -r '...'`
// after `tofu apply`). The variable names match the HCL emit's
// `output {}` blocks. by-id composition is still unhandled.
func EndpointEnvVars(rr *ResolvedResource) map[string]string {
	if rr == nil {
		return nil
	}
	switch rr.Composition {
	case CompositionOSSLocal:
		return ossLocalEndpoints(rr)
	case CompositionCloudManaged:
		return cloudManagedEndpoints(rr)
	}
	// by-id stays unhandled at M4-cloud — that's the v1 surface.
	return nil
}

// cloudManagedEndpoints returns the env-var passthroughs for cloud-
// managed resources. Values are compose interpolation refs (`${VAR}`);
// the matching HCL output {} blocks land at the same var names so the
// `tofu output -json` → `.env.hybrid` → compose chain wires up.
//
// Variable naming: scalar per-kind names (S3_BUCKET, DATABASE_URL,
// QUEUE_URL, REDIS_URL, OPENSEARCH_URL). PoC constraint: at most one
// resource per kind in a hybrid target. Future multi-resource shapes
// will need per-resource suffixes.
//
// Notable absences:
//   - AWS_ACCESS_KEY_ID / AWS_SECRET_ACCESS_KEY are NOT injected for
//     cloud-managed — credentials resolve via the mounted `~/.aws` +
//     AWS_PROFILE (set by the creds_passthrough block in compose.go).
//   - S3_ENDPOINT_URL is NOT injected for cloud-managed — boto3's
//     default resolution (no override) targets real AWS S3.
func cloudManagedEndpoints(rr *ResolvedResource) map[string]string {
	switch rr.Type {
	case ResourceBucket:
		// CARAVAN_BLOB_BACKEND asserts which impl the SDK's auto_register
		// must wire up. With backend=s3, an empty S3_BUCKET (e.g. user
		// forgot to populate .env.hybrid from `tofu output`) loud-fails at
		// startup instead of silently falling back to LocalFs.
		return map[string]string{
			"CARAVAN_BLOB_BACKEND": "s3",
			"S3_BUCKET":            "${S3_BUCKET}",
		}
	case ResourceDBSQL:
		return map[string]string{
			"DATABASE_URL": "${DATABASE_URL}",
		}
	case ResourceCache:
		return map[string]string{
			"REDIS_URL": "${REDIS_URL}",
		}
	case ResourceQueue:
		return map[string]string{
			"QUEUE_URL": "${QUEUE_URL}",
		}
	case ResourceSearch:
		return map[string]string{
			"OPENSEARCH_URL": "${OPENSEARCH_URL}",
		}
	}
	return nil
}

func ossLocalEndpoints(rr *ResolvedResource) map[string]string {
	switch rr.Type {
	case ResourceBucket:
		// CARAVAN_BLOB_BACKEND=local-fs tells the SDK's auto_register to
		// use LocalFsBlobStore (not S3). MinIO env vars are emitted in
		// case the user wants to opt into S3-against-MinIO later by
		// flipping the marker + adding S3_BUCKET, but by default they're
		// noise — no service consumes MinIO at M9.
		return map[string]string{
			"CARAVAN_BLOB_BACKEND":  "local-fs",
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
