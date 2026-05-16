# Funding Strategy — AxiaOps

> Companion to `business_plan.md` and `tax_strategy.md`. Covers how AxiaOps should be financed from pre-revenue through exit, and whether venture capital belongs in that picture.

---

## TL;DR

**AxiaOps should be bootstrapped.** The target €5M exit in 3–5 years, the ~€3K MRR breakeven, the minimal infra cost (€24–34/mo), and the Holding GmbH + Operating UG structure are all aligned with a founder-owned, non-dilutive path. Taking VC money would damage the exit math for the founder and misalign with what VCs need to return a fund. The realistic funding stack is: founder capital (~€10–20K) + revenue + potentially one non-dilutive grant. VC is an option of last resort, not a default.

---

## What the Business Plan Actually Needs Funded

Before talking about sources, anchor on the capital requirement. The business plan and tax strategy make this small.

| Cost bucket | Amount | Timing |
|---|---|---|
| Holding GmbH + Operating UG setup (notary, Steuerberater) | €2,000–€4,000 | Pre-launch (target Aug 2026) |
| UG minimum share capital | €1 (practically €500–€1,000 for buffer) | At founding |
| Hosting (App Runner + RDS + CloudFront) | €24–€34/month | Ongoing from Phase 2 |
| Tools (GitHub, monitoring, Kinde free tier, domain) | ~€50–€150/month | Ongoing |
| SOC 2 Type II audit (Q4 2027) | €15,000–€25,000 (one-off) | Only when paying customers justify it |
| Part-time contractor (design, content, or eng help) | €0–€2,000/month | From ~€5K MRR onward |
| Marketing experiments (paid, Product Hunt, sponsorships) | €0–€1,000/month | Discretionary |
| Annual Jahresabschluss + tax filings | €1,500–€3,000/year | Annual |

**Pre-revenue cash need:** ~€5,000–€10,000 to incorporate, run 12 months of infra + tools, and survive the first invoice to first paying customer gap.

**Revenue-to-breakeven cash need:** ~€10,000–€20,000 total to cover the gap between launch (Oct 2026, first 5 beta users at €500 MRR) and ~€3K MRR breakeven (projected between Month 9 and Month 12).

This is a rounding error by VC standards. It's covered by a single quarter of a salaried role or a modest personal loan.

---

## Why VC Is the Wrong Default Here

Venture capital is a specific instrument with specific requirements. It's worth being explicit about why AxiaOps doesn't match them.

### The exit math breaks

A Seed VC typically wants ~10x their money at a 10–20% ownership stake. Plug in the business plan's target:

- Target exit: **€5M**
- Typical Seed round: €500K–€1.5M for 10–20% of the company
- Exit proceeds to investor at 15% ownership: **€750K**
- Proceeds to founder after dilution: **€4.25M pre-tax** (vs. €5M bootstrapped)

Then apply the tax layer from `tax_strategy.md`:

- Bootstrapped + Holding GmbH: ~€4.925M to the founder after tax
- VC-funded + Holding GmbH: ~€4.19M to the founder after tax (15% dilution)
- VC-funded, no holding: ~€3.13M after personal capital gains tax

The Holding GmbH structure already does more for the founder's take-home than a Seed round does for the business. You'd raise €1M to lose €750K at exit while the Holding structure saves €1.24M for free.

### The market size doesn't support a VC return

The business plan's SOM is ~€55K MRR (€660K ARR) at Month 24 with 500 customers. Even at a best-case 10x revenue multiple, that's a €6.6M company. A fund that wrote a €1M check needs a 20–30x outcome on that check to move the needle at fund level. AxiaOps's honest ceiling isn't that, and pretending it is corrupts the product decisions (forces international hiring, premature enterprise push, land-grab pricing).

### The operational tempo is wrong

VC money comes with a clock. The business plan's pacing — solo founder through ~30–80 customers, then reassess hiring — is incompatible with the "grow 3x YoY or die" expectation that follows a Seed round. Once you take institutional money you no longer get to choose "steady and profitable" over "big and risky."

### The product doesn't need it

Nothing in the Phase 1–3 roadmap requires capital that revenue + founder savings can't cover. The only item on the roadmap that could justify outside money is the SOC 2 audit (€15–€25K), and by the time that's needed (Q4 2027) the MRR projections have it covered.

---

## When VC Could Make Sense (Narrow Cases)

There are honest scenarios where raising changes from "bad idea" to "worth considering":

1. **A credible strategic competitor (Vantage, Unusd) starts shipping MSP features fast.** Time-to-market becomes the moat and you need a full-time team next quarter, not in 18 months.
2. **An acquirer signals interest early at a much higher price (€15M+).** The exit math flips; at a €15M exit, a €1M Seed raised at a €4M valuation costs ~€3M in dilution but unlocks growth that might have been impossible solo.
3. **The pre-deployment simulation vision (Phase 5) turns out to be the real product.** That is a land-grab, developer-tooling, possibly-platform play. It probably does need VC. But you learn that only after the reactive product is working.

