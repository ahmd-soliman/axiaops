package main

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"

	awsprovider "axiaops.io/ingestion/internal/provider/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/sts"
)

// verifyRequest is the body POSTed by the API service when a customer pastes
// their freshly-minted role ARN into the dashboard.
type verifyRequest struct {
	RoleARN        string `json:"role_arn"`
	ExternalID     string `json:"external_id"`
	Region         string `json:"region"`
	OrganizationID string `json:"organization_id"`
}

// verifyResponse mirrors the shape the API handler translates back to the
// dashboard. {ok:true} means the AssumeRole + GetCallerIdentity round-trip
// succeeded; {ok:false} carries a structured code/reason so the dashboard
// can render targeted help text.
type verifyResponse struct {
	OK        bool   `json:"ok"`
	AccountID string `json:"account_id,omitempty"`
	Code      string `json:"code,omitempty"`
	Reason    string `json:"reason,omitempty"`
	Detail    string `json:"detail,omitempty"`
}

// handleVerifyCredentials runs a synchronous sts:AssumeRole probe against the
// customer's role and reports the outcome. The endpoint is stateless — it
// does not write to the database; the API service owns persistence.
func handleVerifyCredentials(w http.ResponseWriter, r *http.Request) {
	var req verifyRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid request", http.StatusBadRequest)
		return
	}
	if req.RoleARN == "" || req.ExternalID == "" || req.OrganizationID == "" {
		writeVerifyResponse(w, verifyResponse{
			OK:     false,
			Code:   "invalid_request",
			Reason: "missing_field",
			Detail: "role_arn, external_id and organization_id are required",
		})
		return
	}
	region := req.Region
	if region == "" {
		region = "eu-central-1"
	}

	stsClient, err := newSTSClient(r.Context(), region)
	if err != nil {
		slog.Error("verify: load aws config", "error", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	result := awsprovider.VerifyAssumeRole(r.Context(), stsClient, req.RoleARN, req.ExternalID, req.OrganizationID)
	if !result.OK {
		slog.Info("verify: AssumeRole rejected",
			"organization_id", req.OrganizationID,
			"role_arn", req.RoleARN,
			"reason", result.Reason)
	} else {
		slog.Info("verify: AssumeRole succeeded",
			"organization_id", req.OrganizationID,
			"role_arn", req.RoleARN,
			"resolved_account_id", result.AccountID)
	}

	writeVerifyResponse(w, verifyResponse{
		OK:        result.OK,
		AccountID: result.AccountID,
		Code:      result.Code,
		Reason:    result.Reason,
		Detail:    result.Detail,
	})
}

func writeVerifyResponse(w http.ResponseWriter, body verifyResponse) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(body)
}

// newSTSClient returns the STS client used by the verify handler. The
// indirection lets tests swap in a mock STSAPI without spinning up real AWS
// SDK plumbing.
var newSTSClient = func(ctx context.Context, region string) (awsprovider.STSAPI, error) {
	cfg, err := config.LoadDefaultConfig(ctx, config.WithRegion(region))
	if err != nil {
		return nil, err
	}
	return sts.NewFromConfig(cfg), nil
}
