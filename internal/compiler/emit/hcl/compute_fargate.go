package hcl

import (
	"fmt"
	"strings"

	"github.com/hashicorp/hcl/v2/hclwrite"
	"github.com/zclconf/go-cty/cty"

	"github.com/paulxiep/caravan/internal/compiler"
)

// emitFargateCompute writes the per-target Fargate scaffolding (ECS
// cluster + Cloud Map namespace + execution role) plus one task def +
// ECS service per fargateConsumer. Seam consumers also get a Cloud Map
// service so callers resolve their FQDN via private DNS.
//
// Image source convention: each consumer's container references an ECR
// repo pre-created by the user (per M4-cloud-prereq). For entries the
// repo name is "<app>-<entry-name>"; for seams, the host entry's repo is
// reused (the dual-role binary pattern from code-rag — same image, role
// switched via CARAVAN_RPC_ROLE env var). Image tag is hardcoded
// "latest" for v1.
//
// Outputs added: CLUSTER_NAME, CLOUD_MAP_NAMESPACE_ID, and per-seam
// CARAVAN_RPC_<name>_URL — though peer URLs are typically consumed from
// CARAVAN_RPC_PEERS directly, the outputs are useful for debugging.
func emitFargateCompute(body *hclwrite.Body, rp *compiler.ResolvedPlan, target *compiler.Target, perEntryBindings map[string][]EnvBinding, outputs map[string]string) {
	app := rp.Plan.Name

	emitECSCluster(body, app, target, outputs)
	emitCloudMapNamespace(body, app, target, outputs)
	emitTaskExecutionRole(body, target)

	consumers := fargateConsumers(rp, target)

	// ECR repo lookups — one per distinct image source. For seams that
	// reuse the host entry's image, the lookup is shared.
	emitECRLookups(body, app, target, consumers, rp)

	// Find the host entry once so seam tasks can reference its image.
	hostEntry := pickFargateHostEntry(rp, target)

	for _, c := range consumers {
		emitConsumerLogGroup(body, target, c)
		emitConsumerTaskDef(body, app, target, c, hostEntry, rp, perEntryBindings, outputs)
		if c.NeedsCloudMap {
			emitCloudMapService(body, c)
		}
		emitConsumerService(body, target, c)
	}
}

// emitConsumerLogGroup writes one aws_cloudwatch_log_group per Fargate
// consumer. AmazonECSTaskExecutionRolePolicy grants the execution role
// `logs:CreateLogStream` + `logs:PutLogEvents` but NOT
// `logs:CreateLogGroup`, so we can't lean on awslogs-driver's
// `awslogs-create-group=true` — that would 403 at task-start. Pre-
// creating the group via HCL solves it; the task def references the
// group by name, and the awslogs driver only needs the cheaper
// per-stream permissions.
//
// Retention is left unspecified (logs persist indefinitely). Tighten
// here in v1 if cost becomes a concern.
func emitConsumerLogGroup(body *hclwrite.Body, target *compiler.Target, c fargateConsumer) {
	local := consumerLocal(c)
	groupName := fmt.Sprintf("/ecs/caravan-%s-%s", toDashed(target.Name), toDashed(c.Name))
	b := body.AppendNewBlock("resource", []string{"aws_cloudwatch_log_group", local + "_logs"})
	bb := b.Body()
	bb.SetAttributeValue("name", cty.StringVal(groupName))
	body.AppendNewline()
}

// emitECSCluster writes one aws_ecs_cluster per target. Cluster name
// comes from target.ECSClusterName (defaulted in normalize.go to
// "<app>-<target>").
func emitECSCluster(body *hclwrite.Body, app string, target *compiler.Target, outputs map[string]string) {
	_ = app
	b := body.AppendNewBlock("resource", []string{"aws_ecs_cluster", "caravan"})
	bb := b.Body()
	bb.SetAttributeValue("name", cty.StringVal(target.ECSClusterName))
	bb.SetAttributeValue("tags", cty.MapVal(map[string]cty.Value{
		"Name": cty.StringVal(target.ECSClusterName),
	}))
	body.AppendNewline()
	outputs["CLUSTER_NAME"] = "aws_ecs_cluster.caravan.name"
}

