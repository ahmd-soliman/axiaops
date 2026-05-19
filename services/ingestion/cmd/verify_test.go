package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	awsprovider "axiaops.io/ingestion/internal/provider/aws"
	awssdk "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/sts"
	ststypes "github.com/aws/aws-sdk-go-v2/service/sts/types"
)

// stubSTS is a minimal STSAPI used to drive handleVerifyCredentials end-to-end.
type stubSTS struct {
	assumeRoleOut *sts.AssumeRoleOutput
	assumeRoleErr error
}

func (s *stubSTS) AssumeRole(_ context.Context, _ *sts.AssumeRoleInput, _ ...func(*sts.Options)) (*sts.AssumeRoleOutput, error) {
	if s.assumeRoleErr != nil {
		return nil, s.assumeRoleErr
	}
	return s.assumeRoleOut, nil
}

func (s *stubSTS) GetCallerIdentity(_ context.Context, _ *sts.GetCallerIdentityInput, _ ...func(*sts.Options)) (*sts.GetCallerIdentityOutput, error) {
	return nil, nil
}

func withSTSStub(t *testing.T, stub awsprovider.STSAPI) {
	t.Helper()
	prev := newSTSClient
	newSTSClient = func(_ context.Context, _ string) (awsprovider.STSAPI, error) {
		return stub, nil
	}
	t.Cleanup(func() { newSTSClient = prev })
}

func TestHandleVerifyCredentials_Success(t *testing.T) {
	withSTSStub(t, &stubSTS{
		assumeRoleOut: &sts.AssumeRoleOutput{
			AssumedRoleUser: &ststypes.AssumedRoleUser{
				Arn: awssdk.String("arn:aws:sts::123456789012:assumed-role/AxiaOpsIntegrationRole/axiaops-verify-org-1"),
			},
			Credentials: &ststypes.Credentials{
				AccessKeyId:     awssdk.String("ASIAEXAMPLE"),
				SecretAccessKey: awssdk.String("secret"),
				SessionToken:    awssdk.String("token"),
			},
		},
	})

	body := []byte(`{
		"role_arn": "arn:aws:iam::123456789012:role/AxiaOpsIntegrationRole",
		"external_id": "axiaops-ext-9f2a4d1e8b73",
		"region": "eu-central-1",
		"organization_id": "org-1"
	}`)

	req := httptest.NewRequest(http.MethodPost, "/v1/credentials/verify", bytes.NewReader(body))
	rr := httptest.NewRecorder()
	handleVerifyCredentials(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rr.Code)
	}
	var got verifyResponse
	if err := json.NewDecoder(rr.Body).Decode(&got); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if !got.OK {
		t.Fatalf("ok = false, body = %+v", got)
	}
	if got.AccountID != "123456789012" {
		t.Errorf("AccountID = %q, want %q", got.AccountID, "123456789012")
	}
}

func TestHandleVerifyCredentials_TrustPolicyMismatch(t *testing.T) {
	withSTSStub(t, &stubSTS{
		assumeRoleErr: errors.New("api error AccessDenied: User is not authorized to perform: sts:AssumeRole"),
	})

	body := strings.NewReader(`{
		"role_arn": "arn:aws:iam::123:role/Wrong",
		"external_id": "ext",
		"region": "eu-central-1",
		"organization_id": "org-1"
	}`)

	req := httptest.NewRequest(http.MethodPost, "/v1/credentials/verify", body)
	rr := httptest.NewRecorder()
	handleVerifyCredentials(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (the API itself succeeded; AssumeRole did not)", rr.Code)
	}
	var got verifyResponse
	if err := json.NewDecoder(rr.Body).Decode(&got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.OK {
		t.Fatal("ok should be false")
	}
	if got.Reason != "trust_policy_mismatch" {
		t.Errorf("Reason = %q, want trust_policy_mismatch", got.Reason)
	}
}

func TestHandleVerifyCredentials_RejectsMissingFields(t *testing.T) {
	// No STS stub needed — request validation runs before the AWS call.
	body := strings.NewReader(`{"role_arn":"","external_id":"","organization_id":""}`)

	req := httptest.NewRequest(http.MethodPost, "/v1/credentials/verify", body)
	rr := httptest.NewRecorder()
	handleVerifyCredentials(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rr.Code)
	}
	var got verifyResponse
	if err := json.NewDecoder(rr.Body).Decode(&got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.OK {
		t.Fatal("ok should be false on missing fields")
	}
	if got.Code != "invalid_request" {
		t.Errorf("Code = %q, want invalid_request", got.Code)
	}
}
