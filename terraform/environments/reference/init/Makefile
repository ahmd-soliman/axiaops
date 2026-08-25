# Operator workflow for the local `init/` stack + the OOB ceremony that
# accompanies a first deploy.
#
# Targets:
#   init                — terraform init for the init stack (one-time per laptop)
#   plan                — show what `init` would do
#   apply               — `terraform apply` on the init stack (creates IAM + OIDC)
#   outputs             — print the role ARNs to paste into GitHub Actions secrets
#   set-encryption-key  — generate + write ENCRYPTION_KEY to SSM (Tier 1 OOB)
#   first-deploy        — runs `apply` + prints next-step instructions
#
# This Makefile is the "manual steps packed in" seam that a fully-automated
# bootstrap would eliminate — see README.md.

AWS_PROFILE ?= default
ENV_NAME    ?= prod
SSM_PREFIX  := /axiaops/$(ENV_NAME)

.PHONY: init plan apply outputs set-encryption-key first-deploy help

help:
	@echo "make init                 — terraform init"
	@echo "make plan                 — show what init/ would do"
	@echo "make apply                — apply the init stack (one-time per account)"
	@echo "make outputs              — print role ARNs for GitHub Actions secrets"
	@echo "make set-encryption-key   — OOB: generate + write ENCRYPTION_KEY to SSM"
	@echo "make first-deploy         — apply + next-step instructions"

init:
	AWS_PROFILE=$(AWS_PROFILE) terraform init

plan:
	AWS_PROFILE=$(AWS_PROFILE) terraform plan

apply:
	AWS_PROFILE=$(AWS_PROFILE) terraform apply

outputs:
	@AWS_PROFILE=$(AWS_PROFILE) terraform output -json | jq -r '. | to_entries[] | "\(.key): \(.value.value)"'

set-encryption-key:
	@# First-run guard. If a real (non-placeholder) value already exists in
	@# SSM, re-running this would generate a NEW key, overwrite the existing
	@# one, and silently leave every customer credential encrypted-at-rest
	@# in Postgres unreadable. Abort hard. To intentionally re-key, see the
	@# rotation procedure (requires coordinated app-level re-encryption).
	@CURRENT=$$(AWS_PROFILE=$(AWS_PROFILE) aws ssm get-parameter \
	  --name "$(SSM_PREFIX)/api/ENCRYPTION_KEY" --with-decryption \
	  --query 'Parameter.Value' --output text 2>/dev/null || echo "NOT_FOUND"); \
	if [ "$$CURRENT" != "NOT_FOUND" ] && \
	   [ "$$CURRENT" != "PLACEHOLDER_REPLACE_VIA_OUT_OF_BAND_SSM_PUT_PARAMETER" ]; then \
	  echo "ABORT: $(SSM_PREFIX)/api/ENCRYPTION_KEY already has a real value."; \
	  echo "       Re-running this target would generate a new key and silently"; \
	  echo "       brick every customer credential currently encrypted with"; \
	  echo "       the existing key."; \
	  echo ""; \
	  echo "       To rotate intentionally: coordinate an application-level"; \
	  echo "       re-encryption of the customer_credentials table with the new"; \
	  echo "       key, then run the (yet-to-be-written) rotation procedure."; \
	  echo "       Do NOT use this target for rotation."; \
	  exit 1; \
	fi
	@echo "Generating new 32-byte hex AES-256-GCM key..."
	@KEY=$$(openssl rand -hex 32); \
	echo "Writing to SSM (api + ingestion prefixes)..."; \
	AWS_PROFILE=$(AWS_PROFILE) aws ssm put-parameter \
	  --name "$(SSM_PREFIX)/api/ENCRYPTION_KEY" \
	  --value "$$KEY" --type SecureString --overwrite >/dev/null; \
	AWS_PROFILE=$(AWS_PROFILE) aws ssm put-parameter \
	  --name "$(SSM_PREFIX)/ingestion/ENCRYPTION_KEY" \
	  --value "$$KEY" --type SecureString --overwrite >/dev/null; \
	echo ""; \
	echo "WROTE ENCRYPTION_KEY to both SSM paths. Back this up immediately:"; \
	echo ""; \
	echo "    $$KEY"; \
	echo ""; \
	echo "Losing this key means every customer credential in Postgres becomes"; \
	echo "unrecoverable. Save to your password manager NOW."

first-deploy: apply
	@echo ""
	@echo "============================================================"
	@echo "init/ applied. Next steps for a first deploy:"
	@echo "============================================================"
	@echo ""
	@echo "1. Copy the role ARNs into GitHub Actions secrets"
	@echo "   (repo Settings -> Secrets and variables -> Actions):"
	@echo ""
	@$(MAKE) -s outputs
	@echo ""
	@echo "       AWS_CI_TERRAFORM_ROLE_ARN"
	@echo "       AWS_CI_TERRAFORM_PLAN_ROLE_ARN"
	@echo "       AWS_REGION = eu-central-1  (or your chosen region)"
	@echo ""
	@echo "2. Open a PR for any change in main/ (or push to main directly if"
	@echo "   you're confident). The workflow will apply the main stack."
	@echo ""
	@echo "3. After main/'s first apply, the OOB ceremony:"
	@echo "       make set-encryption-key"
	@echo "       (then from the application repo's CI: docker push, ecs run-task migration)"
	@echo ""
