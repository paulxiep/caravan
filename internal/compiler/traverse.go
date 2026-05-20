package compiler

import "gopkg.in/yaml.v3"

// This file isolates yaml.v3's awkward MappingNode / SequenceNode
// stepping behind small declarative helpers. The rest of the compiler
// describes "what" each parser captures via field maps; "how" the
// traversal happens lives here.

// forEachKV iterates the (key, value) pairs of a yaml MappingNode.
// Silently returns for non-mapping nodes — callers should validate
// the node kind explicitly when an error is desired.
func forEachKV(n *yaml.Node, fn func(key, val *yaml.Node)) {
	if n == nil || n.Kind != yaml.MappingNode {
		return
	}
	for i := 0; i+1 < len(n.Content); i += 2 {
		fn(n.Content[i], n.Content[i+1])
	}
}

// forEachItem iterates the items of a yaml SequenceNode.
func forEachItem(n *yaml.Node, fn func(item *yaml.Node)) {
	if n == nil || n.Kind != yaml.SequenceNode {
		return
	}
	for _, item := range n.Content {
		fn(item)
	}
}

// fieldHandler processes the value of a single keyed field.
type fieldHandler = func(v *yaml.Node)

// fieldMap declares a parser's known fields and how to consume each.
// Unknown keys trigger a warning automatically via dispatchFields.
type fieldMap map[string]fieldHandler

// dispatchFields validates that n is a mapping, walks its pairs, and
// dispatches each pair to the appropriate handler in fields. Unknown
// keys produce a warning naming the surrounding context (`what`).
//
// Use this for any struct-like yaml mapping. Builders that need to
// also reject unknown keys (error rather than warn) wrap their handler
// map and override the default; the PoC convention is "warn on
// unknown" — keep code working when the spec adds fields later.
func dispatchFields(file string, n *yaml.Node, diag *Diagnostics, what string, fields fieldMap) {
	if n == nil || n.Kind != yaml.MappingNode {
		diag.Error(nodeSpan(file, n), "%s must be a mapping", what)
		return
	}
	forEachKV(n, func(key, val *yaml.Node) {
		if fn, ok := fields[key.Value]; ok {
			fn(val)
			return
		}
		diag.Warn(nodeSpan(file, key), "unknown %s key %q; ignored", what, key.Value)
	})
}

// mappedItems walks a mapping node and stores each key into a named
// collection via the supplied register function. Duplicate-name
// detection lives in the register callback (so callers can choose
// "warn and skip" vs "error and abort"). Returns nothing — diagnostics
// accumulate via the closure.
func mappedItems(file string, n *yaml.Node, diag *Diagnostics, what string, register func(name string, keyNode, valNode *yaml.Node)) {
	if n == nil || n.Kind != yaml.MappingNode {
		diag.Error(nodeSpan(file, n), "%s must be a mapping", what)
		return
	}
	forEachKV(n, func(key, val *yaml.Node) {
		register(key.Value, key, val)
	})
}
