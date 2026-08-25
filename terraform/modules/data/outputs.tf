output "rds_endpoint" {
  description = "Connect-host portion of the RDS endpoint (no port)."
  value       = aws_db_instance.main.address
}

output "rds_port" {
  description = "RDS port (5432)."
  value       = aws_db_instance.main.port
}

output "rds_identifier" {
  description = "RDS instance identifier."
  value       = aws_db_instance.main.identifier
}

output "rds_arn" {
  description = "RDS instance ARN."
  value       = aws_db_instance.main.arn
}

output "db_name" {
  description = "Initial Postgres database name."
  value       = aws_db_instance.main.db_name
}

output "subnet_group_name" {
  description = "DB subnet group name (referenced by manual restore commands)."
  value       = aws_db_subnet_group.main.name
}