None of these are the base case. They're contingency triggers.

---

## The Recommended Funding Stack (In Priority Order)

### 1. Founder capital — primary source

Plan on contributing **€10,000–€15,000** of personal capital to the Operating UG as a shareholder loan (Gesellschafterdarlehen) rather than additional share capital. A shareholder loan is cleaner: it's repayable from revenue once cashflow permits, doesn't dilute you, and the interest (if any) is deductible at the UG level. Document it properly — your Steuerberater will draft the contract.

Structure:
- €500–€1,000 as UG share capital (€1 is the legal minimum but €500+ looks credible to customers and banks)
- €10,000–€15,000 as a shareholder loan to the Operating UG, callable when MRR > €5K
- Any additional contributions documented the same way

### 2. Revenue — the real growth capital

The business plan's path to €5K MRR (Phase 1) is what actually funds the company. Every €1K in MRR kills one hypothetical line of a funding pitch deck. The only milestone that meaningfully changes the funding calculus is **€5K MRR**, because past that point:

- Hosting and tools are covered
- The founder can pay a part-time contractor
- Revenue-based financing becomes available (see below)
- Banks will consider a small credit line

Get to €5K MRR before considering any outside capital. If you can't, no external money fixes that — it just buys time to keep making the same mistake.

### 3. Non-dilutive sources — the real alternative to VC

Germany has a strong grant ecosystem for early-stage tech, and none of it costs equity.

| Source | Amount | Fit |
|---|---|---|
| **EXIST-Gründerstipendium** | ~€2,500/mo + €30K material costs for 12 months | Very strong fit if pursued from a university affiliation. Requires an academic sponsor. Best if claimed before founding the UG. |
| **EXIST Forschungstransfer** | Up to €250K over 18 months | Harder; requires research origin. Probably not applicable. |
| **Berlin Startup Scholarship / Gründerstipendium NRW** | €2,000–€3,000/mo for 12 months | State-level equivalents. Check based on registered address. |
| **INVEST Zuschuss für Wagniskapital** | 20% of a business angel's investment reimbursed to the angel | Sweetener that makes angel checks easier. Not money to you directly. |
| **go-digital / Digital Jetzt** | Up to €50K for digital transformation projects | Usually aimed at buyers of digital services, not sellers. Limited fit. |
| **Horizon Europe / EIC Accelerator** | €0.5M–€2.5M grant + up to €15M equity | Strong for deep-tech. AxiaOps probably doesn't qualify — not novel enough research. |
| **Gründungszuschuss (Arbeitsagentur)** | ~€1,400/mo for 6 months, extendable to 15 | Available if leaving a salaried role to go full-time on the company. **Very aligned with the Phase 1 path.** Worth investigating before quitting any current job. |

**The two to pursue seriously:** EXIST (if any university affiliation is plausible) and Gründungszuschuss (when transitioning from employment to full-time founder). Together they could cover 12–15 months of founder living costs, which is far more useful than VC money at this stage.

### 4. Revenue-based financing — once MRR is real

Once you're at €5K+ MRR with low churn, RBF providers (Pipe, Capchase, Wayflyer, and EU-focused equivalents like Re:cap or Viceversa) will advance 6–12 months of MRR in exchange for a flat fee (~5–15%). No equity, no personal guarantee, repayment tied to revenue. This is the right tool to fund SOC 2, a strategic hire, or a paid acquisition experiment — not to fund ideation.

Rule of thumb: don't touch RBF until you have 6 months of stable or growing MRR with monthly churn under 3%. Before that, the repayment burden can kill you.

### 5. Angel investors — only if strategic

If you take any external equity, prefer a single strategic angel over a syndicate or VC. Good profile: a FinOps Foundation ambassador, an MSP founder who exited, a former Vantage/CloudHealth exec. What you want from them is distribution and credibility, not the money. Typical structure: €25K–€100K for 1–3% via a SAFE or equivalent German convertible (Wandeldarlehen). Cap it tight. An angel who owns 3% at a €5M exit takes €150K; that's survivable. One who takes 10% is the same problem as a VC with worse terms.

**Use INVEST Zuschuss** to make this more attractive: the Federal Office for Economic Affairs refunds 20% of the angel's investment to the angel. Makes small checks from German angels materially more viable.

### 6. Bank debt — later, and small

Once the UG has 2+ years of filed accounts and positive cashflow, a Hausbank will extend a small credit line (€10K–€50K) to smooth working capital. Useful for bridging customer payment delays or funding a one-off expense. Not a growth tool. Requires personal guarantee in practice, despite the UG's limited liability in theory.

---

## What VCs Would Actually Look Like (If You Ignore The Above)

For completeness, if you do decide to raise a Seed against the advice in this doc, the realistic landscape is:

