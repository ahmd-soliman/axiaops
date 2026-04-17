// Package analyzer detects zombie cloud resources by joining cost records with
// usage metrics and applying per-service threshold rules.
package analyzer

import "axiaops.io/shared/model"

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
func Detect(costs []model.CostRecord, usage []UsageRecord, internalAccountID string) []model.GhostResource {
	// Index usage by resource_id for O(1) lookup.
	usageByID := make(map[string]UsageRecord, len(usage))
	for _, u := range usage {
		usageByID[u.ResourceID] = u
	}

	var ghosts []model.GhostResource
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
			ghosts = append(ghosts, model.GhostResource{
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
	return ghosts
}

// Summary holds aggregate savings figures across all detected ghost resources.
type Summary struct {
	TotalGhosts          int     `json:"total_ghosts"`
	PotentialMonthlySave float64 `json:"potential_monthly_savings"`
	Currency             string  `json:"currency"`
	ByService            map[string]ServiceSummary `json:"by_service"`
}

// ServiceSummary groups ghost counts and savings for one AWS service.
type ServiceSummary struct {
	Ghosts  int     `json:"ghosts"`
	Savings float64 `json:"savings"`
}

// Summarize computes aggregate savings from a slice of ghost resources.
func Summarize(ghosts []model.GhostResource) Summary {
	s := Summary{
		TotalGhosts: len(ghosts),
		ByService:   make(map[string]ServiceSummary),
	}
	for _, g := range ghosts {
		s.PotentialMonthlySave += g.MonthlyCost
		if s.Currency == "" {
			s.Currency = g.Currency
		}
		svc := s.ByService[g.Service]
		svc.Ghosts++
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

// AnnotateAll returns a ResourceRecord for every cost entry that has a
// non-empty resource ID. It uses the pre-computed ghosts slice (which already
// includes EIP and other special-case ghosts) to set IsGhost and Reason,
// so the caller does not need to re-run detection logic here.
func AnnotateAll(costs []model.CostRecord, usage []UsageRecord, ghosts []model.GhostResource) []model.ResourceRecord {
	// Index usage by resource_id for O(1) lookup.
	usageByID := make(map[string]UsageRecord, len(usage))
	for _, u := range usage {
		usageByID[u.ResourceID] = u
	}

	// Index ghost info by resource_id.
	type ghostInfo struct{ reason, owner string }
	ghostByID := make(map[string]ghostInfo, len(ghosts))
	for _, g := range ghosts {
		ghostByID[g.ResourceID] = ghostInfo{reason: g.Reason, owner: g.Owner}
	}

	// Track which resource IDs have a cost record so we can add ghost-only entries below.
	costResourceIDs := make(map[string]struct{}, len(costs))

	var resources []model.ResourceRecord
	for _, c := range costs {
		if c.ResourceID == "" {
			continue
		}
		costResourceIDs[c.ResourceID] = struct{}{}
		u := usageByID[c.ResourceID]
		gi, isGhost := ghostByID[c.ResourceID]
		o := gi.owner
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
			Tags:              c.Tags,
			MonthlyCost:       c.Amount,
			Currency:          c.Currency,
			PeriodStart:       c.PeriodStart,
			PeriodEnd:         c.PeriodEnd,
			UsageMetric:       u.Metric,
			UsageAvg:          u.Avg,
			UsageUnit:         u.Unit,
			IsGhost:           isGhost,
			Reason:            gi.reason,
			Owner:             o,
		})
	}

	// Some ghosts (e.g. unattached EIPs) are discovered via AWS APIs and never
	// appear as individual line items in Cost Explorer. Add them here so they
	// show up in the resource inventory.
	for _, g := range ghosts {
		if _, inCosts := costResourceIDs[g.ResourceID]; inCosts {
			continue
		}
		resources = append(resources, model.ResourceRecord{
			Provider:          g.Provider,
			AccountID:         g.AccountID,
			InternalAccountID: g.InternalAccountID,
			Service:           g.Service,
			Region:            g.Region,
			ResourceID:        g.ResourceID,
			Tags:              g.Tags,
			MonthlyCost:       g.MonthlyCost,
			Currency:          g.Currency,
			PeriodStart:       g.PeriodStart,
			PeriodEnd:         g.PeriodEnd,
			UsageMetric:       g.UsageMetric,
			UsageAvg:          g.UsageAvg,
			UsageUnit:         g.UsageUnit,
			IsGhost:           true,
			Reason:            g.Reason,
			Owner:             g.Owner,
		})
	}

	return resources
}

// round2 rounds f to 2 decimal places.
func round2(f float64) float64 {
	return float64(int(f*100+0.5)) / 100
}
