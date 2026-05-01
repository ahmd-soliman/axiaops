package main

import (
	"bytes"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"axiaops.io/shared/license"
)

// keypair installs a freshly-generated RSA keypair: the private PEM at
// $LICENSE_SIGNING_KEY_PATH (CLI input), the matching public PEM at
// $LICENSE_PUBLIC_KEY_PATH (verifier-side override). Returned for tests
// that need direct access to the *rsa.PrivateKey for sign().
func keypair(t *testing.T) *rsa.PrivateKey {
	t.Helper()
	priv, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	dir := t.TempDir()

	privBytes, err := x509.MarshalPKCS8PrivateKey(priv)
	if err != nil {
		t.Fatalf("marshal priv: %v", err)
	}
	privPEM := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: privBytes})
	privPath := filepath.Join(dir, "priv.pem")
	if err := os.WriteFile(privPath, privPEM, 0o600); err != nil {
		t.Fatalf("write priv: %v", err)
	}

	pubBytes, err := x509.MarshalPKIXPublicKey(&priv.PublicKey)
	if err != nil {
		t.Fatalf("marshal pub: %v", err)
	}
	pubPEM := pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: pubBytes})
	pubPath := filepath.Join(dir, "pub.pem")
	if err := os.WriteFile(pubPath, pubPEM, 0o600); err != nil {
		t.Fatalf("write pub: %v", err)
	}

	t.Setenv(envSigningKey, privPath)
	t.Setenv(license.EnvPubKeyPath, pubPath)
	return priv
}

// TestRun_RoundTrip is the primary acceptance test: a JWT minted by this CLI
// must be accepted by services/shared/license.Load and produce the right
// claim values. If this test breaks, either the issuer or the verifier
// drifted out of sync with the wire shape.
func TestRun_RoundTrip(t *testing.T) {
	keypair(t)

	var stdout, stderr bytes.Buffer
	args := []string{
		"-customer-id=acme-001",
		"-contract-id=MSA-2026-007",
		"-days=365",
		"-max-organizations=5",
	}
	if err := run(args, &stdout, &stderr); err != nil {
		t.Fatalf("run: %v — stderr: %s", err, stderr.String())
	}

	jwtStr := strings.TrimSpace(stdout.String())
	if jwtStr == "" {
		t.Fatal("no JWT on stdout")
	}

	t.Setenv(license.EnvLicense, jwtStr)
	t.Setenv(license.EnvLicensePath, "")

	lic, err := license.Load()
	if err != nil {
		t.Fatalf("license.Load rejected freshly-issued JWT: %v", err)
	}

	if lic.CustomerID != "acme-001" {
		t.Errorf("customer_id: got %q, want acme-001", lic.CustomerID)
	}
	if lic.ContractID != "MSA-2026-007" {
		t.Errorf("contract_id: got %q, want MSA-2026-007", lic.ContractID)
	}
	if lic.MaxOrganizations != 5 {
		t.Errorf("max_organizations: got %d, want 5", lic.MaxOrganizations)
	}
	if lic.GracePeriodDays != 30 {
		t.Errorf("grace_period_days: got %d, want 30 (default)", lic.GracePeriodDays)
	}
	if len(lic.Features) != 1 || lic.Features[0] != "base" {
		t.Errorf("features: got %v, want [base]", lic.Features)
	}
	if !strings.HasPrefix(lic.LicenseID, "lic_acme-001_") {
		t.Errorf("license_id: got %q, want auto-derived lic_acme-001_<year>_v1", lic.LicenseID)
	}
	// exp should be ~365 days out. Allow ±2 days for clock crossings.
	wantExp := time.Now().Add(365 * 24 * time.Hour)
	if delta := lic.ExpiresAt.Sub(wantExp).Abs(); delta > 2*24*time.Hour {
		t.Errorf("expires_at delta: got %v, want < 2 days", delta)
	}
	if license.CheckExpiry(lic) != license.StateValid {
		t.Errorf("CheckExpiry: got %v, want valid", license.CheckExpiry(lic))
	}
}

func TestRun_HonoursExplicitLicenseID(t *testing.T) {
	keypair(t)

	var stdout, stderr bytes.Buffer
	args := []string{
		"-customer-id=acme-001",
		"-contract-id=MSA-2026-007",
		"-days=30",
		"-max-organizations=1",
		"-license-id=lic_explicit_override",
	}
	if err := run(args, &stdout, &stderr); err != nil {
		t.Fatalf("run: %v — stderr: %s", err, stderr.String())
	}

	t.Setenv(license.EnvLicense, strings.TrimSpace(stdout.String()))
	t.Setenv(license.EnvLicensePath, "")
	lic, err := license.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if lic.LicenseID != "lic_explicit_override" {
		t.Errorf("license_id: got %q, want lic_explicit_override", lic.LicenseID)
	}
}

