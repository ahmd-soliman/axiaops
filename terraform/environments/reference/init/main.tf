################################################################################
# init/ — the irreducible local apply for this AWS account.
#
# Bootstraps the GitHub Actions OIDC provider + two IAM roles the CI pipeline
# uses:
#   - github_actions_axiaops_terraform       broad terraform-apply, gated on
#                                             main-branch pushes (production-
#                                             safe trust).
#   - github_actions_axiaops_terraform_plan  read-only, gated on main +
#                                             pull_request runs so reviewers
#                                             see plan diffs on PR review.
#
# Both roles wear a PermissionsBoundary that hard-caps what they can ever do
# even if the inline policy drifts. Defence in depth.
#
# Applied locally with SSO admin, once per AWS account. After that, every
# subsequent change to the stack flows through the pipeline using these
# roles. See README.md + Makefile.
################################################################################

# --- GitHub Actions OIDC provider ---------------------------------------------
#
# AWS no longer validates the thumbprint against certain well-known OIDC
# issuers (GitHub's included) — it just requires the field to be populated.
# Fetched live rather than hardcoded: GitHub has rotated the signing CA
# behind this endpoint before, and a stale hardcoded thumbprint is a needless
# footgun for a value the API doesn't actually check. Same pattern as the
# original GitLab provider this replaced, just pointed at GitHub's endpoint.
data "tls_certificate" "github" {
  url = "https://token.actions.githubusercontent.com"
}

resource "aws_iam_openid_connect_provider" "github" {
  url             = "https://token.actions.githubusercontent.com"
  client_id_list  = ["sts.amazonaws.com"]
  thumbprint_list = [data.tls_certificate.github.certificates[0].sha1_fingerprint]
}

# var.github_{apply,plan}_refs carries only the claim SUFFIX (e.g.
# "ref:refs/heads/main", "pull_request") — this local prepends the
# "repo:<owner>/<repo>:" prefix GitHub's sub claim always starts with, so the
# repository identity lives in exactly one place (var.github_repository).
locals {
  github_apply_subs = [for r in var.github_apply_refs : "repo:${var.github_repository}:${r}"]
  github_plan_subs  = [for r in var.github_plan_refs : "repo:${var.github_repository}:${r}"]
}

# --- Service-linked roles -----------------------------------------------------
#
# Account-singleton roles created deterministically by init/ rather than relying
# on AWS to auto-create them on first resource use. The auto-create path needs
# `iam:CreateServiceLinkedRole` on the CI principal (still granted below as
# defence in depth) and has an IAM-cache window during which a fresh boundary
# edit isn't yet honoured -- a fragile bootstrap. RDS + ElastiCache already
# auto-created cleanly in the account; App Runner's first-VPC-connector apply
# raced the boundary update and never got created, blocking every retry. Making
# the role an explicit Terraform resource closes that race.

resource "aws_iam_service_linked_role" "apprunner" {
  aws_service_name = "apprunner.amazonaws.com"
}

# ECS itself uses AWSServiceRoleForECS internally for housekeeping
# (CloudWatch metric publishing, ENI cleanup, target-group registration for
# the managed ALB Express Mode auto-provisions). Classic ECS RunTask never
# needed it on this account — AWS auto-created it cleanly the first time we
# ran the migrate task. Express Mode is stricter: create-express-gateway-
# service refuses with `Unable to assume the service linked role` if it has
# to wait on the auto-create path, instead of triggering the auto-create
# itself. Pin it here as an explicit resource (same fix shape as the App
# Runner SLR above) so the CI apply role never has to race the SLR-creation
# boundary check.
resource "aws_iam_service_linked_role" "ecs" {
  aws_service_name = "ecs.amazonaws.com"
}

# --- Permissions Boundary -----------------------------------------------------
#
# Hard ceiling on what the CI roles can ever do, even if a future inline-policy
# bug grants more. Resource scopes mirror the design's resource-naming convention
# (axiaops-* prefix). KMS is admitted only for SSM-mediated decrypts.
# IAM action set is intentionally limited to creating/managing axiaops-* roles
# (i.e. App Runner / ECS instance roles created by main/) — not arbitrary IAM
# changes (would let CI rewrite its own trust policy).

