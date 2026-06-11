package license

import "sync/atomic"

// current holds the boot-time *License so handlers (slice 5 scan-gate) and
// the runtime ticker (slice 4) can read the validated claims without
// re-running JWT parsing on every call. Set once during cmd/main.go startup
// via SetCurrent. After B1.7 layer 4 (issue #75) DEV_MODE also populates
// this with the embedded dev fixture — `current == nil` now means "no
// license loaded in a production deployment" or "this is the default (SaaS)
// build" (which bypasses license enforcement via the build-tag seam).
//
// This is the only package-level mutable state; the rest of the package is
// pure functions (license.go) so unit tests can run without disturbing it.
var current atomic.Pointer[License]

// enforcementBypass is the explicit "this binary is exempt from license
// enforcement" flag. Set by the default (SaaS) build via the build-tag seam
// (services/{api,ingestion}/cmd/saasmode_saas.go); the `-tags selfhosted`
// (opt-in) licensed build never sets it. DEV_MODE used to flip
// this flag; layer 4 retired that shortcut by loading the embedded dev
// fixture through the same Load → CheckExpiry chain a real customer
// license travels, so dev exercises the full state machine.
//
// Test helpers (`scheduler_test.go`, `test_main_test.go`, etc.) still call
// SetEnforcementBypass directly to skip license setup in tests under
// check that are orthogonal to license behaviour. That's a test-time
// convenience, distinct from the runtime contract.
//
// atomic.Bool not atomic.Pointer because the value is two-valued and never
// needs to compare against a struct pointer; the lock-free read shape matches
// IsScanAllowed's hot path.
var enforcementBypass atomic.Bool

// SetCurrent stores the boot-time License. Called once from VerifyAtBoot
// after Load + classification succeed; subsequent calls overwrite (the runtime
// ticker never calls SetCurrent — license re-issuance lands via restart, not
// hot-reload).
//
// Pass nil to clear (test cleanup, future SaaS composition root if it skips
// license loading entirely). Self-hosted binaries always have a non-nil
// snapshot post B1.7 layer 4 — DEV_MODE loads the dev fixture and real
// production loads the customer license, both via Load.
func SetCurrent(l *License) {
	current.Store(l)
}

// Snapshot returns the boot-time License or nil when no license is loaded
// (production-without-a-license, or the future SaaS binary). Self-hosted
// dev binaries return the embedded fixture per B1.7 layer 4. Lock-free
// read — safe under concurrency.
//
// The returned pointer is **read-only by contract** — callers must not
// mutate any field. The same pointer is shared by the runtime ticker for
// transition detection; mutation would corrupt the next-tick comparison.
// Go does not enforce this; the contract lives here.
func Snapshot() *License {
	return current.Load()
}

// SnapshotState classifies the boot-time license against time.Now(), or
// returns StateNotLoaded when no license is set (production-without-a-
// license, future SaaS binary, or pre-VerifyAtBoot). Lock-free read.
//
// Most call sites should prefer IsScanAllowed (the policy-encoded predicate);
// SnapshotState is the right call only when the caller needs the specific
// state value — e.g. the ticker's transition detection, or the /v1/version
// handler exposing state to the dashboard banner.
func SnapshotState() State {
	l := Snapshot()
	if l == nil {
		return StateNotLoaded
	}
	return CheckExpiry(l)
}

// SetEnforcementBypass marks this process as exempt from license enforcement.
// Called by the default (SaaS) build via the build-tag seam
// (services/{api,ingestion}/cmd/saasmode_saas.go) and by test helpers that skip
// license setup (`scheduler_test.go`,
// `test_main_test.go`). VerifyAtBoot used to flip this flag under DEV_MODE; B1.7
// layer 4 retired that shortcut and dev now loads the embedded fixture
// instead. Once set, never cleared during a real process lifetime.
//
// Tests use a t.Cleanup pattern (see resetSnapshot in startup_test.go) to
// keep the flag from leaking across test cases.
func SetEnforcementBypass() {
	enforcementBypass.Store(true)
}

// ClearEnforcementBypass is the test-only counterpart to SetEnforcementBypass.
// Production code never calls this; if you find yourself reaching for it
// outside a t.Cleanup, the design has drifted.
func ClearEnforcementBypass() {
	enforcementBypass.Store(false)
}

// IsScanAllowed encodes the post-amendment scan-gate policy
// (docs/b1.6-amendment-feature-gating.md): scans run only when the binary is
// either explicitly exempted (DEV_MODE / future SaaS) OR holds a license in
// StateValid / StateInGrace. StateExpired AND StateNotLoaded both block —
// "no license installed" is no longer a fall-through under the amendment, it
// is a gated state with its own banner copy and 403 error code.
//
// **Use this form for log-only / drop-the-job gate sites** where the caller
// only needs the boolean and doesn't branch on state for downstream output
// (e.g. error-code selection). Examples:
//   - services/ingestion/cmd/main.go scanScheduledAccounts (skip + slog.Info)
//   - services/ingestion/cmd/worker.go scan dequeue (skip + slog.Info)
//
// **Use IsScanAllowedForState** when the caller reads SnapshotState anyway
// to pick a 403 body or a banner string — taking the snapshot once and
// passing it through avoids the wall-clock-driven TOCTOU window where a
// cross-tick of `exp` between two consecutive reads could reclassify the
// license. Examples:
//   - services/api/internal/api/handler.go scanAccount (gates → scanGateBody)
//   - services/ingestion/cmd/main.go POST /scan handler (gates → switch on state)
//
// Both forms share IsScanAllowedForState's policy implementation; widening
// or narrowing (e.g. blocking in-grace per a future customer signal) is one
// edit in that function, compiler-verified across every consumer.
func IsScanAllowed() bool {
	return IsScanAllowedForState(SnapshotState())
}

// IsScanAllowedForState is the state-explicit form of IsScanAllowed. Used by
// callers that already read SnapshotState() and want to gate + branch on the
// same state without a second read — avoids the microsecond TOCTOU window
// where time.Now() advancing across `exp` between two consecutive reads can
// reclassify the license. The enforcement-bypass read is consulted here too
// (single source of policy truth — adding a new bypassed-state would require
// a single edit here), but the bypass flag is set once at boot and never
// changes thereafter, so re-reading it is free of the wall-clock concern.
func IsScanAllowedForState(state State) bool {
	if enforcementBypass.Load() {
		return true
	}
	return state == StateValid || state == StateInGrace
}

// IsEnforcementBypassed reports whether the enforcement-bypass flag is set.
// Returns false in every `-tags selfhosted` (opt-in licensed) runtime path
// post B1.7 layer 4 — DEV_MODE loads the dev fixture (state=valid drives
// scan-gate fall-through) and real production loads a customer license. The
// flag is set in the default (SaaS) build via the build-tag seam
// (services/{api,ingestion}/cmd/saasmode_saas.go), which exempts it from
// scan-gating in favour of the per-tenant entitlement gate.
//
// The /v1/version endpoint branches on this so dashboard and API agree on
// which "no snapshot" semantics apply when SetEnforcementBypass is in
// effect (the default SaaS build).
func IsEnforcementBypassed() bool {
	return enforcementBypass.Load()
}
