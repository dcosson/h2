---
name: babysit-pr
description: Schedule yourself to wake up every 5 minutes and shepherd an open PR — fix failing CI, address review-bot comments (push fixes for legit findings, resolve false positives, re-request review once addressed), reply to mechanical human comments with fixes, and flag architecture-level human comments back to the user. Exits the loop when the PR is green and review is clean. NEVER merges the PR.
user-invocable: true
allowed-tools: Bash Read Write Edit Grep Glob Task
argument-hint: "[pr-number-or-url] [--review-bot @name]"
---

# Babysit PR

You have an open PR that needs ongoing attention as CI runs, review bots post comments, and humans drop occasional feedback. Rather than the user (or you) actively babysitting it, you'll **self-schedule** wakeups every 5 minutes for up to ~20 fires and process whatever's new each time.

This skill ends when the PR is green and review is clean. **It NEVER merges the PR** — see the bottom of this doc.

## Prerequisites: know your review bots

Before starting the loop, identify which review bots are active on this PR and how each one is triggered for re-review. **Do not assume any bot auto-re-reviews on push.** Most do not. Look up the bot's behavior before your first tick — not mid-loop when you've already pushed a fix and need to know.

### Known bots

- **Cursor Bugbot (`@cursor`)** — Runs automatically on PR open and on `ready_for_review`. Does **not** re-run on push. To re-request review after pushing a fix: `gh pr comment <pr> --body "@cursor review"`. Always re-tag explicitly after any fix push.

- **Other bots** — Check the bot's docs or past PR comments to learn its trigger. When in doubt, re-tag explicitly — a redundant review request costs nothing; a missed review cycle costs a whole tick.

## Inputs

- `$0`: PR number or full URL (e.g. `123` or `https://github.com/org/repo/pull/123`).
- `$1` (optional): `--review-bot @name` — the review bot you'll re-tag for follow-up reviews (e.g. `@bugbot`, `@claude`, `@greptile`, `@codex`). If omitted, infer from the bots that have already commented on the PR.

## Phase 1: Set up the wakeup schedule

You'll schedule yourself to receive a wakeup message every 5 minutes, capped so it can't run forever. Get your own agent name and schedule on it:

```bash
AGENT=$(h2 whoami)

h2 schedule add "$AGENT" \
    --name "babysit-pr-<pr-number>" \
    --rrule "FREQ=MINUTELY;INTERVAL=5;COUNT=20" \
    --from babysit-pr \
    --message "babysit-pr: check PR <pr-number>"
```

- `FREQ=MINUTELY;INTERVAL=5` — every 5 minutes.
- `COUNT=20` — at most 20 firings (~1h40m of total elapsed clock time). The cap is a safety belt so a stuck PR doesn't loop forever.
- `--message` injects into your own PTY each wakeup. When you see it arrive, repeat **Phase 2** below.

If you reach the cap without converging, surface that to the user and stop. Do not re-arm.

## Phase 2: What to do on each wakeup

Each 5-minute wakeup, do **every** step in order. Do not skip steps. Even when a step has no new work, you must run the commands and show their output. The user needs to see that you checked, not just hear "nothing new." A step with no output shown is indistinguishable from a step you skipped.

### Step 2a — Check for merge conflicts

```bash
gh pr view <pr-number> --json mergeable,mergeStateStatus
```

- `"mergeable": "MERGEABLE"` → continue to 2b.
- `"mergeable": "CONFLICTING"` → the branch has fallen behind main. Fetch main, merge it into the PR branch, resolve conflicts, and push. For sops-encrypted files or other binary/generated files where both sides re-encrypted, take main's version (`git checkout --theirs <file>`) and note that re-encryption may be needed. For code conflicts, resolve normally. End turn after pushing the merge — let the next wakeup verify.
- `"mergeable": "UNKNOWN"` → GitHub is still computing. Skip and check again next wakeup.

### Step 2b — Check CI

```bash
gh pr checks <pr-number>
```

- All green → continue to 2c.
- Failing → run the failing job, read the error, **push a fix**. Most CI failures are: type errors, lint errors, broken unit tests. Fix them locally, run the local equivalent (`make lint`, `make typecheck`, `make test` — whatever the repo uses), then `git push`. End turn — let the next wakeup re-check.

**Fine-grained personal access tokens can't read the GitHub Checks API.** If `gh` is authenticated with a fine-grained PAT (the default for most workstations — `gh auth status` will show the token type), every code path that touches Checks returns 403 `Resource not accessible by personal access token`. The token grants are not the problem; the Checks API simply isn't exposed to PATs. This will not start working mid-loop. Stop retrying and switch to the fallbacks below.

