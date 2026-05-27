package hcl

import (
	"fmt"

	"github.com/hashicorp/hcl/v2/hclwrite"
	"github.com/zclconf/go-cty/cty"

	"github.com/paulxiep/caravan/internal/compiler"
)

// emitVPC writes the VPC + 2-AZ subnets + IGW + single NAT + route
// tables + Fargate tasks security group into body. Called once per
// Fargate target (before compute emission). Subnet AZs use the AWS
// data source `aws_availability_zones` so the emitter doesn't hardcode
// region-specific AZ names.
//
// Layout (single NAT v1):
//
//	VPC (10.0.0.0/16 by default)
//	├── public-a  (10.0.0.0/24)  ── IGW → internet
//	│   └── NAT gateway
//	├── public-b  (10.0.1.0/24)  ── IGW → internet
//	├── private-a (10.0.10.0/24) ── NAT → internet (egress only)
//	└── private-b (10.0.11.0/24) ── NAT → internet (egress only)
//
// Fargate tasks land in private subnets; NAT lets them reach ECR + the
// public internet (Gemini API, model downloads) without being directly
// reachable from outside.
//
// Outputs added: VPC_ID, PRIVATE_SUBNETS (comma-joined IDs),
// PUBLIC_SUBNETS, TASKS_SG_ID.
func emitVPC(body *hclwrite.Body, app, target string, vpc *compiler.VPCConfig, outputs map[string]string) {
	if vpc == nil {
		// Defensive: normalize.go's defaultFargateTargetFields should
		// have populated this. If it didn't, the AWS-default CIDR keeps
		// the emit valid.
		vpc = &compiler.VPCConfig{CIDR: compiler.DefaultVPCCIDR, NAT: "single"}
	}

	emitAZsDataSource(body)
	emitVPCResource(body, app, target, vpc)
	emitInternetGateway(body, app, target)
	emitSubnets(body, app, target)
	emitNATGateway(body, app, target)
	emitRouteTables(body, app, target)
	emitTasksSecurityGroup(body, app, target)

	outputs["VPC_ID"] = "aws_vpc.caravan.id"
	outputs["PRIVATE_SUBNETS"] = `join(",", [aws_subnet.caravan_private_a.id, aws_subnet.caravan_private_b.id])`
	outputs["PUBLIC_SUBNETS"] = `join(",", [aws_subnet.caravan_public_a.id, aws_subnet.caravan_public_b.id])`
	outputs["TASKS_SG_ID"] = "aws_security_group.caravan_tasks.id"
}

// emitAZsDataSource writes a `data "aws_availability_zones"` block so
// subnet emission can pin AZ names without hardcoding region-specific
// values (us-east-1a, ap-southeast-1a, etc.).
func emitAZsDataSource(body *hclwrite.Body) {
	d := body.AppendNewBlock("data", []string{"aws_availability_zones", "available"})
	d.Body().SetAttributeValue("state", cty.StringVal("available"))
	body.AppendNewline()
}

func emitVPCResource(body *hclwrite.Body, app, target string, vpc *compiler.VPCConfig) {
	b := body.AppendNewBlock("resource", []string{"aws_vpc", "caravan"})
	bb := b.Body()
	bb.SetAttributeValue("cidr_block", cty.StringVal(vpc.CIDR))
	bb.SetAttributeValue("enable_dns_hostnames", cty.BoolVal(true))
	bb.SetAttributeValue("enable_dns_support", cty.BoolVal(true))
	bb.SetAttributeValue("tags", cty.MapVal(map[string]cty.Value{
		"Name": cty.StringVal(fmt.Sprintf("caravan-%s-%s", toDashed(app), toDashed(target))),
	}))
	body.AppendNewline()
}

func emitInternetGateway(body *hclwrite.Body, app, target string) {
	b := body.AppendNewBlock("resource", []string{"aws_internet_gateway", "caravan"})
	bb := b.Body()
	bb.SetAttributeRaw("vpc_id", rawHCL("aws_vpc.caravan.id"))
	bb.SetAttributeValue("tags", cty.MapVal(map[string]cty.Value{
		"Name": cty.StringVal(fmt.Sprintf("caravan-%s-%s-igw", toDashed(app), toDashed(target))),
	}))
	body.AppendNewline()
}

