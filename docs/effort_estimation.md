# Effort Estimation — AxiaOps

## Scope

Single developer, full-time (~6–7 productive coding hours/day).

---

## Codebase Snapshot (as of April 2026)

| Metric | Value |
|--------|-------|
| Go source files | 39 (5,502 LOC) |
| JS frontend files | 10 (1,199 LOC) |
| API endpoints | 12 |
| DB tables | 6 (with RLS) |
| Test files | 9 |
| AWS services integrated | 5 |

---

## Estimate by Phase

| Phase | Scope | Without AI | With AI |
|-------|-------|------------|---------|
| **Phase 1+2 done** | Go services, AWS integration, native auth (replaced Kinde), account management, Vite + React web dashboard, Docker Compose, unit tests | 30–40 days | 9–13 days |
| **Phase 2 remaining** | PostgreSQL + RLS migration, email alerts (Resend), App Runner + RDS deployment | 5–8 days | 2–3 days |
| **Phase 3** | Remediation workflow + audit trail, multi-cloud Azure + GCP, PDF/CSV reports, iOS/Android + App Store | 28–40 days | 9–13 days |
| **Phase 4** | IaC plan parser (Terraform/CDK), cost estimation engine (3 cloud pricing APIs), what-if scenarios, CI/CD budget gate, CLI tool (`brew install`) | 30–46 days | 11–16 days |

---

## Totals

| | Without AI | With AI |
|--|------------|---------|
| **Already done** | 30–40 days | 9–13 days |
| **Remaining** | 63–94 days | 22–32 days |
| **Full project** | **93–134 days** (~5–7 months) | **31–45 days** (~6–9 weeks) |

AI productivity multiplier: ~3x

---

## Notes

- **Phase 3 multi-cloud** is the heaviest remaining chunk — Azure and GCP have completely different billing APIs, roughly 2–3x harder than AWS
- **Phase 4 cost estimation engine** requires integrating 3 separate pricing APIs + parsing Terraform plan JSON
- **App Store submission** is unpredictable — Apple review alone can add days of back-and-forth
- **With a second developer**, remaining work compresses by ~40–50% since Phase 3 and 4 have largely independent tracks (e.g. multi-cloud vs IaC parser)
- The with-AI estimate assumes effective AI-assisted coding workflow is already in place
