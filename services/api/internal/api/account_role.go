package api

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"github.com/google/uuid"

	"axiaops.io/api/internal/audit"
	"axiaops.io/api/internal/middleware"
	"axiaops.io/shared/httpauth"
	"axiaops.io/shared/model"
	"axiaops.io/shared/storage"
)

// externalIDByteLength is the entropy we put behind every per-account
// ExternalId. 32 bytes → 256 bits → 43 base64url characters, well inside
// AWS's 2-1224 character ExternalId window. Design §2 specified at least
// 128-bit; we ship 256-bit because the marginal cost is zero and rejecting a
// guess attempt does not hinge on the lower bound.
const externalIDByteLength = 32

// verifyTimeout bounds the synchronous round-trip to ingestion. STS itself is
// ~50–200 ms; the larger budget is for the wedge case.
const verifyTimeout = 30 * time.Second

// verifyHTTPClient is the package-level client used to call ingestion's
// /v1/credentials/verify endpoint. http.DefaultClient has no Timeout so a
// half-open TCP can hold the goroutine until the per-call context expires.
// The explicit Timeout here is the belt to that suspenders, and matches the
// per-call ctx.WithTimeout below.
var verifyHTTPClient = &http.Client{Timeout: verifyTimeout}

// createDraftAccount kicks off role-based onboarding. Body: {label, region}.
// Generates a server-side ExternalId, persists a row with
// status='pending_role_setup', and returns it (with external_id JSON-visible
// so the dashboard can render the trust policy template).
//
// The customer-facing flow is described in docs/OPERATIONS.md (§1).
func (h *Handler) createDraftAccount(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Provider      string `json:"provider"`
		Label         string `json:"label"`
		Region        string `json:"region"`
		BillingSource string `json:"billing_source"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}
	if req.Provider == "" {
		req.Provider = "aws"
	}
	if req.Provider != "aws" {
		http.Error(w, "role-based auth is only supported for provider=aws", http.StatusBadRequest)
		return
	}
	if req.Region == "" {
		req.Region = "eu-central-1"
	}

	externalID, err := generateExternalID()
	if err != nil {
		slog.Error("createDraftAccount: external_id generation failed", "error", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	organizationID := middleware.OrganizationID(r.Context())
	ctx := storage.WithOrganizationID(r.Context(), organizationID)

	var bs string
	if req.BillingSource == model.BillingSourceCURAthena {
		bs = model.BillingSourceCURAthena
	} else {
		bs = model.BillingSourceCostExplorer
	}

	account := model.Account{
		ID:                uuid.New().String(),
		BillingSource:     bs,
		OrganizationID:    organizationID,
		Provider:          req.Provider,
		Label:             req.Label,
		AuthMethod:        model.AuthMethodRole,
		ExternalID:        externalID,
		Region:            req.Region,
		Status:            model.AccountStatusPendingRoleSetup,
		ScanIntervalHours: 24,
		CreatedAt:         time.Now().UTC(),
	}
	if bs == model.BillingSourceCURAthena {
		defDB := defaultCURDatabase
		defTable := defaultCURTable
		defWG := defaultCURWorkgroup
		defS3 := placeholderCURResultsS3
		// AWS::BCMDataExports::Export only exists in us-east-1, and the
		// same setup stack creates the Glue Database/Table/Athena
		// Workgroup, so all of it lives in us-east-1 regardless of the
		// account's own AWS region -- see handler.go's createAccount for
		// the identical reasoning.
		defRegion := defaultCURRegion
		account.CURDatabase = &defDB
		account.CURTable = &defTable
		account.CURWorkgroup = &defWG
		account.CURResultsS3 = &defS3
		account.CURRegion = &defRegion
	}

	if err := h.store.SaveAccount(ctx, account); err != nil {
		slog.Error("createDraftAccount: save failed", "error", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	// Audit only that a draft was generated — never the ExternalId value.
	audit.Record(r, h.store, model.AuditEvent{
		Action:       model.AuditActionAccountRoleDraftCreated,
		ResourceType: "account",
		ResourceID:   account.ID,
		Metadata: map[string]any{
			"provider":    account.Provider,
			"label":       account.Label,
			"region":      account.Region,
			"auth_method": account.AuthMethod,
		},
	})

	writeJSONStatus(w, http.StatusCreated, account)
}

// verifyRoleViaIngestion is the API-side wrapper around ingestion's
// POST /v1/credentials/verify. The API service has no AWS SDK dependency by
// design (services/shared/CLAUDE.md "No AWS SDK dependency"); the round-trip
// happens over the existing intra-VPC HTTP plumbing already used by
// POST /v1/accounts/{id}/scan.
func (h *Handler) verifyRoleViaIngestion(ctx context.Context, roleARN, externalID, region, organizationID string) (verifyOutcome, error) {
	body, err := json.Marshal(map[string]string{
		"role_arn":        roleARN,
		"external_id":     externalID,
		"region":          region,
		"organization_id": organizationID,
	})
	if err != nil {
		return verifyOutcome{}, fmt.Errorf("marshal verify request: %w", err)
	}

	ctx, cancel := context.WithTimeout(ctx, verifyTimeout)
	defer cancel()

	const verifyPath = "/v1/credentials/verify"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		h.ingestionURL+verifyPath, bytes.NewReader(body))
	if err != nil {
		return verifyOutcome{}, fmt.Errorf("build verify request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if len(h.ingestionSecret) > 0 {
		ts := time.Now()
		sig := httpauth.Sign(h.ingestionSecret, ts, http.MethodPost, verifyPath, body)
		req.Header.Set(httpauth.HeaderTimestamp, strconv.FormatInt(ts.Unix(), 10))
		req.Header.Set(httpauth.HeaderSignature, sig)
	}

	resp, err := verifyHTTPClient.Do(req)
	if err != nil {
		return verifyOutcome{}, fmt.Errorf("verify ingestion call: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		raw, _ := io.ReadAll(resp.Body)
		return verifyOutcome{}, fmt.Errorf("verify ingestion returned %d: %s", resp.StatusCode, string(raw))
	}

	var out verifyOutcome
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return verifyOutcome{}, fmt.Errorf("decode verify response: %w", err)
	}
	return out, nil
}

// verifyOutcome mirrors the response shape returned by ingestion's
// /v1/credentials/verify endpoint. Kept in lockstep with services/ingestion/cmd/verify.go.
type verifyOutcome struct {
	OK        bool   `json:"ok"`
	AccountID string `json:"account_id,omitempty"`
	Code      string `json:"code,omitempty"`
	Reason    string `json:"reason,omitempty"`
	Detail    string `json:"detail,omitempty"`
}

// generateExternalID returns a 256-bit URL-safe random string. crypto/rand is
// the only acceptable source — math/rand is not seeded for security.
func generateExternalID() (string, error) {
	buf := make([]byte, externalIDByteLength)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return "axiaops-ext-" + base64.RawURLEncoding.EncodeToString(buf), nil
}
