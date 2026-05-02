package sso_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"

	"axiaops.io/api/internal/auth"
	"axiaops.io/api/internal/sso"
	"axiaops.io/shared/crypto"
	"axiaops.io/shared/model"
	"axiaops.io/shared/storage"
)

// ─── mock store ─────────────────────────────────────────────────────────────

// mockCallbackStore satisfies sso.CallbackStore. State accumulates across
// the orchestration so a single test can assert end-to-end behaviour
// (UpsertUser → RedeemPendingInvitation precedence → JIT membership →
// audit trail).
type mockCallbackStore struct {
	conn          model.SSOConnection
	domain        model.SSODomain
	domainErr     error // returned by GetVerifiedSSODomainByName
	mappings      []model.SSOGroupMapping
	pendingInvite bool  // RedeemPendingInvitation returns this
	pendingErr    error // when set, RedeemPendingInvitation returns this error
	// existingMembership simulates the JIT-update path: SaveMembership
	// returns ErrMembershipExists, GetMembershipByOrgUser returns this row,
	// UpdateMembershipRole mutates it.
	existingMembership *model.Membership
	upsertedUser       model.User
	saved              []model.Membership
	audit              []model.AuditEvent
}

func (m *mockCallbackStore) GetSSOConnectionByID(_ context.Context, _ string) (model.SSOConnection, error) {
	return m.conn, nil
}

func (m *mockCallbackStore) GetVerifiedSSODomainByName(_ context.Context, _ string) (model.SSODomain, error) {
	if m.domainErr != nil {
		return model.SSODomain{}, m.domainErr
	}
	return m.domain, nil
}

func (m *mockCallbackStore) UpsertUser(_ context.Context, organizationID, sub, email, name string) (model.User, error) {
	m.upsertedUser = model.User{
		ID:    "user-" + sub, // deterministic for assertions
		Email: email,
		Name:  name,
	}
	return m.upsertedUser, nil
}

func (m *mockCallbackStore) RedeemPendingInvitation(_ context.Context, _, _, _ string) (bool, error) {
	return m.pendingInvite, m.pendingErr
}

func (m *mockCallbackStore) ListSSOGroupMappings(_ context.Context, _ string) ([]model.SSOGroupMapping, error) {
	return m.mappings, nil
}

func (m *mockCallbackStore) SaveMembership(_ context.Context, x model.Membership) error {
	if m.existingMembership != nil {
		// JIT-update simulation: signal collision so JITProvisionMembership
		// falls through to GetMembershipByOrgUser + UpdateMembershipRole.
		return storage.ErrMembershipExists
	}
	m.saved = append(m.saved, x)
	return nil
}

func (m *mockCallbackStore) GetMembershipByOrgUser(_ context.Context, organizationID, userID string) (model.Membership, error) {
	if m.existingMembership != nil {
		return *m.existingMembership, nil
	}
	for _, x := range m.saved {
		if x.OrganizationID == organizationID && x.UserID == userID {
			return x, nil
		}
	}
	// Distinct sentinel from ErrMembershipExists — "not found" is its own
	// posture and the JIT path doesn't expect ErrMembershipExists here.
	return model.Membership{}, errors.New("membership not found in mock")
}

func (m *mockCallbackStore) UpdateMembershipRole(_ context.Context, id, role string) error {
	if m.existingMembership != nil && m.existingMembership.ID == id {
		m.existingMembership.Role = role
		return nil
	}
	for i := range m.saved {
		if m.saved[i].ID == id {
			m.saved[i].Role = role
			return nil
		}
	}
	return errors.New("membership not found")
}

func (m *mockCallbackStore) AuditLogWrite(_ context.Context, e model.AuditEvent) (int64, error) {
	m.audit = append(m.audit, e)
	return int64(len(m.audit)), nil
}

// hasAudit reports whether any audit event has the given action.
func (m *mockCallbackStore) hasAudit(action string) bool {
	for _, e := range m.audit {
		if e.Action == action {
			return true
		}
	}
	return false
}

// ─── stub session minter ────────────────────────────────────────────────────

type stubMinter struct {
	called bool
	last   auth.MintRequest
}

