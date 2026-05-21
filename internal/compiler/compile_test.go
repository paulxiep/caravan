package compiler

import (
	"bytes"
	"encoding/json"
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// -update regenerates the golden .spec.json fixture from the current
// resolver output. Run as: `go test ./internal/compiler -update -run TestSpecJSON`.
var update = flag.Bool("update", false, "rewrite golden fixtures")

// TestSpecJSON is the M0 acceptance test: resolve invoice-parse's
// caravan.yaml against the dev-bootstrap target and confirm the JSON
// shape matches what B0 hand-wrote in docker-compose.caravan-bootstrap.yaml.
func TestSpecJSON(t *testing.T) {
	cases := []struct {
		name     string
		fixture  string
		target   string
		goldenJS string
	}{
		{
			name:     "invoice-parse-dev-bootstrap",
			fixture:  "testdata/invoice-parse-bootstrap.yaml",
			target:   "dev-bootstrap",
			goldenJS: "testdata/invoice-parse-bootstrap.dev-bootstrap.spec.json",
		},
		{
			name:     "invoice-parse-dev-inproc",
			fixture:  "testdata/invoice-parse-bootstrap.yaml",
			target:   "dev-inproc",
			goldenJS: "testdata/invoice-parse-bootstrap.dev-inproc.spec.json",
		},
		{
			// M4: composition flip target — proves the resolver applies
			// the per-target `composition: { kind: rabbitmq }` override,
			// landing variant=rabbitmq + QUEUE_URL=amqp://... on the
			// processing consumer.
			name:     "invoice-parse-dev-rabbitmq-flip",
			fixture:  "testdata/invoice-parse-bootstrap.yaml",
			target:   "dev-rabbitmq-flip",
			goldenJS: "testdata/invoice-parse-bootstrap.dev-rabbitmq-flip.spec.json",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rp, diag, err := CompileFileForTarget(tc.fixture, tc.target)
			if err != nil {
				t.Fatalf("compile error: %v", err)
			}
			if diag.HasErrors() {
				buf := &bytes.Buffer{}
				_, _ = diag.WriteTo(buf)
				t.Fatalf("compile produced errors:\n%s", buf.String())
			}
			got, err := json.MarshalIndent(rp, "", "  ")
			if err != nil {
				t.Fatalf("marshal: %v", err)
			}
			// Normalize trailing newline for golden-file parity.
			got = append(got, '\n')

			if *update {
				if err := os.WriteFile(tc.goldenJS, got, 0o644); err != nil {
					t.Fatalf("update golden: %v", err)
				}
				t.Logf("updated %s", tc.goldenJS)
				return
			}
			want, err := os.ReadFile(tc.goldenJS)
			if err != nil {
				t.Fatalf("read golden %s: %v (run with -update to create)", tc.goldenJS, err)
			}
			if !bytes.Equal(got, want) {
				t.Errorf("spec drift in %s:\n--- got ---\n%s\n--- want ---\n%s", tc.name, got, want)
			}
		})
	}
}

// TestSpecMatchesB0HandEdit pins the LLMExtraction peer entry to the
// exact JSON string B0 hand-wrote in docker-compose.caravan-bootstrap.yaml.
// This is the load-bearing M0 acceptance — if M1 emits this exact value
// into compose, the docker compose merge mirrors B0 byte-for-byte.
func TestSpecMatchesB0HandEdit(t *testing.T) {
	rp, diag, err := CompileFileForTarget("testdata/invoice-parse-bootstrap.yaml", "dev-bootstrap")
	if err != nil {
		t.Fatalf("compile error: %v", err)
	}
	if diag.HasErrors() {
		buf := &bytes.Buffer{}
		_, _ = diag.WriteTo(buf)
		t.Fatalf("compile produced errors:\n%s", buf.String())
	}
	if rp == nil {
		t.Fatal("nil ResolvedPlan")
	}
	got := rp.EnvVars["processing"]["CARAVAN_RPC_PEERS"]
	want := `{"LLMExtraction":{"mode":"http","url":"http://llm-extractor:8080"}}`
	if got != want {
		t.Errorf("CARAVAN_RPC_PEERS mismatch:\n  got:  %s\n  want: %s", got, want)
	}
}

