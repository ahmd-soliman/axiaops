# AxiaOps User Stories — April 2026 Status

## Overview

This document tracks user stories and completion status across all phases. Stories are managed via GitLab issues using two scripts:

- **`scripts/update_user_stories.py`** — Closes completed GitLab issues with a "done" label + completion comment
- **`scripts/create_user_stories.py`** — Creates GitLab issues for remaining stories

The dashboard is a **Vite + React web app** (not React Native — see `feedback_no_react_native.md`). Mobile apps are planned for Phase 4 as a separate effort.

---

## Completed Stories ✅

Total: **40 stories completed** as of April 26, 2026

### Phase 1 — Incubator / MVP (April 2026)

| Story | Status | Phase |
|-------|--------|-------|
| Cost fixture data | ✅ Done | 1.1 |
| Usage fixture data | ✅ Done | 1.2 |
| Go backend services | ✅ Done | 1.3 |
| Unit test coverage | ✅ Done | 1.5 |
| Dashboard (Vite + React) | ✅ Done | 1.6 |
| Docker Compose infrastructure | ✅ Done | 1.7 |
| PostgreSQL schema + migrations | ✅ Done | 1.8 |
| Dev environment & fixtures | ✅ Done | 1.9 |

### Phase 2 — Alpha (Shipped April 2026)

| Story | Status | Phase |
|-------|--------|-------|
| AWS Cost Explorer integration | ✅ Done | 2.1 |
| CloudWatch usage metrics | ✅ Done | 2.1 |
| Account management (connect / scan / delete) | ✅ Done | 2.2 |
| Kinde auth + login | ✅ Done | 2.3 |
| Tenant isolation (RLS) | ✅ Done | 2.3 |
| PostgreSQL with versioned migrations | ✅ Done | 2.4 |
| Savings history / trend snapshots | ✅ Done | 2.5 |
| Structured logging + Prometheus metrics | ✅ Done | 2.6 |
| Stuck-scan recovery | ✅ Done | 2.7 |
| API versioning (`/v1/`) | ✅ Done | 2.8 |
| Rate limiting (token bucket + Redis) | ✅ Done | 2.9 |
| Graceful shutdown (SIGTERM drain) | ✅ Done | 2.10 |
| GitLab CI pipeline | ✅ Done | 2.11 |
| Scheduled auto-scan | ✅ Done | 2.12 |
| `cost_records` 90-day retention | ✅ Done | 2.13 |
| Redis (cache, queue, rate-limit) | ✅ Done | 2.14 |
| Tier 1 detections (EBS, snapshots, AMIs, EIPs, stopped EC2) | ✅ Done | 2.Tier1 |
| Tier 2 detections (ElastiCache, OpenSearch, Redshift, SageMaker, DynamoDB, EKS, CloudFront, Kinesis, S3, log groups, RDS snapshots, ECR, Secrets Manager) | ✅ Done | 2.Tier2 / 2.Tier3 |
| Raw cost view (`GET /v1/costs` + `CostAnalyticsScreen`) | ✅ Done | 2.#4 |
| Unified CSV export | ✅ Done | 2.#2 |
| Scan-completion polling | ✅ Done | 2.UX |
| Pricing rates in YAML config | ✅ Done | 2.Pricing |
| ghost → zombie terminology rename | ✅ Done | 2.#7 |
| CE anomaly-monitor "ghost" detection removed | ✅ Done | 2.#8 |

### Phase 3 — Beta / GTM (Shipped Early)

| Story | Status | Phase |
|-------|--------|-------|
| Resource inventory view | ✅ Done | 3.4 |
| Dismiss / snooze workflow | ✅ Done | 3.2 |
| Copy-paste remediation CLI commands | ✅ Done | 3.#2 |
| Audit log + audit screen | ✅ Done | 3.#5 |
| Memberships + roles (owner/admin/member/viewer) | ✅ Done | 3.#8 |
| GDPR right-to-erasure (`DELETE /v1/users/me` + `/v1/tenants/me`) | ✅ Done | 3.#9 |
| GDPR data export (`GET /v1/export`) | ✅ Done | 3.#9 |
| Trend chart UI | ✅ Done | 3.#4 |

---

## Remaining Stories 📋

Total: **~30 stories planned** for May 2026 → Q4 2027

### Phase 2 — Alpha (Remaining)

| Story | Due | Phase | Labels |
|-------|-----|-------|--------|
| Backup and disaster recovery | Aug 2026 | 2.16 | infra, reliability |
| Production deployment (ECS Express + RDS + Secrets Manager via aws-infra) | Aug 2026 | 2.16 | infra, deployment |
| Weekly email digest (Resend) | Aug 2026 | 2.15 | backend, alerting |
| Slack webhook alerts per account | Aug 2026 | 2.#6 | backend, alerting |
| tenant → organization rename | Aug 2026 | 2.#9 | backend, frontend, database |

### Phase 3 — Beta / GTM (September–December 2026)

