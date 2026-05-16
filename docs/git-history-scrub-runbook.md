# Git history scrub — `Co-Authored-By: Claude` removal

Runbook for the one-off operation that strips `Co-Authored-By: Claude …`
trailer lines from every commit message in the repo without touching any
file content. Originally executed on 2026-05-07; this doc captures the
exact procedure so it can be repeated for any future attribution lines
that need to come out of the visible history.

---

## When to run this

- A reviewer external to the team is about to onboard, and the AI
  attribution lines aren't useful context.
- Compliance / customer requests "no third-party AI attribution in
  source-of-truth history."
- A new attribution string starts appearing (model rename, vendor swap,
  etc.) and you want the historical record to match the current shape.

This is a **history-rewrite operation**. Anyone with an in-flight clone
of `develop` will need to re-sync. Do NOT run this lightly.

## Pre-conditions

- `git-filter-repo` installed: `brew install git-filter-repo`
  (or your platform's equivalent — it's a standalone Python script).
- Clean working tree on the local repo: `git status --short` empty.
- No open MRs you authored that target `develop` from other feature
  branches *and* whose review threads are anchored to specific commit
  SHAs. (After the rewrite, those SHAs are gone — the threads orphan.)
- Authorisation to force-push `develop`. Check the GitLab project's
  protected-branches settings: `develop` MUST allow force-push for the
  push step. If it doesn't, temporarily relax the protection in
  Settings → Repository → Protected branches, then re-tighten after.

## Pattern matched

The only lines removed match this regex (case-insensitive):

```regex
^\s*Co-Authored-By:\s*Claude\b
```

The `\b` after `Claude` is a word-boundary — `Claude Haiku 4.5`,
`Claude Sonnet 4.6`, `Claude Opus 4.7` all match; a hypothetical
`ClaudeBot` would NOT match the same line via this regex. Adjust the
pattern if you need to scrub a different attribution string.

Tree hashes (file content) are **unchanged** by this operation. Only
commit messages are rewritten — and along with that, every commit's
SHA, since the SHA is a hash over `tree + parents + author + committer
+ message`.

---

## Procedure

### 1. Snapshot the current `develop` to a backup branch

The whole point of this step is "if something goes wrong I can recover
the pre-rewrite history with one command." Push the backup to origin so
even a catastrophic local-repo loss doesn't destroy the pre-image.

```bash
DATE=$(date +%Y-%m-%d)
git fetch origin --prune
git branch backup/develop-pre-claude-scrub-${DATE} origin/develop
git push -u origin backup/develop-pre-claude-scrub-${DATE}
```

The backup branch is read-only by convention; do not delete it for at
least 30 days after the rewrite, longer if any active contributors are
on a long-lived feature branch cut from pre-rewrite develop.

### 2. Clone to a separate working directory

Running `git filter-repo` on your daily-driver repo makes recovery
fiddly if you change your mind mid-procedure. A fresh clone keeps the
rewrite isolated.

```bash
TMPDIR=/tmp/axiaops-scrub-$$
git clone --no-local file:///Users/ahmed/Developer/repo/axiaops "$TMPDIR"
cd "$TMPDIR"

# Re-point origin to the real remote so the eventual push goes to GitLab
# rather than the local file:// source.
git remote remove origin
git remote add origin git@gitlab.com:axiaops/axiaops.git
```

`--no-local` forces a real clone (with packfiles) instead of a hard-link
clone — important so the rewrite operates on its own object store.

### 3. Run the rewrite

```bash
git filter-repo --force --message-callback '
import re
text = message.decode("utf-8", errors="replace")
lines = text.split("\n")
out = []
for line in lines:
    if re.match(r"^\s*Co-Authored-By:\s*Claude\b", line, re.IGNORECASE):
        # If removing this trailer leaves a double-blank above it,
        # drop the preceding blank too.
        if out and out[-1].strip() == "" and (len(out) < 2 or out[-2].strip() == ""):
            out.pop()
        continue
    out.append(line)
# Collapse any trailing double-blanks the rewrite may have left behind.
while len(out) >= 2 and out[-1].strip() == "" and out[-2].strip() == "":
    out.pop()
return "\n".join(out).encode("utf-8")
'
```

