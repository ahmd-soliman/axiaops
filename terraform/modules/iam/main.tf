locals {
  ecr_arns = [
    for name in var.ecr_repository_names :
    "arn:aws:ecr:${var.region}:${var.account_id}:repository/${name}"
  ]

  # ECS Express Mode runtime services (api + ingestion) live in the
  # `axiaops-runtime` cluster. Service ARNs are service/<cluster>/<name>, so
  # the deploy role's Express verbs scope to this cluster's services.
  runtime_cluster_name           = "axiaops-runtime"
  runtime_service_arn_pattern    = "arn:aws:ecs:${var.region}:${var.account_id}:service/${local.runtime_cluster_name}/*"
  migrate_task_role_arn          = "arn:aws:iam::${var.account_id}:role/axiaops-${var.env_name}-migrate-task"
  migrate_execution_role_arn     = "arn:aws:iam::${var.account_id}:role/axiaops-${var.env_name}-migrate-execution"
  managed_ecs_task_execution_arn = "arn:aws:iam::aws:policy/service-role/AmazonECSTaskExecutionRolePolicy"
  # Verified live (design §code-inventory): exists, attachable, last updated
  # 2026-02-12 v6; 17 statements covering ELB/SG/ACM/AppAutoScaling/CloudWatch
  # alarms, all tag-scoped to AmazonECSManaged=true.
  managed_express_infra_arn = "arn:aws:iam::aws:policy/service-role/AmazonECSInfrastructureRoleforExpressGatewayServices"

  cloudfront_resource = (
    var.cloudfront_distribution_id != ""
    ? "arn:aws:cloudfront::${var.account_id}:distribution/${var.cloudfront_distribution_id}"
    : "arn:aws:cloudfront::${var.account_id}:distribution/*"
  )
}

# --- GitHub Actions CI app-deploy role ---------------------------------------
#
# Note: the GitHub OIDC provider + the terraform-apply/plan roles for this
# repo's infra-apply CI live in environments/reference/init/, not here.
# That's the bootstrap layer applied once locally with SSO admin. This module
# provisions the runtime IAM that the production stack needs — including the
# narrow `github-actions-axiaops-deploy` role the APPLICATION repo's CI
# workflow uses for ECR push, ECS Express Mode service UPDATE (TF owns
# Create/Delete), migrate run-task, dashboard sync, and CloudFront invalidate.

data "aws_iam_policy_document" "github_ci_trust" {
  statement {
    effect  = "Allow"
    actions = ["sts:AssumeRoleWithWebIdentity"]

    principals {
      type        = "Federated"
      identifiers = [var.github_oidc_provider_arn]
    }

    condition {
      test     = "StringEquals"
      variable = "token.actions.githubusercontent.com:aud"
      values   = ["sts.amazonaws.com"]
    }

    condition {
      test     = "StringLike"
      variable = "token.actions.githubusercontent.com:sub"
      values   = var.github_allowed_refs
    }
  }
}

resource "aws_iam_role" "github_ci_deploy" {
  name               = "github-actions-axiaops-deploy"
  description        = "GitHub Actions CI app-deploy role for application releases. Narrow scope (ECR push + ECS Express service update + migrate run-task + dashboard sync + CloudFront invalidate). Service create/delete is TF-owned and intentionally not granted here. NOT the terraform-apply role -- that lives in init/. (IAM description must stay ASCII per the IAM API regex.)"
  assume_role_policy = data.aws_iam_policy_document.github_ci_trust.json
}

