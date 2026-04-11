package observability_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"axiaops.io/shared/observability"
)

func TestHTTPMiddleware(t *testing.T) {
	// Create a simple handler that returns 200 OK
	handler := observability.HTTPMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("Hello"))
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
	observer.Write([]byte("test"))

	// Verify captured values
	if observer.StatusCode() != http.StatusCreated {
		t.Errorf("expected status %d, got %d", http.StatusCreated, observer.StatusCode())
	}

	if observer.BytesWritten() != 4 {
		t.Errorf("expected 4 bytes written, got %d", observer.BytesWritten())
	}
}
