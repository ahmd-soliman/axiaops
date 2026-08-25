terraform {
  required_version = "~> 1.9.0"

  required_providers {
    aws = {
      source  = "hashicorp/aws"
      version = "~> 6.0"
    }
  }

  # Bootstrap uses a LOCAL backend on purpose — it provisions the S3 bucket
  # that holds every other env's remote state, so it cannot live in that
  # bucket itself. Unlike the rest of this stack, this root's state is NOT
  # committed to git: it holds real account identity (the actual bucket/table
  # names) with no scrubbable placeholder. See bootstrap/README.md.
}
