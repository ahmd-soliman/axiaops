# GDPR Compliance Plan — AxiaOps

_Last updated: 2026-04-25_

> **Purpose:** Operational plan to bring AxiaOps to GDPR compliance before the
> first paying EU customer (target: October 2026). Expands the Phase 3
> #9 / §3.10 roadmap item into a concrete, deliverable-by-deliverable plan with owners,
> dependencies, and acceptance criteria.
>
> This document is the source of truth. The task trackers point at it.

---

## 1. Scope and Roles

### 1.1 What GDPR applies to

AxiaOps processes two distinct categories of data:

| Category | Examples | GDPR role |
|---|---|---|
| **Customer (organization) account data** | Email, name, SSO subject identifier (`sso_external_id`), org code, billing details, audit log entries | We are the **data controller** for AxiaOps's own customer relationship |
| **Customer cloud telemetry** | AWS account IDs, ARNs, resource IDs, tags (which may contain employee names/emails), Cost Explorer line items | We are the **data processor** acting on the customer's instructions |

This dual role drives most of the work below. Anything that touches *organization
employee/operator identifiers* falls under controller obligations; anything
that touches *organization cloud data* falls under processor obligations and a Data
Processing Agreement (DPA).

### 1.2 Out of scope (explicitly)

- We do not process special-category data (Art. 9). If an organization's AWS tags
  contain such data we treat it the same as ordinary identifiers — but our
  ToS prohibits putting it there.
- We do not target individuals or data subjects directly. Our customers are
  organisations.
- Children's data: not applicable.

### 1.3 Internal accountability

| Role | Holder | Responsibilities |
|---|---|---|
| Data Protection Officer (DPO) | Not appointed (≤ 250 employees, no large-scale special data — Art. 37 not triggered). Founder acts as **privacy lead** | Owns this document, DSR responses, breach notifications |
| EU representative | Not required (controller established in EU — Holding GmbH / Operating UG, Germany) | — |
| Records of Processing (Art. 30) | Privacy lead | Maintain `docs/compliance/ropa.md` (deliverable in this plan) |
| Sub-processor management | Privacy lead | Maintain public list at `axiaops.io/sub-processors` |

If headcount crosses ~10 employees or we add health-sector customers, revisit
the DPO decision.

---

## 2. Data Inventory

### 2.1 Personal data we hold (controller mode)

Source: `services/shared/storage/postgres/migrations/`. This list must be
rechecked any time a migration adds a column.

| Table | Field | Purpose | Retention |
|---|---|---|---|
| `organizations` | `id`, `kinde_org_code`, `name` | Organization identity | Until organization deletion + 30 days |
| `users` | `id`, `kinde_sub`, `email`, `last_seen` | Authentication & audit | Until organization deletion or user removal + 30 days |
| `accounts` | `label`, `region`, `secret_encrypted` | Customer-supplied AWS creds (label may include user names) | Until organization deletes the account |
| `audit_log` (planned, 3.3) | `user_id`, `action`, `resource_id`, `created_at` | Security & compliance audit trail | 12 months minimum, 7 years for billing-related events |
| `dismissed_zombies` | `dismissed_by` (user_id), `note` | Workflow audit | Until organization deletion |

### 2.2 Cloud telemetry we hold (processor mode)

| Table | Field | Notes |
|---|---|---|
| `cost_records` | `resource_id`, `tags` (JSONB) | Tags may contain emails / names if the customer puts them there |
| `resource_records` | `resource_id`, `arn`, `tags` | Same |
| `zombie_records` | `resource_id`, `reason`, `tags` | Same |
| `zombie_snapshots` / `_services` | aggregate counts, no raw resource IDs after rollup | — |

### 2.3 Data we deliberately do **not** keep

- AWS access logs from inside the customer's account (we don't fetch them).
- CloudTrail data.
- Anything outside the read-only IAM policy documented in `docs/production.md`.

### 2.4 Where data lives

- **Production data:** AWS RDS PostgreSQL, region `eu-central-1` (Frankfurt).
- **Backups:** RDS automated snapshots, same region, 7-day retention.
- **Logs:** CloudWatch Logs, 7-day retention (`docs/production.md`).
- **Metrics:** Prometheus scrape → no PII (only counters / gauges).
- **Email:** Resend (sub-processor) — see §6.

No production data leaves `eu-central-1`. CI/CD test data is synthetic.

---

## 3. Lawful Basis & Notices

### 3.1 Lawful basis matrix

