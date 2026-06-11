package license

import (
	"errors"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"time"

	"axiaops.io/shared/observability"
)

// VerifyAtBoot is the single entry point cmd/main.go calls to classify the
// license before serverbuild composes the rest of the API.
//
// Per the B1.6 amendment (docs/b1.6-amendment-feature-gating.md), this
// function never returns an error for the *missing-license* / *expired*
// cases — those soft-fail to a scan-gated runtime. The outcomes are:
//
//   - DEV_MODE + dev fixture compiled in (B1.7 layer 4 / issue #75)
//                          → loads the embedded 100-year dev fixture via Load,
//                            SetCurrent stores the parsed License with
//                            customer_id="axiaops-dev-fixture", scan-gate
//                            falls through via state=valid (NOT via the
//                            legacy enforcementBypass shortcut). Dev now
//                            exercises the same Load → CheckExpiry → state
//                            chain as a real customer. slog.Info confirms
//                            the fixture path.
//   - DEV_MODE + license configured → **layer 2 anti-tamper refusal**: returns
//                          a non-nil error so cmd/main.go's die() fires. This
//                          is the one case VerifyAtBoot is allowed to refuse;
//                          the threat is "operator with shell access flips
//                          DEV_MODE to bypass an installed license." Plan
//                          §4.10.2 layer 2.
//   - Valid / in-grace   → SetCurrent stores the parsed License; gauges set;
//                          slog.Info or slog.Warn carries the operator
//                          context. Scan-gate falls through.
//   - StateExpired       → SetCurrent still stores the parsed License (so
//                          /v1/version reports state="expired" with the full
//                          claim sub-object); gauges set with state="expired";
//                          slog.Error carries the renewal contact and
//                          license_id; binary continues running. Scan-gate
//                          blocks via license.IsScanAllowed → 403
//                          license_expired.
//   - Missing / load err → LicenseLoadErrorsTotal bumped with the classified
//                          reason; slog.Error explains how to install (env
//                          var or file path); no Snapshot is set; binary
//                          continues running. Scan-gate blocks via
//                          IsScanAllowed → 403 license_not_loaded.
//
// Why log+continue rather than os.Exit for missing/expired: industry-standard
// graceful degradation, customer trust posture, operational fragility (this
// runs before DB init by design — a corrupted JWT or NTP step would
// otherwise take the whole service down). Full rationale in the amendment.
//
// Why hard-refuse for DEV_MODE + license configured: the inverse posture.
// An operator who installed a license clearly intended to run licensed; if
// they ALSO set DEV_MODE=true, that's deliberate-bypass intent (or a
// catastrophic misconfig); either way refusing is correct.
//
// Audit-row writes are intentionally NOT performed here. License events are
// binary-wide (no organization_id) and the audit_log table is org-scoped +
// RLS-isolated; the durable trace is the structured slog line plus the
// Prometheus metrics. See plan §4.9.3 for the full rationale.
func VerifyAtBoot(devMode bool) error {
	if devMode {
		// Layer 2 anti-tamper (plan §4.10.2): if a license is configured —
		// either via the env var OR a file at the resolved path — refuse to
		// honour DEV_MODE. The "license is present" signal is what tells us
		// this is a production posture and DEV_MODE is being used as a
		// bypass tool, not a developer convenience. Doesn't catch an
		// attacker who removes the license first; layer 3 (build-tag
		// stripping for customer binaries) is the structural defense for
		// that. See docs/b1.6-amendment-feature-gating.md for layer
		// taxonomy.
		if path, present := licensePresent(); present {
			// Increment BEFORE returning so a watchdog scraping /metrics can
			// observe the refusal even though the binary is about to exit
			// (cmd/main.go's die() flushes logs but does not flush
			// Prometheus to a push gateway). Prefers the alert-runbook
			// "dev_mode_with_license >0 firings" pattern.
			observability.Global.LicenseLoadErrorsTotal.WithLabelValues("dev_mode_with_license").Inc()
			return fmt.Errorf(
				"license: DEV_MODE=true is forbidden when a license is configured (%s). "+
					"This is layer-2 anti-tamper enforcement (plan §4.10.2). "+
					"To run in DEV_MODE, remove the license source first; to run with the "+
					"license, unset DEV_MODE. See docs/sso-implementation-plan.md §4.10.2 "+
					"and docs/b1.6-amendment-feature-gating.md",
				path,
			)
		}
		// B1.7 layer 4 (issue #75): instead of flipping enforcementBypass,
		// load the embedded dev fixture and run it through the same
		// classification chain a real customer license travels. Closes the
		// dev/prod parity gap — Load(), CheckExpiry(), IsScanAllowedForState()
		// are all exercised in dev. The fixture is signed by a dev-only key
		// (devEmbeddedPubKeyPEM); production-tagged binaries zero both the
		// fixture and the dev pubkey, so this branch literally cannot fire
		// in a customer build (cmd/devmode_production.go also hard-wires
		// devModeEnabled() to false — both seams must regress simultaneously
		// for the bypass channel to re-open).
		if len(devFixtureJWT) == 0 {
			// Fallback: dev fixture not compiled in — possible only in a
			// production-tagged build that somehow set devMode=true. Treat
			// as missing-license rather than a hard refuse so the test
			// suites that cross-build with -tags production don't regress
			// on a corner case the runtime never actually sees.
			slog.Error("license: DEV_MODE without an embedded dev fixture — install a real license or run a default build")
			observability.Global.LicenseLoadErrorsTotal.WithLabelValues("missing").Inc()
			return nil
		}
		lic, err := loadFromJWT(string(devFixtureJWT))
		if err != nil {
			// Embedded fixture failed to verify — package-level bug, surface
			// loud. Don't fall through to the real-license code path because
			// the dev fixture being broken doesn't mean the operator wants
			// strict licensing in dev.
			slog.Error("license: dev fixture failed to load — likely embed mismatch", "err", err)
			observability.Global.LicenseLoadErrorsTotal.WithLabelValues("format").Inc()
			return nil
		}
		state := CheckExpiry(lic)
		days := lic.DaysRemaining()
		observability.Global.LicenseExpiresAt.Set(float64(lic.ExpiresAt.Unix()))
		observability.Global.LicenseDaysRemaining.Set(float64(days))
		observability.Global.LicenseStateInfo.Reset()
		observability.Global.LicenseStateInfo.WithLabelValues(state.String(), lic.CustomerID).Set(1)
		SetCurrent(lic)
		slog.Info("license: DEV_MODE — using embedded dev fixture",
			"customer_id", lic.CustomerID,
			"days_remaining", days,
		)
		return nil
	}

	// SaaS default build (design §7.1): cmd/saasmode_saas.go flips the
	// enforcement bypass BEFORE this call, the license JWT is dormant, and
	// scans gate on per-tenant entitlement instead. Running without a license
	// is the EXPECTED posture here — falling through would log "scans will be
	// blocked" at ERROR (false: the gates bypass the license) and bump
	// LicenseLoadErrorsTotal{reason="missing"}, which pairs with the
	// missing-license alert, on every SaaS boot. Skip the load entirely:
	// /v1/version collapses to {state:"managed"} whenever the bypass is set
	// (a snapshot would be invisible anyway) and RunTicker no-ops on a nil
	// snapshot. The `-tags selfhosted` build never sets the bypass, so the
	// licensed path below is unchanged for customer binaries.
	if IsEnforcementBypassed() {
		slog.Info("license: dormant — enforcement bypassed (SaaS build); scans gate on per-tenant entitlement")
		return nil
	}

	lic, err := Load()
	if err != nil {
		// The reason label drives the alert runbook (slice 9 docs). Under
		// the B1.6 amendment we no longer refuse-at-boot, so this slog.Error
		// is the operator's only durable signal that the binary is running
		// without a license — pair it with `LicenseLoadErrorsTotal{reason="missing"}>0`
		// in the Prometheus alert rules.
		reason := classifyLoadErr(err)
		observability.Global.LicenseLoadErrorsTotal.WithLabelValues(reason).Inc()
		slog.Error("license: not loaded — scans will be blocked",
			"reason", reason,
			"detail", err.Error(),
			"action", "set AXIAOPS_LICENSE or AXIAOPS_LICENSE_PATH; see docs/license-issuance.md",
		)
		// Leave Snapshot at nil and enforcementBypass at false. IsScanAllowed
		// returns false, the scan-gate 403s with license_not_loaded, the
		// dashboard banner shows the install URL.
		return nil
	}

	state := CheckExpiry(lic)
	days := lic.DaysRemaining()

	observability.Global.LicenseExpiresAt.Set(float64(lic.ExpiresAt.Unix()))
	observability.Global.LicenseDaysRemaining.Set(float64(days))
	observability.Global.LicenseStateInfo.Reset()
	observability.Global.LicenseStateInfo.WithLabelValues(state.String(), lic.CustomerID).Set(1)

	// SetCurrent up front so /v1/version reports the loaded license claims
	// regardless of state — `state="expired"` with the full sub-object is
	// more actionable for operators than "no license" + a separate metric.
	SetCurrent(lic)

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
		// Per the B1.6 amendment: log loud, leave the snapshot in place so
		// /v1/version reports state="expired" with the full claim sub-object,
		// and let IsScanAllowed handle the actual gating at the scan path.
		// Not bumping LicenseLoadErrorsTotal — the JWT loaded fine; expiry
		// is a contractual refusal, surfaced via LicenseStateInfo above.
		slog.Error("license: expired past grace — scans will be blocked",
			"detail", expiredMessage(lic),
		)
	}

	return nil
}

