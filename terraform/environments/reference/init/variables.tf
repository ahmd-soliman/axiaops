variable "env_name" {
  description = "Environment name (e.g. prod). Used in resource naming so the same `init` module shape can be re-applied for a future staging env without collisions."
  type        = string
  default     = "prod"
}

variable "region" {
  description = "AWS region."
  type        = string
  default     = "eu-central-1"
}

variable "account_id" {
  description = "AWS account ID. No default — must be set explicitly."
  type        = string
}

variable "github_repository" {
  description = "Fully-qualified GitHub repository (\"owner/repo\") that drives this stack via CI. Used in the OIDC trust policy `sub` claim restriction. No default — must be set explicitly, or the trust policy below has nothing to scope itself to."
  type        = string
}

variable "github_apply_refs" {
  description = "GitHub Actions OIDC `sub` claim patterns whose runs are allowed to assume the broad terraform-apply role. Production-safe default: `main` branch pushes only. See https://docs.github.com/en/actions/deployment/security-hardening-your-deployments/about-security-hardening-with-openid-connect for the sub-claim format."
  type        = list(string)
  default     = ["ref:refs/heads/main"]
}

variable "github_plan_refs" {
  description = "GitHub Actions OIDC `sub` claim patterns allowed to assume the read-only plan role. Includes `pull_request` so reviewers see the plan diff on PR review."
  type        = list(string)
  default     = ["ref:refs/heads/main", "pull_request"]
}

variable "state_bucket_name" {
  description = "Name of the S3 bucket holding Terraform remote state. No default."
  type        = string
}

variable "state_lock_table_name" {
  description = "DynamoDB state-lock table name. No default."
  type        = string
}
