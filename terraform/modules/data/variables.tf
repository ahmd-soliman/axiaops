variable "env_name" {
  description = "Environment name used as a resource-identifier prefix."
  type        = string
}

variable "public_subnet_ids" {
  description = "Public subnet IDs (one per AZ) for the DB subnet group."
  type        = list(string)
}

variable "rds_sg_id" {
  description = "Security group attached to the RDS instance."
  type        = string
}

variable "owner_password" {
  description = "RDS master user password from secrets-passwords."
  type        = string
  sensitive   = true
}

variable "engine_version" {
  description = "PostgreSQL engine version. Pin to a specific minor (not major-only) so prod upgrades are intentional; bump when AWS deprecates the current pin (RDS retires older minors on a ~12-month cadence). Keep major aligned with axiaops's docker-compose Postgres image — major-version skew between prod and dev/staging breaks RLS-heavy queries' planner equivalence. Cross-major bumps additionally require `allow_major_version_upgrade = true` and a matching `parameter_group_family` (postgresN where N is the target major)."
  type        = string
  # AWS auto-applied a minor upgrade (auto_minor_version_upgrade) — live prod RDS
  # is 17.9, so the previous 17.5 pin made plan attempt a downgrade ("Cannot
  # upgrade postgres from 17.9 to 17.5"). Realign the pin to reality; same major
  # (postgres17 family unchanged, no allow_major_version_upgrade needed).
  default = "17.9"
}

variable "parameter_group_family" {
  description = "DB parameter-group family. Must match the major of `engine_version` (`postgres16` for 16.x, `postgres17` for 17.x, etc.). Encoded into the parameter group name so cross-major bumps replace the resource cleanly instead of deadlocking on the name-pinned destroy."
  type        = string
  default     = "postgres17"
}

variable "allow_major_version_upgrade" {
  description = "Permit a cross-major engine_version bump on the next apply. Default false (safety latch). Flip to true ONLY in the apply that runs the upgrade, then back to false on the next apply. The provider rejects an engine_version change that crosses majors unless this is true at plan time."
  type        = bool
  default     = false
}

variable "apply_immediately" {
  description = "Apply RDS modifications immediately rather than queuing them for the maintenance window (Sun 03:30-04:30 UTC). Default false — most changes can wait for the window. Flip to true for major-version upgrades and engine_version bumps so the apply doesn't sit gated for up to a week."
  type        = bool
  default     = false
}

variable "instance_class" {
  description = "RDS instance class."
  type        = string
  default     = "db.t4g.micro"
}

variable "allocated_storage_gb" {
  description = "Allocated storage in GB."
  type        = number
  default     = 20
}

variable "backup_retention_days" {
  description = "Automated backup retention in days."
  type        = number
  default     = 7
}

variable "db_name" {
  description = "Initial Postgres database name."
  type        = string
  default     = "axiaops"
}

variable "master_username" {
  description = "RDS master username (owns the schema)."
  type        = string
  default     = "axiaops_owner"
}

variable "deletion_protection" {
  description = "RDS deletion_protection. Default true (production-safe). Set false for short-lived test environments you intend to destroy via `terraform destroy`."
  type        = bool
  default     = true
}

variable "skip_final_snapshot" {
  description = "Skip the final RDS snapshot on destroy. Default false (production-safe — destroy creates `axiaops-<env>-final-snapshot` you can restore from). Set true alongside deletion_protection=false for clean teardown that leaves no storage residue."
  type        = bool
  default     = false
}
