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
	written, err := EmitHCL(rp, outDir, nil)
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
		if _, err := EmitHCL(rp, outDir, nil); err != nil {
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
		if _, err := EmitHCL(rp, outDir, nil); err != nil {
			t.Fatal(err)
		}
		body, _ := os.ReadFile(filepath.Join(outDir, "main.tf"))
		if !strings.Contains(string(body), "aws_opensearch_domain") {
			t.Errorf("used search resource should emit:\n%s", body)
		}
	})
}

// TestEmitHCL_FargateMulti exercises the full M4b Fargate emit path
// against a synthesized code-rag-shaped plan: one container-mode entry
// (chat) + one container-mode seam (Embedder) peer. Asserts the
// load-bearing pieces — VPC, ECS cluster, Cloud Map namespace + per-seam
// service, ECR data lookup, per-consumer task def + service, IAM role
// (not user-policy) attachment — without pinning hclwrite formatting.
//
// Catches regressions in the placement-emitter abstraction, the IAM
// principal-kind refactor, and the resolve.go Cloud Map FQDN dispatch.
func TestEmitHCL_FargateMulti(t *testing.T) {
	plan := &compiler.Plan{
		Name: "code-rag",
		Entries: map[string]*compiler.Entry{
			"chat": {
				Name:       "chat",
				Path:       ".",
				Dockerfile: "dockerfile/Dockerfile",
			},
		},
		Seams: map[string]*compiler.Seam{
			"Embedder": {
				Name:        "Embedder",
				Impl:        "code_rag_store::FastEmbedImpl",
				ServiceName: "embedder",
			},
		},
		Targets: map[string]*compiler.Target{
			"staging-fargate": {
				Name:       "staging-fargate",
				Runtime:    compiler.RuntimeFargate,
				Region:     "ap-southeast-1",
				AwsProfile: "caravan-poc",
				Backend: &compiler.BackendConfig{
					Bucket:    "caravan-rpc-poc-state",
					LockTable: "caravan-poc-state-lock",
					Region:    "ap-southeast-1",
					Key:       "code-rag/staging-fargate.tfstate",
				},
				Entries: map[string]compiler.EntryDispatchMode{
					"chat": compiler.EntryContainer,
				},
				Seams: map[string]compiler.SeamDispatchMode{
					"Embedder": compiler.SeamContainer,
				},
				VPC:               &compiler.VPCConfig{CIDR: "10.0.0.0/16", NAT: "single"},
				CloudMapNamespace: "code-rag.local",
				ECSClusterName:    "code-rag-staging-fargate",
			},
		},
	}
	diag := &compiler.Diagnostics{}
	rp := compiler.Resolve(plan, "staging-fargate", diag)
	if rp == nil || diag.HasErrors() {
		t.Fatalf("Resolve failed: %v", diag)
	}

	outDir := t.TempDir()
	written, err := EmitHCL(rp, outDir, nil)
	if err != nil {
		t.Fatalf("EmitHCL: %v", err)
	}
	// versions.tf + backend.tf + main.tf. No iam.tf (no IAM grants — no
	// cloud-managed resources declared in this synthetic plan).
	if len(written) != 3 {
		t.Errorf("expected 3 files (versions, backend, main); got %d: %v", len(written), written)
	}

	read := func(name string) string {
		b, err := os.ReadFile(filepath.Join(outDir, name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		return string(b)
	}

	main := read("main.tf")
	for _, want := range []string{
		// Provider
		`provider "aws"`,
		`"ap-southeast-1"`,
		// VPC layer
		`"aws_vpc" "caravan"`,
		`"10.0.0.0/16"`,
		`"aws_internet_gateway" "caravan"`,
		`"aws_subnet" "caravan_public_a"`,
		`"aws_subnet" "caravan_public_b"`,
		`"aws_subnet" "caravan_private_a"`,
		`"aws_subnet" "caravan_private_b"`,
		`"aws_nat_gateway" "caravan"`,
		`"aws_route_table" "caravan_public"`,
		`"aws_route_table" "caravan_private"`,
		`"aws_security_group" "caravan_tasks"`,
		// ECS cluster + Cloud Map namespace
		`"aws_ecs_cluster" "caravan"`,
		`"code-rag-staging-fargate"`,
		`"aws_service_discovery_private_dns_namespace" "caravan"`,
		`"code-rag.local"`,
		// Execution role (per-target, AWS-managed policy attached)
		`"aws_iam_role" "caravan_execution"`,
		`AmazonECSTaskExecutionRolePolicy`,
		// ECR data lookups — repo name = entry name verbatim
		`"aws_ecr_repository" "caravan_chat"`,
		`name = "chat"`,
		// Per-consumer task defs
		`"aws_ecs_task_definition" "chat"`,
		`"aws_ecs_task_definition" "embedder"`,
		`"FARGATE"`,
		`"awsvpc"`,
		// Cloud Map service ONLY for the seam (not the entry)
		`"aws_service_discovery_service" "embedder"`,
		// ECS services + service_registries on the seam
		`"aws_ecs_service" "chat"`,
		`"aws_ecs_service" "embedder"`,
		`service_registries`,
		// CARAVAN_RPC_PEERS env: Cloud Map FQDN for the Embedder URL
		`http://embedder.code-rag.local:8080`,
		// Peer service env carries the role switch
		`"peer-Embedder"`,
		// Outputs
		`output "VPC_ID"`,
		`output "CLOUD_MAP_NAMESPACE_ID"`,
		`output "CLUSTER_NAME"`,
		`output "TASKS_SG_ID"`,
	} {
		if !strings.Contains(main, want) {
			t.Errorf("main.tf missing %q", want)
		}
	}

	// Negative: the chat entry should NOT have a Cloud Map service
	// (only seams do — entries don't have internal callers within the
	// VPC for M4b's PoC scope).
	if strings.Contains(main, `"aws_service_discovery_service" "chat"`) {
		t.Errorf("chat entry should not register a Cloud Map service:\n%s", main)
	}
	// Negative: laptop-IP SG must NOT be emitted on Fargate targets
	// (the tasks SG replaces it; tasks reach resources from inside the
	// VPC, not from the developer's laptop).
	if strings.Contains(main, `"aws_security_group" "caravan_dev"`) {
		t.Errorf("Fargate target should not emit the laptop-IP SG:\n%s", main)
	}
	// Negative: Embedder URL in CARAVAN_RPC_PEERS must include the
	// Cloud Map namespace suffix, never a bare hostname.
	if strings.Contains(main, `http://embedder:8080`) {
		t.Errorf("Fargate target emitted bare hostname instead of Cloud Map FQDN:\n%s", main)
	}
}

// TestEmitHCL_FargateWithCloudManagedResources covers the Fargate ×
// cloud-managed-resource cross-product deferred from M4b to M9-cloud.
// Synthesizes a plan with three resources (S3 bucket, RDS, SQS queue,
// ElastiCache) on a Fargate target, then asserts the two M9-cloud bug
// fixes:
//
//  1. The hybrid-dev laptop-IP SG (`caravan_dev`) must NOT appear; the
//     new `caravan_resources` SG must reference the tasks SG via
//     intra-VPC ingress on the engine ports (5432, 6379).
//  2. Fargate task-def env entries for cloud-managed resources must be
//     raw HCL refs (e.g. aws_sqs_queue.X.url, interpolated postgres URLs)
//     instead of quoted `${VAR}` literals — a Fargate container has no
//     shell to expand compose-style passthroughs.
//
// Also exercises the new aws_db_subnet_group / aws_elasticache_subnet_group
// + publicly_accessible=false flip for VPC-anchored RDS/Cache.
func TestEmitHCL_FargateWithCloudManagedResources(t *testing.T) {
	plan := &compiler.Plan{
		Name: "invoice-parse",
		Entries: map[string]*compiler.Entry{
			"processing": {
				Name: "processing",
				Uses: []string{"invoice_queue", "invoice_db", "invoice_blobs", "session_cache"},
			},
		},
		Resources: map[string]*compiler.Resource{
			"invoice_blobs": {Name: "invoice_blobs", Type: compiler.ResourceBucket},
			"invoice_db": {
				Name: "invoice_db", Type: compiler.ResourceDBSQL,
				User: "invoice", Password: "invoice", DBName: "invoice_parse",
			},
			"invoice_queue": {Name: "invoice_queue", Type: compiler.ResourceQueue},
			"session_cache": {Name: "session_cache", Type: compiler.ResourceCache},
		},
		Targets: map[string]*compiler.Target{
			"prod-mixed": {
				Name:               "prod-mixed",
				Runtime:            compiler.RuntimeFargate,
				Region:             "ap-southeast-1",
				AwsProfile:         "caravan-poc",
				DefaultComposition: compiler.CompositionCloudManaged,
				Backend: &compiler.BackendConfig{
					Bucket:    "caravan-rpc-poc-state",
					LockTable: "caravan-poc-state-lock",
					Region:    "ap-southeast-1",
					Key:       "invoice-parse/prod-mixed.tfstate",
				},
				Entries: map[string]compiler.EntryDispatchMode{
					"processing": compiler.EntryContainer,
				},
				VPC:               &compiler.VPCConfig{CIDR: "10.0.0.0/16", NAT: "single"},
				CloudMapNamespace: "invoice-parse.local",
				ECSClusterName:    "invoice-parse-prod-mixed",
			},
		},
	}
	diag := &compiler.Diagnostics{}
	rp := compiler.Resolve(plan, "prod-mixed", diag)
	if rp == nil || diag.HasErrors() {
		t.Fatalf("Resolve failed: %v", diag)
	}

	outDir := t.TempDir()
	if _, err := EmitHCL(rp, outDir, nil); err != nil {
		t.Fatalf("EmitHCL: %v", err)
	}
	body, err := os.ReadFile(filepath.Join(outDir, "main.tf"))
	if err != nil {
		t.Fatal(err)
	}
	main := string(body)

	// Bug-fix #1: the new Fargate-resources SG must appear and reference
	// the tasks SG on the engine ports. hclwrite aligns `=` per block,
	// so substring assertions skip the spacing and pin the load-bearing
	// tokens only.
	for _, want := range []string{
		`"aws_security_group" "caravan_resources"`,
		// Ingress block references the tasks SG.
		`security_groups = [aws_security_group.caravan_tasks.id]`,
		// Engine-port numbers — 5432 (RDS) + 6379 (Cache).
		`5432`,
		`6379`,
		// Subnet group resources.
		`"aws_db_subnet_group" "caravan_resources"`,
		`"aws_elasticache_subnet_group" "caravan_resources"`,
		// RDS and Cache use the new SG + subnet groups; RDS is private.
		`vpc_security_group_ids = [aws_security_group.caravan_resources.id]`,
		`security_group_ids = [aws_security_group.caravan_resources.id]`,
		`db_subnet_group_name`,
		`aws_db_subnet_group.caravan_resources.name`,
		`subnet_group_name`,
		`aws_elasticache_subnet_group.caravan_resources.name`,
		`publicly_accessible`,
		`= false`,
	} {
		if !strings.Contains(main, want) {
			t.Errorf("main.tf missing %q", want)
		}
	}

	// Bug-fix #2: Fargate task-def env for cloud-managed resources must
	// be raw HCL refs, never quoted compose-style `${VAR}` literals.
	for _, want := range []string{
		// QUEUE_URL + S3_BUCKET → bare HCL refs (no surrounding quotes).
		`name = "QUEUE_URL", value = aws_sqs_queue.invoice_queue.url`,
		`name = "S3_BUCKET", value = aws_s3_bucket.invoice_blobs.bucket`,
		// DATABASE_URL → templated HCL string with the endpoint interpolation.
		`name = "DATABASE_URL", value = "postgresql://invoice:invoice@${aws_db_instance.invoice_db.endpoint}/invoice_parse"`,
		// REDIS_URL → templated HCL string with the cluster endpoint.
		`name = "REDIS_URL", value = "redis://${aws_elasticache_cluster.session_cache.cache_nodes[0].address}:6379"`,
	} {
		if !strings.Contains(main, want) {
			t.Errorf("task-def env missing %q", want)
		}
	}

	// Negatives: the hybrid-dev SG/lookups must NOT appear on Fargate,
	// and no quoted compose-passthrough should leak into the task def.
	for _, unwanted := range []string{
		`"aws_security_group" "caravan_dev"`,
		`data "http" "myip"`,
		`name = "DATABASE_URL", value = "${DATABASE_URL}"`,
		`name = "QUEUE_URL", value = "${QUEUE_URL}"`,
		`name = "S3_BUCKET", value = "${S3_BUCKET}"`,
		`name = "REDIS_URL", value = "${REDIS_URL}"`,
		`publicly_accessible = true`,
	} {
		if strings.Contains(main, unwanted) {
			t.Errorf("main.tf should not contain %q", unwanted)
		}
	}
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
	written, err := EmitHCL(rp, outDir, nil)
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
