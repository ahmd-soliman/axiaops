# First-run bootstrap — local testing runbook

Operator-facing walkthrough for testing the Phase B1 native-auth flow
end-to-end against `make start-staging`. Mirrors the production install
shape (HTTPS at the edge, native session cookie, install-token-via-file).

For the design rationale and acceptance criteria see
[`docs/sso-implementation-plan.md`](sso-implementation-plan.md) §4.5 / §4.6.

## One-time prerequisites

Install [mkcert](https://github.com/FiloSottile/mkcert) so the local stack
can serve real TLS at `https://localhost:8443`. Without it the dashboard
container fails to start (nginx can't load the cert files).

```bash
brew install mkcert         # macOS — Chrome / Safari only
brew install mkcert nss     # macOS — also covers Firefox
```

`make tls-certs` (run automatically by `make start-staging`) calls
`mkcert -install` the first time, which prompts once for your sudo
password to install mkcert's root CA into the macOS keychain. After
that, generated certs are auto-trusted by the browser — no
"Warning: Potential Security Risk" page to click through.

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
reset, etc) you don't need to wipe — just skip to step 5 and use the
account you already have.

### 3. Start the stack

```bash
make start-staging
```

This runs in order: `tls-certs` (idempotent — skips if certs already
exist) → `migrate` (apply schema) → `docker compose up --build -d`.

Wait until you see:

```
Dashboard:  https://localhost:8443  ← use this in the browser
Bootstrap:  https://localhost:8443/bootstrap
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

### 5. Open the bootstrap form

Open **`https://localhost:8443/bootstrap`** in a browser.

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

## Troubleshooting

### `make tls-certs`: "mkcert not installed"

`brew install mkcert` (and optionally `nss` for Firefox).

### Browser shows "Warning: Potential Security Risk"

mkcert's root CA didn't make it into the trust store. Re-run:

```bash
mkcert -install
```

In Firefox, you also need `nss`:

```bash
brew install nss
mkcert -install        # re-run after nss is present
```

### Dashboard container won't start

Most likely the cert files are missing or the filenames don't match.
Expected: `services/dashboard/certs/localhost.pem` and
`localhost-key.pem`. If you see `localhost+N.pem` (with a numeric
suffix), `make tls-certs` was run with multiple host arguments — delete
the extras and re-run:

```bash
rm services/dashboard/certs/localhost+*
make tls-certs
docker compose up -d dashboard
```

### Bootstrap returns 409 immediately

An organisation already exists. Either truncate (step 2 above) or use
the existing user via `/login`.

### Bootstrap returns 401 invalid_token

You pasted the wrong token, OR the token includes the shell's `%` /
`$` trailing character. The token is exactly 64 hex chars
`[0-9a-f]{64}`.

### Cookie not sticking — `/v1/me` returns 401 right after a 200 bootstrap

Two known causes:

1. **You're hitting `http://localhost:8082` directly** instead of
   `https://localhost:8443`. Plain HTTP doesn't accept the `Secure`
   cookie. Use HTTPS — the HTTP port redirects via 308.
2. **`X-Forwarded-Proto: https` isn't being set on the proxy hop** —
   shouldn't happen with the bundled `services/dashboard/nginx.conf`
   but worth checking if you've customised it.

### "Address already in use" on :8443 / :8080 / :5432

Old containers from a previous run. `make stop` is idempotent and
should clear them; if not, `docker ps -a | grep axiaops` and
`docker rm -f <id>` the stragglers.

## Production parity notes

The local stack mirrors production in shape:

| | Local docker-compose | Production (App Runner) |
|---|---|---|
| TLS termination | nginx in `axiaops-dashboard` container | App Runner LB |
| API protocol from edge | `X-Forwarded-Proto: https` (set by nginx) | `X-Forwarded-Proto: https` (set by App Runner) |
| Internal API plain-HTTP | yes (over docker network) | yes (over VPC) |
| Cookie `Secure` flag | derived per-request from header | derived per-request from header |
| Install-token path | `/var/run/axiaops/initial_setup_token` (mode 0600) | same; persistent disk if running on App Runner with volume |

The only meaningful difference is that production has a real cert
issued by the LB, not mkcert. The cookie code-path is identical.
