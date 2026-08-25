# Demo setup — Alice, Bob, Carol

How to seed an AxiaOps env with the three named demo personas (alice / bob / carol) so prospect walk-throughs and screen recordings always log in as the same identifiable users across reseeds.

This doc covers:

- The persona shape (who owns what, who can switch where)
- The password flow (env var, never in source)
- Running `make seed --demo` on each env type
- The `--bootstrap-first` shortcut for ephemeral demo envs

## The three personas

| Email | Display name | Primary role | Cross-org membership |
|---|---|---|---|
| `alice@axiaops.io` | Alice (FinOps Lead) | **owner** of AxiaOps Dev (bootstrap org) | viewer in Acme + Globex |
| `bob@globex.io` | Bob (Cloud Architect) | **owner** of Globex Inc | viewer in AxiaOps Dev + Acme |
| `carol@acme.io` | Carol (Finance Lead) | **owner** of Acme Corp | viewer in AxiaOps Dev + Globex |

The membership shape lets demos walk through the org-switcher in three directions — alice → Globex (viewer view), bob → Acme (viewer view), carol → AxiaOps Dev (viewer view) — without re-logging.

All three share the same password, sourced from the `DEMO_USERS_PASSWORD` env var at seed-time. Same password = simpler demo handoff between teammates; different argon2id salts per row because each row is hashed independently. (We deliberately don't reuse the same hash across rows even when the cleartext matches.)

## The password — where it lives

**Not in this repo.** Never. The hash is generated on the fly from the `DEMO_USERS_PASSWORD` env var at the moment `make seed --demo` runs.

Where the cleartext actually lives:

- **Local development**: in your `~/.zshrc` (or whatever shell init) — `export DEMO_USERS_PASSWORD='…'`. Optional; the seed gracefully skips persona creation if unset.
- **CI / nightly reseed jobs**: a per-env masked + protected CI variable named `DEMO_USERS_PASSWORD`, scoped to the env (Settings → CI/CD → Variables → Environment scope: `demo` etc.).
- **Team password manager**: 1Password / Bitwarden vault entry "AxiaOps demo users — alice/bob/carol". Anyone joining the team gets the password from there, not from a chat message or an issue comment.

Rotating the password is just changing the env var and re-running the seed — the `ON CONFLICT … DO UPDATE` clause re-hashes and overwrites in place.

## Running it

### Local (`make seed --demo`)

DEV_MODE bypasses auth, so the personas are useful for testing the org-switcher UI and CSV exports but not for actual login (the dev-bypass middleware logs you in as `dev-user-axiaops`).

```bash
export DEMO_USERS_PASSWORD='demo@AxiaOps!'   # whatever you've agreed as the team demo password
make seed                                    # base data only
DEMO_USERS_PASSWORD="$DEMO_USERS_PASSWORD" make seed-demo  # adds Acme + Globex + alice/bob/carol
```

Without `DEMO_USERS_PASSWORD` set, the alice/bob/carol block prints a one-line warning and skips user creation. Re-running with the env var set fills the gap on the next run.

### Remote auth-on envs — already bootstrapped (`preview` / `demo` / `staging` / `integration`)

Bootstrap once via the dashboard at `https://axiaops-<env>.local/bootstrap`. Then:

```bash
export DEMO_USERS_PASSWORD='demo@AxiaOps!'
./scripts/seed_test_data.sh --remote preview --demo --yes
```

The existing bootstrap user (your bootstrap email/password) keeps its login. Alice/Bob/Carol are added alongside as additional users.

### Remote env that's **not yet bootstrapped** — `--bootstrap-first`

For ephemeral demo envs (the public demo deployment scenario), the bootstrap ceremony is friction. The `--bootstrap-first` flag skips it by minting the first org + alice as the first owner directly via SQL, then sealing `/auth/bootstrap` so subsequent install-token POSTs return 409.

```bash
export DEMO_USERS_PASSWORD='demo@AxiaOps!'
./scripts/seed_test_data.sh --remote demo --demo --bootstrap-first --yes
```

This is **opt-in, demo-tier only**. Requires all three of:
- `--bootstrap-first` (flag)
- `--demo` (the personas are demo-mode only)
- `DEMO_USERS_PASSWORD` env var (so alice has a real hashed login)

If any are missing the script bails loudly. Don't use this against `staging` or `integration` — those envs want the dashboard bootstrap ceremony's install-token gate.

## Security model — when `--bootstrap-first` is safe

The native bootstrap ceremony does five things at the SQL level:

1. INSERT into `organizations`
2. INSERT into `users` (with `password_hash`)
3. INSERT into `memberships` (`role='owner'`)
4. DELETE from `bootstrap_state` (seals `/auth/bootstrap`)
5. Validates the install token before any of the above

Steps 1-4 are pure SQL. The seed script has `axiaops_owner` schema access, so it can execute them directly. `--bootstrap-first` skips step 5.

**Safe to skip step 5 when**: you control the deployment AND the seed runner. The install-token gate proves "this seed-runner had access to the install token"; on an env where you also control the secrets store and the host, that proof adds no real security beyond "you have repo + secrets access", which the seed runner already does.

**Unsafe to skip step 5 when**: you want a verifiable independent gate that "the person who set up this install was operationally privileged at install time". Staging / preview / integration / prod want this gate because a future contributor with seed access could otherwise replace the legitimate first owner.

The flag's three-way precondition (`--bootstrap-first` + `--demo` + `DEMO_USERS_PASSWORD`) is designed to prevent accidental fire on the wrong env.

## What the seed actually writes

For the curious — the SQL the personas block executes:

```sql
-- Idempotent on the (id) PK + on re-runs with a rotated password
INSERT INTO users (id, organization_id, external_id, email, name, password_hash, password_set_at, …) VALUES
  ('demo-user-alice', '<bootstrap-org>',  'demo:alice', 'alice@axiaops.io', 'Alice (FinOps Lead)',    '<argon2id>', NOW(), …),
  ('demo-user-bob',   '<globex-org-id>',  'demo:bob',   'bob@globex.io',    'Bob (Cloud Architect)',  '<argon2id>', NOW(), …),
  ('demo-user-carol', '<acme-org-id>',    'demo:carol', 'carol@acme.io',    'Carol (Finance Lead)',   '<argon2id>', NOW(), …)
ON CONFLICT (id) DO UPDATE SET password_hash = EXCLUDED.password_hash, password_set_at = EXCLUDED.password_set_at;

-- Cross-org membership matrix (9 rows total — 3 personas × 3 orgs)
INSERT INTO memberships (id, organization_id, user_id, role, …) VALUES
  (…, '<bootstrap-org>',  'demo-user-alice', 'owner',  …),
  (…, '<acme-org-id>',    'demo-user-alice', 'viewer', …),
  (…, '<globex-org-id>',  'demo-user-alice', 'viewer', …),
  …
ON CONFLICT (organization_id, user_id) DO NOTHING;
```

The hash is generated by `services/api/cmd/hash-password`, which calls `services/api/internal/auth.Hash()` — the exact same code path `/v1/auth/login` validates against. So a hash produced by the seed-time CLI matches the runtime auth without parameter drift.

## See also

- `scripts/seed_test_data.sh` — the `--demo` and `--bootstrap-first` implementation.
- `services/api/cmd/hash-password/main.go` — the hash-only CLI used by the seed.
- `services/api/internal/auth/password.go` — argon2id parameters (kept in lockstep with the CLI).
