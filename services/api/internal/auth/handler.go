package auth

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"strconv"
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
	store      storage.NativeAuthStore
	sessions   *Manager
	cookieCfg  CookieConfig
	auditFn    AuditWriter
	loginLimit *LoginRateLimiter // nil → no rate limiting (dev fallback)
}

// AuditWriter is the seam for hooking audit_log writes from this
// package without dragging the audit/ helper package in transitively.
// In production main.go wires it as a closure over the audit package.
// Pass nil to disable audit writes — useful in tests.
type AuditWriter func(ctx context.Context, organizationID, userID, action string, metadata map[string]any)

// NewHandler returns a wired Handler. cookieCfg is the same value the
// app middleware uses (DEV_MODE flips Secure off). auditFn may be nil.
// Rate limiting is added separately via WithLoginRateLimit so it stays
// out of the constructor's required-arg list (most tests don't need it).
func NewHandler(store storage.NativeAuthStore, sessions *Manager, cookieCfg CookieConfig, auditFn AuditWriter) *Handler {
	return &Handler{
		store:     store,
		sessions:  sessions,
		cookieCfg: cookieCfg,
		auditFn:   auditFn,
	}
}

// WithLoginRateLimit attaches the rate limiter that gates POST
// /v1/auth/login. Pass nil to disable (dev fallback). Returns the
// receiver for fluent setup so cmd/main.go can chain.
func (h *Handler) WithLoginRateLimit(rl *LoginRateLimiter) *Handler {
	h.loginLimit = rl
	return h
}

