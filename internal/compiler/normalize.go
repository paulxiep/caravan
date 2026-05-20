package compiler

// Normalize runs phase 3 on a ParsedDoc. It validates cross-references
// and fills in defaults. The PoC has a small enough schema that we keep
// normalize/parse on the same struct type — Normalize mutates the doc
// in place and returns the same pointer for convenience.
//
// Structured as a pipeline:
//
//  1. defaulters — fill in computed defaults (e.g. seam.ServiceName).
//  2. validators — assert cross-refs and structural invariants;
//     diagnostics are accumulated, the pipeline always runs to
//     completion so the caller sees every problem at once.
func Normalize(doc *ParsedDoc, diag *Diagnostics) *Plan {
	if doc == nil {
		return nil
	}
	applyDefaults(doc)
	runValidators(doc, diag)
	return doc
}

// --- defaulters --------------------------------------------------------------

// applyDefaults runs each defaulter in order. A defaulter never reports
// diagnostics — it only fills in computed values.
func applyDefaults(doc *ParsedDoc) {
	defaulters := []func(*ParsedDoc){
		defaultSeamServiceNames,
	}
	for _, fn := range defaulters {
		fn(doc)
	}
}

// defaultSeamServiceNames fills Seam.ServiceName from kebab-case(Name)
// for any seam that didn't declare one in yaml. M1's compose-emitter
// reads ServiceName as authoritative — defaulting here keeps the
// emitter free of "fallback" branches.
func defaultSeamServiceNames(doc *ParsedDoc) {
	for _, s := range doc.Seams {
		if s.ServiceName == "" {
			s.ServiceName = kebabCase(s.Name)
		}
	}
}

// --- validators --------------------------------------------------------------

// validator is one cross-ref / invariant check. Each is independent
// and runs unconditionally so the caller sees every problem at once.
type validator func(*ParsedDoc, *Diagnostics)

// runValidators executes every validator. Order doesn't matter for
// correctness (diagnostics are accumulated, not short-circuited), but
// the order here roughly follows what a human would check first.
func runValidators(doc *ParsedDoc, diag *Diagnostics) {
	pipeline := []validator{
		validateEntryUses,
		validateEntryTriggerRefs,
		validateSeamUses,
		validateTargetRefs,
		validateSeamImplWhenDispatched,
		validateNamespaceCollisions,
		validateDefaultTarget,
	}
	for _, fn := range pipeline {
		fn(doc, diag)
	}
}

// validateEntryUses checks that every `uses:` name on an entry resolves
// to a declared resource or secret.
func validateEntryUses(doc *ParsedDoc, diag *Diagnostics) {
	for _, e := range doc.Entries {
		for _, ref := range e.Uses {
			if !nameDeclared(doc, ref) {
				diag.Error(e.Span, "entries.%s uses unknown name %q (not in resources or secrets)", e.Name, ref)
			}
		}
	}
}

// validateEntryTriggerRefs checks queue/stream triggers point at known
// resources.
func validateEntryTriggerRefs(doc *ParsedDoc, diag *Diagnostics) {
	for _, e := range doc.Entries {
		for _, t := range e.Triggers {
			switch t.Kind {
			case TriggerQueue:
				if t.Queue != nil && t.Queue.From != "" {
					if _, ok := doc.Resources[t.Queue.From]; !ok {
						diag.Error(t.Span, "entries.%s queue trigger references unknown resource %q", e.Name, t.Queue.From)
					}
				}
			case TriggerStream:
				if t.Stream != nil && t.Stream.From != "" {
					if _, ok := doc.Resources[t.Stream.From]; !ok {
						diag.Error(t.Span, "entries.%s stream trigger references unknown resource %q", e.Name, t.Stream.From)
					}
				}
			}
		}
	}
}

// validateSeamUses mirrors validateEntryUses for seams.
func validateSeamUses(doc *ParsedDoc, diag *Diagnostics) {
	for _, s := range doc.Seams {
		for _, ref := range s.Uses {
			if !nameDeclared(doc, ref) {
				diag.Error(s.Span, "seams.%s uses unknown name %q (not in resources or secrets)", s.Name, ref)
			}
		}
	}
}

// validateTargetRefs checks every name keyed under target.entries /
// .seams / .composition refers to an actual declaration.
func validateTargetRefs(doc *ParsedDoc, diag *Diagnostics) {
	for _, t := range doc.Targets {
		for name := range t.Entries {
			if _, ok := doc.Entries[name]; !ok {
				diag.Error(t.Span, "targets.%s.entries references unknown entry %q", t.Name, name)
			}
		}
		for name := range t.Seams {
			if _, ok := doc.Seams[name]; !ok {
				diag.Error(t.Span, "targets.%s.seams references unknown seam %q", t.Name, name)
			}
		}
		for name := range t.Composition {
			if _, ok := doc.Resources[name]; !ok {
				diag.Error(t.Span, "targets.%s.composition references unknown resource %q", t.Name, name)
			}
		}
	}
}

