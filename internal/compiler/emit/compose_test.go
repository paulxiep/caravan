package emit

import (
	"bytes"
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/paulxiep/caravan/internal/compiler"
)

var update = flag.Bool("update", false, "rewrite golden fixtures")

// TestEmitComposeOverride is the M1 acceptance test (offline portion).
// The full M1 gate also requires docker compose + ingest + postgres
// extraction comparison against B0's .b0-runs/c1/extraction.json — see
// docs/development_plan.md §M1.
func TestEmitComposeOverride(t *testing.T) {
	const fixture = "../testdata/invoice-parse-bootstrap.yaml"
	const golden = "../testdata/dev-bootstrap.override.golden.yaml"

	rp, diag, err := compiler.CompileFileForTarget(fixture, "dev-bootstrap")
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	if diag.HasErrors() {
		buf := &bytes.Buffer{}
		_, _ = diag.WriteTo(buf)
		t.Fatalf("compile errors:\n%s", buf.String())
	}

	got, err := EmitComposeOverride(rp)
	if err != nil {
		t.Fatalf("emit: %v", err)
	}

	if *update {
		if err := os.WriteFile(filepath.Clean(golden), got, 0o644); err != nil {
			t.Fatalf("write golden: %v", err)
		}
		t.Logf("updated %s", golden)
		return
	}
	want, err := os.ReadFile(filepath.Clean(golden))
	if err != nil {
		t.Fatalf("read golden %s: %v (run with -update to create)", golden, err)
	}
	if !bytes.Equal(got, want) {
		t.Errorf("compose drift:\n--- got ---\n%s\n--- want ---\n%s", got, want)
	}
}

// TestEmitComposeMatchesB0Shape pins the load-bearing pieces of the
// emitted override against B0's hand-edit. Robust to yaml formatting
// drift (blank lines, scalar style) by checking string content.
func TestEmitComposeMatchesB0Shape(t *testing.T) {
	rp, diag, err := compiler.CompileFileForTarget("../testdata/invoice-parse-bootstrap.yaml", "dev-bootstrap")
	if err != nil || diag.HasErrors() {
		t.Fatalf("compile error: %v / %v", err, diag.HasErrors())
	}
	got, err := EmitComposeOverride(rp)
	if err != nil {
		t.Fatal(err)
	}
	out := string(got)

	mustContain := []string{
		`CARAVAN_RPC_PEERS: '{"LLMExtraction":{"mode":"http","url":"http://llm-extractor:8080"}}'`,
		`CARAVAN_RPC_SHARED_SECRET: dev-secret-placeholder`,
		`condition: service_started`,
		`dockerfile: services/processing/Dockerfile`,
		// command-args use docker-compose-required quoted style.
		`- "caravan_rpc.serve"`,
		`- "LLMExtraction"`,
		`- "invoice_processing.extraction:GeminiExtractor"`,
		`- "8080"`,
		`profiles:`,
		`- app`,
	}
	for _, s := range mustContain {
		if !strings.Contains(out, s) {
			t.Errorf("emitted compose missing: %q\n--- output ---\n%s", s, out)
		}
	}
}
