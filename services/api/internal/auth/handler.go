package auth

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"strings"

	"github.com/google/uuid"

	"axiaops.io/shared/model"
	"axiaops.io/shared/observability"
	"axiaops.io/shared/storage"
)

// Handler exposes the unauthenticated POST endpoints used to acquire a
// session: /v1/auth/bootstrap (first-owner install), /v1/auth/login
// (email + password), /v1/auth/logout (cookie-bound but tolerant).
//
// Constructed once at startup. All endpoints are intentionally NOT
// guarded by WrapNative — they're how callers obtain authentication.
// publicPath() in middleware/auth.go matches the /v1/auth/ prefix to
// keep them out of the WrapNative chain.
type Handler struct {
	store     storage.NativeAuthStore
	sessions  *Manager
	cookieCfg CookieConfig
	auditFn   AuditWriter
}

// AuditWriter is the seam for hooking audit_log writes from this
// package without dragging the audit/ helper package in transitively.
// In production main.go wires it as a closure over the audit package.
// Pass nil to disable audit writes — useful in tests.
type AuditWriter func(ctx context.Context, organizationID, userID, action string, metadata map[string]any)

// NewHandler returns a wired Handler. cookieCfg is the same value the
// app middleware uses (DEV_MODE flips Secure off). auditFn may be nil.
func NewHandler(store storage.NativeAuthStore, sessions *Manager, cookieCfg CookieConfig, auditFn AuditWriter) *Handler {
	return &Handler{
		store:     store,
		sessions:  sessions,
		cookieCfg: cookieCfg,
		auditFn:   auditFn,
	}
}

// Register attaches the auth routes to the supplied mux. Endpoints:
//
//	POST /v1/auth/bootstrap   first-owner install token redemption
//	POST /v1/auth/login       email + password → session cookie
//	POST /v1/auth/logout      revoke + clear cookie
func (h *Handler) Register(mux *http.ServeMux) {
	mux.HandleFunc("POST /v1/auth/bootstrap", h.bootstrap)
	mux.HandleFunc("POST /v1/auth/login", h.login)
	mux.HandleFunc("POST /v1/auth/logout", h.logout)
}

// ── POST /v1/auth/bootstrap ─────────────────────────────────────────────────

type bootstrapRequest struct {
	Token            string `json:"token"`
	Email            string `json:"email"`
	Password         string `json:"password"`
	Name             string `json:"name"`
	OrganizationName string `json:"organization_name"`
}

type bootstrapResponse struct {
	User user      `json:"user"`
	Org  orgRecord `json:"organization"`
}

type user struct {
	ID    string `json:"id"`
	Email string `json:"email"`
	Name  string `json:"name"`
	Role  string `json:"role"`
}

type orgRecord struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

func (h *Handler) bootstrap(w http.ResponseWriter, r *http.Request) {
	var req bootstrapRequest
	if err := decodeJSON(w, r, &req); err != nil {
		writeAuthError(w, http.StatusBadRequest, "bad_request", "invalid JSON body")
		return
	}
	req.Email = strings.TrimSpace(req.Email)
	req.Name = strings.TrimSpace(req.Name)
	req.OrganizationName = strings.TrimSpace(req.OrganizationName)

	if req.Token == "" || req.Email == "" || req.Password == "" || req.Name == "" {
		writeAuthError(w, http.StatusBadRequest, "bad_request", "token, email, password, name required")
		return
	}
	if req.OrganizationName == "" {
		req.OrganizationName = "AxiaOps"
	}
	if err := CheckPolicy(req.Password); err != nil {
		writeAuthError(w, http.StatusBadRequest, "weak_password", err.Error())
		return
	}

	passwordHash, err := Hash(req.Password)
	if err != nil {
		slog.Error("auth: bootstrap password hash failed", "err", err)
		writeAuthError(w, http.StatusInternalServerError, "internal", "internal error")
		return
	}

	plaintextSessionToken, err := mintTokenPlaintext()
	if err != nil {
		slog.Error("auth: bootstrap session-token mint failed", "err", err)
		writeAuthError(w, http.StatusInternalServerError, "internal", "internal error")
		return
	}
	sessionTokenHash := HashToken(plaintextSessionToken)

	in := storage.BootstrapConsume{
		TokenHash:            HashToken(req.Token),
		OrganizationID:       uuid.New().String(),
		OrganizationName:     req.OrganizationName,
		UserID:               uuid.New().String(),
		UserEmail:            req.Email,
		UserName:             req.Name,
		UserPasswordHash:     passwordHash,
		SessionID:            uuid.New().String(),
		SessionTokenHash:     sessionTokenHash,
		SessionExpiresAt:     h.sessions.now().Add(h.sessions.cfg.TTL),
		SessionUserAgentHash: hashUserAgent(r.Header.Get("User-Agent")),
		SessionIP:            requestIP(r).String(),
	}

	res, err := h.store.ConsumeBootstrapState(r.Context(), in)
	switch {
	case errors.Is(err, storage.ErrBootstrapAlreadyDone):
		observability.Global.BootstrapAttemptsTotal.WithLabelValues("sealed").Inc()
		writeAuthError(w, http.StatusConflict, "bootstrap_already_done",
			"bootstrap is already complete; sign in via /v1/auth/login")
		return
	case errors.Is(err, storage.ErrBootstrapTokenMismatch):
		observability.Global.BootstrapAttemptsTotal.WithLabelValues("invalid_token").Inc()
		writeAuthError(w, http.StatusUnauthorized, "invalid_token", "install token does not match")
		return
	case errors.Is(err, storage.ErrUserEmailExists):
		// Should not happen on a fresh install (no users yet), but
		// surface explicitly so a misconfigured environment fails
		// loudly rather than silently. Distinct metric label so
		// dashboards don't conflate this with sealed/invalid_token.
		observability.Global.BootstrapAttemptsTotal.WithLabelValues("email_taken").Inc()
		writeAuthError(w, http.StatusConflict, "email_taken", "email is already registered")
		return
	case err != nil:
		slog.Error("auth: bootstrap consume failed", "err", err)
		writeAuthError(w, http.StatusInternalServerError, "internal", "internal error")
		return
	}

	observability.Global.BootstrapAttemptsTotal.WithLabelValues("success").Inc()

	// Pre-warm the session cache so the first authenticated request after
	// bootstrap doesn't take the cache-miss path. Uses the Manager's now
	// (so injected fake clocks in tests stay coherent).
	if h.sessions.cache != nil {
		h.sessions.cache.Put(r.Context(), res.Session, h.sessions.now())
	}

	if h.auditFn != nil {
		h.auditFn(r.Context(), res.User.OrganizationID, res.User.ID,
			model.AuditActionBootstrapCompleted,
			map[string]any{"organization_name": req.OrganizationName})
	}

	SetSession(w, h.cookieCfg, plaintextSessionToken, res.Session.ExpiresAt)
	writeJSON(w, http.StatusOK, bootstrapResponse{
		User: user{
			ID:    res.User.ID,
			Email: res.User.Email,
			Name:  res.User.Name,
			Role:  "owner",
		},
		Org: orgRecord{
			ID:   res.User.OrganizationID,
			Name: req.OrganizationName,
		},
	})
}

