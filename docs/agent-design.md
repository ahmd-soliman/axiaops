# AxiaOps Agent — Design

**Status:** Proposed (RFC, v0.2) | **Last Updated:** May 2026 | **Owner:** Platform / Architect

This document proposes a third deployment SKU for AxiaOps — the **Agent** — alongside the
existing pure-SaaS and full-self-hosted shapes. The Agent is a small, customer-side process
that performs cloud scanning locally and ships only aggregated results to AxiaOps' SaaS api.
It solves a class of enterprise procurement objections that neither pure SaaS nor full
self-hosted addresses cleanly, and it generalises to multi-account / multi-cloud trivially
because credentials never leave the customer's perimeter.

This document defines the architecture; implementation lands in separate phased tickets.

> **v0.2 changes (post-architect review):** §4.1 framing tightened — Agent depends on
> a `Scanner` abstraction that does not exist today and must be extracted first (see §3.5,
> §4.1, §8 Precondition). §4.3 redesigned to consume the existing `services/shared/queue/`
> Queue interface instead of a parallel long-poll pattern. §4.4 expanded — result upload
> is six model collections plus account-lifecycle state machine, not "one call." §5
> rewritten — Agent JWT uses a **separate keypair** (`services/shared/agentauth/`), NOT
> the license keypair. New §4.6 (data model — five new tables). New §4.7 (license
> enforcement moves to SaaS-side). New §4.8 (audit log actor). New §4.9 (observability
> surface). New §4.10 (protocol versioning). §7 expanded with "what crosses the perimeter"
> subsection for GDPR honesty. §8 phasing rewritten — Phase 1 is 10–14 weeks for one dev,
> not 4–8.

---

## 1. Goals & Non-Goals

### Goals

- **Eliminate the "credentials cross the boundary" objection.** Today AxiaOps' SaaS stores
  customer AWS credentials (encrypted with `ENCRYPTION_KEY`) in `accounts.secret_encrypted`
  and decrypts them on each scan in the ingestion service. Enterprise security teams in
  AxiaOps' stated ICP (DACH Mittelstand, regulated industries) frequently veto this. The
  Agent inverts the relationship: credentials live in the customer's perimeter, only
  zombie summaries cross to SaaS.
- **Eliminate the "no inbound from internet to our VPC" objection.** Agent connects
  outbound-only over HTTPS to `api.axiaops.io`. No firewall rules, no VPN, no peering.
- **First-class multi-account.** A single Agent instance can hold credentials for N AWS
  accounts, M Azure subscriptions, K GCP projects — all configured locally, all scanned
  by the same Agent process. The SaaS api side stays unaware of credentials and just
  consumes results.
- **Constrained data residency.** Raw CloudWatch metrics and Describe API responses stay
  inside the customer's region during scan. **Note (§7):** the SaaS DB still holds the
  resource-shaped output (resource IDs, ARNs, tags, dismissal state, audit log) — only
  scan-intermediate payloads stay local. See §7 "What crosses the perimeter" for honest
  scope.
- **Reuse the existing scan logic without forking.** The Agent and the existing ingestion
  binary share the provider/analyzer code via a new `Scanner` abstraction (see §3.5 and
  §4.1). The Scanner is a precondition for Phase 1 and the largest single piece of
  refactor work in the proposal.

### Non-Goals

- **Replacing the SaaS-only SKU.** Customers who are fine with paste-a-role-ARN remain
  on the existing onboarding. Agent is an alternative for customers who reject that
  posture, not a forced migration.
- **Replacing the self-hosted SKU.** Self-hosted is for customers who refuse any SaaS
  relationship at all. Agent is for customers who accept a SaaS dashboard / DB but
  reject SaaS-held credentials. Different buyer.
- **Hosting customer cost / zombie data in the customer's perimeter.** That is what
  self-hosted is for. Agent ships results to AxiaOps' SaaS DB — the dashboard, audit
  log, dismissals, and snapshots are all served from there.
