package auth

// Test-only re-exports of unexported helpers. See `requestIP` for the
// production threat model — RequestIP is the test handle.

import "net/http"
import "net"

func RequestIP(r *http.Request) net.IP { return requestIP(r) }

// ValidUserName re-exports validUserName for the bootstrap/invitation
// name-validation test in handler_test.go.
func ValidUserName(s string) bool { return validUserName(s) }
