// Command license-issue mints a self-hosted AxiaOps license JWT (Phase B1.6
// slice 7). Operator-side only — never deployed alongside the api or
// ingestion binaries.
//
// The signing key is AxiaOps's offline RS256 private key, located via
// $LICENSE_SIGNING_KEY_PATH. The matching public key is embedded into the
// runtime binary at services/shared/license/pubkey.pem; rotating one without
// the other will cause every customer's binary to refuse to start, so
// rotations are coordinated with a release cycle (see docs/license-issuance.md
// in slice 9).
//
// Usage (typical):
//
//	LICENSE_SIGNING_KEY_PATH=~/secure/axiaops-license.pem \
//	  go run ./services/api/cmd/license-issue \
//	    -customer-id=acme-001 \
//	    -contract-id=MSA-2026-007 \
//	    -days=365 \
//	    -max-organizations=5 \
//	    > /tmp/acme.jwt
//
// The JWT is written to stdout so the operator can redirect to a file. All
// diagnostics go to stderr.
package main

import (
	"crypto/rsa"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/golang-jwt/jwt/v5"

	"axiaops.io/shared/license"
)

// envSigningKey is the path-to-PEM env var the operator sets. Distinct from
// LICENSE_PUBLIC_KEY_PATH (the verifier-side test override) because the
// signing key must NEVER live alongside the runtime binary — keeping the
// names different makes a misconfigured deploy fail loud.
const envSigningKey = "LICENSE_SIGNING_KEY_PATH"

// maxGracePeriodDays mirrors the verifier-side bound (license.go) so the
// CLI catches fat-finger mistakes at issuance time rather than at the
// customer's first boot. 90 days = quarterly cadence; rationale lives on
// the verifier's constant. Kept as a separate constant rather than imported
// to avoid circular concern (CLI would otherwise import "the policy" from
// the verifier and have to track its semantics; the values being identical
// is the test contract — see TestRun_ExcessiveGracePeriodDays in main_test).
const maxGracePeriodDays = 90

// issueParams collects the validated CLI inputs. Validation lives in
// validateParams so the test suite can hit each branch without re-parsing
// flags.
type issueParams struct {
	customerID       string
	contractID       string
	licenseID        string
	days             int
	maxOrganizations int
	features         []string
	gracePeriodDays  int
}

// licenseClaims mirrors services/shared/license.licenseClaims. Field tags are
// the wire contract; the round-trip test in main_test.go signs here and
// verifies via license.Load to catch any drift between the two structs.
//
// We do NOT import the verifier's claim struct because it is unexported, and
// exporting it would invite production code to construct one — only this CLI
// has any business minting claims.
type licenseClaims struct {
	jwt.RegisteredClaims
	LicenseID        string   `json:"license_id"`
	ContractID       string   `json:"contract_id"`
	MaxOrganizations int      `json:"max_organizations"`
	Features         []string `json:"features"`
	GracePeriodDays  int      `json:"grace_period_days"`
}

func main() {
	if err := run(os.Args[1:], os.Stdout, os.Stderr); err != nil {
		fmt.Fprintln(os.Stderr, "license-issue:", err)
		os.Exit(1)
	}
}

// run is the testable entry point. All output goes through the supplied
// writers so tests can capture stdout (the JWT) and stderr (diagnostics)
// without process-level redirection.
func run(args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("license-issue", flag.ContinueOnError)
	fs.SetOutput(stderr)

	var (
		p           issueParams
		featuresCSV string
	)
	fs.StringVar(&p.customerID, "customer-id", "", "Customer identifier; becomes the JWT sub claim. Required.")
	fs.StringVar(&p.contractID, "contract-id", "", "MSA / contract reference, surfaced in /v1/version + audit_log. Required.")
	fs.StringVar(&p.licenseID, "license-id", "", "Stable license identifier. Defaults to lic_<customer-id>_<year>_v1.")
	fs.IntVar(&p.days, "days", 0, "Days from now until exp. Required (> 0).")
	fs.IntVar(&p.maxOrganizations, "max-organizations", 0, "Advisory cap; surfaced via /v1/version + license_state_info but not enforced by the verifier. Required (> 0).")
	fs.StringVar(&featuresCSV, "features", "base", "Comma-separated feature flags. B1.6 ships only \"base\".")
	fs.IntVar(&p.gracePeriodDays, "grace-period-days", 30, "Soft expiry window past exp. >= 0.")

	if err := fs.Parse(args); err != nil {
		return err
	}

	p.features = parseCSV(featuresCSV)
	if err := validateParams(&p, time.Now()); err != nil {
		fs.Usage()
		return err
	}

	privateKey, err := loadPrivateKey()
	if err != nil {
		return err
	}

	now := time.Now()
	token, err := sign(privateKey, &p, now)
	if err != nil {
		return fmt.Errorf("sign: %w", err)
	}

	if _, err := fmt.Fprintln(stdout, token); err != nil {
		return err
	}
	// Confirmation line on stderr — leaves a durable audit trace in the
	// operator's terminal scrollback / CI log without polluting the JWT
	// stream on stdout. Quarterly issuance is rare enough that one log line
	// per call is the right granularity.
	exp := now.Add(time.Duration(p.days) * 24 * time.Hour)
	if _, err := fmt.Fprintf(stderr, "license-issue: issued %s for %s exp %s (%d days)\n",
		p.licenseID, p.customerID, exp.UTC().Format(time.RFC3339), p.days); err != nil {
		return err
	}
	return nil
}

