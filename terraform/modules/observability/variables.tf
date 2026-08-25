variable "env_name" {
  description = "Environment name."
  type        = string
}

variable "log_retention_days" {
  description = "CloudWatch Logs retention (§11.1, §12.1)."
  type        = number
  default     = 7
}
