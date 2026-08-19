# Go-Live Checklist — AxiaOps

> What must be complete before the first paying customer is billed.
> Derived from `development_plan.md`.
> Target: first invoice **October 2026**.

---

## Hard Blockers — Cannot Launch Without These

### Infrastructure

| Item | Dev Plan Ref | Target |
|------|-------------|--------|
| Scheduled auto-scan (24h default per account) | 2.11 | June 2026 ✅ |
| `cost_records` 90-day retention cleanup job | 2.12 | June 2026 ✅ |
| RDS automated backups + Secrets Manager for `ENCRYPTION_KEY` | 2.13 | June 2026 ✅ |
| Redis — JWKS cache, scan job queue, rate limiting | 2.14 | July 2026 ✅ |
| Production deployment — ECS Express + RDS + Secrets Manager via Terraform (aws-infra) | 2.16 | ✅ live June 2026 |
| GitLab CI full pipeline — test → build → deploy to ECR + ECS Express | 2.10 | May 2026 ✅ |

### Product

| Item | Dev Plan Ref | Target |
|------|-------------|--------|
| Stripe billing — 3 tiers (Starter €49, Growth €149, Team €399) | 3.1 | September 2026 |
| Dismiss zombie workflow + snooze | 3.2 | ✅ shipped June 2026 |
| GDPR / right to erasure + data export | 3.10 | ✅ shipped June 2026 |
| Privacy policy + terms of service pages | 3.10 | September 2026 |

### Legal

| Item | Dev Plan Ref | Target |
|------|-------------|--------|
| Found Holding GmbH | 3.12 | August 2026 |
| Found Operating UG + IP assignment agreement | 3.12 | August 2026 |
| Business bank account (Qonto or Fyrst) | 3.12 | August 2026 |
| VAT registration (Umsatzsteuer-ID) | 3.12 | August 2026 |
| Engage Steuerberater | 3.12 | August 2026 |

> No beta customer should be charged before all legal steps above are complete.

---

## Strong Recommendations — Ship Before or Shortly After Launch

| Item | Why | Dev Plan Ref |
|------|-----|-------------|
| Weekly email digest + Slack alerts | Core retention driver; users need to know when new zombies appear | 2.15 ✅ (scan digests shipped June 2026) |
| Scan history log | Customers need to see when scans ran and what changed | 3.5 |
| Per-account summary (`GET /summary?account_id`) | MSP use case — per-client savings breakdown | 3.8 |
| CSV export | Required for Pro tier; common first request from paying customers | 3.7 |
| Tag/team filtering | Needed for teams with multiple services/environments | 3.6 |
| Demo mode (pre-loaded mock data) | Biggest activation blocker is hesitation to connect a live account | business_plan |

---

## What's Already Done (Phase 2 shipped ahead of schedule)

- AWS Cost Explorer + CloudWatch integration ✅
- Native cookie sessions (argon2id) + per-org OIDC SSO + multi-tenancy (RLS) ✅
- Account management — connect AWS, encrypted secrets, on-demand scan ✅
- Resource inventory view (`GET /resources`) ✅
- Savings history / trend (`zombie_snapshots` + `GET /trend`) ✅
- Observability — structured logging (`slog`), Prometheus metrics ✅
- Scan recovery — stuck scan timeout detection ✅
- API versioning — `/v1/` prefix on all endpoints ✅
- In-memory rate limiting — per-organization token bucket ✅
- Graceful shutdown — SIGTERM handling ✅
- GitLab CI pipeline — test + build stages ✅
- Production deployment — ECS Express + RDS, eu-central-1 ✅
- Email + Slack scan digests (notification channels) ✅
- Dismiss + snooze workflow with audit trail ✅
- GDPR right-to-erasure + data export ✅

---

## Competitive Context

The core "idle resource detection" is free from AWS Trusted Advisor. AxiaOps's
moat must be the **workflow layer** — not detection alone.

The features that turn this from a dashboard into a product people pay for:

1. **Dismiss workflow + audit trail** (✅ shipped June 2026) — without this, every scan resurfaces the same known-intentional resources.
2. **Stripe billing** — obvious, but the tier structure (Starter/Growth/Team) must enforce limits (account count, scan frequency) from day one.
3. **MSP-native multi-client view** — no direct competitor has a proper reseller tier. This is the highest-value differentiator to build toward.
4. **EU-first GDPR compliance** — meaningful for European MSPs; German incorporation signals regulatory seriousness.

The full lifecycle vision (pre-deployment simulation + CI/CD budget gate) is the
long-term moat but is Phase 5 (2027+) — it doesn't block launch.

---

## Related

- [development_plan.md](development_plan.md) — full phase-by-phase breakdown
- [production.md](production.md) — infrastructure setup for production deployment
- [deployment.md](deployment.md) — deployment environments, cost estimates by phase
