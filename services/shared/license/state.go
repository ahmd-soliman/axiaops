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

// IsScanAllowed encodes the Option-3 scan-gate policy decided in plan §4.9
// scope intro: only the explicit StateExpired blocks scans; valid, in-grace,
// and not-loaded all fall through.
//
// Both api and ingestion scan-gate sites route through this single predicate.
// If the policy ever widens (e.g. block in-grace per a future B1.7 customer
// signal), the change is one edit here, compiler-verified across every
// consumer — without it the policy was spelled out in 4 places with no
// compiler help (slice 5c review).
func IsScanAllowed() bool {
	return SnapshotState() != StateExpired
}
