# Tax Strategy — Germany (AxiaOps)

> Disclaimer: This is a strategic overview, not legal advice. Engage a Steuerberater
> (tax advisor) familiar with SaaS and holding structures before acting on any of this.

---

## The Core Structure and Why It Matters

```
You (Ahmed) — natural person, 100% shareholder
        |
  Holding GmbH
  (Receives dividends, holds shares, receives exit proceeds)
        |
  Operating UG → GmbH (once revenue justifies it)
  (Runs the business, owns IP, signs contracts, pays salaries)
```

This structure exists for one reason: **Germany taxes individuals heavily, but companies taxing other companies is far cheaper.**

---

## The Exit Tax Saving (The Big One)

When AxiaOps is acquired for €5M:

### Without the holding structure (you own the UG directly as an individual)
- Sale proceeds land on your personal tax return
- Capital gains tax (Abgeltungsteuer): **25% + Soli = ~26.375%**
- On €5M exit: **~€1.32M in tax**
- You keep: **~€3.68M**

### With the holding structure (Holding GmbH owns the UG)
- The Holding GmbH sells its shares in the Operating UG
- Under §8b KStG (Körperschaftsteuergesetz): **95% of the gain is tax-exempt** for a corporation selling shares in another corporation
- Effective tax rate on the gain: **~1.5%**
- On €5M exit: **~€75,000 in tax**
- You keep: **~€4.925M**

**Difference: ~€1.245M saved.** That is why you set up the holding before writing a single line of production code.

The catch: money is now inside the Holding GmbH. To get it out personally, you pay dividend tax (Abgeltungsteuer ~26.375%) — but you control the timing. You can invest it inside the holding, buy real estate through it, or draw it down over years at lower effective rates.

---

## VAT (Umsatzsteuer) — SaaS Specifics

### Registration
- Register with your local Finanzamt for VAT as soon as the UG is founded
- Apply for a **USt-IdNr** (VAT ID) — required for B2B invoicing in the EU

### B2B EU Sales (Reverse Charge)
- If your customer is a business in another EU country with a valid VAT ID:
  - You **do not charge VAT**
  - You state "Reverse Charge — §13b UStG" on the invoice
  - The customer self-accounts for VAT in their country
  - This is the standard for most SaaS B2B EU sales

### B2C EU Sales (OSS)
- If selling to individuals (consumers) in other EU countries:
  - You must charge VAT at the **customer's country rate**
  - Register for the **OSS (One-Stop-Shop)** scheme via the Bundeszentralamt für Steuern
  - File one quarterly OSS return instead of registering in every EU country

### Non-EU Sales (US, etc.)
- Generally outside German VAT scope
- No German VAT charged on US B2B customers
- Check local rules if revenue from US becomes significant (US sales tax is state-by-state)

### Kleinunternehmerregelung (Small Business Rule)
- If annual revenue stays **below €22,000** in year 1 and estimated below €50,000 in year 2:
  - You can opt out of VAT (no charging, no reclaiming)
  - Simpler, but means you **cannot reclaim VAT** on your expenses (hosting, tools, etc.)
- **Recommendation:** Do NOT use Kleinunternehmer if you expect to grow. Register for VAT from day one. The input tax reclaim on AWS bills, software licenses, and contractor invoices is worth it.

---

## Corporation Tax on Operating Profits

The Operating UG/GmbH pays:
- **Körperschaftsteuer (KSt):** 15%
- **Solidaritätszuschlag:** 0.825% (5.5% of KSt)
- **Gewerbesteuer (trade tax):** ~14–17% depending on municipality

**Total effective corporate tax rate: ~28–30%**

Compare to your personal income tax rate as an employee: up to **42% (+ Soli)**

This means retaining profits inside the company and reinvesting them is more tax-efficient than paying yourself a large salary.

### Optimal Salary Strategy (Employed + Founder)
While still at [redacted]:
- Pay yourself a **minimal managing director salary** from the UG (e.g., €1,000–€2,000/month)
- Keep profits inside the UG
- This avoids pushing your total income into the 42% bracket
- Once you leave [redacted], reassess — you'll need a market-rate salary for pension contributions

---

## Gewerbesteuer (Trade Tax)

- Levied by municipalities, rate varies (~14–17% effective)
- **Frankfurt:** ~16.1% | **Berlin:** ~14.35% | **Munich:** ~17.15%
- If you found the UG in a lower-tax municipality, this matters at scale
- Many founders register in Berlin for this reason (lower Gewerbesteuer + startup ecosystem)

---

## Pension and Social Security

As a GmbH managing director (Gesellschafter-Geschäftsführer) with >50% shares:
- You are **not subject to mandatory German pension insurance (DRV)**
- You must arrange **private pension / Altersvorsorge** yourself
- Options: ETF portfolio inside the holding, private Rürup pension (tax-deductible), real estate

This is actually an advantage — you control where your retirement savings go, rather than paying into the state system.

---

## Key Dates and Actions

| Action | When | Cost (approx.) |
|--------|------|---------------|
| Found Holding GmbH | Before any coding in "production" capacity | €1,000–€2,000 notary + Steuerberater |
| Found Operating UG | Same time | €500–€1,000 |
| IP Assignment Agreement | Immediately after founding | Included with Steuerberater setup |
| Register for VAT (USt-IdNr) | Before first invoice | Free |
| Register for OSS (if selling B2C EU) | Before first B2C sale | Free |
| Annual financial statements (Jahresabschluss) | Within 6 months of fiscal year end | €1,500–€3,000/year |
| Gewerbesteuererklärung | Annually | Included with Steuerberater |

---

## Recommended Steuerberater Profile

Look for a Steuerberater who:
- Has experience with **GmbH/UG holding structures**
- Works with **SaaS or tech startups**
- Understands **international SaaS VAT (OSS, reverse charge)**
- Is comfortable with **founders who are also employees elsewhere**

Networks to find one:
- **Gründerszene** advisor directory
- **DATEV-Beratersuche** (official search)
- Ask in Slack communities: **FoundersBerlin**, **German Startup Jobs**
- **Lexware / DATEV** certified advisors familiar with SaaS

**Budget:** €2,000–€4,000 to set up the structure correctly. €1,500–€3,000/year ongoing. Worth every cent given the €5M exit scenario.

---

## Summary: Priority Order

1. **Found the Holding GmbH + Operating UG** — before any public launch or revenue
2. **Sign the IP assignment agreement** — transfer code to the UG
3. **Register for VAT** — before the first invoice
4. **Set a minimal salary** — keep profits in the company while at [redacted]
5. **Engage a Steuerberater** — do not DIY the annual filings
6. **Plan the exit structure early** — the holding only saves tax if it owns the shares *before* the exit
