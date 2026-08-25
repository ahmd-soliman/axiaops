output "database_url_api_arn" {
  description = "SSM ARN for the api's DATABASE_URL."
  value       = aws_ssm_parameter.database_url_api.arn
}

output "encryption_key_api_arn" {
  description = "SSM ARN for the api's ENCRYPTION_KEY."
  value       = aws_ssm_parameter.encryption_key_api.arn
}

output "smtp_pass_api_arn" {
  description = "SSM ARN for the api's SMTP_PASS placeholder (invite-relay App Password)."
  value       = aws_ssm_parameter.smtp_pass_api.arn
}

output "turnstile_secret_key_api_arn" {
  description = "SSM ARN for the api's TURNSTILE_SECRET_KEY placeholder (signup-CAPTCHA siteverify secret, OOB-written)."
  value       = aws_ssm_parameter.turnstile_secret_key_api.arn
}

output "ingestion_shared_secret_api_arn" {
  description = "SSM ARN for the api's INGESTION_SHARED_SECRET (the signer)."
  value       = aws_ssm_parameter.ingestion_shared_secret_api.arn
}

output "database_url_ingestion_arn" {
  description = "SSM ARN for the ingestion service's DATABASE_URL."
  value       = aws_ssm_parameter.database_url_ingestion.arn
}

output "encryption_key_ingestion_arn" {
  description = "SSM ARN for the ingestion service's ENCRYPTION_KEY."
  value       = aws_ssm_parameter.encryption_key_ingestion.arn
}


output "ingestion_shared_secret_primary_arn" {
  description = "SSM ARN for ingestion's INGESTION_SHARED_SECRET (verifier primary)."
  value       = aws_ssm_parameter.ingestion_shared_secret_primary.arn
}

output "ingestion_shared_secret_next_arn" {
  description = "SSM ARN for ingestion's INGESTION_SHARED_SECRET_NEXT (verifier staging slot)."
  value       = aws_ssm_parameter.ingestion_shared_secret_next.arn
}

output "database_url_migrate_arn" {
  description = "SSM ARN for the migrate task's DATABASE_URL."
  value       = aws_ssm_parameter.database_url_migrate.arn
}

output "migration_database_url_migrate_arn" {
  description = "SSM ARN for the migrate task's MIGRATION_DATABASE_URL."
  value       = aws_ssm_parameter.migration_database_url_migrate.arn
}

output "app_user_password_migrate_arn" {
  description = "SSM ARN for the migrate task's APP_USER_PASSWORD."
  value       = aws_ssm_parameter.app_user_password_migrate.arn
}

output "redis_url_api_arn" {
  description = "SSM ARN for the api's REDIS_URL (rediss:// with embedded AUTH token)."
  value       = aws_ssm_parameter.redis_url_api.arn
}

output "redis_url_ingestion_arn" {
  description = "SSM ARN for the ingestion service's REDIS_URL (rediss:// with embedded AUTH token)."
  value       = aws_ssm_parameter.redis_url_ingestion.arn
}

output "migration_database_url_api_arn" {
  description = "SSM ARN for the api's MIGRATION_DATABASE_URL (owner DSN for the RLS-bypassing adminPool)."
  value       = aws_ssm_parameter.migration_database_url_api.arn
}

output "migration_database_url_ingestion_arn" {
  description = "SSM ARN for the ingestion service's MIGRATION_DATABASE_URL (owner DSN for the RLS-bypassing adminPool)."
  value       = aws_ssm_parameter.migration_database_url_ingestion.arn
}

output "runtime_admin_database_url_api_arn" {
  description = "SSM ARN for the api's RUNTIME_ADMIN_DATABASE_URL (least-privilege RLS-bypass DSN)."
  value       = aws_ssm_parameter.runtime_admin_database_url_api.arn
}

output "runtime_admin_database_url_ingestion_arn" {
  description = "SSM ARN for the ingestion service's RUNTIME_ADMIN_DATABASE_URL."
  value       = aws_ssm_parameter.runtime_admin_database_url_ingestion.arn
}

output "runtime_admin_database_url_migrate_arn" {
  description = "SSM ARN for the migrate task's RUNTIME_ADMIN_DATABASE_URL (Bootstrap creates + syncs the runtime role)."
  value       = aws_ssm_parameter.runtime_admin_database_url_migrate.arn
}