// emitCloudMapNamespace writes the private DNS namespace per target.
// All Fargate services in this target register A records under it so
// CARAVAN_RPC_PEERS URLs like `http://embedder.code-rag.local:8080`
// resolve to task private IPs.
func emitCloudMapNamespace(body *hclwrite.Body, app string, target *compiler.Target, outputs map[string]string) {
	_ = app
	b := body.AppendNewBlock("resource", []string{"aws_service_discovery_private_dns_namespace", "caravan"})
	bb := b.Body()
	bb.SetAttributeValue("name", cty.StringVal(target.CloudMapNamespace))
	bb.SetAttributeValue("description", cty.StringVal("Cloud Map private DNS namespace for caravan target "+target.Name))
	bb.SetAttributeRaw("vpc", rawHCL("aws_vpc.caravan.id"))
	body.AppendNewline()
	outputs["CLOUD_MAP_NAMESPACE_ID"] = "aws_service_discovery_private_dns_namespace.caravan.id"
}

// emitTaskExecutionRole writes the per-target ECS task execution role.
// Every Fargate task uses this role for image pulls (ECR) and CloudWatch
// log writes. It's separate from the per-entry task role (in iam.go)
// which carries the application's IAM grants.
//
// Attaches the AWS-managed AmazonECSTaskExecutionRolePolicy via
// aws_iam_role_policy_attachment — covers ECR + CW logs without
// re-deriving the action list.
func emitTaskExecutionRole(body *hclwrite.Body, target *compiler.Target) {
	roleName := fmt.Sprintf("caravan-%s-execution", toDashed(target.Name))

	role := body.AppendNewBlock("resource", []string{"aws_iam_role", "caravan_execution"})
	rb := role.Body()
	rb.SetAttributeValue("name", cty.StringVal(roleName))
	rb.SetAttributeRaw("assume_role_policy", rawHCL("jsonencode("+fargateAssumeRolePolicy()+")"))
	body.AppendNewline()

	attach := body.AppendNewBlock("resource", []string{"aws_iam_role_policy_attachment", "caravan_execution"})
	ab := attach.Body()
	ab.SetAttributeRaw("role", rawHCL("aws_iam_role.caravan_execution.name"))
	ab.SetAttributeValue("policy_arn", cty.StringVal("arn:aws:iam::aws:policy/service-role/AmazonECSTaskExecutionRolePolicy"))
	body.AppendNewline()
}

// emitECRLookups writes a `data "aws_ecr_repository"` block per distinct
// image source the target's consumers need. Entries get their own repo
// (named <app>-<entry-name>); seams share the host entry's repo.
//
// Caravan does not create ECR repos — those are pre-created by the user
// per M4-cloud-prereq's `docs/aws_onboarding_checklist.md`. The data
// lookup surfaces a clear tofu plan error if the repo is missing.
func emitECRLookups(body *hclwrite.Body, app string, target *compiler.Target, consumers []fargateConsumer, rp *compiler.ResolvedPlan) {
	_ = target
	// Distinct entry names that own an image source. Seams don't own
	// images in M4b (they reuse the host entry's), so only entries
	// generate lookups.
	seen := map[string]bool{}
	emitted := false
	for _, c := range consumers {
		if c.Kind != "entry" {
			continue
		}
		if seen[c.Name] {
			continue
		}
		seen[c.Name] = true
		emitECRRepoLookup(body, app, c.Name)
		emitted = true
	}
	// If there are no container entries but there are container seams,
	// the seams still need an image — fall through to the host entry
	// (pickFargateHostEntry picks the first container entry alphabetically;
	// the caller relies on it).
	if !emitted {
		host := pickFargateHostEntry(rp, target)
		if host != nil && host.Name != "" {
			emitECRRepoLookup(body, app, host.Name)
		}
	}
}

// emitECRRepoLookup writes one data block. Convention: ECR repo name =
// entry name verbatim (dashed). Matches the dev plan's per-image
// onboarding-checklist convention (`code-rag-chat`, `invoice-parse-processing`)
// where the user names their entry to match the ECR repo they pre-create.
func emitECRRepoLookup(body *hclwrite.Body, app, entryName string) {
	_ = app
	repoName := toDashed(entryName)
	local := "caravan_" + terraformLocalName(entryName)
	b := body.AppendNewBlock("data", []string{"aws_ecr_repository", local})
	b.Body().SetAttributeValue("name", cty.StringVal(repoName))
	body.AppendNewline()
}

