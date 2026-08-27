// Package aws implements the Provider interface for Amazon Web Services.
// It uses the AWS Cost Explorer API (GetCostAndUsage) to retrieve daily costs
// grouped by service and region, and normalizes them into model.CostRecord.
// Credentials are loaded automatically from the environment or ~/.aws/credentials.
package aws

import (
	"context"
	cryptorand "crypto/rand"
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/credentials/stscreds"
	cloudwatchsdk "github.com/aws/aws-sdk-go-v2/service/cloudwatch"
	"github.com/aws/aws-sdk-go-v2/service/costexplorer"
	"github.com/aws/aws-sdk-go-v2/service/costexplorer/types"
	"github.com/aws/aws-sdk-go-v2/service/sts"
	ststypes "github.com/aws/aws-sdk-go-v2/service/sts/types"

	"axiaops.io/shared/analyzer"
	"axiaops.io/shared/crypto"
	"axiaops.io/shared/model"
	"axiaops.io/shared/pricing"
	"axiaops.io/shared/retry"
)

const dateLayout = "2006-01-02"

// Client fetches costs from the AWS Cost Explorer API and usage from CloudWatch.
type Client struct {
	accountID string
	cfg       aws.Config
	ce        CostExplorerAPI
	cw        CloudWatchAPI
	pricing   *pricing.Config
}

// NewWithStaticCredentials builds a Client using the given access key (e.g. per-organization scan)
// without mutating process-wide environment variables.
func NewWithStaticCredentials(ctx context.Context, accessKeyID, secretAccessKey, region string) (*Client, error) {
	if region == "" {
		region = "eu-central-1"
	}
	cfg, err := config.LoadDefaultConfig(ctx,
		config.WithCredentialsProvider(credentials.NewStaticCredentialsProvider(accessKeyID, secretAccessKey, "")),
		config.WithRegion(region),
	)
	if err != nil {
		return nil, fmt.Errorf("aws: load config: %w", err)
	}
	out, err := sts.NewFromConfig(cfg).GetCallerIdentity(ctx, &sts.GetCallerIdentityInput{})
	if err != nil {
		return nil, fmt.Errorf("aws: GetCallerIdentity: %w", err)
	}
	accountID := aws.ToString(out.Account)
	slog.Info("aws: resolved account ID", "account_id", accountID)
	return &Client{
		accountID: accountID,
		cfg:       cfg,
		ce:        costexplorer.NewFromConfig(cfg),
		cw:        cloudwatchsdk.NewFromConfig(cfg),
		pricing:   pricing.Default(),
	}, nil
}

// NewWithClient creates a Client with custom API implementations.
// Used in tests to inject mocks.
func NewWithClient(accountID string, ce CostExplorerAPI, cw CloudWatchAPI) *Client {
	return &Client{accountID: accountID, ce: ce, cw: cw, pricing: pricing.Default()}
}

// assumeRoleSessionDuration is the lifetime requested on every sts:AssumeRole
// call for a real scan. The AWS SDK's CredentialsCache refreshes transparently
// before expiry, so 1h is plenty for any realistic scan.
const assumeRoleSessionDuration = 1 * time.Hour

// verifyAssumeRoleSessionDuration is used by the synchronous
// /v1/credentials/verify flow only — credentials are discarded the moment the
// response is written, so a short lifetime matches the threat model
// (design §4.4).
const verifyAssumeRoleSessionDuration = 15 * time.Minute

// AssumeRoleVerification is the result of a synchronous AssumeRole probe used
// by POST /v1/credentials/verify. Returned by VerifyAssumeRole.
type AssumeRoleVerification struct {
	OK        bool
	AccountID string // populated when OK is true (resolved via GetCallerIdentity)
	Code      string // structured error code, e.g. "role_assume_failed"
	Reason    string // structured reason, e.g. "trust_policy_mismatch"
	Detail    string // human-readable AWS error message (safe to log; do not echo to UI verbatim)
}

