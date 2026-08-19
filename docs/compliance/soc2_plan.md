# SOC 2 Compliance Plan — AxiaOps

_Last updated: 2026-04-25_

> **Purpose:** Plan to bring AxiaOps to SOC 2 Type I (point-in-time) and then
> Type II (six-month observation window). Companion to `gdpr_plan.md` —
> Art. 32 controls and SOC 2 Common Criteria overlap heavily; this plan owns
> the SOC 2 frame.
>
> **Targets**:
> - **SOC 2 Type I:** Q2 2027 — to unlock mid-market deals that ask for
>   "any SOC 2 report"
> - **SOC 2 Type II:** Q4 2027 — required to seriously sell to MSPs running
>   regulated client workloads
>
> Earlier SOC 2 makes no sense — no real customers, no audit evidence, the
> €15–30k audit cost has nothing to attest. The plan below builds the
> evidence pipeline now so the audit window is short and clean.

---

## 1. Why SOC 2 (and Why Now)

### 1.1 Sales gating

Sales gating observations:

- MSPs and Team-tier customers (>€399/mo) routinely block on a security
  questionnaire that asks for "SOC 2 Type II report or equivalent." A "we're
  in progress" letter unblocks 60% of these; an actual report unblocks 90%.
- Enterprise (€custom) deals are gated on Type II — no exceptions.
- Without SOC 2, the ceiling is ~€10–15k MRR before deals start stalling.

### 1.2 Why not ISO 27001 instead

Considered. Decision: SOC 2 first because (a) the buyer questionnaires we'll
see in 2026–27 ask for SOC 2 by name, (b) ISO 27001 audit cost is similar but
turnaround is longer, (c) SOC 2 evidence pipeline subsumes ~70% of ISO 27001
work — we can layer ISO on top in 2028 if a major EU customer demands it.

### 1.3 Trust Services Criteria scope

| TSC | Include for Type I/II? | Rationale |
|---|---|---|
| **Security** (CC1–CC9) | **Yes** | Mandatory; the core of SOC 2 |
| **Availability** | **Yes** | Customer-visible SLA in Team/MSP tiers; small marginal cost |
| **Confidentiality** | **Yes** | Customer cloud data is confidential by contract; small marginal cost |
| Processing Integrity | No (Type I); reconsider for Type II | Cost computation accuracy is interesting but hard to attest cleanly; defer |
| Privacy | No | GDPR plan covers privacy; SOC 2 Privacy TSC is US-centric and adds audit cost without EU sales benefit |

Initial scope: **Security + Availability + Confidentiality**.

---

## 2. Auditor & Tooling Choice

### 2.1 Compliance automation platform

Pick one of: **Drata, Vanta, Secureframe, Sprinto**.

Decision criteria:

- AWS connector quality (we're 100% AWS) — all four have it
- GitLab connector — Drata and Vanta have native; Secureframe needs a custom integration
- EU pricing tier — Drata and Vanta both quote ~€7–10k/year for our size
- Auditor partner network — all four overlap on the auditors below

**Recommendation:** Drata. Native GitLab, EU presence, transparent pricing,
larger startup customer base than Vanta in EU. Sign in Q4 2026 — earlier and
the platform is paying-for-air.

### 2.2 Auditor

Two paths:

1. **Big Four** (Deloitte, EY, KPMG, PwC) — €40–80k Type II, brand value, slow.
2. **Boutique** (Prescient Assurance, Insight Assurance, A-LIGN, Johanson, Schellman) — €15–30k Type II, faster, fine for SMB/mid-market sales.

**Recommendation:** boutique (Prescient or Schellman — both routinely audit
EU SaaS at our stage). Big-Four name doesn't move our buyer (mid-market
DevOps + MSPs), and the cost differential pays for half a year of runway.

### 2.3 Cost envelope

| Item | Year 1 | Year 2 |
|---|---|---|
| Drata (or equivalent) subscription | €7–10k | €7–10k |
| Type I audit | — | €8–12k |
| Type II audit (6-month window) | — | €15–25k |
| Pen-test (annual, scoped to API + auth) | €4–8k | €4–8k |
| Lawyer review of policies (one-off) | €2–3k | €0 |
| **Total** | **€13–21k** | **€34–55k** |

Budget pre-committed — built into Phase 3 / Phase 4 financial projections.

---

## 3. Control Mapping (Common Criteria)

Below: each Common Criteria area, what we have today, and what's missing.
"Status" reflects the **2026-04-25** snapshot — must be re-walked quarterly.

### CC1 — Control Environment

