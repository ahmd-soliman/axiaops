# Self-Signup Implementation Plan — Minimal Validation-Beta Cut

Status: **draft for refinement** (refreshed 2026-06-12 against the codebase; none of Slices 1–6 implemented yet — `RegisterSelfService`, `/auth/register`, `RegisterScreen`, `email_verifications`, `SIGNUP_ENABLED`, Turnstile all absent). Implements the critical-path + soft-gate items
from [`self-signup-gap.md`](self-signup-gap.md) under
[ADR-0002 (SaaS-first)](decisions/0002-saas-first-for-awareness.md). Slice style
matches [`sso-implementation-plan.md`](sso-implementation-plan.md): numbered,
dependency-ordered, each independently shippable + testable with explicit
acceptance criteria.

> **Goal:** the minimal path by which an unauthenticated stranger creates a
> brand-new (user + organization) pair, reaches the dashboard, and lands in the
> onboarding wizard — with abuse protection (rate-limit + CAPTCHA + soft-gate
> email verification) — and nothing heavier. Guiding principle (ADR-0002):
> **validate self-serve activation BEFORE building heavy plumbing.**

---

## 0. Decisions taken (settle before coding)

| # | Decision | Rationale |
|---|---|---|
| **DS1** | **`SIGNUP_ENABLED` env flag, default `false`.** Self-hosted installs are unaffected (single-tenant bootstrap stays the only org-creation path). The SaaS build sets `SIGNUP_ENABLED=true`. The `/v1/auth/register` route is only registered, and the `RegisterScreen` only reachable, when the flag is on. | Resolves coexistence. Self-hosted = bootstrap-only (one org per install); SaaS = open signup (many orgs). A flag — not a build tag — because there is no separate SaaS binary: the build-tag model was inverted (Tasks.md 2.7.5a), so **SaaS is the default build** (`go build ./cmd/`) and self-hosted licensed is the `-tags selfhosted` opt-in. There is **no `cmd/api-saashosted` / `cmd/api-selfhosted` directory** — the SKU seam is the `cmd/saasmode_{saas,selfhosted}.go` build-tag pair (selected by the `selfhosted` tag), not a composition-root directory. The default (SaaS) build should default `SIGNUP_ENABLED=true`; any `-tags selfhosted` build keeps it `false`. No second binary needed for the beta. |
| **DS2** | **`devModeEnabled()` does NOT gate signup.** Signup is a production feature, wired only in the `!cfg.DevMode` branch (mirrors how `auth.Handler` is registered). `DEV_MODE` replaces the whole auth chain with `DevBypass`, so register is meaningless there. Test via `make start-staging` + `SIGNUP_ENABLED=true`. | Consistent with login/bootstrap being `!cfg.DevMode`-only. |
| **DS3** | **Soft-gate email verification only.** A registered user gets a session + full dashboard immediately; `users.email_verified_at` stays null until they click the link. Unverified banner shows; **nothing restricted**. | ADR-0002 "validate before heavy plumbing." Hard-gating risks killing the activation measurement before deliverability ops are solid. |
| **DS4** | **CAPTCHA = Cloudflare Turnstile.** | Free, no per-request cost, privacy-friendly (no Google reCAPTCHA cookie baggage — EU posture), trivial server-side `siteverify`. hCaptcha is the equivalent fallback. Server-side token verification mandatory. |
| **DS5** | **Org-name: allow duplicates.** `organizations.name` stays non-unique; the UUID is the identifier (as bootstrap already does). No dedup, no slug. | Gap doc §8. Never expose name as an identifier. |
| **DS6** | **Signup always creates a NEW org.** An existing email → 409 `email_taken` (global `users_email_lower_unique`). "Existing user starts another org" is deferred; existing users log in, cross-org join is the shipped invite flow. | Gap doc §9. The B1.5 multi-membership model already supports the deferred case. |

---

## 1. Slice ordering & critical path

```
Slice 1  RegisterSelfService storage method        (S)  ── critical path
Slice 2  POST /v1/auth/register handler + wiring    (M)  ── critical path
Slice 3  Register rate-limit (per-IP budget)        (S)  ── critical path
Slice 4  RegisterScreen.jsx + /login↔/register      (M)  ── critical path
         + post-register → onboarding wizard
─────────────────────────────────────────────── (minimum shippable funnel above)
Slice 5  Turnstile CAPTCHA on /register             (M)  ── abuse hardening
Slice 6  Soft-gate email verification               (L)  ── abuse hardening
         (migration + mint/redeem + verify email + banner)
```

