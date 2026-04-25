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
| `test:*`, `test:integration:*`, `build:images`, `deploy:*` | Docker executor + mounted `/var/run/docker.sock` + persistent Go cache bind mounts + `gitlab-runner-network` available on the host |

`deploy:dev` / `deploy:staging` manage long-lived containers on the runner
host via docker-compose (attached to `gitlab-runner-network`).
`build:images` and `deploy:production` push images to registries. Everything
shares the same host daemon, so no runner tagging is needed — the pipeline
sets no `tags:` (default or per-job).

The DinD vs socket mount section below is reference material only. The
pipeline does not use DinD anywhere — every job mounts the host socket. The
self-hosted-hosted runner was already one level of Docker nesting deep, so a
per-job DinD daemon was pure overhead.

---

## DinD vs socket mount

Two ways a CI job can get Docker daemon access. This pipeline uses socket
mount for every job; DinD is documented here only as the alternative pattern
you would pick on shared multi-tenant runners.

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

### Why this pipeline uses socket mount everywhere

The runner is self-hosted on self-hosted — a single-tenant, single-runner setup.
The two reasons to prefer DinD (per-job daemon isolation, GitLab.com
shared-runner compatibility) don't apply: nothing else schedules jobs on this
runner, and there is no plan to move to shared runners (deploy jobs require
host Docker access regardless, which shared runners can't provide).

What socket mount buys: instant job startup (no DinD boot per job), warm
build cache (image layers persist on the host daemon between jobs), and one
config to maintain — `deploy:*` jobs need the host daemon anyway, and
running tests + builds the same way removes a special case.

The cost: a compromised job has the host daemon's capabilities. Acceptable
on a single-tenant runner that already runs `gitlab-runner` as root.

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

**Why there is no `network_mode`.** `gitlab-runner-network` is
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
docker network inspect gitlab-runner-network >/dev/null 2>&1 \
  || docker network create gitlab-runner-network
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
    network_mode = "gitlab-runner-network"
    pull_policy = "if-not-present"
```

No `privileged` or `services_privileged` needed — this runner only handles
deploy jobs, which don't use DinD.

Register without tags — the pipeline routes deploy jobs by job name, not
runner tags:

```bash
gitlab-runner register \
  --non-interactive \
  --url "https://gitlab.example.com/" \
  --registration-token "<token>" \
  --executor docker \
  --docker-image "docker:24" \
  --description "axiaops-deploy"
```

Bootstrap the network as in Option A.

Note: with no tags, this runner is also eligible to pick up shared-runner
jobs unless you set `[[runners]] limit = 0` on the shared-runner-only jobs
or restrict via project settings. For Option B to make sense, gate this
runner so it only picks up `deploy:dev` / `deploy:staging` — easiest is to
re-introduce a tag (`deploy`) on those two jobs and on this runner.

---

## Migration from the previous shell-executor setup

1. Pick Option A or B.
2. Update `config.toml` on the target runner(s). Ensure `gitlab-runner-network` exists on the Docker daemon each runner talks to. `systemctl restart gitlab-runner`.
3. Decommission runners that no longer fit the new model:
   - self-hosted-based shell-executor runner → decommission.
   - Old socket-mount shell-executor runner → either repurpose with the config above, or decommission if moving to Option B.
4. Push the `feature/containerized-ci` branch. Confirm the pipeline is green.
5. Delete any custom runner images you previously built (`axiaops-ci-runner:*` or similar). No longer needed.

---

## Sanity checks

Before merging `feature/containerized-ci`:

- `test:storage` picks up the `postgres` service alias. First run confirms per-service `variables:` work (requires GitLab 14.5+).
- `test:integration:*` runs `make test-integration-*` against the host daemon via the mounted socket. `before_script` installs `make` and `docker-cli-compose` via `apk`; if the package name differs on your alpine version, swap for `docker-compose` or `pip install docker-compose`.
- `deploy:dev` reaches `axiaops-dev-db` by name — confirms the deploy compose file's `gitlab-runner-network` attachment works and the host network exists.

---

## What this setup removes

- No custom runner image to build, version, or publish.
- No `.go_setup` or host-tool assumptions (Go, golangci-lint, Docker installed on runner host).
- No manual `docker run` + readiness probe + `after_script` cleanup in CI jobs.
- No DinD service container or `services_privileged` requirement.
- No IP-lookup workaround (the commit `dafac6b` pattern).

(Note: a `RUNNER_NETWORK` variable still appears in `.gitlab-ci.yml` for
backwards reference but isn't used — `--network` flags and
`deploy/{dev,staging}.yml` use the literal `gitlab-runner-network`. Safe to
delete in a future cleanup.)

---

## Troubleshooting

**`Cannot connect to the Docker daemon at unix:///var/run/host-docker.sock`.**
A `DOCKER_HOST` override is set on a job, but the runner mounts the host
daemon at the standard `/var/run/docker.sock` path (see Option A `volumes`).
Don't set `DOCKER_HOST` — the Docker CLI defaults to `/var/run/docker.sock`,
which is exactly where the runner mounts it. The non-standard `host-docker.sock`
rebind only makes sense if a DinD service container also runs in the same job
and needs to claim `/var/run/docker.sock` for itself; this pipeline doesn't
do that anywhere.

**`docker: not found` inside the job.** The job is using an image without the
Docker CLI. For CI jobs that need `docker` commands, set `image: docker:24`.

**`deploy:dev` fails with `unable to resolve axiaops-dev-db`.** The DB
container isn't on `gitlab-runner-network`, or the network doesn't
exist on the runner host. Confirm `docker network ls` shows
`gitlab-runner-network`, and that `deploy/dev.yml` declares it as
`external: true` with every service attached.

**`test:storage` fails with postgres connection refused.** The `postgres`
alias isn't reachable. Check `services:` in the job has `alias: postgres` and
that `DATABASE_URL` uses `@postgres:5432` as the host.

**Pipeline stalls with no runner picking up jobs.** The pipeline currently
sets no `tags:` (default or per-job), so any runner enabled for the project
is eligible. If jobs sit in pending: confirm at least one runner is online
in Project Settings → CI/CD → Runners, and that no `default: tags:` block
has been re-introduced in `.gitlab-ci.yml` pointing at a tag no live runner
carries.

**(DinD reference, not used by this pipeline.)** If you ever switch a job
to DinD, the daemon needs `services_privileged = true` (or `privileged =
true`) to set up cgroups, iptables, and overlayfs. And if the runner also
mounts the host socket onto `/var/run/docker.sock`, DinD will fail with
`device or resource busy` — rebind the host socket to a non-conflicting
path (e.g. `/var/run/host-docker.sock`) for the DinD-using jobs only.
