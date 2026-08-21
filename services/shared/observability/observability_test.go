package observability_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"

	"axiaops.io/shared/observability"
)

// TestMetricsHandler_MergesDefaultAndPrivateRegistries pins the merge shape
// every binary's `/metrics` endpoint relies on. The shared observability
// package binds Global.* to a package-private registry; promhttp.Handler()
// reads only the default registry. A binary that wires `/metrics` with the
// bare promhttp.Handler() silently drops every auth_provider/http_*/db_*/
// aws_*/scan_* metric — the regression caught on MR !85's preview env.
//
// MetricsHandler is the single seam every binary now uses. This test pins
// both directions: a metric registered to the default registry AND a metric
// registered to the private registry must both appear in one scrape.
func TestMetricsHandler_MergesDefaultAndPrivateRegistries(t *testing.T) {
	// Register a sentinel into the default registry so we can assert the
	// merge isn't accidentally dropping DefaultGatherer. Using a unique name
	// to avoid collision with anything any other test may have registered.
	defaultSentinel := prometheus.NewCounter(prometheus.CounterOpts{
		Name: "axiaops_metrics_handler_default_registry_sentinel_total",
		Help: "Test-only sentinel; registered into prometheus.DefaultRegisterer to prove MetricsHandler scrapes it.",
	})
	prometheus.MustRegister(defaultSentinel)
	t.Cleanup(func() { prometheus.Unregister(defaultSentinel) })

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	observability.MetricsHandler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("MetricsHandler status: got %d want 200", rec.Code)
	}
	body := rec.Body.String()

	// Default-registry side — proves DefaultGatherer is in the merge.
	if !strings.Contains(body, "axiaops_metrics_handler_default_registry_sentinel_total") {
		t.Error("/metrics missing default-registry sentinel — Gatherers dropped DefaultGatherer")
	}

	// Private-registry side — bare metrics that emit at zero. Vec types
	// don't surface until a label-set is observed, so they're useless as
	// canaries. Pinning one bare metric per surface the observability
	// package owns keeps the failure message specific when a regression
	// disconnects the private registry from the merge.
	wantPrivate := []string{
		"axiaops_http_requests_total",        // §2.6 HTTP observability
		"axiaops_db_connections_active",      // §2.6 DB observability
		"axiaops_application_uptime_seconds", // application observability
		"axiaops_session_cache_errors_total", // session cache observability
	}
	for _, name := range wantPrivate {
		if !strings.Contains(body, name) {
			t.Errorf("/metrics missing private-registry metric %q — Gatherers dropped Registry()", name)
		}
	}
}

func TestHTTPMiddleware(t *testing.T) {
	// Create a simple handler that returns 200 OK
	handler := observability.HTTPMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("Hello"))
	}))

	// Create a test request
	req := httptest.NewRequest("GET", "/test", nil)
	w := httptest.NewRecorder()

	// Record the initial metrics
	handler.ServeHTTP(w, req)

	// Verify response
	if w.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d", w.Code)
	}

	if w.Body.String() != "Hello" {
		t.Errorf("expected 'Hello', got %q", w.Body.String())
	}
}

func TestDatabaseObserver(t *testing.T) {
	observer := observability.NewDatabaseObserver("SELECT")

	// Simulate some work
	time.Sleep(10 * time.Millisecond)

	// Should not panic
	observer.Observe()
	observer.ObserveError()
}

func TestTransactionObserver(t *testing.T) {
	observer := observability.NewTransactionObserver("insert")

	// Simulate some work
	time.Sleep(10 * time.Millisecond)

	// Should not panic
	observer.Observe()
}

func TestScanObserver(t *testing.T) {
	observer := observability.NewScanObserver("fetch")

	// Simulate scan work
	time.Sleep(10 * time.Millisecond)

	// Should not panic
	observer.Observe()
}

func TestAWSObserver(t *testing.T) {
	observer := observability.NewAWSObserver("CostExplorer")

	// Simulate AWS API call
	time.Sleep(10 * time.Millisecond)

	// Should not panic
	observer.Observe()
	observer.ObserveError()
}

func TestRecordScanMetrics(t *testing.T) {
	ctx := context.Background()

	// Should not panic
	observability.RecordScanStart(ctx)
	observability.RecordScanEnd(ctx)
	observability.RecordScanError("account-123", "network_error")
}

func TestHTTPObserverWriter(t *testing.T) {
	// Create a real response writer
	w := httptest.NewRecorder()

	// Wrap it with observer
	observer := &observability.HTTPObserverWriter{ResponseWriter: w}

	// Write header and data
	observer.WriteHeader(http.StatusCreated)
	_, _ = observer.Write([]byte("test"))

	// Verify captured values
	if observer.StatusCode() != http.StatusCreated {
		t.Errorf("expected status %d, got %d", http.StatusCreated, observer.StatusCode())
	}

	if observer.BytesWritten() != 4 {
		t.Errorf("expected 4 bytes written, got %d", observer.BytesWritten())
	}
}
