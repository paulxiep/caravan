package hcl

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/hashicorp/hcl/v2/hclwrite"
	"github.com/zclconf/go-cty/cty"

	"github.com/paulxiep/caravan/internal/compiler"
)

// renderIAM writes iam.tf — one `aws_iam_user_policy` per entry that
// consumes cloud-managed resources, attached to the M4-cloud-prereq
// IAM user. The user name is the same as AwsProfile (the convention
// in docs/aws_onboarding_checklist.md): the `caravan-poc` profile uses
// the `caravan-poc` IAM user.
//
// Why user-policies (not role-policies): M4-cloud has no compute. The
// compose containers authenticate via the developer's `~/.aws` which
// holds long-lived access keys tied to the IAM user. M4b switches to
// Fargate roles; M7 switches to Lambda roles. At that point this file
// gets re-emit with `aws_iam_role_policy` and per-entry roles.
//
// Policy shape: one statement per (entry, resource) pair from the
// resolved IAMGrants. Resources reference the cloud-side ARN of the
// resource caravan also emits in main.tf (via the HCL identifier
// derived from terraformLocalName).
func renderIAM(rp *compiler.ResolvedPlan, target *compiler.Target) []byte {
	f := hclwrite.NewEmptyFile()
	body := f.Body()
	body.AppendUnstructuredTokens(headerTokens("iam.tf", target.Name))

	// data block to look up the existing IAM user — caravan does NOT
	// create it (that's M4-cloud-prereq's job, see
	// docs/aws_onboarding_checklist.md).
	data := body.AppendNewBlock("data", []string{"aws_iam_user", "caravan_poc"})
	data.Body().SetAttributeValue("user_name", cty.StringVal(target.AwsProfile))
	body.AppendNewline()

	// One policy per entry. Stable order: sorted entry names.
	entries := make([]string, 0, len(rp.IAMGrants))
	for e := range rp.IAMGrants {
		entries = append(entries, e)
	}
	sort.Strings(entries)

	for _, entryName := range entries {
		stmts := rp.IAMGrants[entryName]
		if len(stmts) == 0 {
			continue
		}
		emitEntryPolicy(body, target, entryName, stmts)
		body.AppendNewline()
	}

	return f.Bytes()
}

// emitEntryPolicy writes one aws_iam_user_policy with statements
// derived from the entry's IAM grants. Policy name embeds the target
// and entry so multiple targets coexist on the same IAM user.
func emitEntryPolicy(body *hclwrite.Body, target *compiler.Target, entryName string, stmts []compiler.IAMStatement) {
	hcLocal := terraformLocalName(entryName)
	policyName := fmt.Sprintf("caravan-%s-%s", toDashed(target.Name), toDashed(entryName))

	b := body.AppendNewBlock("resource", []string{"aws_iam_user_policy", hcLocal})
	bb := b.Body()
	bb.SetAttributeValue("name", cty.StringVal(policyName))
	bb.SetAttributeRaw("user", rawHCL("data.aws_iam_user.caravan_poc.user_name"))

	policy := iamPolicyJSON(stmts, target)
	bb.SetAttributeRaw("policy", rawHCL("jsonencode("+policy+")"))
}

// iamPolicyJSON builds the inner JSON object literal for the policy
// attribute. Emitted as raw HCL so cross-resource ARN refs land
// unquoted inside the policy (e.g. `Resource = [aws_s3_bucket.X.arn]`).
func iamPolicyJSON(stmts []compiler.IAMStatement, target *compiler.Target) string {
	var b strings.Builder
	b.WriteString("{\n")
	b.WriteString(`    Version = "2012-10-17"`)
	b.WriteString("\n    Statement = [\n")
	for i, s := range stmts {
		b.WriteString("      {\n")
		b.WriteString(`        Effect = "Allow"`)
		b.WriteString("\n        Action = ")
		actionsBytes, _ := json.Marshal(s.Actions)
		b.WriteString(string(actionsBytes))
		b.WriteString("\n        Resource = ")
		b.WriteString(iamResourceExpr(s, target))
		b.WriteString("\n      }")
		if i < len(stmts)-1 {
			b.WriteString(",")
		}
		b.WriteString("\n")
	}
	b.WriteString("    ]\n")
	b.WriteString("  }")
	return b.String()
}

// iamResourceExpr returns the HCL expression for the policy
// statement's Resource field. S3 buckets need both bucket and object
// ARNs; SQS / OpenSearch return a single ARN.
func iamResourceExpr(s compiler.IAMStatement, _ *compiler.Target) string {
	local := terraformLocalName(s.ResourceRef)
	switch s.ResourceKind {
	case compiler.ResourceBucket:
		// s3:ListBucket needs the bucket ARN; s3:GetObject etc need
		// the object ARN. Grant both for PoC simplicity.
		return fmt.Sprintf("[aws_s3_bucket.%s.arn, \"${aws_s3_bucket.%s.arn}/*\"]", local, local)
	case compiler.ResourceQueue:
		return fmt.Sprintf("[aws_sqs_queue.%s.arn]", local)
	case compiler.ResourceSearch:
		return fmt.Sprintf("[aws_opensearch_domain.%s.arn, \"${aws_opensearch_domain.%s.arn}/*\"]", local, local)
	case compiler.ResourceStream:
		return fmt.Sprintf("[aws_kinesis_stream.%s.arn]", local)
	}
	// Fall-through: unknown kind. Empty list — tofu plan will surface
	// the issue. Caravan does not error here because new resource
	// kinds get added at this layer last.
	return "[]"
}
