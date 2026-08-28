# Developer Guide — AxiaOps

How to be productive in this codebase, ranked roughly first-day → first-month.

> Read [ARCHITECTURE.md](ARCHITECTURE.md) first if you haven't. This guide assumes you understand the service layout.

---

## 0. Prerequisites

| Tool | Version | Why |
|---|---|---|
| Go | 1.25+ | Backend services. Uses `log/slog`, `http.ServeMux` `r.PathValue()`, and other modern stdlib features. |
| Node | 20+ | Dashboard build (Vite + React). |
| Docker + Compose | recent | Postgres, Redis, full-stack `start-staging`. |
| `make` | any | Workflow entry point. |
| `gh` | recent | GitHub CLI, for opening PRs. |
| AWS account | — | For real scans. Use a personal sandbox account or read-only role. |
| `psql` | 16+ | Optional but useful for poking the DB directly. |

Optional but nice:
- `delve` for Go debugging
- `mkcert` if you need local TLS

---

## 1. First-day setup

```bash
# Clone
git clone git@github.com:ahmd-soliman/axiaops.git
cd axiaops

# AWS credentials — put in services/ingestion/.env or export in your shell.
# `make start-dev` will refuse without them.
cp services/ingestion/.env.example services/ingestion/.env
$EDITOR services/ingestion/.env

# 32-byte hex encryption key — needs to match between api and ingestion.
# Generate once and reuse:
openssl rand -hex 32   # paste into both .env files as ENCRYPTION_KEY

# Boot
make start-dev
```

`make start-dev` brings up:

- A Postgres 17 container (`docker compose up -d postgres`)
- API as a host-mode Go process on `:8080` with `DEV_MODE=true`
- Ingestion as a host-mode Go process on `:8081`
- Vite dev server for the dashboard on `:5173`

Open `http://localhost:5173`. With `DEV_MODE=true` you skip auth entirely — the dev user (`dev@axiaops.local`) lands directly in the dashboard with `owner` role on the dev organization.

To shut everything down: `make stop` (kills the host-mode Go processes AND `docker compose down`s the stack).

---

## 2. Two dev modes — `start-dev` vs `start-staging`

| | `start-dev` | `start-staging` |
|---|---|---|
| **Mode** | Host-mode Go binaries | Full docker-compose stack |
| **Auth** | Bypassed (`DEV_MODE=true`) | Native cookie auth (`DEV_MODE=false`) |
| **Redis** | Not started | Yes — sessions cache, queue, rate-limit |
| **Dashboard** | Vite dev server `:5173` | nginx-served bundle on `:8082` |
| **Use when...** | Day-to-day coding, tight feedback loop | Auth flows, Redis features, container parity, OIDC ceremony, cookie behaviour |

Spend most of your time in `start-dev`. Reach for `start-staging` only when you're debugging something that requires the full stack (cookie `Secure` flag, X-Forwarded-Proto handling, Redis-backed sessions, etc.).

`start-dev` first-run is documented in [AUTHENTICATION.md § 3](AUTHENTICATION.md) — but you won't hit it because DEV_MODE skips bootstrap.

### Fake-data scenarios (`DEV_SCENARIO`)

In `DEV_MODE=true`, ingestion runs against a fake AWS provider (`services/ingestion/internal/provider/fake`) instead of real AWS calls — same `runPipeline()` used in production, just a different data source, selected via `DEV_SCENARIO`:

| Scenario | Resources | Zombies | Use for |
|---|---|---|---|
| `startup` (default) | 4 | 2 | General dev work |
| `enterprise` | 12 | 6 | Demos, realistic multi-service mix |
| `all-zombies` | 3 | 3 | Dismiss/snooze workflow testing |
| `no-zombies` | 2 | 0 | Empty-state testing |

```bash
DEV_MODE=true DEV_SCENARIO=enterprise make start-dev
```

`make seed` instead loads persistent fixture data (12 zombies, 19 resources, 90 days of trend snapshots) for regular dev work. `make test-scenarios` unit-tests the fake provider itself — no DB/Docker/AWS required.

To add a scenario, add a case in `services/ingestion/internal/provider/fake/scenarios.go` (cost + usage records, joined on `ResourceID`) — zombie thresholds live in the detector (e.g. EC2 CPU ≤5% avg, RDS/Lambda/ELB 0 usage).