// ── POST /v1/auth/login ─────────────────────────────────────────────────────

type loginRequest struct {
	Email    string `json:"email"`
	Password string `json:"password"`
}

type loginResponse struct {
	User user      `json:"user"`
	Org  orgRecord `json:"organization"`
}

// loginMultiOrgResponse is the 409 body returned to users with >1 active
// membership in B1. B1.5 will replace this with an org-picker flow; the
// `b15_pending` flag is the marker for the frontend to swap in the
// picker once available.
type loginMultiOrgResponse struct {
	Error      string `json:"error"`
	Detail     string `json:"detail"`
	B15Pending bool   `json:"b15_pending"`
}

func (h *Handler) login(w http.ResponseWriter, r *http.Request) {
	var req loginRequest
	if err := decodeJSON(w, r, &req); err != nil {
		writeAuthError(w, http.StatusBadRequest, "bad_request", "invalid JSON body")
		return
	}
	req.Email = strings.TrimSpace(req.Email)
	if req.Email == "" || req.Password == "" {
		writeAuthError(w, http.StatusBadRequest, "bad_request", "email and password required")
		return
	}

	u, memberships, err := h.store.LookupUserByEmail(r.Context(), req.Email)
	switch {
	case errors.Is(err, storage.ErrUserNotFound):
		// Verify against a placeholder hash anyway to keep the timing
		// envelope flat — argon2id is the dominant cost of login, and
		// an attacker should not learn whether the email is registered
		// from response latency. ErrPasswordTooShort returns are
		// irrelevant here (placeholder is well-formed).
		_ = Verify(req.Password, placeholderHash)
		observability.Global.AuthLoginTotal.WithLabelValues("failure", "unknown_user").Inc()
		writeAuthError(w, http.StatusUnauthorized, "invalid_credentials", "invalid email or password")
		return
	case err != nil:
		slog.Error("auth: login lookup failed", "err", err)
		writeAuthError(w, http.StatusInternalServerError, "internal", "internal error")
		return
	}

	if u.PasswordHash == "" {
		// User exists (perhaps from a Kinde-era row) but has no native
		// password set. Surface as invalid_credentials — the user can
		// recover via admin-issued password reset. Verify against the
		// placeholder so the timing envelope matches the wrong-password
		// path; otherwise an attacker probing Kinde-era email lists
		// would observe a faster 401 here than for password-mismatch.
		_ = Verify(req.Password, placeholderHash)
		observability.Global.AuthLoginTotal.WithLabelValues("failure", "unknown_user").Inc()
		writeAuthError(w, http.StatusUnauthorized, "invalid_credentials", "invalid email or password")
		return
	}
	if err := Verify(req.Password, u.PasswordHash); err != nil {
		observability.Global.AuthLoginTotal.WithLabelValues("failure", "bad_password").Inc()
		writeAuthError(w, http.StatusUnauthorized, "invalid_credentials", "invalid email or password")
		return
	}

	switch len(memberships) {
	case 0:
		// Account exists but isn't tied to any organization — should
		// be rare (deleted org or never-redeemed invite). Same
		// response as wrong-password to avoid info-leak.
		observability.Global.AuthLoginTotal.WithLabelValues("failure", "unknown_user").Inc()
		writeAuthError(w, http.StatusUnauthorized, "invalid_credentials", "invalid email or password")
		return
	case 1:
		// Single-membership: mint session bound to that org.
	default:
		// B1: multi-membership users can't log in until B1.5 ships.
		observability.Global.AuthLoginTotal.WithLabelValues("failure", "org_selection_required").Inc()
		writeJSON(w, http.StatusConflict, loginMultiOrgResponse{
			Error:      "multi_org_not_supported",
			Detail:     "multi-organization login lands in B1.5; contact your admin to consolidate or wait for the next release",
			B15Pending: true,
		})
		return
	}
	mship := memberships[0]

	mint, err := h.sessions.MintSession(r.Context(), MintRequest{
		UserID:         u.ID,
		OrganizationID: mship.OrganizationID,
		AuthMode:       model.AuthModePassword,
		IP:             requestIP(r),
		UserAgent:      r.Header.Get("User-Agent"),
	})
	if err != nil {
		slog.Error("auth: login mint session failed", "err", err)
		writeAuthError(w, http.StatusInternalServerError, "internal", "internal error")
		return
	}
	observability.Global.AuthLoginTotal.WithLabelValues("success", "").Inc()

	SetSession(w, h.cookieCfg, mint.PlaintextToken, mint.ExpiresAt)
	writeJSON(w, http.StatusOK, loginResponse{
		User: user{
			ID:    u.ID,
			Email: u.Email,
			Name:  u.Name,
			Role:  mship.Role,
		},
		Org: orgRecord{
			ID: mship.OrganizationID,
		},
	})
}

