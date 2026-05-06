package middleware

import "context"

// RateLimitMax exposes the unexported package constant rateLimitMax so
// black-box rate-limit tests can iterate up to the cap without hardcoding
// the value (which would silently drift if the production constant
// changes).
const RateLimitMax = rateLimitMax

// ContextWithOrganizationID returns a context with the package's
// organization-ID key populated. Black-box tests need this to construct
// the context shape Allow expects to read; the key itself is (correctly)
// unexported.
func ContextWithOrganizationID(ctx context.Context, organizationID string) context.Context {
	return context.WithValue(ctx, organizationIDKey, organizationID)
}

// ContextWithAuthMode returns a context with the package's auth-mode key
// populated. Used by sso_enforcement_test to drive the EnforceSSO branches
// without standing up a full WrapNative chain. Same rationale as
// ContextWithOrganizationID — the key is unexported on purpose.
func ContextWithAuthMode(ctx context.Context, authMode string) context.Context {
	return context.WithValue(ctx, authModeKey, authMode)
}
