# GitLab Runner Setup

How to configure GitLab runners for the AxiaOps CI pipeline.

The `.gitlab-ci.yml` is written so that every job carries its own `image:` and
`services:` — no runner-host tooling is assumed. Any runner that can run
Docker-executor jobs with DinD works. There is no custom runner image to build
or maintain.

---

## Runner capability profiles

The pipeline has two runner profiles:

| Jobs | Needs | Runs on |
|---|---|---|
| `test:*`, `build:images`, `deploy:production` | Docker executor + DinD service (privileged) | GitLab.com shared runners, or self-hosted |
| `deploy:dev`, `deploy:staging` | Docker executor + mounted host socket + attached to `gitlab-cloud-runner-network` | Self-hosted only (tags: `self-hosted`, `docker-socket`) |

`deploy:dev` / `deploy:staging` manage long-lived containers on a specific
runner host via docker-compose. They need the host Docker daemon, not DinD, and
must be on the same Docker network as the dev/staging DB container. They're
routed via `tags:` in the CI file.

All other jobs are untagged and go to any runner that can pick them up.

---

## Option A — one unified self-hosted runner

Simplest. One `[[runners]]` block handles everything.

`/etc/gitlab-runner/config.toml`:

```toml
[[runners]]
  name = "axiaops-runner"
  url = "https://gitlab.example.com/"
  token = "<existing-token>"
  executor = "docker"
  [runners.docker]
    image = "docker:24"                              # default for jobs without image:
    services_privileged = true                       # DinD service needs privileged
    volumes = [
      "/var/run/docker.sock:/var/run/docker.sock",   # deploy:dev / deploy:staging
      "/cache",
    ]
    network_mode = "gitlab-cloud-runner-network"     # deploy:dev/staging reach DB by name
    pull_policy = "if-not-present"
```

One-time bootstrap on the runner's Docker daemon:

```bash
docker network inspect gitlab-cloud-runner-network >/dev/null 2>&1 \
  || docker network create gitlab-cloud-runner-network
```

Register (or re-register) the runner with tags `self-hosted, docker-socket`:

```bash
gitlab-runner register \
  --non-interactive \
  --url "https://gitlab.example.com/" \
  --registration-token "<project-or-group-token>" \
  --executor docker \
  --docker-image "docker:24" \
  --tag-list "self-hosted,docker-socket" \
  --description "axiaops-runner"
```

