package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"

	"axiaops.io/api/internal/auth"
	"axiaops.io/shared/model"
	"axiaops.io/shared/storage"
)

// runSeedStaff mints the first superadmin (the bootstrap path — there is no
// staff to create one via the API yet). Idempotency is left to the caller:
// re-running with an existing email exits non-zero with staff_email_taken.
//
//	api-admin seed-staff --email a@x.io --name "Ada" --password '…' [--role superadmin]
//
// The password is read from --password or, preferred, the STAFF_SEED_PASSWORD
// env var so it never lands in shell history.
func runSeedStaff(args []string) {
	fs := flag.NewFlagSet("seed-staff", flag.ExitOnError)
	email := fs.String("email", "", "staff email (required)")
	name := fs.String("name", "", "staff display name")
	password := fs.String("password", "", "staff password (or set STAFF_SEED_PASSWORD)")
	role := fs.String("role", string(model.StaffRoleSuperadmin), "comma-free single role to grant")
	_ = fs.Parse(args)

	if *password == "" {
		*password = os.Getenv("STAFF_SEED_PASSWORD")
	}
	if *email == "" || *password == "" {
		die("seed-staff: --email and --password (or STAFF_SEED_PASSWORD) are required")
	}
	staffRole := model.StaffRole(*role)
	if !model.ValidStaffRole(staffRole) {
		die("seed-staff: invalid --role", "role", *role)
	}
	// CLI path — no identity context (email/name) available here, so this
	// deliberately stays on plain CheckPolicy rather than CheckPolicyWithIdentity.
	// Length + breach screening is sufficient for the operator-run seed path; the
	// asymmetry vs the HTTP set-password sites is intentional (see
	// docs/password-breach-check-design.md).
	if err := auth.CheckPolicy(*password); err != nil {
		die("seed-staff: weak password", "error", err.Error())
	}

	ctx := context.Background()
	store := openStore(ctx)
	defer closeStore(store)

	hash, err := auth.Hash(*password)
	if err != nil {
		die("seed-staff: hash failed", "error", err.Error())
	}
	created, err := store.CreateStaffUser(ctx, storage.CreateStaffUserInput{
		Email:        *email,
		Name:         *name,
		PasswordHash: hash,
		Roles:        []model.StaffRole{staffRole},
		// GrantedBy empty → bootstrap grant (no prior staff).
	})
	if errors.Is(err, storage.ErrStaffEmailExists) {
		// Idempotent: re-seeding an existing staff member is a no-op success
		// (a seed command should be safe to re-run; the password is NOT reset).
		fmt.Printf("staff user %s already exists — leaving it unchanged\n", *email)
		return
	}
	if err != nil {
		die("seed-staff: create failed", "error", err.Error())
	}
	fmt.Printf("created staff user %s (%s) with role %s\n", created.ID, created.Email, staffRole)
}