func (s *stubMinter) MintSession(_ context.Context, in auth.MintRequest) (auth.MintResult, error) {
	s.called = true
	s.last = in
	return auth.MintResult{
		PlaintextToken: "stub-session-token",
		ExpiresAt:      time.Now().Add(24 * time.Hour),
	}, nil
}

// ─── builders ───────────────────────────────────────────────────────────────

// callbackTest assembles the callback handler with shared deps. Returns the
// store + state cache + minter so individual tests can inspect post-call
// state without re-deriving them.
type callbackTest struct {
	t      *testing.T
	idp    *idpFixture
	store  *mockCallbackStore
	state  *sso.StateStore
	minter *stubMinter
	mux    http.Handler
}

func newCallbackTest(t *testing.T) *callbackTest {
	t.Helper()
	t.Setenv("ENCRYPTION_KEY", "0000000000000000000000000000000000000000000000000000000000000000")

	idp := newIDPFixture(t)

	// Encrypt the OIDC client_secret the way the connection-CRUD callsite
	// would (crypto.Encrypt → hex string). The callback round-trips it
	// through []byte storage to exercise decryptClientSecret end-to-end.
	cipherHex, err := crypto.Encrypt("test-client-secret")
	if err != nil {
		t.Fatalf("crypto.Encrypt: %v", err)
	}

	conn := model.SSOConnection{
		ID:                         "conn-1",
		OrganizationID:             "org-test",
		Protocol:                   model.SSOProtocolOIDC,
		Status:                     model.SSOStatusActive,
		DefaultRole:                "viewer",
		OIDCClientID:               "client-test",
		OIDCDiscoveryURL:           idp.discoveryURL,
		OIDCClientSecretCiphertext: []byte(cipherHex),
	}
	store := &mockCallbackStore{
		conn: conn,
		domain: model.SSODomain{
			ID:              "dom-1",
			OrganizationID:  conn.OrganizationID,
			SSOConnectionID: conn.ID,
			Domain:          "acme.com",
			Status:          "verified",
		},
	}

	v := sso.NewValidator(newMockCache())
	v.SetHTTPClient(idp.server.Client())
	stateStore := sso.NewStateStore(newMockCache())
	minter := &stubMinter{}

	mux := http.NewServeMux()
	mux.Handle("GET /v1/sso/oidc/{cid}/callback",
		sso.NewCallbackHandler(sso.CallbackOptions{
			Store:        store,
			Validator:    v,
			StateStore:   stateStore,
			Sessions:     minter,
			CookieConfig: auth.NewCookieConfig(),
			PublicHost:   "https://app.example.com",
			HTTPClient:   idp.server.Client(),
		}))

	return &callbackTest{
		t:      t,
		idp:    idp,
		store:  store,
		state:  stateStore,
		minter: minter,
		mux:    mux,
	}
}

// generateState persists a StateData under a fresh state token and returns
// it; callers compose into the callback URL.
func (ct *callbackTest) generateState(cid string) (string, sso.StateData) {
	ct.t.Helper()
	state, data, err := sso.GenerateState(cid, "")
	if err != nil {
		ct.t.Fatalf("GenerateState: %v", err)
	}
	if err := ct.state.Persist(context.Background(), state, data); err != nil {
		ct.t.Fatalf("Persist: %v", err)
	}
	return state, data
}

// claimsFor returns a baseline ID-token claim set that the IdP fixture
// will sign on the next /token call. Tests mutate it before SetNextToken.
func (ct *callbackTest) claimsFor(nonce string) jwt.MapClaims {
	return jwt.MapClaims{
		"iss":   ct.idp.issuer,
		"aud":   "client-test",
		"sub":   "idp-sub-123",
		"email": "alice@acme.com",
		"name":  "Alice Smith",
		"exp":   time.Now().Add(5 * time.Minute).Unix(),
		"iat":   time.Now().Unix(),
		"nonce": nonce,
	}
}