| Processing activity | Basis (Art. 6) |
|---|---|
| Authenticate organization users | Contract performance — Art. 6(1)(b) |
| Process cloud telemetry to detect zombies | Contract performance — same |
| Send transactional product emails (digest, scan failed) | Legitimate interest — Art. 6(1)(f), opt-out provided |
| Send marketing emails | Consent — Art. 6(1)(a), explicit opt-in only |
| Audit logging | Legitimate interest — Art. 6(1)(f), security obligation |
| Billing records (invoices, Stripe) | Legal obligation — Art. 6(1)(c), §147 AO 10-year retention |

### 3.2 Required public-facing documents

- [ ] **Privacy Policy** — `docs/legal/privacy.md` rendered at `axiaops.io/privacy`. Sections per Art. 13/14: identity & contact, purposes & basis, recipients/sub-processors, retention, rights (incl. complaint to BfDI / state DPA), automated decision-making (none).
- [ ] **Terms of Service** — `docs/legal/terms.md` at `axiaops.io/terms`.
- [ ] **Data Processing Agreement (DPA)** — template at `axiaops.io/dpa`. Aligned with Art. 28(3); customer signs by checking a box at signup or by countersigned PDF for Team/MSP plans. Include sub-processor list by reference.
- [ ] **Sub-processors page** — `axiaops.io/sub-processors`, machine-readable list, with 30-day notice mechanism for additions/removals (email opt-in).
- [ ] **Cookie notice** — minimal: only first-party functional cookies on the dashboard (auth token storage). No tracking analytics in v1. If we add Plausible later it's cookieless and stays out of consent flow.

Single PR target: `legal/initial-policies` — all four documents land together.

---

## 4. Data Subject Rights (Art. 15–22)

The product surface for these rights is mostly already sketched in §3.10 of
the roadmap. This section consolidates and adds what's missing.

### 4.1 Self-service surface (target Phase 3, September 2026)

| Right | UI path | Backend |
|---|---|---|
| Access (Art. 15) | Settings → "Download my data" | `GET /v1/export` — JSON dump of organization data |
| Rectification (Art. 16) | Settings → profile fields are editable | `PATCH /v1/users/me` |
| Erasure (Art. 17) | Settings → "Delete account" with 14-day grace period | `DELETE /v1/organizations/me` (soft-delete) → cron hard-delete after 14 days |
| Portability (Art. 20) | Same as Access — JSON is structured & machine-readable | `GET /v1/export` |
| Restriction (Art. 18) | Email request only (rare in B2B SaaS) | Manual — DB flag `processing_restricted_at` on organization |
| Objection (Art. 21) | Email-only opt-out for legitimate-interest emails | Manual — `notification_preferences` row |
| Automated decisions (Art. 22) | N/A — we don't make automated decisions about people | — |

### 4.2 Acceptance criteria

For erasure:

1. Soft-delete: organization disappears from UI immediately, all API calls return 404, scheduled scans cancelled, Stripe subscription cancelled via API call (not webhook).
2. Grace period: 14 days, undoable by privacy lead via runbook.
3. Hard-delete: cascade across all tables in §2.1 and §2.2; encrypted secrets zeroised; audit log entries anonymised (user_id → tombstone, log row preserved for 12 months).
4. Backups: RDS snapshots aged out within 7 days; documented in privacy policy as the maximum residual retention window.
5. Confirmation email sent on initiation and on completion.

For export:

1. JSON download covers everything in §2.1 and §2.2 for the requesting organization.
2. Excludes secrets, sub-processor identifiers, internal-only audit fields (`created_by_internal`).
3. Generated on demand (not pre-built); generation logged in audit log.
4. Returned within 30 days (Art. 12(3)) — target ≤ 1 hour for normal organizations, ≤ 24 h for organizations > 1M records.

### 4.3 DSR intake for non-organizations (data subjects who aren't our customers)

Cloud telemetry tags can contain employee emails. If an end-user employee
contacts us asking what data we hold about them, we route them to the
controller (our customer) per Art. 28(3)(e) — we do **not** unilaterally
delete data from a customer's organization. Privacy policy must say this plainly.

### 4.4 SLA

- Acknowledge within 72 hours.
- Fulfil within 30 days (extendable by 60 days under Art. 12(3) with reasoning).
- Track in `audit_log` with action `gdpr.dsr.received` / `.completed`.

---

## 5. Retention & Minimisation

| Data | Retention | Mechanism |
|---|---|---|
| `cost_records` | 90 days rolling | Daily cleanup ticker (already shipped — Phase 2.13) |
| `zombie_records` | Until next scan replaces them; 90 days dormant max | Add cleanup ticker (new — see deliverables) |
| `zombie_snapshots` | 24 months (needed for Phase 4 forecasting) | Document; no automatic deletion |
| `audit_log` | 12 months hot, 7 years for billing-related events | Archive job to S3 Glacier after 12 months |
| User session / JWT cache | 1 hour TTL (Redis) | Already shipped |
| Stripe data | 10 years (German tax law, §147 AO) | Stripe-side retention |
| RDS automated backups | 7 days | RDS config |
| Application logs (CloudWatch) | 7 days | Already configured |

