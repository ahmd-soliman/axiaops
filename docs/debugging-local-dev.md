# Debugging AxiaOps in VS Code — Local Dev Runbook

How to attach Delve to the host-mode Go services from VS Code, what
breakpoints are useful, and how to recover from the two failures
that block a fresh laptop:

1. Process exits at startup with `auth: DEV_MODE=true requires
   DEV_ORGANIZATION_ID to be set` (an env-config gap in the launch
   configs).
2. Delve crashes during DWARF load with
   `panic: runtime error: slice bounds out of range [14:0]` in
   `parseFileEntries5` (a Delve/Go-version incompatibility).

Companion to `services/api/CLAUDE.md` and the
[`launch.json`](../.vscode/launch.json) header. The launch configs
themselves do **not** start Postgres for you — pair them with a
running stack (see [Stack pairing](#stack-pairing)).

## Prerequisites

- **Go 1.25.x or 1.26.x.** Delve's release tag must understand the
  DWARF emitted by the Go you're using. With Go **1.26.2** the
  matching released `dlv 1.26.2` panics inside `parseFileEntries5`
  on the API binary; install Delve from `master` or downgrade Go
  to 1.25.x. See [Delve panic on Go 1.26.2](#delve-panic-on-go-1262).
- **Delve installed under `$GOBIN`** (defaults to `~/go/bin`):
  ```
  go install github.com/go-delve/delve/cmd/dlv@master
  ```
  VS Code's Go extension picks up `~/go/bin/dlv` automatically. If
  it doesn't, pin it explicitly in `.vscode/settings.json`:
  ```jsonc
  { "go.alternateTools": { "dlv": "/Users/<you>/go/bin/dlv" } }
  ```
- **VS Code Go extension** with the standard `delve` debugger type.
- **Stack dependencies running** — at minimum Postgres for both
  services. Redis + a non-debugged Ingestion are needed for the
  `(auth)` variants. The launch configs deliberately don't start
  these; `make start-dev` does.

## Stack pairing

| Compound config | Pairs with | Auth | DB | Notes |
|---|---|---|---|---|
| **Debug Full Stack** | `make start-dev` (then stop the host Go services it spawns; keep the Postgres container) | `DEV_MODE=true`, JWT bypassed | Local Postgres container | The default. F5 builds and attaches to API + Ingestion + a Chrome session for the dashboard. |
| **Debug Full Stack (auth)** | `make start-staging`, then `docker stop axiaops-api axiaops-ingestion` | `DEV_MODE=false`, real Kinde JWTs | Compose Postgres + Redis | For debugging auth flows or anything that depends on Redis. |

The launch configs assume host ports 5432 (Postgres) and — for the
auth variants — 6379 (Redis), 8081 (Ingestion). Free those before
hitting F5; that's why the two-step `make start-staging` →
`docker stop axiaops-api axiaops-ingestion` recipe exists.

## Debug session

Hit **F5** with no config selected to launch the default compound
("Debug Full Stack"). Or use the picker:

- **Debug Full Stack** — most day-to-day work. DEV_MODE=true.
- **Debug Full Stack (auth)** — when Kinde or Redis matters.
- **Debug Migrate CLI** — runs `services/shared/cmd/migrate`
  against the API's `.env`. Useful when a migration misbehaves.
- **Debug Tests** — debugs the test file currently focused in the
  editor. Sets one breakpoint in the test, hit F5.

The per-service entries (`Debug API`, `Debug Ingestion`, and the
auth twins) are hidden from F5 by default — they back the
compounds. Flip `presentation.hidden` to `false` in `launch.json`
if you want to debug one service in isolation.

## Required env vars in `DEV_MODE=true`

`services/api/cmd/main.go` calls `die()` at startup if any of these
are missing when `DEV_MODE=true`:

- `DEV_ORGANIZATION_ID` — no default, hard-required. The API uses
  it as the literal organization id and pins the row at startup so
  `DevBypass` can inject it onto every request without DB lookups.
- `DEV_USER_ID` — defaults to `dev-user-axiaops` if unset.
- `DEV_USER_EMAIL` — defaults to `dev@axiaops.local` if unset.

The launch configs set all three explicitly to the values
`scripts/seed_test_data.sh` writes, so seeded data is visible
through the debugger:

```jsonc
"env": {
  "DEV_MODE": "true",
  "DEV_ORGANIZATION_ID": "dev-organization-axiaops",
  "DEV_USER_ID": "dev-user-axiaops",
  "DEV_USER_EMAIL": "dev@axiaops.local",
  ...
}
```

If you change `DEV_ORGANIZATION_ID` here you must reseed (or the
dashboard will render an empty org).

## Where to set breakpoints

The handler methods live in `services/api/internal/api/handler.go`
unless noted otherwise. Drop a breakpoint on the first line of the
method to break on every request to that route:

| Endpoint | Method | Notes |
|---|---|---|
| `GET /v1/summary` | `getSummary` | Aggregate savings, per-service breakdown |
| `GET /v1/zombies` | `listZombies` | Filtered by `account_id`, `service` |
| `GET /v1/resources` | `listResources` | Active + zombie resources |
| `GET /v1/accounts` | `listAccounts` | |
| `GET /v1/costs` | `listCosts` | |
| `GET /v1/trend` | `getTrend` | |
| `POST /v1/accounts/{id}/scan` | `scanAccount` | Fires goroutine — switch to it via the **Call Stack** panel |

Other useful spots:

- `services/api/internal/middleware/auth.go` — the `DEV_MODE`
  short-circuit (`DevBypass`) and the JWKS-verification path. Watch
  how `organization_id` lands on the request context.
- `services/api/cmd/main.go` request-logging middleware (~L161) —
  fires for every request before routing. Good for tracing
  request_id and timing.
- `services/shared/storage/postgres/postgres.go` — drop one inside
  `LoadZombies`, `Summary`, etc. to inspect SQL parameters and the
  RLS-set `app.organization_id` on the connection.

## Evaluating expressions while paused

VS Code's Go Debug Console takes **raw Go expressions**. Don't
prefix with `print` (GDB syntax) — type the expression itself:

```
accountID
r.URL.Query().Get("account_id")
storage.OrganizationIDFromCtx(r.Context())
len(zombies)
```

To run Delve REPL commands, prefix with `dlv ` (lowercase, with
space):

```
dlv args
dlv vars
dlv goroutines
dlv stack
```

Caveats:

- The expression's identifiers must be in scope at the **paused
  frame**. If you're stopped inside `listZombies` you won't see
  locals from `getTrend`.
- Function calls in expressions work for pure helpers and most
  methods. Calls that lock or block (e.g. running another DB query
  while the connection is held) can hang the eval — restart the
  session if so.
- The **Variables** panel always shows what's actually in scope.
- **Conditional breakpoints** (right-click the dot → Edit
  Breakpoint) and **logpoints** (`Add Logpoint`, message
  `org={tid} account={accountID}`) take the same expression syntax.

## Troubleshooting

### Process exits with code 2 immediately after launch

```
auth: DEV_MODE=true requires DEV_ORGANIZATION_ID to be set
dlv dap exited with code: 2
```

The launch config didn't pass `DEV_ORGANIZATION_ID`. The committed
`launch.json` sets it for both `Debug API` and `Debug Ingestion`
(DEV_MODE=true). If you've customised yours, restore it from the
repo or re-add the three `DEV_*` vars listed above.

### Delve panic on Go 1.26.2

```
panic: runtime error: slice bounds out of range [14:0]
github.com/go-delve/delve/pkg/dwarf/line.parseFileEntries5
delve@v1.26.2/pkg/dwarf/line/line_parser.go:315
```

Delve's released line-info parser at v1.26.2 chokes on something
the Go 1.26.2 toolchain emits in larger binaries. The migration
binary's debug info is small enough to slip through; the API
binary trips it.

The program runs anyway because LLDB launches the binary in a
separate process — but Delve's DWARF maps never finished loading,
so breakpoints, stepping, and variable inspection don't work. You
get logs and not much else.

Fix:

```
go install github.com/go-delve/delve/cmd/dlv@master
```

Restart VS Code if it caches the path. If `@master` still panics,
fall back to Go 1.25.x.

### Empty dashboard after the debugger attaches

The org row exists, but nothing shows. The DB is empty for that
org. Either:

- `make seed` — populates `dev-organization-axiaops` with dummy
  accounts, zombies, and 90 days of trend data.
- Connect a real AWS account and trigger a scan from the dashboard
  Accounts page.

### Cannot drag the bottom panel taller

Use the command palette: `View: Toggle Maximized Panel`, or
`View: Move Panel Right` to dock it editor-height on the side.
`⌘J` toggles the panel itself.
