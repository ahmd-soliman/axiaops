package httpauth_test

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"axiaops.io/shared/httpauth"
)

func newSignedReq(t *testing.T, secret []byte, now time.Time, method, path string, body []byte) *http.Request {
	t.Helper()
	r := httptest.NewRequest(method, path, bytes.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set(httpauth.HeaderTimestamp, strconv.FormatInt(now.Unix(), 10))
	r.Header.Set(httpauth.HeaderSignature, httpauth.Sign(secret, now, method, path, body))
	return r
}

func TestMiddleware_HappyPath(t *testing.T) {
	now := time.Unix(1_715_740_000, 0)
	called := false
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		got, _ := io.ReadAll(r.Body)
		if !bytes.Equal(got, testBody) {
			t.Fatalf("inner saw body %q, want %q", got, testBody)
		}
		w.WriteHeader(http.StatusOK)
	})
	h := httpauth.Middleware(testSecret, httpauth.Options{Now: nowAt(now)}, inner)

	rr := httptest.NewRecorder()
	r := newSignedReq(t, testSecret, now, testMethod, testPath, testBody)
	h.ServeHTTP(rr, r)

	if !called {
		t.Fatal("inner handler not invoked")
	}
	if rr.Code != http.StatusOK {
		t.Fatalf("status %d, want 200", rr.Code)
	}
}

func TestMiddleware_MissingHeaders_401(t *testing.T) {
	now := time.Unix(1_715_740_000, 0)
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("inner handler should not be invoked on missing header")
	})
	h := httpauth.Middleware(testSecret, httpauth.Options{Now: nowAt(now)}, inner)

	rr := httptest.NewRecorder()
	r := httptest.NewRequest(testMethod, testPath, bytes.NewReader(testBody))
	h.ServeHTTP(rr, r)

	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("status %d, want 401", rr.Code)
	}
	if !strings.Contains(rr.Body.String(), "ingestion_unauthorised") {
		t.Fatalf("body %q missing ingestion_unauthorised", rr.Body.String())
	}
	if got := rr.Header().Get("WWW-Authenticate"); got != httpauth.SignatureAlgorithm {
		t.Fatalf("WWW-Authenticate %q, want %q", got, httpauth.SignatureAlgorithm)
	}
}

func TestMiddleware_WrongSecret_401(t *testing.T) {
	now := time.Unix(1_715_740_000, 0)
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("inner handler should not be invoked on wrong secret")
	})
	h := httpauth.Middleware(testSecret, httpauth.Options{Now: nowAt(now)}, inner)

	rr := httptest.NewRecorder()
	r := newSignedReq(t, otherSecret, now, testMethod, testPath, testBody)
	h.ServeHTTP(rr, r)

	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("status %d, want 401", rr.Code)
	}
}

func TestMiddleware_StaleTimestamp_401(t *testing.T) {
	now := time.Unix(1_715_740_000, 0)
	stale := now.Add(-2 * time.Minute)
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("inner handler should not be invoked on stale timestamp")
	})
	h := httpauth.Middleware(testSecret, httpauth.Options{MaxSkew: time.Minute, Now: nowAt(now)}, inner)

	rr := httptest.NewRecorder()
	r := newSignedReq(t, testSecret, stale, testMethod, testPath, testBody)
	h.ServeHTTP(rr, r)

	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("status %d, want 401", rr.Code)
	}
}

func TestMiddleware_BodyCap_413(t *testing.T) {
	now := time.Unix(1_715_740_000, 0)
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("inner handler should not be invoked when body is too large")
	})
	h := httpauth.Middleware(testSecret, httpauth.Options{Now: nowAt(now)}, inner)

	huge := bytes.Repeat([]byte{'x'}, int(httpauth.MaxBodyBytes)+1)
	rr := httptest.NewRecorder()
	r := newSignedReq(t, testSecret, now, testMethod, testPath, huge)
	h.ServeHTTP(rr, r)

	if rr.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("status %d, want 413", rr.Code)
	}
	if !strings.Contains(rr.Body.String(), "request_body_too_large") {
		t.Fatalf("body %q missing request_body_too_large", rr.Body.String())
	}
}

