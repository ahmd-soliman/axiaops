# CI/CD — Free Tools

## GitLab CI/CD (Recommended — Already Included)

Built into GitLab. No external service needed. Define pipelines in `.gitlab-ci.yml` at the root of each repo.

**Free tier:**
- 400 CI/CD minutes/month on shared runners
- Unlimited minutes on self-hosted runners (register a €5/month Hetzner VPS)
- Parallel jobs
- Docker-in-Docker support
- Caching and artifacts
- Environments and deployment tracking
- Container Registry integration

**Example `.gitlab-ci.yml` for AxiaOps (Go + Docker):**

```yaml
stages:
  - test
  - build
  - deploy

variables:
  DOCKER_IMAGE: registry.gitlab.com/axiaops-works/axiaopsops

test:
  stage: test
  image: golang:1.22
  script:
    - go test ./...
  cache:
    paths:
      - .go/pkg/mod/

build:
  stage: build
  image: docker:24
  services:
    - docker:dind
  script:
    - docker build -t $DOCKER_IMAGE:$CI_COMMIT_SHORT_SHA .
    - docker push $DOCKER_IMAGE:$CI_COMMIT_SHORT_SHA
  only:
    - main

deploy:
  stage: deploy
  script:
    - flyctl deploy --image $DOCKER_IMAGE:$CI_COMMIT_SHORT_SHA
  only:
    - main
```

---

## Self-Hosted GitLab Runner

Register your own runner to remove the 400 minute/month limit entirely.

```bash
# macOS
brew install gitlab-runner
gitlab-runner register \
  --url https://gitlab.com \
  --token YOUR_PROJECT_TOKEN \
  --executor docker \
  --docker-image alpine
```

**Recommended VPS for runner:** Hetzner CX22 — €4.51/month, 2 vCPU, 4GB RAM. More than enough for a solo project.

---

## Fly.io Deploy Integration

AxiaOps's backend will deploy to Fly.io. Add the Fly CLI to your pipeline:

```yaml
deploy:
  stage: deploy
  image: flyio/flyctl:latest
  script:
    - flyctl deploy
  environment:
    name: production
    url: https://axiaops.fly.dev
  only:
    - main
```

Set `FLY_API_TOKEN` as a GitLab CI/CD variable (masked).

---

## Free CI/CD Tools Comparison

| Tool | Free Minutes | Self-Hosted | Best For |
|------|-------------|-------------|----------|
| **GitLab CI** | 400/month | Yes (unlimited) | Already using GitLab |
| **GitHub Actions** | 2,000/month | Yes | GitHub repos |
| **Woodpecker CI** | Unlimited (self-hosted) | Yes | Lightweight, open-source |
| **Dagger** | N/A (local + any CI) | Yes | Portable pipelines across CI systems |

**Recommendation:** Stay with **GitLab CI** and register a self-hosted runner to remove the minute limit. No reason to add another tool.

---

## Code Quality (Free)

| Tool | What It Does | Free |
|------|-------------|------|
| **golangci-lint** | Go linter aggregator (50+ linters) | Yes — open-source |
| **ESLint** | JavaScript / TypeScript linter | Yes — open-source |
| **Prettier** | Code formatter (JS/TS/CSS) | Yes — open-source |
| **SonarCloud** | Static analysis + security scan | Free for public repos; free for 1 private |
| **Semgrep** | Security-focused static analysis | Free tier available |

Add golangci-lint to the pipeline:

```yaml
lint:
  stage: test
  image: golangci/golangci-lint:latest
  script:
    - golangci-lint run ./...
```