- **Active remediation in this design.** Phase 1 ships a read-only Agent (same surface
  as today's ingestion). Remediation (`terminate`, `resize`, `snapshot+delete`) is
  pre-figured by the name "Agent" (vs. "Collector"; see §6) but is its own
  design exercise — deferred to whatever ticket follows automated-remediation
  prioritisation.
- **Solving multi-region high availability for AxiaOps' own deployment.** Where SaaS
  runs is independent of how customers connect to it.
- **Replacing the existing role-ARN onboarding for SaaS-only customers.** Roles + access
  keys remain the SaaS path. The Agent is an additional path, gated behind an enterprise
  SKU.

---

## 2. Problem Statement

### Today

```
Browser ──► dashboard ──► api ──► postgres
                          │
                          ▼
                     ingestion ──AWS SDK──► AWS APIs (with customer creds from DB)
```

- Customer AWS credentials sit in `accounts.secret_encrypted`, decrypted inside the
  ingestion process at scan time.
- Every scan exits AxiaOps' AWS account (or wherever the SaaS runs) and calls Cost
  Explorer / CloudWatch / Describe APIs in the customer's account.
- The SaaS api is the only thing customers' browsers talk to; ingestion is internal.

### What this fails to satisfy

| Customer concern | Today's posture |
|---|---|
| "Credentials must never leave our infra" | ❌ Credentials decrypt in SaaS ingestion |
| "No inbound from SaaS to our VPC" | ✅ Already outbound-only |
| "Data must stay in our region" | ❌ Cost / metrics flow to SaaS |
| "We need to audit every action taken in our cloud" | ⚠️ AxiaOps emits structured logs but they live in SaaS, not customer SIEM |
| "Procurement blocks SaaS principals on cross-account roles" | ❌ Customer cannot accept the trust relationship |
| "We have 30+ AWS accounts and don't want 30 role ARNs in someone else's DB" | ⚠️ Works but adds blast radius |

The Agent SKU resolves the four ❌ rows and improves the ⚠️ rows.

### What this is NOT solving

- **Generic SaaS resistance.** Customers who refuse a SaaS dashboard regardless go to
  self-hosted, not Agent.
- **Reduced SaaS reliance.** Agent depends on SaaS uptime for the dashboard, audit
  log, dismissals, and scan-job dispatch. If AxiaOps SaaS is down, Agent has no work.
- **Bandwidth / volume problems.** FinOps data volumes are tiny by observability
  standards (kilobytes per scan, not megabytes/second). Agent isn't motivated by
  network economics.

---

## 3. Why Agent (vs. alternatives)

We considered four shapes before landing on Agent. The trade-off table:

| Shape | Customer holds creds? | Inbound to customer? | Multi-cloud scaling | Customer ops burden | Engineering cost |
|---|---|---|---|---|---|
| **Today's SaaS** | No (SaaS holds them) | No | Paste each set into SaaS | None | (existing) |
| **Full self-hosted** | N/A (no SaaS) | No | Configure locally | High (full stack) | (existing) |
| **VPC-peered SaaS** | Yes (creds in customer VPC, SaaS peers in) | Yes (peering link) | Complex (per-cloud peering) | Medium | High (new infra story) |
| **Agent** ✅ | **Yes (in agent's local store)** | **No (outbound HTTPS)** | **Native — one agent, many cred sets** | **Low (one binary)** | **Medium** |

The Agent is the only shape that:
- Keeps credentials customer-side
- Keeps inbound posture clean
- Scales naturally to multi-account / multi-cloud
- Keeps customer ops burden small (one binary, not a full stack)
- Keeps the SaaS dashboard intact (the workflow / audit / dismissal value remains)

### Comparison to other FinOps vendors

No major FinOps vendor (Vantage, CloudHealth, Cloudability, CloudZero, ProsperOps, Spot.io,
Unusd, Yotascale) currently offers an Agent SKU. The universal pattern is cross-account
IAM role assumption, with paste-the-role-ARN UX. The agent/collector pattern dominates in
**adjacent industries**: observability (Datadog, Grafana, OpenTelemetry), security
(CrowdStrike, SentinelOne, Wazuh), CI/CD (GitHub Actions self-hosted runners).

This means **AxiaOps shipping an Agent SKU would be a competitive differentiator** in
FinOps procurement conversations — particularly for the German Mittelstand / DACH-regulated
target customer where "no credentials in the SaaS" is often a hard procurement gate.

The closest precedent in finops space: CloudHealth FlexBridge and Apptio On-Premise
Collector, both narrowly focused on on-prem VMware data — not the cloud-account scanning
use case AxiaOps targets.

### 3.5 Precondition — the `Scanner` abstraction

The existing ingestion binary (`services/ingestion/cmd/main.go:358–747`, `runScan` +
`runIngestionCore`) is not cleanly decomposable into "fetch" vs "persist." It is a
procedural pipeline that interleaves cloud API calls with DB writes:

- `store.GetAccount` (line ~373) and `store.SaveAccount` (line ~387) — back-fill the AWS
  account ID *mid-scan*
- `store.UpsertOrganization` (line ~425) — inside scan logic when org metadata is missing
- `store.Save(costRecords)` (lines ~467, ~496) — between fetch stages, not at the end
- `store.SaveZombies` / `SaveSnapshot` / `SaveSnapshotServices` / `SaveResources` (lines
  ~693–744) — final persistence

The `provider.Provider` interface
(`services/ingestion/internal/provider/provider.go`) is also too narrow: it declares
only `FetchCosts`. The other 13+ calls (`FetchUsage`, `FetchResourceCosts`,
`FetchCostExplorerAPICosts`, `DiscoverXxx` API-only checks at lines ~545–686) are made
directly on the concrete `aws.Client`, bypassing any interface.

Without a refactor, the Agent cannot reuse this code — it would have to fork it or
plumb a Store-like seam into every interleaved write site.

**The required precondition: introduce a `Scanner` type** in
`services/ingestion/internal/scanner/` (proposed location):

```go
type ScanResult struct {
    Account           model.Account
    OrganizationID    string
    CostRecords       []model.CostRecord
    ResourceCosts     []model.CostRecord
    CEAPIRecords      []model.CostRecord
    UsageRecords      []analyzer.UsageRecord
    Zombies           []model.ZombieResource
    Resources         []model.ResourceRecord
    Snapshot          model.ZombieSnapshot
    SnapshotServices  []model.SnapshotService
    DiscoveredAccountID string  // for the line-387 back-fill case
}

type Scanner interface {
    Scan(ctx context.Context, account model.Account) (ScanResult, error)
}
```

The `Scanner` runs end-to-end and returns a self-contained payload. Persistence is the
caller's responsibility — the existing ingestion HTTP handler calls Scanner then writes
to its local `Store`; the new Agent main calls Scanner then POSTs the result to SaaS.

**This refactor is 1–2 weeks of work and a Phase 1 prerequisite.** It also benefits the
existing codebase independently: simpler testability, clearer separation of cloud-call
logic from persistence.

---

## 4. Architecture

### Topology

```
                                                    Customer VPC
                                                   ┌──────────────────────────────────┐
SaaS (AxiaOps)                                     │                                   │
┌──────────────────────┐                           │   ┌──────────────────────────┐    │
│                      │                           │   │   AxiaOps Agent          │    │
│  dashboard (browser) │                           │   │   (Go binary, docker     │    │
│        │             │                           │   │    or systemd unit)      │    │
│        ▼             │                           │   │                          │    │
│  api (:8080)         │                           │   │  ┌────────────────────┐  │    │
│        │             │ ◄─ outbound HTTPS ──────────────┤ Poll: GET /v1/agent│  │    │
│        │ writes      │   (long-poll for jobs)    │   │  │   /jobs/next      │  │    │
│        ▼             │                           │   │  └────────────────────┘  │    │
│  postgres            │                           │   │             │            │    │
│        ▲             │                           │   │             ▼            │    │
│        │             │                           │   │  ┌────────────────────┐  │    │
│        │ POST results│ ◄─ outbound HTTPS ──────────────┤ Scan execution     │  │    │
│        │             │                           │   │  │ (existing ingestion│  │    │
│        │             │                           │   │  │  scan logic)       │  │    │
│        │             │                           │   │  └────────────────────┘  │    │
│                      │                           │   │             │            │    │
│                      │                           │   │             ▼            │    │
│                      │                           │   │  ┌────────────────────┐  │    │
│                      │                           │   │  │ Local credentials  │  │    │
│                      │                           │   │  │ (per-account)      │  │    │
│                      │                           │   │  └─────────┬──────────┘  │    │
│                      │                           │   │            │             │    │
│                      │                           │   └────────────┼─────────────┘    │
│                      │                           │                │                  │
└──────────────────────┘                           │                ▼                  │
                                                   │       AWS / Azure / GCP APIs       │
                                                   │       (intra-VPC where possible)   │
                                                   └──────────────────────────────────┘
```

### Components

#### 4.1 Agent binary

The Agent and ingestion share the `Scanner` precondition (§3.5). On top of `Scanner`,
each binary has its own composition root.

Today's ingestion binary (`services/ingestion/cmd/main.go`):
- Listens on `:8081`
- Receives `POST /scan {account_id, organization_id}` from the api
- Reads credentials from postgres via `Store.GetAccount`
- Calls `Scanner.Scan` (post-refactor)
- Writes results to local postgres via `Store.SaveZombies` / `Store.SaveResources` / etc.

Agent binary (proposed at `services/agent/cmd/main.go`):
- Does NOT listen on any inbound port (Phase 1; optional localhost `/metrics` per §4.9)
- Consumes work from `services/shared/queue/` via a new Agent-flavoured backend (§4.3)
- Reads credentials from local config (file, env, or local Vault / Secrets Manager)
- Calls the same `Scanner.Scan` as ingestion
- POSTs the `ScanResult` to `https://api.axiaops.io/v1/agent/results` (§4.4)
- Authenticates via Agent JWT, separate keypair from license (§5)

The provider code, analyzer, detection rules, and `Scanner` are **shared**. The
divergence is in the wiring at composition-root level only.

#### 4.2 Pairing / registration

Customer flow:
1. Admin in AxiaOps dashboard clicks "Add Agent" → SaaS generates a one-time pairing
   token bound to the organization.
2. Customer runs the Agent: `docker run axiaops/agent --pair-token=<token>`
3. Agent presents pairing token to SaaS → SaaS issues a long-lived **Agent JWT**
   (license-JWT-like, scoped to `agent:<uuid>` with claims `{organization_id,
   agent_id, issued_at, max_versions_behind}`).
4. Agent persists JWT locally (mode 0600 file in its data dir).
5. Subsequent calls use the JWT as `Authorization: Bearer <jwt>` header.

This mirrors AxiaOps' existing bootstrap-token pattern at the install level, and the
license-JWT pattern at the per-customer level.

#### 4.3 Job dispatch — reuse `services/shared/queue/`

The existing `Queue` interface (`services/shared/queue/queue.go`) already abstracts
Enqueue / Dequeue with a Redis-BRPOP backend and a sync-HTTP fallback. Agent dispatch
is naturally a third backend, not a parallel pattern.

**Proposed:** add an `agentqueue` backend that the SaaS api uses to enqueue jobs
keyed by `agent_id`. The Agent's outbound long-poll endpoint
(`GET /v1/agent/jobs/next`) is implemented as a thin BRPOP-over-HTTP shim in the SaaS
api — Agent connects, holds the connection for up to 30s, SaaS api BRPOPs from
`agent:<id>:queue`, returns the job (or `204 No Content` on timeout for the Agent to
re-poll).

The Queue interface signature already carries `ScanJob{OrganizationID, AccountID, ...}`.
We add an `AgentID` field. Job payload over the wire:

```json
{
  "job_id": "uuid",
  "account_label": "prod-aws-acc-1",
  "scan_type": "full",
  "deadline": "2026-05-12T10:00:00Z",
  "protocol_version": 1
}
```

Agent looks up `account_label` in its local config to find the credentials, executes
the scan, POSTs results back. Account credentials never appear in the wire format
between SaaS and Agent.

This approach inherits the existing Queue tests, Redis fallback semantics, and metrics
(`axiaops_queue_depth`, etc.) — no parallel observability surface.

#### 4.4 Result upload — six collections + account lifecycle

The handwave in v0.1 of this doc was "same code paths." The honest surface is larger.
A single `ScanResult` upload carries:

1. **Cost records** (aggregate Cost Explorer) — `[]model.CostRecord`
2. **Resource-level cost records** — `[]model.CostRecord` (separate batch, written via
   `Store.Save` line ~467 today)
3. **Cost Explorer API records** — `[]model.CostRecord` (third batch, line ~496 today)
4. **Usage records** — `[]analyzer.UsageRecord`
5. **Zombies** — `[]model.ZombieResource` (replaces wholesale per internal account)
6. **Resource records** — `[]model.ResourceRecord`
7. **Snapshot** — one `model.ZombieSnapshot` + N `model.SnapshotService` rows

Plus **account-lifecycle side-effects** the SaaS api must reproduce on receipt:

- `TryMarkAccountScanning` was called at the api side when the job was enqueued. On
  result receipt, SaaS api must `UpdateAccountStatus("connected")` or
  `SetAccountError(...)` based on result outcome — mirroring
  `services/ingestion/cmd/main.go:200,204`.
- `Scanner` may have **discovered** the AWS account ID (the line-383 back-fill case).
  The `ScanResult.DiscoveredAccountID` field surfaces this — SaaS api writes it via
  `Store.SaveAccount` if it differs from the row's current `account_id`. (This is a
  rare path — only the first scan for an access-key-onboarded account where the user
  did not paste the account number.)

**Partial-failure semantics.** Scanner returns a `ScanResult` only on full success. On
partial fetch failure (e.g., CloudWatch GetMetricStatistics fails for some resources),
Scanner can return a successful result with the partial data — the same way ingestion
handles it today. Catastrophic failure → Scanner returns an error → Agent reports
`/v1/agent/results/failure` with the error string, SaaS api calls `SetAccountError`.

**Transactionality.** Today's ingestion writes are NOT in one transaction (different
calls write at different points in the pipeline). Moving to a single upload payload
lets SaaS api wrap the seven writes in one transaction — small posture improvement.

