variable "env_name" {
  description = "Environment name used as a resource-identifier prefix."
  type        = string
}

variable "vpc_id" {
  description = "VPC the endpoint lives in. Must be the same VPC as the RDS instance."
  type        = string
}

variable "subnet_id" {
  description = "Subnet for the EC2 Instance Connect Endpoint ENI. Any subnet in `vpc_id` works — the endpoint never accepts traffic from the subnet's route table (AWS-managed entry point) so public-vs-private is irrelevant for security; we use the first public subnet to match the rest of the stack's no-NAT posture."
  type        = string
}

variable "rds_sg_id" {
  description = "Security group attached to the RDS instance. The module adds an ingress rule on this SG sourced from its own endpoint SG."
  type        = string
}

variable "enabled" {
  description = "Toggle the endpoint and its SG plumbing on or off. EICE has no hourly idle charge (AWS bills only per-GB of data transferred while a tunnel is open), so the default `true` is genuinely near-zero-cost. Flip to `false` if you want the resource gone entirely — leaves no surface area, costs nothing to flip back."
  type        = bool
  default     = true
}
