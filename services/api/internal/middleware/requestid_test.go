package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestRequestID_GeneratesUUIDWhenMissing(t *testing.T) {
	var capturedID string
	h := RequestID(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedID = RequestIDFromCtx(r.Context())
		w.WriteHeader(http.StatusOK)
	}))

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/test", nil)
	h.ServeHTTP(w, r)

	if capturedID == "" {
		t.Fatal("expected request ID in context")
	}

	headerID := w.Header().Get("X-Request-ID")
	if headerID == "" {
		t.Error("expected X-Request-ID header")
	}

	if capturedID != headerID {
		t.Errorf("context ID %q != header ID %q", capturedID, headerID)
	}
}

func TestRequestID_PassesExistingHeader(t *testing.T) {
	existingID := "custom-123-request-id"
	var capturedID string

	h := RequestID(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedID = RequestIDFromCtx(r.Context())
		w.WriteHeader(http.StatusOK)
	}))

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/test", nil)
	r.Header.Set("X-Request-ID", existingID)
	h.ServeHTTP(w, r)

	if capturedID != existingID {
		t.Fatalf("expected %s, got %s", existingID, capturedID)
	}

	headerID := w.Header().Get("X-Request-ID")
	if headerID != existingID {
		t.Errorf("expected header %s, got %s", existingID, headerID)
	}
}

func TestRequestID_StoredInContext(t *testing.T) {
	h := RequestID(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id1 := RequestIDFromCtx(r.Context())
		id2 := RequestIDFromCtx(r.Context())

		if id1 != id2 {
			t.Errorf("context ID not consistent: %q vs %q", id1, id2)
		}

		if id1 == "" {
			t.Fatal("context ID should not be empty")
		}

		w.WriteHeader(http.StatusOK)
	}))

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/test", nil)
	h.ServeHTTP(w, r)

	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", w.Code)
	}
}

func TestRequestID_MultipleRequests_DifferentIDs(t *testing.T) {
	var id1, id2 string

	h := RequestID(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if id1 == "" {
			id1 = RequestIDFromCtx(r.Context())
		} else {
			id2 = RequestIDFromCtx(r.Context())
		}
		w.WriteHeader(http.StatusOK)
	}))

	// First request
	w1 := httptest.NewRecorder()
	r1 := httptest.NewRequest(http.MethodGet, "/test", nil)
	h.ServeHTTP(w1, r1)

	// Second request
	w2 := httptest.NewRecorder()
	r2 := httptest.NewRequest(http.MethodGet, "/test", nil)
	h.ServeHTTP(w2, r2)

	if id1 == id2 {
		t.Errorf("expected different IDs, both got %q", id1)
	}

	if id1 == "" || id2 == "" {
		t.Fatal("IDs should not be empty")
	}
}

func TestRequestIDFromCtx_EmptyWhenMissing(t *testing.T) {
	r := httptest.NewRequest(http.MethodGet, "/test", nil)
	id := RequestIDFromCtx(r.Context())

	if id != "" {
		t.Errorf("expected empty string, got %q", id)
	}
}

func TestRequestID_HeaderSetInResponse(t *testing.T) {
	h := RequestID(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/test", nil)
	h.ServeHTTP(w, r)

	headerID := w.Header().Get("X-Request-ID")
	if headerID == "" {
		t.Fatal("expected X-Request-ID header in response")
	}
}

func TestRequestID_PreservesCasing(t *testing.T) {
	customID := "AbCdEf-123"
	var capturedID string

	h := RequestID(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedID = RequestIDFromCtx(r.Context())
		w.WriteHeader(http.StatusOK)
	}))

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/test", nil)
	r.Header.Set("X-Request-ID", customID)
	h.ServeHTTP(w, r)

	if capturedID != customID {
		t.Errorf("expected %s, got %s", customID, capturedID)
	}
}
