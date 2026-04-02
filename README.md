# Axia

A FinOps SaaS tool that detects idle and zombie cloud resources still incurring costs despite zero usage — and surfaces an actionable remediation workflow with a full audit trail.

> **Know the value of every resource.**

---

## What It Does

AxiaOps connects to your cloud billing via read-only IAM access and delivers:

- **The Ghost Number** — total monthly spend on idle resources across all connected accounts
- **The Ghost List** — itemized breakdown by resource with age, cost/day, and remediation suggestion
- **The Remediation Workflow** — approve, delegate, or dismiss each ghost with a full audit trail
- **The Weekly Digest** — email/Slack alert when new ghosts appear
- **Multi-account Dashboard** — single pane for managing multiple cloud accounts

---

## Target Users

- DevOps engineers and CTOs managing cloud costs without dedicated FinOps tooling
- MSPs managing cloud spend across multiple client accounts
- FinOps consultants needing client-facing savings reports

---

## Tech Stack

| Layer | Technology |
|-------|-----------|
| Backend | Go 1.22+ |
| Database | SQLite (MVP) → PostgreSQL |
| Frontend | Web app (React / Next.js) |
| Auth | Clerk or Supabase Auth |
| Hosting | Fly.io / Railway |
| CI/CD | GitHub Actions |
| Cloud APIs | AWS CUR, Cost Explorer, Azure Cost Mgmt, GCP Billing |

---

## Roadmap

### Phase 1 — MVP (April – June 2026)
- Mock data generator (fake enterprise billing CSV)
- Go worker: idle resource detection logic
- Web dashboard: savings summary + ghost list

### Phase 2 — Alpha (July – September 2026)
- AWS CUR / Cost Explorer integration
- Auth + multi-tenancy
- Alerting (email + Slack)

### Phase 3 — Beta / Launch (October – December 2026)
- App Store / Play Store (mobile companion for alerts + approvals)
- Azure and GCP support
- MSP multi-client dashboard
- PDF savings reports

---

## Documentation

| File | Description |
|------|-------------|
| [docs/development_plan.md](docs/development_plan.md) | Full technical development plan, milestones, and stack decisions |
| [docs/business_plan.md](docs/business_plan.md) | Business model, market analysis, pricing, and GTM strategy |
| [docs/tax_strategy.md](docs/tax_strategy.md) | German tax structure (Holding GmbH + UG), VAT, exit planning |
| [docs/suggestions.md](docs/suggestions.md) | Strategic recommendations on platform, moat, and go-to-market |
| [docs/names_final.md](docs/names_final.md) | Final name shortlist with rationale |

---

## Legal

AxiaOps is developed under a clean room protocol:
- All development on personal hardware only
- Code owned by the Operating UG via IP assignment agreement
- No employer resources, data, or infrastructure used

---

## Status

**Incubator phase — April 2026.** MVP in active development.