| Sub-criterion | Status | Gap |
|---|---|---|
| CC1.1 Integrity & ethical values | Partial | Code of conduct doc; founder-only company so light |
| CC1.2 Board/management oversight | N/A (founder-led); revisit when first employee joins | Document founder-as-management explicitly |
| CC1.3 Org structure, authorities | N/A → light | Org chart in `docs/compliance/org.md` |
| CC1.4 Commitment to competence | Partial | Hiring rubric exists |
| CC1.5 Accountability | Partial | RACI for incidents — see breach runbook |

**Deliverables:** `docs/compliance/policies/code_of_conduct.md`, `org.md`.

### CC2 — Communication & Information

| Sub-criterion | Status | Gap |
|---|---|---|
| CC2.1 Internal information quality | Partial | `CLAUDE.md`s + the roadmap tracker cover it; formalise in policy |
| CC2.2 Internal communications | Partial | Slack/email; document channels |
| CC2.3 External communications | Partial | Status page (Phase 3 deliverable) |

**Deliverables:** `docs/compliance/policies/communications.md`,
`status.axiaops.io` (Statuspage / Instatus).

### CC3 — Risk Assessment

| Sub-criterion | Status | Gap |
|---|---|---|
| CC3.1 Specifies objectives | Strong | Business plan + roadmap |
| CC3.2 Identifies & analyses risk | Weak | Annual risk register required |
| CC3.3 Assesses fraud risk | N/A | Document non-applicability |
| CC3.4 Identifies & assesses change | Partial | Migration / change-management docs |

**Deliverable:** `docs/compliance/risk_register.md`, reviewed quarterly,
covering at minimum: cloud provider outage, data breach, key personnel
loss, regulatory change, supply-chain compromise.

### CC4 — Monitoring Activities

| Sub-criterion | Status | Gap |
|---|---|---|
| CC4.1 Selects & develops monitoring | Strong | Prometheus + slog already in place |
| CC4.2 Evaluates & communicates deficiencies | Weak | Quarterly internal control review process |

**Deliverables:** quarterly internal review template + cadence.

### CC5 — Control Activities

| Sub-criterion | Status | Gap |
|---|---|---|
| CC5.1 Selects & develops control activities | Partial | Mostly documented in CLAUDE.md, needs roll-up |
| CC5.2 Selects & develops technology controls | Strong | RLS, encryption, auth — all in place |
| CC5.3 Deploys through policies & procedures | Weak | Policy library missing |

**Deliverable:** policy library (see §4 below).

### CC6 — Logical & Physical Access ⚠️ **biggest gap area**

| Sub-criterion | Status | Gap |
|---|---|---|
| CC6.1 Logical access — security software | Strong | Native auth (argon2id + cookie sessions), per-org OIDC SSO, RLS |
| CC6.2 Logical access — registers/auths | Partial | User onboarding/offboarding runbook missing |
| CC6.3 Logical access — modifies/removes | Partial | Quarterly access review missing |
| CC6.4 Physical access | N/A | All cloud — document AWS reliance |
| CC6.5 Logical access — disposes assets | Partial | Hardware disposal policy (laptops) |
| CC6.6 Logical access — boundary protection | Strong | Security groups, ECS Express, no NAT |
| CC6.7 Restricts data movement | Partial | DLP not in place; document why proportional |
| CC6.8 Prevents unauthorised software | Partial | Endpoint policy on developer laptops |

**Deliverables:**

- `docs/compliance/policies/access_control.md`
- `docs/compliance/policies/asset_management.md` (laptop encryption, disposal)
- Quarterly access review job + audit log
- Mandatory MFA on all SaaS (AWS, GitLab, Stripe, Resend, Drata)
- Hardware-key (YubiKey) for AWS root + GitLab admin

### CC7 — System Operations

| Sub-criterion | Status | Gap |
|---|---|---|
| CC7.1 Detects & monitors changes | Strong | GitLab CI on every push |
| CC7.2 Monitors system components | Partial | Prometheus alerts (Phase 2.6 done); add SLO alerts |
| CC7.3 Evaluates security events | Weak | SIEM-light: CloudWatch alarms wired to Slack/email |
| CC7.4 Responds to security incidents | Partial | Breach runbook in GDPR plan; expand with security-only flow |
| CC7.5 Recovers from incidents | Partial | RDS backup + restore drill; documented but not tested |

**Deliverables:**

- `docs/compliance/runbooks/incident_response.md` (security-specific, complements breach runbook)
- CloudWatch alarms for: failed-auth spikes, DB role escalation, secret access patterns, deploy outside business hours
- Quarterly restore drill (also a GDPR deliverable)
- SLO definition + alerting (`docs/compliance/slos.md`)

