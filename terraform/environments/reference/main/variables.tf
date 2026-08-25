variable "env_name" {
  description = "Environment name used for namespacing."
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
  description = "Fully-qualified GitHub repository (\"owner/repo\") whose CI is allowed to assume the app-deploy role. Used to build the OIDC trust policy's sub-claim allowlist (see local.github_deploy_subs). No default."
  type        = string
}

variable "github_deploy_refs" {
  description = "GitHub Actions OIDC sub-claim suffixes (without the repo:<owner>/<repo>: prefix) allowed to assume the app-deploy role."
  type        = list(string)
  default     = ["ref:refs/heads/main", "ref:refs/tags/*"]
}

variable "domain_name" {
  description = "Apex domain. DNS managed via Route 53 (see providers.tf) — must already be a hosted zone in this account. No default."
  type        = string
}

variable "app_subdomain" {
  description = "Dashboard subdomain."
  type        = string
  default     = "app"
}

variable "dashboard_bucket_name" {
  description = "S3 bucket holding the dashboard SPA bundle. No default — bucket names are globally unique, pick one."
  type        = string
}

variable "state_bucket_name" {
  description = "Terraform state bucket name (from bootstrap). Used by `data \"terraform_remote_state\" \"init\"` to read init's outputs. No default."
  type        = string
}

# Used by aws_ssm_parameter.deployed_version (deployed_version.tf) — single
# source of truth for "what infra version is currently deployed." CI sets
# this on every pipeline via .tf-aws.before_script's
# `TF_VAR_deployed_version="${CI_COMMIT_TAG:-$CI_COMMIT_SHORT_SHA}"`.
# Operator laptops without TF_VAR_deployed_version set will see the
# "uninitialized" default — audit hint that the apply didn't come from CI.
variable "deployed_version" {
  description = "Git tag or short SHA stamped into /axiaops/prod/infra/DEPLOYED_VERSION on every apply. CI passes $CI_COMMIT_TAG (tag pipelines) or $CI_COMMIT_SHORT_SHA (everything else). Operator laptops can override with TF_VAR_deployed_version=$(git describe --tags --always --dirty)."
  type        = string
  default     = "uninitialized"
}

variable "cloudfront_distribution_id" {
  description = "CloudFront distribution ID for tightening the CI role's invalidate policy. Set AFTER `module.edge` has been applied (two-pass tightening — empty on first apply, wildcard invalidate scope until then)."
  type        = string
  default     = ""
}

# ---- Lifecycle / destroy flags ----------------------------------------------
# All default to production-safe (protection on, no force-destroy). Flip them
# all in `terraform.tfvars.test` (see terraform.tfvars.test.example) for a
# short-lived test environment you intend to tear down via `terraform destroy`.

variable "deletion_protection" {
  description = "RDS deletion_protection. Default true."
  type        = bool
  default     = true
}

variable "skip_final_snapshot" {
  description = "Skip the final RDS snapshot on destroy. Default false."
  type        = bool
  default     = false
}

variable "ecr_force_delete" {
  description = "Allow ECR repos to be destroyed even when images are pushed. Default false."
  type        = bool
  default     = false
}

variable "dashboard_bucket_force_destroy" {
  description = "Allow the dashboard S3 bucket to be destroyed even when it has objects. Default false."
  type        = bool
  default     = false
}

variable "ops_rds_access_enabled" {
  description = "Provision the EC2 Instance Connect Endpoint for operator psql/DBeaver sessions against RDS. Default true: EICE has no idle hourly cost (AWS bills only per-GB transferred during an open tunnel), so leaving it on costs effectively nothing. Flip to false if security policy mandates the path be torn down between sessions; the module leaves no residue when disabled. See docs/rds-shell-access.md."
  type        = bool
  default     = true
}

# ---- RDS major-version upgrade flags ---------------------------------------
# These exist so an operator can stage a major-version upgrade in TWO applies
# without permanent code change: (1) apply with both true → upgrade runs;
# (2) apply with both false → safety latches re-engaged. The module defaults
# are both false; ship the temporary `true` via TF_VAR_* at apply time, not
# in this file. See docs/rds-major-version-upgrade.md.

variable "rds_allow_major_version_upgrade" {
  description = "Permit a cross-major engine_version bump on the next apply. Default false (safety latch). Flip to true via TF_VAR_rds_allow_major_version_upgrade=true ONLY in the apply that runs the upgrade; revert on the very next apply."
  type        = bool
  default     = false
}

variable "rds_apply_immediately" {
  description = "Apply RDS modifications immediately rather than queuing them for the Sun 03:30-04:30 UTC maintenance window. Default false. Flip to true via TF_VAR_rds_apply_immediately=true for the upgrade apply so the engine bump doesn't sit gated for up to a week."
  type        = bool
  default     = false
}

# ---- Sensitive inputs supplied via TF_VAR_* at apply time -------------------
# These are NEVER stored in terraform.tfvars (which is committed). The CI/local
# apply caller is responsible for setting them via the environment.
#
# Note: ENCRYPTION_KEY is intentionally NOT here. The SSM resource shape is TF-managed (lifecycle.ignore_changes on
# value) and the operator writes the real key OOB via `aws ssm put-parameter`.
# The key never flows through TF variables, TF state, or CI variables.

# C-1 HMAC: §8.5 specifies TF generates the initial value via random_id and
# passes the same value into all three §8.3 variables in steady state. Leave
# these unset to use the auto-generated value; override during a rotation per
# the §13.2 playbook.
variable "api_ingestion_secret_override" {
  description = "Override the api-side HMAC signer slot. Empty → use the TF-generated random_id (steady state)."
  type        = string
  default     = ""
  sensitive   = true
}

variable "ingestion_primary_secret_override" {
  description = "Override the ingestion verifier primary slot. Empty → match api signer (steady state)."
  type        = string
  default     = ""
  sensitive   = true
}

variable "ingestion_next_secret_override" {
  description = "Override the ingestion verifier staging slot. Empty → match api signer (defensive duplicate per §8.3)."
  type        = string
  default     = ""
  sensitive   = true
}
