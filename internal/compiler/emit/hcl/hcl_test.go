package hcl

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/paulxiep/caravan/internal/compiler"
)

// TestEmitHCL_InvoiceParseHybridDev exercises the full HCL emit path
// against a synthesized invoice-parse hybrid-dev plan. Substring-based
// assertions — the file shapes are pinned where load-bearing (resource
// types, IAM actions, outputs) and left flexible where they don't
// matter (formatting, attribute order).
func TestEmitHCL_InvoiceParseHybridDev(t *testing.T) {
	plan := &compiler.Plan{
		Name: "invoice-parse",
		Entries: map[string]*compiler.Entry{
			"processing": {
				Name: "processing",
				Triggers: []compiler.Trigger{
					{Kind: compiler.TriggerQueue, Queue: &compiler.QueueTrigger{From: "invoice_queue"}},
				},
				Uses: []string{"invoice_queue", "invoice_db", "invoice_blobs"},
			},
			"ingest": {
				Name: "ingest",
				Uses: []string{"invoice_queue", "invoice_blobs"},
			},
		},
		Resources: map[string]*compiler.Resource{
			"invoice_blobs": {Name: "invoice_blobs", Type: compiler.ResourceBucket},
			"invoice_db": {
				Name: "invoice_db", Type: compiler.ResourceDBSQL,
				User: "invoice", Password: "invoice", DBName: "invoice_parse",
			},
			"invoice_queue": {Name: "invoice_queue", Type: compiler.ResourceQueue},
		},
		Targets: map[string]*compiler.Target{
			"hybrid-dev": {
				Name:               "hybrid-dev",
				Runtime:            compiler.RuntimeDockerCompose,
				Region:             "ap-southeast-1",
				DefaultComposition: compiler.CompositionCloudManaged,
				CredsPassthrough:   true,
				AwsProfile:         "caravan-poc",
				Backend: &compiler.BackendConfig{
					Bucket:    "caravan-rpc-poc-state",
					LockTable: "caravan-poc-state-lock",
					Region:    "ap-southeast-1",
					Key:       "invoice-parse/hybrid-dev.tfstate",
				},
				Entries: map[string]compiler.EntryDispatchMode{
					"processing": compiler.EntryContainer,
					"ingest":     compiler.EntryContainer,
				},
			},
		},
	}
	diag := &compiler.Diagnostics{}
	rp := compiler.Resolve(plan, "hybrid-dev", diag)
	if rp == nil || diag.HasErrors() {
		t.Fatalf("Resolve failed: %v", diag)
	}

	outDir := t.TempDir()
	written, err := EmitHCL(rp, outDir)
	if err != nil {
		t.Fatalf("EmitHCL: %v", err)
	}
	if len(written) != 4 {
		t.Errorf("expected 4 files (versions, backend, main, iam); got %d: %v", len(written), written)
	}

	read := func(name string) string {
		b, err := os.ReadFile(filepath.Join(outDir, name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		return string(b)
	}

	versions := read("versions.tf")
	for _, want := range []string{
		`required_version`,
		`">= 1.6.0"`,
		`hashicorp/aws`,
		`"~> 5.0"`,
	} {
		if !strings.Contains(versions, want) {
			t.Errorf("versions.tf missing %q:\n%s", want, versions)
		}
	}

	backend := read("backend.tf")
	// hclwrite aligns `=` signs, so we check key + value substrings
	// independently (not the literal "bucket = ..." form).
	for _, want := range []string{
		`backend "s3"`,
		`"caravan-rpc-poc-state"`,
		`"caravan-poc-state-lock"`,
		`"invoice-parse/hybrid-dev.tfstate"`,
		`"ap-southeast-1"`,
	} {
		if !strings.Contains(backend, want) {
			t.Errorf("backend.tf missing %q:\n%s", want, backend)
		}
	}

	main := read("main.tf")
	for _, want := range []string{
		// Provider
		`provider "aws"`,
		// Security group + IP lookup (RDS triggers it)
		`data "http" "myip"`,
		`"aws_security_group" "caravan_dev"`,
		// Resources
		`resource "aws_s3_bucket" "invoice_blobs"`,
		`"invoice-parse-invoice-blobs-hybrid-dev"`,
		`resource "aws_db_instance" "invoice_db"`,
		`"postgres"`,
		`"invoice_parse"`,
		`resource "aws_sqs_queue" "invoice_queue"`,
		// Outputs for the .env.hybrid flow
		`output "DATABASE_URL"`,
		`output "QUEUE_URL"`,
		`output "S3_BUCKET"`,
	} {
		if !strings.Contains(main, want) {
			t.Errorf("main.tf missing %q:\n%s", want, main)
		}
	}
	// No OpenSearch (no search resource declared)
	if strings.Contains(main, "aws_opensearch_domain") {
		t.Errorf("main.tf should not emit OpenSearch when no resource uses it:\n%s", main)
	}

	iam := read("iam.tf")
	for _, want := range []string{
		// IAM user data lookup
		`data "aws_iam_user" "caravan_poc"`,
		`user_name = "caravan-poc"`,
		// Per-entry policies
		`aws_iam_user_policy" "processing"`,
		`aws_iam_user_policy" "ingest"`,
		// Action surfaces — processing consumes + produces queue
		`"sqs:ReceiveMessage"`,
		`"sqs:DeleteMessage"`,
		`"sqs:SendMessage"`,
		// S3 actions
		`"s3:GetObject"`,
		`"s3:PutObject"`,
		// Resource refs via HCL identifier
		`aws_s3_bucket.invoice_blobs.arn`,
		`aws_sqs_queue.invoice_queue.arn`,
	} {
		if !strings.Contains(iam, want) {
			t.Errorf("iam.tf missing %q:\n%s", want, iam)
		}
	}
}

// TestEmitHCL_OpenSearchGated confirms aws_opensearch_domain emits ONLY
// when at least one entry's uses: lists the search resource. Cost
// guard per dev_plan §759.
func TestEmitHCL_OpenSearchGated(t *testing.T) {
	makePlan := func(uses []string) *compiler.Plan {
		return &compiler.Plan{
			Name: "app",
			Entries: map[string]*compiler.Entry{
				"api": {Name: "api", Uses: uses},
			},
			Resources: map[string]*compiler.Resource{
				"vectors": {Name: "vectors", Type: compiler.ResourceSearch},
			},
			Targets: map[string]*compiler.Target{
				"hybrid-dev": {
					Name: "hybrid-dev", Runtime: compiler.RuntimeDockerCompose,
					Region: "us-east-1", DefaultComposition: compiler.CompositionCloudManaged,
					CredsPassthrough: true, AwsProfile: "caravan-poc",
					Backend: &compiler.BackendConfig{Bucket: "b", LockTable: "l", Region: "us-east-1", Key: "k"},
					Entries: map[string]compiler.EntryDispatchMode{"api": compiler.EntryContainer},
				},
			},
		}
	}

	t.Run("declared but unused: NOT emitted", func(t *testing.T) {
		rp := compiler.Resolve(makePlan(nil), "hybrid-dev", &compiler.Diagnostics{})
		outDir := t.TempDir()
		if _, err := EmitHCL(rp, outDir); err != nil {
			t.Fatal(err)
		}
		body, _ := os.ReadFile(filepath.Join(outDir, "main.tf"))
		if strings.Contains(string(body), "aws_opensearch_domain") {
			t.Errorf("unused search resource should be gated:\n%s", body)
		}
	})

	t.Run("declared and used: emitted", func(t *testing.T) {
		rp := compiler.Resolve(makePlan([]string{"vectors"}), "hybrid-dev", &compiler.Diagnostics{})
		outDir := t.TempDir()
		if _, err := EmitHCL(rp, outDir); err != nil {
			t.Fatal(err)
		}
		body, _ := os.ReadFile(filepath.Join(outDir, "main.tf"))
		if !strings.Contains(string(body), "aws_opensearch_domain") {
			t.Errorf("used search resource should emit:\n%s", body)
		}
	})
}

// TestEmitHCL_NoIAMFileWhenNoGrants confirms iam.tf is skipped when no
// entry consumes a cloud-managed resource that grants IAM perms (i.e.
// db.sql / cache only). Avoids an empty placeholder file.
func TestEmitHCL_NoIAMFileWhenNoGrants(t *testing.T) {
	plan := &compiler.Plan{
		Name: "app",
		Entries: map[string]*compiler.Entry{
			"api": {Name: "api", Uses: []string{"db"}},
		},
		Resources: map[string]*compiler.Resource{
			"db": {Name: "db", Type: compiler.ResourceDBSQL, User: "u", Password: "p", DBName: "d"},
		},
		Targets: map[string]*compiler.Target{
			"hybrid-dev": {
				Name: "hybrid-dev", Runtime: compiler.RuntimeDockerCompose,
				Region: "us-east-1", DefaultComposition: compiler.CompositionCloudManaged,
				CredsPassthrough: true, AwsProfile: "caravan-poc",
				Backend: &compiler.BackendConfig{Bucket: "b", LockTable: "l", Region: "us-east-1", Key: "k"},
				Entries: map[string]compiler.EntryDispatchMode{"api": compiler.EntryContainer},
			},
		},
	}
	rp := compiler.Resolve(plan, "hybrid-dev", &compiler.Diagnostics{})
	outDir := t.TempDir()
	written, err := EmitHCL(rp, outDir)
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range written {
		if strings.HasSuffix(p, "iam.tf") {
			t.Errorf("iam.tf should not be written when no IAM grants exist: %v", written)
		}
	}
	if _, err := os.Stat(filepath.Join(outDir, "iam.tf")); err == nil {
		t.Errorf("iam.tf exists on disk despite no grants")
	}
}
