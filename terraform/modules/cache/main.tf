#
# ElastiCache (Valkey) — single-node cache.t4g.micro, no replication.
#
# Engine: Valkey 7.2 — the BSD-3-licensed fork of Redis 7.2.4 that AWS
# ElastiCache, Debian, Ubuntu, and Fedora now default to. Speaks the same
# RESP wire protocol as Redis, so `github.com/redis/go-redis/v9` and the
# `rediss://` URL scheme continue to work unchanged. The variable / output
# names in this module retain the `redis_*` prefix on purpose — they describe
# the protocol (RESP), not the vendor, and renaming them would force a
# coordinated rename across secrets-urls, platform_inventory, and the
# axiaops CI variable lookups for zero functional gain. See the sibling
# the shared package's cache docs for the same posture
# applied to env var names (REDIS_URL stays).
#
# Sizing rationale: axiaops's api + ingestion both degrade gracefully when
# the cache is absent (rate limiter falls back to in-memory counters, queue
# falls back to sync HTTP) so we run the smallest cost-effective shape and
# avoid Multi-AZ. Mirrors the cost-conscious db.t4g.micro pattern of the
# data module.
#
# Cost note: cache.t4g.micro in eu-central-1 is approximately
# €11–14/month (~$13/mo on-demand). Single-AZ — no replica cost. Combined
# with the RDS db.t4g.micro and ECS Express Mode scale-to-zero ingestion this
# raises the steady-state stack from ~€24–34/mo to ~€35–48/mo. Valkey is
# ~20% cheaper than Redis OSS at the same node — small absolute saving here
# but it stacks with the licensing/governance reasons for the swap.
#
# Security posture:
#   - VPC-only — no public-access option exists on a subnet-group-bound
#     ElastiCache cluster.
#   - SG ingress on 6379 from the ECS runtime SG (Express services + migrate
#     task) + (optionally) a separate migrate task SG. No 0.0.0.0/0.
#   - In-transit + at-rest encryption forced on (transit_encryption_enabled,
#     at_rest_encryption_enabled).
#   - AUTH token required for connect. Generated here, surfaced via SSM
#     under the same /axiaops/<env>/infra/ prefix used for RDS passwords.
#

resource "aws_elasticache_subnet_group" "main" {
  name       = "axiaops-${var.env_name}-cache"
  subnet_ids = var.subnet_ids

  tags = {
    Name = "axiaops-${var.env_name}-cache"
  }
}

# No custom aws_elasticache_parameter_group resource — the replication group
# references AWS's family-managed default (`default.${family}`) directly. The
# previous custom resource carried zero `parameter {}` blocks; it was
# functionally identical to the AWS default but introduced an apply-time
# destroy-deadlock on family changes (parameter_group_family is immutable, so
# a redis7 → valkey7 swap forces destroy-recreate, but the still-running
# replication group references the old group and AWS rejects the destroy
# with InvalidCacheParameterGroupState — hit in the 2026-05-27 valkey-engine-
# swap apply). Pointing at default.* sidesteps the deadlock entirely.
#
# To customize parameters later: re-introduce the resource with the family
# encoded in the name (axiaops-${env_name}-cache-${parameter_group_family})
# + lifecycle.create_before_destroy = true so the next family bump replaces
# cleanly. AWS provider v6 doesn't accept name_prefix on
# aws_elasticache_parameter_group, so family-in-name is the working
# equivalent for replaceability.

resource "aws_security_group" "cache" {
  # name_prefix + create_before_destroy is the only way `aws_security_group`
  # can be cleanly replaced when something is attached. Background: every
  # SG-replacing change (`description`, `vpc_id`) triggers a destroy-then-
  # create cycle in the provider, but the new SG cannot adopt the old SG's
  # name while the old still exists (AWS SG names are unique per VPC), so
  # default `name`-pinned SGs deadlock with their parent resource holding
  # the old SG's ENI. Burned this on the App Runner -> ECS rename: cache
  # cluster ended up needing manual deletion to break the loop. With
  # name_prefix + create_before_destroy, AWS gets a random suffix
  # (axiaops-prod-cache-<hex>) so old + new can coexist briefly while TF
  # swaps the ElastiCache RG to the new SG, then destroys the old one.
  name_prefix = "axiaops-${var.env_name}-cache-"
  description = "ElastiCache Valkey ingress from ECS runtime tasks (+ migrate task) only."
  vpc_id      = var.vpc_id

  tags = {
    Name = "axiaops-${var.env_name}-cache"
  }

  lifecycle {
    create_before_destroy = true
  }
}

resource "aws_vpc_security_group_ingress_rule" "cache_from_runtime" {
  security_group_id            = aws_security_group.cache.id
  referenced_security_group_id = var.runtime_sg_id
  ip_protocol                  = "tcp"
  from_port                    = 6379
  to_port                      = 6379
  description                  = "RESP (Valkey) from ECS runtime tasks"
}

