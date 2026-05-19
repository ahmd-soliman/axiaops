package main

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"os"
	"strconv"
	"time"

	"axiaops.io/shared/httpauth"
	"axiaops.io/shared/license"
	"axiaops.io/shared/storage"
)

// scanHandler is the extracted POST /scan handler. Pulled out of the inline
// closure in main.go so the route registration cleanly wraps in
// httpauth.Middleware (the wrap is a per-handler shape, not a per-mux shape;
// see docs/c1-hmac-plan.md §4.2).
//
// The HMAC middleware (wrapping this handler) runs BEFORE the license gate
// here, so an unauthenticated caller cannot probe license state via the
// 403/401 timing differential. Same ordering applies to /v1/credentials/verify.
func scanHandler(store storage.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		// License scan-gate (plan §4.9.2b, post-amendment). Routed through
		// IsScanAllowedForState so the predicate stays in lockstep with the
		// api-side handler and the scheduler — single source of truth for
		// the policy.
		//
		// State is read ONCE and used for both the gate predicate and the
		// error-code branch — avoids the wall-clock-driven TOCTOU where a
		// valid → in_grace → expired cross-tick between two consecutive
		// reads could cross-classify the response body.
		licState := license.SnapshotState()
		if !license.IsScanAllowedForState(licState) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusForbidden)
			switch licState {
			case license.StateExpired:
				_, _ = w.Write([]byte(`{"error":"license_expired"}`))
			case license.StateNotLoaded:
				_, _ = w.Write([]byte(`{"error":"license_not_loaded"}`))
			case license.StateValid, license.StateInGrace:
				slog.Warn("ingestion scan-gate: allow-listed state reached body builder",
					"state", licState.String(),
				)
				_, _ = w.Write([]byte(`{"error":"license_inactive"}`))
			default:
				slog.Warn("ingestion scan-gate: unhandled license state", "state", licState.String())
				_, _ = w.Write([]byte(`{"error":"license_inactive"}`))
			}
			return
		}

		var req struct {
			AccountID      string `json:"account_id"`
			OrganizationID string `json:"organization_id"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			slog.Error("scan: invalid request", "error", err)
			http.Error(w, "invalid request", http.StatusBadRequest)
			return
		}

		ctx := storage.WithOrganizationID(context.Background(), req.OrganizationID)

		if err := runScan(ctx, store, req.AccountID); err != nil {
			slog.Error("scan: ingestion failed", "account_id", req.AccountID, "error", err)
			// Persist the failure reason on the row so the dashboard can surface
			// it without forcing operators into the logs (design §6.6). Same
			// path serves access-key and role accounts.
			_ = store.SetAccountError(ctx, req.AccountID, err.Error())
			http.Error(w, "ingestion failed", http.StatusInternalServerError)
			return
		}
		_ = store.UpdateAccountStatus(ctx, req.AccountID, "connected")
		w.WriteHeader(http.StatusOK)
	}
}

// loadIngestionSecrets returns the slice of accepted HMAC secrets — current
// plus optional next (for zero-downtime rotation, plan §5). devMode allows
// the current slot to be empty; the caller switches to a PassthroughWithWarning
// wrap so signed traffic into a dev binary surfaces a one-shot warning.
//
// Also returns the SoftEnforce flag derived from INGESTION_HMAC_SOFT_ENFORCE.
func loadIngestionSecrets(devMode bool) (secrets [][]byte, softEnforce bool) {
	current, err := httpauth.LoadFromEnv("INGESTION_SHARED_SECRET", devMode)
	if err != nil {
		die("hmac: " + err.Error())
	}
	if current != nil {
		secrets = append(secrets, current)
	}
	// Optional `_NEXT` slot for rotation staging — present on the verifier
	// during a rotation, never on the signer.
	next, err := httpauth.LoadFromEnv("INGESTION_SHARED_SECRET_NEXT", true)
	if err != nil {
		die("hmac: " + err.Error())
	}
	if next != nil {
		secrets = append(secrets, next)
	}
	softEnforce = os.Getenv("INGESTION_HMAC_SOFT_ENFORCE") == "true"
	return secrets, softEnforce
}

// primarySecret returns the first non-nil secret in the slice (the "current"
// one used by the signer — Enqueue side). nil when no secrets are configured.
func primarySecret(secrets [][]byte) []byte {
	for _, s := range secrets {
		if len(s) > 0 {
			return s
		}
	}
	return nil
}

// composeHMACProtect returns a wrapper function that adds HMAC verification
// around the given handler. When no secrets are configured (DEV_MODE), the
// wrapper degrades to PassthroughWithWarning so a signed request from a
// production api binary surfaces a one-shot slog.Warn.
func composeHMACProtect(secrets [][]byte, maxSkew time.Duration, softEnforce bool) func(http.Handler) http.Handler {
	if primarySecret(secrets) == nil {
		// DEV_MODE: no secret configured; wrap with the warning-passthrough.
		return func(h http.Handler) http.Handler {
			return httpauth.PassthroughWithWarning(h)
		}
	}
	opts := httpauth.Options{MaxSkew: maxSkew, SoftEnforce: softEnforce}
	return func(h http.Handler) http.Handler {
		return httpauth.MultiSecretMiddleware(secrets, opts, h)
	}
}

// secretsFingerprint returns a short diagnostic of the loaded secret slots
// (lengths only — never the bytes). Emitted at boot in main.go so an
// operator inspecting a running container can confirm rotation is staged
// correctly without exposing the secret to log scrapers (plan §4.5).
func secretsFingerprint(secrets [][]byte) string {
	parts := make([]string, 0, len(secrets))
	for i, s := range secrets {
		parts = append(parts, "slot"+strconv.Itoa(i)+"="+strconv.Itoa(len(s))+"B")
	}
	if len(parts) == 0 {
		return "<none>"
	}
	out := parts[0]
	for _, p := range parts[1:] {
		out += "," + p
	}
	return out
}