// licensePresent reports whether a license is configured via env var or via
// a readable file at the resolved path (EnvLicensePath, defaulting to
// DefaultLicensePath). Used by layer 2 anti-tamper to distinguish
// "DEV_MODE in a dev slot with no license — fine" from "DEV_MODE on a host
// with a real license installed — refuse." Returns (source, present); the
// source string is the operator-facing identifier for the error message.
//
// We intentionally do NOT call Load() here: layer 2 fires before the JWT is
// parsed, so a corrupt-license + DEV_MODE attempt also refuses (correct —
// you can't bypass the layer by tampering with the JWT contents). The check
// is purely "is something there", not "is what's there valid."
//
// For the env-var case the source string is the descriptive
// "the AXIAOPS_LICENSE environment variable" rather than the literal JWT
// (which would leak to logs) and rather than `$AXIAOPS_LICENSE` (which
// looks like an unresolved shell variable in JSON log aggregators like
// Loki / CloudWatch Logs Insights). For the file case it is the absolute
// resolved path so the operator can grep/inspect the source.
func licensePresent() (string, bool) {
	if os.Getenv(EnvLicense) != "" {
		return "the " + EnvLicense + " environment variable", true
	}
	path := os.Getenv(EnvLicensePath)
	if path == "" {
		path = DefaultLicensePath
	}
	if _, err := os.Stat(path); err == nil {
		return path, true
	}
	return "", false
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