# Egress 6379 on the runtime SG -> cache. Owned here (not in the network
# module) because this module holds both the cache SG and the runtime SG id;
# putting it in network would form a network<->cache dependency cycle. This
# also closes a latent gap in the prior App Runner setup, whose egress only
# covered 5432 + 443 — port 6379 (RESP) was never reachable (the service
# never came up, so it never surfaced).
resource "aws_vpc_security_group_egress_rule" "runtime_to_cache" {
  security_group_id            = var.runtime_sg_id
  referenced_security_group_id = aws_security_group.cache.id
  ip_protocol                  = "tcp"
  from_port                    = 6379
  to_port                      = 6379
  description                  = "RESP (Valkey) to ElastiCache"
}

# Only when the migrate task has its OWN dedicated SG. When it shares the
# runtime SG (the default — migrate_sg_id = ""), the `cache_from_runtime`
# rule above already admits it. The precondition catches the second escape
# hatch — migrate_sg_id explicitly set to the runtime SG itself — which would
# fail the apply with InvalidPermission.Duplicate.
#
# count depends only on var.migrate_sg_id (literal at the call site, known at
# plan time). The runtime-SG equality check lives in the precondition so that
# when runtime_sg_id is "known after apply" (e.g. the App Runner -> ECS SG
# replacement) Terraform defers the check to apply instead of refusing to plan.
resource "aws_vpc_security_group_ingress_rule" "cache_from_migrate" {
  count = var.migrate_sg_id == "" ? 0 : 1

  security_group_id            = aws_security_group.cache.id
  referenced_security_group_id = var.migrate_sg_id
  ip_protocol                  = "tcp"
  from_port                    = 6379
  to_port                      = 6379
  description                  = "RESP (Valkey) from a dedicated ECS migrate task SG"

  lifecycle {
    precondition {
      condition     = var.migrate_sg_id != var.runtime_sg_id
      error_message = "migrate_sg_id must not equal runtime_sg_id — cache_from_runtime already covers the runtime SG and a duplicate rule would fail with InvalidPermission.Duplicate."
    }
  }
}

# AUTH token: 32 chars, alphanumeric only — ElastiCache rejects some special
# chars in AUTH tokens, and we splice the value into a URL-encoded
# rediss:// connection string, so keeping it alnum sidesteps both edges.
resource "random_password" "auth_token" {
  length  = 32
  special = false
  upper   = true
  lower   = true
  numeric = true
}

resource "aws_ssm_parameter" "auth_token" {
  # Name retained as `redis_auth_token` on purpose — referenced by the
  # secrets-urls module and downstream operator runbooks. The token gates a
  # RESP-speaking cache; the vendor (Redis → Valkey) is incidental.
  name        = "/axiaops/${var.env_name}/infra/redis_auth_token"
  type        = "SecureString"
  value       = random_password.auth_token.result
  description = "ElastiCache Valkey AUTH token — written into rediss:// connection strings by secrets-urls."
}

resource "aws_elasticache_replication_group" "main" {
  # `replication_group_id` and the `Name` tag retain the `-redis` suffix on
  # purpose. Renaming would force a destroy-then-create cycle on the cluster
  # itself (the ID is immutable and the name is a primary identifier in AWS
  # ops tooling). Engine swap below is supported in-place by AWS for the
  # redis 7.x → valkey 7.2 transition.
  replication_group_id = "axiaops-${var.env_name}-redis"
  description          = "axiaops ${var.env_name} Valkey - rate-limit counters, session cache, api-ingestion queue."

  engine                     = "valkey"
  engine_version             = var.engine_version
  node_type                  = var.node_type
  num_cache_clusters         = 1
  port                       = 6379
  parameter_group_name       = "default.${var.parameter_group_family}"
  subnet_group_name          = aws_elasticache_subnet_group.main.name
  security_group_ids         = [aws_security_group.cache.id]
  multi_az_enabled           = false
  automatic_failover_enabled = false

  # Encryption: in-transit (TLS on the wire) + at-rest (volume encryption).
  # AUTH token gates connect; required on every command.
  transit_encryption_enabled = true
  at_rest_encryption_enabled = true
  auth_token                 = random_password.auth_token.result

  snapshot_retention_limit = var.snapshot_retention_limit

  # Apply cluster modifications immediately rather than queuing them for the
  # next maintenance window (Thu 05:00 UTC). The 2026-05-27 Redis → Valkey
  # migration hit this footgun: the engine_version bump (7.1.0 → 7.2.6, a
  # prerequisite step AWS auto-picks before accepting an engine swap to
  # Valkey) was queued for the maintenance window, blocking the chain of
  # engine swap → param-group swap → app-side scan validation by a day.
  # With apply_immediately=true, terraform-driven cluster changes apply
  # within minutes (and we accept the ~30-60s cache blip — the api +
  # ingestion both fall back gracefully when the cache is unreachable, per
  # the queue/cache backend selection at services/shared/queue/queue.go
  # and services/shared/cache/cache.go).
  apply_immediately = true

  tags = {
    Name = "axiaops-${var.env_name}-redis"
  }
}
