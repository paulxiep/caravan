package compiler

import "sort"

// resolveIAMGrants computes the per-entry IAM grant set for M4-cloud's
// HCL emit. Each entry's grants are the union of:
//
//   - "producer" perms for every cloud-managed resource named in
//     entry.Uses (e.g. s3:PutObject on a bucket, sqs:SendMessage on a
//     queue).
//   - "consumer" perms for every cloud-managed resource named in a
//     queue/stream trigger on the entry (e.g. sqs:ReceiveMessage,
//     sqs:DeleteMessage).
//   - "lambda invoke" perms (M7) for every seam dispatched as lambda
//     whose impl lives inside this entry's path. The caller (entry)
//     gets lambda:InvokeFunctionUrl on the seam's Lambda function ARN.
//
// All surfaces can fire on the same entry. The union is deduped + sorted
// per resource.
//
// Returns nil when no entry has any grant. The HCL emit stage uses
// nil-ness to decide whether to emit iam.tf at all.
func resolveIAMGrants(plan *Plan, resolved map[string]*ResolvedResource, target *Target) map[string][]IAMStatement {
	if len(plan.Entries) == 0 {
		return nil
	}
	out := map[string][]IAMStatement{}
	for _, entryName := range sortedKeys(plan.Entries) {
		e := plan.Entries[entryName]
		if e == nil {
			continue
		}
		statements := buildEntryIAM(e, resolved)
		// M7: append a lambda:InvokeFunctionUrl statement per Lambda
		// seam whose path matches this entry. One statement per seam,
		// scoped to the function's ARN (HCL emit resolves ARN via
		// aws_lambda_function.<seam>.arn).
		statements = append(statements, lambdaInvokeStatements(e, plan, target)...)
		if len(statements) > 0 {
			out[entryName] = statements
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// lambdaInvokeStatements returns one IAMStatement per Lambda seam this
// entry calls. "Calls" resolves in two steps:
//
//  1. Path-match: the seam's `path:` equals the entry's `path:`. Used
//     when caravan.yaml declares both explicitly (invoice-parse).
//  2. Fallback: the entry is a container-mode entry in the target and
//     no Lambda seam has an explicit path that excludes it. Used by
//     code-rag, which declares no per-seam path (all seam impls live
//     in the single repo-root entry).
//
// Statements are sorted by seam name for byte-stable HCL output.
func lambdaInvokeStatements(e *Entry, plan *Plan, target *Target) []IAMStatement {
	if target == nil || len(target.Seams) == 0 || e == nil {
		return nil
	}
	// Only container-mode entries can invoke Lambda peers — they're the
	// only deploy units with a task role to attach the grant to.
	if target.Entries[e.Name] != EntryContainer {
		return nil
	}
	seamNames := make([]string, 0)
	for name, mode := range target.Seams {
		if mode != SeamLambda {
			continue
		}
		s := plan.Seams[name]
		if s == nil {
			continue
		}
		// Path-match if both are set; fallback to "any container entry"
		// when the seam declares no path.
		matches := false
		switch {
		case s.Path != "" && e.Path != "":
			matches = s.Path == e.Path
		default:
			matches = true
		}
		if !matches {
			continue
		}
		seamNames = append(seamNames, name)
	}
	sort.Strings(seamNames)
	out := make([]IAMStatement, 0, len(seamNames))
	for _, n := range seamNames {
		out = append(out, IAMStatement{
			ResourceRef:  n,
			ResourceKind: ResourceKindLambdaCall,
			Actions:      []string{"lambda:InvokeFunctionUrl"},
		})
	}
	return out
}

// buildEntryIAM walks one entry's uses + triggers and returns the
// merged + sorted IAM statement list. Statements are merged per
// resource: an entry that both produces and consumes a queue gets one
// statement with the union of actions.
func buildEntryIAM(e *Entry, resolved map[string]*ResolvedResource) []IAMStatement {
	if e == nil {
		return nil
	}
	// resourceName → action set (deduped via map).
	actionsByRes := map[string]map[string]struct{}{}
	kindByRes := map[string]ResourceKind{}

	// Producer perms from uses:. Only cloud-managed resources get
	// IAM grants (oss-local containers don't need them).
	for _, ref := range e.Uses {
		rr := resolved[ref]
		if rr == nil || rr.Composition != CompositionCloudManaged {
			continue
		}
		for _, action := range producerActionsFor(rr.Type) {
			ensureActionSet(actionsByRes, ref)[action] = struct{}{}
		}
		kindByRes[ref] = rr.Type
	}

	// Consumer perms from queue/stream triggers.
	for _, t := range e.Triggers {
		var ref string
		switch t.Kind {
		case TriggerQueue:
			if t.Queue != nil {
				ref = t.Queue.From
			}
		case TriggerStream:
			if t.Stream != nil {
				ref = t.Stream.From
			}
		}
		if ref == "" {
			continue
		}
		rr := resolved[ref]
		if rr == nil || rr.Composition != CompositionCloudManaged {
			continue
		}
		for _, action := range consumerActionsFor(rr.Type) {
			ensureActionSet(actionsByRes, ref)[action] = struct{}{}
		}
		kindByRes[ref] = rr.Type
	}

	if len(actionsByRes) == 0 {
		return nil
	}

	refs := make([]string, 0, len(actionsByRes))
	for r := range actionsByRes {
		refs = append(refs, r)
	}
	sort.Strings(refs)

	out := make([]IAMStatement, 0, len(refs))
	for _, ref := range refs {
		actSet := actionsByRes[ref]
		actions := make([]string, 0, len(actSet))
		for a := range actSet {
			actions = append(actions, a)
		}
		sort.Strings(actions)
		out = append(out, IAMStatement{
			ResourceRef:  ref,
			ResourceKind: kindByRes[ref],
			Actions:      actions,
		})
	}
	return out
}

// ensureActionSet returns the action-set for a resource, creating it
// on first touch.
func ensureActionSet(m map[string]map[string]struct{}, ref string) map[string]struct{} {
	if s, ok := m[ref]; ok {
		return s
	}
	s := map[string]struct{}{}
	m[ref] = s
	return s
}

// producerActionsFor returns the IAM actions an entry that *uses* (but
// doesn't trigger-consume) a resource of this type needs. Caravan-owned
// mapping; PoC-grade — wide GetObject/PutObject permissions per bucket,
// SendMessage per queue. Tighten in v1.
//
// Resources without IAM actions (RDS, ElastiCache — security-group
// gated) return nil and produce no statement.
func producerActionsFor(k ResourceKind) []string {
	switch k {
	case ResourceBucket:
		return []string{
			"s3:DeleteObject",
			"s3:GetObject",
			"s3:ListBucket",
			"s3:PutObject",
		}
	case ResourceQueue:
		return []string{
			"sqs:GetQueueAttributes",
			"sqs:GetQueueUrl",
			"sqs:SendMessage",
		}
	case ResourceSearch:
		return []string{
			"es:ESHttpGet",
			"es:ESHttpPost",
			"es:ESHttpPut",
		}
	case ResourceStream:
		return []string{
			"kinesis:PutRecord",
			"kinesis:PutRecords",
		}
	}
	return nil
}

// consumerActionsFor returns the IAM actions an entry that has a
// queue/stream trigger on this resource needs. Receiving + deleting
// from SQS; reading from a Kinesis stream.
func consumerActionsFor(k ResourceKind) []string {
	switch k {
	case ResourceQueue:
		return []string{
			"sqs:ChangeMessageVisibility",
			"sqs:DeleteMessage",
			"sqs:GetQueueAttributes",
			"sqs:GetQueueUrl",
			"sqs:ReceiveMessage",
		}
	case ResourceStream:
		return []string{
			"kinesis:DescribeStream",
			"kinesis:GetRecords",
			"kinesis:GetShardIterator",
			"kinesis:ListShards",
		}
	}
	return nil
}
