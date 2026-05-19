package sync_test

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"axiaops.io/shared/httpauth"
	syncqueue "axiaops.io/shared/queue/sync"
)

var testSecret = []byte("0123456789abcdef0123456789abcdef")

func TestEnqueue_SignsRequest(t *testing.T) {
	var (
		seenTimestamp string
		seenSignature string
		seenBody      []byte
	)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seenTimestamp = r.Header.Get(httpauth.HeaderTimestamp)
		seenSignature = r.Header.Get(httpauth.HeaderSignature)
		seenBody, _ = io.ReadAll(r.Body)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	q := syncqueue.New(srv.URL, testSecret)
	job := syncqueue.ScanJob{OrganizationID: "org-1", AccountID: "acc-1"}
	if err := q.Enqueue(context.Background(), job); err != nil {
		t.Fatalf("Enqueue: %v", err)
	}

	if seenTimestamp == "" || seenSignature == "" {
		t.Fatalf("expected signed headers, got timestamp=%q signature=%q",
			seenTimestamp, seenSignature)
	}
	tsSecs, err := strconv.ParseInt(seenTimestamp, 10, 64)
	if err != nil {
		t.Fatalf("timestamp parse: %v", err)
	}
	ts := time.Unix(tsSecs, 0)
	if err := httpauth.Verify(testSecret, time.Minute, time.Now,
		seenTimestamp, seenSignature, "POST", "/scan", seenBody); err != nil {
		t.Fatalf("server-side Verify failed: %v (ts=%s)", err, ts)
	}

	// Body shape sanity — same shape the receiver decodes.
	want := `{"account_id":"acc-1","organization_id":"org-1"}`
	if strings.TrimSpace(string(seenBody)) != want {
		t.Fatalf("body %q, want %q", seenBody, want)
	}
}

func TestEnqueue_NoSecret_SkipsHeaders(t *testing.T) {
	var (
		seenTimestamp string
		seenSignature string
	)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seenTimestamp = r.Header.Get(httpauth.HeaderTimestamp)
		seenSignature = r.Header.Get(httpauth.HeaderSignature)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	q := syncqueue.New(srv.URL, nil) // DEV_MODE
	job := syncqueue.ScanJob{OrganizationID: "org-1", AccountID: "acc-1"}
	if err := q.Enqueue(context.Background(), job); err != nil {
		t.Fatalf("Enqueue: %v", err)
	}

	if seenTimestamp != "" || seenSignature != "" {
		t.Fatalf("expected no auth headers in DEV_MODE, got timestamp=%q signature=%q",
			seenTimestamp, seenSignature)
	}
}

func TestEnqueue_PropagatesNon2xx(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer srv.Close()

	q := syncqueue.New(srv.URL, testSecret)
	job := syncqueue.ScanJob{OrganizationID: "org-1", AccountID: "acc-1"}
	err := q.Enqueue(context.Background(), job)
	if err == nil {
		t.Fatal("expected error on 401")
	}
	if !strings.Contains(err.Error(), "401") {
		t.Fatalf("error %v should mention 401", err)
	}
}
