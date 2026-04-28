# Cross-Account Role Onboarding — Local Dev Runbook

Companion to [`cross-account-roles-design.md`](./cross-account-roles-design.md). The
design doc explains *why* AxiaOps onboards AWS accounts via `sts:AssumeRole` plus a
per-account `ExternalId`. This runbook is *how* to exercise the flow end-to-end on a
laptop, with AxiaOps running locally via `make start-dev` and **no AxiaOps
infrastructure deployed in AWS**.

The design doc's §8 "Local-dev / `make start-dev` trust principal" flags one wrinkle:
the trust-policy template names `role/AxiaOpsScanner` as the principal (production
identity), but locally the AxiaOps process runs under whichever IAM identity the
developer's AWS credentials resolve to — typically an IAM user, not a role. This
runbook works around that by editing the rendered trust policy before applying it
on the customer-side account.

## Topology

```
┌─────────────────────────┐                      ┌──────────────────────────┐
│ Account A — "AxiaOps"   │  sts:AssumeRole +    │ Account B — "Customer"   │
│                         │  ExternalId          │                          │
│  IAM user/role with     │ ───────────────────► │  IAM role                │
│  sts:AssumeRole on B    │                      │  AxiaOpsIntegrationRole  │
│  (local creds you put   │                      │  Trust: principal=A,     │
│   in env / ~/.aws)      │                      │         ExternalId=…     │
│                         │                      │  Perms: §3.2 read-only   │
│  ↑                      │                      │                          │
│  AxiaOps running on     │                      │  ↓                       │
│  your laptop (host-mode │                      │  Resources scanned:      │
│  Go services + Vite +   │                      │  EC2, RDS, Lambda,       │
│  local Postgres)        │                      │  CloudWatch, Cost Expl.  │
└─────────────────────────┘                      └──────────────────────────┘
```

