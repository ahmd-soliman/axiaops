package sso

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/url"
	"strings"

	"axiaops.io/shared/model"
	"axiaops.io/shared/storage"
)

// Connector is the seam for connection-side mutations — Save
// (insert/update), Delete, and (slice 4+) live IdP probe via Test.
// Handlers depend only on this interface, not on the concrete impl. A
// future impl could add a kindeConnector that mirrors connections to the
// Kinde Mgmt API and populates kinde_connection_id; B2 ships only
// NativeConnector.
type Connector interface {
	// Save creates a new connection (when c.ID is empty) or updates an
	// existing one. Performs Option-B validation: rejects non-empty
	// kinde_connection_id (per design §4.2). Slice 4+ will extend with
	// discovery-doc fetch + cert validation; for the skeleton it's an
	// unsmart pass-through to the store.
	Save(ctx context.Context, c model.SSOConnection) (model.SSOConnection, error)

	// Delete removes a connection (CASCADE drops its domains + group mappings).
	Delete(ctx context.Context, id string) error

	// Get returns a single connection. Surfaces storage.ErrSSOConnectionNotFound.
	Get(ctx context.Context, id string) (model.SSOConnection, error)

	// List returns all connections in the request org.
	List(ctx context.Context) ([]model.SSOConnection, error)

	// Test performs a synthetic auth probe against the IdP. Returns a reason
	// code when the probe fails. NOT IMPLEMENTED in B2 slice 3 (skeleton);
	// the OIDC RP slice will fill this in. The interface entry exists now so
	// the handler signature doesn't churn between slices.
	Test(ctx context.Context, id string) (TestResult, error)
}

// TestResult is the outcome of a Connector.Test probe. Reason is empty on
// success; on failure it's one of a fixed enum (slice 4+ defines the values).
type TestResult struct {
	OK     bool
	Reason string
}

// ErrConnectorTestNotImplemented is returned by Connector.Test in B2 slice 3.
// The synthetic auth probe lands in the OIDC RP slice once oidc.go exists.
var ErrConnectorTestNotImplemented = errors.New("sso: connector test not implemented in B2 slice 3 (OIDC ceremony lands in subsequent slice)")

// NativeConnector is the on-prem / self-hosted impl. Pass-through over the
// store with Option-B validation at the boundary.
type NativeConnector struct {
	store storage.Store
}

// NewNativeConnector constructs a connector over the given store.
func NewNativeConnector(store storage.Store) *NativeConnector {
	return &NativeConnector{store: store}
}

// Save handles both create (empty ID) and update. Option-B validation:
// kinde_connection_id MUST be empty in self-hosted mode. The DB CHECK
// constraint also rejects status=active+oidc with empty client_secret —
// that's a backstop, not a primary validation.
//
// Discovery / metadata URLs are required to be https. Audit H-3: a
// deceptive admin can otherwise route the OIDC ceremony over plain HTTP,
// exposing client_secret on the token POST to any LAN-side observer
// (we sit behind nginx but the IdP hop happens before that). Loopback
// http://localhost is permitted so local fake-IdP tests keep working.
func (n *NativeConnector) Save(ctx context.Context, c model.SSOConnection) (model.SSOConnection, error) {
	if strings.TrimSpace(c.KindeConnectionID) != "" {
		return model.SSOConnection{}, fmt.Errorf("sso: kinde_connection_id must be empty under self-hosted deployment (Option B per design §4.2)")
	}
	if err := RequireHTTPS(c.OIDCDiscoveryURL, "oidc_discovery_url"); err != nil {
		return model.SSOConnection{}, err
	}
	if err := RequireHTTPS(c.IdPMetadataURL, "idp_metadata_url"); err != nil {
		return model.SSOConnection{}, err
	}
	if c.ID == "" {
		return n.store.CreateSSOConnection(ctx, c)
	}
	if err := n.store.UpdateSSOConnection(ctx, c); err != nil {
		return model.SSOConnection{}, err
	}
	// TOCTOU note: a concurrent PATCH on the same connection can land
	// between this Update and the follow-up Get, so the returned row may
	// reflect the racing caller's state rather than ours. Cosmetic in
	// practice (admins rarely mutate the same connection within ms), but
	// real. The principled fix is to add a RETURNING clause to
	// UpdateSSOConnection — deferred to keep the Store interface stable
	// while the OIDC ceremony slice is in flight.
	return n.store.GetSSOConnection(ctx, c.ID)
}

func (n *NativeConnector) Delete(ctx context.Context, id string) error {
	return n.store.DeleteSSOConnection(ctx, id)
}

func (n *NativeConnector) Get(ctx context.Context, id string) (model.SSOConnection, error) {
	return n.store.GetSSOConnection(ctx, id)
}

func (n *NativeConnector) List(ctx context.Context) ([]model.SSOConnection, error) {
	return n.store.ListSSOConnections(ctx)
}

// Test is a placeholder in B2 slice 3 — returns the documented sentinel.
// Subsequent slices fill this in by sourcing the IdP discovery doc and
// running a synthetic flow against it.
func (n *NativeConnector) Test(_ context.Context, _ string) (TestResult, error) {
	return TestResult{OK: false, Reason: "not_implemented"}, ErrConnectorTestNotImplemented
}

var _ Connector = (*NativeConnector)(nil)

// RequireHTTPS enforces https:// on the IdP-facing URL fields persisted on a
// connection. Empty is allowed (the field is optional for non-OIDC flows or
// when discovery is disabled). Loopback http://localhost / 127.0.0.1 / [::1]
// is permitted so the local fake-IdP test fixture keeps working without TLS.
//
// The loopback check parses the URL and inspects the hostname via net.ParseIP
// (which normalises IPv6 variants like ::1, ::0001, 0:0:0:0:0:0:0:1 — RFC 4291
// — to the same address) rather than string-matching on prefixes. A naive
// prefix match on "http://[::1]" would miss legitimate equivalent forms and
// would also be vulnerable to substring tricks like
// "http://attacker.evil/[::1].txt"; parsing isolates the hostname so the path
// can never spoof loopback. Exported so the connector test (package sso_test,
// black-box per the project convention) can exercise the validator directly.
func RequireHTTPS(raw, field string) error {
	v := strings.TrimSpace(raw)
	if v == "" {
		return nil
	}
	lower := strings.ToLower(v)
	if strings.HasPrefix(lower, "https://") {
		return nil
	}
	if strings.HasPrefix(lower, "http://") && isLoopbackHTTP(v) {
		return nil
	}
	return fmt.Errorf("sso: %s must use https:// (got %q) — plain HTTP leaks client_secret on the token POST", field, raw)
}

// isLoopbackHTTP reports whether raw is an http:// URL whose host is a
// loopback address — "localhost" (case-insensitive), IPv4 127.0.0.0/8, or
// any IPv6 loopback form (::1, [::0001], [0:0:0:0:0:0:0:1]). The URL must
// parse and have an http (not https) scheme; callers that need https
// already short-circuited.
func isLoopbackHTTP(raw string) bool {
	u, err := url.Parse(raw)
	if err != nil {
		return false
	}
	if !strings.EqualFold(u.Scheme, "http") {
		return false
	}
	host := u.Hostname() // strips brackets for IPv6, strips :port for both families
	if host == "" {
		return false
	}
	if strings.EqualFold(host, "localhost") {
		return true
	}
	if ip := net.ParseIP(host); ip != nil && ip.IsLoopback() {
		return true
	}
	return false
}