**True critical path** (stranger signs up → dashboard): Slices **1–4**. Slices 5–6 are abuse hardening — a *public* endpoint must not ship to real strangers without at least Slice 5 (CAPTCHA) + Slice 3 (rate-limit), but the four-slice core is demoable behind `SIGNUP_ENABLED` for internal activation dry-runs.

**Cut-further option:** Slice 6's verification *email send* can stub to a no-op that just writes the token row, so the schema + soft-gate banner ship without the production mail relay being fully provisioned (deliverability is an ops prerequisite, not a code blocker). Slice 5 is the one piece that must not be cut for a public launch.

---

## Slice 1 — `RegisterSelfService` storage method (S)

**Goal.** Generalise the bootstrap atomic transaction into a repeatable, un-gated org-creation path — the load-bearing reuse from the gap doc.

**Files**
- `services/shared/storage/storage_native_auth.go` — interface method + input/output structs (interface-first).
- `services/shared/storage/postgres/native_auth.go` — impl.

**Net-new vs reused.** ~90% reused. `ConsumeBootstrapState` (`native_auth.go`, function `ConsumeBootstrapState`) is the template. `RegisterSelfService` is that transaction **minus** the `bootstrap_state` lookup + `ConstantTimeCompare` token check, **minus** the `DELETE FROM bootstrap_state` seal, **minus** the advisory lock; and the org-empty guard is simply never invoked (it lives in `CreateBootstrapState`, not in the consume path). The 5-step body — INSERT organization → INSERT user (catch `23505` → `ErrUserEmailExists`) → `set_config('app.organization_id', …, true)` → INSERT owner membership → INSERT session — is lifted verbatim, **plus a 6th step: write the org's default `entitlements` row inside the same transaction** — `ConsumeBootstrapState` does this with an inline INSERT in its tx (the `ensureDefaultEntitlement` helper in `postgres.go` is the out-of-tx equivalent used by `UpsertOrganization`/`EnsureOrganization`); copy the inline-INSERT form so the row is atomic with the org. Without it the new org has no `entitlements` row and the default-build fail-closed scan gate (`entitlement.IsScanAllowedForOrg` — missing row denies) blocks every self-signup org's scans. This is the single forward seam the companion billing plan ([`billing-plan.md`](billing-plan.md)) will repoint from the current `active`/`internal` default to a trial — name it explicitly so billing changes one function, not every chokepoint. `auth_mode='password'` (migration-021 line 60 CHECK permits `'password'`, `'sso'`, `'bootstrap'`).

**Additions**
- `RegisterSelfService(ctx, in RegisterSelfServiceInput) (RegisterSelfServiceResult, error)` on `NativeAuthStore`.
- `RegisterSelfServiceInput{ OrganizationID, OrganizationName, UserID, UserEmail, UserName, UserPasswordHash, SessionID, SessionTokenHash, SessionUserAgentHash, SessionIP string; SessionExpiresAt time.Time }`; `RegisterSelfServiceResult{ User model.User; Session model.Session }`.
- Reuse the existing `storage.ErrUserEmailExists` sentinel for the unique-index collision.
- Inside `RegisterSelfService`, after the session INSERT and within the same transaction, the default-entitlement INSERT (`ON CONFLICT (organization_id) DO NOTHING`) — copied from `ConsumeBootstrapState`'s inline form, not via the out-of-tx `ensureDefaultEntitlement` helper (`postgres.go`). In-tx makes it **fatal by construction**: a failed entitlement write rolls back the whole registration, so a missing row can't silently produce an org that can never scan.

**RLS note.** Uses `s.adminPool` exactly as `ConsumeBootstrapState` (registration is pre-org-context — it *creates* the org). The membership INSERT sets `app.organization_id` via `set_config(..., true)` in-tx. `organizations`/`users`/`sessions` have no RLS; `memberships` does and is satisfied by the local GUC.

