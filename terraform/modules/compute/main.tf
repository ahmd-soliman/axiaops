locals {
  ecr_lifecycle_policy = jsonencode({
    rules = [
      {
        rulePriority = 1
        description  = "Keep last 30 tagged images"
        selection = {
          tagStatus      = "tagged"
          tagPatternList = ["*"]
          countType      = "imageCountMoreThan"
          countNumber    = 30
        }
        action = { type = "expire" }
      },
      {
        rulePriority = 2
        description  = "Expire untagged after 7 days"
        selection = {
          tagStatus   = "untagged"
          countType   = "sinceImagePushed"
          countUnit   = "days"
          countNumber = 7
        }
        action = { type = "expire" }
      }
    ]
  })
}

# --- ECR repos ----------------------------------------------------------------

resource "aws_ecr_repository" "api" {
  name = "axiaops-api"
  # IMMUTABLE: each release's image is the source of truth for what's deployed;
  # the registry MUST refuse silent overwrites. The axiaops CI pipeline tags
  # images with ${CI_COMMIT_TAG:-$CI_COMMIT_SHORT_SHA} (semver tag on a release,
  # short SHA on interim main deploys — matches InfraVersion/APP_VERSION) and
  # references that tag in the ECS Express service's --primary-container image
  # identifier — see the application repo's CI deploy workflow.
  image_tag_mutability = "IMMUTABLE"

  image_scanning_configuration {
    scan_on_push = true
  }

  # Set true for test environments you intend to destroy. Without it,
  # `terraform destroy` fails on any repo that has images pushed (the
  # CI pipeline pushes on every release).
  force_delete = var.ecr_force_delete
}

resource "aws_ecr_lifecycle_policy" "api" {
  repository = aws_ecr_repository.api.name
  policy     = local.ecr_lifecycle_policy
}

resource "aws_ecr_repository" "ingestion" {
  name = "axiaops-ingestion"
  # IMMUTABLE — see rationale on `aws_ecr_repository.api` above.
  image_tag_mutability = "IMMUTABLE"

  image_scanning_configuration {
    scan_on_push = true
  }

  # Set true for test environments you intend to destroy. Without it,
  # `terraform destroy` fails on any repo that has images pushed (the
  # CI pipeline pushes on every release).
  force_delete = var.ecr_force_delete
}

resource "aws_ecr_lifecycle_policy" "ingestion" {
  repository = aws_ecr_repository.ingestion.name
  policy     = local.ecr_lifecycle_policy
}

resource "aws_ecr_repository" "migrate" {
  name = "axiaops-migrate"
  # IMMUTABLE — see rationale on `aws_ecr_repository.api` above.
  image_tag_mutability = "IMMUTABLE"

  image_scanning_configuration {
    scan_on_push = true
  }

  # Set true for test environments you intend to destroy. Without it,
  # `terraform destroy` fails on any repo that has images pushed (the
  # CI pipeline pushes on every release).
  force_delete = var.ecr_force_delete
}

resource "aws_ecr_lifecycle_policy" "migrate" {
  repository = aws_ecr_repository.migrate.name
  policy     = local.ecr_lifecycle_policy
}

# --- ECS runtime cluster (Express Mode services: api + ingestion) ------------
#
# Separate from `aws_ecs_cluster.migrate` (design open question #2): migrate is
# a short-lived run-task consumer; this cluster owns the long-running Express
# services with their own scaling. Cleaner isolation, easier console reading.
resource "aws_ecs_cluster" "runtime" {
  name = "axiaops-runtime"

  setting {
    name  = "containerInsights"
    value = "disabled"
  }
}

# --- Express runtime services (api + ingestion) ------------------------------
#
# TF owns the service shape; axiaops CI owns the container (image + env +
# secrets + container healthCheck). The split is enforced by
# `lifecycle.ignore_changes = [primary_container]` — on first apply the
# resource is created with the bootstrap busybox image, CI's first
# update-express-gateway-service flips primary_container to the real ECR image,
# and subsequent TF plans do not see the container drift.
#
# Why this split rather than full-TF or full-CI:
#   - Full-TF would put the image tag (changes every release) into the TF state
#     and force a `terraform apply` for every deploy. Same layering inversion
#     that killed the original apprunner setup (commit 52e890d) — TF would also
#     refuse to create on a fresh env because the ECR repo has no image yet.
#   - Full-CI (the previous shape) kept service shape changes — memory,
#     scaling, health-check path, role swaps — buried in a 130-line shell
#     script with no review surface, and required `Create`/`PassRole` grants
#     on the CI role that are wider than necessary for ongoing operation.
#   - This split: shape diffs are reviewable in MR; CI deploy keeps the
#     surgical Update + PassRole grants only; bootstrap container doesn't
#     need a real image to exist on first apply.
#
# Notes:
#   - `infrastructure_role_arn` is RequiresReplace in the provider — never
#     change it after first apply or both services get recreated.
#   - `health_check_path` is the ALB target-group path; the container's
#     internal healthCheck command lives in primary_container and so is
#     ignored here.
#   - Bootstrap leaves ALB targets unhealthy until first CI deploy. That's
#     fine: no traffic is routed to the AWS-owned hostname until the
#     CloudFront origin points at it, which happens after CI deploy.