// VerifyAssumeRole performs a one-shot sts:AssumeRole + GetCallerIdentity round
// trip and discards the credentials. Used by the /v1/credentials/verify
// endpoint to confirm a customer's trust policy is wired correctly before the
// account row is finalised. Stateless: never touches the database.
//
// organizationID is the AxiaOps tenant ID; threaded into the session name so
// concurrent scans for the same organization are distinguishable in CloudTrail.
// (Previously also passed as an AxiaOpsOrg session tag for future SCP-based
// controls per design §8 Q7, but tag-passing was removed 2026-05-27 — it
// required sts:TagSession on both the caller identity policy AND the customer
// trust policy, and an IAM-side propagation issue made the dual grant flaky.
// Re-introduce when an actual SCP key on aws:PrincipalTag/AxiaOpsOrg ships.)
func VerifyAssumeRole(ctx context.Context, sts STSAPI, roleARN, externalID, organizationID string) AssumeRoleVerification {
	sessionName := newRoleSessionName("verify", organizationID)
	out, err := sts.AssumeRole(ctx, assumeRoleInput(roleARN, externalID, organizationID, sessionName, verifyAssumeRoleSessionDuration))
	if err != nil {
		return classifyAssumeRoleError(err)
	}
	if out == nil || out.Credentials == nil {
		return AssumeRoleVerification{OK: false, Code: "role_assume_failed", Reason: "empty_response", Detail: "STS returned no credentials"}
	}
	// Resolve the customer's AWS account number with the assumed credentials.
	// The SDK pattern would be to build a new sts.Client from the credentials,
	// but since the verify endpoint only needs the Account field of
	// GetCallerIdentity (which AssumeRole's caller arn embeds), we extract it
	// from the AssumedRoleUser.Arn directly to avoid a second STS call.
	accountID := awsAccountIDFromAssumedRoleArn(aws.ToString(out.AssumedRoleUser.Arn))
	if accountID == "" {
		return AssumeRoleVerification{OK: false, Code: "role_assume_failed", Reason: "account_id_unresolved", Detail: "could not parse account ID from AssumedRoleUser ARN"}
	}
	return AssumeRoleVerification{OK: true, AccountID: accountID}
}

// NewWithAssumedRole builds a Client that signs every AWS request with
// short-lived credentials obtained via sts:AssumeRole. The customer's role
// must allow our AxiaOpsScanner principal with the matching ExternalId on its
// trust policy.
//
// organizationID is used only for the CloudTrail-distinguishable role session
// name (no longer passed as a session tag — see VerifyAssumeRole doc comment).
func NewWithAssumedRole(ctx context.Context, roleARN, externalID, region, organizationID string) (*Client, error) {
	if region == "" {
		region = "eu-central-1"
	}
	if roleARN == "" || externalID == "" {
		return nil, fmt.Errorf("aws: role_arn and external_id are required")
	}
	baseCfg, err := config.LoadDefaultConfig(ctx, config.WithRegion(region))
	if err != nil {
		return nil, fmt.Errorf("aws: load base config: %w", err)
	}
	stsClient := sts.NewFromConfig(baseCfg)
	provider := stscreds.NewAssumeRoleProvider(stsClient, roleARN, func(o *stscreds.AssumeRoleOptions) {
		o.ExternalID = aws.String(externalID)
		o.Duration = assumeRoleSessionDuration
		// Per-process unique session name so concurrent scans for the same
		// organization are distinguishable in CloudTrail.
		o.RoleSessionName = newRoleSessionName("scan", organizationID)
	})
	cfg := baseCfg.Copy()
	cfg.Credentials = aws.NewCredentialsCache(provider)

	out, err := sts.NewFromConfig(cfg).GetCallerIdentity(ctx, &sts.GetCallerIdentityInput{})
	if err != nil {
		return nil, fmt.Errorf("aws: GetCallerIdentity (assumed role): %w", err)
	}
	accountID := aws.ToString(out.Account)
	slog.Info("aws: assumed role and resolved account ID", "account_id", accountID, "role_arn", roleARN)
	return &Client{
		accountID: accountID,
		cfg:       cfg,
		ce:        costexplorer.NewFromConfig(cfg),
		cw:        cloudwatchsdk.NewFromConfig(cfg),
		pricing:   pricing.Default(),
	}, nil
}