// emitSubnets writes 2 public + 2 private subnets, one per AZ. Public
// subnets get auto-assigned public IPs (needed for NAT gateway). Private
// subnets don't.
func emitSubnets(body *hclwrite.Body, app, target string) {
	specs := []struct {
		name   string
		cidr   string
		az     string
		public bool
	}{
		{"public_a", "10.0.0.0/24", "data.aws_availability_zones.available.names[0]", true},
		{"public_b", "10.0.1.0/24", "data.aws_availability_zones.available.names[1]", true},
		{"private_a", "10.0.10.0/24", "data.aws_availability_zones.available.names[0]", false},
		{"private_b", "10.0.11.0/24", "data.aws_availability_zones.available.names[1]", false},
	}
	for _, s := range specs {
		local := "caravan_" + s.name
		b := body.AppendNewBlock("resource", []string{"aws_subnet", local})
		bb := b.Body()
		bb.SetAttributeRaw("vpc_id", rawHCL("aws_vpc.caravan.id"))
		bb.SetAttributeValue("cidr_block", cty.StringVal(s.cidr))
		bb.SetAttributeRaw("availability_zone", rawHCL(s.az))
		bb.SetAttributeValue("map_public_ip_on_launch", cty.BoolVal(s.public))
		bb.SetAttributeValue("tags", cty.MapVal(map[string]cty.Value{
			"Name": cty.StringVal(fmt.Sprintf("caravan-%s-%s-%s", toDashed(app), toDashed(target), toDashed(s.name))),
		}))
		body.AppendNewline()
	}
}

// emitNATGateway writes a single NAT gateway in public_a's subnet, with
// its EIP. M4b ships v1 with single NAT; HA NAT (one per AZ) is the
// v1.1 flag `nat: ha`.
func emitNATGateway(body *hclwrite.Body, app, target string) {
	eip := body.AppendNewBlock("resource", []string{"aws_eip", "caravan_nat"})
	eb := eip.Body()
	eb.SetAttributeValue("domain", cty.StringVal("vpc"))
	eb.SetAttributeValue("tags", cty.MapVal(map[string]cty.Value{
		"Name": cty.StringVal(fmt.Sprintf("caravan-%s-%s-nat", toDashed(app), toDashed(target))),
	}))
	body.AppendNewline()

	nat := body.AppendNewBlock("resource", []string{"aws_nat_gateway", "caravan"})
	nb := nat.Body()
	nb.SetAttributeRaw("allocation_id", rawHCL("aws_eip.caravan_nat.id"))
	nb.SetAttributeRaw("subnet_id", rawHCL("aws_subnet.caravan_public_a.id"))
	nb.SetAttributeValue("tags", cty.MapVal(map[string]cty.Value{
		"Name": cty.StringVal(fmt.Sprintf("caravan-%s-%s-nat", toDashed(app), toDashed(target))),
	}))
	// Explicit dependency so tofu plans the IGW before the NAT.
	nb.SetAttributeRaw("depends_on", rawHCL("[aws_internet_gateway.caravan]"))
	body.AppendNewline()
}

// emitRouteTables writes two route tables — one public (default route
// via IGW), one private (default route via NAT) — and associates each
// pair of subnets to its table.
func emitRouteTables(body *hclwrite.Body, app, target string) {
	// Public route table.
	pubRT := body.AppendNewBlock("resource", []string{"aws_route_table", "caravan_public"})
	prtb := pubRT.Body()
	prtb.SetAttributeRaw("vpc_id", rawHCL("aws_vpc.caravan.id"))
	pubRoute := prtb.AppendNewBlock("route", nil)
	pubRoute.Body().SetAttributeValue("cidr_block", cty.StringVal("0.0.0.0/0"))
	pubRoute.Body().SetAttributeRaw("gateway_id", rawHCL("aws_internet_gateway.caravan.id"))
	prtb.SetAttributeValue("tags", cty.MapVal(map[string]cty.Value{
		"Name": cty.StringVal(fmt.Sprintf("caravan-%s-%s-public-rt", toDashed(app), toDashed(target))),
	}))
	body.AppendNewline()

	// Private route table.
	privRT := body.AppendNewBlock("resource", []string{"aws_route_table", "caravan_private"})
	prtb2 := privRT.Body()
	prtb2.SetAttributeRaw("vpc_id", rawHCL("aws_vpc.caravan.id"))
	privRoute := prtb2.AppendNewBlock("route", nil)
	privRoute.Body().SetAttributeValue("cidr_block", cty.StringVal("0.0.0.0/0"))
	privRoute.Body().SetAttributeRaw("nat_gateway_id", rawHCL("aws_nat_gateway.caravan.id"))
	prtb2.SetAttributeValue("tags", cty.MapVal(map[string]cty.Value{
		"Name": cty.StringVal(fmt.Sprintf("caravan-%s-%s-private-rt", toDashed(app), toDashed(target))),
	}))
	body.AppendNewline()

	// Associations.
	assocs := []struct {
		local, subnet, rt string
	}{
		{"caravan_public_a", "aws_subnet.caravan_public_a.id", "aws_route_table.caravan_public.id"},
		{"caravan_public_b", "aws_subnet.caravan_public_b.id", "aws_route_table.caravan_public.id"},
		{"caravan_private_a", "aws_subnet.caravan_private_a.id", "aws_route_table.caravan_private.id"},
		{"caravan_private_b", "aws_subnet.caravan_private_b.id", "aws_route_table.caravan_private.id"},
	}
	for _, a := range assocs {
		b := body.AppendNewBlock("resource", []string{"aws_route_table_association", a.local})
		bb := b.Body()
		bb.SetAttributeRaw("subnet_id", rawHCL(a.subnet))
		bb.SetAttributeRaw("route_table_id", rawHCL(a.rt))
		body.AppendNewline()
	}
}

