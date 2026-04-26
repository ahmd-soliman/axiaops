// Package audit writes user-initiated mutations to audit_log. One Record call
// per mutating handler — see docs/audit_trail_plan.md.
//
// Best-effort: if the write fails the user operation still succeeds; the
// axiaops_audit_writes_total{status="failed"} counter is bumped so ops can
// alert on sustained failures.
package audit

import (
	"context"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"time"

	"axiaops.io/api/internal/middleware"
	"axiaops.io/shared/model"
	"axiaops.io/shared/observability"
	"axiaops.io/shared/storage"
)

// writeTimeout caps how long an audit write can block the caller. Audit writes
// happen after the user-visible work is done; a slow DB should not extend the
// request tail latency. 2 seconds is generous — the INSERT should take < 5ms.
const writeTimeout = 2 * time.Second

// Writer is the subset of storage.Store that this package needs.
// Accepting an interface keeps handler tests from needing a full Store mock.
type Writer interface {
	AuditLogWrite(ctx context.Context, e model.AuditEvent) (int64, error)
}

// Record emits one audit event for the request. The event is enriched from
// the request (organization_id, user_id, actor_email, request_id, ip, user_agent)
// before being handed to the store. Only Action is strictly required on the
// caller-supplied event; the other fields are action-specific.
//
// Never blocks the caller on a failed write — errors are logged and counted.
func Record(r *http.Request, w Writer, e model.AuditEvent) {
	if r == nil || w == nil {
		return
	}
	if e.Action == "" {
		slog.Error("audit: Record called with empty action — dropping event")
		return
	}

	ctx := r.Context()
	organizationID := middleware.OrganizationID(ctx)
	if organizationID == "" {
		// No organization means RLS will reject the insert; don't even try.
		observability.Global.AuditWritesTotal.WithLabelValues(e.Action, "failed").Inc()
		slog.Error("audit: organization_id missing from context — dropping event", "action", e.Action)
		return
	}

	// Enrich — caller-supplied fields win over request-derived ones, so a
	// handler can override the actor (rare, but e.g. on-behalf-of flows).
	if e.UserID == "" {
		e.UserID = middleware.UserID(ctx)
	}
	if e.ActorEmail == "" {
		e.ActorEmail = middleware.UserEmail(ctx)
	}
	if e.RequestID == "" {
		e.RequestID = middleware.RequestIDFromCtx(ctx)
	}
	if len(e.IPAddress) == 0 {
		e.IPAddress = clientIP(r)
	}
	if e.UserAgent == "" {
		e.UserAgent = r.UserAgent()
	}

	// Use a fresh context with a short deadline — if the client disconnects
	// mid-handler we still want the audit row to land, and we don't want a
	// slow DB to tie up resources either.
	writeCtx, cancel := context.WithTimeout(context.Background(), writeTimeout)
	defer cancel()
	writeCtx = storage.WithOrganizationID(writeCtx, organizationID)

	if _, err := w.AuditLogWrite(writeCtx, e); err != nil {
		observability.Global.AuditWritesTotal.WithLabelValues(e.Action, "failed").Inc()
		slog.Error("audit: write failed",
			"action", e.Action,
			"resource_type", e.ResourceType,
			"resource_id", e.ResourceID,
			"organization_id", organizationID,
			"error", err,
		)
		return
	}
	observability.Global.AuditWritesTotal.WithLabelValues(e.Action, "ok").Inc()
}

// clientIP returns the best-effort client address. Honours X-Forwarded-For
// (nginx/App Runner set this) and falls back to RemoteAddr. Returns nil if
// neither yields a parseable IP so the audit row stores NULL rather than a
// bogus value.
//
// XFF is trusted as-is — in production the service sits behind nginx/App Runner
// which overwrites the header, so only the leftmost (true client) entry is
// user-supplied. In `start-dev` there is no proxy and XFF is forgeable, so IPs
// in dev/staging audit rows are advisory only and must not be used for incident
// attribution without cross-referencing the ingress logs.
func clientIP(r *http.Request) net.IP {
	if fwd := r.Header.Get("X-Forwarded-For"); fwd != "" {
		// XFF is a comma-separated list; the leftmost entry is the original client.
		if first, _, ok := strings.Cut(fwd, ","); ok {
			if ip := net.ParseIP(strings.TrimSpace(first)); ip != nil {
				return ip
			}
		}
		if ip := net.ParseIP(strings.TrimSpace(fwd)); ip != nil {
			return ip
		}
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	return net.ParseIP(host)
}