func TestRun_HonoursMultipleFeatures(t *testing.T) {
	keypair(t)

	var stdout, stderr bytes.Buffer
	args := []string{
		"-customer-id=acme-001",
		"-contract-id=MSA-2026-007",
		"-days=30",
		"-max-organizations=1",
		"-features=base, premium ,enterprise",
	}
	if err := run(args, &stdout, &stderr); err != nil {
		t.Fatalf("run: %v", err)
	}

	t.Setenv(license.EnvLicense, strings.TrimSpace(stdout.String()))
	t.Setenv(license.EnvLicensePath, "")
	lic, err := license.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	want := []string{"base", "premium", "enterprise"}
	if len(lic.Features) != len(want) {
		t.Fatalf("features len: got %v, want %v", lic.Features, want)
	}
	for i, f := range want {
		if lic.Features[i] != f {
			t.Errorf("features[%d]: got %q, want %q", i, lic.Features[i], f)
		}
	}
}

func TestRun_MissingSigningKeyEnv(t *testing.T) {
	// Public key still set so a missing signing key is the only failure mode.
	t.Setenv(envSigningKey, "")

	var stdout, stderr bytes.Buffer
	args := []string{
		"-customer-id=acme-001",
		"-contract-id=MSA-2026-007",
		"-days=30",
		"-max-organizations=1",
	}
	err := run(args, &stdout, &stderr)
	if err == nil {
		t.Fatal("expected error when LICENSE_SIGNING_KEY_PATH is unset")
	}
	if !strings.Contains(err.Error(), envSigningKey) {
		t.Errorf("error should name the env var; got: %v", err)
	}
	if stdout.Len() != 0 {
		t.Errorf("expected no JWT on stdout when signing fails, got: %q", stdout.String())
	}
}

// TestRun_RejectsWorldReadableKey covers the file-mode guard in
// loadPrivateKey. A 0644 signing key is the most common operator misstep
// (cp from USB / scp without --preserve=mode); refusing to sign with a
// pointed error message turns a quiet leak into a fix-it-now nag.
func TestRun_RejectsWorldReadableKey(t *testing.T) {
	priv := keypair(t)
	// keypair wrote the priv at 0600 already — re-write it 0644 so this
	// test exercises the guard without needing a separate keypair.
	privBytes, err := x509.MarshalPKCS8PrivateKey(priv)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	privPEM := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: privBytes})
	leakyPath := filepath.Join(t.TempDir(), "leaky.pem")
	if err := os.WriteFile(leakyPath, privPEM, 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	t.Setenv(envSigningKey, leakyPath)

	var stdout, stderr bytes.Buffer
	args := []string{
		"-customer-id=acme-001",
		"-contract-id=MSA-2026-007",
		"-days=30",
		"-max-organizations=1",
	}
	err = run(args, &stdout, &stderr)
	if err == nil {
		t.Fatal("expected error on world-readable signing key")
	}
	if !strings.Contains(err.Error(), "chmod 600") {
		t.Errorf("error should hint at the fix; got: %v", err)
	}
	if stdout.Len() != 0 {
		t.Errorf("expected no JWT on stdout, got: %q", stdout.String())
	}
}

// TestRun_RejectsWeakKey covers the bit-size guard. A 1024-bit RSA key
// signs fine through jwt.ParseRSAPrivateKeyFromPEM, so the floor is
// enforced explicitly — without this guard, a stray test key from a dev
// machine could mint a production-looking license that's trivially
// forgeable.
func TestRun_RejectsWeakKey(t *testing.T) {
	weak, err := rsa.GenerateKey(rand.Reader, 1024)
	if err != nil {
		t.Fatalf("generate weak key: %v", err)
	}
	weakBytes, err := x509.MarshalPKCS8PrivateKey(weak)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	weakPEM := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: weakBytes})
	weakPath := filepath.Join(t.TempDir(), "weak.pem")
	if err := os.WriteFile(weakPath, weakPEM, 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	t.Setenv(envSigningKey, weakPath)

	var stdout, stderr bytes.Buffer
	args := []string{
		"-customer-id=acme-001",
		"-contract-id=MSA-2026-007",
		"-days=30",
		"-max-organizations=1",
	}
	err = run(args, &stdout, &stderr)
	if err == nil {
		t.Fatal("expected error on 1024-bit RSA key")
	}
	if !strings.Contains(err.Error(), "1024 bits") {
		t.Errorf("error should mention key size; got: %v", err)
	}
}

// TestRun_StderrConfirmation verifies the audit-trail line on stderr.
// Operators rely on this for terminal scrollback when issuing licenses
// quarterly; a regression that drops the line would silently degrade the
// audit posture without any user-visible failure.
func TestRun_StderrConfirmation(t *testing.T) {
	keypair(t)

	var stdout, stderr bytes.Buffer
	args := []string{
		"-customer-id=acme-001",
		"-contract-id=MSA-2026-007",
		"-days=365",
		"-max-organizations=5",
		"-license-id=lic_explicit",
	}
	if err := run(args, &stdout, &stderr); err != nil {
		t.Fatalf("run: %v", err)
	}
	got := stderr.String()
	for _, want := range []string{"lic_explicit", "acme-001", "365 days"} {
		if !strings.Contains(got, want) {
			t.Errorf("stderr confirmation missing %q; got: %s", want, got)
		}
	}
}

