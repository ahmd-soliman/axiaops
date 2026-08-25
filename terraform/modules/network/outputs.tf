output "vpc_id" {
  description = "ID of the main VPC."
  value       = aws_vpc.main.id
}

output "public_subnet_ids" {
  description = "IDs of the public subnets (one per AZ)."
  value       = aws_subnet.public[*].id
}

output "runtime_sg_id" {
  description = "Security group attached to all ECS Fargate tasks (Express runtime services + migrate task). Egress-only."
  value       = aws_security_group.ecs_runtime.id
}

output "rds_sg_id" {
  description = "Security group attached to the RDS instance."
  value       = aws_security_group.rds.id
}
