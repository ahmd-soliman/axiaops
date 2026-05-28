# Native invitations — manual test runbook

Operator-facing probe pack for the native invitation flow end-to-end. Mirrors the structure of `docs/sso-local-keycloak.md` so the
two runbooks fit together as the pre-merge checklist for `feat/sso → develop`.

Each probe is independently runnable and atomic. Tick the boxes as you go,
record anything weird in **Notes:**, and log failures in the **Bugs found**
table at the bottom. Run probes 1 → 12 in order — later probes assume the
state set up by earlier ones.

For the invitation-flow design rationale see
[`docs/invitation-flow.md`](invitation-flow.md). For the surrounding native-
auth bootstrap see [`docs/native-auth-bootstrap.md`](native-auth-bootstrap.md).

## 1. Prerequisites

- A running stack with `AUTH_PROVIDER=native` (or `both`). For local: `make
  start-staging` — http://localhost:8082. For preview env:
  https://axiaops-preview.example.com.
- A bootstrapped owner you can sign in as. See native-auth-bootstrap.md if
  none exists yet.
- `INVITATION_TTL_DAYS` left at its default (14) for happy-path probes; you
  can override per-probe via SQL (probe 6) when you need an expired token.
- DB shell access for state checks. The preview env uses
  `axiaops-postgres-preview`; local uses `axiaops-postgres`. Adjust the
  container name in the SQL snippets accordingly.

## 2. Probe 1 — Happy path: new user

Pre-state: signed in as the bootstrapped owner.

- [ ] Navigate to **Settings → Team**.
- [ ] In **Invite a teammate**, enter `bob@example.com`, role `member`,
      submit.
- [ ] **Inline panel appears below the form**, green-tinted, showing:
      "Invitation created for bob@example.com (member)" with the
      redemption URL in a monospace input and a **Copy link** button.
      Pinned by commit `1f38731`.
- [ ] Click **Copy link** → button changes to "Copied!" for ~1.5s.
- [ ] DB check:
      ```sql
      SELECT email, role, expires_at IS NOT NULL AS has_expiry,
             invite_token_hash IS NOT NULL AS has_hash
      FROM axiaops.pending_memberships;
      ```
      One row, both booleans true, expiry ~14 days out.
