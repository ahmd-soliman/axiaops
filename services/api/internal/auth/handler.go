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
//	POST /v1/auth/login                  email + password → session cookie (or org picker on multi-org)
//	POST /v1/auth/select-org             email + password + organization_id → session cookie (B1.5 multi-org follow-up)
//	POST /v1/auth/switch-org             organization_id (session-authenticated) → rotated session bound to target org
//	POST /v1/auth/logout                 revoke + clear cookie
//	POST /v1/auth/invitations/preview    peek invite token → {email, organization_name, existing_user}
//	POST /v1/auth/invitations/redeem     accept invite token → set password (new user) or verify password (existing user) → session
//	POST /v1/auth/password-reset/redeem  redeem reset token → set new password → all sessions revoked
func (h *Handler) Register(mux *http.ServeMux) {
	mux.HandleFunc("POST /v1/auth/bootstrap", h.bootstrap)
	mux.HandleFunc("POST /v1/auth/login", h.login)
	mux.HandleFunc("POST /v1/auth/select-org", h.selectOrg)
	mux.HandleFunc("POST /v1/auth/switch-org", h.switchOrg)
	mux.HandleFunc("POST /v1/auth/logout", h.logout)
	mux.HandleFunc("POST /v1/auth/invitations/preview", h.previewInvitation)
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
		// 500 rather than guessing. The JOIN cannot return MORE rows than
		// the source (organizations.id is the PK on the right side, so a
		// membership row matches at most one org row), so `<` is the only
		// inconsistency direction and `len(orgRows) == 0` is the worst case
		// of it (every org row missing). Both manifest as 500.
		if len(orgRows) < len(memberships) {
			slog.Error("auth: login org-list join shorter than memberships — referential-integrity break (organizations row missing for a membership we just read)",
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

// ── POST /v1/auth/select-org ────────────────────────────────────────────────

// selectOrgRequest is the body for the multi-org picker step. Email +
// password are re-supplied so the picker step can re-run the password
// check independently — defence in depth, the frontend never holds
// state we trust to round-trip a credential through.
type selectOrgRequest struct {
	Email          string `json:"email"`
	Password       string `json:"password"`
	OrganizationID string `json:"organization_id"`
}

// selectOrgResponse is identical in shape to loginResponse — once
// the picker step succeeds we're back on the single-org login path,
// session minted, dashboard ready.
//
// NOTE: this is a type alias, not a struct copy. Adding a field to
// loginResponse silently widens select-org's wire shape too. That's
// the intent (single-org login and post-picker login should agree on
// the same envelope), but the silent inheritance means the zero value
// of any new field must be sensible for the picker path. Audit the
// JSON shape on every loginResponse field addition.
type selectOrgResponse = loginResponse

// selectOrg is the picker counterpart to login. Plan §4.7.1: a
// multi-membership user is bounced from /v1/auth/login with the
// `needs_org_selection` payload; the dashboard collects the chosen
// organization_id (UUID) and POSTs it here along with the *same* email
// + password the user just typed. We re-validate the password from
// scratch — never trust the frontend to remember step 1 — and confirm
// the user actually holds a membership in the chosen org before
// minting a session bound to that org.
//
// Failure modes are deliberately collapsed to one 401 shape: wrong
// password, unknown email, AND chosen-org-not-in-memberships all return
// the same `invalid_credentials` body. The narrow benefit: an attacker
// *without* valid credentials can't distinguish "org exists but you
// don't belong" from "org does not exist" — both look like a generic
// 401. (Against an attacker WITH valid credentials, the collapse buys
// nothing — they already see the org list via the 200 response on
// /v1/auth/login. The defence is for the no-creds case only.)
func (h *Handler) selectOrg(w http.ResponseWriter, r *http.Request) {
	var req selectOrgRequest
	if err := decodeJSON(w, r, &req); err != nil {
		writeAuthError(w, http.StatusBadRequest, "bad_request", "invalid JSON body")
		return
	}
	req.Email = strings.TrimSpace(req.Email)
	req.OrganizationID = strings.TrimSpace(req.OrganizationID)
	if req.Email == "" || req.Password == "" || req.OrganizationID == "" {
		writeAuthError(w, http.StatusBadRequest, "bad_request", "email, password, and organization_id required")
		return
	}

	// Same rate-limit contract as /v1/auth/login (10/min/IP, 5/min/email).
	// Sharing the same limiter instance means an attacker can't
	// alternate /login and /select-org to double their budget against
	// one email.
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
		// Same timing-flat treatment as /v1/auth/login — verify against
		// placeholder so an attacker can't time-detect unknown emails.
		_ = Verify(req.Password, placeholderHash)
		observability.Global.AuthLoginTotal.WithLabelValues("failure", "unknown_user").Inc()
		writeAuthError(w, http.StatusUnauthorized, "invalid_credentials", "invalid email or password")
		return
	case err != nil:
		slog.Error("auth: select-org lookup failed", "err", err)
		observability.Global.AuthLoginTotal.WithLabelValues("failure", "internal").Inc()
		writeAuthError(w, http.StatusInternalServerError, "internal", "internal error")
		return
	}

	if u.PasswordHash == "" {
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

	// Membership check: the chosen organization_id must be in the
	// user's live membership set. Linear scan is fine — users with
	// dozens of memberships are not a realistic profile, and skipping
	// a SQL roundtrip on the hot path matters more than the loop cost.
	var chosen model.Membership
	var found bool
	for _, m := range memberships {
		if m.OrganizationID == req.OrganizationID {
			chosen = m
			found = true
			break
		}
	}
	if !found {
		// Org not in user's set. Same 401 shape as wrong-password to
		// avoid widening the membership-probe channel — the org-picker
		// list on /login already exposes this to an authenticated
		// caller, but emitting a distinct error here would let an
		// attacker who has the right email-and-password test arbitrary
		// org_ids without ever holding a session in those orgs.
		observability.Global.AuthLoginTotal.WithLabelValues("failure", "unknown_user").Inc()
		writeAuthError(w, http.StatusUnauthorized, "invalid_credentials", "invalid email or password")
		return
	}

	mint, err := h.sessions.MintSession(r.Context(), MintRequest{
		UserID:         u.ID,
		OrganizationID: chosen.OrganizationID,
		AuthMode:       model.AuthModePassword,
		IP:             requestIP(r),
		UserAgent:      r.Header.Get("User-Agent"),
	})
	if err != nil {
		slog.Error("auth: select-org mint session failed", "err", err)
		observability.Global.AuthLoginTotal.WithLabelValues("failure", "internal").Inc()
		writeAuthError(w, http.StatusInternalServerError, "internal", "internal error")
		return
	}
	observability.Global.AuthLoginTotal.WithLabelValues("success", "").Inc()

	SetSession(w, r, h.cookieCfg, mint.PlaintextToken, mint.ExpiresAt)
	writeJSON(w, http.StatusOK, selectOrgResponse{
		User: user{
			ID:    u.ID,
			Email: u.Email,
			Name:  u.Name,
			Role:  chosen.Role,
		},
		Org: orgRecord{
			ID: chosen.OrganizationID,
		},
	})
}

// ── POST /v1/auth/switch-org ────────────────────────────────────────────────

type switchOrgRequest struct {
	OrganizationID string `json:"organization_id"`
}

// switchOrgUser is the slim user shape returned by /v1/auth/switch-org —
// just id + role. Defined as a separate struct (not a reuse of `user`)
// so empty-email and empty-name don't end up on the wire as `""`. The
// frontend already has email/name from /v1/me; rebinding to a different
// org doesn't change them.
type switchOrgUser struct {
	ID   string `json:"id"`
	Role string `json:"role"`
}

// switchOrgResponse is a deliberately slim confirmation payload —
// `{user: {id, role}, organization: {id}}`. Job is to confirm the new
// binding and surface the role at target so the dashboard can re-render
// UI gates without re-fetching /v1/me.
type switchOrgResponse struct {
	User switchOrgUser `json:"user"`
	Org  orgRecord     `json:"organization"`
}

// switchOrg flips the caller's session from one org they belong to to
// another (B1.5 §4.7.1). Authentication is via the existing session
// cookie — the user is already logged in; this is just a re-binding,
// no fresh password check. Slice-2 review confirmed: collapsing the
// "wrong org" channel to a generic 401 was for the no-creds case, but
// here the caller IS authenticated, so we use 403 `not_a_member` to
// distinguish "you don't belong" from "your session is dead" (401).
//
// Failure modes:
//   - missing/invalid session  → 401 (handler enforces — /v1/auth/* paths
//     bypass WrapNative, so we read the cookie ourselves).
//   - missing organization_id  → 400.
//   - target == current org    → 200 no-op (don't rotate, don't audit;
//     idempotent contract for clients that may double-fire on UI race).
//   - target not in caller's memberships → 403 `not_a_member`.
//   - rotation failure → 500. Note: if the PG revoke succeeded and the
//     mint failed, the caller is logged out — they re-auth via /login.
//     This is the documented worst case in RotateSessionForOrg.
//
// Audit: writes one row to the FROM org's audit_log with action
// `session.org_switched` and metadata `{from, to}`. The user_id is
// the actor on the audit row already, so duplicating it in metadata
// (as plan §4.7.4 row 5 loosely suggests) would just inflate the row.
func (h *Handler) switchOrg(w http.ResponseWriter, r *http.Request) {
	// /v1/auth/* paths bypass WrapNative — read + validate the session
	// cookie inline. Same shape logout uses, but strict (no tolerance for
	// missing/invalid cookies — switching orgs without a session is a
	// client bug, not a graceful degrade).
	token := ReadSession(r)
	if token == "" {
		writeAuthError(w, http.StatusUnauthorized, "unauthorized", "no session")
		return
	}
	sess, err := h.sessions.ValidateSession(r.Context(), token)
	if err != nil {
		writeAuthError(w, http.StatusUnauthorized, "unauthorized", "no session")
		return
	}

	var req switchOrgRequest
	if err := decodeJSON(w, r, &req); err != nil {
		writeAuthError(w, http.StatusBadRequest, "bad_request", "invalid JSON body")
		return
	}
	target := strings.TrimSpace(req.OrganizationID)
	if target == "" {
		writeAuthError(w, http.StatusBadRequest, "bad_request", "organization_id required")
		return
	}

	// Verify the target is in the user's membership set. Use the wider
	// ListUserMemberships (joined with organizations) so we have the
	// org name + role in hand for the response without a second roundtrip.
	rows, err := h.store.ListUserMemberships(r.Context(), sess.UserID)
	if err != nil {
		slog.Error("auth: switch-org list memberships failed", "user_id", sess.UserID, "err", err)
		writeAuthError(w, http.StatusInternalServerError, "internal", "internal error")
		return
	}
	var chosen model.MembershipWithOrganization
	var found bool
	for _, m := range rows {
		if m.OrganizationID == target {
			chosen = m
			found = true
			break
		}
	}
	if !found {
		// 403, not 401 — the user IS authenticated; they just lack a
		// membership in target. Distinct from the /select-org collapse
		// because the no-creds-membership-probe channel doesn't exist
		// here (caller already proved a valid session).
		writeAuthError(w, http.StatusForbidden, "not_a_member", "you are not a member of that organization")
		return
	}

	// Same-org no-op: don't rotate, don't audit. Reuses sess.OrganizationID
	// rather than mutating the cookie so the caller's clock-bound expires_at
	// doesn't shift. Idempotent for racy clients.
	if target == sess.OrganizationID {
		writeJSON(w, http.StatusOK, switchOrgResponse{
			User: switchOrgUser{ID: sess.UserID, Role: chosen.Role},
			Org:  orgRecord{ID: target},
		})
		return
	}

	// Rotate: revoke old session (PG + cache + metric) then mint new one
	// bound to target. The Manager.RotateSessionForOrg ordering guarantee
	// is "revoke first → if mint fails, user is logged out (recoverable)".
	mint, err := h.sessions.RotateSessionForOrg(r.Context(), sess.ID, sess.SessionTokenHash, MintRequest{
		UserID:         sess.UserID,
		OrganizationID: target,
		// Preserve the originating auth mode across an org switch — an
		// SSO-authenticated session that switches orgs must stay
		// auth_mode='sso' on the new session row, otherwise audit
		// tooling and any future SSO-enforcement gate would silently
		// see a forged 'password' session.
		AuthMode:  sess.AuthMode,
		IP:        requestIP(r),
		UserAgent: r.Header.Get("User-Agent"),
	})
	if err != nil {
		slog.Error("auth: switch-org rotate failed", "user_id", sess.UserID, "err", err)
		writeAuthError(w, http.StatusInternalServerError, "internal", "internal error")
		return
	}

	// Audit on the FROM org (where the action originated). Failure
	// non-fatal — the rotation already committed; an audit miss is a
	// telemetry gap, not a security regression.
	if h.auditFn != nil {
		h.auditFn(r.Context(), sess.OrganizationID, sess.UserID, model.AuditActionSessionOrgSwitched,
			map[string]any{"from": sess.OrganizationID, "to": target})
	}

	SetSession(w, r, h.cookieCfg, mint.PlaintextToken, mint.ExpiresAt)
	writeJSON(w, http.StatusOK, switchOrgResponse{
		User: switchOrgUser{ID: sess.UserID, Role: chosen.Role},
		Org:  orgRecord{ID: target},
	})
}

// ── POST /v1/auth/invitations/preview ───────────────────────────────────────

type previewInvitationRequest struct {
	Token string `json:"token"`
}

// previewInvitationResponse is the wire shape returned to AcceptInviteScreen.
// Drives the UI choice: when ExistingUser is true, the form prompts for the
// existing password; when false, it prompts to set a new one and a name.
//
// The user's password_hash is intentionally NOT exposed — only the boolean.
// The actual hash stays inside the storage layer for redeem-time verification.
type previewInvitationResponse struct {
	Email            string `json:"email"`
	OrganizationName string `json:"organization_name"`
	Role             string `json:"role"`
	ExistingUser     bool   `json:"existing_user"`
	// ExistingUserName is the existing user's display name (if any),
	// shown by the UI as "Welcome back, <name>" so the picker step
	// doesn't feel anonymous. Empty when ExistingUser is false or the
	// user's name was never set.
	ExistingUserName string `json:"existing_user_name,omitempty"`
}

func (h *Handler) previewInvitation(w http.ResponseWriter, r *http.Request) {
	var req previewInvitationRequest
	if err := decodeJSON(w, r, &req); err != nil {
		writeAuthError(w, http.StatusBadRequest, "bad_request", "invalid JSON body")
		return
	}
	req.Token = strings.TrimSpace(req.Token)
	if req.Token == "" {
		writeAuthError(w, http.StatusBadRequest, "bad_request", "token required")
		return
	}

	// Rate-limit gate: this endpoint reveals (a) whether a token is
	// valid and (b) whether the invited email already maps to a user
	// (existing_user) plus their display name. Without this gate, an
	// attacker who can guess or harvest tokens can use it to enumerate
	// AxiaOps users globally. We don't have an email at request time
	// (that's what the response reveals), so the per-IP cap is the
	// only key. Email key passed empty — ratelimit.go treats that as
	// "IP only" (no per-email amplification).
	if h.loginLimit != nil {
		outcome := h.loginLimit.Allow(r.Context(), requestIP(r), "")
		if !outcome.Allowed {
			retry := int(outcome.RetryAfter.Seconds())
			if retry < 1 {
				retry = 1
			}
			w.Header().Set("Retry-After", strconv.Itoa(retry))
			writeAuthError(w, http.StatusTooManyRequests, "rate_limited",
				"too many invitation lookups; please retry shortly")
			return
		}
	}

	peek, err := h.store.LookupInvitationByToken(r.Context(), HashToken(req.Token))
	switch {
	case errors.Is(err, storage.ErrInvitationNotFound):
		writeAuthError(w, http.StatusGone, "invitation_invalid", "invitation token is invalid or expired")
		return
	case err != nil:
		slog.Error("auth: invitation preview failed", "err", err)
		writeAuthError(w, http.StatusInternalServerError, "internal", "internal error")
		return
	}

	resp := previewInvitationResponse{
		Email:            peek.Email,
		OrganizationName: peek.OrganizationName,
		Role:             peek.Role,
		ExistingUser:     peek.ExistingUser != nil,
	}
	if peek.ExistingUser != nil {
		resp.ExistingUserName = peek.ExistingUser.Name
	}
	writeJSON(w, http.StatusOK, resp)
}

// ── POST /v1/auth/invitations/redeem ────────────────────────────────────────

type redeemInvitationRequest struct {
	Token    string `json:"token"`
	Password string `json:"password"`
	// Name is required only on the new-user flow. The existing-user
	// flow ignores it (the user's name in their other organisation
	// stays unchanged).
	Name string `json:"name"`
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
	if req.Token == "" || req.Password == "" {
		writeAuthError(w, http.StatusBadRequest, "bad_request", "token and password required")
		return
	}

	tokenHash := HashToken(req.Token)

	// Peek to discover whether the email already maps to a global user.
	// This is what selects between the new-user (set password + name)
	// and existing-user (verify existing password) flow. The token row
	// is NOT consumed here.
	peek, err := h.store.LookupInvitationByToken(r.Context(), tokenHash)
	// Rate-limit gate. Defer until AFTER the peek so we have the email
	// to key the per-email cap on — without it, the limiter only sees
	// the per-IP key and an attacker rotating IPs (or a botnet)
	// trivially bypasses the per-email cap that's the actual
	// brute-force defence. The peek result determines email; the
	// password attempt comes after.
	//
	// Pre-condition: ErrInvitationNotFound is handled below the gate
	// (we don't want to reveal "valid token" by 410ing without the
	// rate-limit check), but we DO want to short-circuit malformed
	// tokens — the gate runs unconditionally on lookup success/failure
	// alike, so an attacker probing thousands of garbage tokens is
	// also limited.
	if h.loginLimit != nil {
		emailKey := ""
		if err == nil {
			emailKey = peek.Email
		}
		outcome := h.loginLimit.Allow(r.Context(), requestIP(r), emailKey)
		if !outcome.Allowed {
			retry := int(outcome.RetryAfter.Seconds())
			if retry < 1 {
				retry = 1
			}
			w.Header().Set("Retry-After", strconv.Itoa(retry))
			observability.Global.AuthInvitationsTotal.WithLabelValues("rate_limited").Inc()
			writeAuthError(w, http.StatusTooManyRequests, "rate_limited",
				"too many invitation redeem attempts; please retry shortly")
			return
		}
	}
	switch {
	case errors.Is(err, storage.ErrInvitationNotFound):
		observability.Global.AuthInvitationsTotal.WithLabelValues("expired").Inc()
		writeAuthError(w, http.StatusGone, "invitation_invalid", "invitation token is invalid or expired")
		return
	case err != nil:
		slog.Error("auth: invitation redeem peek failed", "err", err)
		writeAuthError(w, http.StatusInternalServerError, "internal", "internal error")
		return
	}

	in := storage.NativeInviteRedeem{TokenHash: tokenHash}
	if peek.ExistingUser != nil {
		// Existing-user flow (B1.5): verify the supplied password
		// against the user's stored hash. We never trust the
		// frontend's "I'm an existing user" claim; the truth lives in
		// the password column.
		if err := Verify(req.Password, peek.ExistingUser.PasswordHash); err != nil {
			writeAuthError(w, http.StatusUnauthorized, "invalid_credentials", "invalid password")
			return
		}
		in.ExistingUserID = peek.ExistingUser.ID
		// Name from req is intentionally ignored — the existing
		// user's display name belongs to them, not to whoever types
		// in the picker.
	} else {
		// New-user flow: require name and enforce the password policy.
		if req.Name == "" {
			writeAuthError(w, http.StatusBadRequest, "bad_request", "name required for new user")
			return
		}
		if err := CheckPolicy(req.Password); err != nil {
			writeAuthError(w, http.StatusBadRequest, "weak_password", err.Error())
			return
		}
		passwordHash, hashErr := Hash(req.Password)
		if hashErr != nil {
			slog.Error("auth: invitation redeem hash failed", "err", hashErr)
			writeAuthError(w, http.StatusInternalServerError, "internal", "internal error")
			return
		}
		in.UserID = uuid.New().String()
		in.UserName = req.Name
		in.PasswordHash = passwordHash
	}

	resolvedUser, mship, err := h.store.RedeemNativeInvitation(r.Context(), in)
	switch {
	case errors.Is(err, storage.ErrInvitationNotFound):
		// Race between the peek and the redeem — token consumed by
		// another caller in the gap. Single-source response for
		// "token unknown / expired / already redeemed".
		observability.Global.AuthInvitationsTotal.WithLabelValues("expired").Inc()
		writeAuthError(w, http.StatusGone, "invitation_invalid", "invitation token is invalid or expired")
		return
	case errors.Is(err, storage.ErrUserEmailExists):
		// Race: a global user with this email was created between the
		// peek (which saw none) and the INSERT. The right thing for the
		// caller is to retry — peek will now route through the
		// existing-user flow.
		writeAuthError(w, http.StatusConflict, "email_taken", "this email was just registered — please refresh and try again")
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
