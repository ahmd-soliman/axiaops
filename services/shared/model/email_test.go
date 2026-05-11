package model_test

import (
	"errors"
	"testing"

	"axiaops.io/shared/model"
)

func TestValidateInvitableEmail(t *testing.T) {
	cases := []struct {
		name    string
		input   string
		wantErr error // model.Err* sentinel; nil = should pass
	}{
		// Happy paths
		{"standard", "alice@example.com", nil},
		{"plus addressing", "alice+team@example.com", nil},
		{"dotted local", "alice.bob@example.co.uk", nil},
		{"hyphenated domain", "alice@my-corp.com", nil},
		{"leading whitespace trimmed", "  alice@example.com", nil},
		{"trailing whitespace trimmed", "alice@example.com  ", nil},
		{"single-char tld", "alice@example.c", nil}, // RFC permissive; .c happens to be valid (.co.uk single-char doesn't exist, but the regex doesn't check tld registry — that's beyond scope)

		// Empty / missing
		{"empty", "", model.ErrInvalidEmailMissing},
		{"whitespace only", "   ", model.ErrInvalidEmailMissing},

		// Malformed (caught by net/mail.ParseAddress)
		{"no at-sign", "notanemail", model.ErrInvalidEmail},
		{"trailing at-sign", "alice@", model.ErrInvalidEmail},
		{"leading at-sign", "@example.com", model.ErrInvalidEmail},
		{"two at-signs", "alice@@example.com", model.ErrInvalidEmail},
		{"spaces in local", "alice b@example.com", model.ErrInvalidEmail},

		// Missing TLD — the main case this validator exists for
		{"single-label host", "alice@example", model.ErrInvalidEmailNoTLD},
		{"single-label intranet", "alice@intranet", model.ErrInvalidEmailNoTLD},
		{"single-label localhost", "alice@localhost", model.ErrInvalidEmailNoTLD},
		// net/mail.ParseAddress rejects "alice@example." before our TLD
		// check runs — so the error is ErrInvalidEmail, not the more
		// specific ErrInvalidEmailNoTLD. Either is acceptable (input is
		// rejected), pin the actual behaviour so a future parser swap is
		// surfaced.
		{"trailing dot on domain", "alice@example.", model.ErrInvalidEmail},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := model.ValidateInvitableEmail(tc.input)
			switch {
			case tc.wantErr == nil:
				if err != nil {
					t.Errorf("ValidateInvitableEmail(%q) = %v, want nil", tc.input, err)
				}
			case !errors.Is(err, tc.wantErr):
				t.Errorf("ValidateInvitableEmail(%q) = %v, want errors.Is %v", tc.input, err, tc.wantErr)
			}
		})
	}
}
