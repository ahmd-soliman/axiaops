# CloudTrail Analysis — Why Not Now, When Later

**Status:** Deferred to Phase 4+ | **Last Updated:** April 2026

## Overview

CloudTrail logs all AWS API calls, including Cost Explorer API usage, Lambda invocations, database queries, and more. While CloudTrail provides comprehensive API visibility, it was not prioritized for the MVP because of unfavorable ROI compared to high-impact zombie resource detection.

---

## What CloudTrail Would Let Us Detect

### 1. Wasted Cost Explorer API Calls

**Current situation:**
- Cost Explorer API costs $0.01 per request
- AxiaOps calls it once per scan for service-level costs
- Customers call it via CLI, dashboards, automations, etc.

**Detectable with CloudTrail:**
```
CloudTrail Event: GetCostAndUsage
  User: jane@acme.com
  Timestamp: 2026-04-21 10:30:45 UTC
  RequestParameters: {TimePeriod: 2026-04-01 to 2026-04-30}

Analyze pattern:
  - Same query 100x in one day → suspicious
  - Cost Anomaly Detection monitor with zero findings → disable it
  - Automated hourly cost pulls → might be redundant
```

**Cost to customer:**
- 10,000 API calls/month = $100/year
- If optimized: 1,000 calls/month = $10/year
- **Potential savings: $5-90/year** ❌ Low value

### 2. Unused Lambda Functions (Alternative Detection)

**Current approach:**
- CloudWatch metric: `Invocations = 0`
- Works great ✅

**CloudTrail approach:**
- Query CloudTrail for `Invoke` events per function
- Would duplicate CloudWatch data
- Unnecessary complexity

### 3. Unused IAM Principals / API Keys

**Current approach:**
- Not detected (Phase 3+)

**CloudTrail approach:**
- Track `AccessKey` events per principal
- Find keys with zero API calls in 90 days
- **Potential savings: $10-50/month per stale key** ⚠️ Moderate value
- **Complexity: High** (IAM principal tracking, key rotation policies)

### 4. Unused Automation / Scheduled Tasks

**Example:**
- Lambda function triggered by EventBridge rule
- Rule fires every hour but Lambda is disabled
- Costs: EventBridge $0.35/million invocations + Lambda pricing

