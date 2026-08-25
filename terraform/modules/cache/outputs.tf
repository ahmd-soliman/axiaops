output "endpoint" {
  description = "Primary endpoint hostname of the Valkey replication group (no scheme, no port). For a single-node group this is the writer endpoint."
  value       = aws_elasticache_replication_group.main.primary_endpoint_address
}

output "port" {
  description = "Cache port (6379 — RESP)."
  value       = aws_elasticache_replication_group.main.port
}

output "auth_token" {
  description = "Plaintext AUTH token — consumed by secrets-urls to compose rediss://default:<token>@host:port/0. Output name retains the `auth_token` shape; the rediss:// scheme is RESP-over-TLS, vendor-neutral."
  value       = random_password.auth_token.result
  sensitive   = true
}

output "auth_token_ssm_parameter_name" {
  description = "SSM parameter name holding the cache AUTH token. Operators can `aws ssm get-parameter --with-decryption` for OOB recovery."
  value       = aws_ssm_parameter.auth_token.name
}

output "auth_token_ssm_parameter_arn" {
  description = "SSM ARN for the cache AUTH token. Reference target for downstream IAM scoping if a tighter policy is ever needed."
  value       = aws_ssm_parameter.auth_token.arn
}

output "security_group_id" {
  description = "Security group attached to the Valkey cluster (for visibility / debugging)."
  value       = aws_security_group.cache.id
}

output "replication_group_id" {
  description = "ElastiCache replication group ID (for `aws elasticache` CLI ops)."
  value       = aws_elasticache_replication_group.main.id
}
