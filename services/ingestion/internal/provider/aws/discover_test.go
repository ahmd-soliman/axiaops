package aws

// White-box tests for discover.go helper functions.
// The API-calling functions (DiscoverUnattachedEBSVolumes, etc.) create EC2
// clients from aws.Config internally, so they are integration-tested against
// a real AWS account. The helpers tested here contain the core business logic
// (time parsing, region validation) and are the highest-value unit tests.

import (
	"testing"
	"time"
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

// ── ceAnomalyMonitorMonthlyCost sanity check ──────────────────────────────────

func TestCEAnomalyMonitorMonthlyCost_IsThreeDollars(t *testing.T) {
	// $0.10/day × 30 days = $3.00/month per paid monitor.
	want := 3.00
	if ceAnomalyMonitorMonthlyCost != want {
		t.Errorf("ceAnomalyMonitorMonthlyCost = %v; want %v", ceAnomalyMonitorMonthlyCost, want)
	}
}
