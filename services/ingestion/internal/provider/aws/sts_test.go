package aws_test

import (
	"context"
	"errors"
	"testing"
	"time"

	awssdk "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/sts"
	ststypes "github.com/aws/aws-sdk-go-v2/service/sts/types"

	"axiaops.io/ingestion/internal/provider/aws"
)

// mockSTSClient implements the STSAPI interface and records the AssumeRole
// input it received so tests can assert ExternalId / Tags / TransitiveTagKeys
// flow through correctly.
type mockSTSClient struct {
	gotAssumeRoleInput *sts.AssumeRoleInput
	assumeRoleOutput   *sts.AssumeRoleOutput
	assumeRoleErr      error

	getCallerIdentityOut *sts.GetCallerIdentityOutput
	getCallerIdentityErr error
}

func (m *mockSTSClient) AssumeRole(
	_ context.Context,
	in *sts.AssumeRoleInput,
	_ ...func(*sts.Options),
) (*sts.AssumeRoleOutput, error) {
	m.gotAssumeRoleInput = in
	if m.assumeRoleErr != nil {
		return nil, m.assumeRoleErr
	}
	return m.assumeRoleOutput, nil
}

func (m *mockSTSClient) GetCallerIdentity(
	_ context.Context,
	_ *sts.GetCallerIdentityInput,
	_ ...func(*sts.Options),
) (*sts.GetCallerIdentityOutput, error) {
	if m.getCallerIdentityErr != nil {
		return nil, m.getCallerIdentityErr
	}
	return m.getCallerIdentityOut, nil
}

func successfulAssumeRoleOutput() *sts.AssumeRoleOutput {
	expiry := time.Now().Add(15 * time.Minute)
	return &sts.AssumeRoleOutput{
		AssumedRoleUser: &ststypes.AssumedRoleUser{
			Arn:           awssdk.String("arn:aws:sts::123456789012:assumed-role/AxiaOpsIntegrationRole/axiaops-verify-org-1"),
			AssumedRoleId: awssdk.String("AROAEXAMPLE:axiaops-verify-org-1"),
		},
		Credentials: &ststypes.Credentials{
			AccessKeyId:     awssdk.String("ASIAEXAMPLE"),
			SecretAccessKey: awssdk.String("secret"),
			SessionToken:    awssdk.String("token"),
			Expiration:      &expiry,
		},
	}
}

func TestVerifyAssumeRole_Success(t *testing.T) {
	mock := &mockSTSClient{
		assumeRoleOutput: successfulAssumeRoleOutput(),
	}

	got := aws.VerifyAssumeRole(
		context.Background(),
		mock,
		"arn:aws:iam::123456789012:role/AxiaOpsIntegrationRole",
		"axops-ext-secret-value",
		"org-1",
	)

	if !got.OK {
		t.Fatalf("VerifyAssumeRole returned OK=false: %+v", got)
	}
	if got.AccountID != "123456789012" {
		t.Errorf("AccountID = %q, want %q", got.AccountID, "123456789012")
	}

	in := mock.gotAssumeRoleInput
	if in == nil {
		t.Fatal("AssumeRole was not called")
	}
	if awssdk.ToString(in.ExternalId) != "axops-ext-secret-value" {
		t.Errorf("ExternalId = %q, want %q", awssdk.ToString(in.ExternalId), "axops-ext-secret-value")
	}
	// Session tag is the entire reason we eat this complexity in v1: adding
	// it later requires every customer to edit their trust policy to allow
	// sts:TagSession (design §8 Q7).
	if len(in.Tags) != 1 {
		t.Fatalf("Tags = %d entries, want 1", len(in.Tags))
	}
	if awssdk.ToString(in.Tags[0].Key) != "AxiaOpsOrg" || awssdk.ToString(in.Tags[0].Value) != "org-1" {
		t.Errorf("Tags[0] = (%q, %q), want (AxiaOpsOrg, org-1)",
			awssdk.ToString(in.Tags[0].Key), awssdk.ToString(in.Tags[0].Value))
	}
	if len(in.TransitiveTagKeys) != 1 || in.TransitiveTagKeys[0] != "AxiaOpsOrg" {
		t.Errorf("TransitiveTagKeys = %v, want [AxiaOpsOrg]", in.TransitiveTagKeys)
	}
	if awssdk.ToInt32(in.DurationSeconds) != 900 {
		t.Errorf("DurationSeconds = %d, want 900 (verify path uses short-lived session)",
			awssdk.ToInt32(in.DurationSeconds))
	}
}

func TestVerifyAssumeRole_TrustPolicyMismatch(t *testing.T) {
	mock := &mockSTSClient{
		assumeRoleErr: errors.New("operation error STS: AssumeRole, https response error StatusCode: 403, RequestID: x, api error AccessDenied: User: arn:aws:sts::123456789012:assumed-role/AxiaOpsScanner/abc is not authorized to perform: sts:AssumeRole on resource: arn:aws:iam::123:role/Wrong"),
	}

	got := aws.VerifyAssumeRole(context.Background(), mock,
		"arn:aws:iam::123456789012:role/Whatever", "ext", "org-1")

	if got.OK {
		t.Fatal("VerifyAssumeRole should fail on AccessDenied")
	}
	if got.Code != "role_assume_failed" {
		t.Errorf("Code = %q, want %q", got.Code, "role_assume_failed")
	}
	if got.Reason != "trust_policy_mismatch" {
		t.Errorf("Reason = %q, want %q", got.Reason, "trust_policy_mismatch")
	}
}

func TestVerifyAssumeRole_ExternalIdMismatch(t *testing.T) {
	mock := &mockSTSClient{
		assumeRoleErr: errors.New("api error AccessDenied: ExternalId condition mismatch"),
	}

	got := aws.VerifyAssumeRole(context.Background(), mock,
		"arn:aws:iam::123456789012:role/X", "wrong-ext", "org-1")

	if got.OK {
		t.Fatal("expected verification to fail")
	}
	if got.Reason != "external_id_mismatch" {
		t.Errorf("Reason = %q, want %q", got.Reason, "external_id_mismatch")
	}
}

func TestVerifyAssumeRole_RoleNotFound(t *testing.T) {
	mock := &mockSTSClient{
		assumeRoleErr: errors.New("api error: Role arn:aws:iam::123:role/NeverExisted cannot be found"),
	}

	got := aws.VerifyAssumeRole(context.Background(), mock,
		"arn:aws:iam::123:role/NeverExisted", "ext", "org-1")

	if got.OK {
		t.Fatal("expected failure for missing role")
	}
	if got.Reason != "role_not_found" {
		t.Errorf("Reason = %q, want %q", got.Reason, "role_not_found")
	}
}

func TestVerifyAssumeRole_EmptyResponse(t *testing.T) {
	mock := &mockSTSClient{
		assumeRoleOutput: &sts.AssumeRoleOutput{}, // no Credentials
	}

	got := aws.VerifyAssumeRole(context.Background(), mock,
		"arn:aws:iam::123:role/X", "ext", "org-1")

	if got.OK {
		t.Fatal("expected failure for empty STS response")
	}
	if got.Reason != "empty_response" {
		t.Errorf("Reason = %q, want %q", got.Reason, "empty_response")
	}
}
