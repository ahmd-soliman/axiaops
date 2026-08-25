# First-run bootstrap — local testing runbook

Operator-facing walkthrough for testing the Phase B1 native-auth flow
end-to-end against `make start-staging`. Mirrors the production install
shape *minus* edge TLS — production terminates HTTPS at the edge proxy
(ECS Express / customer ingress), this local stack runs plain HTTP and
relies on the cookie path's `X-Forwarded-Proto` propagation to behave
identically when there *is* an edge proxy in front.

## One-time prerequisites

None. The local stack runs plain HTTP; no mkcert / certs / TLS setup.
Earlier revisions required mkcert for an in-container HTTPS listener;
that listener was removed because it was a latent landmine for on-prem
deploys (no cert volume → nginx refused to start). TLS termination is
the edge proxy's job in every real deployment.

## Bootstrap flow — step by step

### 1. Stop anything that's running

```bash
make stop
```

### 2. Decide whether to start fresh

Bootstrap is **single-use per install** — once an organisation exists,
the endpoint returns 409 forever. Two ways to reset for a clean test:

```bash
# Option A — full filesystem wipe (Postgres re-initdbs from scratch)
sudo rm -rf pg_data         # `sudo` because postgres files are root-owned

# Option B — truncate via SQL (faster; no sudo)
docker compose up -d postgres
docker exec axiaops-postgres psql -U axiaops_owner -d axiaops -c "
  TRUNCATE TABLE
    axiaops.audit_log, axiaops.memberships, axiaops.zombie_snapshots,
    axiaops.resource_records, axiaops.zombie_records, axiaops.cost_records,
    axiaops.accounts, axiaops.sessions, axiaops.password_resets,
    axiaops.bootstrap_state, axiaops.users, axiaops.organizations
  CASCADE;"
```

If you only want to *test* an existing user (login flow, password
reset, etc) you don't need to wipe — just skip to step 4 and use the
account you already have.

### 3. Start the stack

```bash
make start-staging
```

This runs in order: `migrate` (apply schema) → `docker compose up --build -d`.

Wait until you see:

```
Dashboard:  http://localhost:8082  ← use this in the browser
Bootstrap:  http://localhost:8082/bootstrap
```

### 4. Retrieve the install token

The token file path is `/var/run/axiaops/initial_setup_token` **inside
the API container** (mode 0600, deleted on first successful bootstrap).

```bash
docker exec axiaops-api cat /var/run/axiaops/initial_setup_token
```

⚠️ **The shell shows a trailing `%` (zsh) or `$` (bash) after the
token**. That's the shell's "no-trailing-newline" indicator, NOT part
of the token. The token is exactly **64 hex characters**. Don't include
the indicator when pasting.

Alternative — set `BOOTSTRAP_PRINT_BANNER=true` in `services/api/.env`
to also print a banner to the API container's stderr:

```bash
docker compose logs api 2>&1 | grep -A 8 "first-run setup"
```

> **On ECS Express / Fargate** the container filesystem isn't reachable
> (no `docker exec`, no ECS Exec), so the file above is a dead end.
> Production therefore sets `BOOTSTRAP_PRINT_BANNER=true` +
> `BOOTSTRAP_TOKEN_FILE_PATH=""` and reads the token from CloudWatch:
> ```bash
> aws logs tail /aws/ecs/axiaops-api --since 15m --region eu-central-1 --format short | grep -iA4 'first-run setup'
> ```
> You get **one** capture at first boot — a restart won't reprint (only the
> SHA-256 hash is kept). Recovery: delete the `bootstrap_state` row to re-mint.

### 5. Open the bootstrap form

Open **`http://localhost:8082`** in a browser. On a fresh install with no
organizations yet, the dashboard probes `GET /v1/auth/bootstrap/state`
at mount time and auto-redirects to `/bootstrap` —
older docs and the install banner still name `/bootstrap` directly, and
both paths land in the same place. Quick CLI probe before opening a
browser:

```bash
curl -s http://localhost:8082/api/v1/auth/bootstrap/state
# {"available":true}   ← redirect will fire on the dashboard
# {"available":false}  ← bootstrap is sealed; sign in at /login
```

| Field | Notes |
|---|---|
| Token | Paste from step 4 (no `%`) |
| Organisation name | Anything; defaults to "AxiaOps" if blank |
| Your name | Anything |
| Email | Anything memorable — that's what you'll use on `/login`. No verification email is sent (no SMTP in v1) |
| Password | Minimum 12 characters |

Submit. On success:
- The server creates the org, your user (with `role=owner`), an active
  session, and sets the `axiaops_session` cookie.
- The bootstrap_state row is deleted in the same transaction → endpoint
  is sealed forever (until a fresh DB).
- The token file `/var/run/axiaops/initial_setup_token` is removed.
- Dashboard redirects to `/` — you should land on the home view as
  owner (settings menu visible, etc).

The cookie is **non-Secure** under direct-HTTP localhost access; that's
correct for plain-HTTP traffic and matches what the api's cookie helper
emits when `X-Forwarded-Proto` is empty. Behind a real edge proxy that
sets `X-Forwarded-Proto: https`, the same code path emits a Secure cookie.

### 6. Verify

