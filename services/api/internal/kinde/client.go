// Package kinde wraps the Kinde Management API. The auth middleware verifies
// JWTs using a separate JWKS path; this package handles only outbound calls
// (invite user, remove user, rename organization).
//
// See docs/invitation-flow.md §5 and docs/onboarding-wizard.md §6 for the
// design and required scopes.
package kinde

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

// Client is the abstraction handlers depend on. The concrete implementation
// is *HTTPClient (talks to Kinde) and *Stub (in-memory, used by DEV_MODE and
// tests).
type Client interface {
	// InviteUser sends an organization-scoped invitation email via Kinde and
	// returns Kinde's invitation_id and the user_id Kinde minted for the
	// invitee. Both IDs are persisted by the handler so RemoveUser can revoke
	// the user from the org later.
	InviteUser(ctx context.Context, orgCode, email, fullName string) (kindeInvitationID, kindeUserID string, err error)

	// RemoveUser removes a user from a Kinde organization, invalidating any
	// outstanding invitation email link. Returns nil on 2xx and on 404
	// (idempotent — Kinde already forgot about the user).
	RemoveUser(ctx context.Context, orgCode, kindeUserID string) error

	// RenameOrganization updates the organization's display name in Kinde.
	// Used by PATCH /v1/organizations/me to keep Kinde-hosted surfaces
	// (invitation emails, switcher UI) aligned with the local rename. Local
	// transaction is rolled back on non-2xx.
	RenameOrganization(ctx context.Context, orgCode, name string) error
}

// HTTPClient is the production implementation. It manages an M2M
// client-credentials token cache and issues authenticated HTTP requests.
type HTTPClient struct {
	mgmtAPIURL   string
	tokenURL     string
	clientID     string
	clientSecret string
	http         *http.Client

	mu     sync.Mutex
	token  string
	expiry time.Time
}

// New constructs a production HTTP client.
//
// issuer is Kinde's OAuth issuer (e.g. https://example.kinde.com) — used as
// both the token endpoint base and, when mgmtAPIURL is empty, the management
// API base. clientID/clientSecret are the M2M app credentials.
//
// Returns an error if any required field is empty.
func New(issuer, mgmtAPIURL, clientID, clientSecret string) (*HTTPClient, error) {
	issuer = strings.TrimRight(issuer, "/")
	if issuer == "" {
		return nil, fmt.Errorf("kinde: issuer is required")
	}
	if clientID == "" || clientSecret == "" {
		return nil, fmt.Errorf("kinde: M2M client_id and client_secret are required")
	}
	if mgmtAPIURL == "" {
		mgmtAPIURL = issuer
	}
	mgmtAPIURL = strings.TrimRight(mgmtAPIURL, "/")
	return &HTTPClient{
		mgmtAPIURL:   mgmtAPIURL,
		tokenURL:     issuer + "/oauth2/token",
		clientID:     clientID,
		clientSecret: clientSecret,
		http:         &http.Client{Timeout: 10 * time.Second},
	}, nil
}

// kindeError is returned to callers when Kinde rejects a request. It carries
// the HTTP status so handlers can map 4xx → upstream client error and 5xx → 502.
type kindeError struct {
	StatusCode int
	Body       string
	Op         string
}

func (e *kindeError) Error() string {
	return fmt.Sprintf("kinde: %s: %d %s", e.Op, e.StatusCode, e.Body)
}

// IsClientError reports whether an error is a Kinde 4xx response.
func IsClientError(err error) bool {
	var ke *kindeError
	if !errorsAs(err, &ke) {
		return false
	}
	return ke.StatusCode >= 400 && ke.StatusCode < 500
}

// IsServerError reports whether an error is a Kinde 5xx or transport failure.
func IsServerError(err error) bool {
	var ke *kindeError
	if !errorsAs(err, &ke) {
		// Transport errors aren't Kinde-typed; treat as server-side.
		return err != nil
	}
	return ke.StatusCode >= 500
}

// IsNotFound reports whether an error is a Kinde 404 — used to make
// RemoveUser idempotent.
func IsNotFound(err error) bool {
	var ke *kindeError
	if !errorsAs(err, &ke) {
		return false
	}
	return ke.StatusCode == http.StatusNotFound
}

// errorsAs is a tiny wrapper to avoid pulling errors into every call site.
func errorsAs(err error, target **kindeError) bool {
	for err != nil {
		if ke, ok := err.(*kindeError); ok {
			*target = ke
			return true
		}
		// Unwrap is in errors stdlib; we use a manual check to avoid an import
		// cycle when kindeError grows wrapping later.
		type unwrapper interface{ Unwrap() error }
		u, ok := err.(unwrapper)
		if !ok {
			return false
		}
		err = u.Unwrap()
	}
	return false
}

// ── M2M token cache ──────────────────────────────────────────────────────────

type tokenResponse struct {
	AccessToken string `json:"access_token"`
	ExpiresIn   int    `json:"expires_in"`
	TokenType   string `json:"token_type"`
}

// authToken returns a valid bearer token, refreshing at 80% of TTL.
func (c *HTTPClient) authToken(ctx context.Context) (string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.token != "" && time.Until(c.expiry) > 0 {
		return c.token, nil
	}
	// url.Values escapes special characters — important for client secrets,
	// which are commonly machine-generated and may contain `+`, `&`, `=`.
	form := url.Values{}
	form.Set("grant_type", "client_credentials")
	form.Set("client_id", c.clientID)
	form.Set("client_secret", c.clientSecret)
	form.Set("audience", c.mgmtAPIURL+"/api")
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.tokenURL, strings.NewReader(form.Encode()))
	if err != nil {
		return "", fmt.Errorf("kinde: token request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := c.http.Do(req)
	if err != nil {
		return "", fmt.Errorf("kinde: token http: %w", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", &kindeError{StatusCode: resp.StatusCode, Body: string(body), Op: "token"}
	}
	var tr tokenResponse
	if err := json.Unmarshal(body, &tr); err != nil {
		return "", fmt.Errorf("kinde: token decode: %w", err)
	}
	if tr.AccessToken == "" {
		return "", fmt.Errorf("kinde: empty access_token")
	}
	c.token = tr.AccessToken
	// Refresh at 80% of TTL — falls back to a 5-minute floor when expires_in
	// is unset or absurdly small.
	ttl := time.Duration(tr.ExpiresIn) * time.Second
	if ttl < 5*time.Minute {
		ttl = 5 * time.Minute
	}
	c.expiry = time.Now().Add(ttl * 80 / 100)
	return c.token, nil
}

// do is the shared request path: fetch token → set headers → execute → check status.
func (c *HTTPClient) do(ctx context.Context, op, method, url string, body []byte, out any) error {
	tok, err := c.authToken(ctx)
	if err != nil {
		return err
	}
	var reqBody io.Reader
	if body != nil {
		reqBody = strings.NewReader(string(body))
	}
	req, err := http.NewRequestWithContext(ctx, method, url, reqBody)
	if err != nil {
		return fmt.Errorf("kinde: %s request: %w", op, err)
	}
	req.Header.Set("Authorization", "Bearer "+tok)
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("kinde: %s http: %w", op, err)
	}
	defer resp.Body.Close()
	respBody, _ := io.ReadAll(resp.Body)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return &kindeError{StatusCode: resp.StatusCode, Body: string(respBody), Op: op}
	}
	if out != nil && len(respBody) > 0 {
		if err := json.Unmarshal(respBody, out); err != nil {
			return fmt.Errorf("kinde: %s decode: %w", op, err)
		}
	}
	return nil
}
