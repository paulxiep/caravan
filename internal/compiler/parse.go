package compiler

import (
	"fmt"

	"gopkg.in/yaml.v3"
)

// ParsedDoc is phase 2's output. Shares the Plan struct type — the
// validation cost of two parallel shapes outweighs the type-safety win
// at this stage. Cross-ref resolution and default fill-in happen in
// Normalize, not here.
type ParsedDoc = Plan

// Parse converts a RawYAML into a typed ParsedDoc plus diagnostics.
// Schema errors land in diag; the returned error is reserved for
// catastrophic shapes (root not a mapping). Callers must check
// diag.HasErrors() before progressing.
func Parse(raw *RawYAML) (*ParsedDoc, *Diagnostics, error) {
	diag := &Diagnostics{}
	if raw == nil || raw.Root == nil {
		return nil, diag, fmt.Errorf("nil yaml input")
	}
	root := docRoot(raw.Root)
	if root.Kind != yaml.MappingNode {
		diag.Error(nodeSpan(raw.File, root), "top-level YAML must be a mapping; got kind %d", root.Kind)
		return nil, diag, nil
	}

	doc := newDoc()
	dispatchFields(raw.File, root, diag, "top-level", fieldMap{
		"name":           func(v *yaml.Node) { doc.Name = stringScalar(raw.File, v, diag, "name") },
		"default_target": func(v *yaml.Node) { doc.DefaultTarget = stringScalar(raw.File, v, diag, "default_target") },
		"output_dir":     func(v *yaml.Node) { doc.OutputDir = stringScalar(raw.File, v, diag, "output_dir") },
		"entries":        func(v *yaml.Node) { parseEntries(raw.File, v, doc, diag) },
		"seams":          func(v *yaml.Node) { parseSeams(raw.File, v, doc, diag) },
		"resources":      func(v *yaml.Node) { parseResources(raw.File, v, doc, diag) },
		"secrets":        func(v *yaml.Node) { parseSecrets(raw.File, v, doc, diag) },
		"targets":        func(v *yaml.Node) { parseTargets(raw.File, v, doc, diag) },
	})

	if doc.Name == "" {
		diag.Error(nodeSpan(raw.File, root), "top-level `name:` is required")
	}
	return doc, diag, nil
}

func newDoc() *ParsedDoc {
	return &ParsedDoc{
		Entries:   map[string]*Entry{},
		Seams:     map[string]*Seam{},
		Resources: map[string]*Resource{},
		Secrets:   map[string]*Secret{},
		Targets:   map[string]*Target{},
	}
}

// --- scalar / list helpers ---------------------------------------------------

func stringScalar(file string, n *yaml.Node, diag *Diagnostics, what string) string {
	if n == nil || n.Kind != yaml.ScalarNode {
		diag.Error(nodeSpan(file, n), "%s must be a scalar string", what)
		return ""
	}
	return n.Value
}

func intScalar(file string, n *yaml.Node, diag *Diagnostics, what string) int {
	if n == nil || n.Kind != yaml.ScalarNode {
		diag.Error(nodeSpan(file, n), "%s must be a scalar integer", what)
		return 0
	}
	var i int
	if err := n.Decode(&i); err != nil {
		diag.Error(nodeSpan(file, n), "%s must be an integer: %v", what, err)
		return 0
	}
	return i
}

func boolScalar(file string, n *yaml.Node, diag *Diagnostics, what string) bool {
	if n == nil || n.Kind != yaml.ScalarNode {
		diag.Error(nodeSpan(file, n), "%s must be a scalar boolean", what)
		return false
	}
	var b bool
	if err := n.Decode(&b); err != nil {
		diag.Error(nodeSpan(file, n), "%s must be a boolean: %v", what, err)
		return false
	}
	return b
}

func stringList(file string, n *yaml.Node, diag *Diagnostics, what string) []string {
	if n == nil || n.Kind != yaml.SequenceNode {
		diag.Error(nodeSpan(file, n), "%s must be a sequence", what)
		return nil
	}
	out := make([]string, 0, len(n.Content))
	forEachItem(n, func(item *yaml.Node) {
		if item.Kind != yaml.ScalarNode {
			diag.Error(nodeSpan(file, item), "%s items must be scalars", what)
			return
		}
		out = append(out, item.Value)
	})
	return out
}

// --- entries ----------------------------------------------------------------