// pickFargateHostEntry mirrors the compose emitter's pickHostEntry
// (compose.go:348): returns the first alphabetically-sorted entry in
// target.Entries that's marked container. Seam tasks reuse this entry's
// image. Returns nil for empty-Fargate-consumer targets (caught by
// validateFargateTarget).
func pickFargateHostEntry(rp *compiler.ResolvedPlan, target *compiler.Target) *compiler.Entry {
	names := sortedKeysEntries(target)
	for _, name := range names {
		if target.Entries[name] != compiler.EntryContainer {
			continue
		}
		if e := rp.Plan.Entries[name]; e != nil {
			return e
		}
	}
	return nil
}

// fargateSeamHostEntry returns the entry whose image a container-mode
// seam peer reuses on a Fargate target. Mirrors compose.go's
// pickSeamHostEntry resolution order:
//
//  1. Path-match: alphabetically-first container-mode entry whose
//     `path:` equals the seam's path (multi-entry repos like
//     invoice-parse where each entry has its own directory).
//  2. Fallback: alphabetically-first container-mode entry in the target
//     (single-entry repos where seam.Path was defaulted to the entry's
//     path by defaultSeamPaths).
//
// Returns nil when no container entry exists — caller falls back to
// the seam's own name (treated as a missing-binding signal).
func fargateSeamHostEntry(rp *compiler.ResolvedPlan, target *compiler.Target, seam *compiler.Seam) *compiler.Entry {
	if rp == nil || rp.Plan == nil || target == nil || seam == nil {
		return nil
	}
	if seam.Path != "" {
		for _, name := range sortedKeysEntries(target) {
			if target.Entries[name] != compiler.EntryContainer {
				continue
			}
			e := rp.Plan.Entries[name]
			if e != nil && e.Path == seam.Path {
				return e
			}
		}
	}
	return pickFargateHostEntry(rp, target)
}

// emitConsumerTaskDef writes one aws_ecs_task_definition per consumer.
// All consumers share the per-target execution role; per-entry task
// roles attach when iam.go emitted one (i.e. the entry has IAMGrants).
// Seams use the host entry's task role.
func emitConsumerTaskDef(body *hclwrite.Body, app string, target *compiler.Target, c fargateConsumer, hostEntry *compiler.Entry, rp *compiler.ResolvedPlan, perEntryBindings map[string][]EnvBinding, outputs map[string]string) {
	local := consumerLocal(c)
	family := fmt.Sprintf("caravan-%s-%s", toDashed(target.Name), toDashed(c.Name))

	// Pick image source: entries → own repo; seams → host entry's repo.
	imageEntryName := c.Name
	if c.Kind == "seam" {
		if hostEntry != nil && hostEntry.Name != "" {
			imageEntryName = hostEntry.Name
		}
	}
	ecrLocal := "caravan_" + terraformLocalName(imageEntryName)
	imageRef := fmt.Sprintf(`"${data.aws_ecr_repository.%s.repository_url}:latest"`, ecrLocal)

	// Pick task role: prefer per-entry role from iam.go (only exists when
	// IAMGrants for that entry is non-empty). Otherwise fall back to the
	// execution role.
	taskRoleRef := "aws_iam_role.caravan_execution.arn"
	roleEntryName := imageEntryName
	if _, hasGrants := rp.IAMGrants[roleEntryName]; hasGrants {
		taskRoleRef = fmt.Sprintf("aws_iam_role.%s.arn", terraformLocalName(roleEntryName))
	}

	b := body.AppendNewBlock("resource", []string{"aws_ecs_task_definition", local})
	bb := b.Body()
	bb.SetAttributeValue("family", cty.StringVal(family))
	bb.SetAttributeValue("network_mode", cty.StringVal("awsvpc"))
	bb.SetAttributeValue("requires_compatibilities", cty.ListVal([]cty.Value{cty.StringVal("FARGATE")}))
	bb.SetAttributeValue("cpu", cty.StringVal("256"))
	bb.SetAttributeValue("memory", cty.StringVal("512"))
	bb.SetAttributeRaw("execution_role_arn", rawHCL("aws_iam_role.caravan_execution.arn"))
	bb.SetAttributeRaw("task_role_arn", rawHCL(taskRoleRef))

	containerDef := containerDefinition(c, imageRef, rp, app, target, perEntryBindings, outputs)
	bb.SetAttributeRaw("container_definitions", rawHCL("jsonencode("+containerDef+")"))

	body.AppendNewline()
}

