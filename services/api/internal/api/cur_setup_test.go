package api_test

import (
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"axiaops.io/api/internal/api"
	"axiaops.io/shared/model"
)

// ── GET /accounts/{id}/cur-setup ─────────────────────────────────────────────
//
// Pins the rendered shape of templates/cur_setup.yaml.tmpl — specifically
// that Athena partition projection is present on AxiaOpsCURTable. Without
// it, Athena has no visibility into any BILLING_PERIOD=YYYY-MM folder until
// something explicitly registers the partition (MSCK REPAIR, a crawler, a
// Lambda) — the table silently returns 0 rows for data that's actually in
// S3, every month, for every customer. Regression-pins the fix so it can't
// silently regress back to an unregistered table.

func TestGetCURSetup_Returns200AndYAMLContentType(t *testing.T) {
	store := NewMockStore().WithAccounts([]model.Account{
		{ID: "acc-1", OrganizationID: "organization-test-uuid", Provider: "aws", BillingSource: model.BillingSourceCURAthena},
	})
	h := api.New(store, noopQueue())
	mux := http.NewServeMux()
	h.Register(mux)

	w := httptest.NewRecorder()
	mux.ServeHTTP(w, orgRequest(http.MethodGet, "/v1/accounts/acc-1/cur-setup"))

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d — body: %s", w.Code, w.Body.String())
	}
	if ct := w.Header().Get("Content-Type"); ct != "text/yaml" {
		t.Errorf("expected text/yaml, got %s", ct)
	}
}

func TestGetCURSetup_AccountNotFound_Returns404(t *testing.T) {
	store := NewMockStore().WithGetAccountError(errors.New("not found"))
	h := api.New(store, noopQueue())
	mux := http.NewServeMux()
	h.Register(mux)

	w := httptest.NewRecorder()
	mux.ServeHTTP(w, orgRequest(http.MethodGet, "/v1/accounts/nonexistent/cur-setup"))

	if w.Code != http.StatusNotFound {
		t.Errorf("expected 404, got %d", w.Code)
	}
}

func TestGetCURSetup_PopulatesScanPermissions(t *testing.T) {
	store := NewMockStore().WithAccounts([]model.Account{
		{ID: "acc-1", OrganizationID: "organization-test-uuid", Provider: "aws", BillingSource: model.BillingSourceCURAthena},
	})
	h := api.New(store, noopQueue())
	mux := http.NewServeMux()
	h.Register(mux)

	w := httptest.NewRecorder()
	mux.ServeHTTP(w, orgRequest(http.MethodGet, "/v1/accounts/acc-1/cur-setup"))

	body := w.Body.String()

	// The Go-template loops over generalScanPermissions/curAthenaPermissions
	// must have actually executed — no leftover "{{" template syntax, and
	// the known actions from both lists present in the rendered policy.
	if strings.Contains(body, "{{") || strings.Contains(body, "}}") {
		t.Errorf("expected no unrendered template syntax, got: %s", body)
	}
	for _, action := range []string{
		"sts:GetCallerIdentity",
		"ce:GetCostAndUsage",
		"athena:StartQueryExecution",
		"glue:GetPartitions",
		"glue:BatchGetPartition",
	} {
		if !strings.Contains(body, action) {
			t.Errorf("expected %q in rendered template, got: %s", action, body)
		}
	}
}

func TestGetCURSetup_GlueTableHasPartitionProjection(t *testing.T) {
	store := NewMockStore().WithAccounts([]model.Account{
		{ID: "acc-1", OrganizationID: "organization-test-uuid", Provider: "aws", BillingSource: model.BillingSourceCURAthena},
	})
	h := api.New(store, noopQueue())
	mux := http.NewServeMux()
	h.Register(mux)

	w := httptest.NewRecorder()
	mux.ServeHTTP(w, orgRequest(http.MethodGet, "/v1/accounts/acc-1/cur-setup"))

	body := w.Body.String()

	if !strings.Contains(body, "AxiaOpsCURTable") {
		t.Fatalf("expected AxiaOpsCURTable resource in rendered template, got: %s", body)
	}

	// Every projection property partition projection needs to work without
	// MSCK REPAIR / a crawler / a Lambda.
	for _, want := range []string{
		"'projection.enabled': 'true'",
		"'projection.billing_period.type': 'date'",
		"'projection.billing_period.format': 'yyyy-MM'",
		"'projection.billing_period.interval': '1'",
		"'projection.billing_period.interval.unit': 'MONTHS'",
		"'projection.billing_period.range': '2024-01,NOW'",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("expected %q in rendered Glue table Parameters, got: %s", want, body)
		}
	}

	// storage.location.template must resolve the bucket via CFN !Sub while
	// keeping the Athena placeholder ${billing_period} literal (escaped as
	// ${!billing_period} in Fn::Sub syntax) — get either half wrong and
	// projection silently matches nothing.
	if !strings.Contains(body, "storage.location.template") {
		t.Fatalf("expected storage.location.template in rendered Glue table Parameters, got: %s", body)
	}
	if !strings.Contains(body, "cur/axiaops/data/BILLING_PERIOD=${!billing_period}/") {
		t.Errorf("expected storage.location.template to use the literal ${!billing_period} placeholder, got: %s", body)
	}

	// PartitionKeys must still declare billing_period — projection augments
	// the table, it doesn't replace the partition key declaration.
	if !strings.Contains(body, "Name: billing_period") {
		t.Errorf("expected PartitionKeys to declare billing_period, got: %s", body)
	}
}

