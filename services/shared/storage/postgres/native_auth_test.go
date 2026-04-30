package postgres_test

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"

	"axiaops.io/shared/model"
	"axiaops.io/shared/storage"
)

// LookupMembership is the hot-path helper used by the native auth provider
// — every authenticated request hits it. The integration test pins the
// SQL behaviour: empty inputs are no-ops, present rows return role+email
// in one call, missing rows return ("","") with no error.

func TestLookupMembership_ReturnsRoleAndEmail(t *testing.T) {
	s := newTestStore(t)
	ctx, org := newOrgCtx(t, s)
	u := seedUser(t, s, org.ID, "alice@example.com")
	if err := s.SaveMembership(ctx, model.Membership{
		ID: uuid.NewString(), OrganizationID: org.ID, UserID: u.ID, Role: "admin",
	}); err != nil {
		t.Fatalf("SaveMembership: %v", err)
	}

	role, email, err := s.LookupMembership(context.Background(), org.ID, u.ID)
	if err != nil {
		t.Fatalf("LookupMembership: %v", err)
	}
	if role != "admin" {
		t.Errorf("role = %q; want admin", role)
	}
	if email != "alice@example.com" {
		t.Errorf("email = %q; want alice@example.com", email)
	}
}

func TestLookupMembership_NoRowReturnsEmpty(t *testing.T) {
	s := newTestStore(t)
	_, org := newOrgCtx(t, s)

	role, email, err := s.LookupMembership(context.Background(), org.ID, "non-existent-user")
	if err != nil {
		t.Fatalf("LookupMembership: %v", err)
	}
	if role != "" || email != "" {
		t.Errorf("missing membership returned (%q, %q); want empty", role, email)
	}
}

func TestLookupMembership_EmptyInputsAreNoop(t *testing.T) {
	s := newTestStore(t)
	role, email, err := s.LookupMembership(context.Background(), "", "")
	if err != nil {
		t.Fatalf("LookupMembership: %v", err)
	}
	if role != "" || email != "" {
		t.Errorf("empty inputs returned (%q, %q); want empty", role, email)
	}
}

func TestLookupMembership_UserExistsWithoutMembership(t *testing.T) {
	// User row exists but membership is missing — could happen after
	// admin removes a member while their session is still cached. The
	// SELECT JOIN returns no rows; LookupMembership returns ("", "", nil).
	// Provider treats that as "no membership" and rejects.
	s := newTestStore(t)
	_, org := newOrgCtx(t, s)
	u := seedUser(t, s, org.ID, "orphan@example.com")
	// Deliberately skip SaveMembership — user exists, no membership.
	role, email, err := s.LookupMembership(context.Background(), org.ID, u.ID)
	if err != nil {
		t.Fatalf("LookupMembership: %v", err)
	}
	if role != "" || email != "" {
		t.Errorf("user without membership returned (%q, %q); want empty", role, email)
	}
}

// LookupUserByEmail backs POST /v1/auth/login. Verifies the global
// (RLS-bypassing) lookup behaviour: case-insensitive email match,
// memberships across orgs, and ErrUserNotFound on miss.

func TestLookupUserByEmail_HappyPath(t *testing.T) {
	s := newTestStore(t)
	ctx, org := newOrgCtx(t, s)
	u := seedUser(t, s, org.ID, "alice@example.com")
	if err := s.SaveMembership(ctx, model.Membership{
		ID: uuid.NewString(), OrganizationID: org.ID, UserID: u.ID, Role: "member",
	}); err != nil {
		t.Fatalf("SaveMembership: %v", err)
	}

	got, mships, err := s.LookupUserByEmail(context.Background(), "alice@example.com")
	if err != nil {
		t.Fatalf("LookupUserByEmail: %v", err)
	}
	if got.ID != u.ID {
		t.Errorf("user.ID = %q; want %q", got.ID, u.ID)
	}
	if len(mships) != 1 || mships[0].Role != "member" {
		t.Errorf("memberships = %+v; want one with role=member", mships)
	}
}

func TestLookupUserByEmail_CaseInsensitive(t *testing.T) {
	s := newTestStore(t)
	_, org := newOrgCtx(t, s)
	seedUser(t, s, org.ID, "Mixed@Example.com")

	got, _, err := s.LookupUserByEmail(context.Background(), "mixed@example.com")
	if err != nil {
		t.Fatalf("LookupUserByEmail: %v", err)
	}
	if got.Email != "Mixed@Example.com" {
		t.Errorf("expected canonical-case email round-trip, got %q", got.Email)
	}
}

func TestLookupUserByEmail_NotFound(t *testing.T) {
	s := newTestStore(t)
	_, _, err := s.LookupUserByEmail(context.Background(), "ghost@example.com")
	if !errors.Is(err, storage.ErrUserNotFound) {
		t.Fatalf("expected ErrUserNotFound, got %v", err)
	}
}

func TestLookupUserByEmail_EmptyInput(t *testing.T) {
	s := newTestStore(t)
	_, _, err := s.LookupUserByEmail(context.Background(), "")
	if !errors.Is(err, storage.ErrUserNotFound) {
		t.Fatalf("empty email should return ErrUserNotFound, got %v", err)
	}
}
