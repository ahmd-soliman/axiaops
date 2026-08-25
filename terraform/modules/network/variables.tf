variable "env_name" {
  description = "Short environment name (e.g. prod, staging) used as a name prefix."
  type        = string
}

variable "vpc_cidr" {
  description = "CIDR block for the VPC."
  type        = string
  default     = "10.0.0.0/16"
}

variable "public_subnet_cidrs" {
  description = "Two public subnet CIDRs, one per AZ."
  type        = list(string)
  default     = ["10.0.1.0/24", "10.0.2.0/24"]

  validation {
    condition     = length(var.public_subnet_cidrs) == 2
    error_message = "Exactly two public subnets are required (one per AZ) — RDS DB subnet groups need two AZs."
  }
}

variable "availability_zones" {
  description = "AZ names matching public_subnet_cidrs order."
  type        = list(string)
  default     = ["eu-central-1a", "eu-central-1b"]
}