func TestMiddleware_SoftEnforce_PassesThrough(t *testing.T) {
	now := time.Unix(1_715_740_000, 0)
	called := false
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusAccepted)
	})
	h := httpauth.Middleware(testSecret, httpauth.Options{SoftEnforce: true, Now: nowAt(now)}, inner)

	rr := httptest.NewRecorder()
	// Unsigned request — would normally 401.
	r := httptest.NewRequest(testMethod, testPath, bytes.NewReader(testBody))
	h.ServeHTTP(rr, r)

	if !called {
		t.Fatal("soft-enforce should pass through to inner")
	}
	if rr.Code != http.StatusAccepted {
		t.Fatalf("status %d, want 202 (from inner)", rr.Code)
	}
}

func TestMiddleware_InnerCanDecodeJSON(t *testing.T) {
	// Verifies body re-presentation: the inner handler's json.Decoder must
	// see the same bytes that signed the request. This is the regression
	// guard against the body-consume-then-forget bug in early drafts.
	now := time.Unix(1_715_740_000, 0)
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var got struct {
			AccountID      string `json:"account_id"`
			OrganizationID string `json:"organization_id"`
		}
		if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
			t.Fatalf("inner decode: %v", err)
		}
		if got.AccountID != "acc-1" || got.OrganizationID != "org-1" {
			t.Fatalf("decoded %+v, want {acc-1, org-1}", got)
		}
		w.WriteHeader(http.StatusOK)
	})
	h := httpauth.Middleware(testSecret, httpauth.Options{Now: nowAt(now)}, inner)

	rr := httptest.NewRecorder()
	r := newSignedReq(t, testSecret, now, testMethod, testPath, testBody)
	h.ServeHTTP(rr, r)
	if rr.Code != http.StatusOK {
		t.Fatalf("status %d, want 200", rr.Code)
	}
}

func TestMultiSecretMiddleware_BothAccept(t *testing.T) {
	// Two-secret rotation: a request signed with secret A must verify when
	// the middleware has [B, A] in its slot list (any order).
	now := time.Unix(1_715_740_000, 0)
	inner := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	h := httpauth.MultiSecretMiddleware([][]byte{otherSecret, testSecret},
		httpauth.Options{Now: nowAt(now)}, inner)

	rr := httptest.NewRecorder()
	r := newSignedReq(t, testSecret, now, testMethod, testPath, testBody)
	h.ServeHTTP(rr, r)
	if rr.Code != http.StatusOK {
		t.Fatalf("status %d, want 200 (signed with second slot)", rr.Code)
	}
}

func TestMultiSecretMiddleware_NoSecretsIsPassthrough(t *testing.T) {
	// DEV_MODE composition root passes nil — the middleware degrades to a
	// passthrough so unsigned dev traffic still works.
	now := time.Unix(1_715_740_000, 0)
	called := false
	inner := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	})
	h := httpauth.MultiSecretMiddleware([][]byte{nil},
		httpauth.Options{Now: nowAt(now)}, inner)

	rr := httptest.NewRecorder()
	r := httptest.NewRequest(testMethod, testPath, bytes.NewReader(testBody))
	h.ServeHTTP(rr, r)
	if !called {
		t.Fatal("nil-secrets passthrough should invoke inner")
	}
	if rr.Code != http.StatusOK {
		t.Fatalf("status %d, want 200", rr.Code)
	}
}

func TestPassthroughWithWarning_UnsignedReachesInner(t *testing.T) {
	called := false
	inner := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	})
	h := httpauth.PassthroughWithWarning(inner)

	rr := httptest.NewRecorder()
	r := httptest.NewRequest(testMethod, testPath, bytes.NewReader(testBody))
	h.ServeHTTP(rr, r)
	if !called {
		t.Fatal("passthrough should invoke inner")
	}
	if rr.Code != http.StatusOK {
		t.Fatalf("status %d, want 200", rr.Code)
	}
}

func TestPassthroughWithWarning_SignedReachesInnerToo(t *testing.T) {
	// Passthrough must NOT reject signed requests — the DEV_MODE / prod-api
	// mismatch should be loud-once but never 401 the request (otherwise the
	// gap-during-rollout cuts traffic).
	now := time.Unix(1_715_740_000, 0)
	called := false
	inner := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	})
	h := httpauth.PassthroughWithWarning(inner)

	rr := httptest.NewRecorder()
	r := newSignedReq(t, testSecret, now, testMethod, testPath, testBody)
	h.ServeHTTP(rr, r)
	if !called {
		t.Fatal("passthrough should invoke inner for signed requests")
	}
	if rr.Code != http.StatusOK {
		t.Fatalf("status %d, want 200", rr.Code)
	}
}