### CC8 — Change Management

| Sub-criterion | Status | Gap |
|---|---|---|
| CC8.1 Authorises, designs, develops, tests, approves, documents, deploys changes | Partial | Two-person rule for prod migrations missing; we have CI + code review but not formalised |

**Deliverables:**

- `docs/compliance/policies/change_management.md` — code review required, prod deploys logged, rollback plan documented per release
- Tag-based release process: only signed tags from `main` deploy to prod (already partial via GitLab CI)
- Migration approval workflow: privacy lead approves any migration touching `organizations`, `users`, `accounts`, `audit_log`

### CC9 — Risk Mitigation

| Sub-criterion | Status | Gap |
|---|---|---|
| CC9.1 Identifies, selects, develops risk mitigation | Partial | Vendor risk management process |
| CC9.2 Manages vendor & business partner risks | Weak | Sub-processor evaluation checklist |

**Deliverables:**

- `docs/compliance/policies/vendor_management.md`
- Vendor risk checklist (reviewed when adding any sub-processor — overlaps with GDPR §6)

### Availability TSC

| Criterion | Status | Gap |
|---|---|---|
| A1.1 Performance & capacity | Partial | Capacity plan + alarms; we have metrics but no documented thresholds |
| A1.2 Backup & restore | Partial | Restore drill not yet executed |
| A1.3 Recovery infrastructure | Partial | Multi-AZ RDS deferred (cost) — document risk acceptance |

**Deliverables:**

- `docs/compliance/policies/business_continuity.md` — RTO 4h, RPO 1h targets
- DR runbook with tested restore drill evidence
- Status page with public uptime history

### Confidentiality TSC

| Criterion | Status | Gap |
|---|---|---|
| C1.1 Identifies confidential information | Partial | Data classification scheme |
| C1.2 Disposes of confidential information | Partial | Erasure flow already in GDPR plan; map to SOC 2 evidence |

**Deliverables:**

- `docs/compliance/data_classification.md` — Public / Internal / Confidential / Customer-Restricted
- Mapping doc: which tables are which class

---

## 4. Policy Library

Auditors expect ~12–15 policies. Each one is a 1–3 page document, reviewed
annually, signed off by the founder (later: the security lead). Drata
ships templates for all of these — start from the templates, customise for
AxiaOps reality.

| # | Policy | Owner | First version due |
|---|---|---|---|
| 1 | Information Security Policy (master) | Privacy lead | Q4 2026 |
| 2 | Access Control Policy | Privacy lead | Q4 2026 |
| 3 | Acceptable Use Policy | Privacy lead | Q4 2026 |
| 4 | Asset Management Policy | Privacy lead | Q1 2027 |
| 5 | Change Management Policy | Eng lead | Q4 2026 |
| 6 | Code of Conduct | Privacy lead | Q4 2026 |
| 7 | Data Classification Policy | Privacy lead | Q4 2026 |
| 8 | Encryption Policy | Eng lead | Q1 2027 |
| 9 | Incident Response Policy | Privacy lead | Q4 2026 |
| 10 | Vendor / Sub-processor Management Policy | Privacy lead | Q4 2026 (overlaps GDPR §6) |
| 11 | Business Continuity / DR Policy | Eng lead | Q1 2027 |
| 12 | Risk Management Policy | Privacy lead | Q1 2027 |
| 13 | Secure Development Policy (SDLC) | Eng lead | Q1 2027 |
| 14 | Backup & Retention Policy | Eng lead | Q1 2027 |
| 15 | Onboarding & Offboarding Policy | Privacy lead | Q1 2027 (single-founder for now; needed before first hire) |

All in `docs/compliance/policies/`. Markdown, version-controlled, reviewed
annually with `last_reviewed` frontmatter so Drata can track staleness.

---

## 5. Evidence Pipeline

The Type II audit looks back 6 months. Evidence collection has to be already
running when the window opens. This is the bulk of the work.

### 5.1 Continuously collected (automated via Drata)

- AWS CloudTrail logs
- IAM user/role inventory + last-used timestamp
- GitLab project settings (branch protection, code-review enforcement)
- GitLab CI runs (security scans must pass)
- SSO/MFA enforcement state (native auth + per-org OIDC)
- SaaS account inventory (with MFA flags)
- Endpoint security state (laptops — Drata Agent or similar)

### 5.2 Manually collected (quarterly)

