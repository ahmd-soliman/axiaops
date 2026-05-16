package sso_test

import (
	"context"
	"net/url"
	"strings"
	"testing"

	"axiaops.io/api/internal/sso"
	"axiaops.io/shared/crypto"
	"axiaops.io/shared/model"
	"axiaops.io/shared/storage"
)

// TestLogoutResolver_NativeSession_ReturnsEmpty pins the native fast-path:
// password / bootstrap sessions never produce a logout URL because there is
// no IdP session to invalidate. The handler treats empty URL as "fall back
// to 204", which preserves the legacy logout shape for native users.
func TestLogoutResolver_NativeSession_ReturnsEmpty(t *testing.T) {
	r := sso.NewLogoutResolver(&fakeLogoutStore{}, &fakeDiscoveryFetcher{}, "https://app.example.com")
	url, err := r.ResolveLogoutURL(context.Background(), model.Session{
		AuthMode:         model.AuthModePassword,
		IDTokenEncrypted: "shouldnotbereadbecauseitsnative",
	})
	if err != nil {
		t.Fatalf("ResolveLogoutURL: native session: unexpected error: %v", err)
	}
	if url != "" {
		t.Errorf("ResolveLogoutURL: native session: expected empty URL, got %q", url)
	}
}

// TestLogoutResolver_SSOWithoutIDToken_ReturnsEmpty pins the early-out for
// SSO sessions whose id_token wasn't captured (pre-migration-027 sessions,
// or callbacks where encryption fell through). Without a hint we can't ask
// the IdP to silently log out — fall back to 204 rather than building a
// half-formed URL the OP will reject.
func TestLogoutResolver_SSOWithoutIDToken_ReturnsEmpty(t *testing.T) {
	r := sso.NewLogoutResolver(&fakeLogoutStore{}, &fakeDiscoveryFetcher{}, "https://app.example.com")
	url, err := r.ResolveLogoutURL(context.Background(), model.Session{
		UserID:           "u1",
		AuthMode:         model.AuthModeSSO,
		IDTokenEncrypted: "",
	})
	if err != nil {
		t.Fatalf("ResolveLogoutURL: sso w/o id_token: unexpected error: %v", err)
	}
	if url != "" {
		t.Errorf("ResolveLogoutURL: sso w/o id_token: expected empty URL, got %q", url)
	}
}

// TestLogoutResolver_UserNotFound_ReturnsEmpty pins right-to-erasure
// behaviour. If the user row is gone (cascade-deleted) the session is
// orphaned but logout should still complete — collapse to empty URL so the
// handler responds 204.
func TestLogoutResolver_UserNotFound_ReturnsEmpty(t *testing.T) {
	r := sso.NewLogoutResolver(
		&fakeLogoutStore{getConnIDErr: storage.ErrUserNotFound},
		&fakeDiscoveryFetcher{},
		"https://app.example.com",
	)
	url, err := r.ResolveLogoutURL(context.Background(), withEncrypted(t, model.Session{
		UserID:   "u-deleted",
		AuthMode: model.AuthModeSSO,
	}, "raw-id-token"))
	if err != nil {
		t.Fatalf("ResolveLogoutURL: erased user: unexpected error: %v", err)
	}
	if url != "" {
		t.Errorf("ResolveLogoutURL: erased user: expected empty URL, got %q", url)
	}
}

// TestLogoutResolver_NoConnection_ReturnsEmpty pins the demoted-user case:
// the user row exists but sso_connection_id is NULL (the connection was
// deleted with ON DELETE SET NULL). Nothing to log out from on the IdP
// side; return empty so the handler 204s.
func TestLogoutResolver_NoConnection_ReturnsEmpty(t *testing.T) {
	r := sso.NewLogoutResolver(
		&fakeLogoutStore{userConnID: ""},
		&fakeDiscoveryFetcher{},
		"https://app.example.com",
	)
	url, err := r.ResolveLogoutURL(context.Background(), withEncrypted(t, model.Session{
		UserID:   "u1",
		AuthMode: model.AuthModeSSO,
	}, "raw-id-token"))
	if err != nil {
		t.Fatalf("ResolveLogoutURL: nil connection: unexpected error: %v", err)
	}
	if url != "" {
		t.Errorf("ResolveLogoutURL: nil connection: expected empty URL, got %q", url)
	}
}

