// Package analyzer detects zombie cloud resources by joining cost records with
// usage metrics and applying per-service threshold rules.
package analyzer

// rule defines the zombie detection threshold for a specific AWS service.
// A resource is flagged when its usage metric average falls at or below the
// threshold for the entire billing period.
type rule struct {
	metric    string  // CloudWatch metric name to check
	threshold float64 // flag if avg <= threshold
	unit      string  // expected unit (informational)
	reason    string  // human-readable explanation shown in the API response
}

// serviceRules maps AWS service names to their zombie-detection rules.
// Services not listed here are skipped (no usage data available in MVP).
var serviceRules = map[string]rule{
	// Tier 0 — CloudWatch-based detection (MVP)
	"AmazonEC2": {
		metric:    "CPUUtilization",
		threshold: 5.0,
		unit:      "Percent",
		reason:    "EC2 instance CPU utilization below 5% — likely idle",
	},
	"AmazonRDS": {
		metric:    "DatabaseConnections",
		threshold: 0.0,
		unit:      "Count",
		reason:    "RDS instance has zero database connections — likely abandoned",
	},
	"AWSLambda": {
		metric:    "Invocations",
		threshold: 0.0,
		unit:      "Count",
		reason:    "Lambda function has zero invocations — likely unused",
	},
	"AmazonElasticLoadBalancing": {
		metric:    "RequestCount",
		threshold: 0.0,
		unit:      "Count",
		reason:    "Load balancer has zero requests — likely abandoned",
	},
	"AmazonVPC": {
		metric:    "BytesOutToDestination",
		threshold: 0.0,
		unit:      "Bytes",
		reason:    "NAT Gateway has zero outbound bytes — likely unused",
	},
	// Tier 2 — CloudWatch-based detection (Phase 2)
	"AmazonElastiCache": {
		metric:    "CurrConnections",
		threshold: 0.0,
		unit:      "Count",
		reason:    "ElastiCache cluster has zero connections — likely idle",
	},
	"AmazonES": {
		metric:    "SearchRate",
		threshold: 0.0,
		unit:      "Count",
		reason:    "OpenSearch/Elasticsearch cluster has zero search rate — likely unused",
	},
	"AmazonRedshift": {
		metric:    "DatabaseConnections",
		threshold: 0.0,
		unit:      "Count",
		reason:    "Redshift cluster has zero database connections — likely abandoned",
	},
	"AmazonSageMaker": {
		metric:    "Invocations",
		threshold: 0.0,
		unit:      "Count",
		reason:    "SageMaker endpoint has zero invocations — likely forgotten",
	},
	"AmazonDynamoDB": {
		metric:    "ConsumedReadCapacityUnits",
		threshold: 0.0,
		unit:      "Count",
		reason:    "DynamoDB table (provisioned mode) has zero read capacity consumed — likely unused",
	},
	// Requires Container Insights enabled on the cluster (namespace: ContainerInsights).
	// Clusters without Container Insights return no CloudWatch data and are skipped.
	"AmazonEKS": {
		metric:    "cluster_node_count",
		threshold: 0.0,
		unit:      "Count",
		reason:    "EKS cluster has zero nodes — control plane ($73/mo) billing with no workload",
	},
	"AmazonECS": {
		metric:    "CPUUtilization",
		threshold: 2.0,
		unit:      "Percent",
		reason:    "ECS service CPU utilization below 2% — likely idle",
	},
	"AmazonDocDB": {
		metric:    "DatabaseConnections",
		threshold: 0.0,
		unit:      "Count",
		reason:    "DocumentDB cluster has zero active database connections — likely unused",
	},
	"AmazonMSK": {
		metric:    "MessagesInPerSec",
		threshold: 0.0,
		unit:      "Count",
		reason:    "MSK cluster has zero incoming messages — likely idle",
	},
	"AmazonBedrock": {
		metric:    "Invocations",
		threshold: 0.0,
		unit:      "Count",
		reason:    "Bedrock provisioned throughput model has zero invocations — likely unused ($10k+/mo leak)",
	},
	"AmazonKendra": {
		metric:    "SearchQueryCount",
		threshold: 0.0,
		unit:      "Count",
		reason:    "Kendra AI search index has zero search queries — likely abandoned ($810+/mo leak)",
	},
	// NOTE: CloudFront, Kinesis, and S3 use Tier 1-style direct detection
	// (DiscoverIdle* functions in ingestion/provider/aws/discover.go) instead
	// of flowing through Detect(). They are NOT in this map.
}

