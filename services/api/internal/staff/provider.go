package staff

import (
	"net/http"

	"axiaops.io/shared/model"
)

// SessionProvider authenticates admin-plane requests from the staff session
// cookie: resolve token → cache → staff_user_id, then re-read the staff row so
// roles + status are always fresh (a revoked role or suspended account takes
// effect on the next request, not at next login).
type SessionProvider struct {
	store    Store
	sessions *SessionManager
}

// NewSessionProvider wires the provider.
func NewSessionProvider(store Store, sessions *SessionManager) *SessionProvider {
	return &SessionProvider{store: store, sessions: sessions}
}

var _ Provider = (*SessionProvider)(nil)

// Authenticate resolves the request to a staff Identity, or ErrUnauthenticated.
func (p *SessionProvider) Authenticate(r *http.Request) (Identity, error) {
	staffUserID, tokenHash, ok := p.sessions.resolve(r.Context(), readStaffCookie(r))
	if !ok {
		return Identity{}, ErrUnauthenticated
	}
	user, grants, err := p.store.GetStaffUserByID(r.Context(), staffUserID)
	if err != nil || !user.Active() {
		// Unknown / deleted / suspended staff → unauthenticated. Never leak
		// which (mirrors the tenant provider's fixed-401 discipline).
		return Identity{}, ErrUnauthenticated
	}
	roles := make([]model.StaffRole, 0, len(grants))
	for _, g := range grants {
		roles = append(roles, g.Role)
	}
	return Identity{
		StaffUserID: user.ID,
		Email:       user.Email,
		Name:        user.Name,
		Roles:       roles,
		TokenHash:   tokenHash,
	}, nil
}
