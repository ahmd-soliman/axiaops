# GitLab Runner Setup

How to configure GitLab runners for the AxiaOps CI pipeline.

The `.gitlab-ci.yml` is written so that every job carries its own `image:` —
no runner-host toolchain is assumed. Every job that touches Docker (integration
tests, builds, deploys) talks to the runner host's Docker daemon through a
mounted `/var/run/docker.sock`. No DinD, no per-job daemon.

---

## Runner capability profile

One runner profile covers every job in the pipeline:

| Jobs | Needs |
|---|---|
| `test:*`, `test:integration:*`, `build:images`, `deploy:*` | Docker executor + mounted `/var/run/docker.sock` + persistent Go cache bind mounts + `gitlab-cloud-runner-network` available on the host |

`deploy:dev` / `deploy:staging` manage long-lived containers on the runner
host via docker-compose (attached to `gitlab-cloud-runner-network`).
`build:images` and `deploy:production` push images to registries. Everything
shares the same host daemon, so no runner tagging is needed.

The DinD vs socket mount section below is kept as background — the pipeline
used DinD briefly on `feature/containerized-ci` but was switched to
socket-mount-for-all because the self-hosted-hosted runner was already one level of
Docker nesting deep and the per-job DinD boot was pure overhead.

---

## DinD vs socket mount

Two ways a CI job can get Docker daemon access. The pipeline uses DinD for the
jobs that can tolerate fresh-daemon-per-job, and socket mount only for deploy
jobs that must manage long-lived containers on the runner host.

### What each is

**Docker-in-Docker (DinD).** GitLab starts a second, ephemeral Docker daemon
as a service container (`docker:24-dind`) alongside the job container. The job
talks to this daemon via `DOCKER_HOST=tcp://docker:2375`. Daemon is destroyed
when the job ends.

**Socket mount.** The host's Docker daemon (`/var/run/docker.sock`) is bind-mounted
into the job container. The job's `docker` commands go straight to the daemon
that's already running on the runner host. Daemon and its state persist across
jobs.

### Comparison

| | DinD | Socket mount |
|---|---|---|
| Isolation between jobs | Each job gets its own daemon — no state leak | Shared daemon — images, containers, networks persist between jobs |
| Runner config | `services_privileged = true` (or `privileged = true`) | Mount `/var/run/docker.sock` into jobs |
| Startup cost | ~3–5s per job to launch DinD | Instant |
| Build cache reuse | Cold per job unless using BuildKit remote cache | Warm — previous layers stay on host daemon |
| Runner compromise blast radius | Attacker gets the DinD service's capabilities (inside the runner) | Attacker gets the host Docker daemon — can read/write/run anything the host can |
| Works on GitLab.com shared runners | **Yes** — their standard pattern | **No** — shared multi-tenant infra doesn't allow socket mount |
| Works on self-hosted runners | Yes (needs privileged flag) | Yes |
| Good for running long-lived containers on the runner host | No | Yes — the deploy pattern |
| Suitable for multi-tenant CI | Yes | No |

### When to pick which

- **DinD** — when you want per-job daemon isolation, or when you might move to
  GitLab.com shared runners later. This is the portable default.
- **Socket mount** — when jobs legitimately need to manage containers on the
  runner host itself (long-running dev/staging stacks), or on fully self-hosted
  setups where you accept the shared-daemon risk as a simplicity win.

### Why this pipeline uses DinD (mostly)

The deciding factor is GitLab.com compatibility. If the pipeline used socket
mount for test + build jobs, moving to shared runners later would require a CI
rewrite. DinD works on both self-hosted (with `services_privileged`) and shared
runners without changes.

`deploy:dev` / `deploy:staging` genuinely need host Docker — they manage
persistent containers on a specific runner host as part of the deploy model.
They can't use DinD because the DinD daemon dies with the job.

### Why DinD needs privileged

A Docker daemon needs kernel capabilities that default (unprivileged)
containers don't have:

- manage cgroups,
- create network namespaces and manipulate iptables,
- set up overlayfs (image layer mounts),
- mount tmpfs and `/sys/fs/cgroup`,
- create device nodes.

Without `privileged`, `docker:24-dind` fails at boot with "operation not
permitted" errors setting up cgroups or iptables.

`services_privileged = true` (GitLab Runner 15.0+) is preferred — it scopes
privileged to the DinD service container only. The job container (running
`docker build` / `go test`) stays unprivileged. Blast radius: a compromised
DinD service could only break out to the runner host, which is already what's
running `gitlab-runner`.

### Daemonless alternatives

If `services_privileged` still bothers you:

- **Socket mount everywhere** — works but undoes isolation; not portable to
  GitLab.com shared runners.
- **Buildah** — daemonless image builder, Dockerfile-compatible, rootless-capable.
  Replaces `docker build` in `build:images`. Doesn't help integration tests or
  deploy jobs — they still need a container runtime.
- **Sysbox runtime** — lets unprivileged containers run a Docker daemon.
  Requires Sysbox installed on the runner host.
- **Rootless DinD** — limited iptables/overlayfs support; integration tests
  that use compose networking generally break.

