// Command entitlement-seed writes one org's SaaS entitlement row directly,
// going through the same entitlement.ApplyBillingEvent projection a future
// billing webhook will use. It is the manual / dev write path for the dormant
// Phase 2A scaffold (design §7.1) — there is no admin UI or Stripe integration
// yet, so this CLI is how you put a tenant into trialing/active/past_due/etc. on
// dev-1 to exercise the entitlements table (and, once Phase 2B wires the
// default (SaaS) build's scan gates, the scan-gate behaviour).
//
// It connects with the runtime-admin role because the `entitlements` table is
// system-scoped and granted ONLY to axiaops_runtime (migration 033) — the app
// role deliberately cannot touch it.
//
// Usage:
//
//	RUNTIME_ADMIN_DATABASE_URL=postgres://axiaops_runtime:…@host/axiaops \
//	  go run ./services/api/cmd/entitlement-seed \
//	    -org=<organization_id> -status=active -plan=pro -max-accounts=10 \
//	    -period-end=2026-07-01T00:00:00Z
//
// All diagnostics go to stderr; a one-line confirmation goes to stdout.
package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"time"

	"axiaops.io/shared/entitlement"
	"axiaops.io/shared/model"
	"axiaops.io/shared/storage/postgres"
)

func main() {
	if err := run(os.Args[1:], os.Stdout, os.Stderr); err != nil {
		fmt.Fprintln(os.Stderr, "entitlement-seed:", err)
		os.Exit(1)
	}
}

type seedParams struct {
	org         string
	status      string
	plan        string
	maxAccounts int
	periodEnd   string
	trialEnds   string
	databaseURL string
}

func run(args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("entitlement-seed", flag.ContinueOnError)
	fs.SetOutput(stderr)

	var p seedParams
	fs.StringVar(&p.org, "org", "", "Organization ID to seed. Required.")
	fs.StringVar(&p.status, "status", "active", "Entitlement status: trialing|active|past_due|canceled|suspended.")
	fs.StringVar(&p.plan, "plan", "free", "Plan: free|pro|enterprise.")
	fs.IntVar(&p.maxAccounts, "max-accounts", 1, "Connected-account cap for the plan.")
	fs.StringVar(&p.periodEnd, "period-end", "", "Current billing period end (RFC3339). Anchors the past_due grace window. Optional.")
	fs.StringVar(&p.trialEnds, "trial-ends", "", "Trial end (RFC3339). Optional.")
	fs.StringVar(&p.databaseURL, "database-url", "", "Override the runtime-admin connection URL (else $RUNTIME_ADMIN_DATABASE_URL, then $DATABASE_URL).")

	if err := fs.Parse(args); err != nil {
		return err
	}
	if p.org == "" {
		fs.Usage()
		return fmt.Errorf("-org is required")
	}
	if !model.ValidEntitlementStatus(model.EntitlementStatus(p.status)) {
		return fmt.Errorf("invalid -status %q (want trialing|active|past_due|canceled|suspended)", p.status)
	}

	evt := entitlement.BillingEvent{
		OrganizationID: p.org,
		Plan:           p.plan,
		Status:         model.EntitlementStatus(p.status),
		MaxAccounts:    p.maxAccounts,
	}
	if p.periodEnd != "" {
		t, err := time.Parse(time.RFC3339, p.periodEnd)
		if err != nil {
			return fmt.Errorf("parse -period-end: %w", err)
		}
		evt.CurrentPeriodEnd = &t
	}
	if p.trialEnds != "" {
		t, err := time.Parse(time.RFC3339, p.trialEnds)
		if err != nil {
			return fmt.Errorf("parse -trial-ends: %w", err)
		}
		evt.TrialEndsAt = &t
	}

	dbURL := p.databaseURL
	if dbURL == "" {
		dbURL = os.Getenv("RUNTIME_ADMIN_DATABASE_URL")
	}
	if dbURL == "" {
		dbURL = os.Getenv("DATABASE_URL")
	}
	if dbURL == "" {
		return fmt.Errorf("no connection URL — set -database-url, RUNTIME_ADMIN_DATABASE_URL, or DATABASE_URL (must be the axiaops_runtime role; entitlements is not granted to the app role)")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	// Connect the same URL as both pools: UpsertEntitlement runs on adminPool,
	// and for this CLI that role IS the runtime-admin role.
	store, err := postgres.NewWithRuntimeAdmin(ctx, dbURL, dbURL)
	if err != nil {
		return fmt.Errorf("connect: %w", err)
	}
	defer func() { _ = store.Close() }()

	if err := entitlement.ApplyBillingEvent(ctx, store, evt); err != nil {
		return fmt.Errorf("seed: %w", err)
	}

	_, _ = fmt.Fprintf(stdout, "entitlement set: org=%s status=%s plan=%s max_accounts=%d\n", p.org, p.status, p.plan, p.maxAccounts)
	return nil
}