func (ct *callbackTest) hit(cid, code, state string) *httptest.ResponseRecorder {
	ct.t.Helper()
	rec := httptest.NewRecorder()
	target := "/v1/sso/oidc/" + cid + "/callback"
	if code != "" || state != "" {
		target += "?code=" + code + "&state=" + state
	}
	req := httptest.NewRequest(http.MethodGet, target, nil)
	ct.mux.ServeHTTP(rec, req)
	return rec
}

// ─── happy paths ────────────────────────────────────────────────────────────

func TestCallback_HappyPath_JITDefaultRole(t *testing.T) {
	ct := newCallbackTest(t)
	state, data := ct.generateState("conn-1")
	ct.idp.SetNextToken(ct.claimsFor(data.Nonce))

	rec := ct.hit("conn-1", "auth-code-xyz", state)

	if rec.Code != http.StatusFound {
		t.Fatalf("status: got %d want %d. body=%q", rec.Code, http.StatusFound, rec.Body.String())
	}
	if loc := rec.Header().Get("Location"); loc != "/dashboard" {
		t.Errorf("redirect: got %q want /dashboard", loc)
	}
	if cookie := rec.Header().Get("Set-Cookie"); !strings.Contains(cookie, "axiaops_session=stub-session-token") {
		t.Errorf("session cookie missing or wrong: %q", cookie)
	}
	if !ct.minter.called {
		t.Error("MintSession was not called")
	}
	if ct.minter.last.AuthMode != model.AuthModeSSO {
		t.Errorf("session AuthMode: got %q want %q", ct.minter.last.AuthMode, model.AuthModeSSO)
	}
	if got := ct.minter.last.UserID; got != "user-idp-sub-123" {
		t.Errorf("session UserID: got %q want user-idp-sub-123", got)
	}
	if len(ct.store.saved) != 1 {
		t.Fatalf("memberships saved: got %d want 1", len(ct.store.saved))
	}
	if got := ct.store.saved[0].Role; got != "viewer" {
		t.Errorf("default role: got %q want viewer", got)
	}
	if !ct.store.hasAudit(model.AuditActionSSOJITProvisioned) {
		t.Error("AuditActionSSOJITProvisioned not written")
	}
	if !ct.store.hasAudit(model.AuditActionSSOLoginSucceeded) {
		t.Error("AuditActionSSOLoginSucceeded not written")
	}
}

func TestCallback_HappyPath_GroupMappingPrecedence(t *testing.T) {
	ct := newCallbackTest(t)
	ct.store.mappings = []model.SSOGroupMapping{
		{GroupExternalID: "engineers", Role: "member"},
		{GroupExternalID: "platform-admins", Role: "admin"},
	}
	state, data := ct.generateState("conn-1")
	claims := ct.claimsFor(data.Nonce)
	claims["groups"] = []any{"engineers", "platform-admins"}
	ct.idp.SetNextToken(claims)

	rec := ct.hit("conn-1", "auth-code-xyz", state)
	if rec.Code != http.StatusFound {
		t.Fatalf("status: %d body=%s", rec.Code, rec.Body.String())
	}
	if len(ct.store.saved) != 1 {
		t.Fatalf("memberships saved: got %d want 1", len(ct.store.saved))
	}
	if got := ct.store.saved[0].Role; got != "admin" {
		t.Errorf("highest-role precedence: got %q want admin", got)
	}
}

func TestCallback_HappyPath_JITRoleUpdated(t *testing.T) {
	ct := newCallbackTest(t)
	// Simulate a re-login where the user already has a viewer membership
	// and group claims now resolve to admin.
	ct.store.existingMembership = &model.Membership{
		ID:             "m-existing",
		UserID:         "user-idp-sub-123",
		OrganizationID: "org-test",
		Role:           "viewer",
		// ProvisionedVia=jit — the role-reconcile path applies only to
		// JIT-placed memberships (provenance guard added post-merge).
		ProvisionedVia: model.ProvisionedViaJIT,
	}
	ct.store.mappings = []model.SSOGroupMapping{
		{GroupExternalID: "platform-admins", Role: "admin"},
	}
	state, data := ct.generateState("conn-1")
	claims := ct.claimsFor(data.Nonce)
	claims["groups"] = []any{"platform-admins"}
	ct.idp.SetNextToken(claims)

	rec := ct.hit("conn-1", "auth-code-xyz", state)
	if rec.Code != http.StatusFound {
		t.Fatalf("status: %d body=%s", rec.Code, rec.Body.String())
	}
	if got := ct.store.existingMembership.Role; got != "admin" {
		t.Errorf("role updated: got %q want admin", got)
	}
	if !ct.store.hasAudit(model.AuditActionSSOJITRoleUpdated) {
		t.Error("AuditActionSSOJITRoleUpdated not written on role change")
	}
	if ct.store.hasAudit(model.AuditActionSSOJITProvisioned) {
		t.Error("AuditActionSSOJITProvisioned written on role-update path (should be jit_role_updated)")
	}
}

