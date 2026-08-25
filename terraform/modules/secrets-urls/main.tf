locals {
  ssl_suffix = "?sslmode=verify-full&sslrootcert=${var.rds_ca_path}"

  database_url = format(
    "postgres://%s:%s@%s:%d/%s%s",
    var.app_user_username,
    var.app_user_password,
    var.rds_endpoint,
    var.rds_port,
    var.db_name,
    local.ssl_suffix,
  )

  migration_database_url = format(
    "postgres://%s:%s@%s:%d/%s%s",
    var.owner_username,
    var.owner_password,
    var.rds_endpoint,
    var.rds_port,
    var.db_name,
    local.ssl_suffix,
  )

  # axiaops_runtime — least-privilege RLS-bypass DSN the api/ingestion runtime
  # use instead of the owner DSN. Same verify-full ssl_suffix (prod RDS forces
  # SSL via rds.force_ssl=1). The role is created by app migration 029; the
  # migrate Bootstrap syncs its LOGIN+password from this URL.
  runtime_admin_database_url = format(
    "postgres://%s:%s@%s:%d/%s%s",
    var.runtime_admin_username,
    var.runtime_admin_password,
    var.rds_endpoint,
    var.rds_port,
    var.db_name,
    local.ssl_suffix,
  )

  # rediss:// — TLS-wrapped Redis. ElastiCache has transit_encryption_enabled
  # so the plain `redis://` scheme would fail handshake. AUTH token is the
  # password; user is "default" per Redis 6 ACL convention. DB index is 0.
  redis_url = format(
    "rediss://default:%s@%s:%d/%d",
    var.redis_auth_token,
    var.redis_endpoint,
    var.redis_port,
    var.redis_db_index,
  )
}

# --- api-prefixed copies (§8.2 path scoping for least-privilege) -------------

resource "aws_ssm_parameter" "database_url_api" {
  name  = "/axiaops/${var.env_name}/api/DATABASE_URL"
  type  = "SecureString"
  value = local.database_url
}

resource "aws_ssm_parameter" "encryption_key_api" {
  name  = "/axiaops/${var.env_name}/api/ENCRYPTION_KEY"
  type  = "SecureString"
  value = var.encryption_key_placeholder

  # TF owns the resource shape, the operator owns the value. TF creates the
  # SSM param once with the placeholder; the real 32-byte hex key is set via
  #   aws ssm put-parameter --name /axiaops/<env>/api/ENCRYPTION_KEY \
  #     --value "$KEY" --type SecureString --overwrite
  # The key never enters TF state, never enters CI variables, never
  # leaves the operator's password manager + the SSM SecureString store.
  lifecycle {
    ignore_changes = [value]
  }
}

resource "aws_ssm_parameter" "smtp_pass_api" {
  name  = "/axiaops/${var.env_name}/api/SMTP_PASS"
  type  = "SecureString"
  value = var.smtp_password_placeholder

  # api-only: the InviteMailer's global SMTP-relay password (a Gmail Workspace
  # App Password). An external credential — it can't be TF-generated, so it
  # follows the SMTP_PASS/ENCRYPTION_KEY OOB pattern: TF creates the param with the
  # placeholder, the operator writes the real value via
  #   aws ssm put-parameter --name /axiaops/<env>/api/SMTP_PASS \
  #     --value "$APP_PASSWORD" --type SecureString --overwrite
  # ingestion has NO SMTP_PASS (scan digests use per-org channels, not the
  # global relay). The value never enters TF state or CI variables.
  lifecycle {
    ignore_changes = [value]
  }
}

# Cloudflare Turnstile signup-CAPTCHA secret (docs/self-signup-plan.md). api-only:
# the server-side siteverify secret the register handler checks the widget token
# against. An external credential minted in the Cloudflare Turnstile dashboard, so
# it follows the ENCRYPTION_KEY OOB pattern: TF creates the SecureString
# with a placeholder, the operator writes the real value via
#   aws ssm put-parameter --name /axiaops/<env>/api/TURNSTILE_SECRET_KEY \
#     --value "$SECRET" --type SecureString --overwrite
# Empty/placeholder ⇒ the api leaves CAPTCHA unenforced (signup still works); the
# deploy job's api secrets[] wiring is graceful, so an unwritten value can't break
# a deploy. ingestion has NO Turnstile secret (no signup path). The value never
# enters TF state or CI variables.
resource "aws_ssm_parameter" "turnstile_secret_key_api" {
  name  = "/axiaops/${var.env_name}/api/TURNSTILE_SECRET_KEY"
  type  = "SecureString"
  value = var.turnstile_secret_key_placeholder

  lifecycle {
    ignore_changes = [value]
  }
}

resource "aws_ssm_parameter" "ingestion_shared_secret_api" {
  name  = "/axiaops/${var.env_name}/api/INGESTION_SHARED_SECRET"
  type  = "SecureString"
  value = var.api_ingestion_secret
}

resource "aws_ssm_parameter" "redis_url_api" {
  name        = "/axiaops/${var.env_name}/api/REDIS_URL"
  type        = "SecureString"
  value       = local.redis_url
  description = "rediss://default:<auth>@<endpoint>:6379/0 — TLS-wrapped Redis URL the api reads for rate-limit counters, session cache, and the queue producer."
}