| Story | Due | Phase | Labels |
|-------|-----|-------|--------|
| Stripe billing (Free / Starter / Growth / Team) | Sep 2026 | 3.1 | backend, frontend, billing |
| Tag / team filtering | Oct 2026 | 3.#3 | backend, frontend |
| Per-account scan history log | Oct 2026 | 3.6 | backend, frontend, reporting |
| Cost forecast (linear projection over snapshots) | Nov 2026 | 3.7 | backend, frontend, reporting |
| GDPR paperwork (privacy / ToS / DPA / DPIA / RoPA / breach runbook) | Sep–Oct 2026 | 3.#9 | compliance |
| Custom per-tenant detection thresholds | Nov 2026 | 3.11 | backend, frontend |
| Custodian rule backlog (~13 rules) | Nov 2026 | 3.10 | backend |
| CUR ingestion (actual-cost mode) | Q4 2026 | 3.13 | backend |
| Live AWS Pricing API for rate refresh | Q4 2026 | 3.12 | backend |
| Multi-organization UX (org switcher + invite-by-email) | Nov 2026 | 3.14 | backend, frontend |
| Org-level dashboard (7 widgets) | Nov 2026 | 3.15 | backend, frontend, reporting |
| Historical zombie lineage (`zombie_history` table) | Dec 2026 | 3.16 | backend, frontend |
| Per-service DB users (security hardening) | Dec 2026 | 3.14 | backend, database |
| Migration history log (checksum drift detection) | Dec 2026 | 3.15 | backend, database |
| SOC 2 Type I → Type II program | Q4 2026 → Q4 2027 | 3.17 | compliance |
| PDF savings report | Nov 2026 | 3.13 | backend, frontend, reporting |

### Phase 4 — Multi-cloud + Mobile + FOCUS (Q1–Q2 2027)

| Story | Due | Phase | Labels |
|-------|-----|-------|--------|
| Azure cost data | Q1 2027 | 4.2 | backend, frontend, azure |
| GCP cost data (BigQuery billing export) | Q2 2027 | 4.3 | backend, frontend |
| FOCUS Consumer role (`focusfile` provider) | Q2 2027 | 4.4 | backend |
| FOCUS Producer role (`GET /v1/export/focus`) | Q3 2027 | 4.4 | backend |
| FOCUS Foundation conformance assertion | Q4 2027 | 4.4 | compliance |
| iOS + Android mobile app | Q2 2027 | 4.5 | frontend, mobile |

### Phase 5 — Proactive Cost Simulation (Q3–Q4 2027)

| Story | Due | Phase | Labels |
|-------|-----|-------|--------|
| Terraform / CDK plan parser | Q3 2027 | 5.1 | backend, iac |
| Cost estimation engine (live Pricing APIs) | Q3 2027 | 5.2 | backend, iac |
| What-if scenarios (gp3↔gp2, region change, Spot) | Q4 2027 | 5.3 | backend |
| CI/CD budget gate (MR comment) | Q4 2027 | 5.4 | backend, cicd |
| `axiaops` CLI via Homebrew | Q4 2027 | 5.5 | backend, cli |

### Tracked in the roadmap only (not user stories)

- CI runner containerization (pure infra refactor)
- Operating entity registration / VAT (legal)

---

## How to Update Stories

### To Mark a Story as Done

1. Add an entry to `COMPLETED_STORIES` in `scripts/update_user_stories.py`
2. Include: title, phase, and completion note
3. Run:
   ```bash
   export GITLAB_TOKEN="<your-personal-access-token>"
   python3 scripts/update_user_stories.py --dry-run   # preview
   python3 scripts/update_user_stories.py             # apply
   ```
4. This will:
   - Create a "done" label (if not exists)
   - Find the GitLab issue matching the title
   - Add the "done" label
   - Post your completion note as a comment
   - Close the issue

### To Create New Stories

1. Add entries to `STORIES` in `scripts/create_user_stories.py`
2. Include: title, description (acceptance criteria), labels
3. Run:
   ```bash
   export GITLAB_TOKEN="<your-personal-access-token>"
   python3 scripts/create_user_stories.py
   ```
4. This will:
   - Ensure all label colors exist
   - Create new GitLab issues for stories (skip if already exist)

### Labels Used

**Phases:** `phase-2`, `phase-3`, `phase-4`, `phase-5`

**Domains:** `backend`, `frontend`, `infra`, `mobile`, `cli`, `iac`

**Features:** `user-story`, `alerting`, `database`, `multi-tenant`, `observability`, `deployment`, `remediation`, `reporting`, `performance`, `reliability`, `automation`, `azure`, `cicd`, `billing`, `compliance`

---

## GitLab Integration

**Project:** `axiaops/axiaops` (https://gitlab.com/axiaops/axiaops)

**Token Required:** Personal Access Token with `api` scope
- Create at: GitLab → User Settings → Access Tokens

**Sample API Calls:**

```bash
# List all open issues
curl -H "PRIVATE-TOKEN: <token>" "https://gitlab.com/api/v4/projects/axiaops%2Faxiaops/issues?state=opened&per_page=100"

# Search for issue by title
curl -H "PRIVATE-TOKEN: <token>" "https://gitlab.com/api/v4/projects/axiaops%2Faxiaops/issues?search=AWS&state=opened"
```

---

## Workflow Checklist

When shipping a new feature:

- [ ] Update the roadmap tracker — move the item from 🔲 Remaining to ✅ Done with implementation notes
- [ ] Add story title + completion note to `COMPLETED_STORIES` in `scripts/update_user_stories.py`
- [ ] Run `python3 scripts/update_user_stories.py --dry-run` to preview, then without `--dry-run` to apply
- [ ] Verify the issue is closed in GitLab with "done" label + completion comment
- [ ] If adding new work, append to `STORIES` in `scripts/create_user_stories.py` and run to create issues

---

## Notes

- Completed stories use the user-story format: "As a [role], I want [action] so [benefit]"
- Non-user-story deliverables (fixtures, tests, infra) are also included with adapted titles
- Phase 1 was completed entirely in April 2026 (ahead of schedule)
- Phase 2 is largely shipped (24/29 stories); remaining 5 are deployment, backups, email, Slack, and the tenant→organization rename
- Phase 3 has shipped 8 stories ahead of schedule (resource inventory, dismiss/snooze, remediation, audit, memberships, GDPR endpoints, data export, trend UI); ~16 remain for Sep–Dec 2026
- Phase 5 (proactive cost simulation, CLI) replaces what used to be called "Phase 4 long-term" in earlier docs

---

*Last updated: April 26, 2026*