None are worth adopting at AxiaOps scale unless a specific policy demands it.

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
    image = "docker:24"                                       # default for jobs without image:
    volumes = [
      "/var/run/docker.sock:/var/run/docker.sock",            # host daemon for all jobs
      "/cache/gomod:/gocache/mod",                            # persistent Go module cache
      "/cache/gobuild:/gocache/build",                        # persistent Go build cache
      "/cache/golangci-lint:/gocache/golangci-lint",          # persistent lint cache
      "/cache",                                               # GitLab runner scratch
    ]
    pull_policy = "if-not-present"
```

**Why the host Docker socket is mounted at the default path.** Every job
(tests, build, deploy) uses the runner host's Docker daemon via this
mount. No DinD service runs alongside the jobs — the previous `extends:
.dind` anchor and `services_privileged` flag were removed to eliminate
nested-Docker overhead on the self-hosted-hosted runner. Since nothing else
competes for `/var/run/docker.sock` inside job containers, mounting it at
the default path is simplest.

**Why Go caches are persisted via bind mount, not GitLab's `cache:`
mechanism.** GitLab Runner archives cached paths into a tarball at job
end and extracts it at job start. For a Go module cache (~24k small files
on a ZFS-backed self-hosted container), that's ~3 minutes of tar+gzip per job —
pure overhead on a single-runner setup where the archive is written and
read on the same machine. Bind-mounting persistent directories from the
host skips the archive dance entirely: Go writes straight to the host
path, next job sees the same files instantly. `.gitlab-ci.yml` sets
`GOMODCACHE=/gocache/mod`, `GOCACHE=/gocache/build`, and
`GOLANGCI_LINT_CACHE=/gocache/golangci-lint` to match these mounts.

**Why there is no `network_mode`.** `gitlab-cloud-runner-network` is
attached by the deploy compose files (`external: true`) and by explicit
`--network` flags on one-off `docker run` commands. The runner itself does
not need to be on that network.

One-time bootstrap on the runner host — create the cache directories and
the shared deploy network:

```bash
# Persistent Go caches (777 so root-in-container jobs can write)
sudo mkdir -p /cache/gomod /cache/gobuild /cache/golangci-lint
sudo chmod 777 /cache/gomod /cache/gobuild /cache/golangci-lint

# Shared network for deploy compose stacks and the migration docker run
docker network inspect gitlab-cloud-runner-network >/dev/null 2>&1 \
  || docker network create gitlab-cloud-runner-network
```

Register (or re-register) the runner — no tags needed for the unified
single-runner setup:

```bash
gitlab-runner register \
  --non-interactive \
  --url "https://gitlab.example.com/" \
  --registration-token "<project-or-group-token>" \
  --executor docker \
  --docker-image "docker:24" \
  --description "axiaops-runner"
```

Then edit `config.toml` to add the `[runners.docker]` settings shown above
(registration doesn't set `services_privileged` or `volumes`).

Restart: `systemctl restart gitlab-runner`.

If your GitLab Runner version predates `services_privileged` (added in 15.0),
fall back to `privileged = true`. Worse scoping — job containers are also
privileged — but still better than shell executor. See
[DinD vs socket mount](#dind-vs-socket-mount) above for the rationale.

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
- `deploy:dev` reaches `axiaops-dev-db` by name — confirms the deploy compose file's `gitlab-cloud-runner-network` attachment works and the host network exists.

---

## What this setup removes

- No custom runner image to build, version, or publish.
- No `.go_setup` or host-tool assumptions (Go, golangci-lint, Docker installed on runner host).
- No manual `docker run` + readiness probe + `after_script` cleanup in CI jobs.
- No `RUNNER_NETWORK` variable in `.gitlab-ci.yml`.
- No IP-lookup workaround (the commit `dafac6b` pattern).

---

## Troubleshooting

**DinD service fails to start with `services_privileged` set.** Look at the
service container logs for `can't create unix socket /var/run/docker.sock:
device or resource busy`. This means the runner's `volumes =` line mounts
the host socket onto `/var/run/docker.sock` inside the DinD container,
blocking DinD from creating its own. Rebind to a non-conflicting path:
`/var/run/docker.sock:/var/run/host-docker.sock`, and set
`DOCKER_HOST=unix:///var/run/host-docker.sock` in deploy jobs that need
the host daemon.

**DinD service fails to start otherwise.** Check that `services_privileged =
true` (or `privileged = true`) is set. `docker:24-dind` cannot start
without it.

**`docker: not found` inside the job.** The job is using an image without the
Docker CLI. For CI jobs that need `docker` commands, make sure `image:` is
`docker:24` (directly or via `extends: .dind`).

**`deploy:dev` fails with `unable to resolve axiaops-dev-db`.** The DB
container isn't on `gitlab-cloud-runner-network`, or the network doesn't
exist on the runner host. Confirm `docker network ls` shows
`gitlab-cloud-runner-network`, and that `deploy/dev.yml` declares it as
`external: true` with every service attached.

**`test:storage` fails with postgres connection refused.** The `postgres`
alias isn't reachable. Check `services:` in the job has `alias: postgres` and
that `DATABASE_URL` uses `@postgres:5432` as the host.

**Shared runners don't pick up the pipeline.** Tags routing: jobs with
`tags: [self-hosted, docker-socket]` won't run on shared runners. That's
correct for `deploy:dev` / `deploy:staging`; for other jobs, ensure no stray
tags are set and shared runners are enabled on the project.
