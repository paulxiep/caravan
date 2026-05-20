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
