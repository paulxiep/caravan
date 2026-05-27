package hcl

import (
	"fmt"
	"strings"

	"github.com/hashicorp/hcl/v2/hclwrite"
	"github.com/zclconf/go-cty/cty"

	"github.com/paulxiep/caravan/internal/compiler"
)

// emitLambdaSeams writes HCL for each `mode: lambda` seam in the target:
// an aws_lambda_function (container image source) + aws_lambda_function_url
// (AuthType=AWS_IAM) + a shared execution role. Called from renderMain
// regardless of target.Runtime — Lambda seams compose with any host
// runtime (today: Fargate-entry callers; later: compose-local-mixed).
//
// Image source convention: each Lambda seam's image uses the host entry's
// ECR repo with a per-seam tag (`lambda-<seamname>`). The slim variant
// (when seam.ImageTarget is set) is built by `caravan up` with
// `docker build --target=<ImageTarget>` and pushed under that tag. ECR
// repos are pre-created per the M4-cloud-prereq onboarding checklist.
//
// IAM caller grants (lambda:InvokeFunctionUrl on the entry's task role)
// are emitted from iam.go alongside the entry's resource grants.
func emitLambdaSeams(body *hclwrite.Body, rp *compiler.ResolvedPlan, target *compiler.Target, perEntryBindings map[string][]EnvBinding, outputs map[string]string) {
	seams := lambdaConsumers(rp, target)
	if len(seams) == 0 {
		return
	}

	emitLambdaExecutionRole(body)

	for _, c := range seams {
		emitLambdaFunction(body, target, c, perEntryBindings)
		emitLambdaFunctionURL(body, c, outputs)
	}
}

// lambdaConsumer is the per-seam shape emitLambdaSeams iterates over.
type lambdaConsumer struct {
	Name      string          // plan IR seam name (PascalCase, e.g. ValidateExtraction)
	Seam      *compiler.Seam  // backing IR
	HostEntry *compiler.Entry // entry whose ECR repo + Dockerfile we reuse
}

// lambdaConsumers walks target.Seams alphabetically, picking out
// SeamLambda entries and pairing each with its host entry. Stable order
// keeps emitted HCL diffs deterministic.
func lambdaConsumers(rp *compiler.ResolvedPlan, target *compiler.Target) []lambdaConsumer {
	out := make([]lambdaConsumer, 0)
	for _, name := range sortedKeysSeams(target) {
		if target.Seams[name] != compiler.SeamLambda {
			continue
		}
		s := rp.Plan.Seams[name]
		if s == nil {
			continue
		}
		host := lambdaHostEntry(rp, target, s)
		out = append(out, lambdaConsumer{Name: name, Seam: s, HostEntry: host})
	}
	return out
}

// lambdaHostEntry returns the entry whose binary/image carries the
// seam's impl code. Resolution order:
//
//  1. Path-match: the first alphabetically-sorted entry whose `path:`
//     equals the seam's `path:`. Used when caravan.yaml declares both
//     explicitly (invoice-parse separates entries by directory).
//  2. Fallback: the first alphabetically-sorted container-mode entry in
//     the target. Used by code-rag, which declares no per-seam path
//     because all seam impls live in the single repo-root entry.
//
// Returns nil only when the target has no container entries — caught
// upstream by validateFargateTarget for Fargate, surfaces as a clear
// `tofu plan` error otherwise.
func lambdaHostEntry(rp *compiler.ResolvedPlan, target *compiler.Target, seam *compiler.Seam) *compiler.Entry {
	if rp == nil || rp.Plan == nil {
		return nil
	}
	names := make([]string, 0, len(rp.Plan.Entries))
	for n := range rp.Plan.Entries {
		names = append(names, n)
	}
	sortStrings(names)
	// 1. Path-match.
	if seam != nil && seam.Path != "" {
		for _, n := range names {
			e := rp.Plan.Entries[n]
			if e != nil && e.Path != "" && e.Path == seam.Path {
				return e
			}
		}
	}
	// 2. Fallback: first container-mode entry in this target.
	for _, n := range names {
		if target != nil && target.Entries[n] != compiler.EntryContainer {
			continue
		}
		if e := rp.Plan.Entries[n]; e != nil {
			return e
		}
	}
	return nil
}

