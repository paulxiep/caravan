package hcl

import (
	"fmt"

	"github.com/hashicorp/hcl/v2/hclwrite"
	"github.com/zclconf/go-cty/cty"

	"github.com/paulxiep/caravan/internal/compiler"
)

// Per-resource emitters. Each writes one or more `resource {}` blocks
// to body and updates the outputs map with the env-var-name → HCL
// reference expression that the runtime compose env consumes.
//
// Naming conventions:
//   - HCL local name comes from terraformLocalName(resource).
//   - AWS-side name comes from awsResourceName(app, resource, target) —
//     "<app>-<resource>-<target>", lowercased, dash-separated.
//
// PoC-grade defaults: minimal tiers (db.t3.micro, cache.t3.micro, S3
// default storage class). No retention, no versioning, no encryption-
// at-rest tuning beyond AWS defaults. Tighten in v1.

// emitBucket writes an `aws_s3_bucket` and exports `S3_BUCKET` to
// outputs. Bucket name uses the AWS pattern (lowercase, dashed).
//
// PoC: deterministic name → must be globally unique. Adding the
// 12-digit account ID would be safer; for now we rely on the
// per-target suffix to dodge collisions. Document this in the README.
func emitBucket(body *hclwrite.Body, app, target, resName string, _ *compiler.ResolvedResource, outputs map[string]string) {
	hcLocal := terraformLocalName(resName)
	awsName := awsResourceName(app, resName, target)

	b := body.AppendNewBlock("resource", []string{"aws_s3_bucket", hcLocal})
	bb := b.Body()
	bb.SetAttributeValue("bucket", cty.StringVal(awsName))
	bb.SetAttributeValue("force_destroy", cty.BoolVal(true)) // PoC: tofu destroy wipes contents.

	outputs["S3_BUCKET"] = fmt.Sprintf("aws_s3_bucket.%s.bucket", hcLocal)
}

// emitDBSQL writes an `aws_db_instance` (single-AZ, publicly_accessible
// for laptop reachability) and exports DATABASE_URL.
//
// PoC choice: db.t3.micro, 20GB gp2, no multi-AZ, no backups, password
// from yaml. Production grade resides at v1.
func emitDBSQL(body *hclwrite.Body, app, target, resName string, rr *compiler.ResolvedResource, outputs map[string]string) {
	hcLocal := terraformLocalName(resName)
	awsName := awsResourceName(app, resName, target)

	user := firstNonEmpty(rr.User, "caravan")
	password := firstNonEmpty(rr.Password, "caravan")
	dbname := firstNonEmpty(rr.DBName, "caravan")

	b := body.AppendNewBlock("resource", []string{"aws_db_instance", hcLocal})
	bb := b.Body()
	bb.SetAttributeValue("identifier", cty.StringVal(awsName))
	bb.SetAttributeValue("engine", cty.StringVal("postgres"))
	bb.SetAttributeValue("engine_version", cty.StringVal("16"))
	bb.SetAttributeValue("instance_class", cty.StringVal("db.t3.micro"))
	bb.SetAttributeValue("allocated_storage", cty.NumberIntVal(20))
	bb.SetAttributeValue("storage_type", cty.StringVal("gp2"))
	bb.SetAttributeValue("db_name", cty.StringVal(dbname))
	bb.SetAttributeValue("username", cty.StringVal(user))
	bb.SetAttributeValue("password", cty.StringVal(password))
	bb.SetAttributeValue("publicly_accessible", cty.BoolVal(true))
	bb.SetAttributeValue("skip_final_snapshot", cty.BoolVal(true))
	bb.SetAttributeValue("apply_immediately", cty.BoolVal(true))
	// Security group reference — both `aws_db_instance` and the SG
	// emit pre-create their own; the SG locks down to the laptop's IP
	// via data "http" "myip".
	bb.SetAttributeRaw("vpc_security_group_ids", rawHCL("[aws_security_group.caravan_dev.id]"))

	// DATABASE_URL embeds the AWS-resolved endpoint; the `tofu output
	// -json` flow substitutes the actual host:port at runtime.
	outputs["DATABASE_URL"] = fmt.Sprintf(`"postgresql://%s:%s@${aws_db_instance.%s.endpoint}/%s"`, user, password, hcLocal, dbname)
}

// emitQueue writes an `aws_sqs_queue` and exports QUEUE_URL.
func emitQueue(body *hclwrite.Body, app, target, resName string, _ *compiler.ResolvedResource, outputs map[string]string) {
	hcLocal := terraformLocalName(resName)
	awsName := awsResourceName(app, resName, target)

	b := body.AppendNewBlock("resource", []string{"aws_sqs_queue", hcLocal})
	bb := b.Body()
	bb.SetAttributeValue("name", cty.StringVal(awsName))
	// PoC: visibility timeout default (30s), no DLQ, no FIFO.
	bb.SetAttributeValue("visibility_timeout_seconds", cty.NumberIntVal(60))
	bb.SetAttributeValue("message_retention_seconds", cty.NumberIntVal(345600)) // 4 days

	outputs["QUEUE_URL"] = fmt.Sprintf("aws_sqs_queue.%s.url", hcLocal)
}

