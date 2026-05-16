# Kinde Removal Plan

Single MR (`chore/remove-kinde-auth → develop`). Four commits, each leaving `make test` + `make build-production` green at the boundary. Slice 2 additionally requires `make test-storage`; Slice 3 additionally requires `glab ci lint`.

## Why now

Native auth merged to `develop` today via MR !85 (commit `29524bb`). The plan doc (`docs/sso-implementation-plan.md` lines 361, 366, 1208) describes a 30-day "rollback window" before kinde removal — that rollback path **does not exist**. Native and kinde do not share a user store; flipping `AUTH_PROVIDER=kinde` post-bootstrap leaves users locked out (no Kinde identity, no OIDC re-auth, sessions table mismatch). The strangler tier was a deploy-flapping mitigation (`both` mode for rolling restarts), NOT a recovery posture. So we remove now rather than waiting until D2 = 2026-10-30.

## Verification (run before slicing)

```bash
# A. Go-side surface — every file mentioning Kinde
grep -rn -i "kinde" services/ --include="*.go" -l | sort

# B. Static strings + literal AuthMode "kinde"
grep -rn '"kinde"\|AuthProviderTier\|KindeProvider\|CompositeProvider' services/ --include="*.go"

# C. SQL columns and constraint names
grep -rn "kinde_sub\|kinde_invitation_id\|kinde_user_id\|users_dev_kinde_sub_matches_id" services/shared/storage/

# D. Dashboard surface
grep -rn -i "kinde\|VITE_KINDE\|@kinde-oss" services/dashboard/

# E. Deploy + CI surface
grep -n "KINDE\|AUTH_PROVIDER" .gitlab-ci.yml docker-compose.yml deploy/*.yml

# F. Doc surface (sweep target — not a removal blocker)
grep -rn -i "kinde\|AUTH_PROVIDER" docs/ Tasks.md README.md CLAUDE.md services/*/CLAUDE.md

# G. Migration ordering — confirm next free number
ls services/shared/storage/postgres/migrations/ | sort

# H. CI pipeline lint sanity (run after each slice that touches it)
glab ci lint
```

Existing migrations end at **023** (`023_sso_force_reauth.up.sql`). Plan §4.5's prophesied "migration 023 for `pending_memberships` tightening" is now **migration 024**.

## Decisions (settle once, lock for all slices)

### `auth.Provider` seam — keep the interface, delete the kinde delegate

`auth.Provider` exists today only to host two impls (kinde, native). With kinde gone there is one impl forever (`auth.NativeProvider`). Keep the interface (cost: zero — one type, one method) so `WrapNative` doesn't bind to a concrete `*Manager`, preserving `serverbuild/build_test.go:stubProvider`.

Delete: `CompositeProvider`, `KindeProvider`, `buildAuthProvider` switch, `mustNewKindeAuth`, the entire `AUTH_PROVIDER` env-var read.

`AuthProviderTier` collapses: `password|sso|bootstrap → "native"`, anything else → `"unknown"`.

### JWKS package — stays put

`services/shared/jwks/` was lifted to serve both Kinde + OIDC. Post-removal only the per-connection (OIDC) caller remains. No move, no rename. The Kinde-shape test (`TestFromCache_CacheHit_SkipsHTTPFetch`) stays — it still proves the issuer-bound code path works for any future caller.

### Migration 024 — single migration does column drops AND rename

```sql
-- 024_drop_kinde_residue.up.sql
DELETE FROM pending_memberships WHERE invite_token_hash IS NULL;          -- legacy Kinde-mode rows
ALTER TABLE pending_memberships ALTER COLUMN invite_token_hash SET NOT NULL;
ALTER TABLE pending_memberships DROP COLUMN kinde_invitation_id;
ALTER TABLE pending_memberships DROP COLUMN kinde_user_id;
ALTER TABLE users DROP CONSTRAINT IF EXISTS users_dev_kinde_sub_matches_id;
ALTER TABLE users RENAME COLUMN kinde_sub TO external_id;
COMMENT ON COLUMN users.external_id IS 'Stable identifier from external IdP (SSO sub) or "native:<id>"/"dev:<id>" sentinels for native/dev users.';
```