// TestCallback_InvitePlacedMembership_NotOverwrittenByJIT pins the
// post-merge race fix at the callback level: a user who already has an
// invitation-placed membership (admin chose admin role via /v1/invitations)
// and then logs in via SSO with group claims that resolve to a lower role
// must NOT have their role silently downgraded. Closes the cross-flow race
// between POST /v1/auth/invitations/redeem and the SSO callback's
// invite-redeem step. Also exercises the same guard for the simpler
// re-login-after-manual-promotion case.
func TestCallback_InvitePlacedMembership_NotOverwrittenByJIT(t *testing.T) {
	ct := newCallbackTest(t)
	// Existing admin membership placed by /v1/invitations (the loser of
	// the FOR-UPDATE race scenario sees this row when it falls through
	// from RedeemPendingInvitation → JIT).
	ct.store.existingMembership = &model.Membership{
		ID:             "m-existing",
		UserID:         "user-idp-sub-123",
		OrganizationID: "org-test",
		Role:           "admin",
		ProvisionedVia: model.ProvisionedViaInvitation,
	}
	ct.store.mappings = []model.SSOGroupMapping{
		{GroupExternalID: "engineers", Role: "member"}, // SSO resolves lower
	}
	state, data := ct.generateState("conn-1")
	claims := ct.claimsFor(data.Nonce)
	claims["groups"] = []any{"engineers"}
	ct.idp.SetNextToken(claims)

	rec := ct.hit("conn-1", "auth-code-xyz", state)
	if rec.Code != http.StatusFound {
		t.Fatalf("status: %d body=%s", rec.Code, rec.Body.String())
	}
	if got := ct.store.existingMembership.Role; got != "admin" {
		t.Errorf("admin role overwritten by JIT: got %q want admin (the bug the provenance guard prevents)", got)
	}
	// Neither JIT audit fires on the noop path — re-login on a sticky
	// admin-placed row is not a JIT event.
	if ct.store.hasAudit(model.AuditActionSSOJITRoleUpdated) {
		t.Error("AuditActionSSOJITRoleUpdated written on invitation-placed row — guard bypassed")
	}
	if ct.store.hasAudit(model.AuditActionSSOJITProvisioned) {
		t.Error("AuditActionSSOJITProvisioned written on existing membership path")
	}
	if !ct.store.hasAudit(model.AuditActionSSOLoginSucceeded) {
		t.Error("AuditActionSSOLoginSucceeded missing on guard-skipped path")
	}
}

func TestCallback_HappyPath_JITNoopOnUnchangedRole(t *testing.T) {
	ct := newCallbackTest(t)
	// Re-login: user already has membership at the role the resolver will
	// return. JITOutcomeNoop must skip both jit_provisioned AND
	// jit_role_updated audit — auditing every SSO login as a JIT event
	// would drown the trail in noise.
	ct.store.existingMembership = &model.Membership{
		ID:             "m-existing",
		UserID:         "user-idp-sub-123",
		OrganizationID: "org-test",
		Role:           "viewer", // matches conn.DefaultRole; no mappings present
	}
	state, data := ct.generateState("conn-1")
	ct.idp.SetNextToken(ct.claimsFor(data.Nonce))

	rec := ct.hit("conn-1", "auth-code-xyz", state)
	if rec.Code != http.StatusFound {
		t.Fatalf("status: %d body=%s", rec.Code, rec.Body.String())
	}
	if got := ct.store.existingMembership.Role; got != "viewer" {
		t.Errorf("role should be unchanged: got %q want viewer", got)
	}
	if ct.store.hasAudit(model.AuditActionSSOJITProvisioned) {
		t.Error("AuditActionSSOJITProvisioned written on noop path (should be silent)")
	}
	if ct.store.hasAudit(model.AuditActionSSOJITRoleUpdated) {
		t.Error("AuditActionSSOJITRoleUpdated written on noop path (should be silent)")
	}
	if !ct.store.hasAudit(model.AuditActionSSOLoginSucceeded) {
		t.Error("AuditActionSSOLoginSucceeded missing on noop happy path")
	}
}