// emitCache writes an `aws_elasticache_cluster` (single-node Redis 7)
// and exports REDIS_URL.
func emitCache(body *hclwrite.Body, app, target, resName string, _ *compiler.ResolvedResource, outputs map[string]string) {
	hcLocal := terraformLocalName(resName)
	awsName := awsResourceName(app, resName, target)

	b := body.AppendNewBlock("resource", []string{"aws_elasticache_cluster", hcLocal})
	bb := b.Body()
	bb.SetAttributeValue("cluster_id", cty.StringVal(awsName))
	bb.SetAttributeValue("engine", cty.StringVal("redis"))
	bb.SetAttributeValue("engine_version", cty.StringVal("7.1"))
	bb.SetAttributeValue("node_type", cty.StringVal("cache.t3.micro"))
	bb.SetAttributeValue("num_cache_nodes", cty.NumberIntVal(1))
	bb.SetAttributeValue("port", cty.NumberIntVal(6379))
	bb.SetAttributeRaw("security_group_ids", rawHCL("[aws_security_group.caravan_dev.id]"))

	outputs["REDIS_URL"] = fmt.Sprintf(`"redis://${aws_elasticache_cluster.%s.cache_nodes[0].address}:6379"`, hcLocal)
}

// emitSearch writes an `aws_opensearch_domain`. Gated upstream — only
// reached when at least one entry's uses: references this resource.
func emitSearch(body *hclwrite.Body, app, target, resName string, _ *compiler.ResolvedResource, outputs map[string]string) {
	hcLocal := terraformLocalName(resName)
	awsName := awsResourceName(app, resName, target)

	b := body.AppendNewBlock("resource", []string{"aws_opensearch_domain", hcLocal})
	bb := b.Body()
	bb.SetAttributeValue("domain_name", cty.StringVal(awsName))
	bb.SetAttributeValue("engine_version", cty.StringVal("OpenSearch_2.11"))

	cluster := bb.AppendNewBlock("cluster_config", nil)
	cluster.Body().SetAttributeValue("instance_type", cty.StringVal("t3.small.search"))
	cluster.Body().SetAttributeValue("instance_count", cty.NumberIntVal(1))

	ebs := bb.AppendNewBlock("ebs_options", nil)
	ebs.Body().SetAttributeValue("ebs_enabled", cty.BoolVal(true))
	ebs.Body().SetAttributeValue("volume_size", cty.NumberIntVal(10))

	outputs["OPENSEARCH_URL"] = fmt.Sprintf(`"https://${aws_opensearch_domain.%s.endpoint}"`, hcLocal)
}

// emitMyIPLookup writes a `data "http" "myip"` block that fetches the
// laptop's public IP at apply time. Used by the security group when
// VPC-only resources (RDS, ElastiCache) need to be reachable from a
// developer laptop.
func emitMyIPLookup(body *hclwrite.Body) {
	b := body.AppendNewBlock("data", []string{"http", "myip"})
	b.Body().SetAttributeValue("url", cty.StringVal("https://api.ipify.org"))
	body.AppendNewline()
}

// emitSecurityGroup writes one shared SG `caravan_dev` for RDS +
// ElastiCache. Ingress: laptop IP only (data.http.myip). Egress: all.
//
// Dev-only by design — production would use private subnets + VPN.
// Documented in the generated README header.
func emitSecurityGroup(body *hclwrite.Body, app, target string) {
	awsName := awsResourceName(app, "dev", target)
	b := body.AppendNewBlock("resource", []string{"aws_security_group", "caravan_dev"})
	bb := b.Body()
	bb.SetAttributeValue("name", cty.StringVal(awsName))
	bb.SetAttributeValue("description", cty.StringVal("Caravan M4-cloud dev: laptop-IP-only ingress for RDS + ElastiCache"))

	ingress := bb.AppendNewBlock("ingress", nil)
	ig := ingress.Body()
	ig.SetAttributeValue("from_port", cty.NumberIntVal(0))
	ig.SetAttributeValue("to_port", cty.NumberIntVal(0))
	ig.SetAttributeValue("protocol", cty.StringVal("-1"))
	ig.SetAttributeRaw("cidr_blocks", rawHCL(`["${chomp(data.http.myip.response_body)}/32"]`))

	egress := bb.AppendNewBlock("egress", nil)
	eg := egress.Body()
	eg.SetAttributeValue("from_port", cty.NumberIntVal(0))
	eg.SetAttributeValue("to_port", cty.NumberIntVal(0))
	eg.SetAttributeValue("protocol", cty.StringVal("-1"))
	eg.SetAttributeRaw("cidr_blocks", rawHCL(`["0.0.0.0/0"]`))

	body.AppendNewline()
}

// emitOutputs writes one `output {}` block per entry in outputs. Sorted
// by output (env var) name. Values whose expression starts with a
// quote are treated as raw HCL expressions; bare attribute refs
// (`aws_s3_bucket.X.bucket`) become `value = ref`.
func emitOutputs(body *hclwrite.Body, outputs map[string]string) {
	if len(outputs) == 0 {
		return
	}
	names := make([]string, 0, len(outputs))
	for k := range outputs {
		names = append(names, k)
	}
	// Sort for determinism.
	for i := 1; i < len(names); i++ {
		for j := i; j > 0 && names[j-1] > names[j]; j-- {
			names[j-1], names[j] = names[j], names[j-1]
		}
	}
	for _, name := range names {
		expr := outputs[name]
		b := body.AppendNewBlock("output", []string{name})
		b.Body().SetAttributeRaw("value", rawHCL(expr))
	}
}

// rawHCL packages an unstructured HCL expression as the tokens
// hclwrite expects for SetAttributeRaw. We use this for cross-resource
// references (e.g. `aws_security_group.caravan_dev.id`) where the
// cty-typed builders don't fit.
func rawHCL(expr string) hclwrite.Tokens {
	return hclwrite.Tokens{{
		Type:  0,
		Bytes: []byte(" " + expr),
	}}
}

// firstNonEmpty mirrors the helper in compiler/resource_endpoints.go.
func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}