data "aws_iam_policy_document" "ci_boundary" {
  statement {
    sid    = "CoreAWSServices"
    effect = "Allow"
    actions = [
      "ec2:*",
      "rds:*",
      "ecr:*",
      "ecs:*",
      "apprunner:*",
      "elasticache:*",
      "cloudfront:*",
      "acm:*",
      "route53:*",
      "logs:*",
      "ssm:*",
      "s3:*",
      "dynamodb:*",
      "sts:GetCallerIdentity",
    ]
    resources = ["*"]
  }

  statement {
    sid    = "ServiceLinkedRoleCreation"
    effect = "Allow"
    # App Runner needs TWO SLRs: apprunner.amazonaws.com (general operations)
    # and networking.apprunner.amazonaws.com (VPC connectors specifically).
    # Auto-create fails for the networking one without this entry, which
    # surfaces as the same misleading "Couldn't create a service-linked role"
    # error as the regular App Runner SLR. ElastiCache and RDS auto-create
    # their SLRs on first use of their respective services. EC2 Instance
    # Connect Endpoint (modules/ops-rds-access) similarly requires
    # AWSServiceRoleForEC2InstanceConnect on first endpoint creation in the
    # account. Condition locks the action to known services so the boundary
    # doesn't widen into a generic IAM-role-creation grant.
    actions   = ["iam:CreateServiceLinkedRole"]
    resources = ["arn:aws:iam::*:role/aws-service-role/*"]
    condition {
      test     = "StringEquals"
      variable = "iam:AWSServiceName"
      values = [
        "apprunner.amazonaws.com",
        "networking.apprunner.amazonaws.com",
        "elasticache.amazonaws.com",
        "rds.amazonaws.com",
        "ecs.amazonaws.com",
        "ec2-instance-connect.amazonaws.com",
      ]
    }
  }

  statement {
    sid    = "ScopedIAM"
    effect = "Allow"
    # CI can touch IAM resources matching the production stack's naming
    # convention. The `axiaops-*` pattern covers every role/policy the
    # composition in main/ creates (App Runner instance roles, ECS migrate
    # roles, etc.), plus `github-actions-axiaops-deploy` which predates the
    # env-prefixed convention and is listed explicitly.
    #
    # Notably this scope does NOT match the init-managed roles
    # (github-actions-axiaops-terraform*), so CI cannot rewrite its own trust
    # policy or boundary. The explicit DenyMutationsOnInitResources
    # statement below is belt-and-braces on top of that name-scoping.
    actions = [
      "iam:CreateRole",
      "iam:DeleteRole",
      "iam:GetRole",
      "iam:UpdateRole",
      "iam:UpdateRoleDescription",
      "iam:UpdateAssumeRolePolicy",
      "iam:TagRole",
      "iam:UntagRole",
      "iam:ListRoleTags",
      # ListInstanceProfilesForRole is what aws_iam_role's DELETE path calls
      # FIRST to find any attached instance profiles to detach -- without it,
      # every DeleteRole on an axiaops-* role fails with AccessDenied even if
      # no instance profile is actually attached. RemoveRoleFromInstanceProfile
      # + DeleteInstanceProfile are the follow-up calls TF makes when the list
      # is non-empty (defence in depth -- the App Runner roles being removed
      # in the ECS Express refactor had no instance profiles, but a future
      # role that does won't trip the same trap on next DeleteRole).
      "iam:ListInstanceProfilesForRole",
      "iam:RemoveRoleFromInstanceProfile",
      "iam:DeleteInstanceProfile",
      "iam:CreatePolicy",
      "iam:DeletePolicy",
      "iam:GetPolicy",
      "iam:GetPolicyVersion",
      "iam:CreatePolicyVersion",
      "iam:DeletePolicyVersion",
      "iam:ListPolicyVersions",
      "iam:PutRolePolicy",
      "iam:DeleteRolePolicy",
      "iam:GetRolePolicy",
      "iam:ListRolePolicies",
      "iam:AttachRolePolicy",
      "iam:DetachRolePolicy",
      "iam:ListAttachedRolePolicies",
      "iam:PassRole",
    ]
    resources = [
      "arn:aws:iam::${var.account_id}:role/axiaops-*",
      "arn:aws:iam::${var.account_id}:policy/axiaops-*",
      # AxiaOpsScanner is the customer-facing cross-account scanner principal
      # (modules/iam — the renamed ingestion task role). It intentionally breaks
      # the lowercase `axiaops-*` substrate convention because its name is a
      # stable contract baked into customer trust policies and the onboarding
      # CloudFormation template, so CI must be allowed to manage it explicitly.
      # `AxiaOps*` (capitalised) mirrors the existing wildcard style and leaves
      # room for future customer-facing roles without another init apply.
      "arn:aws:iam::${var.account_id}:role/AxiaOps*",
      # The application repo's app-deploy role + its inline policy.
      # modules/iam creates these under main/. Different naming
      # convention from `axiaops-*` because the role name is part of
      # the deploy workflow's contract (AWS_CI_ROLE_ARN).
      "arn:aws:iam::${var.account_id}:role/github-actions-axiaops-deploy",
      "arn:aws:iam::${var.account_id}:policy/github-actions-axiaops-deploy",
    ]
  }

  statement {
    sid    = "DenyMutationsOnInitResources"
    effect = "Deny"
    # Explicit deny so CI can never touch the init-managed roles, even if a
    # downstream inline policy grants iam:* on them by mistake.
    actions = [
      "iam:CreateRole",
      "iam:DeleteRole",
      "iam:UpdateRole",
      "iam:UpdateAssumeRolePolicy",
      "iam:PutRolePolicy",
      "iam:DeleteRolePolicy",
      "iam:AttachRolePolicy",
      "iam:DetachRolePolicy",
      "iam:DeleteOpenIDConnectProvider",
      "iam:UpdateOpenIDConnectProviderThumbprint",
    ]
    resources = [
      "arn:aws:iam::${var.account_id}:role/github-actions-axiaops-terraform",
      "arn:aws:iam::${var.account_id}:role/github-actions-axiaops-terraform-plan",
      aws_iam_openid_connect_provider.github.arn,
    ]
  }
}

