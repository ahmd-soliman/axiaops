// Package observability provides Prometheus metrics, Sentry error tracking,
// and related observability utilities for AxiaOps services.
package observability

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

// Metrics holds all Prometheus metrics for AxiaOps services.
// These are pre-registered with the default Prometheus registry in init().
type Metrics struct {
	// HTTP API metrics
	HTTPRequestsTotal       prometheus.Counter       // Total HTTP requests received
	HTTPRequestsDuration    prometheus.Histogram     // HTTP request latency (seconds)
	HTTPRequestsInFlight    prometheus.Gauge         // In-flight HTTP requests
	HTTPResponsesTotal      *prometheus.CounterVec   // Total responses by method, route, status
	HTTPErrorsTotal         *prometheus.CounterVec   // Total HTTP errors by method, route, status

	// Database metrics
	DBQueryDuration         *prometheus.HistogramVec // Query latency by operation
	DBQueryErrors           *prometheus.CounterVec   // Query errors by operation
	DBConnectionsActive     prometheus.Gauge         // Active database connections
	DBTransactionDuration   *prometheus.HistogramVec // Transaction latency

	// AWS/Ingestion metrics
	AWSAPICallDuration      *prometheus.HistogramVec // AWS API call latency by service
	AWSAPICallErrors        *prometheus.CounterVec   // AWS API errors by service
	CostRecordsFetched      *prometheus.CounterVec   // Cost records fetched by provider
	ResourcesAnalyzed       prometheus.Counter       // Total resources analyzed
	GhostsDetected          *prometheus.GaugeVec     // Ghost resources detected by provider
	PotentialMonthlySaving  *prometheus.GaugeVec     // Potential monthly savings USD by provider

	// Scan lifecycle metrics
	ScanDuration            *prometheus.HistogramVec // Scan operation duration by stage
	ScanErrors              *prometheus.CounterVec   // Scan errors by account_id, error_type
	ScanQueueDepth          prometheus.Gauge         // Current scan queue depth
	AccountsScanning        prometheus.Gauge         // Accounts currently being scanned

	// Cache metrics
	CacheOperationsTotal    *prometheus.CounterVec   // Cache ops by op, backend, status
	CacheOperationDuration  *prometheus.HistogramVec // Cache op latency by op, backend

	// Application/process metrics
	ApplicationUptime       prometheus.Gauge         // Seconds since service startup
	ApplicationErrors       prometheus.Counter       // Total application errors
}

// registry is the global Prometheus registry.
var registry = prometheus.NewRegistry()

// Global holds the singleton Metrics instance.
var Global = newMetrics()

