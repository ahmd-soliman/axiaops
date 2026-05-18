# Cross-Account IAM Role Onboarding — Design

**Status:** Proposed | **Last Updated:** April 2026 | **Owner:** Platform / Architect

This document proposes replacing AxiaOps' long-lived IAM access-key flow with cross-account
`sts:AssumeRole` plus a per-account `ExternalId`. The deliverable is the design itself —
implementation lands in a separate ticket once the team signs off.

---

## 1. Goals & Non-Goals

### Goals

- **Role-based onboarding for AWS as a first-class option.** A customer can connect their
  AWS account by creating an IAM role in their own account that grants AxiaOps the read-only
  permissions enumerated in `docs/production.md` lines 39–60, and pasting back the role ARN.
  No long-lived credentials cross the trust boundary.
- **Confused-deputy mitigation.** Every role connection carries a server-generated, per-account,
  unguessable `ExternalId`. The customer's trust policy must require it; AxiaOps' assume-role
  call must present it.
- **Drop-in replacement for the current ingestion path.** The same `aws.Client` produced by
  `services/ingestion/internal/provider/aws/aws.go:43` (`NewWithStaticCredentials`) is also
  produced by a new role-based constructor, returning the same `*Client` shape. Discovery,
  Cost Explorer, and CloudWatch code paths do not change.
- **Coexistence with access keys** for the lifetime of Phase 2 — see §7. Existing customers
  continue to scan without any user action.

### Non-Goals

- **Azure / GCP equivalents** (federated identity / workload identity). Phase 4 territory;
  the `Provider` interface in `services/ingestion/internal/provider/provider.go` already
  isolates this, but every cloud has its own onboarding shape and they are not blocked by
  AWS role support.
- **Automated CloudFormation / Terraform deployment of the customer-side role.** We provide
  a JSON snippet and (recommended) a one-click CloudFormation Quick-Create URL, but we do
  not deploy stacks into customer accounts on their behalf. Discussed in §4 and §8.
- **Removing access-key support in this ticket.** The deprecation is sketched in §7 but
  the actual sunset date is a product call, not an engineering one.
- **Multi-region role assumption.** A single role with global trust is sufficient — STS
  is global, the assumed credentials work across regions. We do not need per-region roles.
- **Role chaining / `RoleSessionName` policies beyond the basics.** We tag the session with
  the AxiaOps organization ID and that is it. No customer-configurable session policies.

---

## 2. Threat Model & Confused-Deputy Mitigation

### The threat

AWS's *confused deputy* scenario is the entire reason `ExternalId` exists. Without it,
the attack works as follows:

1. AxiaOps publishes "to connect, allow the principal `arn:aws:iam::AXIAOPS_ACCOUNT:role/AxiaOpsScanner`
   to assume your role".
2. Acme creates a role with that trust policy.
3. Mallory — a different AxiaOps customer — discovers Acme's role ARN (it leaks in a
   support ticket, in a screenshot, in a misconfigured CloudTrail export, etc.).
4. Mallory configures their own AxiaOps account with Acme's role ARN.
5. AxiaOps, with no way to distinguish "scan request authorized by Acme" from "scan
   request authorized by Mallory", obediently calls `sts:AssumeRole` against Acme's role,
   gets credentials, and ships Acme's cost data into Mallory's AxiaOps tenant.

The AxiaOps service is the *deputy*; it has been *confused* into acting on behalf of the
wrong principal. Authoritative reading: AWS IAM User Guide, *"How to use an external ID
when granting access to your AWS resources to a third party"*
(`https://docs.aws.amazon.com/IAM/latest/UserGuide/id_roles_create_for-user_externalid.html`).

### The mitigation

`ExternalId` breaks step 5 by adding a customer-controlled secret to the assume-role
contract:

- The customer's trust policy includes `"Condition": { "StringEquals": { "sts:ExternalId": "<value>" } }`.
- AxiaOps must present that exact value on every `AssumeRole` call.
- Mallory does not know Acme's ExternalId and cannot guess it. The AssumeRole call from
  Mallory's tenant fails with `AccessDenied` regardless of whether the role ARN is right.

### Residual risk: AxiaOps-side compromise

Role-based onboarding narrows the blast radius compared to access keys — there are no
plaintext customer secrets in the AxiaOps database, so a SQL-level compromise leaks
ARNs and ExternalIds, not credentials. But the design does *not* eliminate the
trusted-deputy risk: if the `AxiaOpsScanner` role's own credentials leak (via
container compromise, IMDS exfiltration, or a stolen developer session in a role-chained
workflow), an attacker can call `sts:AssumeRole` against every connected customer's
role using the ExternalId stored alongside it. That is the same residual risk profile
as today's `ENCRYPTION_KEY` compromise — one shared secret unlocks all customer data —
just with the locus moved from the database into the AWS account boundary. Worth
acknowledging here so it informs operational hardening (least-privilege on
`AxiaOpsScanner`, short session lifetimes, no shell access into the running ingestion
container) rather than being assumed away.

### How AxiaOps generates and stores ExternalId

Decisions, opinionated:

- **Generated server-side, never customer-supplied.** A customer-chosen value (e.g. their
  org name) is too easy to guess; we reject any client-supplied `external_id` field on
  `POST /v1/accounts`. The dashboard receives it on the response from a new
  `POST /v1/accounts/draft` endpoint (see §4) and shows it back to the user verbatim.
- **128-bit URL-safe random.** `crypto/rand.Read(32 bytes) → base64url`. Roughly 43
  characters, fits AWS's 2-1224 character `ExternalId` window with comfortable headroom.
- **Per account, not per organization.** A customer with three AWS accounts gets three
  ExternalIds. Limits blast radius if one trust policy is misconfigured or one ID leaks.
- **Stable for the lifetime of the connection.** No rotation in v1. Rotating ExternalId
  requires the customer to edit their trust policy in lockstep; no recovery if they
  miss the change. Defer to a follow-up ticket if/when a customer asks.