Down: restore columns NULLABLE, rename back. RLS unaffected.

The `kinde_sub → external_id` rename matters because the column genuinely stores three things now (SSO sub, `native:<uuid>`, `dev:<uuid>`). Doing it in this MR avoids a second migration in 6 weeks.

If the rename feels too coupled, split Slice 2 into 2a (drop kinde columns; keep `kinde_sub` named) + 2b (rename to `external_id`) — bringing the count to 5. Recommend keeping 4.

## Slice 1 — Backend code: collapse providers, delete kinde package

**Files modified (non-exhaustive — grep before each touch):**

Backend:
- `services/api/cmd/main.go` — delete `mustNewKindeAuth`, `buildKindeClient`, `AUTH_PROVIDER` env read, `buildAuthProvider` switch (collapse to direct `auth.NewNativeProvider(mgr, membershipLookup(store))` call); drop `Inviter` from `Deps`; drop `kinde` import.
- `services/api/internal/serverbuild/build.go` — remove `Deps.Inviter`; drop `apiH.WithKinde` call; remove `kinde` import; remove `Config.{AuthProviderMode, NativeAuthActive, NativeInvitations}` (always derived now).
- `services/api/internal/serverbuild/build_test.go` — drop 5 `Inviter: kinde.NewStub()` lines + import.
- `services/api/internal/middleware/auth.go` — delete `Auth` struct, `NewAuth`, `newWithKeyfunc`, `Wrap`, `OrganizationCode`. Keep `publicPath`, `DevBypass`, context-key getters/setters. Grep `bearerToken` — if 0 callers, delete.
- `services/api/internal/middleware/auth_test.go` — delete entirely (~340 LoC of Kinde JWT cases).
- `services/api/internal/middleware/{kinde_provider,kinde_provider_test}.go` — delete.
- `services/api/internal/middleware/auth_native.go` — comment-edit (drop `kinde` from doc); `providerTier` collapses to native+unknown.
- `services/api/internal/auth/{composite_provider,composite_provider_test}.go` — delete.
- `services/api/internal/auth/provider.go` — keep interface; doc-edit `Identity.AuthMode` to drop `"kinde"`; drop `SessionID empty under Kinde` parenthetical.
- `services/api/internal/auth/tier.go` — drop `case "kinde"` branch.
- `services/api/internal/api/me_test.go:93`, `services/api/internal/middleware/auth_native_test.go:168` — flip `kinde-maps-to-kinde` → `kinde-maps-to-unknown` (or delete row).
- `services/api/internal/api/handler.go` — drop `kinde` import, `kinde kinde.Client` field, `nativeAuth` field (always true), `WithKinde`, `WithNativeInvitations`.
- `services/api/internal/api/invitations.go` — collapse `createInvitation` to native branch (delete kinde-mode branch); collapse `revokeInvitation` (drop Kinde RemoveUser).
- `services/api/internal/api/organizations.go` — `updateCurrentOrganization` becomes single rename + audit; drop `if h.kinde == nil { 503 }` guard; drop `kinde_synced` audit metadata.
- `services/api/internal/api/invitations_test.go` — delete `TestCreateInvitation_KindeFailure_502_AndCompensates`, `TestRevokeInvitation_HappyPath_CallsKinde`, `TestRevokeInvitation_KindeFails_502`. Keep + rename `TestRevokeInvitation_NativeAuth_NoKindeCall_204`.
- `services/api/internal/api/organizations_test.go` — delete 3 Kinde-coupled tests; add 1 local-only.
- `services/api/internal/api/test_helpers_test.go` — drop `MockStore.UpdateInvitationKindeIDs` (interface method removed in this slice — see below).
- `services/api/internal/kinde/` — delete the entire package (4 files).
- `services/shared/storage/storage.go` — remove `UpdateInvitationKindeIDs` from `Store` interface.
- `services/shared/storage/postgres/invitations.go` — drop `UpdateInvitationKindeIDs` impl.

