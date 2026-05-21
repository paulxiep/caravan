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

	got, err := EmitComposeOverride(rp, "", nil)
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

// TestEmitComposeOverride_TwoSeams (M3) verifies the multi-seam case:
// when caravan.yaml declares two container-mode seams, the emit loop
// produces a peer service per seam and the consumer's CARAVAN_RPC_PEERS
// carries both interface entries. Uses substring assertions (not a
// byte-golden) because M4's resource emit fires through the same
// pipeline against this fixture; M4 owns the resource portion's
// shape.
func TestEmitComposeOverride_TwoSeams(t *testing.T) {
	rp, diag, err := compiler.CompileFileForTarget("../testdata/invoice-parse-two-seams.yaml", "dev-bootstrap-twoseams")
	if err != nil || diag.HasErrors() {
		t.Fatalf("compile error: %v / %v", err, diag.HasErrors())
	}
	got, err := EmitComposeOverride(rp, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	out := string(got)

	mustContain := []string{
		// Both peer services emitted
		"llm-extractor:",
		"ocr-text:",
		// Both interfaces in the consumer's peers JSON
		`"LLMExtraction":{"mode":"http","url":"http://llm-extractor:8080"}`,
		`"OCRText":{"mode":"http","url":"http://ocr-text:8080"}`,
		// Each peer service runs its own serve command with its own interface
		`- "LLMExtraction"`,
		`- "OCRText"`,
		`- "invoice_processing.extraction:GeminiExtractor"`,
		`- "invoice_processing.ocr:PaddleOCRTextImpl"`,
		// Consumer depends_on both peer services (sorted alphabetic)
		"llm-extractor:\n        condition: service_started",
		"ocr-text:\n        condition: service_started",
	}
	for _, s := range mustContain {
		if !strings.Contains(out, s) {
			t.Errorf("missing substring %q in output:\n%s", s, out)
		}
	}
}

// TestEmitComposeOverride_SeamMixAndMatch (M3) verifies that when one
// seam is inproc and the other is container, only the container seam
// emits a peer service and the inproc seam appears in CARAVAN_RPC_PEERS
// as {"mode":"inproc"}. This is the thesis claim: yaml-line changes
// alone flip dispatch per-seam.
func TestEmitComposeOverride_SeamMixAndMatch(t *testing.T) {
	rp, diag, err := compiler.CompileFileForTarget("../testdata/invoice-parse-two-seams.yaml", "dev-split-llm")
	if err != nil || diag.HasErrors() {
		t.Fatalf("compile error: %v / %v", err, diag.HasErrors())
	}
	got, err := EmitComposeOverride(rp, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	out := string(got)

	mustContain := []string{
		// Only LLMExtraction peer service emitted
		"llm-extractor:",
		// OCRText dispatches inproc — no peer service emit
		`"OCRText":{"mode":"inproc"}`,
		// LLMExtraction is still container-mode
		`"LLMExtraction":{"mode":"http","url":"http://llm-extractor:8080"}`,
	}
	for _, s := range mustContain {
		if !strings.Contains(out, s) {
			t.Errorf("missing substring %q in output:\n%s", s, out)
		}
	}
	// Negative: ocr-text peer service must NOT appear when OCRText is inproc
	if strings.Contains(out, "ocr-text:\n") {
		t.Errorf("ocr-text peer service emitted but OCRText is inproc:\n%s", out)
	}
}

// TestEmitComposeRabbitMQFlip is M4's load-bearing acceptance test:
// flipping `composition.invoice_queue: { kind: rabbitmq }` in yaml
// causes the emitter to (a) inject QUEUE_URL=amqp://... into the
// `processing` consumer (instead of redis://...) and (b) write a
// `rabbitmq:` service definition. Same source, different deployment;
// the thesis claim on the composition dimension.
func TestEmitComposeRabbitMQFlip(t *testing.T) {
	rp, diag, err := compiler.CompileFileForTarget("../testdata/invoice-parse-bootstrap.yaml", "dev-rabbitmq-flip")
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	if diag.HasErrors() {
		buf := &bytes.Buffer{}
		_, _ = diag.WriteTo(buf)
		t.Fatalf("compile errors:\n%s", buf.String())
	}

	got, err := EmitComposeOverride(rp, "", nil)
	if err != nil {
		t.Fatalf("emit: %v", err)
	}
	out := string(got)

	mustContain := []string{
		// Resource env var flipped to amqp:// for the rabbitmq variant
		"QUEUE_URL: amqp://guest:guest@rabbitmq:5672",
		// New rabbitmq service emitted
		"rabbitmq:",
		"image: rabbitmq:3-management-alpine",
		"RABBITMQ_DEFAULT_USER: guest",
	}
	mustNotContain := []string{
		// The redis-streams QUEUE_URL must not appear — the flip
		// replaces it entirely. (Redis still emits as a service for
		// any cache resource, but no consumer should get the redis
		// QUEUE_URL on this target.)
		"QUEUE_URL: redis://",
	}
	for _, s := range mustContain {
		if !strings.Contains(out, s) {
			t.Errorf("rabbitmq flip missing substring %q\n--- output ---\n%s", s, out)
		}
	}
	for _, s := range mustNotContain {
		if strings.Contains(out, s) {
			t.Errorf("rabbitmq flip should not contain %q\n--- output ---\n%s", s, out)
		}
	}
}

// TestEmitComposeBaseComposeCollision is M4's "skip emission when
// service already named in base compose" guarantee. With a base set
// that includes `postgres` + `redis`, M4 must not emit those
// containers but MUST still inject DATABASE_URL / QUEUE_URL into the
// consumer entry.
func TestEmitComposeBaseComposeCollision(t *testing.T) {
	rp, diag, err := compiler.CompileFileForTarget("../testdata/invoice-parse-bootstrap.yaml", "dev-bootstrap")
	if err != nil || diag.HasErrors() {
		t.Fatalf("compile error: %v / %v", err, diag.HasErrors())
	}
	baseServices := map[string]bool{
		"postgres": true,
		"redis":    true,
	}
	got, err := EmitComposeOverride(rp, "", baseServices)
	if err != nil {
		t.Fatal(err)
	}
	out := string(got)

	// Env vars still flow into the processing consumer
	mustContain := []string{
		"DATABASE_URL: postgresql://caravan:caravan@postgres:5432/caravan",
		"QUEUE_URL: redis://redis:6379",
		// MinIO has no collision → still emitted
		"minio:",
	}
	mustNotContain := []string{
		// Caravan must not double-publish postgres / redis
		"\n  postgres:\n",
		"\n  redis:\n",
	}
	for _, s := range mustContain {
		if !strings.Contains(out, s) {
			t.Errorf("collision case missing substring %q\n--- output ---\n%s", s, out)
		}
	}
	for _, s := range mustNotContain {
		if strings.Contains(out, s) {
			t.Errorf("collision case must not contain %q\n--- output ---\n%s", s, out)
		}
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
	got, err := EmitComposeOverride(rp, "", nil)
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