// validateSeamImplWhenDispatched requires `impl:` on any seam that
// dispatches as container or lambda in any target — M1's emitter needs
// it to write the peer service's command.
func validateSeamImplWhenDispatched(doc *ParsedDoc, diag *Diagnostics) {
	for _, t := range doc.Targets {
		for seamName, mode := range t.Seams {
			if mode != SeamContainer && mode != SeamLambda {
				continue
			}
			s := doc.Seams[seamName]
			if s == nil {
				continue // already flagged by validateTargetRefs
			}
			if s.Impl == "" {
				diag.Error(s.Span, "seams.%s dispatches as %s in target %s but has no `impl:` field", s.Name, mode, t.Name)
			}
		}
	}
}

// validateNamespaceCollisions catches entries, seams, and resources
// sharing a name — they all map onto compose service names so a
// collision would silently shadow.
func validateNamespaceCollisions(doc *ParsedDoc, diag *Diagnostics) {
	pairs := []struct {
		groupA, groupB string
		namesA, namesB map[string]struct{}
	}{
		{"entry", "seam", keysOf(doc.Entries), keysOf(doc.Seams)},
		{"entry", "resource", keysOf(doc.Entries), keysOf(doc.Resources)},
		{"seam", "resource", keysOf(doc.Seams), keysOf(doc.Resources)},
	}
	for _, p := range pairs {
		for n := range p.namesA {
			if _, dup := p.namesB[n]; dup {
				diag.Error(Span{}, "name %q is declared as both %s and %s — cross-namespace collision", n, p.groupA, p.groupB)
			}
		}
	}
}

// validateDefaultTarget pins default_target to a real target name.
func validateDefaultTarget(doc *ParsedDoc, diag *Diagnostics) {
	if doc.DefaultTarget == "" {
		return
	}
	if _, ok := doc.Targets[doc.DefaultTarget]; !ok {
		diag.Error(Span{}, "default_target %q is not a declared target", doc.DefaultTarget)
	}
}

// --- helpers -----------------------------------------------------------------

// nameDeclared reports whether name is declared as a resource or secret.
func nameDeclared(doc *ParsedDoc, name string) bool {
	if _, ok := doc.Resources[name]; ok {
		return true
	}
	_, ok := doc.Secrets[name]
	return ok
}

// keysOf returns the keys of a map as a set.
func keysOf[V any](m map[string]V) map[string]struct{} {
	out := make(map[string]struct{}, len(m))
	for k := range m {
		out[k] = struct{}{}
	}
	return out
}

// kebabCase converts a CamelCase / PascalCase / snake_case identifier
// to kebab-case. Examples:
//
//	LLMExtraction    → llm-extraction
//	HTTPRequest      → http-request
//	parse_invoice    → parse-invoice
//	ParseInvoiceData → parse-invoice-data
//
// Rule: insert a hyphen before any uppercase letter preceded by a
// lowercase letter, OR before the last uppercase of an acronym run
// that's followed by a lowercase. Underscores become hyphens. Result
// is lowercase.
func kebabCase(s string) string {
	if s == "" {
		return ""
	}
	runes := []rune(s)
	out := make([]byte, 0, len(runes)+4)
	for i, r := range runes {
		switch {
		case r == '_':
			out = append(out, '-')
		case r >= 'A' && r <= 'Z':
			if needsHyphenBeforeUpper(runes, i) && len(out) > 0 && out[len(out)-1] != '-' {
				out = append(out, '-')
			}
			out = append(out, byte(r-'A'+'a'))
		default:
			out = append(out, byte(r))
		}
	}
	return string(out)
}

func needsHyphenBeforeUpper(runes []rune, i int) bool {
	if i == 0 {
		return false
	}
	prev := runes[i-1]
	prevLower := prev >= 'a' && prev <= 'z'
	prevDigit := prev >= '0' && prev <= '9'
	if prevLower || prevDigit {
		return true
	}
	// Acronym run boundary: H-T-T-P-R-e-q-u-e-s-t → http-request.
	// Insert hyphen when the prior char is uppercase and the next char
	// is lowercase — that marks the start of a new word.
	if !(prev >= 'A' && prev <= 'Z') {
		return false
	}
	if i+1 >= len(runes) {
		return false
	}
	next := runes[i+1]
	return next >= 'a' && next <= 'z'
}
