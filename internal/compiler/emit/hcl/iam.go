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

// renderIAM writes iam.tf. The attachment shape depends on the target's
// IAM principal kind (see compiler.PrincipalForTarget):
//
//	PrincipalIAMUser              → one `data "aws_iam_user"` lookup
//	                                + `aws_iam_user_policy` per entry.
//	                                Used by M4-cloud's hybrid-dev: compose
//	                                containers authenticate via the
//	                                developer's ~/.aws (long-lived keys on
//	                                the IAM user).
//	PrincipalFargateTaskRole      → `aws_iam_role` (one per entry, with an
//	                                ecs-tasks.amazonaws.com assume-role
//	                                policy) + `aws_iam_role_policy` per
//	                                entry. Used by M4b's staging-fargate:
//	                                each Fargate task assumes its own role.
//	PrincipalLambdaExecutionRole  → M7 fills this in (Lambda execution
//	                                role + per-caller InvokeFunctionUrl
//	                                grants). Returns an empty file for now
//	                                so a target accidentally tagged with
//	                                this principal in pre-M7 code fails at
//	                                tofu plan, not in emit.
//
// Per-entry policy statements come from rp.IAMGrants (built in
// resolve_iam.go, principal-agnostic). The iamPolicyJSON / iamResourceExpr
// helpers below are shared across principals — they build the inner
// Statement[] array, which is the same shape regardless of who holds the
// policy.
func renderIAM(rp *compiler.ResolvedPlan, target *compiler.Target) []byte {
	principal := compiler.PrincipalForTarget(target)
	if principal == compiler.PrincipalLambdaExecutionRole {
		// M7 territory. Returning nil signals the caller to skip iam.tf
		// emit entirely so we don't ship an empty file. The Lambda path
		// will replace this stub.
		return nil
	}

	f := hclwrite.NewEmptyFile()
	body := f.Body()
	body.AppendUnstructuredTokens(headerTokens("iam.tf", target.Name))

	// User-mode needs a single shared data lookup. Role-mode emits its
	// roles inline per entry.
	if principal == compiler.PrincipalIAMUser {
		data := body.AppendNewBlock("data", []string{"aws_iam_user", "caravan_poc"})
		data.Body().SetAttributeValue("user_name", cty.StringVal(target.AwsProfile))
		body.AppendNewline()
	}

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
		if principal == compiler.PrincipalFargateTaskRole {
			emitFargateTaskRole(body, target, entryName)
		}
		emitEntryPolicy(body, target, entryName, stmts, principal)
		body.AppendNewline()
	}

	return f.Bytes()
}

// emitFargateTaskRole writes an `aws_iam_role` block that each Fargate
// task assumes at startup. The assume-role policy trusts the ECS tasks
// service principal — ECS attaches the role to the task at run time.
//
// Per-entry roles (not a shared cluster-wide role) so the policy attached
// in emitEntryPolicy stays scoped to just that entry's IAM grants. Cheap
// (roles are free) and follows least-privilege.
func emitFargateTaskRole(body *hclwrite.Body, target *compiler.Target, entryName string) {
	hcLocal := terraformLocalName(entryName)
	roleName := fmt.Sprintf("caravan-%s-%s-task", toDashed(target.Name), toDashed(entryName))

	b := body.AppendNewBlock("resource", []string{"aws_iam_role", hcLocal})
	bb := b.Body()
	bb.SetAttributeValue("name", cty.StringVal(roleName))
	bb.SetAttributeRaw("assume_role_policy", rawHCL("jsonencode("+fargateAssumeRolePolicy()+")"))
}

// fargateAssumeRolePolicy returns the inner JSON document for the ECS
// task assume-role policy. ecs-tasks.amazonaws.com is the AWS-published
// service principal for Fargate task identity.
func fargateAssumeRolePolicy() string {
	return `{
    Version = "2012-10-17"
    Statement = [
      {
        Effect = "Allow"
        Principal = { Service = "ecs-tasks.amazonaws.com" }
        Action = "sts:AssumeRole"
      }
    ]
  }`
}

// emitEntryPolicy writes one policy attachment for the entry. The block
// type and the attachment target field name depend on PrincipalKind:
//
//	IAMUser           → aws_iam_user_policy.user = data.aws_iam_user.caravan_poc.user_name
//	FargateTaskRole   → aws_iam_role_policy.role = aws_iam_role.<entry>.name
//
// The policy document (Statement[]) is identical across both — built by
// iamPolicyJSON from the same rp.IAMGrants statements.
func emitEntryPolicy(body *hclwrite.Body, target *compiler.Target, entryName string, stmts []compiler.IAMStatement, principal compiler.PrincipalKind) {
	hcLocal := terraformLocalName(entryName)
	policyName := fmt.Sprintf("caravan-%s-%s", toDashed(target.Name), toDashed(entryName))

	var blockType, holderField, holderRef string
	switch principal {
	case compiler.PrincipalFargateTaskRole:
		blockType = "aws_iam_role_policy"
		holderField = "role"
		holderRef = fmt.Sprintf("aws_iam_role.%s.name", hcLocal)
	default: // PrincipalIAMUser
		blockType = "aws_iam_user_policy"
		holderField = "user"
		holderRef = "data.aws_iam_user.caravan_poc.user_name"
	}

	b := body.AppendNewBlock("resource", []string{blockType, hcLocal})
	bb := b.Body()
	bb.SetAttributeValue("name", cty.StringVal(policyName))
	bb.SetAttributeRaw(holderField, rawHCL(holderRef))

	policy := iamPolicyJSON(stmts, target)
	bb.SetAttributeRaw("policy", rawHCL("jsonencode("+policy+")"))
}

// iamPolicyJSON builds the inner JSON object literal for the policy
// attribute. Emitted as raw HCL so cross-resource ARN refs land
// unquoted inside the policy (e.g. `Resource = [aws_s3_bucket.X.arn]`).
//
// Principal-agnostic: the same statements attach equivalently to a user
// policy or a role policy. The principal-specific scaffolding (role
// definition, attachment block type) lives in emitFargateTaskRole +
// emitEntryPolicy.
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