Then edit `config.toml` to add the `[runners.docker]` settings shown above
(registration doesn't set `services_privileged`, `volumes`, or `network_mode`).

Restart: `systemctl restart gitlab-runner`.

If your GitLab Runner version predates `services_privileged` (added in 15.0),
fall back to `privileged = true`. Worse scoping — job containers are also
privileged — but still better than shell executor.

### Why privileged is required

DinD runs a second Docker daemon inside a service container. A Docker daemon
needs kernel capabilities that a default (unprivileged) container doesn't have:

- manage cgroups (resource isolation),
- create network namespaces and manipulate iptables,
- set up overlayfs (image layer mounts),
- mount tmpfs, `/sys/fs/cgroup`, etc.,
- create device nodes.

`privileged = true` grants all kernel capabilities and disables seccomp /
AppArmor confinement. Without it, `docker:24-dind` fails at boot with
"operation not permitted" errors while setting up cgroups or iptables.

`services_privileged = true` is preferred because it scopes privileged to the
DinD service only. The job container (running `docker build` / `go test` / etc.)
stays unprivileged. Blast radius: a compromised DinD service could only break
out into the runner host — which is already what's running `gitlab-runner`.

Alternatives, in order of effort:

- **Socket mount only, no DinD** — share the runner's Docker daemon with jobs.
  No privileged needed. Downside: images, containers, and networks leak between
  jobs, which undoes most of the isolation benefit DinD gives you.
- **Sysbox runtime** — unprivileged containers can run a Docker daemon safely,
  but runner host needs Sysbox installed and containerd/Docker configured to
  use it.
- **Rootless DinD** — works but doesn't support iptables or overlayfs without
  extra kernel config; AxiaOps integration tests depend on both.

---

## Option B — GitLab.com shared runners + one minimal self-hosted

Zero runner maintenance for tests, build, and production deploy.

### Shared runners

Enable in Project Settings → CI/CD → Runners → "Enable shared runners for this
project." No config required; they already support DinD.

### Self-hosted runner (deploy:dev / deploy:staging only)

`/etc/gitlab-runner/config.toml`:

```toml
[[runners]]
  name = "axiaops-deploy"
  url = "https://gitlab.example.com/"
  token = "<token>"
  executor = "docker"
  [runners.docker]
    image = "docker:24"
    volumes = [
      "/var/run/docker.sock:/var/run/docker.sock",
      "/cache",
    ]
    network_mode = "gitlab-cloud-runner-network"
    pull_policy = "if-not-present"
```

No `privileged` or `services_privileged` needed — this runner only handles
deploy jobs, which don't use DinD.

Register with tags `self-hosted, docker-socket`:

```bash
gitlab-runner register \
  --non-interactive \
  --url "https://gitlab.example.com/" \
  --registration-token "<token>" \
  --executor docker \
  --docker-image "docker:24" \
  --tag-list "self-hosted,docker-socket" \
  --description "axiaops-deploy"
```

Bootstrap the network as in Option A.

---

## Migration from the previous shell-executor setup

1. Pick Option A or B.
2. Update `config.toml` on the target runner(s). Ensure `gitlab-cloud-runner-network` exists on the Docker daemon each runner talks to. `systemctl restart gitlab-runner`.
3. Decommission runners that no longer fit the new model:
   - self-hosted-based shell-executor runner → decommission.
   - Old socket-mount shell-executor runner → either repurpose with the config above, or decommission if moving to Option B.
4. Push the `feature/containerized-ci` branch. Confirm the pipeline is green.
5. Delete any custom runner images you previously built (`axiaops-ci-runner:*` or similar). No longer needed.

---

## Sanity checks

Before merging `feature/containerized-ci`:

- `test:storage` picks up the `postgres` service alias. First run confirms per-service `variables:` work (requires GitLab 14.5+).
- `test:integration:*` runs `make test-integration-*` successfully under DinD. `before_script` installs `make` and `docker-cli-compose` via `apk`; if the package name differs on your alpine version, swap for `docker-compose` or `pip install docker-compose`.
- `deploy:dev` reaches `axiaops-dev-db` by name — confirms the self-hosted runner is on `gitlab-cloud-runner-network`.

---

## What this setup removes

- No custom runner image to build, version, or publish.
- No `.go_setup` or host-tool assumptions (Go, golangci-lint, Docker installed on runner host).
- No manual `docker run` + readiness probe + `after_script` cleanup in CI jobs.
- No `RUNNER_NETWORK` variable in `.gitlab-ci.yml`.
- No IP-lookup workaround (the commit `dafac6b` pattern).

---

## Troubleshooting

**DinD service fails to start.** Check that `services_privileged = true` (or
`privileged = true`) is set. `docker:24-dind` cannot start without it.

**`docker: not found` inside the job.** The job is using an image without the
Docker CLI. For CI jobs that need `docker` commands, make sure `image:` is
`docker:24` (directly or via `extends: .dind`).

**`deploy:dev` fails with `unable to resolve axiaops-dev-db`.** The job container
is not attached to `gitlab-cloud-runner-network`. Confirm `network_mode =
"gitlab-cloud-runner-network"` in `config.toml` and that the network actually
exists (`docker network ls`).

**`test:storage` fails with postgres connection refused.** The `postgres`
alias isn't reachable. Check `services:` in the job has `alias: postgres` and
that `DATABASE_URL` uses `@postgres:5432` as the host.

**Shared runners don't pick up the pipeline.** Tags routing: jobs with
`tags: [self-hosted, docker-socket]` won't run on shared runners. That's
correct for `deploy:dev` / `deploy:staging`; for other jobs, ensure no stray
tags are set and shared runners are enabled on the project.