// containerDefinition builds the JSON object for the task def's
// containerDefinitions field. Returns an HCL-friendly object literal
// (the wrapping `jsonencode(...)` is added by the caller).
func containerDefinition(c fargateConsumer, imageRef string, rp *compiler.ResolvedPlan, app string, target *compiler.Target, perEntryBindings map[string][]EnvBinding, outputs map[string]string) string {
	containerName := toDashed(c.Name)

	envEntries := containerEnvEntries(c, rp, target, perEntryBindings, outputs)

	var b strings.Builder
	b.WriteString("[{\n")
	b.WriteString(fmt.Sprintf(`    name = %q`, containerName))
	b.WriteString("\n    image = ")
	b.WriteString(imageRef)
	b.WriteString("\n    essential = true")
	b.WriteString("\n    portMappings = [{ containerPort = 8080, hostPort = 8080, protocol = \"tcp\" }]")

	b.WriteString("\n    environment = [\n")
	for i, e := range envEntries {
		b.WriteString(fmt.Sprintf("      { name = %q, value = %s }", e.Name, e.Value))
		if i < len(envEntries)-1 {
			b.WriteString(",")
		}
		b.WriteString("\n")
	}
	b.WriteString("    ]")

	b.WriteString("\n    logConfiguration = {")
	b.WriteString("\n      logDriver = \"awslogs\"")
	b.WriteString("\n      options = {")
	// Log group is pre-created via emitConsumerLogGroup so the
	// execution role only needs CreateLogStream + PutLogEvents (covered
	// by AmazonECSTaskExecutionRolePolicy). Don't set
	// awslogs-create-group=true — that would require CreateLogGroup at
	// task-start, which the managed policy doesn't grant.
	b.WriteString(fmt.Sprintf("\n        \"awslogs-group\" = \"/ecs/caravan-%s-%s\"", toDashed(target.Name), toDashed(c.Name)))
	b.WriteString(fmt.Sprintf("\n        \"awslogs-region\" = %q", target.Region))
	b.WriteString("\n        \"awslogs-stream-prefix\" = \"caravan\"")
	b.WriteString("\n      }")
	b.WriteString("\n    }")
	b.WriteString("\n  }]")
	_ = app
	return b.String()
}

// envEntry is one env-var name+value pair to inject into a container.
// Value is the HCL expression (either a string literal in quotes or a
// reference like aws_sqs_queue.X.url).
type envEntry struct {
	Name  string
	Value string // HCL expression literal — already quoted if a string
}