// ── POST /v1/auth/logout ────────────────────────────────────────────────────

func (h *Handler) logout(w http.ResponseWriter, r *http.Request) {
	// Logout tolerates absent / invalid cookies — the user-facing
	// outcome is identical (cookie cleared, 204). We try to revoke if
	// we have a recognisable session, but never gate the response on
	// it. This also makes "double-logout" idempotent under the
	// browser's auto-retry flows.
	token := ReadSession(r)
	if token != "" {
		hash := HashToken(token)
		// Look up the session ID so we can call Manager.RevokeSession,
		// which does the write-through cache invalidation. If lookup
		// fails (already revoked, expired, never existed) we still
		// clear the cookie — defence-in-depth.
		if sess, err := h.store.GetSessionByTokenHash(r.Context(), hash); err == nil {
			if revErr := h.sessions.RevokeSession(r.Context(), sess.ID, sess.SessionTokenHash, RevokeReasonLogout); revErr != nil {
				slog.Warn("auth: logout revoke failed", "err", revErr, "session_id", sess.ID)
			}
		}
	}
	ClearSession(w, h.cookieCfg)
	w.WriteHeader(http.StatusNoContent)
}

// ── helpers ─────────────────────────────────────────────────────────────────

// placeholderHash is a precomputed argon2id hash used to keep the
// `unknown email` login path's timing envelope close to the `wrong
// password` path. The plaintext is irrelevant — Verify will mismatch
// either way. Computed once at process start to amortise the cost.
//
// Note: this is best-effort timing equalisation. Argon2id parameters
// are constant, so the dominant computational cost (memory + iterations)
// is identical; the small DB-query difference between found and
// not-found remains and is what an attacker would need to measure.
// The proper defence is rate-limiting per-email, which is plumbed in
// a follow-up slice.
var placeholderHash = func() string {
	h, _ := Hash("axiaops-login-timing-equaliser")
	return h
}()

// requestIP extracts the client IP from common proxy headers, falling
// back to RemoteAddr. The IP is captured into sessions.ip purely for
// forensics — never used for authorization.
func requestIP(r *http.Request) net.IP {
	if v := r.Header.Get("X-Forwarded-For"); v != "" {
		if i := strings.IndexByte(v, ','); i >= 0 {
			v = v[:i]
		}
		if ip := net.ParseIP(strings.TrimSpace(v)); ip != nil {
			return ip
		}
	}
	if v := r.Header.Get("X-Real-IP"); v != "" {
		if ip := net.ParseIP(strings.TrimSpace(v)); ip != nil {
			return ip
		}
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	return net.ParseIP(host)
}

// decodeJSON decodes the body, capping the size to 64 KiB to fence off
// trivial DoS attempts. The endpoints have small payloads (~1 KiB).
// `w` is passed to MaxBytesReader so the connection-close hint is sent
// when the limit is hit (stdlib uses it only for that signal).
func decodeJSON(w http.ResponseWriter, r *http.Request, dst any) error {
	r.Body = http.MaxBytesReader(w, r.Body, 64<<10)
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	return dec.Decode(dst)
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

func writeAuthError(w http.ResponseWriter, status int, code, message string) {
	writeJSON(w, status, map[string]any{
		"error":   code,
		"message": message,
	})
}