// emitFargateResourcesSupport writes the Fargate-side scaffolding for
// VPC-anchored cloud-managed resources (RDS, ElastiCache). Called from
// renderMain after emitVPC when the target declares a db.sql or cache
// resource. Three emits:
//
//   - aws_security_group.caravan_resources — ingress from the tasks SG
//     on whichever ports are needed (5432 for RDS, 6379 for cache).
//   - aws_db_subnet_group.caravan_resources — spans the two private
//     subnets, consumed by RDS (db_subnet_group_name).
//   - aws_elasticache_subnet_group.caravan_resources — same, consumed by
//     ElastiCache (subnet_group_name).
//
// Resources reach this SG / subnet group via the resourceEmitOpts flow in
// hcl.go::renderMain (which threads "caravan_resources" into emitDBSQL +
// emitCache when target.Runtime == Fargate).
func emitFargateResourcesSupport(body *hclwrite.Body, app, target string, hasDB, hasCache bool) {
	if !hasDB && !hasCache {
		return
	}
	emitFargateResourcesSG(body, app, target, hasDB, hasCache)
	if hasDB {
		emitDBSubnetGroup(body, app, target)
	}
	if hasCache {
		emitCacheSubnetGroup(body, app, target)
	}
}

// emitFargateResourcesSG writes the shared SG for VPC-anchored cloud
// resources on a Fargate target. Ingress is restricted to the tasks SG
// (intra-VPC) on the per-engine port(s); egress is left open for AWS-API
// callbacks. Replaces the hybrid-dev laptop-IP SG (`caravan_dev`) on
// Fargate paths — tasks reach resources from inside the VPC, no laptop
// ingress needed.
func emitFargateResourcesSG(body *hclwrite.Body, app, target string, hasDB, hasCache bool) {
	awsName := fmt.Sprintf("caravan-%s-%s-resources", toDashed(app), toDashed(target))
	b := body.AppendNewBlock("resource", []string{"aws_security_group", "caravan_resources"})
	bb := b.Body()
	bb.SetAttributeValue("name", cty.StringVal(awsName))
	bb.SetAttributeValue("description", cty.StringVal("Fargate-resource ingress from caravan_tasks on RDS/ElastiCache ports"))
	bb.SetAttributeRaw("vpc_id", rawHCL("aws_vpc.caravan.id"))

	if hasDB {
		ig := bb.AppendNewBlock("ingress", nil).Body()
		ig.SetAttributeValue("description", cty.StringVal("postgres from tasks"))
		ig.SetAttributeValue("from_port", cty.NumberIntVal(5432))
		ig.SetAttributeValue("to_port", cty.NumberIntVal(5432))
		ig.SetAttributeValue("protocol", cty.StringVal("tcp"))
		ig.SetAttributeRaw("security_groups", rawHCL("[aws_security_group.caravan_tasks.id]"))
	}
	if hasCache {
		ig := bb.AppendNewBlock("ingress", nil).Body()
		ig.SetAttributeValue("description", cty.StringVal("redis from tasks"))
		ig.SetAttributeValue("from_port", cty.NumberIntVal(6379))
		ig.SetAttributeValue("to_port", cty.NumberIntVal(6379))
		ig.SetAttributeValue("protocol", cty.StringVal("tcp"))
		ig.SetAttributeRaw("security_groups", rawHCL("[aws_security_group.caravan_tasks.id]"))
	}

	eg := bb.AppendNewBlock("egress", nil).Body()
	eg.SetAttributeValue("from_port", cty.NumberIntVal(0))
	eg.SetAttributeValue("to_port", cty.NumberIntVal(0))
	eg.SetAttributeValue("protocol", cty.StringVal("-1"))
	eg.SetAttributeValue("cidr_blocks", cty.ListVal([]cty.Value{cty.StringVal("0.0.0.0/0")}))

	bb.SetAttributeValue("tags", cty.MapVal(map[string]cty.Value{
		"Name": cty.StringVal(awsName),
	}))
	body.AppendNewline()
}

