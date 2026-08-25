# Self-Signup Gap Map

Status: **gap map, not a design.** Feeds the build-sequence question under
ADR-0002 (SaaS-first): is open
self-registration the true top-of-funnel blocker, ahead of the mail-invite system?

> **Author's note (2026-06-04).** Written to answer "before we build mail invites,
> is self-*signup* the more fundamental gap?" Conclusion: **yes** — and the gap is
> deeper than a missing form. The current auth model is single-tenant by
> construction; open self-signup is the path that operationally turns AxiaOps
> multi-tenant. This maps what exists vs what's missing; it does not propose the
> design.

## The reframe

There is **no path today by which an unauthenticated stranger creates a brand-new
(user + organization) pair.** All three account-creation entry points require
out-of-band coordination, and — critically — **bootstrap seals after the first
org**, so the shipped model is *one organization per deployment*. RLS supports
many orgs in one DB (multi-membership users already exist, B1.5), but **no
creation path mints the Nth org for a self-service stranger.** Self-signup is
therefore not just a new endpoint — it is the feature that flips AxiaOps from
"one org per install" to "many orgs per install", i.e. where multi-tenancy
operationally begins.

## What exists today (all gated)

| Entry point | Endpoint | Creates user? | Creates org? | Gate |
|---|---|---|---|---|
| **Bootstrap** (first-owner install) | `POST /v1/auth/bootstrap` | yes | yes (exactly one) | install token (file/env) **+ refuses if `organizations` is non-empty**; single-use, sealed forever |
| **Invite redemption** | `POST /v1/auth/invitations/redeem` | yes (new-user flow) | **no** — joins a pre-existing org | a `pending_memberships` row an admin created |
| **SSO OIDC JIT** | `GET /v1/sso/oidc/callback` | yes (`UpsertUser`) | **no** — joins the org the verified domain is bound to | a pre-configured active connection + verified domain |

`docs/onboarding-wizard.md`: "onboarding" is *post*-account-creation setup
(invite teammates → connect AWS), tracked by `organizations.onboarding_completed_at`.
It is **not** an account-creation path.

**Structural blocker:** bootstrap's `ConsumeBootstrapState`
(`services/shared/storage/postgres/native_auth.go`) returns `ErrBootstrapAlreadyDone`
once any organization exists, and deletes the `bootstrap_state` singleton on first
success. That guard *is* the single-tenant assumption. Self-signup must introduce a
deliberately un-gated, repeatable org-creation path that coexists with it.

## What's reusable (most of the mechanics already exist)

The account-creation *machinery* is largely built — self-signup mirrors bootstrap
minus the token:

- **The atomic (org + user + owner membership + session) transaction** already
  exists in `ConsumeBootstrapState`. A `RegisterSelfService(...)` storage method is
  that transaction with the token-consume step removed and the "orgs must be empty"
  guard dropped. This is the load-bearing reuse.
- **Password hashing** — `auth.Hash` / `auth.CheckPolicy` (argon2id, 12-char min). As-is.
- **Email + name validation** — `model.ValidateInvitableEmail`, `validUserName`. As-is.
- **Session mint + cookie** — `auth.Manager.MintSession` + `auth.SetSession`. As-is (use `auth_mode='password'`).
- **Rate-limit infra** — `LoginRateLimiter` / `IPRateLimiter` (Redis-backed, in-mem fallback). Extend with a separate per-IP budget for register.
- **Email transport** — `notifications/email_smtp.go` exists, so the verification-email send is not from scratch (the *deliverability* setup is — see below).
- **Audit** — add one `AuditActionUserRegisteredSelf` constant; the write path exists.

## What's missing (net-new)

Split by whether it's on the critical path to a *validation beta* vs hardening:

### Critical path (a stranger can sign up and reach the dashboard)
1. **Public `POST /v1/auth/register`** — `{email, password, name, organization_name}` → the reused atomic transaction → session cookie. Must be on the `publicPath` allowlist (unauthenticated).
2. **`RegisterSelfService` storage method** — the bootstrap transaction generalised (no token, no empty-orgs guard).
3. **Dashboard `RegisterScreen.jsx`** — no signup UI exists today (only Login / Bootstrap / AcceptInvite). Plus the `/login` ↔ `/register` toggle and post-register redirect into the onboarding wizard.
4. **Register rate-limit** — a per-IP budget separate from login, so signup spam doesn't exhaust the login limiter.

### Abuse hardening (a *public* endpoint, not a token-gated one)
5. **Email verification** — the biggest net-new subsystem. **No `email_verified_at` / verification-token columns exist.** Needs: schema (verified-at + token hash + expiry), mint/redeem handlers, the verification email (reusing the SMTP transport), and a policy decision: hard-gate (no dashboard until verified) vs soft-gate (banner, limited access). Without it, anyone signs up as anyone's email.
6. **Bot / CAPTCHA protection** — none today. A public signup form is a spam/abuse magnet; needs reCAPTCHA/hCaptcha/Turnstile or equivalent.
7. **Email deliverability ops** — sending verification mail means a verified sending domain + SES out of sandbox + SPF/DKIM/bounce handling. The transport *code* exists; the *operational* surface does not. (Shared with the mail-invite system — do it once.)

### Decisions self-signup forces
8. **Org-name policy** — today only `org_code` is unique; `organizations.name` is not. Self-signup needs a rule: allow duplicate display names (differentiate by slug/UUID — simplest), or dedup/suggest-variant. Recommend: allow duplicates, never expose name as an identifier.
9. **One-org-per-email or many?** — does a self-signup always create a *new* org, or can an existing user "start another org"? (B1.5 multi-membership means the data model already allows a user in N orgs; the signup UX must decide.)

## Bottom line for the build sequence

- **Self-signup is the correct first build** under ADR-0002 — it is the actual entry to the funnel, and mail-invite is the *expansion* motion that only matters once people are inside.
- **The account-creation mechanics are ~mostly reuse** (the bootstrap transaction + the auth primitives). The genuine new work is **email verification + abuse protection + deliverability ops** — and #7 (deliverability) is shared with the mail-invite system, so building signup first means invites get the hard part for free.
- **Minimal validation-beta cut**, if you want to measure activation fast: `/register` + `RegisterScreen` + register rate-limit + **soft-gate** email verification (allow in, show "verify your email" banner, restrict nothing yet) + a CAPTCHA. Defer hard-gating and dedup niceties until activation proves out — consistent with ADR-0002's "validate before heavy plumbing".

## Key file references

- Bootstrap atomic txn (the reuse template): `services/shared/storage/postgres/native_auth.go` → `ConsumeBootstrapState`
- Bootstrap handler + single-use seal: `services/api/internal/auth/handler.go`
- Password hash/policy: `services/api/internal/auth/password.go`
- Rate-limit infra: `services/api/internal/auth/ratelimit.go`
- Public-path allowlist: `services/api/internal/middleware/auth.go` (`publicPath`)
- Dashboard auth screens (no Register yet): `services/dashboard/src/screens/{NativeLoginScreen,BootstrapScreen,AcceptInviteScreen}.jsx`
- Email transport (reused for verification mail): `services/shared/notifications/email_smtp.go`
- Org model: `services/shared/model/organization.go`
