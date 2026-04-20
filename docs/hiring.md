# Hiring Plan & Solo-Founder Runway — AxiaOps

**Audience:** Internal / founder
**Date:** 20 April 2026
**Companion docs:** `docs/business_plan.md`, `docs/market-readiness-2026-04.md`

> **TL;DR** — Solo is realistic for ~12–15 months from today (April 2026 → mid-2027, covering launch and first ~20 paying customers). Beyond that, staying solo actively shrinks the business. First hire should be a part-time customer success / marketing contractor at roughly €3K–8K MRR, pre-committed in the financial model rather than triggered reactively.

---

## 1. Phase-by-phase solo viability

### Phase A: Now → soft launch + first 10 customers (Apr → Sept 2026, ~€1K–3K MRR)

Completely realistic solo. The 8-week launch plan in the market-readiness doc is sized for one person. Support volume at 0–10 customers is near zero, sales is 30-minute founder-led demos to warm contacts, and the engineering work left is mostly polish plus the Stripe integration.

**Risk here isn't workload, it's pacing.** Hard-cap 50 hours/week or you'll arrive at launch already tired. Pre-commit to a 4-day week the week after soft launch (already in the 8-week plan).

### Phase B: 10–30 customers (Sept 2026 → Mar 2027, ~€3K–8K MRR)

Still solo-viable, but the shape of the work changes. Support becomes recurring (call it 15 conversations/week), sales calls compound with existing-customer expansion calls, and churn defense kicks in — at 3% monthly you're losing one customer/month, so one new customer/week is just treading water.

The only way this stays manageable is if the *self-serve muscle* is strong:

- Email/Slack digest shipping on time so customers don't need the dashboard daily
- Docs site genuinely answering most questions without a human
- Aggressive "no" to custom work and one-off integrations

Skip any of those and you're drowning by 20 customers.

### Phase C: 30–80 customers (Mar → Sept 2027, ~€8K–15K MRR)

**This is the break point.** It's technically possible to stay solo but the cost is your health and your quality. Historical pattern for bootstrapped SaaS: solo founders at this tier work 70–80 hour weeks and churn rises because bugs sit in the queue.

Concretely, this is where you want:

- Part-time customer success person (€2–3K/month, remote, ~20h/week)
- Fractional marketing/content contractor (€1–2K/month retainer) for the SEO content pipeline

Both are well under what adding a full engineer costs, and both unlock the highest-leverage founder time (product + sales).

### Phase D: 80+ customers / €15K+ MRR (Sept 2027 onward)

**Solo stops working.** You need at minimum a part-time support person and a contract engineer.

The Month 24 financial projection in `business_plan.md` (500 customers, €55K MRR) is only reachable with a team of 3–4 — founder + engineer + customer success + fractional marketing. Continuing solo past this point actively shrinks the business: customer quality suffers, roadmap slips, churn compounds.

---

## 2. Hiring order when the time comes

| # | Role | Trigger | Monthly cost | Why this one first |
|---|---|---|---|---|
| 1 | Part-time customer success (20h/week, remote) | ~20 paying customers, or support queue > 8h wait | €2–3K | Frees founder from ticket triage — highest leverage on founder time |
| 2 | Contract / fractional engineer | ~€10K MRR, or roadmap commitments start slipping | €3–5K | Continuous delivery of detection rules, Azure/GCP, feature polish |
| 3 | Fractional marketing / content writer | SEO cadence slipping for 3+ weeks | €1–2K retainer | Unblocks the content engine the business plan depends on — often should be first in practice |
| 4 | Second engineer (FTE or senior contract) | ~€20K MRR | €5–8K | Parallel workstreams: one on core product, one on multi-cloud |
| 5 | Full-time customer success lead | ~€25K MRR or >100 customers | €4–6K | Replaces part-time CS, owns onboarding + retention |

**Order note:** #3 (marketing) is listed third but in practice is often the right *first* hire. If you're consistently skipping content for product work, a €1,500/month retained writer pays for itself in pipeline within 60 days.

---

## 3. Signals that say "hire now, don't wait"

Any one of these sustained for more than 2–3 weeks is a hire trigger, not an edge case to power through:

