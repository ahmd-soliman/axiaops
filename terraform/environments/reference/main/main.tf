locals {
  public_host = "https://${var.app_subdomain}.${var.domain_name}"

  # var.github_deploy_refs carries only the claim SUFFIX (e.g.
  # "ref:refs/tags/*") — this prepends the "repo:<owner>/<repo>:" prefix
  # GitHub's sub claim always starts with, same pattern as init/main.tf's
  # github_apply_subs/github_plan_subs.
  github_deploy_subs = [for r in var.github_deploy_refs : "repo:${var.github_repository}:${r}"]

  # §8.5: TF generates the initial HMAC value; the operator overrides via the
  # *_override vars during a C-1 rotation (§13.2). Steady state has all three
  # slots carrying the same value — the C-1 verifier code path is exercised
  # continuously rather than `==""` being a load-bearing precondition.
  api_ingestion_secret     = var.api_ingestion_secret_override != "" ? var.api_ingestion_secret_override : random_id.ingestion_shared_secret.hex
  ingestion_primary_secret = var.ingestion_primary_secret_override != "" ? var.ingestion_primary_secret_override : random_id.ingestion_shared_secret.hex
  ingestion_next_secret    = var.ingestion_next_secret_override != "" ? var.ingestion_next_secret_override : random_id.ingestion_shared_secret.hex
}

resource "random_id" "ingestion_shared_secret" {
  byte_length = 32
}

module "network" {
  source = "../../../modules/network"

  env_name = var.env_name
}

module "secrets_passwords" {
  source = "../../../modules/secrets-passwords"

  env_name = var.env_name
}

module "data" {
  source = "../../../modules/data"

  env_name          = var.env_name
  public_subnet_ids = module.network.public_subnet_ids
  rds_sg_id         = module.network.rds_sg_id
  owner_password    = module.secrets_passwords.owner_password

  deletion_protection = var.deletion_protection
  skip_final_snapshot = var.skip_final_snapshot

  # Both default false; flip to true via TF_VAR_* for the apply that runs a
  # major-version upgrade, then revert on the next apply. See
  # docs/rds-major-version-upgrade.md.
  allow_major_version_upgrade = var.rds_allow_major_version_upgrade
  apply_immediately           = var.rds_apply_immediately
}

module "ops_rds_access" {
  source = "../../../modules/ops-rds-access"

  env_name = var.env_name
  vpc_id   = module.network.vpc_id

  # EICE in a public subnet looks surprising at a glance, but the public-vs-
  # private split only matters for resources that originate or receive traffic
  # via the subnet's route table. The endpoint never does either — AWS owns
  # the control plane and the data plane terminates on an AWS-managed entry
  # point. Picking the public subnet matches the rest of this stack's no-NAT
  # posture and avoids introducing private subnets just for ops access.
  subnet_id = module.network.public_subnet_ids[0]
  rds_sg_id = module.network.rds_sg_id

  # Operator must hold ec2:OpenTunnel on the EICE ARN (output
  # `ops_rds_access_endpoint_arn`). The grant is NOT terraformed here — it
  # lives in the AWS SSO permission set used by humans (see
  # docs/terraform-prod-design.md §5.2). CI roles intentionally have no
  # grant; only humans tunnel into RDS. New operators: ask whoever owns the
  # SSO permission set to add `ec2:OpenTunnel` + `ec2:DescribeInstance
  # ConnectEndpoints` scoped to the endpoint ARN. Runbook:
  # docs/rds-shell-access.md.
  enabled = var.ops_rds_access_enabled
}

module "cache" {
  source = "../../../modules/cache"

  env_name      = var.env_name
  vpc_id        = module.network.vpc_id
  subnet_ids    = module.network.public_subnet_ids
  runtime_sg_id = module.network.runtime_sg_id
  # The migrate task shares the ECS runtime SG (cache ingress from it is the
  # `cache_from_runtime` rule), so no separate migrate ingress rule is needed.
  migrate_sg_id = ""
}

module "secrets_urls" {
  source = "../../../modules/secrets-urls"

  env_name = var.env_name

  rds_endpoint           = module.data.rds_endpoint
  rds_port               = module.data.rds_port
  db_name                = module.data.db_name
  owner_password         = module.secrets_passwords.owner_password
  app_user_password      = module.secrets_passwords.app_user_password
  runtime_admin_password = module.secrets_passwords.runtime_admin_password

