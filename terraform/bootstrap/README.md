# Bootstrap — Terraform state backend

**Run this exactly once per AWS account, at account inception. Never re-apply.**

This module provisions the S3 bucket and DynamoDB lock table that every other
environment's remote backend uses. It cannot itself live in that bucket — a
bucket can't hold the state that describes its own creation, so this one root
module uses a local backend instead.

## Prerequisites

- AWS credentials with `AdministratorAccess` on the target account.
- `tfenv` honouring `../.terraform-version`.
- A globally-unique S3 bucket name for state (defaults to
  `axiaops-tf-state-<account_id>`; override with `state_bucket_name` if that's
  taken).

## Run

```bash
cd terraform/bootstrap
terraform init           # uses a LOCAL backend
terraform apply -var account_id=<your-account-id>
```

`bootstrap`'s own state is **not** committed to git — unlike the rest of this
stack, it holds real account identity (the bucket/table names it just
created) with no placeholder to scrub. Keep `terraform.tfstate` local, or move
it to a private location of your choosing; either way, `terraform apply`
against this root only ever needs to run again on genuine drift or recovery
(see below), not as part of normal operations.

Subsequent envs (`environments/reference/`, `environments/staging/`) declare
the S3 backend in their `versions.tf` (as a **partial** backend config — no
account identity hardcoded there either) and supply the bucket/region/table
via `terraform init -backend-config=backend.hcl` (see `backend.hcl.example`
at the repo root of `terraform/`).

## Recovery

If the state bucket is deleted, recreate it manually with the same name and
the same lifecycle config (versioning, encryption, public-access-block), then
re-apply this module. Without a committed state snapshot to import against,
recovery means either `terraform import`-ing the recreated resources back in,
or accepting a fresh bucket/table under a new name and re-pointing every
other root's backend config at it.

Once the stack is stable, consider enabling S3 Object Lock (compliance mode)
on the bucket to prevent `aws s3 rb --force` from succeeding — deliberately
left off by default to keep initial-setup recovery cheap.
