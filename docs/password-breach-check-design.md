# Password Breach-Corpus Screening — Design (Tasks.md 2.7.11)

**Status:** designed, not yet implemented.
**Supersedes:** the live-API sketch in Tasks.md 2.7.11 and plan §4.5 D6 ("top-1000 list").
**Decision owner:** auth/native-auth path. Architect-reviewed.

## Why this exists

The B1 native-auth password policy is **length-only (≥12 chars)** today
(`services/api/internal/auth/password.go` → `CheckPolicy`). That is the one
outright **NIST SP 800-63B §5.1.1.2** violation in our password story — the
standard requires screening new passwords against a known-compromised corpus.

## The decision: embed an offline HIBP subset, do NOT call the live API

The original plan (and Tasks.md 2.7.11) called the live HaveIBeenPwned
k-anonymity API (`api.pwnedpasswords.com/range/`) at signup. **We rejected
that** for one reason: **AxiaOps is self-hosted.** A customer instance may be
egress-restricted or air-gapped, so a live external call is unreliable or
unavailable *by design*, and the "soft warning / fail-open" degradation
silently becomes "does nothing."

Prior art settles it: **GitLab** (also self-hosted) ships a bundled offline
weak-password list and hard-blocks; **Django** bundles ~20k common passwords;
**Entra/Okta** maintain their own server-side corpus. We follow the
GitLab/Django model but with far better coverage: **embed the top-N
most-prevalent SHA-1 hashes from HIBP's downloadable Pwned Passwords corpus**
as a Go `embed` asset, mirroring how `services/shared/license/` embeds its
fixture.

Consequences of going offline:
- **Hard-block on a corpus hit** (proper NIST compliance) — no soft warning.
- **No availability branch.** The check is deterministic and always runs;
  there is no fail-open/fail-closed question because there is no network call.
- **Works in air-gapped installs** — the whole reason for the pivot.

## Licence handling (embedded data)

We are embedding HIBP's data into our shipped binary, so the licence matters.

- **HIBP Pwned Passwords data:** released under **Creative Commons Attribution
  4.0 International (CC BY 4.0)**; HIBP's own guidance is that attribution is
  *welcomed* and you "must identify Have I Been Pwned as the source of the
  data." Conservative posture: **treat it as CC BY 4.0 and attribute
  unconditionally** (attribution is cheap and removes all doubt).
- **Action:** ship a `NOTICE` (or `THIRD_PARTY_NOTICES.md`) at the repo root
  crediting HIBP / Troy Hunt as the source of the embedded corpus, with the
  CC BY 4.0 reference and the corpus version/date. **The repo has no
  third-party-notice convention today — this task establishes it.** Any future
  embedded third-party asset/code appends here.
- **The downloader tool** (`HaveIBeenPwned/PwnedPasswordsDownloader`,
  separately licensed) is used only to *generate* the asset — its code is
  **not** embedded, so its licence does not propagate into our binary. Only
  the data is embedded.
- CC BY 4.0 permits commercial redistribution; no copyleft, no source-
  disclosure obligation. Compatible with shipping in a commercial self-hosted
  product.

## Design

### Where the check is wired

