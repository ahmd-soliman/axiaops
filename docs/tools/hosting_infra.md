# Hosting & Infrastructure — Free / Cheap Tools

## Backend Hosting

### Fly.io (Recommended)
Deploy the Go API as a Docker container. Generous free tier, no credit card required to start.

**Free tier:**
- 3 shared VMs (256MB RAM each)
- 3GB persistent volume storage
- 160GB outbound transfer/month
- Free TLS certificates (auto-managed)
- Runs in Frankfurt by default (EU data residency — important for German customers)

**Paid (when needed):** $1.94/month for a shared-cpu-1x 256MB VM. Scale to $5–10/month for a real workload.

```bash
fly launch          # create app
fly deploy          # deploy from Dockerfile
fly secrets set DATABASE_URL=...
```

---

### Railway (Alternative)
Similar to Fly.io. Good developer experience.

**Free tier:** $5 free credit/month (roughly enough for a low-traffic service).

**When to choose Railway over Fly:** Slightly simpler setup for beginners; better UI for managing environment variables and logs.

---

### Hetzner VPS (Best Value for Money)
If you prefer full control, a Hetzner CX22 (2 vCPU, 4GB RAM, 40GB SSD) costs **€4.51/month** in Germany. Run Docker Compose directly.

**Best for:** Self-hosted GitLab Runner + staging environment on the same VPS.

---

## Database

### Supabase (Recommended)
Managed PostgreSQL with a generous free tier. Also provides Auth, Storage, and Edge Functions.

**Free tier:**
- 500MB database storage
- 2GB file storage
- 50,000 monthly active users (Auth)
- 500K Edge Function invocations
- Pauses after 1 week of inactivity (free tier only — resumable instantly)

**Paid:** $25/month Pro plan when you need uptime guarantees.

---

### Neon (Alternative)
Serverless PostgreSQL. Scales to zero — no cost when idle.

**Free tier:**
- 0.5GB storage
- 1 project, 10 branches
- Auto-suspend after 5 minutes of inactivity

**Best for:** Development and staging databases — free and never charges for idle time.

---

## DNS & Domains

| Provider | Cost | Notes |
|----------|------|-------|
| **Cloudflare Registrar** | At-cost pricing (~€10/year for .com) | No markup on domain renewals |
| **Cloudflare DNS** | Free | Fast, DDoS protection, SSL included |
| **Porkbun** | ~€9/year for .com | Good alternative, cheap renewals |

**Recommendation:** Register the domain at Cloudflare Registrar and use Cloudflare DNS. Free SSL, free DDoS protection, free caching — no extra setup needed.

---

## TLS / SSL

- **Fly.io** — auto-managed, free via Let's Encrypt
- **Cloudflare** — free SSL at the edge
- **an edge proxy** (if self-hosting) — automatic HTTPS, free

---

## Object Storage (for billing CSV uploads)

| Provider | Free Tier | Cost After |
|----------|-----------|-----------|
| **Cloudflare R2** | 10GB storage, 1M reads/month free | $0.015/GB — no egress fees |
| **Backblaze B2** | 10GB free | $0.006/GB |
| **AWS S3** | 5GB for 12 months | $0.023/GB |

**Recommendation:** **Cloudflare R2** — no egress fees (AWS S3 charges per GB downloaded), generous free tier, S3-compatible API so your Go code works with both.

---

## Monitoring & Observability (Free Tier)

| Tool | What It Does | Free Tier |
|------|-------------|-----------|
| **Grafana Cloud** | Metrics, logs, traces | 10K series, 50GB logs/month free |
| **CloudWatch** | Logs, metrics (via App Runner) | Included with App Runner |
| **UptimeRobot** | Uptime monitoring + alerts | 50 monitors, 5-min checks free |
| **Betterstack** | Uptime + logs + on-call | Free tier available |

**Minimum viable stack:** CloudWatch logs (free via App Runner) + Prometheus/Grafana (free tier) + UptimeRobot (free).

---

## Email (Transactional — for alerts and digests)

| Provider | Free Tier | Notes |
|----------|-----------|-------|
| **Resend** | 3,000 emails/month, 100/day | Best developer experience, React Email support |
| **Brevo (Sendinblue)** | 300 emails/day | Good free tier, EU-based (GDPR) |
| **Postmark** | 100 emails/month free | Best deliverability |

**Recommendation:** **Resend** for transactional (alerts, digests). EU-based customers → consider **Brevo** for GDPR compliance clarity.