resource "aws_iam_policy" "ci_boundary" {
  name        = "github-actions-axiaops-terraform-boundary"
  description = "Permissions Boundary for the CI terraform-apply role. Hard ceiling on blast radius even if the role's inline policy drifts."
  policy      = data.aws_iam_policy_document.ci_boundary.json
}

# --- Trust policies -----------------------------------------------------------

data "aws_iam_policy_document" "ci_trust_apply" {
  statement {
    effect  = "Allow"
    actions = ["sts:AssumeRoleWithWebIdentity"]

    principals {
      type        = "Federated"
      identifiers = [aws_iam_openid_connect_provider.github.arn]
    }

    condition {
      test     = "StringEquals"
      variable = "token.actions.githubusercontent.com:aud"
      values   = ["sts.amazonaws.com"]
    }

    condition {
      test     = "StringLike"
      variable = "token.actions.githubusercontent.com:sub"
      values   = local.github_apply_subs
    }
  }
}

data "aws_iam_policy_document" "ci_trust_plan" {
  statement {
    effect  = "Allow"
    actions = ["sts:AssumeRoleWithWebIdentity"]

    principals {
      type        = "Federated"
      identifiers = [aws_iam_openid_connect_provider.github.arn]
    }

    condition {
      test     = "StringEquals"
      variable = "token.actions.githubusercontent.com:aud"
      values   = ["sts.amazonaws.com"]
    }

    condition {
      test     = "StringLike"
      variable = "token.actions.githubusercontent.com:sub"
      values   = local.github_plan_subs
    }
  }
}

# --- Role: broad terraform-apply (used on main + tags) ------------------------

resource "aws_iam_role" "ci_terraform_apply" {
  name                 = "github-actions-axiaops-terraform"
  description          = "CI role for `terraform apply` against the production main stack. Broad permissions inside a hard PermissionsBoundary; gated on main + tag pipelines."
  assume_role_policy   = data.aws_iam_policy_document.ci_trust_apply.json
  permissions_boundary = aws_iam_policy.ci_boundary.arn
}

data "aws_iam_policy_document" "ci_terraform_apply" {
  # The boundary above is the actual ceiling; this inline policy can grant
  # anything the boundary admits. Keep it broad (Allow * within boundary) —
  # changes to specific resource types are governed by the boundary edits.
  statement {
    sid    = "BoundedFullAccess"
    effect = "Allow"
    actions = [
      "ec2:*", "rds:*", "ecr:*", "ecs:*", "apprunner:*",
      "elasticache:*",
      "cloudfront:*", "acm:*", "route53:*",
      "logs:*", "ssm:*", "s3:*", "dynamodb:*",
      "iam:*",
      "sts:GetCallerIdentity",
    ]
    resources = ["*"]
  }
}

resource "aws_iam_role_policy" "ci_terraform_apply" {
  name   = "github-actions-axiaops-terraform"
  role   = aws_iam_role.ci_terraform_apply.id
  policy = data.aws_iam_policy_document.ci_terraform_apply.json
}

