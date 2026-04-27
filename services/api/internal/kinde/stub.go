package kinde

import (
	"context"
	"fmt"
	"strings"
	"sync"
)

// Stub is an in-memory implementation of Client used by DEV_MODE and tests.
// No network calls; deterministic IDs derived from email so tests can assert
// on them without setting up an httptest server.
type Stub struct {
	mu              sync.Mutex
	invitedEmails   map[string]string // email → kindeUserID we minted
	removedUserIDs  []string          // log of RemoveUser calls
	renamedOrgs     map[string]string // orgCode → last-set name
	failNextInvite  error             // optional injected failure (consumed once)
	failNextRemove  error
	failNextRename  error
}

// NewStub returns an empty in-memory Kinde client.
func NewStub() *Stub {
	return &Stub{
		invitedEmails: map[string]string{},
		renamedOrgs:   map[string]string{},
	}
}

// FailNextInvite makes the next InviteUser return err. Consumed once.
func (s *Stub) FailNextInvite(err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.failNextInvite = err
}

// FailNextRemove makes the next RemoveUser return err. Consumed once.
func (s *Stub) FailNextRemove(err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.failNextRemove = err
}

// FailNextRename makes the next RenameOrganization return err. Consumed once.
func (s *Stub) FailNextRename(err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.failNextRename = err
}

// RemovedUserIDs returns the IDs RemoveUser was called with, in order.
func (s *Stub) RemovedUserIDs() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]string, len(s.removedUserIDs))
	copy(out, s.removedUserIDs)
	return out
}

// OrgName returns the last name passed to RenameOrganization for orgCode,
// or "" if RenameOrganization was never called for that code.
func (s *Stub) OrgName(orgCode string) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.renamedOrgs[orgCode]
}

// InviteUser implements Client. Returns deterministic IDs derived from email.
func (s *Stub) InviteUser(_ context.Context, orgCode, email, _ string) (string, string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.failNextInvite != nil {
		err := s.failNextInvite
		s.failNextInvite = nil
		return "", "", err
	}
	uid := "kinde-user-" + strings.ToLower(email)
	iid := "kinde-inv-" + orgCode + "-" + strings.ToLower(email)
	s.invitedEmails[strings.ToLower(email)] = uid
	return iid, uid, nil
}

// RemoveUser implements Client. Records the call; idempotent.
func (s *Stub) RemoveUser(_ context.Context, _, kindeUserID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.failNextRemove != nil {
		err := s.failNextRemove
		s.failNextRemove = nil
		return err
	}
	s.removedUserIDs = append(s.removedUserIDs, kindeUserID)
	return nil
}

// RenameOrganization implements Client. Records the rename in-memory.
func (s *Stub) RenameOrganization(_ context.Context, orgCode, name string) error {
	if orgCode == "" {
		return fmt.Errorf("kinde stub: rename organization: orgCode required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.failNextRename != nil {
		err := s.failNextRename
		s.failNextRename = nil
		return err
	}
	s.renamedOrgs[orgCode] = name
	return nil
}
