package aws

// White-box tests for discover.go helper functions.
// The API-calling functions (DiscoverUnattachedEBSVolumes, etc.) create EC2
// clients from aws.Config internally, so they are integration-tested against
// a real AWS account. The helpers tested here contain the core business logic
// (time parsing, region validation) and are the highest-value unit tests.

import (
	"testing"
	"time"

	"axiaops.io/shared/model"
)

// ── parseStopTime ─────────────────────────────────────────────────────────────

func TestParseStopTime_ValidUserInitiated(t *testing.T) {
	reason := "User initiated (2024-01-15 14:30:45 GMT)"
	got, ok := parseStopTime(reason)
	if !ok {
		t.Fatal("expected ok=true, got false")
	}
	want := time.Date(2024, 1, 15, 14, 30, 45, 0, time.UTC)
	if !got.Equal(want) {
		t.Errorf("expected %v, got %v", want, got)
	}
}

func TestParseStopTime_NoTimestamp(t *testing.T) {
	// Reason string without a timestamp — e.g. for terminated instances.
	_, ok := parseStopTime("Client.UserInitiatedShutdown")
	if ok {
		t.Error("expected ok=false for reason with no timestamp")
	}
}

func TestParseStopTime_EmptyString(t *testing.T) {
	_, ok := parseStopTime("")
	if ok {
		t.Error("expected ok=false for empty reason")
	}
}

func TestParseStopTime_MalformedDate(t *testing.T) {
	// Parentheses present but the date is garbage.
	_, ok := parseStopTime("User initiated (not-a-date GMT)")
	if ok {
		t.Error("expected ok=false for malformed date")
	}
}

func TestParseStopTime_TimezonePreserved(t *testing.T) {
	// AWS uses "GMT" which time.Parse maps to UTC.
	reason := "User initiated (2023-06-01 00:00:00 GMT)"
	got, ok := parseStopTime(reason)
	if !ok {
		t.Fatal("expected ok=true")
	}
	if got.UTC().Year() != 2023 || got.UTC().Month() != 6 || got.UTC().Day() != 1 {
		t.Errorf("wrong date: %v", got)
	}
}

// ── isAWSRegion ───────────────────────────────────────────────────────────────

func TestIsAWSRegion_ValidRegions(t *testing.T) {
	cases := []string{
		"eu-central-1",
		"us-east-1",
		"us-west-2",
		"ap-southeast-1",
		"ca-central-1",
		"sa-east-1",
	}
	for _, r := range cases {
		if !isAWSRegion(r) {
			t.Errorf("expected %q to be a valid AWS region", r)
		}
	}
}

func TestIsAWSRegion_PseudoValues(t *testing.T) {
	// Cost Explorer pseudo-values must not be treated as real regions.
	cases := []string{
		"global",
		"NoRegion",
		"",
		"us",
	}
	for _, r := range cases {
		if isAWSRegion(r) {
			t.Errorf("expected %q NOT to be a valid AWS region", r)
		}
	}
}

// ── arnSuffix ─────────────────────────────────────────────────────────────────

func TestArnSuffix_ApplicationLoadBalancer(t *testing.T) {
	// arnSuffix returns from the second-to-last slash.
	// For .../loadbalancer/app/my-lb/abc123def456 that is "my-lb/abc123def456".
	arn := "arn:aws:elasticloadbalancing:eu-central-1:123456789012:loadbalancer/app/my-lb/abc123def456"
	got := arnSuffix(arn)
	want := "my-lb/abc123def456"
	if got != want {
		t.Errorf("expected %q, got %q", want, got)
	}
}

func TestArnSuffix_NetworkLoadBalancer(t *testing.T) {
	arn := "arn:aws:elasticloadbalancing:us-east-1:123456789012:loadbalancer/net/my-nlb/abc123"
	got := arnSuffix(arn)
	want := "my-nlb/abc123"
	if got != want {
		t.Errorf("expected %q, got %q", want, got)
	}
}

func TestArnSuffix_FallbackOnPlainID(t *testing.T) {
	// If the input has no slash at all, arnSuffix should return the input unchanged.
	id := "vol-0123456789abcdef0"
	got := arnSuffix(id)
	if got != id {
		t.Errorf("expected %q unchanged, got %q", id, got)
	}
}

// ── stoppedInstanceThreshold / oldAMIThreshold sanity checks ─────────────────

func TestStoppedInstanceThreshold_Is30Days(t *testing.T) {
	want := 30 * 24 * time.Hour
	if stoppedInstanceThreshold != want {
		t.Errorf("stoppedInstanceThreshold = %v; want %v", stoppedInstanceThreshold, want)
	}
}

