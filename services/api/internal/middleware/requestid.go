package middleware

import (
	"context"
	"net/http"

	"github.com/google/uuid"
)

type requestIDKey struct{}

// RequestID injects a unique X-Request-ID header into every request and
// stores the ID in the context for structured log lines.
func RequestID(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := r.Header.Get("X-Request-ID")
		if id == "" {
			id = uuid.New().String()
		}
		w.Header().Set("X-Request-ID", id)
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), requestIDKey{}, id)))
	})
}

// RequestIDFromCtx returns the request ID stored in ctx, or "".
func RequestIDFromCtx(ctx context.Context) string {
	v, _ := ctx.Value(requestIDKey{}).(string)
	return v
}