- AxiaOps runs on your laptop via `make start-dev`. Nothing is deployed in AWS.
- Account A is your AxiaOps-side account — its credentials sit in env vars or
  `~/.aws/credentials` on your laptop. The base SDK config
  (`config.LoadDefaultConfig` in `services/ingestion/internal/provider/aws/aws.go`'s
  `NewWithAssumedRole` and `services/ingestion/cmd/verify.go`'s `newSTSClient`)
  picks them up.
- Account B is the account whose costs/zombies you want to scan. It hosts the
  `AxiaOpsIntegrationRole` you create through the dashboard onboarding flow.

A single-account variant works too (Account A and B collapsed into one) — the trust
policy still uses ExternalId. Two accounts mirror production more closely.

## Critical files (already in this branch)

- `services/ingestion/cmd/verify.go` — `POST /v1/credentials/verify` handler;
  `newSTSClient` is the SDK-config seam.
- `services/ingestion/internal/provider/aws/aws.go` — `VerifyAssumeRole`,
  `NewWithAssumedRole`, `NewForAccount`, `classifyAssumeRoleError`.
- `services/ingestion/internal/provider/aws/sts_api.go` — `STSAPI` interface.
- `services/api/internal/api/account_role.go` — `createDraftAccount`,
  `verifyRoleViaIngestion`, `generateExternalID`.
- `services/api/internal/api/handler.go` — route registration for
  `POST /v1/accounts/draft` and `PATCH /v1/accounts/{id}`.
- `services/dashboard/src/config.js` — `FEATURE_ROLE_AUTH`, `AXIAOPS_AWS_ACCOUNT_ID`
  flags.
- `services/dashboard/src/screens/ConnectScreen.jsx` — `trustPolicyJSON()` builds
  the trust-policy template; principal is hardcoded to
  `arn:aws:iam::${AXIAOPS_AWS_ACCOUNT_ID}:role/AxiaOpsScanner`.
- `services/shared/storage/postgres/migrations/019_account_role_auth.up.sql` — schema.
- [`cross-account-roles-design.md`](./cross-account-roles-design.md) §3.1 (trust
  policy), §3.2 (permissions), §4 (UX flow), §8 (local-dev caveat).

## Step 1 — Account A (AxiaOps side): IAM permissions

The IAM identity whose credentials sit in your laptop env / `~/.aws/credentials` only
needs **one** action: `sts:AssumeRole` on the role you'll create in Step 4.

Attach this policy (or inline it) to the local IAM user/role in Account A:

```json
{
  "Version": "2012-10-17",
  "Statement": [{
    "Effect": "Allow",
    "Action": "sts:AssumeRole",
    "Resource": "arn:aws:iam::<ACCOUNT_B_ID>:role/AxiaOpsIntegrationRole"
  }]
}
```

Verify with `aws sts get-caller-identity` — that proves the SDK can find the
credentials the same way ingestion will.

## Step 2 — Configure AxiaOps for `start-dev`

### 2a. AWS credentials for the ingestion service

Put Account A's credentials where the AWS SDK default chain picks them up. Easiest:

```
# services/ingestion/.env
AWS_REGION=eu-central-1
AWS_ACCESS_KEY_ID=AKIA…   # Account A access key
AWS_SECRET_ACCESS_KEY=…   # Account A secret
```

Or rely on `~/.aws/credentials` with `AWS_PROFILE=…` exported in your shell before
`make start-dev`. The SDK chain in `NewWithAssumedRole` uses
`config.LoadDefaultConfig` and accepts either.

### 2b. Dashboard feature flag + principal account ID

Vite reads `VITE_*` at dev time. Add to `services/dashboard/.env` (or `.env.local`):

```
VITE_FEATURE_ROLE_AUTH=true
VITE_AXIAOPS_AWS_ACCOUNT_ID=<ACCOUNT_A_ID>
```

`VITE_FEATURE_ROLE_AUTH=true` exposes the "Role ARN (recommended)" tab on the
Connect screen. `VITE_AXIAOPS_AWS_ACCOUNT_ID` populates the principal ARN that the
trust-policy template renders.

### 2c. Encryption key

`ENCRYPTION_KEY` is **not used** for role-based accounts (`aws.go:NewForAccount`
short-circuits decryption on `AuthMethod=role`), but `make start-dev` still requires
it to be set for the access-key path and migrations. Reuse whatever you already have
in `services/api/.env` and `services/ingestion/.env` from prior dev runs — no new
value needed. Generate one with `openssl rand -hex 32` if missing.

### 2d. Start everything

```bash
make start-dev
```

Auth is bypassed (`DEV_MODE=true`). Dashboard at `http://localhost:5173`, API at
`:8080`, ingestion at `:8081`, Postgres in Docker. Migration `019_account_role_auth`
runs automatically and adds the `auth_method`, `role_arn`, `external_id`, and
`error_message` columns plus the CHECK constraints.

## Step 3 — Onboard via the dashboard

1. Open `http://localhost:5173` → **Connect** screen.
2. Pick the **Role ARN (recommended)** tab.
3. Enter a Label (e.g. "Account B sandbox") and a Region (e.g. `eu-central-1`),
   then click **Generate connection**. This calls `POST /v1/accounts/draft`, which
   server-side generates a 256-bit `ExternalId`, persists an account row in
   `status='pending_role_setup'`, and returns it. The dashboard reveals Step 2.
4. The dashboard now shows:
   - **External ID** — copy this exact string.
   - **AxiaOps Principal ARN** — `arn:aws:iam::<ACCOUNT_A_ID>:role/AxiaOpsScanner`.
     **You will edit this** in Step 4b — see below.
   - **Show trust policy JSON** — click to reveal the full template.

## Step 4 — Account B (customer side): create the IAM role

### 4a. Permissions policy

In Account B (IAM console → Policies → Create policy → JSON), paste the read-only
permissions policy from [`cross-account-roles-design.md`](./cross-account-roles-design.md)
§3.2 (the 27-action `AxiaOpsReadOnlyScan` block). Save it as `AxiaOpsReadOnly`.

### 4b. Trust policy — **edit the principal**

Take the trust-policy JSON the dashboard rendered. It looks like:

```json
{
  "Version": "2012-10-17",
  "Statement": [{
    "Sid": "AllowAxiaOpsToAssumeForReadOnlyScans",
    "Effect": "Allow",
    "Principal": { "AWS": "arn:aws:iam::<ACCOUNT_A_ID>:role/AxiaOpsScanner" },
    "Action": "sts:AssumeRole",
    "Condition": { "StringEquals": { "sts:ExternalId": "axops-ext-…" } }
  }]
}
```

The hardcoded `role/AxiaOpsScanner` does not exist on your laptop — your local
identity is most likely `user/<your-iam-username>`. Replace that line with whichever
of these matches your local creds:

```json
"Principal": { "AWS": "arn:aws:iam::<ACCOUNT_A_ID>:user/<your-iam-username>" }
```

or, if your local creds belong to an IAM role:

```json
"Principal": { "AWS": "arn:aws:iam::<ACCOUNT_A_ID>:role/<your-role-name>" }
```

To find what to put: run `aws sts get-caller-identity` locally and copy the `Arn`
field verbatim.

> **Why the design doc said "test in staging only":** §8 calls out exactly this —
> in production the principal is a stable role (`AxiaOpsScanner`); locally it is a
> human identity, which means each developer would need their own trust-policy
> edit. We accept that small friction here because it is a local sandbox account,
> not a customer-facing trust contract.

### 4c. Create the role

IAM console → Roles → Create role → Custom trust policy → paste the edited JSON →
attach `AxiaOpsReadOnly` → name it `AxiaOpsIntegrationRole` → Create.

Copy the role ARN: `arn:aws:iam::<ACCOUNT_B_ID>:role/AxiaOpsIntegrationRole`.

## Step 5 — Verify and connect

Back in the dashboard Step 2 panel:

1. Paste the role ARN into the **Role ARN** input.
2. Click **Verify and connect**.

Under the hood:

- Dashboard calls `PATCH /v1/accounts/{id}` with `{role_arn}`.
- API handler (`account_role.go:verifyRoleViaIngestion`) POSTs
  `{role_arn, external_id, region, organization_id}` to ingestion at
  `http://localhost:8081/v1/credentials/verify` with a 30 s timeout.
- Ingestion (`verify.go:handleVerifyCredentials`) loads the AWS SDK config (your
  Account A creds) and calls `awsprovider.VerifyAssumeRole`, which executes
  `sts:AssumeRole` against Account B's role with the ExternalId condition.
- On success: ingestion returns `{ok:true, account_id:"<ACCOUNT_B_ID>"}`. API
  flips status to `connected`, persists the resolved AWS account ID, returns 200.
- On failure: ingestion returns `{ok:false, code, reason, detail}` with one of
  `external_id_mismatch`, `trust_policy_mismatch`, `role_not_found`,
  `malformed_policy`, `access_denied`, `unknown` — `ConnectScreen.jsx` renders
  targeted help text.

## Step 6 — Trigger a real scan against Account B

Once status is `connected`, hit **Scan now** on the account row, or:

```bash
curl -X POST http://localhost:8080/v1/accounts/<ACCOUNT_ID>/scan \
  -H 'Content-Type: application/json'
```

Ingestion's scan path goes through `aws.NewForAccount`, which dispatches on
`AuthMethod=role` and calls `NewWithAssumedRole`. The SDK's `aws.CredentialsCache`
auto-refreshes the assumed credentials transparently (design §6.5). Cost Explorer
+ CloudWatch + Describe APIs all run against Account B; results land in your local
Postgres.

## Verification

- `aws sts get-caller-identity` (locally) returns Account A's identity.
  **Required before `make start-dev`.**
- `make start-dev` boots cleanly; logs show `migration 019_account_role_auth applied`.
- `POST /v1/accounts/draft` returns a row with non-empty `external_id` and
  `status="pending_role_setup"`. Inspect via `psql`:
  ```sql
  SELECT id, auth_method, status, role_arn, external_id FROM axiaops.accounts;
  ```
- After Step 5, the same row reads `auth_method='role'`, `status='connected'`,
  `role_arn` populated, `account_id=<ACCOUNT_B_ID>`.
- After Step 6, `axiaops.cost_records` and `axiaops.zombie_records` have rows tied
  to Account B.
- Run the targeted Go tests on the branch (no AWS calls — they use stub STS):
  ```bash
  cd services/ingestion && go test ./cmd/... ./internal/provider/aws/...
  cd services/api        && go test ./internal/api/...
  ```
- `make test-storage` exercises the migration + CHECK constraints
  (`postgres_test.go` round-trips a role-based account).

## Common failure modes

| Symptom | Cause | Fix |
|---|---|---|
| Verify returns `external_id_mismatch` | Trust policy has a different ExternalId than the dashboard generated | Re-copy ExternalId from dashboard, update Account B trust policy |
| Verify returns `trust_policy_mismatch` | Principal in Account B trust policy doesn't match your local AWS identity | Run `aws sts get-caller-identity`, paste that ARN exactly into trust policy |
| Verify returns `role_not_found` | Wrong role ARN, or role not yet propagated (IAM is eventually consistent ~10 s) | Wait 30 s, retry |
| `start-dev` ingestion logs `failed to get caller identity` | No AWS creds reached the ingestion process | Check `services/ingestion/.env` has `AWS_ACCESS_KEY_ID` / `AWS_SECRET_ACCESS_KEY` |
| Dashboard shows access-key form only, no Role ARN tab | `VITE_FEATURE_ROLE_AUTH` not set | Add to `services/dashboard/.env`, restart Vite |
| Dashboard renders `<AxiaOpsAccountId>` as literal text | `VITE_AXIAOPS_AWS_ACCOUNT_ID` missing — `ConnectScreen.jsx` falls back to placeholder | Set the var to Account A's 12-digit account ID |

## Out of scope

- Deploying `AxiaOpsScanner` as an IAM role and assuming it locally (would let the
  unmodified trust-policy template work, but adds setup complexity — pick this if
  you want to mirror production exactly).
- LocalStack / STS emulation — would let you skip real AWS entirely, but weakens
  the ExternalId / trust-policy enforcement that is the whole point of this design.
- Pure-mocked stub mode (env-flag toggle around `newSTSClient`) — would require a
  small code change to `verify.go` and `aws.go`. Useful only for clicking through
  the UX without any AWS account.
- CloudFormation Quick-Create URL — design §3.3 ships this for production; locally
  you click through the IAM console manually.