func TestOldAMIThreshold_Is90Days(t *testing.T) {
	want := 90 * 24 * time.Hour
	if oldAMIThreshold != want {
		t.Errorf("oldAMIThreshold = %v; want %v", oldAMIThreshold, want)
	}
}

// ── Threshold sanity checks ─────────────────────────────────────────────────
//
// Pricing-rate checks now live in services/shared/pricing/pricing_test.go —
// only the time-based detection thresholds stay here.

func TestRDSSnapshotAgeThreshold_Is30Days(t *testing.T) {
	want := 30 * 24 * time.Hour
	if rdsSnapshotAgeThreshold != want {
		t.Errorf("rdsSnapshotAgeThreshold = %v; want %v", rdsSnapshotAgeThreshold, want)
	}
}

func TestECRStaleImageThreshold_Is90Days(t *testing.T) {
	want := 90 * 24 * time.Hour
	if ecrStaleImageThreshold != want {
		t.Errorf("ecrStaleImageThreshold = %v; want %v", ecrStaleImageThreshold, want)
	}
}

func TestUnusedSecretThreshold_Is90Days(t *testing.T) {
	want := 90 * 24 * time.Hour
	if unusedSecretThreshold != want {
		t.Errorf("unusedSecretThreshold = %v; want %v", unusedSecretThreshold, want)
	}
}

// ── classifyECRImages ────────────────────────────────────────────────────────

func TestClassifyECRImages_AllFresh_NoStale(t *testing.T) {
	now := time.Now()
	images := []ecrImageInfo{
		{sizeBytes: 100_000_000, pushedAt: now.Add(-24 * time.Hour), tagged: true},
		{sizeBytes: 200_000_000, pushedAt: now.Add(-48 * time.Hour), tagged: true},
	}
	count, size := classifyECRImages(images, 90*24*time.Hour, now)
	if count != 0 || size != 0 {
		t.Errorf("expected 0 stale, got count=%d size=%d", count, size)
	}
}

func TestClassifyECRImages_UntaggedFlagged(t *testing.T) {
	now := time.Now()
	images := []ecrImageInfo{
		{sizeBytes: 100_000_000, pushedAt: now.Add(-24 * time.Hour), tagged: true}, // latest, keep
		{sizeBytes: 50_000_000, pushedAt: now.Add(-48 * time.Hour), tagged: false}, // untagged, stale
	}
	count, size := classifyECRImages(images, 90*24*time.Hour, now)
	if count != 1 {
		t.Errorf("expected 1 stale untagged image, got %d", count)
	}
	if size != 50_000_000 {
		t.Errorf("expected 50MB stale, got %d", size)
	}
}

func TestClassifyECRImages_OldTaggedFlagged(t *testing.T) {
	now := time.Now()
	images := []ecrImageInfo{
		{sizeBytes: 100_000_000, pushedAt: now.Add(-1 * time.Hour), tagged: true},        // latest, keep
		{sizeBytes: 200_000_000, pushedAt: now.Add(-120 * 24 * time.Hour), tagged: true}, // >90 days, stale
	}
	count, _ := classifyECRImages(images, 90*24*time.Hour, now)
	if count != 1 {
		t.Errorf("expected 1 stale old tagged image, got %d", count)
	}
}

func TestClassifyECRImages_LatestProtected(t *testing.T) {
	now := time.Now()
	// Only one image — even if old + untagged, it's the latest and should be kept.
	images := []ecrImageInfo{
		{sizeBytes: 100_000_000, pushedAt: now.Add(-200 * 24 * time.Hour), tagged: false},
	}
	count, _ := classifyECRImages(images, 90*24*time.Hour, now)
	if count != 0 {
		t.Errorf("expected 0 stale (latest must be protected), got %d", count)
	}
}

func TestClassifyECRImages_Empty(t *testing.T) {
	count, size := classifyECRImages(nil, 90*24*time.Hour, time.Now())
	if count != 0 || size != 0 {
		t.Errorf("expected 0/0 for empty, got %d/%d", count, size)
	}
}

// ── isSecretUnused ───────────────────────────────────────────────────────────

func TestIsSecretUnused_RecentAccess_NotUnused(t *testing.T) {
	now := time.Now()
	accessed := now.Add(-30 * 24 * time.Hour) // 30 days ago
	days := isSecretUnused(&accessed, nil, 90*24*time.Hour, now)
	if days != -1 {
		t.Errorf("expected -1 (not unused), got %d", days)
	}
}

