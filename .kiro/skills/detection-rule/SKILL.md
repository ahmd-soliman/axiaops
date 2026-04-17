---
name: detection-rule
description: "Add a new zombie/idle resource detection rule to AxiaOps. Use this skill whenever someone wants to add support for detecting a new AWS service (e.g., EBS volumes, S3 buckets, ElastiCache), modify an existing detection threshold, or extend the analyzer with new resource types. Also trigger when the conversation mentions 'ghost detection', 'zombie rule', 'idle threshold', 'usage metric', or adding CloudWatch metrics for a new service."
---

# Detection Rule Skill

This skill walks you through adding a new zombie/idle resource detection rule to AxiaOps. Each rule maps an AWS service to a CloudWatch metric and a threshold — resources whose usage falls at or below that threshold are flagged as "ghosts" (idle/zombie resources still incurring costs).

## Before You Start

Read the existing rules to understand the pattern:

- `services/shared/analyzer/rules.go` — the `serviceRules` map
- `services/shared/analyzer/detector.go` — `Detect()`, `Summarize()`, `AnnotateAll()`
- `services/ingestion/internal/provider/aws/cloudwatch.go` — how metrics are fetched

The root `CLAUDE.md` has a table of current thresholds under "FinOps Domain Rules" — do not change existing thresholds without business justification.

## Steps to Add a New Rule

### 1. Add the rule to `serviceRules` in `services/shared/analyzer/rules.go`

Each entry in the map follows this structure:

```go
"AWSServiceName": {
    metric:    "CloudWatchMetricName",
    threshold: 0.0,              // flag if avg <= this value
    unit:      "Count",          // informational, matches CW unit
    reason:    "Human-readable explanation shown in the API response",
},
```

The map key must match exactly what AWS Cost Explorer returns in the `SERVICE` dimension (e.g., `"AmazonEC2"`, `"AmazonRDS"`, `"AWSLambda"`, `"AmazonElasticLoadBalancing"`, `"AmazonVPC"`).

To find the correct service name, check the AWS Cost Explorer `GetCostAndUsage` API docs or run a query against a real AWS account.

### 2. Verify the CloudWatch metric exists

The ingestion service fetches metrics via `cloudwatch.go`. Confirm that:

- The metric name you chose exists in the correct CloudWatch namespace
- The namespace mapping in `cloudwatch.go` covers the service (add it if missing)
- The dimension used to match the resource ID is correct

Common namespace → metric mappings:

| Service | Namespace | Metric | Dimension |
|---------|-----------|--------|-----------|
| EC2 | AWS/EC2 | CPUUtilization | InstanceId |
| RDS | AWS/RDS | DatabaseConnections | DBInstanceIdentifier |
| Lambda | AWS/Lambda | Invocations | FunctionName |
| ELB | AWS/ELB or AWS/ApplicationELB | RequestCount | LoadBalancerName |
| NAT GW | AWS/NATGateway | BytesOutToDestination | NatGatewayId |
| EBS | AWS/EBS | VolumeReadOps + VolumeWriteOps | VolumeId |
| ElastiCache | AWS/ElastiCache | CurrConnections | CacheClusterId |
| S3 | AWS/S3 | NumberOfObjects | BucketName |

### 3. Add resource discovery (if needed)

If the new service requires Describe API calls to map Cost Explorer line items to actual resource IDs, update `services/ingestion/internal/provider/aws/discover.go`. The existing pattern uses the AWS SDK v2 with pagination.

### 4. Write tests

Add test cases to `services/shared/analyzer/detector_test.go`:

```go
func TestDetect_NewService_ZeroUsage_FlagsGhost(t *testing.T) {
    costs := []model.CostRecord{costRecord("NewService", 50.0)}
    usage := []model.UsageRecord{usageRecord("NewService", "MetricName", 0.0)}
    ghosts := analyzer.Detect(costs, usage)
    if len(ghosts) != 1 {
        t.Fatalf("expected 1 ghost, got %d", len(ghosts))
    }
    if ghosts[0].Service != "NewService" {
        t.Errorf("expected service NewService, got %s", ghosts[0].Service)
    }
}

func TestDetect_NewService_AboveThreshold_NoGhost(t *testing.T) {
    costs := []model.CostRecord{costRecord("NewService", 50.0)}
    usage := []model.UsageRecord{usageRecord("NewService", "MetricName", 100.0)}
    ghosts := analyzer.Detect(costs, usage)
    if len(ghosts) != 0 {
        t.Fatalf("expected 0 ghosts, got %d", len(ghosts))
    }
}
```

Follow project conventions: black-box tests (`package analyzer_test`), no testify, use helper functions for fixture building.

### 5. Update the FinOps Domain Rules table

Add the new rule to the table in the root `CLAUDE.md` under "FinOps Domain Rules" so future contributors (and Claude) know about it.

### 6. Run tests

```bash
make test
```

All existing tests must continue to pass. The analyzer tests are pure functions with no external dependencies, so they run fast.

## Modifying an Existing Threshold

If changing an existing threshold (e.g., bumping EC2 CPU from 5% to 10%):

1. Update the `threshold` value in `serviceRules`
2. Update the `CLAUDE.md` domain rules table
3. Update any tests that assert the old threshold behavior
4. Document the business justification in the commit message

Threshold changes affect what gets flagged for every customer — be conservative and justify with data.
