package httpjson_test

import (
	"bytes"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"axiaops.io/api/internal/httpjson"
)

type sampleRequest struct {
	Email string `json:"email"`
	Token string `json:"token"`
}

// TestDecode_HappyPath — well-formed body within the size cap decodes.
func TestDecode_HappyPath(t *testing.T) {
	t.Parallel()
	body := `{"email":"u@example.com","token":"abc"}`
	r := httptest.NewRequest(http.MethodPost, "/x", strings.NewReader(body))
	w := httptest.NewRecorder()

	var got sampleRequest
	if err := httpjson.Decode(w, r, &got); err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if got.Email != "u@example.com" || got.Token != "abc" {
		t.Errorf("decoded mismatch: %+v", got)
	}
}

// TestDecode_RejectsUnknownFields — audit H-4: DisallowUnknownFields must
// be on so a future struct rename surfaces as a 400 rather than silent
// data loss. A request carrying an extra field is rejected.
func TestDecode_RejectsUnknownFields(t *testing.T) {
	t.Parallel()
	body := `{"email":"u@example.com","token":"abc","extra":"sneaky"}`
	r := httptest.NewRequest(http.MethodPost, "/x", strings.NewReader(body))
	w := httptest.NewRecorder()

	var got sampleRequest
	err := httpjson.Decode(w, r, &got)
	if err == nil {
		t.Fatal("expected unknown-field rejection, got nil")
	}
	if !strings.Contains(err.Error(), "unknown field") {
		t.Errorf("error doesn't name the unknown field: %v", err)
	}
}

// TestDecode_RejectsOversizedBody — audit H-4 core: a multi-GB request must
// not OOM the api. We use a body just over the cap to verify the limit fires.
func TestDecode_RejectsOversizedBody(t *testing.T) {
	t.Parallel()
	// Build a body that's MaxBodyBytes + 1 bytes long. The JSON shape is
	// valid (`{"token":"<huge>"}`) so only the size cap can reject it.
	padding := strings.Repeat("x", httpjson.MaxBodyBytes)
	body := `{"token":"` + padding + `"}`
	r := httptest.NewRequest(http.MethodPost, "/x", strings.NewReader(body))
	w := httptest.NewRecorder()

	var got sampleRequest
	err := httpjson.Decode(w, r, &got)
	if err == nil {
		t.Fatal("expected size-cap rejection on >64KiB body, got nil")
	}
	// The stdlib returns *http.MaxBytesError when the cap fires. Tests
	// caller can map this to 413; we just assert detectability.
	var mbe *http.MaxBytesError
	if !errors.As(err, &mbe) {
		t.Errorf("error not detectable as *http.MaxBytesError: %v", err)
	}
}

// TestDecode_RejectsMalformedJSON — a syntactically invalid body returns
// the decoder's error (caller maps to 400).
func TestDecode_RejectsMalformedJSON(t *testing.T) {
	t.Parallel()
	r := httptest.NewRequest(http.MethodPost, "/x", bytes.NewReader([]byte(`{not json}`)))
	w := httptest.NewRecorder()

	var got sampleRequest
	if err := httpjson.Decode(w, r, &got); err == nil {
		t.Fatal("expected JSON syntax error, got nil")
	}
}
