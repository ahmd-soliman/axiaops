package auth

// Test-only re-exports of unexported helpers.

// ValidUserName re-exports validUserName for the bootstrap/invitation
// name-validation test in handler_test.go.
func ValidUserName(s string) bool { return validUserName(s) }