- **Stored in plaintext.** ExternalId is *not a secret* in the way a password is. Even
  if the value leaks, exploitation requires the attacker to *also* know the customer's
  role ARN *and* be an authenticated AxiaOps tenant whose request can reach our
  ingestion code path — three independent pieces of information across two trust
  boundaries. That is bounded leakage, qualitatively different from a credential like
  an AWS secret access key whose mere possession grants access. Treat ExternalId like
  an account ID. We do not encrypt it at rest; arguments for at-rest encryption are
  cargo-culting and should be rejected.
- **Logged at INFO, never at DEBUG-with-payload.** Slog should treat ExternalId as a
  bounded identifier, the same way we already treat AWS account IDs in
  `services/ingestion/cmd/main.go:330`. Do not log the value alongside arbitrary request
  bodies.

### Where the customer sees it

In the new "Connect via Role ARN" flow, the dashboard renders a single page that contains:

1. The trust policy JSON (with `<AxiaOpsAccountId>` and `<ExternalId>` already filled in).
2. The permissions policy JSON (statically known — see §3).
3. A **Copy** button on each block.
4. The role-ARN input field (where the customer pastes back their newly-created ARN).
5. A **Verify connection** button that triggers a server-side `sts:AssumeRole`
   round-trip before the account row is finalised.

This is the only place ExternalId is displayed; subsequent visits to the account-detail
page show it as a read-only field for reference. Never via email, never logged in audit
metadata as a value (only that it was generated).

---

## 3. Trust Policy & Permissions Policy

The customer applies two separate JSON documents to a single IAM role in their AWS
account. The terminology matches AWS's own onboarding patterns (Datadog, Wiz, CloudHealth
all use this split).

### 3.1 Trust policy (who can assume the role)

```json
{
  "Version": "2012-10-17",
  "Statement": [
    {
      "Sid": "AllowAxiaOpsToAssumeForReadOnlyScans",
      "Effect": "Allow",
      "Principal": {
        "AWS": "arn:aws:iam::<AxiaOpsAccountId>:role/AxiaOpsScanner"
      },
      "Action": "sts:AssumeRole",
      "Condition": {
        "StringEquals": {
          "sts:ExternalId": "<ExternalId>"
        }
      }
    }
  ]
}
```

Notes:

- `<AxiaOpsAccountId>` is hard-coded for the production AxiaOps AWS account. Staging gets
  a different account ID; we render whichever matches `APP_ENV`.
- `Principal` is `AxiaOpsScanner`, an IAM role in the AxiaOps account whose name describes
  a **capability**, not a deployment substrate. Whatever runs ingestion (today: App Runner
  task role; potentially: Lambda execution role, ECS task role) adopts `AxiaOpsScanner`
  as its execution identity. This decouples the customer-facing trust contract from our
  compute choice — moving ingestion to Lambda later does not require any customer to
  edit their trust policy. Do not rename this role to anything that leaks the substrate
  (e.g. `AxiaOpsIngestionTaskRole`, `AxiaOpsLambdaRole`); doing so creates a customer-visible
  migration cost the next time we change platforms.
- Do not use `"AWS": "arn:aws:iam::<AxiaOpsAccountId>:root"`. That allows any principal
  in the AxiaOps account to assume the role; pinning to `AxiaOpsScanner` is the principle
  of least privilege.
- `ExternalId` is required. There is no fallback path that omits the condition; an account
  with `auth_method='role'` and `external_id IS NULL` is a bug.

### 3.2 Permissions policy (what the role can do)

This must mirror the AxiaOpsReadOnly policy already documented for the ingestion task role
in `docs/production.md` lines 39–60, *plus* every Describe/List call that landed since
that doc was written. Enumerated from the actual code in
`services/ingestion/internal/provider/aws/`:

```json
{
  "Version": "2012-10-17",
  "Statement": [
    {
      "Sid": "AxiaOpsReadOnlyScan",
      "Effect": "Allow",
      "Action": [
        "ce:GetCostAndUsage",
        "cloudwatch:GetMetricStatistics",
        "cloudwatch:ListMetrics",
        "ec2:DescribeInstances",
        "ec2:DescribeVolumes",
        "ec2:DescribeSnapshots",
        "ec2:DescribeImages",
        "ec2:DescribeAddresses",
        "ec2:DescribeNatGateways",
        "rds:DescribeDBInstances",
        "rds:DescribeDBSnapshots",
        "lambda:ListFunctions",
        "elasticloadbalancing:DescribeLoadBalancers",
        "logs:DescribeLogGroups",
        "ecr:DescribeRepositories",
        "ecr:DescribeImages",
        "secretsmanager:ListSecrets",
        "elasticache:DescribeCacheClusters",
        "es:DescribeDomains",
        "redshift:DescribeClusters",
        "sagemaker:ListNotebookInstances",
        "dynamodb:ListTables",
        "kinesis:ListStreams",
        "cloudfront:ListDistributions",
        "eks:DescribeClusters",
        "s3:ListAllMyBuckets",
        "s3:GetBucketLocation"
      ],
      "Resource": "*"
    }
  ]
}
```

The list is opinionated:

- **No write actions, ever.** No `*:Modify*`, no `*:Delete*`. AxiaOps is read-only by
  design and the policy enforces that at the IAM layer, not just at the application layer.
- **`Resource: "*"`.** None of the Describe/List calls support resource-level conditions
  in any meaningful way; restricting by ARN here would either be a no-op or break things.