// NewForAccount dispatches to the right credential constructor based on the
// account's auth_method. This is the single integration point ingestion's
// scan loop calls — the scan path no longer needs to know whether the
// underlying credentials are static keys or assumed-role short-lived ones.
//
// For access-key accounts, NewForAccount calls crypto.Decrypt on the encrypted
// secret. For role accounts, the ENCRYPTION_KEY is not needed at all.
func NewForAccount(ctx context.Context, account model.Account) (*Client, error) {
	switch account.AuthMethod {
	case model.AuthMethodRole:
		return NewWithAssumedRole(ctx, account.RoleARN, account.ExternalID, account.Region, account.OrganizationID)
	case model.AuthMethodAccessKey, "": // empty == access_key for back-compat with pre-MR3 rows
		if account.SecretEncrypted == "" {
			return nil, fmt.Errorf("aws: access-key account has no encrypted secret")
		}
		secret, err := crypto.Decrypt(account.SecretEncrypted)
		if err != nil {
			return nil, fmt.Errorf("aws: decrypt credentials: %w", err)
		}
		return NewWithStaticCredentials(ctx, account.AccessKeyID, secret, account.Region)
	default:
		return nil, fmt.Errorf("aws: unsupported auth_method %q", account.AuthMethod)
	}
}

// assumeRoleInput builds the AssumeRole request shape used by both the verify
// flow and the long-lived scan provider. Pulled into a helper so both paths
// stay in lockstep on the ExternalId contract. organizationID is accepted but
// only used by the caller for session naming — no longer passed as a session
// tag (see VerifyAssumeRole doc comment for the removal rationale).
func assumeRoleInput(roleARN, externalID, organizationID, sessionName string, duration time.Duration) *sts.AssumeRoleInput {
	_ = organizationID // kept on the signature so call sites and tests don't churn; threaded into sessionName by the caller
	return &sts.AssumeRoleInput{
		RoleArn:         aws.String(roleARN),
		RoleSessionName: aws.String(sessionName),
		ExternalId:      aws.String(externalID),
		// AWS takes seconds as int32; the Go-side type is time.Duration so
		// callers cannot accidentally pass milliseconds or hours.
		DurationSeconds: aws.Int32(int32(duration.Seconds())),
	}
}

// newRoleSessionName builds a CloudTrail-distinguishable session name. Format:
// "axiaops-<purpose>-<orgID>-<random>". AWS limits the field to 64 chars and
// the regex [\w+=,.@-]; we truncate organizationID if needed and use a 6-char
// hex suffix so concurrent calls in the same process never collide.
func newRoleSessionName(purpose, organizationID string) string {
	const maxOrgLen = 32
	if len(organizationID) > maxOrgLen {
		organizationID = organizationID[:maxOrgLen]
	}
	var b [4]byte
	if _, err := cryptorand.Read(b[:]); err != nil {
		// Random read failure is genuinely fatal for SDK/boot — fall back to
		// time-based suffix so we never block on entropy.
		return fmt.Sprintf("axiaops-%s-%s-%d", purpose, organizationID, time.Now().UnixNano())
	}
	return fmt.Sprintf("axiaops-%s-%s-%x", purpose, organizationID, b)
}

// classifyAssumeRoleError maps a raw STS error into the structured
// {code, reason} pairs the dashboard renders into targeted help text. The
// distinction between "trust_policy_mismatch" and "external_id_mismatch"
// comes from the AWS error type — both surface as AccessDenied at the top
// level, but the underlying APIError carries enough detail to disambiguate.
func classifyAssumeRoleError(err error) AssumeRoleVerification {
	v := AssumeRoleVerification{OK: false, Code: "role_assume_failed", Detail: err.Error()}

	// MalformedPolicyDocumentException is the only AssumeRole-side typed error
	// the SDK exports that we care to surface explicitly. AccessDenied (the
	// most common failure shape) is wrapped in a generic SDK error, so we
	// disambiguate "trust_policy_mismatch" / "external_id_mismatch" /
	// "role_not_found" by string-matching the AWS error message.
	var malformed *ststypes.MalformedPolicyDocumentException
	if errors.As(err, &malformed) {
		v.Reason = "malformed_policy"
		return v
	}

	msg := err.Error()
	switch {
	case strings.Contains(msg, "ExternalId") || strings.Contains(msg, "external ID"):
		v.Reason = "external_id_mismatch"
	case strings.Contains(msg, "Not authorized to perform sts:AssumeRole") ||
		strings.Contains(msg, "not authorized to perform: sts:AssumeRole"):
		v.Reason = "trust_policy_mismatch"
	case strings.Contains(msg, "Role") && strings.Contains(msg, "cannot be found") ||
		strings.Contains(msg, "Role") && strings.Contains(msg, "does not exist"):
		v.Reason = "role_not_found"
	case strings.Contains(msg, "AccessDenied"):
		v.Reason = "access_denied"
	default:
		v.Reason = "unknown"
	}
	return v
}

