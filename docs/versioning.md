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

- `.gitlab-ci.yml:510` / `:850` — `deploy:staging` fires automatically on pushes to `main` (and to `develop`, for staging soak — but the release-line is `main`).
- `.gitlab-ci.yml:738` — `deploy:production` rule gates on `$CI_COMMIT_BRANCH == "main"` only.
- Tagging from `develop` would build registry images that the deploy pipeline can't promote to production. Tagging from a feature branch is worse — see [§What NOT to do](#what-not-to-do).

**Promotion flow** for every release cut:

1. Confirm `develop` is green on CI and contains everything the release should ship.
2. Open MR `develop → main` titled `release: X.Y.Z[-PRERELEASE.N]`. Body summarises the diff (or links to the CHANGELOG entry, once that exists).
3. Merge (squash off — preserve develop's commit history on main).
4. Tag `main` at the merge commit — see [§How to cut a release](#how-to-cut-a-release).

`develop` stays the integration trunk; `main` is "what's been released" + the CI release-line. The two should be very close to each other at any moment — `main` lags `develop` only by the time between merge and the next release MR.

### One-time bootstrap

`main` currently contains only the initial commit (`bf85ed7`); every real change has accumulated on `develop`. Before the first semver tag (`0.1.0-alpha.1`) can be cut, one bootstrap MR `develop → main` must fast-forward `main` to `develop`'s tip. After that, the steady-state promotion flow above takes over. This note can be deleted once the bootstrap MR merges.

---

## Where the version surfaces

The convention is already implicit in the dashboard code: **state goes in the banner, identity (version + commit) goes in the footer and License page.** Don't wedge the version string into `LicenseBanner` — it's reserved for actionable license-lifecycle warnings (in-grace, expired). A version number has no call-to-action and would train users to ignore the banner.

| Surface | What it shows | Code |
| --- | --- | --- |
| **Build footer** (every page) | `dashboard 0.1.0-alpha.1 · abc1234` + `api 0.1.0-alpha.1 · abc1234`. `userSelect:'all'` so a single click selects the whole identifier for support tickets. | `services/dashboard/src/components/AppShell.jsx:219` |
| **Settings → License page** | Full claim block: Service / Version / Commit / Env alongside license claims (customer_id, expires_at, max_orgs). | `services/dashboard/src/pages/settings/License.jsx` |
| **`GET /v1/version`** | Canonical machine-readable source. Drives the two above via React Query key `['api-version']`. | `services/api/internal/api/handler.go` |
| **`LicenseBanner`** | **License state only** (in-grace countdown, expired call-to-action). Never the version. | `services/dashboard/src/components/LicenseBanner.jsx` |

---

## How to cut a release

1. **Decide the version.** Check the latest release tag (`git tag --sort=-v:refname -l '[0-9]*.[0-9]*.[0-9]*' | head -1`) and pick the next per the rules above. The filter excludes the legacy snapshot tags (`_backup_pre_split_*`, `before-removing-kinde`, `dind`, `docker-socket`) that predate this convention.
2. **Update the CHANGELOG on `develop`.** Move entries from `## [Unreleased]` into a new section headed `## [X.Y.Z] — YYYY-MM-DD`. Commit on `develop` with `chore(release): X.Y.Z`. (CHANGELOG bootstrapping is a follow-up — see [§Open follow-ups](#open-follow-ups).)
3. **Promote `develop` → `main`** via the flow in [§Release promotion](#release-promotion-develop--main). Wait for the MR to merge.
4. **Tag `main`** at the merge commit:
   ```bash
   git checkout main && git pull
   git tag -a 0.1.0-alpha.1 -m "0.1.0-alpha.1"
   git push origin 0.1.0-alpha.1
   ```
   Annotated tags only (`-a`) — lightweight tags lose author + message metadata.
5. **CI does the rest.** A tag pipeline is gated to always run (`.gitlab-ci.yml:34`); `APP_VERSION=$CI_COMMIT_TAG` flows into all four service images and surfaces per [§Where the version surfaces](#where-the-version-surfaces). `deploy:staging` auto-fires from the `main`-branch pipeline that just landed; `deploy:production` waits for a manual click.
6. **Verify** post-pipeline:
   ```bash
   curl -s https://axiaops-<env>.example.com/v1/version | jq .version
   # expect "0.1.0-alpha.1"
   ```

### Hotfixes

For a fix on an already-released line: branch off the tag, fix, tag `0.Y.(Z+1)` from that branch, merge back to `main`. We don't have a `release/0.Y` branch convention yet — add one when the first hotfix actually needs it, not before.

### What NOT to do

- **Don't retag.** Tags are immutable contracts with CI and the LicenseBanner. If a tag is wrong, cut the next one (`0.1.0-alpha.2`) and note the skip in the CHANGELOG.
- **Don't tag from `develop` or feature branches.** Only `main` (see [§Release promotion](#release-promotion-develop--main)). The CI rule `.gitlab-ci.yml:34` (`- if: '$CI_COMMIT_TAG'`) has no branch constraint, so a tag on `develop` or a feature branch *will* still build and publish images to the registry — but `deploy:production` gates on `$CI_COMMIT_BRANCH == "main"` (`:738`), so the resulting images sit in the registry with a release-shaped tag while being unpromotable. That's a confusing artefact, not a working release.
- **Don't tag with `v` prefix.** The CI substitution doesn't strip it; you'd get `APP_VERSION=v0.1.0-alpha.1` instead of `0.1.0-alpha.1`.
- **Don't skip the suffix on pre-release cuts.** A bare `0.1.0` carries an implicit promise — "we'd ship this." Use `-alpha.N` until that's true.

---

## Open follow-ups

- **CHANGELOG.md** — not yet bootstrapped. Convention will be [Keep a Changelog 1.1.0](https://keepachangelog.com/en/1.1.0/) (Added / Changed / Fixed / Removed / Security under each version). First entry should retroactively reference Phase 1 MVP (April 2026) as the `0.0.x` history-before-tagging baseline.
- **`docs/USER_STORIES_STATUS.md` cross-link** — when a version ships, mark the relevant user stories with the tag they landed in.
- **Production deploy + tag pipeline interaction** — `deploy:production` is a manual gate (per memory: all `deploy:*` jobs are operator-clicked). Confirm tag pipelines surface the manual gate without auto-promoting to prod.
