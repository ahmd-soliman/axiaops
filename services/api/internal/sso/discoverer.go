// Package sso implements the per-organisation SSO surface (Phase B2).
//
// The package owns:
//   - The Discoverer / Connector seams that decouple the HTTP handlers
//     from the storage layer and from any particular provider impl.
//   - HTTP CRUD on connections, domains, and group mappings.
//   - The pre-auth /v1/sso/discover handler that routes a login email's
//     domain to a connection.
//   - Domain DNS verification (TXT record + public-suffix-list rejection).
//   - JIT role resolution (group → role precedence; owner-never-via-JIT).
//   - The 24h sweep ticker for stale domains and (Phase C) replay rows.
//
// Out of scope for B2 slice 3 (the backend skeleton): OIDC RP ceremony
// (oidc.go, oidc_callback.go, initiate.go), the synthetic auth probe (test.go),
// the mockoidc integration test, and the frontend. Those land in subsequent
// slices that build on this skeleton.
package sso

import (
	"context"
	"errors"
	"net/url"
	"strings"
	"time"

	"axiaops.io/shared/storage"
)

// Discoverer maps a login email's domain to a connection. The HTTP handler
// for GET /v1/sso/discover delegates to this interface — handler is impl-
// agnostic. Today only NativeDiscoverer ships; a future impl could add a
// compositeDiscoverer wrapping native + an external IdP's management API.
//
// Lookup is constant-shape (always returns DiscoverResult, never errors for
// "not found") so the handler response shape doesn't fork on the discovery
// outcome — a uniform response shape narrows the timing/observation channel
// an attacker can use to enumerate which orgs use SSO.
type Discoverer interface {
	Discover(ctx context.Context, email string) (DiscoverResult, error)
}

// DiscoverResult is the HasSSO + RedirectURL outcome of a Discover call.
// HasSSO=false carries an empty RedirectURL; the dashboard then reveals the
// password field. RedirectURL is the connection-initiate URL the dashboard
// will window.location.assign() to when HasSSO=true.
type DiscoverResult struct {
	HasSSO          bool   `json:"has_sso"`
	RedirectURL     string `json:"redirect_url,omitempty"`
	ConnectionID    string `json:"-"` // for audit/log purposes; never returned over the wire
	OrganizationID  string `json:"-"`
	Protocol        string `json:"-"`
}

// NativeDiscoverer is the on-prem / self-hosted implementation. It hits
// sso_domains via the admin pool (pre-auth lookup, no org context).
type NativeDiscoverer struct {
	store storage.Store
	// publicHost is the externally-reachable origin used to build the
	// redirect URL ("https://app.example.com"). Empty produces relative URLs
	// the frontend resolves against window.location.origin.
	publicHost string
}

// NewNativeDiscoverer constructs a discoverer over the given store.
func NewNativeDiscoverer(store storage.Store, publicHost string) *NativeDiscoverer {
	return &NativeDiscoverer{store: store, publicHost: strings.TrimRight(publicHost, "/")}
}

// Discover looks up the email's domain in sso_domains. Returns
// (DiscoverResult{HasSSO:false}, nil) on any miss — collapses
// ErrSSODomainNotFound so the handler doesn't have to special-case the
// expected "not found" path. Real DB errors propagate so the handler can
// log them.
func (d *NativeDiscoverer) Discover(ctx context.Context, email string) (DiscoverResult, error) {
	domain := emailDomain(email)
	if domain == "" {
		return DiscoverResult{HasSSO: false}, nil
	}

	dom, err := d.store.GetVerifiedSSODomainByName(ctx, domain)
	if errors.Is(err, storage.ErrSSODomainNotFound) {
		return DiscoverResult{HasSSO: false}, nil
	}
	if err != nil {
		return DiscoverResult{HasSSO: false}, err
	}

	// Forward the typed email to /initiate as ?email=<urlencoded>. /initiate
	// turns it into the OIDC `login_hint` param so the IdP login form is
	// pre-populated (RFC 6749 §3.1.2 + OIDC Core §3.1.2.1). Pure UX polish —
	// `prompt=login` already forces fresh auth on the IdP side, login_hint
	// only affects the rendered username field. We URL-encode via url.Values
	// so `+` (e.g. `alice+test@acme.com`) and other reserved chars round-trip.
	q := url.Values{}
	q.Set("email", email)
	return DiscoverResult{
		HasSSO:         true,
		RedirectURL:    d.publicHost + "/v1/sso/oidc/" + dom.SSOConnectionID + "/initiate?" + q.Encode(),
		ConnectionID:   dom.SSOConnectionID,
		OrganizationID: dom.OrganizationID,
		// Protocol is on the connection, not the domain — the OIDC RP slice
		// will join through to populate this; for now leave empty since the
		// handler doesn't need it pre-OIDC-ceremony.
	}, nil
}

// minDiscoverLatency is the minimum time a Discover call appears to take.
// The discoverer's response shape is constant whether or not the domain is
// verified, but raw DB lookups are O(ms) and an attacker could time the
// difference between "domain not in table" (~1ms) vs "domain in table"
// (~5ms join). Padding to a fixed floor of 5ms collapses the timing channel.
//
// 5ms was chosen empirically: above the variance of a hot PG cache hit, well
// below human-perceptible UI latency for a single field-blur lookup.
const minDiscoverLatency = 5 * time.Millisecond

// PadUntil sleeps until `deadline`. The handler captures
// `start := time.Now(); deadline := start.Add(minDiscoverLatency)` BEFORE the
// discoverer call so the floor is enforced regardless of how long the DB
// lookup itself takes. Sleeping by `deadline - time.Since(start)` after the
// call would silently degrade to "no pad" on a slow DB hit, reintroducing
// the timing channel for the "domain in table" case (which has the slow JOIN).
func PadUntil(deadline time.Time) {
	if remaining := time.Until(deadline); remaining > 0 {
		time.Sleep(remaining)
	}
}

// emailDomain extracts the domain portion of an email, lowercased. Returns ""
// for malformed input — callers treat that as "no SSO" (HasSSO=false).
func emailDomain(email string) string {
	at := strings.LastIndex(email, "@")
	if at <= 0 || at == len(email)-1 {
		return ""
	}
	return strings.ToLower(strings.TrimSpace(email[at+1:]))
}

// Compile-time interface assertion — keep the impl honest as the seam evolves.
var _ Discoverer = (*NativeDiscoverer)(nil)
