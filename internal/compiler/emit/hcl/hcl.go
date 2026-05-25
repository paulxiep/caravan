package hcl

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/hashicorp/hcl/v2/hclwrite"
	"github.com/zclconf/go-cty/cty"

	"github.com/paulxiep/caravan/internal/compiler"
)

// EmitHCL writes the M4-cloud HCL artifacts for one resolved hybrid
// target into outDir. Returns the list of written file paths in
// deterministic order.
//
// Preconditions: target.CredsPassthrough is true and target.Backend is
// populated (validateHybridTarget enforces this upstream). HCL emit
// itself does not re-validate; callers must.
func EmitHCL(rp *compiler.ResolvedPlan, outDir string) ([]string, error) {
	if rp == nil || rp.Plan == nil {
		return nil, fmt.Errorf("nil ResolvedPlan")
	}
	target := rp.Plan.Targets[rp.TargetName]
	if target == nil {
		return nil, fmt.Errorf("target %q not in plan", rp.TargetName)
	}
	if !target.CredsPassthrough || target.Backend == nil {
		return nil, fmt.Errorf("EmitHCL: target %q is not in hybrid-dev mode", target.Name)
	}

	if err := os.MkdirAll(outDir, 0o755); err != nil {
		return nil, fmt.Errorf("EmitHCL mkdir: %w", err)
	}

	files := []struct {
		name string
		body []byte
	}{
		{name: "versions.tf", body: renderVersions()},
		{name: "backend.tf", body: renderBackend(target.Backend)},
		{name: "main.tf", body: renderMain(rp, target)},
	}
	if len(rp.IAMGrants) > 0 {
		files = append(files, struct {
			name string
			body []byte
		}{name: "iam.tf", body: renderIAM(rp, target)})
	}

	written := make([]string, 0, len(files))
	for _, f := range files {
		path := filepath.Join(outDir, f.name)
		if err := os.WriteFile(path, f.body, 0o644); err != nil {
			return nil, fmt.Errorf("EmitHCL write %s: %w", f.name, err)
		}
		written = append(written, path)
	}
	return written, nil
}

// renderVersions emits versions.tf — the OpenTofu version floor and
// AWS provider pin. Convention is to keep this file dead simple so
// upgrades are a one-line diff.
func renderVersions() []byte {
	f := hclwrite.NewEmptyFile()
	body := f.Body()
	body.AppendUnstructuredTokens(headerTokens("versions.tf", ""))

	tf := body.AppendNewBlock("terraform", nil)
	tfBody := tf.Body()
	tfBody.SetAttributeValue("required_version", cty.StringVal(">= 1.6.0"))

	providers := tfBody.AppendNewBlock("required_providers", nil)
	providers.Body().SetAttributeValue("aws", cty.ObjectVal(map[string]cty.Value{
		"source":  cty.StringVal("hashicorp/aws"),
		"version": cty.StringVal("~> 5.0"),
	}))

	return f.Bytes()
}

// renderBackend emits backend.tf — the S3 + DynamoDB state backend
// pointing at the M4-cloud-prereq-created bucket + lock table. The
// `terraform { backend "s3" { ... } }` block must be in its own file
// (or near the top of versions.tf) because tofu init reads it before
// the rest of the config.
func renderBackend(b *compiler.BackendConfig) []byte {
	f := hclwrite.NewEmptyFile()
	body := f.Body()
	body.AppendUnstructuredTokens(headerTokens("backend.tf", ""))

	tf := body.AppendNewBlock("terraform", nil)
	backend := tf.Body().AppendNewBlock("backend", []string{"s3"})
	bb := backend.Body()
	bb.SetAttributeValue("bucket", cty.StringVal(b.Bucket))
	bb.SetAttributeValue("key", cty.StringVal(b.Key))
	bb.SetAttributeValue("region", cty.StringVal(b.Region))
	bb.SetAttributeValue("dynamodb_table", cty.StringVal(b.LockTable))
	bb.SetAttributeValue("encrypt", cty.BoolVal(true))

	return f.Bytes()
}

