package api_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"axiaops.io/api/internal/api"
)

// pingableCache satisfies cache.Cache with a configurable Ping outcome. Only
// Ping is exercised by the readyz handler — the other methods exist so this
// stub satisfies the interface but are no-ops.
type pingableCache struct {
	pingErr error
}

func (p *pingableCache) Get(context.Context, string) ([]byte, error)              { return nil, nil }
func (p *pingableCache) Set(context.Context, string, []byte, time.Duration) error { return nil }
func (p *pingableCache) Del(context.Context, string) error                        { return nil }
func (p *pingableCache) Incr(context.Context, string, time.Duration) (int64, error) {
	return 0, nil
}
func (p *pingableCache) Ping(context.Context) error { return p.pingErr }
func (p *pingableCache) Close() error               { return nil }

// ── /livez ────────────────────────────────────────────────────────────────────

func TestLivez_AlwaysReturnsOK(t *testing.T) {
	store := NewMockStore()
	h := api.New(store, noopQueue())
	mux := newMux(h)

	w := httptest.NewRecorder()
	mux.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/livez", nil))

	if w.Code != http.StatusOK {
		t.Fatalf("livez: expected 200, got %d", w.Code)
	}
	if got := w.Body.String(); got != `{"status":"ok"}` {
		t.Errorf("livez body: got %q, want %q", got, `{"status":"ok"}`)
	}
}

// livez never pings the DB — failing-DB store should still get 200.
func TestLivez_NeverChecksDB(t *testing.T) {
	failingStore := &mockStoreWithFailingPing{
		MockStore: NewMockStore(),
		pingErr:   errors.New("db is down"),
	}
	h := api.New(failingStore, noopQueue())
	mux := newMux(h)

	w := httptest.NewRecorder()
	mux.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/livez", nil))

	if w.Code != http.StatusOK {
		t.Errorf("livez should ignore DB state, got %d", w.Code)
	}
}

// ── /readyz ───────────────────────────────────────────────────────────────────

func TestReadyz_AllOK_Returns200(t *testing.T) {
	store := NewMockStore()
	h := api.New(store, noopQueue()).WithRedisCache(&pingableCache{})
	mux := newMux(h)

	w := httptest.NewRecorder()
	mux.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/readyz", nil))

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d — body: %s", w.Code, w.Body.String())
	}
	body := decodeBody(t, w)
	if body["status"] != "ok" || body["db"] != "ok" || body["redis"] != "ok" {
		t.Errorf("body: got %+v, want all ok", body)
	}
}

func TestReadyz_DBDown_Returns503(t *testing.T) {
	store := &mockStoreWithFailingPing{
		MockStore: NewMockStore(),
		pingErr:   errors.New("db is down"),
	}
	h := api.New(store, noopQueue()).WithRedisCache(&pingableCache{})
	mux := newMux(h)

	w := httptest.NewRecorder()
	mux.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/readyz", nil))

	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("DB down: expected 503, got %d", w.Code)
	}
	body := decodeBody(t, w)
	if body["status"] != "error" {
		t.Errorf("status: got %q, want error", body["status"])
	}
	if body["db"] != "unreachable" {
		t.Errorf("db: got %q, want unreachable", body["db"])
	}
}

func TestReadyz_RedisDown_Returns200WithDegradedStatus(t *testing.T) {
	// DB up, Redis down — instance should stay in rotation. Pulling the
	// instance for a degraded cache is worse than the degradation itself.
	store := NewMockStore()
	h := api.New(store, noopQueue()).
		WithRedisCache(&pingableCache{pingErr: errors.New("redis is down")})
	mux := newMux(h)

	w := httptest.NewRecorder()
	mux.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/readyz", nil))

	if w.Code != http.StatusOK {
		t.Fatalf("redis down should not 503, got %d", w.Code)
	}
	body := decodeBody(t, w)
	if body["status"] != "degraded" {
		t.Errorf("status: got %q, want degraded", body["status"])
	}
	if body["db"] != "ok" {
		t.Errorf("db: got %q, want ok", body["db"])
	}
	if body["redis"] != "unreachable" {
		t.Errorf("redis: got %q, want unreachable", body["redis"])
	}
}

func TestReadyz_RedisNotConfigured_ReportsSkipped(t *testing.T) {
	// No WithRedisCache → handler treats Redis as not configured for this
	// deployment. Distinct from "unreachable" so monitoring rules can ignore
	// dev/local environments.
	store := NewMockStore()
	h := api.New(store, noopQueue())
	mux := newMux(h)

	w := httptest.NewRecorder()
	mux.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/readyz", nil))

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	body := decodeBody(t, w)
	if body["redis"] != "skipped" {
		t.Errorf("redis: got %q, want skipped", body["redis"])
	}
	if body["status"] != "ok" {
		t.Errorf("status: got %q, want ok", body["status"])
	}
}

// ── /health back-compat ───────────────────────────────────────────────────────

// /health was the deep check before the split; existing consumers (Docker
// depends_on, nginx) rely on its current behaviour. The split must not change
// it — same body, same DB-ping semantics, same status code on failure.
func TestHealth_StillPingsDB(t *testing.T) {
	store := &mockStoreWithFailingPing{
		MockStore: NewMockStore(),
		pingErr:   errors.New("db is down"),
	}
	h := api.New(store, noopQueue())
	mux := newMux(h)

	w := httptest.NewRecorder()
	mux.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/health", nil))

	if w.Code != http.StatusServiceUnavailable {
		t.Errorf("health with failing DB: got %d, want 503", w.Code)
	}
}

// ── helpers ───────────────────────────────────────────────────────────────────

func decodeBody(t *testing.T, w *httptest.ResponseRecorder) map[string]string {
	t.Helper()
	var body map[string]string
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v — raw: %s", err, w.Body.String())
	}
	return body
}
