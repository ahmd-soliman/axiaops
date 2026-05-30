# CI/CD Secrets Setup — GitLab Variables Configuration

Complete checklist for configuring GitLab CI/CD variables and AWS secrets for AxiaOps.

> ⚠️ **STALE — pre-SSO Phase B (April 2026).** This doc was written before native
> SSO, licensing, per-env self-hosted deploys, and the strangler auth pattern. Source
> of truth has moved:
>
> - **CI variables**: inline comments in `.gitlab-ci.yml` (especially the
>   `.deploy-dev` template's required-variables block) + `CLAUDE.md`
>   "Deployment topology" section
> - **License signing key + ceremony**: `docs/license-issuance.md`
> - **Per-service env vars**: `services/{api,ingestion,shared,dashboard}/CLAUDE.md`
>
> Kept here for historical reference; do not follow as setup steps.

---

## Overview

GitLab CI/CD needs access to:
1. **AWS OIDC role** — the `deploy:production` job assumes `gitlab-ci-axiaops-deploy` via `sts:AssumeRoleWithWebIdentity` (no static AWS keys). The role is provisioned in `axiaops/aws-infra`.
2. **Encryption key** — to encrypt/decrypt AWS secrets in production
3. **Ingestion shared secret** — for api → ingestion HMAC signing
4. **CloudFront distribution ID** — to invalidate cache after dashboard deployment

All secrets should be:
- ✓ **Masked** — hidden in job logs
- ✓ **Protected** — only available on protected branches / tags

---

## Step 1: AWS OIDC deploy role

Production CI uses **GitLab OIDC** — no IAM user, no static access keys. The `deploy:production` job exchanges a GitLab `id_token` for short-lived AWS credentials via `sts:AssumeRoleWithWebIdentity`, assuming the role `gitlab-ci-axiaops-deploy`.

The role, its trust policy, and its permission boundary are defined in the `axiaops/aws-infra` Terraform repo. See [`aws-infra/docs/terraform-prod-design.md`](https://gitlab.com/axiaops/aws-infra/-/blob/main/docs/terraform-prod-design.md) for the exact IAM shape. Add `AWS_CI_ROLE_ARN` (the role ARN, from Terraform output) to GitLab CI/CD variables.

---

## Step 2: Generate ENCRYPTION_KEY

The encryption key is used to encrypt AWS secrets at rest in the PostgreSQL database.

### 2.1 Generate 32-byte Hex Key

```bash
# macOS / Linux
openssl rand -hex 32

# Output: 
# 3f9c8a7e2d5b4c6f1a9e8d7c6b5a4f3e2d1c0b9a8f7e6d5c4b3a2f1e0d9c8b

# Copy this value
```

**Requirements:**
- Exactly 32 bytes (64 hex characters)
- Secure random generation
- Must be rotated every 12 months (with DB re-encryption)

---

## Step 3: Auth credentials (post-Kinde removal)

> **Kinde is removed.** The `VITE_KINDE_ISSUER` / `VITE_KINDE_CLIENT_ID` variables below
> are obsolete. Auth is now native cookie sessions. The required secrets are:
>
> - `ENCRYPTION_KEY` — 32-byte hex for AES-256-GCM (already in Step 2)
> - `INGESTION_SHARED_SECRET` — 32-byte hex for the api → ingestion HMAC
>   (`openssl rand -hex 32`)
> - Per-env OIDC SSO config (`OIDC_*`) is stored per-org in the database, not as CI
>   variables. See `services/api/CLAUDE.md` and `docs/sso-integration-design.md`.

---

## Step 4: Get CloudFront Distribution ID

### 4.1 List CloudFront Distributions

```bash
aws cloudfront list-distributions \
  --query 'DistributionList.Items[*].[Id,DomainName,Status]'
```

Output:
```
[
  ["E123ABC456DEF", "d123abc456def.cloudfront.net", "Deployed"]
]
```

**Copy the distribution ID** (e.g., `E123ABC456DEF`).

If you don't have a CloudFront distribution yet, you can skip this for now. The deploy job will warn but continue without invalidation.

---

## Step 5: Add Variables to GitLab

### 5.1 Navigate to GitLab Settings

In your GitLab project:
1. **Settings → CI/CD → Variables**
2. Expand **Variables**

### 5.2 Add Each Variable

Create the following variables (all must be **Masked** and **Protected**):

#### AWS_CI_ROLE_ARN
| Field | Value |
|-------|-------|
| Key | `AWS_CI_ROLE_ARN` |
| Value | ARN of the `gitlab-ci-axiaops-deploy` OIDC role (from `aws-infra` Terraform output) |
| Type | Variable |
| Protect | ✓ Yes |
| Mask | ✓ Yes |

#### ENCRYPTION_KEY
| Field | Value |
|-------|-------|
| Key | `ENCRYPTION_KEY` |
| Value | `3f9c8a7e...` (from Step 2.1) |
| Type | Variable |
| Protect | ✓ Yes |
| Mask | ✓ Yes |

#### INGESTION_SHARED_SECRET
| Field | Value |
|-------|-------|
| Key | `INGESTION_SHARED_SECRET` |
| Value | output of `openssl rand -hex 32` |
| Type | Variable |
| Protect | ✓ Yes |
| Mask | ✓ Yes |

#### CLOUDFRONT_DISTRIBUTION_ID
| Field | Value |
|-------|-------|
| Key | `CLOUDFRONT_DISTRIBUTION_ID` |
| Value | `E123ABC456DEF` (from Step 4.1) |
| Type | Variable |
| Protect | ✓ Yes |
| Mask | ✓ Yes |

---

## Verification Checklist

### In GitLab: Settings → CI/CD → Variables

- [ ] `AWS_CI_ROLE_ARN` — masked ✓, protected ✓
- [ ] `ENCRYPTION_KEY` — masked ✓, protected ✓
- [ ] `INGESTION_SHARED_SECRET` — masked ✓, protected ✓
- [ ] `CLOUDFRONT_DISTRIBUTION_ID` — masked ✓, protected ✓

### Test the Configuration

1. Push a commit to `main`:
   ```bash
   git commit --allow-empty -m "Test CI/CD pipeline"
   git push origin main
   ```

2. Go to **GitLab → CI/CD → Pipelines**

3. Wait for test stage to complete

4. Verify build stage succeeds:
   - `build:api` — ECR push successful
   - `build:ingestion` — ECR push successful
   - `build:dashboard` — ECR push successful

5. Verify deploy stage (tag-gated manual gate):
   - `deploy:production` — DB migration ran, ECS Express services updated, S3 + CloudFront invalidated

---

## Secret Rotation Schedule

| Secret | Rotation Interval | Procedure |
|--------|-------------------|-----------|
| `AWS_CI_ROLE_ARN` | Never (unless role is replaced) | Role trust is managed in `axiaops/aws-infra` Terraform; no key to rotate |
| `ENCRYPTION_KEY` | Generate-once; requires re-encryption of all `accounts.secret_encrypted` rows before swap | See `docs/ops.md` for procedure |
| `INGESTION_SHARED_SECRET` | As needed | Rotate both api and ingestion together using the dual-slot process in `docs/c1-hmac-plan.md` |
| `CLOUDFRONT_DISTRIBUTION_ID` | Never (unless distribution is replaced) | Only update if CloudFront distribution is recreated |

---

## Troubleshooting

### "Permission denied: User is not authorized to perform: ecr:PutImage"
- OIDC token exchange succeeded but the deploy role lacks the permission
- Verify the `gitlab-ci-axiaops-deploy` role policy in `axiaops/aws-infra` includes ECR push permissions
- Check AWS account ID is correct (`123456789012`)

### "ECS Express service not found"
- ECS Express service does not exist
- Services are provisioned by `axiaops/aws-infra` Terraform — run `terraform apply` first
- Verify service name in `.gitlab-ci.yml` matches what Terraform provisioned

### "Cannot get authorization token" / OIDC exchange fails
- `AWS_CI_ROLE_ARN` is not set or incorrect
- Verify the role trust policy allows the GitLab project's OIDC subject claim
- See `axiaops/aws-infra` for the trust policy definition

### Variables not available in job
- Check if variable is marked as **Protected** and job is on `main` branch
- Feature branches can't access protected variables
- Temporarily unprotect for testing (not recommended for secrets)

### CloudFront invalidation fails gracefully
- Non-fatal error; deployment succeeded but cache wasn't cleared
- Manually invalidate in AWS CloudFront console
- Or wait up to 24 hours for TTL to expire

---

## Security Best Practices

1. **No static AWS keys in CI** — OIDC tokens are short-lived and role-scoped; no rotation needed
2. **Audit IAM policy** — grant only minimum required permissions; policy is defined in `axiaops/aws-infra`
3. **Never use root or personal AWS credentials**
4. **Mask secrets in logs** — all sensitive variables should be masked
5. **Protect production variables** — only available on `main` branch
6. **Monitor access** — check AWS CloudTrail for unexpected API calls
7. **Document rotation** — keep a log of when secrets were rotated
8. **Secure backup** — store `ENCRYPTION_KEY` in a secure location (password manager, Vault, etc.)

---

## Related Documentation

- [docs/gitlab_ci_pipeline.md](gitlab_ci_pipeline.md) — Pipeline configuration and setup
- [docs/ci_cd_quick_reference.md](ci_cd_quick_reference.md) — Troubleshooting and operations
- [docs/deployment.md](deployment.md) — AWS infrastructure details
- [GitLab CI/CD Variables](https://docs.gitlab.com/ee/ci/variables/)
- [AWS IAM User Guide](https://docs.aws.amazon.com/iam/)
