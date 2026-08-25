variable "env_name" {
  description = "Environment name used to namespace SSM parameter paths."
  type        = string
}

variable "rds_endpoint" {
  description = "RDS endpoint host (data.rds_endpoint)."
  type        = string
}

variable "rds_port" {
  description = "RDS port (data.rds_port)."
  type        = number
}

variable "db_name" {
  description = "Postgres database name."
  type        = string
  default     = "axiaops"
}

variable "owner_username" {
  description = "Owner username (RDS master)."
  type        = string
  default     = "axiaops_owner"
}

variable "owner_password" {
  description = "Owner password from secrets-passwords."
  type        = string
  sensitive   = true
}

variable "app_user_username" {
  description = "App-user (RLS) username."
  type        = string
  default     = "axiaops"
}

variable "app_user_password" {
  description = "App-user password from secrets-passwords."
  type        = string
  sensitive   = true
}

variable "runtime_admin_username" {
  description = "Runtime RLS-bypass-no-DDL role username."
  type        = string
  default     = "axiaops_runtime"
}

variable "runtime_admin_password" {
  description = "axiaops_runtime password from secrets-passwords."
  type        = string
  sensitive   = true
}

# §8.3: three separate HMAC variables so each rotation step's apply touches
# only one slot. A single coupled var would flip api+ingestion atomically,
# defeating the zero-downtime rotation contract.
variable "api_ingestion_secret" {
  description = "What the api SIGNS with. Flipped in C-1 rotation Step 2."
  type        = string
  sensitive   = true
}

variable "ingestion_primary_secret" {
  description = "Ingestion verifier primary slot. Promoted in C-1 rotation Step 3 only."
  type        = string
  sensitive   = true
}

variable "ingestion_next_secret" {
  description = "Ingestion verifier staging slot. Staged in Step 1, cleared/aligned in Step 3."
  type        = string
  sensitive   = true
  default     = ""
}


variable "encryption_key_placeholder" {
  description = "Placeholder sentinel for ENCRYPTION_KEY before the operator overwrites it OOB. TF creates the resource shape, operator runs `aws ssm put-parameter` with the real 32-byte hex key. The value never enters TF state or CI variables."
  type        = string
  default     = "PLACEHOLDER_REPLACE_VIA_OUT_OF_BAND_SSM_PUT_PARAMETER"
}

variable "smtp_password_placeholder" {
  description = "Placeholder sentinel for the api's SMTP_PASS (the InviteMailer's global SMTP-relay App Password) before the operator overwrites it OOB. TF creates the SSM SecureString, operator runs `aws ssm put-parameter` with the real value. Never enters TF state or CI variables."
  type        = string
  default     = "PLACEHOLDER_REPLACE_VIA_OUT_OF_BAND_SSM_PUT_PARAMETER"
}

variable "turnstile_secret_key_placeholder" {
  description = "Placeholder sentinel for the api's TURNSTILE_SECRET_KEY (Cloudflare Turnstile secret for signup-CAPTCHA siteverify) before the operator overwrites it OOB. Same lifecycle as SMTP_PASS: TF creates the SSM SecureString, operator runs `aws ssm put-parameter` with the real value. Never enters TF state or CI variables."
  type        = string
  default     = "PLACEHOLDER_REPLACE_VIA_OUT_OF_BAND_SSM_PUT_PARAMETER"
}

variable "rds_ca_path" {
  description = "Path inside service containers where the RDS CA bundle is baked (§6.1.1)."
  type        = string
  default     = "/etc/ssl/certs/rds-ca-eu-central-1.pem"
}

# --- Redis (ElastiCache) -----------------------------------------------------

variable "redis_endpoint" {
  description = "ElastiCache primary endpoint hostname (cache.endpoint)."
  type        = string
}

variable "redis_port" {
  description = "Redis port (cache.port — typically 6379)."
  type        = number
  default     = 6379
}

variable "redis_auth_token" {
  description = "Redis AUTH token from the cache module — spliced into the rediss:// connection URL."
  type        = string
  sensitive   = true
}

variable "redis_db_index" {
  description = "Redis logical DB index. Always 0 for ElastiCache (multi-DB is disabled in cluster mode and a no-op anyway)."
  type        = number
  default     = 0
}