data "aws_iam_policy_document" "github_ci_deploy" {
  # ecr:GetAuthorizationToken cannot be scoped per the AWS API spec.
  statement {
    sid       = "ECRAuth"
    effect    = "Allow"
    actions   = ["ecr:GetAuthorizationToken"]
    resources = ["*"]
  }

  statement {
    sid    = "ECRPush"
    effect = "Allow"
    actions = [
      "ecr:BatchCheckLayerAvailability",
      "ecr:GetDownloadUrlForLayer",
      "ecr:BatchGetImage",
      "ecr:PutImage",
      "ecr:InitiateLayerUpload",
      "ecr:UploadLayerPart",
      "ecr:CompleteLayerUpload",
      "ecr:DescribeRepositories",
      "ecr:ListImages",
    ]
    resources = local.ecr_arns
  }

  # ECS Express Mode service lifecycle. The deploy workflow's job rolls
  # a new image per release via `update-express-gateway-service` and polls
  # service state via `describe-express-gateway-service` (which the
  # `monitor-express-gateway-service` waiter calls under the hood). Service
  # CREATE and DELETE are TF-owned (see modules/compute/main.tf —
  # aws_ecs_express_gateway_service.{api,ingestion}) and intentionally NOT
  # granted here; the CI role can swap the running image but cannot replace
  # or remove the service shape itself.
  #
  # NOTE: Express Mode is new (GA Nov 2025); resource-level support on these
  # verbs is not yet doc-confirmed (design open question). If a scratch-account
  # update rejects resource scoping with AccessDenied, widen to "*".
  statement {
    sid    = "ECSExpressServiceLifecycle"
    effect = "Allow"
    actions = [
      "ecs:UpdateExpressGatewayService",
      "ecs:DescribeExpressGatewayService",
    ]
    resources = [local.runtime_service_arn_pattern]
  }

  statement {
    sid       = "ECSExpressServiceList"
    effect    = "Allow"
    actions   = ["ecs:ListExpressGatewayServices"]
    resources = ["*"]
  }

  statement {
    sid    = "ECSRunMigrate"
    effect = "Allow"
    # ecs:RunTask is authorised against BOTH the task-definition AND the
    # cluster. Scoping only to the task-definition is insufficient — the
    # call returns AccessDeniedException at the cluster-resource check
    # before AWS even reads the task-definition.
    actions = ["ecs:RunTask"]
    resources = [
      "arn:aws:ecs:${var.region}:${var.account_id}:task-definition/axiaops-migrate:*",
      "arn:aws:ecs:${var.region}:${var.account_id}:cluster/axiaops-migrate",
    ]
  }

  statement {
    sid    = "ECSDescribeMigrateTasks"
    effect = "Allow"
    # ecs:DescribeTasks is authorised against the TASK ARN (created at
    # run time), not the task-definition. Polling for completion needs
    # this scoped to task/<cluster>/* so we can `wait` on the run-task
    # exit code from CI without escalating to a wildcard.
    actions   = ["ecs:DescribeTasks"]
    resources = ["arn:aws:ecs:${var.region}:${var.account_id}:task/axiaops-migrate/*"]
  }

  statement {
    sid    = "ECSRegisterMigrateTaskDef"
    effect = "Allow"
    # The migrate task definition pins a baseline :latest image, but the ECR
    # repo is IMMUTABLE and run-task cannot override the container image. So to
    # run a specific release's migrations the deploy job DescribeTaskDefinition
    # on the TF-managed base, swaps the image to the release-tagged one
    # (axiaops-migrate:${CI_COMMIT_TAG:-$CI_COMMIT_SHORT_SHA}), and registers a
    # new revision to run. Without these the migrate flow only works for the
    # first deploy (the one allowed :latest push) and then has no legal way to
    # run a newer build.
    #
    # Neither RegisterTaskDefinition nor DescribeTaskDefinition supports
    # resource-level scoping, so these are necessarily "*". Low-risk: describing
    # and registering a definition runs nothing — RunTask is already scoped to
    # axiaops-migrate, and PassMigrateRoles (below) bounds which roles a
    # registered revision may reference.
    actions = [
      "ecs:RegisterTaskDefinition",
      "ecs:DescribeTaskDefinition",
    ]
    resources = ["*"]
  }

  # iam:PassRole for the migrate roles — needed both when registering a
  # migrate task-def revision (it references these roles) and at run-task.
  # Scoped by PassedToService.
  statement {
    sid       = "PassMigrateRoles"
    effect    = "Allow"
    actions   = ["iam:PassRole"]
    resources = [local.migrate_task_role_arn, local.migrate_execution_role_arn]

    condition {
      test     = "StringEquals"
      variable = "iam:PassedToService"
      values   = ["ecs-tasks.amazonaws.com"]
    }
  }

  # iam:PassRole for the Express runtime task roles (execution + api + ingestion).
  # `update-express-gateway-service` re-binds these on every container swap,
  # so the CI role keeps PassRole here even though it no longer creates the
  # service itself. Scoped by PassedToService=ecs-tasks.amazonaws.com.
  statement {
    sid     = "PassExpressTaskRoles"
    effect  = "Allow"
    actions = ["iam:PassRole"]
    resources = [
      aws_iam_role.ecs_task_execution.arn,
      aws_iam_role.ecs_api_task.arn,
      aws_iam_role.ecs_ingestion_task.arn,
    ]

    condition {
      test     = "StringEquals"
      variable = "iam:PassedToService"
      values   = ["ecs-tasks.amazonaws.com"]
    }
  }

  # The deploy job reads the platform inventory (cluster ARN, role ARNs, ECR
  # URLs, subnet/SG IDs, secret ARNs) at runtime to assemble its ECS calls.
  statement {
    sid       = "ReadPlatformInventory"
    effect    = "Allow"
    actions   = ["ssm:GetParameters", "ssm:GetParameter", "ssm:GetParametersByPath"]
    resources = ["arn:aws:ssm:${var.region}:${var.account_id}:parameter/axiaops/${var.env_name}/platform/*"]
  }

  # The deploy job provisions runtime secrets that are intentionally kept
  # OUT of TF state (placeholder + ignore_changes in modules/secrets-urls):
  #   - ENCRYPTION_KEY — generated once if still the placeholder sentinel
  #   - api/SMTP_PASS  — reconciled per deploy from a masked SMTP_PASS CI
  #                      secret (the InviteMailer's global SMTP-relay App
  #                      Password)
  # It reads them to detect the placeholder, then writes the real SecureString.
  # Scoped to EXACTLY these params — deliberately NOT /{api,ingestion}/*
  # (the deploy role has no business reading DATABASE_URL / REDIS_URL / the HMAC
  # secret; those flow only to the task roles via secrets[] at task start).
  # SMTP_PASS is api-only (ingestion scan digests use per-org channels).
  statement {
    sid     = "ProvisionRuntimeSecrets"
    effect  = "Allow"
    actions = ["ssm:GetParameter", "ssm:PutParameter"]
    resources = [
      "arn:aws:ssm:${var.region}:${var.account_id}:parameter/axiaops/${var.env_name}/api/ENCRYPTION_KEY",
      "arn:aws:ssm:${var.region}:${var.account_id}:parameter/axiaops/${var.env_name}/ingestion/ENCRYPTION_KEY",
      "arn:aws:ssm:${var.region}:${var.account_id}:parameter/axiaops/${var.env_name}/api/SMTP_PASS",
    ]
  }

  # These params are SecureString under the AWS-managed `alias/aws/ssm` key
  # (no key_id in modules/secrets-urls → the default key). For the managed key
  # AWS does not strictly require explicit kms perms, but every other role here
  # (ecs task + execution DecryptSecrets) carries a ViaService-scoped grant and
  # successfully reads these params — so we keep the convention for consistency
  # and to stay correct if the params ever move to a CMK. kms:Encrypt is the
  # action SSM invokes for a Standard-tier SecureString PUT (values < 4 KB);
  # kms:Decrypt for GetParameter --with-decryption. ViaService pins both to
  # SSM-originated calls (the role can't use the key directly). key/* matches
  # the existing roles — the aws/ssm key ARN isn't TF-referenceable.
  statement {
    sid       = "ProvisionRuntimeSecretsKMS"
    effect    = "Allow"
    actions   = ["kms:Encrypt", "kms:Decrypt"]
    resources = ["arn:aws:kms:${var.region}:${var.account_id}:key/*"]
    condition {
      test     = "StringEquals"
      variable = "kms:ViaService"
      values   = ["ssm.${var.region}.amazonaws.com"]
    }
  }

  statement {
    sid    = "DashboardSync"
    effect = "Allow"
    # Per §10.1: write-only sync. `s3:GetObject` is deliberately omitted —
    # CI doesn't need to read the bundle back, and reading would expose any
    # runtime-env.js shipped alongside the SPA.
    actions = [
      "s3:PutObject",
      "s3:DeleteObject",
      "s3:ListBucket",
    ]
    resources = [
      "arn:aws:s3:::${var.dashboard_bucket_name}",
      "arn:aws:s3:::${var.dashboard_bucket_name}/*",
    ]
  }

  statement {
    sid    = "CloudFrontInvalidate"
    effect = "Allow"
    actions = [
      "cloudfront:CreateInvalidation",
      "cloudfront:GetInvalidation",
    ]
    resources = [local.cloudfront_resource]
  }

  # The `TerraformStateAccess` statement that used to live here (S3 + DynamoDB
  # state-bucket + lock-table access) moved to environments/reference/init/.
  # That role's job — terraform-apply on the prod stack — is now handled by
  # `github-actions-axiaops-terraform`. This role here is app-deploys-only and
  # has no business touching Terraform state.
}