// emitLambdaExecutionRole writes the per-target Lambda execution role +
// the AWSLambdaBasicExecutionRole managed-policy attachment (covers
// CloudWatch Logs writes — every Lambda needs this). One role shared
// across all Lambda seams in the target; the role is principal-agnostic
// (no seam-specific permissions; those come from the caller's task role
// for outbound, and from M9-cloud's AWS-resource grants when Lambda
// seams start touching cloud resources directly).
func emitLambdaExecutionRole(body *hclwrite.Body) {
	role := body.AppendNewBlock("resource", []string{"aws_iam_role", "caravan_lambda_execution"})
	rb := role.Body()
	rb.SetAttributeValue("name", cty.StringVal("caravan-lambda-execution"))
	rb.SetAttributeRaw("assume_role_policy", rawHCL("jsonencode("+lambdaAssumeRolePolicy()+")"))
	body.AppendNewline()

	attach := body.AppendNewBlock("resource", []string{"aws_iam_role_policy_attachment", "caravan_lambda_execution"})
	ab := attach.Body()
	ab.SetAttributeRaw("role", rawHCL("aws_iam_role.caravan_lambda_execution.name"))
	ab.SetAttributeValue("policy_arn", cty.StringVal("arn:aws:iam::aws:policy/service-role/AWSLambdaBasicExecutionRole"))
	body.AppendNewline()
}

// lambdaAssumeRolePolicy returns the inner JSON document trusting the
// Lambda service principal.
func lambdaAssumeRolePolicy() string {
	return `{
    Version = "2012-10-17"
    Statement = [
      {
        Effect = "Allow"
        Principal = { Service = "lambda.amazonaws.com" }
        Action = "sts:AssumeRole"
      }
    ]
  }`
}

// emitLambdaFunction writes one aws_lambda_function block per Lambda
// seam. Container image source, image URI = host entry's ECR repo + per-
// seam tag (`lambda-<seam>`). Memory + timeout are conservative defaults
// suitable for the M7 demos; future tuning can lift these into yaml.
//
// Environment: CARAVAN_RPC_LAMBDA_INTERFACE, CARAVAN_RPC_LAMBDA_IMPL,
// and CARAVAN_RPC_LAMBDA_INTERFACE_MODULE drive caravan_rpc.lambda_handler
// at cold start. CARAVAN_RPC_PEERS isn't injected — a Lambda peer never
// dispatches outward through the SDK in M7's seam carving.
func emitLambdaFunction(body *hclwrite.Body, target *compiler.Target, c lambdaConsumer, perEntryBindings map[string][]EnvBinding) {
	local := terraformLocalName(c.Name)
	funcName := fmt.Sprintf("caravan-%s-%s", toDashed(target.Name), toDashed(c.Name))

	// ECR repo = host entry's repo (data block emitted by
	// emitECRRepoLookup from compute_fargate.go when target.Runtime ==
	// Fargate). When the target runtime is something else (future
	// lambda-mixed compose target), we still need the lookup — emitted
	// here as a defensive shortcut so Lambda-only test targets work.
	ecrLocal := "caravan_" + terraformLocalName(c.HostEntry.Name)
	imageURI := fmt.Sprintf(`"${data.aws_ecr_repository.%s.repository_url}:lambda-%s"`,
		ecrLocal, toDashed(c.Name))

	b := body.AppendNewBlock("resource", []string{"aws_lambda_function", local})
	bb := b.Body()
	bb.SetAttributeValue("function_name", cty.StringVal(funcName))
	bb.SetAttributeValue("package_type", cty.StringVal("Image"))
	bb.SetAttributeRaw("image_uri", rawHCL(imageURI))
	bb.SetAttributeRaw("role", rawHCL("aws_iam_role.caravan_lambda_execution.arn"))
	bb.SetAttributeValue("memory_size", cty.NumberIntVal(512))
	bb.SetAttributeValue("timeout", cty.NumberIntVal(30))

	// Lambda env: caravan-internal vars (CARAVAN_RPC_*) inlined as cty
	// literals; user-app bindings (declared secrets + env_file +
	// environment block passthroughs) mixed in as either literals or
	// `var.X` refs. Because cty.ObjectVal needs typed values and we
	// need HCL var refs side-by-side, the variables map is emitted as
	// a raw HCL expression rather than a cty object.
	envBlock := bb.AppendNewBlock("environment", nil)
	eb := envBlock.Body()
	envExpr := lambdaEnvExpr(c, perEntryBindings)
	eb.SetAttributeRaw("variables", rawHCL(envExpr))

	body.AppendNewline()
}

