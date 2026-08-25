# Runtime RLS-bypass role (`axiaops_runtime`)

**Status:** code + self-hosted non-dev envs shipped (2026-05-29 on preview, then staging/demo/integration); production (ECS Express) pending the aws-infra apply per §"Deployed-env wiring" below.
**Drives:** *"Remove `MIGRATION_DATABASE_URL` from the runtime services."*
**Related:** `TODO(#107)` (the `NewWithOwner` seam), security finding **H-1** (#94, app-pool RLS refactor).

## Problem

`MIGRATION_DATABASE_URL` is the `axiaops_owner` **schema-owner** connection. The §2.16 box reads as "stop injecting it into the api + ingestion task defs — only the migrate task needs it," but the naive delete would silently break production: the runtime services use that connection at runtime — **not for migrations** — as the RLS-bypassing `adminPool`.

- `services/api/cmd/main.go:72-96` and `services/ingestion/cmd/main.go:718-731` read it, `die()` outside DEV_MODE if empty, and pass it to `postgres.NewWithOwner` (`services/shared/storage/postgres/postgres.go:39-53`).
- `adminPool` bypasses RLS for: native login / `LookupUserByEmail` (reads `memberships`/`users` with no `app.organization_id`), `/v1/me` `GetUserByID`, scheduled-scan `ListAllAccounts` (spans all orgs), cross-org sweeps (`invitations.go`, `sso.go`), the GDPR org-cascade purge, and `ResetStuckScans`.
- It works only because `axiaops_owner` **owns** every table and no table has `FORCE ROW LEVEL SECURITY` — owners are exempt from RLS. Deleting the env var makes `NewWithOwner` fall back to the RLS-bound app pool, and those cross-org/pre-auth reads return **zero rows**.

So the runtime genuinely needs a bypass connection. The problem is that it currently uses the **schema owner**, which also carries DDL / DROP / ownership — far more than a long-lived, internet-adjacent service should hold. An RCE in the always-on api/ingestion container today gets the keys to reshape the schema.

**Goal:** give the runtime a *least-privilege* RLS-bypass role (DML only, no DDL, no ownership) and reserve the schema-owner connection for the one-off migrate task.

## Why not a `BYPASSRLS` role (the obvious answer)

The obvious design is a dedicated role with PostgreSQL's `BYPASSRLS` attribute. **This does not work on the production target (AWS RDS).** Setting `BYPASSRLS` requires a true superuser; on RDS the master/owner role is `rds_superuser`, which is *not* a real superuser and **cannot grant `BYPASSRLS`** ([RDS docs](https://docs.aws.amazon.com/AmazonRDS/latest/UserGuide/Appendix.PostgreSQL.CommonDBATasks.Roles.rds_superuser.html), [PG role attributes](https://www.postgresql.org/docs/current/role-attributes.html)). The migrate task's `ALTER ROLE … BYPASSRLS` would succeed against the local docker Postgres (a real superuser) and **fail on prod RDS** — a local/prod divergence. This is why the existing owner bypasses via *ownership*, never the attribute.

## Chosen mechanism — per-role permissive RLS policy

Keep a dedicated role `axiaops_runtime` as a **plain DML role** — `NOLOGIN`/`NOSUPERUSER`/`NOCREATEDB`/`NOCREATEROLE`, **no ownership, no `BYPASSRLS`** — and give it cross-org visibility with a **permissive RLS policy per RLS-enabled table**:

```sql
CREATE POLICY <table>_runtime_bypass ON axiaops.<table>
    TO axiaops_runtime USING (true) WITH CHECK (true);
```

PostgreSQL combines permissive policies with **OR**, so for `axiaops_runtime` the effective predicate becomes `<existing org-isolation policy> OR true` = always visible, while the app role `axiaops` keeps its org-scoped policy untouched (the new policy is role-scoped to `axiaops_runtime`, which `axiaops` is **not** a member of). The table **owner** can create policies — **no superuser needed → RDS-safe.**

### Which tables get the policy?
Every **RLS-enabled** table, not every table. Non-RLS tables need nothing (a DML grant already sees all rows). The migration enumerates them dynamically so it can't drift from a hand-maintained list:

```sql
DO $$
DECLARE t text;
BEGIN
  FOR t IN
    SELECT c.relname FROM pg_class c
    JOIN pg_namespace n ON n.oid = c.relnamespace
    WHERE n.nspname = 'axiaops' AND c.relkind = 'r' AND c.relrowsecurity
  LOOP
    EXECUTE format('DROP POLICY IF EXISTS %I ON axiaops.%I', t || '_runtime_bypass', t);
    EXECUTE format('CREATE POLICY %I ON axiaops.%I TO axiaops_runtime USING (true) WITH CHECK (true)',
                   t || '_runtime_bypass', t);
  END LOOP;
END $$;
```

**Maintenance obligation:** a *future* migration that adds a new RLS table must also add its `_runtime_bypass` policy. To keep this from becoming a silent footgun (forgotten policy → runtime gets zero rows on that table), add a **CI invariant test** that fails if any `relrowsecurity` table in schema `axiaops` lacks a `_runtime_bypass` policy. Also add a line to the "Adding New Tables" checklist in `services/shared/CLAUDE.md`.

### Alternatives considered
- **`BYPASSRLS` attribute** — rejected: not grantable on RDS (above).
- **SECURITY DEFINER refactor of the ~30 `adminPool` callsites (architect Option B)** — strongest isolation (no bypass role at all) but a large surface that collides with H-1 and carries high regression risk. Out of scope for a "stop using the owner connection" cleanup; revisit with H-1.
- **Scope the policy to only the tables `adminPool` touches** — rejected: fragile (a new bypass call on a new table silently returns zero rows). The role's purpose is cross-org access, which the owner already has today; "all RLS tables minus DDL/ownership" is strictly less privilege than the status quo.

## Implementation outline

Wiring is identical to a `BYPASSRLS` role except the migration creates **policies** instead of setting the attribute.

### MR #1 — code + local/test + docs (self-contained; nothing touches a deployed env)
1. **Migration `029_runtime_admin_role.{up,down}.sql`** (latest is `028`; never edit released migrations — `docs/versioning.md`). Runs as `axiaops_owner`. `up`: idempotent `CREATE ROLE axiaops_runtime` (guarded, like `migrate.go:75`); `GRANT CONNECT`/`USAGE`/`SELECT,INSERT,UPDATE,DELETE ON ALL TABLES`/`USAGE,SELECT ON ALL SEQUENCES` + the two `ALTER DEFAULT PRIVILEGES … TO axiaops_runtime` (mirrors `000_init.up.sql:13-25`); the dynamic per-RLS-table policy loop above; `REVOKE INSERT,UPDATE,DELETE ON migration_history, migration_state` (bookkeeping stays owner-only, mirrors `025`). `down`: drop the policies + revoke + `DROP ROLE IF EXISTS`.
   - **`audit_log` note:** the broad grant gives `axiaops_runtime` `DELETE` on `audit_log`, needed by the GDPR org-cascade purge (run on the bypass pool). This is no new exposure — the app role already holds `DELETE` on `audit_log` via the `000_init` `ALTER DEFAULT PRIVILEGES` grant (`014`'s "no DELETE" comment was never actually enforced; tightening it is out of scope). Documented in the migration itself. The role still cannot DDL.
2. **Bootstrap** (`services/shared/storage/postgres/migrate.go:35`): extend `Bootstrap(ownerURL, appURL)` → add `runtimeAdminURL`; when non-empty, `CREATE ROLE axiaops_runtime LOGIN` + `ALTER ROLE … PASSWORD <pq.QuoteLiteral>` synced from the URL (mirrors the `axiaops` user logic at lines 75-88). Thread `RUNTIME_ADMIN_DATABASE_URL` through both migrate entrypoints (`services/shared/cmd/migrate/main.go`, `services/migrate/main.go`).
3. **Storage** (`postgres.go`): add `NewWithRuntimeAdmin(ctx, appURL, runtimeAdminURL)` (resolves `TODO(#107)`) with a readiness probe — a cheap query needing cross-org visibility with no org context — that fails fast if the role can't see across orgs. Reword the `adminPool` field comment. Keep `NewWithOwner` for the empty-fallback path tests rely on (`memberships_test.go:410-426`).
4. **Service main.go** (api `:72-96`, ingestion `:718-731`): read `RUNTIME_ADMIN_DATABASE_URL`, call `NewWithRuntimeAdmin`, update `die()` messages, repoint `ResetStuckScans` to the runtime URL. DEV_MODE keeps the single-pool collapse. The migrate binaries keep reading `MIGRATION_DATABASE_URL` — now the only legitimate owner-connection consumers.
5. **Local + test wiring** (sensible defaults / test paths only): `docker-compose.yml:51,108`, `test-infra/integration/docker-compose.yml:25,56` + `docker-compose.test.yml:42`, `scripts/start.sh` (DEV_MODE=false host launches), `scripts/migrate.sh`, `Makefile` test targets + the CI **test** variable block (`.gitlab-ci.yml:234`). Runtime services → `RUNTIME_ADMIN_DATABASE_URL`; migrate service keeps owner + gains the runtime URL so Bootstrap can sync the role password.
6. **Integration test** `runtime_admin_test.go`: bypass works (two orgs, no org context, runtime sees both); DDL denied (`CREATE/DROP/ALTER/TRUNCATE/CREATE ROLE` → `42501`); required DML succeeds (incl. `audit_log` DELETE); the readiness-probe constructor; **plus the per-RLS-table policy-coverage invariant**.
7. **Docs:** root + `services/{shared,api,ingestion}/CLAUDE.md` (three roles: `axiaops_owner` migrate-only, `axiaops_runtime` RLS-bypass-no-DDL, `axiaops` app), this file.

### Deployed-env wiring — self-hosted non-dev envs DONE (this MR); prod pending aws-infra
**Shipped in this MR** (verified on preview 2026-05-29 — role flipped to LOGIN, API healthy):
- `deploy/{preview,staging,demo,integration}.yml`: api+ingestion `MIGRATION_DATABASE_URL` → `RUNTIME_ADMIN_DATABASE_URL`. `deploy/dev.yml` (dev-1/dev-2, DEV_MODE=true) left on the single-pool fallback.
- `.gitlab-ci.yml` deploy template (`.deploy-dev` + staging + integration blocks): construct `RUNTIME_ADMIN_DATABASE_URL` from `${POSTGRES_RUNTIME_PASSWORD:-axiaops_runtime}` + `DB_HOST`, pass it to the migrate `docker run` (Bootstrap syncs the role LOGIN+password) and export it for compose-up. Operators should set a strong `POSTGRES_RUNTIME_PASSWORD` CI variable per env (it defaults to a weak literal so an unset var can't break the dev migrate).

**Still pending — production (ECS Express), blocked on aws-infra:** in the prod task defs replace the `MIGRATION_DATABASE_URL` secret entry in the api (`:1562`) and ingestion (`:1600`) task defs with `RUNTIME_ADMIN_DATABASE_URL`/`${SECRET_ARN_*_RUNTIME_ADMIN_DATABASE_URL}`, switch the `:?` guards (`:1346,:1348`). The TF-managed migrate task def keeps the owner URL. **Do this only after the aws-infra step below** — the next prod release must not ship this code before then, or prod api/ingestion `die()` at boot (the exact failure we hit + fixed on preview).

### Cross-repo (`axiaops/aws-infra`) + rollout — prod is LIVE
1. Terraform: a `random_password.runtime_admin` + two Secrets Manager secrets holding the `axiaops_runtime@rds` connection string; two SSM params publishing the secret ARNs; add the ARNs to the api/ingestion ECS execution-role `secretsmanager:GetSecretValue`; add `RUNTIME_ADMIN_DATABASE_URL` to the TF-managed **migrate** task def so prod Bootstrap can sync the role password.
2. **Order:** aws-infra apply (inert) → migration 029 via the migrate Fargate task (creates role + policies) → deploy updated api/ingestion task defs (MR #2) → verify a login + a scheduled scan → optionally drop the `*_migration_database_url` ARNs from the api/ingestion exec roles.
3. **Rollback:** one-line revert of the task-def secret entry back to `MIGRATION_DATABASE_URL` + redeploy; the owner secret + role are never removed on the hot path, so old containers come up exactly as before. Migration 029 can stay applied (an unused role + policies are harmless).

## Verification (MR #1)
`make test`; `make build-production` + `go test -tags production ./cmd/` per binary; `make test-storage` with `RUNTIME_ADMIN_DATABASE_URL` (runs `runtime_admin_test.go`); `make test-integration` (a login + scheduled-scan enumeration must still work via the runtime role); `make start-staging` with `DEV_MODE=false` (bootstrap → native login → `/v1/me` succeed with no owner connection at runtime).

## Risks
- **Forgotten policy on a future RLS table** → that table returns zero rows to the runtime. Mitigated by the CI invariant test + the `CLAUDE.md` checklist line.
- **Grant gap** on a table the broad grant misses → `42501`. Mitigated by `ALTER DEFAULT PRIVILEGES` (future tables) + the integration test enumerating the real surface.
- **H-1 sequencing:** land this first (pure privilege reduction; moves no callsite). H-1 later shrinks the bypass surface against a least-privilege baseline. No conflict.
