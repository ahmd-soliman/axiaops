// Package analyzer detects zombie cloud resources by joining cost records with
// usage metrics and applying per-service threshold rules.
package analyzer

import (
	"fmt"
	"sort"

	"axiaops.io/shared/model"
)

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

// Validate enforces the strict invariants every UsageRecord must satisfy:
//   - ResourceID non-empty
//   - Metric non-empty
//   - Avg non-negative (no AWS metric AxiaOps consumes can legitimately be < 0)
//   - PeriodDays non-negative
//
// Unit is informational and not checked. Returns *model.ValidationError on
// failure so callers can switch on the field.
func (u UsageRecord) Validate() error {
	if u.ResourceID == "" {
		return &model.ValidationError{Field: "resource_id", Message: "must be non-empty"}
	}
	if u.Metric == "" {
		return &model.ValidationError{Field: "metric", Message: "must be non-empty"}
	}
	if u.Avg < 0 {
		return &model.ValidationError{Field: "avg", Message: fmt.Sprintf("%.4f is negative", u.Avg)}
	}
	if u.PeriodDays < 0 {
		return &model.ValidationError{Field: "period_days", Message: fmt.Sprintf("%d is negative", u.PeriodDays)}
	}
	return nil
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
				ResourceType:      ResourceType(c.Service, u.Metric),
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
	for svc, summary := range s.ByService {
		summary.Savings = round2(summary.Savings)
		s.ByService[svc] = summary
	}
	return s
}

// ServiceResourceBreakdown is one (service, resource_type) bucket of zombies —
// the grain persisted to zombie_snapshot_services so the trend resource-type
// filter can scope a service's history to a single sub-type.
type ServiceResourceBreakdown struct {
	Service      string
	ResourceType string
	Zombies      int
	Savings      float64
	Currency     string
}

// SummarizeByServiceResourceType buckets zombies by (service, resource_type).
// Unlike Summarize (one bucket per service, used by /summary), this is the
// per-snapshot breakdown that powers the trend resource-type filter. The result
// is sorted by service then resource_type for deterministic output.
func SummarizeByServiceResourceType(zombies []model.ZombieResource) []ServiceResourceBreakdown {
	type key struct{ service, resourceType string }
	buckets := make(map[key]*ServiceResourceBreakdown)
	for _, z := range zombies {
		k := key{z.Service, z.ResourceType}
		b, ok := buckets[k]
		if !ok {
			b = &ServiceResourceBreakdown{
				Service:      z.Service,
				ResourceType: z.ResourceType,
				Currency:     z.Currency,
			}
			buckets[k] = b
		}
		b.Zombies++
		b.Savings += z.MonthlyCost
	}

	// Collect and sort deterministically (map iteration order is random).
	out := make([]ServiceResourceBreakdown, 0, len(buckets))
	for _, b := range buckets {
		b.Savings = round2(b.Savings)
		out = append(out, *b)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Service != out[j].Service {
			return out[i].Service < out[j].Service
		}
		return out[i].ResourceType < out[j].ResourceType
	})
	return out
}

// ByAccountSummary holds per-account zombie aggregates across an organization.
type ByAccountSummary struct {
	Currency string           `json:"currency"`
	Accounts []AccountSummary `json:"accounts"`
}

// AccountSummary groups zombie counts and savings for one connected account.
// InternalAccountID is the grouping key (the accounts-table UUID); AccountID is
// the AWS account number, retained for display only.
type AccountSummary struct {
	InternalAccountID string  `json:"internal_account_id"`
	AccountID         string  `json:"account_id"`
	TotalZombies      int     `json:"total_zombies"`
	PotentialMonthly  float64 `json:"potential_monthly_savings"`
	TopService        string  `json:"top_service"`
}

// SummarizeByAccount groups zombie resources by their internal account ID and
// computes per-account totals: zombie count, summed monthly savings, and the
// service with the largest summed savings in that account (top_service; "" if
// none). Accounts with zero zombies are omitted. The returned Accounts slice is
// always non-nil so it serialises as [] rather than null on empty input.
func SummarizeByAccount(zombies []model.ZombieResource) ByAccountSummary {
	type accountAgg struct {
		accountID    string
		totalZombies int
		savings      float64
		byService    map[string]float64
	}

	order := make([]string, 0)
	aggs := make(map[string]*accountAgg)

	out := ByAccountSummary{Accounts: []AccountSummary{}}
	for _, z := range zombies {
		if out.Currency == "" {
			out.Currency = z.Currency
		}
		a, ok := aggs[z.InternalAccountID]
		if !ok {
			a = &accountAgg{byService: make(map[string]float64)}
			aggs[z.InternalAccountID] = a
			order = append(order, z.InternalAccountID)
		}
		if a.accountID == "" {
			a.accountID = z.AccountID
		}
		a.totalZombies++
		a.savings += z.MonthlyCost
		a.byService[z.Service] += z.MonthlyCost
	}

	for _, id := range order {
		a := aggs[id]
		out.Accounts = append(out.Accounts, AccountSummary{
			InternalAccountID: id,
			AccountID:         a.accountID,
			TotalZombies:      a.totalZombies,
			PotentialMonthly:  round2(a.savings),
			TopService:        topService(a.byService),
		})
	}
	return out
}

// topService returns the service key with the largest summed savings, or "" when
// the map is empty. Ties break toward the lexicographically smallest service
// name so output is deterministic.
func topService(byService map[string]float64) string {
	top := ""
	var max float64
	for svc, savings := range byService {
		if top == "" || savings > max || (savings == max && svc < top) {
			top = svc
			max = savings
		}
	}
	return top
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
	type zombieInfo struct{ reason, owner, resourceType string }
	zombieByID := make(map[string]zombieInfo, len(zombies))
	for _, z := range zombies {
		zombieByID[z.ResourceID] = zombieInfo{reason: z.Reason, owner: z.Owner, resourceType: z.ResourceType}
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
		// Prefer the resource_type the zombie already carries — API-only zombies
		// (e.g. unattached EBS volumes) have no CloudWatch usage, so u.Metric is
		// empty here and ResourceType(service, "") would yield "". The zombie was
		// classified from its own usage metric upstream; reuse that.
		rt := zi.resourceType
		if rt == "" {
			rt = ResourceType(c.Service, u.Metric)
		}
		resources = append(resources, model.ResourceRecord{
			Provider:          c.Provider,
			AccountID:         c.AccountID,
			InternalAccountID: "", // Will be set by caller
			Service:           c.Service,
			ResourceType:      rt,
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
			ResourceType:      z.ResourceType,
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
