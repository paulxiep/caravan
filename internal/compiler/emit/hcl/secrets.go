package hcl

import (
	"github.com/hashicorp/hcl/v2/hclwrite"
	"github.com/zclconf/go-cty/cty"
)

// emitVarBindings writes one tofu `variable {}` block per unique
// VarName across all entries' bindings. Sensitive bindings (declared
// secrets) get `sensitive = true`; env_file / environment passthroughs
// stay non-sensitive. `caravan up` reads the sidecar wiring manifest
// (caravan.tfwiring.json) to resolve TF_VAR_<VarName> values from the
// host process env at apply time — no value text lives in the on-disk
// HCL itself.
//
// Replaces the M9-pre emitSecretVariables, which only handled declared
// secrets and grepped main.tf textually for the var → env mapping.
func emitVarBindings(body *hclwrite.Body, perEntryBindings map[string][]EnvBinding) {
	vars := dedupedVarBindings(perEntryBindings)
	if len(vars) == 0 {
		return
	}
	for _, b := range vars {
		blk := body.AppendNewBlock("variable", []string{b.VarName})
		bb := blk.Body()
		// `type` is a tofu identifier (bare `string`), not a string literal.
		bb.SetAttributeRaw("type", rawHCL("string"))
		bb.SetAttributeValue("sensitive", cty.BoolVal(b.Sensitive))
		// Description carries `env:<HOST_VAR_NAME>` so legacy readers (and
		// human reviewers of main.tf) can trace the binding without the
		// sidecar manifest. The wiring manifest (caravan.tfwiring.json) is
		// the structured source of truth `caravan up` consumes.
		bb.SetAttributeValue("description", cty.StringVal("env:"+b.EnvName))
		body.AppendNewline()
	}
}

// secretVarName is the tofu-variable name for a Caravan-IR secret. Used
// by both bindings.go (computing declared-secret bindings) and the
// variable-block emit so the two stay in sync. Mirrors
// terraformLocalName's normalization (digits can't lead an HCL identifier).
func secretVarName(secretName string) string {
	return terraformLocalName(secretName)
}
