package hcl

import (
	"fmt"

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
func emitLambdaSeams(body *hclwrite.Body, rp *compiler.ResolvedPlan, target *compiler.Target, outputs map[string]string) {
	seams := lambdaConsumers(rp, target)
	if len(seams) == 0 {
		return
	}

	emitLambdaExecutionRole(body)

	for _, c := range seams {
		emitLambdaFunction(body, target, c)
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
func emitLambdaFunction(body *hclwrite.Body, target *compiler.Target, c lambdaConsumer) {
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

	envBlock := bb.AppendNewBlock("environment", nil)
	eb := envBlock.Body()
	envVars := lambdaEnvVars(c)
	eb.SetAttributeValue("variables", cty.ObjectVal(envVars))

	body.AppendNewline()
}

// lambdaEnvVars returns the per-seam Lambda env var map. The Python
// caravan_rpc.lambda_handler reads these at cold start to bind the
// interface + impl class. For Rust, run_or_serve auto-detects the
// AWS_LAMBDA_RUNTIME_API env var (set by AWS) and uses CARAVAN_RPC_ROLE
// + the inventory factory for dispatch — Rust seams don't need these
// env vars but emitting them is harmless and keeps the surface
// consistent across languages.
func lambdaEnvVars(c lambdaConsumer) map[string]cty.Value {
	out := map[string]cty.Value{
		"CARAVAN_RPC_ROLE":             cty.StringVal("peer-" + c.Name),
		"CARAVAN_RPC_LAMBDA_INTERFACE": cty.StringVal(c.Name),
		"CARAVAN_RPC_LAMBDA_IMPL":      cty.StringVal(c.Seam.Impl),
	}
	// For Python seams, the interface class often lives in a different
	// module from the impl (the @wagon class can be co-located with the
	// pure function it wraps). The handler reads INTERFACE_MODULE if set;
	// falls back to the impl's module otherwise. We populate it
	// unconditionally for now; future iterations can detect language
	// + only emit when relevant.
	if c.Seam.Impl != "" {
		// Heuristic: same module as Impl unless the seam explicitly
		// declares otherwise. M7 wraps both seams (ValidateExtraction +
		// IntentClassifier) in the same module as the underlying logic,
		// so the impl module path covers it.
		out["CARAVAN_RPC_LAMBDA_INTERFACE_MODULE"] = cty.StringVal("")
	}
	return out
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
