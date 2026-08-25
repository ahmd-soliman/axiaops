resource "aws_vpc" "main" {
  cidr_block           = var.vpc_cidr
  enable_dns_hostnames = true
  enable_dns_support   = true

  tags = {
    Name = "axiaops-${var.env_name}"
  }
}

resource "aws_internet_gateway" "main" {
  vpc_id = aws_vpc.main.id

  tags = {
    Name = "axiaops-${var.env_name}-igw"
  }
}

resource "aws_subnet" "public" {
  count = length(var.public_subnet_cidrs)

  vpc_id            = aws_vpc.main.id
  cidr_block        = var.public_subnet_cidrs[count.index]
  availability_zone = var.availability_zones[count.index]

  # No subnet-level public IP auto-assign. ECS Express Mode and one-off
  # `aws ecs run-task` tasks set assignPublicIp=ENABLED at the task ENI level
  # (Fargate needs a public IP to reach ECR/SSM/RDS with no NAT gateway —
  # the cost-conscious posture, design §cost). RDS opts out explicitly.
  map_public_ip_on_launch = false

  tags = {
    Name = "axiaops-${var.env_name}-public-${var.availability_zones[count.index]}"
  }
}

resource "aws_route_table" "public" {
  vpc_id = aws_vpc.main.id

  route {
    cidr_block = "0.0.0.0/0"
    gateway_id = aws_internet_gateway.main.id
  }

  tags = {
    Name = "axiaops-${var.env_name}-public"
  }
}

resource "aws_route_table_association" "public" {
  count = length(aws_subnet.public)

  subnet_id      = aws_subnet.public[count.index].id
  route_table_id = aws_route_table.public.id
}

# Security group attached to all Fargate tasks in this VPC: the ECS Express
# Mode runtime services (api + ingestion) AND the one-off `aws ecs run-task`
# migrate task. Egress only — the Express-Mode-created ALB owns ingress to the
# runtime services via a separate Service SG it manages itself (design §"Risks
# → Service-SG ingress is wired by Express Mode automatically"); we must NOT
# declare an ingress rule from that SG here (circular, unnecessary).
#
# Egress targets: RDS 5432 (rule below), ElastiCache 6379 (in the cache
# module, which owns both SGs — avoids a network<->cache cycle), and 443 to
# AWS APIs / external endpoints.
resource "aws_security_group" "ecs_runtime" {
  name        = "axiaops-${var.env_name}-ecs-runtime"
  description = "ECS Fargate task egress (Express runtime services + migrate task): RDS, ElastiCache, AWS APIs."
  vpc_id      = aws_vpc.main.id

  tags = {
    Name = "axiaops-${var.env_name}-ecs-runtime"
  }
}

resource "aws_security_group" "rds" {
  # KEPT on literal `name` (not `name_prefix`) because aws_db_instance.main
  # has apply_immediately = false: any SG replacement is blocked at the
  # destroy step (RDS modify queues for maintenance window so the old SG is
  # never released). create_before_destroy + name_prefix don't save us here
  # the way they do for cache, because the parent never swaps off the old
  # SG in time. The current literal "-v2" suffix matches the SG name an
  # operator created manually during the 2026-05-25 unblock (after the App
  # Runner -> ECS rename deadlocked the first apply). Future description
  # changes on this SG will hit the same trap and require another OOB
  # swap + state-import cycle -- see docs/refactor-to-ecs-express-mode-
  # plan.md for the runbook. To break the cycle permanently, set
  # apply_immediately = true on aws_db_instance.main and switch this SG
  # to name_prefix on the same apply.
  name        = "axiaops-${var.env_name}-rds-v2"
  description = "RDS PostgreSQL ingress from ECS runtime tasks only."
  vpc_id      = aws_vpc.main.id

  tags = {
    Name = "axiaops-${var.env_name}-rds"
  }
}

resource "aws_vpc_security_group_ingress_rule" "rds_from_runtime" {
  security_group_id            = aws_security_group.rds.id
  referenced_security_group_id = aws_security_group.ecs_runtime.id
  ip_protocol                  = "tcp"
  from_port                    = 5432
  to_port                      = 5432
  description                  = "Postgres from ECS runtime tasks"
}

resource "aws_vpc_security_group_egress_rule" "runtime_to_rds" {
  security_group_id            = aws_security_group.ecs_runtime.id
  referenced_security_group_id = aws_security_group.rds.id
  ip_protocol                  = "tcp"
  from_port                    = 5432
  to_port                      = 5432
  description                  = "Postgres to RDS"
}

resource "aws_vpc_security_group_egress_rule" "runtime_https_world" {
  security_group_id = aws_security_group.ecs_runtime.id
  cidr_ipv4         = "0.0.0.0/0"
  ip_protocol       = "tcp"
  from_port         = 443
  to_port           = 443
  description       = "HTTPS to AWS APIs (ECR/SSM/STS) and external endpoints (no plaintext-80)"
}

# STARTTLS submission to the invite-email SMTP relay (smtp-relay.gmail.com:587).
# The api's InviteMailer dials the relay on 587 (services/api/cmd/main.go uses
# STARTTLS, not implicit-TLS 465). Without this rule the connection is dropped
# at the SG and every invite email times out. Destination is 0.0.0.0/0: the
# Gmail relay resolves to a rotating Google IP range that can't be pinned to a
# stable CIDR, and this is egress-only. Submission auth is App-Password (PLAIN
# over the STARTTLS channel), so it works from the task's rotating public IP.
resource "aws_vpc_security_group_egress_rule" "runtime_smtp_submission" {
  security_group_id = aws_security_group.ecs_runtime.id
  cidr_ipv4         = "0.0.0.0/0"
  ip_protocol       = "tcp"
  from_port         = 587
  to_port           = 587
  description       = "SMTP STARTTLS submission to the invite-email relay (smtp-relay.gmail.com)"
}