// awsAccountIDFromAssumedRoleArn pulls the AWS account number out of an
// assumed-role principal ARN of the form
// "arn:aws:sts::<ACCOUNT_ID>:assumed-role/<ROLE_NAME>/<SESSION_NAME>". Returns
// "" if the ARN is not in the expected shape.
func awsAccountIDFromAssumedRoleArn(arn string) string {
	parts := strings.Split(arn, ":")
	if len(parts) < 5 {
		return ""
	}
	return parts[4]
}

// Rates returns the effective AWS pricing rates for the given region —
// default values merged with any per-region overrides from rates.yml.
func (c *Client) Rates(region string) pricing.Rates {
	if c.pricing == nil {
		c.pricing = pricing.Default()
	}
	return c.pricing.For(region)
}

// Currency returns the currency (e.g., "USD") used by the loaded pricing config.
func (c *Client) Currency() string {
	if c.pricing == nil {
		c.pricing = pricing.Default()
	}
	return c.pricing.Currency
}

// FetchUsage discovers resources via service APIs then queries CloudWatch
// for usage metrics for each discovered resource.
func (c *Client) FetchUsage(ctx context.Context, records []model.CostRecord, start, end time.Time) ([]analyzer.UsageRecord, error) {
	discovered := DiscoverResources(ctx, c, records)
	slog.Info("discover: found resources", "resources", len(discovered), "cost_records", len(records))
	return FetchUsage(ctx, c.cw, discovered, start, end)
}

func (c *Client) Name() string      { return "aws" }
func (c *Client) AccountID() string { return c.accountID }

// configForRegion creates a new AWS config for the specified region using the same credentials
func (c *Client) configForRegion(ctx context.Context, region string) (aws.Config, error) {
	cfg := c.cfg.Copy()
	cfg.Region = region
	return cfg, nil
}

// FetchCosts calls GetCostAndUsage with daily granularity, grouped by
// SERVICE and REGION, and normalizes each result into a CostRecord.
func (c *Client) FetchCosts(ctx context.Context, start, end time.Time) ([]model.CostRecord, error) {
	input := &costexplorer.GetCostAndUsageInput{
		TimePeriod: &types.DateInterval{
			Start: aws.String(start.Format(dateLayout)),
			End:   aws.String(end.Format(dateLayout)),
		},
		Granularity: types.GranularityDaily,
		Metrics:     []string{"NetAmortizedCost"},
		GroupBy: []types.GroupDefinition{
			{Type: types.GroupDefinitionTypeDimension, Key: aws.String("SERVICE")},
			{Type: types.GroupDefinitionTypeDimension, Key: aws.String("REGION")},
		},
	}

	var records []model.CostRecord

	for {
		var page *costexplorer.GetCostAndUsageOutput
		err := retry.Do(ctx, retry.DefaultConfig(), func() error {
			var err error
			page, err = c.ce.GetCostAndUsage(ctx, input)
			return err
		})

		if err != nil {
			var unavail *types.DataUnavailableException
			if errors.As(err, &unavail) {
				return nil, fmt.Errorf(
					"aws: Cost Explorer data is not yet available for this account — "+
						"it can take up to 24 hours after first enabling Cost Explorer before data appears: %w", err)
			}
			return nil, fmt.Errorf("aws: GetCostAndUsage: %w", err)
		}

		for _, result := range page.ResultsByTime {
			periodStart, _ := time.Parse(dateLayout, aws.ToString(result.TimePeriod.Start))
			periodEnd, _ := time.Parse(dateLayout, aws.ToString(result.TimePeriod.End))

			for _, group := range result.Groups {
				ceService := group.Keys[0]
				region := group.Keys[1]

				metric := group.Metrics["NetAmortizedCost"]
				amount, _ := strconv.ParseFloat(aws.ToString(metric.Amount), 64)
				// NetAmortizedCost can be negative (credits, refunds, SP true-ups).
				// Skip non-positive rows so they don't subtract from zombie savings.
				if amount <= 0 {
					continue
				}

				records = append(records, model.CostRecord{
					Provider:    "aws",
					AccountID:   c.accountID,
					Service:     normalizeService(ceService),
					Region:      region,
					Amount:      amount,
					Currency:    aws.ToString(metric.Unit),
					PeriodStart: periodStart,
					PeriodEnd:   periodEnd,
					FetchedAt:   time.Now().UTC(),
				})
			}
		}

		if page.NextPageToken == nil {
			break
		}
		input.NextPageToken = page.NextPageToken
	}

	return records, nil
}

