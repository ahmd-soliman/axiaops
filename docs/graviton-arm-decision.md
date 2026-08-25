# Graviton / ARM64 for Prod Compute — Declined (ECS Express is x86-only, verified)

**Status:** Declined — x86-only blocker confirmed | **Last Updated:** June 2026 | **Supersedes:** the June-2026 "Viable — the blocker was wrong" rewrite, which was itself wrong.

## TL;DR

A prior rewrite of this doc **reversed** the original "declined" verdict, claiming
ECS Express Mode supports selecting ARM/Graviton (citing a third-party blog). That
rewrite carried its own warning — *"⚠️ Verify before implementing"* — so on
2026-06-06 we verified it against the live AWS API on the production account. **The
"Express supports ARM" claim does not hold.** The original verdict was right:

- **The Express API has no architecture knob.** `create-express-gateway-service` /
  `update-express-gateway-service` expose only `cpu` + `memory` — there is no
  `runtimePlatform` / `cpuArchitecture` field anywhere in the input schema, nor on
  the describe output of the running prod service.
- **Fargate under Express hard-demands `linux/amd64`.** A live probe pointing an
  Express service at an arm64-only image failed to launch with
  `CannotPullContainerError: ... image Manifest does not contain descriptor
  matching platform 'linux/amd64'`. Express does **not** auto-detect arch from the
  image manifest — it requires amd64.
- **An unofficial hack exists but is rejected for prod** (see below): manually
  overriding the auto-generated task definition to `cpuArchitecture: ARM64`.
  Express owns that task def via its service-revision/reconcile model, so the
  override is liable to be reverted on the next Express deploy → exec-format crash.
- The DB is already on Graviton (`db.t4g.micro`) and every service image is
  arm64-ready, but none of that matters while the **compute target can't be told
  to run ARM** through a supported path.

**Recommendation: stay x86 on ECS Express. The ~€46/yr compute saving does not
justify an unsupported, self-reverting hack. Recheck for *official* Express ARM
support periodically (see the disclaimer below) — the app + DB are ready the day it
lands.**

This is a FinOps-for-AxiaOps decision — see the Cost Awareness section in the root
`CLAUDE.md`.

---

## ⚠️ Recheck trigger — official support may arrive

This verdict is pinned to the AWS API as it behaved on **2026-06-06**. Re-open this
decision the moment **either** of these becomes true:

- `aws ecs create-express-gateway-service` / `update-express-gateway-service` grows a
  `runtimePlatform` / `cpuArchitecture` input (check `--generate-cli-skeleton`), **or**
- the CloudFormation `AWS::ECS::ExpressGatewayService` resource gains a
  `RuntimePlatform` property.

If official support lands, adoption is cheap: the work is then just multi-arch CI
images + setting the property. Until then, the only route to ARM is the unsupported
hack, which we decline. **Don't re-flip this doc on the strength of a blog post —
re-verify against the live API first, the way this revision did.**

---

## How we verified (2026-06-06, prod acct `123456789012`, eu-central-1)

Three independent confirmations, then immediate teardown (~zero cost):

1. **Input schema** — `aws ecs create-express-gateway-service --generate-cli-skeleton`
   lists exactly: `executionRoleArn, infrastructureRoleArn, serviceName, cluster,
   healthCheckPath, primaryContainer{…}, taskRoleArn, networkConfiguration, cpu,
   memory, scalingTarget, tags`. **No** `cpuArchitecture` / `runtimePlatform` — not
   top-level, not nested in `primaryContainer`. Same for `update-…`.
2. **Live describe** — `describe-express-gateway-service` on the running
   `axiaops-api` prod service reports no architecture field at all (only
   `cpu`/`memory`).
3. **arm64-image probe** — created a throwaway Express service pointing at an
   arm64-only image (`arm64v8/nginx`). The task never started:

   ```
   CannotPullContainerError: pull image manifest has been retried 7 time(s):
   image Manifest does not contain descriptor matching platform 'linux/amd64'
   ```

   Fargate demanded `linux/amd64` and refused the arm64 manifest → Express places
   tasks on x86 and does not infer arch from the image. Probe + log group deleted
   immediately afterward.

## The unofficial hack — and why we decline it

A third-party walkthrough ([classmethod][2]) shows ARM *is* reachable by going
**underneath** the Express API to the normal-ECS primitives it auto-creates:

1. Create the Express service with a **dummy x86 image** (so it starts).
2. `aws ecs register-task-definition` a new revision of the auto-generated task def
   with `runtimePlatform.cpuArchitecture = "ARM64"` + the real arm64 image.
3. `aws ecs update-service --task-definition <new-rev> --force-new-deployment` to
   point the underlying service at it.

We confirmed the precondition is real — the Express service is a standard ECS
service (`deploymentController: ECS`) backed by an ordinary, editable task def
(`requiresCompatibilities: [FARGATE]`, `networkMode: awsvpc`,
`cpuArchitecture: X86_64`). We did **not** force-deploy it on prod (correctly
gated).

**Why we reject it for production:**