- Access review (CC6.3) — sign-off in Drata
- Risk register review (CC3.2)
- Vendor review (CC9.2)
- Restore drill evidence (CC7.5, A1.2) — runbook output, screenshot, log
- Incident retrospectives (CC7.4) — even if "no incidents this quarter, here's the audit log"
- Change ticket sample (CC8.1) — pull 5 random merges, show review + deploy trail

### 5.3 Annually collected

- Pen-test report
- Tabletop exercise output
- Security awareness training completion (founder + any contractors)
- Policy review sign-offs

---

## 6. Roadmap

### Phase 2 finish (May–Aug 2026) — set the stage

- [ ] Document data classification (free; 4 hours)
- [ ] Stand up status page (Instatus, ~€20/mo)
- [ ] Quarterly access review process — even when there's only one user, build the muscle
- [ ] Hardware key on AWS root, GitLab admin
- [ ] CloudWatch alarms: failed-auth spike, secret-access pattern, off-hours deploys

### Phase 3 (Sep–Dec 2026) — operational baseline

- [ ] Sign Drata in October — 12 months of evidence collection ahead of Type II window
- [ ] Write policy library (15 docs) — 4–6 weeks elapsed; 1 doc/week pace
- [ ] Restore drill #1 — actually do it, capture evidence
- [ ] Risk register v1
- [ ] Incident response runbook (overlaps with GDPR breach runbook)
- [ ] Pen-test #0 (also a GDPR deliverable; one report covers both)
- [ ] Vendor questionnaire pack — ready to answer enterprise-style buyers
- [ ] **Public statement: "SOC 2 in progress, Type II Q4 2027"** — already promised in business plan

### Phase 4 / Q1 2027 — Type I prep

- [ ] Drata gap analysis pass — close any control showing "Not implemented"
- [ ] Boutique auditor selection (Q1 2027)
- [ ] **Type I audit (Q2 2027)** — point-in-time, 4–6 weeks engagement
- [ ] Publish Type I report (NDA-gated download)
- [ ] Customer security page: `axiaops.io/security` — controls summary, sub-processors, status, CSF mapping

### Q2–Q4 2027 — Type II window

- [ ] Begin Type II observation window (May 1, 2027 → Oct 31, 2027 — 6 months)
- [ ] Quarterly cadence kicks in: access review, risk review, vendor review, restore drill
- [ ] Pen-test #2 within window
- [ ] Tabletop exercise within window
- [ ] **Type II audit (Q4 2027)** — fieldwork ~6–8 weeks; report by Dec 2027
- [ ] Publish Type II report

### Ongoing (2028+)

- [ ] Annual Type II renewal (rolling 12-month windows)
- [ ] Add Privacy TSC if EU enterprise customers ask
- [ ] Layer ISO 27001 if a major customer demands it (~70% of work overlaps)

---

## 7. Risks & Mitigations

| Risk | Mitigation |
|---|---|
| Drata pricing creeps as we add employees | Lock in 2-year deal at signature |
| Auditor scoping disagreement (e.g. wants Privacy TSC) | Agreed scope in writing before fieldwork starts |
| Evidence gap discovered late in observation window | Drata's continuous-monitoring dashboard catches gaps weekly; review monthly with calendar entry |
| Single-person company is a CC1 weakness | Add a second person (advisor or contractor) listed as security backup before audit; document founder-mode acceptance with auditor |
| Pen-test finds high-severity issues right before audit | Run pen-test 6 months before audit, not 1 month before |
| Customer asks for SOC 2 before we have it | "In progress" letter + Drata trust report dashboard satisfies most asks |

---

## 8. Open Questions

1. Do we want SOC 2 + ISO 27001 dual-track from the start? Decision: no, phase
   ISO 27001 to 2028 unless a deal demands otherwise.
2. Trust report public or NDA-gated? Decision: NDA-gated for the actual reports;
   public summary at `axiaops.io/security` with control narrative.
3. Customer audit-right clauses (Art. 28-style) — push back to "annual auditor
   report sufficient" except for Enterprise tier where we negotiate.
4. Bring-your-own-key (BYOK) — separate work item; not blocked by SOC 2 but
   often asked for in the same questionnaires.

---

## 9. References

- `docs/compliance/gdpr_plan.md` — companion plan; Art. 32 ↔ CC6/CC7 overlap
- `docs/development_plan.md` — phase timing
- `docs/audit_trail_plan.md` — audit log feeds CC7.3 evidence
- `docs/production.md` — hosting topology, IAM, secrets
- `docs/rls.md` — organization isolation (CC6.1)
- `docs/auth.md` — authentication design (CC6.1)
- `docs/security-audit-2026-05-09.md` — security audit findings + resolution status
