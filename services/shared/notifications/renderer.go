package notifications

import (
	"fmt"
	"sort"
	"strings"

	"axiaops.io/shared/analyzer"
	"axiaops.io/shared/model"
)

// formatMoney renders an amount for a digest. USD (the only currency the cost
// pipeline emits today) gets the "$1,234.50" shape; anything else falls back to
// "1234.50 EUR" so an unexpected currency is still legible.
func formatMoney(amount float64, currency string) string {
	if currency == "" || currency == "USD" {
		return fmt.Sprintf("$%.2f", amount)
	}
	return fmt.Sprintf("%.2f %s", amount, currency)
}

// BuildPayload assembles a transport-agnostic Payload from a completed scan. The
// per-service breakdown is sorted by savings descending (ties broken by service
// name for determinism) and trimmed to topN — the channel's digest_top_n "body
// trim" knob. topN <= 0 means "include every service".
//
// dashboardOrigin is the org's externally-reachable origin (PUBLIC_HOST). When
// empty the DashboardURL is left empty and transports fall back to a relative
// link or omit it.
func BuildPayload(snap model.ZombieSnapshot, summary analyzer.Summary, topN int, dashboardOrigin string) Payload {
	rows := make([]ServiceRow, 0, len(summary.ByService))
	for service, s := range summary.ByService {
		rows = append(rows, ServiceRow{Service: service, Count: s.Zombies, Savings: s.Savings})
	}
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].Savings != rows[j].Savings {
			return rows[i].Savings > rows[j].Savings
		}
		return rows[i].Service < rows[j].Service
	})
	if topN > 0 && len(rows) > topN {
		rows = rows[:topN]
	}

	currency := summary.Currency
	if currency == "" {
		currency = snap.Currency
	}

	return Payload{
		OrganizationID: snap.OrganizationID,
		AccountID:      snap.AccountID,
		ZombieCount:    summary.TotalZombies,
		MonthlySavings: summary.PotentialMonthlySave,
		Currency:       currency,
		TopServices:    rows,
		DashboardURL:   strings.TrimSuffix(dashboardOrigin, "/"),
		SnapshotID:     snap.ID,
	}
}
