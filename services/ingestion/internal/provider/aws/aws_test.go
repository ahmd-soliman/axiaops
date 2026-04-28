package aws_test

import (
	"context"
	"fmt"
	"testing"
	"time"

	ceaws "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/cloudwatch"
	"github.com/aws/aws-sdk-go-v2/service/costexplorer"
	"github.com/aws/aws-sdk-go-v2/service/costexplorer/types"

	"axiaops.io/ingestion/internal/provider/aws"
)

// mockCEClient is a test double for CostExplorerAPI.
// pages holds successive GetCostAndUsage responses; resourcePages holds
// successive GetCostAndUsageWithResources responses. capturedResourceInput
// records the most recent resource-API request so tests can assert call shape.
type mockCEClient struct {
	pages         []costexplorer.GetCostAndUsageOutput
	resourcePages []costexplorer.GetCostAndUsageWithResourcesOutput
	call          int
	resourceCall  int
	err           error
	resourceErr   error

	capturedResourceInput *costexplorer.GetCostAndUsageWithResourcesInput
}

func (m *mockCEClient) GetCostAndUsage(
	_ context.Context,
	_ *costexplorer.GetCostAndUsageInput,
	_ ...func(*costexplorer.Options),
) (*costexplorer.GetCostAndUsageOutput, error) {
	if m.err != nil {
		return nil, m.err
	}
	page := m.pages[m.call]
	m.call++
	return &page, nil
}

func (m *mockCEClient) GetCostAndUsageWithResources(
	_ context.Context,
	input *costexplorer.GetCostAndUsageWithResourcesInput,
	_ ...func(*costexplorer.Options),
) (*costexplorer.GetCostAndUsageWithResourcesOutput, error) {
	m.capturedResourceInput = input
	if m.resourceErr != nil {
		return nil, m.resourceErr
	}
	page := m.resourcePages[m.resourceCall]
	m.resourceCall++
	return &page, nil
}

// mockCWClient is a test double for CloudWatchAPI.
type mockCWClient struct{}

func (m *mockCWClient) GetMetricStatistics(
	_ context.Context,
	_ *cloudwatch.GetMetricStatisticsInput,
	_ ...func(*cloudwatch.Options),
) (*cloudwatch.GetMetricStatisticsOutput, error) {
	return &cloudwatch.GetMetricStatisticsOutput{}, nil
}

func TestFetchCosts_SinglePage(t *testing.T) {
	mock := &mockCEClient{
		pages: []costexplorer.GetCostAndUsageOutput{
			{
				ResultsByTime: []types.ResultByTime{
					{
						TimePeriod: &types.DateInterval{
							Start: ceaws.String("2026-03-01"),
							End:   ceaws.String("2026-03-31"),
						},
						Groups: []types.Group{
							{
								Keys: []string{"AmazonEC2", "eu-central-1"},
								Metrics: map[string]types.MetricValue{
									"NetAmortizedCost": {
										Amount: ceaws.String("1243.87"),
										Unit:   ceaws.String("USD"),
									},
								},
							},
						},
					},
				},
			},
		},
	}

	client := aws.NewWithClient("123456789012", mock, &mockCWClient{})
	records, err := client.FetchCosts(context.Background(), time.Now(), time.Now())

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(records) != 1 {
		t.Fatalf("expected 1 record, got %d", len(records))
	}

	r := records[0]
	if r.Provider != "aws" {
		t.Errorf("expected provider aws, got %s", r.Provider)
	}
	if r.Service != "AmazonEC2" {
		t.Errorf("expected service AmazonEC2, got %s", r.Service)
	}
	if r.Region != "eu-central-1" {
		t.Errorf("expected region eu-central-1, got %s", r.Region)
	}
	if r.Amount != 1243.87 {
		t.Errorf("expected amount 1243.87, got %f", r.Amount)
	}
	if r.Currency != "USD" {
		t.Errorf("expected currency USD, got %s", r.Currency)
	}
}

