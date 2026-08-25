# FOCUS Conformance Plan — AxiaOps

_Last updated: 2026-04-25_

> **Purpose:** Plan to bring AxiaOps to conformance with the FinOps Foundation's
> **FOCUS** (FinOps Open Cost & Usage Specification). Sits alongside
> `gdpr_plan.md` (regulatory) and `soc2_plan.md` (security attestation) — FOCUS
> is a **data-spec** standard, not a regulatory or security regime, but
> customer questionnaires increasingly list "FOCUS-conformant" as a
> requirement, especially in mid-market and MSP buyer profiles.
>
> **Targets** (aligned with the Phase 4 §4.4 roadmap):
> - **FOCUS Consumer (ingest)** — Q2 2027, supports customer-supplied FOCUS
>   exports as a unified ingestion path across AWS / Azure / GCP
> - **FOCUS Producer (export)** — Q3 2027, AxiaOps emits FOCUS-conformant
>   data for downstream tools (BI, finance, customer data lakes)
> - **Foundation conformance review submission** — Q4 2027, after Producer
>   role ships and the spec version is locked

---

## 1. Why FOCUS

### 1.1 What FOCUS is

The FinOps Open Cost & Usage Specification is an open standard published by the
FinOps Foundation. It defines a normalised schema for cloud cost and usage
data — column names, types, semantics — so a single tool can ingest billing
exports from AWS (CUR), Azure (Cost Management), GCP (BigQuery Billing
Export), and OCI without writing per-cloud parsers.

Current GA: FOCUS 1.x. The spec is versioned; conformance is asserted against
a specific version.

### 1.2 Why we care

Three concrete reasons, ranked:

1. **Multi-cloud unification cost goes to ~zero** — when we ship Azure (Phase 4
   §4.2) and GCP (§4.3), FOCUS lets us write **one** ingestion parser instead
   of three. Direct dependency on cloud-provider SDKs only for resource
   discovery; cost ingestion is a single path.
2. **Procurement asks for it** — MSPs and FinOps consultants serving regulated
   customers increasingly demand FOCUS conformance as a checklist item.
   Vantage, Apptio Cloudability, and CloudHealth have already announced
   conformance. Saying "we support FOCUS" removes a friction point in the
   security questionnaire.
3. **Customer data lakes** — Team / MSP / Enterprise tier customers want to
   pipe AxiaOps data into Snowflake, BigQuery, or their own warehouse.
   Emitting FOCUS-conformant Parquet/CSV makes that integration trivial
   for them and removes a custom-export feature request from our backlog.

### 1.3 Why we don't care (yet)

- The spec doesn't help our **detection** moat — that's CloudWatch, Describe
  APIs, and our analyzer rules, none of which are FOCUS-shaped.
- Conformance attestation costs nothing legally but takes ~2 weeks of
  engineering review-cycle time. Not worth doing before Phase 4.
- For AWS-only customers (Phase 1–3 ICP), Cost Explorer + CUR are richer than
  FOCUS in some axes (e.g. RI/SP allocation detail). FOCUS doesn't replace
  CUR for AWS-only — it sits alongside.

---

## 2. Conformance Roles

The FinOps Foundation defines several conformance roles. We're choosing two.

| Role | Definition (paraphrased) | AxiaOps decision |
|---|---|---|
| **Data Producer** | Emits FOCUS-conformant data for downstream tools | **Yes** — Q3 2027 |
| **Data Consumer** | Ingests FOCUS-conformant data and uses it correctly | **Yes** — Q2 2027 (first deliverable) |
| **Tool Provider** | Software that operates on FOCUS data without altering semantics | Implicit — covered by Producer + Consumer |
| **Display** | Presents FOCUS data in UI with correct labels/semantics | Defer — only matters once we expose FOCUS columns directly in dashboard |

Decision rationale: **Consumer first** because it directly unlocks Azure/GCP
Phase 4 work. **Producer second** because it unlocks the customer-data-lake
export use case. Display is a UI polish item we'll add when at least one
customer asks — until then, we expose AxiaOps-native field names and a FOCUS
mapping table in the docs.

---

## 3. Spec Version Strategy

FOCUS is versioned. As of 2026-04-25 the GA branch is FOCUS 1.x with minor
revisions every ~6 months.

**Pinning policy:**

