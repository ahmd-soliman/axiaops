# `init/` — the irreducible local apply

This root module provisions the only AWS resources that genuinely **cannot**
be provisioned by CI:

- The GitHub Actions OIDC provider in IAM.
- The two roles CI assumes (`github-actions-axiaops-terraform` for apply,
  `github-actions-axiaops-terraform-plan` for PR plan).
- The Permissions Boundary that hard-caps what those roles can ever do.

It's applied **once per AWS account** by an operator with admin-equivalent
credentials. After that, every subsequent change to the stack flows through
CI using the roles this module created.

## Prerequisites

You need **admin-equivalent AWS credentials** configured locally before any
command in this README will work.

You also need:

- `terraform` 1.9.x. Use `tfenv` to honour the repo's `.terraform-version`,
  or the `hashicorp/terraform:1.9` docker image. Local terraform 1.10+ is
  rejected by `required_version = "~> 1.9.0"`.
- `terraform/bootstrap` applied (creates the S3 state bucket this stack uses
  as its backend).

## When to apply

- **First-ever deploy of this AWS account**: apply once.
- **Trust-policy widening or tightening**: edit `variables.tf`
  (`github_apply_refs` / `github_plan_refs`), re-apply.
- **CI role policy hardening**: edit `main.tf` (boundary / inline policy),
  re-apply.
- **Never in steady-state operation.** This is bootstrap.

## How

```bash
cd terraform/environments/reference/init
cp terraform.tfvars.example terraform.tfvars   # fill in account_id + github_repository
cp ../../../backend.hcl.example backend.hcl    # fill in your bucket/region/table
terraform init -backend-config=backend.hcl
make plan        # review what's about to land
make apply
make outputs     # role ARNs + OIDC provider ARN
```

Or the all-in-one:

```bash
make first-deploy
```

Which runs apply and prints the next-step instructions for the OOB ceremony
that accompanies a first deploy.

## The OOB targets

This Makefile also bundles the operator-only ceremony steps that aren't
Terraform actions but happen at the same time:

```bash
# After main/ has been applied at least once:
make set-encryption-key            # generates new key, writes to SSM, prints for backup
```

This isn't a `terraform apply` invocation — it's an `aws ssm put-parameter`
shell command. The Makefile bundles it with the `init/` apply so the operator
has one place to look for "everything I do by hand."

## Why two roles instead of one

| Role | When assumed | What it can do |
|---|---|---|
| `github-actions-axiaops-terraform` | Runs on `main` | Broad apply, bounded by `github-actions-axiaops-terraform-boundary` |
| `github-actions-axiaops-terraform-plan` | PR runs (any branch) | Read-only + DynamoDB lock acquire/release for plan |

Splitting these lets PR reviewers see plan diffs (which requires reading
state) without giving every PR branch the ability to mutate AWS resources.
The plan role's IAM policy doesn't grant any `*Create*` / `*Update*` /
`*Delete*` actions — even if a compromised token for the plan role leaks, the
damage ceiling is "can read your AWS resources," not "can change them."

## Why a Permissions Boundary

The inline policy on `github-actions-axiaops-terraform` is intentionally
broad — it needs to manage almost every resource type in the stack. The
Permissions Boundary is the actual ceiling:

- IAM mutations restricted to `axiaops-*` named roles + policies. CI can
  manage the ECS roles `main/` creates but cannot create arbitrary IAM
  resources.
- Explicit `Deny` on mutations to the init-managed roles themselves (the CI
  role cannot rewrite its own trust policy or permissions).
- Everything else (EC2, RDS, ElastiCache, ECR, ECS, CloudFront, ACM, Route
  53, CloudWatch Logs, SSM, S3, DynamoDB) is admitted.

This is defence in depth: even if a future change to the role's inline
policy broadens it (intentionally or not), the boundary prevents the
broadened permissions from taking effect.

## State

Backed by S3 at `s3://<your-state-bucket>/reference/init/terraform.tfstate`,
using the lock table from `bootstrap`. The same bucket that holds `main/`'s
state. **`init/` always applies against this S3 backend** — local operation
is about who's authorised to assume the bootstrap-tier identity (only an
operator with admin credentials, never CI), not about where state lives.

### Why the S3 location is load-bearing

`main/`'s `data_init.tf` reads this state via `terraform_remote_state` to
discover the OIDC provider ARN. If init's state isn't at that S3 key, the
first CI plan job fails with:

```
Error: Unable to find remote state
  with data.terraform_remote_state.init,
  on data_init.tf line 11, in data "terraform_remote_state" "init":
```

### Don't redirect the backend to a local file

The repo's `terraform/.gitignore` includes `*_override.tf`, so nothing
stops you from dropping a `backend_override.tf` here that points the
backend at a local file. **Don't.** There's no scenario where it helps:

- `terraform plan` against the real S3 backend is already safe — it
  acquires the DynamoDB state lock for the duration of the plan and
  releases it on exit. No partial-write risk.
- `terraform apply` against a local override puts the state ledger on
  your laptop while AWS still gets the real resources, leaving `main/`'s
  `terraform_remote_state` lookup pointing at an empty S3 key. The error
  above is the inevitable result.
- For a no-state-write dry-run, use `terraform plan -lock=false
  -lock-timeout=0s` against S3 (read-only, no lock acquisition).

The `*_override.tf` gitignore pattern exists as a generic Terraform
escape hatch for situations that don't apply here. For `init/`, treat
the S3 backend as non-negotiable: `terraform init` + `terraform apply`
write to S3 from the very first run on a fresh account.

## Destroy

Don't, ordinarily. Destroying `init/` revokes CI's ability to manage anything
else. If you genuinely need to nuke everything:

```bash
# Destroy main/ first (via CI or local):
cd ../main && terraform destroy

# Then destroy init/:
cd ../init && terraform destroy

# Then manual cleanup of the bootstrap state backend:
aws s3 rm s3://<your-state-bucket> --recursive
aws s3 rb s3://<your-state-bucket>
aws dynamodb delete-table --table-name <your-lock-table>
```
