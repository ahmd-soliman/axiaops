variable "env_name" {
  description = "Environment name."
  type        = string
}

variable "region" {
  description = "AWS region."
  type        = string
  default     = "eu-central-1"
}

variable "account_id" {
  description = "AxiaOps AWS account ID. Baked into the onboarding CloudFormation template as the AxiaOpsScanner trust principal customers grant cross-account read access to."
  type        = string
}

variable "api_origin_domain" {
  description = "Live ECS Express ingress hostname for axiaops-api (no scheme, e.g. ax-6b90c30f...ecs.eu-central-1.on.aws). Used as the CloudFront /api/* and /v1/* origin. Sourced from module.compute.ecs_api_gateway_endpoint — AWS assigns a random per-service suffix, so this is NOT the deterministic name the origin previously hardcoded (which never resolved → CloudFront 502)."
  type        = string
}

variable "domain_name" {
  description = "Apex domain (e.g. example.com). DNS is managed via Route 53 — the hosted zone must already exist in this AWS account (see data \"aws_route53_zone\" \"main\" in main.tf)."
  type        = string
}

variable "app_subdomain" {
  description = "Dashboard subdomain (e.g. app)."
  type        = string
  default     = "app"
}

variable "dashboard_bucket_name" {
  description = "S3 bucket holding the dashboard SPA bundle."
  type        = string
}

variable "dashboard_bucket_force_destroy" {
  description = "Allow `terraform destroy` to nuke the dashboard S3 bucket even when it has objects. Default false (production-safe). Set true for short-lived test environments — without it, destroy fails the moment CI has uploaded the SPA bundle (i.e. always)."
  type        = bool
  default     = false
}
