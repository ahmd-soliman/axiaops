package model

// KnownServices is the canonical set of internal service identifiers AxiaOps
// recognises across CostRecord, ZombieResource, ResourceRecord, and the
// detector. Any string written into CostRecord.Service or
// ZombieResource.Service must be a member of this set.
//
// Sources of truth this set unifies:
//   - Values of ceServiceToInternal in services/ingestion/internal/provider/aws/aws.go
//     (Cost Explorer service-name normalisation).
//   - Service literals emitted by Tier 1 discover_*.go functions
//     (AmazonCloudFront, AmazonECR, AmazonKinesis, AWSSecretsManager).
//   - Service cases handled by BuildARN.
//
// When adding a new service, register it here first — Validate() rejects
// unknown services, so an unregistered name causes loud test failures
// instead of silently-dropped rows.
var KnownServices = map[string]struct{}{
	// Tier 0 / Tier 2 — services flagged via CloudWatch metrics
	"AmazonEC2":                  {},
	"AmazonRDS":                  {},
	"AWSLambda":                  {},
	"AmazonElasticLoadBalancing": {},
	"AmazonVPC":                  {},
	"AmazonElastiCache":          {},
	"AmazonES":                   {},
	"AmazonRedshift":             {},
	"AmazonSageMaker":            {},
	"AmazonDynamoDB":             {},
	"AmazonEKS":                  {},

	// Tier 1 — services flagged via Describe APIs
	"AmazonCloudFront":  {},
	"AmazonECR":         {},
	"AmazonKinesis":     {},
	"AmazonS3":          {},
	"AWSSecretsManager": {},
	"AmazonCloudWatch":  {},

	// Cost-only line items — appear in CostRecord but never zombies
	"AWSCostExplorer":    {},
	"AWSDataTransfer":    {},
	"AWSGlue":            {},
	"AmazonSNS":          {},
	"AmazonSQS":          {},
	"AWSKms":             {},
	"AmazonGlacier":      {},
	"AWSCloudFormation":  {},
	"AmazonECS":          {},
	// Tax is its own CUR/CE line-item type (line_item_line_item_type = "Tax"
	// / Cost Explorer's own "Tax" service dimension), never a real AWS
	// service — surfaced as its own cost line for invoice reconciliation,
	// deliberately excluded from analyzer.serviceRules (no rule will ever
	// match it, so it can never become a zombie finding) and from every
	// amortization/optimization calculation upstream.
	"Tax": {},
}

// IsKnownService reports whether s is registered in KnownServices.
func IsKnownService(s string) bool {
	_, ok := KnownServices[s]
	return ok
}

// AWSRegionLike reports whether s looks like a real AWS region identifier
// (e.g. "us-east-1", "eu-central-1") rather than a Cost Explorer pseudo-value
// like "global" or "NoRegion". Real AWS regions always end in a digit and
// contain at least two hyphens.
//
// Mirrored from services/ingestion/internal/provider/aws/discover.go to keep
// the model package free of the AWS SDK import. If you change one, change
// both.
func AWSRegionLike(s string) bool {
	if len(s) < 5 {
		return false
	}
	last := s[len(s)-1]
	if last < '0' || last > '9' {
		return false
	}
	hyphens := 0
	for i := 0; i < len(s); i++ {
		if s[i] == '-' {
			hyphens++
		}
	}
	return hyphens >= 2
}

// CEPseudoRegions are the non-AWS-region values Cost Explorer can return for
// the REGION group key. They are valid in CostRecord even though they will
// be filtered out before resource discovery.
var CEPseudoRegions = map[string]struct{}{
	"":         {}, // CE returns empty region for some account-level rows
	"global":   {},
	"NoRegion": {},
}