func TestCallback_InvitationRedeemError_FailsLogin(t *testing.T) {
	ct := newCallbackTest(t)
	ct.store.pendingErr = errors.New("DB hiccup")
	state, data := ct.generateState("conn-1")
	ct.idp.SetNextToken(ct.claimsFor(data.Nonce))

	rec := ct.hit("conn-1", "auth-code-xyz", state)
	assertCallbackError(t, rec, ct.minter)
	// Critical: must NOT silently fall through to JIT — admin-chosen role
	// would otherwise be silently replaced.
	if len(ct.store.saved) != 0 {
		t.Errorf("invite-redeem failure must not provision via JIT; saved=%v", ct.store.saved)
	}
	if !ct.store.hasAudit(model.AuditActionSSOLoginFailed) {
		t.Error("AuditActionSSOLoginFailed not written on invite-redeem failure")
	}
}

func TestCallback_HappyPath_PendingInviteWinsOverJIT(t *testing.T) {
	ct := newCallbackTest(t)
	ct.store.pendingInvite = true
	ct.store.mappings = []model.SSOGroupMapping{
		{GroupExternalID: "platform-admins", Role: "admin"},
	}
	state, data := ct.generateState("conn-1")
	claims := ct.claimsFor(data.Nonce)
	claims["groups"] = []any{"platform-admins"}
	ct.idp.SetNextToken(claims)

	rec := ct.hit("conn-1", "auth-code-xyz", state)
	if rec.Code != http.StatusFound {
		t.Fatalf("status: %d", rec.Code)
	}
	// JIT must NOT have been invoked — the invitation provides the role.
	if len(ct.store.saved) != 0 {
		t.Errorf("invite precedence: SaveMembership called even though pending invite exists; saved=%v", ct.store.saved)
	}
	if ct.store.hasAudit(model.AuditActionSSOJITProvisioned) {
		t.Error("AuditActionSSOJITProvisioned written even though invite took precedence")
	}
	if !ct.store.hasAudit(model.AuditActionSSOLoginSucceeded) {
		t.Error("AuditActionSSOLoginSucceeded missing on invite-precedence path")
	}
}

// ─── pre-token failure paths (no audit, no session minted) ──────────────────

func TestCallback_MissingCode_RedirectsToLoginError(t *testing.T) {
	ct := newCallbackTest(t)
	state, _ := ct.generateState("conn-1")
	rec := ct.hit("conn-1", "", state)
	assertCallbackError(t, rec, ct.minter)
}

func TestCallback_MissingState_RedirectsToLoginError(t *testing.T) {
	ct := newCallbackTest(t)
	rec := ct.hit("conn-1", "auth-code-xyz", "")
	assertCallbackError(t, rec, ct.minter)
}

func TestCallback_UnknownState_RedirectsToLoginError(t *testing.T) {
	ct := newCallbackTest(t)
	rec := ct.hit("conn-1", "auth-code-xyz", "never-issued-state")
	assertCallbackError(t, rec, ct.minter)
}

func TestCallback_StateCIDMismatch_RedirectsToLoginError(t *testing.T) {
	ct := newCallbackTest(t)
	state, _ := ct.generateState("conn-1") // state issued for conn-1 ...
	rec := ct.hit("conn-2", "auth-code-xyz", state) // ... but presented at conn-2
	assertCallbackError(t, rec, ct.minter)
}

// ─── post-connection failure paths (audit login_failed; no session) ────────

