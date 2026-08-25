output "github_oidc_provider_arn" {
  description = "ARN of the GitHub Actions OIDC provider in IAM. Identifier only; not sensitive."
  value       = aws_iam_openid_connect_provider.github.arn
}

output "ci_terraform_apply_role_arn" {
  description = "Role ARN for `terraform apply` (broad, gated on main-branch pushes). Set this as the AWS role-to-assume input for the terraform-apply job in .github/workflows/."
  value       = aws_iam_role.ci_terraform_apply.arn
}

output "ci_terraform_plan_role_arn" {
  description = "Role ARN for `terraform plan` on PR runs (read-only). Set this as the AWS role-to-assume input for the terraform-plan job."
  value       = aws_iam_role.ci_terraform_plan.arn
}

output "ci_terraform_apply_boundary_arn" {
  description = "Apply-role Permissions Boundary ARN. Informational; used by main/ if it ever attaches additional policies to the CI apply role."
  value       = aws_iam_policy.ci_boundary.arn
}

output "ci_terraform_plan_boundary_arn" {
  description = "Plan-role Permissions Boundary ARN. Informational; mirrors the apply boundary for the read-only plan role."
  value       = aws_iam_policy.ci_plan_boundary.arn
}