resource "aws_iam_role_policy" "github_ci_deploy" {
  name   = "github-actions-axiaops-deploy"
  role   = aws_iam_role.github_ci_deploy.id
  policy = data.aws_iam_policy_document.github_ci_deploy.json
}

# --- Trust policies ----------------------------------------------------------

# ecs-tasks.amazonaws.com: assumed by Fargate task ENIs. Used by the execution
# role (image pull + secret injection) and every task role (runtime identity)
# for both the Express services and the migrate task.
data "aws_iam_policy_document" "ecs_task_trust" {
  statement {
    effect  = "Allow"
    actions = ["sts:AssumeRole"]

    principals {
      type        = "Service"
      identifiers = ["ecs-tasks.amazonaws.com"]
    }
  }
}

# ecs.amazonaws.com: the ECS control plane itself (NOT ecs-tasks). Express Mode
# assumes this role to provision the shared ALB, target groups, listener rules,
# and the Service SG. Different principal from the task trust above — a common
# confusion point (design §"Risks").
data "aws_iam_policy_document" "ecs_express_infra_trust" {
  statement {
    effect  = "Allow"
    actions = ["sts:AssumeRole"]

    principals {
      type        = "Service"
      identifiers = ["ecs.amazonaws.com"]
    }
  }
}

# --- ECS task execution role (shared: Express api + ingestion) ---------------
# Fargate uses this to pull the image and inject SSM SecureString secrets[]
# into the container at task start. One execution role serves both runtime
# services. The migrate task keeps its own dedicated execution role below
# (separate cluster, separate secret prefix).

