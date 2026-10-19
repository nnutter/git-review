---
name: git-review
description: Retrieve saved git-review feedback, address comments, and prepare commits for re-review. Use when the user asks to address git-review feedback, insert fixup commits, or create new commits for file reviews.
compatibility: Requires Git and git-review on PATH, a feature branch with a configured upstream for commit reviews, and permission to change history for fixups.
---

# Address git-review feedback

Use this skill to address reviews that the user completed through git-review.
This skill does not launch reviews or register harness commands.
The user controls review and approval.

## Check review progress

Use this read-only command for current stack details and commit counts:

```sh
git-review status --needs-review --json
```

Successful output is a versioned result object with `"version":1` and `"command":"status"`.
With `--needs-review`, the `entries` array omits approved stacks.
The `view.stacks` array includes all current stacks.
The `summary` array contains elements with a `state` string and a `count` integer.
The summary always includes these states in this order, including zero counts:

1. `needs_review`
2. `reviewing`
3. `feedback`
4. `incomplete`
5. `approved`

Counts include all commits in the current feature range, including attached fixups.
Each commit inherits its stack's current state.
Historical attempts and removed commits do not contribute to the counts.
Changed stacks lose prior approval and count as `needs_review` until a new attempt records another state.
An empty feature range produces all five states with zero counts.

Check the exit code and output version before interpreting the result.
Command failures exit with status `1` and emit the standard versioned JSON error object instead.

Read `entries` for unresolved stack details and current commit IDs.
The summary counts the full feature range even when entries are filtered.
Use `git-review feedback --json` for saved comments and session notes.
Counts do not identify feedback targets or authorize approval.

## Distinguish commits and files output

`git-review commits --json` reviews commit stacks and returns `"command":"commits"`.
Address its feedback with fixup commits inserted immediately after their target stacks.

`git-review files [<path>] --json` reviews working-tree files and returns `"command":"files"`.
It runs `tuicr tui --all-files` without a path, `tuicr tui --file <path>` with a path, and `tuicr tui --file .` with `--untracked` and no path.
Address its feedback as one or more new commits, logically organized and sized.
Do not use `fixup!`, `amend!`, or `reword!` commits for file feedback.

Check the result's `command` before choosing a workflow.
Do not infer the workflow from file paths or comment text alone.

## Retrieve feedback

Run this command from the feature checkout:

```sh
git-review feedback --json
```

Inspect the JSON result, not only the exit code.
Stop on command errors or an unsupported output version.
The supported output version is `1`.

Read the `feedback` array rather than `entries`.
The array is absent when no saved notes exist.
Each record contains an `attempt` and a `historical` boolean.

For each selected attempt, read:

- `id`: identifies the feedback attempt.
- `stack`: contains the captured commits, tip, subject, and optional Change-Id.
- `review.comments`: contains comments from all scopes, including drafts.
- `review.sessionNotes`: contains notes about the whole review.
- `response` and `replacement`: provide prior response context when present.

Read every comment, including file, line, commit-message, and review comments.
Session notes are feedback even when the comments array is empty.

Use records with `historical: false` as the default work queue.
Historical records provide context, not an instruction to repeat previous fixes.
The `idle` status can still include historical records.
If the user requests historical feedback, identify its current target before changing code.
Ask the user when the target or requested change is ambiguous.

Retain selected attempt IDs and captured ranges for the whole batch.
Your own history rewrites can make other records historical.
Do not silently lose selected feedback or address the same attempt twice.

If `active` contains an attempt, pause before rewriting history.
Ask the user to finish or resolve the active review.
Check live activity with `git-review status --is-reviewing --json` before rewriting history.
When `is_reviewing` is true, a `commits` or `files` review holds the worktree lock.
Ask the user to finish the live review instead of rewriting underneath it.
This command does not collect uncaptured notes from interrupted tuicr sessions.
Do not use `git-review commits --recover` to retrieve feedback.
Recovery opens an interactive review and requires the user's choice.

## Prepare the changes

1. Check the feature branch and configured upstream.
2. Check the working tree and any Git operation already in progress.
3. Preserve unrelated user changes before changing branches or rewriting history.
4. Summarize the selected feedback and proposed fixes.
5. Record the original branch tip and create a uniquely named local backup branch.
6. Identify each current target stack through git-review's `view.stacks` data.

