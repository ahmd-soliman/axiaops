package model

import (
	"errors"
	"fmt"
	"net/mail"
	"strings"
)

// Sentinel errors so handlers can distinguish format failures from
// missing-TLD ("alice@test") for messaging purposes if they want. Most
// callers can just `errors.Is(err, ErrInvalidEmail)` or surface err.Error().
var (
	ErrInvalidEmail        = errors.New("invalid email")
	ErrInvalidEmailNoTLD   = errors.New("email must include a domain (e.g. user@example.com)")
	ErrInvalidEmailMissing = errors.New("email is required")
)

// ValidateInvitableEmail is the strict validator for emails entering the
// system through invitation, bootstrap, or add-member flows. It is
// deliberately stricter than RFC 5322 (which net/mail.ParseAddress
// implements): RFC allows single-label hostnames like "alice@intranet",
// but in our DACH/Mittelstand customer base the vastly more common cause
// of such addresses is a typo ("alice@test.com" → "alice@test"), and a
// silently-undeliverable invite is worse than rejecting a rare valid
// intranet address.
//
// Use this at every endpoint where a user supplies an email that the
// system will later route invite mail / OOB redemption URLs to.
//
// Do NOT use this on lookup paths (login, password-reset-redeem) — those
// must match whatever is in the DB, and being stricter than the
// insertion-time check would lock out accounts that somehow have legacy
// non-conformant emails.
func ValidateInvitableEmail(s string) error {
	s = strings.TrimSpace(s)
	if s == "" {
		return ErrInvalidEmailMissing
	}
	addr, err := mail.ParseAddress(s)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrInvalidEmail, err)
	}
	// addr.Address is the normalised local-part@domain form. Find the @
	// and require the right-hand side to contain at least one '.' followed
	// by at least one character — rules out "alice@example" and "alice@."
	// alike.
	at := strings.LastIndexByte(addr.Address, '@')
	if at < 0 {
		// Unreachable: mail.ParseAddress would have failed already. Belt
		// and braces.
		return ErrInvalidEmail
	}
	domain := addr.Address[at+1:]
	dot := strings.LastIndexByte(domain, '.')
	if dot < 0 || dot == len(domain)-1 {
		return ErrInvalidEmailNoTLD
	}
	return nil
}
