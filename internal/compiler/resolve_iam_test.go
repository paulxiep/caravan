package compiler

import (
	"reflect"
	"testing"
)

// TestResolveIAMGrants_ProducerOnly covers an entry that lists a bucket
// in `uses:` but has no queue trigger. Should emit S3 producer perms.
func TestResolveIAMGrants_ProducerOnly(t *testing.T) {
	plan := &Plan{
		Entries: map[string]*Entry{
			"writer": {Name: "writer", Uses: []string{"blobs"}},
		},
	}
	resolved := map[string]*ResolvedResource{
		"blobs": {Name: "blobs", Type: ResourceBucket, Composition: CompositionCloudManaged},
	}
	got := resolveIAMGrants(plan, resolved, nil)
	want := map[string][]IAMStatement{
		"writer": {{
			ResourceRef:  "blobs",
			ResourceKind: ResourceBucket,
			Actions: []string{
				"s3:DeleteObject",
				"s3:GetObject",
				"s3:ListBucket",
				"s3:PutObject",
			},
		}},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %#v\nwant %#v", got, want)
	}
}

// TestResolveIAMGrants_ConsumerOnly covers an entry that triggers on a
// queue (consumer perms) without listing it in uses (no producer perms).
func TestResolveIAMGrants_ConsumerOnly(t *testing.T) {
	plan := &Plan{
		Entries: map[string]*Entry{
			"reader": {
				Name: "reader",
				Triggers: []Trigger{
					{Kind: TriggerQueue, Queue: &QueueTrigger{From: "jobs"}},
				},
			},
		},
	}
	resolved := map[string]*ResolvedResource{
		"jobs": {Name: "jobs", Type: ResourceQueue, Composition: CompositionCloudManaged},
	}
	got := resolveIAMGrants(plan, resolved, nil)
	want := map[string][]IAMStatement{
		"reader": {{
			ResourceRef:  "jobs",
			ResourceKind: ResourceQueue,
			Actions: []string{
				"sqs:ChangeMessageVisibility",
				"sqs:DeleteMessage",
				"sqs:GetQueueAttributes",
				"sqs:GetQueueUrl",
				"sqs:ReceiveMessage",
			},
		}},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %#v\nwant %#v", got, want)
	}
}

// TestResolveIAMGrants_ProducerAndConsumer covers an entry like
// invoice-parse `output` that triggers on a queue AND uses it as a
// producer (re-publishes). The union of consumer + producer actions
// should land in one statement.
func TestResolveIAMGrants_ProducerAndConsumer(t *testing.T) {
	plan := &Plan{
		Entries: map[string]*Entry{
			"output": {
				Name: "output",
				Uses: []string{"jobs"},
				Triggers: []Trigger{
					{Kind: TriggerQueue, Queue: &QueueTrigger{From: "jobs"}},
				},
			},
		},
	}
	resolved := map[string]*ResolvedResource{
		"jobs": {Name: "jobs", Type: ResourceQueue, Composition: CompositionCloudManaged},
	}
	got := resolveIAMGrants(plan, resolved, nil)
	want := map[string][]IAMStatement{
		"output": {{
			ResourceRef:  "jobs",
			ResourceKind: ResourceQueue,
			Actions: []string{
				"sqs:ChangeMessageVisibility",
				"sqs:DeleteMessage",
				"sqs:GetQueueAttributes",
				"sqs:GetQueueUrl",
				"sqs:ReceiveMessage",
				"sqs:SendMessage",
			},
		}},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %#v\nwant %#v", got, want)
	}
}

// TestResolveIAMGrants_OssLocalSkipped confirms oss-local resources
// produce no IAM grants — local containers don't need AWS perms.
func TestResolveIAMGrants_OssLocalSkipped(t *testing.T) {
	plan := &Plan{
		Entries: map[string]*Entry{
			"writer": {Name: "writer", Uses: []string{"blobs"}},
		},
	}
	resolved := map[string]*ResolvedResource{
		"blobs": {Name: "blobs", Type: ResourceBucket, Composition: CompositionOSSLocal},
	}
	got := resolveIAMGrants(plan, resolved, nil)
	if got != nil {
		t.Errorf("expected nil; got %#v", got)
	}
}

// TestResolveIAMGrants_MixedResources covers an entry that uses several
// resources with different cloud-managed/oss-local compositions. Only
// the cloud-managed ones get grants; the result must be sorted by
// resource ref.
func TestResolveIAMGrants_MixedResources(t *testing.T) {
	plan := &Plan{
		Entries: map[string]*Entry{
			"processing": {
				Name: "processing",
				Uses: []string{"invoice_blobs", "invoice_db", "invoice_queue"},
				Triggers: []Trigger{
					{Kind: TriggerQueue, Queue: &QueueTrigger{From: "invoice_queue"}},
				},
			},
		},
	}
	resolved := map[string]*ResolvedResource{
		"invoice_blobs": {Name: "invoice_blobs", Type: ResourceBucket, Composition: CompositionCloudManaged},
		"invoice_db":    {Name: "invoice_db", Type: ResourceDBSQL, Composition: CompositionCloudManaged},
		"invoice_queue": {Name: "invoice_queue", Type: ResourceQueue, Composition: CompositionCloudManaged},
	}
	got := resolveIAMGrants(plan, resolved, nil)
	if len(got["processing"]) != 2 {
		t.Fatalf("expected 2 statements (db.sql has no IAM); got %d: %#v", len(got["processing"]), got["processing"])
	}
	if got["processing"][0].ResourceRef != "invoice_blobs" {
		t.Errorf("first statement ref: got %q want invoice_blobs", got["processing"][0].ResourceRef)
	}
	if got["processing"][1].ResourceRef != "invoice_queue" {
		t.Errorf("second statement ref: got %q want invoice_queue", got["processing"][1].ResourceRef)
	}
}

// TestResolveIAMGrants_NoEntries returns nil cleanly.
func TestResolveIAMGrants_NoEntries(t *testing.T) {
	if got := resolveIAMGrants(&Plan{}, nil, nil); got != nil {
		t.Errorf("expected nil; got %#v", got)
	}
}
