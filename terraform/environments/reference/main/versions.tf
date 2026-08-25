terraform {
  required_version = "~> 1.9.0"

  required_providers {
    aws = {
      source  = "hashicorp/aws"
      version = "~> 6.0"
    }
    random = {
      source  = "hashicorp/random"
      version = "~> 3.6"
    }
    tls = {
      source  = "hashicorp/tls"
      version = "~> 4.0"
    }
    null = {
      source  = "hashicorp/null"
      version = "~> 3.2"
    }
  }

  # State key is `reference/main/terraform.tfstate` (the `init/` root uses
  # `reference/init/terraform.tfstate`). The split is documented in
  # ../init/README.md.
  #
  # Partial backend config — bucket/region/dynamodb_table are supplied at
  # `terraform init -backend-config=backend.hcl` (see backend.hcl.example),
  # not hardcoded here, so this file carries no account-specific identity.
  backend "s3" {
    key     = "reference/main/terraform.tfstate"
    encrypt = true
  }
}
