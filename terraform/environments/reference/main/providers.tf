provider "aws" {
  region = var.region

  default_tags {
    tags = {
      Project   = "axiaops"
      Env       = var.env_name
      ManagedBy = "terraform"
    }
  }
}

# CloudFront ACM certificates must live in us-east-1.
provider "aws" {
  alias  = "us_east_1"
  region = "us-east-1"

  default_tags {
    tags = {
      Project   = "axiaops"
      Env       = var.env_name
      ManagedBy = "terraform"
    }
  }
}

# DNS lives in Route 53 (module.edge's ACM validation + the app alias record
# both use it). `var.domain_name` must already be a hosted zone in this
# account — module.edge looks it up via `data "aws_route53_zone"` rather than
# creating it, since creating a zone would force a manual nameserver hand-off
# at the registrar mid-`terraform apply`. See modules/edge/main.tf.
#
# No separate DNS provider/credential needed: Route 53 is reached through the
# same `aws` provider above.