Dashboard:
- `services/dashboard/src/{auth/kinde.js, pages/Callback.jsx, screens/LoginScreen.jsx}` — delete.
- `services/dashboard/src/auth/storage.js` — delete (verify no surviving caller via grep).
- `services/dashboard/src/App.jsx` — drop `getKindeClient` import + warmup + `/callback` route.
- `services/dashboard/src/pages/Login.jsx` — collapse to native-only (drop `getKindeClient`, `LoginScreen`, `handleKindeLogin`/`handleKindeSignUp`, the `AUTH_PROVIDER === 'kinde'` branch + import).
- `services/dashboard/src/components/AuthGuard.jsx` — drop legacy `getToken` check; cookie + `/v1/me` 200 is the sole signal.
- `services/dashboard/src/api/client.js` — drop `setAuthToken` + `Authorization: Bearer` injection (dead post-storage.js delete). Keep `UNAUTHORIZED_EVENT`.
- `services/dashboard/src/components/{OrgSwitcher.jsx, AppShell.jsx}` + `pages/settings/{Team.jsx, Organization.jsx}` — comment-only edits removing `AUTH_PROVIDER=kinde` parentheticals + `Organization.jsx:79` "Updates Kinde…" sentence.
- `services/dashboard/src/config.js` — drop `KINDE_ISSUER`, `KINDE_CLIENT_ID`, `AUTH_PROVIDER` exports + doc paragraphs.
- `services/dashboard/src/screens/NativeLoginScreen.jsx:6` — drop `AUTH_PROVIDER=native|both` qualifier.
- `services/dashboard/inject-env.sh:15` — remove `KINDE_ISSUER KINDE_CLIENT_ID AUTH_PROVIDER` from `keys` split.
- `services/dashboard/Dockerfile` — drop `ARG`/`ENV VITE_KINDE_*` lines.
- `services/dashboard/package.json` — drop `@kinde-oss/kinde-auth-pkce-js` dep. Run `npm install` to regenerate `package-lock.json`. Commit both.

**Boundary tests:** `cd services/api && go test ./...`, `make test-all`.

**What can go wrong:** the kinde-package delete + interface-method removal must land together — the postgres impl removal is required for the interface to compile clean. Done in one slice.

## Slice 2 — Storage + model: drop kinde columns, rename to `external_id`

**Files modified:**
- `services/shared/storage/postgres/migrations/024_drop_kinde_residue.{up,down}.sql` — new files per §Decisions above.
- `services/shared/model/invitation.go` — drop `KindeInvitationID`, `KindeUserID`. Update `InviteTokenHash` doc (drop "Empty for Kinde-mode rows").
- `services/shared/model/organization.go` — `User.KindeSub` → `User.ExternalID`. Update doc.
- `services/shared/storage/storage.go` — `UpsertUser(ctx, organizationID, kindeSub, email, name)` → `UpsertUser(ctx, organizationID, externalID, email, name)`.
- `services/shared/storage/postgres/{postgres.go, native_auth.go, postgres_test.go}` — bulk rename `kinde_sub` → `external_id` (SQL), `KindeSub` → `ExternalID` (Go), `kindeSub` → `externalID` (locals).
- `services/shared/storage/postgres/invitations.go` — drop `kinde_invitation_id`/`kinde_user_id` from SELECTs + `INSERT … RETURNING`.
- `services/shared/storage/postgres/memberships_migration_test.go:136` — fixture INSERT flips column.
- `services/api/internal/api/test_helpers_test.go` + `services/api/internal/sso/oidc_callback_test.go` + `services/api/internal/sso/oidc_callback.go:45,228` — `UpsertUser` mock + interface + call sites flip param name.

Migration `013_kinde_sub_dev_prefix.up.sql` stays as-is (migrations are append-only history).

**Boundary tests:** `make test`, `make test-storage`, `make test-integration`.

**What can go wrong:**
- Stale dev DBs on partial migration history. Recovery: `make stop` + clean pgdata. Mention in MR description.
- `pending_memberships` rows with `invite_token_hash IS NULL` will be DELETED. Pre-merge check: `SELECT count(*) FROM pending_memberships WHERE invite_token_hash IS NULL` against staging — confirm 0 (no production install ran kinde-mode invitations).

## Slice 3 — Deploy + CI surface

