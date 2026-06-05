# Graviton / ARM64 for Prod Compute — Viable (the blocker was wrong)

**Status:** Viable — low-priority adoption | **Last Updated:** June 2026 | **Supersedes:** the earlier "Declined — ECS Express is x86-only" verdict

## TL;DR

The previous version of this doc **declined** Graviton on the premise that **ECS
Express is x86-only**. That premise was wrong (or has since changed): ECS Express
Mode — the managed successor to AWS App Runner, running on Fargate — **does
support selecting ARM/Graviton**. With the blocker gone:

- **The database is already on Graviton** — `db.t4g.micro` (`t4g` = Graviton2).
  The stateful tier, the one a migration would actually have risked, needs nothing.
- **Every service image is arm64-ready today** — no cgo, no arch pins, pure-Go DB
  drivers, multi-arch base images.
- The remaining work is **infra config, not code**: build multi-arch images in CI
  and set the Express service's runtime platform to ARM64 in `aws-infra`.
- The saving is still small (~€46/yr, compute-only) but now at **moderate effort**,
  not the hard "migrate off Express" the prior doc assumed.

**Recommendation: viable — adopt when next touching `aws-infra`, or alongside
multi-arch CI work. Not urgent (~€4/mo), but no longer blocked.**

This is a FinOps-for-AxiaOps decision — see the Cost Awareness section in the root
`CLAUDE.md`.

---

## What changed — the "x86-only" premise was wrong

The June-2026 version of this doc claimed `create-express-gateway-service` has no
`--runtime-platform` / `--cpu-architecture` input and that Express is hardcoded to
x86_64. The current understanding — per the [ECS Express Mode getting-started
guide][1], and consistent with Express being the App-Runner-on-Fargate successor —
is the opposite:

> "Or use `linux/arm64` and **select ARM/Graviton in Express Mode** for 20% cost
> savings."

So ARM/Graviton is a **selectable runtime platform** on Express, not a forbidden
one. Either AWS exposed it after the original check, or that check looked at the
wrong seam: the architecture lives on the task-definition / service runtime
platform, which in our stack is **TF-owned in `aws-infra`** — *not* set by the CI
`update-express-gateway-service --primary-container` call, which only carries
image + env. CI never touching arch is not the same as Express not supporting it.

> ⚠️ **Verify before implementing.** Confirm our specific Express service accepts
> `cpuArchitecture: ARM64` against the *current* live API
> (`aws ecs create-express-gateway-service help`) and the `aws-infra` TF resource
> schema. The article asserts Express supports it; the one task left is to make it
> true for our stack.

## Component readiness — all green

| Component | Current arch | Graviton-ready? | Effort | Notes |
|---|---|---|---|---|
| Go services (api, ingestion, api-admin, migrate) | x86 (runner-default) | ✅ | trivial | No cgo, no arch pins, pure-Go pgx/pq, argon2 ships ARM64 asm |
| `services/shared` | x86 | ✅ | trivial | — |
| Dashboards (dashboard, dashboard-admin) | x86 / serverless | ✅ | trivial | multi-arch node/nginx bases; **prod dashboard is S3+CloudFront — arch-free** |
| **RDS PostgreSQL** (`db.t4g.micro`) | **ARM (Graviton2)** | ✅ **already done** | none | PG17 fully supported on t4g; RLS, schema, the 3 DB roles are all arch-invisible |
| **ECS Express compute** | x86 (TF-set) | ✅ supported | moderate | flip the service runtime platform to ARM64 in `aws-infra` |
| CI build pipeline | x86 runner | ⚠️ partial | moderate | add `--platform` / multi-arch manifests (see below) |
| Homelab dev/staging (self-hosted) | x86 (on-prem) | N/A | — | **not AWS** — stays x86; pulls the same images, so they must be multi-arch |

The application layer presents **zero** engineering blocker. The work is entirely
in infra (CI + the deploy target), not code.

## The work (now that it's unblocked)