resource "aws_iam_role" "ecs_task_execution" {
  name               = "axiaops-${var.env_name}-ecs-task-execution"
  description        = "ECS execution role for the Express runtime services: pulls images, injects api+ingestion SSM secrets."
  assume_role_policy = data.aws_iam_policy_document.ecs_task_trust.json
}

# AmazonECSTaskExecutionRolePolicy covers ECR auth/pull + CloudWatch Logs
# create-stream/put-events. It does NOT cover ssm:GetParameters for the
# SecureString secrets[] injection — that's the inline policy below.
resource "aws_iam_role_policy_attachment" "ecs_task_execution_managed" {
  role       = aws_iam_role.ecs_task_execution.name
  policy_arn = local.managed_ecs_task_execution_arn
}

data "aws_iam_policy_document" "ecs_task_execution" {
  statement {
    sid     = "InjectSecrets"
    effect  = "Allow"
    actions = ["ssm:GetParameters"]
    resources = [
      "arn:aws:ssm:${var.region}:${var.account_id}:parameter/axiaops/${var.env_name}/api/*",
      "arn:aws:ssm:${var.region}:${var.account_id}:parameter/axiaops/${var.env_name}/ingestion/*",
    ]
  }

  statement {
    sid    = "DecryptSecrets"
    effect = "Allow"
    # Kept (NOT omitted as the plan's "no kms needed" note suggested): every
    # existing role in this stack that reads these same alias/aws/ssm
    # SecureStrings -- migrate_task, migrate_execution, the former App Runner
    # instance roles -- carries this statement, and the migrate flow works.
    # IAM matches KMS resources against the key ARN (key/<uuid>), not the
    # alias; the aws/ssm key id is per-account/region and not in TF state, so
    # we scope to all keys + a kms:ViaService condition admitting only SSM
    # SecureString decrypts.
    actions   = ["kms:Decrypt"]
    resources = ["arn:aws:kms:${var.region}:${var.account_id}:key/*"]
    condition {
      test     = "StringEquals"
      variable = "kms:ViaService"
      values   = ["ssm.${var.region}.amazonaws.com"]
    }
  }
}

