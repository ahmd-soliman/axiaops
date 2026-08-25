variable "env_name" {
  description = "Environment name used as a resource-identifier prefix."
  type        = string
}

variable "region" {
  description = "AWS region."
  type        = string
  default     = "eu-central-1"
}

variable "migrate_task_role_arn" {
  description = "IAM role attached to the migrate ECS task at runtime."
  type        = string
}

variable "migrate_execution_role_arn" {
  description = "IAM role used by ECS to pull the migrate image + fetch SSM params."
  type        = string
}

variable "migrate_log_group_name" {
  description = "CloudWatch log group name for the migrate ECS task."
  type        = string
}

variable "log_level" {
  description = "slog level (debug|info|warn|error) for the migrate task."
  type        = string
  default     = "info"
}

# SSM ARNs for the migrate task's injected secrets (from secrets-urls).
variable "database_url_migrate_arn" {
  description = "SSM ARN for migrate DATABASE_URL."
  type        = string
}

variable "migration_database_url_migrate_arn" {
  description = "SSM ARN for migrate MIGRATION_DATABASE_URL."
  type        = string
}

variable "runtime_admin_database_url_migrate_arn" {
  description = "SSM ARN for migrate RUNTIME_ADMIN_DATABASE_URL (Bootstrap creates + syncs the axiaops_runtime role)."
  type        = string
}

variable "app_user_password_migrate_arn" {
  description = "SSM ARN for migrate APP_USER_PASSWORD."
  type        = string
}

variable "ecr_force_delete" {
  description = "Allow `terraform destroy` to nuke ECR repos even when images are present. Default false (production-safe). Set true for short-lived test environments — without it, destroy fails on the first repo that has any pushed image."
  type        = bool
  default     = false
}

# --- Express runtime services (api + ingestion) -----------------------------
#
# This Terraform stack owns the SHAPE of the Express services (cluster, IAM roles,
# networking, scaling, ALB health path, cpu/memory) via TF. The CONTENT of
# `primary_container` — image tag, env, secrets, container healthCheck — is
# owned by the application repo's CI, which calls `aws ecs update-express-gateway-
# service` on every release. The aws_ecs_express_gateway_service resources
# below use `lifecycle.ignore_changes = [primary_container]` to make that
# split safe: TF creates the service with a bootstrap container, CI flips it
# to the real image on first deploy, and subsequent TF plans no longer touch
# the container block.

variable "ecs_task_execution_role_arn" {
  description = "Shared ECS execution role ARN — pulls images, injects SSM secrets. Same role used by api and ingestion Express services."
  type        = string
}

variable "ecs_express_infrastructure_role_arn" {
  description = "ECS Express Mode infrastructure role ARN. ECS itself assumes this (ecs.amazonaws.com trust) to provision the managed ALB/SGs that front Express services. RequiresReplace on the resource — never change after first apply."
  type        = string
}

variable "ecs_api_task_role_arn" {
  description = "Runtime task role for the api Express service."
  type        = string
}

variable "ecs_ingestion_task_role_arn" {
  description = "Runtime task role for the ingestion Express service."
  type        = string
}

variable "runtime_sg_id" {
  description = "Security group attached to both Express services (egress-only — ALB ingress is managed by ECS via the infrastructure role)."
  type        = string
}

variable "runtime_subnet_ids" {
  description = "Subnets the Express tasks land in. Shared with the migrate task."
  type        = list(string)
}

variable "api_log_group_name" {
  description = "CloudWatch log group name for the api Express service. Pre-created in the observability module with declarative retention. The bootstrap container references this group; CI's update-express-gateway-service preserves it (the container JSON template wires the same group)."
  type        = string
}

variable "ingestion_log_group_name" {
  description = "CloudWatch log group name for the ingestion Express service. Same posture as api_log_group_name."
  type        = string
}

variable "runtime_bootstrap_image" {
  description = "Image used in primary_container on first TF apply. Required by the resource schema, replaced by the first CI deploy via update-express-gateway-service. Default is a minimal busybox that sleeps — the ALB target stays unhealthy until CI flips the image, which is fine for first-env bring-up (no traffic yet)."
  type        = string
  default     = "public.ecr.aws/docker/library/busybox:1.36"
}
