package middleware

import (
	"context"

	"github.com/golang-jwt/jwt/v5"
)

// NewWithKeyfunc exposes the unexported test helper newWithKeyfunc to
// black-box (`package middleware_test`) test files. Same shape and
// contract as newWithKeyfunc — see auth.go for the comment.
//
// Function (not var) so test files can't reassign it and silently break
// parallel tests.
func NewWithKeyfunc(issuer string, kf jwt.Keyfunc) *Auth {
	return newWithKeyfunc(issuer, kf)
}

// RateLimitMax exposes the unexported package constant rateLimitMax so
// black-box rate-limit tests can iterate up to the cap without hardcoding
// the value (which would silently drift if the production constant
// changes).
const RateLimitMax = rateLimitMax

// ContextWithOrganizationID returns a context with the package's
// organization-ID key populated. Black-box tests need this to construct
// the context shape Wrap and Allow expect to read; the key itself is
// (correctly) unexported.
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