// resourceLevelServices lists AWS services that reliably return resource-level
// IDs from Cost Explorer when grouped by RESOURCE_ID dimension.
var resourceLevelServices = []string{
	"Amazon Elastic Compute Cloud - Compute",
	"Amazon Relational Database Service",
	"AWS Lambda",
	"Amazon Elastic Load Balancing",
	"Amazon Virtual Private Cloud",
	"Amazon ElastiCache",
	"Amazon OpenSearch Service",
	"Amazon Redshift",
	"Amazon SageMaker",
	"Amazon DynamoDB",
	"Amazon Elastic Kubernetes Service",
	"Amazon Elastic Container Service",
	"Amazon DocumentDB",
	"Amazon Managed Streaming for Apache Kafka",
	"Amazon Route 53",
	"Amazon Bedrock",
	"Amazon Kendra",
}

// ceServiceToInternal maps Cost Explorer service names (as returned with
// RESOURCE_ID grouping) to the internal service names used in serviceRules.
var ceServiceToInternal = map[string]string{
	"Amazon Elastic Compute Cloud - Compute": "AmazonEC2",
	"Amazon Relational Database Service":     "AmazonRDS",
	"AWS Lambda":                             "AWSLambda",
	"Amazon Elastic Load Balancing":          "AmazonElasticLoadBalancing",
	"Amazon Virtual Private Cloud":           "AmazonVPC",
	"Amazon ElastiCache":                     "AmazonElastiCache",
	"Amazon OpenSearch Service":              "AmazonES",
	"Amazon Redshift":                        "AmazonRedshift",
	"Amazon SageMaker":                       "AmazonSageMaker",
	"Amazon DynamoDB":                        "AmazonDynamoDB",
	"Amazon Elastic Kubernetes Service":      "AmazonEKS",
	"EC2 Container Registry (ECR)":           "AmazonECR",
	"Amazon EC2 Container Registry (ECR)":    "AmazonECR",
	"Amazon CloudFront":                      "AmazonCloudFront",
	"Amazon Kinesis":                         "AmazonKinesis",
	"Amazon Elastic Container Service":       "AmazonECS",
	"Amazon Cost Explorer":                   "AWSCostExplorer",
	"AWS Cost Explorer":                      "AWSCostExplorer",
	"AWS Data Transfer":                      "AWSDataTransfer",
	"Amazon CloudWatch":                      "AmazonCloudWatch",
	// GetCostAndUsage (regular, non-resource-level) returns this service's
	// name WITHOUT the space that GetCostAndUsageWithResources uses above —
	// confirmed live: SERVICE-dimension cost records came back as
	// "AmazonCloudWatch", not "Amazon CloudWatch", causing every CloudWatch
	// cost row to fall through normalizeService's unknown-service warning
	// path. Both spellings map to the same internal name.
	"AmazonCloudWatch":                   "AmazonCloudWatch",
	"AWS Step Functions":                 "AWSStepFunctions",
	"Amazon Simple Storage Service":      "AmazonS3",
	"AWS Glue":                           "AWSGlue",
	"Amazon Simple Notification Service": "AmazonSNS",
	"Amazon Simple Queue Service":        "AmazonSQS",
	"AWS Secrets Manager":                "AWSSecretsManager",
	"AWS Key Management Service":         "AWSKms",
	"Amazon Glacier":                     "AmazonGlacier",
	"AWS CloudFormation":                 "AWSCloudFormation",
	"Amazon DocumentDB":                  "AmazonDocDB",
	"Amazon Managed Streaming for Apache Kafka": "AmazonMSK",
	"Amazon Route 53":                    "AmazonRoute53",
	"Amazon Bedrock":                     "AmazonBedrock",
	"Amazon Kendra":                      "AmazonKendra",
}