// TestLogoutResolver_NoEndSessionEndpoint_ReturnsEmpty pins the older-OP
// case — Discovery returns a doc without end_session_endpoint. Older OIDC
// OPs and minimally-conformant federations omit it; fall back rather than
// fabricating a URL.
func TestLogoutResolver_NoEndSessionEndpoint_ReturnsEmpty(t *testing.T) {
	r := sso.NewLogoutResolver(
		&fakeLogoutStore{userConnID: "conn-1"},
		&fakeDiscoveryFetcher{doc: sso.DiscoveryDoc{Issuer: "https://idp.example.com"}},
		"https://app.example.com",
	)
	url, err := r.ResolveLogoutURL(context.Background(), withEncrypted(t, model.Session{
		UserID:   "u1",
		AuthMode: model.AuthModeSSO,
	}, "raw-id-token"))
	if err != nil {
		t.Fatalf("ResolveLogoutURL: no end_session_endpoint: unexpected error: %v", err)
	}
	if url != "" {
		t.Errorf("ResolveLogoutURL: no end_session_endpoint: expected empty URL, got %q", url)
	}
}

// TestLogoutResolver_HappyPath_ReturnsCompleteURL is the load-bearing test:
// when every component lines up, the resolver returns an end_session_endpoint
// URL with id_token_hint, client_id, and post_logout_redirect_uri all
// correctly populated. id_token_hint is what makes the IdP skip its confirm
// prompt — verify it round-trips through the decrypt step.
func TestLogoutResolver_HappyPath_ReturnsCompleteURL(t *testing.T) {
	r := sso.NewLogoutResolver(
		&fakeLogoutStore{
			userConnID: "conn-1",
			conn: model.SSOConnection{
				ID:           "conn-1",
				OIDCClientID: "axiaops-prod",
			},
		},
		&fakeDiscoveryFetcher{
			doc: sso.DiscoveryDoc{
				EndSessionEndpoint: "https://idp.example.com/realms/main/protocol/openid-connect/logout",
			},
		},
		"https://app.example.com/",
	)

	rawIDToken := "header.payload.signature"
	got, err := r.ResolveLogoutURL(context.Background(), withEncrypted(t, model.Session{
		UserID:   "u1",
		AuthMode: model.AuthModeSSO,
	}, rawIDToken))
	if err != nil {
		t.Fatalf("ResolveLogoutURL: happy path: unexpected error: %v", err)
	}
	if got == "" {
		t.Fatalf("ResolveLogoutURL: happy path: expected non-empty URL, got empty")
	}

	parsed, err := url.Parse(got)
	if err != nil {
		t.Fatalf("ResolveLogoutURL: happy path: returned URL is unparseable: %v", err)
	}
	if parsed.Scheme+"://"+parsed.Host+parsed.Path != "https://idp.example.com/realms/main/protocol/openid-connect/logout" {
		t.Errorf("ResolveLogoutURL: wrong base URL: %s", parsed.Scheme+"://"+parsed.Host+parsed.Path)
	}
	q := parsed.Query()
	if got := q.Get("id_token_hint"); got != rawIDToken {
		t.Errorf("id_token_hint: got %q want %q (decrypt step is what makes the IdP skip its confirm prompt)", got, rawIDToken)
	}
	if got := q.Get("client_id"); got != "axiaops-prod" {
		t.Errorf("client_id: got %q want %q", got, "axiaops-prod")
	}
	// Trailing slash on publicHost should be stripped before joining /login.
	if got := q.Get("post_logout_redirect_uri"); got != "https://app.example.com/login" {
		t.Errorf("post_logout_redirect_uri: got %q want %q", got, "https://app.example.com/login")
	}
}

