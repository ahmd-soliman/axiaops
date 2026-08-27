package analyzer_test

import (
	"testing"
	"time"

	"axiaops.io/shared/analyzer"
	"axiaops.io/shared/model"
)

// ── helpers ──────────────────────────────────────────────────────────────────

func costRecord(service, resourceID string, amount float64) model.CostRecord {
	return model.CostRecord{
		Provider:    "aws",
		AccountID:   "000000000000",
		Service:     service,
		Region:      "eu-central-1",
		ResourceID:  resourceID,
		Amount:      amount,
		Currency:    "USD",
		PeriodStart: time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC),
		PeriodEnd:   time.Date(2026, 3, 31, 0, 0, 0, 0, time.UTC),
		Tags:        map[string]string{"team": "platform", "env": "staging"},
		FetchedAt:   time.Now(),
	}
}

func usageRecord(resourceID, metric string, avg float64) analyzer.UsageRecord {
	return analyzer.UsageRecord{
		ResourceID: resourceID,
		Metric:     metric,
		Unit:       "Count",
		Avg:        avg,
		PeriodDays: 30,
	}
}

// ── Detect ────────────────────────────────────────────────────────────────────

func TestDetect_FlagsZeroUsage(t *testing.T) {
	costs := []model.CostRecord{
		costRecord("AmazonRDS", "db-stag-01", 210.00),
	}
	usage := []analyzer.UsageRecord{
		usageRecord("db-stag-01", "DatabaseConnections", 0),
	}

	zombies := analyzer.Detect(costs, usage, "")

	if len(zombies) != 1 {
		t.Fatalf("expected 1 zombie, got %d", len(zombies))
	}
	z := zombies[0]
	if z.ResourceID != "db-stag-01" {
		t.Errorf("wrong resource ID: %s", z.ResourceID)
	}
	if z.MonthlyCost != 210.00 {
		t.Errorf("wrong cost: %f", z.MonthlyCost)
	}
	if z.Owner != "platform" {
		t.Errorf("expected owner platform, got %s", z.Owner)
	}
}

func TestDetect_SkipsActiveResource(t *testing.T) {
	costs := []model.CostRecord{
		costRecord("AmazonEC2", "i-active", 189.60),
	}
	usage := []analyzer.UsageRecord{
		// CPU well above 5% threshold
		usageRecord("i-active", "CPUUtilization", 62.4),
	}

	zombies := analyzer.Detect(costs, usage, "")

	if len(zombies) != 0 {
		t.Errorf("expected 0 zombies for active resource, got %d", len(zombies))
	}
}

func TestDetect_FlagsEC2BelowThreshold(t *testing.T) {
	costs := []model.CostRecord{
		costRecord("AmazonEC2", "i-idle", 189.60),
	}
	usage := []analyzer.UsageRecord{
		// 1.1% CPU — below the 5% threshold
		usageRecord("i-idle", "CPUUtilization", 1.1),
	}

	zombies := analyzer.Detect(costs, usage, "")

	if len(zombies) != 1 {
		t.Fatalf("expected 1 zombie for idle EC2, got %d", len(zombies))
	}
}

func TestDetect_SkipsUnknownService(t *testing.T) {
	// AmazonMQ has no detection rule — should be skipped
	costs := []model.CostRecord{
		costRecord("AmazonMQ", "broker-id-001", 134.10),
	}
	usage := []analyzer.UsageRecord{
		usageRecord("broker-id-001", "QueueDepth", 0),
	}

	zombies := analyzer.Detect(costs, usage, "")

	if len(zombies) != 0 {
		t.Errorf("expected 0 zombies for service with no rule, got %d", len(zombies))
	}
}

func TestDetect_SkipsResourceWithNoUsageData(t *testing.T) {
	costs := []model.CostRecord{
		costRecord("AmazonRDS", "db-no-usage", 210.00),
	}
	// No matching usage record
	usage := []analyzer.UsageRecord{}

	zombies := analyzer.Detect(costs, usage, "")

	if len(zombies) != 0 {
		t.Errorf("expected 0 zombies when usage data is missing, got %d", len(zombies))
	}
}

