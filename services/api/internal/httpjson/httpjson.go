// Package httpjson decodes JSON request bodies with a default-secure posture:
// a 64 KiB MaxBytesReader cap so a multi-GB body can't OOM the api, plus
// DisallowUnknownFields so a future struct rename surfaces as a 400 rather
// than silent data loss.
//
// Every mutating handler in services/api should use Decode rather than calling
// json.NewDecoder(r.Body).Decode directly. Audit H-4 caught three packages
// (api, sso, auth-pre-extraction) each rolling their own — the auth package
// already had a private decodeJSON with the right shape; this package promotes
// it to the single seam every handler uses.
//
// Request structs are small (~1 KiB max in the codebase today). The 64 KiB
// cap is generous enough for any current or near-future request struct.
package httpjson

import (
	"encoding/json"
	"net/http"
)

// MaxBodyBytes is the per-request body cap applied by Decode.
const MaxBodyBytes = 64 << 10

// Decode reads at most MaxBodyBytes from r.Body, decodes the JSON into dst,
// and rejects unknown fields. ResponseWriter is passed to MaxBytesReader so
// the stdlib emits the connection-close hint when the cap is hit (signal
// only — Decode does not write the response; the caller decides the status
// code from the returned error).
//
// Returns the decoder's error verbatim. Callers typically map any error to
// 400 with an "invalid request body" or "body too large" message; the
// stdlib's *http.MaxBytesError is distinguishable via errors.As if a
// caller wants a 413 for the size case specifically.
func Decode(w http.ResponseWriter, r *http.Request, dst any) error {
	r.Body = http.MaxBytesReader(w, r.Body, MaxBodyBytes)
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	return dec.Decode(dst)
}
