// Package fake provides a scenario-based Provider for dev mode and testing.
// It returns pre-defined cost and usage records without making any AWS API calls.
//
// Usage:
//
//	p := fake.New("startup")          // named scenario
//	p := fake.New("")                 // defaults to "startup"
//	records, _ := p.FetchCosts(ctx, start, end)
//	usage, _   := p.FetchUsage(ctx, records, start, end)
package fake

import (
	"context"
	"time"

	"axiaops.io/shared/analyzer"
	"axiaops.io/shared/model"
)

// Provider implements provider.Provider using static scenario data.
type Provider struct {
	scenario string
}

// New returns a fake Provider for the named scenario.
// If scenario is empty or unknown, it falls back to "startup".
func New(scenario string) *Provider {
	if _, ok := scenarios[scenario]; !ok {
		scenario = "startup"
	}
	return &Provider{scenario: scenario}
}

func (p *Provider) Name() string { return "fake-aws" }

// FetchCosts returns the cost records for the active scenario.
// start and end are accepted for interface compatibility but ignored.
func (p *Provider) FetchCosts(_ context.Context, start, end time.Time) ([]model.CostRecord, error) {
	s := scenarios[p.scenario]
	records := make([]model.CostRecord, len(s.Costs))
	for i, r := range s.Costs {
		r.PeriodStart = start
		r.PeriodEnd = end
		r.FetchedAt = time.Now().UTC()
		records[i] = r
	}
	return records, nil
}

// FetchUsage returns the usage records for the active scenario.
// records, start, and end are accepted for interface compatibility but ignored.
func (p *Provider) FetchUsage(_ context.Context, _ []model.CostRecord, _, _ time.Time) ([]analyzer.UsageRecord, error) {
	return scenarios[p.scenario].Usage, nil
}

// ScenarioNames returns all available scenario names.
func ScenarioNames() []string {
	names := make([]string, 0, len(scenarios))
	for k := range scenarios {
		names = append(names, k)
	}
	return names
}
