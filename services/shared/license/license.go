// Package license verifies the AxiaOps self-hosted license JWT at startup.
//
// Per plan §4.9 the verification is purely offline: the binary trusts an
// embedded RS256 public key, the customer ships a JWT signed by AxiaOps's
// matching offline private key, and the binary refuses to start once that
// JWT is past its grace window. There is no license server and no phone-home.
//
// SaaS deployments do not use this package — the SaaS composition root
// gates access via Stripe instead. Self-hosted main.go calls Load + CheckExpiry
// before serverbuild.ComposeServer. Post B1.7 layer 4 (issue #75), DEV_MODE
// loads an embedded 100-year dev fixture through the same Load chain a
// real customer license travels — dev exercises CheckExpiry, the runtime
// ticker, the scan-gate predicates, and the /v1/version handler the same
// way a customer install does.
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
// SaaS / pre-license-ceremony / production-without-license all surface as
// StateNotLoaded and the policy lives at the call site, not buried in this
// accessor. Post B1.7 layer 4, DEV_MODE no longer surfaces here — the dev
// fixture loads through Snapshot() with state=Valid like a real license.
type State int

const (
	StateValid State = iota
	StateInGrace
	StateExpired
	StateNotLoaded
)

// AllStates lists every State the iota currently defines, in order. Used by
// downstream tests (services/api/internal/api/scan_gate_body_test.go) to
// assert exhaustive coverage — adding a new state to the iota requires
// adding it here too, which forces the dependent tests to fail until they
// account for the new value. Without this seam, tests that synthesise
// "unknown" via a high-integer cast (e.g. `State(99)`) silently keep
// passing when a real new state is added at the next iota slot.
var AllStates = []State{StateValid, StateInGrace, StateExpired, StateNotLoaded}

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
// In dev builds Load also accepts a JWT signed by the embedded dev key
// (B1.7 layer 4 / issue #75) — the production pubkey is tried first, and
// only on a signature-invalid error does the dev key get a turn. Customer-
// shipping (`-tags production`) binaries embed `nil` for the dev key, so
// the fallback branch is unreachable structurally and a leaked dev fixture
// cannot authenticate against a customer install.
//
// The function is a pure read — it does not cache, does not log, and does
// not mutate global state. Callers re-invoke on restart.
func Load() (*License, error) {
	raw, err := readLicenseJWT()
	if err != nil {
		return nil, err
	}
	return loadFromJWT(raw)
}

// loadFromJWT verifies a raw JWT against the available public keys and
// returns the parsed License. Factored out so VerifyAtBoot can pass the
// embedded dev fixture in directly (B1.7 layer 4) without round-tripping
// through env vars or temp files.
func loadFromJWT(raw string) (*License, error) {
	prodPub, err := loadProductionPublicKey()
	if err != nil {
		return nil, fmt.Errorf("license: load public key: %w", err)
	}

	claims, sigErr := verifyJWT(raw, prodPub)
	if sigErr != nil &&
		errors.Is(sigErr, jwt.ErrTokenSignatureInvalid) &&
		!errors.Is(sigErr, jwt.ErrTokenUnverifiable) &&
		len(devEmbeddedPubKeyPEM) > 0 {
		// Dev-key fallback (B1.7 layer 4). Only attempted when the prod
		// pubkey rejected the *signature* AND we actually have a dev pubkey
		// compiled in — production-tagged builds zero this seam out so
		// the fallback is unreachable. The `!ErrTokenUnverifiable` guard
		// narrows away the alg-confusion path: golang-jwt v5 wraps
		// `WithValidMethods` rejections with both `ErrTokenSignatureInvalid`
		// and `ErrTokenUnverifiable`, but those inputs (alg=none, alg=HS256)
		// cannot validate against any RSA pubkey so the fallback attempt is
		// wasted CPU. golang-jwt also wraps keyfunc-returned errors with
		// `ErrTokenUnverifiable` (not `ErrTokenSignatureInvalid`), so the
		// keyfunc-path alg guard naturally falls through `else if sigErr != nil`
		// without retrying. Any other error (parse failure, malformed claims)
		// is fatal — those are not signed-by-the-other-key problems and
		// re-trying buys nothing.
		devPub, devErr := jwt.ParseRSAPublicKeyFromPEM(devEmbeddedPubKeyPEM)
		if devErr != nil {
			// Embedded dev key failed to parse — package-level bug,
			// surface the original prod-key signature error so the
			// operator-facing message stays consistent.
			return nil, fmt.Errorf("license: verify: %w", sigErr)
		}
		var retryErr error
		claims, retryErr = verifyJWT(raw, devPub)
		if retryErr != nil {
			// Neither key verified — return the prod-key error since
			// it's the one operators should see for a real-license
			// signature problem.
			return nil, fmt.Errorf("license: verify: %w", sigErr)
		}
	} else if sigErr != nil {
		return nil, fmt.Errorf("license: verify: %w", sigErr)
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

// loadProductionPublicKey returns the parsed *rsa.PublicKey from
// EnvPubKeyPath when set (test override) or from the embedded pubkey.pem
// otherwise. The dev pubkey (devEmbeddedPubKeyPEM, defined in
// embed_dev.go / embed_production.go) is consulted separately by Load on
// signature-failure fallback — it is not merged with this key.
func loadProductionPublicKey() (*rsa.PublicKey, error) {
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

// verifyJWT runs the RS256 + alg-confusion-guard parse against a single
// public key and returns the decoded claims. The wraps-jwt.ErrTokenSignatureInvalid
// shape is what Load() inspects to decide whether to try a dev-key fallback.
// Callers must never reorder the parser options — WithValidMethods is the
// alg-confusion gate, WithoutClaimsValidation defers exp checking to
// CheckExpiry (per the in-grace handling design).
func verifyJWT(raw string, pub *rsa.PublicKey) (*licenseClaims, error) {
	claims := &licenseClaims{}
	parser := jwt.NewParser(
		jwt.WithValidMethods([]string{jwt.SigningMethodRS256.Name}),
		jwt.WithoutClaimsValidation(),
	)
	if _, err := parser.ParseWithClaims(raw, claims, func(t *jwt.Token) (any, error) {
		if _, ok := t.Method.(*jwt.SigningMethodRSA); !ok {
			return nil, fmt.Errorf("unexpected signing method %q", t.Method.Alg())
		}
		return pub, nil
	}); err != nil {
		return nil, err
	}
	return claims, nil
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
