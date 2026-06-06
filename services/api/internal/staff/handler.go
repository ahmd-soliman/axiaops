package staff

import (
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"axiaops.io/api/internal/auth"
	"axiaops.io/api/internal/httpip"
	"axiaops.io/api/internal/httpjson"
)

// Handler serves the admin-plane HTTP surface: staff auth, the read-only
// tenant console, and (superadmin) staff management.
type Handler struct {
	store     Store
	sessions  *SessionManager
	provider  Provider
	loginRate *auth.IPRateLimiter // nil disables login rate limiting
}

// NewHandler wires the admin handler. loginRate may be nil (no limiting).
func NewHandler(store Store, sessions *SessionManager, provider Provider, loginRate *auth.IPRateLimiter) *Handler {
	return &Handler{store: store, sessions: sessions, provider: provider, loginRate: loginRate}
}

// Register attaches all admin routes to mux. WrapStaff (applied by the
// composition root) gates everything except publicAdminPath entries.
func (h *Handler) Register(mux *http.ServeMux) {
	mux.HandleFunc("POST /admin/auth/login", h.login)
	mux.HandleFunc("POST /admin/auth/logout", h.logout)
	mux.HandleFunc("GET /admin/me", h.me)

	// Read-only tenant console (any authenticated staff — design §7.5).
	mux.HandleFunc("GET /admin/tenants", h.listTenants)
	mux.HandleFunc("GET /admin/tenants/{id}", h.getTenant)

	// Staff management (superadmin-gated inside each handler).
	mux.HandleFunc("GET /admin/staff", h.listStaff)
	mux.HandleFunc("POST /admin/staff", h.createStaff)
	mux.HandleFunc("POST /admin/staff/{id}/roles", h.grantRole)
	mux.HandleFunc("DELETE /admin/staff/{id}/roles/{role}", h.revokeRole)
}

// ── auth ────────────────────────────────────────────────────────────────────

type loginRequest struct {
	Email    string `json:"email"`
	Password string `json:"password"`
}

type staffSummary struct {
	StaffUserID string   `json:"staff_user_id"`
	Email       string   `json:"email"`
	Name        string   `json:"name"`
	Roles       []string `json:"roles"`
}

func (h *Handler) login(w http.ResponseWriter, r *http.Request) {
	if h.loginRate != nil {
		if out := h.loginRate.Allow(r.Context(), httpip.Request(r)); !out.Allowed {
			w.Header().Set("Retry-After", retryAfterSeconds(out.RetryAfter))
			writeError(w, http.StatusTooManyRequests, "rate_limited", "too many login attempts")
			return
		}
	}

	var req loginRequest
	if err := httpjson.Decode(w, r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", "malformed body")
		return
	}

	// Collapse every failure (unknown email, wrong password, suspended) to one
	// 401 shape — no staff-account enumeration.
	user, grants, err := h.store.LookupStaffUserByEmail(r.Context(), req.Email)
	if err != nil || !user.Active() || user.PasswordHash == "" {
		// Verify against a decoy hash to keep the timing envelope flat —
		// argon2id dominates login latency, so skipping it on the unknown/
		// suspended/no-hash path would let an attacker time-detect which
		// staff emails are registered. Mirrors the tenant login's
		// placeholderHash guard (auth/handler.go).
		_ = auth.Verify(req.Password, loginTimingDecoyHash)
		invalidStaffCredentials(w)
		return
	}
	if err := auth.Verify(req.Password, user.PasswordHash); err != nil {
		invalidStaffCredentials(w)
		return
	}

	if _, err := h.sessions.Mint(r.Context(), w, r, user.ID); err != nil {
		slog.Error("staff: mint session", "error", err)
		writeError(w, http.StatusInternalServerError, "internal_error", "could not establish session")
		return
	}
	slog.Info("staff: login", "staff_user_id", user.ID, "email", user.Email)
	writeJSON(w, http.StatusOK, staffSummary{
		StaffUserID: user.ID, Email: user.Email, Name: user.Name, Roles: roleStrings(grantsToRoles(grants)),
	})
}

func (h *Handler) logout(w http.ResponseWriter, r *http.Request) {
	h.sessions.Revoke(r.Context(), w, r)
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) me(w http.ResponseWriter, r *http.Request) {
	id, ok := FromContext(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthenticated", "staff authentication required")
		return
	}
	writeJSON(w, http.StatusOK, staffSummary{
		StaffUserID: id.StaffUserID, Email: id.Email, Name: id.Name, Roles: roleStrings(id.Roles),
	})
}

// loginTimingDecoyHash is a well-formed argon2id hash used to equalise login
// latency on the staff-not-found / suspended / no-password paths (see login).
var loginTimingDecoyHash = func() string {
	h, _ := auth.Hash("axiaops-staff-login-timing-equaliser")
	return h
}()

func invalidStaffCredentials(w http.ResponseWriter) {
	// Constant-shape 401; a tiny constant delay is unnecessary here — the
	// argon2id verify on the happy path already dominates timing, and the
	// rate limiter caps probing.
	writeError(w, http.StatusUnauthorized, "invalid_credentials", "invalid email or password")
}

func retryAfterSeconds(d time.Duration) string {
	secs := int(d.Seconds())
	if secs < 1 {
		secs = 1
	}
	return strconv.Itoa(secs)
}