1. **CI: build multi-arch images.** Today `build:images` runs plain `docker build`
   on x86 GitLab runners → x86 images. The homelab dev/staging self-hosted hosts are
   **x86** and pull the **same** images, so CI must build **multi-arch manifests**
   (`docker buildx build --platform linux/amd64,linux/arm64 --push`), **not**
   arm64-only — or dev/staging breaks. Under QEMU on the existing x86 runner this
   adds minutes per build (Go + Vite under emulation); a native arm64 runner avoids
   the slowdown but is new infra.
2. **aws-infra: set the Express service runtime platform to `ARM64`.** This is the
   change the prior doc thought impossible. Prod then pulls the arm64 image out of
   the multi-arch manifest.
3. **migrate one-off task** inherits the TF-set arch automatically — no separate
   change.

## The savings math

Fargate on-demand, eu-central-1; ARM64 is ~20% cheaper per vCPU-hr and GB-hr.

| | per vCPU-hr | per GB-hr |
|---|---|---|
| x86_64 | $0.04656 | $0.00511 |
| ARM64 (Graviton) | $0.03725 | $0.00409 |

Per task (0.25 vCPU + 0.5 GB) × 730 hr/mo, both `axiaops-api` + `axiaops-ingestion`:

| | Monthly | Annual |
|---|---|---|
| x86 (today) | **$20.72** | $248.6 |
| ARM (Graviton) | **$16.58** | $199.0 |
| **Saving** | **$4.14 / mo** | **~$49.6 / yr (~€46)** |

The ALB (~$20/mo), RDS, CloudFront, S3, and data transfer are **unchanged** by
arch, so they never enter the delta. The DB being already-Graviton doesn't
*increase* the prize — it confirms the prize is **only** the compute line. What
changed versus June 2026 is the **cost to capture it**: from "rebuild off Express"
(the old reject) down to "multi-arch CI + a TF arch flip."

## When to do it

- **Opportunistically / soon:** the multi-arch CI change is good hygiene on its own
  (future-proofs the AWS arm64 target *and* any ARM dev box). Pair it with the
  `aws-infra` arch flip and you bank ~€46/yr.
- **Definitely** when prod task sizes grow materially: the 20% saving scales with
  vCPU/GB (~$33/yr **per task** at 1 vCPU / 2 GB), so the case strengthens with load.
- **Skip** only if the multi-arch build time / runner cost outweighs ~€4/mo for the
  pipeline — a real but small consideration.

## ECS Express vs normal ECS (for context)

| | ECS Express | Normal ECS |
|---|---|---|
| Task definition | Auto-managed by AWS | You author it |
| CPU architecture | **ARM64 / x86 selectable** (runtime platform) | ARM64 / x86 / Spot — first-class |
| Load balancer | ALB auto-created (shared ≤25 svc) | You provision |
| Deploy safety | Canary + rollback **built-in** | You wire it (e.g. CodeDeploy) |
| Setup effort | Minutes | Substantial IaC |
| Best for | Simple stateless web services | Anything needing Spot / multi-container / low-level control |

We're on Express because api + ingestion are two simple stateless web services and
Express gives the ALB + canary deploys + autoscaling for near-zero config — and,
as it turns out, **Graviton is on the menu too**, so we keep the convenience and
the ARM saving both.

## History

- **June 2026 (original):** Declined on the premise "ECS Express is x86-only" — no
  arch input found on `create-express-gateway-service`.
- **June 2026 (this rewrite):** premise corrected — Express Mode supports ARM/
  Graviton selection (the App-Runner-on-Fargate successor); the DB is already on
  t4g Graviton; all service images are arm64-ready. Re-classified from *Declined
  (blocked)* to **Viable (low-priority)**. Concrete next steps: verify the live
  Express API accepts `cpuArchitecture: ARM64`, add multi-arch CI builds, flip the
  `aws-infra` runtime platform.

## References

- [AWS ECS Express Mode — getting-started guide (2026)][1] — confirms ARM/Graviton
  is selectable in Express Mode.
- AWS docs: [ECS task definitions for 64-bit ARM workloads](https://docs.aws.amazon.com/AmazonECS/latest/developerguide/ecs-arm64.html),
  [Resources created by ECS Express Mode services](https://docs.aws.amazon.com/AmazonECS/latest/developerguide/express-service-work.html).

[1]: https://dev.to/parag477/aws-ecs-express-mode-the-complete-getting-started-guide-2026-257j
