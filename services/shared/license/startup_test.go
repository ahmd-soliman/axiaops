package license_test

import (
	"os"
	"strings"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"

	"axiaops.io/shared/license"
)

// resetSnapshot ensures one test's SetCurrent / enforcement-bypass doesn't
// leak into the next. Both pieces of package-level state get cleared.
func resetSnapshot(t *testing.T) {
	t.Helper()
	t.Cleanup(func() {
		license.SetCurrent(nil)
		license.ClearEnforcementBypass()
	})
}

// TestVerifyAtBoot_DevModeLoadsFixture lives in embed_dev_test.go because
// the dev fixture is only compiled into !production builds. The
// startup_test.go suite covers paths that work under both build tags; the
// fixture-specific assertion lives next to its dependency.

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

// TestVerifyAtBoot_DevModeWithLicenseEnvRefuses — B1.7 layer 2 anti-tamper.
// DEV_MODE=true with a license configured via env is a deliberate-bypass
// signal; refuse to start. The error message must reference both the plan
// section (operator runbook entry point) and the amendment doc (rationale).
func TestVerifyAtBoot_DevModeWithLicenseEnvRefuses(t *testing.T) {
	resetSnapshot(t)
	k := setupKeys(t)
	raw := signLicense(t, k, nil, nil, nil)
	installLicense(t, k, raw) // sets EnvLicense

	err := license.VerifyAtBoot(true)
	if err == nil {
		t.Fatal("VerifyAtBoot(devMode=true, license configured) returned nil; want layer-2 refusal")
	}
	msg := err.Error()
	for _, want := range []string{"DEV_MODE", "AXIAOPS_LICENSE", "§4.10.2", "b1.6-amendment-feature-gating.md"} {
		if !strings.Contains(msg, want) {
			t.Errorf("error %q missing expected substring %q", msg, want)
		}
	}
	// Layer 2 must NOT flip the enforcement-bypass — refusal is the whole
	// point. A regression that called SetEnforcementBypass before checking
	// licensePresent would silently neuter the gate. Pin the order.
	if license.IsEnforcementBypassed() {
		t.Error("layer-2 refusal should NOT flip enforcement-bypass; bypass would defeat the gate on the next caller")
	}
}

// TestVerifyAtBoot_DevModeWithLicenseFileRefuses — same posture as the env
// case, but via the file-path branch. Pinning both paths because the layer-2
// helper resolves them differently and a regression in one is invisible
// from the other.
func TestVerifyAtBoot_DevModeWithLicenseFileRefuses(t *testing.T) {
	resetSnapshot(t)
	// Drop a sentinel file at a temp path; layer 2 doesn't parse it, only
	// stats it, so the contents can be empty.
	dir := t.TempDir()
	path := dir + "/license.jwt"
	if err := os.WriteFile(path, []byte("not-a-real-jwt"), 0o600); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	t.Setenv(license.EnvLicense, "")
	t.Setenv(license.EnvLicensePath, path)

	err := license.VerifyAtBoot(true)
	if err == nil {
		t.Fatal("VerifyAtBoot(devMode=true, license file present) returned nil; want layer-2 refusal")
	}
	if !strings.Contains(err.Error(), path) {
		t.Errorf("error %q should name the license path so the operator knows where to look", err.Error())
	}
}

// TestVerifyAtBoot_ExpiredSoftFails is the post-amendment shape of what
// used to be TestVerifyAtBoot_ExpiredHardFails: the binary keeps running on
// a past-grace license, the operator-facing detail still lands in the slog
// stream, and IsScanAllowed flips to false so the scan-gate 403s. The
// snapshot stays in place so /v1/version can still report the loaded
// license claims with state="expired".
func TestVerifyAtBoot_ExpiredSoftFails(t *testing.T) {
	resetSnapshot(t)
	k := setupKeys(t)
	now := time.Now()
	raw := signLicense(t, k, func(c *jwt.MapClaims) {
		(*c)["exp"] = now.Add(-60 * 24 * time.Hour).Unix() // 60 days past exp
		(*c)["grace_period_days"] = 30                     // 30-day grace ended 30 days ago
	}, nil, nil)
	installLicense(t, k, raw)

	if err := license.VerifyAtBoot(false); err != nil {
		t.Fatalf("VerifyAtBoot(past_grace) returned %v, want nil under amendment", err)
	}
	if state := license.SnapshotState(); state != license.StateExpired {
		t.Errorf("SnapshotState() = %v, want StateExpired", state)
	}
	if license.Snapshot() == nil {
		t.Error("past-grace boot should retain the snapshot so /v1/version can report claims with state=\"expired\"")
	}
	if license.IsScanAllowed() {
		t.Error("IsScanAllowed past-grace = true, want false — scan-gate must block expired licenses")
	}
	if license.IsEnforcementBypassed() {
		t.Error("expired license should NOT flip enforcement-bypass — bypass is for DEV_MODE / SaaS only")
	}
}

// TestVerifyAtBoot_LoadError — tampered/invalid JWTs land the binary in
// StateNotLoaded post-amendment (Load fails → no snapshot → scan-gate
// blocks). Pre-amendment this returned an error; the new contract is that
// VerifyAtBoot never returns an error and the metric is the durable signal.
func TestVerifyAtBoot_LoadError(t *testing.T) {
	resetSnapshot(t)
	k := setupKeys(t)
	// Tampered signature → Load returns error, VerifyAtBoot now logs +
	// continues. The classifyLoadErr metric is the durable trace.
	raw := signLicense(t, k, nil, nil, nil)
	parts := strings.Split(raw, ".")
	if len(parts) != 3 {
		t.Fatalf("unexpected JWT shape: %d", len(parts))
	}
	mid := len(parts[2]) / 2
	flipped := parts[2][:mid] + flipChar(parts[2][mid]) + parts[2][mid+1:]
	tampered := parts[0] + "." + parts[1] + "." + flipped
	installLicense(t, k, tampered)

	if err := license.VerifyAtBoot(false); err != nil {
		t.Fatalf("VerifyAtBoot(tampered) returned %v, want nil under amendment", err)
	}
	if license.Snapshot() != nil {
		t.Error("tampered license should leave Snapshot nil")
	}
	if license.IsScanAllowed() {
		t.Error("IsScanAllowed with tampered license = true, want false")
	}
}