func parseEntries(file string, n *yaml.Node, doc *ParsedDoc, diag *Diagnostics) {
	mappedItems(file, n, diag, "entries", func(name string, k, v *yaml.Node) {
		if _, dup := doc.Entries[name]; dup {
			diag.Error(nodeSpan(file, k), "duplicate entry %q", name)
			return
		}
		doc.Entries[name] = parseEntry(file, name, k, v, diag)
	})
}

func parseEntry(file, name string, k, v *yaml.Node, diag *Diagnostics) *Entry {
	e := &Entry{Name: name, Span: nodeSpan(file, k)}
	what := "entries." + name
	dispatchFields(file, v, diag, what, fieldMap{
		"path":           func(v *yaml.Node) { e.Path = stringScalar(file, v, diag, what+".path") },
		"dockerfile":     func(v *yaml.Node) { e.Dockerfile = stringScalar(file, v, diag, what+".dockerfile") },
		"runtime_target": func(v *yaml.Node) { e.RuntimeTarget = stringScalar(file, v, diag, what+".runtime_target") },
		"env_file":       func(v *yaml.Node) { e.EnvFile = stringScalar(file, v, diag, what+".env_file") },
		"triggers":       func(v *yaml.Node) { e.Triggers = parseTriggers(file, v, diag, what+".triggers") },
		"uses":           func(v *yaml.Node) { e.Uses = stringList(file, v, diag, what+".uses") },
	})
	return e
}

// --- triggers (tagged-union dispatch by kind) -------------------------------

func parseTriggers(file string, n *yaml.Node, diag *Diagnostics, what string) []Trigger {
	if n.Kind != yaml.SequenceNode {
		diag.Error(nodeSpan(file, n), "%s must be a sequence", what)
		return nil
	}
	out := make([]Trigger, 0, len(n.Content))
	forEachItem(n, func(item *yaml.Node) {
		if t, ok := parseTrigger(file, item, diag, what); ok {
			out = append(out, t)
		}
	})
	return out
}

// triggerParsers maps a TriggerKind to the function that parses its
// inner mapping. Adding a new trigger kind is one entry here + one
// const in kinds.go.
var triggerParsers = map[TriggerKind]func(file string, n *yaml.Node, diag *Diagnostics, t *Trigger){
	TriggerHTTP: func(file string, n *yaml.Node, diag *Diagnostics, t *Trigger) {
		t.HTTP = parseHTTPTrigger(file, n, diag)
	},
	TriggerQueue: func(file string, n *yaml.Node, diag *Diagnostics, t *Trigger) {
		t.Queue = parseQueueTrigger(file, n, diag)
	},
	TriggerCron: func(file string, n *yaml.Node, diag *Diagnostics, t *Trigger) {
		t.Cron = parseCronTrigger(file, n, diag)
	},
	TriggerStream: func(file string, n *yaml.Node, diag *Diagnostics, t *Trigger) {
		t.Stream = parseStreamTrigger(file, n, diag)
	},
}

func parseTrigger(file string, item *yaml.Node, diag *Diagnostics, what string) (Trigger, bool) {
	if item.Kind != yaml.MappingNode || len(item.Content) < 2 {
		diag.Error(nodeSpan(file, item), "%s item must be a single-key mapping", what)
		return Trigger{}, false
	}
	key := item.Content[0]
	val := item.Content[1]
	kind := TriggerKind(key.Value)
	parser, ok := triggerParsers[kind]
	if !ok {
		diag.Error(nodeSpan(file, key), "unknown trigger kind %q", key.Value)
		return Trigger{}, false
	}
	t := Trigger{Kind: kind, Span: nodeSpan(file, key)}
	parser(file, val, diag, &t)
	return t, true
}

func parseHTTPTrigger(file string, n *yaml.Node, diag *Diagnostics) *HTTPTrigger {
	out := &HTTPTrigger{}
	dispatchFields(file, n, diag, "http trigger", fieldMap{
		"path":   func(v *yaml.Node) { out.Path = stringScalar(file, v, diag, "http.path") },
		"port":   func(v *yaml.Node) { out.Port = intScalar(file, v, diag, "http.port") },
		"public": func(v *yaml.Node) { out.Public = boolScalar(file, v, diag, "http.public") },
	})
	return out
}

