package compiler

// CompileFile chains phases 1–3 (Lex → Parse → Normalize). Useful
// when callers want a normalized Plan but not target-specific resolution.
//
// On phase-1 failures (filesystem or yaml-syntax) returns (nil, diag, err).
// Schema errors land in diag; callers should check diag.HasErrors().
func CompileFile(file string) (*Plan, *Diagnostics, error) {
	raw, err := LexFile(file)
	if err != nil {
		return nil, &Diagnostics{}, err
	}
	doc, diag, err := Parse(raw)
	if err != nil {
		return nil, diag, err
	}
	if doc == nil {
		return nil, diag, nil
	}
	plan := Normalize(doc, diag)
	return plan, diag, nil
}

// CompileFileForTarget chains phases 1–4 against a specific target.
// Same error semantics as CompileFile.
func CompileFileForTarget(file, target string) (*ResolvedPlan, *Diagnostics, error) {
	plan, diag, err := CompileFile(file)
	if err != nil {
		return nil, diag, err
	}
	if plan == nil || diag.HasErrors() {
		return nil, diag, nil
	}
	rp := Resolve(plan, target, diag)
	return rp, diag, nil
}
