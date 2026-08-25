variable "env_name" {
  description = "Environment name."
  type        = string
}

variable "region" {
  description = "AWS region."
  type        = string
  default     = "eu-central-1"
}

variable "account_id" {
  description = "AWS account ID."
  type        = string
}

variable "github_oidc_provider_arn" {
  description = "ARN of the GitHub Actions OIDC provider in IAM. Provisioned by environments/reference/init/ — passed into this module as input. The OIDC IdP registration itself is a Tier 1 bootstrap concern owned by init/."
  type        = string
}

variable "github_allowed_refs" {
  description = "GitHub Actions OIDC sub-claim patterns (already including the repo:<owner>/<repo>: prefix) allowed to assume the application repo's app-deploy role. main-branch pushes only by design."
  type        = list(string)
}

variable "dashboard_bucket_name" {
  description = "S3 dashboard bucket name (e.g. axiaops-dashboard-prod). Drives the CI role's least-privilege S3 ARN."
  type        = string
}

variable "cloudfront_distribution_id" {
  description = "CloudFront distribution ID (from edge). Empty on first-apply when edge has not yet been provisioned — the CI role's CloudFront invalidate policy gets created with a placeholder and tightened on a later apply."
  type        = string
  default     = ""
}

# The two ECR repo names are deterministic by design — repo ARNs are
# constructed inside the module from name + account + region to break the
# iam <-> compute apply-order dependency.
variable "ecr_repository_names" {
  description = "ECR repository names that CI may push to."
  type        = list(string)
  default     = ["axiaops-api", "axiaops-ingestion", "axiaops-migrate"]
}
