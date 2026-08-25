output "ecr_api_repository_url" {
  description = "ECR repository URL for axiaops-api."
  value       = aws_ecr_repository.api.repository_url
}

output "ecr_api_repository_arn" {
  description = "ECR repository ARN for axiaops-api."
  value       = aws_ecr_repository.api.arn
}

output "ecr_ingestion_repository_url" {
  description = "ECR repository URL for axiaops-ingestion."
  value       = aws_ecr_repository.ingestion.repository_url
}

output "ecr_ingestion_repository_arn" {
  description = "ECR repository ARN for axiaops-ingestion."
  value       = aws_ecr_repository.ingestion.arn
}

output "ecr_migrate_repository_url" {
  description = "ECR repository URL for axiaops-migrate."
  value       = aws_ecr_repository.migrate.repository_url
}

output "ecr_migrate_repository_arn" {
  description = "ECR repository ARN for axiaops-migrate."
  value       = aws_ecr_repository.migrate.arn
}

output "ecs_runtime_cluster_name" {
  description = "ECS cluster the Express runtime services (api + ingestion) run in. Published to the SSM platform inventory for the axiaops CI deploy job."
  value       = aws_ecs_cluster.runtime.name
}

output "ecs_runtime_cluster_arn" {
  description = "ARN of the Express runtime ECS cluster."
  value       = aws_ecs_cluster.runtime.arn
}

output "ecs_migrate_cluster_name" {
  description = "ECS cluster the migrate task runs in (pass to `aws ecs run-task --cluster`)."
  value       = aws_ecs_cluster.migrate.name
}

output "ecs_migrate_cluster_arn" {
  description = "ARN of the migrate ECS cluster."
  value       = aws_ecs_cluster.migrate.arn
}

output "ecs_migrate_task_definition_arn" {
  description = "ARN of the latest migrate task definition revision."
  value       = aws_ecs_task_definition.migrate.arn
}

output "ecs_migrate_task_definition_family" {
  description = "Family name of the migrate task definition."
  value       = aws_ecs_task_definition.migrate.family
}

output "ecs_api_service_arn" {
  description = "ARN of the api Express Gateway service. Published to the SSM platform inventory so the axiaops CI deploy job can target update-express-gateway-service without rebuilding it from convention."
  value       = aws_ecs_express_gateway_service.api.service_arn
}

output "ecs_ingestion_service_arn" {
  description = "ARN of the ingestion Express Gateway service. See ecs_api_service_arn for posture."
  value       = aws_ecs_express_gateway_service.ingestion.service_arn
}

# Live ECS Express ingress hostnames. These are AWS-assigned and carry a random
# per-service suffix (e.g. ax-6b90c30f...ecs.<region>.on.aws) — they are NOT the
# deterministic "axiaops-api.ecs.<region>.on.aws" the code originally assumed
# (design open question #5 resolved: the suffix exists, confirmed in state).
# Selecting the PUBLIC entry keeps this correct if AWS ever adds an internal-only
# path alongside it. The `try(..., [])` guards the create-time window where the
# computed ingress_paths may briefly be null (iterating null is a hard error);
# `one()` then enforces the "exactly one PUBLIC path" invariant — a zero/multi
# match fails loudly at plan, naming this output, rather than silently emitting a
# broken (empty-host) CloudFront origin / SSM URL.
#
# `trimprefix(..., "https://")` normalizes AWS's representation: the ECS Express
# DescribeService `ingress_paths[].endpoint` field changed server-side from a bare
# host (ax-...on.aws) to a scheme-prefixed URL (https://ax-...on.aws). That value
# is computed/read-only, so refresh adopts it unconditionally and it flows into
# consumers that require a BARE host — CloudFront's origin domain_name (rejects the
# colon: "origin name cannot contain a colon") and the SSM api_url/ingestion_url
# params (which build "https://${...}" and would otherwise double the scheme).
# Stripping here re-asserts the "no scheme" contract at the boundary, insulating
# every downstream consumer regardless of which form AWS returns. No-op if AWS
# reverts to a bare host.
output "ecs_api_gateway_endpoint" {
  description = "Live public ECS Express ingress hostname for axiaops-api (no scheme). Feeds the CloudFront /api/* origin and the SSM platform inventory's api_url."
  value       = trimprefix(one([for p in try(aws_ecs_express_gateway_service.api.ingress_paths, []) : p.endpoint if p.access_type == "PUBLIC"]), "https://")
}

output "ecs_ingestion_gateway_endpoint" {
  description = "Live public ECS Express ingress hostname for axiaops-ingestion (no scheme). Feeds the SSM platform inventory's ingestion_url, which the api consumes as INGESTION_URL."
  value       = trimprefix(one([for p in try(aws_ecs_express_gateway_service.ingestion.ingress_paths, []) : p.endpoint if p.access_type == "PUBLIC"]), "https://")
}
