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
	// Tier 3 — CloudWatch-based detection (Phase 2, April 2026)
	"AmazonCloudFront": {
		metric:    "Requests",
		threshold: 0.0,
		unit:      "Count",
		reason:    "CloudFront distribution has zero requests — likely abandoned",
	},
	"AmazonKinesis": {
		metric:    "IncomingRecords",
		threshold: 0.0,
		unit:      "Count",
		reason:    "Kinesis data stream has zero incoming records — likely unused",
	},
	// Requires S3 request metrics to be enabled on the bucket (not default).
	// Buckets without request metrics return no CloudWatch data and are skipped.
	"AmazonS3": {
		metric:    "AllRequests",
		threshold: 0.0,
		unit:      "Count",
		reason:    "S3 bucket has zero requests — likely abandoned (requires request metrics enabled)",
	},
}