// newMetrics creates and registers all metrics with Prometheus.
func newMetrics() *Metrics {
	factory := promauto.With(registry)

	m := &Metrics{
		// HTTP metrics
		HTTPRequestsTotal: factory.NewCounter(prometheus.CounterOpts{
			Name: "axiaops_http_requests_total",
			Help: "Total HTTP requests received.",
		}),
		HTTPRequestsDuration: factory.NewHistogram(prometheus.HistogramOpts{
			Name:    "axiaops_http_request_duration_seconds",
			Help:    "HTTP request latency in seconds.",
			Buckets: prometheus.DefBuckets,
		}),
		HTTPRequestsInFlight: factory.NewGauge(prometheus.GaugeOpts{
			Name: "axiaops_http_requests_in_flight",
			Help: "Current number of in-flight HTTP requests.",
		}),
		HTTPResponsesTotal: factory.NewCounterVec(prometheus.CounterOpts{
			Name: "axiaops_http_responses_total",
			Help: "Total HTTP responses by method, route, and status.",
		}, []string{"method", "route", "status"}),
		HTTPErrorsTotal: factory.NewCounterVec(prometheus.CounterOpts{
			Name: "axiaops_http_errors_total",
			Help: "Total HTTP errors by method, route, and status.",
		}, []string{"method", "route", "status"}),

		// Database metrics
		DBQueryDuration: factory.NewHistogramVec(prometheus.HistogramOpts{
			Name:    "axiaops_db_query_duration_seconds",
			Help:    "Database query latency in seconds by operation.",
			Buckets: prometheus.DefBuckets,
		}, []string{"operation"}),
		DBQueryErrors: factory.NewCounterVec(prometheus.CounterOpts{
			Name: "axiaops_db_query_errors_total",
			Help: "Total database query errors by operation.",
		}, []string{"operation"}),
		DBConnectionsActive: factory.NewGauge(prometheus.GaugeOpts{
			Name: "axiaops_db_connections_active",
			Help: "Number of active database connections.",
		}),
		DBTransactionDuration: factory.NewHistogramVec(prometheus.HistogramOpts{
			Name:    "axiaops_db_transaction_duration_seconds",
			Help:    "Database transaction latency in seconds by type.",
			Buckets: prometheus.DefBuckets,
		}, []string{"type"}),

		// AWS/Ingestion metrics
		AWSAPICallDuration: factory.NewHistogramVec(prometheus.HistogramOpts{
			Name:    "axiaops_aws_api_call_duration_seconds",
			Help:    "AWS API call latency in seconds by service.",
			Buckets: prometheus.DefBuckets,
		}, []string{"service"}),
		AWSAPICallErrors: factory.NewCounterVec(prometheus.CounterOpts{
			Name: "axiaops_aws_api_errors_total",
			Help: "Total AWS API errors by service.",
		}, []string{"service"}),
		CostRecordsFetched: factory.NewCounterVec(prometheus.CounterOpts{
			Name: "axiaops_cost_records_fetched_total",
			Help: "Total cost records fetched by provider.",
		}, []string{"provider", "tenant_id"}),
		ResourcesAnalyzed: factory.NewCounter(prometheus.CounterOpts{
			Name: "axiaops_resources_analyzed_total",
			Help: "Total resources analyzed.",
		}),
		GhostsDetected: factory.NewGaugeVec(prometheus.GaugeOpts{
			Name: "axiaops_ghosts_detected",
			Help: "Number of ghost resources detected by provider.",
		}, []string{"provider", "tenant_id"}),
		PotentialMonthlySaving: factory.NewGaugeVec(prometheus.GaugeOpts{
			Name: "axiaops_potential_monthly_savings_usd",
			Help: "Potential monthly savings in USD by provider.",
		}, []string{"provider", "tenant_id"}),

		// Scan lifecycle metrics
		ScanDuration: factory.NewHistogramVec(prometheus.HistogramOpts{
			Name:    "axiaops_scan_duration_seconds",
			Help:    "Scan operation duration in seconds by stage (fetch, analyze, save).",
			Buckets: prometheus.DefBuckets,
		}, []string{"stage"}),
		ScanErrors: factory.NewCounterVec(prometheus.CounterOpts{
			Name: "axiaops_scan_errors_total",
			Help: "Total scan errors by account_id and error_type.",
		}, []string{"account_id", "error_type"}),
		ScanQueueDepth: factory.NewGauge(prometheus.GaugeOpts{
			Name: "axiaops_scan_queue_depth",
			Help: "Current depth of the scan request queue.",
		}),
		AccountsScanning: factory.NewGauge(prometheus.GaugeOpts{
			Name: "axiaops_accounts_scanning",
			Help: "Number of accounts currently being scanned.",
		}),

		// Cache metrics
		CacheOperationsTotal: factory.NewCounterVec(prometheus.CounterOpts{
			Name: "axiaops_cache_operations_total",
			Help: "Total cache operations by op, backend, and status.",
		}, []string{"op", "backend", "status"}),
		CacheOperationDuration: factory.NewHistogramVec(prometheus.HistogramOpts{
			Name:    "axiaops_cache_operation_duration_seconds",
			Help:    "Cache operation latency in seconds by op and backend.",
			Buckets: prometheus.DefBuckets,
		}, []string{"op", "backend"}),

		// Application metrics
		ApplicationUptime: factory.NewGauge(prometheus.GaugeOpts{
			Name: "axiaops_application_uptime_seconds",
			Help: "Application uptime in seconds.",
		}),
		ApplicationErrors: factory.NewCounter(prometheus.CounterOpts{
			Name: "axiaops_application_errors_total",
			Help: "Total application errors.",
		}),
	}

	return m
}

// Registry returns the Prometheus registry.
func Registry() prometheus.Gatherer {
	return registry
}
