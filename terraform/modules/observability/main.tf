# ECS log groups, owned here (the established home for ECS log groups in this
# stack) so retention is set declaratively and the task definitions — which
# live in the application repo's CI for the Express runtime services, and in
# the compute module for migrate — reference them by name.
#
# This replaces the old App Runner pattern, where App Runner auto-created its
# own log groups on first run with indefinite retention and the compute module
# retention-overrode them via a null_resource local-exec (§11.1 Option B).
# Pre-creating the ECS groups here removes that hack entirely.
resource "aws_cloudwatch_log_group" "ecs_migrate" {
  name              = "/aws/ecs/axiaops-migrate"
  retention_in_days = var.log_retention_days
}

resource "aws_cloudwatch_log_group" "ecs_api" {
  name              = "/aws/ecs/axiaops-api"
  retention_in_days = var.log_retention_days
}

resource "aws_cloudwatch_log_group" "ecs_ingestion" {
  name              = "/aws/ecs/axiaops-ingestion"
  retention_in_days = var.log_retention_days
}
