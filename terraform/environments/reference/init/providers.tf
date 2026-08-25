provider "aws" {
  region = var.region

  default_tags {
    tags = {
      ManagedBy = "terraform"
      Project   = "axiaops"
      Env       = var.env_name
      Stack     = "init"
    }
  }
}