**Files modified:**
- `.gitlab-ci.yml`:
  - Lines 70–83: drop `AUTH_PROVIDER` doc block + global default. Replace with one-line "Auth provider is native; this used to be a strangler tier — see commit `<sha>` for removal."
  - Line 274: drop `--build-arg VITE_KINDE_ISSUER=… --build-arg VITE_KINDE_CLIENT_ID=…` from dashboard build.
  - Lines 301–348: delete `.dev-mode-gate` template + `gate:strangler:*` jobs entirely. Verify no `deploy:*` job's `needs:` references them. **Keep `gate:devmode:staging` and `gate:devmode:production`** — different gate (B1.7 layer 1, refuses `DEV_MODE=true` on customer envs).
  - Lines 804–807: drop the four `KINDE_*` propagation lines + line 809 `AUTH_PROVIDER=` from staging deploy.
- `docker-compose.yml`: drop `KINDE_*` env vars on `api` (lines 91–98), `VITE_KINDE_*` build args + `KINDE_*` env passthroughs on `dashboard` (lines 120–133), the explanatory comment block.
- `deploy/{preview,staging,demo}.yml`: drop every `KINDE_*` env on `api` + `dashboard` blocks; drop `AUTH_PROVIDER: ${AUTH_PROVIDER:-native}`; drop the strangler-explanation comment paragraphs. `staging.yml:23` (migrations block) — drop standalone `KINDE_ISSUER`.
- `deploy/dev.yml` — no edit (already kinde-free).

**Boundary tests:** `glab ci lint .gitlab-ci.yml`; `make start-staging` boots; `make test-integration` green.

**Operator action after merge:** delete project's CI/CD variables `KINDE_*` and `AUTH_PROVIDER` (every scope). They're no longer read but stale secrets are footguns.

## Slice 4 — Docs + Tasks.md sweep

Strategy: real edits where docs are operator-facing (`README.md`, `CLAUDE.md` files, `Tasks.md`, `docs/CI_CD_SECRETS_SETUP.md`). Banner-and-defer for everything else (`docs/auth*.md`, `docs/middleware.md`, `docs/invitation-flow.md`, design docs). Perfect doc cleanup is not the gating criterion.

**Real edits:**
- `CLAUDE.md` — line 31 (`DEV_MODE=false → Kinde JWT auth on` → native), line 49 (delete `AUTH_PROVIDER=kinde|both` requirement sentence), line 91 (`no real Kinde calls` → `no real network calls`), line 138 (delete Kinde OAuth bullet).
- `services/api/CLAUDE.md` — endpoints table: drop `AUTH_PROVIDER=kinde` branches from `POST /invitations` + `DELETE /invitations/{id}`. Auth Middleware section: rewrite native-only. Env Variables table: delete `AUTH_PROVIDER`, `KINDE_*` rows.
- `services/dashboard/CLAUDE.md` — grep first; expect a few env-var refs.
- `README.md` — line 28 (auth row in tech-stack table), line 61 (Kinde setup instructions → bootstrap link), line 372 (Phase 1 checkbox).
- `Tasks.md`:
  - Row 2.7.1 — flip ✅ with "Done in MR !85; kinde package removed in MR <this one>."
  - Row 2.7.12 — drop kinde-context paragraph.
  - Row 2.7.15 — drop "(a) AUTH_PROVIDER=kinde walkthrough" from to-do list.
  - Row 207 (secrets list) — drop KINDE entries.
  - Row 26, 46 — phase-1 / tech-stack entries to "native + SSO".
  - Phase 4 mobile bullet (Kinde PKCE on mobile) — strike, flag for separate reconsideration.
  - Other backlog rows that pre-date ADR-0001 — line-by-line triage; mark "n/a per ADR-0001" or strike. Don't try to make pretty in this MR.
- `docs/CI_CD_SECRETS_SETUP.md` — delete KINDE_* env var instructions (operators following this shouldn't be told to set vars that don't exist).
- `docs/sso-implementation-plan.md` — top banner "Kinde removal completed via MR <this one>; D2 moved up from 2026-10-30 to <today>." Mark D1/D2 as executed. Line 209 migration note: "Shipped as migration 024." Lines 238–272 strangler-tier paragraphs: top-of-section banner "historical context only".