Commands that **fail** under a PAT — avoid these for CI status:

- `gh pr checks <pr>` — GraphQL `statusCheckRollup` path.
- `gh pr view <pr> --json statusCheckRollup` (and any other `--json` field whose name contains `statusCheck` / `checkRuns`). The rest of the JSON still comes back, so it looks like it worked — but stderr is full of per-context 403s and the field you wanted is empty.
- `gh api repos/<org>/<repo>/commits/<sha>/check-runs` — REST Checks API.
- `gh api repos/<org>/<repo>/check-suites/...` — same family.

Commands that **work** under a PAT — use these instead:

```bash
# Actions workflow runs for the branch (covers anything running in GitHub Actions).
gh run list --branch <branch> --limit 40 \
    --json status,conclusion,name,headSha \
    --jq '[.[] | select(.headSha == "<head-sha>")] |
          group_by(.name) |
          map({name: .[0].name, status: .[0].status, conclusion: .[0].conclusion})'

# Legacy combined commit status (covers external CIs reporting via the Statuses API:
# CircleCI, Vercel, Buildkite, etc.).
gh api "repos/<org>/<repo>/commits/<head-sha>/status" \
    --jq '{state, statuses: [.statuses[] | {context, state, description}]}'

# Failing-job logs — also works under a PAT.
gh run view <run-id> --log-failed
```

Use both queries: `gh run list` for Actions, the combined-status call for external CIs. Together they reconstruct the same picture `gh pr checks` would have shown.

Everything else you need for this skill works fine under a PAT — `gh pr view --json reviews,comments,body,...` (just not the check fields), `gh pr comment`, `gh api .../pulls/<n>/comments`, `gh api .../issues/<n>/comments`, `gh run view`, `gh run list`. Only the Checks API path is blocked.

### Step 2c — Pull review comments

```bash
gh pr view <pr-number> --json reviews,comments
gh api "repos/<org>/<repo>/pulls/<pr-number>/comments"
```

Group comments by author. The relevant authors are:

- **Review bots** — `@bugbot`, `@claude` (Claude Code review), `@codex`, `@greptile`, etc. Anything that looks programmatic.
- **Humans** — everyone else.

### Step 2d — Handle review-bot comments

For each bot comment that isn't already resolved/handled:

