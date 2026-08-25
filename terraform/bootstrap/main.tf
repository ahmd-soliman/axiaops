locals {
  state_bucket_name = var.state_bucket_name != "" ? var.state_bucket_name : "axiaops-tf-state-${var.account_id}"
}

resource "aws_s3_bucket" "state" {
  bucket = local.state_bucket_name
}

resource "aws_s3_bucket_versioning" "state" {
  bucket = aws_s3_bucket.state.id

  versioning_configuration {
    status = "Enabled"
  }
}

resource "aws_s3_bucket_server_side_encryption_configuration" "state" {
  bucket = aws_s3_bucket.state.id

  rule {
    apply_server_side_encryption_by_default {
      # Aws-managed key — the marginal security of a CMK does not pay for
      # the +€1/mo and the IAM complexity at this scale (§4.1).
      sse_algorithm = "AES256"
    }
  }
}

resource "aws_s3_bucket_public_access_block" "state" {
  bucket = aws_s3_bucket.state.id

  block_public_acls       = true
  block_public_policy     = true
  ignore_public_acls      = true
  restrict_public_buckets = true
}

resource "aws_dynamodb_table" "lock" {
  name         = var.lock_table_name
  billing_mode = "PAY_PER_REQUEST"
  hash_key     = "LockID"

  attribute {
    name = "LockID"
    type = "S"
  }

  # Consistent with the state bucket's SSE posture above. Lock rows
  # carry no sensitive data, but a future reader shouldn't have to
  # squint to confirm "yes, the state backend is encrypted at rest
  # end-to-end". AWS-owned key → no additional cost.
  server_side_encryption {
    enabled = true
  }
}
