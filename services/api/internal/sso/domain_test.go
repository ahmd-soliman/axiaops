package sso_test

import (
	"context"
	"errors"
	"testing"

	"axiaops.io/api/internal/sso"
)

// stubResolver returns canned TXT records and an optional error.
type stubResolver struct {
	records map[string][]string
	err     error
}

func (s stubResolver) LookupTXT(_ context.Context, name string) ([]string, error) {
	if s.err != nil {
		return nil, s.err
	}
	return s.records[name], nil
}

func TestNormaliseDomain_AcceptsCustomerDomains(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		{"acme.com", "acme.com"},
		{"ACME.COM", "acme.com"},
		{"  acme.com  ", "acme.com"},
		{"@acme.com", "acme.com"},
		{"acme.com.", "acme.com"},
		{"sub.acme.com", "sub.acme.com"},
		{"acme.co.uk", "acme.co.uk"},
	}
	for _, tc := range cases {
		t.Run(tc.in, func(t *testing.T) {
			got, err := sso.NormaliseDomain(tc.in)
			if err != nil {
				t.Fatalf("NormaliseDomain(%q) err = %v; want nil", tc.in, err)
			}
			if got != tc.want {
				t.Errorf("NormaliseDomain(%q) = %q; want %q", tc.in, got, tc.want)
			}
		})
	}
}

func TestNormaliseDomain_RejectsPublicSuffixes(t *testing.T) {
	// gmail.com / outlook.com etc. are not rejected here — they're technically
	// registrable, so publicsuffix.PublicSuffix returns false for them. A
	// "common email provider" blocklist is a follow-up slice's job.
	cases := []string{
		"co.uk",     // bare ICANN public suffix
		"github.io", // private suffix (per Public Suffix List)
	}
	for _, in := range cases {
		t.Run(in, func(t *testing.T) {
			_, err := sso.NormaliseDomain(in)
			if !errors.Is(err, sso.ErrPublicSuffixDomain) {
				t.Errorf("NormaliseDomain(%q) err = %v; want ErrPublicSuffixDomain", in, err)
			}
		})
	}
}

func TestNormaliseDomain_RejectsMalformed(t *testing.T) {
	cases := []string{
		"",
		"   ",
		"localhost",          // no dot
		"http://acme.com",    // contains forbidden character
		"acme.com/path",      // contains forbidden character
		"acme com",           // contains space
	}
	for _, in := range cases {
		t.Run(in, func(t *testing.T) {
			_, err := sso.NormaliseDomain(in)
			if !errors.Is(err, sso.ErrInvalidDomain) {
				t.Errorf("NormaliseDomain(%q) err = %v; want ErrInvalidDomain", in, err)
			}
		})
	}
}

func TestVerifyTXT_FindsExactMatch(t *testing.T) {
	res := stubResolver{records: map[string][]string{
		"acme.com": {"v=spf1 -all", "axiaops-domain-verification=tok-123"},
	}}
	ok, err := sso.VerifyTXT(t.Context(), res, "acme.com", "axiaops-domain-verification=tok-123")
	if err != nil {
		t.Fatalf("VerifyTXT err = %v", err)
	}
	if !ok {
		t.Fatal("VerifyTXT returned false; want true (exact match present)")
	}
}

func TestVerifyTXT_RejectsTokenMismatch(t *testing.T) {
	res := stubResolver{records: map[string][]string{
		"acme.com": {"axiaops-domain-verification=tok-WRONG"},
	}}
	ok, err := sso.VerifyTXT(t.Context(), res, "acme.com", "axiaops-domain-verification=tok-123")
	if err != nil {
		t.Fatalf("VerifyTXT err = %v", err)
	}
	if ok {
		t.Fatal("VerifyTXT returned true on mismatched token; want false")
	}
}
