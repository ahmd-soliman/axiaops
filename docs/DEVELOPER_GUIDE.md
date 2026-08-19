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
| `glab` | recent | GitLab CLI. **Not `gh`** — this repo lives on GitLab. |
| AWS account | — | For real scans. Use a personal sandbox account or read-only role. |
| `psql` | 16+ | Optional but useful for poking the DB directly. |

Optional but nice:
- `delve` for Go debugging
- `mkcert` if you need local TLS

---

## 1. First-day setup

```bash
# Clone
git clone git@gitlab.com:axiaops/axiaops.git
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
| **License** | Dev fixture (`customer_id="axiaops-dev-fixture"`) auto-injected | Same dev fixture (per `make start-staging` target) — separate from deployed staging which gets a CI-minted production-signed JWT |

Spend most of your time in `start-dev`. Reach for `start-staging` only when you're debugging something that requires the full stack (cookie `Secure` flag, X-Forwarded-Proto handling, Redis-backed sessions, etc.).

`start-dev` first-run is documented in [docs/native-auth-bootstrap.md](native-auth-bootstrap.md) — but you won't hit it because DEV_MODE skips bootstrap.

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

**Conventions** (from [TEST_STRATEGY.md](TEST_STRATEGY.md)):

- Standard `testing` package only — **no testify** or third-party assertion libs.
- **Black-box tests**: `package foo_test`, not `package foo`.
- Mock external services (AWS SDK, HTTP clients) via interfaces. The provider seam at `services/ingestion/internal/provider/Provider` is the AWS abstraction — implement it for Azure/GCP later.
- Helper builders for fixtures: `costRecord()`, `usageRecord()`.
- `httptest.NewRecorder` for handler tests.
- **No real network calls** in unit tests.
- **Always** run `make test` before committing.

Detection rule changes have a special test workflow — see § 6 below.

---

## 5. Common workflows

### 5.1 Adding an API endpoint

The full play is documented in the [api-endpoint skill](../.claude/skills/api-endpoint/) — auto-loads when you ask Claude to add an endpoint. Quick gist:

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

The full play is documented in the [db-migration skill](../.claude/skills/db-migration/). Gist:

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

See [docs/migrations.md](migrations.md) for the deeper conventions.

### 5.3 Adding a detection rule

Already in [ARCHITECTURE.md § 8](ARCHITECTURE.md#8-detection-engine), but to summarise the dev loop:

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

Documented in the [dashboard-screen skill](../.claude/skills/dashboard-screen/). Quick gist:

1. New file under `services/dashboard/src/screens/MyScreen.jsx` or `src/pages/MyScreen.jsx`.
2. Add a route in `App.jsx`.
3. Use `useTheme()` for all colors. No hardcoded hex.
4. Data through `useQuery` (TanStack Query) hitting the API client (`api/client.js`).
5. Add a nav entry to `AppShell.jsx`'s `NAV_ITEMS` if it's a top-level screen, **with a `requires` permission gate if appropriate**.
6. If the screen exposes a list/table the user might want to export, add CSV download via the [csv-export skill](../.claude/skills/csv-export/).

### 5.5 Submitting work

We use GitLab. **Always**:

```bash
git checkout -b feat/short-name        # or fix/, chore/, docs/, experiment/
# ... commit work ...
git push -u origin feat/short-name
glab mr create --target-branch develop --title "feat(scope): tight summary" --description "..."
```

Conventions:

- **Commit messages**: `type(scope): subject` — `type` ∈ {feat, fix, chore, docs, experiment, refactor, test}, `scope` is the touched service/area. Body explains the **why**, not the what.
- **MR target**: `develop` by default; `main` only for hot-fixes targeting production.
- **MR description**: summary + test plan checklist. Use the `gitlab-mr-create` skill template.
- Per [/CLAUDE.md](../CLAUDE.md), delegate the actual commit creation to the `commit` agent and code review to the `code-reviewer` agent before pushing.
- Don't `git push --force` unless explicitly authorised. Don't bypass hooks (`--no-verify`).

---

## 6. The deployment topology, briefly

(Detail in [ARCHITECTURE.md § 4](ARCHITECTURE.md#4-deployment-topology) and [docs/deployment.md](deployment.md).)

What you actually need to remember:

- **Each env runs on its own self-hosted host.** Dev-1, dev-2, staging are separate physical hosts at `192.0.2.121` / `.123` / `.122`. Production is AWS ECS Express Mode (account `123456789012`, `eu-central-1`), fronted by CloudFront.
- **an edge proxy (an edge proxy)** is the edge. Browser hits `https://axiaops-<env>.local`, an edge proxy terminates TLS and reverse-proxies to the host's docker-compose stack on plain HTTP. The dashboard's nginx propagates `X-Forwarded-Proto` so the API's session cookie correctly toggles `Secure`.
- **CI deploys via SSH-as-Docker-context.** `DOCKER_HOST: ssh://deploy@${DEPLOY_HOST_IP}` lets the CI runner's `docker compose up` execute on the per-env host's daemon. `DEPLOY_SSH_PRIVATE_KEY` **must** be a File-type CI variable.
- **All `deploy:*` jobs are manual gates.** They never auto-trigger. If one is running, an operator clicked it.
- **`PUBLIC_HOST`** and **`INTERNAL_DNS`** are per-env CI variables. Empty `PUBLIC_HOST` → SSO ceremonies fail at the IdP redirect.

