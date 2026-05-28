# Auth Flow — Dashboard & API

> **HISTORICAL.** This document described the legacy Kinde OAuth 2.0 + PKCE flow.
> Kinde was removed in MR `chore/remove-kinde-auth` (2026-05-06).

## Current flow (post-ADR-0001)

**Login:** dashboard `POST /v1/auth/login` (email + password) → API sets `axiaops_session` HttpOnly cookie → all subsequent requests send the cookie.

**SSO login:** browser visits `/v1/sso/oidc/{cid}/initiate` → redirected to IdP → IdP redirects back to `/v1/sso/oidc/callback` → API mints a native session with `auth_mode='sso'`.

**Middleware chain (outermost → innermost):**
```
request-logging + metrics → request-id → auth (DevBypass | WrapNative + EnforceSSO) → rate-limiter → CORS → mux
```

`WrapNative` (`services/api/internal/middleware/auth_native.go`) calls `auth.NativeProvider.Authenticate(r)`, which looks up the `axiaops_session` cookie in the `sessions` table. On success it attaches `OrganizationID`, `UserID`, `Email`, `Role`, and `AuthMode` to the request context.

**Dev mode:** `DEV_MODE=true` replaces `WrapNative` with `DevBypass`, which injects `DEV_ORGANIZATION_ID` / `DEV_USER_ID` / `DEV_USER_EMAIL` into every request context — no cookie required.

**Public paths** (bypass auth): `/health`, `/livez`, `/readyz`, `/metrics`, `/v1/sso/discover`, `/v1/auth/*`, `/v1/sso/oidc/*/initiate`, `/v1/sso/oidc/*/callback`.

## References

- [`docs/native-auth-bootstrap.md`](native-auth-bootstrap.md) — first-run install flow
- [`docs/decisions/0001-deployment-model.md`](decisions/0001-deployment-model.md) — ADR that drove the Kinde removal
- [`docs/middleware.md`](middleware.md) — full middleware chain description
- `services/api/internal/middleware/auth_native.go` — `WrapNative` implementation
- `services/api/internal/serverbuild/build.go` — composition root that wires the chain
