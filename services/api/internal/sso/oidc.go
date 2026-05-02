package sso

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/golang-jwt/jwt/v5"

	"axiaops.io/shared/cache"
	"axiaops.io/shared/jwks"
	"axiaops.io/shared/model"
)

// DiscoveryDoc captures the fields of an OIDC discovery document the RP
// reads at runtime. Extra JSON keys are ignored by encoding/json. Exported
// so the initiate handler can read AuthorizationEndpoint without going
// through the validator's full ID-token surface.
type DiscoveryDoc struct {
	Issuer                string   `json:"issuer"`
	JWKSURI               string   `json:"jwks_uri"`
	AuthorizationEndpoint string   `json:"authorization_endpoint"`
	TokenEndpoint         string   `json:"token_endpoint"`
	IDTokenSigningAlgs    []string `json:"id_token_signing_alg_values_supported"`
}

// discoveryDocTTL caches the OIDC discovery doc for 24h per design §8.2.
// Refresh-on-config-change paths land in the connector's Test() flow; this
// validator just reads from cache and falls back to a live fetch on miss.
const discoveryDocTTL = 24 * time.Hour

// discoveryFetchTimeout caps a single discovery-doc HTTP fetch.
const discoveryFetchTimeout = 5 * time.Second

// allowedSigningAlgs lists the asymmetric signature algorithms the RP will
// accept regardless of what the discovery doc says. This is the second half
// of the alg-confusion mitigation (design §11.3): even if a malicious or
// misconfigured IdP advertises HS256 in id_token_signing_alg_values_supported,
// the RP refuses it because HS256 is symmetric and signing keys for it must
// be pre-shared (which we do not do — JWKS publishes asymmetric keys).
//
// A token alg is accepted only when it is BOTH listed here AND in the
// discovery doc's published set. `none` is never on this list and is
// additionally rejected by jwt.WithValidMethods.
var allowedSigningAlgs = map[string]bool{
	"RS256": true,
	"RS384": true,
	"RS512": true,
	"ES256": true,
	"ES384": true,
	"ES512": true,
	"PS256": true,
	"PS384": true,
	"PS512": true,
}

// ErrIDTokenInvalid is the validator's terminal error. Handlers must NEVER
// surface a more specific reason to the browser — error specifics are a side
// channel for an attacker probing what's wrong with their forged token. The
// wrapped error is preserved for the audit row and slog line.
var ErrIDTokenInvalid = errors.New("sso: id token validation failed")

// Validator runs OIDC ID-token validation against per-connection JWKS.
// Stateless after construction except for the caches it consults; safe for
// concurrent use across handler goroutines.
type Validator struct {
	cache  cache.Cache
	client *http.Client
	now    func() time.Time
}

// NewValidator wires a Validator. The cache is required in production —
// pass cache.New(...) which always returns a non-nil Cache.
func NewValidator(c cache.Cache) *Validator {
	return &Validator{
		cache:  c,
		client: http.DefaultClient,
		now:    time.Now,
	}
}

// SetClock overrides the validator's clock — test affordance.
func (v *Validator) SetClock(now func() time.Time) { v.now = now }

// SetHTTPClient overrides the HTTP client used for the discovery-doc fetch.
// Test affordance for httptest.
//
// Scope: this only covers the discovery-doc fetch. The JWKS fetch goes
// through services/shared/jwks/, which currently uses http.DefaultClient
// (TODO at jwks.go:100). When per-connection transport (mTLS to private
// IdPs, customer proxies) lands in jwks/, the validator will plumb v.client
// through too. Until then, do not assume an injected client reaches JWKS.
func (v *Validator) SetHTTPClient(c *http.Client) { v.client = c }