### EU FinOps / DevTools VCs

| Firm | Fit | Check size |
|---|---|---|
| **Point Nine Capital** (Berlin) | Strong SaaS focus, historically B2B infrastructure. Invested in Zendesk, Algolia, Loom. Good warm intro network in Berlin. | €500K–€2M Seed |
| **Cherry Ventures** (Berlin) | Early stage, operator-led. Will look at solo founders. | €500K–€3M |
| **Speedinvest** (Vienna/Berlin) | Pre-seed and Seed, strong SaaS thesis, explicit fintech/infra focus. | €300K–€1.5M |
| **Earlybird** (Berlin/Munich) | Later stage for Seed but strong DevTools thesis. Better fit for Series A. | €1M–€5M |
| **Project A** (Berlin) | Operator-led fund, hands-on. Has done infra SaaS. | €500K–€3M |
| **HV Capital** (Munich) | Growth-stage primarily, but has Seed arm. | €500K–€2M |
| **BlueYard Capital** (Berlin) | Thesis-driven, developer tooling friendly. | €250K–€1M |

### US VCs (unlikely warm intro path from Germany, but relevant if targeting US MSPs)

| Firm | Fit | Check size |
|---|---|---|
| **Andreessen Horowitz (a16z) — Infra fund** | Invested in Vantage. Unlikely to back a direct competitor unless the thesis is strongly differentiated. | $1M–$5M |
| **Battery Ventures** | FinOps track record (CloudHealth). | $2M–$10M |
| **Accel** | DevTools-heavy, but Seed rarely lead from EU. | $1M–$5M |
| **Kleiner Perkins** | Enterprise SaaS focus. | $1M–$5M |
| **Redpoint** | Infra and FinOps experience. | $1M–$5M |

### Reality check

Any firm on either list will want to see:

1. Clear path to €10M+ ARR within 5 years (the business plan does not project this; the honest number is ~€1M ARR)
2. Founder willing to go full-time immediately (currently Phase C per `hiring.md` — not now)
3. At least a credible MVP with ~10 paying customers, or a team of 2+
4. A total addressable market positioned as €1B+ (the business plan honestly puts SAM at ~50K companies × €110/mo = ~€66M/yr, which is a TAM for a small SaaS, not a VC-sized story)

**You would have to re-pitch the company as something bigger than what the business plan says it is.** That's a red flag, not a strategy.

---

## Decision Framework

Answer these, in order, and the funding path falls out.

**1. Can you personally cover €10–15K to incorporate and run 12 months of infra + tools?**
- Yes → bootstrap. Go to step 2.
- No → apply for Gründungszuschuss first; if denied, reconsider timing of launch.

**2. Are you leaving a salaried job to go full-time?**
- Yes → apply for Gründungszuschuss (Arbeitsagentur) before resigning. That's 6–15 months of founder living costs, non-dilutive.
- No → continue earning, keep burn to zero, ship nights/weekends as currently planned.

**3. Is there any university/research affiliation available?**
- Yes → apply for EXIST-Gründerstipendium. It's 12 months of €2,500/mo + €30K of materials. Almost no strings.
- No → skip.

**4. Do you have 6 months of MRR > €5K with <3% churn?**
- Yes → consider RBF for one specific purchase (SOC 2, a hire, a campaign). Still no equity.
- No → do not take on any financing. Keep growing.

**5. Has a credible acquirer or strategic partner signaled serious interest at >€10M valuation?**
- Yes → reconsider angel or Seed, but only with that specific exit story in mind.
- No → stay the course. Default to bootstrap.

---

## What To Put In Front Of The Cap Table Right Now

Before any funding conversation happens, even with an angel you've known for 10 years, these must exist:

1. **Holding GmbH founded**, Operating UG founded, IP assignment signed. Target: August 2026 per the business plan. No exception.
2. **Shareholder loan agreement** documenting any founder capital contributions (avoid it being treated as gifts or undeclared equity).
3. **Clean cap table** — 100% founder ownership, no handshake deals, no verbal promises of equity to friends who "helped."
4. **A written founder vesting schedule on your own shares** (4 years, 1-year cliff). Feels silly as a solo founder — it's not. An acquirer will demand it, and retrofitting is painful.
5. **Clear commit history** — all code authored from personal accounts before incorporation is assigned to the UG via the IP assignment. Acquirers dig into this.

`tax_strategy.md` covers items 1–2. Items 3–5 are funding-hygiene and belong here.

---

## Summary

The €5M exit target, the Holding GmbH structure, and the €3K MRR breakeven together tell one story: this is a bootstrap-first, founder-owned SaaS. Funding equals revenue plus a modest amount of founder capital plus, if you're lucky on the grant lottery, one non-dilutive tranche of living-cost support. Venture capital would require re-pitching the company as something it's not designed to be, and would cost more at exit than it could plausibly buy in growth. Keep it boring. Keep it yours.