func TestFetchCosts_Pagination(t *testing.T) {
	nextToken := "token-page-2"
	mock := &mockCEClient{
		pages: []costexplorer.GetCostAndUsageOutput{
			{
				NextPageToken: &nextToken,
				ResultsByTime: []types.ResultByTime{
					{
						TimePeriod: &types.DateInterval{
							Start: ceaws.String("2026-03-01"),
							End:   ceaws.String("2026-03-15"),
						},
						Groups: []types.Group{
							{
								Keys: []string{"AmazonS3", "eu-central-1"},
								Metrics: map[string]types.MetricValue{
									"NetAmortizedCost": {Amount: ceaws.String("50.00"), Unit: ceaws.String("USD")},
								},
							},
						},
					},
				},
			},
			{
				ResultsByTime: []types.ResultByTime{
					{
						TimePeriod: &types.DateInterval{
							Start: ceaws.String("2026-03-16"),
							End:   ceaws.String("2026-03-31"),
						},
						Groups: []types.Group{
							{
								Keys: []string{"AmazonRDS", "eu-central-1"},
								Metrics: map[string]types.MetricValue{
									"NetAmortizedCost": {Amount: ceaws.String("876.42"), Unit: ceaws.String("USD")},
								},
							},
						},
					},
				},
			},
		},
	}

	client := aws.NewWithClient("123456789012", mock, &mockCWClient{})
	records, err := client.FetchCosts(context.Background(), time.Now(), time.Now())

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(records) != 2 {
		t.Fatalf("expected 2 records from 2 pages, got %d", len(records))
	}
	if records[0].Service != "AmazonS3" {
		t.Errorf("expected AmazonS3 on page 1, got %s", records[0].Service)
	}
	if records[1].Service != "AmazonRDS" {
		t.Errorf("expected AmazonRDS on page 2, got %s", records[1].Service)
	}
}

func TestFetchCosts_APIError(t *testing.T) {
	mock := &mockCEClient{
		err: fmt.Errorf("AccessDeniedException: User is not authorized"),
	}

	client := aws.NewWithClient("123456789012", mock, &mockCWClient{})
	_, err := client.FetchCosts(context.Background(), time.Now(), time.Now())

	if err == nil {
		t.Fatal("expected error, got nil")
	}
}

func TestFetchResourceCosts_HappyPath(t *testing.T) {
	mock := &mockCEClient{
		resourcePages: []costexplorer.GetCostAndUsageWithResourcesOutput{
			{
				ResultsByTime: []types.ResultByTime{
					{
						TimePeriod: &types.DateInterval{
							Start: ceaws.String("2026-04-20"),
							End:   ceaws.String("2026-04-21"),
						},
						Groups: []types.Group{
							{
								Keys: []string{"Amazon Elastic Compute Cloud - Compute", "arn:aws:ec2:eu-central-1:123456789012:instance/i-0abc123"},
								Metrics: map[string]types.MetricValue{
									"UnblendedCost": {Amount: ceaws.String("12.34"), Unit: ceaws.String("USD")},
								},
							},
						},
					},
				},
			},
		},
	}

	client := aws.NewWithClient("123456789012", mock, &mockCWClient{})
	end := time.Now()
	start := end.Add(-7 * 24 * time.Hour)
	records, err := client.FetchResourceCosts(context.Background(), start, end)

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(records) != 1 {
		t.Fatalf("expected 1 record, got %d", len(records))
	}

	r := records[0]
	if r.Service != "AmazonEC2" {
		t.Errorf("expected service AmazonEC2 (normalized), got %s", r.Service)
	}
	if r.Region != "eu-central-1" {
		t.Errorf("expected region eu-central-1 (parsed from ARN), got %s", r.Region)
	}
	if r.ResourceID != "i-0abc123" {
		t.Errorf("expected short resource id i-0abc123, got %s", r.ResourceID)
	}
	if r.Amount != 12.34 {
		t.Errorf("expected amount 12.34, got %f", r.Amount)
	}

	// Verify the call shape — Daily granularity, Filter present.
	in := mock.capturedResourceInput
	if in == nil {
		t.Fatal("expected GetCostAndUsageWithResources to be invoked")
	}
	if in.Granularity != types.GranularityDaily {
		t.Errorf("expected GranularityDaily, got %v", in.Granularity)
	}
	if in.Filter == nil || in.Filter.Dimensions == nil {
		t.Error("expected Filter with service Dimensions; required by GetCostAndUsageWithResources")
	}
}

