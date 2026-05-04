package license

import "sync/atomic"

// current holds the boot-time *License so handlers (slice 5 scan-gate) and
// the runtime ticker (slice 4) can read the validated claims without
// re-running JWT parsing on every call. Set once during cmd/main.go startup
// via SetCurrent; nil under DEV_MODE or in the SaaS binary.
//
// This is the only package-level mutable state; the rest of the package is
// pure functions (license.go) so unit tests can run without disturbing it.
var current atomic.Pointer[License]

// enforcementBypass is the explicit "this binary is exempt from license
// enforcement" flag. Set by VerifyAtBoot when DEV_MODE=true; the future SaaS
// composition root will set it through the same predicate. Distinct from
// `current == nil` (which under the B1.6 amendment now means "no license
// installed in production" and gates scans) so the gate can tell DEV_MODE
// apart from missing-license without re-reading os.Getenv.
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
// Pass nil to clear (DEV_MODE bypass / SaaS binary).
func SetCurrent(l *License) {
	current.Store(l)
}

// Snapshot returns the boot-time License or nil when no license is loaded
// (DEV_MODE / SaaS binary). Lock-free read — safe under concurrency.
//
// The returned pointer is **read-only by contract** — callers must not
// mutate any field. The same pointer is shared by the runtime ticker for
// transition detection; mutation would corrupt the next-tick comparison.
// Go does not enforce this; the contract lives here.
func Snapshot() *License {
	return current.Load()
}

// SnapshotState classifies the boot-time license against time.Now(), or
// returns StateNotLoaded when no license is set (DEV_MODE / SaaS binary /
// pre-VerifyAtBoot). Lock-free read.
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
// Called by VerifyAtBoot under DEV_MODE; the future SaaS composition root
// (cmd/api-saashosted, planned per §4.9.6) will call it directly so its scan
// path falls through. Once set, never cleared during the process lifetime.
//
// Tests use a t.Cleanup pattern (see resetEnforcementBypass) to keep the
// flag from leaking across test cases.
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
// Re-reads the snapshot internally each call. Callers that need the state
// for downstream branching (e.g. picking a 403 error code) should use
// IsScanAllowedForState instead — taking SnapshotState() once and passing
// it through avoids the TOCTOU window where a wall-clock cross-tick of
// `exp` between two consecutive reads can re-classify the license.
//
// Both api and ingestion scan-gate sites route through this single predicate.
// Widening or narrowing the policy (e.g. blocking in-grace per a future
// customer signal) is one edit in IsScanAllowedForState, compiler-verified
// across every consumer.
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
// Handlers consult this to distinguish DEV_MODE (no banner, no 403, scans
// fall through) from production-with-no-license (banner, 403, scans gated).
//
// The /v1/version endpoint and the scan-gate handler both branch on this so
// the dashboard and the API agree on which "no snapshot" semantics apply.
func IsEnforcementBypassed() bool {
	return enforcementBypass.Load()
}