**CloudTrail approach:**
- Track Lambda invocations vs CloudWatch metrics
- Detect mismatches (rule fires but Lambda doesn't execute)
- **Potential savings: $1-10/month** ⚠️ Low value, high effort

---

## ROI Analysis: Why Not Now

### Phase 1 MVP: High-Impact Wins (Shipped ✅)

| Detection | Cost/Customer | Time | Status |
|-----------|---------------|------|--------|
| EC2 idle (CPU ≤ 5%) | $50–500/mo | ✅ Done | CloudWatch |
| Unattached EBS volumes | $50–200/mo | ✅ Done | EC2 API |
| Orphaned snapshots | $50–100/mo | ✅ Done | EC2 API |
| Stopped instances > 30 days | $20–100/mo | ✅ Done | EC2 API |
| Idle RDS instances | $20–500/mo | ✅ Done | CloudWatch |
| Unused Lambda | $5–50/mo | ✅ Done | CloudWatch |
| Cost Anomaly monitors | $3–10/mo | ✅ Done | Cost Explorer API |

**Average high-value target: $50–300/month per customer**

### CloudTrail: Low-Value Options

| Detection | Cost/Customer | Time | Complexity | Status |
|-----------|---------------|------|-----------|--------|
| Wasted CE API calls | $1–10/mo | 3-4 weeks | Medium | Deferred |
| Stale IAM keys | $10–50/mo | 3-4 weeks | High | Phase 4+ |
| Unused EventBridge rules | $1–5/mo | 2-3 weeks | High | Phase 4+ |

**Average CloudTrail target: $1–50/month per customer**

### Cost of CloudTrail

**Infrastructure:**
- CloudTrail logging: $2.50 per 100K events
- S3 storage: $0.023 per GB/month
- Typical customer: 10M events/month = $250/month storage + retrieval

**AxiaOps Cost:**
- 2-3 weeks engineering per feature
- Ongoing maintenance (new API patterns, AWS SDK updates)
- Complexity in analyzer (machine learning to detect "waste")

**Per-Feature ROI:**

```
CE API Waste Detection:
  Benefit: $5/customer/month
  Cost: $250/customer/month infrastructure
       + $1000/month engineering (amortized)
  = NEGATIVE ROI ❌

Stale IAM Keys:
  Benefit: $20/customer/month
  Cost: $250/customer/month infrastructure
       + $1000/month engineering
  = NEGATIVE ROI ❌
```

---

## When to Add CloudTrail (Decision Tree)

### Add CloudTrail if ANY of these are true:

1. **Enterprise customers paying 10x average**
   - $500K+ spend = $500-5000/month in API waste significant
   - Can justify $250/month infrastructure cost

2. **Compliance requirement**
   - Customer mandate: "Must audit all AWS API calls"
   - Worth the cost for contract value

3. **Feature parity with Vantage**
   - Vantage shows API usage patterns
   - Customer comparing tools asks for it
   - Add as competitive feature, not ROI-driven

4. **Mature customer base**
   - After 100+ customers using Phase 1 detections
   - Low-hanging fruit exhausted
   - Need differentiation → Phase 4 features

### Don't add CloudTrail if:

- Customer base is SMB (<$100K AWS spend)
- Focus is on high-impact zombie resource detection
- Engineering time better spent on multi-cloud support (Phase 4)

---

## Implementation Plan (If/When Added)

### Phase 4 Candidate Feature: "API Audit"

**Scope:**
1. Enable CloudTrail for customer account (via IAM role)
2. Read logs from S3 daily
3. Aggregate API call patterns (by principal, by service, by hour)
4. Flag anomalies:
   - Cost Explorer queries > 100/day
   - Lambda invocations in CloudTrail but zero in CloudWatch (disabled function)
   - EventBridge rule fires but no Lambda execution (stale automation)

**Storage:**
```sql
CREATE TABLE api_events (
    id BIGSERIAL PRIMARY KEY,
    tenant_id TEXT NOT NULL,
    api_service TEXT,  -- "ce", "lambda", "eventbridge", etc.
    principal TEXT,    -- IAM user/role
    event_count INT,
    period_start TIMESTAMPTZ,
    period_end TIMESTAMPTZ,
    anomaly_type TEXT, -- "excessive", "orphaned", "disabled"
    anomaly_score FLOAT -- 0.0-1.0 confidence
);
```

**Effort Estimate:**
- CloudTrail parser: 1 week
- Analyzer rules: 1 week
- Tests: 3 days
- Total: 2.5 weeks

**Cost to Customer:**
- CloudTrail logging: $2.50 per 100K events
- S3 storage: $20-50/month for log retention
- Total additional: $25-60/month per account

---

## Comparison: AxiaOps vs. Vantage vs. Unused

| Feature | AxiaOps | Vantage | Unused |
|---------|---------|---------|--------|
| **CloudWatch zombie detection** | ✅ MVP | ✅ Yes | ✅ Yes |
| **API-based detection** (EBS, snapshots, etc.) | ✅ MVP | ✅ Yes | ✅ Yes |
| **CloudTrail API tracking** | ❌ Deferred | ✅ Yes | ❌ No |
| **Scheduled scans** | ✅ Phase 2 | ✅ Yes | ✅ Yes |
| **Multi-cloud** | 🎯 Phase 4 | ✅ AWS/Azure/GCP | ❌ AWS only |

**Position:** AxiaOps focuses on **high-impact resource detection** (Phases 1-3). CloudTrail features (API audit) are lower priority but possible Phase 4 additions if customer demand warrants.

---

## Related Decisions

- **Why not S3/CloudFront?** See `CLAUDE.md` — ambiguous thresholds, deferred to Phase 3
- **Why not IAM audit?** See this doc — low ROI, complex pattern matching
- **Why Cost Explorer API over native billing?** See `aws-coverage-and-cost-explorer-notes.md` — cost data availability, resource-level granularity

---

## Appendix: CloudTrail API Cost Examples

**Conservative customer (SMB):**
- 1M API calls/month
- CloudTrail cost: $25/month
- AxiaOps scans 4x/month = 4 × GetCostAndUsage = $0.04/month
- Wasted CE API (assumption): $2/month
- **Not worth tracking**

**Large customer (enterprise):**
- 100M API calls/month
- CloudTrail cost: $2,500/month
- AxiaOps scans daily = 30 × GetCostAndUsage = $0.30/month
- Wasted CE API (assumption): $50/month (if optimized: $5/month)
- **Possible savings: $45/month** — still outweighed by $2500 storage cost

**Extreme case (massive org):**
- 1B API calls/month
- CloudTrail cost: $25,000+/month
- **But:** This org has dedicated cost optimization team — AxiaOps is small piece
- **CloudTrail ROI:** Possible, but not primary value driver
