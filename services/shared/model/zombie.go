// Package model defines shared data structures used across the ingestion
// and analysis layers.
package model

import (
	"fmt"
	"strings"
	"time"
)

// ZombieResource represents a cloud resource that is incurring cost but shows
// no meaningful usage — a zombie resource that is safe to review for removal.
type ZombieResource struct {
	Provider          string            `json:"provider"`
	AccountID         string            `json:"account_id"`          // AWS account number, GCP project, etc.
	InternalAccountID string            `json:"internal_account_id"` // UUID from accounts table
	Service           string            `json:"service"`
	ResourceType      string            `json:"resource_type,omitempty"` // sub-classification (e.g. "volume", "snapshot", "ami")
	Region            string            `json:"region"`
	ResourceID        string            `json:"resource_id"`
	ARN               string            `json:"arn,omitempty"` // Amazon Resource Name (AWS only)
	Tags              map[string]string `json:"tags"`

	// Cost fields
	MonthlyCost float64   `json:"monthly_cost"`
	CostBasis        string       `json:"cost_basis"`
	Currency    string    `json:"currency"`
	PeriodStart time.Time `json:"period_start"`
	PeriodEnd   time.Time `json:"period_end"`

	// Usage fields
	UsageMetric string  `json:"usage_metric"` // e.g. "CPUUtilization"
	UsageAvg    float64 `json:"usage_avg"`    // average value over the period
	UsageUnit   string  `json:"usage_unit"`   // e.g. "Percent", "Count"

	// Detection metadata
	Reason string `json:"reason"` // human-readable explanation
	Owner  string `json:"owner"`  // derived from tags["team"]

	// Dismissal state — enriched at read time by the API handler.
	// Zero values mean the resource is not dismissed/snoozed.
	DismissalID   *int64     `json:"dismissal_id,omitempty"`
	DismissAction string     `json:"dismiss_action,omitempty"` // "dismiss" | "snooze"
	DismissReason string     `json:"dismiss_reason,omitempty"`
	DismissNote   string     `json:"dismiss_note,omitempty"`
	SnoozedUntil  *time.Time `json:"snoozed_until,omitempty"`
}

// BuildARN constructs an Amazon Resource Name for AWS resources.
// Returns empty string for non-AWS providers or unsupported services.
func BuildARN(provider, accountID, region, service, resourceID string) string {
	if provider != "aws" {
		return ""
	}

	var arnService, resourcePath string

	switch service {
	case "AmazonEC2":
		arnService = "ec2"
		if strings.HasPrefix(resourceID, "i-") {
			resourcePath = fmt.Sprintf("instance/%s", resourceID)
		} else if strings.HasPrefix(resourceID, "eipalloc-") {
			resourcePath = fmt.Sprintf("elastic-ip/%s", resourceID)
		} else if strings.HasPrefix(resourceID, "vol-") {
			resourcePath = fmt.Sprintf("volume/%s", resourceID)
		} else if strings.HasPrefix(resourceID, "snap-") {
			resourcePath = fmt.Sprintf("snapshot/%s", resourceID)
		}
	case "AmazonVPC":
		arnService = "ec2"
		if strings.HasPrefix(resourceID, "nat-") {
			resourcePath = fmt.Sprintf("natgateway/%s", resourceID)
		} else if strings.HasPrefix(resourceID, "eipalloc-") {
			resourcePath = fmt.Sprintf("elastic-ip/%s", resourceID)
		} else if strings.HasPrefix(resourceID, "vpc-") {
			resourcePath = fmt.Sprintf("vpc/%s", resourceID)
		}
	case "AmazonRDS":
		arnService = "rds"
		resourcePath = fmt.Sprintf("db:%s", resourceID)
	case "AWSLambda":
		arnService = "lambda"
		resourcePath = fmt.Sprintf("function:%s", resourceID)
	case "AmazonElasticLoadBalancing":
		arnService = "elasticloadbalancing"
		resourcePath = fmt.Sprintf("loadbalancer/%s", resourceID)
	case "AmazonS3":
		arnService = "s3"
		resourcePath = resourceID
	case "AmazonCloudFront":
		arnService = "cloudfront"
		resourcePath = fmt.Sprintf("distribution/%s", resourceID)
	case "AmazonDynamoDB":
		arnService = "dynamodb"
		resourcePath = fmt.Sprintf("table/%s", resourceID)
	case "AmazonElastiCache":
		arnService = "elasticache"
		resourcePath = fmt.Sprintf("cluster:%s", resourceID)
	case "AmazonRedshift":
		arnService = "redshift"
		resourcePath = fmt.Sprintf("cluster:%s", resourceID)
	case "AmazonSNS":
		arnService = "sns"
		if strings.HasPrefix(resourceID, "arn:") {
			return resourceID
		}
		resourcePath = resourceID
	case "AmazonSQS":
		arnService = "sqs"
		if strings.HasPrefix(resourceID, "arn:") {
			return resourceID
		}
		resourcePath = resourceID
	case "AmazonECS":
		arnService = "ecs"
		resourcePath = fmt.Sprintf("cluster/%s", resourceID)
	case "AmazonEKS":
		arnService = "eks"
		resourcePath = fmt.Sprintf("cluster/%s", resourceID)
	default:
		return ""
	}

	if resourcePath == "" {
		return ""
	}

	return fmt.Sprintf("arn:aws:%s:%s:%s:%s", arnService, region, accountID, resourcePath)
}
