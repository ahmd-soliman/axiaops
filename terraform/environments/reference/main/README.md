# Reference environment — main stack

This composes every module in `../../../modules/` into a deployable AxiaOps
environment: VPC, RDS, ElastiCache, ECS Express Mode services (api,
ingestion), the migrate one-off task, and the CloudFront/S3 dashboard edge.

Copy this whole `environments/reference/` directory to `environments/<name>/`
for a second environment (e.g. staging) — nothing here is environment-name-
hardcoded beyond what `var.env_name` parameterizes.

## Prerequisites

1. `../../../bootstrap/` has been applied.
2. `../../reference/init/` has been applied (provisions the GitHub Actions
   OIDC provider + CI roles this stack's `data "terraform_remote_state"
   "init"` reads from).
3. Credentials with admin-equivalent access on the target account for the
   first apply; CI uses the OIDC-federated role from `init/` after that.
4. Terraform 1.9.x via `tfenv` (honouring `../../../.terraform-version`).
5. An existing Route 53 hosted zone for your domain in this account —
   `module.edge` looks it up, it doesn't create one.

## Sensitive inputs (NEVER commit)

Most secrets are TF-generated (`random_password` for RDS, `random_id` for the
ingestion HMAC secret) or operator-injected via SSM out-of-band:

| Secret | Mechanism |
|---|---|
| RDS owner / app passwords | TF `random_password`, stored in SSM, never leaves state |
| Ingestion HMAC secret (`INGESTION_SHARED_SECRET[_NEXT]`) | TF `random_id` at first apply. During a rotation, set `TF_VAR_api_ingestion_secret_override` / `TF_VAR_ingestion_primary_secret_override` / `TF_VAR_ingestion_next_secret_override`. |
| `ENCRYPTION_KEY` | TF creates the SSM resource with a placeholder + `lifecycle.ignore_changes`. Operator writes the real key OOB after first apply (`init/`'s `make set-encryption-key`, or manually — see below). |

```bash
KEY="$(openssl rand -hex 32)"

# Same value in both api and ingestion prefixes — api encrypts on
# POST /accounts, ingestion decrypts during a scan.
aws ssm put-parameter --name /axiaops/<env>/api/ENCRYPTION_KEY \
  --value "$KEY" --type SecureString --overwrite
aws ssm put-parameter --name /axiaops/<env>/ingestion/ENCRYPTION_KEY \
  --value "$KEY" --type SecureString --overwrite
```

**Back this up to your password manager immediately after generating it.**
Losing it means every connected account's credentials, encrypted with it,
become unrecoverable — same blast radius as losing the RDS master password.

## First-apply sequence

Module dependencies mean a from-scratch apply generally goes:

```bash
cp terraform.tfvars.example terraform.tfvars   # fill in account/domain/bucket values first
terraform init -backend-config=../../../backend.hcl   # once, after bootstrap exists
terraform apply -target=module.network
terraform apply -target=module.secrets_passwords
terraform apply -target=module.data
terraform apply -target=module.cache
terraform apply -target=module.secrets_urls
terraform apply -target=module.iam
terraform apply -target=module.observability
terraform apply -target=module.compute
# (operator OOB: build+push images, run the migrate task, then flip the two
# Express services' containers via `aws ecs update-express-gateway-service` —
# see the ECS deployment guide in the project docs)
terraform apply -target=module.edge
# ACM DNS validation typically completes within a few minutes once the
# Route 53 record is created — module.edge's aws_acm_certificate_validation
# waits on it.
# After edge has been applied, capture the CloudFront distribution ID and
# tighten the CI role's invalidate policy from wildcard to the specific dist:
export TF_VAR_cloudfront_distribution_id="$(terraform output -raw cloudfront_distribution_id)"
terraform apply                                # final no-diff
```

Steady-state applies afterward are `terraform plan` + `terraform apply` with
no target.

## Short-lived test deploys (deploy → test → destroy)

The production-safe defaults in `variables.tf` are correct for a real
deployment, but they block `terraform destroy`:

| Default | Why it blocks destroy |
|---|---|
| `deletion_protection = true` (RDS) | AWS refuses the delete API call |
| `skip_final_snapshot = false` (RDS) | Destroy creates a final snapshot that costs storage indefinitely |
| `ecr_force_delete = false` | Repos with pushed images refuse the destroy |
| `dashboard_bucket_force_destroy = false` | S3 bucket with the SPA bundle refuses the destroy |

For a deploy you intend to tear down, copy the example overrides and use
`-var-file` on every apply / destroy:

```bash
cp terraform.tfvars.test.example terraform.tfvars.test
# terraform.tfvars.test is gitignored under the *.tfvars rule.

terraform apply  -var-file=terraform.tfvars.test
# ... test the stack ...
terraform destroy -var-file=terraform.tfvars.test
```

After destroy, the bootstrap state backend (S3 bucket + DynamoDB lock table)
survives — it's a separate config root, not covered by this destroy. Clean it
up manually if you're tearing the whole account down:

```bash
aws s3 rm s3://<your-state-bucket> --recursive
aws s3 rb s3://<your-state-bucket>
aws dynamodb delete-table --table-name <your-lock-table>
```

This is intentional — state buckets should never be auto-destroyed.

## Outputs consumed by CI

After the final apply, copy `github_ci_deploy_role_arn` into a GitHub Actions
secret the deploy workflow uses as its `role-to-assume` input.

Everything else the deploy workflow needs (ECR repo URLs, the runtime +
migrate cluster names, task/execution role ARNs, the Express service ARNs,
migrate SG + subnet IDs, secret ARNs, `public_host`/`cors_origin`, log group
names) is published to `/axiaops/<env>/platform/*` and fetched at deploy time
— see `platform_inventory.tf`. The deploy role is granted `ssm:GetParameters`
on exactly that prefix.

The Express services themselves are TF-owned
(`modules/compute/main.tf` — `aws_ecs_express_gateway_service.{api,ingestion}`,
created on apply with a busybox bootstrap container under
`lifecycle.ignore_changes = [primary_container]`); CI flips the container to
the real ECR image on each release via `aws ecs update-express-gateway-service`.
The migrate task runs via `aws ecs run-task` against the TF-managed task
definition.

`ecs_express_infrastructure_role_arn` is intentionally NOT published to the
SSM platform inventory: only TF creates Express services, and the CI deploy
role holds neither `iam:PassRole` for that role nor the
`ecs:CreateExpressGatewayService` verb.