func TestFetchResourceCosts_SkipsMissingAndNonPositive(t *testing.T) {
	// Skip rows with empty / "NoResourceId" identifiers and rows with amount <= 0.
	mock := &mockCEClient{
		resourcePages: []costexplorer.GetCostAndUsageWithResourcesOutput{
			{
				ResultsByTime: []types.ResultByTime{
					{
						TimePeriod: &types.DateInterval{
							Start: ceaws.String("2026-04-20"),
							End:   ceaws.String("2026-04-21"),
						},
						Groups: []types.Group{
							{
								Keys: []string{"AWS Lambda", ""},
								Metrics: map[string]types.MetricValue{
									"UnblendedCost": {Amount: ceaws.String("12.00"), Unit: ceaws.String("USD")},
								},
							},
							{
								Keys: []string{"AWS Lambda", "NoResourceId"},
								Metrics: map[string]types.MetricValue{
									"UnblendedCost": {Amount: ceaws.String("8.00"), Unit: ceaws.String("USD")},
								},
							},
							{
								Keys: []string{"Amazon Relational Database Service", "arn:aws:rds:eu-central-1:123456789012:db:mydb"},
								Metrics: map[string]types.MetricValue{
									"UnblendedCost": {Amount: ceaws.String("0.00"), Unit: ceaws.String("USD")},
								},
							},
							{
								Keys: []string{"Amazon Relational Database Service", "arn:aws:rds:eu-central-1:123456789012:db:realdb"},
								Metrics: map[string]types.MetricValue{
									"UnblendedCost": {Amount: ceaws.String("47.25"), Unit: ceaws.String("USD")},
								},
							},
						},
					},
				},
			},
		},
	}

	client := aws.NewWithClient("123456789012", mock, &mockCWClient{})
	end := time.Now()
	records, err := client.FetchResourceCosts(context.Background(), end.Add(-7*24*time.Hour), end)

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(records) != 1 {
		t.Fatalf("expected 1 surviving record, got %d", len(records))
	}
	if records[0].ResourceID != "realdb" {
		t.Errorf("expected resource id realdb, got %s", records[0].ResourceID)
	}
}

func TestFetchResourceCosts_ClampsLookbackTo14Days(t *testing.T) {
	// GetCostAndUsageWithResources only supports the last 14 days. The function
	// must clamp longer windows so AWS doesn't reject the request.
	mock := &mockCEClient{
		resourcePages: []costexplorer.GetCostAndUsageWithResourcesOutput{{}},
	}
	client := aws.NewWithClient("123456789012", mock, &mockCWClient{})

	// Fixed `end` at midnight so the date-format truncation in the SDK input
	// doesn't add wall-clock slack to the assertion below.
	end := time.Date(2026, 4, 29, 0, 0, 0, 0, time.UTC)
	start := end.Add(-30 * 24 * time.Hour) // caller asks for 30 days

	_, err := client.FetchResourceCosts(context.Background(), start, end)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	in := mock.capturedResourceInput
	if in == nil {
		t.Fatal("expected GetCostAndUsageWithResources to be invoked")
	}
	gotStart, parseErr := time.Parse("2006-01-02", ceaws.ToString(in.TimePeriod.Start))
	if parseErr != nil {
		t.Fatalf("could not parse Start: %v", parseErr)
	}
	want := end.Add(-14 * 24 * time.Hour)
	if !gotStart.Equal(want) {
		t.Errorf("expected clamped Start = %s, got %s", want.Format("2006-01-02"), gotStart.Format("2006-01-02"))
	}
}

