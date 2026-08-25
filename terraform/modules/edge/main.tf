locals {
  fqdn = "${var.app_subdomain}.${var.domain_name}"
}

# --- Route 53 zone lookup -----------------------------------------------------
# The hosted zone is a prerequisite, not something this module creates —
# creating it here would force a manual nameserver hand-off at the registrar
# in the middle of `terraform apply`. Point `var.domain_name` at a zone that
# already exists in this account.

data "aws_route53_zone" "main" {
  name = var.domain_name
}

# --- ACM cert in us-east-1 (CloudFront requirement) --------------------------

resource "aws_acm_certificate" "cloudfront" {
  provider = aws.us_east_1

  domain_name       = local.fqdn
  validation_method = "DNS"

  lifecycle {
    create_before_destroy = true
  }
}

# ACM DNS-01 validation record, published into Route 53.
resource "aws_route53_record" "cloudfront_validation" {
  for_each = {
    for dvo in aws_acm_certificate.cloudfront.domain_validation_options : dvo.domain_name => {
      name   = dvo.resource_record_name
      record = dvo.resource_record_value
      type   = dvo.resource_record_type
    }
  }

  zone_id = data.aws_route53_zone.main.zone_id
  name    = each.value.name
  type    = each.value.type
  records = [each.value.record]
  ttl     = 60
  # ACM sometimes reissues a validation record with the same name+type on a
  # cert renewal — without this, a second apply that hits an already-present
  # record errors instead of updating it in place.
  allow_overwrite = true
}

resource "aws_acm_certificate_validation" "cloudfront" {
  provider = aws.us_east_1

  certificate_arn         = aws_acm_certificate.cloudfront.arn
  validation_record_fqdns = [for r in aws_route53_record.cloudfront_validation : r.fqdn]
}

# --- S3 dashboard bucket -----------------------------------------------------

resource "aws_s3_bucket" "dashboard" {
  bucket = var.dashboard_bucket_name

  # Set true for test environments you intend to destroy. The bucket
  # holds the Vite SPA bundle which CI re-uploads on every deploy, so
  # objects regenerate cheaply if destroyed. Without it, destroy fails
  # whenever the bucket has any object — which is essentially always
  # after the first deploy.
  force_destroy = var.dashboard_bucket_force_destroy
}

resource "aws_s3_bucket_public_access_block" "dashboard" {
  bucket = aws_s3_bucket.dashboard.id

  block_public_acls       = true
  block_public_policy     = true
  ignore_public_acls      = true
  restrict_public_buckets = true
}

resource "aws_s3_bucket_server_side_encryption_configuration" "dashboard" {
  bucket = aws_s3_bucket.dashboard.id

  rule {
    apply_server_side_encryption_by_default {
      sse_algorithm = "AES256"
    }
  }
}

# --- CloudFront --------------------------------------------------------------

resource "aws_cloudfront_origin_access_control" "s3" {
  name                              = "axiaops-${var.env_name}-dashboard-oac"
  description                       = "OAC for dashboard S3 bucket."
  origin_access_control_origin_type = "s3"
  signing_behavior                  = "always"
  signing_protocol                  = "sigv4"
}

resource "aws_cloudfront_response_headers_policy" "spa" {
  name = "axiaops-${var.env_name}-spa"

  security_headers_config {
    strict_transport_security {
      access_control_max_age_sec = 31536000
      include_subdomains         = true
      preload                    = true
      override                   = true
    }

    content_type_options {
      override = true
    }

    frame_options {
      frame_option = "DENY"
      override     = true
    }

    referrer_policy {
      referrer_policy = "strict-origin-when-cross-origin"
      override        = true
    }
  }
}

