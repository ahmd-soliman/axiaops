# Passwords have no data dep on the RDS endpoint — generating them in their
# own module breaks the secrets↔data cycle (design §3.2). Module ordering:
# secrets-passwords → data → secrets-urls.

resource "random_password" "owner" {
  length  = 40
  special = false
}

resource "random_password" "app_user" {
  length  = 40
  special = false
}

# axiaops_runtime — least-privilege RLS-bypass role (DML + per-table bypass
# policies, no DDL/ownership) used by the api/ingestion runtime instead of the
# owner connection. TF mints the password only; migration 029 + the migrate
# Bootstrap create the LOGIN role and sync this password.
resource "random_password" "runtime_admin" {
  length  = 40
  special = false
}

resource "aws_ssm_parameter" "owner_password" {
  name        = "/axiaops/${var.env_name}/infra/owner_password"
  type        = "SecureString"
  value       = random_password.owner.result
  description = "axiaops_owner (RDS master) password."
}

resource "aws_ssm_parameter" "app_user_password" {
  name        = "/axiaops/${var.env_name}/infra/app_user_password"
  type        = "SecureString"
  value       = random_password.app_user.result
  description = "axiaops (RLS app user) password — written to DATABASE_URL."
}

resource "aws_ssm_parameter" "runtime_admin_password" {
  name        = "/axiaops/${var.env_name}/infra/runtime_admin_password"
  type        = "SecureString"
  value       = random_password.runtime_admin.result
  description = "axiaops_runtime (RLS-bypass-no-DDL runtime role) password — written to RUNTIME_ADMIN_DATABASE_URL."
}