func TestDetect_OwnerFallback(t *testing.T) {
	c := costRecord("AmazonRDS", "db-no-team", 100.00)
	c.Tags = map[string]string{} // no team tag

	zombies := analyzer.Detect(
		[]model.CostRecord{c},
		[]analyzer.UsageRecord{usageRecord("db-no-team", "DatabaseConnections", 0)},
		"",
	)

	if len(zombies) != 1 {
		t.Fatalf("expected 1 zombie, got %d", len(zombies))
	}
	if zombies[0].Owner != "unknown" {
		t.Errorf("expected owner 'unknown', got %s", zombies[0].Owner)
	}
}

func TestDetect_MultipleZombies(t *testing.T) {
	costs := []model.CostRecord{
		costRecord("AmazonRDS", "db-01", 210.00),
		costRecord("AWSLambda", "fn-old", 12.80),
		costRecord("AmazonEC2", "i-active", 1243.87),
	}
	usage := []analyzer.UsageRecord{
		usageRecord("db-01", "DatabaseConnections", 0),
		usageRecord("fn-old", "Invocations", 0),
		usageRecord("i-active", "CPUUtilization", 62.4), // active — not a zombie
	}

	zombies := analyzer.Detect(costs, usage, "")

	if len(zombies) != 2 {
		t.Fatalf("expected 2 zombies, got %d", len(zombies))
	}
}

// ── Tier 2 CloudWatch rules ───────────────────────────────────────────────────

func TestDetect_ElastiCache_ZeroConnections_FlagsZombie(t *testing.T) {
	costs := []model.CostRecord{costRecord("AmazonElastiCache", "cache-prod-01", 55.20)}
	usage := []analyzer.UsageRecord{usageRecord("cache-prod-01", "CurrConnections", 0)}
	zombies := analyzer.Detect(costs, usage, "")
	if len(zombies) != 1 {
		t.Fatalf("expected 1 zombie, got %d", len(zombies))
	}
}

func TestDetect_ElastiCache_ActiveConnections_NoZombie(t *testing.T) {
	costs := []model.CostRecord{costRecord("AmazonElastiCache", "cache-prod-01", 55.20)}
	usage := []analyzer.UsageRecord{usageRecord("cache-prod-01", "CurrConnections", 42)}
	zombies := analyzer.Detect(costs, usage, "")
	if len(zombies) != 0 {
		t.Errorf("expected 0 zombies for active ElastiCache cluster, got %d", len(zombies))
	}
}

func TestDetect_OpenSearch_ZeroSearchRate_FlagsZombie(t *testing.T) {
	costs := []model.CostRecord{costRecord("AmazonES", "search-logs-prod", 180.00)}
	usage := []analyzer.UsageRecord{usageRecord("search-logs-prod", "SearchRate", 0)}
	zombies := analyzer.Detect(costs, usage, "")
	if len(zombies) != 1 {
		t.Fatalf("expected 1 zombie, got %d", len(zombies))
	}
}

func TestDetect_OpenSearch_ActiveSearches_NoZombie(t *testing.T) {
	costs := []model.CostRecord{costRecord("AmazonES", "search-logs-prod", 180.00)}
	usage := []analyzer.UsageRecord{usageRecord("search-logs-prod", "SearchRate", 12.5)}
	zombies := analyzer.Detect(costs, usage, "")
	if len(zombies) != 0 {
		t.Errorf("expected 0 zombies for active OpenSearch domain, got %d", len(zombies))
	}
}

func TestDetect_Redshift_ZeroConnections_FlagsZombie(t *testing.T) {
	costs := []model.CostRecord{costRecord("AmazonRedshift", "dwh-analytics", 340.00)}
	usage := []analyzer.UsageRecord{usageRecord("dwh-analytics", "DatabaseConnections", 0)}
	zombies := analyzer.Detect(costs, usage, "")
	if len(zombies) != 1 {
		t.Fatalf("expected 1 zombie, got %d", len(zombies))
	}
}

func TestDetect_Redshift_ActiveConnections_NoZombie(t *testing.T) {
	costs := []model.CostRecord{costRecord("AmazonRedshift", "dwh-analytics", 340.00)}
	usage := []analyzer.UsageRecord{usageRecord("dwh-analytics", "DatabaseConnections", 7)}
	zombies := analyzer.Detect(costs, usage, "")
	if len(zombies) != 0 {
		t.Errorf("expected 0 zombies for active Redshift cluster, got %d", len(zombies))
	}
}

