package middleware

import "context"

// NewWithKeyfunc exposes the unexported test helper newWithKeyfunc to
// black-box (`package middleware_test`) test files. Same shape and
// contract as newWithKeyfunc — see auth.go for the comment.
var NewWithKeyfunc = newWithKeyfunc

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
