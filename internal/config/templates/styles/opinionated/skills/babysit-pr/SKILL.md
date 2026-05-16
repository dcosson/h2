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

Each 5-minute wakeup, do these steps in order. Skip what's obviously not relevant (no new comments, no failing checks). Don't re-do work you've already done in a previous tick.

### Step 2a — Check CI

```bash
gh pr checks <pr-number>
```

- All green → continue to 2b.
- Failing → run the failing job, read the error, **push a fix**. Most CI failures are: type errors, lint errors, broken unit tests. Fix them locally, run the local equivalent (`make lint`, `make typecheck`, `make test` — whatever the repo uses), then `git push`. End turn — let the next wakeup re-check.

### Step 2b — Pull review comments

```bash
gh pr view <pr-number> --json reviews,comments
gh api "repos/<org>/<repo>/pulls/<pr-number>/comments"
```

Group comments by author. The relevant authors are:

- **Review bots** — `@bugbot`, `@claude` (Claude Code review), `@codex`, `@greptile`, etc. Anything that looks programmatic.
- **Humans** — everyone else.

### Step 2c — Handle review-bot comments

For each bot comment that isn't already resolved/handled:

1. **Read it carefully.** Look at the file/line, understand what the bot is claiming.
2. **Decide: false positive or legit?**
   - **False positive** (bot misunderstood, claim doesn't apply, code is actually correct as-is): mark the comment resolved with a brief reason. Use `gh api` to mark the conversation resolved. **This is the only time you resolve a comment yourself.**
   - **Legit** (bot is right, code should change): write the fix, push it. **DO NOT resolve the comment yourself.** Leave it open. The next review pass — when you re-tag the bot — is what verifies your fix actually addressed the concern. If you resolve it yourself, you're declaring "fixed" without verification.

3. **Why this matters:** resolving a comment yourself short-circuits the verification loop. The whole point of the bot review is that it independently checks your work. Push fix → request re-review → bot reads new code → bot resolves OR flags new issues. That second pass is the safety check.

### Step 2d — Handle human comments

For each human comment that isn't already addressed:

- **Mechanical / specific** (rename this var, extract this helper, add a null check, fix this typo): push the fix, reply to the comment explaining what you pushed (`gh pr comment` or `gh api` to reply inline). Don't ask permission for the kind of change a careful reader would just accept.

- **Architectural / "let's think about this"** (let's reconsider the approach, what if we did X entirely differently, this whole component should be rewritten, why are we even doing this): **do not start a rewrite**. This skill is not the place for fundamental scope changes. Reply to the comment along the lines of:

  > Waiting for user to discuss this — @<user-handle> let me know how you'd like to proceed.

  Then continue processing other comments. If this thread is the only outstanding item, you'll still end the turn and wake up later — the user may have responded by then.

### Step 2e — Re-request bot reviews

If you pushed any fixes in 2c or 2d, re-request the relevant bot reviews so they see the new code:

```bash
gh pr comment <pr-number> --body "@bugbot review"
gh pr comment <pr-number> --body "@claude review"
# etc. for whichever bots are reviewing this PR
```

Each bot has its own trigger phrase — the convention is `@<botname> review` for the most common ones. If you're not sure, look at how previous turns triggered the bot on this PR.

### Step 2f — End turn

Output a one-paragraph summary of what you did this tick (CI status, comments addressed, fixes pushed, bots re-tagged), then end the turn. The next scheduled wakeup will arrive in 5 minutes.

## Phase 3: Exit when clean

The PR is "clean" when:

- All CI checks pass (`gh pr checks` is green).
- No outstanding bot comments are unresolved (either you resolved a false positive, or you pushed a fix AND the bot's subsequent review approved/resolved it).
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

1. **False positive vs. legit (Step 2c).** Bots get this wrong sometimes — both false positives and false-cleans. Read the actual code. If the bot's claim doesn't match what the code does, mark it false positive with a one-line reason. If you're not sure, err toward "legit" — pushing a small refactor is cheaper than having a real bug slip through.

2. **Mechanical vs. architectural (Step 2d).** A "rename this variable" is mechanical. A "let's consider whether this whole abstraction is right" is architectural. When in doubt, treat it as architectural and flag for the user — the cost of waiting one extra cycle is small; the cost of starting a wrong rewrite is large.

3. **When to stop early.** If the same comment keeps coming back after you've pushed multiple fixes the bot disagrees with, stop and flag the user. You're likely in a loop where you and the bot disagree about what "fixed" means — escalate rather than ping-pong.

4. **What "re-request review" means for each bot.** Conventions differ:
   - Some bots auto-re-review on any push (no comment needed).
   - Some require an explicit `@<bot> review` comment.
   - Some have custom slash commands (`/review`, `/recheck`).
   Check past comments on this PR to see how the bot was triggered before, and use that.

## Anti-patterns

- **Self-resolving bot comments after pushing a fix.** You're skipping the verification step that's the whole point of having a reviewer. Push the fix, leave the comment, re-tag the bot, let the next review verify.
- **Starting an architectural rewrite from a single review comment.** That's a much bigger commitment than this skill is sized for. Flag the user.
- **Reaching the COUNT cap without converging and silently re-arming.** If you ran 20 cycles and the PR still isn't clean, something's stuck (loop with bot, flaky test, waiting on user). Stop and report — don't add another schedule.
- **Editing the merge target.** All commits go on the PR branch. Don't touch `main` (or whatever the base is). The merge happens via the UI by a human, after this skill has ended.
- **Merging the PR.** See above. Don't.
