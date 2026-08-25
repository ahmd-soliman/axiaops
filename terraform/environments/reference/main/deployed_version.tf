# Single source of truth for "what infra version is currently deployed".
#
# Replaces the previous `default_tags.InfraVersion` stamp on every resource.
# That approach lied at the per-resource level: `default_tags` only repaints
# a resource when something else about it changes, so individual tags drifted
# to "the version of the apply that last happened to touch me," not "the
# version that's currently deployed." Per-branch plans also produced 30+ tag
# rewrites of no operational value.
#
# Honest sources for the deploy state are now:
#   - git tags + CHANGELOG.md (release identifier + content)
#   - CI's terraform-apply job history (timestamps + commit refs)
#   - this SSM parameter (single, queryable, always current)
#
# Value flows from `var.deployed_version`, which CI sets via
# `TF_VAR_deployed_version="${CI_COMMIT_TAG:-$CI_COMMIT_SHORT_SHA}"` in
# .tf-aws.before_script. Plan from any pipeline shows one diff line —
# "value: <prev> -> <this-commit>" — which is honest: that IS what apply
# would write. Apply is main-only + manual, so the value that lands in
# prod is always a main SHA or a tag, never a feature-branch slug.
#
# Default "uninitialized" only kicks in for operator-laptop applies that
# don't export TF_VAR_deployed_version — same audit-hint pattern the old
# `var.infra_version` used.
resource "aws_ssm_parameter" "deployed_version" {
  name        = "/axiaops/prod/infra/DEPLOYED_VERSION"
  description = "Git tag or short SHA of the currently-deployed infra revision. Set by CI on every apply via TF_VAR_deployed_version."
  type        = "String"
  value       = var.deployed_version
}
