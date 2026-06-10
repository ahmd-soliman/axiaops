//go:build !saashosted

package main

import (
	"time"

	"axiaops.io/shared/entitlement"
	"axiaops.io/shared/storage"
)

// Self-hosted build (default — no `saashosted` tag). The license JWT gates
// scans; per-tenant entitlement is never consulted. This file is the compile-
// time counterpart of saasmode_saashosted.go: the license-bypass call lives
// ONLY in the saashosted sibling, so a self-hosted/customer binary has no code
// path that disables its license gate (mirrors the production-tag DEV_MODE
// strip). See docs/saas-platform-admin-design.md §7.1.

// bypassLicenseForSaaS is a no-op in self-hosted builds.
func bypassLicenseForSaaS() {}

// entitlementGate returns (nil, 0) so the scan gates run the license predicate.
func entitlementGate(_ storage.Store) (entitlement.Resolver, time.Duration) {
	return nil, 0
}
