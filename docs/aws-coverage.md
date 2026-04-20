# AWS Service Coverage

AxiaOps detects idle and zombie resources across **27 detection rules** covering **18 AWS services**.

Detection is split into two tiers:

- **Tier 1 (CloudWatch-based)** — Joins Cost Explorer billing data with CloudWatch metrics. If a resource has cost but its usage metric is at or below the threshold, it's flagged as a ghost.
- **Tier 2 (API-only)** — Uses Describe APIs directly. The resource's state alone determines it's a zombie (e.g., an unattached EBS volume is always waste).

---

## Tier 1 — CloudWatch-Based Detection

| # | AWS Service | CloudWatch Metric | Namespace | Threshold | Verdict |
|---|-------------|-------------------|-----------|-----------|---------|
| 1 | AmazonEC2 | CPUUtilization | AWS/EC2 | ≤ 5% | Idle instance |
| 2 | AmazonRDS | DatabaseConnections | AWS/RDS | = 0 | Abandoned database |
| 3 | AWSLambda | Invocations | AWS/Lambda | = 0 | Unused function |
| 4 | Elastic Load Balancing | RequestCount | AWS/ApplicationELB | = 0 | Abandoned load balancer |
| 5 | AmazonVPC (NAT Gateway) | BytesOutToDestination | AWS/NATGateway | = 0 | Unused NAT Gateway |
| 6 | AmazonElastiCache | CurrConnections | AWS/ElastiCache | = 0 | Idle cache cluster |
| 7 | AmazonES (OpenSearch) | SearchRate | AWS/ES | = 0 | Unused search cluster |
| 8 | AmazonRedshift | DatabaseConnections | AWS/Redshift | = 0 | Abandoned data warehouse |
| 9 | AmazonSageMaker | Invocations | AWS/SageMaker | = 0 | Forgotten endpoint |
| 10 | AmazonDynamoDB | ConsumedReadCapacityUnits | AWS/DynamoDB | = 0 | Unused table (provisioned mode) |
| 11 | AmazonEKS | cluster_node_count | ContainerInsights | = 0 | Empty cluster ($73/mo control plane) |
| 12 | AmazonCloudFront | Requests | AWS/CloudFront | = 0 | Abandoned distribution |
| 13 | AmazonKinesis | IncomingRecords | AWS/Kinesis | = 0 | Unused data stream |
| 14 | AmazonS3 | AllRequests | AWS/S3 | = 0 | Abandoned bucket |

> **Note:** EKS detection requires Container Insights enabled on the cluster.
> **Note:** S3 detection requires S3 request metrics to be enabled on the bucket (not default).

---

## Tier 2 — API-Only Detection

| # | Resource Type | AWS API | Condition | Verdict | Est. Cost |
|---|---------------|---------|-----------|---------|-----------|
| 12 | Elastic IP | ec2:DescribeAddresses | Not attached to any ENI | Unattached EIP | $3.60/mo |
| 13 | EBS Volume | ec2:DescribeVolumes | state = "available" | Unattached volume | $0.08/GB-mo |
| 14 | EBS Snapshot | ec2:DescribeSnapshots + DescribeImages | Source volume deleted, not backing any AMI | Orphaned snapshot | $0.05/GB-mo |
| 15 | EC2 Instance (stopped) | ec2:DescribeInstances | Stopped > 30 days | Long-stopped instance | $0.08/GB-mo (attached EBS) |
| 16 | AMI | ec2:DescribeImages + DescribeInstances | Age > 90 days, no instance references it | Unused AMI | $0.05/GB-mo (backing snapshots) |
| 17 | Cost Anomaly Monitor | ce:GetAnomalyMonitors + GetAnomalies | Paid monitor with 0 anomalies in lookback window | Idle anomaly monitor | ~$3.00/mo |
| 18 | CloudWatch Log Group | logs:DescribeLogGroups | No retention policy set (logs stored forever) | Wasteful log group | $0.03/GB-mo |
| 19 | RDS Snapshot (manual) | rds:DescribeDBSnapshots | Age > 30 days, source DB deleted | Orphaned RDS snapshot | $0.095/GB-mo |
| 20 | ECR Repository | ecr:DescribeRepositories + ListImages | Untagged images or images > 90 days old | Stale container images | $0.10/GB-mo |
| 21 | Secrets Manager | secretsmanager:ListSecrets | LastAccessedDate > 90 days | Unused secret | $0.40/secret-mo |

---

## Required IAM Permissions

```json
{
  "Version": "2012-10-17",
  "Statement": [
    {
      "Effect": "Allow",
      "Action": [
        "ce:GetCostAndUsage",
        "ce:GetAnomalyMonitors",
        "ce:GetAnomalies",
        "cloudwatch:GetMetricStatistics",
        "ec2:DescribeInstances",
        "ec2:DescribeAddresses",
        "ec2:DescribeVolumes",
        "ec2:DescribeSnapshots",
        "ec2:DescribeImages",
        "ec2:DescribeNatGateways",
        "rds:DescribeDBInstances",
        "lambda:ListFunctions",
        "elasticloadbalancing:DescribeLoadBalancers",
        "elasticache:DescribeCacheClusters",
        "es:ListDomainNames",
        "redshift:DescribeClusters",
        "sagemaker:ListEndpoints",
        "dynamodb:ListTables",
        "eks:ListClusters",
        "sts:GetCallerIdentity",
        "logs:DescribeLogGroups",
        "rds:DescribeDBSnapshots",
        "rds:DescribeDBInstances",
        "ecr:DescribeRepositories",
        "ecr:DescribeImages",
        "secretsmanager:ListSecrets",
        "cloudfront:ListDistributions",
        "kinesis:ListStreams",
        "s3:ListAllMyBuckets"
      ],
      "Resource": "*"
    }
  ]
}
```

---

## Source Files

| File | Purpose |
|------|---------|
| `services/shared/analyzer/rules.go` | Tier 1 threshold definitions (`serviceRules` map) |
| `services/shared/analyzer/detector.go` | Core detection logic (`Detect()`, `Summarize()`) |
| `services/ingestion/internal/provider/aws/cloudwatch.go` | CloudWatch metric mapping and fetching |
| `services/ingestion/internal/provider/aws/discover.go` | All resource discovery (Tier 1 + Tier 2) |
| `services/ingestion/cmd/main.go` | Ingestion orchestration (`runIngestionCore()`) |

---

## Detection Flow

```
POST /scan → Fetch costs (Cost Explorer)
           → Discover resources (per-service Describe APIs)
           → Fetch usage (CloudWatch GetMetricStatistics)
           → Detect Tier 1 ghosts (cost + usage join, apply thresholds)
           → Discover Tier 2 ghosts (API-only state checks)
           → Combine, summarize, save to PostgreSQL
```
