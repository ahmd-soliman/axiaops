provider "aws" {
  region = var.region

  default_tags {
    tags = {
      Project   = "axiaops"
      ManagedBy = "terraform"
      Module    = "bootstrap"
      AccountId = var.account_id
    }
  }
}