// containerEnvEntries assembles the env-vars a Fargate container needs:
//
//   - CARAVAN_RPC_PEERS (caravan-internal peer table)
//   - CARAVAN_RPC_ROLE (peers only — caravan-internal dispatch hint)
//   - resource endpoint env vars from rp.ResourceEnvVars (DATABASE_URL,
//     S3_BUCKET, etc.) — rewritten to HCL refs when cloud-managed
//   - declared secrets + env_file passthroughs + base compose
//     `environment:` passthroughs, sourced from perEntryBindings
//     (computed by ComputeBindings, threaded in from the compile flow).
//
// Fargate containers have no shell layer to expand compose-style
// `${VAR}` passthroughs at runtime, so every value the user expects
// to see in env must be baked into the task def at apply time. The
// binding pipeline produces either a literal string (inlined) or a
// `var.X` reference (tofu fills it from TF_VAR_X at apply time).
func containerEnvEntries(c fargateConsumer, rp *compiler.ResolvedPlan, target *compiler.Target, perEntryBindings map[string][]EnvBinding, outputs map[string]string) []envEntry {
	out := []envEntry{}

	if rp.PeersJSON != "" {
		// CARAVAN_RPC_PEERS — emit the JSON as a quoted HCL string
		// literal. The container reads it as a single env-var value;
		// caravan-rpc.json.loads() parses it back into the peer table.
		// (Don't wrap in jsonencode() — that'd double-encode the string.)
		out = append(out, envEntry{
			Name:  "CARAVAN_RPC_PEERS",
			Value: hclLiteralFromJSON(rp.PeersJSON),
		})
	}

	if c.Kind == "seam" {
		out = append(out, envEntry{
			Name:  "CARAVAN_RPC_ROLE",
			Value: fmt.Sprintf("%q", "peer-"+c.Name),
		})
	}

	// Host-entry resolution: entries use their own name; seams resolve
	// to the host entry whose image they reuse (deterministic — path-
	// match first, alphabetical container-entry fallback). Same key drives
	// both resource env vars AND the user-app env bindings.
	envSource := c.Name
	if c.Kind == "seam" {
		if host := fargateSeamHostEntry(rp, target, c.Seam); host != nil {
			envSource = host.Name
		}
	}

	// Resource env vars per host entry.
	if vars, ok := rp.ResourceEnvVars[envSource]; ok {
		for k, v := range vars {
			out = append(out, envEntry{
				Name:  k,
				Value: taskDefEnvValue(k, v, outputs),
			})
		}
	}

	// User-app env bindings (declared secrets + env_file passthroughs +
	// environment block passthroughs). Bindings are computed per host
	// entry; seam consumers reuse the host entry's bindings since the
	// peer process runs the same image with the same env-var expectations.
	for _, b := range perEntryBindings[envSource] {
		if b.Literal != "" {
			out = append(out, envEntry{
				Name:  b.Key,
				Value: fmt.Sprintf("%q", b.Literal),
			})
			continue
		}
		if b.VarName != "" {
			out = append(out, envEntry{
				Name:  b.Key,
				Value: "var." + b.VarName,
			})
		}
	}

	// Stable order.
	sortEnvEntries(out)
	return out
}

// taskDefEnvValue returns the HCL expression to use as a Fargate task-def
// env-var value. For cloud-managed resources (rp.ResourceEnvVars holds a
// compose-style `${VAR}` passthrough), it returns the matching HCL ref
// from the outputs map (which evaluates at tofu apply time). For everything
// else, the value is wrapped as a quoted HCL string literal — same as
// before.
//
// The outputs map is built by the per-resource emitters in resources.go;
// each entry maps an env-var name to an HCL expression. When the
// passthrough value is `${VAR}` and outputs[VAR] exists, the HCL ref
// supersedes the literal text.
func taskDefEnvValue(k, v string, outputs map[string]string) string {
	if isComposePassthrough(v) {
		varName := v[2 : len(v)-1]
		if ref, ok := outputs[varName]; ok && ref != "" {
			return ref
		}
	}
	return fmt.Sprintf("%q", v)
}

// isComposePassthrough reports whether v is a bare compose interpolation
// reference of the form `${NAME}`. We rewrite these on Fargate task defs;
// everything else stays as a literal string.
func isComposePassthrough(v string) bool {
	if len(v) < 4 {
		return false
	}
	if v[0] != '$' || v[1] != '{' || v[len(v)-1] != '}' {
		return false
	}
	for i := 2; i < len(v)-1; i++ {
		c := v[i]
		if c == '$' || c == '{' || c == '}' {
			return false
		}
	}
	return true
}

func sortEnvEntries(s []envEntry) {
	for i := 1; i < len(s); i++ {
		for j := i; j > 0 && s[j-1].Name > s[j].Name; j-- {
			s[j-1], s[j] = s[j], s[j-1]
		}
	}
}

