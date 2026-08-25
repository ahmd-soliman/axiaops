output "endpoint_id" {
  description = "EICE ID — pass to `aws ec2-instance-connect open-tunnel --instance-connect-endpoint-id`. `null` when disabled."
  value       = var.enabled ? aws_ec2_instance_connect_endpoint.main[0].id : null
}

output "endpoint_arn" {
  description = "EICE ARN — scope `ec2:OpenTunnel` IAM policies to this Resource. `null` when disabled."
  value       = var.enabled ? aws_ec2_instance_connect_endpoint.main[0].arn : null
}

output "endpoint_sg_id" {
  description = "Security group ID of the endpoint itself. Surfaced for debugging — the RDS-side ingress rule is already wired by this module. `null` when disabled."
  value       = var.enabled ? aws_security_group.eice[0].id : null
}