func TestDetect_SageMaker_ZeroInvocations_FlagsZombie(t *testing.T) {
	costs := []model.CostRecord{costRecord("AmazonSageMaker", "fraud-detection-v2", 210.00)}
	usage := []analyzer.UsageRecord{usageRecord("fraud-detection-v2", "Invocations", 0)}
	zombies := analyzer.Detect(costs, usage, "")
	if len(zombies) != 1 {
		t.Fatalf("expected 1 zombie, got %d", len(zombies))
	}
}

func TestDetect_SageMaker_ActiveInvocations_NoZombie(t *testing.T) {
	costs := []model.CostRecord{costRecord("AmazonSageMaker", "fraud-detection-v2", 210.00)}
	usage := []analyzer.UsageRecord{usageRecord("fraud-detection-v2", "Invocations", 884)}
	zombies := analyzer.Detect(costs, usage, "")
	if len(zombies) != 0 {
		t.Errorf("expected 0 zombies for active SageMaker endpoint, got %d", len(zombies))
	}
}

func TestDetect_DynamoDB_ZeroReads_FlagsZombie(t *testing.T) {
	costs := []model.CostRecord{costRecord("AmazonDynamoDB", "legacy-sessions", 28.40)}
	usage := []analyzer.UsageRecord{usageRecord("legacy-sessions", "ConsumedReadCapacityUnits", 0)}
	zombies := analyzer.Detect(costs, usage, "")
	if len(zombies) != 1 {
		t.Fatalf("expected 1 zombie, got %d", len(zombies))
	}
}

func TestDetect_DynamoDB_ActiveReads_NoZombie(t *testing.T) {
	costs := []model.CostRecord{costRecord("AmazonDynamoDB", "legacy-sessions", 28.40)}
	usage := []analyzer.UsageRecord{usageRecord("legacy-sessions", "ConsumedReadCapacityUnits", 1500)}
	zombies := analyzer.Detect(costs, usage, "")
	if len(zombies) != 0 {
		t.Errorf("expected 0 zombies for active DynamoDB table, got %d", len(zombies))
	}
}

func TestDetect_ECS_LowCPU_FlagsZombie(t *testing.T) {
	costs := []model.CostRecord{costRecord("AmazonECS", "api-service", 120.00)}
	usage := []analyzer.UsageRecord{usageRecord("api-service", "CPUUtilization", 0.5)}
	zombies := analyzer.Detect(costs, usage, "")
	if len(zombies) != 1 {
		t.Fatalf("expected 1 zombie for low CPU ECS service, got %d", len(zombies))
	}
	if zombies[0].ResourceType != "ecs_service" {
		t.Errorf("expected ecs_service slug, got %s", zombies[0].ResourceType)
	}
}

func TestDetect_DocDB_ZeroConnections_FlagsZombie(t *testing.T) {
	costs := []model.CostRecord{costRecord("AmazonDocDB", "orders-docdb", 250.00)}
	usage := []analyzer.UsageRecord{usageRecord("orders-docdb", "DatabaseConnections", 0)}
	zombies := analyzer.Detect(costs, usage, "")
	if len(zombies) != 1 {
		t.Fatalf("expected 1 zombie for zero connection DocumentDB, got %d", len(zombies))
	}
	if zombies[0].ResourceType != "docdb_cluster" {
		t.Errorf("expected docdb_cluster slug, got %s", zombies[0].ResourceType)
	}
}

func TestDetect_MSK_ZeroMessages_FlagsZombie(t *testing.T) {
	costs := []model.CostRecord{costRecord("AmazonMSK", "events-kafka", 400.00)}
	usage := []analyzer.UsageRecord{usageRecord("events-kafka", "MessagesInPerSec", 0)}
	zombies := analyzer.Detect(costs, usage, "")
	if len(zombies) != 1 {
		t.Fatalf("expected 1 zombie for zero throughput MSK cluster, got %d", len(zombies))
	}
	if zombies[0].ResourceType != "msk_cluster" {
		t.Errorf("expected msk_cluster slug, got %s", zombies[0].ResourceType)
	}
}