# --- Plan-role Permissions Boundary ------------------------------------------
#
# Defence in depth. The plan role's inline policy below is read-only by
# design — but inline policies drift over time as people add convenient
# extras ("just one more ssm:PutParameter to read computed outputs"...).
# This boundary hard-caps what the plan role can ever reach, even if a
# future inline-policy edit broadens its actions:
#
#   - S3 reads scoped to the state bucket only (not arbitrary S3).
#   - SSM reads scoped to /axiaops/* parameters only (not the whole account).
#   - All Describe-style reads keep `*` because AWS doesn't support
#     resource-scoping for most Describe actions (provider-side limitation).
#   - DynamoDB writes scoped to the state-lock table only.
#
# A leaked plan-role JWT now exposes at most the AxiaOps state file and
# /axiaops/* SSM values — not the whole account's SSM tree.

data "aws_iam_policy_document" "ci_plan_boundary" {
  statement {
    sid    = "StateBucketReads"
    effect = "Allow"
    actions = [
      "s3:GetObject",
      "s3:GetBucket*",
      "s3:GetEncryptionConfiguration",
      "s3:List*",
    ]
    resources = [
      "arn:aws:s3:::${var.state_bucket_name}",
      "arn:aws:s3:::${var.state_bucket_name}/*",
    ]
  }

  statement {
    sid    = "ManagedBucketReads"
    effect = "Allow"
    # The plan must refresh every bucket the production stack creates
    # (axiaops-dashboard-prod and any future axiaops-* bucket). Scoped to
    # the axiaops-* name prefix so the boundary still excludes arbitrary
    # buckets in the account.
    #
    # Note: `s3:GetBucket*` does NOT cover Accelerate/Lifecycle/Replication —
    # the IAM action names lack the `Bucket` prefix. The AWS provider's
    # aws_s3_bucket Read calls all three on every refresh, so they must be
    # listed explicitly.
    #
    # GetObject + GetObjectTagging are object-level reads the provider issues
    # when refreshing an aws_s3_object (the onboarding CFN template under
    # modules/edge — first managed object in an axiaops-* bucket, added in !34):
    # HeadObject is authorised by s3:GetObject, and the Read pass also lists the
    # object's tags via s3:GetObjectTagging. Both must be in the boundary or the
    # intersection rejects them even when the identity policy grants them.
    actions = [
      "s3:GetBucket*",
      "s3:GetEncryptionConfiguration",
      "s3:GetAccelerateConfiguration",
      "s3:GetLifecycleConfiguration",
      "s3:GetReplicationConfiguration",
      "s3:GetObject",
      "s3:GetObjectTagging",
      "s3:List*",
    ]
    resources = [
      "arn:aws:s3:::axiaops-*",
      "arn:aws:s3:::axiaops-*/*",
    ]
  }

  statement {
    sid    = "OwnSSMReads"
    effect = "Allow"
    actions = [
      "ssm:GetParameter*",
      "ssm:Describe*",
      "ssm:List*",
    ]
    resources = [
      "arn:aws:ssm:${var.region}:${var.account_id}:parameter/axiaops/*",
    ]
  }

  statement {
    sid    = "AccountWideSSMDescribe"
    effect = "Allow"
    # ssm:DescribeParameters is an account-level enumeration call -- AWS
    # rejects resource-scoped ARNs on this specific action. The AWS Terraform
    # provider invokes it during state refresh for every aws_ssm_parameter
    # resource, so the boundary must grant it at account scope. Listing
    # parameter metadata (name/type/tier) is not equivalent to reading
    # SecureString values, which are still gated by ssm:GetParameter on the
    # /axiaops/* scope above.
    actions   = ["ssm:DescribeParameters"]
    resources = ["*"]
  }

  statement {
    sid    = "ResourceDescribeForPlan"
    effect = "Allow"
    # AWS-side constraint: most Describe* / List* / Get* actions on these
    # services do not accept resource-scoped ARNs. Resource = "*" is the
    # only legal shape -- the boundary still gates everything else.
    actions = [
      "ec2:Describe*",
      "rds:Describe*", "rds:List*",
      "ecr:Describe*", "ecr:List*", "ecr:Get*",
      "ecs:Describe*", "ecs:List*",
      "apprunner:Describe*", "apprunner:List*",
      "elasticache:Describe*", "elasticache:List*",
      "cloudfront:Describe*", "cloudfront:Get*", "cloudfront:List*",
      "acm:Describe*", "acm:List*", "acm:Get*",
      "route53:Get*", "route53:List*",
      "logs:Describe*", "logs:List*",
      "iam:Get*", "iam:List*",
      "sts:GetCallerIdentity",
    ]
    resources = ["*"]
  }

  statement {
    sid    = "StateLockAccess"
    effect = "Allow"
    # Plan must acquire the DynamoDB state-lock. PutItem + DeleteItem are
    # lock operations, not data mutations — they don't touch any AWS
    # resource that this stack manages.
    actions = [
      "dynamodb:Describe*",
      "dynamodb:GetItem",
      "dynamodb:Query",
      "dynamodb:PutItem",
      "dynamodb:DeleteItem",
    ]
    resources = [
      "arn:aws:dynamodb:${var.region}:${var.account_id}:table/${var.state_lock_table_name}",
    ]
  }
}

