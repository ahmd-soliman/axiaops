# Languages — Why Go, and Can You Mix?

## The Practical Split

```
axiaops/
├── worker/        → Go      (billing ingestion, idle detection, AWS polling)
├── api/           → Go      (REST API — stays fast and lean)
├── frontend/      → React   (Next.js — TypeScript)
└── scripts/       → Python  (mock data generator, one-off AWS queries)
```

Each language does what it's best at. No single language owns everything.

---

## Go vs Java vs Python for AxiaOps

### Performance & Resource Cost

| | Go | Java | Python |
|--|-----|------|--------|
| Memory (idle API) | ~15MB | ~150–300MB | ~50–80MB |
| Startup time | ~10ms | ~2–5s (JVM) | ~200ms |
| Fly.io free tier fit | Easily | Struggles (256MB RAM) | Borderline |

Fly.io's free tier gives 256MB RAM per VM. A Spring Boot app often won't even
start within that. A Go binary runs comfortably at 15–20MB.

### Concurrency — Critical for AxiaOps

AxiaOps's core worker needs to ingest billing CSVs, poll multiple AWS accounts in
parallel, and process resource checks concurrently. Go's goroutines handle
thousands of concurrent operations with minimal overhead.

```go
// Fan out across 50 AWS accounts simultaneously — trivial in Go
for _, account := range accounts {
    go processAccount(account, results)
}
```

Python needs async/threading complexity to do the same. Java can do it but
requires more boilerplate (thread pools, executors).

### Deployment Simplicity

| | Go | Java | Python |
|--|-----|------|--------|
| Output | Single binary | JAR + JVM | Interpreter + venv |
| Docker image size | ~10MB | ~200–400MB | ~100–200MB |
| Dependencies on host | None | JVM required | Python + packages |

Go compiles to a single static binary. Dockerfile becomes:

```dockerfile
FROM scratch
COPY axiaops /axiaops
CMD ["/axiaops"]
```

### Where the Others Win

**Python is better if:**
- You need fast data science / ML integrations (pandas, numpy)
- Your team already knows Python deeply
- You need rich AWS SDK tooling — boto3 is the best AWS SDK on the market

**Java is better if:**
- You come from a Java background and know Spring Boot well
- You need the JVM ecosystem (mature libraries, enterprise tooling)
- Team size and long-term type safety matter more than resource footprint

---

## Why Go Wins for AxiaOps

| Reason | Detail |
|--------|--------|
| **Free tier hosting** | Fits in 256MB easily; Java often doesn't |
| **Billing CSV processing** | Handles large file ingestion fast and cheaply |
| **Concurrent AWS polling** | Goroutines are purpose-built for this |
| **Single binary deployment** | Simplest possible CI/CD pipeline |
| **Cloud tooling** | AWS, GCP, Azure all have solid Go SDKs |
| **Long-term cost** | Smaller containers = lower hosting bill at scale |

**The one strong argument for Python:** boto3 (AWS SDK for Python) is
significantly more mature and better documented than the AWS Go SDK.
Use it in scripts, not in the core service.

---

## Mixing Languages — How It Works

### Where Each Language Belongs

**Go — the core**
- Billing CSV ingestion and processing
- Concurrent AWS/Azure/GCP account polling
- REST API serving the dashboard
- Performance-sensitive, long-running workers

**Python — supporting scripts**
- Mock data generator (boto3 is the best AWS SDK)
- One-off data migration scripts
- Exploratory queries against billing data
- Glue scripts and automation

**TypeScript — frontend**
- Web dashboard (Next.js)
- No debate here — JavaScript ecosystem owns the frontend

### How They Communicate

They don't share code — they talk over standard boundaries:

| Method | When to use |
|--------|------------|
| **REST API** | Frontend talks to Go API |
| **PostgreSQL** | Python scripts write data, Go reads it |
| **Files / S3** | Python generates CSV, Go ingests it |
| **Message queue** | Async jobs later (Redis, SQS) |

### What to Avoid

- Don't mix languages within the same service — one language per service
- Don't use Python for the hot path (API, worker) — defeats the performance advantage
- Don't over-engineer early — start with Go + TypeScript, add Python scripts only when boto3 or data processing makes it the obvious choice

---

## Verdict

Start with two languages: **Go (backend) + TypeScript (frontend).**
Add Python in a `/scripts` folder as needed. Each lives in its own directory,
communicates over standard interfaces, and is deployed independently.