// NOTE: CloudFront, Kinesis, and S3 detection tests are in the ingestion
// service (discover_test.go) since they use Tier 1-style direct detection
// instead of flowing through Detect().

func TestDetect_SkipsServicesWithoutRules(t *testing.T) {
	// CloudFront, Kinesis, S3 no longer have rules in serviceRules — they use
	// direct detection. Verify Detect() silently skips them.
	costs := []model.CostRecord{costRecord("AmazonCloudFront", "E1A2B3C4D5E6F7", 15.00)}
	usage := []analyzer.UsageRecord{usageRecord("E1A2B3C4D5E6F7", "Requests", 0)}
	zombies := analyzer.Detect(costs, usage, "")
	if len(zombies) != 0 {
		t.Errorf("expected 0 zombies for service without rule, got %d", len(zombies))
	}
}

// ── UsageRecord.Validate ──────────────────────────────────────────────────────

func TestUsageRecord_Validate_HappyPath(t *testing.T) {
	u := analyzer.UsageRecord{
		ResourceID: "i-1",
		Metric:     "CPUUtilization",
		Unit:       "Percent",
		Avg:        4.2,
		PeriodDays: 30,
	}
	if err := u.Validate(); err != nil {
		t.Fatalf("expected nil, got %v", err)
	}
}

func TestUsageRecord_Validate_RejectsBadFields(t *testing.T) {
	cases := []struct {
		name  string
		mut   func(*analyzer.UsageRecord)
		field string
	}{
		{"empty resource_id", func(u *analyzer.UsageRecord) { u.ResourceID = "" }, "resource_id"},
		{"empty metric", func(u *analyzer.UsageRecord) { u.Metric = "" }, "metric"},
		{"negative avg", func(u *analyzer.UsageRecord) { u.Avg = -1 }, "avg"},
		{"negative period_days", func(u *analyzer.UsageRecord) { u.PeriodDays = -1 }, "period_days"},
	}

	base := analyzer.UsageRecord{
		ResourceID: "i-1", Metric: "CPUUtilization", Avg: 1.0, PeriodDays: 7,
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			u := base
			tc.mut(&u)
			err := u.Validate()
			if err == nil {
				t.Fatalf("expected error, got nil")
			}
			ve, ok := err.(*model.ValidationError)
			if !ok {
				t.Fatalf("expected *model.ValidationError, got %T (%v)", err, err)
			}
			if ve.Field != tc.field {
				t.Errorf("Field = %q, want %q", ve.Field, tc.field)
			}
		})
	}
}

// ── Summarize ─────────────────────────────────────────────────────────────────

func TestSummarize_Empty(t *testing.T) {
	s := analyzer.Summarize(nil)

	if s.TotalZombies != 0 {
		t.Errorf("expected 0 zombies, got %d", s.TotalZombies)
	}
	if s.PotentialMonthlySave != 0 {
		t.Errorf("expected 0 savings, got %f", s.PotentialMonthlySave)
	}
}

func TestSummarize_AggregatesSavings(t *testing.T) {
	zombies := []model.ZombieResource{
		{Service: "AmazonRDS", MonthlyCost: 210.00, Currency: "USD"},
		{Service: "AmazonEC2", MonthlyCost: 189.60, Currency: "USD"},
		{Service: "AmazonEC2", MonthlyCost: 100.00, Currency: "USD"},
	}

	s := analyzer.Summarize(zombies)

	if s.TotalZombies != 3 {
		t.Errorf("expected 3 zombies, got %d", s.TotalZombies)
	}
	if s.PotentialMonthlySave != 499.60 {
		t.Errorf("expected savings 499.60, got %f", s.PotentialMonthlySave)
	}
	if s.Currency != "USD" {
		t.Errorf("expected USD, got %s", s.Currency)
	}
	if s.ByService["AmazonEC2"].Zombies != 2 {
		t.Errorf("expected 2 EC2 zombies, got %d", s.ByService["AmazonEC2"].Zombies)
	}
	if s.ByService["AmazonEC2"].Savings != 289.60 {
		t.Errorf("expected EC2 savings 289.60, got %f", s.ByService["AmazonEC2"].Savings)
	}
}