**Test plan** (`postgres_test.go`, integration — `make test-storage`):
- Happy path: user+session returned; org/user/membership/session rows present; role `owner`; `auth_mode='password'`.
- **Repeatable:** two calls, different emails → two distinct orgs (proves the single-tenant seal is gone — the multi-tenancy assertion).
- **Concurrent same-email:** two goroutines, same email → exactly one succeeds, the other `ErrUserEmailExists` (`users_email_lower_unique`, migration 021 line 36, `23505`). Verify the losing tx rolls back its org INSERT (no orphan org).
- **RLS bypass:** succeeds with no `app.organization_id` set.
- **Entitlement row present:** after a happy-path register, the org has an `entitlements` row (plan `internal`, status `active`) so the fail-closed scan gate passes. Regression-pins the chokepoint.

**Effort: S** — copy-and-strip of a tested transaction.

---

## Slice 2 — `POST /v1/auth/register` handler + wiring (M)

**Goal.** Public endpoint `{email, password, name, organization_name}` → `RegisterSelfService` → session cookie → 200. Gated by `SIGNUP_ENABLED`.

**Files**
- `services/api/internal/auth/handler.go` — `register` method + `Register(mux)` route; `WithRegisterRateLimit`/`WithCaptcha`/signup-enabled setters.
- `services/api/internal/serverbuild/build.go` — wire the route inside the `if !cfg.DevMode {` block, only when `cfg.SignupEnabled`; add `SignupEnabled bool` to `Config`.
- `services/api/cmd/main.go` — read `SIGNUP_ENABLED` into `cfg.SignupEnabled`.
- `services/shared/model/audit.go` — `AuditActionUserRegisteredSelf = "user_registered_self"`.
- `services/api/internal/middleware/auth.go` — **no change**: `publicPath()` already covers any `/v1/auth/` prefix (line 60). Note for reviewers so no redundant case is added.