- You've worked >55 hours/week for 4 consecutive weeks
- A paying customer has had a bug unresolved for >2 weeks
- You're skipping the content pipeline for 3+ weeks in a row
- Demo requests are being rescheduled because you're in support triage
- You feel relief when a customer churns (classic burnout signal)
- You're saying "I'll get to it next week" to yourself more than twice a week
- Your sleep or exercise has materially degraded for 3+ weeks

The last two are early warning signs; the first five are already-too-late signs.

---

## 4. AxiaOps-specific factors

### Factors that *help* you stretch solo longer

- **Automation-heavy product** — scans run themselves; low-touch once activated
- **B2B ICP** — support can be business hours, not 24/7
- **MSP segment** — fewer, larger customers vs. B2C's ticket firehose
- **Clean codebase** — 44% test LOC ratio, Go workspace, RLS done right; you ship fast when you do ship
- **Detection engine already broad** — 15 AWS resource types covered; marginal cost of adding #16 is small

### Factors that *shorten* the solo runway

- **AWS API churn** — new services and API changes are continuous engineering work, not one-off
- **Multi-cloud is not a solo project** — Azure/GCP (Phase 4) is too big for a solo founder while doing everything else; plan the first engineer hire around this milestone explicitly
- **German UG/GmbH compliance overhead** scales with revenue — invoicing, VAT, bookkeeping, AGB updates, GDPR obligations. Steuerberater from day one is non-negotiable (already in the business plan)
- **MSP tier complexity** — white-label branding, reseller dashboards, partner invoicing add non-trivial surface area; each MSP wants slightly different reporting

---

## 5. Pre-commitment (the important part)

The failure mode is waiting until you're drowning to hire. By then you're a terrible interviewer, onboarding eats the time you don't have, and the first hire doesn't stick.

**Concrete pre-commitment to make today:**

1. Budget a €2–3K/month customer success contractor in the financial model starting March 2027 — whether or not you "need" one yet.
2. Start identifying candidates **three months before** the trigger. Short list of 5 freelancers / fractional people by December 2026. LinkedIn searches, FinOps Foundation community, r/msp, personal network referrals.
3. Write the first hire's job description **now**, before you need it. Revisit quarterly. If the role is unclear, the hire will fail.
4. Set a personal red-line: if you hit any two of the "hire now" signals in §3 for a full month, the hire is non-negotiable — no more postponing.
5. Track a weekly "founder time allocation" journal (15 minutes on Friday). Categories: engineering, sales, support, content, ops, admin. If support + admin exceed 40% for 4 weeks running, that's the trigger — that time should be bought, not spent.

---

## 6. What *not* to hire for (yet)

Avoid these hires until well past €20K MRR. They are common founder mistakes.

- **Full-time senior engineer (FTE) before €15K MRR.** Burn rate risk outweighs velocity gain; contract engineers are more flexible.
- **Sales rep / BDR.** Founder-led sales is the right motion through at least 50 customers. A BDR hired too early will churn and leave you with a broken pipeline.
- **Designer FTE.** One-off design sprints (€3–5K for a landing page redesign, €2K for a dashboard polish sprint) are better than a full-time designer until product-market fit is rock-solid.
- **Head of marketing / growth.** A fractional contractor + founder-led content is cheaper and better until ~€30K MRR.
- **Operations / chief of staff.** You are the ops until you physically cannot be. If you hire an ops person, you're not running a startup anymore — you're running an agency.

---

## 7. Recommendation

**Budget for a fractional customer success or content hire in March 2027** (roughly month 9 post-soft-launch) regardless of whether you feel you "need" one. Pre-commit in the financial model so the hiring decision is a trigger, not a panicked reaction.

**Solo past summer 2027 is achievable but costly.** Solo past fall 2027 is a bad trade — you're leaving revenue on the table and risking burnout. The right frame is: the first hire isn't a cost, it's unlocking founder time at the highest-leverage activities (product direction + sales + strategic content), and that unlock more than pays for itself within 90 days if the hire is right.

**Most important meta-point:** the business plan's Month 24 target (500 customers, €55K MRR) is a *team* milestone, not a solo one. If you're still solo at month 18, you've either massively outperformed the model (in which case hire immediately with the excess cash) or fallen short (in which case the lack of team is *why*). Either way, the hiring conversation needs to be live by Q2 2027.

---

*This document is a personal operating plan, not a public HR policy. Revisit quarterly. Trigger a hiring conversation earlier than planned if the signals in §3 fire — they almost always will.*