// TestVerifyAtBoot_MissingLicense — no env, no file, no DEV_MODE: the
// binary boots into StateNotLoaded and the scan-gate 403s. Pre-amendment
// this refused to start; post-amendment the operator gets the slog.Error
// install instructions and the LicenseLoadErrorsTotal{reason="missing"}
// metric increments.
func TestVerifyAtBoot_MissingLicense(t *testing.T) {
	resetSnapshot(t)
	t.Setenv(license.EnvLicense, "")
	t.Setenv(license.EnvLicensePath, t.TempDir()+"/no-such-license.jwt")

	if err := license.VerifyAtBoot(false); err != nil {
		t.Fatalf("VerifyAtBoot(missing) returned %v, want nil under amendment", err)
	}
	if license.Snapshot() != nil {
		t.Error("missing license should leave Snapshot nil")
	}
	if state := license.SnapshotState(); state != license.StateNotLoaded {
		t.Errorf("SnapshotState() = %v, want StateNotLoaded", state)
	}
	if license.IsScanAllowed() {
		t.Error("IsScanAllowed with no license = true, want false (scan-gate must block production not_loaded)")
	}
	if license.IsEnforcementBypassed() {
		t.Error("missing license must not flip enforcement-bypass — bypass is for DEV_MODE / SaaS only")
	}
}

func TestSnapshotState_NoLicense(t *testing.T) {
	resetSnapshot(t)
	license.SetCurrent(nil)
	if state := license.SnapshotState(); state != license.StateNotLoaded {
		t.Errorf("SnapshotState() with no license = %v, want StateNotLoaded", state)
	}
}

// TestVerifyAtBoot_LoadErrorClassification pins each license-load failure
// mode to the post-amendment behaviour: VerifyAtBoot returns nil, no
// snapshot is set, and the scan-gate blocks. The Prometheus reason label
// is the durable classification signal — covered by the metrics test below
// (TestVerifyAtBoot_LoadErrorReasonMetric). Together they replace the old
// error-substring regression that no longer applies.
func TestVerifyAtBoot_LoadErrorClassification(t *testing.T) {
	cases := []struct {
		name    string
		mutate  func(c *jwt.MapClaims)
		method  jwt.SigningMethod
		signKey any
	}{
		{
			name:   "wrong_issuer",
			mutate: func(c *jwt.MapClaims) { (*c)["iss"] = "https://evil.example.com/licenses" },
		},
		{
			name:   "wrong_audience",
			mutate: func(c *jwt.MapClaims) { (*c)["aud"] = "axiaops-wrong-aud" },
		},
		{
			name:   "future_iat",
			mutate: func(c *jwt.MapClaims) { (*c)["iat"] = time.Now().Add(48 * time.Hour).Unix() },
		},
		{
			name:    "alg_none_classified_as_signature",
			method:  jwt.SigningMethodNone,
			signKey: jwt.UnsafeAllowNoneSignatureType,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			resetSnapshot(t)
			k := setupKeys(t)
			raw := signLicense(t, k, tc.mutate, tc.method, tc.signKey)
			installLicense(t, k, raw)

			if err := license.VerifyAtBoot(false); err != nil {
				t.Fatalf("VerifyAtBoot returned %v, want nil under amendment", err)
			}
			if license.Snapshot() != nil {
				t.Error("load failure should leave Snapshot nil")
			}
			if license.IsScanAllowed() {
				t.Error("IsScanAllowed = true after load failure; want false")
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


// TestVerifyAtBoot_BypassSkipsLicenseLoad pins the SaaS-default posture
// (design §7.1): cmd/saasmode_saas.go flips the enforcement bypass BEFORE
// VerifyAtBoot, making "no license" the expected state — boot verification
// must return nil without travelling the missing-license error path (the
// "scans will be blocked" slog.Error + LicenseLoadErrorsTotal bump that
// pairs with the operator alert), and must not load a configured license
// either: /v1/version collapses to state="managed" off the bypass flag
// alone, so a snapshot would be dead weight kept alive by the ticker.
func TestVerifyAtBoot_BypassSkipsLicenseLoad(t *testing.T) {
	t.Run("no license configured", func(t *testing.T) {
		resetSnapshot(t)
		t.Setenv("AXIAOPS_LICENSE", "")
		t.Setenv("AXIAOPS_LICENSE_PATH", "")
		license.SetEnforcementBypass()

		if err := license.VerifyAtBoot(false); err != nil {
			t.Fatalf("VerifyAtBoot(bypassed, no license) = %v, want nil", err)
		}
		if license.Snapshot() != nil {
			t.Error("Snapshot() non-nil after bypassed boot; want nil (license dormant)")
		}
	})

	t.Run("license configured but dormant", func(t *testing.T) {
		resetSnapshot(t)
		k := setupKeys(t)
		raw := signLicense(t, k, nil, nil, nil)
		installLicense(t, k, raw)
		license.SetEnforcementBypass()

		if err := license.VerifyAtBoot(false); err != nil {
			t.Fatalf("VerifyAtBoot(bypassed, license present) = %v, want nil", err)
		}
		if license.Snapshot() != nil {
			t.Error("Snapshot() non-nil; the SaaS build must not load the dormant license")
		}
	})
}
