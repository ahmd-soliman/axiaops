package api

// generalScanPermissions are the read-only Describe/List actions every
// connected account needs for resource discovery and CloudWatch usage,
// regardless of billing source. This is the single source of truth for two
// consumers that used to be hand-maintained, separately, in two languages:
// the CUR-setup CloudFormation template's AxiaOpsPolicy resource
// (templates/cur_setup.yaml.tmpl) and the manual access-key setup policy
// the dashboard displays (GET /v1/scan-permissions). They drifted before —
// the dashboard's copy never even had the Athena/Glue statement below.
var generalScanPermissions = []string{
	"sts:GetCallerIdentity",
	"ce:GetCostAndUsage",
	"ce:GetCostAndUsageWithResources",
	"cloudwatch:GetMetricStatistics",
	"ec2:DescribeInstances",
	"ec2:DescribeVolumes",
	"ec2:DescribeSnapshots",
	"ec2:DescribeImages",
	"ec2:DescribeAddresses",
	"ec2:DescribeNatGateways",
	"rds:DescribeDBInstances",
	"rds:DescribeDBSnapshots",
	"lambda:ListFunctions",
	"elasticloadbalancing:DescribeLoadBalancers",
	"logs:DescribeLogGroups",
	"ecr:DescribeRepositories",
	"ecr:DescribeImages",
	"secretsmanager:ListSecrets",
	"elasticache:DescribeCacheClusters",
	"es:ListDomainNames",
	"redshift:DescribeClusters",
	"sagemaker:ListEndpoints",
	"dynamodb:ListTables",
	"kinesis:ListStreams",
	"kinesis:DescribeStreamSummary",
	"cloudfront:ListDistributions",
	"eks:ListClusters",
	"s3:ListAllMyBuckets",
	"s3:GetBucketLocation",
	// Backs DiscoverIncompleteMultipartUploads (discover_s3.go) — stale/
	// incomplete multipart uploads bill storage but never show up in a
	// normal object listing. Without this the check AccessDenied's on every
	// bucket, every scan, for zero possible result.
	"s3:ListBucketMultipartUploads",
	// Backs DiscoverUnusedHostedZones (discover_route53.go) — flags Route53
	// hosted zones with only default NS/SOA records ($0.50/mo each) as
	// zombies. Without this the check AccessDenied's every scan; the
	// handler logs a warning and returns (nil, nil) rather than failing the
	// whole scan, so the gap was silent.
	"route53:ListHostedZones",
	// Backs discoverECS (discover_ecs.go), part of the general
	// resource-discovery pipeline (discover.go) that feeds CloudWatch
	// usage checks for AmazonECS cost records. Both actions are needed:
	// ListClusters enumerates clusters, ListServices (called per cluster
	// right after) enumerates the services within each one. Surfaced by a
	// scan against a real account with real ECS spend — the gap was silent
	// on every account without ECS usage in its scan window.
	"ecs:ListClusters",
	"ecs:ListServices",
}

// curAthenaPermissions are the additional actions a cur_athena billing
// account needs on top of generalScanPermissions, to run the amortization
// query against the customer's own Glue/Athena catalog. GetPartition and
// BatchGetPartition (singular/batch, alongside the plural GetPartitions)
// were missing here until a real scan against a real IAM policy — as
// opposed to an admin-equivalent test credential — surfaced the gap.
var curAthenaPermissions = []string{
	"athena:StartQueryExecution",
	"athena:GetQueryExecution",
	"athena:GetQueryResults",
	"athena:GetWorkGroup",
	"glue:GetDatabase",
	"glue:GetTable",
	"glue:GetPartitions",
	"glue:GetPartition",
	"glue:BatchGetPartition",
}

// Default Glue/Athena resource names the CUR-setup CloudFormation template
// (templates/cur_setup.yaml.tmpl) creates, and the account defaults applied
// when billing_source=cur_athena is chosen. createAccount (handler.go) and
// createDraftAccount (account_role.go) both need these and used to carry
// separate copies of the same literals — exactly the drift this file exists
// to prevent for the permission lists above. placeholderCURResultsS3 is
// never a real bucket; callers must leave the account in
// AccountStatusPendingCURDelivery until it's replaced via PATCH.
const (
	defaultCURDatabase      = "axiaops_cur_db"
	defaultCURTable         = "axiaops_cur_table"
	defaultCURWorkgroup     = "axiaops_athena_wg"
	defaultCURRegion        = "us-east-1"
	defaultCURRoleName      = "AxiaOpsRole"
	placeholderCURResultsS3 = "s3://axiaops-athena-results-placeholder"
)

// scanPermissionsPolicy builds the IAM policy document a customer should
// attach to the IAM user (access-key mode) or role (CFN-managed mode)
// AxiaOps uses to scan their account. includeCURAthena adds the Athena/Glue
// statement — scoped to Resource '*' here, unlike the CFN template's own
// AxiaOpsPolicy, which additionally scopes an S3 statement to the exact
// buckets it just created — because this document is served before any
// account-specific bucket names exist. A customer who already knows their
// CUR/results bucket ARNs can tighten the equivalent S3 actions themselves.
func scanPermissionsPolicy(includeCURAthena bool) map[string]any {
	statements := []map[string]any{
		{
			"Effect":   "Allow",
			"Action":   generalScanPermissions,
			"Resource": "*",
		},
	}
	if includeCURAthena {
		statements = append(statements, map[string]any{
			"Effect":   "Allow",
			"Action":   curAthenaPermissions,
			"Resource": "*",
		})
	}
	return map[string]any{
		"Version":   "2012-10-17",
		"Statement": statements,
	}
}