resource "aws_ssm_parameter" "migration_database_url_api" {
  name  = "/axiaops/${var.env_name}/api/MIGRATION_DATABASE_URL"
  type  = "SecureString"
  value = local.migration_database_url

  # The api needs the OWNER connection to build its RLS-bypassing adminPool
  # (storage/postgres.NewWithOwner). Without it, adminPool silently falls back
  # to the RLS-bound app pool, and the pre-auth membership lookup in native
  # login — LookupUserByEmail reads the RLS-protected `memberships` table with
  # no app.organization_id set — returns zero rows, which the handler maps to
  # `invalid_credentials`. That breaks EVERY native login regardless of
  # password. The api does not run migrations; this is purely the owner pool.
  description = "Owner DSN for the api's RLS-bypassing adminPool (native-login membership lookup)."
}

resource "aws_ssm_parameter" "runtime_admin_database_url_api" {
  name  = "/axiaops/${var.env_name}/api/RUNTIME_ADMIN_DATABASE_URL"
  type  = "SecureString"
  value = local.runtime_admin_database_url

  # Least-privilege replacement for the owner DSN above: the api's adminPool
  # now connects as axiaops_runtime (RLS-bypass via per-table policies, no DDL).
  # The owner DSN stays migrate-task-only. See docs/runtime-admin-db-role.md.
  description = "Least-privilege RLS-bypass DSN (axiaops_runtime) for the api's adminPool."
}

# --- ingestion-prefixed copies -----------------------------------------------

resource "aws_ssm_parameter" "database_url_ingestion" {
  name  = "/axiaops/${var.env_name}/ingestion/DATABASE_URL"
  type  = "SecureString"
  value = local.database_url
}

resource "aws_ssm_parameter" "encryption_key_ingestion" {
  name  = "/axiaops/${var.env_name}/ingestion/ENCRYPTION_KEY"
  type  = "SecureString"
  value = var.encryption_key_placeholder

  # See `encryption_key_api` above for the operator OOB-write contract.
  # Must be set to the SAME value as the api copy — both services need
  # to derive the same AES-GCM key to decrypt customer credentials.
  lifecycle {
    ignore_changes = [value]
  }
}

resource "aws_ssm_parameter" "ingestion_shared_secret_primary" {
  name  = "/axiaops/${var.env_name}/ingestion/INGESTION_SHARED_SECRET"
  type  = "SecureString"
  value = var.ingestion_primary_secret
}

resource "aws_ssm_parameter" "ingestion_shared_secret_next" {
  name  = "/axiaops/${var.env_name}/ingestion/INGESTION_SHARED_SECRET_NEXT"
  type  = "SecureString"
  value = var.ingestion_next_secret
}

resource "aws_ssm_parameter" "redis_url_ingestion" {
  name        = "/axiaops/${var.env_name}/ingestion/REDIS_URL"
  type        = "SecureString"
  value       = local.redis_url
  description = "rediss://default:<auth>@<endpoint>:6379/0 — TLS-wrapped Redis URL the ingestion service reads for the queue consumer."
}

resource "aws_ssm_parameter" "migration_database_url_ingestion" {
  name  = "/axiaops/${var.env_name}/ingestion/MIGRATION_DATABASE_URL"
  type  = "SecureString"
  value = local.migration_database_url

  # Same rationale as the api copy: the ingestion service's owner pool
  # (ListAllAccounts for scheduled scans bypasses RLS via the adminPool) needs
  # the owner DSN, or it falls back to the RLS-bound app pool and silently
  # enumerates zero accounts. The ingestion service does not run migrations.
  description = "Owner DSN for the ingestion service's RLS-bypassing adminPool (scheduled-scan account enumeration)."
}

resource "aws_ssm_parameter" "runtime_admin_database_url_ingestion" {
  name  = "/axiaops/${var.env_name}/ingestion/RUNTIME_ADMIN_DATABASE_URL"
  type  = "SecureString"
  value = local.runtime_admin_database_url

  # Least-privilege replacement for the owner DSN above (scheduled-scan
  # ListAllAccounts cross-org enumeration). See docs/runtime-admin-db-role.md.
  description = "Least-privilege RLS-bypass DSN (axiaops_runtime) for the ingestion service's adminPool."
}

# --- migrate-prefixed copies -------------------------------------------------

resource "aws_ssm_parameter" "database_url_migrate" {
  name  = "/axiaops/${var.env_name}/migrate/DATABASE_URL"
  type  = "SecureString"
  value = local.database_url
}

resource "aws_ssm_parameter" "migration_database_url_migrate" {
  name  = "/axiaops/${var.env_name}/migrate/MIGRATION_DATABASE_URL"
  type  = "SecureString"
  value = local.migration_database_url
}

resource "aws_ssm_parameter" "runtime_admin_database_url_migrate" {
  name  = "/axiaops/${var.env_name}/migrate/RUNTIME_ADMIN_DATABASE_URL"
  type  = "SecureString"
  value = local.runtime_admin_database_url

  # The migrate Bootstrap reads this to CREATE ROLE axiaops_runtime LOGIN + sync
  # its password (mirrors how APP_USER_PASSWORD seeds the axiaops app user).
  description = "Runtime-role DSN the migrate Bootstrap uses to create + sync the axiaops_runtime role."
}

resource "aws_ssm_parameter" "app_user_password_migrate" {
  name  = "/axiaops/${var.env_name}/migrate/APP_USER_PASSWORD"
  type  = "SecureString"
  value = var.app_user_password
}
