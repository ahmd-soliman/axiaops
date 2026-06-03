# Graviton / ARM64 for Prod Compute — Why Not Now

**Status:** Declined | **Last Updated:** June 2026 | **Revisit when:** prod task sizes grow materially (see trigger below)

## Overview

We evaluated migrating AxiaOps's own production compute (the always-on Fargate
tasks) from x86_64 to ARM64 (AWS Graviton) to cut the Fargate bill. The
conclusion: the saving is real but trivial (~€46/yr) at the current footprint,
and **every** path to it costs more than the prize in either operational risk or
engineering effort. Declined for now; the revisit trigger is documented below so
this isn't re-litigated cold.

This is a FinOps-for-AxiaOps decision — see the Cost Awareness section in the
root `CLAUDE.md`.

---

## Current prod footprint (live, eu-central-1)

| Service | CPU / Mem | Arch | Notes |
|---|---|---|---|
| `axiaops-api` | 0.25 vCPU / 0.5 GB | x86_64 | always-on Fargate |
| `axiaops-ingestion` | 0.25 vCPU / 0.5 GB | x86_64 | always-on Fargate |
| ALB `ecs-express-gateway-alb-*` | — | — | one **shared** ALB (Express serves ≤25 services per ALB) |

Managed by **ECS Express** (`ExpressGatewayService`), CANARY deploy strategy
(`canaryPercent: 5`, `bakeTime: 3min`) with a rollback alarm + deployment
circuit-breaker.

## The blocker: ECS Express is x86-only

ECS Express is a high-level managed abstraction — it auto-generates and owns the
task definition, ALB, autoscaling, and deploy strategy. It does **not** expose
the low-level knob needed for ARM:

- `create-express-gateway-service` / `update-express-gateway-service` have **no
  `--runtime-platform` / `--cpu-architecture` input**. Confirmed against the live
  API schema (zero matches for `arch|arm|x86|runtimeplatform` in the command help)
  and against AWS docs — Express is hardcoded to x86_64.
- The same restriction blocks Fargate Spot and ECS Exec at creation.

There is an **undocumented two-stage hack** (deploy an x86 dummy with
`MinTaskCount: 0`, then hand-mutate the auto-generated task def to `ARM64` via CLI
and `update-service`) — but AWS community guidance explicitly scopes it to
dev/test: Express stack reconciles can revert the arch back to x86, which on a
running ARM task is an exec-format crash. Not acceptable on the prod path.

## The savings math

Fargate on-demand, eu-central-1; ARM64 is ~20% cheaper per vCPU-hr and GB-hr.

| | per vCPU-hr | per GB-hr |
|---|---|---|
| x86_64 | $0.04656 | $0.00511 |
| ARM64 (Graviton) | $0.03725 | $0.00409 |

Per task (0.25 vCPU + 0.5 GB) × 730 hr/mo, both tasks:

| | Monthly | Annual |
|---|---|---|
| x86 (today) | **$20.72** | $248.6 |
| ARM (Graviton) | **$16.58** | $199.0 |
| **Saving** | **$4.14 / mo** | **~$49.6 / yr (~€46)** |

The ALB (~$20/mo), RDS, CloudFront, S3, and data transfer are **unchanged** by
arch, so they never enter the delta. The saving is purely ~20% of the compute
line — and compute is only part of the ~€24–34/mo total, so it's ~12–15% of the
whole bill.

## Why both ARM paths lose

| Path | Saving | Cost to get it | Verdict |
|---|---|---|---|
| **ARM on Express** (two-stage hack) | ~€46/yr | Undocumented, fights the managed abstraction; reconcile reverts to x86 → exec-format crash in **prod** | Reject — fragile prod for €4/mo |
| **Drop Express → normal ECS** for first-class ARM | ~€46/yr | Rebuild ALB wiring + service + autoscaling + the canary/rollback/circuit-breaker machinery Express gives free, all in `aws-infra`; permanent maintenance | Reject — large effort + lost deploy-safety for €46/yr |
| **Stay on Express + x86** | — | none | **Accept** — already the cheapest sane shape |

The dollar saving is *identical* for the first two paths (only the compute rate
changes); they differ only in what you pay in risk vs engineering. Neither clears
€46/yr.

## ECS Express vs normal ECS (for context)

| | ECS Express | Normal ECS |
|---|---|---|
| Task definition | Auto-managed by AWS | You author it |
| CPU architecture | **x86-only** (ARM via fragile hack) | **ARM64 / x86 / Spot — first-class** |
| Load balancer | ALB auto-created & mandatory (shared ≤25 svc) | You provision |
| Deploy safety | Canary + rollback **built-in** | You wire it (e.g. CodeDeploy) |
| Autoscaling | Managed defaults | You configure |
| Setup effort | Minutes | Substantial IaC |
| Best for | Simple stateless web services | Anything needing ARM/Spot/multi-container/low-level control |

We're on Express precisely because api + ingestion are two simple stateless web
services, and Express gave us the ALB + canary deploys + autoscaling for near-zero
config. The x86 lock-in is the price of that convenience.

## Revisit trigger

Re-open this decision when a workload forces prod task sizes up **materially** —
e.g. an ingestion worker pool at 1 vCPU / 2 GB. At that point:

1. The 20% Graviton saving scales with vCPU/GB (~$33/yr **per task** at 1/2), and
2. You'll likely want low-level control (Fargate Spot, multi-container, custom
   scaling) that Express can't provide **anyway**.

So a migration to normal ECS pays for itself on multiple axes at once — and
Graviton comes along for free as part of authoring your own task definitions.
Until then: stay on Express + x86.

## How this was verified (June 2026)

- Live ECS task defs inspected via the `axiaops-prod-admin` SSO profile
  (`runtimePlatform.cpuArchitecture: X86_64`, `requiresCompatibilities: FARGATE`).
- `create-express-gateway-service` API schema — no arch input.
- AWS docs: [ECS task definitions for 64-bit ARM workloads](https://docs.aws.amazon.com/AmazonECS/latest/developerguide/ecs-arm64.html),
  [Resources created by ECS Express Mode services](https://docs.aws.amazon.com/AmazonECS/latest/developerguide/express-service-work.html).
- ECS Express ARM hack writeup:
  [DevelopersIO — unsupported ARM64/Spot/Exec in Express Mode](https://dev.classmethod.jp/en/articles/ecs-express-mode-arm64-fargate-spot-exec/).