resource "aws_cloudfront_function" "api_rewrite" {
  name    = "axiaops-${var.env_name}-api-rewrite"
  runtime = "cloudfront-js-1.0"
  comment = "Strip /api/ prefix; block /api/metrics externally (§9.4)."
  publish = true
  code    = <<-EOT
    function handler(event) {
      var r = event.request;
      if (r.uri === '/api/metrics') {
        return { statusCode: 404, statusDescription: 'Not Found' };
      }
      if (r.uri.startsWith('/api/')) {
        r.uri = r.uri.substring(4);
      }
      return r;
    }
  EOT
}

resource "aws_cloudfront_function" "spa_fallback" {
  name    = "axiaops-${var.env_name}-spa-fallback"
  runtime = "cloudfront-js-1.0"
  comment = "Rewrite extensionless non-asset paths to /index.html for SPA routing (§9.4)."
  publish = true
  code    = <<-EOT
    function handler(event) {
      var r = event.request;
      var uri = r.uri;
      if (uri === '/' || uri === '') {
        r.uri = '/index.html';
        return r;
      }
      if (uri.startsWith('/assets/')) {
        return r;
      }
      var lastDot = uri.lastIndexOf('.');
      var lastSlash = uri.lastIndexOf('/');
      if (lastDot <= lastSlash) {
        r.uri = '/index.html';
      }
      return r;
    }
  EOT
}

resource "aws_cloudfront_distribution" "main" {
  enabled         = true
  is_ipv6_enabled = true
  comment         = "axiaops-${var.env_name} dashboard"
  http_version    = "http2and3"
  aliases         = [local.fqdn]
  price_class     = "PriceClass_100"

  origin {
    domain_name              = aws_s3_bucket.dashboard.bucket_regional_domain_name
    origin_id                = "S3-spa"
    origin_access_control_id = aws_cloudfront_origin_access_control.s3.id
  }

  # ECS Express Mode publishes the api service at an AWS-owned hostname under
  # *.ecs.<region>.on.aws (AWS owns the DNS + cert, matched via SNI). Design
  # open question #5 is RESOLVED: that hostname carries a random per-service
  # suffix (ax-<hex>.ecs.<region>.on.aws), so it is NOT knowable at plan time.
  # The deterministic guess "axiaops-api.ecs.<region>.on.aws" does not resolve
  # → CloudFront returned 502 "couldn't resolve the origin domain name". The
  # real hostname is now threaded in from module.compute (the resource's
  # computed ingress_paths endpoint), single apply — no SSM two-pass needed
  # since the service already exists when this origin is (re)computed.
  origin {
    domain_name = var.api_origin_domain
    origin_id   = "ECS-api"

    custom_origin_config {
      http_port              = 80
      https_port             = 443
      origin_protocol_policy = "https-only"
      origin_ssl_protocols   = ["TLSv1.2"]
    }
  }

  default_cache_behavior {
    target_origin_id       = "S3-spa"
    viewer_protocol_policy = "redirect-to-https"
    allowed_methods        = ["GET", "HEAD", "OPTIONS"]
    cached_methods         = ["GET", "HEAD"]
    compress               = true

    # AWS-managed CachingOptimized policy (assets fingerprinted; index.html
    # and runtime-env.js get no-store via S3 object metadata per §9.3).
    cache_policy_id            = "658327ea-f89d-4fab-a63d-7e88639e58f6"
    response_headers_policy_id = aws_cloudfront_response_headers_policy.spa.id

    function_association {
      event_type   = "viewer-request"
      function_arn = aws_cloudfront_function.spa_fallback.arn
    }
  }

  ordered_cache_behavior {
    path_pattern           = "/api/*"
    target_origin_id       = "ECS-api"
    viewer_protocol_policy = "https-only"
    allowed_methods        = ["GET", "HEAD", "OPTIONS", "PUT", "POST", "PATCH", "DELETE"]
    cached_methods         = ["GET", "HEAD"]
    compress               = true

    # AWS-managed CachingDisabled + AllViewerExceptHostHeader.
    # NOT AllViewer: AllViewer forwards the viewer Host header (app.example.com)
    # to the custom origin, and CloudFront then uses it as the TLS SNI to the
    # ECS Express gateway — whose cert is for ax-<hex>.ecs.<region>.on.aws, not
    # app.example.com. The handshake fails and CloudFront returns 502
    # "can't connect to server". AllViewerExceptHostHeader forwards everything
    # (Authorization, Cookie, query strings) EXCEPT Host, so CloudFront uses the
    # origin domain for SNI/Host and the gateway routes correctly. The API
    # derives nothing from the viewer Host (PUBLIC_HOST env + X-Forwarded-Proto).
    cache_policy_id          = "4135ea2d-6df8-44a3-9df3-4b5a84be39ad"
    origin_request_policy_id = "b689b0a8-53d0-40ab-baf2-68738e2966ac"

    function_association {
      event_type   = "viewer-request"
      function_arn = aws_cloudfront_function.api_rewrite.arn
    }
  }

  ordered_cache_behavior {
    path_pattern           = "/v1/*"
    target_origin_id       = "ECS-api"
    viewer_protocol_policy = "https-only"
    allowed_methods        = ["GET", "HEAD", "OPTIONS", "PUT", "POST", "PATCH", "DELETE"]
    cached_methods         = ["GET", "HEAD"]
    compress               = true

    # AllViewerExceptHostHeader — see the /api/* behavior above for why NOT
    # AllViewer (forwarded Host → SNI mismatch → 502 on the custom ECS origin).
    cache_policy_id          = "4135ea2d-6df8-44a3-9df3-4b5a84be39ad"
    origin_request_policy_id = "b689b0a8-53d0-40ab-baf2-68738e2966ac"
  }

  restrictions {
    geo_restriction {
      restriction_type = "none"
    }
  }

  viewer_certificate {
    acm_certificate_arn      = aws_acm_certificate_validation.cloudfront.certificate_arn
    ssl_support_method       = "sni-only"
    minimum_protocol_version = "TLSv1.2_2021"
  }
}