**Implementation note.** The api will need a new internal "scan apply" function that
takes `ScanResult` and executes the writes + lifecycle state machine. Same function
should be callable by both the new `/v1/agent/results` handler and the existing
ingestion path (which today does these writes scattered across `runIngestionCore`).
Consolidating that is a side benefit of the refactor.

#### 4.5 Heartbeat / health surface

Agent emits `POST /v1/agent/heartbeat` every 30s with version, uptime, and last-scan
timestamp. SaaS api updates the `agents` table (§4.6). Dashboard surfaces "Agent
v1.4.2 online, last scan 12m ago" or "Agent offline (last seen 8h ago)" with red /
green indicators.

Without heartbeat, a silent Agent failure (network partition, crash loop, expired
creds, OOM kill) is invisible to the customer.

#### 4.6 Data model — five new tables / column additions

This section was underspecified in v0.1. The full surface:

##### `agents` (new table)

```sql
CREATE TABLE axiaops.agents (
    id                  TEXT        PRIMARY KEY,    -- agent UUID (in JWT sub)
    organization_id     TEXT        NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    label               TEXT        NOT NULL,       -- "eu-central-1-agent", customer-supplied
    paired_at           TIMESTAMPTZ NOT NULL,
    revoked_at          TIMESTAMPTZ,
    last_seen_at        TIMESTAMPTZ,
    last_version        TEXT,                       -- "v1.4.2"
    protocol_version    INT         NOT NULL DEFAULT 1,
    jwt_id              TEXT,                       -- JTI claim of currently-active JWT
    jwt_issued_at       TIMESTAMPTZ,
    jwt_expires_at      TIMESTAMPTZ
);

CREATE INDEX agents_organization_id_idx ON axiaops.agents(organization_id);
CREATE INDEX agents_last_seen_at_idx ON axiaops.agents(last_seen_at);
```

