package analyzer_test

import (
	"testing"

	"axiaops.io/shared/analyzer"
	"axiaops.io/shared/model"
)

// ── ResourceType classification ───────────────────────────────────────────────

func TestResourceType(t *testing.T) {
	cases := []struct {
		service, metric, want string
	}{
		// AmazonEC2 spans several kinds, disambiguated by usage metric.
		{"AmazonEC2", "VolumeState", "volume"},
		{"AmazonEC2", "SourceVolumeExists", "snapshot"},
		{"AmazonEC2", "DaysStopped", "stopped_instance"},
		{"AmazonEC2", "DaysSinceCreation", "ami"},
		{"AmazonEC2", "CPUUtilization", "instance"},
		// AmazonVPC: EIP vs NAT gateway.
		{"AmazonVPC", "NetworkInterfaceAttachment", "eip"},
		{"AmazonVPC", "BytesOutToDestination", "nat_gateway"},
		// AmazonRDS: snapshot vs instance.
		{"AmazonRDS", "SourceDBExists", "db_snapshot"},
		{"AmazonRDS", "DatabaseConnections", "db_instance"},
		// One-kind services.
		{"AmazonCloudWatch", "RetentionDays", "log_group"},
		{"AmazonECR", "StaleImageCount", "ecr_image"},
		{"AmazonCloudFront", "Requests", "distribution"},
		{"AWSSecretsManager", "DaysSinceAccess", "secret"},
		{"AmazonS3", "AllRequests", "bucket"},
		{"AmazonKinesis", "IncomingRecords", "stream"},
		{"AWSLambda", "Invocations", "function"},
		{"AmazonElasticLoadBalancing", "RequestCount", "load_balancer"},
		{"AmazonElastiCache", "CurrConnections", "node"},
		{"AmazonES", "SearchRate", "domain"},
		{"AmazonRedshift", "DatabaseConnections", "cluster"},
		{"AmazonSageMaker", "Invocations", "endpoint"},
		{"AmazonDynamoDB", "ConsumedReadCapacityUnits", "table"},
		{"AmazonEKS", "cluster_node_count", "cluster"},
		// Unrecognised pairs degrade to "" (filtered out of the trend view).
		{"AmazonEC2", "SomeFutureMetric", ""},
		{"AmazonUnknown", "CPUUtilization", ""},
		{"", "", ""},
	}
	for _, c := range cases {
		if got := analyzer.ResourceType(c.service, c.metric); got != c.want {
			t.Errorf("ResourceType(%q, %q) = %q, want %q", c.service, c.metric, got, c.want)
		}
	}
}

// Same-metric / different-service pairs must NOT collide (the classifier keys on
// both fields). DatabaseConnections → db_instance for RDS but cluster for
// Redshift; Invocations → function for Lambda but endpoint for SageMaker.
func TestResourceType_SameMetricDistinctServices(t *testing.T) {
	if rds, rs := analyzer.ResourceType("AmazonRDS", "DatabaseConnections"), analyzer.ResourceType("AmazonRedshift", "DatabaseConnections"); rds == rs {
		t.Errorf("RDS and Redshift collided on DatabaseConnections: both %q", rds)
	}
	if lam, sm := analyzer.ResourceType("AWSLambda", "Invocations"), analyzer.ResourceType("AmazonSageMaker", "Invocations"); lam == sm {
		t.Errorf("Lambda and SageMaker collided on Invocations: both %q", lam)
	}
}

// Detect must stamp the resource_type on the zombies it produces, deriving it
// from each service's own usage metric (not a single hard-coded service).
func TestDetect_SetsResourceType(t *testing.T) {
	cases := []struct {
		service, resourceID, metric string
		avg                         float64
		want                        string
	}{
		{"AmazonEC2", "i-idle-01", "CPUUtilization", 1.0, "instance"},
		{"AmazonRDS", "db-idle-01", "DatabaseConnections", 0, "db_instance"},
		{"AWSLambda", "fn-idle-01", "Invocations", 0, "function"},
	}
	for _, c := range cases {
		costs := []model.CostRecord{costRecord(c.service, c.resourceID, 50.0)}
		usage := []analyzer.UsageRecord{usageRecord(c.resourceID, c.metric, c.avg)}
		zombies := analyzer.Detect(costs, usage, "")
		if len(zombies) != 1 {
			t.Fatalf("%s: expected 1 zombie, got %d", c.service, len(zombies))
		}
		if zombies[0].ResourceType != c.want {
			t.Errorf("%s: expected resource_type=%q, got %q", c.service, c.want, zombies[0].ResourceType)
		}
	}
}

