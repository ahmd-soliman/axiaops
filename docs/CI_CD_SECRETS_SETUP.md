# CI/CD Secrets Setup — GitLab Variables Configuration

Complete checklist for configuring GitLab CI/CD variables and AWS secrets for AxiaOps.

---

## Overview

GitLab CI/CD needs access to:
1. **AWS credentials** — to push images to ECR and update App Runner
2. **Encryption key** — to encrypt/decrypt AWS secrets in production
3. **Kinde OAuth** — to configure the dashboard authentication
4. **CloudFront distribution ID** — to invalidate cache after dashboard deployment

All secrets should be:
- ✓ **Masked** — hidden in job logs
- ✓ **Protected** — only available on protected branches (e.g., `main`)

---

## Step 1: Create AWS IAM User

Create a dedicated IAM user for GitLab CI/CD with minimal permissions.

### 1.1 Create User in AWS IAM

```bash
aws iam create-user --user-name gitlab-ci-axiaops
```

### 1.2 Create Access Keys

```bash
aws iam create-access-key --user-name gitlab-ci-axiaops
```

**Output:**
```json
{
  "AccessKey": {
    "AccessKeyId": "AKIA...",
    "SecretAccessKey": "wJalr...",
    "UserName": "gitlab-ci-axiaops",
    "CreateDate": "2026-04-11T...",
    "Status": "Active"
  }
}
```

**Save these securely** — you'll need them for GitLab.

### 1.3 Attach Policy to User

Create a policy with minimal permissions:

```bash
cat > /tmp/gitlab-ci-policy.json << 'EOF'
{
  "Version": "2012-10-17",
  "Statement": [
    {
      "Sid": "ECRAccess",
      "Effect": "Allow",
      "Action": [
        "ecr:GetAuthorizationToken",
        "ecr:BatchGetImage",
        "ecr:GetDownloadUrlForLayer",
        "ecr:PutImage",
        "ecr:InitiateLayerUpload",
        "ecr:UploadLayerPart",
        "ecr:CompleteLayerUpload",
        "ecr:DescribeRepositories",
        "ecr:ListImages"
      ],
      "Resource": "arn:aws:ecr:eu-central-1:123456789012:repository/axiaops-*"
    },
    {
      "Sid": "AppRunnerAccess",
      "Effect": "Allow",
      "Action": [
        "apprunner:UpdateService",
        "apprunner:DescribeService"
      ],
      "Resource": "arn:aws:apprunner:eu-central-1:123456789012:service/axiaops-*"
    },
    {
      "Sid": "CloudFrontAccess",
      "Effect": "Allow",
      "Action": [
        "cloudfront:CreateInvalidation",
        "cloudfront:GetInvalidation"
      ],
      "Resource": "arn:aws:cloudfront::123456789012:distribution/*"
    }
  ]
}
EOF

aws iam put-user-policy \
  --user-name gitlab-ci-axiaops \
  --policy-name GitLabCIPolicy \
  --policy-document file:///tmp/gitlab-ci-policy.json
```

Verify:
```bash
aws iam get-user-policy \
  --user-name gitlab-ci-axiaops \
  --policy-name GitLabCIPolicy
```

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

## Step 3: Get Kinde OAuth Credentials

### 3.1 Log in to Kinde Dashboard

https://kinde.com/dashboard

### 3.2 Find Issuer URL

1. Go to **Settings → Applications**
2. Find your AxiaOps application
3. Copy the **Issuer URL** (e.g., `https://YOUR_KINDE_DOMAIN.kinde.com`)

### 3.3 Find Client ID

1. Same location as issuer
2. Copy the **Client ID** (e.g., `your_client_id_here`)

These are **not secrets** (safe to embed in frontend), but should still be masked in CI logs.

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

#### AWS_ACCESS_KEY_ID
| Field | Value |
|-------|-------|
| Key | `AWS_ACCESS_KEY_ID` |
| Value | `AKIA...` (from Step 1.2) |
| Type | Variable |
| Protect | ✓ Yes |
| Mask | ✓ Yes |

#### AWS_SECRET_ACCESS_KEY
| Field | Value |
|-------|-------|
| Key | `AWS_SECRET_ACCESS_KEY` |
| Value | `wJalr...` (from Step 1.2) |
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

#### EXPO_PUBLIC_KINDE_ISSUER
| Field | Value |
|-------|-------|
| Key | `EXPO_PUBLIC_KINDE_ISSUER` |
| Value | `https://YOUR_KINDE_DOMAIN.kinde.com` |
| Type | Variable |
| Protect | ✓ Yes |
| Mask | ✓ No (not a secret, but safe to mask) |

#### EXPO_PUBLIC_KINDE_CLIENT_ID
| Field | Value |
|-------|-------|
| Key | `EXPO_PUBLIC_KINDE_CLIENT_ID` |
| Value | `your_client_id_here` |
| Type | Variable |
| Protect | ✓ Yes |
| Mask | ✓ No (not a secret, but safe to mask) |

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

- [ ] `AWS_ACCESS_KEY_ID` — masked ✓, protected ✓
- [ ] `AWS_SECRET_ACCESS_KEY` — masked ✓, protected ✓
- [ ] `ENCRYPTION_KEY` — masked ✓, protected ✓
- [ ] `EXPO_PUBLIC_KINDE_ISSUER` — masked ✓, protected ✓
- [ ] `EXPO_PUBLIC_KINDE_CLIENT_ID` — masked ✓, protected ✓
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

5. Verify deploy stage succeeds:
   - `deploy:api` — App Runner updated
   - `deploy:ingestion` — App Runner updated
   - `deploy:dashboard` — App Runner updated + CloudFront invalidated

---

## Secret Rotation Schedule

| Secret | Rotation Interval | Procedure |
|--------|-------------------|-----------|
| `AWS_ACCESS_KEY_ID` | 90 days | Create new key pair in AWS IAM; update GitLab variables; delete old keys |
| `AWS_SECRET_ACCESS_KEY` | 90 days | Same as above |
| `ENCRYPTION_KEY` | 12 months | Run `db/migrate-encryption-key.sql` (requires database downtime); then update GitLab variable |
| `EXPO_PUBLIC_KINDE_*` | As per Kinde policy | Update when Kinde credentials change |
| `CLOUDFRONT_DISTRIBUTION_ID` | Never (unless distribution is replaced) | Only update if CloudFront distribution is recreated |

---

## Troubleshooting

### "Permission denied: User is not authorized to perform: ecr:PutImage"
- IAM policy missing or incorrect
- Verify policy is attached to `gitlab-ci-axiaops` user
- Check AWS account ID is correct (`123456789012`)
- Regenerate access keys

### "serviceName: 'axiaops-api' not found"
- App Runner service doesn't exist
- Create it via AWS Console or CLI (see docs/gitlab_ci_pipeline.md)
- Verify service name in deploy job matches

### "Cannot get authorization token"
- AWS credentials invalid or missing in GitLab variables
- Verify `AWS_ACCESS_KEY_ID` and `AWS_SECRET_ACCESS_KEY` are set
- Test locally: `aws ecr get-login-password --region eu-central-1`

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

1. **Rotate credentials regularly** — AWS keys every 90 days
2. **Use separate IAM user** — never use root or personal AWS credentials
3. **Audit IAM policy** — grant only minimum required permissions
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