// emitDBSubnetGroup writes aws_db_subnet_group.caravan_resources spanning
// the VPC's two private subnets. RDS uses this to place its instances
// inside the VPC (publicly_accessible=false on Fargate).
func emitDBSubnetGroup(body *hclwrite.Body, app, target string) {
	awsName := fmt.Sprintf("caravan-%s-%s-db", toDashed(app), toDashed(target))
	b := body.AppendNewBlock("resource", []string{"aws_db_subnet_group", "caravan_resources"})
	bb := b.Body()
	bb.SetAttributeValue("name", cty.StringVal(awsName))
	bb.SetAttributeValue("description", cty.StringVal("Caravan Fargate RDS subnet group (private subnets)"))
	bb.SetAttributeRaw("subnet_ids", rawHCL("[aws_subnet.caravan_private_a.id, aws_subnet.caravan_private_b.id]"))
	bb.SetAttributeValue("tags", cty.MapVal(map[string]cty.Value{
		"Name": cty.StringVal(awsName),
	}))
	body.AppendNewline()
}

// emitCacheSubnetGroup writes aws_elasticache_subnet_group.caravan_resources
// spanning the VPC's two private subnets. ElastiCache uses this via the
// subnet_group_name attribute on a Fargate target.
func emitCacheSubnetGroup(body *hclwrite.Body, app, target string) {
	awsName := fmt.Sprintf("caravan-%s-%s-cache", toDashed(app), toDashed(target))
	b := body.AppendNewBlock("resource", []string{"aws_elasticache_subnet_group", "caravan_resources"})
	bb := b.Body()
	bb.SetAttributeValue("name", cty.StringVal(awsName))
	bb.SetAttributeValue("description", cty.StringVal("Caravan Fargate ElastiCache subnet group (private subnets)"))
	bb.SetAttributeRaw("subnet_ids", rawHCL("[aws_subnet.caravan_private_a.id, aws_subnet.caravan_private_b.id]"))
	body.AppendNewline()
}

// emitTasksSecurityGroup writes the SG shared by all Fargate tasks for
// this target. Ingress: from self (intra-SG) on port 8080 — caravan-rpc
// HTTP dispatch between tasks via Cloud Map. Egress: anywhere — needed
// for ECR pulls + outbound API calls (Gemini, model downloads).
func emitTasksSecurityGroup(body *hclwrite.Body, app, target string) {
	b := body.AppendNewBlock("resource", []string{"aws_security_group", "caravan_tasks"})
	bb := b.Body()
	bb.SetAttributeValue("name", cty.StringVal(fmt.Sprintf("caravan-%s-%s-tasks", toDashed(app), toDashed(target))))
	bb.SetAttributeValue("description", cty.StringVal("Fargate task-to-task RPC on 8080 + outbound for ECR/APIs"))
	bb.SetAttributeRaw("vpc_id", rawHCL("aws_vpc.caravan.id"))

	ingress := bb.AppendNewBlock("ingress", nil)
	ib := ingress.Body()
	ib.SetAttributeValue("description", cty.StringVal("caravan-rpc intra-SG"))
	ib.SetAttributeValue("from_port", cty.NumberIntVal(8080))
	ib.SetAttributeValue("to_port", cty.NumberIntVal(8080))
	ib.SetAttributeValue("protocol", cty.StringVal("tcp"))
	ib.SetAttributeRaw("self", rawHCL("true"))

	egress := bb.AppendNewBlock("egress", nil)
	eb := egress.Body()
	eb.SetAttributeValue("from_port", cty.NumberIntVal(0))
	eb.SetAttributeValue("to_port", cty.NumberIntVal(0))
	eb.SetAttributeValue("protocol", cty.StringVal("-1"))
	eb.SetAttributeValue("cidr_blocks", cty.ListVal([]cty.Value{cty.StringVal("0.0.0.0/0")}))

	bb.SetAttributeValue("tags", cty.MapVal(map[string]cty.Value{
		"Name": cty.StringVal(fmt.Sprintf("caravan-%s-%s-tasks", toDashed(app), toDashed(target))),
	}))
	body.AppendNewline()
}