// normalizeService maps Cost Explorer service names to internal names.
// Unknown services are logged as warnings and returned as-is.
func normalizeService(ceService string) string {
	if mapped, ok := ceServiceToInternal[ceService]; ok {
		return mapped
	}
	slog.Warn("unknown AWS service from Cost Explorer", "service", ceService)
	return ceService
}

// resourceCostMaxLookback is the maximum window GetCostAndUsageWithResources
// supports. AWS rejects requests older than the past 14 days for this API.
const resourceCostMaxLookback = 14 * 24 * time.Hour

// FetchResourceCosts calls GetCostAndUsageWithResources grouped by SERVICE and
// RESOURCE_ID to get per-resource cost data. Only this API supports the
// RESOURCE_ID dimension — the regular GetCostAndUsage rejects it. Constraints
// imposed by AWS: granularity must be DAILY (not MONTHLY), the time window is
// capped at 14 days, and the customer's account must have opted in to
// "hourly granularity and resource-level data" in Cost Explorer; without
// opt-in the call returns DataUnavailableException and we fall through to
// the non-fatal path.
//
// Records returned have ResourceID populated, enabling Detect() to join with
// usage. This is supplemental data — failures are logged and swallowed.
func (c *Client) FetchResourceCosts(ctx context.Context, start, end time.Time) ([]model.CostRecord, error) {
	// Clamp the window to the API's 14-day cap. Also handle reversed inputs
	// (start after end) — the swallowed-error path would otherwise hide the
	// misconfiguration silently behind a "resource-level costs failed" warning.
	if start.After(end) || end.Sub(start) > resourceCostMaxLookback {
		start = end.Add(-resourceCostMaxLookback)
	}

	// Filter is required for GetCostAndUsageWithResources. Restrict to services
	// that reliably emit resource-level data.
	filter := &types.Expression{
		Dimensions: &types.DimensionValues{
			Key:    types.DimensionService,
			Values: resourceLevelServices,
		},
	}

	input := &costexplorer.GetCostAndUsageWithResourcesInput{
		TimePeriod: &types.DateInterval{
			Start: aws.String(start.Format(dateLayout)),
			End:   aws.String(end.Format(dateLayout)),
		},
		Granularity: types.GranularityMonthly,
		Metrics:     []string{"NetAmortizedCost"},
		Filter:      filter,
		GroupBy: []types.GroupDefinition{
			{Type: types.GroupDefinitionTypeDimension, Key: aws.String("SERVICE")},
			{Type: types.GroupDefinitionTypeDimension, Key: aws.String("RESOURCE_ID")},
		},
	}

	var records []model.CostRecord

	for {
		var page *costexplorer.GetCostAndUsageWithResourcesOutput
		err := retry.Do(ctx, retry.DefaultConfig(), func() error {
			var err error
			page, err = c.ce.GetCostAndUsageWithResources(ctx, input)
			return err
		})

		if err != nil {
			// Non-fatal: resource-level data is supplemental. Most common
			// cause in practice is the customer not having enabled
			// "hourly granularity and resource-level data" in Cost Explorer.
			slog.Warn("aws: FetchResourceCosts failed, continuing without resource-level costs", "error", err)
			return nil, nil
		}

		for _, result := range page.ResultsByTime {
			periodStart, _ := time.Parse(dateLayout, aws.ToString(result.TimePeriod.Start))
			periodEnd, _ := time.Parse(dateLayout, aws.ToString(result.TimePeriod.End))

			for _, group := range result.Groups {
				ceService := group.Keys[0]
				resourceID := group.Keys[1]

				if resourceID == "" || resourceID == "NoResourceId" {
					continue
				}

				metric := group.Metrics["NetAmortizedCost"]
				amount, _ := strconv.ParseFloat(aws.ToString(metric.Amount), 64)
				if amount <= 0 {
					continue
				}

				// Extract region and short resource ID from ARN if possible.
				region, shortID := parseResourceID(resourceID)

				records = append(records, model.CostRecord{
					Provider:    "aws",
					AccountID:   c.accountID,
					Service:     normalizeService(ceService),
					Region:      region,
					ResourceID:  shortID,
					Amount:      amount,
					Currency:    aws.ToString(metric.Unit),
					PeriodStart: periodStart,
					PeriodEnd:   periodEnd,
					FetchedAt:   time.Now().UTC(),
				})
			}
		}

		if page.NextPageToken == nil {
			break
		}
		input.NextPageToken = page.NextPageToken
	}

	slog.Info("aws: fetched resource-level costs", "count", len(records))
	return records, nil
}

