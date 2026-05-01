package license_test

import (
	"strings"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"

	"axiaops.io/shared/license"
)

// resetSnapshot ensures one test's SetCurrent doesn't leak into the next.
func resetSnapshot(t *testing.T) {
	t.Helper()
	t.Cleanup(func() { license.SetCurrent(nil) })
}

func TestVerifyAtBoot_DevModeBypass(t *testing.T) {
	resetSnapshot(t)
	if err := license.VerifyAtBoot(true); err != nil {
		t.Fatalf("DEV_MODE bypass returned error: %v", err)
	}
	if license.Snapshot() != nil {
		t.Errorf("DEV_MODE should leave Snapshot() nil")
	}
	if state := license.SnapshotState(); state != license.StateNotLoaded {
		t.Errorf("SnapshotState() under DEV_MODE = %v, want StateNotLoaded — slice 5 scan-gate decides policy from this", state)
	}
}

func TestVerifyAtBoot_ValidLicense(t *testing.T) {
	resetSnapshot(t)
	k := setupKeys(t)
	raw := signLicense(t, k, nil, nil, nil)
	installLicense(t, k, raw)

	if err := license.VerifyAtBoot(false); err != nil {
		t.Fatalf("VerifyAtBoot(valid) = %v, want nil", err)
	}

	snap := license.Snapshot()
	if snap == nil {
		t.Fatal("Snapshot() returned nil after successful boot")
	}
	if snap.LicenseID != "lic_acme_2026_v1" {
		t.Errorf("Snapshot LicenseID = %q, want lic_acme_2026_v1", snap.LicenseID)
	}
	if state := license.SnapshotState(); state != license.StateValid {
		t.Errorf("SnapshotState() = %v, want StateValid", state)
	}
}

func TestVerifyAtBoot_InGraceDoesNotRefuse(t *testing.T) {
	resetSnapshot(t)
	k := setupKeys(t)
	now := time.Now()
	raw := signLicense(t, k, func(c *jwt.MapClaims) {
		(*c)["exp"] = now.Add(-5 * 24 * time.Hour).Unix()
		(*c)["grace_period_days"] = 30
	}, nil, nil)
	installLicense(t, k, raw)

	// In-grace must NOT return an error — the binary still starts.
	if err := license.VerifyAtBoot(false); err != nil {
		t.Fatalf("VerifyAtBoot(in_grace) returned error: %v", err)
	}
	if state := license.SnapshotState(); state != license.StateInGrace {
		t.Errorf("SnapshotState() = %v, want StateInGrace", state)
	}
}

func TestVerifyAtBoot_ExpiredHardFails(t *testing.T) {
	resetSnapshot(t)
	k := setupKeys(t)
	now := time.Now()
	raw := signLicense(t, k, func(c *jwt.MapClaims) {
		(*c)["exp"] = now.Add(-60 * 24 * time.Hour).Unix() // 60 days past exp
		(*c)["grace_period_days"] = 30                     // 30-day grace ended 30 days ago
	}, nil, nil)
	installLicense(t, k, raw)

	err := license.VerifyAtBoot(false)
	if err == nil {
		t.Fatal("VerifyAtBoot(past_grace) returned nil, want hard-fail error")
	}
	// The operator-facing message must include the renewal contact and the
	// license ID — without these the error is unactionable in a runbook.
	msg := err.Error()
	for _, want := range []string{"sales@axiaops.io", "lic_acme_2026_v1", "grace ended"} {
		if !strings.Contains(msg, want) {
			t.Errorf("expected substring %q in error %q", want, msg)
		}
	}
	// Snapshot must NOT have been set — slice 5's scan-gate would otherwise
	// accept the binary's continued operation as licensed-and-valid.
	if license.Snapshot() != nil {
		t.Errorf("past-grace boot should leave Snapshot() nil, got %#v", license.Snapshot())
	}
}