`--force` is required because `git-filter-repo` refuses to operate on a
non-pristine clone by default — and once we re-pointed origin in step 2
the clone is "non-pristine" by its definition. The 1089-commit history
took 0.12 seconds when this ran on 2026-05-07.

`--message-callback` receives every commit's message as `bytes` and
returns rewritten `bytes`. The body is implicitly wrapped in
`def callback(message):` and indented by `git-filter-repo`, so you
supply ONLY the function body.

### 4. Verify

Three checks. All must pass before the push.

```bash
# (a) Zero remaining attribution lines
git log --grep="Co-Authored-By: Claude" --oneline | wc -l
# expect 0

# (b) Tree hash unchanged — file content is byte-for-byte identical
git rev-parse HEAD^{tree}
# compare against the original (run from your daily-driver repo):
#   git rev-parse origin/develop^{tree}
# the two hashes MUST match

# (c) Eyeball the most recent few messages
git log -5 --format="--- %H ---%n%B"
```

If (a) is non-zero, the regex missed a variant — investigate
(e.g. a typo in the original line, a different model name, a
non-standard separator). If (b) differs, **abort the push** — the
rewrite touched something it shouldn't have.

### 5. Force-push to origin

```bash
git push --force-with-lease origin develop
```

`--force-with-lease` is preferred over `--force` because it refuses the
push if anyone else moved the remote `develop` between your `git fetch`
in step 1 and now. This is the safety net that catches "someone else
merged something during the rewrite procedure."

### 6. Re-sync the daily-driver repo

```bash
cd /Users/ahmed/Developer/repo/axiaops
git fetch origin
git checkout develop
git reset --hard origin/develop
```

Any other local clones (CI runners, other contributors) need the same
`fetch + reset --hard origin/develop`. CI doesn't usually need anything
because new pipelines run on the new SHAs.

### 7. Tell collaborators

Anyone with a local `develop` clone or a feature branch cut from
pre-rewrite develop needs:

```bash
git fetch origin --prune
git checkout develop && git reset --hard origin/develop

# For each feature branch cut from old develop:
git checkout my-feature
git rebase --onto origin/develop <old-develop-tip-sha> my-feature
# OR, if the branch hasn't diverged much:
git rebase origin/develop
```

Open MRs whose source branch has been rebased need a `git push --force-with-lease`
on the source branch. GitLab MR history (review threads, approvals,
comments) is preserved across force-push of the source — only the diff
re-renders against the new commits.

---

## Rollback

If something goes wrong AFTER the force-push (rare but possible — e.g.
you discover the regex was too aggressive and ate a legitimate trailer):

```bash
# Restore develop from the backup branch
git fetch origin
git checkout develop
git reset --hard origin/backup/develop-pre-claude-scrub-${DATE}
git push --force-with-lease origin develop
```

This is exactly why step 1 is non-optional.

---

## Variations

### Scrubbing a different attribution string

Replace the regex inside the callback. Examples:

```python
# A specific co-author email
re.match(r"^\s*Co-Authored-By:[^<]*<noreply@example\.com>", line)

# Any "Generated with X" trailer line
re.match(r"^\s*🤖\s*Generated with", line)

# An entire signed-off-by trailer block (multiple lines) —
# you'd need a stateful filter; the inline callback is one-line-at-
# a-time. For multi-line patterns, write a separate
# `--message-callback-file my-callback.py` instead.
```

### Scrubbing on every push (don't)

Don't. History rewrites are a one-off operation. Use git hooks or
commit templates to keep the new attribution OUT of the message in the
first place — not to scrub it after the fact on every push.

### Scrubbing committer/author names (different ask)

`--message-callback` only rewrites messages. To rewrite *author* or
*committer* fields (the From/Date headers), use `--name-callback` and
`--email-callback` instead. Different problem, different tool flag.