---

## 3. Code conventions you'll want to absorb

### Go

- **Error wrapping** — always `fmt.Errorf("context: operation: %w", err)`. Never bare returns. `%w` propagates the chain so callers can `errors.Is`/`errors.As`.
- **Logging** — `slog.Info/Warn/Error("action", "key", value)`. Never `log.Printf`. Init via `logging.Init(serviceName)` at startup; auto-attaches `service`, `env`, `version`, `commit_sha`.
- **Naming** — explicit, no abbreviations beyond `ctx`, `err`, `tx`, `mux`. `accountID` not `aid`; `organizationID` not `tid` (except in handlers for legacy `tid` reads from middleware — that's a known convention).
- **Handler pattern** — `Handler` struct with `New(store) → Register(mux) → method-per-route`. Routes registered as `mux.HandleFunc("METHOD /path", handler.Method)` using Go 1.22+ method-prefix syntax.
- **JSON responses** — always `writeJSON(w, data)` helper. Never raw `json.NewEncoder` — the helper sets `Content-Type` and handles errors.
- **Context propagation** — `storage.WithOrganizationID(ctx, organizationID)` before any DB call. The postgres Store explicitly errors at `services/shared/storage/postgres/postgres.go:87` if the context is missing the org-id, so forgetting fails fast.
- **Transactions** — `defer tx.Rollback(ctx)` immediately after `Begin()`. Even if you commit, the deferred rollback is a no-op after commit.
- **Constants** — named duration constants (`const stuckScanTimeout = 15 * time.Minute`).
- **Comments** — only when WHY is non-obvious. Don't narrate WHAT the code does — well-named identifiers do that.

### React / Dashboard

- **Theme tokens** — every color through `useTheme()`, never hardcoded hex. The token catalog is `services/dashboard/src/theme/ThemeContext.jsx`.
- **API client** — single seam in `services/dashboard/src/api/client.js`. Don't `fetch` directly.
- **Tabular numerals** — already enabled globally via `font-feature-settings: 'tnum'` in `index.css`. Currency columns will align without manual effort.
- **Monospace** — use `'"Geist Mono Variable", monospace'` (already propagated across all inline styles).
- **Per-screen file** — one screen = one file under `src/screens/` or `src/pages/`. Composition over deep prop-drilling.

---

## 4. Testing matrix

```bash
make test                # All Go unit tests across the workspace
make test-storage        # Postgres integration (RLS, migrations) — needs running postgres
make test-all            # Unit + storage
make test-integration    # Spins isolated docker-compose stack and runs end-to-end tests
make test-integration-api          # Just the API suite
make test-integration-ingestion    # Just the ingestion suite
make build-production    # go build -tags production — catches DEV_MODE leak regressions
```

**Conventions** (from [TESTING.md](TESTING.md)):

- Standard `testing` package only — **no testify** or third-party assertion libs.
- **Black-box tests**: `package foo_test`, not `package foo`.
- Mock external services (AWS SDK, HTTP clients) via interfaces. The provider seam at `services/ingestion/internal/provider/Provider` is the AWS abstraction — implement it for Azure/GCP later.
- Helper builders for fixtures: `costRecord()`, `usageRecord()`.
- `httptest.NewRecorder` for handler tests.
- **No real network calls** in unit tests.
- **Always** run `make test` before committing.

Detection rule changes have a special test workflow — see § 5.3 below.

---

## 5. Common workflows

### 5.1 Adding an API endpoint

Quick gist:

1. Decide on shape — REST verb + path + auth tier (public / authed / role-gated).
2. Add a handler method on the `Handler` struct in `services/api/internal/api/handler.go`.
3. Register it in `Register(mux)` with the correct method-prefix.
4. Pull org from context: `tid := middleware.OrganizationID(r.Context())`.
5. Push it back: `ctx := storage.WithOrganizationID(r.Context(), tid)`.
6. If you need new data access, add a method to the `Store` interface in `services/shared/storage/storage.go`, then implement in `postgres/postgres.go`.
7. Use `writeJSON(w, data)` for responses.
8. Add a test in `services/api/internal/api/handler_test.go` using `httptest.NewRecorder` + the mock Store.
9. Update [services/api/CLAUDE.md](../services/api/CLAUDE.md) endpoint table.

### 5.2 Adding a DB migration

Gist:

1. New file pair in `services/shared/storage/postgres/migrations/`: `NNN_description.up.sql` + `NNN_description.down.sql`. NNN is the next zero-padded number.
2. Per-organization tables need `organization_id UUID NOT NULL` + an RLS policy:
   ```sql
   ALTER TABLE my_table ENABLE ROW LEVEL SECURITY;
   CREATE POLICY tenant_isolation ON my_table
     USING (organization_id = current_setting('app.organization_id', true));
   ```
3. Migrations run on service startup using `MIGRATION_DATABASE_URL` (owner role). On every boot the wrapper drives `m.Steps(1)` and records each event in `axiaops.migration_history` — every up / down / force gets a forensic row with file SHA-256, build identity, and timing.
4. Don't forget the `.down.sql` — it gets run by integration test setup/teardown.
5. **Editing an already-applied migration triggers drift detection.** If you must mutate one (rare — usually you'd write a new migration instead), expect a `slog.Warn "migration_history: file checksum drift detected"` on the next migrate run. CI runs `MIGRATION_HISTORY_STRICT=true` so the boot fails, which is the canary you want.
6. For ad-hoc migrate operations use `bin/axiaopsctl migrate {up,down,force,drift,history}` (`make axiaopsctl` to build). A bastion `migrate` install bypasses the history table — never use that path.
7. If the schema change implies a new Store method, add to the `Store` interface + postgres impl + integration test under `services/shared/storage/postgres/postgres_test.go`.

See [ARCHITECTURE.md § 5](ARCHITECTURE.md) ("Migration system") for the deeper conventions.

### 5.3 Adding a detection rule

Already in [ARCHITECTURE.md § 7](ARCHITECTURE.md#7-detection-engine), but to summarise the dev loop:

```bash
# Edit serviceRules + provider code
# Then write the golden fixture:
mkdir services/shared/analyzer/testdata/golden/my_rule
$EDITOR services/shared/analyzer/testdata/golden/my_rule/input_costs.json
$EDITOR services/shared/analyzer/testdata/golden/my_rule/input_usage.json

# Generate the expected output by running the harness in update mode:
UPDATE_GOLDEN=1 go test ./services/shared/analyzer/...

# Review the generated expected_zombies.json. Commit only after eyeballing it —
# expected_zombies.json IS the spec.
```

The validators will reject unknown service names at fixture-load time with a labelled `*model.ValidationError`, so you don't get the silent "0 zombies" failure that an unregistered service would otherwise produce.

### 5.4 Adding a dashboard screen

1. New file under `services/dashboard/src/screens/MyScreen.jsx` or `src/pages/MyScreen.jsx`.
2. Add a route in `App.jsx`.
3. Use `useTheme()` for all colors. No hardcoded hex.
4. Data through `useQuery` (TanStack Query) hitting the API client (`api/client.js`).
5. Add a nav entry to `AppShell.jsx`'s `NAV_ITEMS` if it's a top-level screen, **with a `requires` permission gate if appropriate**.

### 5.5 Submitting work

We use GitHub. **Always**:

```bash
git checkout -b feat/short-name        # or fix/, chore/, docs/, experiment/
# ... commit work ...
git push -u origin feat/short-name
gh pr create --base main --title "feat(scope): tight summary" --body "..."
```

Conventions:

- **Commit messages**: `type(scope): subject` — `type` ∈ {feat, fix, chore, docs, experiment, refactor, test}, `scope` is the touched service/area. Body explains the **why**, not the what.
- **PR description**: summary + test plan checklist.
- Don't `git push --force` unless explicitly authorised. Don't bypass hooks (`--no-verify`).

---

## 6. Testing SSO locally (Entra / Keycloak)

Click-through validation of the OIDC RP — admin UX, email-blur discovery, the OIDC
ceremony, JIT membership provisioning — against a real IdP, faster than standing up
a staging deployment. SAML is not implemented; OIDC only.

**Common to both IdPs:**

- Stack: `make start-staging` (`DEV_MODE=false` — the SSO ceremony routes only
  register when native auth is active).
- `PUBLIC_HOST=http://localhost:8082` in `services/api/.env` — an empty value logs
  `PUBLIC_HOST is empty` and the IdP-registered redirect URI won't match.
- Redirect URI to register at the IdP: `http://localhost:8082/v1/sso/oidc/callback`
  (cid-less — connection identity flows through the OAuth `state` parameter, not the
  URL path; one registered URI covers every connection).
- Domain verification is bypassed for local testing with a direct Postgres `UPDATE`
  on `sso_domains` (`status='verified'`) — that column is the load-bearing field, not
  `verified_at`. This bypass is dev-only; real deployments go through DNS TXT
  verification.
- Leave **Enforcement = optional** until the round-trip works — flipping straight to
  `required` on a misconfigured connection locks out password login (the only escape
  is `/v1/auth/logout`).

**Microsoft Entra ID specifics:**

- Free `*.onmicrosoft.com` test tenant at <https://aka.ms/CreateTenant> — same
  issuer/JWKS shape as a corporate tenant.
- App registration → capture **Application (client) ID** and **Directory (tenant)
  ID** (ignore **Object ID**, internal Entra bookkeeping). Client secret's **Value**
  column is the actual secret — the **Secret ID** GUID next to it is never used by
  any OIDC client; pasting it by mistake produces `AADSTS700016`.
- Discovery URL: `https://login.microsoftonline.com/<tenant_id>/v2.0/.well-known/openid-configuration`.
- For group→role JIT mapping: add a **Groups claim** (Security groups, Group ID
  type) to **Token configuration**, and leave **"Emit groups as role claims"
  unchecked** — checked routes group IDs into `roles` instead of `groups`, and every
  JIT login silently falls through to the default role.
- Common errors: `AADSTS50011` (redirect URI not registered — exact-match, scheme/port/path all matter), `AADSTS50105` ("Assignment required" and the test user isn't assigned), `AADSTS50058`/`no_tokens_found` (third-party cookies blocked during MSAL silent renewal — allow cookies for `[*.]microsoftonline.com` or use a fresh Chrome profile).

**Keycloak specifics:**

- Any version ≥ 20. The **API container** must be able to reach
  `<keycloak>/realms/<realm>/.well-known/openid-configuration` — if Keycloak
  resolves differently from inside Docker than from your host, use the LAN IP or
  `host.docker.internal`, not `localhost`.
- Client: confidential (client auth on), Standard flow only, redirect URI as above.
- Group→role mapping: a **Group Membership** protoMapper on the client's dedicated
  scope, token claim name `groups`, **full group path off** (JIT does an exact-match
  lookup against bare names), **Add to ID token: on**.
- Watch the API logs for the audit trail: `SSO_LOGIN_SUCCEEDED`, then
  `SSO_JIT_PROVISIONED` (first login) or `SSO_JIT_ROLE_UPDATED` (re-login with a
  changed group). Failure reason codes (`state_invalid`, `code_exchange_failed`,
  `id_token_invalid`, `domain_unverified`, `cross_connection_domain`,
  `jit_failed`, `mint_session_failed`) are deliberately coarse in the audit
  trail — detail goes to `slog` only.
- `groups` claim missing from the ID token almost always means the mapper's "Add to
  ID token" toggle is off (defaults off).

Both runbooks validate the same surface end-to-end: `/v1/sso/discover` (pre-auth,
constant-shape lookup), `/v1/sso/oidc/{cid}/initiate` (PKCE + state + nonce), the
callback's full ID-token validation chain (alg-confusion guard rejects `none`/
`HS256`, issuer/audience/nonce, anti-spoofing domain check scoped to *this*
connection *and* this org), JIT provisioning, and `/v1/me` returning the
JIT-resolved role.

---

## 7. Debugging in VS Code

Attaching Delve to the host-mode Go services. Companion to `services/api/CLAUDE.md`
and the `.vscode/launch.json` header — the launch configs don't start Postgres for
you, pair them with a running stack.

**Prerequisites**: Go 1.25.x or 1.26.x — with **Go 1.26.2** the matching released
`dlv 1.26.2` panics inside `parseFileEntries5` on the API binary (slice-bounds
DWARF-parser bug); install Delve from `master`
(`go install github.com/go-delve/delve/cmd/dlv@master`) or downgrade Go to 1.25.x.
The program still runs if this happens — LLDB launches it in a separate process —
but breakpoints/stepping/variables silently don't work, so you just get logs.

**Stack pairing:**

| Compound config | Pairs with | Auth |
|---|---|---|
| **Debug Full Stack** (default) | `make start-dev`, then stop the host Go services it spawns (keep the Postgres container) | `DEV_MODE=true` |
| **Debug Full Stack (auth)** | `make start-staging`, then `docker stop axiaops-api axiaops-ingestion` | `DEV_MODE=false`, native sessions |

Hit **F5** with no config selected for the default compound, or pick **Debug
Migrate CLI** / **Debug Tests** (debugs the test focused in the editor) from the picker.

**Required env vars under `DEV_MODE=true`** — the API `die()`s at startup if
`DEV_ORGANIZATION_ID` is unset (no default; `DEV_USER_ID`/`DEV_USER_EMAIL` do have
defaults). The committed `launch.json` sets all three to match
`scripts/seed_test_data.sh`'s output — if you change `DEV_ORGANIZATION_ID` you must reseed.

**Where to break**: handler methods in `services/api/internal/api/handler.go` (one
breakpoint per route); `middleware/auth.go`'s `DevBypass`/JWKS path to watch
`organization_id` land on the context; `storage/postgres/postgres.go`'s
`LoadZombies`/`Summary` to inspect SQL params and the RLS-set `app.organization_id`.

The VS Code Debug Console takes raw Go expressions (`accountID`,
`storage.OrganizationIDFromCtx(r.Context())`) — no `print` prefix. Delve REPL
commands need a `dlv ` prefix (`dlv goroutines`, `dlv stack`). Identifiers must be
in scope at the currently-paused frame.

**Common failures**: empty dashboard after attaching → `make seed` (populates the
dev org with fixtures) or connect a real account and scan; "address already in use"
on :8082/:8080/:5432 → stale containers from a previous run, `make stop` then `docker
rm -f` stragglers if it doesn't clear them.

---

## 8. Versioning & releases

[Semantic Versioning 2.0.0](https://semver.org/spec/v2.0.0.html), no `v` prefix
(`0.3.0`, not `v0.3.0` — CI consumes the bare tag verbatim as `APP_VERSION`).
Pre-release suffixes: `-alpha.N` (internal/dogfood cuts, schema/API may change
without notice), `-beta.N` (design-partner cuts, breaking changes called out in the
CHANGELOG), `-rc.N` (no new features, only fixes), no suffix = a stable cut promised
to customers. **`1.0.0` is reserved** for GA / first paying customer — don't burn it
on a routine bump.

**Pre-1.0** (current): while `MAJOR` is `0`, semver §4 gives latitude, used with
discipline — `0.Y.0` for substantive feature work or non-strictly-additive
migrations, `0.Y.Z` for fixes/additive changes within the line. Only the latest
`0.Y.x` line is supported.

**Cutting a release**: update `CHANGELOG.md` (move `[Unreleased]` entries into a
dated `## [X.Y.Z]` section — format is [Keep a Changelog
1.1.0](https://keepachangelog.com/en/1.1.0/)) → tag `main` at the commit you want to
release with an **annotated** tag (`git tag -a 0.1.0-alpha.1 -m "..."`, never
lightweight — lightweight tags lose author/message metadata) → push the tag. CI does
the rest: `release.yml` validates the tag, waits for `ci.yml`'s `publish-images` job to
finish building/testing that exact commit, then promotes the already-built
`main-<shortsha>` images to the release tag with `docker buildx imagetools create`
(a manifest copy, not a rebuild — the release image is byte-for-byte what CI already
tested) and cuts a GitHub Release. **Never retag** — if a tag is wrong, cut the next
one and note the skip in the CHANGELOG. **Only tag `main`** — release tags are cut
from what's already merged, never from a feature branch.

**Migration backwards-compatibility contract** (what makes skip-a-minor upgrades
safe): never delete or edit a released migration — fix forward; never reuse a
migration number; `.down.sql` is for local dev only, production downgrade is
"restore from backup"; renames/drops are a two-release dance (add + dual-write, then
remove next release); migrations should be single-digit seconds on a 100k-row table
or need an operator runbook entry. See [ARCHITECTURE.md § 5](ARCHITECTURE.md) for
the migration wrapper's own mechanics (drift detection, `migration_history`).

---

## 9. The deployment topology, briefly

(Detail in [ARCHITECTURE.md § 4](ARCHITECTURE.md#4-deployment-topology).)

What you actually need to remember:

- **Three paths**: `docker compose` (local/single-host, `make start-dev` /
  `make start-staging`), Kubernetes via [`deploy/helm/`](../deploy/helm/axiaops/),
  or AWS via [`terraform/`](../terraform/) (ECS Express + RDS).
- **TLS termination is always someone else's job** — a reverse proxy, an ingress
  controller, or CloudFront, never the api/ingestion services themselves. They speak
  plain HTTP internally and trust `X-Forwarded-Proto` to decide whether the session
  cookie's `Secure` flag is set.
- **`PUBLIC_HOST`** must be the externally-reachable hostname, not an internal
  host/IP. Empty → SSO ceremonies fail at the IdP redirect. **`INTERNAL_DNS`** is
  only needed for split-horizon IdP setups.

---

## 10. Common gotchas

**For Go work:**

- **Go workspace** — `go.work` links `api`, `ingestion`, `shared`. Editor tooling (gopls) sometimes needs a kick after pulling new modules; restart the language server if imports go red.
- **Forgetting `WithOrganizationID`** fails fast — the postgres Store guards on a missing org-id-in-context and returns an error. Fix is one line, but it's easy to miss in a new code path. Run the integration test against a non-default org to be sure.
- **Forgetting `defer tx.Rollback`** is a connection leak waiting to happen. Idiom: `Begin → defer Rollback → ... → Commit (no-op rollback runs)`.
- **`os.Getenv("DEV_MODE")` directly** — don't. Use `devModeEnabled()` (`services/api/cmd/devmode_*.go` and same shape in ingestion). The lint job `test:lint:no-direct-devmode` rejects new direct reads.

**For dashboard work:**

- **Pre-ThemeProvider screens** — `LoginScreen.jsx`, `ConnectScreen.jsx`, `AccountSettingsScreen.jsx` render before the ThemeProvider mounts and hand-mirror dark colors. If you change theme tokens, double-check these three files. The drift hazard is documented in `services/dashboard/src/theme/COLORS.md`.
- **Mixing React Native idioms** — this is a pure web app (Vite + React). If you find yourself reaching for `View` or `Text` components, you're in the wrong mental model.
- **Hardcoded `'monospace'`** — every inline `fontFamily` should be `'"Geist Mono Variable", monospace'`. The codebase was sweep-updated; new code should match.

**For deployment / CI:**

- **`PUBLIC_HOST`**: empty → API logs `"sso: ceremony: PUBLIC_HOST is empty"` at startup and SSO ceremonies fail at the IdP redirect. Set per env scope in CI.
- **`INTERNAL_DNS`**: needed when the IdP hostname has split-horizon DNS. Without it, the API tries to resolve via public DNS, hits whatever WAF fronts the IdP (Cloudflare Bot Fight Mode rejects Go's default UA on `/.well-known/openid-configuration`), and OIDC discovery fails.

---

## 11. Where to ask, where to look

| Question | First-stop |
|---|---|
| "How does X work?" | [ARCHITECTURE.md](ARCHITECTURE.md) → linked CLAUDE.md → grep |
| "What's the AWS service rule for..." | [ARCHITECTURE.md § 7](ARCHITECTURE.md#7-detection-engine) |
| "How does first-run install / RBAC / SSO work?" | [AUTHENTICATION.md](AUTHENTICATION.md) |
| "How do I connect an AWS account / add a notification channel?" | [OPERATIONS.md](OPERATIONS.md) |

If a doc disagrees with the code, **the code wins** — file a PR to update the doc.

---

## 12. Glossary refresher

See [ARCHITECTURE.md § 12](ARCHITECTURE.md#12-glossary) for the full list. The terms that trip new developers most often:

- **Tier 1 vs Tier 2** — API-only detection vs CloudWatch-driven.
- **Mummer** — the AWS-API mock for integration tests. Fixtures in `test-infra/mummer/`.
- **start-dev vs start-staging** — see § 2.