```bash
# Token file is gone (plan §4.6 AC2)
docker exec axiaops-api ls /var/run/axiaops/                 # empty

# Bootstrap row is gone (sealed)
docker exec axiaops-postgres psql -U axiaops_owner -d axiaops -c "
  SELECT count(*) FROM axiaops.bootstrap_state;"             # → 0

# Your user + owner membership exist
docker exec axiaops-postgres psql -U axiaops_owner -d axiaops -c "
  SELECT u.email, m.role, o.name AS org
  FROM axiaops.users u
  JOIN axiaops.memberships m ON m.user_id = u.id
  JOIN axiaops.organizations o ON o.id = m.organization_id;"
```

Then exercise the rest:

- `/logout` — cookie cleared
- `/login` with the same email + password — back in the dashboard
- A second visit to `/bootstrap` — should show 409
- 11 wrong-password login attempts from the same IP — the 11th gets a
  429 with `Retry-After` header (rate limit, plan §4.2)

## Changing a user's email

There is no `PATCH /v1/users/me/email` endpoint, by design.

- **SSO users** — change the email in your IdP (Keycloak, Entra, …).
  The next SSO login calls `UpsertUser` with the fresh claims and the
  local row updates in place. `users.sso_external_id` is the stable
  lookup key, not email.
  Don't try to mutate the email locally; the next login would silently
  overwrite it and produce a split-brain.

- **Native users** — no self-serve flow. Use the workaround below.
  Building a verified-change endpoint requires SMTP (to send a token to
  the new address) which v1 self-hosted doesn't have, and admin-mediated
  OOB tokens reduce to the workaround anyway.

### Workaround — fixing the bootstrap owner's typo (or any native user's email)

Solo owner with a typo'd email, no other members:

1. **Logged in as the typo'd owner**, open Settings → Members → Invite
   and invite `correct@example.com` at role `admin`. Copy the redemption
   URL from the response (no SMTP — admin shares OOB).
2. **Open the redemption URL in a private/incognito window** (so the
   existing session doesn't interfere). Set name + password. A second
   user now exists in the org with `role=admin`.
3. **Switch back to the typo'd owner's session** and call
   `POST /v1/organizations/transfer-ownership` with the new user's
   `user_id` (visible in Settings → Members). The typo'd user is now
   `admin`, the correct-email user is now `owner`.
4. **Still as the typo'd user**, call `DELETE /v1/users/me`. Passes the
   `ErrLastOwner` guard because the org has another owner. The user row
   is deleted; audit-log entries are anonymised across all orgs.

After step 4 the org has exactly one member, the new owner, with the
correct email. Six API calls, ten minutes, one-time per install.

If the org has *other* owners already, skip step 1 (just transfer to an
existing owner) and pick up at step 3 with their `user_id`.

## Troubleshooting

### Dashboard container won't start

Check the logs — `docker compose logs dashboard`. The most common
historical failure (nginx exiting with `cannot load certificate`) is
no longer possible because the SSL listener has been removed; if you
hit it on an older branch, rebase onto a branch that has this doc.

### Bootstrap returns 409 immediately

An organisation already exists. Either truncate (step 2 above) or use
the existing user via `/login`.

### Bootstrap returns 401 invalid_token

You pasted the wrong token, OR the token includes the shell's `%` /
`$` trailing character. The token is exactly 64 hex chars
`[0-9a-f]{64}`.

### Cookie not sticking — `/v1/me` returns 401 right after a 200 bootstrap

The session cookie should be non-Secure on the local stack (plain HTTP)
and round-trip cleanly. If it isn't:

1. **Check the cookie's `Secure` flag in DevTools.** If it's `Secure: true`
   on a plain-HTTP request, something upstream of the api is sending
   `X-Forwarded-Proto: https` even though the actual edge protocol is
   HTTP. The bundled `services/dashboard/nginx.conf` propagates the
   header instead of hardcoding it, so this only happens if you've
   customised the conf or are running behind an edge proxy that lies.
2. **Check the api's cookie helper** at `services/api/internal/auth/cookie.go`
   — `IsSecureRequest` reads `r.TLS != nil || r.Header.Get("X-Forwarded-Proto") == "https"`.
   Both should be false on this local stack.

### "Address already in use" on :8082 / :8080 / :5432

Old containers from a previous run. `make stop` is idempotent and
should clear them; if not, `docker ps -a | grep axiaops` and
`docker rm -f <id>` the stragglers.

## Production parity notes

The local stack mirrors production *post-edge* — i.e. what the api
sees once an edge proxy has terminated TLS and forwarded HTTP to the
service. The differences below are deliberate; the cookie code-path
is identical in both.

| | Local docker-compose | Production / on-prem dev/staging |
|---|---|---|
| Edge TLS | none — direct HTTP at `localhost:8082` | ECS Express ALB / on-prem reverse proxy |
| API protocol from edge | empty `X-Forwarded-Proto` (no edge) | `X-Forwarded-Proto: https` (set by edge) |
| Internal API plain-HTTP | yes (over docker network) | yes (over VPC / docker network) |
| Cookie `Secure` flag | non-Secure (header empty) | Secure (header propagated by dashboard nginx) |
| Install-token retrieval | `docker exec … cat /var/run/axiaops/initial_setup_token` (file reachable on the local container) | **ECS Express/Fargate:** file unreachable — `BOOTSTRAP_PRINT_BANNER=true` prints it to CloudWatch (`/aws/ecs/axiaops-api`); capture on first boot |

The cookie behaviour is identical *given the same input request* —
both code paths read `X-Forwarded-Proto` and decide. Local and prod
just produce different inputs.