**Hard rule:** no data is retained beyond the table above unless a specific
legal hold is active. Privacy lead reviews retention quarterly.

---

## 6. Sub-Processors

### 6.1 Current list (as of 2026-04-25)

| Sub-processor | Purpose | Region | DPA in place |
|---|---|---|---|
| AWS (ECS Express, RDS, S3, CloudWatch, Secrets Manager) | Hosting, storage, secrets | eu-central-1 | AWS DPA + EU SCCs |
<!-- Kinde removed 2026-05 (chore/remove-kinde-auth) — auth is native; no auth sub-processor. -->
| Stripe | Billing (Phase 3.1) | IE / global | Stripe DPA + SCCs |
| Resend | Transactional email (Phase 2.15) | US (data minimised — only email + subject + body) | Resend DPA + SCCs |
| GitLab.com | Source hosting, CI | US (or EU if we move to gitlab.com EU region) | DPA + SCCs |

### 6.2 Deliverables

- [ ] `docs/compliance/sub-processors.md` — canonical list, kept in sync with public page.
- [ ] Notification mechanism: email opt-in for sub-processor change notifications (30-day notice for adds, 14-day for removals).
- [ ] Each row above maps to a signed DPA in `docs/compliance/dpas/` (PDFs not committed; index file with location + signing date is).
- [ ] Annual review reminder (calendar entry on privacy lead).

### 6.3 International transfers

Only transfers outside the EEA are to Stripe (IE — adequacy) and Resend / GitLab
(US — relying on **EU-US Data Privacy Framework** + SCCs as fallback). Document
the basis for each in the privacy policy and DPA.

---

## 7. Security Controls Mapping (Art. 32)

GDPR Art. 32 requires "appropriate technical and organisational measures."
This is where GDPR work overlaps heavily with SOC 2 — see `soc2_plan.md`.
The list below is the GDPR-specific subset; SOC 2 expands it.

### 7.1 Technical measures already in place

- Encryption at rest: AES-256-GCM on customer secrets in DB (`crypto/`), RDS storage encryption, EBS encryption.
- Encryption in transit: TLS 1.2+ everywhere (CloudFront-managed certs, RDS SSL).
- Organization isolation: PostgreSQL Row-Level Security on every table (`docs/rls.md`).
- Auth: native cookie sessions (argon2id) + per-org OIDC SSO (RS256 ID-token validation, per-connection JWKS cached with 1h TTL).
- Audit trail: `audit_log` table (Phase 3.3) — every mutation logged with user, organization, action, resource.
- Backups: 7-day RDS automated snapshots; tested restore drill scheduled quarterly (deliverable).
- Secrets management: AWS Secrets Manager in production; no `.env` files committed.

### 7.2 Gaps to close before launch

- [ ] **Quarterly restore drill** — runbook + ticketed quarterly task; first drill before first paying customer.
- [ ] **Pseudonymisation of tag data in logs** — request logs must redact `tags` JSONB content.
- [ ] **Access control review** — quarterly review of who has prod AWS access; SOC 2 §CC6 covers this.
- [ ] **Penetration test** — one external pen-test before opening to paying customers; scope: API + auth.
- [ ] **Data breach detection** — CloudWatch alarms on unusual query patterns, on `pg_audit` access to `accounts.secret_encrypted`. SOC 2 plan covers detail.
- [ ] **Vulnerability scanning** — Dependabot / Renovate on Go modules and JS deps; weekly review.

---

## 8. Breach Response (Art. 33–34)

### 8.1 Definitions

- **Personal data breach:** unauthorised access, disclosure, alteration, loss, or destruction of personal data.
- **Notifiable to DPA:** within **72 hours** of awareness, unless unlikely to result in risk.
- **Notifiable to data subjects:** if high risk, "without undue delay."

### 8.2 Runbook (target: `docs/compliance/breach_runbook.md`)

1. **Detection** — alert source (alarm, customer report, security researcher, …).
2. **Triage (≤ 4 h)** — privacy lead + on-call engineer; classify severity (P0–P3).
3. **Contain (≤ 24 h)** — rotate keys, revoke tokens, isolate affected organizations.
4. **Assess (≤ 48 h)** — what data, how many subjects, attack vector, reversibility.
5. **Notify DPA (≤ 72 h)** — Bavarian DPA (BayLDA) for Bavarian-registered entities; submit via online form. Template letter pre-drafted.
6. **Notify subjects (high-risk only)** — email + dashboard banner; template pre-drafted.
7. **Postmortem (≤ 14 days)** — public for confirmed breaches, internal otherwise. Link from status page if customer-facing.

