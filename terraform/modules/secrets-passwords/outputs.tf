output "owner_password" {
  description = "Plaintext RDS master password — consumed by the data module to create the RDS instance."
  value       = random_password.owner.result
  sensitive   = true
}

output "app_user_password" {
  description = "Plaintext app-user password — composed into DATABASE_URL by secrets-urls."
  value       = random_password.app_user.result
  sensitive   = true
}

output "owner_password_arn" {
  description = "ARN of the SSM SecureString holding the owner password."
  value       = aws_ssm_parameter.owner_password.arn
}

output "app_user_password_arn" {
  description = "ARN of the SSM SecureString holding the app-user password."
  value       = aws_ssm_parameter.app_user_password.arn
}

output "runtime_admin_password" {
  description = "Plaintext axiaops_runtime password — composed into RUNTIME_ADMIN_DATABASE_URL by secrets-urls."
  value       = random_password.runtime_admin.result
  sensitive   = true
}

output "runtime_admin_password_arn" {
  description = "ARN of the SSM SecureString holding the axiaops_runtime password."
  value       = aws_ssm_parameter.runtime_admin_password.arn
}