func TestVerifyAtBoot_LoadError(t *testing.T) {
	resetSnapshot(t)
	k := setupKeys(t)
	// Tampered signature → Load returns error, VerifyAtBoot must propagate.
	raw := signLicense(t, k, nil, nil, nil)
	parts := strings.Split(raw, ".")
	if len(parts) != 3 {
		t.Fatalf("unexpected JWT shape: %d", len(parts))
	}
	mid := len(parts[2]) / 2
	flipped := parts[2][:mid] + flipChar(parts[2][mid]) + parts[2][mid+1:]
	tampered := parts[0] + "." + parts[1] + "." + flipped
	installLicense(t, k, tampered)

	err := license.VerifyAtBoot(false)
	if err == nil {
		t.Fatal("VerifyAtBoot(tampered) returned nil, want signature error")
	}
}

func TestVerifyAtBoot_MissingLicense(t *testing.T) {
	resetSnapshot(t)
	t.Setenv(license.EnvLicense, "")
	t.Setenv(license.EnvLicensePath, t.TempDir()+"/no-such-license.jwt")

	err := license.VerifyAtBoot(false)
	if err == nil {
		t.Fatal("VerifyAtBoot(missing) returned nil, want ErrNoLicense propagation")
	}
	if !strings.Contains(err.Error(), "AXIAOPS_LICENSE") {
		t.Errorf("error %q should mention the env var to set", err.Error())
	}
}

func TestSnapshotState_NoLicense(t *testing.T) {
	resetSnapshot(t)
	license.SetCurrent(nil)
	if state := license.SnapshotState(); state != license.StateNotLoaded {
		t.Errorf("SnapshotState() with no license = %v, want StateNotLoaded", state)
	}
}

// TestVerifyAtBoot_LoadErrorReasonLabels pins each license-load failure mode
// to the Prometheus reason label classifyLoadErr emits. Substring matching
// against fmt.Errorf-wrapped messages is fragile, so this regression test
// fails loudly if license.go's error strings change without a matching
// classifyLoadErr update. The labels match metrics.go's documented set.
func TestVerifyAtBoot_LoadErrorReasonLabels(t *testing.T) {
	cases := []struct {
		name       string
		mutate     func(c *jwt.MapClaims)
		method     jwt.SigningMethod
		signKey    any
		wantSubstr string // must appear in the error message
	}{
		{
			name:       "wrong_issuer",
			mutate:     func(c *jwt.MapClaims) { (*c)["iss"] = "https://evil.example.com/licenses" },
			wantSubstr: "wrong issuer",
		},
		{
			name:       "wrong_audience",
			mutate:     func(c *jwt.MapClaims) { (*c)["aud"] = "axiaops-saashosted" },
			wantSubstr: "wrong audience",
		},
		{
			name:       "future_iat",
			mutate:     func(c *jwt.MapClaims) { (*c)["iat"] = time.Now().Add(48 * time.Hour).Unix() },
			wantSubstr: "iat",
		},
		{
			name:    "alg_none_classified_as_signature",
			method:  jwt.SigningMethodNone,
			signKey: jwt.UnsafeAllowNoneSignatureType,
			// jwt/v5 emits "signing method none is invalid" or similar — the
			// "verify" wrapper substring is what we match. Either way it
			// classifies to "signature" via the verify-substring branch.
			wantSubstr: "verify",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			resetSnapshot(t)
			k := setupKeys(t)
			raw := signLicense(t, k, tc.mutate, tc.method, tc.signKey)
			installLicense(t, k, raw)

			err := license.VerifyAtBoot(false)
			if err == nil {
				t.Fatalf("VerifyAtBoot returned nil; want error containing %q", tc.wantSubstr)
			}
			if !strings.Contains(err.Error(), tc.wantSubstr) {
				t.Errorf("error %q does not contain expected substring %q — classifyLoadErr label mapping in startup.go may be out of sync", err.Error(), tc.wantSubstr)
			}
		})
	}
}

func TestSnapshot_ConcurrentReadDuringSet(t *testing.T) {
	// Smoke: SetCurrent + Snapshot under concurrent access. atomic.Pointer
	// makes this trivially correct, but the test guards against future
	// regressions if the storage mechanism changes.
	resetSnapshot(t)
	done := make(chan struct{})
	go func() {
		for i := 0; i < 1000; i++ {
			_ = license.Snapshot()
		}
		close(done)
	}()
	for i := 0; i < 1000; i++ {
		license.SetCurrent(&license.License{LicenseID: "x"})
	}
	<-done
}

