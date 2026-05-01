package license

import (
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"axiaops.io/shared/observability"
)

// VerifyAtBoot is the single entry point cmd/main.go calls to validate the
// license before serverbuild composes the rest of the API. Three outcomes:
//
//   - DEV_MODE bypass    → returns nil; no Snapshot is set; slog.Warn
//   - Valid / in-grace   → returns nil; SetCurrent stores the parsed License;
//                          Prometheus gauges set; slog.Info or slog.Warn
//                          carries the operator-facing context
//   - Past grace / load  → returns a non-nil error with an operator-actionable
//     failure              message; gauges still updated when classification
//                          got far enough; LicenseLoadErrorsTotal incremented
//                          before return so the alert fires even if the caller
//                          never gets a chance to log
//
// Caller (cmd/main.go) is expected to die() on a non-nil error — VerifyAtBoot
// never calls os.Exit itself, so it stays unit-testable. The error value is
// the message the caller should log; it includes the renewal contact for the
// past-grace case (see expiredMessage).
//
// Audit-row writes are intentionally NOT performed here. License events are
// binary-wide (no organization_id) and the audit_log table is org-scoped +
// RLS-isolated; the durable trace is the structured slog line plus the
// Prometheus metrics. See plan §4.9.3 for the full rationale.
func VerifyAtBoot(devMode bool) error {
	if devMode {
		slog.Warn("license: DEV_MODE — skipping verification")
		return nil
	}

	lic, err := Load()
	if err != nil {
		// The reason label drives the alert runbook (slice 9 docs).
		// die() in cmd/main.go logs the wrapped error message — no internal
		// slog here to avoid two log lines per refusal.
		observability.Global.LicenseLoadErrorsTotal.WithLabelValues(classifyLoadErr(err)).Inc()
		return fmt.Errorf("license: %w", err)
	}

	state := CheckExpiry(lic)
	days := lic.DaysRemaining()

	observability.Global.LicenseExpiresAt.Set(float64(lic.ExpiresAt.Unix()))
	observability.Global.LicenseDaysRemaining.Set(float64(days))
	observability.Global.LicenseStateInfo.Reset()
	observability.Global.LicenseStateInfo.WithLabelValues(state.String(), lic.CustomerID).Set(1)

	switch state {
	case StateValid:
		slog.Info("license: loaded",
			"license_id", lic.LicenseID,
			"customer_id", lic.CustomerID,
			"contract_id", lic.ContractID,
			"expires_at", lic.ExpiresAt.Format(time.RFC3339),
			"days_remaining", days,
		)
	case StateInGrace:
		slog.Warn("license: in grace period — renewal required",
			"license_id", lic.LicenseID,
			"customer_id", lic.CustomerID,
			"contract_id", lic.ContractID,
			"expires_at", lic.ExpiresAt.Format(time.RFC3339),
			"days_remaining", days,
		)
	case StateExpired:
		// Hard fail. The state gauge already carries `state="expired"` and
		// the operator-facing message (renewal contact + license_id + grace
		// boundaries) lands in die()'s log line via the returned error —
		// not bumping LicenseLoadErrorsTotal here because expiry is not a
		// load error (the JWT loaded fine), it is a contractual refusal.
		// Leaf error — nothing to wrap; expiredMessage already carries all
		// the operator-actionable context.
		return errors.New(expiredMessage(lic))
	}

	SetCurrent(lic)
	return nil
}

// expiredMessage is the operator-facing error printed when the binary refuses
// to start. Mirrors plan §4.9.2 step 4. Includes the renewal contact so the
// operator does not have to grep docs to find out who to mail.
//
// Dates are normalised to UTC before formatting + the "UTC" suffix is
// printed so the operator can't mis-read the day in a timezone other than
// the issuer's. JWT exp is at second granularity, but the printed date
// only carries days — without the UTC suffix a deployment in PT and a JWT
// issued near midnight UTC could show off-by-one dates for the same instant.
func expiredMessage(l *License) string {
	graceEnded := l.ExpiresAt.Add(time.Duration(l.GracePeriodDays) * 24 * time.Hour)
	return fmt.Sprintf(
		"License expired %s UTC (grace ended %s UTC). Contact sales@axiaops.io to renew. License: %s",
		l.ExpiresAt.UTC().Format("2006-01-02"),
		graceEnded.UTC().Format("2006-01-02"),
		l.LicenseID,
	)
}

// classifyLoadErr maps a Load() error to one of the LicenseLoadErrorsTotal
// reason labels documented in metrics.go. Substring-matching is acceptable
// here because the error strings are owned by this package — a future error
// shape change would land alongside a label-name update.
func classifyLoadErr(err error) string {
	if errors.Is(err, ErrNoLicense) {
		return "missing"
	}
	msg := err.Error()
	switch {
	case strings.Contains(msg, "wrong issuer"):
		return "wrong_issuer"
	case strings.Contains(msg, "wrong audience"):
		return "wrong_audience"
	case strings.Contains(msg, "iat"), strings.Contains(msg, "not-before"):
		return "future_iat"
	case strings.Contains(msg, "verify"), strings.Contains(msg, "signature"):
		return "signature"
	default:
		return "format"
	}
}
