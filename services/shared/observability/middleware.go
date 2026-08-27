package observability

import (
	"context"
	"net/http"
	"strconv"
	"time"
)

// HTTPObserverWriter wraps http.ResponseWriter to capture status codes.
type HTTPObserverWriter struct {
	http.ResponseWriter
	statusCode int
	bytesWrit  int
}

// WriteHeader captures the HTTP status code.
func (w *HTTPObserverWriter) WriteHeader(code int) {
	w.statusCode = code
	w.ResponseWriter.WriteHeader(code)
}

// Write captures bytes written to the response.
func (w *HTTPObserverWriter) Write(p []byte) (int, error) {
	n, err := w.ResponseWriter.Write(p)
	w.bytesWrit += n
	return n, err
}

// StatusCode returns the captured HTTP status code.
func (w *HTTPObserverWriter) StatusCode() int {
	if w.statusCode == 0 {
		return http.StatusOK
	}
	return w.statusCode
}

// BytesWritten returns the number of bytes written to the response.
func (w *HTTPObserverWriter) BytesWritten() int {
	return w.bytesWrit
}

// HTTPMiddleware creates HTTP observability middleware.
// It records request duration, status, and errors to Prometheus.
// This should be placed early in the middleware chain (after RequestID but before auth/CORS).
func HTTPMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		observer := &HTTPObserverWriter{ResponseWriter: w, statusCode: http.StatusOK}

		// Increment in-flight requests
		Global.HTTPRequestsInFlight.Inc()
		defer Global.HTTPRequestsInFlight.Dec()

		// Increment total requests
		Global.HTTPRequestsTotal.Inc()

		// Serve the request
		next.ServeHTTP(observer, r)

		// Observe metrics
		duration := time.Since(start).Seconds()
		statusStr := strconv.Itoa(observer.statusCode)

		// Record response time and status (use r.Pattern if available to avoid label cardinality explosion)
		route := r.Pattern
		if route == "" {
			route = r.URL.Path
		}
		Global.HTTPRequestsDuration.Observe(duration)
		Global.HTTPResponsesTotal.WithLabelValues(r.Method, route, statusStr).Inc()

		// Count errors (5xx)
		if observer.statusCode >= 500 {
			Global.HTTPErrorsTotal.WithLabelValues(r.Method, route, statusStr).Inc()
		}
	})
}

// DatabaseObserver records database query metrics.
type DatabaseObserver struct {
	operation string
	start     time.Time
}

// NewDatabaseObserver creates a new database metrics observer.
func NewDatabaseObserver(operation string) *DatabaseObserver {
	return &DatabaseObserver{
		operation: operation,
		start:     time.Now(),
	}
}

// Observe records the query latency.
func (o *DatabaseObserver) Observe() {
	duration := time.Since(o.start).Seconds()
	Global.DBQueryDuration.WithLabelValues(o.operation).Observe(duration)
}

// ObserveError records a query error.
func (o *DatabaseObserver) ObserveError() {
	o.Observe()
	Global.DBQueryErrors.WithLabelValues(o.operation).Inc()
}

// TransactionObserver records database transaction metrics.
type TransactionObserver struct {
	txType string
	start  time.Time
}

// NewTransactionObserver creates a new transaction observer.
func NewTransactionObserver(txType string) *TransactionObserver {
	return &TransactionObserver{
		txType: txType,
		start:  time.Now(),
	}
}

// Observe records the transaction duration.
func (o *TransactionObserver) Observe() {
	duration := time.Since(o.start).Seconds()
	Global.DBTransactionDuration.WithLabelValues(o.txType).Observe(duration)
}

// ScanObserver records scan operation metrics.
type ScanObserver struct {
	stage string
	start time.Time
}

// NewScanObserver creates a new scan observer.
func NewScanObserver(stage string) *ScanObserver {
	return &ScanObserver{
		stage: stage,
		start: time.Now(),
	}
}

// Observe records the scan stage duration.
func (o *ScanObserver) Observe() {
	duration := time.Since(o.start).Seconds()
	Global.ScanDuration.WithLabelValues(o.stage).Observe(duration)
}

// AWSObserver records AWS API call metrics.
type AWSObserver struct {
	service string
	start   time.Time
}

// NewAWSObserver creates a new AWS API observer.
func NewAWSObserver(service string) *AWSObserver {
	return &AWSObserver{
		service: service,
		start:   time.Now(),
	}
}

// Observe records the AWS API call duration.
func (o *AWSObserver) Observe() {
	duration := time.Since(o.start).Seconds()
	Global.AWSAPICallDuration.WithLabelValues(o.service).Observe(duration)
}

// ObserveError records an AWS API error.
func (o *AWSObserver) ObserveError() {
	o.Observe()
	Global.AWSAPICallErrors.WithLabelValues(o.service).Inc()
}

// RecordScanStart increments the accounts being scanned counter.
func RecordScanStart(ctx context.Context) {
	Global.AccountsScanning.Inc()
}

// RecordScanEnd decrements the accounts being scanned counter.
func RecordScanEnd(ctx context.Context) {
	Global.AccountsScanning.Dec()
}

// RecordScanError records a scan error.
func RecordScanError(accountID string, errorType string) {
	Global.ScanErrors.WithLabelValues(accountID, errorType).Inc()
}