// Register attaches the auth routes to the supplied mux. Endpoints:
//
//	POST /v1/auth/bootstrap              first-owner install token redemption
//	POST /v1/auth/login                  email + password → session cookie
//	POST /v1/auth/logout                 revoke + clear cookie
//	POST /v1/auth/invitations/redeem     accept invite token → set password → session
//	POST /v1/auth/password-reset/redeem  redeem reset token → set new password → all sessions revoked
func (h *Handler) Register(mux *http.ServeMux) {
	mux.HandleFunc("POST /v1/auth/bootstrap", h.bootstrap)
	mux.HandleFunc("POST /v1/auth/login", h.login)
	mux.HandleFunc("POST /v1/auth/logout", h.logout)
	mux.HandleFunc("POST /v1/auth/invitations/redeem", h.redeemInvitation)
	mux.HandleFunc("POST /v1/auth/password-reset/redeem", h.redeemPasswordReset)
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
	req.Token = strings.TrimSpace(req.Token)
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

	// AC2 (plan §4.6): delete the install-token file post-consume. The
	// hash is already wiped from `bootstrap_state` inside the same tx
	// the consume ran in; removing the plaintext file closes the only
	// remaining trace on disk. Honours the same env conventions as
	// auth.MaybeGenerateInstallToken — explicit empty disables file
	// management entirely. Best-effort: log + continue on failure.
	removeInstallTokenFile()

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

	SetSession(w, r, h.cookieCfg, plaintextSessionToken, res.Session.ExpiresAt)
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

// loginNeedsOrgSelectionResponse is the 200 body returned to users with >1
// active membership (B1.5 §4.7.1). Mirror of the bootstrap-style picker:
// the frontend lands on /select-org, displays orgs[], and POSTs the
// chosen organization_id back to /v1/auth/select-org along with the
// re-supplied credentials. No session is minted by /v1/auth/login on
// this branch — defence in depth, the picker step re-validates the
// password before cutting a session.
type loginNeedsOrgSelectionResponse struct {
	NeedsOrgSelection bool             `json:"needs_org_selection"`
	Orgs              []orgPickerEntry `json:"orgs"`
}

// orgPickerEntry is the slim per-org shape inside
// loginNeedsOrgSelectionResponse.Orgs. Just enough for the picker to
// render — the user picks one and slice 3's /v1/auth/select-org
// authenticates the choice via organization_id (UUID), not name.
type orgPickerEntry struct {
	ID   string `json:"id"`
	Name string `json:"name"`
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

	// Rate-limit gate (plan §4.2: 10/min/IP, 5/min per email). Runs
	// BEFORE LookupUserByEmail so an attacker probing valid emails
	// can't drive DB load past the cap. Anti-stuffing: the per-email
	// cap caps how many distinct passwords an attacker can try
	// against one account from a botnet. Failing-open posture means a
	// cache outage degrades to "no rate limiting" rather than locking
	// users out — matches the legacy middleware/RateLimiter.
	if h.loginLimit != nil {
		outcome := h.loginLimit.Allow(r.Context(), requestIP(r), req.Email)
		if !outcome.Allowed {
			retry := int(outcome.RetryAfter.Seconds())
			if retry < 1 {
				retry = 1
			}
			w.Header().Set("Retry-After", strconv.Itoa(retry))
			observability.Global.AuthLoginTotal.WithLabelValues("failure", "rate_limited").Inc()
			writeAuthError(w, http.StatusTooManyRequests, "rate_limited",
				"too many login attempts; please retry shortly")
			return
		}
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
		// Single-membership: mint session bound to that org. Falls
		// through to the MintSession block below.
	default:
		// Multi-membership: redirect to the org picker. Per slice-1
		// review: a DB error here MUST surface as 500, not silently
		// degrade to single-org. The user has already passed the
		// password check; an empty org list at this point would
		// land them on a dead-end picker.
		orgRows, err := h.store.ListUserMemberships(r.Context(), u.ID)
		if err != nil {
			slog.Error("auth: login list user memberships failed", "user_id", u.ID, "err", err)
			observability.Global.AuthLoginTotal.WithLabelValues("failure", "internal").Inc()
			writeAuthError(w, http.StatusInternalServerError, "internal", "internal error")
			return
		}
		// Defensive: LookupUserByEmail just told us len(memberships) >= 2.
		// If the JOIN returned fewer, an organizations row is missing for a
		// membership we just read. That's a referential-integrity break —
		// 500 rather than guessing.
		if len(orgRows) < len(memberships) {
			slog.Error("auth: login org-list join shorter than memberships",
				"user_id", u.ID,
				"memberships", len(memberships),
				"joined", len(orgRows),
			)
			observability.Global.AuthLoginTotal.WithLabelValues("failure", "internal").Inc()
			writeAuthError(w, http.StatusInternalServerError, "internal", "internal error")
			return
		}
		orgs := make([]orgPickerEntry, 0, len(orgRows))
		for _, m := range orgRows {
			orgs = append(orgs, orgPickerEntry{ID: m.OrganizationID, Name: m.OrganizationName})
		}
		observability.Global.AuthLoginTotal.WithLabelValues("org_selection_required", "").Inc()
		writeJSON(w, http.StatusOK, loginNeedsOrgSelectionResponse{
			NeedsOrgSelection: true,
			Orgs:              orgs,
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

	SetSession(w, r, h.cookieCfg, mint.PlaintextToken, mint.ExpiresAt)
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

// ── POST /v1/auth/invitations/redeem ────────────────────────────────────────

type redeemInvitationRequest struct {
	Token    string `json:"token"`
	Password string `json:"password"`
	Name     string `json:"name"`
}

type redeemInvitationResponse struct {
	User user      `json:"user"`
	Org  orgRecord `json:"organization"`
}

func (h *Handler) redeemInvitation(w http.ResponseWriter, r *http.Request) {
	var req redeemInvitationRequest
	if err := decodeJSON(w, r, &req); err != nil {
		writeAuthError(w, http.StatusBadRequest, "bad_request", "invalid JSON body")
		return
	}
	// Trim the token so a copy-paste with leading/trailing whitespace
	// still hashes correctly. Bootstrap takes the same precaution.
	req.Token = strings.TrimSpace(req.Token)
	req.Name = strings.TrimSpace(req.Name)
	if req.Token == "" || req.Password == "" || req.Name == "" {
		writeAuthError(w, http.StatusBadRequest, "bad_request", "token, password, name required")
		return
	}
	if err := CheckPolicy(req.Password); err != nil {
		writeAuthError(w, http.StatusBadRequest, "weak_password", err.Error())
		return
	}

	passwordHash, err := Hash(req.Password)
	if err != nil {
		slog.Error("auth: invitation redeem hash failed", "err", err)
		writeAuthError(w, http.StatusInternalServerError, "internal", "internal error")
		return
	}

	in := storage.NativeInviteRedeem{
		TokenHash:    HashToken(req.Token),
		UserID:       uuid.New().String(), // ignored by store if email matches existing user
		UserName:     req.Name,
		PasswordHash: passwordHash,        // ignored if email matches existing user (B1.5 path)
	}
	resolvedUser, mship, err := h.store.RedeemNativeInvitation(r.Context(), in)
	switch {
	case errors.Is(err, storage.ErrInvitationNotFound):
		// Single-source response for "token unknown / expired / already
		// redeemed". Don't differentiate so callers can't probe which
		// state a token is in.
		observability.Global.AuthInvitationsTotal.WithLabelValues("expired").Inc()
		writeAuthError(w, http.StatusGone, "invitation_invalid", "invitation token is invalid or expired")
		return
	case errors.Is(err, storage.ErrUserEmailExists):
		// Race: a user with the same email was created concurrently
		// (extraordinarily rare on a single-org install). Surface to
		// the user as a generic "already taken" — they can sign in
		// directly via /v1/auth/login.
		writeAuthError(w, http.StatusConflict, "email_taken", "this email is already registered")
		return
	case err != nil:
		slog.Error("auth: invitation redeem failed", "err", err)
		writeAuthError(w, http.StatusInternalServerError, "internal", "internal error")
		return
	}

	// Mint a session bound to the org from the consumed invitation.
	mint, mintErr := h.sessions.MintSession(r.Context(), MintRequest{
		UserID:         resolvedUser.ID,
		OrganizationID: mship.OrganizationID,
		AuthMode:       model.AuthModePassword,
		IP:             requestIP(r),
		UserAgent:      r.Header.Get("User-Agent"),
	})
	if mintErr != nil {
		slog.Error("auth: invitation redeem mint session failed", "err", mintErr)
		writeAuthError(w, http.StatusInternalServerError, "internal", "internal error")
		return
	}

	observability.Global.AuthInvitationsTotal.WithLabelValues("redeemed").Inc()

	if h.auditFn != nil {
		h.auditFn(r.Context(), mship.OrganizationID, resolvedUser.ID,
			model.AuditActionInvitationRedeemedNative,
			map[string]any{"role": mship.Role})
	}

	SetSession(w, r, h.cookieCfg, mint.PlaintextToken, mint.ExpiresAt)
	writeJSON(w, http.StatusOK, redeemInvitationResponse{
		User: user{
			ID:    resolvedUser.ID,
			Email: resolvedUser.Email,
			Name:  resolvedUser.Name,
			Role:  mship.Role,
		},
		Org: orgRecord{
			ID: mship.OrganizationID,
		},
	})
}

// ── POST /v1/auth/password-reset/redeem ─────────────────────────────────────

type redeemPasswordResetRequest struct {
	Token       string `json:"token"`
	NewPassword string `json:"new_password"`
}

func (h *Handler) redeemPasswordReset(w http.ResponseWriter, r *http.Request) {
	var req redeemPasswordResetRequest
	if err := decodeJSON(w, r, &req); err != nil {
		writeAuthError(w, http.StatusBadRequest, "bad_request", "invalid JSON body")
		return
	}
	req.Token = strings.TrimSpace(req.Token)
	if req.Token == "" || req.NewPassword == "" {
		writeAuthError(w, http.StatusBadRequest, "bad_request", "token and new_password required")
		return
	}
	if err := CheckPolicy(req.NewPassword); err != nil {
		writeAuthError(w, http.StatusBadRequest, "weak_password", err.Error())
		return
	}

	newHash, err := Hash(req.NewPassword)
	if err != nil {
		slog.Error("auth: password reset hash failed", "err", err)
		writeAuthError(w, http.StatusInternalServerError, "internal", "internal error")
		return
	}

	userID, organizationID, err := h.store.RedeemPasswordReset(r.Context(), HashToken(req.Token), newHash)
	switch {
	case errors.Is(err, storage.ErrPasswordResetNotFound),
		errors.Is(err, storage.ErrPasswordResetExpired):
		// Collapse the two cases to a single 410 so an attacker can't
		// distinguish "never issued" from "already redeemed/expired".
		writeAuthError(w, http.StatusGone, "reset_invalid", "password reset token is invalid or expired")
		return
	case err != nil:
		slog.Error("auth: password reset redeem failed", "err", err)
		writeAuthError(w, http.StatusInternalServerError, "internal", "internal error")
		return
	}

	// Critical: revoke EVERY live session for this user. A reset
	// implies "the user (or a recovery flow) has the new password" —
	// every existing cookie is potentially compromised. Manager.RevokeUserSessions
	// already enumerates token hashes for explicit cache eviction
	// (architect C4). Errors are logged + counted but not surfaced —
	// the password change has already committed; a stale session
	// can't survive its own ExpiresAt anyway.
	if revoked, revErr := h.sessions.RevokeUserSessions(r.Context(), userID, RevokeReasonPasswordReset); revErr != nil {
		slog.Error("auth: password reset session revoke failed",
			"err", revErr, "user_id", userID)
	} else {
		slog.Info("auth: password reset — sessions revoked",
			"user_id", userID, "count", revoked)
	}

	if h.auditFn != nil {
		// organizationID comes from the password_resets row itself —
		// the redeem flow has no auth context at this point, so the
		// stored row is the only source of truth for which org owns
		// the audit event. audit_log requires a non-empty org_id.
		h.auditFn(r.Context(), organizationID, userID,
			model.AuditActionUserPasswordResetRedeemed, nil)
	}

	w.WriteHeader(http.StatusNoContent)
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
	ClearSession(w, r, h.cookieCfg)
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
// back to RemoteAddr. Used for both forensics (sessions.ip) and the
// rate-limiter key — the latter is security-critical, so the order
// here is deliberately attacker-resistant.
//
// Threat: nginx and App Runner both *append* the connecting peer's IP
// to whatever X-Forwarded-For header the client sent. So a request from
// `attacker-ip` carrying `X-Forwarded-For: 1.2.3.4` becomes
// `X-Forwarded-For: 1.2.3.4, attacker-ip` by the time it reaches us.
// Taking the *leftmost* token (the previous version of this helper)
// returned `1.2.3.4` — attacker-controlled — letting the attacker
// rotate spoofed values to bypass the per-IP rate-limit cap entirely.
//
// We instead trust:
//  1. X-Real-IP — set by nginx via `proxy_set_header X-Real-IP $remote_addr`,
//     which unconditionally overwrites any client-supplied value. Reliable
//     in the staging shape; not set by App Runner.
//  2. The *rightmost* token of X-Forwarded-For — the one our trusted
//     proxy added, i.e. the actual peer that connected to it. Reliable
//     in both staging (single nginx hop) and production (single App
//     Runner LB hop). If a future deployment introduces a second trusted
//     proxy, this needs to take the rightmost-N-th token instead.
//  3. RemoteAddr — for direct-to-API requests (tests, dev mode).
func requestIP(r *http.Request) net.IP {
	if v := r.Header.Get("X-Real-IP"); v != "" {
		if ip := net.ParseIP(strings.TrimSpace(v)); ip != nil {
			return ip
		}
	}
	if v := r.Header.Get("X-Forwarded-For"); v != "" {
		if i := strings.LastIndexByte(v, ','); i >= 0 {
			v = v[i+1:]
		}
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