  redis_endpoint   = module.cache.endpoint
  redis_port       = module.cache.port
  redis_auth_token = module.cache.auth_token

  # ENCRYPTION_KEY is intentionally not wired through TF — see module
  # `secrets-urls`: the SSM resource uses lifecycle.ignore_changes on
  # value, operator writes the real key via OOB `aws ssm put-parameter`.
  api_ingestion_secret     = local.api_ingestion_secret
  ingestion_primary_secret = local.ingestion_primary_secret
  ingestion_next_secret    = local.ingestion_next_secret
}

module "iam" {
  source = "../../../modules/iam"

  env_name              = var.env_name
  region                = var.region
  account_id            = var.account_id
  dashboard_bucket_name = var.dashboard_bucket_name

  # The OIDC provider is created by init/ and read here via remote_state
  # (see data_init.tf). modules/iam needs the ARN to wire the trust policy
  # for the github_ci_deploy (application repo's app-deploy) role.
  github_oidc_provider_arn = data.terraform_remote_state.init.outputs.github_oidc_provider_arn
  github_allowed_refs      = local.github_deploy_subs

  # Wiring module.edge.cloudfront_distribution_id directly here would form a
  # graph cycle (iam → edge → compute → iam). Instead the operator passes the
  # distribution ID via TF_VAR_cloudfront_distribution_id once module.edge has
  # been applied; the next apply tightens the CI role's CloudFront invalidate
  # policy from wildcard to the specific distribution.
  cloudfront_distribution_id = var.cloudfront_distribution_id
}

module "observability" {
  source = "../../../modules/observability"

  env_name = var.env_name
}

module "compute" {
  source = "../../../modules/compute"

  env_name = var.env_name
  region   = var.region

  # Migrate task wiring (one-off Fargate run-task; axiaops CI invokes
  # `aws ecs run-task` against this task definition per release).
  migrate_task_role_arn      = module.iam.migrate_task_role_arn
  migrate_execution_role_arn = module.iam.migrate_execution_role_arn
  migrate_log_group_name     = module.observability.ecs_migrate_log_group_name

  database_url_migrate_arn               = module.secrets_urls.database_url_migrate_arn
  migration_database_url_migrate_arn     = module.secrets_urls.migration_database_url_migrate_arn
  runtime_admin_database_url_migrate_arn = module.secrets_urls.runtime_admin_database_url_migrate_arn
  app_user_password_migrate_arn          = module.secrets_urls.app_user_password_migrate_arn

  # Express runtime service wiring. TF owns service SHAPE (cluster, IAM,
  # networking, scaling, ALB health-check path, cpu/memory); axiaops CI owns
  # service CONTAINER (image + env + secrets + container healthCheck) via
  # `aws ecs update-express-gateway-service`. See compute/main.tf for the
  # full rationale on the ignore_changes split.
  ecs_task_execution_role_arn         = module.iam.ecs_task_execution_role_arn
  ecs_express_infrastructure_role_arn = module.iam.ecs_express_infrastructure_role_arn
  ecs_api_task_role_arn               = module.iam.ecs_api_task_role_arn
  ecs_ingestion_task_role_arn         = module.iam.ecs_ingestion_task_role_arn
  runtime_sg_id                       = module.network.runtime_sg_id
  runtime_subnet_ids                  = module.network.public_subnet_ids
  api_log_group_name                  = module.observability.ecs_api_log_group_name
  ingestion_log_group_name            = module.observability.ecs_ingestion_log_group_name

  ecr_force_delete = var.ecr_force_delete
}

module "edge" {
  source = "../../../modules/edge"

  providers = {
    aws           = aws
    aws.us_east_1 = aws.us_east_1
  }

  env_name              = var.env_name
  region                = var.region
  account_id            = var.account_id
  domain_name           = var.domain_name
  app_subdomain         = var.app_subdomain
  dashboard_bucket_name = var.dashboard_bucket_name

  # CloudFront's /api/* + /v1/* origin is the api service's LIVE ECS Express
  # ingress hostname (ax-<hex>.ecs.<region>.on.aws), threaded from compute.
  # The deterministic name the origin used to hardcode never resolved — design
  # open question #5 resolved: the hostname carries a random suffix. See
  # edge/main.tf and modules/compute/outputs.tf.
  api_origin_domain = module.compute.ecs_api_gateway_endpoint

  dashboard_bucket_force_destroy = var.dashboard_bucket_force_destroy
}