// AnnotateAll must carry resource_type onto resource_records. The cost-record
// branch is the tricky one: an API-only zombie (e.g. an unattached EBS volume)
// can have a Cost Explorer line item but no CloudWatch usage, so the metric is
// empty — the resource_type must come from the matching zombie, not a re-derive.
func TestAnnotateAll_ResourceTypeFromZombie(t *testing.T) {
	costs := []model.CostRecord{
		costRecord("AmazonEC2", "vol-orphan-01", 8.0), // EBS volume — has a cost line, no usage
		costRecord("AmazonEC2", "i-idle-01", 50.0),    // idle instance — has CloudWatch usage
	}
	usage := []analyzer.UsageRecord{
		usageRecord("i-idle-01", "CPUUtilization", 1.0),
	}
	zombies := []model.ZombieResource{
		// As produced by the EBS discoverer (+ ingestion backfill): classified, no usage record.
		{Service: "AmazonEC2", ResourceType: "volume", ResourceID: "vol-orphan-01", UsageMetric: "VolumeState", MonthlyCost: 8.0, Currency: "USD"},
		{Service: "AmazonEC2", ResourceType: "instance", ResourceID: "i-idle-01", UsageMetric: "CPUUtilization", MonthlyCost: 50.0, Currency: "USD"},
	}

	records := analyzer.AnnotateAll(costs, usage, zombies)

	byID := make(map[string]model.ResourceRecord, len(records))
	for _, r := range records {
		byID[r.ResourceID] = r
	}
	if got := byID["vol-orphan-01"].ResourceType; got != "volume" {
		t.Errorf("EBS volume (cost line, no usage): expected resource_type=volume, got %q", got)
	}
	if got := byID["i-idle-01"].ResourceType; got != "instance" {
		t.Errorf("idle instance: expected resource_type=instance, got %q", got)
	}
}

// ── SummarizeByServiceResourceType ────────────────────────────────────────────

func TestSummarizeByServiceResourceType(t *testing.T) {
	zombies := []model.ZombieResource{
		{Service: "AmazonEC2", ResourceType: "instance", MonthlyCost: 10, Currency: "USD"},
		{Service: "AmazonEC2", ResourceType: "instance", MonthlyCost: 5, Currency: "USD"},
		{Service: "AmazonEC2", ResourceType: "volume", MonthlyCost: 2, Currency: "USD"},
		{Service: "AmazonVPC", ResourceType: "eip", MonthlyCost: 3.6, Currency: "USD"},
	}

	got := analyzer.SummarizeByServiceResourceType(zombies)

	// Sorted by service then resource_type: EC2/instance, EC2/volume, VPC/eip.
	want := []analyzer.ServiceResourceBreakdown{
		{Service: "AmazonEC2", ResourceType: "instance", Zombies: 2, Savings: 15, Currency: "USD"},
		{Service: "AmazonEC2", ResourceType: "volume", Zombies: 1, Savings: 2, Currency: "USD"},
		{Service: "AmazonVPC", ResourceType: "eip", Zombies: 1, Savings: 3.6, Currency: "USD"},
	}
	if len(got) != len(want) {
		t.Fatalf("expected %d buckets, got %d: %+v", len(want), len(got), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("bucket %d = %+v, want %+v", i, got[i], want[i])
		}
	}
}

func TestSummarizeByServiceResourceType_Empty(t *testing.T) {
	if got := analyzer.SummarizeByServiceResourceType(nil); len(got) != 0 {
		t.Errorf("expected no buckets for empty input, got %+v", got)
	}
}