1. **Read it carefully.** Look at the file/line, understand what the bot is claiming.
2. **Decide: false positive or legit?**
   - **False positive** (bot misunderstood, claim doesn't apply, code is actually correct as-is): reply to the comment, then resolve the review thread. **This is the only time you resolve a thread yourself.** Do not minimize the comment — minimizing hides it, which is not the same as resolving.

     Fine-grained PATs with `pull_requests:write` do not have permission to resolve threads via `resolveReviewThread`. If you get a 403, leave a reply on the comment instead. The reply must start with `Ok to resolve` on the first line, then a line break, then a short explanation of why the issue is already addressed or is a false positive. The user will resolve the thread from the GitHub UI.
   - **Legit** (bot is right, code should change): write the fix, push it. **DO NOT resolve the comment yourself.** Leave it open. The next review pass — when you re-tag the bot — is what verifies your fix actually addressed the concern. If you resolve it yourself, you're declaring "fixed" without verification.

3. **Why this matters:** resolving a comment yourself short-circuits the verification loop. The whole point of the bot review is that it independently checks your work. Push fix → request re-review → bot reads new code → bot resolves OR flags new issues. That second pass is the safety check.

### Step 2e — Handle human comments

For each human comment that isn't already addressed:

- **Mechanical / specific** (rename this var, extract this helper, add a null check, fix this typo): push the fix, reply to the comment explaining what you pushed (`gh pr comment` or `gh api` to reply inline). Don't ask permission for the kind of change a careful reader would just accept.

- **Architectural / "let's think about this"** (let's reconsider the approach, what if we did X entirely differently, this whole component should be rewritten, why are we even doing this): **do not start a rewrite**. This skill is not the place for fundamental scope changes. Reply to the comment along the lines of:

  > Waiting for user to discuss this — @<user-handle> let me know how you'd like to proceed.

  Then continue processing other comments. If this thread is the only outstanding item, you'll still end the turn and wake up later — the user may have responded by then.

### Step 2f — Re-request bot reviews

If you pushed any fixes in 2d or 2e, re-request review from every active bot. Always do this — do not assume any bot auto-re-reviews on push (see Prerequisites above).

```bash
gh pr comment <pr-number> --body "@cursor review"
# etc. for whichever bots are reviewing this PR
```

### Step 2g — End turn

Output a structured summary for this tick. For each step (2a–2f), one line: what you checked and what you found. Then a final line: what action you took (pushed a fix, flagged user, or "no action needed"). The user should be able to read your summary and know exactly what state the PR is in without clicking through to GitHub.

## Phase 3: Exit when clean

The PR is "clean" when:

- `mergeStateStatus` is NOT `BLOCKED`. Check with `gh pr view <pr> --json mergeStateStatus`. If it's `BLOCKED`, diagnose why: check for required reviews (`gh pr view <pr> --json reviewDecision`), unresolved conversations, or failing required checks. Report the specific blocker in your summary. If the blocker is something only a human can fix (e.g. required approval), say so explicitly on the first tick you detect it and keep polling. Do not repeat the same "waiting on approval" message every tick — after the first report, just confirm "still blocked on human approval" in one line.
- The PR is mergeable (no merge conflicts with the base branch).
- All CI checks pass (use `gh run list`, not `gh pr checks`).
- No outstanding bot comments are unresolved (either you resolved a false positive, or you pushed a fix AND the bot re-reviewed and found no new issues).
- No outstanding human comments are unaddressed (you replied to mechanicals after pushing, or you flagged architecturals for user discussion).

When you confirm all of the above:

```bash
# Find the schedule you registered
h2 schedule list "$(h2 whoami)"

# Remove it by ID
h2 schedule remove "$(h2 whoami)" <schedule-id>
```

Post a final summary on the PR (and to the user via `h2 send <user> "..."`) noting the PR is ready for human review/merge. Then stop. **Do not merge.**

## Hard rule: DO NOT MERGE THE PR

**Under no circumstances does this skill merge the PR — not even if every check is green, every bot has approved, every comment is resolved, and the author asks nicely.**

- Don't run `gh pr merge`.
- Don't run `gh pr ready` if it would auto-merge.
- Don't trigger any merge bot or merge-queue join.
- Don't bypass branch protection.

The merge decision belongs to a human. This skill exists to get the PR INTO a mergeable state and STOP. The final "ship it" click is someone else's responsibility, by design — it forces a deliberate human pause before code lands on the trunk.

If you find yourself about to type `gh pr merge`, stop. That's not what this skill does.

## What requires judgment

1. **False positive vs. legit (Step 2d).** Bots get this wrong sometimes — both false positives and false-cleans. Read the actual code. If the bot's claim doesn't match what the code does, mark it false positive with a one-line reason. If you're not sure, err toward "legit" — pushing a small refactor is cheaper than having a real bug slip through.

2. **Mechanical vs. architectural (Step 2e).** A "rename this variable" is mechanical. A "let's consider whether this whole abstraction is right" is architectural. When in doubt, treat it as architectural and flag for the user — the cost of waiting one extra cycle is small; the cost of starting a wrong rewrite is large.

3. **When to stop early.** If the same comment keeps coming back after you've pushed multiple fixes the bot disagrees with, stop and flag the user. You're likely in a loop where you and the bot disagree about what "fixed" means — escalate rather than ping-pong.

4. **What "re-request review" means for each bot.** See the Prerequisites section at the top for known bots. For unknown bots, always re-tag explicitly — a redundant review request is free; a missed review cycle wastes a tick.

## Anti-patterns

- **Silently skipping a step because a command failed.** If `gh pr checks` returns 403, you don't get to skip CI checking. You try `gh run list`, `gh api`, or any other way to get the information. If nothing works, you flag it to the user **on the very first wakeup** — not on the 10th. Every step in Phase 2 is mandatory. A step you silently skipped is a category of problems you're not monitoring.
- **Self-resolving bot comments after pushing a fix.** You're skipping the verification step that's the whole point of having a reviewer. Push the fix, leave the comment, re-tag the bot, let the next review verify.
- **Starting an architectural rewrite from a single review comment.** That's a much bigger commitment than this skill is sized for. Flag the user.
- **Reaching the COUNT cap without converging and silently re-arming.** If you ran 20 cycles and the PR still isn't clean, something's stuck (loop with bot, flaky test, waiting on user). Stop and report — don't add another schedule.
- **Editing the merge target.** All commits go on the PR branch. Don't touch `main` (or whatever the base is). The merge happens via the UI by a human, after this skill has ended.
- **Merging the PR.** See above. Don't.
