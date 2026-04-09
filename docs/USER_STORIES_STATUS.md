# AxiaOps User Stories — April 2026 Status

## Overview

This document tracks user stories and completion status across all phases. Stories are managed via GitLab issues using two scripts:

- **`scripts/update_user_stories.py`** — Closes completed GitLab issues with a "done" label + completion comment
- **`scripts/create_user_stories.py`** — Creates GitLab issues for remaining stories

---

## Completed Stories (Phase 1 & Phase 2 Early) ✅

Total: **14 stories completed** as of April 2026

### Phase 1 — Incubator / MVP (April 2026)

| Story | Status | Phase |
|-------|--------|-------|
| Cost fixture data | ✅ Done | 1.1 |
| Usage fixture data | ✅ Done | 1.2 |
| Go backend services | ✅ Done | 1.3 |
| Unit test coverage | ✅ Done | 1.5 |
| React Native frontend | ✅ Done | 1.6 |
| Docker Compose infrastructure | ✅ Done | 1.7 |
| PostgreSQL schema + migrations | ✅ Done | 1.8 |
| Dev environment & fixtures | ✅ Done | 1.9 |

### Phase 2 — Alpha (Shipped Early in April 2026)

| Story | Status | Phase |
|-------|--------|-------|
| AWS Cost Explorer integration | ✅ Done | 2.1 |
| CloudWatch usage metrics | ✅ Done | 2.1 |
| Kinde auth + login | ✅ Done | 2.3 |
| Tenant isolation | ✅ Done | 2.3 |
| Account management (connect / scan / delete) | ✅ Done | 2.2 |
| Resource inventory view | ✅ Done | 3.4 |

---

## In-Progress Stories (Phase 2 Continued) 🔄

Total: **7 stories planned** for May–August 2026

### Phase 2 — Alpha (Remaining Work)

| Story | Due | Phase | Labels |
|-------|-----|-------|--------|
| Structured logging & observability | May 2026 | 2.5 | backend, observability |
| PostgreSQL migration (production-grade) | May 2026 | 2.4 | backend, database |
| Scheduled automatic scans | June 2026 | 2.6 | backend, automation |
| Savings history & trend charts | June 2026 | 2.7 | frontend, backend, reporting |
| Redis caching & queue | July 2026 | 2.9 | backend, performance |
| Backup & disaster recovery | June 2026 | 2.8 | infra, reliability |
| Production deployment (App Runner + RDS) | August 2026 | 2.11 | infra, deployment |

---

## Upcoming Stories (Phase 3 & Beyond) 📋

### Phase 3 — Beta / GTM (September–December 2026)

Stories include:
- Pricing & billing (Stripe integration)
- Dismiss ghost workflow + snooze
- Remediation CLI commands + audit trail
- Scan history log + per-account summary
- Tag/team filtering + CSV export
- Expanded detection rules + configurable thresholds
- User management + roles (admin/viewer)
- PDF savings report
- One-click remediation suggestions
- Azure cost data integration

### Phase 4 — Scale & Expand (Q1–Q2 2027)

Stories include:
- Cost forecasting (linear regression)
- Multi-cloud (Azure, GCP)
- Mobile app (iOS + Android)
- Terraform plan parser
- Cost estimation engine
- CI/CD budget gate

---

## How to Update Stories

### To Mark a Story as Done

1. Add an entry to `COMPLETED_STORIES` in `scripts/update_user_stories.py`
2. Include: title, phase, and completion note
3. Run:
   ```bash
   export GITLAB_TOKEN="<your-personal-access-token>"
   python3 scripts/update_user_stories.py
   ```
4. This will:
   - Create a "done" label (if not exists)
   - Find the GitLab issue matching the title
   - Add the "done" label
   - Post your completion note as a comment
   - Close the issue

### To Create New Stories

1. Add entries to `STORIES` in `scripts/create_user_stories.py`
2. Include: title, description (acceptance criteria), labels, milestone
3. Run:
   ```bash
   export GITLAB_TOKEN="<your-personal-access-token>"
   python3 scripts/create_user_stories.py
   ```
4. This will:
   - Ensure all label colors exist
   - Create new GitLab issues for stories (skip if already exist)

### Labels Used

**Phases:**
- `phase-2`, `phase-3`, `phase-4`

**Domains:**
- `backend`, `frontend`, `infra`, `mobile`, `cli`, `iac`

**Features:**
- `user-story`, `alerting`, `database`, `multi-tenant`, `observability`, `deployment`, `remediation`, `reporting`, `performance`, `reliability`, `automation`, `azure`, `cicd`

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

- [ ] Update `development_plan.md` with completion status (mark ✅ Done)
- [ ] Add story title + completion note to `COMPLETED_STORIES` in `update_user_stories.py`
- [ ] Run `python3 scripts/update_user_stories.py` to close the GitLab issue
- [ ] Verify the issue is closed with "done" label + completion comment
- [ ] If adding new work, add to `STORIES` in `create_user_stories.py` and run to create issues

---

## Notes

- Completed stories use user story format: "As a [role], I want [action] so [benefit]"
- Non-user-story deliverables (fixtures, tests, infra) are also included with adapted titles
- Phase 1 was completed entirely in April 2026 (ahead of schedule)
- Phase 2 is 6/13 features done; remaining work planned for May–August
- Phase 3 gates go-live; Phase 4 is long-term roadmap

---

*Last updated: April 9, 2026*