---

## 7. Common gotchas

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
- **Adding a new env** isn't a port-pair change. You need: provision a new self-hosted host via `self-hosted-infra/stacks/axiaops-dev` Terraform, register the hostname in an edge proxy with a TLS cert, add `deploy:<env>` + `gate:devmode:<env>` CI jobs.

**Source control:**

- **GitLab, not GitHub.** `gh pr ...` will fail. Use `glab mr ...`. Terminology: "MR" / "merge request", not "PR".

---

## 8. Where to ask, where to look

| Question | First-stop |
|---|---|
| "How does X work?" | [ARCHITECTURE.md](ARCHITECTURE.md) → linked CLAUDE.md → grep |
| "What's the AWS service rule for..." | [aws-coverage.md](aws-coverage.md) |
| "Is Tier 2 detection done for X?" | [tier2_detections_status.md](tier2_detections_status.md) |
| "Why isn't CloudTrail wired in?" | [cloudtrail-analysis.md](cloudtrail-analysis.md) — RoI deferral doc |
| "How does first-run install work?" | [native-auth-bootstrap.md](native-auth-bootstrap.md) |
| "How does invite-flow redemption work?" | [invitation-flow.md](invitation-flow.md), [invitations-manual-test.md](invitations-manual-test.md) |
| "Why was decision X made?" | `docs/decisions/` ADRs + `docs/change_list_2026_*.md` |
| "What's the CI pipeline doing?" | [ci.md](ci.md), [ci_cd_quick_reference.md](ci_cd_quick_reference.md) |
| "Phase 2 status?" | [phase2-plan.md](phase2-plan.md), [PHASE2_STATUS.md](PHASE2_STATUS.md) |
| "Tasks.md vs ARCHITECTURE — which is canonical?" | Tasks.md tracks open work; ARCHITECTURE describes shipped state. |

If a doc disagrees with the code, **the code wins** — file an MR to update the doc.

---

## 9. Glossary refresher

See [ARCHITECTURE.md § 13](ARCHITECTURE.md#13-glossary) for the full list. The terms that trip new developers most often:

- **Tier 1 vs Tier 2** — API-only detection vs CloudWatch-driven.
- **Mummer** — the AWS-API mock for integration tests. Fixtures in `test-infra/mummer/`.
- **start-dev vs start-staging** — see § 2.
- **an edge proxy** — an edge proxy (the edge proxy), **not** the JavaScript package manager.
