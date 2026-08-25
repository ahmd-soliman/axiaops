output "ecs_migrate_log_group_name" {
  description = "CloudWatch log group name for the migrate ECS task."
  value       = aws_cloudwatch_log_group.ecs_migrate.name
}

output "ecs_migrate_log_group_arn" {
  description = "CloudWatch log group ARN for the migrate ECS task."
  value       = aws_cloudwatch_log_group.ecs_migrate.arn
}

output "ecs_api_log_group_name" {
  description = "CloudWatch log group name for the api Express Mode service."
  value       = aws_cloudwatch_log_group.ecs_api.name
}

output "ecs_api_log_group_arn" {
  description = "CloudWatch log group ARN for the api Express Mode service."
  value       = aws_cloudwatch_log_group.ecs_api.arn
}

output "ecs_ingestion_log_group_name" {
  description = "CloudWatch log group name for the ingestion Express Mode service."
  value       = aws_cloudwatch_log_group.ecs_ingestion.name
}

output "ecs_ingestion_log_group_arn" {
  description = "CloudWatch log group ARN for the ingestion Express Mode service."
  value       = aws_cloudwatch_log_group.ecs_ingestion.arn
}