func TestFetchResourceCosts_APIError_NonFatal(t *testing.T) {
	// Customers without "hourly granularity & resource-level data" enabled in
	// Cost Explorer get a DataUnavailableException-style error here.
	// FetchResourceCosts is supplemental — return nil, nil and let the caller
	// proceed with service-level cost data.
	mock := &mockCEClient{
		resourceErr: fmt.Errorf("DataUnavailableException: resource-level data is not enabled"),
	}

	client := aws.NewWithClient("123456789012", mock, &mockCWClient{})
	end := time.Now()
	records, err := client.FetchResourceCosts(context.Background(), end.Add(-7*24*time.Hour), end)

	if err != nil {
		t.Errorf("expected non-fatal error handling, got error: %v", err)
	}
	if len(records) != 0 {
		t.Errorf("expected nil records on error, got %d", len(records))
	}
}

func TestFetchCostExplorerAPICosts_SinglePage(t *testing.T) {
	mock := &mockCEClient{
		pages: []costexplorer.GetCostAndUsageOutput{
			{
				ResultsByTime: []types.ResultByTime{
					{
						TimePeriod: &types.DateInterval{
							Start: ceaws.String("2026-04-01"),
							End:   ceaws.String("2026-04-30"),
						},
						Groups: []types.Group{
							{
								Keys: []string{"Amazon Cost Management APIs", "us-east-1"},
								Metrics: map[string]types.MetricValue{
									"NetAmortizedCost": {
										Amount: ceaws.String("0.47"),
										Unit:   ceaws.String("USD"),
									},
								},
							},
						},
					},
				},
			},
		},
	}

	client := aws.NewWithClient("123456789012", mock, &mockCWClient{})
	records, err := client.FetchCostExplorerAPICosts(context.Background(), time.Now(), time.Now())

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(records) != 1 {
		t.Fatalf("expected 1 record, got %d", len(records))
	}

	r := records[0]
	if r.Provider != "aws" {
		t.Errorf("expected provider aws, got %s", r.Provider)
	}
	if r.Service != "AWSCostExplorer" {
		t.Errorf("expected service AWSCostExplorer, got %s", r.Service)
	}
	if r.Region != "us-east-1" {
		t.Errorf("expected region us-east-1, got %s", r.Region)
	}
	if r.Amount != 0.47 {
		t.Errorf("expected amount 0.47, got %f", r.Amount)
	}
	if r.Currency != "USD" {
		t.Errorf("expected currency USD, got %s", r.Currency)
	}
}

func TestFetchCostExplorerAPICosts_SkipsZeroAmount(t *testing.T) {
	mock := &mockCEClient{
		pages: []costexplorer.GetCostAndUsageOutput{
			{
				ResultsByTime: []types.ResultByTime{
					{
						TimePeriod: &types.DateInterval{
							Start: ceaws.String("2026-04-01"),
							End:   ceaws.String("2026-04-30"),
						},
						Groups: []types.Group{
							{
								Keys: []string{"Amazon Cost Management APIs", "us-east-1"},
								Metrics: map[string]types.MetricValue{
									"NetAmortizedCost": {
										Amount: ceaws.String("0.00"),
										Unit:   ceaws.String("USD"),
									},
								},
							},
						},
					},
				},
			},
		},
	}

	client := aws.NewWithClient("123456789012", mock, &mockCWClient{})
	records, err := client.FetchCostExplorerAPICosts(context.Background(), time.Now(), time.Now())

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(records) != 0 {
		t.Fatalf("expected 0 records for zero amount, got %d", len(records))
	}
}

func TestFetchCostExplorerAPICosts_APIError_NonFatal(t *testing.T) {
	mock := &mockCEClient{
		err: fmt.Errorf("ServiceUnavailableException"),
	}

	client := aws.NewWithClient("123456789012", mock, &mockCWClient{})
	records, err := client.FetchCostExplorerAPICosts(context.Background(), time.Now(), time.Now())

	// Should be non-fatal: returns nil, nil
	if err != nil {
		t.Errorf("expected non-fatal error handling, got error: %v", err)
	}
	if len(records) != 0 {
		t.Errorf("expected nil records on error, got %d", len(records))
	}
}