// ResourceType classifies a zombie into a stable snake_case sub-type slug from
// its (service, usageMetric) pair. A single AWS service spans several resource
// kinds (e.g. AmazonEC2 covers instances, volumes, snapshots, AMIs) that are
// disambiguated by the usage metric — the same logic migration 006 used to
// backfill historical rows. The sub-type drives the trend resource-type filter
// (zombie_snapshot_services.resource_type) and is shown on /zombies + /resources.
//
// Returns "" for an unrecognised pair; callers persist that as an empty
// resource_type, which the trend filter excludes — so a new detection that
// forgets to register its metric here degrades to "no sub-type" rather than a
// wrong one.
func ResourceType(service, usageMetric string) string {
	switch service {
	case "AmazonEC2":
		switch usageMetric {
		case "VolumeState":
			return "volume"
		case "SourceVolumeExists":
			return "snapshot"
		case "DaysStopped":
			return "stopped_instance"
		case "DaysSinceCreation":
			return "ami"
		case "CPUUtilization":
			return "instance"
		}
	case "AmazonVPC":
		switch usageMetric {
		case "NetworkInterfaceAttachment":
			return "eip"
		case "BytesOutToDestination":
			return "nat_gateway"
		}
	case "AmazonRDS":
		switch usageMetric {
		case "SourceDBExists":
			return "db_snapshot"
		case "DatabaseConnections":
			// Migration 006 used "primary" for the RDS instance; relabeled to the
			// clearer "db_instance". The old slug never reached the trend table
			// (006 only touched RDS in zombie_records/resource_records, which
			// SaveZombies overwrites per scan), so there's no split to reconcile.
			return "db_instance"
		}
	case "AmazonCloudWatch":
		if usageMetric == "RetentionDays" {
			return "log_group"
		}
	case "AmazonECR":
		if usageMetric == "StaleImageCount" {
			return "ecr_image"
		}
	case "AmazonCloudFront":
		if usageMetric == "Requests" {
			return "distribution"
		}
	case "AWSSecretsManager":
		if usageMetric == "DaysSinceAccess" {
			return "secret"
		}
	case "AmazonS3":
		switch usageMetric {
		case "AllRequests":
			return "bucket"
		case "MultipartUploads":
			return "s3_multipart"
		}
	case "AmazonKinesis":
		if usageMetric == "IncomingRecords" {
			return "stream"
		}
	case "AWSLambda":
		if usageMetric == "Invocations" {
			return "function"
		}
	case "AmazonElasticLoadBalancing":
		if usageMetric == "RequestCount" {
			return "load_balancer"
		}
	case "AmazonElastiCache":
		if usageMetric == "CurrConnections" {
			return "node"
		}
	case "AmazonES":
		if usageMetric == "SearchRate" {
			return "domain"
		}
	case "AmazonRedshift":
		if usageMetric == "DatabaseConnections" {
			return "cluster"
		}
	case "AmazonSageMaker":
		if usageMetric == "Invocations" {
			return "endpoint"
		}
	case "AmazonDynamoDB":
		if usageMetric == "ConsumedReadCapacityUnits" {
			return "table"
		}
	case "AmazonEKS":
		if usageMetric == "cluster_node_count" {
			return "cluster"
		}
	case "AmazonECS":
		if usageMetric == "CPUUtilization" {
			return "ecs_service"
		}
	case "AmazonDocDB":
		if usageMetric == "DatabaseConnections" {
			return "docdb_cluster"
		}
	case "AmazonMSK":
		if usageMetric == "MessagesInPerSec" {
			return "msk_cluster"
		}
	case "AmazonRoute53":
		if usageMetric == "QueryCount" {
			return "route53_zone"
		}
	case "AmazonBedrock":
		if usageMetric == "Invocations" {
			return "bedrock_throughput"
		}
	case "AmazonKendra":
		if usageMetric == "SearchQueryCount" {
			return "kendra_index"
		}
	}
	return ""
}