# OAC requires a bucket policy granting CloudFront-only read.
data "aws_iam_policy_document" "dashboard_bucket" {
  statement {
    sid       = "CloudFrontOACRead"
    effect    = "Allow"
    actions   = ["s3:GetObject"]
    resources = ["${aws_s3_bucket.dashboard.arn}/*"]

    principals {
      type        = "Service"
      identifiers = ["cloudfront.amazonaws.com"]
    }

    condition {
      test     = "StringEquals"
      variable = "AWS:SourceArn"
      values   = [aws_cloudfront_distribution.main.arn]
    }
  }
}

resource "aws_s3_bucket_policy" "dashboard" {
  bucket = aws_s3_bucket.dashboard.id
  policy = data.aws_iam_policy_document.dashboard_bucket.json
}

# --- Route 53 alias for the app subdomain -------------------------------------
# Alias records (not CNAME) so the record can resolve at zone apex if
# app_subdomain is ever empty, and so there's no extra DNS lookup / query
# charge on top of what CloudFront already costs — an alias resolves directly
# to CloudFront's edge IPs. Both A and AAAA since the distribution has
# is_ipv6_enabled=true.

resource "aws_route53_record" "app" {
  zone_id = data.aws_route53_zone.main.zone_id
  name    = var.app_subdomain
  type    = "A"

  alias {
    name                   = aws_cloudfront_distribution.main.domain_name
    zone_id                = aws_cloudfront_distribution.main.hosted_zone_id
    evaluate_target_health = false
  }
}

resource "aws_route53_record" "app_ipv6" {
  zone_id = data.aws_route53_zone.main.zone_id
  name    = var.app_subdomain
  type    = "AAAA"

  alias {
    name                   = aws_cloudfront_distribution.main.domain_name
    zone_id                = aws_cloudfront_distribution.main.hosted_zone_id
    evaluate_target_health = false
  }
}
