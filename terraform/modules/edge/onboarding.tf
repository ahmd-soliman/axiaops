# --- Onboarding CloudFormation template (PUBLIC) -----------------------------
# Vantage-style one-click "Launch Stack": the dashboard Connect screen deep-links
# the customer into the CloudFormation console with this template's URL and their
# ExternalId pre-filled. CloudFormation's TemplateURL must be a PUBLIC https S3
# URL — it does not accept a CloudFront/custom-domain URL — so this bucket is
# intentionally public-read, unlike the OAC-locked dashboard bucket above.
#
# Safe to be public: the template is non-secret. The only per-customer value
# (ExternalId) is supplied as a stack parameter at launch time, never baked into
# the file. The template grants the customer's own account a read-only role; it
# cannot be abused by a third party who reads it.
#
# NOTE: if the AWS account has account-level S3 Block Public Access enabled, the
# public bucket policy below is overridden and the template won't be reachable.
# This bucket needs account-level BPA to permit bucket-policy public grants (the
# per-bucket block settings here are already relaxed for exactly this object).

locals {
  onboarding_cfn_key = "AxiaOpsIntegrationRole.yaml"
  onboarding_cfn_body = templatefile(
    "${path.module}/files/AxiaOpsIntegrationRole.yaml.tftpl",
    { axiaops_account_id = var.account_id }
  )
}

resource "aws_s3_bucket" "onboarding" {
  bucket = "axiaops-${var.env_name}-onboarding"
}

resource "aws_s3_bucket_public_access_block" "onboarding" {
  bucket = aws_s3_bucket.onboarding.id

  # Block public ACLs (we grant read via bucket policy, not ACLs), but allow a
  # public bucket policy — that's how CloudFormation fetches the template.
  block_public_acls       = true
  ignore_public_acls      = true
  block_public_policy     = false
  restrict_public_buckets = false
}

resource "aws_s3_bucket_server_side_encryption_configuration" "onboarding" {
  bucket = aws_s3_bucket.onboarding.id

  rule {
    apply_server_side_encryption_by_default {
      sse_algorithm = "AES256"
    }
  }
}

# Versioned so a bad template apply (e.g. wrong account id) can be rolled back —
# this object is the live "Launch Stack" target for every new customer, so a bad
# deploy window breaks onboarding until corrected.
resource "aws_s3_bucket_versioning" "onboarding" {
  bucket = aws_s3_bucket.onboarding.id

  versioning_configuration {
    status = "Enabled"
  }
}

resource "aws_s3_object" "onboarding_cfn_template" {
  bucket        = aws_s3_bucket.onboarding.id
  key           = local.onboarding_cfn_key
  content       = local.onboarding_cfn_body
  content_type  = "text/yaml"
  cache_control = "public, max-age=300"
  # Re-upload when the rendered template changes (e.g. permission-list edits).
  etag = md5(local.onboarding_cfn_body)
}

data "aws_iam_policy_document" "onboarding_public_read" {
  statement {
    sid       = "PublicReadOnboardingTemplate"
    effect    = "Allow"
    actions   = ["s3:GetObject"]
    resources = ["${aws_s3_bucket.onboarding.arn}/*"]

    principals {
      type        = "*"
      identifiers = ["*"]
    }
  }
}

resource "aws_s3_bucket_policy" "onboarding" {
  bucket = aws_s3_bucket.onboarding.id
  policy = data.aws_iam_policy_document.onboarding_public_read.json

  # The policy is a public grant; it must be applied after the public-access
  # block is relaxed or S3 rejects it.
  depends_on = [aws_s3_bucket_public_access_block.onboarding]
}