RLS policy: `organization_id = current_setting('app.organization_id', true)`.

##### `agent_pairing_tokens` (new table — short-lived, replaces in-memory state)

```sql
CREATE TABLE axiaops.agent_pairing_tokens (
    token_hash      TEXT        PRIMARY KEY,
    organization_id TEXT        NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    created_by      TEXT        NOT NULL,       -- user id who initiated pairing
    created_at      TIMESTAMPTZ NOT NULL,
    expires_at      TIMESTAMPTZ NOT NULL,
    consumed_at     TIMESTAMPTZ,
    consumed_by_agent_id TEXT
);
```

Pairing tokens are SHA-256 hashed at rest, same posture as the install token in
`bootstrap_state`. Single-use, short TTL (15 min default).

##### `agent_scan_jobs` (new table)

```sql
CREATE TABLE axiaops.agent_scan_jobs (
    id                  TEXT        PRIMARY KEY,
    organization_id     TEXT        NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    agent_id            TEXT        NOT NULL REFERENCES agents(id) ON DELETE CASCADE,
    account_id          TEXT        NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
    dispatched_at       TIMESTAMPTZ NOT NULL,
    picked_up_at        TIMESTAMPTZ,
    completed_at        TIMESTAMPTZ,
    failed_reason       TEXT,
    deadline_at         TIMESTAMPTZ NOT NULL
);

CREATE INDEX agent_scan_jobs_agent_id_idx ON axiaops.agent_scan_jobs(agent_id);
CREATE INDEX agent_scan_jobs_pending_idx ON axiaops.agent_scan_jobs(agent_id, completed_at)
    WHERE completed_at IS NULL;
```

This is the place that lets us answer "what happens when an Agent goes offline
mid-scan" (open question §9): a sweeper looks for rows where
`dispatched_at < now() - deadline_at` and `completed_at IS NULL`, marks them failed,
optionally re-dispatches to another paired Agent in the same org.

##### `accounts.agent_id` (column addition)

```sql
ALTER TABLE axiaops.accounts
    ADD COLUMN agent_id TEXT REFERENCES agents(id) ON DELETE SET NULL,
    ADD COLUMN auth_mode TEXT NOT NULL DEFAULT 'saas_static'
        CHECK (auth_mode IN ('saas_static', 'saas_role', 'agent'));

-- Make secret_encrypted nullable; only saas_* modes populate it
ALTER TABLE axiaops.accounts
    ALTER COLUMN secret_encrypted DROP NOT NULL;
```

`auth_mode='agent'` rows MUST have `agent_id IS NOT NULL` and `secret_encrypted IS NULL`.
SaaS api enforces this in `SaveAccount` with a CHECK constraint at the column level.

##### `agent_revocations` (new table — denylist cache backing)

```sql
CREATE TABLE axiaops.agent_revocations (
    jwt_id          TEXT        PRIMARY KEY,
    organization_id TEXT        NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    revoked_at      TIMESTAMPTZ NOT NULL,
    revoked_by      TEXT        NOT NULL,
    reason          TEXT
);
```

This is the authoritative revocation source. The Redis denylist (§5) is a cache; this
table is the truth. Avoids the "Redis flushed → revoked Agent comes back online" failure
mode.

##### Total schema impact

- 4 new tables + 2 column additions on `accounts`
- ~5 new RLS policies
- ~12 new Store methods (CRUD over the above)
- ~2 weeks of focused schema/Store work

#### 4.7 License enforcement — moves to SaaS side

The current scan-gate is in `services/ingestion/cmd/main.go:156–182` — the binary
itself enforces `license.IsScanAllowed` before executing a scan. This works for
SaaS-and-self-hosted today because both ingestion variants are deployed by parties
the license-bearer trusts.

**For the Agent, this assumption breaks.** The Agent ships as a customer-side binary;
the operator can `strings` it, identify the gate, and patch it. The binary's
embedded `pubkey.pem` is also the operator's machine — they can swap it. Trusting a
customer-shipped binary to enforce its own license is unsound for a paid SKU.

**Resolution.** Move the scan-gate to the SaaS side at `/v1/agent/results`. The
handler verifies `license.IsScanAllowed` for the Agent's organization on every
upload; expired-license uploads return 403 `license_expired`. The Agent binary
itself does NOT carry the license keypair or the gate — it's a credential-holding
scanner, period.