// ValidateIDToken parses and validates an ID token against the connection's
// IdP. On success, returns the claims map for the caller to extract
// sub/email/groups.
//
// expectedNonce is the nonce the RP put in the authorization request; it
// must match the token's nonce claim. Pass "" to skip the check (used by
// flows that don't carry a nonce — the OIDC callback in the next slice WILL
// pass a non-empty value).
//
// JWKS auto-refresh on signature failure (architect S5):
//   1. Validate with cached JWKS.
//   2. If parse fails on a signature error, evict the JWKS cache and retry
//      once with a fresh fetch.
//   3. If the retry still fails, surface ErrIDTokenInvalid.
//
// All non-success paths collapse to ErrIDTokenInvalid externally; the wrapped
// error is preserved for the slog line.
func (v *Validator) ValidateIDToken(ctx context.Context, conn model.SSOConnection, rawToken, expectedNonce string) (jwt.MapClaims, error) {
	if conn.OIDCDiscoveryURL == "" {
		return nil, fmt.Errorf("%w: connection has no oidc_discovery_url", ErrIDTokenInvalid)
	}
	if conn.OIDCClientID == "" {
		return nil, fmt.Errorf("%w: connection has no oidc_client_id", ErrIDTokenInvalid)
	}

	doc, err := v.discoveryDoc(ctx, conn.ID, conn.OIDCDiscoveryURL)
	if err != nil {
		return nil, fmt.Errorf("%w: discovery: %w", ErrIDTokenInvalid, err)
	}

	// First parse — cached JWKS.
	claims, err := v.parseToken(ctx, conn, doc, rawToken, false)
	if err == nil {
		return v.validateClaims(claims, conn, doc, expectedNonce)
	}
	if !isSignatureError(err) {
		return nil, fmt.Errorf("%w: %w", ErrIDTokenInvalid, err)
	}

	// Architect S5: signature failed — evict JWKS cache, fetch fresh, retry once.
	// TODO: wrap eviction + retry with golang.org/x/sync/singleflight keyed on
	// conn.ID. jwks.CacheKey godoc warns that N concurrent requests on the
	// signature-failure path all call Del + FromCache, fanning out N live
	// JWKS fetches at the IdP at the worst moment (mid-rotation under load).
	// Single-flight collapses them to one fetch. Lands with the slice 4
	// callback wiring once the auto-refresh path is exercised under load.
	if v.cache != nil {
		if delErr := v.cache.Del(ctx, jwks.CacheKey(conn.ID)); delErr != nil {
			slog.Warn("sso: oidc: jwks cache eviction failed", "connection_id", conn.ID, "err", delErr)
		}
	}
	claims, err = v.parseToken(ctx, conn, doc, rawToken, true)
	if err != nil {
		return nil, fmt.Errorf("%w: signature failed after refresh: %w", ErrIDTokenInvalid, err)
	}
	return v.validateClaims(claims, conn, doc, expectedNonce)
}

// parseToken fetches the JWKS keyfunc and parses the token under strict alg
// restrictions. When bypassCache is true, the cache is skipped entirely so
// the auto-refresh path sees fresh JWKS.
func (v *Validator) parseToken(ctx context.Context, conn model.SSOConnection, doc DiscoveryDoc, rawToken string, bypassCache bool) (jwt.MapClaims, error) {
	c := v.cache
	if bypassCache {
		c = nil // forces FromCache to do a live fetch with no cache write
	}
	keyfunc, err := jwks.FromCache(ctx, conn.ID, doc.JWKSURI, c)
	if err != nil {
		return nil, fmt.Errorf("jwks: %w", err)
	}

	algs := intersectAllowed(doc.IDTokenSigningAlgs)
	if len(algs) == 0 {
		return nil, fmt.Errorf("no acceptable signing algs (idp published: %v)", doc.IDTokenSigningAlgs)
	}

	parser := jwt.NewParser(
		jwt.WithValidMethods(algs),
		jwt.WithExpirationRequired(),
	)
	token, err := parser.ParseWithClaims(rawToken, jwt.MapClaims{}, keyfunc)
	if err != nil {
		return nil, err
	}
	if !token.Valid {
		return nil, errors.New("token marked invalid")
	}
	claims, ok := token.Claims.(jwt.MapClaims)
	if !ok {
		return nil, errors.New("claims shape unexpected")
	}
	return claims, nil
}

// validateClaims runs OIDC-specific claim checks: issuer match, audience
// match, nonce match, iat sanity. exp is already enforced by jwt.Parser
// when WithExpirationRequired is set.
func (v *Validator) validateClaims(claims jwt.MapClaims, conn model.SSOConnection, doc DiscoveryDoc, expectedNonce string) (jwt.MapClaims, error) {
	// Issuer must match the discovery doc's claimed issuer (defense against
	// a malicious discovery doc pointing at a JWKS the attacker controls but
	// declaring a different issuer in the token).
	iss, _ := claims["iss"].(string)
	if iss == "" || iss != doc.Issuer {
		return nil, fmt.Errorf("%w: issuer mismatch", ErrIDTokenInvalid)
	}
	// Entra signs across tenants with the same key (design §8.4 / §9). When
	// the connection is an Entra tenant, the issuer string must contain the
	// configured tenant ID — otherwise a different Entra tenant could mint
	// tokens that pass signature checks. tenant IDs are GUIDs; substring
	// match is sufficient (no path-segment collision risk).
	if conn.OIDCTenantID != "" && !strings.Contains(iss, conn.OIDCTenantID) {
		return nil, fmt.Errorf("%w: tenant mismatch", ErrIDTokenInvalid)
	}

	if !audienceMatches(claims["aud"], conn.OIDCClientID) {
		return nil, fmt.Errorf("%w: audience mismatch", ErrIDTokenInvalid)
	}

	// nonce binds the token to the auth request the RP originated. Skipping
	// is a footgun: a caller that forgets to pass the nonce gets no
	// validation silently. The empty-string skip is reserved for tests; emit
	// a slog.Warn so a production caller-bug shows up in staging logs.
	if expectedNonce == "" {
		slog.Warn("sso: oidc: validating ID token without nonce check", "connection_id", conn.ID)
	} else {
		gotNonce, _ := claims["nonce"].(string)
		if gotNonce != expectedNonce {
			return nil, fmt.Errorf("%w: nonce mismatch", ErrIDTokenInvalid)
		}
	}

	// iat must not be in the future beyond a small skew. 2min matches the
	// §7.4 SAML default and is the OIDC industry common.
	if iat, ok := claims["iat"].(float64); ok {
		const maxSkew = 2 * time.Minute
		if time.Unix(int64(iat), 0).After(v.now().Add(maxSkew)) {
			return nil, fmt.Errorf("%w: iat in future", ErrIDTokenInvalid)
		}
	}

	return claims, nil
}