- We assert conformance against a **specific minor version** (e.g. "FOCUS 1.2-conformant").
- Bump policy: lag by one minor version for stability — adopt 1.N once 1.N+1 ships,
  unless a breaking column change forces immediate adoption.
- The version we assert is recorded in `services/shared/focus/VERSION` (committed
  text file) and in every produced FOCUS export's metadata header.
- Deprecated columns: keep emitting them for one minor version after the spec
  deprecates them, with a `Deprecated` flag in our docs.

**Test data:** the FinOps Foundation publishes reference datasets per version;
we keep them in `services/shared/focus/testdata/` as fixtures for the parser
and emitter tests.

---

## 4. Data Model Mapping

### 4.1 Where FOCUS lives in our stack

New package: `services/shared/focus/` — pure mapping/parsing code, no I/O.
Sibling to `analyzer/`. Imported by `ingestion` (consumer side) and `api`
(producer side).

```
services/shared/focus/
  schema.go        — Go types matching the FOCUS column set for the pinned version
  parse.go         — Parquet/CSV → []FocusRecord
  emit.go          — []model.CostRecord → FOCUS Parquet/CSV
  mapping.go       — model.CostRecord ↔ FocusRecord conversion
  validate.go      — schema conformance check (used in tests + CI)
  testdata/        — reference fixtures from the foundation
  VERSION          — pinned FOCUS spec version
```

### 4.2 Column mapping (FOCUS ↔ `model.CostRecord`)

`model.CostRecord` (current) → FOCUS columns (target). This is the canonical
mapping the `mapping.go` file implements.

| `model.CostRecord` field | FOCUS column | Notes |
|---|---|---|
| `Provider` | `ProviderName` | Direct (`aws` → `AWS`, `gcp` → `Google Cloud`, `azure` → `Microsoft`) |
| — (publisher) | `PublisherName` | Same as ProviderName for first-party; differs for resold (MSP scenarios) |
| `AccountID` | `BillingAccountId` + `SubAccountId` | AWS payer vs linked account; Azure subscription vs management group |
| `InternalAccountID` | not in FOCUS | AxiaOps-internal — kept out of producer output |
| `Service` | `ServiceName` + `ServiceCategory` | We store `AmazonEC2`; FOCUS wants `Amazon EC2` (display-cased) and a category like `Compute`. Add a lookup table. |
| `Region` | `RegionId` + `RegionName` | We store `eu-central-1`; FOCUS wants both ID and human name. |
| `ResourceID` | `ResourceId` + `ResourceName` + `ResourceType` | We have ID only — emitting just `ResourceId` is conformant; Name/Type optional. |
| `Amount` | `BilledCost` + `EffectiveCost` + `ListCost` | We currently only store list-price estimates. **Gap** — see §5.2. |
| `Currency` | `BillingCurrency` | Direct |
| `PeriodStart` | `ChargePeriodStart` + `BillingPeriodStart` | Direct (UTC) |
| `PeriodEnd` | `ChargePeriodEnd` + `BillingPeriodEnd` | Direct |
| `Tags` | `Tags` | Direct (FOCUS uses key/value map, same shape) |
| `FetchedAt` | not in FOCUS | Internal metadata; kept out of producer output |
| — | `ChargeCategory` | Required FOCUS field; map by service (Usage / Tax / Credit / Adjustment / Refund). We currently only ingest Usage — emit `Usage` constant for now and revisit when we add CUR ingestion. |
| — | `ChargeClass` | Optional, leave blank in v1 |
| — | `CommitmentDiscountType` | Required for SP/RI handling — depends on §5.2 |
| — | `PricingCategory` / `PricingQuantity` / `PricingUnit` | Optional; populate from CUR when available |
| — | `SkuId` / `SkuPriceId` | Optional; from CUR |
| — | `InvoiceIssuerName` | Same as PublisherName for first-party |

### 4.3 Schema gaps to close

To be a credible FOCUS Producer we need fields we don't currently store. New
migrations and ingestion logic — these are tracked in §5.

