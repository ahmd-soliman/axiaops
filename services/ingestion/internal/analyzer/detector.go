// Package analyzer detects zombie cloud resources by joining cost records with
// usage metrics and applying per-service threshold rules.
package analyzer

import "axiaops.io/ingestion/internal/model"

// UsageRecord holds the average usage metric for a single resource over a
// billing period. Sourced from CloudWatch in production; from a fixture file
// in dev mode.
type UsageRecord struct {
	ResourceID  string  `json:"resource_id"`
	Metric      string  `json:"metric"`
	Unit        string  `json:"unit"`
	Avg         float64 `json:"avg"`
	PeriodDays  int     `json:"period_days"`
}

// Detect joins cost records with usage metrics and returns any resources that
// are incurring cost but show no meaningful activity according to the
// per-service threshold rules in rules.go.
// internalAccountID is the UUID from the accounts table, used for filtering.
func Detect(costs []model.CostRecord, usage []UsageRecord, internalAccountID string) []model.ZombieResource {
	// Index usage by resource_id for O(1) lookup.
	usageByID := make(map[string]UsageRecord, len(usage))
	for _, u := range usage {
		usageByID[u.ResourceID] = u
	}

	var zombies []model.ZombieResource
	for _, c := range costs {
		r, hasRule := serviceRules[c.Service]
		if !hasRule {
			continue // no rule for this service in MVP
		}

		u, hasUsage := usageByID[c.ResourceID]
		if !hasUsage {
			continue // no usage data — cannot make a determination
		}

		if u.Avg <= r.threshold {
			zombies = append(zombies, model.ZombieResource{
				Provider:          c.Provider,
				AccountID:         c.AccountID,
				InternalAccountID: internalAccountID,
				Service:           c.Service,
				Region:            c.Region,
				ResourceID:        c.ResourceID,
				Tags:              c.Tags,
				MonthlyCost:       c.Amount,
				Currency:          c.Currency,
				PeriodStart:       c.PeriodStart,
				PeriodEnd:         c.PeriodEnd,
				UsageMetric:       u.Metric,
				UsageAvg:          u.Avg,
				UsageUnit:         u.Unit,
				Reason:            r.reason,
				Owner:             owner(c.Tags),
			})
		}
	}
	return zombies
}

// Summary holds aggregate savings figures across all detected zombie resources.
type Summary struct {
	TotalZombies         int     `json:"total_zombies"`
	PotentialMonthlySave float64 `json:"potential_monthly_savings"`
	Currency             string  `json:"currency"`
	ByService            map[string]ServiceSummary `json:"by_service"`
}

// ServiceSummary groups zombie counts and savings for one AWS service.
type ServiceSummary struct {
	Zombies int     `json:"zombies"`
	Savings float64 `json:"savings"`
}

// Summarize computes aggregate savings from a slice of zombie resources.
func Summarize(zombies []model.ZombieResource) Summary {
	s := Summary{
		TotalZombies: len(zombies),
		ByService:    make(map[string]ServiceSummary),
	}
	for _, g := range zombies {
		s.PotentialMonthlySave += g.MonthlyCost
		if s.Currency == "" {
			s.Currency = g.Currency
		}
		svc := s.ByService[g.Service]
		svc.Zombies++
		svc.Savings += g.MonthlyCost
		s.ByService[g.Service] = svc
	}
	s.PotentialMonthlySave = round2(s.PotentialMonthlySave)
	return s
}

// owner derives the responsible team from resource tags.
// Falls back to "unknown" when no team tag is present.
func owner(tags map[string]string) string {
	if t, ok := tags["team"]; ok && t != "" {
		return t
	}
	return "unknown"
}

// round2 rounds f to 2 decimal places.
func round2(f float64) float64 {
	return float64(int(f*100+0.5)) / 100
}
