# The OIDC provider lives in environments/reference/init/ now (Tier 1
# bootstrap); the ARN is passed back into this module via the
# `github_oidc_provider_arn` input variable. No output here.

output "github_ci_deploy_role_arn" {
  description = "Role the application repo's CI assumes for app deploys (ECR push + ECS Express service create/update). Distinct from the terraform-apply role, which lives in init/."
  value       = aws_iam_role.github_ci_deploy.arn
}

output "ecs_task_execution_role_arn" {
  description = "Shared ECS execution role ARN for the Express runtime services (image pull + secret injection). Consumed by the compute module's aws_ecs_express_gateway_service resources on TF-side create, and re-bound by axiaops CI on every update-express-gateway-service call."
  value       = aws_iam_role.ecs_task_execution.arn
}

output "ecs_api_task_role_arn" {
  description = "ECS api Express service task role ARN (runtime identity)."
  value       = aws_iam_role.ecs_api_task.arn
}

output "ecs_ingestion_task_role_arn" {
  description = "AxiaOpsScanner role ARN — the ECS ingestion runtime identity AND the customer-facing cross-account scanner principal. Consumed by the compute module (ingestion task_role_arn) and the SSM platform inventory."
  value       = aws_iam_role.ecs_ingestion_task.arn
}

output "ecs_express_infrastructure_role_arn" {
  description = "ECS Express Mode infrastructure role ARN (provisions the shared ALB/SGs). Consumed by the compute module's aws_ecs_express_gateway_service resources on TF-side create. Not published to the SSM platform inventory — CI no longer creates services and so cannot pass this role."
  value       = aws_iam_role.ecs_express_infrastructure.arn
}

output "migrate_task_role_arn" {
  description = "ECS migrate task role ARN."
  value       = aws_iam_role.migrate_task.arn
}

output "migrate_execution_role_arn" {
  description = "ECS migrate execution role ARN."
  value       = aws_iam_role.migrate_execution.arn
}