func parseQueueTrigger(file string, n *yaml.Node, diag *Diagnostics) *QueueTrigger {
	out := &QueueTrigger{}
	dispatchFields(file, n, diag, "queue trigger", fieldMap{
		"from": func(v *yaml.Node) { out.From = stringScalar(file, v, diag, "queue.from") },
	})
	if out.From == "" {
		diag.Error(nodeSpan(file, n), "queue trigger requires `from:` resource name")
	}
	return out
}

func parseCronTrigger(file string, n *yaml.Node, diag *Diagnostics) *CronTrigger {
	out := &CronTrigger{}
	dispatchFields(file, n, diag, "cron trigger", fieldMap{
		"schedule": func(v *yaml.Node) { out.Schedule = stringScalar(file, v, diag, "cron.schedule") },
		"timezone": func(v *yaml.Node) { out.Timezone = stringScalar(file, v, diag, "cron.timezone") },
	})
	return out
}

func parseStreamTrigger(file string, n *yaml.Node, diag *Diagnostics) *StreamTrigger {
	out := &StreamTrigger{}
	dispatchFields(file, n, diag, "stream trigger", fieldMap{
		"from": func(v *yaml.Node) { out.From = stringScalar(file, v, diag, "stream.from") },
	})
	return out
}

// --- seams -------------------------------------------------------------------

func parseSeams(file string, n *yaml.Node, doc *ParsedDoc, diag *Diagnostics) {
	mappedItems(file, n, diag, "seams", func(name string, k, v *yaml.Node) {
		if _, dup := doc.Seams[name]; dup {
			diag.Error(nodeSpan(file, k), "duplicate seam %q", name)
			return
		}
		doc.Seams[name] = parseSeam(file, name, k, v, diag)
	})
}

func parseSeam(file, name string, k, v *yaml.Node, diag *Diagnostics) *Seam {
	s := &Seam{Name: name, Span: nodeSpan(file, k)}
	what := "seams." + name
	dispatchFields(file, v, diag, what, fieldMap{
		"path":         func(v *yaml.Node) { s.Path = stringScalar(file, v, diag, what+".path") },
		"dockerfile":   func(v *yaml.Node) { s.Dockerfile = stringScalar(file, v, diag, what+".dockerfile") },
		"uses":         func(v *yaml.Node) { s.Uses = stringList(file, v, diag, what+".uses") },
		"impl":         func(v *yaml.Node) { s.Impl = stringScalar(file, v, diag, what+".impl") },
		"service_name": func(v *yaml.Node) { s.ServiceName = stringScalar(file, v, diag, what+".service_name") },
		"env_file":     func(v *yaml.Node) { s.EnvFile = stringScalar(file, v, diag, what+".env_file") },
		"image_target": func(v *yaml.Node) { s.ImageTarget = stringScalar(file, v, diag, what+".image_target") },
	})
	return s
}

// --- resources ---------------------------------------------------------------

func parseResources(file string, n *yaml.Node, doc *ParsedDoc, diag *Diagnostics) {
	mappedItems(file, n, diag, "resources", func(name string, k, v *yaml.Node) {
		if _, dup := doc.Resources[name]; dup {
			diag.Error(nodeSpan(file, k), "duplicate resource %q", name)
			return
		}
		doc.Resources[name] = parseResource(file, name, k, v, diag)
	})
}

func parseResource(file, name string, k, v *yaml.Node, diag *Diagnostics) *Resource {
	r := &Resource{Name: name, Span: nodeSpan(file, k), Extra: map[string]any{}}
	what := "resources." + name
	if v == nil || v.Kind != yaml.MappingNode {
		diag.Error(nodeSpan(file, v), "%s must be a mapping", what)
		return r
	}
	// Resources have a known core (type, composition) plus type-specific
	// fields that flow into Extra. We use forEachKV (not dispatchFields)
	// because the unknown-key behavior is "capture", not "warn".
	forEachKV(v, func(fk, fv *yaml.Node) {
		switch fk.Value {
		case "type":
			val := stringScalar(file, fv, diag, what+".type")
			kind := ResourceKind(val)
			if !kind.IsValid() {
				diag.Error(nodeSpan(file, fv), "unknown resource type %q (%s)", val, what)
				return
			}
			r.Type = kind
		case "composition":
			val := stringScalar(file, fv, diag, what+".composition")
			mode := CompositionMode(val)
			if !mode.IsValid() {
				diag.Error(nodeSpan(file, fv), "unknown composition %q (%s)", val, what)
				return
			}
			r.Composition = mode
		case "kind":
			r.Variant = ResourceVariant(stringScalar(file, fv, diag, what+".kind"))
		case "user":
			r.User = stringScalar(file, fv, diag, what+".user")
		case "password":
			r.Password = stringScalar(file, fv, diag, what+".password")
		case "dbname":
			r.DBName = stringScalar(file, fv, diag, what+".dbname")
		default:
			var anyVal any
			if err := fv.Decode(&anyVal); err == nil {
				r.Extra[fk.Value] = anyVal
			}
		}
	})
	if r.Type == "" {
		diag.Error(nodeSpan(file, v), "%s requires `type:`", what)
	}
	return r
}

