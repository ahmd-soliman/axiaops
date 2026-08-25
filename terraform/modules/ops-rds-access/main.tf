# Operator-only RDS shell access via EC2 Instance Connect Endpoint (EICE).
#
# Why EICE and not a bastion EC2:
#   - No instance to run, patch, or harden.
#   - No idle billing: AWS charges only per-GB of data transferred WHILE a
#     tunnel is open. A typical psql/DBeaver session is sub-cent.
#   - IAM-native auth: who can open a tunnel is gated by the `ec2:OpenTunnel`
#     permission on the endpoint ARN, not by SSH keys.
#
# How it's used: an operator runs
#   aws ec2-instance-connect open-tunnel \
#     --instance-connect-endpoint-id <eice-id> \
#     --remote-port 5432 \
#     --local-port 5432 \
#     --private-ip-address <rds-private-ip>
# which opens a local TCP forwarder on 127.0.0.1:5432. They then point psql
# or DBeaver at localhost with sslmode=verify-full + the RDS CA bundle.
# See docs/rds-shell-access.md for the full runbook.
#
# IAM: this module does NOT grant `ec2:OpenTunnel`. The grant must live in the
# AWS SSO permission set the operator assumes — terraform-prod-design.md §5.2
# is explicit that "all operator access to AWS is via SSO." Output
# `endpoint_arn` is surfaced so that SSO policy can scope the grant tightly:
#   {
#     "Effect": "Allow",
#     "Action": ["ec2:OpenTunnel", "ec2:DescribeInstanceConnectEndpoints"],
#     "Resource": "<endpoint_arn>"
#   }
# CI roles intentionally have no such grant — only humans tunnel into RDS.

resource "aws_security_group" "eice" {
  count = var.enabled ? 1 : 0

  # name_prefix + create_before_destroy follows the cache module's pattern:
  # any future SG-replacing change (description, vpc_id) gets a random-suffix
  # name during the swap, so the EICE can move to the new SG before the old
  # one is destroyed. See modules/cache/main.tf:44-66 for the original
  # rationale this module inherits.
  name_prefix = "axiaops-${var.env_name}-eice-"
  description = "EC2 Instance Connect Endpoint egress: RDS 5432 only."
  vpc_id      = var.vpc_id

  tags = {
    Name = "axiaops-${var.env_name}-eice"
  }

  lifecycle {
    create_before_destroy = true
  }
}

# Egress: EICE -> RDS on 5432. This is the only TCP destination the endpoint
# ever needs to reach for the psql use case; declaring it narrowly keeps the
# blast radius if the EICE's IAM auth is ever misconfigured.
resource "aws_vpc_security_group_egress_rule" "eice_to_rds" {
  count = var.enabled ? 1 : 0

  security_group_id            = aws_security_group.eice[0].id
  referenced_security_group_id = var.rds_sg_id
  ip_protocol                  = "tcp"
  from_port                    = 5432
  to_port                      = 5432
  description                  = "Postgres to RDS"
}

# Ingress on the RDS SG (owned upstream in modules/network) sourced from the
# endpoint SG. Owned HERE rather than in network/ because (a) this module
# already holds the endpoint SG id and (b) leaving network/ ignorant of the
# operator-access path keeps that module focused on runtime traffic.

# Hazard note for future operators: the RDS SG (`var.rds_sg_id`) is owned by
# the network module and is pinned on a literal `name`, not `name_prefix`
# (modules/network/main.tf:77-98 explains why — apply_immediately=false on the
# RDS instance deadlocks create_before_destroy). If that SG ever goes through
# its documented painful replacement cycle, this ingress rule gets orphaned
# and TF will retry-destroy against the gone SG until the network module's
# OOB swap completes. Same pattern the cache module accepts at
# modules/cache/main.tf:84-90 with its `cache_from_runtime` rule.
resource "aws_vpc_security_group_ingress_rule" "rds_from_eice" {
  count = var.enabled ? 1 : 0

  security_group_id            = var.rds_sg_id
  referenced_security_group_id = aws_security_group.eice[0].id
  ip_protocol                  = "tcp"
  from_port                    = 5432
  to_port                      = 5432
  description                  = "Postgres from EC2 Instance Connect Endpoint (operator shell access)"
}

resource "aws_ec2_instance_connect_endpoint" "main" {
  count = var.enabled ? 1 : 0

  subnet_id          = var.subnet_id
  security_group_ids = [aws_security_group.eice[0].id]

  # Required `false` whenever the tunnel target is not a regular EC2 instance
  # (RDS, ElastiCache, etc.). `preserve_client_ip = true` rewrites the source
  # IP at the target ENI using an EC2-specific mechanism — AWS's API rejects
  # the value outright for non-EC2 ENIs, so leaving it `true` here would fail
  # `terraform apply` at the EICE resource with a validation error.
  preserve_client_ip = false

  tags = {
    Name = "axiaops-${var.env_name}-eice"
  }
}