// renderMain emits main.tf — the AWS provider block + per-resource
// blocks + outputs. Order:
//
//	provider
//	[data "http" "myip" — only when any VPC-only resource exists]
//	[aws_security_group — same condition]
//	per-resource (sorted alphabetically by resource name)
//	outputs (sorted alphabetically by output name)
func renderMain(rp *compiler.ResolvedPlan, target *compiler.Target) []byte {
	f := hclwrite.NewEmptyFile()
	body := f.Body()
	body.AppendUnstructuredTokens(headerTokens("main.tf", target.Name))

	// Provider block — pin region from target. AWS credential resolution
	// at apply time uses the developer's `~/.aws/credentials` (the same
	// file the compose containers see via creds_passthrough).
	provider := body.AppendNewBlock("provider", []string{"aws"})
	provider.Body().SetAttributeValue("region", cty.StringVal(target.Region))
	body.AppendNewline()

	// Walk cloud-managed resources in sorted order. Resource-kind
	// dispatch picks the right emitter from the catalog.
	resources := cloudManagedResources(rp)
	needsSG := false
	for _, name := range resources {
		rr := rp.ResolvedResources[name]
		if rr.Type == compiler.ResourceDBSQL || rr.Type == compiler.ResourceCache {
			needsSG = true
			break
		}
	}

	if needsSG {
		emitMyIPLookup(body)
		emitSecurityGroup(body, rp.Plan.Name, target.Name)
	}

	outputs := map[string]string{} // env-var name → HCL reference expression
	gateOpenSearch := target.AwsProfile // placeholder; gating logic below

	for _, name := range resources {
		rr := rp.ResolvedResources[name]
		if rr.Type == compiler.ResourceSearch && !isResourceUsed(rp, name) {
			// Cost-guard per dev_plan §759: OpenSearch domains are 20+
			// min to provision and ~$25/mo idle. Skip when no entry's
			// uses: actually references it.
			continue
		}
		_ = gateOpenSearch
		switch rr.Type {
		case compiler.ResourceBucket:
			emitBucket(body, rp.Plan.Name, target.Name, name, rr, outputs)
		case compiler.ResourceDBSQL:
			emitDBSQL(body, rp.Plan.Name, target.Name, name, rr, outputs)
		case compiler.ResourceQueue:
			emitQueue(body, rp.Plan.Name, target.Name, name, rr, outputs)
		case compiler.ResourceCache:
			emitCache(body, rp.Plan.Name, target.Name, name, rr, outputs)
		case compiler.ResourceSearch:
			emitSearch(body, rp.Plan.Name, target.Name, name, rr, outputs)
		}
		body.AppendNewline()
	}

	// Output blocks — drive the `tofu output -json | jq -r` flow
	// documented in the generated README. Sorted by env-var name.
	emitOutputs(body, outputs)

	return f.Bytes()
}

// cloudManagedResources returns the sorted names of resources whose
// resolved composition is cloud-managed. Empty when no resources are.
func cloudManagedResources(rp *compiler.ResolvedPlan) []string {
	names := make([]string, 0, len(rp.ResolvedResources))
	for name, rr := range rp.ResolvedResources {
		if rr != nil && rr.Composition == compiler.CompositionCloudManaged {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	return names
}

// isResourceUsed reports whether at least one entry's uses: lists the
// resource. The cost-guard branch for OpenSearch.
func isResourceUsed(rp *compiler.ResolvedPlan, name string) bool {
	for _, e := range rp.Plan.Entries {
		for _, ref := range e.Uses {
			if ref == name {
				return true
			}
		}
	}
	return false
}

// headerTokens builds the comment block at the top of every emitted
// .tf file. The first line marks the file as generated; the optional
// second line names the target.
func headerTokens(filename, target string) hclwrite.Tokens {
	var b strings.Builder
	b.WriteString(fmt.Sprintf("# Generated by `caravan compile`. Do not edit by hand — %s.\n", filename))
	if target != "" {
		b.WriteString(fmt.Sprintf("# Target: %s. Re-emit after editing caravan.yaml.\n", target))
	}
	b.WriteString("\n")
	return hclwrite.Tokens{{
		Type:  0,
		Bytes: []byte(b.String()),
	}}
}

// renderToBytes is a small helper retained for testability — the
// hclwrite.Format pass tightens any irregular spacing from
// AppendUnstructuredTokens.
func renderToBytes(f *hclwrite.File) []byte {
	var buf bytes.Buffer
	if _, err := f.WriteTo(&buf); err != nil {
		// hclwrite.File.WriteTo writes to a bytes.Buffer; err is
		// impossible in practice.
		return nil
	}
	return hclwrite.Format(buf.Bytes())
}