`auth.CheckPolicy(candidate)` in `services/api/internal/auth/password.go` is
already the **single seam every password-set path funnels through** — its own
doc comment defers breach screening to this task. All set-password call sites
go through it (verify-only paths like `/auth/login`, `/select-org`,
`/switch-org`, and the existing-user invite branch correctly do NOT screen —
they check an existing hash, they don't set one):

| Call site | File | Path |
|---|---|---|
| Bootstrap (first-owner) | `internal/auth/handler.go` | `POST /v1/auth/bootstrap` |
| Invitation redeem (new user) | `internal/auth/handler.go` | `POST /v1/auth/invitations/redeem` |
| Password-reset redeem | `internal/auth/handler.go` | `POST /v1/auth/password-reset/redeem` |
| Staff create | `internal/staff/admin_handler.go` | `POST /admin/staff` |
| `seed-staff` CLI | `cmd/api-admin/seed.go` | first-superadmin |
| `hash-password` CLI | `cmd/hash-password/main.go` | operator tooling |

So this is "thread a corpus lookup into the one validator," not wiring sprawl.

### Data structure and N

Embed **top-1,000,000** prevalence-ordered SHA-1 digests as a **sorted raw
20-byte-per-record binary blob**, queried by **binary search** over the
embedded `[]byte`. Rationale:

- Sorted-binary-blob beats a generated Go map (compile/binary bloat at 1M+)
  and an in-memory set/bloom (needless boot cost; bloom false-positives would
  block a *valid* password). The bytes are already resident in the binary
  image — `sort.Search` over `data[i*20:(i+1)*20]` needs **zero startup
  deserialization**.
- **N = 1,000,000 → 20 MB** embedded asset. HIBP is prevalence-ordered, so the
  top 1M covers the overwhelming majority of real-world breach hits. Top-100k
  (2 MB) misses the mid-tail; top-10M (200 MB) is past diminishing returns and
  bloats every image pull. **Fallback knob: top-500k (10 MB)** — same code,
  smaller N at generation time, if the 20 MB image bump proves heavy.
- **The blob lands in TWO production images, not one.** `internal/auth` is
  linked by both the `api` binary (`cmd/`, `Dockerfile`) and the `api-admin`
  staff-plane binary (`cmd/api-admin`, `Dockerfile.admin`) — both call
  `CheckPolicy`/`Hash`. ECS pulls them independently, so the size bump is paid
  twice. (`cmd/hash-password` links it too, but that's operator tooling, not a
  pulled service image.) Re-run the `make build-production` size diff against
  **both** `./cmd/` and `./cmd/api-admin/`; the 500k fallback gets more
  attractive once the bump is counted twice.

### New package `services/api/internal/breachlist/`

**Placement:** `services/api/internal/breachlist`, NOT `services/shared`. The
only callers live under `services/api` (the `auth` package). Keeping it out of
`shared` makes the ingestion-side exclusion **structural** — ingestion's import
graph never references it, so the 20 MB blob is simply never in ingestion's
link set, rather than relying on dead-code elimination to drop it (see Risks).

- `breachlist.go` — `func IsCompromised(plaintext string) bool`: `crypto/sha1`
  the candidate **(lookup-only — NEVER storage; storage stays argon2id)**,
  binary-search the embedded blob. **Raw-byte invariant:** the blob and the
  lookup both operate on **raw 20-byte digests** (`sha1.Sum` output /
  `hex.Decode` of HIBP's uppercase-hex line) — NEVER on hex strings. Raw-byte
  comparison is exactly what makes the check case-agnostic; an "optimization"
  to compare hex strings (or a forgotten `ToUpper`/`ToLower`) silently turns
  the check into an always-miss with no error. Package doc must state this and
  the SHA-1-is-index-not-storage caveat loudly.
  Add a scoped `//nolint:gosec // G401/G505: SHA-1 is the HIBP corpus index,
  not a security primitive; storage is argon2id` at the import + `sha1.Sum`
  site, so a future gosec CI rollout doesn't trip on the blocklisted import.
- `embed.go` — `//go:embed pwned-top1m.bin` `var corpus []byte`. **Unconditional**
  — unlike the license dev-fixture (which is *stripped* from `-tags production`
  for a security boundary), the corpus must be present in *every* shipped
  build because production binaries are exactly the ones doing real signups.
  No `embed_{dev,production}.go` split. Do not add a build tag to make it
  optional — an operator who builds it out is silently NIST-non-compliant.
- `breachlist_test.go` — **end-to-end known vector** (`IsCompromised("password")
  == true`, asserting against the hex-decode of
  `5BAA61E4C9B93F3F0682250B6CF8331B7EE68FD8`) so an encoding/case regression
  fails loudly; random digest → false; `len(blob) % 20 == 0` (corruption
  guard); blob is sorted (binary-search invariant guard).

### Password-policy change — add a function, don't mutate the signature

`auth.CheckPolicy(candidate string)` (`password.go`) is called from 6 prod
sites **plus 2 existing unit tests** (`password_test.go`). Changing its
signature to take a required `PolicyContext` is a compile-break across 8 sites
and rewrites passing tests for no gain. Instead:

- Keep `CheckPolicy(candidate string)` unchanged — it now also runs the breach
  lookup (`breachlist.IsCompromised`) after the length check. The 2 CLIs, the
  reset-redeem path, and the existing tests keep calling it as-is.
- Add `CheckPolicyWithIdentity(candidate string, identity PolicyContext)` —
  `PolicyContext{ Email, Name string }` — that runs everything `CheckPolicy`
  does **plus** the GitLab-style identity-similarity check. Only the 4 HTTP
  sites that have email/name in scope (bootstrap, invite-redeem, staff-create,
  and reset-redeem if we thread it later) call this. Blast radius: 4 sites, no
  test churn.
- Add sentinels `ErrPasswordBreached`, `ErrPasswordContainsIdentity`.
- Order inside each: **length → breach** (`CheckPolicy`); identity-similarity
  slots before breach in the `WithIdentity` variant. Length stays first so a
  4-char password reports "too short," not "breached."
- **GitLab-style similarity add-on** (~15 lines): reject when the candidate,
  case-folded, equals/contains the user's email local-part, full email, or
  display name. Catches `JaneDoe2026!`-class passwords the corpus never holds.
  Reset-redeem can stay on plain `CheckPolicy` for v1 (breach + length still
  fire); threading email/name there is a deferred refinement.

### Error mapping

All sites already map `CheckPolicy` errors to a `weak_password` 400 with
`err.Error()` as the message. New sentinels flow through unchanged — no new
HTTP shapes, no frontend contract change beyond message text.
`AcceptInviteScreen` / bootstrap / reset forms already render `weak_password`
verbatim.

### Generator + provenance

`scripts/gen-breachlist.sh` (+ optional `services/api/cmd/breachlist-gen/`):

1. Download HIBP's prevalence-ordered SHA-1 file (via PwnedPasswordsDownloader
   ordered mode, or the published torrent).
2. Take the first N lines (already prevalence-sorted); strip the `:count`
   suffix; keep the 40-hex hash (HIBP emits **uppercase** hex).
3. Hex-decode → **raw 20 bytes** (case-insensitive decode — this is what makes
   the asset match `sha1.Sum` output at lookup); **re-sort ascending by digest**
   (binary search needs digest order, not prevalence order); write
   `pwned-top1m.bin` as concatenated raw 20-byte records, no delimiters.
4. Commit the `.bin`. The script appends a provenance manifest (source URL,
   HIBP corpus version/date, N, SHA-256 of output) to `docs/breachlist-provenance.md`,
   and the `NOTICE` attribution is updated. Modeled on the
   `docs/license-issuance.md` ceremony. Revisit trigger: regenerate each minor
   release or when HIBP publishes a major corpus update.

## Risks / notes

- **SHA-1 is corpus-index only** — we hash solely to find the candidate in
  HIBP's SHA-1-keyed list. Storage is argon2id, unchanged.
- **No-normalization invariant:** `IsCompromised` MUST hash the exact bytes
  (`[]byte(candidate)`) that `auth.Hash` later stores — no trimming, no Unicode
  normalization, in either path. They are coupled: if anyone ever adds
  `TrimSpace`/NFC to one path and not the other, the breach check screens a
  different string than the one stored. Both pass raw UTF-8 bytes today; keep it
  that way.
- **DoS / input length:** non-issue — `httpjson.Decode` caps request bodies at
  64 KiB, so the worst-case input to both SHA-1 and the pre-existing argon2id
  hash is ~64 KB (SHA-1 of that is microseconds; argon2id's cost already exists
  and is unchanged). No separate length guard needed. The CLI paths have no body
  cap but are operator-local/non-adversarial.
- **Timing:** non-issue — set-time only, the secret is the user's own brand-new
  password, no cross-user oracle. Document, don't mitigate.
- **Linker / ingestion exclusion:** structural, not a DCE gamble — `breachlist`
  lives in `services/api/internal`, which ingestion's import graph never
  references, so the blob is never in ingestion's link set. Confirm with a
  `go build` size diff, but it's a confirmation, not a load-bearing assumption.
  (The risk would only appear if `breachlist` got pulled into a widely-imported
  package like `model`/`storage` — don't.)
- **False positives:** zero by construction (exact-digest membership, no bloom).
- **Staleness:** the offline list ages between regenerations — inherent and
  acceptable (GitLab/Django ship static lists too).

## Effort: ½ week (≈2.5–3.5 days)

| Item | Est |
|---|---|
| `breachlist` package (lookup + embed + invariant tests incl. known vector) | 0.5d |
| Generator script + `docs/breachlist-provenance.md` + `NOTICE` + first corpus commit | 0.5d–1d |
| `CheckPolicy` breach lookup + `CheckPolicyWithIdentity` + sentinels + similarity | 0.5d |
| Wire the 4 identity-bearing sites to `WithIdentity` + per-site tests | 0.5d |
| Handler/CLI tests (mandated: `"password"` and a 12-char breached string MUST 400; a `crypto/rand` 24-char string MUST pass) + `make build-production` size check on `./cmd/` **and** `./cmd/api-admin/` | 0.5d |

Honest range is **2.5–3.5 days** — the generator/provenance ceremony
(torrent download → top-N filter → sort → manifest) is the line most likely to
slip past 0.5d. The additive-function approach (no `CheckPolicy` signature
break) keeps the call-site churn to 4 sites with zero existing-test rewrites.
The offline approach trades network/fail-open complexity (avoided entirely) for
this one-time generate-and-embed cost.

## Explicitly deferred

- Live HIBP k-anonymity fallback for egress-capable installs (settled out).
- Re-screening existing stored passwords at login (separate, larger feature).
- Configurable N / operator-supplied custom blocklist.
- Similarity check on the reset path (zero `PolicyContext` is fine for v1).

## Sources

- [HIBP Pwned Passwords](https://haveibeenpwned.com/Passwords)
- [HIBP API v3 docs](https://haveibeenpwned.com/API/v3)
- [PwnedPasswordsDownloader (offline corpus tool)](https://github.com/HaveIBeenPwned/PwnedPasswordsDownloader)
- NIST SP 800-63B §5.1.1.2 (compromised-credential screening)
