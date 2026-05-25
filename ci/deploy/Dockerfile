# Pinned CI deploy image for `deploy:production` (issue #102).
#
# Replaces the floating `apk add --no-cache --upgrade aws-cli jq expat` on the
# Alpine `docker:24` base, which:
#   (a) installed Alpine's aws-cli 2.15.57 — too old for the ECS Express Mode
#       verbs (`update-/monitor-express-gateway-service`, GA Nov 2025), so the
#       service-update step failed with "Invalid choice"; and
#   (b) hit a musl libexpat/pyexpat symbol skew
#       (`XML_SetAllocTrackerActivationThreshold: symbol not found`).
#
# A glibc base + a pinned, official aws-cli v2 fixes both and is reproducible.
# Contents the deploy job needs:
#   - docker CLI  — ECR login + image retag/push against the runner's
#                   socket-mounted daemon
#   - aws-cli v2  — ECS Express verbs, SSM, S3, CloudFront, STS (OIDC)
#   - jq          — SSM inventory JSON wrangling
#   - nodejs/npm  — dashboard static-bundle build (`npm ci && npm run build`)
FROM amazonlinux:2023

# Must be >= the ECS Express Mode GA (Nov 2025) so the express-gateway-service
# verbs exist. 2.34.24 verified to carry them. Bump CI_DEPLOY_IMAGE_TAG in
# .gitlab-ci.yml whenever this changes.
ARG AWSCLI_VERSION=2.34.24

ENV AWS_PAGER=""

RUN dnf -y install --setopt=install_weak_deps=False \
        docker jq nodejs npm unzip tar gzip ca-certificates \
 && dnf clean all && rm -rf /var/cache/dnf

RUN curl -fsSL "https://awscli.amazonaws.com/awscli-exe-linux-x86_64-${AWSCLI_VERSION}.zip" \
        -o /tmp/awscliv2.zip \
 && unzip -q /tmp/awscliv2.zip -d /tmp \
 && /tmp/aws/install \
 && rm -rf /tmp/awscliv2.zip /tmp/aws

# Fail the image build (not the deploy, weeks later) if the pinned aws-cli lacks
# the ECS Express verbs — turns a silent deploy-time "Invalid choice" into a
# loud build-time failure.
RUN aws --version && docker --version && jq --version && node --version \
 && if aws ecs update-express-gateway-service 2>&1 | grep -qi 'invalid choice'; then \
        echo "FATAL: pinned aws-cli ${AWSCLI_VERSION} lacks ECS Express Mode verbs" >&2; \
        exit 1; \
    fi
