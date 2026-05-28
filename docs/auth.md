# Auth — AxiaOps

> **HISTORICAL — provider evaluation record.** This document records the original Phase-2
> decision to use Kinde as the auth provider. That decision was reversed by
> [ADR-0001](decisions/0001-deployment-model.md) and Kinde was removed in MR
> `chore/remove-kinde-auth` (2026-05-06).
>
> **Production auth today:** native cookie sessions (argon2id password hashing) +
> per-org OIDC SSO via the seam in `services/api/internal/sso/`. See
> [`docs/native-auth-bootstrap.md`](native-auth-bootstrap.md) for the first-run
> install flow and [`docs/decisions/0001-deployment-model.md`](decisions/0001-deployment-model.md)
> for the ADR that drove the change.

The original vendor comparison (Kinde vs Supabase Auth vs Clerk vs Cognito) and the Kinde
setup/migration notes have been removed. Read this doc for historical context on _why_
Kinde was initially chosen; for current auth behaviour read the sources listed above.

Authorization (role-based access control) is documented in `docs/rbac-design.md`.
