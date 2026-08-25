# --- Platform inventory: /axiaops/<env>/platform/* ---------------------------
#
# Cross-repo CI shape: the application repo does NOT own a Terraform stack.
# This stack publishes every value its deploy workflow needs as a named SSM
# String parameter; the deploy job reads them at runtime via
# `aws ssm get-parameters` and assembles its `aws ecs
# create-express-gateway-service` / `update-express-gateway-service` calls.
#
# All values here are NON-secret platform inventory: role ARNs, ECR repo URLs,
# cluster names/ARNs, subnet/SG IDs, log group names, and the ARNs of the
# /axiaops/<env>/{api,ingestion}/* SecureString secrets. The secret ARNs are
# published as type String because the value is an ARN, not the secret itself
# — the deploy job passes them by ARN in the `secrets[]` field of
# --primary-container, and ECS fetches the values at task-start using the task
# execution role. The secret *values* never leave the SecureString store.
#
# The CI deploy role reads only this prefix (see iam module
# `github_ci_deploy.ReadPlatformInventory`).

locals {
  platform_inventory = {
    # ECS clusters
    "ecs_runtime_cluster_arn"  = module.compute.ecs_runtime_cluster_arn
    "ecs_runtime_cluster_name" = module.compute.ecs_runtime_cluster_name
    "ecs_migrate_cluster_name" = module.compute.ecs_migrate_cluster_name

    # Express Gateway service ARNs. TF owns the services (see compute/main.tf);
    # the CI deploy job reads these to target update-express-gateway-service
    # rather than rebuilding the ARN from convention.
    "ecs_api_service_arn"       = module.compute.ecs_api_service_arn
    "ecs_ingestion_service_arn" = module.compute.ecs_ingestion_service_arn

    # IAM roles. The Express task roles + execution role still flow to CI so
    # update-express-gateway-service can re-bind them on each update. The
    # ecs_express_infrastructure_role_arn is intentionally NOT published —
    # only TF creates Express services, and that role is consumed inside the
    # compute module via module.iam directly.
    "ecs_task_execution_role_arn" = module.iam.ecs_task_execution_role_arn
    "ecs_api_task_role_arn"       = module.iam.ecs_api_task_role_arn
    "ecs_ingestion_task_role_arn" = module.iam.ecs_ingestion_task_role_arn
    "migrate_task_role_arn"       = module.iam.migrate_task_role_arn
    "migrate_execution_role_arn"  = module.iam.migrate_execution_role_arn

    # ECR repositories
    "ecr_api_repository_url"       = module.compute.ecr_api_repository_url
    "ecr_ingestion_repository_url" = module.compute.ecr_ingestion_repository_url
    "ecr_migrate_repository_url"   = module.compute.ecr_migrate_repository_url

    # Networking (runtime + migrate share the runtime SG and the public subnets)
    "vpc_id"                    = module.network.vpc_id
    "runtime_security_group_id" = module.network.runtime_sg_id
    "runtime_subnet_ids"        = join(",", module.network.public_subnet_ids)
    "migrate_security_group_id" = module.network.runtime_sg_id
    "migrate_subnet_ids"        = join(",", module.network.public_subnet_ids)

    # Log groups (pre-created with 7-day retention by the observability module)
    "api_log_group_name"       = module.observability.ecs_api_log_group_name
    "ingestion_log_group_name" = module.observability.ecs_ingestion_log_group_name
    "migrate_log_group_name"   = module.observability.ecs_migrate_log_group_name

    # Service env-var inputs the deploy job splices into --primary-container
    "public_host" = local.public_host
    "cors_origin" = local.public_host

    # Invite-email relay — NON-SECRET config (the password is the separate
    # SecureString secret_arn_api_smtp_pass below). Published here so the deploy
    # job splices them into the api task-def environment[] block, parity with
    # public_host. The relay endpoint + sender identity are deployment facts,
    # not code, so they live in inventory rather than hardcoded in the workflow.
    # smtp_port stays a clean integer string — the api die()s on a non-numeric
    # SMTP_PORT at boot (services/api/cmd/main.go).
    "smtp_host"      = "smtp-relay.gmail.com"
    "smtp_port"      = "587"
    "smtp_user"      = "notifications@example.com"
    "smtp_from"      = "noreply@example.com"
    "smtp_from_name" = "AxiaOps"

    # Live Express Mode service URLs. `ingestion_url` is the api's required
    # INGESTION_URL env var (App Runner injected it as the ingestion service
    # URL); published here so the CI deploy job reads it from one place rather
    # than reconstructing a hostname convention. `api_url` is published for
    # parity / post-deploy smoke checks. Design open question #5 is RESOLVED:
    # the live ECS Express hostname carries a random per-service suffix, so
    # these are sourced from the resource's computed ingress endpoint (via
    # module.compute) rather than the deterministic guess that never resolved.
    "api_url"       = "https://${module.compute.ecs_api_gateway_endpoint}"
    "ingestion_url" = "https://${module.compute.ecs_ingestion_gateway_endpoint}"

    # Edge / CDN — the deploy job syncs the dashboard bundle to this bucket and
    # invalidates this distribution after a release.
    "dashboard_bucket_name"      = module.edge.dashboard_bucket_name
    "cloudfront_distribution_id" = module.edge.cloudfront_distribution_id

    # Secret ARNs (String-typed: the value is an ARN, not the secret) —
    # passed by ARN in the secrets[] field; ECS fetches values at task start.
    "secret_arn_api_database_url"               = module.secrets_urls.database_url_api_arn
    "secret_arn_api_migration_database_url"     = module.secrets_urls.migration_database_url_api_arn
    "secret_arn_api_runtime_admin_database_url" = module.secrets_urls.runtime_admin_database_url_api_arn
    "secret_arn_api_encryption_key"             = module.secrets_urls.encryption_key_api_arn
    "secret_arn_api_ingestion_shared_secret"    = module.secrets_urls.ingestion_shared_secret_api_arn
    "secret_arn_api_redis_url"                  = module.secrets_urls.redis_url_api_arn
    "secret_arn_api_smtp_pass"                  = module.secrets_urls.smtp_pass_api_arn
    "secret_arn_api_turnstile_secret_key"       = module.secrets_urls.turnstile_secret_key_api_arn

    "secret_arn_ingestion_database_url"               = module.secrets_urls.database_url_ingestion_arn
    "secret_arn_ingestion_migration_database_url"     = module.secrets_urls.migration_database_url_ingestion_arn
    "secret_arn_ingestion_runtime_admin_database_url" = module.secrets_urls.runtime_admin_database_url_ingestion_arn
    "secret_arn_ingestion_encryption_key"             = module.secrets_urls.encryption_key_ingestion_arn
    "secret_arn_ingestion_shared_secret_primary"      = module.secrets_urls.ingestion_shared_secret_primary_arn
    "secret_arn_ingestion_shared_secret_next"         = module.secrets_urls.ingestion_shared_secret_next_arn
    "secret_arn_ingestion_redis_url"                  = module.secrets_urls.redis_url_ingestion_arn
  }
}

resource "aws_ssm_parameter" "platform" {
  for_each = local.platform_inventory

  name  = "/axiaops/${var.env_name}/platform/${each.key}"
  type  = "String"
  value = each.value
}
