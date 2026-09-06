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
// pages holds successive GetCostAndUsage responses.
type mockCEClient struct {
	pages []costexplorer.GetCostAndUsageOutput
	call  int
	err   error
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

func TestFetchCosts_SkipsNonPositiveAmounts(t *testing.T) {
	// NetAmortizedCost can be negative (credits, refunds, SP true-ups) or zero.
	// FetchCosts must drop these so they don't subtract from PotentialMonthlySave.
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
									"NetAmortizedCost": {Amount: ceaws.String("100.00"), Unit: ceaws.String("USD")},
								},
							},
							{
								Keys: []string{"AWSPromotionalCredits", "eu-central-1"},
								Metrics: map[string]types.MetricValue{
									"NetAmortizedCost": {Amount: ceaws.String("-25.00"), Unit: ceaws.String("USD")},
								},
							},
							{
								Keys: []string{"AmazonS3", "us-east-1"},
								Metrics: map[string]types.MetricValue{
									"NetAmortizedCost": {Amount: ceaws.String("0.00"), Unit: ceaws.String("USD")},
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
		t.Fatalf("expected 1 record (negative + zero dropped), got %d", len(records))
	}
	if records[0].Service != "AmazonEC2" {
		t.Errorf("expected AmazonEC2 surviving, got %s", records[0].Service)
	}
	if records[0].Amount != 100.00 {
		t.Errorf("expected amount 100.00, got %f", records[0].Amount)
	}
}