// validateParams enforces required-field rules and fills the licenseID
// default. Takes `now` so tests can pin the auto-generated licenseID year
// without touching the wall clock.
func validateParams(p *issueParams, now time.Time) error {
	if p.customerID == "" {
		return errors.New("--customer-id is required")
	}
	if p.contractID == "" {
		return errors.New("--contract-id is required")
	}
	if p.days <= 0 {
		return errors.New("--days must be > 0")
	}
	if p.maxOrganizations <= 0 {
		return errors.New("--max-organizations must be > 0")
	}
	if p.gracePeriodDays < 0 {
		return errors.New("--grace-period-days must be >= 0")
	}
	// Mirror the verifier's upper bound (90 days). Catches fat-finger /
	// copy-paste-from-test-fixture mistakes at issuance time instead of at
	// the customer's first boot, where the failure looks like a corrupt
	// license. Long-runway extensions use `-days` (longer exp), not grace.
	if p.gracePeriodDays > maxGracePeriodDays {
		return fmt.Errorf("--grace-period-days %d exceeds maximum %d (use --days for longer contract extensions instead)", p.gracePeriodDays, maxGracePeriodDays)
	}
	if len(p.features) == 0 {
		return errors.New("--features must be non-empty (default \"base\")")
	}
	if p.licenseID == "" {
		p.licenseID = fmt.Sprintf("lic_%s_%d_v1", strings.ToLower(p.customerID), now.Year())
	}
	return nil
}

// sign builds and signs the JWT. Pure — does not read env, does not touch
// disk. Issuer / Audience pull from the verifier package so a rename there
// is caught at compile time.
func sign(key *rsa.PrivateKey, p *issueParams, now time.Time) (string, error) {
	exp := now.Add(time.Duration(p.days) * 24 * time.Hour)
	claims := licenseClaims{
		RegisteredClaims: jwt.RegisteredClaims{
			Issuer:    license.Issuer,
			Subject:   p.customerID,
			Audience:  jwt.ClaimStrings{license.Audience},
			IssuedAt:  jwt.NewNumericDate(now),
			ExpiresAt: jwt.NewNumericDate(exp),
		},
		LicenseID:        p.licenseID,
		ContractID:       p.contractID,
		MaxOrganizations: p.maxOrganizations,
		Features:         p.features,
		GracePeriodDays:  p.gracePeriodDays,
	}
	tok := jwt.NewWithClaims(jwt.SigningMethodRS256, claims)
	return tok.SignedString(key)
}

// minRSABits is the smallest RSA modulus we'll sign with. RS256 is
// cryptographically weak below 2048 (NIST SP 800-57 has 2048 as the floor for
// new keys through 2030). `openssl genrsa 1024` is still a valid command, so
// the floor is enforced explicitly rather than left to operator discipline.
const minRSABits = 2048

// loadPrivateKey reads the PEM at $LICENSE_SIGNING_KEY_PATH, refuses
// world/group-readable files, and refuses RSA keys below minRSABits.
//
// There is no default path — there is no safe default for a private signing
// key, and a default would invite a stray default-named file to be picked
// up silently.
func loadPrivateKey() (*rsa.PrivateKey, error) {
	path := os.Getenv(envSigningKey)
	if path == "" {
		return nil, fmt.Errorf("%s is not set — point it at AxiaOps's offline RS256 private key (PEM)", envSigningKey)
	}
	info, err := os.Stat(path)
	if err != nil {
		return nil, fmt.Errorf("stat %s: %w", path, err)
	}
	// File-mode check: refuse anything readable by group or world. Operators
	// who `cp` the key off a USB stick onto a fresh laptop occasionally end
	// up with 0644 — failing loud here turns a quiet leak into a one-line
	// error that points at the fix.
	if mode := info.Mode().Perm(); mode&0o077 != 0 {
		return nil, fmt.Errorf("%s is readable by group or world (mode %04o); chmod 600 first", path, mode)
	}
	pemBytes, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	key, err := jwt.ParseRSAPrivateKeyFromPEM(pemBytes)
	if err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	if bits := key.N.BitLen(); bits < minRSABits {
		return nil, fmt.Errorf("%s: RSA key is %d bits; minimum %d required", path, bits, minRSABits)
	}
	return key, nil
}

// parseCSV splits the --features value, trimming whitespace and dropping
// empties. "base" → ["base"]; "base, premium" → ["base", "premium"];
// "" → nil (caught by validateParams).
func parseCSV(s string) []string {
	if s == "" {
		return nil
	}
	parts := strings.Split(s, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}
