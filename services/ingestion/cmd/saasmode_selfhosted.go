//go:build selfhosted

package main

import (
	"time"

	"axiaops.io/shared/entitlement"
	"axiaops.io/shared/storage"
)

// Self-hosted build — the OPT-IN (`-tags selfhosted`, paired with `production`
// for the customer shipping image). The license JWT gates scans; per-tenant
// entitlement is never consulted. This file is the compile-time counterpart of
// saasmode_saas.go (the default): the license-bypass call lives ONLY in the
// default SaaS sibling, so a self-hosted/customer binary built with this tag has
// no code path that disables its license gate (mirrors the production-tag
// DEV_MODE strip). See docs/saas-platform-admin-design.md §7.1.

// bypassLicenseForSaaS is a no-op in self-hosted builds.
func bypassLicenseForSaaS() {}

// entitlementGate returns (nil, 0) so the scan gates run the license predicate.
func entitlementGate(_ storage.Store) (entitlement.Resolver, time.Duration) {
	return nil, 0
}