// TestLogoutResolver_PreservesExistingQueryString pins the rare-IdP case:
// an end_session_endpoint that already carries a query string (some
// multi-tenant IdPs append a tenant param). The resolver must use & not ?
// when appending — otherwise the IdP sees a malformed URL and rejects.
func TestLogoutResolver_PreservesExistingQueryString(t *testing.T) {
	r := sso.NewLogoutResolver(
		&fakeLogoutStore{
			userConnID: "conn-1",
			conn:       model.SSOConnection{ID: "conn-1", OIDCClientID: "c"},
		},
		&fakeDiscoveryFetcher{
			doc: sso.DiscoveryDoc{
				EndSessionEndpoint: "https://idp.example.com/logout?tenant=acme",
			},
		},
		"https://app.example.com",
	)
	got, err := r.ResolveLogoutURL(context.Background(), withEncrypted(t, model.Session{
		UserID: "u1", AuthMode: model.AuthModeSSO,
	}, "tok"))
	if err != nil {
		t.Fatalf("ResolveLogoutURL: query-string passthrough: unexpected error: %v", err)
	}
	if !strings.Contains(got, "?tenant=acme&") {
		t.Errorf("ResolveLogoutURL: expected '?tenant=acme&' in URL (existing query string preserved); got %q", got)
	}
}

// TestLogoutResolver_DecryptFailure_ReturnsError pins that an unrecoverable
// decrypt error (corrupt ciphertext, key rotated underneath us) propagates
// to the handler as a real error rather than collapsing to empty. The
// handler logs and falls back to 204; we want the warning to fire so a
// rotated key surfaces in ops dashboards.
func TestLogoutResolver_DecryptFailure_ReturnsError(t *testing.T) {
	r := sso.NewLogoutResolver(
		&fakeLogoutStore{
			userConnID: "conn-1",
			conn:       model.SSOConnection{ID: "conn-1", OIDCClientID: "c"},
		},
		&fakeDiscoveryFetcher{doc: sso.DiscoveryDoc{EndSessionEndpoint: "https://idp.example.com/logout"}},
		"https://app.example.com",
	)
	got, err := r.ResolveLogoutURL(context.Background(), model.Session{
		UserID:           "u1",
		AuthMode:         model.AuthModeSSO,
		IDTokenEncrypted: "deadbeefnotvalidhex",
	})
	if err == nil {
		t.Fatalf("ResolveLogoutURL: decrypt failure: expected error, got nil (URL=%q)", got)
	}
	if got != "" {
		t.Errorf("ResolveLogoutURL: decrypt failure: expected empty URL on error, got %q", got)
	}
}

// ── fakes ──────────────────────────────────────────────────────────────────

// fakeLogoutStore implements just enough of storage.Store to satisfy the
// resolver. Other methods panic on call so a regression that adds a new
// dependency on the store fails loudly rather than silently passing.
type fakeLogoutStore struct {
	storage.Store // embed for the methods we don't implement — they'll panic on call

	userConnID   string
	getConnIDErr error
	conn         model.SSOConnection
	getConnErr   error
}

func (f *fakeLogoutStore) GetUserSSOConnectionID(_ context.Context, _ string) (string, error) {
	if f.getConnIDErr != nil {
		return "", f.getConnIDErr
	}
	return f.userConnID, nil
}

func (f *fakeLogoutStore) GetSSOConnectionByID(_ context.Context, _ string) (model.SSOConnection, error) {
	if f.getConnErr != nil {
		return model.SSOConnection{}, f.getConnErr
	}
	return f.conn, nil
}

type fakeDiscoveryFetcher struct {
	doc sso.DiscoveryDoc
	err error
}

func (f *fakeDiscoveryFetcher) Discovery(_ context.Context, _ model.SSOConnection) (sso.DiscoveryDoc, error) {
	if f.err != nil {
		return sso.DiscoveryDoc{}, f.err
	}
	return f.doc, nil
}

// withEncrypted is a test helper that encrypts plaintext with the
// well-known test key (set via t.Setenv on first use) and stores the
// ciphertext on the supplied session. The encrypt → decrypt round-trip is
// load-bearing for the happy-path test, so we can't use a fake crypto
// primitive — we exercise the real one.
func withEncrypted(t *testing.T, sess model.Session, plaintext string) model.Session {
	t.Helper()
	t.Setenv("ENCRYPTION_KEY", "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef")
	enc, err := crypto.Encrypt(plaintext)
	if err != nil {
		t.Fatalf("withEncrypted: crypto.Encrypt: %v", err)
	}
	sess.IDTokenEncrypted = enc
	return sess
}

