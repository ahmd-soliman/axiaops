package observability

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

// hmacFailuresTotal counts ingestion HMAC verification failures, labelled
// by reason. Cardinality is bounded to four series (missing_header,
// malformed, timestamp_skew, signature_mismatch) so the metric stays cheap.
//
// Registered into the package-private registry so it surfaces through
// MetricsHandler() — the same path Global.* takes. Used by the
// httpauth.Middleware on every rejection.
//
// Single unknown-reason bucket is reserved for the impossible-path case
// (Verify returning an error the label-switch doesn't recognise) so a
// future sentinel addition shows up rather than panicking.
//
// Mirrors the shape of axiaops_session_revocations_total.
var hmacFailuresTotal = promauto.With(registry).NewCounterVec(
	prometheus.CounterOpts{
		Name: "axiaops_ingestion_hmac_failures_total",
		Help: "Total ingestion HMAC verification failures, labelled by reason. Alert on > 1/min for > 5min in any hard-enforce env.",
	},
	[]string{"reason"},
)

// hmacEnvelopeRejectionsTotal counts Redis-queue envelope-signature failures
// observed by the ingestion worker. Single label `reason` mirrors the HTTP
// path so dashboards can reuse the same query shape across both surfaces.
var hmacEnvelopeRejectionsTotal = promauto.With(registry).NewCounterVec(
	prometheus.CounterOpts{
		Name: "axiaops_ingestion_envelope_rejections_total",
		Help: "Total Redis-queue envelope verification failures, labelled by reason. Same alert posture as axiaops_ingestion_hmac_failures_total.",
	},
	[]string{"reason"},
)

// hmacEnforceMode is set once at ingestion boot to surface the current
// enforcement posture. 1 for the active mode, 0 for the others. Prometheus
// alert: mode="soft" outside dev for > 24h → soft-enforce stuck on, C-1
// silently re-opens.
var hmacEnforceMode = promauto.With(registry).NewGaugeVec(
	prometheus.GaugeOpts{
		Name: "axiaops_ingestion_hmac_enforce_mode",
		Help: "Ingestion HMAC enforcement posture. Exactly one label set to 1. mode='soft' outside dev for > 24h should page (C-1 silently re-opens).",
	},
	[]string{"mode"},
)

// RecordHMACFailure increments the HTTP-path failure counter under the
// supplied reason label.
func RecordHMACFailure(reason string) {
	hmacFailuresTotal.WithLabelValues(reason).Inc()
}

// RecordEnvelopeRejection increments the Redis-path envelope-failure
// counter under the supplied reason label.
func RecordEnvelopeRejection(reason string) {
	hmacEnvelopeRejectionsTotal.WithLabelValues(reason).Inc()
}

// SetHMACEnforceMode sets the enforcement-mode gauge — exactly one label
// (soft or hard) is set to 1, the other to 0. Callers wire this once at
// ingestion boot.
func SetHMACEnforceMode(softEnforce bool) {
	if softEnforce {
		hmacEnforceMode.WithLabelValues("soft").Set(1)
		hmacEnforceMode.WithLabelValues("hard").Set(0)
		return
	}
	hmacEnforceMode.WithLabelValues("hard").Set(1)
	hmacEnforceMode.WithLabelValues("soft").Set(0)
}