Pre-drafted templates and DPA contact list live in the runbook so we are not
writing legal text under time pressure.

### 8.3 Test

Annual tabletop exercise — simulate a breach, run the timeline, identify gaps.
First exercise before launch.

---

## 9. DPIA (Art. 35)

A Data Protection Impact Assessment is required for high-risk processing.
AxiaOps's core processing (cloud telemetry analysis) is **not** in the
mandatory DPIA list (CNIL/EDPB lists), but we will conduct a lightweight
DPIA voluntarily because:

- Processing customer cloud data at scale.
- Sensitive — exposure could reveal customer infrastructure.

Deliverable: `docs/compliance/dpia.md` covering the nine elements of Art. 35(7),
reviewed annually. ~1 day of work; first version as part of launch checklist.

---

## 10. Records of Processing (Art. 30)

Even though Art. 30(5) exempts <250 employees from formal RoPA when processing
is occasional/low-risk, we exceed the "regular processing" threshold by virtue
of running a SaaS, so we maintain one anyway.

Deliverable: `docs/compliance/ropa.md` — table per processing activity:

- Name and purpose
- Categories of data subjects
- Categories of personal data
- Categories of recipients
- Transfers + safeguards
- Retention
- Security measures (link to SOC 2 evidence)

Reviewed quarterly.

---

## 11. Implementation Roadmap

Mapped onto the existing Phase 2 / Phase 3 roadmap cadence. The §3.10
entry there is the "ship feature" line; this plan is the "ship feature +
paperwork" wrapper.

### Phase 2 finish (May–Aug 2026) — pre-paperwork groundwork

- [ ] §2.1/§2.2 data inventory pass — verify against current migrations
- [ ] CloudWatch log redaction for `tags` field (§7.2)
- [ ] Quarterly restore drill #0 (one before launch)
- [ ] Sub-processors list page (§6.2) — even if empty, scaffold the page
- [ ] DPA reviews for AWS, Stripe (signature-on-file check)

### Phase 3 — Sep 2026 (concurrent with §3.10 task)

- [ ] `DELETE /v1/organizations/me` (soft + hard delete) — §4.2
- [ ] `GET /v1/export` — §4.2
- [ ] Privacy policy + ToS + DPA template + Sub-processors page — §3.2
- [ ] DSR intake form (`gdpr@axiaops.io` + ticketing)
- [ ] Audit log entries for every DSR step
- [ ] Stripe cancellation on organization deletion
- [ ] User anonymisation on hard-delete (audit_log tombstone)
- [ ] Notification preferences UI (legitimate-interest opt-out)

### Phase 3 — Oct 2026 (before first paying EU customer)

- [ ] Breach runbook + tabletop exercise #0 — §8
- [ ] DPIA v1 — §9
- [ ] RoPA v1 — §10
- [ ] External pen-test report — §7.2
- [ ] DPO decision review (still no, document why)
- [ ] Public status page
- [ ] Privacy policy review by Steuerberater / Rechtsanwalt (€1–2k one-off)

### Phase 3 — Dec 2026

- [ ] First quarterly review of RoPA, retention table, sub-processor list
- [ ] First customer-requested DSR (synthetic, internal test)

### Ongoing (Phase 4+)

- [ ] Quarterly: RoPA review, restore drill, access review, sub-processor review
- [ ] Annual: DPIA refresh, breach tabletop, pen-test
- [ ] When adding a sub-processor: 30-day customer notice, DPA on file before go-live

---

## 12. Open Questions

1. Do we want a Verein/Vertrauensperson DPA review for German enterprise sales? Probably yes once we sign first DAX-style customer; not before.
2. Schrems II posture: confirm Resend's DPF certification status before Phase 2.15 ships email digest. If not certified, swap to a Frankfurt-based provider (Mailpace, MessageBird).
3. Customer-managed encryption keys (BYOK) — defer to Phase 4 enterprise tier; mention in privacy policy as "available on request" once we have the integration.
4. DSAR fee policy — Art. 12(5) allows a "reasonable fee" for manifestly unfounded/excessive requests. Default: free. Document threshold for charging.

---

## 13. References

- `docs/development_plan.md` §3.10 — original feature sketch
- `docs/audit_trail_plan.md` — audit log design (feeds DSR & breach work)
- `docs/auth.md`, `docs/auth_flow.md` — authentication design
- `docs/rls.md` — organization isolation
- `docs/production.md` — hosting topology, IAM, log retention
- `docs/compliance/soc2_plan.md` — companion plan; many controls are shared
