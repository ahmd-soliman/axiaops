package auth

import (
	"context"
	"log/slog"
	"time"

	"axiaops.io/shared/model"
	"axiaops.io/shared/observability"
	"axiaops.io/shared/storage"
)

// AuditLogWriter is the narrow Store surface NewAuditWriter needs to
// emit audit_log rows. *postgres.Store satisfies it. Defined here so
// the auth package doesn't drag in the full storage.Store interface
// just to gain access to one method.
type AuditLogWriter interface {
	AuditLogWrite(ctx context.Context, e model.AuditEvent) (int64, error)
}

// auditWriteTimeout caps how long a native-auth event write can block.
// The user-visible response has already been written by the time the
// audit closure runs; a slow DB should never propagate to the caller.
const auditWriteTimeout = 2 * time.Second

// NewAuditWriter returns the AuditWriter closure cmd/main.go injects
// into Handler. It bridges the auth-package surface to the audit_log
// table without forcing this package to import services/api/internal/audit
// (that helper is request-scoped via *http.Request, which the auth
// closures don't have).
//
// Invariants matching audit.Record:
//   - Empty action → drop silently (programmer error elsewhere).
//   - Empty org/user → drop with a `slog.Warn` and a `failed` counter
//     bump. audit_log requires both fields non-empty, so a write would
//     fail at the DB layer anyway.
//   - Write failures are logged + counted, never returned (the user
//     response has already shipped; an audit gap is logged for ops to
//     catch via the failed-counter alert).
//
// The supplied store must outlive every request that uses the closure
// — pass the long-lived *postgres.Store from cmd/main.go.
func NewAuditWriter(store AuditLogWriter) AuditWriter {
	return func(ctx context.Context, organizationID, userID, action string, metadata map[string]any) {
		if action == "" {
			// Empty action reaching this layer is always a programmer
			// error in a call site (audit constants are named
			// AuditAction*; a zero-value action means a forgotten
			// argument). Log loud so it lands in alerts. No counter
			// increment — the action label would be empty and useless.
			slog.Error("audit: native-auth event has empty action — dropping",
				"org_id", organizationID, "user_id", userID)
			return
		}
		if organizationID == "" || userID == "" {
			observability.Global.AuditWritesTotal.WithLabelValues(action, "failed").Inc()
			slog.Warn("audit: native-auth event missing org/user — dropping",
				"action", action,
				"org_set", organizationID != "",
				"user_set", userID != "")
			return
		}

		// Short-deadline, fresh context — auditFn runs after the user
		// response is committed, so we deliberately decouple from the
		// (possibly cancelled) request context. The org GUC must still
		// be set for RLS on audit_log inserts.
		writeCtx, cancel := context.WithTimeout(context.Background(), auditWriteTimeout)
		defer cancel()
		writeCtx = storage.WithOrganizationID(writeCtx, organizationID)

		event := model.AuditEvent{
			OrganizationID: organizationID,
			UserID:         userID,
			Action:         action,
			Metadata:       metadata,
		}
		if _, err := store.AuditLogWrite(writeCtx, event); err != nil {
			observability.Global.AuditWritesTotal.WithLabelValues(action, "failed").Inc()
			slog.Error("audit: native-auth event write failed",
				"err", err, "action", action, "user_id", userID)
			return
		}
		observability.Global.AuditWritesTotal.WithLabelValues(action, "ok").Inc()
	}
}
