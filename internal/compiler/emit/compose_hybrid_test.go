package emit

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/paulxiep/caravan/internal/compiler"
)

// TestEmitComposeOverride_HybridDev exercises the M4-cloud hybrid path
// end-to-end at the emit level. The synthesized ResolvedPlan has:
//
//   - one container-mode entry (processing) that uses a cloud-managed
//     bucket + db + queue;
//   - all seams inproc (no peer services to emit);
//   - target.CredsPassthrough true with region us-east-1 and profile
//     caravan-poc.
//
// Assertions are substring-based — the goal is to pin the load-bearing
// behavior (mount, env-var passthroughs, no MinIO container) without
// being so strict that compose-emit cosmetic tweaks break the test.
func TestEmitComposeOverride_HybridDev(t *testing.T) {
	plan := &compiler.Plan{
		Name:      "invoice-parse",
		OutputDir: "infra",
		Entries: map[string]*compiler.Entry{
			"processing": {
				Name:       "processing",
				Path:       "services/processing",
				Dockerfile: "services/processing/Dockerfile",
				Triggers: []compiler.Trigger{
					{Kind: compiler.TriggerQueue, Queue: &compiler.QueueTrigger{From: "invoice_queue"}},
				},
				Uses: []string{"invoice_queue", "invoice_db", "invoice_blobs"},
			},
		},
		Resources: map[string]*compiler.Resource{
			"invoice_blobs": {Name: "invoice_blobs", Type: compiler.ResourceBucket},
			"invoice_db":    {Name: "invoice_db", Type: compiler.ResourceDBSQL},
			"invoice_queue": {Name: "invoice_queue", Type: compiler.ResourceQueue},
		},
		Targets: map[string]*compiler.Target{
			"hybrid-dev": {
				Name:               "hybrid-dev",
				Runtime:            compiler.RuntimeDockerCompose,
				Region:             "us-east-1",
				DefaultComposition: compiler.CompositionCloudManaged,
				CredsPassthrough:   true,
				AwsProfile:         "caravan-poc",
				Backend: &compiler.BackendConfig{
					Bucket:    "my-state",
					LockTable: "my-lock",
					Region:    "us-east-1",
					Key:       "invoice-parse/hybrid-dev.tfstate",
				},
				Entries: map[string]compiler.EntryDispatchMode{
					"processing": compiler.EntryContainer,
				},
			},
		},
	}

	diag := &compiler.Diagnostics{}
	rp := compiler.Resolve(plan, "hybrid-dev", diag)
	if rp == nil {
		t.Fatalf("Resolve returned nil; diag=%v", diag)
	}
	if diag.HasErrors() {
		t.Fatalf("Resolve errors: %#v", diag)
	}

	body, err := EmitComposeOverride(rp, "invoice-parse", nil)
	if err != nil {
		t.Fatalf("EmitComposeOverride: %v", err)
	}
	out := string(body)

	home, _ := os.UserHomeDir()
	wantMount := filepath.ToSlash(filepath.Join(home, ".aws")) + ":/root/.aws:ro"

	wants := []string{
		// Mount substring (quoted in compose output).
		`"` + wantMount + `"`,
		// AWS_REGION + AWS_PROFILE injected by creds_passthrough.
		"AWS_REGION: us-east-1",
		"AWS_PROFILE: caravan-poc",
		// Cloud-managed endpoint passthroughs.
		"S3_BUCKET: ${S3_BUCKET}",
		"DATABASE_URL: ${DATABASE_URL}",
		"QUEUE_URL: ${QUEUE_URL}",
	}
	for _, w := range wants {
		if !strings.Contains(out, w) {
			t.Errorf("missing substring %q in compose output:\n%s", w, out)
		}
	}

	// No oss-local resource containers should appear.
	bad := []string{
		"minio/minio",
		"postgres:16-alpine",
		"S3_ENDPOINT_URL", // never emitted in cloud-managed mode
	}
	for _, b := range bad {
		if strings.Contains(out, b) {
			t.Errorf("unexpected substring %q in compose output (cloud-managed should not emit oss-local containers or endpoint url):\n%s", b, out)
		}
	}
}

// TestEmitComposeOverride_NonHybridUnchanged confirms an oss-local
// target keeps its Phase-1 behavior (no ~/.aws mount, no AWS_PROFILE).
// Guards against the creds_passthrough block leaking into existing
// targets.
func TestEmitComposeOverride_NonHybridUnchanged(t *testing.T) {
	plan := &compiler.Plan{
		Name:      "invoice-parse",
		OutputDir: "infra",
		Entries: map[string]*compiler.Entry{
			"processing": {Name: "processing", Path: "services/processing", Uses: []string{"invoice_blobs"}},
		},
		Resources: map[string]*compiler.Resource{
			"invoice_blobs": {Name: "invoice_blobs", Type: compiler.ResourceBucket},
		},
		Targets: map[string]*compiler.Target{
			"dev-bootstrap": {
				Name:    "dev-bootstrap",
				Runtime: compiler.RuntimeDockerCompose,
				Entries: map[string]compiler.EntryDispatchMode{
					"processing": compiler.EntryContainer,
				},
			},
		},
	}
	diag := &compiler.Diagnostics{}
	rp := compiler.Resolve(plan, "dev-bootstrap", diag)
	body, err := EmitComposeOverride(rp, "invoice-parse", nil)
	if err != nil {
		t.Fatalf("EmitComposeOverride: %v", err)
	}
	out := string(body)
	if strings.Contains(out, "AWS_PROFILE") {
		t.Errorf("non-hybrid target should not inject AWS_PROFILE:\n%s", out)
	}
	if strings.Contains(out, "/root/.aws") {
		t.Errorf("non-hybrid target should not mount ~/.aws:\n%s", out)
	}
}
