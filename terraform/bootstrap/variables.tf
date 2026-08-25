variable "region" {
  description = "AWS region for the state bucket and lock table."
  type        = string
  default     = "eu-central-1"
}

variable "account_id" {
  description = "AWS account ID owning the state bucket (appended to the bucket name). No default — must be set explicitly so a forgotten variable can't silently target the wrong account."
  type        = string
}

variable "state_bucket_name" {
  description = "Override the state bucket name. Default is axiaops-tf-state-<account_id>."
  type        = string
  default     = ""
}

variable "lock_table_name" {
  description = "DynamoDB table name for Terraform state locking. No default — pick a name unique to your account."
  type        = string
}
