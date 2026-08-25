# Reads outputs from the sibling `init/` root.
#
# `init/` provisions the GitHub Actions OIDC provider + the two CI roles.
# `main/` needs the OIDC provider ARN to wire the trust policy for the
# `github-actions-axiaops-deploy` app-deploy role (modules/iam).
#
# Cross-stack reference, not a TF-graph dependency — `init/` must have been
# applied before `main/` is applied. The operator runs `init/` once locally;
# CI applies `main/` forever after.

data "terraform_remote_state" "init" {
  backend = "s3"
  config = {
    bucket = var.state_bucket_name
    key    = "reference/init/terraform.tfstate"
    region = var.region
  }
}