- [ ] Open the redemption URL in a **private/incognito** window.
- [ ] AcceptInviteScreen auto-calls `/v1/auth/invitations/preview` and
      morphs into the **"Accept your invitation"** form (NOT "Welcome
      back"). Org name visible. Form disabled until the preview lands.
- [ ] Fill name `Bob`, password ≥ 12 chars. Submit.
- [ ] Browser lands on `/` as bob. `/v1/me` returns
      `{role: "member", auth_mode: "password", email: "bob@example.com"}`.
- [ ] DB:
      ```sql
      SELECT u.email, m.role, m.provisioned_via
      FROM axiaops.users u
      JOIN axiaops.memberships m ON m.user_id = u.id
      WHERE u.email = 'bob@example.com';
      ```
      One row, role=member, provisioned_via='invitation'.
- [ ] `pending_memberships` row is gone.
- [ ] Audit log row: `invitation.redeemed`.
- [ ] Cookie `axiaops_session` is set on the dashboard host.
- Notes:

## 3. Probe 2 — Single-use: token can't be redeemed twice

Pre-state: probe 1 completed. The redemption URL from probe 1 is consumed.

- [ ] In a **second** private window, paste the same URL.
- [ ] Preview returns **410** with body `{code: "invitation_invalid"}`.
- [ ] UI shows "This invitation link is invalid, expired, or has already
      been used. Ask for a fresh invite."
- [ ] No session cookie set, no DB writes (re-run the query from probe 1
      to confirm bob's row is unchanged).
- Notes:

## 4. Probe 3 — Weak password rejected (new-user flow)

- [ ] As owner, invite `weak@example.com`, role member.
- [ ] Open the URL in incognito → form loads.
- [ ] Type name + a **6-character** password. Submit.
- [ ] Frontend pre-validation rejects with "Password must be at least 12
      characters."
- [ ] Bypass the frontend (DevTools → modify the input attribute or
      submit via the API directly) → server returns 400 `weak_password`
      with the policy text. UI surfaces the message.
- [ ] Token is **NOT** consumed (DB: `pending_memberships` row still
      present). Retry with a 12+ char password → succeeds.
- Notes:

## 5. Probe 4 — Existing user (cross-org B1.5 flow)

Goal: prove the existing-user redemption branch verifies the user's existing
password and adds a *second* membership rather than overwriting their data.

Pre-state: at least two organisations exist. Easiest setup: bootstrap-then-
truncate-only-organizations to spin a second org, OR delete just enough to
re-bootstrap. Or: if you already have multi-org, skip ahead.

- [ ] Confirm `alice@example.com` exists as a known-good user in **org A**
      with a known password. Note her user_id.
- [ ] **Sign in as the owner of org B** (different org from alice's).
- [ ] Settings → Team → Invite `alice@example.com`, role member.
- [ ] Copy URL. Open in incognito.
- [ ] Preview returns `existing_user: true`, `existing_user_name: "Alice"`.
      UI renders the **"Welcome back, Alice"** form with a single password
      input (no name field).
- [ ] Type alice's **wrong** password → 401 `invalid_credentials`. UI
      shows "That password doesn't match your existing account."
- [ ] Type alice's **correct** password → land on `/` as alice.
- [ ] `/v1/me.organization_id` matches **org B** (not A).
- [ ] DB:
      ```sql
      SELECT m.organization_id, m.role, m.provisioned_via, o.name
      FROM axiaops.memberships m
      JOIN axiaops.organizations o ON o.id = m.organization_id
      WHERE m.user_id = '<alice_uid>';
      ```
      Two rows. Org A row unchanged; org B row role=member,
      provisioned_via='invitation'.
- [ ] `users` row for alice unchanged — name and password_hash are
      identical to before.
- [ ] Org switcher in the dashboard nav now lists both orgs.
- Notes:

## 6. Probe 5 — Revoke before redeem

- [ ] As owner, invite `revoke-me@example.com`. Copy URL but do **NOT**
      redeem.
- [ ] Settings → Team → Pending invitations → click **Revoke** next to
      this invite.
- [ ] DB: row gone (or marked revoked, depending on schema):
      ```sql
      SELECT * FROM axiaops.pending_memberships
      WHERE email = 'revoke-me@example.com';
      ```
- [ ] Open the URL in incognito → 410 `invitation_invalid`. Generic
      "invalid/expired/used" UI (no oracle revealing it was revoked vs
      expired vs unknown).
- Notes:

## 7. Probe 6 — Expiry

- [ ] As owner, invite `expired@example.com`. Copy URL.
- [ ] Fast-forward expiry via SQL:
      ```sql
      UPDATE axiaops.pending_memberships
      SET expires_at = NOW() - INTERVAL '1 hour'
      WHERE email = 'expired@example.com';
      ```
- [ ] Open the URL → 410 `invitation_invalid`. Same UI as revoked / used.
- Notes:

## 8. Probe 7 — Rate limiting on preview (per-IP)

The preview endpoint reveals (a) whether a token is valid and (b) whether
the invited email maps to an existing user. Per-IP cap defends against
mass token enumeration.

- [ ] Loop 11 garbage-token previews from one IP within a minute:
      ```bash
      for i in $(seq 1 11); do
        curl -s -o /dev/null -w "%{http_code}\n" \
          -X POST https://<host>/api/v1/auth/invitations/preview \
          -H 'content-type: application/json' \
          -d '{"token":"'"$(openssl rand -hex 32)"'"}'
      done
      ```
- [ ] First 10 → `410`. The 11th (or whichever exceeds the cap) → `429`
      with `Retry-After` header set to a positive integer.
- [ ] After waiting `Retry-After` seconds, the next call → 410 (rate
      limit released).
- Notes:

## 9. Probe 8 — Rate limiting on redeem (per-IP + per-email)

The redeem endpoint is gated by both per-IP and per-email caps. Per-email
defends against IP-rotation; per-IP defends against email-rotation.

- [ ] As owner, invite `ratelimit@example.com` and grab the URL (or use a
      fresh existing-user URL).
- [ ] In a script, attempt redeem **>5 times with the WRONG password**
      against the existing-user flow:
      ```bash
      for i in $(seq 1 11); do
        curl -s -o /dev/null -w "%{http_code}\n" \
          -X POST https://<host>/api/v1/auth/invitations/redeem \
          -H 'content-type: application/json' \
          -d '{"token":"<plaintext_token>","password":"wrong-pw-'"$i"'"}'
      done
      ```
- [ ] First N → `401 invalid_credentials`. Subsequent → `429` with
      `Retry-After`.
- [ ] After waiting `Retry-After`, a CORRECT password → succeeds. Rate
      limit released.
- Notes:

## 10. Probe 9 — Race: email_taken (optional, hard to reproduce)

Two simultaneous redemption attempts where a global user with the same
email is created in the gap between peek and redeem. Second redeem returns
409 `email_taken`. Hard to reproduce naturally; skip unless you want to
engineer it (suspend the API mid-redeem with a debugger and race a second
request through).

- [ ] (Optional) Confirm `redemption_email_taken` shape: 409 + body
      `{code: "email_taken"}`. UI surfaces "Another account with this
      email was just created. Please refresh the page and try again."
- Notes:

## 11. Probe 10 — Token tampering / malformed input

- [ ] **Truncated by 1 char**: paste `?token=<plaintext minus last char>`
      → 410.
- [ ] **Whitespace inside**: paste `?token=<first half> <second half>`
      → 410. Outer whitespace is trimmed; inner is not.
- [ ] **Empty token POST**: `{"token":"","password":"..."}` →
      400 `bad_request`.
- [ ] **Garbage non-hex**: `{"token":"!!!not-a-token!!!"}` → 410.
- [ ] **Wrong content-type** (e.g. `text/plain`): server rejects 400.
- Notes:

## 12. Probe 11 — SSO-enforcement interaction (KNOWN AWKWARD)

Pins the orthogonal-but-confusing state: when an org has SSO
`enforcement=required`, a native invitation can still be issued and
redeemed, the user gets a session cookie — but every authenticated request
returns 403 `sso_required`. The user is in a dead state.

- [ ] In an org with at least one SSO connection, set Settings → SSO →
      Enforcement = `required`.
- [ ] As owner, invite `native-victim@example.com`. Redeem in incognito
      with a strong password.
- [ ] Form succeeds, dashboard mounts briefly, then **every API call
      returns 403 `sso_required`**. UI bounces the user back to /login
      with an error.
- [ ] Status: documented as expected behaviour, not a bug. Tracked as a
      potential hardening: server-side guard on `/v1/invitations` that
      rejects with `400 sso_required_for_org` when the target org has
      `enforcement=required`. Not a merge blocker.
- [ ] Reset enforcement to `optional` before continuing.
- Notes:

## 13. Probe 12 — Redemption URL panel UX (regression pin)

Pins the fix in commit `1f38731`: the API has always returned
`redemption_url` in the response body, but the dashboard's `addMutation
.onSuccess` previously discarded it, leaving admins with no way to share
the invite under self-hosted (no SMTP).

- [ ] **API response**: in DevTools → Network, the POST `/v1/invitations`
      response body must contain a `redemption_url` field with the full
      `https://.../accept-invite?token=<64-hex>` URL.
- [ ] **Dashboard panel**: a green-tinted box appears below the Invite
      form within ~250ms of submit, showing the URL in a read-only
      monospace input.
- [ ] **Auto-select on focus**: clicking into the input auto-selects the
      full URL — keyboard users can `Cmd-C` / `Ctrl-C` immediately.
- [ ] **Copy button**: clicking **Copy link** writes the URL to the
      clipboard, the button label changes to "Copied!" for ~1.5s, then
      reverts.
- [ ] **Clipboard API fallback**: under HTTP (insecure context — local
      `make start-staging`), `navigator.clipboard.writeText` rejects.
      The button does NOT crash; the user can still select and copy
      manually. Confirmed by trying the button under
      http://localhost:8082.
- [ ] **Dismiss button**: clicking the `×` clears the panel.
- [ ] **Auto-clear**: starting a new invite clears the previous panel
      and shows the new one.
- [ ] **Security warning footnote**: panel includes "Anyone with this
      link can redeem the invitation, so share over a private channel.
      Revoke from the Pending invitations table if needed."
- [ ] **addMember fallback**: when inviting an existing AxiaOps user who already has
      a `users` row but no membership in this org, the API response contains no
      `redemption_url` — the panel does NOT render (gated on `data?.redemption_url`).
- Notes:

## 14. Bugs found

| # | Severity | Probe | Description | Fix commit |
|---|---|---|---|---|
|   |   |   |   |   |

## 15. Decision

- [ ] All probes green → invitation flow ready to merge with
      feat/sso → develop
- [ ] Bugs found, fixed in `<commits>`, re-tested → safe to merge
- [ ] Bugs found, deferred → describe risk and gate the merge

## 16. Test report template

Copy this block into a fresh document or a comment on the relevant MR /
issue, fill it in as you go, and save it as evidence the round-trip held.

```markdown
# Native invitation flow — manual test — <YYYY-MM-DD>

- **Tester**: <your name>
- **AxiaOps SHA**: $(git rev-parse --short HEAD) on <branch>
- **Stack**: <preview | local make start-staging | other>
- **AUTH_PROVIDER**: <native | both>
- **INVITATION_TTL_DAYS**: <value or "default 14">

## Probes

| # | Topic | Result |
|---|---|---|
| 1 | Happy path: new user | pass / fail |
| 2 | Single-use | pass / fail |
| 3 | Weak password rejected | pass / fail |
| 4 | Existing user (cross-org) | pass / fail / skip-no-multi-org |
| 5 | Revoke before redeem | pass / fail |
| 6 | Expiry | pass / fail |
| 7 | Rate limit (preview) | pass / fail |
| 8 | Rate limit (redeem) | pass / fail |
| 9 | email_taken race | pass / fail / skip |
| 10 | Token tampering | pass / fail |
| 11 | SSO-enforcement interaction | pass-known-awkward / fail / skip-no-sso |
| 12 | Redemption URL panel UX | pass / fail |

## Bugs found
<copy from §14>

## Decision
<copy from §15>
```