// --- secrets -----------------------------------------------------------------

func parseSecrets(file string, n *yaml.Node, doc *ParsedDoc, diag *Diagnostics) {
	mappedItems(file, n, diag, "secrets", func(name string, k, v *yaml.Node) {
		if _, dup := doc.Secrets[name]; dup {
			diag.Error(nodeSpan(file, k), "duplicate secret %q", name)
			return
		}
		doc.Secrets[name] = parseSecret(file, name, k, v, diag)
	})
}

func parseSecret(file, name string, k, v *yaml.Node, diag *Diagnostics) *Secret {
	s := &Secret{Name: name, Span: nodeSpan(file, k)}
	what := "secrets." + name
	dispatchFields(file, v, diag, what, fieldMap{
		"from": func(v *yaml.Node) { s.From = stringScalar(file, v, diag, what+".from") },
		"path": func(v *yaml.Node) { s.Path = stringScalar(file, v, diag, what+".path") },
	})
	if s.From == "" {
		diag.Error(nodeSpan(file, v), "%s requires `from:`", what)
	}
	return s
}

// --- targets -----------------------------------------------------------------

func parseTargets(file string, n *yaml.Node, doc *ParsedDoc, diag *Diagnostics) {
	mappedItems(file, n, diag, "targets", func(name string, k, v *yaml.Node) {
		if _, dup := doc.Targets[name]; dup {
			diag.Error(nodeSpan(file, k), "duplicate target %q", name)
			return
		}
		doc.Targets[name] = parseTarget(file, name, k, v, diag)
	})
}

func parseTarget(file, name string, k, v *yaml.Node, diag *Diagnostics) *Target {
	t := &Target{Name: name, Span: nodeSpan(file, k)}
	what := "targets." + name
	dispatchFields(file, v, diag, what, fieldMap{
		"runtime": func(v *yaml.Node) {
			val := stringScalar(file, v, diag, what+".runtime")
			rk := RuntimeKind(val)
			if !rk.IsValid() {
				diag.Error(nodeSpan(file, v), "unknown runtime %q (%s)", val, what)
				return
			}
			t.Runtime = rk
		},
		"default_composition": func(v *yaml.Node) {
			val := stringScalar(file, v, diag, what+".default_composition")
			cm := CompositionMode(val)
			if !cm.IsValid() {
				diag.Error(nodeSpan(file, v), "unknown composition %q (%s)", val, what)
				return
			}
			t.DefaultComposition = cm
		},
		"region":      func(v *yaml.Node) { t.Region = stringScalar(file, v, diag, what+".region") },
		"entries":     func(v *yaml.Node) { t.Entries = parseEntryDispatchMap(file, v, diag, what+".entries") },
		"seams":       func(v *yaml.Node) { t.Seams = parseSeamDispatchMap(file, v, diag, what+".seams") },
		"composition": func(v *yaml.Node) { t.Composition = parseCompositionMap(file, v, diag, what+".composition") },
		"creds_passthrough": func(v *yaml.Node) {
			t.CredsPassthrough = boolScalar(file, v, diag, what+".creds_passthrough")
		},
		"aws_profile": func(v *yaml.Node) { t.AwsProfile = stringScalar(file, v, diag, what+".aws_profile") },
		"backend":     func(v *yaml.Node) { t.Backend = parseBackendConfig(file, v, diag, what+".backend") },
	})
	if t.Runtime == "" {
		diag.Error(nodeSpan(file, v), "%s requires `runtime:`", what)
	}
	return t
}

