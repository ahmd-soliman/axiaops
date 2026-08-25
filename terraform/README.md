# `terraform/` — AxiaOps infrastructure

Declarative state for every AWS resource an AxiaOps deployment needs, minus
one bootstrap-only exception (the Terraform state backend itself, which by
definition can't live in the state it creates).

This is a **reference implementation**, not a template with placeholders to
fill in — it's the same shape the hosted AxiaOps service runs in production.
Copy `environments/reference/` to `environments/<your-env>/`, supply your own
account/domain/bucket values, and apply.

## Layout

```
terraform/
├── bootstrap/                 one-time, local-backend module that provisions
│                              the S3 state bucket + DynamoDB lock table.
│                              Never re-applied. See bootstrap/README.md.
├── environments/
│   ├── reference/
│   │   ├── init/               GitHub Actions OIDC provider + CI IAM roles.
│   │   │                       Applied once locally with admin credentials.
│   │   └── main/                composes every module under modules/.
│   │                            S3 backend. Applied by CI thereafter.
│   └── staging/                reserved for a second environment — copy
│                                reference/ here when you need one.
└── modules/
    ├── network/               VPC, public subnets, IGW, security groups
    ├── secrets-passwords/     random_password for RDS owner + app users
    ├── data/                  RDS PostgreSQL (Single-AZ)
    ├── cache/                 ElastiCache Valkey
    ├── secrets-urls/          per-consumer SSM SecureString params
    │                          (DATABASE_URL, ENCRYPTION_KEY,
    │                          INGESTION_SHARED_SECRET[_NEXT], ...) — composed
    │                          from rds/cache endpoints + secrets-passwords
    ├── compute/               ECR repos, ECS cluster, ECS Express Mode
    │                          services (api, ingestion), migrate task
    ├── edge/                  S3 dashboard bucket, CloudFront, ACM
    │                          (us-east-1 alias), Route 53 records (consumes
    │                          an existing hosted zone — doesn't create one)
    ├── iam/                   GitHub Actions OIDC-federated CI deploy role;
    │                          ECS task/execution roles; migrate task role
    ├── observability/         CloudWatch log groups (declarative retention)
    └── ops-rds-access/        optional EC2 Instance Connect Endpoint for
                               operator psql/DBeaver access to RDS
```

The `secrets-passwords` ↔ `secrets-urls` split breaks a dependency cycle
each would otherwise form against `data`/`cache`.

## Prerequisites

1. An AWS account, with credentials that have admin-equivalent access for the
   one-time `bootstrap` and `init` applies.
2. `tfenv` installed; `.terraform-version` (1.9.x) is honoured.
3. A domain with an existing Route 53 hosted zone in this account — `edge`
   consumes it, it doesn't create one (creating a zone forces a manual
   nameserver hand-off at the registrar mid-`terraform apply`).
4. `bootstrap/` has been applied — that creates the S3 bucket
   `environments/reference/{init,main}` depend on. See `bootstrap/README.md`.

## Workflow

### One-time, at account inception

```bash
cd terraform/bootstrap
terraform init                               # local backend
terraform apply -var account_id=<your-account-id>
```

### One-time per account: OIDC + CI roles

```bash
cd terraform/environments/reference/init
cp ../../../backend.hcl.example backend.hcl  # fill in your bucket/region/table
terraform init -backend-config=backend.hcl
terraform apply -var account_id=<your-account-id> -var github_repository=<owner>/<repo>
make outputs                                 # role ARNs to paste into GitHub Actions secrets
```

### Per-environment first apply

```bash
cd terraform/environments/reference/main
cp terraform.tfvars.example terraform.tfvars   # fill in account/domain/bucket values
terraform init -backend-config=../../../backend.hcl
terraform plan
terraform apply
```

`terraform.tfvars.test.example` is a separate, narrower file — it only carries
the four flags that let a short-lived test deploy be `terraform destroy`'d
cleanly (see "Short-lived test deploys" in `environments/reference/main/README.md`).
It's not a starting point for a real first apply.

Sensitive inputs (HMAC override secrets, if you're rotating) are never put in
`terraform.tfvars` at all — supply them via `TF_VAR_*` in the shell instead.

After the first apply: run `init/`'s `make set-encryption-key` (mints
`ENCRYPTION_KEY` and writes it to SSM — the app can't start without it), then
push an image and let your deploy workflow run the migrate task and update
the two Express services.

### Steady-state

`terraform plan` + `terraform apply` with no target. Drift is expected on
each Express service's `primary_container` — CI mutates it via
`aws ecs update-express-gateway-service`, and `lifecycle.ignore_changes`
keeps that out of the plan diff; TF owns service *shape* (cluster, IAM,
scaling, networking), CI owns the running *container* (image, env, secrets).

## Outputs consumed by CI

The application repo's CI does NOT own a Terraform stack — the contract is
one-way: this stack exports outputs (role ARNs) and publishes non-secret
platform inventory to SSM (`/axiaops/<env>/platform/*`, see
`environments/reference/main/platform_inventory.tf`); the deploy workflow
reads both at runtime rather than having values pasted in as CI variables.

## Quality gates

Run before pushing:

```bash
terraform fmt -recursive
terraform -chdir=bootstrap validate
terraform -chdir=environments/reference/init init -backend=false && terraform -chdir=environments/reference/init validate
terraform -chdir=environments/reference/main init -backend=false && terraform -chdir=environments/reference/main validate
```

`terraform validate` needs the provider plugins but not AWS credentials —
`-backend=false` skips the remote-state check too, so this runs offline.

## What lives outside this directory

Not provisioned here:

- CI workflow configuration (`.github/workflows/`).
- Customer cross-account roles (live in the customer's own AWS account — see
  `modules/edge/files/AxiaOpsIntegrationRole.yaml.tftpl` for the template
  customers launch to create theirs).
- WAF, VPN, bastion, EventBridge cron, Multi-AZ RDS, multi-region — none of
  these exist in the reference stack; add them if you need them.
- Domain registration itself (registrar billing relationship — only the
  Route 53 records within an existing zone are TF-managed).
- GitHub Actions repository secrets (the role ARNs `init/` outputs still need
  to be pasted in once, manually).