func TestGetCURSetup_CURDataBucketIsReadOnly(t *testing.T) {
	store := NewMockStore().WithAccounts([]model.Account{
		{ID: "acc-1", OrganizationID: "organization-test-uuid", Provider: "aws", BillingSource: model.BillingSourceCURAthena},
	})
	h := api.New(store, noopQueue())
	mux := http.NewServeMux()
	h.Register(mux)

	w := httptest.NewRecorder()
	mux.ServeHTTP(w, orgRequest(http.MethodGet, "/v1/accounts/acc-1/cur-setup"))

	body := w.Body.String()

	// The scanning role/user must never get write access to the customer's
	// own CUR data bucket — it's only ever written to by
	// bcm-data-exports.amazonaws.com (a separate bucket policy). Isolate the
	// AxiaOpsPolicy statement granting access to CURDataBucket and assert it
	// carries no PutObject, while the AthenaQueryResultsBucket statement
	// (which legitimately needs to write query results) still does.
	curBucketStmt := strings.Index(body, "Resource:\n              - !Sub 'arn:aws:s3:::${CURDataBucket}'\n")
	if curBucketStmt == -1 {
		t.Fatalf("expected a statement granting access to CURDataBucket, got: %s", body)
	}
	athenaBucketStmt := strings.Index(body, "Resource:\n              - !Sub 'arn:aws:s3:::${AthenaQueryResultsBucket}'\n")
	if athenaBucketStmt == -1 {
		t.Fatalf("expected a statement granting access to AthenaQueryResultsBucket, got: %s", body)
	}

	// Walk backward from each Resource: block to its own "- Effect: Allow"
	// header to isolate just that statement's Action list.
	statementStart := func(resourceIdx int) int {
		s := strings.LastIndex(body[:resourceIdx], "- Effect: Allow")
		if s == -1 {
			t.Fatalf("could not locate statement header before offset %d", resourceIdx)
		}
		return s
	}
	curStmt := body[statementStart(curBucketStmt):curBucketStmt]
	athenaStmt := body[statementStart(athenaBucketStmt):athenaBucketStmt]

	if strings.Contains(curStmt, "s3:PutObject") {
		t.Errorf("expected no s3:PutObject in the CURDataBucket statement, got: %s", curStmt)
	}
	if !strings.Contains(athenaStmt, "s3:PutObject") {
		t.Errorf("expected s3:PutObject in the AthenaQueryResultsBucket statement, got: %s", athenaStmt)
	}
}

// ── PATCH /accounts/{id} — CUR config injection guard ───────────────────────
//
// cur_database/cur_table/cur_workgroup/cur_results_s3/cur_region are
// interpolated into Athena SQL via fmt.Sprintf as quoted identifiers
// (cur/query.go's buildAmortizedSQL/buildTaxSQL) — unvalidated, an
// authenticated user with accounts:write could escape the identifier quotes
// and inject arbitrary Presto SQL, or point the scan at another
// organization's Glue table/S3 results bucket. Pins that every CUR field is
// checked against AWS's own naming rules before being persisted.

func TestUpdateAccount_RejectsMaliciousCURFields(t *testing.T) {
	cases := []struct {
		name  string
		field string
		value string
	}{
		{"database with quote", "cur_database", `foo" UNION SELECT * FROM other_org_secrets --`},
		{"database with space", "cur_database", "not a valid identifier"},
		{"table with SQL comment", "cur_table", "axiaops_cur_table -- "},
		{"workgroup with slash", "cur_workgroup", "wg/../other"},
		{"results_s3 not an s3 URI", "cur_results_s3", "https://evil.example.com/exfil"},
		{"results_s3 with injected quote", "cur_results_s3", `s3://bucket/"; DROP TABLE accounts; --`},
		{"region not a real region format", "cur_region", "us-east-1; DROP TABLE accounts"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			store := NewMockStore().WithAccounts([]model.Account{{
				ID: "acc-1", OrganizationID: "organization-test-uuid", Provider: "aws",
				BillingSource: model.BillingSourceCURAthena,
			}})
			h := api.New(store, noopQueue())
			mux := http.NewServeMux()
			h.Register(mux)

			body := fmt.Sprintf(`{"billing_source":"cur_athena","cur_database":"axiaops_cur_db","cur_table":"axiaops_cur_table","cur_workgroup":"axiaops_athena_wg","cur_results_s3":"s3://axiaops-athena-results-123456789012-us-east-1","cur_region":"us-east-1","%s":%q}`, tc.field, tc.value)

			w := httptest.NewRecorder()
			mux.ServeHTTP(w, orgRequestWithBody(http.MethodPatch, "/v1/accounts/acc-1", body))

			if w.Code != http.StatusBadRequest {
				t.Errorf("expected 400 for %s=%q, got %d — body: %s", tc.field, tc.value, w.Code, w.Body.String())
			}
		})
	}
}

func TestUpdateAccount_AcceptsValidCURFields(t *testing.T) {
	store := NewMockStore().WithAccounts([]model.Account{{
		ID: "acc-1", OrganizationID: "organization-test-uuid", Provider: "aws",
		BillingSource: model.BillingSourceCURAthena,
	}})
	h := api.New(store, noopQueue())
	mux := http.NewServeMux()
	h.Register(mux)

	body := `{"billing_source":"cur_athena","cur_database":"axiaops_cur_db","cur_table":"axiaops_cur_table","cur_workgroup":"axiaops_athena_wg","cur_results_s3":"s3://axiaops-athena-results-123456789012-us-east-1","cur_region":"us-east-1"}`

	w := httptest.NewRecorder()
	mux.ServeHTTP(w, orgRequestWithBody(http.MethodPatch, "/v1/accounts/acc-1", body))

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 for valid CUR fields, got %d — body: %s", w.Code, w.Body.String())
	}
}
