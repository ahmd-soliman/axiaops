package license_test

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"

	"axiaops.io/shared/license"
)

// fixtureKeys holds the test signing keypair shared across tests in this
// file. Each test points the package at the matching public key via the
// LICENSE_PUBLIC_KEY_PATH override (t.Setenv); the production embedded
// pubkey.pem is never used in this suite.
type fixtureKeys struct {
	priv       *rsa.PrivateKey
	pubKeyPath string
}

func setupKeys(t *testing.T) *fixtureKeys {
	t.Helper()
	priv, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	pubBytes, err := x509.MarshalPKIXPublicKey(&priv.PublicKey)
	if err != nil {
		t.Fatalf("marshal pub: %v", err)
	}
	pubPEM := pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: pubBytes})
	dir := t.TempDir()
	pubPath := filepath.Join(dir, "pub.pem")
	if err := os.WriteFile(pubPath, pubPEM, 0o600); err != nil {
		t.Fatalf("write pub: %v", err)
	}
	return &fixtureKeys{priv: priv, pubKeyPath: pubPath}
}

// signLicense mints a JWT with the given claim overrides applied on top of
// a known-valid baseline. Tests pass a mutator to express the case under test
// (e.g. push exp into the past) instead of repeating the full claim block.
func signLicense(t *testing.T, k *fixtureKeys, mutate func(c *jwt.MapClaims), method jwt.SigningMethod, signKey any) string {
	t.Helper()
	now := time.Now()
	claims := jwt.MapClaims{
		"iss":               license.Issuer,
		"aud":               license.Audience,
		"sub":               "acme-001",
		"iat":               now.Add(-1 * time.Hour).Unix(),
		"exp":               now.Add(365 * 24 * time.Hour).Unix(),
		"license_id":        "lic_acme_2026_v1",
		"contract_id":       "MSA-2026-007",
		"max_organizations": 5,
		"features":          []string{"base"},
		"grace_period_days": 30,
	}
	if mutate != nil {
		mutate(&claims)
	}
	if method == nil {
		method = jwt.SigningMethodRS256
	}
	if signKey == nil {
		signKey = k.priv
	}
	tok := jwt.NewWithClaims(method, claims)
	raw, err := tok.SignedString(signKey)
	if err != nil {
		t.Fatalf("sign: %v", err)
	}
	return raw
}

// installLicense wires the env vars Load reads. Each test calls this once
// after constructing its JWT.
func installLicense(t *testing.T, k *fixtureKeys, raw string) {
	t.Helper()
	t.Setenv(license.EnvLicense, raw)
	t.Setenv(license.EnvLicensePath, "")
	t.Setenv(license.EnvPubKeyPath, k.pubKeyPath)
}

func TestLoad_Valid(t *testing.T) {
	k := setupKeys(t)
	raw := signLicense(t, k, nil, nil, nil)
	installLicense(t, k, raw)

	lic, err := license.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if lic.LicenseID != "lic_acme_2026_v1" {
		t.Errorf("LicenseID = %q, want lic_acme_2026_v1", lic.LicenseID)
	}
	if lic.CustomerID != "acme-001" {
		t.Errorf("CustomerID = %q, want acme-001", lic.CustomerID)
	}
	if lic.MaxOrganizations != 5 {
		t.Errorf("MaxOrganizations = %d, want 5", lic.MaxOrganizations)
	}
	if lic.GracePeriodDays != 30 {
		t.Errorf("GracePeriodDays = %d, want 30", lic.GracePeriodDays)
	}
	if state := license.CheckExpiry(lic); state != license.StateValid {
		t.Errorf("CheckExpiry = %v, want StateValid", state)
	}
}

func TestLoad_LicenseFromFile(t *testing.T) {
	k := setupKeys(t)
	raw := signLicense(t, k, nil, nil, nil)

	dir := t.TempDir()
	path := filepath.Join(dir, "license.jwt")
	if err := os.WriteFile(path, []byte(raw+"\n"), 0o600); err != nil {
		t.Fatalf("write license: %v", err)
	}
	t.Setenv(license.EnvLicense, "")
	t.Setenv(license.EnvLicensePath, path)
	t.Setenv(license.EnvPubKeyPath, k.pubKeyPath)

	if _, err := license.Load(); err != nil {
		t.Fatalf("Load (from file): %v", err)
	}
}

func TestLoad_ExpiredWithinGrace(t *testing.T) {
	k := setupKeys(t)
	now := time.Now()
	raw := signLicense(t, k, func(c *jwt.MapClaims) {
		(*c)["exp"] = now.Add(-5 * 24 * time.Hour).Unix() // 5 days ago
		(*c)["grace_period_days"] = 30                    // 25 days remaining of grace
	}, nil, nil)
	installLicense(t, k, raw)

	lic, err := license.Load()
	if err != nil {
		t.Fatalf("Load (in grace): %v", err)
	}
	if state := license.CheckExpiry(lic); state != license.StateInGrace {
		t.Errorf("CheckExpiry = %v, want StateInGrace", state)
	}
	if dr := lic.DaysRemaining(); dr < 0 || dr > 30 {
		t.Errorf("DaysRemaining = %d, want in (0, 30]", dr)
	}
}