// hclLiteralFromJSON converts a JSON string (rp.PeersJSON) into an HCL
// object literal that jsonencode() can re-marshal. Since PeersJSON is
// already JSON, we can pass it through to jsonencode as a parsed object
// only if we represent it as HCL — but the simpler path is just to
// emit the JSON string verbatim as the literal value (jsonencode of a
// string IS that string). For M4b PoC, emit the JSON as a quoted
// string; the Fargate container's env var gets the raw JSON, which is
// what caravan-rpc expects.
//
// Practical shape: CARAVAN_RPC_PEERS arrives in env as a JSON string,
// not as an HCL-decoded object. So jsonencode(jsondecode("...")) is
// redundant — just pass the string.
func hclLiteralFromJSON(s string) string {
	// Just quote the JSON for HCL. jsonencode of a string is the
	// quoted-as-JSON-string form, which when read back yields the same
	// JSON text. So the container gets exactly s as the env value.
	// HCL escape: backslash and double-quote.
	var b strings.Builder
	b.WriteByte('"')
	for _, r := range s {
		switch r {
		case '\\':
			b.WriteString(`\\`)
		case '"':
			b.WriteString(`\"`)
		case '\n':
			b.WriteString(`\n`)
		case '\r':
			b.WriteString(`\r`)
		case '\t':
			b.WriteString(`\t`)
		default:
			b.WriteRune(r)
		}
	}
	b.WriteByte('"')
	return b.String()
}

// emitCloudMapService writes one aws_service_discovery_service per
// container-mode seam. The ECS service references this via
// service_registries to auto-register/deregister task IPs as the
// service scales.
func emitCloudMapService(body *hclwrite.Body, c fargateConsumer) {
	local := consumerLocal(c)
	b := body.AppendNewBlock("resource", []string{"aws_service_discovery_service", local})
	bb := b.Body()
	bb.SetAttributeValue("name", cty.StringVal(c.ServiceName))

	dns := bb.AppendNewBlock("dns_config", nil)
	db := dns.Body()
	db.SetAttributeRaw("namespace_id", rawHCL("aws_service_discovery_private_dns_namespace.caravan.id"))
	dnsRec := db.AppendNewBlock("dns_records", nil)
	dnsRec.Body().SetAttributeValue("type", cty.StringVal("A"))
	dnsRec.Body().SetAttributeValue("ttl", cty.NumberIntVal(60))
	db.SetAttributeValue("routing_policy", cty.StringVal("MULTIVALUE"))

	hc := bb.AppendNewBlock("health_check_custom_config", nil)
	hc.Body().SetAttributeValue("failure_threshold", cty.NumberIntVal(1))

	body.AppendNewline()
}

// emitConsumerService writes one aws_ecs_service per consumer. Tasks
// land in private subnets with the shared tasks SG. Seam consumers
// attach to their Cloud Map service via service_registries.
func emitConsumerService(body *hclwrite.Body, target *compiler.Target, c fargateConsumer) {
	_ = target
	local := consumerLocal(c)
	b := body.AppendNewBlock("resource", []string{"aws_ecs_service", local})
	bb := b.Body()
	bb.SetAttributeValue("name", cty.StringVal(c.ServiceName))
	bb.SetAttributeRaw("cluster", rawHCL("aws_ecs_cluster.caravan.id"))
	bb.SetAttributeRaw("task_definition", rawHCL(fmt.Sprintf("aws_ecs_task_definition.%s.arn", local)))
	bb.SetAttributeValue("launch_type", cty.StringVal("FARGATE"))
	bb.SetAttributeValue("desired_count", cty.NumberIntVal(1))

	netcfg := bb.AppendNewBlock("network_configuration", nil)
	nb := netcfg.Body()
	nb.SetAttributeRaw("subnets", rawHCL("[aws_subnet.caravan_private_a.id, aws_subnet.caravan_private_b.id]"))
	nb.SetAttributeRaw("security_groups", rawHCL("[aws_security_group.caravan_tasks.id]"))
	nb.SetAttributeValue("assign_public_ip", cty.BoolVal(false))

	if c.NeedsCloudMap {
		sr := bb.AppendNewBlock("service_registries", nil)
		sr.Body().SetAttributeRaw("registry_arn", rawHCL(fmt.Sprintf("aws_service_discovery_service.%s.arn", local)))
	}

	body.AppendNewline()
}

// consumerLocal returns the HCL local name shared across a consumer's
// task def + ECS service + Cloud Map service. Same as terraformLocalName
// of the consumer's plan-IR name.
func consumerLocal(c fargateConsumer) string {
	return terraformLocalName(c.Name)
}