// TestSummarize_ByServiceRoundedToCents guards against ByService entries
// leaking raw binary floating-point residue into the API response — e.g.
// summing 0.1 + 0.2 gives 0.30000000000000004, not 0.3. PotentialMonthlySave
// was already rounded; ByService's per-service Savings was not, so a client
// summing MonthlyCost values that don't round cleanly in binary could see a
// long trailing-digit float like 0.0000013639591634273529 instead of a clean
// two-decimal amount.
func TestSummarize_ByServiceRoundedToCents(t *testing.T) {
	zombies := []model.ZombieResource{
		{Service: "AmazonCloudWatch", MonthlyCost: 0.1, Currency: "USD"},
		{Service: "AmazonCloudWatch", MonthlyCost: 0.2, Currency: "USD"},
	}

	s := analyzer.Summarize(zombies)

	if got := s.ByService["AmazonCloudWatch"].Savings; got != 0.3 {
		t.Errorf("expected AmazonCloudWatch savings rounded to 0.3, got %v", got)
	}
}

// ── SummarizeByAccount ─────────────────────────────────────────────────────────

func TestSummarizeByAccount(t *testing.T) {
	zombies := []model.ZombieResource{
		// Account A (AWS 111111111111): RDS dominates → top_service AmazonRDS.
		{InternalAccountID: "acc-a", AccountID: "111111111111", Service: "AmazonRDS", MonthlyCost: 210.00, Currency: "USD"},
		{InternalAccountID: "acc-a", AccountID: "111111111111", Service: "AmazonEC2", MonthlyCost: 50.00, Currency: "USD"},
		{InternalAccountID: "acc-a", AccountID: "111111111111", Service: "AmazonEC2", MonthlyCost: 40.00, Currency: "USD"},
		// Account B (AWS 222222222222): EC2 only.
		{InternalAccountID: "acc-b", AccountID: "222222222222", Service: "AmazonEC2", MonthlyCost: 100.00, Currency: "USD"},
	}

	got := analyzer.SummarizeByAccount(zombies)

	if got.Currency != "USD" {
		t.Errorf("expected currency USD, got %q", got.Currency)
	}
	if len(got.Accounts) != 2 {
		t.Fatalf("expected 2 accounts, got %d", len(got.Accounts))
	}

	byID := make(map[string]analyzer.AccountSummary, len(got.Accounts))
	for _, a := range got.Accounts {
		byID[a.InternalAccountID] = a
	}

	a, ok := byID["acc-a"]
	if !ok {
		t.Fatalf("expected acc-a in result")
	}
	if a.AccountID != "111111111111" {
		t.Errorf("acc-a: expected account_id 111111111111, got %q", a.AccountID)
	}
	if a.TotalZombies != 3 {
		t.Errorf("acc-a: expected 3 zombies, got %d", a.TotalZombies)
	}
	if a.PotentialMonthly != 300.00 {
		t.Errorf("acc-a: expected savings 300.00, got %f", a.PotentialMonthly)
	}
	// EC2 sums to 90, RDS to 210 → top_service must be AmazonRDS, not the
	// service with the most resources.
	if a.TopService != "AmazonRDS" {
		t.Errorf("acc-a: expected top_service AmazonRDS, got %q", a.TopService)
	}

	b, ok := byID["acc-b"]
	if !ok {
		t.Fatalf("expected acc-b in result")
	}
	if b.TotalZombies != 1 {
		t.Errorf("acc-b: expected 1 zombie, got %d", b.TotalZombies)
	}
	if b.PotentialMonthly != 100.00 {
		t.Errorf("acc-b: expected savings 100.00, got %f", b.PotentialMonthly)
	}
	if b.TopService != "AmazonEC2" {
		t.Errorf("acc-b: expected top_service AmazonEC2, got %q", b.TopService)
	}
}

func TestSummarizeByAccount_Empty(t *testing.T) {
	got := analyzer.SummarizeByAccount(nil)

	if got.Accounts == nil {
		t.Fatal("expected non-nil Accounts slice so it serialises as [], not null")
	}
	if len(got.Accounts) != 0 {
		t.Errorf("expected 0 accounts, got %d", len(got.Accounts))
	}
}
