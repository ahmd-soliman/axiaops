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
// pre-VerifyAtBoot). Callers decide policy on the result — slice 5's
// scan-gate treats StateExpired as 403 and falls through on StateNotLoaded;
// slice 4's ticker watches for transitions and ignores StateNotLoaded
// entirely. Lock-free read.
func SnapshotState() State {
	l := Snapshot()
	if l == nil {
		return StateNotLoaded
	}
	return CheckExpiry(l)
}
