package auth

import (
	"context"
	"errors"
	"log/slog"
	"net/http"

	"axiaops.io/shared/storage"
)

// MembershipDetails is what MembershipLookup returns for the (org, user)
// pair bound to the session. Both fields are required by callers:
//   - Role is the authorization decision input.
//   - Email is the audit_log.actor_email value written for every mutation,
//     and the body of GET /v1/me.
//
// Empty Role indicates "no membership exists" — Authenticate rejects.
type MembershipDetails struct {
	Role  string
	Email string
}

// MembershipLookup resolves the membership role + user email in a single
// DB call. Composed as a function-typed seam so unit tests can stub
// without standing up a full Store. In production cmd/main.go binds it
// to a single SELECT that joins memberships + users — both fields come
// from the same query, no adapter needed.
type MembershipLookup func(ctx context.Context, organizationID, userID string) (MembershipDetails, error)

// NativeProvider authenticates requests via the session cookie + the
// Manager's cache-aside read path. Membership details (role + email) are
// resolved per-request via the MembershipLookup — sub-millisecond when
// warm in the cache.
//
// Constructed at startup; safe for concurrent use.
type NativeProvider struct {
	mgr        *Manager
	membership MembershipLookup
}

// NewNativeProvider wires a NativeProvider. The composition root in
// cmd/main.go supplies a MembershipLookup that joins memberships + users.
func NewNativeProvider(mgr *Manager, membership MembershipLookup) *NativeProvider {
	return &NativeProvider{mgr: mgr, membership: membership}
}

// Authenticate reads the session cookie, validates the session via the
// Manager (which is cache-aside), then resolves the membership details.
//
// Any failure returns ErrUnauthenticated — the caller (middleware) maps
// every non-nil error to HTTP 401. Internal failure reasons go to slog
// inside the Manager, never out the wire.
func (p *NativeProvider) Authenticate(r *http.Request) (Identity, error) {
	token := ReadSession(r)
	if token == "" {
		return Identity{}, ErrUnauthenticated
	}
	sess, err := p.mgr.ValidateSession(r.Context(), token)
	if err != nil {
		// storage.ErrSessionNotFound is the documented failure mode of
		// ValidateSession; any other error is treated identically on
		// the wire — we never leak the reason. But operators need to
		// see real DB / cache failures in logs to diagnose them, so
		// log everything that ISN'T the expected "not found" case.
		if !errors.Is(err, storage.ErrSessionNotFound) {
			slog.Error("auth: native provider — session validation failed",
				"err", err, "method", r.Method, "path", r.URL.Path)
		}
		return Identity{}, ErrUnauthenticated
	}
	m, err := p.membership(r.Context(), sess.OrganizationID, sess.UserID)
	if err != nil {
		// LookupMembership has no "not found" sentinel (missing rows
		// return zero value, nil error). Any non-nil err is a real
		// DB-side failure worth surfacing.
		slog.Error("auth: native provider — membership lookup failed",
			"err", err, "user_id", sess.UserID, "organization_id", sess.OrganizationID)
		return Identity{}, ErrUnauthenticated
	}
	if m.Role == "" {
		// Session is live but the user has no membership in the bound
		// organization — should be impossible (sessions FK references
		// organizations + memberships), but defend in depth.
		return Identity{}, ErrUnauthenticated
	}
	return Identity{
		UserID:           sess.UserID,
		OrganizationID:   sess.OrganizationID,
		Role:             m.Role,
		Email:            m.Email,
		AuthMode:         string(sess.AuthMode),
		SessionID:        sess.ID,
		SessionTokenHash: sess.SessionTokenHash,
	}, nil
}