| Gap | Action |
|---|---|
| No `BillingAccountId` vs `SubAccountId` distinction | Already partly there — `accounts` table stores AWS account ID; need to record payer/linked split. |
| Single `Amount` field — no list/effective/billed split | Add columns when CUR ingestion lands (Phase 3 #13). |
| Missing `ChargeCategory` | Default to `Usage` until CUR ingestion adds the others. |
| Missing `CommitmentDiscountType` | Populate from CUR's `savings_plan_*` and `reservation_*` fields. |
| No `ServiceCategory` lookup | Build a table — small static mapping, ~30 entries. |

---

## 5. Implementation Roadmap

Mapped against the existing Phase 3 / Phase 4 roadmap. The §4.4 entry
there is the "ship feature" line; this plan is the "ship feature + conformance
+ paperwork" wrapper.

### Phase 3 finish (Q3–Q4 2026) — foundations

The non-FOCUS work below is required before FOCUS conformance is meaningful;
it's not new, just called out for the dependency chain.

- [ ] **CUR ingestion** (Phase 3 #13) — without this, our `Amount`
      field is list-price-only. FOCUS demands `BilledCost` + `EffectiveCost` +
      `ListCost` for credible Producer role. CUR ingestion is the prerequisite.
- [ ] `ServiceCategory` lookup table — `services/shared/focus/service_categories.go`,
      ~30 entries (Compute / Storage / Networking / Database / etc.). One-time work,
      can ship before CUR is done.

### Phase 4 — Q1 2027 — Consumer role

- [ ] Pin FOCUS version: write `services/shared/focus/VERSION` with the chosen
      version. Initial pick: latest GA minus one minor (per §3 policy).
- [ ] Implement `services/shared/focus/schema.go` — Go types for the pinned
      version's columns.
- [ ] Implement `services/shared/focus/parse.go` — Parquet + CSV readers.
      Parquet via `github.com/parquet-go/parquet-go`; CSV via stdlib.
- [ ] Implement `services/shared/focus/mapping.go` — FocusRecord → CostRecord.
- [ ] Add `focusfile` provider in `services/ingestion/internal/provider/focusfile/`
      — reads from S3 (or generic blob storage). Same `Provider` interface as `aws`.
- [ ] Validate against the foundation's reference dataset — `make test-focus`.
- [ ] Document customer setup in `docs/focus_ingestion.md` — how to grant
      cross-account S3 read; how to enable FOCUS export in AWS / Azure / GCP;
      schedule expectations (daily file drop).
- [ ] Update `docs/aws-coverage.md` (or new `docs/multicloud-coverage.md`) to
      list which detection rules work via FOCUS-only ingestion vs which still
      require CloudWatch / Describe APIs.

**Acceptance:** a customer can drop a FOCUS-formatted Parquet file in their
S3 bucket, and AxiaOps ingests it identically to a Cost Explorer pull.

### Phase 4 — Q2 2027 — Producer role

- [ ] Implement `services/shared/focus/emit.go` — Parquet + CSV writers.
- [ ] Implement `services/shared/focus/validate.go` — schema-shape check used
      in CI to catch regressions when we bump the spec version.
- [ ] Add `GET /v1/export/focus?period=YYYY-MM&format=parquet|csv` endpoint
      in `services/api/internal/api/`. Streams a FOCUS-conformant file for
      the requesting organization.
- [ ] Schema-validate every emitted file in tests against the foundation
      reference validator.
- [ ] Plan-gate Producer access — Team tier and above.
      Free / Starter / Growth get Consumer (ingest) only.
- [ ] Customer-facing docs: `docs/focus_export.md` — examples for piping into
      Snowflake / BigQuery / Athena / Power BI.
- [ ] Audit log entry on every export (overlaps GDPR §4.2 — `gdpr.dsr.export`
      and `focus.export.generated` are sibling events).

**Acceptance:** an emitted file passes the foundation's reference validator
and round-trips through our own Consumer parser without data loss.

### Phase 4 — Q3 2027 — Multi-cloud unification (the actual payoff)

This is the strategic reason we did all the above.

- [ ] Replace Azure-specific cost ingestion (originally planned in §4.2) with
      "Azure → FOCUS export → AxiaOps focusfile provider." Two pieces shrink
      to one.
- [ ] Same for GCP (§4.3): "GCP Billing Export → FOCUS → focusfile provider."
- [ ] Document the new ingestion topology in `docs/multicloud-coverage.md`.
- [ ] Update `Provider` interface comment in
      `services/ingestion/internal/provider/provider.go` — note that
      `focusfile` is the canonical multi-cloud path; native SDK providers
      remain for resource discovery and CloudWatch-equivalent metrics.

### Phase 4 — Q4 2027 — Foundation conformance review

- [ ] Submit conformance assertion via the FinOps Foundation's portal (if a
      formal review process exists at that point — currently self-attestation
      with optional review).
- [ ] Add the badge to `axiaops.io/security` and the homepage.
- [ ] Reference in customer questionnaires and pricing pages.
- [ ] Re-assert annually or on minor version bump (§3).

---

## 6. Customer-Facing Surface

| Touchpoint | What we say |
|---|---|
| `axiaops.io/security` | "FOCUS 1.x conformant Consumer (since YYYY-MM) and Producer (since YYYY-MM). Latest assertion: link to public statement." |
| Pricing page | "Team tier and above can export their AxiaOps cost data as FOCUS-conformant Parquet or CSV." |
| `docs/focus_ingestion.md` | Step-by-step: enable FOCUS export at provider, grant S3 read, paste bucket ARN in AxiaOps account form. |
| `docs/focus_export.md` | Step-by-step: schedule export, pipe into Snowflake / BigQuery / Athena. Includes example DDL. |
| In-app | A small "FOCUS export" button on Cost Analytics screen for Team+ organizations. |
| Sub-processor / DPA | No change — FOCUS is a data shape, not a sub-processor. The output is still customer data we already process. |

---

## 7. Risks & Mitigations

| Risk | Mitigation |
|---|---|
| FOCUS spec evolves faster than we adopt — claims of "1.x conformant" go stale | Pin policy in §3; CI test against current and current-minus-1 reference datasets |
| Producer output drifts from spec after a refactor | `validate.go` runs in CI on every emitted fixture; failing build blocks merge |
| CUR ingestion (Phase 3 #13) slips → Producer claim becomes weak (list-price-only `BilledCost`) | Don't claim Producer until CUR ships. Update §5 acceptance criteria with this dependency; assert Consumer-only until then. |
| Customers expect FOCUS to also carry usage metrics — it doesn't | Doc explicitly: FOCUS covers cost. Usage signals (CPU%, connections) come from CloudWatch and don't ship in our FOCUS export. |
| Foundation deprecates FOCUS in favour of a successor (low likelihood, mention for completeness) | Spec is governed by a vendor-neutral foundation; deprecation would be slow and well-signposted |
| Conformance review process changes (currently self-attestation) | Re-assess Q4 2027 — if the foundation has shifted to formal third-party review, treat similar to SOC 2: scope, auditor, timeline |

---

## 8. Open Questions

1. **Parquet vs CSV first?** Parquet is the FOCUS-recommended format and what
   serious downstream tools (Snowflake, BigQuery, Athena) want. CSV is easier
   to debug and cheaper to ship first. Decision: ship CSV in Phase 4 Q1 if
   that's the only path to Consumer-on-time, but Parquet must follow within
   the same quarter.
2. **Currency handling.** FOCUS supports multi-currency exports, but our
   `cost_records.Currency` is a single field per row. Confirm whether the
   FOCUS `BillingCurrency` is per-row (it is — confirmed) so our model already
   accommodates this.
3. **Tag passthrough.** Should we strip empty-value tags during emit? FOCUS
   permits them but downstream tools sometimes choke. Decision: emit as-is;
   document tag hygiene as a customer-side concern.
4. **Conformance for Display.** Worth adding once the Cost Analytics screen
   surfaces FOCUS-shaped columns natively. Defer to 2028.

---

## 9. References

- FinOps Foundation: <https://focus.finops.org> (spec, conformance roles, reference data — verify URL on next read; do not link blindly)
- `docs/development_plan.md` Phase 4 — multi-cloud and FOCUS framing
- `services/shared/model/cost.go` — current `CostRecord` shape, the source of truth for the mapping in §4.2
- `services/ingestion/internal/provider/` — `Provider` interface that `focusfile` will satisfy
- `docs/compliance/gdpr_plan.md` §4.2 — `GET /v1/export` is the GDPR portability endpoint; `GET /v1/export/focus` is its FOCUS-shaped sibling for Producer role
- `docs/compliance/soc2_plan.md` — Confidentiality TSC covers customer data egress; FOCUS Producer flows under that umbrella