func TestRun_BadSigningKeyPEM(t *testing.T) {
	dir := t.TempDir()
	bad := filepath.Join(dir, "not-a-pem.txt")
	if err := os.WriteFile(bad, []byte("this is definitely not a PEM"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	t.Setenv(envSigningKey, bad)

	var stdout, stderr bytes.Buffer
	args := []string{
		"-customer-id=acme-001",
		"-contract-id=MSA-2026-007",
		"-days=30",
		"-max-organizations=1",
	}
	err := run(args, &stdout, &stderr)
	if err == nil {
		t.Fatal("expected error on bad PEM")
	}
}

func TestValidateParams(t *testing.T) {
	now := time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC)

	cases := []struct {
		name    string
		mutate  func(*issueParams)
		wantErr string // substring; "" means valid
		wantID  string // expected licenseID after defaulting; "" skips check
	}{
		{
			name:   "valid with auto license_id",
			mutate: func(p *issueParams) {},
			wantID: "lic_acme-001_2026_v1",
		},
		{
			name: "explicit license_id is preserved",
			mutate: func(p *issueParams) {
				p.licenseID = "lic_custom"
			},
			wantID: "lic_custom",
		},
		{
			name: "license_id derivation lowercases customer_id",
			mutate: func(p *issueParams) {
				p.customerID = "ACME-001"
			},
			wantID: "lic_acme-001_2026_v1",
		},
		{
			name: "missing customer-id",
			mutate: func(p *issueParams) {
				p.customerID = ""
			},
			wantErr: "customer-id",
		},
		{
			name: "missing contract-id",
			mutate: func(p *issueParams) {
				p.contractID = ""
			},
			wantErr: "contract-id",
		},
		{
			name: "zero days",
			mutate: func(p *issueParams) {
				p.days = 0
			},
			wantErr: "days",
		},
		{
			name: "negative days",
			mutate: func(p *issueParams) {
				p.days = -1
			},
			wantErr: "days",
		},
		{
			name: "zero max-organizations",
			mutate: func(p *issueParams) {
				p.maxOrganizations = 0
			},
			wantErr: "max-organizations",
		},
		{
			name: "negative grace",
			mutate: func(p *issueParams) {
				p.gracePeriodDays = -1
			},
			wantErr: "grace-period-days",
		},
		{
			name: "excessive grace caught at issuance",
			mutate: func(p *issueParams) {
				p.gracePeriodDays = 3651 // 10 years + 1 day
			},
			wantErr: "grace-period-days",
		},
		{
			name: "grace at maximum is accepted",
			mutate: func(p *issueParams) {
				p.gracePeriodDays = 3650
			},
			wantID: "lic_acme-001_2026_v1",
		},
		{
			name: "empty features rejected",
			mutate: func(p *issueParams) {
				p.features = nil
			},
			wantErr: "features",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p := issueParams{
				customerID:       "acme-001",
				contractID:       "MSA-2026-007",
				days:             365,
				maxOrganizations: 5,
				features:         []string{"base"},
				gracePeriodDays:  30,
			}
			tc.mutate(&p)

			err := validateParams(&p, now)
			if tc.wantErr != "" {
				if err == nil {
					t.Fatalf("expected error containing %q, got nil", tc.wantErr)
				}
				if !strings.Contains(err.Error(), tc.wantErr) {
					t.Errorf("error %q does not mention %q", err, tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if tc.wantID != "" && p.licenseID != tc.wantID {
				t.Errorf("license_id: got %q, want %q", p.licenseID, tc.wantID)
			}
		})
	}
}

func TestParseCSV(t *testing.T) {
	cases := []struct {
		in   string
		want []string
	}{
		{"", nil},
		{"base", []string{"base"}},
		{"base,premium", []string{"base", "premium"}},
		{" base , premium ", []string{"base", "premium"}},
		{"base,,premium", []string{"base", "premium"}},
		{",", nil},
	}
	for _, tc := range cases {
		t.Run(tc.in, func(t *testing.T) {
			got := parseCSV(tc.in)
			if len(got) != len(tc.want) {
				t.Fatalf("len: got %v, want %v", got, tc.want)
			}
			for i := range got {
				if got[i] != tc.want[i] {
					t.Errorf("[%d]: got %q, want %q", i, got[i], tc.want[i])
				}
			}
		})
	}
}

// flagParseError is a sanity check that flag.ContinueOnError surfaces parse
// errors as Go errors rather than os.Exit, so tests can assert on them
// without subprocess gymnastics.
func TestRun_UnknownFlag(t *testing.T) {
	keypair(t)

	var stdout, stderr bytes.Buffer
	args := []string{"-not-a-real-flag"}
	err := run(args, &stdout, &stderr)
	if err == nil {
		t.Fatal("expected error on unknown flag")
	}
	if !errors.Is(err, flag.ErrHelp) && !strings.Contains(err.Error(), "flag") {
		t.Errorf("error should originate from flag parsing; got: %v", err)
	}
}