func TestIsSecretUnused_OldAccess_Unused(t *testing.T) {
	now := time.Now()
	accessed := now.Add(-120 * 24 * time.Hour) // 120 days ago
	days := isSecretUnused(&accessed, nil, 90*24*time.Hour, now)
	if days != 120 {
		t.Errorf("expected 120, got %d", days)
	}
}

func TestIsSecretUnused_NeverAccessed_UsesCreatedDate(t *testing.T) {
	now := time.Now()
	created := now.Add(-180 * 24 * time.Hour) // created 180 days ago, never accessed
	days := isSecretUnused(nil, &created, 90*24*time.Hour, now)
	if days != 180 {
		t.Errorf("expected 180, got %d", days)
	}
}

func TestIsSecretUnused_NeverAccessed_RecentlyCreated(t *testing.T) {
	now := time.Now()
	created := now.Add(-10 * 24 * time.Hour) // 10 days old
	days := isSecretUnused(nil, &created, 90*24*time.Hour, now)
	if days != -1 {
		t.Errorf("expected -1 (recently created), got %d", days)
	}
}

func TestIsSecretUnused_NoDates_Skip(t *testing.T) {
	days := isSecretUnused(nil, nil, 90*24*time.Hour, time.Now())
	if days != -1 {
		t.Errorf("expected -1 (no dates), got %d", days)
	}
}

// ── isRDSSnapshotOrphaned ────────────────────────────────────────────────────

func TestIsRDSSnapshotOrphaned_SourceExists_NotOrphaned(t *testing.T) {
	days := isRDSSnapshotOrphaned(true, 60*24*time.Hour, 30*24*time.Hour)
	if days != -1 {
		t.Errorf("expected -1 (source exists), got %d", days)
	}
}

func TestIsRDSSnapshotOrphaned_YoungSnapshot_NotOrphaned(t *testing.T) {
	days := isRDSSnapshotOrphaned(false, 15*24*time.Hour, 30*24*time.Hour)
	if days != -1 {
		t.Errorf("expected -1 (too young), got %d", days)
	}
}

func TestIsRDSSnapshotOrphaned_OldOrphan_Flagged(t *testing.T) {
	days := isRDSSnapshotOrphaned(false, 45*24*time.Hour, 30*24*time.Hour)
	if days != 45 {
		t.Errorf("expected 45, got %d", days)
	}
}

// ── discoveryRegions ─────────────────────────────────────────────────────────────
//
// accountRegion is a floor, not a replacement: without it, a freshly
// connected CUR account with no cost data yet (up to 24h before first
// delivery, per the migration plan §2) produces an empty region set, so
// every Discover* function's "for region := range regions" loop runs zero
// times — nothing gets checked anywhere, even the account's own region.

func TestDiscoveryRegions_EmptyRecords_StillIncludesAccountRegion(t *testing.T) {
	regions := discoveryRegions(nil, "us-east-1")
	if _, ok := regions["us-east-1"]; !ok {
		t.Fatalf("expected account region present with no cost records, got %v", regions)
	}
	if len(regions) != 1 {
		t.Errorf("expected exactly 1 region, got %d: %v", len(regions), regions)
	}
}

func TestDiscoveryRegions_UnionsAccountRegionWithCostRecordRegions(t *testing.T) {
	records := []model.CostRecord{
		{Region: "eu-central-1"},
		{Region: "us-west-2"},
	}
	regions := discoveryRegions(records, "us-east-1")
	for _, want := range []string{"eu-central-1", "us-west-2", "us-east-1"} {
		if _, ok := regions[want]; !ok {
			t.Errorf("expected %q in result, got %v", want, regions)
		}
	}
	if len(regions) != 3 {
		t.Errorf("expected 3 regions, got %d: %v", len(regions), regions)
	}
}

func TestDiscoveryRegions_AccountRegionAlreadyPresent_NoDuplicate(t *testing.T) {
	records := []model.CostRecord{{Region: "us-east-1"}, {Region: "us-east-1"}}
	regions := discoveryRegions(records, "us-east-1")
	if len(regions) != 1 {
		t.Errorf("expected 1 region (no duplication), got %d: %v", len(regions), regions)
	}
}

func TestDiscoveryRegions_InvalidAccountRegion_Excluded(t *testing.T) {
	regions := discoveryRegions(nil, "global")
	if len(regions) != 0 {
		t.Errorf("expected 0 regions (invalid account region rejected like any other), got %v", regions)
	}
}
