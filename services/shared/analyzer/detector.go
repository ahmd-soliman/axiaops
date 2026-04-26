// Package analyzer detects zombie cloud resources by joining cost records with
// usage metrics and applying per-service threshold rules.
package analyzer

import "axiaops.io/shared/model"

// UsageRecord holds the average usage metric for a single resource over a
// billing period. Sourced from CloudWatch in production; from a fixture file
// in dev mode.
type UsageRecord struct {
	ResourceID string  `json:"resource_id"`
	Metric     string  `json:"metric"`
	Unit       string  `json:"unit"`
	Avg        float64 `json:"avg"`
	PeriodDays int     `json:"period_days"`
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
				ARN:               model.BuildARN(c.Provider, c.AccountID, c.Region, c.Service, c.ResourceID),
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
	TotalZombies         int                       `json:"total_zombies"`
	PotentialMonthlySave float64                   `json:"potential_monthly_savings"`
	Currency             string                    `json:"currency"`
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
	for _, z := range zombies {
		s.PotentialMonthlySave += z.MonthlyCost
		if s.Currency == "" {
			s.Currency = z.Currency
		}
		svc := s.ByService[z.Service]
		svc.Zombies++
		svc.Savings += z.MonthlyCost
		s.ByService[z.Service] = svc
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

// AnnotateAll returns a ResourceRecord for every cost entry that has a
// non-empty resource ID. It uses the pre-computed zombies slice (which already
// includes EIP and other special-case zombies) to set IsZombie and Reason,
// so the caller does not need to re-run detection logic here.
func AnnotateAll(costs []model.CostRecord, usage []UsageRecord, zombies []model.ZombieResource) []model.ResourceRecord {
	// Index usage by resource_id for O(1) lookup.
	usageByID := make(map[string]UsageRecord, len(usage))
	for _, u := range usage {
		usageByID[u.ResourceID] = u
	}

	// Index zombie info by resource_id.
	type zombieInfo struct{ reason, owner string }
	zombieByID := make(map[string]zombieInfo, len(zombies))
	for _, z := range zombies {
		zombieByID[z.ResourceID] = zombieInfo{reason: z.Reason, owner: z.Owner}
	}

	// Track which resource IDs have a cost record so we can add zombie-only entries below.
	costResourceIDs := make(map[string]struct{}, len(costs))

	var resources []model.ResourceRecord
	for _, c := range costs {
		if c.ResourceID == "" {
			continue
		}
		costResourceIDs[c.ResourceID] = struct{}{}
		u := usageByID[c.ResourceID]
		zi, isZombie := zombieByID[c.ResourceID]
		o := zi.owner
		if o == "" {
			o = owner(c.Tags)
		}
		resources = append(resources, model.ResourceRecord{
			Provider:          c.Provider,
			AccountID:         c.AccountID,
			InternalAccountID: "", // Will be set by caller
			Service:           c.Service,
			Region:            c.Region,
			ResourceID:        c.ResourceID,
			ARN:               model.BuildARN(c.Provider, c.AccountID, c.Region, c.Service, c.ResourceID),
			Tags:              c.Tags,
			MonthlyCost:       c.Amount,
			Currency:          c.Currency,
			PeriodStart:       c.PeriodStart,
			PeriodEnd:         c.PeriodEnd,
			UsageMetric:       u.Metric,
			UsageAvg:          u.Avg,
			UsageUnit:         u.Unit,
			IsZombie:          isZombie,
			Reason:            zi.reason,
			Owner:             o,
		})
	}

	// Some zombies (e.g. unattached EIPs) are discovered via AWS APIs and never
	// appear as individual line items in Cost Explorer. Add them here so they
	// show up in the resource inventory.
	for _, z := range zombies {
		if _, inCosts := costResourceIDs[z.ResourceID]; inCosts {
			continue
		}
		resources = append(resources, model.ResourceRecord{
			Provider:          z.Provider,
			AccountID:         z.AccountID,
			InternalAccountID: z.InternalAccountID,
			Service:           z.Service,
			Region:            z.Region,
			ResourceID:        z.ResourceID,
			ARN:               z.ARN,
			Tags:              z.Tags,
			MonthlyCost:       z.MonthlyCost,
			Currency:          z.Currency,
			PeriodStart:       z.PeriodStart,
			PeriodEnd:         z.PeriodEnd,
			UsageMetric:       z.UsageMetric,
			UsageAvg:          z.UsageAvg,
			UsageUnit:         z.UsageUnit,
			IsZombie:          true,
			Reason:            z.Reason,
			Owner:             z.Owner,
		})
	}

	return resources
}

// round2 rounds f to 2 decimal places.
func round2(f float64) float64 {
	return float64(int(f*100+0.5)) / 100
}
