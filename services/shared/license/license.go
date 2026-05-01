// Package license verifies the AxiaOps self-hosted license JWT at startup.
//
// Per plan §4.9 the verification is purely offline: the binary trusts an
// embedded RS256 public key, the customer ships a JWT signed by AxiaOps's
// matching offline private key, and the binary refuses to start once that
// JWT is past its grace window. There is no license server and no phone-home.
//
// SaaS deployments do not use this package — the SaaS composition root
// gates access via Stripe instead. Self-hosted main.go calls Load + CheckExpiry
// before serverbuild.ComposeServer; DEV_MODE=true short-circuits the call.
package license

import (
	"crypto/rsa"
	"errors"
	"fmt"
	"math"
	"os"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

// Issuer and Audience are the fixed claim values every license must carry.
// Mismatched values are rejected — this is the cheapest defence against a
// JWT minted by some other AxiaOps signing key (e.g. a forgotten test key)
// being accepted by a production binary.
const (
	Issuer   = "https://axiaops.io/licenses"
	Audience = "axiaops-api"
)

// Env var names. EnvLicensePath defaults to DefaultLicensePath when unset.
// EnvPubKeyPath is a *test/dev* override — production binaries use the
// embedded key. Documented in CLAUDE.md (slice 7).
const (
	EnvLicense         = "AXIAOPS_LICENSE"
	EnvLicensePath     = "AXIAOPS_LICENSE_PATH"
	EnvPubKeyPath      = "LICENSE_PUBLIC_KEY_PATH"
	DefaultLicensePath = "/etc/axiaops/license.jwt"
)

// clockSkewLeeway tolerates small wall-clock drift between the issuer host
// and the verifying host on iat / nbf checks. Keeps a freshly-issued license
// from being rejected on a target machine whose NTP is a second behind.
// Mirrors jwt.WithLeeway's role for the parser-level checks we opted out of.
const clockSkewLeeway = 60 * time.Second

// maxGracePeriodDays bounds the grace_period_days claim. 90 days = quarterly
// rotation cadence, aligned with SOC2-style high-value-credential lifetime
// guidance and matching the grace ceilings comparable vendors (GitLab EE,
// Atlassian DC, HashiCorp Enterprise) ship with. Compile-time constant: the
// only way past it is a binary release with a new value, which the leaked-
// key threat model deliberately excludes from the attacker's reach.
//
// Legitimate "extend this customer further" cases use a longer `exp`
// (contract end date) instead of a longer grace — different fields with
// different semantics; see docs/license-issuance.md.
const maxGracePeriodDays = 90

// State classifies a loaded license against the current wall clock.
//
// StateNotLoaded is a separate value (not a permissive default of StateValid)
// so callers like the scan-gate in slice 5 can decide policy explicitly:
// DEV_MODE / SaaS / pre-license-ceremony all surface as StateNotLoaded and
// the policy lives at the call site, not buried in this accessor.
type State int

const (
	StateValid State = iota
	StateInGrace
	StateExpired
	StateNotLoaded
)

func (s State) String() string {
	switch s {
	case StateValid:
		return "valid"
	case StateInGrace:
		return "in_grace"
	case StateExpired:
		return "expired"
	case StateNotLoaded:
		return "not_loaded"
	default:
		return "unknown"
	}
}

// License is the validated set of claims. The struct holds only the fields
// the API service consumes — the raw JWT is discarded after parsing.
type License struct {
	LicenseID        string
	CustomerID       string    // sub
	ContractID       string
	IssuedAt         time.Time
	ExpiresAt        time.Time
	MaxOrganizations int
	Features         []string
	GracePeriodDays  int
}

// ErrNoLicense is returned when neither EnvLicense nor a readable file at
// EnvLicensePath / DefaultLicensePath is present. The startup path in
// cmd/main.go translates this into a clear "set AXIAOPS_LICENSE=..." message
// so operators are not left guessing.
var ErrNoLicense = errors.New("license: no license configured (set AXIAOPS_LICENSE or AXIAOPS_LICENSE_PATH)")

// licenseClaims is the wire-shape of the JWT body. Standard RegisteredClaims
// gives us iss/sub/aud/iat/exp validation via jwt.WithValidMethods + WithIssuer
// + WithAudience. Custom claims are decoded explicitly.
type licenseClaims struct {
	jwt.RegisteredClaims
	LicenseID        string   `json:"license_id"`
	ContractID       string   `json:"contract_id"`
	MaxOrganizations int      `json:"max_organizations"`
	Features         []string `json:"features"`
	GracePeriodDays  int      `json:"grace_period_days"`
}

// Load reads the license JWT from EnvLicense (preferred) or the file at
// EnvLicensePath (default DefaultLicensePath), verifies its RS256 signature
// against the embedded (or EnvPubKeyPath-overridden) public key, validates
// iss / aud / iat / nbf, and returns the parsed License.
//
// The function is a pure read — it does not cache, does not log, and does
// not mutate global state. Callers re-invoke on restart.
func Load() (*License, error) {
	raw, err := readLicenseJWT()
	if err != nil {
		return nil, err
	}

	pub, err := loadPublicKey()
	if err != nil {
		return nil, fmt.Errorf("license: load public key: %w", err)
	}

	claims := &licenseClaims{}
	// Claim validation is intentionally disabled here so Load can return a
	// parsed License even when exp has passed — the in-grace classification
	// is CheckExpiry's job. iss / aud / iat are validated manually below
	// against the same claim values; signature and alg are still enforced
	// by the parser.
	parser := jwt.NewParser(
		jwt.WithValidMethods([]string{jwt.SigningMethodRS256.Name}),
		jwt.WithoutClaimsValidation(),
	)
	if _, err := parser.ParseWithClaims(raw, claims, func(t *jwt.Token) (any, error) {
		// Algorithm-confusion mitigation (plan §4.9 → §11.3 generalised).
		// jwt.WithValidMethods already gates the alg header, but we belt-and-
		// braces with a type assertion in case a future library change weakens
		// the gate.
		if _, ok := t.Method.(*jwt.SigningMethodRSA); !ok {
			return nil, fmt.Errorf("unexpected signing method %q", t.Method.Alg())
		}
		return pub, nil
	}); err != nil {
		return nil, fmt.Errorf("license: verify: %w", err)
	}

	if claims.Issuer != Issuer {
		return nil, fmt.Errorf("license: wrong issuer %q (want %q)", claims.Issuer, Issuer)
	}
	if !claimsHasAudience(claims, Audience) {
		return nil, fmt.Errorf("license: wrong audience (want %q)", Audience)
	}
	if claims.IssuedAt == nil {
		return nil, errors.New("license: missing iat claim")
	}
	now := time.Now()
	if claims.IssuedAt.After(now.Add(clockSkewLeeway)) {
		return nil, fmt.Errorf("license: iat %s is in the future", claims.IssuedAt.Format(time.RFC3339))
	}
	if claims.NotBefore != nil && claims.NotBefore.After(now.Add(clockSkewLeeway)) {
		return nil, fmt.Errorf("license: not-before %s is in the future", claims.NotBefore.Format(time.RFC3339))
	}
	if claims.LicenseID == "" {
		return nil, errors.New("license: missing license_id claim")
	}
	if claims.ExpiresAt == nil {
		return nil, errors.New("license: missing exp claim")
	}
	if claims.GracePeriodDays < 0 {
		return nil, fmt.Errorf("license: negative grace_period_days %d", claims.GracePeriodDays)
	}
	// Upper bound is defensive: a JWT minted with an absurd value (signing
	// mistake, copy-paste from a unit-test fixture, attacker with a stolen
	// signing key) would otherwise make the license effectively irrevocable.
	// 90 days is the operational ceiling — see the constant's docstring for
	// the rationale. Legitimate long-runway cases use `exp`, not grace.
	if claims.GracePeriodDays > maxGracePeriodDays {
		return nil, fmt.Errorf("license: grace_period_days %d exceeds maximum %d", claims.GracePeriodDays, maxGracePeriodDays)
	}

	return &License{
		LicenseID:        claims.LicenseID,
		CustomerID:       claims.Subject,
		ContractID:       claims.ContractID,
		IssuedAt:         claims.IssuedAt.Time,
		ExpiresAt:        claims.ExpiresAt.Time,
		MaxOrganizations: claims.MaxOrganizations,
		Features:         claims.Features,
		GracePeriodDays:  claims.GracePeriodDays,
	}, nil
}

// CheckExpiry classifies a license against time.Now().
//   - StateValid:   exp is in the future
//   - StateInGrace: exp has passed but exp + grace_period_days is in the future
//   - StateExpired: exp + grace_period_days is in the past
func CheckExpiry(l *License) State {
	return checkExpiryAt(l, time.Now())
}

func checkExpiryAt(l *License, now time.Time) State {
	if now.Before(l.ExpiresAt) {
		return StateValid
	}
	hardCutoff := l.ExpiresAt.Add(time.Duration(l.GracePeriodDays) * 24 * time.Hour)
	if now.Before(hardCutoff) {
		return StateInGrace
	}
	return StateExpired
}

// DaysRemaining is the count of whole days from now until exp + grace_period.
// Negative once past hard cutoff. Surfaced via /v1/version (slice 6) and the
// LicenseDaysRemaining gauge (slice 2).
//
// Uses math.Floor on the hours-fraction so a license that crossed hard
// cutoff one hour ago reports -1 (not 0). Plain `int(d / (24*time.Hour))`
// truncates toward zero, which would mask the first 24 hours of expiry from
// the `license_days_remaining < 0` alert.
func (l *License) DaysRemaining() int {
	return daysRemainingAt(l, time.Now())
}

func daysRemainingAt(l *License, now time.Time) int {
	hardCutoff := l.ExpiresAt.Add(time.Duration(l.GracePeriodDays) * 24 * time.Hour)
	hours := hardCutoff.Sub(now).Hours()
	return int(math.Floor(hours / 24))
}

// readLicenseJWT returns the raw JWT string from EnvLicense (preferred) or
// the file at EnvLicensePath / DefaultLicensePath. ErrNoLicense when neither
// source has data.
func readLicenseJWT() (string, error) {
	if raw := os.Getenv(EnvLicense); raw != "" {
		return raw, nil
	}
	path := os.Getenv(EnvLicensePath)
	if path == "" {
		path = DefaultLicensePath
	}
	b, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return "", ErrNoLicense
		}
		return "", fmt.Errorf("license: read %s: %w", path, err)
	}
	raw := string(trimSpace(b))
	if raw == "" {
		return "", ErrNoLicense
	}
	return raw, nil
}