resource "aws_iam_policy" "ci_plan_boundary" {
  name        = "github-actions-axiaops-terraform-plan-boundary"
  description = "Permissions Boundary for the CI plan role. Narrower than the apply boundary: S3 reads scoped to axiaops-* buckets, SSM Get/List scoped to /axiaops/* (DescribeParameters is account-wide by AWS API), DynamoDB writes scoped to the state-lock table."
  policy      = data.aws_iam_policy_document.ci_plan_boundary.json
}

# --- Role: read-only plan (used on MR pipelines) -----------------------------
#
# Narrower than the apply role. Can read AWS state to compute a plan diff but
# cannot mutate any real AWS resource (only the DynamoDB state-lock). The
# PermissionsBoundary above caps even read access to the AxiaOps namespace.

resource "aws_iam_role" "ci_terraform_plan" {
  name                 = "github-actions-axiaops-terraform-plan"
  description          = "CI role for `terraform plan` on MR pipelines. Read-only by inline policy; PermissionsBoundary hard-caps reads to the AxiaOps namespace."
  assume_role_policy   = data.aws_iam_policy_document.ci_trust_plan.json
  permissions_boundary = aws_iam_policy.ci_plan_boundary.arn
}

data "aws_iam_policy_document" "ci_terraform_plan" {
  statement {
    sid    = "ReadAWSResources"
    effect = "Allow"
    actions = [
      "ec2:Describe*",
      "rds:Describe*", "rds:List*",
      "ecr:Describe*", "ecr:List*", "ecr:Get*",
      "ecs:Describe*", "ecs:List*",
      "apprunner:Describe*", "apprunner:List*",
      "elasticache:Describe*", "elasticache:List*",
      "cloudfront:Describe*", "cloudfront:Get*", "cloudfront:List*",
      "acm:Describe*", "acm:List*", "acm:Get*",
      "route53:Get*", "route53:List*",
      "logs:Describe*", "logs:List*",
      "ssm:Describe*", "ssm:List*", "ssm:GetParameter*",
      "iam:Get*", "iam:List*",
      # `s3:GetBucket*` does NOT cover Accelerate/Lifecycle/Replication — the
      # IAM action names lack the `Bucket` prefix. Listed explicitly so the
      # aws_s3_bucket Read pass succeeds during plan refresh. GetObjectTagging
      # is the object-level read the provider issues when refreshing the
      # onboarding aws_s3_object (modules/edge, !34) alongside GetObject.
      "s3:List*", "s3:GetBucket*", "s3:GetObject", "s3:GetObjectTagging", "s3:GetEncryptionConfiguration",
      "s3:GetAccelerateConfiguration", "s3:GetLifecycleConfiguration", "s3:GetReplicationConfiguration",
      "dynamodb:Describe*", "dynamodb:GetItem", "dynamodb:Query",
      "sts:GetCallerIdentity",
    ]
    resources = ["*"]
  }

  statement {
    sid    = "DynamoDBLockReadWrite"
    effect = "Allow"
    # The plan must acquire the state-lock to read state safely. Lock writes
    # are NOT a mutation of real AWS resources.
    actions = [
      "dynamodb:PutItem",
      "dynamodb:DeleteItem",
    ]
    resources = [
      "arn:aws:dynamodb:${var.region}:${var.account_id}:table/${var.state_lock_table_name}",
    ]
  }
}

resource "aws_iam_role_policy" "ci_terraform_plan" {
  name   = "github-actions-axiaops-terraform-plan"
  role   = aws_iam_role.ci_terraform_plan.id
  policy = data.aws_iam_policy_document.ci_terraform_plan.json
}
