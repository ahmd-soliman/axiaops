# Self-Signup Implementation Plan — Minimal Validation-Beta Cut

Status: **draft for refinement.** Implements the critical-path + soft-gate items
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
| **DS1** | **`SIGNUP_ENABLED` env flag, default `false`.** Self-hosted installs are unaffected (single-tenant bootstrap stays the only org-creation path). The SaaS build sets `SIGNUP_ENABLED=true`. The `/v1/auth/register` route is only registered, and the `RegisterScreen` only reachable, when the flag is on. | Resolves coexistence. Self-hosted = bootstrap-only (one org per install); SaaS = open signup (many orgs). A flag — not a build tag — because both SKUs build from the same `cmd/api-selfhosted` today; the SaaS composition root (`cmd/api-saashosted`, ADR-0002 #5) defaults the flag on. No second binary needed for the beta. |
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

**Cut-further option:** Slice 6's verification *email send* can stub to a no-op that just writes the token row, so the schema + soft-gate banner ship without SES being out of sandbox (deliverability is an ops prerequisite, not a code blocker). Slice 5 is the one piece that must not be cut for a public launch.

---

## Slice 1 — `RegisterSelfService` storage method (S)

**Goal.** Generalise the bootstrap atomic transaction into a repeatable, un-gated org-creation path — the load-bearing reuse from the gap doc.

**Files**
- `services/shared/storage/storage_native_auth.go` — interface method + input/output structs (interface-first).
- `services/shared/storage/postgres/native_auth.go` — impl.

**Net-new vs reused.** ~90% reused. `ConsumeBootstrapState` (native_auth.go ~lines 848–981) is the template. `RegisterSelfService` is that transaction **minus** the `bootstrap_state` lookup + `ConstantTimeCompare` token check, **minus** the `DELETE FROM bootstrap_state` seal, **minus** the advisory lock; and the org-empty guard is simply never invoked (it lives in `CreateBootstrapState`, not in the consume path). The 5-step body — INSERT organization → INSERT user (catch `23505` → `ErrUserEmailExists`) → `set_config('app.organization_id', …, true)` → INSERT owner membership → INSERT session — is lifted verbatim. `auth_mode='password'` (migration-021 CHECK already permits it).

**Additions**
- `RegisterSelfService(ctx, in RegisterSelfServiceInput) (RegisterSelfServiceResult, error)` on `NativeAuthStore`.
- `RegisterSelfServiceInput{ OrganizationID, OrganizationName, UserID, UserEmail, UserName, UserPasswordHash, SessionID, SessionTokenHash, SessionUserAgentHash, SessionIP string; SessionExpiresAt time.Time }`; `RegisterSelfServiceResult{ User model.User; Session model.Session }`.
- Reuse the existing `storage.ErrUserEmailExists` sentinel for the unique-index collision.

**RLS note.** Uses `s.adminPool` exactly as `ConsumeBootstrapState` (registration is pre-org-context — it *creates* the org). The membership INSERT sets `app.organization_id` via `set_config(..., true)` in-tx. `organizations`/`users`/`sessions` have no RLS; `memberships` does and is satisfied by the local GUC.

**Test plan** (`postgres_test.go`, integration — `make test-storage`):
- Happy path: user+session returned; org/user/membership/session rows present; role `owner`; `auth_mode='password'`.
- **Repeatable:** two calls, different emails → two distinct orgs (proves the single-tenant seal is gone — the multi-tenancy assertion).
- **Concurrent same-email:** two goroutines, same email → exactly one succeeds, the other `ErrUserEmailExists` (`users_email_lower_unique`, migration 021 line 36, `23505`). Verify the losing tx rolls back its org INSERT (no orphan org).
- **RLS bypass:** succeeds with no `app.organization_id` set.

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

### 6.1 Migration — `032_email_verification.up.sql` / `.down.sql`
Next number is **032** (highest is `031_notification_channels`). `users` has **no RLS** and a **global** `lower(email)` unique index (migration 021), so verification state lives on `users` and the token in a sibling capability table (no RLS, like `password_resets`/`sessions`).

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
```
- **Runtime-admin grant:** verify against `029_runtime_admin_role.up.sql` — if that role's grants are table-enumerated rather than schema-wide, add the equivalent grant to `axiaops_runtime` (the capability-table reads run on the admin pool, like `sessions`/`password_resets`).
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

### 6.4 Verification email — reuse the SMTP layer
The channel `EmailTransport.Send` (email_smtp.go ~line 126) is coupled to the notification-channel `Payload` + encrypted `config_ciphertext` — **not** directly reusable. The reusable seam is the lower-level **`dialingSendMail(addr, auth, from, to, msg)`** (email_smtp.go ~line 45). Add `services/shared/notifications/transactional.go` with `SendTransactionalEmail(ctx, smtpCfg, to, subject, body)` building an RFC-5322 message and calling `dialingSendMail`. SMTP config from **env** (`SMTP_HOST/PORT/USER/PASS/FROM`) — there is no per-org channel for system mail. **This same helper is what the deferred mail-invite system uses (do it once.)** If SES is still sandboxed, wire a no-op/log sender behind an interface so schema + redeem + banner ship and activation stays measurable.

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
| SIGNUP_ENABLED | No | `false` | Master switch for open self-registration. `false` → self-hosted single-tenant (bootstrap only); `/v1/auth/register` not registered (404). `true` → SaaS. `cmd/api-saashosted` defaults it on. |
| SIGNUP_RATE_LIMIT_PER_IP | No | `5` | Per-IP-per-minute cap on register. Separate `IPRateLimiter` budget (`"auth:register"`) from `/login`. |
| TURNSTILE_SECRET_KEY | When `SIGNUP_ENABLED=true` (prod) | — | Turnstile secret for `siteverify`. Unset → CAPTCHA skipped (dev/staging only; **must** be set for public launch). |
| EMAIL_VERIFICATION_TTL_HOURS | No | `48` | Verification-token lifetime. Generous (soft-gate). |
| SMTP_HOST / SMTP_PORT / SMTP_USER / SMTP_PASS / SMTP_FROM | When verification email is live | — | System (transactional) SMTP for verification + (deferred) invite mail. Distinct from per-org notification-channel SMTP. |

Dashboard build envs: `VITE_SIGNUP_ENABLED`, `VITE_TURNSTILE_SITE_KEY`.

---

## Coexistence: bootstrap (self-hosted) vs open signup (SaaS) — resolved

- **Mutually exclusive by default, co-existing on one binary.** Bootstrap is gated by the `bootstrap_state` singleton + org-empty guard (seals after first org). Signup is gated by `SIGNUP_ENABLED`. Same `RegisterSelfService`-shaped transaction, different entry points + guards.
- **Self-hosted (`SIGNUP_ENABLED=false`, default):** only bootstrap creates the one org; `/v1/auth/register` → 404. Single-tenant fully preserved. **No self-hosted install is affected by this work.**
- **SaaS (`SIGNUP_ENABLED=true`, set by `cmd/api-saashosted`):** open signup live; bootstrap is irrelevant and harmless (409s forever once an org exists).
- **Flag, not build tag:** the `production` build tag is the DEV_MODE-bypass seam — orthogonal to the SKU axis. SKU = composition root (`cmd/api-selfhosted` vs `cmd/api-saashosted`) + `SIGNUP_ENABLED`. A customer-shipping self-hosted binary (`-tags production`) must have signup **off**; the SaaS binary **on**.

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
- **SES-out-of-sandbox / SPF / DKIM / bounce handling** — an **ops prerequisite, not code**: the transactional-email code (6.4) exists, but a verified sending domain + SES production access must be provisioned before the verification email is enabled in prod. Shared with the deferred mail-invite system — do it once.
- **Self-service forgot-password** — admin-issued reset already ships (`/v1/auth/password-reset/redeem`); self-service is a separate later item.
- **Stripe/billing, free-tier shape, card-required trial** — ADR-0002 open follow-ups; not part of the activation-validation cut.

---

## Two grounding notes (verified in source)

1. `users` has **no RLS** and a **global** `lower(email)` unique index (`021_native_auth.up.sql` line 36; comment in `postgres/native_auth.go` ~lines 48–52) — which is why `RegisterSelfService` reuses the bootstrap admin-pool transaction wholesale and `23505 → ErrUserEmailExists` is the concurrent-same-email guarantee.
2. The channel `EmailTransport.Send` is coupled to the encrypted notification-channel config (`email_smtp.go` ~line 126), so the verification email reuses the lower-level `dialingSendMail` (~line 45), not `Send`.