// loadPublicKey returns the parsed *rsa.PublicKey from EnvPubKeyPath when
// set (test/dev override), otherwise from the embedded pubkey.pem.
func loadPublicKey() (*rsa.PublicKey, error) {
	pem := embeddedPubKeyPEM
	if path := os.Getenv(EnvPubKeyPath); path != "" {
		b, err := os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("read %s: %w", path, err)
		}
		pem = b
	}
	pub, err := jwt.ParseRSAPublicKeyFromPEM(pem)
	if err != nil {
		return nil, fmt.Errorf("parse public key: %w", err)
	}
	return pub, nil
}

// claimsHasAudience checks if the audience set on the claims contains the
// expected value. RegisteredClaims.Audience is a slice because aud may be a
// JSON string OR array — license JWTs use a single string but we tolerate
// either shape on parse.
func claimsHasAudience(c *licenseClaims, want string) bool {
	for _, a := range c.Audience {
		if a == want {
			return true
		}
	}
	return false
}

// trimSpace strips leading/trailing whitespace including a trailing newline
// that operators routinely leave in a `cat /etc/axiaops/license.jwt`-style file.
// Equivalent to strings.TrimSpace but on a byte slice — avoids the allocation
// of a string conversion just to trim.
func trimSpace(b []byte) []byte {
	start := 0
	for start < len(b) && isSpace(b[start]) {
		start++
	}
	end := len(b)
	for end > start && isSpace(b[end-1]) {
		end--
	}
	return b[start:end]
}

func isSpace(c byte) bool {
	return c == ' ' || c == '\t' || c == '\r' || c == '\n'
}
