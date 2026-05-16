package auth_test

import (
	"context"
	"errors"
	"sync"
	"testing"

	"axiaops.io/api/internal/auth"
	"axiaops.io/shared/model"
)

// captureWriter records every AuditLogWrite call so tests can assert
// on the persisted shape. err is returned to the caller on Write —
// nil for happy-path tests, set for error-path tests.
type captureWriter struct {
	mu     sync.Mutex
	events []model.AuditEvent
	err    error
}

func (c *captureWriter) AuditLogWrite(_ context.Context, e model.AuditEvent) (int64, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.err != nil {
		return 0, c.err
	}
	c.events = append(c.events, e)
	return int64(len(c.events)), nil
}

func TestNewAuditWriterHappyPath(t *testing.T) {
	t.Parallel()
	cw := &captureWriter{}
	writer := auth.NewAuditWriter(cw)

	writer(context.Background(), "org-1", "user-1", model.AuditActionBootstrapCompleted,
		map[string]any{"organization_name": "Acme"})

	cw.mu.Lock()
	defer cw.mu.Unlock()
	if len(cw.events) != 1 {
		t.Fatalf("events = %d; want 1", len(cw.events))
	}
	got := cw.events[0]
	if got.OrganizationID != "org-1" || got.UserID != "user-1" {
		t.Errorf("event = %+v; want org-1/user-1", got)
	}
	if got.Action != model.AuditActionBootstrapCompleted {
		t.Errorf("action = %q; want bootstrap_completed", got.Action)
	}
	if got.Metadata["organization_name"] != "Acme" {
		t.Errorf("metadata not propagated: %+v", got.Metadata)
	}
}

func TestNewAuditWriterDropsEmptyAction(t *testing.T) {
	t.Parallel()
	cw := &captureWriter{}
	writer := auth.NewAuditWriter(cw)
	writer(context.Background(), "org-1", "user-1", "", nil)
	if len(cw.events) != 0 {
		t.Errorf("events = %d; want 0 (empty action must drop)", len(cw.events))
	}
}

func TestNewAuditWriterDropsEmptyOrg(t *testing.T) {
	// audit_log requires organization_id NOT NULL — bypass the DB
	// round-trip when the closure can already see the constraint
	// will fail. Mirrors the audit.Record behaviour.
	t.Parallel()
	cw := &captureWriter{}
	writer := auth.NewAuditWriter(cw)
	writer(context.Background(), "", "user-1", model.AuditActionBootstrapCompleted, nil)
	if len(cw.events) != 0 {
		t.Errorf("events = %d; want 0 (empty org must drop)", len(cw.events))
	}
}

func TestNewAuditWriterDropsEmptyUser(t *testing.T) {
	t.Parallel()
	cw := &captureWriter{}
	writer := auth.NewAuditWriter(cw)
	writer(context.Background(), "org-1", "", model.AuditActionBootstrapCompleted, nil)
	if len(cw.events) != 0 {
		t.Errorf("events = %d; want 0 (empty user must drop)", len(cw.events))
	}
}

func TestNewAuditWriterSwallowsStoreErrors(t *testing.T) {
	// Write failures must NOT propagate — the user response has
	// already shipped. The metric counter is the operator-visible
	// signal.
	t.Parallel()
	cw := &captureWriter{err: errors.New("simulated DB outage")}
	writer := auth.NewAuditWriter(cw)
	// No panic, no return value — this just has to not crash.
	writer(context.Background(), "org-1", "user-1", model.AuditActionBootstrapCompleted, nil)
}