func TestCallback_TokenEndpointRejects_AuditsAndRedirects(t *testing.T) {
	ct := newCallbackTest(t)
	state, _ := ct.generateState("conn-1")
	ct.idp.SetTokenError("invalid_grant")

	rec := ct.hit("conn-1", "auth-code-xyz", state)
	assertCallbackError(t, rec, ct.minter)
	if !ct.store.hasAudit(model.AuditActionSSOLoginFailed) {
		t.Error("AuditActionSSOLoginFailed not written on token-endpoint failure")
	}
}

func TestCallback_NonceMismatch_AuditsAndRedirects(t *testing.T) {
	ct := newCallbackTest(t)
	state, _ := ct.generateState("conn-1")
	claims := ct.claimsFor("wrong-nonce-from-attacker")
	ct.idp.SetNextToken(claims)

	rec := ct.hit("conn-1", "auth-code-xyz", state)
	assertCallbackError(t, rec, ct.minter)
	if !ct.store.hasAudit(model.AuditActionSSOLoginFailed) {
		t.Error("AuditActionSSOLoginFailed not written on id-token validation failure")
	}
}

func TestCallback_DomainNotVerified_AuditsAndRedirects(t *testing.T) {
	ct := newCallbackTest(t)
	ct.store.domainErr = storage.ErrSSODomainNotFound
	state, data := ct.generateState("conn-1")
	ct.idp.SetNextToken(ct.claimsFor(data.Nonce))

	rec := ct.hit("conn-1", "auth-code-xyz", state)
	assertCallbackError(t, rec, ct.minter)
	if !ct.store.hasAudit(model.AuditActionSSOLoginFailed) {
		t.Error("AuditActionSSOLoginFailed not written on domain-unverified")
	}
}

func TestCallback_DomainBoundToDifferentConnection_AuditsAndRedirects(t *testing.T) {
	ct := newCallbackTest(t)
	// Domain belongs to a different connection in the same org — anti
	// cross-routing guard.
	ct.store.domain.SSOConnectionID = "other-conn"
	state, data := ct.generateState("conn-1")
	ct.idp.SetNextToken(ct.claimsFor(data.Nonce))

	rec := ct.hit("conn-1", "auth-code-xyz", state)
	assertCallbackError(t, rec, ct.minter)
	if !ct.store.hasAudit(model.AuditActionSSOLoginFailed) {
		t.Error("AuditActionSSOLoginFailed not written on cross-connection domain")
	}
}

func TestCallback_StateIsSingleUse(t *testing.T) {
	ct := newCallbackTest(t)
	state, data := ct.generateState("conn-1")
	ct.idp.SetNextToken(ct.claimsFor(data.Nonce))

	// First call — happy path.
	rec1 := ct.hit("conn-1", "auth-code-xyz", state)
	if rec1.Code != http.StatusFound || rec1.Header().Get("Location") != "/dashboard" {
		t.Fatalf("first call must succeed; got %d %s", rec1.Code, rec1.Header().Get("Location"))
	}

	// Second call with same state — must fail at state-consume.
	ct.idp.SetNextToken(ct.claimsFor(data.Nonce)) // re-arm, would otherwise 400
	rec2 := ct.hit("conn-1", "auth-code-xyz", state)
	if rec2.Code != http.StatusFound || rec2.Header().Get("Location") != "/login?error=auth_failed" {
		t.Errorf("replayed state must fail at consume; got %d %q", rec2.Code, rec2.Header().Get("Location"))
	}
}

// ─── helpers ────────────────────────────────────────────────────────────────

func assertCallbackError(t *testing.T, rec *httptest.ResponseRecorder, minter *stubMinter) {
	t.Helper()
	if rec.Code != http.StatusFound {
		t.Fatalf("status: got %d want %d (302 to /login)", rec.Code, http.StatusFound)
	}
	if loc := rec.Header().Get("Location"); loc != "/login?error=auth_failed" {
		t.Errorf("error redirect: got %q want /login?error=auth_failed", loc)
	}
	if minter.called {
		t.Error("MintSession was called on a failure path")
	}
	if cookie := rec.Header().Get("Set-Cookie"); cookie != "" {
		t.Errorf("session cookie set on failure path: %q", cookie)
	}
}