**Handler shape** (follows the `bootstrap` handler minus the token):
- Decode body; validate `model.ValidateInvitableEmail`, `validUserName`, `CheckPolicy` (≥12). Empty `organization_name` → 400 (an org name is meaningful here, unlike bootstrap's install default).
- `Hash(password)`; mint session token; build `RegisterSelfServiceInput` with pre-generated UUIDs.
- Call `h.store.RegisterSelfService`.
- Errors: `ErrUserEmailExists` → **409 `email_taken`** (DS6 — must tell the user to log in; do NOT collapse to a generic error); other → 500.
- Success: pre-warm session cache, write audit `AuditActionUserRegisteredSelf`, `SetSession`, 200 `{user{…,role:"owner"}, organization{…}}`.
- Leave a `// TODO slice 6` seam after the audit write for the verification-email mint.

**Seams filled by later slices** (nil-tolerant hooks, before any DB work): `if h.registerLimit != nil {…}` (Slice 3), `if h.captcha != nil {…}` (Slice 5).

**Endpoint-table addition** (`services/api/CLAUDE.md`):
| Method | Path | Auth | Purpose |
|---|---|---|---|
| POST | /auth/register | No (rate-limited + CAPTCHA, `SIGNUP_ENABLED` only) | Open self-registration. `{email, password, name, organization_name}` → org + owner user + session. 409 `email_taken` if email globally registered. 404 when `SIGNUP_ENABLED` unset (self-hosted single-tenant). |

**Test plan** (`handler_test.go`, black-box, `httptest`, mock `NativeAuthStore`):
- Happy path: 200, `Set-Cookie: axiaops_session`, role `owner`, audit writer invoked.
- Validation: weak password → 400 `weak_password`; bad email → 400; bad/empty name → 400; empty org name → 400.
- Store `ErrUserEmailExists` → 409 `email_taken`; generic error → 500.
- `signupEnabled=false` → route absent / 404 (extend `serverbuild/build_test.go` `TestComposeServer_NativeAuthMode`).

**Effort: M.**

---

## Slice 3 — Register rate-limit (per-IP, separate from login) (S)

**Goal.** A per-IP signup cap that does not share the login budget.

**Files**
- `services/api/internal/auth/ratelimit.go` — **no new type**: reuse `IPRateLimiter` (already takes a `keyPrefix`); new prefix `"auth:register"`.
- `services/api/internal/serverbuild/build.go` — `auth.NewIPRateLimiter(deps.Cache, "auth:register", cfg.SignupRateLimitPerIP)` via `WithRegisterRateLimit`, inside `!cfg.DevMode && cfg.SignupEnabled`.
- `services/api/internal/auth/handler.go` — `registerLimit *IPRateLimiter` field + setter (mirror `WithBootstrapProbeRateLimit`).

**Why per-IP only.** Signup has no existing account to stuff; the vector is *volume of new accounts from one source*. Tighter than login's default — propose `SIGNUP_RATE_LIMIT_PER_IP=5/min`. Fails open on cache error (CAPTCHA is the durable bot wall).

**Test plan** (`ratelimit_test.go` + handler): `cap=2` → 3rd call blocked, `Reason:"ip"`, `RetryAfter` set; handler returns 429 `rate_limited` + `Retry-After` **before** any store call (assert `RegisterSelfService` not invoked); nil limiter → no limiting.

**Effort: S.**

---

## Slice 4 — `RegisterScreen.jsx` + `/login`↔`/register` + post-register onboarding (M)

**Goal.** Signup form, login/register toggle, post-register redirect into the existing onboarding wizard.

**Files**
- `services/dashboard/src/screens/RegisterScreen.jsx` — **new**. Email, name, password, organization_name. POSTs `/v1/auth/register`. Modelled on `NativeLoginScreen.jsx` + `BootstrapScreen.jsx`.
- `services/dashboard/src/screens/NativeLoginScreen.jsx` — "Create an account" link → `/register`, behind `VITE_SIGNUP_ENABLED`.
- `services/dashboard/src/api/auth.js` — `register({email, password, name, organizationName})`.
- `services/dashboard/src/App.jsx` — `<Route path="/register" …>` in the public routes block; add `/register` to the BootstrapGate exclusion list so the fresh-install auto-redirect doesn't hijack it.

**Post-register flow.** On 200 the API has set the cookie + returned the new org. Navigate to `/onboarding`; the new org has `onboarding_completed_at = NULL` (RegisterSelfService inserts null, as bootstrap does), so `OnboardingGate` routes into the wizard exactly as a freshly-bootstrapped owner. **No new onboarding code.**

**Frontend flag.** Beta: always register the route (API 404s when disabled); hide the login-screen link behind build-time `VITE_SIGNUP_ENABLED`. Self-hosted builds ship it off → no link, direct `/register` 404s at the API. No new backend probe endpoint.

**Test plan** (Vitest + RTL, `*.test.jsx`): renders four fields; submit posts the right body; 200 → navigate `/onboarding`; 409 → "email already registered — log in"; 429 → "too many attempts"; toggle visibility per `VITE_SIGNUP_ENABLED`. Mock fetch; no real network.

**Effort: M.**

---

## Slice 5 — Turnstile CAPTCHA on `/register` (M)

**Goal.** Bot protection: client renders the widget; server verifies the token against `siteverify` before account creation.

**Files**
- `services/api/internal/auth/captcha.go` — **new**. `CaptchaVerifier` interface + `TurnstileVerifier`: `Verify(ctx, token, remoteIP) (bool, error)` → POST `https://challenges.cloudflare.com/turnstile/v0/siteverify`. Interface so tests inject a fake (no real network).
- `services/api/internal/auth/handler.go` — fill the CAPTCHA seam: read `cf-turnstile-response` from the body, `h.captcha.Verify(...)` before DB write; failure → 400 `captcha_failed`; nil verifier → skip.
- `services/api/internal/serverbuild/build.go` — construct from `TURNSTILE_SECRET_KEY` via `WithCaptcha` when set + `cfg.SignupEnabled`.
- `services/dashboard/src/screens/RegisterScreen.jsx` — Turnstile widget (`VITE_TURNSTILE_SITE_KEY`), token in the POST body.

**Posture.** Bounded `context.WithTimeout` (2–3s) and **fail closed** (CAPTCHA is the bot wall; a `siteverify` outage should block signups, not wave bots through — document the availability trade). Handler ordering: rate-limit (local) → CAPTCHA (external) → validation → DB.

**Env:** `TURNSTILE_SECRET_KEY` (server), `VITE_TURNSTILE_SITE_KEY` (build).

**Test plan:** `captcha_test.go` against an `httptest.Server` standing in for `siteverify` (success/false/malformed→error); handler with fake verifier false → 400 `captcha_failed`, store not called; nil → skipped; Vitest asserts the token in the submit body.

**Effort: M.**

---

## Slice 6 — Soft-gate email verification (L)

**Goal.** Schema for verification state + token; mint-on-register + redeem + verification email (reusing the SMTP layer); unverified banner. **Soft-gate: user is let in; nothing restricted.**

### 6.1 Migration — `035_email_verification.up.sql` / `.down.sql`
Next free number is **035** (highest applied is `034_entitlement_internal_plan`; `032` is `staff_identity`, `033` is `entitlements`). Confirm the next free number at implementation time. `users` has **no RLS** and a **global** `lower(email)` unique index (migration 021), so verification state lives on `users` and the token in a sibling capability table (no RLS, like `password_resets`/`sessions`).

```sql
SET search_path TO axiaops;

ALTER TABLE users
    ADD COLUMN email_verified_at TIMESTAMPTZ;   -- NULL = unverified (soft-gate)

CREATE TABLE IF NOT EXISTS email_verifications (
    id               TEXT        PRIMARY KEY,
    user_id          TEXT        NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    organization_id  TEXT        NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    token_hash       TEXT        NOT NULL UNIQUE,
    expires_at       TIMESTAMPTZ NOT NULL,
    redeemed_at      TIMESTAMPTZ,
    created_at       TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS email_verifications_user_idx    ON email_verifications (user_id);
CREATE INDEX IF NOT EXISTS email_verifications_expires_idx ON email_verifications (expires_at) WHERE redeemed_at IS NULL;

GRANT SELECT, INSERT, UPDATE, DELETE ON email_verifications TO axiaops;
GRANT SELECT, INSERT, UPDATE ON email_verifications TO axiaops_runtime;
```
- **Runtime-admin grant (resolved):** the redeem lookup is pre-auth (public `/v1/auth/` endpoint, no org context) so it runs on the admin pool like `sessions`/`password_resets` — hence the explicit `axiaops_runtime` grant above. The `029_runtime_admin_role` bypass-policy loop only covers RLS-enabled tables and skips this one, so the grant must be in this migration.
- **Down:** `DROP TABLE email_verifications;` then `ALTER TABLE users DROP COLUMN email_verified_at;`.
- **RLS-aware test:** redeem lookup succeeds with no `app.organization_id` set; a regression that adds RLS must fail it.

### 6.2 Storage methods
`storage_native_auth.go` (interface) + `postgres/native_auth.go` (impl):
- `CreateEmailVerification(ctx, id, userID, organizationID, tokenHash, expiresAt)` — mirrors `CreatePasswordReset`.
- `RedeemEmailVerification(ctx, tokenHash) (userID, organizationID, error)` — atomic `SELECT … FOR UPDATE`, check not-redeemed + not-expired, `UPDATE users SET email_verified_at = NOW()`, `UPDATE email_verifications SET redeemed_at = NOW()`. Mirrors `RedeemPasswordReset` minus the password write. New sentinels collapse to 410.
- `model.User` gains `EmailVerifiedAt *time.Time` — **propagate the new column to every `SELECT … FROM users` that scans into `model.User`** (`CreateUserWithPassword`, `LookupUserByEmail`, `LookupInvitationByToken`, `RedeemNativeInvitation`, `ConsumeBootstrapState`) or the scans break. Highest-churn risk in this slice; enumerate them.

### 6.3 Mint + redeem
- **Mint** (fill the Slice-2 seam): after register succeeds, mint token, `CreateEmailVerification(TTL=EMAIL_VERIFICATION_TTL_HOURS)`, send email (6.4). **Non-fatal** — a send failure must NOT fail registration (soft-gate). Log + counter.
- **Redeem:** `POST /v1/auth/verify-email/redeem` (public, `/v1/auth/` prefix). `{token}` → 204; 410 `verification_invalid` on unknown/expired. Audit `AuditActionUserEmailVerified`.
- **Resend (recommend include):** `POST /v1/auth/verify-email/resend` — session-authed, rate-limited via the register budget. Mints + re-sends.

### 6.4 Verification email — reuse the shipped invite-mail seam
System (transactional) email has **already shipped** for invitations: `services/shared/notifications/invite_email.go` defines the `InviteSender` interface and `EmailTransport.SendInvite(ctx, cfg model.EmailConfig, recipient, InviteEmail)`, which builds an RFC-5322 message and sends via the transport's injectable `sendMail` seam (default `dialingSendMail`, `email_smtp.go`). The api owns config resolution through its `InviteMailer` seam (`services/api/internal/api/invite_mailer.go`), which sources a plaintext `model.EmailConfig` from either the org's email notification channel (`notifications.DecodeEmailConfig`) or the global `SMTP_*` env config. **Do NOT add a new `transactional.go` helper** — follow the same pattern: add a `SendVerification`-style method (or a small generic `SendTransactional(ctx, cfg model.EmailConfig, to, subject, body)`) alongside `SendInvite`, taking an already-resolved `model.EmailConfig`, and resolve that config in the api the way `InviteMailer` already does (per-org channel → global `SMTP_*`). The deliverability deferral still holds: if the prod relay isn't ready, wire a no-op/log sender behind the `InviteSender`-style interface so schema + redeem + banner ship and activation stays measurable.

### 6.5 Unverified banner (frontend)
- `/v1/me` returns `email_verified: bool`.
- `services/dashboard/src/components/UnverifiedBanner.jsx` — **new**, in `AppShell` when false: "Verify your email — [Resend]". Non-blocking.
- `services/dashboard/src/screens/VerifyEmailScreen.jsx` (or public `/verify-email?token=…`) POSTs redeem, success → navigate `/`.

**Test plan:** storage create→redeem flips `email_verified_at`; double-redeem / expired → sentinels; RLS-bypass lookup works. Handler (mock store + **fake email sender**): register mints a row + is non-fatal on send error; redeem 204/410. Email helper against an injected fake `dialingSendMail` (as `EmailTransport` already injects via the `sendMail` field) — assert headers + recipient; no real SMTP. Vitest: banner iff `email_verified=false`; resend hits the endpoint; verify route redeems + redirects.

**Effort: L** (migration + 2 storage methods + 2 endpoints + transactional-email seam + banner + `model.User` field propagation).

---

## Env vars (api — `services/api/CLAUDE.md` style)

| Variable | Required | Default | Notes |
|----------|----------|---------|-------|
| SIGNUP_ENABLED | No | `false` | Master switch for open self-registration. `false` → self-hosted single-tenant (bootstrap only); `/v1/auth/register` not registered (404). `true` → SaaS. The default (non-`selfhosted`) build should default it on; the `-tags selfhosted` build leaves it off. |
| SIGNUP_RATE_LIMIT_PER_IP | No | `5` | Per-IP-per-minute cap on register. Separate `IPRateLimiter` budget (`"auth:register"`) from `/login`. |
| TURNSTILE_SECRET_KEY | When `SIGNUP_ENABLED=true` (prod) | — | Turnstile secret for `siteverify`. Unset → CAPTCHA skipped (dev/staging only; **must** be set for public launch). |
| EMAIL_VERIFICATION_TTL_HOURS | No | `48` | Verification-token lifetime. Generous (soft-gate). |
| SMTP_HOST / SMTP_PORT / SMTP_USER / SMTP_PASS / SMTP_FROM / SMTP_FROM_NAME | Already documented api env vars (see `services/api/CLAUDE.md`) | — | Global transactional SMTP relay (Gmail relay `smtp-relay.gmail.com` in prod). **Already used by the shipped invite mailer** — verification email reuses the same vars and `InviteMailer` resolution, no new env. |

Dashboard build envs: `VITE_SIGNUP_ENABLED`, `VITE_TURNSTILE_SITE_KEY`.

---

## Coexistence: bootstrap (self-hosted) vs open signup (SaaS) — resolved

- **Mutually exclusive by default, co-existing on one binary.** Bootstrap is gated by the `bootstrap_state` singleton + org-empty guard (seals after first org). Signup is gated by `SIGNUP_ENABLED`. Same `RegisterSelfService`-shaped transaction, different entry points + guards.
- **Self-hosted (`SIGNUP_ENABLED=false`, default):** only bootstrap creates the one org; `/v1/auth/register` → 404. Single-tenant fully preserved. **No self-hosted install is affected by this work.**
- **SaaS (`SIGNUP_ENABLED=true`, the default build's default):** open signup live; bootstrap is irrelevant and harmless (409s forever once an org exists).
- **Flag, not build tag:** the `production` build tag is the DEV_MODE-bypass seam; the `selfhosted` tag is the SKU seam (default = SaaS, `-tags selfhosted` = licensed self-hosted). Both are orthogonal to `SIGNUP_ENABLED`, which is the runtime switch. A `-tags selfhosted` build must default signup **off**; the default SaaS build **on**. There is no `cmd/api-saashosted`/`cmd/api-selfhosted` — the seam is `cmd/saasmode_{saas,selfhosted}.go`.

---

## Risks & edge cases

- **Concurrent same-email.** `users_email_lower_unique` raises `23505` → `ErrUserEmailExists` → 409. The org INSERT precedes the user INSERT in the tx, so a loser's org rolls back with the failed tx — no orphan org (`defer tx.Rollback`). Tested in Slice 1.
- **Email-enumeration via 409.** `email_taken` reveals registration — a mild enumeration channel, acceptable + standard for self-serve UX (users must be told to log in). Rate-limit + CAPTCHA cap abuse. Do not collapse to a generic error (breaks the funnel). Defer hardening.
- **Soft-gate: signing up as someone else's email.** Mitigated by CAPTCHA + rate-limit + the imposter never receiving the verification mail (it goes to the real owner; "if you didn't sign up, ignore this"). The imposter holds the session, not the owner — annoyance, not compromise. Hard-gate closes it fully; deferred.
- **Verification email non-fatal on send failure** — registration succeeds even if SMTP is down (soft-gate); leaves an unverified user + banner + resend.
- **`model.User.EmailVerifiedAt` propagation** — the highest-churn risk; enumerate every users-SELECT scan.
- **CAPTCHA availability trade** — fail-closed means a `siteverify` outage pauses new signups. Acceptable for a beta; flag in the runbook.
- **Audit:** `AuditActionUserRegisteredSelf` (Slice 2), `AuditActionUserEmailVerified` (Slice 6), via the existing `AuditWriter` seam.

---

## Effort & cut-line summary

| Slice | Effort | Critical path? | Cuttable for minimal funnel? |
|---|---|---|---|
| 1 — `RegisterSelfService` storage | S | Yes | No (load-bearing) |
| 2 — `/v1/auth/register` handler + wiring | M | Yes | No |
| 3 — Register rate-limit | S | Yes (public) | Reuses `IPRateLimiter` — keep |
| 4 — `RegisterScreen` + onboarding redirect | M | Yes | No |
| 5 — Turnstile CAPTCHA | M | Hardening | **Do not cut for public launch**; can defer for internal dry-run |
| 6 — Soft-gate email verification | L | Hardening | Schema + banner keep; **email *send* can stub** until SES is out of sandbox |

**Minimum to measure activation internally:** Slices 1–4. **Minimum responsible public launch:** 1–5 + Slice 6 schema/banner (send stubbed if SES isn't ready).

---

## Explicitly deferred (out of scope for the beta — one-line why)

- **Hard-gating on unverified email** — would block dashboard access before deliverability is proven; soft-gate first (ADR-0002).
- **Org-name dedup / suggest-variant** — UUID is the identifier; duplicate display names are harmless (gap §8).
- **"Existing user starts another org" UX** — B1.5 model allows it, but the UX is non-minimal; existing users log in + use invites (DS6).
- **Sending-domain deliverability (SPF / DKIM / DMARC / bounce handling)** — an **ops prerequisite, not code**: the transactional-email seam already ships (invite mail), but a verified sending domain on the global relay (Gmail SMTP relay in prod) must be provisioned before the verification email is enabled in prod. The mail-invite system is no longer deferred — it shipped — so this is purely a domain/deliverability provisioning item.
- **Self-service forgot-password** — admin-issued reset already ships (`/v1/auth/password-reset/redeem`); self-service is a separate later item.
- **Stripe/billing, free-tier shape, card-required trial** — now planned in [`billing-plan.md`](billing-plan.md) (DS3 there: reverse trial, card-not-required; its Slice 6 repoints this plan's `ensureDefaultEntitlement` seam to `trialing`); not part of the activation-validation cut.

---

## Two grounding notes (verified in source)

1. `users` has **no RLS** and a **global** `lower(email)` unique index (`021_native_auth.up.sql` line 36, `CREATE UNIQUE INDEX users_email_lower_unique`) — which is why `RegisterSelfService` reuses the bootstrap admin-pool transaction wholesale and `23505 → ErrUserEmailExists` is the concurrent-same-email guarantee.
2. System mail already has a shipped seam: `EmailTransport.SendInvite` + the api's `InviteMailer` (`invite_mailer.go`) resolve a plaintext `model.EmailConfig` (per-org channel via `notifications.DecodeEmailConfig`, else global `SMTP_*` env) and send via the transport's injectable `sendMail` field — `EmailTransport.Send` is the channel/`config_ciphertext`-coupled path the verification mail must NOT reuse. Add a verification-mail method beside `SendInvite`, don't introduce a parallel `transactional.go`.
