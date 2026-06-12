# Versioning & Releases — AxiaOps

## TL;DR

- **Scheme:** [Semantic Versioning 2.0.0](https://semver.org/spec/v2.0.0.html).
- **Format:** `MAJOR.MINOR.PATCH[-PRERELEASE]` — **no `v` prefix** (the git tag *is* the version string; matches the `APP_VERSION=2.6.0`-style examples in service `CLAUDE.md` files).
- **Current line:** pre-1.0. The first cut is `0.1.0-alpha.1`. `1.0.0` is reserved for GA (first paying customer per [`business_plan.md`](business_plan.md)).
- **Phase numbers are NOT version numbers.** `Phase 2` (Tasks.md) is a planning bucket; versions track release artefacts. See [§Phases vs versions](#phases-vs-versions).

---

## Format

```
MAJOR.MINOR.PATCH                       e.g. 0.3.0
MAJOR.MINOR.PATCH-PRERELEASE.N          e.g. 0.3.0-alpha.2
```

- No `v` prefix. Tag is `0.3.0`, not `v0.3.0`. CI consumes the bare tag verbatim: `APP_VERSION=${CI_COMMIT_TAG:-…}` in `.gitlab-ci.yml`.
- Lowercase suffix identifiers (`-alpha.1`, not `-Alpha.1`).
- Numeric suffix counter (`-alpha.1`, `-alpha.2`, …), never bare `-alpha`. This keeps sort order deterministic.

### Pre-release identifiers

| Suffix     | Meaning                                                                                                                                | Stability bar                                                          |
| ---------- | -------------------------------------------------------------------------------------------------------------------------------------- | ---------------------------------------------------------------------- |
| `-alpha.N` | Internal / engineering cuts. Schema or API may change between alphas without notice.                                                   | Boots, passes CI, fit for our own dogfooding (dev-1/dev-2/staging).    |
| `-beta.N`  | Design-partner cuts. Breaking changes still allowed but called out in the CHANGELOG entry.                                             | Fit to hand to a self-hosted design partner (Phase 2.7 / Phase 3 GTM). |
| `-rc.N`    | Release candidate for the upcoming `X.Y.0`. No new features — only fixes for issues found in `-beta.N`.                                | We'd ship it if no one reports a blocker.                              |
| _(none)_   | Stable cut of `X.Y.Z`. Breaking change requires a new minor (pre-1.0) or major (post-1.0).                                             | Promised to customers.                                                 |

Sort order semver enforces: `0.1.0-alpha.1 < 0.1.0-alpha.2 < 0.1.0-beta.1 < 0.1.0-rc.1 < 0.1.0`.

---

## Pre-1.0 regime (current)

Semver §4: while `MAJOR` is `0`, "anything MAY change at any time." We use this latitude but stay disciplined:

- **`0.Y.0`** — bump on substantive feature work, schema migrations that aren't strictly additive, or any change a customer would notice.
- **`0.Y.Z`** — bump on bug fixes / additive changes inside the current `0.Y` line.
- **Pre-release suffix** is independent of the number. `0.1.0-alpha.7` and `0.2.0-alpha.1` are both legal — the suffix says "lifecycle stage," the number says "change delta."

**`1.0.0` is reserved.** Per the docs, it lines up with first paying customer / GA, which requires MSA + DPA + SOC 2 commitment (`business_plan.md`). Don't burn `1.0.0` on a routine bump.

---

## Phases vs versions

Easy trap to fall into: "we're in Phase 2, so the version should be `0.2.x`." Don't.

| Axis            | What it tracks                       | Where it lives                |
| --------------- | ------------------------------------ | ----------------------------- |
| **Phase**       | Project-planning scope (what work is in flight) | `Tasks.md`, `docs/phase2-plan.md` |
| **Version tag** | Release artefact (what got cut)      | `git tag`, `APP_VERSION`, `/v1/version` |

A phase produces many releases (probably 10+ pre-release cuts before Phase 2 finishes). Phase numbers and version-minor numbers will drift apart on the first patch release inside a phase. The lifecycle **suffix** (`-alpha` / `-beta`) is the right place to encode phase-correlated maturity — not the number.

Rough correlation (not a contract):

| Phase                          | Typical lifecycle suffix |
| ------------------------------ | ------------------------ |
| Phase 2 — Alpha (current)      | `-alpha.N`               |
| Phase 2.7 — Self-hosted v1     | `-beta.N` (design partners) |
| Phase 3 — Beta / GTM           | `-beta.N` → `-rc.N`      |
| First paying customer / GA     | `1.0.0`                  |

---

## Release promotion (develop → main)

**Tags are cut from `main`, never from `develop` or feature branches.** The CI is built around this:

- `deploy:staging` (`.gitlab-ci.yml`) auto-fires on three triggers: pushes to `main`, pushes to `develop` (manual button), and **tag pipelines** (auto). The tag-pipeline path re-deploys staging with a properly-labelled release image so `/v1/version` matches the tag string.
- `deploy:production` is **tag-only** — its rule matches `$CI_COMMIT_TAG =~ /^[0-9]+\.[0-9]+\.[0-9]+(-[a-z0-9.]+)?$/` with a manual click. A raw `main` commit cannot promote to production; every production deploy must be a named release.
- Tagging from `develop` or a feature branch still builds and publishes a registry image (workflow allows the pipeline), but every release should pass through the `develop → main` merge audit trail before becoming a candidate. See [§What NOT to do](#what-not-to-do).

**Promotion flow** for every release cut:

1. Confirm `develop` is green on CI and contains everything the release should ship.
2. Open MR `develop → main` titled `release: X.Y.Z[-PRERELEASE.N]`. Body summarises the diff (or links to the CHANGELOG entry, once that exists).
3. Merge (squash off — preserve develop's commit history on main).
4. Tag `main` at the merge commit — see [§How to cut a release](#how-to-cut-a-release).

`develop` stays the integration trunk; `main` is "what's been released" + the CI release-line. The two should be very close to each other at any moment — `main` lags `develop` only by the time between merge and the next release MR.

---

## Where the version surfaces

The convention is already implicit in the dashboard code: **state goes in the banner, identity (version + commit) goes in the footer and License page.** Don't wedge the version string into `LicenseBanner` — it's reserved for actionable license-lifecycle warnings (in-grace, expired). A version number has no call-to-action and would train users to ignore the banner.

| Surface | What it shows | Code |
| --- | --- | --- |
| **Build footer** (every page) | One product identity from `/v1/version`: the **release tag when present, else the commit**. A tag is immutable and 1:1 with its commit, so it fully pins the build on its own — `AxiaOps 0.1.0-alpha.1`; the commit is shown only when there's no tag (branch/local builds, where `version` is a slug like `develop`) — `AxiaOps abc1234`. Discriminator is a frontend semver test on `version`. Because `deploy:production` is tag-only, production is always tag-only here — the commit SHA is never surfaced to customers (no special-casing). A ` · <env>` suffix is appended for non-production to disambiguate staging/dev. Dashboard has no independent bundle version (the `VITE_APP_VERSION` wiring was dropped in cc196b2 — traceability is Path-A-only). License-independent: renders in SaaS (`managed`) mode where the License page is hidden. `userSelect:'all'` so a single click selects the whole identifier for support tickets. | `services/dashboard/src/components/AppShell.jsx:202` |
| **Settings → License page** | Full claim block: Service / Version / Commit / Env alongside license claims (customer_id, expires_at, max_orgs). | `services/dashboard/src/pages/settings/License.jsx` |
| **`GET /v1/version`** | Canonical machine-readable source. Drives the two above via React Query key `['api-version']`. | `services/api/internal/api/handler.go` |
| **`LicenseBanner`** | **License state only** (in-grace countdown, expired call-to-action). Never the version. | `services/dashboard/src/components/LicenseBanner.jsx` |

---

## How to cut a release

1. **Decide the version.** Check the latest release tag (`git tag --sort=-v:refname -l '[0-9]*.[0-9]*.[0-9]*' | head -1`) and pick the next per the rules above. The semver glob is a guard against ever-reintroducing freeform tag names — anything that doesn't match is filtered out.
2. **Update the CHANGELOG on `develop`.** In [`CHANGELOG.md`](../CHANGELOG.md), move entries from `## [Unreleased]` into a new section headed `## [X.Y.Z] — YYYY-MM-DD`. At the bottom of the file, update the `[Unreleased]` link to compare against the new tag (`X.Y.Z...develop` instead of the previous tag) and add a `[X.Y.Z]: https://gitlab.com/axiaops/axiaops/-/tags/X.Y.Z` line. Commit on `develop` with `chore(release): X.Y.Z`. Format is [Keep a Changelog 1.1.0](https://keepachangelog.com/en/1.1.0/) — see the "How to update" section at the top of the file for subheading order.
3. **Promote `develop` → `main`** via the flow in [§Release promotion](#release-promotion-develop--main). Wait for the MR to merge.
4. **Tag `main`** at the merge commit:
   ```bash
   git checkout main && git pull
   git tag -a 0.1.0-alpha.1 -m "0.1.0-alpha.1"
   git push origin 0.1.0-alpha.1
   ```
   Annotated tags only (`-a`) — lightweight tags lose author + message metadata.
5. **CI does the rest.** The tag pipeline runs the full test + build suite, then `build:images` publishes registry images with `APP_VERSION=$CI_COMMIT_TAG` so `/v1/version` and the footer match the tag string. `deploy:staging` then **auto-fires from the tag pipeline** (re-deploying staging with the release-labelled image). `deploy:production` becomes available as a manual click on the same tag pipeline. The `main`-branch pipeline that produced the merge commit *also* deployed staging earlier, with `APP_VERSION=main`; the tag-pipeline deploy overwrites that with the better-labelled image.
6. **Verify** post-pipeline:
   ```bash
   curl -s https://axiaops-<env>.example.com/v1/version | jq .version
   # expect "0.1.0-alpha.1"
   ```

### Hotfixes

For a fix on an already-released line: branch off the tag, fix, tag `0.Y.(Z+1)` from that branch, merge back to `main`. We don't have a `release/0.Y` branch convention yet — add one when the first hotfix actually needs it, not before.

### What NOT to do

- **Don't retag.** Tags are immutable contracts with CI and the LicenseBanner. If a tag is wrong, cut the next one (`0.1.0-alpha.2`) and note the skip in the CHANGELOG.
- **Don't tag from `develop` or feature branches.** Only `main` (see [§Release promotion](#release-promotion-develop--main)). The CI rule `.gitlab-ci.yml:34` (`- if: '$CI_COMMIT_TAG'`) has no branch constraint, so a tag on `develop` would still build images and *could* even reach `deploy:production` (the production rule is tag-only, not branch-gated). The reason to still require `main` is the **audit trail**: every release passes through a `develop → main` MR so reviewers see exactly what's in scope. Skipping that hides scope from anyone other than the tagger.
- **Don't tag with `v` prefix.** The CI substitution doesn't strip it; you'd get `APP_VERSION=v0.1.0-alpha.1` instead of `0.1.0-alpha.1`.
- **Don't skip the suffix on pre-release cuts.** A bare `0.1.0` carries an implicit promise — "we'd ship this." Use `-alpha.N` until that's true.

---

## Release cadence & support

Cadence is **pre-1.0 latitude** today: we cut a release when there's something meaningful to ship, not on a calendar. The policy below is what we commit to **once `1.0.0` ships**.

### Cadence (post-1.0)

| Release type                          | Cadence                              | Trigger                                                                                                                                                                                          |
| ------------------------------------- | ------------------------------------ | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------ |
| **Minor** (`X.(Y+1).0`)               | Quarterly (max)                      | Substantive feature work, schema migrations, customer-visible changes. If `develop` accumulates meaningful work mid-quarter, cut earlier — quarterly is a ceiling, not a target.                  |
| **Patch** (`X.Y.(Z+1)`)               | As needed, typically every 4–6 weeks | Bug fixes, additive non-schema changes, security fixes batched into the next patch on the supported lines.                                                                                       |
| **Hotfix** (`X.Y.(Z+1)` off-cycle)    | As needed                            | Security CVE, customer-blocking regression. Bypasses normal cadence.                                                                                                                             |

### Supported-versions window (post-1.0)

| Line                                  | State       | Backports                                              |
| ------------------------------------- | ----------- | ------------------------------------------------------ |
| Current minor (`X.Y.x`)               | Supported   | All patches (bugs, security, additive non-schema)      |
| Previous minor (`X.(Y-1).x`)          | Maintenance | Security patches only                                  |
| Earlier (`X.(Y-2).x` and below)       | EOL         | None — customer must upgrade to consume any fix        |

Pre-1.0 (current): only the latest `0.Y.x` line is supported. Customers on `0.(Y-1).x` upgrade to consume fixes.

### Migration backwards-compatibility

We use `golang-migrate` (`services/shared/storage/postgres/migrations/`). Every release embeds the cumulative migration set and applies whatever's missing at startup. **Skip-a-minor is supported in principle** — a customer on `0.3.0` upgrading directly to `0.5.0` replays every migration from `0.3.0+1` through `0.5.0`'s tip.

The contract that makes that work:

1. **Never delete a released migration.** Once a `.up.sql` is in a tagged release, it's immutable. Fix forward with a new migration — don't edit the old file.
2. **Never reuse a migration number.** Numbers are sequential; gaps are fine, reuse breaks `golang-migrate`'s version tracking.
3. **Down migrations are for local dev only.** We don't promise downgrade via `.down.sql`. Production downgrade is "restore from backup" — the `.down.sql` files exist so engineers can iterate locally.
4. **Renames and drops are a two-release dance.** Release N adds the new column + dual-writes; release N+1 removes the old column. Never drop-in-one if there's any prod data on the column.
5. **Migrations should be fast.** Single-digit seconds on a 100k-row table. Anything potentially long-running needs a runbook entry, not just a `.up.sql` file.

The five rules bind the engineer cutting the release more than they bind the customer. The customer runs the new binary; we do the work that makes "run the new binary" safe regardless of what version they were on.

---

## Open follow-ups

- **`docs/USER_STORIES_STATUS.md` cross-link** — when a version ships, mark the relevant user stories with the tag they landed in.
- **Consider tag-only staging deploys.** Today `deploy:staging` fires on both `main` pushes (auto) and tag pushes (auto). That gives you a continuous "merge → staging" verification loop at the cost of (a) one wasted deploy per release cut and (b) a window where `/v1/version` on staging reports `APP_VERSION=main` instead of a labelled release. Flip to tag-only when any of these become true: (1) design partners hit staging and shouldn't see untagged builds, (2) compliance requires every change on a prod-like environment to be a named release, (3) staging starts being treated like production (long-running sessions you don't want disrupted by main pushes). The change is a single-line edit — remove the `- if: '$CI_COMMIT_BRANCH == "main"'` rule from `deploy:staging`.