**Banner-and-defer:**
- `docs/auth.md`, `docs/auth_flow.md`, `docs/middleware.md`, `docs/invitation-flow.md`, `docs/onboarding-and-app-owned-orgs.md`, `docs/onboarding-wizard.md`, `docs/production.md`, `docs/GITLAB_CI_IMPLEMENTATION_SUMMARY.md`, `docs/USER_STORIES_STATUS.md`, `docs/PHASE2_STATUS.md`, `docs/decisions/0001-deployment-model*.md`, `docs/sso-integration-design-saas.md` — top banner "Kinde removed in MR <this one>; references to `AUTH_PROVIDER=kinde|both` are past-tense."

**Boundary tests:** docs only — `make test` still green.

## Test surface delta

**Delete:**
- `services/api/internal/middleware/auth_test.go`
- `services/api/internal/middleware/kinde_provider_test.go`
- `services/api/internal/auth/composite_provider_test.go`
- `services/api/internal/kinde/client_test.go`

**Modify:**
- `services/api/internal/api/invitations_test.go` — delete 3, rename 1.
- `services/api/internal/api/organizations_test.go` — delete 3, add 1.
- `services/api/internal/api/me_test.go` + `middleware/auth_native_test.go` — flip `kinde→kinde` table entry.
- `services/api/internal/api/test_helpers_test.go` — drop `MockStore.UpdateInvitationKindeIDs`; flip `UpsertUser` param name.
- `services/api/internal/serverbuild/build_test.go` — drop `Inviter` from 5 Deps literals; drop kinde import.
- `services/api/internal/sso/oidc_callback_test.go` — flip `UpsertUser` mock signature.
- `services/shared/storage/postgres/{postgres_test.go, memberships_migration_test.go}` — `kinde_sub → external_id` in SQL fixtures.

**Stay:**
- `services/shared/jwks/jwks_test.go` — issuer-bound case still proves that code path.
- `services/api/internal/sso/*_test.go` — kinde-independent.
- `services/api/internal/auth/{handler,manager,session,…}_test.go` — native untouched.

## Risk register

| Risk | Mitigation |
|---|---|
| Stale GitLab CI Variable `KINDE_*` / `AUTH_PROVIDER=kinde` in project scope | Operator task: delete from CI/CD settings post-merge. |
| Dashboard runtime expecting `KINDE_ISSUER` via `inject-env.sh` | Slice 1 covers. Smoke: `make start-staging`, load `http://localhost:8082`, no console errors. |
| `/v1/version` returns `auth_provider: "kinde"` | `me_test.go` regression-pin + tier collapse. |
| User with live Kinde JWT lands on new build mid-rolling-restart | 401 → re-auth via native (bootstrap already complete). Document in MR description as expected UX. |
| `pending_memberships` row in flight at migration time | Pre-merge: `SELECT count(*)` on staging. Confirm 0. |
| Migration 024 fails partway (column-rename collision) | `make test-storage` runs full chain on fresh DB. Manual: `make start-staging` then `\d users` in psql, confirm `external_id` + UNIQUE index. |
| `package.json` removal of `@kinde-oss/kinde-auth-pkce-js` leaves orphans | Run `npm install` after edit; commit both files. |
| Doc someone followed last week says "set KINDE_ISSUER" | Slice 4 banner-and-defer. Operator-facing docs get real edits. |
| `bearerToken` helper outlives Kinde with no caller | Grep post-Slice-1; if 0, delete. |

## Estimated commit count

**4 commits:**
1. `refactor(auth): collapse strangler — delete kinde provider, package, and dashboard surface` (~50 files; ~2000 LoC removed, ~100 LoC churn)
2. `feat(storage): drop kinde_sub/kinde_invitation_id columns; rename to external_id (migration 024)` (~15 files; ~300 LoC churn)
3. `chore(ci): remove AUTH_PROVIDER env, KINDE_* vars, strangler-gate jobs from CI and deploys` (~5 files; ~80 LoC removed)
4. `docs(kinde): mark removal in plans/Tasks; banner historical docs` (~30 files; ~150 LoC churn, prose only)

Push as one MR with 4 commits — each commit has a single thesis, reviewer can stop after Slice 2 and trust the rest is non-functional.