func TestLoad_ExpiredPastGrace(t *testing.T) {
	k := setupKeys(t)
	now := time.Now()
	raw := signLicense(t, k, func(c *jwt.MapClaims) {
		(*c)["exp"] = now.Add(-60 * 24 * time.Hour).Unix() // 60 days ago
		(*c)["grace_period_days"] = 30                     // grace ended 30 days ago
	}, nil, nil)
	installLicense(t, k, raw)

	// Load itself succeeds — verification covers signature + claims, not
	// expiry-vs-grace classification (that is CheckExpiry's job so callers
	// can distinguish "valid but expired in grace" from "hard fail").
	lic, err := license.Load()
	if err != nil {
		t.Fatalf("Load (past grace): %v", err)
	}
	if state := license.CheckExpiry(lic); state != license.StateExpired {
		t.Errorf("CheckExpiry = %v, want StateExpired", state)
	}
	if dr := lic.DaysRemaining(); dr >= 0 {
		t.Errorf("DaysRemaining = %d, want negative", dr)
	}
}

func TestLoad_Missing(t *testing.T) {
	t.Setenv(license.EnvLicense, "")
	// Point EnvLicensePath at a definitely-nonexistent file so the default
	// /etc/axiaops/license.jwt isn't accidentally read on a CI host that
	// happens to have one. EnvPubKeyPath is irrelevant — Load short-circuits
	// before key loading.
	t.Setenv(license.EnvLicensePath, filepath.Join(t.TempDir(), "no-such-license.jwt"))

	_, err := license.Load()
	if !errors.Is(err, license.ErrNoLicense) {
		t.Fatalf("Load = %v, want ErrNoLicense", err)
	}
}

func TestLoad_TamperedSignature(t *testing.T) {
	k := setupKeys(t)
	raw := signLicense(t, k, nil, nil, nil)
	// Flip the last byte of the signature segment. JWT format: header.body.sig
	parts := strings.Split(raw, ".")
	if len(parts) != 3 {
		t.Fatalf("unexpected JWT shape: %d segments", len(parts))
	}
	sig := parts[2]
	// Flip a base64url char in the middle of the signature, not the trailing
	// position — the last few bits can be ignored as base64 padding and a
	// flip there may decode to identical bytes.
	mid := len(sig) / 2
	flipped := sig[:mid] + flipChar(sig[mid]) + sig[mid+1:]
	tampered := parts[0] + "." + parts[1] + "." + flipped
	installLicense(t, k, tampered)

	if _, err := license.Load(); err == nil {
		t.Fatal("Load(tampered) succeeded; want signature error")
	}
}

func TestLoad_AlgNone(t *testing.T) {
	k := setupKeys(t)
	// jwt.SigningMethodNone needs a special key sentinel — UnsafeAllowNoneSignatureType.
	raw := signLicense(t, k, nil, jwt.SigningMethodNone, jwt.UnsafeAllowNoneSignatureType)
	installLicense(t, k, raw)

	if _, err := license.Load(); err == nil {
		t.Fatal("Load(alg=none) succeeded; want method-rejection error")
	}
}

func TestLoad_AlgHS256(t *testing.T) {
	k := setupKeys(t)
	// The classic alg-confusion attack: sign HS256 with the *public* key bytes
	// as the HMAC secret, hoping the verifier accepts HS256 + the same pubkey.
	pubBytes, err := x509.MarshalPKIXPublicKey(&k.priv.PublicKey)
	if err != nil {
		t.Fatalf("marshal pub: %v", err)
	}
	raw := signLicense(t, k, nil, jwt.SigningMethodHS256, pubBytes)
	installLicense(t, k, raw)

	if _, err := license.Load(); err == nil {
		t.Fatal("Load(alg=HS256) succeeded; want method-rejection error")
	}
}

func TestLoad_WrongIssuer(t *testing.T) {
	k := setupKeys(t)
	raw := signLicense(t, k, func(c *jwt.MapClaims) {
		(*c)["iss"] = "https://evil.example.com/licenses"
	}, nil, nil)
	installLicense(t, k, raw)

	if _, err := license.Load(); err == nil {
		t.Fatal("Load(wrong iss) succeeded; want issuer-rejection error")
	}
}

func TestLoad_WrongAudience(t *testing.T) {
	k := setupKeys(t)
	raw := signLicense(t, k, func(c *jwt.MapClaims) {
		(*c)["aud"] = "axiaops-wrong-aud"
	}, nil, nil)
	installLicense(t, k, raw)

	if _, err := license.Load(); err == nil {
		t.Fatal("Load(wrong aud) succeeded; want audience-rejection error")
	}
}

func TestLoad_FutureIssuedAt(t *testing.T) {
	k := setupKeys(t)
	raw := signLicense(t, k, func(c *jwt.MapClaims) {
		(*c)["iat"] = time.Now().Add(48 * time.Hour).Unix()
	}, nil, nil)
	installLicense(t, k, raw)

	if _, err := license.Load(); err == nil {
		t.Fatal("Load(future iat) succeeded; want iat-rejection error")
	}
}