- **Drift discipline.** When a new detection is added (CLAUDE.md §"Adding New Detection
  Rules" step 5 already says so), this list and `docs/production.md` move together.
  CI lint: a script that diffs the SDK calls in `services/ingestion/internal/provider/aws/`
  against the policy and fails the build if a new permission is needed but not declared.
  Worth a follow-up ticket; out of scope for the role migration itself.

> **Permission expansion — this fixes both flows, not just the new one.**
> The list above contains 27 actions. The currently-documented `AxiaOpsReadOnly` policy
> in `docs/production.md` lines 39–60 enumerates only 9. That is not a discrepancy
> introduced by this design — it is a pre-existing documentation drift. Customers who
> deployed the access-key flow against the documented policy are silently missing
> permissions for ECR, SecretsManager, ElastiCache, OpenSearch, Redshift, SageMaker,
> DynamoDB, Kinesis, CloudFront, EKS, and S3 listing; their scans degrade quietly via
> the `AccessDenied`-then-skip path described in §3.4. **Updating
> `docs/production.md:39-60` is part of this work, not a follow-up ticket.** The
> implementation PR must touch both the role-flow templates and the access-key
> documentation in lockstep; there is no scenario where one is correct and the other
> is not.

### 3.3 Recommendation: ship as a CloudFormation template, *also* as raw JSON

The customer experience matters. Compare:

- **Raw JSON only:** customer opens IAM console, creates a role, pastes JSON, fails the
  first time because they pasted into "Permissions" instead of "Trust relationships",
  retries, finally succeeds. ~10 minutes, error-prone.
- **CloudFormation Quick-Create URL:** a deep link of the form
  `https://console.aws.amazon.com/cloudformation/home#/stacks/quickcreate?templateURL=<S3-hosted-template>&stackName=AxiaOpsIntegrationRole&param_AxiaOpsAccountId=<…>&param_ExternalId=<…>`.
  Customer clicks, reviews, ticks the IAM acknowledgement, hits Create, copies the
  stack output (the role ARN). ~90 seconds.

Recommendation: ship both. The Quick-Create URL is the primary CTA. The raw JSON is a
"prefer to do it manually?" disclosure for security teams who are not allowed to launch
arbitrary CloudFormation templates from third parties.

The template lives in a public, versioned S3 bucket (`s3://axiaops-public-templates/v1/role.yml`).
Versioning matters: when we add a new permission, we ship `v2/role.yml` and the dashboard
generates Quick-Create URLs that point at v2. Existing customers' stacks stay on v1
until they update; we tolerate that — see §3.4.

### 3.4 Permission drift handling

When AxiaOps adds a detection that needs a new IAM action, customers on the old policy
will see scan failures with `AccessDenied`. Our response:

- **Detect at runtime.** When `ce.GetCostAndUsage` or any Describe/List call returns
  `AccessDenied`, log it as a structured warning, attribute it to the missing permission,
  and continue the scan with that data source omitted (rather than failing the whole scan).
- **Surface in the dashboard.** Account detail page shows "Update required: AxiaOps needs
  these new permissions to scan ECR/SecretsManager/etc." with a one-click stack-update
  Quick-Create URL.
- **Never auto-update.** Customer security teams approve every IAM change.

This is graceful degradation, not a hard cutover. Same shape as the staging Kinde stub
pattern in `services/api/CLAUDE.md` — "if the dependency is missing, log and continue".

---

## 4. Onboarding UX

### 4.1 Today

`services/dashboard/src/screens/ConnectScreen.jsx` (lines 35-206) renders a single form:
Label, Access Key ID, Secret Access Key, Region. Submitting POSTs to `/v1/accounts`
which calls `crypto.Encrypt(req.SecretKey)` (handler.go:573) and stores the ciphertext.

### 4.2 Proposed flow — three states

The Connect screen grows a **two-tab selector at the top** ("Role ARN (recommended)" vs
"Access Keys (legacy)") plus a server-issued draft step for the role flow:

```
[Tab: Role ARN (recommended)] [Tab: Access Keys]

──── Role ARN tab, Step 1: Generate ────
[Label: ____________]
[Region: eu-central-1 ▾]
                              [ Generate connection ]    ← POST /v1/accounts/draft

──── Step 2 (revealed after draft is created) ────
External ID:  axops-ext-9f2a4d1e8b73…    [Copy]
AxiaOps Role: arn:aws:iam::905…:role/…    (informational)

[ Launch CloudFormation (recommended) ]   ← deep-link
[ Show JSON instead ]                     ← discloses trust + perms blocks

Once the role exists in your AWS account, paste its ARN below:
[Role ARN: arn:aws:iam::____________:role/__________ ]
                                           [ Verify and connect ]    ← PATCH /v1/accounts/{id}

──── Access Keys tab ────
(Existing form. Shown but de-emphasized: secondary button styling, "Most enterprises
require role-based access — use the recommended tab above.")
```

### 4.3 The two-step backend handshake

The flow needs two server round-trips because `ExternalId` must exist before the
customer creates their AWS-side role, but the role ARN is unknown until *after* they
do. Three options considered:

| Option | Sketch | Verdict |
|---|---|---|
| A. One-shot POST | Customer fills role ARN + label + region in one form, server generates ExternalId, attempts AssumeRole, saves on success | Won't work — customer cannot paste a role ARN that doesn't exist yet, and the role's trust policy has to reference the ExternalId we haven't generated |
| B. Two-step: draft + finalise | `POST /v1/accounts/draft` returns a row with `status='pending_role_setup'` and the new ExternalId. `PATCH /v1/accounts/{id}` accepts `role_arn`, server runs AssumeRole, flips to `status='connected'` | **Recommended.** Clean state machine, ExternalId is real (persisted) before the customer applies it, retries are obvious |
| C. Client-generated ExternalId | Dashboard generates ExternalId, server accepts | Rejected — see §2; client-generated values undermine the threat model |

Recommendation: **B**. It maps cleanly to the data model in §5 and is how Datadog,
CloudHealth, and Wiz all do it.

State machine on `accounts.status` for role-based accounts:

```
(create draft) → pending_role_setup ──verify ok──→ connected ──scan─→ scanning ──→ connected
                          │                            │                              │
                          │                            │                              │
                          └──verify fail──┐            ├──scan fail (AssumeRole/      │
                                          │            │   AccessDenied/missing perm)─→ error
                                          │            │                              │
                                          ▼            ▼                              │
                          pending_role_setup       error ──re-verify─→ pending_role_setup
                          (error_message set)      (error_message      (customer fixed
                                                    set, re-verify       trust policy,
                                                    via PATCH allowed)   re-runs §4.4)
```

All four states defined in §5.4 (`connected | scanning | error | pending_role_setup`)
appear here. The `error → pending_role_setup` re-verify edge is the path described in
§6.6: a scheduled scan hits `AccessDenied` (customer modified the trust policy, deleted
the role, etc.), the row moves to `error` with `error_message` populated, and the
customer recovers by re-issuing `PATCH /v1/accounts/{id}` with the corrected role ARN —
which routes through the same verify path as initial onboarding. The `error` state for
access-key accounts is unchanged (existing behaviour).

The dashboard renders `pending_role_setup` with a "Finish connecting" CTA on the
accounts page so a customer who closes the tab mid-flow can resume; `error` renders
with a "Re-verify" CTA that pre-fills the role ARN.

### 4.4 Verification step

`PATCH /v1/accounts/{id}` with body `{"role_arn": "arn:aws:iam::…:role/…"}` triggers a
synchronous `sts:AssumeRole` round-trip before returning. **The STS call lives in the
ingestion service, not in the API service.** The API forwards to a new ingestion
endpoint and translates the response.

Why ingestion, not API:

- `services/api/go.mod` has zero AWS SDK dependencies today; no `.go` file under
  `services/api/` imports any `github.com/aws/aws-sdk-go*` package. That is a
  deliberate boundary.
- `services/shared/CLAUDE.md` is explicit: *"No AWS SDK dependency — cloud-specific
  code lives in the ingestion service."* Putting the SDK in the API service erodes a
  property that has been carefully maintained.
- The "avoid a cross-service hop" argument does not survive scrutiny: the
  `POST /v1/accounts/{id}/scan` flow already round-trips API → ingestion (see
  `services/api/CLAUDE.md` §"Async scans"), and STS itself is ~50–200 ms while the
  intra-VPC hop is ~1 ms. STS dominates the latency budget regardless of where the
  call lives. There is no UX cost to adding the hop and there is a real architectural
  cost to introducing AWS SDK dependencies in two services.

Flow:

1. **API handler `PATCH /v1/accounts/{id}`** receives `{role_arn}`. Loads the draft
   account, reads `external_id`, `region`. Constructs a request body
   `{role_arn, external_id, region}` and POSTs synchronously to ingestion at
   `POST /v1/credentials/verify` (intra-VPC, same auth pattern as the existing
   API → ingestion `/scan` call).
2. **Ingestion handler `POST /v1/credentials/verify`** receives the request, calls
   `sts.AssumeRole(RoleArn=role_arn, ExternalId=external_id, RoleSessionName="axiaops-verify-<account-id>", DurationSeconds=900)`,
   then calls `sts.GetCallerIdentity` with the returned credentials to resolve the
   AWS account number. Returns
   `{ok: true, account_id: "905…"}` on success or
   `{ok: false, code: "role_assume_failed", reason: "trust_policy_mismatch", detail: "…"}`
   on failure. The ingestion service does not touch the database for this call —
   verification is stateless from its perspective; the API service owns persistence.
3. **API handler** translates the ingestion response back into the existing 200/400
   shape:
   - `ok: true` → persist `role_arn` and the resolved `account_id` (the AWS account
     number from `GetCallerIdentity`, populated into `accounts.account_id` — same
     column used by the existing access-key flow at `aws.go:54`), set
     `status='connected'`, return 200 with the account row.
   - `ok: false` → return 400 with the structured error
     (`{"code":"role_assume_failed","reason":"trust_policy_mismatch","detail":"…"}`),
     persist `error_message`, account stays in `pending_role_setup`.

A 900-second session is short on purpose — verification, then discard. We do *not*
keep the credentials from this call; the ingestion service will assume again at scan
time.

**Boundary preservation:** `services/api/go.mod` stays AWS-SDK-free. The `sts` and
`stscreds` packages enter the codebase only via `services/ingestion/`.

### 4.5 Region picker semantics

The `region` column has the same meaning for role-based accounts as for access-key
accounts: it is the AWS region the SDK client is configured against by default.
AssumeRole credentials returned by STS are not region-scoped — once obtained, they
work in any region the role's IAM policy permits. But the *Describe* APIs themselves
remain region-scoped (EC2, RDS, Lambda, ELB, CloudWatch all return only the resources
in the region the call is dispatched against). Cross-region scanning therefore works
not because the credentials are global, but because the provider re-configures the
SDK client per region — see `aws.go:104` (`configForRegion`), which builds a fresh
`*aws.Config` for each region the scan touches. Region selection in the onboarding
form picks the *default* region; the scan path iterates regions independently.

The form behaviour matches the access-key flow — same `region` column, same default
`eu-central-1`. No data-model change for region.

### 4.6 Audit events

Add these to `services/shared/model/audit.go` alongside the existing
`AuditActionAccountConnected`:

- `AuditActionAccountRoleDraftCreated` — emitted by `POST /v1/accounts/draft`. Metadata:
  `{provider, label, region, auth_method:"role"}`. Never the ExternalId value.
- `AuditActionAccountRoleVerified` — emitted on successful `PATCH` verification. Metadata:
  `{role_arn}` (role ARN is not a secret).
- `AuditActionAccountRoleVerifyFailed` — emitted on failed verification. Metadata:
  `{reason}`. No raw STS error string (may contain sensitive principal info).

The existing `AuditActionAccountConnected` keeps its meaning: emitted when an account
moves into `connected` state regardless of auth method.

---

## 5. Data Model Changes

### 5.1 Today

`services/shared/model/account.go` lines 6-19:

```go
type Account struct {
    ID                string
    OrganizationID    string
    Provider          string
    Label             string
    AccountID         string  // AWS account ID, e.g. "123456789012"
    AccessKeyID       string
    SecretEncrypted   string
    Region            string
    Status            string
    LastScannedAt     *time.Time
    ScanIntervalHours int
    CreatedAt         time.Time
}
```

`accounts` table (migration `001_initial.up.sql:88-100` + later additions): same shape,
with `access_key_id` and `secret_encrypted` both `NOT NULL DEFAULT ''`.

### 5.2 Three options considered

| Option | Schema | Verdict |
|---|---|---|
| **A. Polymorphic credential blob** (`auth_credentials JSONB`) | One JSON column, validated in app code | Loses RDBMS validation, blocks queries like "how many accounts use roles?", no help from `NOT NULL` checks |
| **B. Sibling table `account_credentials`** (one row per credential type, FK to `accounts`) | Normal-form, supports future credential types | Over-engineered for two variants. We are not building a credential plugin system |
| **C. Flat columns with `auth_method` discriminator** + nullable variant fields | Adds `auth_method`, `role_arn`, `external_id`; makes `access_key_id`, `secret_encrypted` nullable | **Recommended** |

Recommendation: **C**. This is exactly the pattern AWS itself uses internally (look at
`aws-sdk-go-v2/credentials/`: env-var creds, role creds, web-identity creds, all read
from a shared profile file). It is also the easiest to migrate to — the existing rows
get a backfilled `auth_method='access_key'` and the new columns are NULL. RLS, indexes,
and queries all stay the same.

### 5.3 Proposed schema (migration `017_account_role_auth`)

`017_account_role_auth.up.sql`:

```sql
SET search_path TO axiaops;

ALTER TABLE accounts
    ADD COLUMN auth_method  TEXT        NOT NULL DEFAULT 'access_key',
    ADD COLUMN role_arn     TEXT,
    ADD COLUMN external_id  TEXT,
    ADD COLUMN error_message TEXT;

ALTER TABLE accounts
    ALTER COLUMN access_key_id    DROP NOT NULL,
    ALTER COLUMN secret_encrypted DROP NOT NULL;

ALTER TABLE accounts
    ADD CONSTRAINT accounts_auth_method_check
    CHECK (auth_method IN ('access_key', 'role'));

ALTER TABLE accounts
    ADD CONSTRAINT accounts_role_fields_present
    CHECK (
        auth_method = 'access_key'
        OR (auth_method = 'role'
            AND external_id IS NOT NULL
            AND (role_arn IS NOT NULL OR status = 'pending_role_setup'))
    );

ALTER TABLE accounts
    ADD CONSTRAINT accounts_access_key_fields_present
    CHECK (
        auth_method = 'role'
        OR (auth_method = 'access_key' AND access_key_id IS NOT NULL AND secret_encrypted IS NOT NULL)
    );

ALTER TABLE accounts
    ADD CONSTRAINT accounts_role_only_for_aws
    CHECK (
        auth_method = 'access_key'
        OR provider = 'aws'
    );

CREATE INDEX idx_accounts_auth_method ON accounts(auth_method);
```

The `accounts_role_only_for_aws` constraint encodes the §1 non-goal explicitly: Azure
managed identity and GCP workload identity are differently shaped contracts (no
`AssumeRole`, no `ExternalId`) and will need their own discriminator values when added
in Phase 4. Pinning role auth to `provider='aws'` at the DB layer prevents a future
handler bug from creating a half-defined `provider='azure', auth_method='role'` row.
The same invariant is also enforced in the API handler (§6.7) so the user-facing error
is friendly; the CHECK constraint is the belt to that suspenders.

`017_account_role_auth.down.sql` is the inverse, with a guard that fails loudly if any
`auth_method='role'` rows exist (so we don't silently drop role connections on rollback).

Notes:

- **`auth_method='access_key'` is the default** for backward compat — existing rows are
  unaffected. The first deployment after this migration backfills correctly without any
  Go code change.
- **Two CHECK constraints, not one.** It is tempting to write a single combined check,
  but splitting them yields better error messages — `accounts_role_fields_present`
  fires for malformed role rows, `accounts_access_key_fields_present` for malformed
  access-key rows. Worth the extra DDL line.
- **No `external_id_hash` column.** ExternalId is not a secret per §2; storing the
  raw value is correct.
- **`error_message` is new** — currently the API service infers errors from logs.
  Surfacing the most recent verification or scan failure on the row itself makes the
  "Finish connecting" UX possible.

### 5.4 Go model change

`services/shared/model/account.go`:

```go
type Account struct {
    ID                string
    OrganizationID    string
    Provider          string
    Label             string
    AccountID         string

    AuthMethod        string  // "access_key" | "role"
    AccessKeyID       string  // populated when AuthMethod == "access_key"
    SecretEncrypted   string  // populated when AuthMethod == "access_key", never JSON-marshalled
    RoleARN           string  // populated when AuthMethod == "role"
    ExternalID        string  // populated when AuthMethod == "role"; safe to surface to org members

    Region            string
    Status            string  // connected | scanning | error | pending_role_setup
    LastScannedAt     *time.Time
    ScanIntervalHours int
    ErrorMessage      string
    CreatedAt         time.Time
}
```

JSON tags follow the existing convention in `account.go:13` —
`SecretEncrypted` keeps its `json:"-"`. `ExternalID` and `RoleARN` are JSON-visible
because the dashboard reads them; the customer is allowed to see their own ExternalId
back.

### 5.5 Backfill

None needed. `auth_method DEFAULT 'access_key'` handles every existing row at the DDL
level. No data migration script, no transitional dual-write code.

---

## 6. Backend Changes — File-by-File

### 6.1 `services/shared/storage/postgres/migrations/`

- `017_account_role_auth.up.sql` and `.down.sql` per §5.3.

### 6.2 `services/shared/model/account.go`

- Add `AuthMethod`, `RoleARN`, `ExternalID`, `ErrorMessage` fields per §5.4.
- Update the `audit.go` constants per §4.6.

### 6.3 `services/shared/storage/storage.go` and `storage/postgres/postgres.go`

- `SaveAccount` and `GetAccount` SQL grow the new columns. No new methods on the `Store`
  interface — the auth-method discriminator is a property of the row, not a separate
  CRUD surface.
- `ListAllAccounts` and `ListAccounts` (storage.go:104, 110) return the new fields
  without filtering changes; the scheduler doesn't care about auth method.
- New helper, **not** on the `Store` interface, lives in
  `services/shared/storage/postgres/postgres.go` only if needed for testing — otherwise
  the integration tests in `postgres_test.go` cover the round-trip.

### 6.4 `services/ingestion/internal/provider/aws/aws.go` and the verify endpoint

This is the core change. Two new constructors alongside the existing `NewWithStaticCredentials`
(aws.go:43):

```
NewWithAssumedRole(ctx, roleARN, externalID, region) (*Client, error)
    → loads default config
    → wraps stscreds.NewAssumeRoleProvider with ExternalId
    → calls GetCallerIdentity to resolve account_id (matches existing aws.go:54)
    → returns *Client

NewForAccount(ctx context.Context, account model.Account) (*Client, error)
    → switches on account.AuthMethod:
        "access_key" → decrypt secret, call NewWithStaticCredentials
        "role"       → call NewWithAssumedRole(account.RoleARN, account.ExternalID, account.Region)
    → opaque to caller; ingestion main.go calls this regardless of auth method
```

`NewForAccount` is the integration point. It moves the `crypto.Decrypt` call currently
in `services/ingestion/cmd/main.go:311` *into* the provider package. That is the right
home for it: the provider knows what credentials it needs, the entry point doesn't.
It also keeps the entry point flat (no more `if account.AuthMethod == ...` branches in
`main.go`).

**`AccountID` resolution is non-optional.** Both constructors must call
`sts.GetCallerIdentity` with the resolved credentials before returning, populate
`account_id` on the resulting `*Client`, and surface it back to callers — same pattern
as the existing `aws.go:54` flow. The verify endpoint (next item) returns this value
to the API handler so it can be persisted into `accounts.account_id` (`model/account.go:11`).
This must not be skipped during implementation: without it, downstream filtering by
AWS account number (the existing `internal_account_id` flow on the costs screen) breaks
silently for role-based accounts.

**New endpoint: `POST /v1/credentials/verify` on the ingestion service.** Per Item 1
in §4.4, the synchronous `sts:AssumeRole` for onboarding lives here, not in the API
service.

```
POST /v1/credentials/verify
Body:    {role_arn, external_id, region}
Returns: {ok, account_id?, code?, reason?, detail?}
```

Implementation sketch:
- Auth: same pattern as the existing `POST /scan` endpoint (intra-VPC, no JWT;
  verifies caller via the existing ingestion-side middleware).
- Calls `NewWithAssumedRole(ctx, role_arn, external_id, region)` directly. Success
  yields a `*Client` whose `AccountID` is populated by `GetCallerIdentity` — return it
  in the response body so the API handler can persist it.
- 900-second session. Credentials are discarded immediately after the response is
  written; this endpoint is stateless.
- Errors map to structured `code`/`reason` pairs (`role_assume_failed` /
  `trust_policy_mismatch`, `external_id_mismatch`, `role_not_found`, etc.) so the
  dashboard can render targeted help text.

The API service grows **no** AWS SDK import. The verify endpoint is reached via the
same intra-VPC HTTP plumbing the API already uses to call `POST /scan` on ingestion.

### 6.5 STS credential caching — discussion

AssumeRole returns credentials valid for `DurationSeconds` (default 1h, max 12h with
session policies, 1h with role-chaining). A typical AxiaOps scan takes minutes. So
within one scan, one assumption is enough.

The question is across scans:

- **Option A: re-assume on every scan.** Simplest. STS calls cost nothing material
  (~$0/month at our scale; STS is not a metered API). One extra ~50 ms call per scan.
  Recommended.
- **Option B: cache short-lived creds in Redis** keyed by account ID, TTL a few minutes
  short of the AWS expiry. Saves the STS call.
  Adds a Redis dependency on a hot path that already runs against an unlimited free
  API. Not recommended.
- **Option C: rely on aws-sdk-go-v2's built-in `aws.CredentialsCache`.** The SDK does
  in-memory caching of AssumeRole responses inside a single process. It does not survive
  process restarts and does not coordinate across replicas. For App Runner's scale-to-zero
  model, this is "free" optimisation that helps within a single warm process and gracefully
  degrades to "always re-assume" otherwise.

Recommendation: **A + C combined**. Use the SDK's built-in `CredentialsCache` (it is the
default behaviour of `stscreds.NewAssumeRoleProvider`; we get it by not disabling it).
Do not add Redis. Document that scans incur one STS call apiece in the worst case.

**Throttling.** AWS rate-limits `sts:AssumeRole` per AxiaOps-side account
(not per customer-side role); when scheduled scans fan out across many connected
accounts in the same minute, the *caller* — i.e. the AxiaOps task role — is the
throttled principal, not each individual customer. Retry/backoff for STS calls reuses
the existing helper at `services/shared/retry/` (the same package the Cost Explorer
and CloudWatch calls already wrap), so there is nothing new to build — just remember
to wrap `AssumeRole` the same way at construction time in `NewWithAssumedRole`.

A second consideration: **handle expiry mid-scan**. A scan that runs longer than 1h can
exhaust the credentials. The SDK's `CredentialsCache` auto-refreshes as long as the
underlying provider can re-assume. The `stscreds.NewAssumeRoleProvider` *can* re-assume
because we hold the role ARN + ExternalId for the lifetime of the request. So no extra
work — we simply must not pin credentials by extracting them once and using them for the
whole scan. Pass the `*aws.Config` everywhere (already the pattern, see `aws.go:104`).

### 6.6 `services/ingestion/cmd/main.go`

Replace the explicit `crypto.Decrypt` + `NewWithStaticCredentials` chain
(`main.go:308-324`, `main.go:352-356`) with a single `aws.NewForAccount(ctx, account)`
call. The error paths are identical:

```
"decrypt credentials" → "load credentials"
"aws init" → "aws init"
```

A new error class is possible — `ErrRoleAssumeFailed` — for the case where AssumeRole
returns AccessDenied during a scheduled scan (because the customer modified the trust
policy, deleted the role, etc.). That should set `account.Status='error'` and
`account.ErrorMessage='role assume failed: <reason>'`, which the dashboard can surface.
The existing scheduled-scan error path already sets `error` status; this is a metadata
addition, not a new state.

**`error_message` is populated for both auth methods, not just role-based ones.** The
existing access-key scan failure path (decrypt failure, malformed key, AWS
`InvalidClientTokenId`, throttling) currently leaves operators reading logs to figure
out what went wrong. As part of this work, that path is updated to write the same
`error_message` column — single source of truth for "why is this account failing".
The dashboard's error rendering does not need to branch on auth method; it just shows
`error_message` when `status='error'`. Treat this as part of the migration ticket, not
a separate cleanup.

### 6.7 `services/api/internal/api/handler.go`

The two endpoints in `handler.go:550-700` are restructured into three:

1. **`POST /v1/accounts`** — the existing access-key creation path. Unchanged externally
   except a new field `auth_method` (defaults to `"access_key"` if absent for back-compat).
   When `auth_method='role'`, returns 400 with a hint pointing at the draft endpoint.
2. **`POST /v1/accounts/draft`** — new. Body: `{label, region, provider, auth_method:"role"}`.
   Generates ExternalId, inserts the row with `status='pending_role_setup'`, returns the
   account with the `external_id` and the AxiaOps trust principal ARN populated. No
   AWS call.
3. **`PATCH /v1/accounts/{id}`** — already exists (handler.go:617). Grows two new optional
   fields: `role_arn` and a verify trigger. Behaviour:
   - If `role_arn` is set and the account is in `pending_role_setup`: forward to
     ingestion's `POST /v1/credentials/verify` per §4.4, flip to `connected` on success
     (and persist the `account_id` returned by ingestion), stay in `pending_role_setup`
     with `error_message` populated on failure.
   - If `role_arn` is set on a `connected` role-based account: same flow, used for
     re-verification ("my role ARN changed", or recovery from `error` state).
   - All other fields: existing behaviour.

The existing in-memory scan-lock map (`api/CLAUDE.md` "Key Patterns" §"Scan lock") is
unaffected — locks are keyed by account ID, the auth method does not matter.

**No AWS SDK in the API service.** This handler does not import `aws-sdk-go-v2/...`;
verification is delegated to ingestion via HTTP. `services/api/go.mod` stays
SDK-free, preserving the boundary `services/shared/CLAUDE.md` calls out.

**Application-level validation:** when `auth_method='role'` is requested,
the handler rejects any `provider != 'aws'` with a 400 before touching the database
(see Item 10 in §5.3 for the matching CHECK-constraint backstop). Friendly error
first, integrity constraint second.

### 6.8 `services/api/internal/middleware/`

No change. RBAC for "who can connect an account" is already enforced via the
`accounts:create` / `accounts:update` permissions; both apply regardless of auth method.

### 6.9 Dashboard

Out of scope for *this* design (per the prompt's "do not change frontend" instruction),
but for completeness the React-side change is:

- `services/dashboard/src/screens/ConnectScreen.jsx` grows the two-tab selector. The
  Access Keys tab is unchanged. The Role ARN tab adds the §4.2 flow.
- New API client functions in `services/dashboard/src/api/client.js`:
  `draftAccount({label, region})` and `verifyAccount(id, {role_arn})`.

The dashboard work originally shipped behind a `VITE_FEATURE_ROLE_AUTH` feature flag so it
could land ahead of the backend without exposing partial UX. The flag has since been removed
(issue #81) — role-based onboarding is now the default Connect-screen tab and Access Keys
remain reachable as a secondary tab.

### 6.10 Testing

- `services/ingestion/internal/provider/aws/aws_test.go` — add a test that constructs a
  mock STS interface (`StsAPI`) and verifies `NewWithAssumedRole` calls `AssumeRole` with
  the right `ExternalId`. Mirrors the existing `mockCEClient` pattern referenced in
  `services/ingestion/CLAUDE.md`.
- `services/api/internal/api/handler_test.go` — three new tests:
  1. `POST /v1/accounts/draft` returns a row with non-empty `external_id`, `status='pending_role_setup'`.
  2. `PATCH /v1/accounts/{id}` with a stub STS client that succeeds → status flips to `connected`.
  3. Same with stub STS that returns AccessDenied → status stays `pending_role_setup`,
     `error_message` populated, audit row written with `role_verify_failed`.
- `services/shared/storage/postgres/postgres_test.go` — round-trip a role-based account
  through `SaveAccount` / `GetAccount` / `ListAccounts`; assert the CHECK constraints
  reject malformed rows.

No real AWS calls in any test.

---

## 7. Migration & Coexistence

### Recommendation: long-running coexistence, no forced sunset

Three options:

| Option | Sketch | Verdict |
|---|---|---|
| Hard sunset on date X | Pick a date, email customers, force migrate or block scans | Hostile to existing customers; SMB tier cannot self-serve role creation; we have no leverage to enforce |
| Sunset by tier ("enterprise must use roles, SMB can keep keys") | Plan tier blocks access keys at signup | Better, but premature for the design — tier-based gating is a product decision, not a technical one, and belongs to a later ticket once tiers exist in the billing model |
| **Indefinite coexistence** | Both work, role is the default and recommended in UX, access keys quietly become legacy | **Recommended** |

Reasoning: access keys are not a *correctness* problem, they are a *fit-for-enterprise*
problem. The fix for "we cannot land enterprise" is "role auth exists and is the default";
forcing existing SMB customers to migrate solves nothing and risks churn. The day we have
a paid customer who insists on "no access-key code in the codebase" we revisit.

### Operational implications

- The encryption key (`ENCRYPTION_KEY`, `services/shared/CLAUDE.md` §"Crypto") stays
  required for the lifetime of access-key support. Same rotation plan as today.
- The IAM policy doc in `docs/production.md` lines 39-60 grows a new section: "Trust
  policy for customer-side roles" with §3.1 inlined. Mention this update as part of the
  implementation PR.
- The "Account Settings" page in the dashboard shows the auth method as a read-only
  badge. A "Migrate to role" button can be added later (link to the draft flow with the
  same label/region pre-filled), but it's out of scope for v1.

### Path if we ever do sunset

If a future product call decides access keys must go:

1. Block new access-key accounts at the API layer (`POST /v1/accounts` rejects
   `auth_method='access_key'`).
2. Email existing customers with a migrate-by date.
3. After the date, add a banner in the dashboard for affected accounts.
4. After grace period, scheduled scans fail with a clear message; customer can still
   migrate from the dashboard.
5. Eventually, a migration drops `access_key_id`, `secret_encrypted`, and the discriminator.

None of that happens now. Documented for completeness.

---

## 8. Open Questions

These need product / GTM input before implementation starts:

- [ ] **CloudFormation Quick-Create vs raw JSON only.** §3.3 recommends both with
      Quick-Create as primary CTA. Confirm: are we comfortable hosting the template at
      `s3://axiaops-public-templates/v1/role.yml` and committing to a versioning policy?
      Alternative: customer-supplied Terraform module — same effort, narrower audience.
- [ ] **AxiaOps AWS account ID per environment.** Production gets one; staging gets
      another. The dashboard renders whichever matches `APP_ENV`. Confirm we are happy
      hard-coding these two account IDs as build-time constants (recommended) vs
      reading from env vars (more flexible but easy to misconfigure → broken trust
      policy). See the next bullet for the local-dev wrinkle.

- [ ] **Local-dev / `make start-dev` trust principal.** The trust policy template names
      `AxiaOpsScanner` as the principal (§3.1). In `make start-dev`, ingestion runs
      as the developer's local IAM user — there is no `AxiaOpsScanner` role on the local
      machine. Two options for testing role-based onboarding against a real customer
      account during development:
      - **Clean (recommended):** staging gets its own dedicated AxiaOps AWS account
        with its own `AxiaOpsScanner` role; developers test role-based onboarding
        end-to-end by deploying to staging. The local `make start-dev` flow continues
        to support *only* the access-key path for development convenience. The trust
        policy template the dashboard renders against `APP_ENV=development` can warn
        the developer that the role flow needs to be exercised in staging.
      - **Messy (rejected):** staging's trust policy template includes a permitted
        IAM-user principal (the developer's local user) alongside `AxiaOpsScanner`. This
        means staging's customer-side trust policies grant access to a human identity,
        which is exactly the anti-pattern §3.1 calls out (`"AWS": ".../root"` is bad
        for the same reason). Also creates trust-policy churn every time a developer
        joins or leaves.
      Recommend the clean option: invest in a separate staging AWS account with its
      own task role, and accept that `make start-dev` does not exercise the role flow.
      The verify endpoint can still be unit-tested with mock STS in `start-dev`; it is
      the end-to-end customer-side trust policy that needs a real staging environment.
- [ ] **Customer-side role default name.** The role the *customer* creates in *their*
      account (separate from our `AxiaOpsScanner` principal — the AxiaOps-side name is
      settled per §3.1). Options: `AxiaOpsIntegrationRole` (matches Datadog's
      `DatadogIntegrationRole` shape) vs `AxiaOpsScannerRole` vs configurable.
      Recommendation: `AxiaOpsIntegrationRole` as the default in the Quick-Create
      template, with a free-text override in the Connect form for customers who namespace.
      A customer rename is harmless because the role ARN, not the role name, is what we
      store and call.
- [ ] **Region scope of trust policy.** §3.5 leaves region as a per-account preference.
      Confirm we never need a "this role can only be assumed from these regions" condition
      — we currently call STS from one App Runner region, so a `aws:RequestedRegion`
      condition would lock us in. Recommendation: omit, document.
- [ ] **Multi-account customer onboarding.** A customer with 5 AWS accounts goes through
      the §4 flow 5 times. Acceptable for v1. Bulk onboarding via AWS Organizations
      service-managed StackSets is a real Phase 4+ feature (Datadog calls it "AWS
      Integration via Organization") and not in scope here. Confirm.
- [ ] **Read-only enforcement at the SCP layer.** Some customers run an SCP that denies
      `sts:AssumeRole` from outside their org. They will need to whitelist the AxiaOps
      AWS account ID. Document this in the onboarding help text.
- [ ] **Session tag for downstream IAM policies.** Some enterprises use
      `aws:PrincipalTag/CostCenter` in resource policies. Setting `Tags` on the
      AssumeRole call (e.g. `[{Key:"AxiaOpsOrg", Value:"<org_id>"}]`) makes that
      possible. Cost: small. Benefit: a future enterprise lever. Recommendation: do it
      from day one, document it; cost of adding later is non-zero (every customer's
      trust policy would need to allow `sts:TagSession`).
- [ ] **Gov-cloud / China.** AWS partition prefix differs (`arn:aws-us-gov:`,
      `arn:aws-cn:`). Trust policy template needs the right prefix. Detect from the role
      ARN format on verify, render the matching template. Confirm we want to support
      these regions at all in Phase 2; if no, defer the partition logic.
- [ ] **Role rotation / ExternalId rotation.** §2 says "no rotation in v1". Confirm.

---

## Related Decisions

- **Why not deploy CloudFormation stacks programmatically?** The customer would have
  to grant us `cloudformation:CreateStack` plus `iam:CreateRole` plus
  `iam:AttachRolePolicy` *before* we can create the stack — i.e. a write IAM policy.
  That is exactly what the role onboarding pattern exists to *avoid*. Quick-Create
  URLs solve the same UX problem without needing any pre-existing AxiaOps access.
- **Why not OIDC / web-identity federation?** That is the right pattern for *AxiaOps'*
  CI/CD assuming roles into AWS, not for customer accounts assuming roles toward
  AxiaOps. Not the same problem.
- **Why not IAM Identity Center / SSO?** Identity Center federates *human* identities;
  this is a service-to-service trust problem.

---

## Appendix A — Why STS calls themselves are free

STS is not a metered API for AssumeRole. From AWS pricing:
"AWS Security Token Service (STS) is a feature of IAM that is offered at no additional
charge." So the §6.5 Option A recommendation ("re-assume on every scan") has zero
direct cost beyond the ~50 ms latency. The cost-awareness section of `CLAUDE.md`
("FinOps for AxiaOps itself") does not need to grow a new line item for this.

## Appendix B — Comparison with peer tools (informational)

| Tool | Onboarding | ExternalId | Quick-Create | Notes |
|---|---|---|---|---|
| Datadog | Role ARN | Yes | Yes | Default and only option since 2019 |
| CloudHealth | Role ARN | Yes | Yes | Has a key-based fallback for very small customers |
| Vantage | Role ARN | Yes | Yes | Same shape we're proposing |
| Wiz | Role ARN | Yes | Yes | Plus optional CFN StackSet for Org-wide |
| AxiaOps (today) | Access keys | — | — | What §1 proposes to fix |

Position after this design ships: AxiaOps matches the standard enterprise AWS
onboarding contract.