// lambdaEnvExpr returns the HCL expression for the Lambda's
// `environment { variables = ... }` map. Emitted as a raw `{ K = V, ... }`
// HCL object so caravan-internal literal values, user-app literal
// bindings, and tofu `var.X` references can all coexist (cty.ObjectVal
// only accepts already-typed values, which doesn't compose with HCL
// refs).
//
// Bindings are sourced from perEntryBindings[c.HostEntry.Name]: declared
// secrets + env_file + base compose `environment:` block, computed by
// ComputeBindings. The Lambda image is built from the host entry's
// Dockerfile (lambda-slim stage), so the runtime expects the same
// user-app env shape as the host's Fargate task.
func lambdaEnvExpr(c lambdaConsumer, perEntryBindings map[string][]EnvBinding) string {
	type kv struct {
		Key  string
		Expr string // already an HCL expression (quoted literal or var.X)
	}
	entries := []kv{
		{Key: "CARAVAN_RPC_ROLE", Expr: fmt.Sprintf("%q", "peer-"+c.Name)},
		{Key: "CARAVAN_RPC_LAMBDA_INTERFACE", Expr: fmt.Sprintf("%q", c.Name)},
		{Key: "CARAVAN_RPC_LAMBDA_IMPL", Expr: fmt.Sprintf("%q", c.Seam.Impl)},
	}
	if c.Seam.Impl != "" {
		// CARAVAN_RPC_LAMBDA_INTERFACE_MODULE: same module as Impl unless
		// the seam explicitly declares otherwise. For Python seams,
		// caravan_rpc.lambda_handler reads this at cold start; Rust runs
		// dispatch by CARAVAN_RPC_ROLE + inventory factory and ignores it.
		entries = append(entries, kv{Key: "CARAVAN_RPC_LAMBDA_INTERFACE_MODULE", Expr: `""`})
	}
	if c.HostEntry != nil {
		for _, b := range perEntryBindings[c.HostEntry.Name] {
			if b.Literal != "" {
				entries = append(entries, kv{Key: b.Key, Expr: fmt.Sprintf("%q", b.Literal)})
				continue
			}
			if b.VarName != "" {
				entries = append(entries, kv{Key: b.Key, Expr: "var." + b.VarName})
			}
		}
	}
	// Stable order (alphabetical) for deterministic diffs.
	for i := 1; i < len(entries); i++ {
		for j := i; j > 0 && entries[j-1].Key > entries[j].Key; j-- {
			entries[j-1], entries[j] = entries[j], entries[j-1]
		}
	}
	var b strings.Builder
	b.WriteString("{\n")
	for _, e := range entries {
		b.WriteString(fmt.Sprintf("      %s = %s\n", e.Key, e.Expr))
	}
	b.WriteString("    }")
	return b.String()
}

// emitLambdaFunctionURL writes the Function URL + caches its function_url
// attribute as a per-seam output. AuthType=AWS_IAM means callers must
// SigV4-sign their requests; caravan-rpc's Rust + Python clients do this
// when the peer table marks the seam as lambda-mode.
func emitLambdaFunctionURL(body *hclwrite.Body, c lambdaConsumer, outputs map[string]string) {
	local := terraformLocalName(c.Name)

	b := body.AppendNewBlock("resource", []string{"aws_lambda_function_url", local})
	bb := b.Body()
	bb.SetAttributeRaw("function_name", rawHCL("aws_lambda_function."+local+".function_name"))
	bb.SetAttributeValue("authorization_type", cty.StringVal("AWS_IAM"))
	body.AppendNewline()

	outputs["CARAVAN_RPC_"+c.Name+"_FUNCTION_URL"] = "aws_lambda_function_url." + local + ".function_url"
}