**Implementation:** the SaaS-side scan-apply function (§4.4) consults
`license.IsScanAllowed(organization_id)` before persisting the `ScanResult`.
Same predicate used by ingestion today, just called from a different layer.

This also simplifies the Agent build — no license/pubkey embedding, smaller surface,
no anti-tamper concerns (because tampering doesn't help — SaaS still gates).

#### 4.8 Audit log actor

`services/shared/model/audit.go` defines `AuditActionScanTriggered` and friends. When a
user clicks "Scan now" in the SaaS dashboard, the audit row's actor is that user. With
Agent SKUs, scans are initiated by:

- A user clicking "Scan now" — actor is the user (unchanged)
- The scheduled-scan ticker on the SaaS side — actor needs a synthetic principal:
  proposed `system:scheduler`
- An Agent re-attempting after a network blip and reading a pending job — actor is
  still the original initiator (user or scheduler), recorded at enqueue time

**Proposed audit event shape addition:**

```go
type AuditEvent struct {
    // ... existing fields ...
    AgentID    string  // set when the action was *executed* by an agent (vs SaaS-side ingestion)
    ScanJobID  string  // joins to agent_scan_jobs
}
```

Auditors querying "which user authorised this AWS API call against my account" get:
- Initiator (user or `system:scheduler`)
- Executor (`agent:<uuid>` or `saas:ingestion`)
- Job lifecycle (via `scan_job_id` join)

Without this, the audit log's "actor" model is ambiguous in the Agent topology.

#### 4.9 Observability — Agent `/metrics`

The Agent ships its own `/metrics` Prometheus endpoint on localhost (default `:9091`).
Customer ops teams will demand it for their Prometheus stack — every comparable
agent (Datadog, Grafana, Wazuh) does this. Absent it, the Agent is operationally
opaque inside the customer's monitoring posture.

**Label scheme is different from the SaaS-side `services/shared/observability/`:**

- Agent metrics include `agent_id`, `agent_version`, `protocol_version` labels
- Agent metrics do **NOT** include `organization_id` — customer only has one org from
  their perspective, the label adds zero information and would leak the org UUID into
  their Prometheus

Proposed Agent metrics:

- `axiaops_agent_scans_total{account_label,status}` — counter
- `axiaops_agent_scan_duration_seconds{account_label}` — histogram
- `axiaops_agent_jobs_dispatched_total` — counter
- `axiaops_agent_upload_errors_total{reason}` — counter
- `axiaops_agent_heartbeat_age_seconds` — gauge (helps the customer detect SaaS-side
  outage too — if heartbeats stop being ACK'd, the SaaS is the problem)
- `axiaops_agent_jwt_expires_in_seconds` — gauge (lets customer alert before
  silent eviction)
- `axiaops_agent_protocol_version_info` — gauge with constant value 1, labelled
  with the protocol version

Add `services/agent/observability/` for the Agent-specific label scheme; the shared
package stays as-is for the SaaS side.

#### 4.10 Protocol versioning

The Agent ↔ SaaS contract WILL evolve. Without an explicit versioning story, the
first protocol break bricks every paired Agent in the field.

**Design:**

- Every Agent request includes `X-Agent-Protocol-Version: 1` header
- Every Agent JWT carries a `ver` claim (set at pairing time, fixed for the lifetime
  of that JWT)
- SaaS api documents a **compat matrix** in code: which protocol versions are
  accepted, when each was deprecated, when each will be sunsetted
- Breaking changes ship as a new protocol version + a deprecation window for the old
  one (minimum 90 days, mirroring the legacy SSO callback deprecation pattern in
  `serverbuild.ComposeServer`)
- Agents auto-poll for their newest acceptable protocol version on every heartbeat;
  if the Agent's version is past sunset, SaaS responds 426 Upgrade Required and the
  dashboard surfaces the warning

Phase 1 ships at protocol version 1. Phase 2 changes (multi-cloud) may need v2.
Phase 4 changes (remediation) almost certainly need v3.

---

## 5. Auth model

### Agent ↔ SaaS

Long-lived Agent JWT (RS256), signed by a **separate keypair** from the license JWT.
v0.1 of this doc proposed reusing the license keypair — that was a smell flagged in
review and corrected here.

**Why a separate keypair:**

- The license keypair is **offline-signed** today (`docs/license-issuance.md`).
  Operator manually mints license JWTs against an air-gapped private key. The signing
  cadence is "once per customer, per renewal." Moving that private key online — to a
  SaaS service that signs new Agent JWTs on every pairing — fundamentally changes the
  threat model for license signing.
- The license public key is **embedded in every customer-shipping binary**
  (`services/shared/license/embed_production.go`, `pubkey.pem`). Customers can
  read it. Conflating verification keys conflates revocation: revoking the Agent
  keypair would invalidate every license issued under it.
- The two trust domains have different rotation cadences. License keys rotate on
  the order of years (binary releases). Agent keys may need to rotate annually or
  on incident (HSM compromise, JWT-id-confusion attack).

**Proposed:** new package `services/shared/agentauth/` with its own RS256 keypair.
The public key is embedded in the SaaS api binary only (not in the Agent binary —
the Agent verifies the SaaS's TLS cert, not the JWT). The private key lives in a
KMS-backed signing service for Phase 1 (AWS KMS, Azure Key Vault, or HashiCorp Vault
Transit — whichever AxiaOps SaaS deploys to).

**JWT claims:**

```
sub:    agent:<uuid>
iss:    api.axiaops.io
aud:    api.axiaops.io
org_id: <customer organization UUID>
iat:    <issued at>
exp:    <expiry — default 1 year>
jti:    <unique JWT ID for revocation tracking>
ver:    <protocol version the agent agreed to>
```

**Validation flow:**

1. Verify RS256 signature against `services/shared/agentauth/pubkey.pem`
2. Check `exp` not in past, `iat` not in future
3. Check `iss` matches AxiaOps SaaS host
4. Check `aud` matches AxiaOps SaaS host
5. Check `jti` is NOT in `agent_revocations` table (DB-authoritative, with Redis
   cache in front — §4.6)
6. Check `org_id` claim matches an active, non-deleted `organizations` row

**Revocation.** The denylist lives in `agent_revocations` (§4.6). Redis cache in front
for fast lookups; cache invalidation on revoke is "delete the row, then publish to
Redis pub/sub channel for every api replica to evict its in-memory cache." Same shape
as session revocation today (`services/shared/cache/`).

**Renewal.** Agent calls `POST /v1/agent/renew` with the current (still-valid) JWT 7
days before expiry. SaaS api validates the old token, issues a new one, marks the old
`jti` for revocation 24h hence (grace window). If the Agent is offline at expiry,
the JWT lapses and the Agent's next request returns 401 + `re_pair_required`;
operator must run the pairing flow again. This is a documented failure mode, not a
bug.

### Agent ↔ customer cloud

Identical to today's ingestion. Whatever credentials are configured locally
(access keys, role ARN with `sts:AssumeRole`, Azure SP, GCP SA key, WIF) — Agent uses
them the same way the ingestion service does today, via the existing
`services/ingestion/internal/provider/...` code.

### Customer ↔ SaaS

Unchanged. Same native-auth / OIDC SSO flows the dashboard uses today.

---

## 6. Naming

"Agent" is the proposed name. Other candidates considered:

| Candidate | Recognition | Future-proofing | Risk |
|---|---|---|---|
| **Agent** ✅ | Universal in B2B SaaS (Datadog, CrowdStrike, AWS SSM) | Implies room for remediation later | Some negative connotation from EDR tooling ("what does it have access to?") |
| Collector | OpenTelemetry / Splunk vocabulary | Locks to read-only forever | Less recognized; collides with OTel if AxiaOps ever emits OTel metrics |
| Relay | Sentry / Cloudflare vocabulary | Implies pass-through, not scan logic | Wrong shape — this isn't a forwarding proxy |
| Connector | Twingate / Tailscale vocabulary | Implies network connectivity | Wrong primary function |
| Satellite | Red Hat Satellite only | Confusing — RH-specific term | Don't pick |

**Agent** chosen for:
- Maximum recognition in enterprise procurement
- Future-proofs against eventual remediation (which the business plan implies as a
  natural next-feature). "Collector with optional execution" would be a forced rename.
- Stronger sales register — "deploy our Agent in your VPC" reads more substantial than
  "deploy our Collector."

---

## 7. How this solves the multi-account question

This was the framing question that motivated this doc. Today's posture for a customer
with N AWS accounts:

```
SaaS DB.accounts table:
  account-1: secret_encrypted = AES(key1)
  account-2: secret_encrypted = AES(key2)
  ...
  account-N: secret_encrypted = AES(keyN)
```

Every credential set crosses the SaaS perimeter, sits encrypted in the SaaS DB, and
decrypts in SaaS memory at each scan. For N accounts the blast radius is N times the
single-account case.

Agent posture for the same customer:

```
SaaS DB.accounts table (per organization):
  account-1: { label: "prod-aws-1", agent_id: "ag-uuid-123", status: "connected" }
  account-2: { label: "prod-aws-2", agent_id: "ag-uuid-123", status: "connected" }
  ...

Agent local config (in customer's VPC):
  prod-aws-1: role_arn = arn:aws:iam::111:role/AxiaOpsScanner
  prod-aws-2: role_arn = arn:aws:iam::222:role/AxiaOpsScanner
  ...
  prod-aws-N: role_arn = arn:aws:iam::NNN:role/AxiaOpsScanner
  azure-prod-1: tenant_id, client_id, client_secret
  gcp-prod:     service_account_json
```

The SaaS DB row stores only metadata (label, agent assignment, status). No credentials.
The Agent locally holds N+M+K credential sets and routes each scan job to the right one
based on label.

### What this unlocks

1. **Cross-cloud scanning from a single deployment.** One Agent in the customer's primary
   cloud holds credentials for AWS + Azure + GCP. The SaaS api doesn't need different
   onboarding flows per cloud; it just dispatches jobs by `account_label`.

2. **Per-account credential rotation without SaaS involvement.** Customer rotates an
   AWS role ARN or an Azure secret — they edit the Agent's local config. SaaS DB has
   no record of credentials, so there's nothing to update there.

3. **Cross-tenant isolation strengthens.** Today, an `ENCRYPTION_KEY` compromise gives
   read access to every customer's credentials. With Agent SKU customers, the same
   compromise gives read access only to non-Agent customers' credentials. Defense-in-depth
   matters for the regulated-industry buyer.

4. **MSP / consultant use case (issue #35).** A consultant managing multiple end-customers
   deploys one Agent per end-customer (in each end-customer's VPC). The consultant's
   AxiaOps dashboard sees all their clients' data; the credentials live with each client.
   Today's design forces the consultant to hold credentials in their SaaS account on
   behalf of clients, which is a procurement landmine.

5. **Data residency for multi-region customers.** EU customer deploys Agent in eu-central-1;
   raw scan-intermediate payloads (CloudWatch metric series, Describe API responses, intermediate
   computation) stay in EU. **But:** see "What crosses the perimeter" below for the honest
   scope — this is not "no data crosses."

### What crosses the perimeter (GDPR honesty)

The v0.1 framing of "only summaries cross" was too generous. A reviewer reading the
schema would call this out, and a customer's security team would too. Honest scope:

**Stays customer-side (never leaves the Agent's host):**

- Raw CloudWatch `GetMetricStatistics` time-series
- Raw `Describe*` API responses (full instance metadata, full RDS configurations, etc.)
- AWS credentials (access keys, role ARNs, assumed-role session credentials)
- Azure SP secrets, GCP service account keys
- AWS Cost Explorer request/response intermediates beyond what `model.CostRecord` carries

**Crosses to SaaS (lives in postgres):**

- `model.CostRecord` rows: `{provider, account_id, internal_account_id, service, region,
  resource_id, amount, currency, period_start, period_end, tags}` — note **tags are
  customer-controlled** and can contain PII (owner emails, team names, project codes)
- `model.ZombieResource` rows: `{resource_id, service, region, monthly_cost, currency,
  reason, owner, tags}` — same tag-PII surface
- `model.ResourceRecord` rows: same shape as zombies but for non-zombie resources
- `model.ZombieSnapshot` + `model.SnapshotService` aggregate rows
- `audit_log` rows: dismissals, snoozes, role changes — all attributed to user
  email/id
- `dismissed_zombies` rows: which resources were dismissed, by whom, with what note
  (note text is free-form, customer-supplied)

**What this means for the data-residency pitch:**

- "Credentials never leave our perimeter" → True. Strong claim.
- "Raw CloudWatch metrics never leave our perimeter" → True. Strong claim.
- "Resource identifiers and tags never leave our perimeter" → **False.** Resource IDs
  (often containing AWS account numbers), ARNs, tag values cross to SaaS.
- "PII never leaves our perimeter" → **Depends on customer tagging discipline.** If
  the customer tags resources with `owner=alice@acme.com`, that email crosses. If
  they use `owner=alice`, it doesn't.
- "Data subject to GDPR Article 6 (cross-border transfer) never leaves our region" →
  **Depends on resource metadata + tags.** Honest answer: customer's DPIA must scope
  this based on their tagging conventions.

**Mitigation options** the Agent could offer (Phase 2+ work, not Phase 1):

- **Tag redaction** at Agent boundary: customer configures a regex / allowlist for
  tag keys; Agent strips unmatched tags before upload.
- **Owner-field hashing**: customer opts in to SHA-256-hashing the `owner` field
  before upload, with a customer-side lookup table. Dashboard shows hashes; customer
  ops looks up real owners locally.

**For the pitch deck**, the honest framing is "credentials and scan intermediates
stay local; resource metadata and dismissal workflow live in SaaS." Procurement
will respect the honesty more than the over-claim.

---

## 8. Phases

### Phase 0 — Scanner abstraction (precondition, 1–2 weeks)

Cannot be skipped. See §3.5 for context.

- Extract `Scanner` interface + `ScanResult` value type from
  `services/ingestion/cmd/main.go:runIngestionCore`
- Move all DB writes out of the scan pipeline; pipeline returns `ScanResult` instead
- Existing ingestion HTTP handler now calls `Scanner.Scan(ctx, account)` then a new
  internal `ApplyScanResult` function that does the writes + account lifecycle
- Extend `provider.Provider` interface to cover the methods today called directly on
  `aws.Client` (`FetchUsage`, `FetchResourceCosts`, `FetchCostExplorerAPICosts`,
  `Discover*`)
- Tests: existing ingestion tests stay green; new tests for `Scanner` in isolation

This phase ships standalone (no Agent code yet) and benefits the existing codebase.
Deploy to staging. Confirm scans still work identically.

### Phase 1 — Single-account read-only Agent MVP (8–10 weeks)

Realistic estimate. v0.1 said 4–8; review correctly called this undersold.

**Schema + Store methods (~2 weeks):**
- 4 new tables (`agents`, `agent_pairing_tokens`, `agent_scan_jobs`,
  `agent_revocations`), 2 column additions on `accounts`, RLS policies
- ~12 new Store methods (CRUD over the above, with RLS-aware contexts)
- Migrations + down migrations + integration tests

**Auth keypair (~1–2 weeks):**
- New `services/shared/agentauth/` package
- RS256 keypair generation + KMS integration (AWS KMS for SaaS-on-AWS)
- JWT signing service surface
- Pairing-token mint + redeem flow
- Revocation surface (DB write + Redis pub/sub eviction)

**Agent binary (~1 week):**
- New `services/agent/cmd/main.go` composition root
- Outbound long-poll consumer of the `agentqueue` Queue backend
- Local config loading (YAML file, env, optional Vault)
- `Scanner.Scan` call + result upload

**SaaS-side handlers (~1–2 weeks):**
- `POST /v1/agent/pair` — token + new agent registration
- `GET /v1/agent/jobs/next` — long-poll dispatch
- `POST /v1/agent/results` — receives `ScanResult`, calls `ApplyScanResult` + license gate
- `POST /v1/agent/results/failure` — partial-failure / error reporting
- `POST /v1/agent/heartbeat` — heartbeat receipt
- `POST /v1/agent/renew` — JWT renewal

**Dashboard UX (~1 week):**
- "Add Agent" button on Connect screen → pairing-token modal
- Agents list page with online/offline status + last-seen + version
- Account onboarding flow gets a third tab: "Agent" (alongside Access Keys + Role ARN)

**Packaging + release (~1–2 weeks):**
- Dockerfile for `axiaops-agent`
- Helm chart skeleton (k8s deployment)
- Signed image release pipeline (cosign or similar)
- Operator install docs (docker run, systemd unit, k8s helm install)
- Smoke test against staging from an external host

**Total: 8–10 weeks for one dev, 6–7 weeks if two devs can parallelise the schema +
keypair tracks. Estimates assume the developer is already familiar with the codebase.**

Acceptance: customer deploys the Agent in their VPC, pairs it with their AxiaOps
SaaS org, runs a scan, gets zombie data in the dashboard identical to what
SaaS-side ingestion produces today, with credentials never touching SaaS DB.

### Phase 2 — Multi-account + multi-cloud (4–6 weeks)

Lighter than Phase 1 because the seams now exist.

- Agent config supports N AWS accounts + Azure + GCP (when those land via #9 / #41)
- Job dispatch by `account_label`; one Agent fans out to multiple credential sets
- Dashboard surfaces per-account status sourced from agent heartbeat + last-scan time
- Multiple Agents per organization, with explicit `accounts.agent_id` assignment

### Phase 3 — Operational hardening (6–8 weeks)

Heavier than v0.1 suggested. Auto-update alone is 3–4 weeks done correctly.

- Agent auto-update mechanism (signed image pulls, rollback on health-check fail,
  staged rollouts via SaaS-side cohort selection)
- Agent → SaaS protocol versioning + back-compat policy + compat-matrix surface
  in SaaS api (§4.10)
- Air-gap variant (operator triggers scan + manually uploads result archive via
  a `kubectl cp`-style upload form in the dashboard)
- Job re-dispatch when an Agent goes offline mid-scan (sweeper over
  `agent_scan_jobs` with `completed_at IS NULL AND dispatched_at < now() - deadline`)
- Tag redaction / owner-hashing options (§7) for PII-conscious customers

### Phase 4 — Active capabilities (optional, deferred)

- If automated remediation lands (currently deferred), Agent executes actions locally. This is
  what justifies the "Agent" name vs. "Collector."
- Likely needs protocol version 3
- Strong audit log + dry-run mode + customer-side circuit breakers

### Total cost estimate

| Phase | Range | Notes |
|---|---|---|
| Phase 0 (precondition) | 1–2 weeks | Refactor only, ships independently |
| Phase 1 (MVP) | 8–10 weeks | One dev; 6–7 if two |
| Phase 2 (multi-account/cloud) | 4–6 weeks | After #9 / #41 land |
| Phase 3 (ops hardening) | 6–8 weeks | Auto-update is the heavy part |
| Phase 4 (remediation) | TBD | Separate design exercise |

**Phase 0 + 1 = realistic 9–12 weeks of focused work** for a buildable, paired-Agent
MVP. Calendar time longer if interleaved with other priorities.

---

## 9. Open questions

Resolved since v0.1 (now design decisions, see referenced sections):

- ~~Where does the Agent obtain its license-JWT chain of trust?~~ → **Resolved §5.**
  Separate keypair (`services/shared/agentauth/`), distinct from the license keypair.
- ~~What happens when an Agent goes offline mid-scan?~~ → **Resolved §4.6.** Sweeper
  over `agent_scan_jobs` reassigns or fails jobs past deadline.
- ~~Multi-agent within one org / dispatch policy.~~ → **Resolved §4.6.** Per-account
  `agent_id` assignment in `accounts.agent_id`.
- ~~Job dispatch primitive (long-poll vs queue)?~~ → **Resolved §4.3.** Reuse
  `services/shared/queue/` with an `agentqueue` backend.

Still open:

- **How does the Agent get its initial scan accounts configured?** Three options: (a)
  config file on customer's host, (b) one-time setup wizard hosted by the Agent on
  localhost, (c) SaaS pushes the list of expected accounts and Agent prompts admin
  via heartbeat ack. Phase 1 lean toward (a); Phase 2 may revisit if customers
  complain about config-file friction.
- **Air-gapped customers** (no outbound to internet at all). The polling model fails
  here. Phase 3 (§8) adds offline-bundle mode (operator runs `agent scan --output=file`,
  manually uploads via dashboard form). Out-of-scope for Phase 1.
- **KMS provider choice for SaaS keypair signing.** AWS KMS, Azure Key Vault, GCP KMS,
  HashiCorp Vault, or self-hosted Sealed Secrets. Decision depends on where AxiaOps
  SaaS deploys (deployed on AWS (ECS Express), so AWS KMS is default — but the
  signing service interface should be cloud-agnostic).
- **Tag redaction / PII handling default posture.** Should Phase 1 ship with
  pass-through-all-tags (today's behaviour), or default-deny with an allowlist
  customer must opt into? Default-deny is safer for GDPR but breaks tag-based
  filtering features. Lean pass-through for Phase 1, ship redaction as opt-in
  Phase 3 (§7).
- **Terraform provider.** Helm chart is Phase 1 (§8); Terraform provider for declarative
  Agent management is unscheduled. Tier 2 customer ops will want it.
- **Dual-Agent pairing for HA.** Customer wants two Agents (active-passive or
  active-active) for the same set of accounts. Phase 2 considers it; Phase 1 is
  one-Agent-per-account.

---

## 10. Open trade-offs to surface explicitly

### Engineering cost — significant, larger than v0.1 admitted

Realistic Phase 0 + Phase 1 = **9–12 weeks** for one developer.

This is NOT "repackage ingestion." Component-by-component cost in §8 makes the size
visible. The pairing flow, the protocol versioning, the keypair management, the
data model, the dashboard surface, the helm chart, and the SaaS-side scan-apply
function are all net-new surface. The Scanner refactor (§3.5) is a precondition that
must happen first.

Phase 2–3 each add 4–8 weeks. Phase 4 (active remediation) is its own design and
estimate exercise.

**Total to a customer-grade hardened Agent SKU (Phase 0 + 1 + 2 + 3) = realistic 19–26
weeks of focused work for one developer.** Calendar time longer if interleaved with
other priorities or features.

Compare this to the FinOps competitive landscape (§3): no other vendor currently
offers an Agent SKU. So the engineering cost buys real differentiation. But the
cost is real — this is not a quick differentiator move.

### SaaS dependency — unavoidable

Agent depends on SaaS uptime for job dispatch and result upload. A SaaS outage means
Agents queue work locally (or drop it, depending on Phase 1 vs Phase 3 sophistication).
This is unavoidable — the Agent is intentionally NOT a full self-hosted deployment.
Customers who can't tolerate the SaaS dependency go to self-hosted instead.

### Naming risk — small

If AxiaOps ever ships OpenTelemetry-flavoured features, "Agent" doesn't collide. If
AxiaOps ever ships an MDM-flavoured customer-side process, "Agent" risks negative
connotation. Mitigation: marketing materials emphasize "scanning agent" or "FinOps
agent" in the first reference.

### Competitive moat — moderate

Vantage / CloudHealth / Cloudability could each ship an Agent in 6 months if they
wanted to. The differentiator is timing — if AxiaOps reaches market with this SKU
12+ months before competitors, the procurement wins compound (enterprise contracts
are 1–3 year terms; an Agent-only win today is a 3-year displacement of pure-SaaS
competitors).

---

## 11. References

- `docs/cross-account-roles-design.md` — current AWS role-based onboarding design
- `services/ingestion/cmd/main.go` — composition root the Agent forks from
- `services/shared/license/` — JWT signing infra the Agent JWT pattern lifts from
- Issue #9 (Azure cost data), #41 (GCP cost data), #35 (multi-organization MSP support)