// TestValidateEntryLanguages covers the M3 language-detection validator.
// Entry.Language is filled from the manifest files present in the entry's
// path:
//
//	pyproject.toml or requirements.txt → python
//	Cargo.toml                         → rust
//	both                               → error (ambiguous)
//	neither + path exists              → warn
//	neither + path missing             → silent (test-fixture friendly)
func TestValidateEntryLanguages(t *testing.T) {
	cases := []struct {
		name      string
		manifests []string // filenames to materialize inside entryPath
		wantLang  Language
		wantErr   string // substring expected in diagnostics (or "")
		wantWarn  string // substring expected in diagnostics (or "")
	}{
		{
			name:      "python via pyproject.toml",
			manifests: []string{"pyproject.toml"},
			wantLang:  LanguagePython,
		},
		{
			name:      "python via requirements.txt",
			manifests: []string{"requirements.txt"},
			wantLang:  LanguagePython,
		},
		{
			name:      "rust via Cargo.toml",
			manifests: []string{"Cargo.toml"},
			wantLang:  LanguageRust,
		},
		{
			name:      "ambiguous: rust + python manifests both present",
			manifests: []string{"Cargo.toml", "pyproject.toml"},
			wantErr:   "ambiguous language",
		},
		{
			name:      "path exists but no manifest → warn",
			manifests: []string{}, // dir made empty, no manifest files
			wantWarn:  "no recognized manifest",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tmp := t.TempDir()
			entryDir := filepath.Join(tmp, "svc")
			if err := os.MkdirAll(entryDir, 0o755); err != nil {
				t.Fatal(err)
			}
			for _, m := range tc.manifests {
				if err := os.WriteFile(filepath.Join(entryDir, m), []byte(""), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			yamlBody := `name: x
default_target: t
entries:
  svc:
    path: ./svc
targets:
  t: { runtime: docker-compose }
`
			path := filepath.Join(tmp, "caravan.yaml")
			if err := os.WriteFile(path, []byte(yamlBody), 0o644); err != nil {
				t.Fatal(err)
			}
			plan, diag, err := CompileFile(path)
			if err != nil {
				t.Fatalf("compile: %v", err)
			}
			buf := &bytes.Buffer{}
			_, _ = diag.WriteTo(buf)
			body := buf.String()
			if tc.wantErr != "" {
				if !strings.Contains(body, tc.wantErr) {
					t.Errorf("expected error containing %q; got:\n%s", tc.wantErr, body)
				}
				return
			}
			if tc.wantWarn != "" && !strings.Contains(body, tc.wantWarn) {
				t.Errorf("expected warn containing %q; got:\n%s", tc.wantWarn, body)
			}
			if diag.HasErrors() {
				t.Fatalf("unexpected errors:\n%s", body)
			}
			if plan == nil {
				t.Fatal("nil plan")
			}
			got := plan.Entries["svc"].Language
			if got != tc.wantLang {
				t.Errorf("Language: got %q, want %q", got, tc.wantLang)
			}
		})
	}
}

// TestValidateEntryLanguages_SyntheticPathPassesSilently confirms
// existing tests with non-existent paths (`path: ./api`) keep working —
// no warn, no error, Language stays empty.
func TestValidateEntryLanguages_SyntheticPathPassesSilently(t *testing.T) {
	tmp := t.TempDir()
	path := filepath.Join(tmp, "caravan.yaml")
	yamlBody := `name: x
default_target: t
entries:
  api:
    path: ./nonexistent-svc
targets:
  t: { runtime: docker-compose }
`
	if err := os.WriteFile(path, []byte(yamlBody), 0o644); err != nil {
		t.Fatal(err)
	}
	plan, diag, err := CompileFile(path)
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	if diag.HasErrors() {
		buf := &bytes.Buffer{}
		_, _ = diag.WriteTo(buf)
		t.Fatalf("unexpected errors:\n%s", buf.String())
	}
	if plan == nil {
		t.Fatal("nil plan")
	}
	if plan.Entries["api"].Language != "" {
		t.Errorf("Language: got %q, want empty (synthetic path)", plan.Entries["api"].Language)
	}
}

// TestNormalizeErrors covers the negative cases the M0 acceptance calls
// out: unknown ref in `uses:`, duplicate entry name, unknown resource
// type. Each must produce a Diagnostic.Error.
func TestNormalizeErrors(t *testing.T) {
	cases := []struct {
		name string
		yaml string
		want string // substring expected in the diagnostic message
	}{
		{
			name: "unknown uses ref",
			yaml: `name: x
default_target: t
entries:
  api:
    path: ./api
    uses: [nonexistent]
targets:
  t: { runtime: docker-compose }
`,
			want: `unknown name "nonexistent"`,
		},
		{
			name: "duplicate seam in yaml",
			yaml: `name: x
default_target: t
seams:
  S:
    path: ./s
  S:
    path: ./s2
targets:
  t: { runtime: docker-compose }
`,
			want: `duplicate seam`,
		},
		{
			name: "unknown resource type",
			yaml: `name: x
default_target: t
resources:
  r: { type: nonexistent-kind }
targets:
  t: { runtime: docker-compose }
`,
			want: `unknown resource type`,
		},
		{
			name: "container seam without impl",
			yaml: `name: x
default_target: t
seams:
  S:
    path: ./s
targets:
  t:
    runtime: docker-compose
    seams: { S: container }
`,
			want: `no ` + "`impl:`",
		},
		{
			name: "missing top-level name",
			yaml: `default_target: t
targets:
  t: { runtime: docker-compose }
`,
			want: "top-level `name:` is required",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tmp := t.TempDir()
			path := filepath.Join(tmp, "caravan.yaml")
			if err := os.WriteFile(path, []byte(tc.yaml), 0o644); err != nil {
				t.Fatal(err)
			}
			_, diag, err := CompileFile(path)
			if err != nil {
				// Some negative cases (duplicate yaml keys) are lex-time
				// errors from yaml.v3 — that's also acceptable here.
				if strings.Contains(err.Error(), tc.want) {
					return
				}
				// Otherwise, surface for visibility.
				t.Logf("err: %v", err)
			}
			buf := &bytes.Buffer{}
			_, _ = diag.WriteTo(buf)
			body := buf.String()
			if !strings.Contains(body, tc.want) {
				if err != nil && strings.Contains(err.Error(), tc.want) {
					return
				}
				t.Errorf("expected diagnostic containing %q; got:\n%s\nerr=%v", tc.want, body, err)
			}
		})
	}
}
