package model

import (
	"net"
	"time"
)

// AuthMode describes how the session was established. Persisted as a string
// in the sessions.auth_mode column (CHECK constrained).
type AuthMode string

const (
	// AuthModePassword — native email/password login (POST /v1/auth/login).
	AuthModePassword AuthMode = "password"
	// AuthModeSSO — minted by an SSO callback (B2 / Phase C).
	AuthModeSSO AuthMode = "sso"
	// AuthModeBootstrap — minted by the first-owner bootstrap flow (D5).
	AuthModeBootstrap AuthMode = "bootstrap"
)

// Session is the server-side record corresponding to one issued auth cookie.
// The plaintext token is never stored — only its SHA-256 hash. The cookie
// carries the plaintext; the request middleware hashes it and looks up by
// SessionTokenHash.
//
// RLS is intentionally NOT enabled on the underlying table — see migration
// 021_native_auth.up.sql for the rationale (lookup precedes any org context).
type Session struct {
	ID               string
	UserID           string
	OrganizationID   string
	AuthMode         AuthMode
	SessionTokenHash string // hex(SHA-256(plaintext token)); never log the plaintext
	CreatedAt        time.Time
	ExpiresAt        time.Time
	RevokedAt        *time.Time // nil while active; non-nil once revoked
	LastSeenAt       time.Time
	IP               net.IP // nil when unknown / DEV_MODE
	UserAgentHash    string // SHA-256 of the User-Agent header; empty when absent
	// IDTokenEncrypted is the AES-256-GCM-encrypted (hex-encoded) OIDC id_token
	// captured at SSO callback time, used as id_token_hint for RP-Initiated
	// Logout (migration 027). Empty for AuthModePassword / AuthModeBootstrap
	// sessions — they have no IdP session to invalidate.
	IDTokenEncrypted string
}

// Live returns true if the session is presently usable: not revoked, not
// expired. The cache read path MUST call Live() after deserialising — the
// presence of a row in the cache is not by itself proof of liveness
// (architect C4).
func (s Session) Live(now time.Time) bool {
	if s.RevokedAt != nil {
		return false
	}
	return now.Before(s.ExpiresAt)
}