- **Self-reverting.** Express owns the task def through its service-revision /
  reconcile model. Any subsequent Express-level change (a normal CI image deploy via
  `update-express-gateway-service --primary-container`, an autoscaling event, a
  platform reconcile) regenerates the task def at the default **x86_64** — silently
  reverting the manual ARM override. The arm64 image then can't be pulled on x86 →
  `exec format error` crashloop in prod.
- **Unsupported & undocumented.** The classmethod author notes the same:
  *"Properties such as `runtimePlatform` and `capacityProviderStrategy` … do not
  exist in Express Mode templates."* There is no official path, so AWS can change
  the auto-managed-task-def behaviour from under us at any time.
- **The prize is tiny.** ~€46/yr (below). Babysitting a prod hack that fights its
  own control plane is not worth €4/mo.

The supported way to get first-class ARM is to drop Express for **normal ECS** (you
author the task def, `runtimePlatform.cpuArchitecture` is first-class) — which is the
"rebuild off Express" cost the original doc already weighed and rejected at this
savings level. See the comparison table below.

## Component readiness — app layer is ready, the deploy target is not

| Component | Current arch | Graviton-ready? | Blocker |
|---|---|---|---|
| Go services (api, ingestion, migrate) | x86 | ✅ | none — no cgo, no arch pins, pure-Go pgx, argon2 ships ARM64 asm |
| `services/shared` | x86 | ✅ | none |
| Dashboard | x86 / serverless | ✅ | none — prod dashboard is S3+CloudFront (arch-free) |
| **RDS PostgreSQL** (`db.t4g.micro`) | **ARM (Graviton2)** | ✅ already done | none |
| CI build pipeline | x86 runner | ⚠️ partial | needs multi-arch manifests (`buildx --platform linux/amd64,linux/arm64`) |
| **ECS Express compute** | **x86 (forced)** | ❌ **blocked** | **no supported arch selector — verified 2026-06-06** |

The application layer presents zero engineering blocker. **The blocker is entirely
the deploy target**: ECS Express won't run ARM through any supported seam.

## The savings math (still small)

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

The ALB (~$20/mo), RDS, CloudFront, S3, and data transfer are **unchanged** by arch.
The DB being already-Graviton doesn't increase the prize — it confirms the prize is
**only** the compute line. The 20% scales with task size (~$33/yr **per task** at
1 vCPU / 2 GB), so the case strengthens if workloads grow.

## ECS Express vs normal ECS (for context)

| | ECS Express | Normal ECS |
|---|---|---|
| Task definition | Auto-managed by AWS (you can't durably override arch) | You author it |
| CPU architecture | **x86 only via supported path** (no arch input) | ARM64 / x86 / Spot — first-class |
| Load balancer | ALB auto-created (shared ≤25 svc) | You provision |
| Deploy safety | Canary + rollback **built-in** | You wire it (e.g. CodeDeploy) |
| Setup effort | Minutes | Substantial IaC |
| Best for | Simple stateless web services | Anything needing Spot / multi-container / ARM / low-level control |

We're on Express because api + ingestion are two simple stateless web services and
Express gives the ALB + canary deploys + autoscaling for near-zero config. The
trade-off is no architecture control — so prod stays x86 until either Express adds
official ARM support or a workload justifies the move to normal ECS (at which point
ARM comes free).

## When to revisit

- **Soon / opportunistically:** only the recheck above — watch for an official
  `runtimePlatform` field on Express. The multi-arch CI change is reasonable hygiene
  on its own (future-proofs any ARM target), but on its own it banks nothing while
  Express can't consume an arm64 image.
- **Definitely** when prod task sizes grow materially (e.g. an ingestion worker pool
  at 1 vCPU / 2 GB): the 20% scales (~$33/yr per task) **and** you'll want
  Spot/multi-container/custom scaling Express can't give — so a normal-ECS migration
  pays for itself on multiple axes and Graviton comes free.

## History

- **June 2026 (original):** Declined on the premise "ECS Express is x86-only" — no
  arch input found on `create-express-gateway-service`.
- **June 2026 (rewrite, later reverted):** premise flipped to "Express supports
  ARM/Graviton" on the strength of a third-party blog; re-classified to *Viable*.
- **2026-06-06 (this revision):** the rewrite's claim was **verified against the live
  AWS API and disproven** (API schema has no arch field; Fargate refuses non-amd64
  manifests — `CannotPullContainerError`). Restored to **Declined — x86-only**, now
  with empirical evidence. Documented the unofficial task-def-override hack and why
  it's unsafe for prod (Express reconcile reverts it). Added the official-support
  recheck trigger.

## References

- AWS docs: [ECS task definitions for 64-bit ARM workloads](https://docs.aws.amazon.com/AmazonECS/latest/developerguide/ecs-arm64.html),
  [Resources created by ECS Express Mode services](https://docs.aws.amazon.com/AmazonECS/latest/developerguide/express-service-work.html).
- [classmethod — ECS Express Mode ARM64 / Fargate Spot / exec walkthrough][2] — the
  source of the unofficial hack; explicitly notes `runtimePlatform` is absent from
  Express templates.

[2]: https://dev.classmethod.jp/en/articles/ecs-express-mode-arm64-fargate-spot-exec/
