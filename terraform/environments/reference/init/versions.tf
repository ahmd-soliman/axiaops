terraform {
  required_version = "~> 1.9.0"

  required_providers {
    aws = {
      source  = "hashicorp/aws"
      version = "~> 6.0"
    }
    tls = {
      source  = "hashicorp/tls"
      version = "~> 4.0"
    }
  }

  # State key sits next to main/'s state in the same bootstrap-created
  # bucket. Init applies locally with SSO admin; main applies via CI
  # using the role this root provisions.
  #
  # Partial backend config — bucket/region/dynamodb_table are supplied at
  # `terraform init -backend-config=backend.hcl` (see ../../../backend.hcl.example).
  backend "s3" {
    key     = "reference/init/terraform.tfstate"
    encrypt = true
  }
}
