# Surfaced for the application repo's GitHub Actions workflow to consume.
# Update the corresponding GitHub Actions secrets/variables to match these
# output values after every apply that changes them.

output "github_ci_deploy_role_arn" {
  description = "Role the application repo's CI assumes for app deploys (ECR push + ECS Express service create/update). Set as the AWS role-to-assume input in the deploy workflow. Distinct from the terraform-apply role which lives in init/."
  value       = module.iam.github_ci_deploy_role_arn
}

# Note: github_oidc_provider_arn is not output here — it's owned by the
# sibling init/ root. Read via `terraform output -raw github_oidc_provider_arn`
# in init/.
#
# The Express runtime services are created by the application repo's CI,
# which reads everything it needs from the SSM platform inventory
# (/axiaops/<env>/platform/*, see platform_inventory.tf) — not from these
# outputs.

output "ecs_runtime_cluster_name" {
  description = "ECS cluster hosting the Express runtime services. Also published to SSM platform inventory."
  value       = module.compute.ecs_runtime_cluster_name
}

output "ecr_api_repository_url" {
  description = "CI variable ECR_API_REPOSITORY — image push target for axiaops-api."
  value       = module.compute.ecr_api_repository_url
}

output "ecr_ingestion_repository_url" {
  description = "CI variable ECR_INGESTION_REPOSITORY — image push target for axiaops-ingestion."
  value       = module.compute.ecr_ingestion_repository_url
}

output "ecr_migrate_repository_url" {
  description = "CI variable ECR_MIGRATE_REPOSITORY — image push target for axiaops-migrate."
  value       = module.compute.ecr_migrate_repository_url
}

output "ecs_migrate_cluster_name" {
  description = "CI variable ECS_MIGRATE_CLUSTER — `aws ecs run-task --cluster`."
  value       = module.compute.ecs_migrate_cluster_name
}

output "ecs_migrate_task_definition_family" {
  description = "CI variable ECS_MIGRATE_TASK_DEFINITION — `aws ecs run-task --task-definition`."
  value       = module.compute.ecs_migrate_task_definition_family
}

output "rds_endpoint" {
  description = "RDS endpoint host. Manual psql tunneling reference."
  value       = module.data.rds_endpoint
}

output "redis_endpoint" {
  description = "ElastiCache Redis primary endpoint hostname. Operational reference only — the live URL (with embedded AUTH token) is stored in SSM and consumed by App Runner."
  value       = module.cache.endpoint
}

output "redis_auth_token_ssm_parameter_name" {
  description = "SSM parameter name holding the Redis AUTH token. `aws ssm get-parameter --with-decryption` for OOB recovery."
  value       = module.cache.auth_token_ssm_parameter_name
}

output "rds_identifier" {
  description = "RDS instance identifier (for `aws rds` CLI ops)."
  value       = module.data.rds_identifier
}

output "ops_rds_access_endpoint_id" {
  description = "EC2 Instance Connect Endpoint ID for operator psql/DBeaver tunnels. Pass to `aws ec2-instance-connect open-tunnel --instance-connect-endpoint-id`. Empty when ops_rds_access_enabled = false."
  value       = module.ops_rds_access.endpoint_id
}

output "ops_rds_access_endpoint_arn" {
  description = "EICE ARN — scope `ec2:OpenTunnel` IAM policies to this Resource."
  value       = module.ops_rds_access.endpoint_arn
}

output "owner_password_ssm_parameter_name" {
  description = "SSM parameter name holding the RDS axiaops_owner password. `aws ssm get-parameter --with-decryption` to fetch during a shell session."
  value       = "/axiaops/${var.env_name}/infra/owner_password"
}

output "app_user_password_ssm_parameter_name" {
  description = "SSM parameter name holding the RDS axiaops (app user) password. Use for psql sessions that need to test under RLS instead of as the schema owner."
  value       = "/axiaops/${var.env_name}/infra/app_user_password"
}

output "dashboard_bucket_name" {
  description = "CI variable DASHBOARD_S3_BUCKET — `aws s3 sync` target."
  value       = module.edge.dashboard_bucket_name
}

output "cloudfront_distribution_id" {
  description = "CI variable CLOUDFRONT_DISTRIBUTION_ID — `aws cloudfront create-invalidation`."
  value       = module.edge.cloudfront_distribution_id
}

output "dashboard_url" {
  description = "User-facing dashboard URL."
  value       = module.edge.dashboard_url
}

output "onboarding_cfn_template_url" {
  description = "CI variable VITE_AXIAOPS_CFN_TEMPLATE_URL — the public S3 URL of the AxiaOpsIntegrationRole CloudFormation template the dashboard's \"Launch Stack\" button deep-links to. Set on the dashboard build job after the apply that creates the onboarding bucket."
  value       = module.edge.onboarding_cfn_template_url
}

output "public_subnet_ids" {
  description = "Public subnet IDs — used by `aws ecs run-task --network-configuration`."
  value       = module.network.public_subnet_ids
}

output "runtime_sg_id" {
  description = "ECS runtime SG ID — attached to Express services and one-off ECS tasks that need RDS/cache access."
  value       = module.network.runtime_sg_id
}

# --- ECS migrate task CI inputs ----------------------------------------------
# The application repo's CI deploy job runs the migrate task via
# `aws ecs run-task --cluster <C> --network-configuration "{subnets=[...],
# securityGroups=[...]}"`. These three outputs map 1:1 to the three pieces
# of information that command needs. Kept as separate, comma-joined string
# outputs (not a single composite object) so the CI YAML can splice them
# directly into the run-task invocation without jq.

output "ecs_migrate_cluster" {
  description = "CI variable ECS_MIGRATE_CLUSTER — `aws ecs run-task --cluster`. Same value as ecs_migrate_cluster_name; kept as a stable, taskname-aligned alias."
  value       = module.compute.ecs_migrate_cluster_name
}

output "ecs_migrate_subnet_ids" {
  description = "CI variable ECS_MIGRATE_SUBNETS — comma-joined subnet IDs for `--network-configuration awsvpcConfiguration={subnets=[...]}`. The public subnets, which (with assignPublicIp=ENABLED) are the NAT-free route to RDS."
  value       = join(",", module.network.public_subnet_ids)
}

output "ecs_migrate_security_group_id" {
  description = "CI variable ECS_MIGRATE_SECURITY_GROUP — SG ID for `--network-configuration awsvpcConfiguration={securityGroups=[...]}`. The migrate task shares the ECS runtime SG, which has the egress-to-RDS/cache rules wired."
  value       = module.network.runtime_sg_id
}
