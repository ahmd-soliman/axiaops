---
title: Authentication & Roles
description: Bootstrapping AxiaOps, understanding roles, and setting up SSO.
---

AxiaOps uses **native auth only** — no third-party identity vendor required. Three
ways in: the one-time bootstrap that creates your first user, email/password login,
and optional SSO via OIDC.

## First-run bootstrap

Bootstrap is single-use per install — once an organization exists, it's sealed
forever (returns `409` on any later attempt).

1. Start AxiaOps. The server writes a one-time install token to
   `/var/run/axiaops/initial_setup_token` inside the `api` container (mode `0600`,
   deleted on first successful bootstrap).
2. Retrieve it:
   ```bash
   docker exec axiaops-api cat /var/run/axiaops/initial_setup_token
   ```
   The token is exactly 64 hex characters — ignore any trailing `%`/`$` your shell appends.
3. Open the dashboard — a fresh install auto-redirects to `/bootstrap`. Enter the
   token, your organization name, your name/email, and a password (12+ characters).

On success: your organization, your user (as `owner`), and a session are all
created in one step.

:::note[Running on ECS Express / Fargate?]
The container filesystem isn't reachable there. Set `BOOTSTRAP_PRINT_BANNER=true`
and `BOOTSTRAP_TOKEN_FILE_PATH=""`, then read the token from CloudWatch logs
instead. You get exactly one capture at first boot.
:::

## Roles

Four roles, strictly hierarchical — each inherits everything the one below it can do:

```
owner > admin > member > viewer
```

| Role | Can do |
|---|---|
| `owner` | Everything, plus transfer ownership and delete the organization. Exactly one at a time. |
| `admin` | Full operational control — manage users, connect/remove cloud accounts, dismiss zombies. |
| `member` | Daily FinOps work — connect/update accounts, trigger scans, dismiss/snooze zombies. |
| `viewer` | Read-only — zombies, summary, trends, costs. Can't change anything. |

A few things worth knowing:

- An organization always needs at least one `owner`. Removing or demoting the last
  one is blocked — transfer ownership first.
- Only an `owner` can promote someone *to* `admin` (an `admin` can't mint another
  `admin`, closing an escalation path).
- Anyone can remove themselves from an organization at any time (subject to the
  last-owner rule above).
- Demoting a logged-in user takes effect on their very next request — no session
  revocation needed, the dashboard just re-renders once it sees a `403`.

## SSO (OIDC)

AxiaOps supports OIDC-based single sign-on — tested against Microsoft Entra ID and
Keycloak, and should work with any standards-compliant OIDC provider. SAML isn't
supported.

1. **Settings → SSO → Connections → New** — give it a label, paste your IdP's
   Discovery URL, Client ID, and Client Secret.
2. **Domains → Add** your organization's email domain, and complete DNS TXT
   verification (proves you own the domain — this is what stops one organization
   from spoofing another's SSO).
3. Leave **Enforcement** on `optional` until you've confirmed the round-trip works.
   Flipping straight to `required` on a misconfigured connection locks out password
   login for that domain — the only escape hatch is `/v1/auth/logout`.
4. *(Optional)* **Group Mappings** — map an IdP group to an AxiaOps role, so new SSO
   users land with the right permissions on first login instead of the connection's
   default role.

Once it's working, log in with an email on the verified domain — AxiaOps redirects
to your IdP automatically and provisions the user on first successful login.

## Learn more

The full request-level flow, permission model internals, and the runtime security
model live in the repo's [`docs/AUTHENTICATION.md`](https://github.com/ahmd-soliman/axiaops/blob/main/docs/AUTHENTICATION.md).