resource "aws_ecs_express_gateway_service" "api" {
  service_name            = "axiaops-api"
  cluster                 = aws_ecs_cluster.runtime.arn
  execution_role_arn      = var.ecs_task_execution_role_arn
  infrastructure_role_arn = var.ecs_express_infrastructure_role_arn
  task_role_arn           = var.ecs_api_task_role_arn

  cpu               = "256"
  memory            = "512"
  health_check_path = "/livez"

  network_configuration {
    subnets         = var.runtime_subnet_ids
    security_groups = [var.runtime_sg_id]
  }

  scaling_target {
    min_task_count            = 1
    max_task_count            = 3
    auto_scaling_metric       = "AVERAGE_CPU"
    auto_scaling_target_value = 70
  }

  primary_container {
    image          = var.runtime_bootstrap_image
    container_port = 8080
    command        = ["sleep", "infinity"]

    aws_logs_configuration {
      log_group         = var.api_log_group_name
      log_stream_prefix = "ecs"
    }
  }

  lifecycle {
    # primary_container: axiaops CI owns image+env+secrets via
    # update-express-gateway-service (see header comment).
    # network_configuration[0].security_groups: ECS Express Mode auto-creates and
    # attaches a per-service ingress SG ("Security group for ECS service: <arn>")
    # in addition to the runtime egress SG we declare. Without ignoring it, every
    # plan shows that managed SG as drift and apply strips it — severing ingress
    # to the service. We own only the egress SG (runtime_sg_id); the ingress SG
    # is AWS-managed and must be left alone.
    ignore_changes = [primary_container, network_configuration[0].security_groups]
  }
}

resource "aws_ecs_express_gateway_service" "ingestion" {
  service_name            = "axiaops-ingestion"
  cluster                 = aws_ecs_cluster.runtime.arn
  execution_role_arn      = var.ecs_task_execution_role_arn
  infrastructure_role_arn = var.ecs_express_infrastructure_role_arn
  task_role_arn           = var.ecs_ingestion_task_role_arn

  cpu               = "256"
  memory            = "512"
  health_check_path = "/health"

  network_configuration {
    subnets         = var.runtime_subnet_ids
    security_groups = [var.runtime_sg_id]
  }

  scaling_target {
    min_task_count            = 1
    max_task_count            = 3
    auto_scaling_metric       = "AVERAGE_CPU"
    auto_scaling_target_value = 70
  }

  primary_container {
    image          = var.runtime_bootstrap_image
    container_port = 8081
    command        = ["sleep", "infinity"]

    aws_logs_configuration {
      log_group         = var.ingestion_log_group_name
      log_stream_prefix = "ecs"
    }
  }

  lifecycle {
    # primary_container: axiaops CI owns image+env+secrets via
    # update-express-gateway-service (see header comment).
    # network_configuration[0].security_groups: ECS Express Mode auto-creates and
    # attaches a per-service ingress SG ("Security group for ECS service: <arn>")
    # in addition to the runtime egress SG we declare. Without ignoring it, every
    # plan shows that managed SG as drift and apply strips it — severing ingress
    # to the service. We own only the egress SG (runtime_sg_id); the ingress SG
    # is AWS-managed and must be left alone.
    ignore_changes = [primary_container, network_configuration[0].security_groups]
  }
}

# --- ECS migrate task (one-off Fargate) --------------------------------------

resource "aws_ecs_cluster" "migrate" {
  name = "axiaops-migrate"

  setting {
    name  = "containerInsights"
    value = "disabled"
  }
}

resource "aws_ecs_task_definition" "migrate" {
  family                   = "axiaops-migrate"
  network_mode             = "awsvpc"
  requires_compatibilities = ["FARGATE"]
  cpu                      = "256"
  memory                   = "512"
  task_role_arn            = var.migrate_task_role_arn
  execution_role_arn       = var.migrate_execution_role_arn

  container_definitions = jsonencode([{
    name = "migrate"
    # Baseline tag only. The ECR repo is IMMUTABLE and axiaops CI builds images
    # tagged ${CI_COMMIT_TAG:-$CI_COMMIT_SHORT_SHA} per release. run-task CANNOT
    # override the container image (containerOverrides covers command/env/cpu/
    # memory, not image), so pinning a specific migration build is the axiaops
    # CI deploy job's concern — it registers a task-definition revision with the
    # release-tagged image and runs that. The CI role is granted
    # ecs:RegisterTaskDefinition for exactly this (iam module,
    # ECSRegisterMigrateTaskDef); the deploy workflow's job
    # (MR B).
    image     = "${aws_ecr_repository.migrate.repository_url}:latest"
    essential = true

    environment = [
      { name = "AWS_REGION", value = var.region },
      { name = "APP_ENV", value = "production" },
      { name = "LOG_LEVEL", value = var.log_level },
      { name = "LOG_OUTPUT", value = "json" },
    ]

    secrets = [
      { name = "DATABASE_URL", valueFrom = var.database_url_migrate_arn },
      { name = "MIGRATION_DATABASE_URL", valueFrom = var.migration_database_url_migrate_arn },
      { name = "RUNTIME_ADMIN_DATABASE_URL", valueFrom = var.runtime_admin_database_url_migrate_arn },
      { name = "APP_USER_PASSWORD", valueFrom = var.app_user_password_migrate_arn },
    ]

    logConfiguration = {
      logDriver = "awslogs"
      options = {
        "awslogs-group"         = var.migrate_log_group_name
        "awslogs-region"        = var.region
        "awslogs-stream-prefix" = "migrate"
      }
    }
  }])
}

# NOTE: the App Runner log-retention null_resources are gone. Express Mode
# writes to the pre-created /aws/ecs/axiaops-{api,ingestion} groups (owned by
# the observability module with retention_in_days set declaratively), so the
# post-create `aws logs put-retention-policy` hack is no longer needed.