resource "aws_iam_role_policy" "ecs_task_execution" {
  name   = "axiaops-${var.env_name}-ecs-task-execution"
  role   = aws_iam_role.ecs_task_execution.id
  policy = data.aws_iam_policy_document.ecs_task_execution.json
}

# --- ECS api task role (runtime identity inside the api container) -----------

resource "aws_iam_role" "ecs_api_task" {
  name               = "axiaops-${var.env_name}-ecs-api-task"
  description        = "ECS api Express service runtime identity (SSM read + SecureString decrypt). ecs-tasks principal."
  assume_role_policy = data.aws_iam_policy_document.ecs_task_trust.json
}

data "aws_iam_policy_document" "ecs_api_task" {
  statement {
    sid       = "ReadOwnSecrets"
    effect    = "Allow"
    actions   = ["ssm:GetParameter", "ssm:GetParameters"]
    resources = ["arn:aws:ssm:${var.region}:${var.account_id}:parameter/axiaops/${var.env_name}/api/*"]
  }

  statement {
    sid    = "DecryptSecrets"
    effect = "Allow"
    # See ecs_task_execution.DecryptSecrets for the kms:ViaService rationale.
    actions   = ["kms:Decrypt"]
    resources = ["arn:aws:kms:${var.region}:${var.account_id}:key/*"]
    condition {
      test     = "StringEquals"
      variable = "kms:ViaService"
      values   = ["ssm.${var.region}.amazonaws.com"]
    }
  }
}

resource "aws_iam_role_policy" "ecs_api_task" {
  name   = "axiaops-${var.env_name}-ecs-api-task"
  role   = aws_iam_role.ecs_api_task.id
  policy = data.aws_iam_policy_document.ecs_api_task.json
}

# --- ECS ingestion task role -------------------------------------------------
# Mirrors the former App Runner ingestion instance role: SSM read + decrypt,
# plus the Tier-1/Tier-2 cloud-cost read-only detection actions and the
# customer cross-account assume.

# Named "AxiaOpsScanner" deliberately: this is the customer-facing principal.
# The cross-account onboarding trust policy (dashboard Connect screen,
# docs/cross-account-roles-design.md §3.1, and the AxiaOpsIntegrationRole.yaml
# CloudFormation template) instructs customers to allow
# arn:aws:iam::<this-account>:role/AxiaOpsScanner to assume their read-only role.
# That principal must actually exist AND be the identity that performs the
# cross-account sts:AssumeRole — which is exactly this ECS ingestion task role.
# The name is capability-based, not substrate-based, per the design doc: do not
# rename it to leak the compute substrate (e.g. *-ecs-ingestion-task). The TF
# resource address stays `ecs_ingestion_task`; only the IAM role name changed.
resource "aws_iam_role" "ecs_ingestion_task" {
  name               = "AxiaOpsScanner"
  description        = "AxiaOps cross-account scanner identity: ECS ingestion runtime (SSM + AWS read-only) AND the principal customers trust for read-only cross-account scans. ecs-tasks principal."
  assume_role_policy = data.aws_iam_policy_document.ecs_task_trust.json
}