// FetchCostExplorerAPICosts queries for Cost Explorer API charges.
// AWS bills these under "Amazon Cost Management APIs" in Cost Explorer.
// This is non-fatal — if unavailable, returns empty slice.
func (c *Client) FetchCostExplorerAPICosts(ctx context.Context, start, end time.Time) ([]model.CostRecord, error) {
	// Filter for Cost Management APIs service (includes Cost Explorer API charges)
	filter := &types.Expression{
		Dimensions: &types.DimensionValues{
			Key:    types.DimensionService,
			Values: []string{"Amazon Cost Management APIs"},
		},
	}

	input := &costexplorer.GetCostAndUsageInput{
		TimePeriod: &types.DateInterval{
			Start: aws.String(start.Format(dateLayout)),
			End:   aws.String(end.Format(dateLayout)),
		},
		Granularity: types.GranularityDaily,
		Metrics:     []string{"NetAmortizedCost"},
		Filter:      filter,
		GroupBy: []types.GroupDefinition{
			{Type: types.GroupDefinitionTypeDimension, Key: aws.String("SERVICE")},
			{Type: types.GroupDefinitionTypeDimension, Key: aws.String("REGION")},
		},
	}

	var records []model.CostRecord

	for {
		var page *costexplorer.GetCostAndUsageOutput
		err := retry.Do(ctx, retry.DefaultConfig(), func() error {
			var err error
			page, err = c.ce.GetCostAndUsage(ctx, input)
			return err
		})

		if err != nil {
			// Non-fatal: API cost tracking is supplemental
			slog.Warn("aws: FetchCostExplorerAPICosts failed, continuing without API cost data", "error", err)
			return nil, nil
		}

		for _, result := range page.ResultsByTime {
			periodStart, _ := time.Parse(dateLayout, aws.ToString(result.TimePeriod.Start))
			periodEnd, _ := time.Parse(dateLayout, aws.ToString(result.TimePeriod.End))

			for _, group := range result.Groups {
				region := group.Keys[1]

				metric := group.Metrics["NetAmortizedCost"]
				amount, _ := strconv.ParseFloat(aws.ToString(metric.Amount), 64)
				if amount <= 0 {
					continue
				}

				records = append(records, model.CostRecord{
					Provider:    "aws",
					AccountID:   c.accountID,
					Service:     "AWSCostExplorer", // Normalize to internal name (Amazon Cost Management APIs)
					Region:      region,
					Amount:      amount,
					Currency:    aws.ToString(metric.Unit),
					PeriodStart: periodStart,
					PeriodEnd:   periodEnd,
					FetchedAt:   time.Now().UTC(),
				})
			}
		}

		if page.NextPageToken == nil {
			break
		}
		input.NextPageToken = page.NextPageToken
	}

	if len(records) > 0 {
		slog.Info("aws: fetched Cost Explorer API costs", "count", len(records))
	}
	return records, nil
}

// parseResourceID extracts the region and short resource ID from an ARN or
// raw resource identifier returned by Cost Explorer.
// For ARNs like "arn:aws:ec2:us-east-1:123456789:instance/i-0abc123" it returns
// ("us-east-1", "i-0abc123"). For non-ARN values it returns ("", rawID).
func parseResourceID(raw string) (region, resourceID string) {
	if !strings.HasPrefix(raw, "arn:") {
		return "", raw
	}
	// ARN format: arn:partition:service:region:account:resource-type/resource-id
	parts := strings.SplitN(raw, ":", 8)
	if len(parts) < 6 {
		return "", raw
	}
	region = parts[3]

	// Resource ID is everything after the last "/" or ":" in the resource part.
	resource := strings.Join(parts[5:], ":")
	if idx := strings.LastIndex(resource, "/"); idx >= 0 {
		resourceID = resource[idx+1:]
	} else if idx := strings.LastIndex(resource, ":"); idx >= 0 {
		resourceID = resource[idx+1:]
	} else {
		resourceID = resource
	}

	if resourceID == "" {
		resourceID = raw
	}
	return region, resourceID
}