// parseEnumMap is a generic mapping-of-name-to-enum-string parser used
// by the three target sub-maps below. T is the enum type; isValid is
// its membership check.
func parseEnumMap[T ~string](
	file string, n *yaml.Node, diag *Diagnostics, what string,
	isValid func(T) bool,
) map[string]T {
	if n == nil || n.Kind != yaml.MappingNode {
		diag.Error(nodeSpan(file, n), "%s must be a mapping", what)
		return nil
	}
	out := map[string]T{}
	forEachKV(n, func(k, v *yaml.Node) {
		val := stringScalar(file, v, diag, what+"."+k.Value)
		typed := T(val)
		if !isValid(typed) {
			diag.Error(nodeSpan(file, v), "invalid value %q (%s.%s)", val, what, k.Value)
			return
		}
		out[k.Value] = typed
	})
	return out
}

func parseEntryDispatchMap(file string, n *yaml.Node, diag *Diagnostics, what string) map[string]EntryDispatchMode {
	return parseEnumMap(file, n, diag, what, EntryDispatchMode.IsValid)
}

func parseSeamDispatchMap(file string, n *yaml.Node, diag *Diagnostics, what string) map[string]SeamDispatchMode {
	return parseEnumMap(file, n, diag, what, SeamDispatchMode.IsValid)
}

// parseCompositionMap parses `targets.<X>.composition:`. Each entry
// accepts two yaml shapes for back-compat + M4 extensibility:
//
//	composition:
//	  invoice_queue: oss-local                              # scalar (M0)
//	  invoice_queue: { mode: oss-local, kind: rabbitmq }    # object (M4)
//
// Scalar form sets Mode only; object form may set Mode and/or Variant.
// Either field may be empty; resolve.go layers the override on top of
// the resource's own declaration.
func parseCompositionMap(file string, n *yaml.Node, diag *Diagnostics, what string) map[string]*CompositionOverride {
	if n == nil || n.Kind != yaml.MappingNode {
		diag.Error(nodeSpan(file, n), "%s must be a mapping", what)
		return nil
	}
	out := map[string]*CompositionOverride{}
	forEachKV(n, func(k, v *yaml.Node) {
		entryWhat := what + "." + k.Value
		out[k.Value] = parseCompositionOverride(file, v, diag, entryWhat)
	})
	return out
}

// parseBackendConfig parses `targets.<X>.backend:` into a BackendConfig.
// Required when the target sets `creds_passthrough: true`; the validator
// in normalize.go enforces this.
func parseBackendConfig(file string, n *yaml.Node, diag *Diagnostics, what string) *BackendConfig {
	if n == nil || n.Kind != yaml.MappingNode {
		diag.Error(nodeSpan(file, n), "%s must be a mapping", what)
		return nil
	}
	b := &BackendConfig{Span: nodeSpan(file, n)}
	dispatchFields(file, n, diag, what, fieldMap{
		"bucket":     func(v *yaml.Node) { b.Bucket = stringScalar(file, v, diag, what+".bucket") },
		"lock_table": func(v *yaml.Node) { b.LockTable = stringScalar(file, v, diag, what+".lock_table") },
		"region":     func(v *yaml.Node) { b.Region = stringScalar(file, v, diag, what+".region") },
		"key":        func(v *yaml.Node) { b.Key = stringScalar(file, v, diag, what+".key") },
	})
	return b
}

// parseCompositionOverride accepts either a scalar string (treated as
// the `mode:` value) or an object {mode, kind}.
func parseCompositionOverride(file string, n *yaml.Node, diag *Diagnostics, what string) *CompositionOverride {
	if n == nil {
		diag.Error(nodeSpan(file, n), "%s must be a scalar or mapping", what)
		return &CompositionOverride{}
	}
	o := &CompositionOverride{Span: nodeSpan(file, n)}
	switch n.Kind {
	case yaml.ScalarNode:
		val := n.Value
		mode := CompositionMode(val)
		if !mode.IsValid() {
			diag.Error(nodeSpan(file, n), "invalid composition mode %q (%s)", val, what)
			return o
		}
		o.Mode = mode
	case yaml.MappingNode:
		dispatchFields(file, n, diag, what, fieldMap{
			"mode": func(v *yaml.Node) {
				val := stringScalar(file, v, diag, what+".mode")
				mode := CompositionMode(val)
				if !mode.IsValid() {
					diag.Error(nodeSpan(file, v), "invalid composition mode %q (%s.mode)", val, what)
					return
				}
				o.Mode = mode
			},
			"kind": func(v *yaml.Node) {
				o.Variant = ResourceVariant(stringScalar(file, v, diag, what+".kind"))
			},
		})
	default:
		diag.Error(nodeSpan(file, n), "%s must be a scalar or mapping", what)
	}
	return o
}