data "aws_iam_policy_document" "ecs_ingestion_task" {
  statement {
    sid       = "ReadOwnSecrets"
    effect    = "Allow"
    actions   = ["ssm:GetParameter", "ssm:GetParameters"]
    resources = ["arn:aws:ssm:${var.region}:${var.account_id}:parameter/axiaops/${var.env_name}/ingestion/*"]
  }

  statement {
    sid    = "DecryptSecrets"
    effect = "Allow"
    # See ecs_task_execution.DecryptSecrets for the kms:ViaService rationale.
    actions   = ["kms:Decrypt"]
    resources = ["arn:aws:kms:${var.region}:${var.account_id}:key/*"]
    condition {
      test     = "StringEquals"
      variable = "kms:ViaService"
      values   = ["ssm.${var.region}.amazonaws.com"]
    }
  }

  # Tier-1 + Tier-2 + API-only detection-rule actions (CLAUDE.md tables,
  # docs/production.md, docs/tier2_detections_status.md). Resource * because
  # describe APIs do not take per-resource ARNs.
  statement {
    sid    = "ReadCloudCosts"
    effect = "Allow"
    actions = [
      "ce:GetCostAndUsage",
      "cloudwatch:GetMetricStatistics",
      "cloudwatch:ListMetrics",
      "ec2:DescribeInstances",
      "ec2:DescribeNatGateways",
      "ec2:DescribeAddresses",
      "ec2:DescribeVolumes",
      "ec2:DescribeSnapshots",
      "ec2:DescribeImages",
      "rds:DescribeDBInstances",
      "rds:DescribeDBSnapshots",
      "lambda:ListFunctions",
      "elasticloadbalancing:DescribeLoadBalancers",
      "elasticache:DescribeCacheClusters",
      "elasticache:DescribeReplicationGroups",
      "es:ListDomainNames",
      "es:DescribeDomain",
      "redshift:DescribeClusters",
      "sagemaker:ListEndpoints",
      "dynamodb:ListTables",
      "dynamodb:DescribeTable",
      "eks:ListClusters",
      "eks:DescribeCluster",
      "ecs:ListClusters",
      "ecs:ListServices",
      "docdb:DescribeDBClusters",
      "kafka:ListClustersV2",
      "route53:ListHostedZones",
      "bedrock:ListProvisionedModelThroughputs",
      "kendra:ListIndices",
      "logs:DescribeLogGroups",
      "ecr:DescribeRepositories",
      "ecr:DescribeImages",
      "secretsmanager:ListSecrets",
      "s3:ListBucketMultipartUploads",
    ]
    resources = ["*"]
  }

  statement {
    sid    = "AssumeCustomerRoles"
    effect = "Allow"
    # sts:TagSession is kept but currently DORMANT.
    #
    # The AxiaOps ingestion previously set an AxiaOpsOrg session tag on every
    # AssumeRole call. That code path was removed on 2026-05-27 (axiaops
    # issue #26) as a workaround for an IAM regional propagation issue that
    # made the dual sts:TagSession grant (customer trust + this identity
    # policy) flake for fresh customer onboardings. The regression is pinned
    # at axiaops/services/ingestion/internal/provider/aws/sts_test.go, which
    # now asserts `len(in.Tags) == 0`.
    #
    # AWS evaluates sts:TagSession on BOTH sides of the call: the target
    # role's trust policy (resource-based, in the AxiaOpsIntegrationRole.yaml
    # CFN template) AND the caller's identity policy (here). Both grants are
    # intentionally KEPT so that when the feature is restored — under SCP-
    # style controls keyed on aws:PrincipalTag/AxiaOpsOrg — neither customers
    # nor this stack need a redeploy. The grants are inert without caller-
    # side use: sts:TagSession is only evaluated when the AssumeRole call
    # carries tags, and today the call carries none.
    actions = ["sts:AssumeRole", "sts:TagSession"]
    # Canonical customer role name is AxiaOpsIntegrationRole — the name the
    # AxiaOpsIntegrationRole.yaml CloudFormation template and the dashboard
    # Connect screen create. Trailing wildcard (no dash) admits both the bare
    # name and namespaced variants (AxiaOpsIntegrationRole-prod, etc.).
    # AxiaOpsCrossAccountReader-* is kept for back-compat with any role created
    # against the older docs before the name was reconciled.
    resources = [
      "arn:aws:iam::*:role/AxiaOpsIntegrationRole*",
      "arn:aws:iam::*:role/AxiaOpsCrossAccountReader-*",
    ]
  }
}

resource "aws_iam_role_policy" "ecs_ingestion_task" {
  name   = "axiaops-${var.env_name}-ecs-ingestion-task"
  role   = aws_iam_role.ecs_ingestion_task.id
  policy = data.aws_iam_policy_document.ecs_ingestion_task.json
}

# --- ECS Express Mode infrastructure role ------------------------------------
# Assumed by ECS (ecs.amazonaws.com) to provision the shared ALB + target
# groups + listener rules + Service SG that front the Express services. All
# ELB/SG operations in the AWS-managed policy are tag-scoped to
# AmazonECSManaged=true, so the role can't be abused for arbitrary ELB CRUD.

