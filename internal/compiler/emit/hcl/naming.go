// Package hcl is the M4-cloud HCL emitter. It writes Terraform/OpenTofu
// configuration for the resource layer of a caravan target: S3, RDS,
// SQS, ElastiCache, and OpenSearch (gated on actual use). No cloud
// compute resources here — that lands at M4b (Fargate) and M7 (Lambda).
//
// Top-level entry: EmitHCL(rp, outDir) writes four files into outDir:
//
//	backend.tf   — S3+DynamoDB state backend config
//	versions.tf  — provider + tofu version pins
//	main.tf      — provider block + per-resource blocks + outputs
//	iam.tf       — per-entry IAM user_policy with statements derived
//	               from rp.IAMGrants (only when non-empty)
//
// File split is for legibility — tofu init/plan/apply ingest the whole
// directory and don't care about boundaries.
package hcl

import "strings"

// terraformLocalName converts a Plan IR resource name to a valid HCL
// local resource name. HCL allows lowercase letters, digits,
// underscores, and hyphens; identifiers must start with a letter.
// Caravan resource names already follow this shape (e.g. invoice_db,
// invoice_blobs), so this is a defensive normalize.
func terraformLocalName(s string) string {
	if s == "" {
		return ""
	}
	out := make([]byte, 0, len(s))
	for i, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '_':
			out = append(out, byte(r))
		case r >= 'A' && r <= 'Z':
			out = append(out, byte(r-'A'+'a'))
		default:
			if i > 0 {
				out = append(out, '_')
			}
		}
	}
	if len(out) > 0 && out[0] >= '0' && out[0] <= '9' {
		out = append([]byte{'r', '_'}, out...)
	}
	return string(out)
}

// awsResourceName generates the cloud-side resource name caravan uses
// for tofu apply. Pattern: "<app>-<resource>-<target>", lowercased and
// dash-separated. Examples:
//
//	"invoice-parse"+"invoice_blobs"+"hybrid-dev" → "invoice-parse-invoice-blobs-hybrid-dev"
//
// Length-bounded by AWS limits (S3 bucket = 63 chars, RDS identifier =
// 63 chars, SQS = 80 chars). Caravan does not truncate — short names
// belong upstream in the yaml. Trailing dash trimmed.
func awsResourceName(app, resource, target string) string {
	parts := []string{
		toDashed(app),
		toDashed(resource),
		toDashed(target),
	}
	return strings.Trim(strings.Join(parts, "-"), "-")
}

// toDashed lowercases + replaces underscores with dashes for AWS
// resource naming (which prefers dashes).
func toDashed(s string) string {
	s = strings.ToLower(s)
	return strings.ReplaceAll(s, "_", "-")
}