func TestLoad_FutureNotBefore(t *testing.T) {
	k := setupKeys(t)
	raw := signLicense(t, k, func(c *jwt.MapClaims) {
		(*c)["nbf"] = time.Now().Add(48 * time.Hour).Unix()
	}, nil, nil)
	installLicense(t, k, raw)

	if _, err := license.Load(); err == nil {
		t.Fatal("Load(future nbf) succeeded; want nbf-rejection error")
	}
}

func TestLoad_IssuedAtWithinSkewLeeway(t *testing.T) {
	// A license whose iat is a few seconds in the future (target host's NTP
	// drift) must still be accepted — see the clockSkewLeeway constant.
	k := setupKeys(t)
	raw := signLicense(t, k, func(c *jwt.MapClaims) {
		(*c)["iat"] = time.Now().Add(5 * time.Second).Unix()
	}, nil, nil)
	installLicense(t, k, raw)

	if _, err := license.Load(); err != nil {
		t.Fatalf("Load(iat 5s in future) = %v, want success within leeway", err)
	}
}

func TestLoad_MissingLicenseID(t *testing.T) {
	k := setupKeys(t)
	raw := signLicense(t, k, func(c *jwt.MapClaims) {
		delete(*c, "license_id")
	}, nil, nil)
	installLicense(t, k, raw)

	if _, err := license.Load(); err == nil {
		t.Fatal("Load(no license_id) succeeded; want missing-claim error")
	}
}

// TestLoad_NegativeGracePeriod and TestLoad_ExcessiveGracePeriod guard the
// grace_period_days bounds. The upper bound (90 days) was added per MR !71
// holistic review — without it a JWT with grace_period_days: 36500 would
// make the license effectively irrevocable. 90 = quarterly rotation cadence
// aligned with high-value-credential lifetime guidance.
func TestLoad_NegativeGracePeriod(t *testing.T) {
	k := setupKeys(t)
	raw := signLicense(t, k, func(c *jwt.MapClaims) {
		(*c)["grace_period_days"] = -1
	}, nil, nil)
	installLicense(t, k, raw)

	if _, err := license.Load(); err == nil {
		t.Fatal("Load(grace=-1) succeeded; want negative-grace error")
	}
}

func TestLoad_ExcessiveGracePeriod(t *testing.T) {
	k := setupKeys(t)
	raw := signLicense(t, k, func(c *jwt.MapClaims) {
		(*c)["grace_period_days"] = 91 // one day past the cap
	}, nil, nil)
	installLicense(t, k, raw)

	_, err := license.Load()
	if err == nil {
		t.Fatal("Load(grace=91) succeeded; want maximum-exceeded error")
	}
	if !strings.Contains(err.Error(), "grace_period_days") {
		t.Errorf("error %q does not name the offending claim", err)
	}
}

// TestLoad_GracePeriodAtMaximum is the boundary case — exactly at the cap is
// accepted. Catches an off-by-one regression in the > vs >= comparison.
func TestLoad_GracePeriodAtMaximum(t *testing.T) {
	k := setupKeys(t)
	raw := signLicense(t, k, func(c *jwt.MapClaims) {
		(*c)["grace_period_days"] = 90
	}, nil, nil)
	installLicense(t, k, raw)

	lic, err := license.Load()
	if err != nil {
		t.Fatalf("Load(grace=90) failed; should accept the maximum: %v", err)
	}
	if lic.GracePeriodDays != 90 {
		t.Errorf("GracePeriodDays = %d, want 90", lic.GracePeriodDays)
	}
}

func TestCheckExpiry_BoundaryAtExp(t *testing.T) {
	now := time.Now()
	lic := &license.License{
		ExpiresAt:       now.Add(-1 * time.Second), // just expired
		GracePeriodDays: 1,
	}
	if state := license.CheckExpiry(lic); state != license.StateInGrace {
		t.Errorf("just-after-exp CheckExpiry = %v, want StateInGrace", state)
	}
}

// TestDaysRemaining_NegativeWithinFirstDayPastHardCutoff guards against the
// integer-truncation bug where `int(d / 24h)` returned 0 for the first 24h
// past hard cutoff (since negative durations truncate toward zero). The
// `license_days_remaining < 0` Prometheus alert relies on this returning
// -1 immediately after the hard-cutoff boundary, not 24h later.
func TestDaysRemaining_NegativeWithinFirstDayPastHardCutoff(t *testing.T) {
	now := time.Now()
	// Hard cutoff = exp + grace = (now - 31d) + 30d = now - 24h.
	lic := &license.License{
		ExpiresAt:       now.Add(-31 * 24 * time.Hour),
		GracePeriodDays: 30,
	}
	if dr := lic.DaysRemaining(); dr >= 0 {
		t.Errorf("DaysRemaining = %d, want negative — math.Floor regression in daysRemainingAt", dr)
	}
}

// flipChar returns a JWT-base64url char that is guaranteed different from c.
// Used to corrupt a signature byte without producing an invalid base64 char.
func flipChar(c byte) string {
	if c == 'A' {
		return "B"
	}
	return "A"
}