Use `git-review status --needs-review --json` when you need a fresh view of current stacks.
Its `view.stacks` includes approved stacks too.
Do not derive targets from comment file paths or commit subjects alone.
Use full commit IDs.
Preserve existing Change-Id trailers.

Prefer processing target stacks from newest to oldest.
This keeps older target commit IDs unchanged while you replay later history.
Refresh the current view when a target no longer matches the captured stack.
Do not guess lineage for split or combined changes.

Make fixes in the feature checkout, not `<git-common-dir>/git-review/worktree`.
The review checkout is disposable and is only for code exploration.
Do not edit git-review's state file or tuicr's saved sessions.

## Create native Git feedback commits

A commit stack contains an original commit followed immediately by its feedback commits.
Each new feedback commit must target the current stack tip, including an existing feedback commit.
Set `target_tip` to that tip's full commit ID.
Use one of these native Git commands:

```sh
# Code changes.
git commit --fixup="$target_tip"

# Code and commit-message changes.
git commit --fixup="amend:$target_tip"

# Commit-message changes only.
git commit --fixup="reword:$target_tip"
```

For amend and reword modes, supply a non-interactive editor through `GIT_EDITOR` when the harness has no terminal.
Keep Git's generated first line unchanged.
Edit the copied message body to express the requested target message.
Reword mode does not include staged file changes.
Do not construct feedback prefixes manually.
Do not replace these commands with `git commit --amend`.

Keep each feedback commit focused on one logical change.
Do not create a feedback commit when no code or commit-message change is necessary.
Report explanations or unresolved questions instead.
Test the change at the target stack before replaying later commits.
If several feedback commits are necessary, target the new tip each time.

## Insert feedback immediately after its target

If the target stack is at the feature branch tip, create the feedback commit there.
No reordering is necessary.

Otherwise, prepare the fix on a uniquely named temporary branch at `target_tip`.
Use the same checkout, not a new worktree.
Record `feature_branch` before switching branches.
Choose a unique `fix_branch` name.

```sh
git switch -c "$fix_branch" "$target_tip"
```

Implement and test the fix on this branch.
Create the native feedback commit as described above.
Then replay the feature branch's later commits onto the new stack tip:

```sh
new_tip=$(git rev-parse HEAD)
git rebase --no-autosquash --no-update-refs --reapply-cherry-picks \
  --empty=keep --onto "$new_tip" "$target_tip" "$feature_branch"
```

This preserves the target stack and inserts its new feedback before the next original commit.
The temporary branch retains prepared work if the rebase fails.
The backup branch retains the original feature history.
The flags prevent autosquashing, unintended updates to other branches, and silent removal of commits that become empty.

Resolve conflicts without discarding unrelated descendant changes.
Use `GIT_EDITOR=true git rebase --continue` in a non-interactive harness.
If safe resolution is unclear, preserve both branches and ask the user.
Do not force-push the rewritten branch unless the user requests it.

Keep the original and feedback commits separate until the user requests squashing.
Never use autosquash to insert feedback.
Do not use `fixup`, `squash`, or `reword` todo actions to consume these commits during insertion.
Git reword fixups normally have an `amend!` subject.
Git generates nested feedback subjects when the target is an existing feedback commit.

## Create new commits for file feedback

File reviews do not use stacks, fixups, or history rewrites.
Make fixes in the feature checkout on the feature branch.
Create one or more new commits with normal messages, logically organized and sized.
Do not use `--fixup`, `--fixup=amend:`, or `--fixup=reword:` for file feedback.
Do not rebase or reorder existing commits to insert file fixes.
Test the final history with the repository's required checks.
Record the new commit IDs for the report.

## Check and report

1. Check that the checkout uses the feature branch after integration.
2. Run the repository's required checks on the final history.
3. Run `git-review status --needs-review --json` to check stack formation.
4. Check that each feedback commit immediately follows its target stack tip.
5. Report each attempt ID, addressed comment, new stack range, and validation result.

Leave review eligibility and approval decisions to git-review.
Do not call `git-review approve`, launch a review, or claim user approval.
Do not change stored state to mark feedback addressed.
Keep backup branches until the user requests their removal.
Remove temporary fix branches only after checking their commits are reachable from the feature branch.
Leave recovery branches intact when integration fails.

Tell the user that the changes are ready for re-review with `git-review commits` for stack feedback or `git-review files` for file feedback.