resource "aws_iam_role" "ecs_express_infrastructure" {
  name               = "axiaops-${var.env_name}-ecs-express-infrastructure"
  description        = "ECS Express Mode infrastructure role: provisions the shared ALB/SGs for Express services. ecs.amazonaws.com principal (NOT ecs-tasks)."
  assume_role_policy = data.aws_iam_policy_document.ecs_express_infra_trust.json
}

resource "aws_iam_role_policy_attachment" "ecs_express_infrastructure" {
  role       = aws_iam_role.ecs_express_infrastructure.name
  policy_arn = local.managed_express_infra_arn
}

# --- ECS migrate task role (runtime IAM identity inside the container) -------

resource "aws_iam_role" "migrate_task" {
  name               = "axiaops-${var.env_name}-migrate-task"
  description        = "ECS migrate task runtime identity."
  assume_role_policy = data.aws_iam_policy_document.ecs_task_trust.json
}

data "aws_iam_policy_document" "migrate_task" {
  statement {
    sid       = "ReadOwnSecrets"
    effect    = "Allow"
    actions   = ["ssm:GetParameter", "ssm:GetParameters"]
    resources = ["arn:aws:ssm:${var.region}:${var.account_id}:parameter/axiaops/${var.env_name}/migrate/*"]
  }

  statement {
    sid    = "DecryptSecrets"
    effect = "Allow"
    # See ecs_task_execution.DecryptSecrets for the kms:ViaService rationale.
    actions   = ["kms:Decrypt"]
    resources = ["arn:aws:kms:${var.region}:${var.account_id}:key/*"]
    condition {
      test     = "StringEquals"
      variable = "kms:ViaService"
      values   = ["ssm.${var.region}.amazonaws.com"]
    }
  }
}

resource "aws_iam_role_policy" "migrate_task" {
  name   = "axiaops-${var.env_name}-migrate-task"
  role   = aws_iam_role.migrate_task.id
  policy = data.aws_iam_policy_document.migrate_task.json
}

# --- ECS migrate execution role (used by Fargate to pull image + fetch SSM) --

resource "aws_iam_role" "migrate_execution" {
  name               = "axiaops-${var.env_name}-migrate-execution"
  description        = "ECS execution role: pulls migrate image, injects SSM secrets into the task."
  assume_role_policy = data.aws_iam_policy_document.ecs_task_trust.json
}

data "aws_iam_policy_document" "migrate_execution" {
  # ecr:GetAuthorizationToken is regional and AWS rejects any scoping other
  # than "*" (same constraint as the CI role above). Repo-level pulls stay
  # scoped to axiaops-*.
  statement {
    sid       = "GetAuthorizationToken"
    effect    = "Allow"
    actions   = ["ecr:GetAuthorizationToken"]
    resources = ["*"]
  }

  statement {
    sid    = "PullImage"
    effect = "Allow"
    actions = [
      "ecr:BatchCheckLayerAvailability",
      "ecr:GetDownloadUrlForLayer",
      "ecr:BatchGetImage",
    ]
    resources = ["arn:aws:ecr:${var.region}:${var.account_id}:repository/axiaops-*"]
  }

  statement {
    sid    = "WriteLogs"
    effect = "Allow"
    actions = [
      "logs:CreateLogStream",
      "logs:PutLogEvents",
    ]
    resources = ["arn:aws:logs:${var.region}:${var.account_id}:log-group:/aws/ecs/axiaops-migrate:*"]
  }

  statement {
    sid       = "InjectSecrets"
    effect    = "Allow"
    actions   = ["ssm:GetParameters"]
    resources = ["arn:aws:ssm:${var.region}:${var.account_id}:parameter/axiaops/${var.env_name}/migrate/*"]
  }

  statement {
    sid    = "DecryptSecrets"
    effect = "Allow"
    # See ecs_task_execution.DecryptSecrets for the kms:ViaService rationale.
    actions   = ["kms:Decrypt"]
    resources = ["arn:aws:kms:${var.region}:${var.account_id}:key/*"]
    condition {
      test     = "StringEquals"
      variable = "kms:ViaService"
      values   = ["ssm.${var.region}.amazonaws.com"]
    }
  }
}

resource "aws_iam_role_policy" "migrate_execution" {
  name   = "axiaops-${var.env_name}-migrate-execution"
  role   = aws_iam_role.migrate_execution.id
  policy = data.aws_iam_policy_document.migrate_execution.json
}
