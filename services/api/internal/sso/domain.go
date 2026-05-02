package sso

import (
	"context"
	"errors"
	"fmt"
	"net"
	"strings"

	"golang.org/x/net/publicsuffix"
)

// DNSResolver is the seam for DNS TXT lookups so tests don't hit real DNS.
type DNSResolver interface {
	LookupTXT(ctx context.Context, name string) ([]string, error)
}

// netResolver wraps net.Resolver in the DNSResolver interface.
type netResolver struct{}

func (netResolver) LookupTXT(ctx context.Context, name string) ([]string, error) {
	return net.DefaultResolver.LookupTXT(ctx, name)
}

// DefaultDNSResolver is the production-bound DNS resolver.
var DefaultDNSResolver DNSResolver = netResolver{}

// ErrPublicSuffixDomain is returned by NormaliseDomain when the input is the
// public suffix itself (e.g. "co.uk", "github.io") or a one-label name. Such
// names cannot be claimed by an organisation — verifying them would let an
// attacker block all logins for any user on that suffix.
var ErrPublicSuffixDomain = errors.New("sso: domain is a public suffix and cannot be claimed")

// ErrInvalidDomain is returned for syntactically malformed input.
var ErrInvalidDomain = errors.New("sso: invalid domain")

// NormaliseDomain lowercases, trims, and rejects:
//   - empty / whitespace-only input
//   - a leading "@" (so admins can paste "@acme.com")
//   - any name that's a public suffix or shorter (gmail.com, outlook.com,
//     and bare TLDs)
//
// Returns the canonical form (lowercased, trimmed, no leading dot).
func NormaliseDomain(raw string) (string, error) {
	d := strings.TrimSpace(raw)
	d = strings.TrimPrefix(d, "@")
	d = strings.TrimSuffix(d, ".")
	d = strings.ToLower(d)
	if d == "" {
		return "", fmt.Errorf("%w: empty", ErrInvalidDomain)
	}
	if strings.ContainsAny(d, " /\\?#") {
		return "", fmt.Errorf("%w: contains forbidden character", ErrInvalidDomain)
	}
	if !strings.Contains(d, ".") {
		return "", fmt.Errorf("%w: missing label", ErrInvalidDomain)
	}

	// publicsuffix.PublicSuffix returns ("co.uk", false) for "acme.co.uk" — the
	// suffix itself. icann=true means it's an ICANN-managed TLD; false covers
	// private suffixes (github.io, herokuapp.com). Both must be rejected: a
	// claim on github.io would let one org block all GitHub Pages users.
	suffix, _ := publicsuffix.PublicSuffix(d)
	if suffix == d {
		return "", fmt.Errorf("%w: %q", ErrPublicSuffixDomain, d)
	}
	// Domain must have at least one label above the public suffix
	// (acme.com → "acme" + suffix "com"). EffectiveTLDPlusOne enforces this.
	if _, err := publicsuffix.EffectiveTLDPlusOne(d); err != nil {
		return "", fmt.Errorf("%w: not registrable: %v", ErrInvalidDomain, err)
	}
	return d, nil
}

// VerifyTXT looks up TXT records on `domain` and returns true if any record
// exactly equals `expectedToken`. Match is case-sensitive — providers preserve
// TXT case verbatim.
//
// Lookup name is the domain itself, not "_axiaops-verification.<domain>" — the
// design doc opted for the bare-domain placement to match what most IdP
// vendors do (Google, Okta), avoiding the operator confusion of "I added the
// TXT record but it's not finding it" caused by zone-file-vs-name mismatches.
func VerifyTXT(ctx context.Context, resolver DNSResolver, domain, expectedToken string) (bool, error) {
	if resolver == nil {
		resolver = DefaultDNSResolver
	}
	records, err := resolver.LookupTXT(ctx, domain)
	if err != nil {
		// LookupTXT returns *net.DNSError on NXDOMAIN; treat as "not found"
		// rather than propagating, so the verification handler returns 404
		// not 500. The caller surfaces the distinction via verified=false.
		var dnsErr *net.DNSError
		if errors.As(err, &dnsErr) && dnsErr.IsNotFound {
			return false, nil
		}
		return false, fmt.Errorf("sso: lookup TXT for %q: %w", domain, err)
	}
	for _, r := range records {
		if r == expectedToken {
			return true, nil
		}
	}
	return false, nil
}