// Discovery returns the cached-or-live OIDC discovery doc for the
// connection. The initiate handler calls this to resolve
// AuthorizationEndpoint before redirecting to the IdP. The first call
// populates the cache that ValidateIDToken consults later in the same
// ceremony, so the callback's token validation hits a warm cache.
func (v *Validator) Discovery(ctx context.Context, conn model.SSOConnection) (DiscoveryDoc, error) {
	if conn.OIDCDiscoveryURL == "" {
		return DiscoveryDoc{}, errors.New("sso: connection has no oidc_discovery_url")
	}
	return v.discoveryDoc(ctx, conn.ID, conn.OIDCDiscoveryURL)
}

// discoveryDoc fetches the discovery doc through the cache layer at
// sso:oidc-discovery:{cid} with 24h TTL.
func (v *Validator) discoveryDoc(ctx context.Context, cid, url string) (DiscoveryDoc, error) {
	cacheKey := "sso:oidc-discovery:" + cid

	if v.cache != nil {
		body, err := v.cache.Get(ctx, cacheKey)
		if err == nil {
			var doc DiscoveryDoc
			if jsonErr := json.Unmarshal(body, &doc); jsonErr == nil {
				return doc, nil
			}
			slog.Warn("sso: oidc: cached discovery doc invalid, re-fetching", "cid", cid)
		} else if !errors.Is(err, cache.ErrNotFound) {
			slog.Warn("sso: oidc: discovery cache error, falling back to live fetch", "cid", cid, "err", err)
		}
	}

	fetchCtx, cancel := context.WithTimeout(ctx, discoveryFetchTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(fetchCtx, http.MethodGet, url, nil)
	if err != nil {
		return DiscoveryDoc{}, fmt.Errorf("build request: %w", err)
	}
	resp, err := v.client.Do(req)
	if err != nil {
		return DiscoveryDoc{}, fmt.Errorf("fetch %s: %w", url, err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return DiscoveryDoc{}, fmt.Errorf("fetch %s: unexpected status %d", url, resp.StatusCode)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return DiscoveryDoc{}, fmt.Errorf("read body: %w", err)
	}
	var doc DiscoveryDoc
	if err := json.Unmarshal(body, &doc); err != nil {
		return DiscoveryDoc{}, fmt.Errorf("parse: %w", err)
	}
	if v.cache != nil {
		if setErr := v.cache.Set(ctx, cacheKey, body, discoveryDocTTL); setErr != nil {
			slog.Warn("sso: oidc: failed to cache discovery doc", "cid", cid, "err", setErr)
		}
	}
	return doc, nil
}

// intersectAllowed returns the algs the IdP advertises that are also in our
// asymmetric whitelist. Empty when the IdP advertises only rejected algs.
func intersectAllowed(idpAlgs []string) []string {
	out := make([]string, 0, len(idpAlgs))
	for _, a := range idpAlgs {
		if allowedSigningAlgs[a] {
			out = append(out, a)
		}
	}
	return out
}

// audienceMatches accepts either a string or []any aud claim. RFC 7519 §4.1.3
// allows aud to carry multiple values; OIDC ID tokens typically have one.
func audienceMatches(claim any, want string) bool {
	if want == "" {
		return false
	}
	switch a := claim.(type) {
	case string:
		return a == want
	case []any:
		for _, v := range a {
			if s, ok := v.(string); ok && s == want {
				return true
			}
		}
	}
	return false
}

// isSignatureError reports whether err is the signature-verification failure
// the auto-refresh path should retry. We don't retry on malformed-token,
// expired, claims-invalid, etc. — those won't change after a JWKS rotation.
func isSignatureError(err error) bool {
	return errors.Is(err, jwt.ErrTokenSignatureInvalid) ||
		errors.Is(err, jwt.ErrTokenUnverifiable)
}
