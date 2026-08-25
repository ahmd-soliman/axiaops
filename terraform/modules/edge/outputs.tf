output "dashboard_bucket_name" {
  description = "Name of the dashboard S3 bucket. CI uses this for `aws s3 sync`."
  value       = aws_s3_bucket.dashboard.id
}

output "dashboard_bucket_arn" {
  description = "ARN of the dashboard S3 bucket."
  value       = aws_s3_bucket.dashboard.arn
}

output "cloudfront_distribution_id" {
  description = "CloudFront distribution ID. CI uses this for `aws cloudfront create-invalidation`."
  value       = aws_cloudfront_distribution.main.id
}

output "cloudfront_distribution_arn" {
  description = "CloudFront distribution ARN."
  value       = aws_cloudfront_distribution.main.arn
}

output "cloudfront_domain_name" {
  description = "Default CloudFront domain (the dXX.cloudfront.net hostname)."
  value       = aws_cloudfront_distribution.main.domain_name
}

output "dashboard_url" {
  description = "User-facing dashboard URL."
  value       = "https://${local.fqdn}"
}

output "onboarding_cfn_template_url" {
  description = "Public https S3 URL of the AxiaOpsIntegrationRole CloudFormation template. Feed to the dashboard build as VITE_AXIAOPS_CFN_TEMPLATE_URL so the Connect screen's \"Launch Stack\" button can deep-link customers into CloudFormation."
  value       = "https://${aws_s3_bucket.onboarding.bucket}.s3.${var.region}.amazonaws.com/${aws_s3_object.onboarding_cfn_template.key}"
}

output "route53_zone_id" {
  description = "Route 53 hosted zone ID (looked up via data source)."
  value       = data.aws_route53_zone.main.zone_id
}

output "acm_certificate_arn" {
  description = "ARN of the validated us-east-1 ACM certificate."
  value       = aws_acm_certificate_validation.cloudfront.certificate_arn
}
