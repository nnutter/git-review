# git-review

git-review tracks user review of feature-branch commit stacks with [tuicr](https://github.com/agavra/tuicr).
It runs independently of Pi or any other coding agent.
Cobra and Fang provide the command interface, Lip Gloss renders summaries, and Huh provides the recovery prompt.

## Requirements

- Linux or macOS.
- Git and tuicr on `PATH`.
- A checked-out feature branch with a configured Git upstream.
- Go 1.27.3 or later to build from source.

```sh
go install github.com/nnutter/git-review@latest
```

For a local build, run `go build -o git-review .`.
With `git-review` on `PATH`, you can also invoke it as `git review`.

## Agent skill

The portable [git-review skill](skills/git-review/SKILL.md) instructs an agent to retrieve saved feedback and prepare amendments for re-review.
Load it in your agent or install `skills/git-review/` in the agent's supported skills location.
The initial workflow uses `git-review feedback --json` and native Git fixups targeting each stack tip.
It replays later commits without autosquashing so feedback commits remain adjacent to their targets.
The user launches reviews and controls approval.
The skill does not register `/git-review` commands or implement background workers.

## Pi integration

Install the Pi extension and git-review skill from this repository:

```sh
pi install git:github.com/nnutter/git-review
```

Install the git-review CLI separately and ensure `git-review` is on `PATH`.
The package does not install the CLI or tuicr.
Remove any manually installed copies of the git-review extension or skill to avoid loading them twice.

Use `/feedback` in Pi to ask the model to retrieve and address saved feedback with the git-review skill.

The extension source lives in [`pi/extensions/git-review/`](pi/extensions/git-review/), with `index.ts` as its entry point and tests in `tests/`.
It publishes review counts for other extensions and asks the model to use the git-review skill when feedback exists after an agent run.
It defers model notices while a review is active for the current branch.
Deferred feedback remains eligible for notification after the review ends.
Activity-query failures suppress notices rather than assume that review ended.
It runs after the mise on-stop hook when the mise-hooks extension is present.
Interactive and RPC sessions also check every five seconds while the agent is idle.
Idle checks can deliver deferred feedback without another agent run.
An aborted run suspends idle notices until a non-aborted run settles.
Monitoring stops when the session shuts down.

Pi 1.1.0 does not support a package subdirectory in a Git install source.
The root `package.json` selects the Pi extension and portable skill explicitly.
For local development, run `pi install .` from this checkout.

## Commands

Run commands from the feature checkout or one of its subdirectories.
git-review reviews commits after the merge-base with `@{upstream}`.
It does not fetch branches or use Pi's target override.
An absent upstream, detached HEAD, merge commits, or ambiguous fixup placement is an error.
If the branch tracks its own remote branch, the review range can be empty.
Configure the intended upstream explicitly with Git.

### Show commits after the upstream

```sh
git-review log
git-review log -p
git-review log -pw
git-review log --stat
git-review log --difft
git-review log --hunk
```

This command runs `git log @{u}..HEAD --reverse`, so commits appear in oldest-first order.
Use `-p` or `--patch` to show patches.
Use `--stat` to show diff statistics, with or without patches.
Use `-w` or `--ignore-all-space` to ignore whitespace changes in patches.
Use `--difft` to show patches with [Difftastic](https://difftastic.wilfred.me.uk/), which requires `difft` on `PATH`.
This option implies `--patch` and runs Git with `-c diff.external=difft` and `--ext-diff`.
Use `--hunk` to show patches through `hunk pager`, which requires `hunk` on `PATH`.
This option implies `--patch` and runs Git with `-c core.pager='hunk pager'`.
Git starts the pager only when stdout is a terminal, so redirected output remains unpaged.
You can combine `--hunk` and `--difft` to send Difftastic output through `hunk pager`.
Neither option changes your saved Git configuration.
Git's normal precedence rules still apply, including overrides from `GIT_EXTERNAL_DIFF` and `GIT_PAGER`.
Output uses Git's format and pager without reading or changing review state.
The command does not support `--json` or revision arguments.
Unlike review commands, it uses the upstream range directly and permits merge commits.

### Show feature stacks and review state

```sh
git-review status
git-review status --needs-review
git-review status --json
git-review status --needs-review --json
```

This read-only command displays all stacks in the current feature range, including approved stacks, in oldest-first order.
The table shows each stack's status, inclusive commit range, and subject, with fixups grouped together.
Saved review details are hidden by default.
Use `-v` or `--verbose` to show those details before the table.
Active attempts remain visible without `--verbose`, and the flag does not change JSON output.
Use `--needs-review` to omit approved stacks and show unresolved stacks and active attempts.

JSON output uses the shared versioned result structure, with `"command":"status"` and a `summary` array.
By default, `entries` includes all current stacks.
With `--needs-review`, `entries` omits approved stacks, but `summary` still counts the full feature range.
The result's `status` remains `pending` while unresolved stacks or active attempts exist, and `idle` otherwise.

The summary counts commits in the current feature range, including attached fixups.
Each commit inherits its stack's current state.
Historical attempts and commits removed from the feature range do not contribute to the counts.
Stacks with changed content need another review and count as `needs_review` until a new attempt records another state.
Unchanged content can retain approval or pending feedback after commit hashes change.

The `summary` array always includes all five states, even when their counts are zero.
It uses this order: `needs_review`, `reviewing`, `feedback`, `incomplete`, `approved`.
For example, the `summary` field can contain:

```json
[
  {"state":"needs_review","count":3},
  {"state":"reviewing","count":0},
  {"state":"feedback","count":2},
  {"state":"incomplete","count":0},
  {"state":"approved","count":4}
]
```

The array preserves state order for consumers.
Command failures use the standard JSON error object.

### List unresolved stacks

```sh
git-review status --needs-review
git-review status --needs-review --json
```

A stack contains one original commit and its immediately following `fixup!`, `amend!`, or `reword!` commits.
Create feedback commits with `git commit --fixup=<stack-tip>`, `--fixup=amend:<stack-tip>`, or `--fixup=reword:<stack-tip>`.
Insert each feedback commit immediately after its target stack, before the next original commit.
Targets can be originals or feedback commits within that stack, but their subjects must identify exactly one earlier commit.
The filtered table omits approved stacks and includes active attempts, even when their original stacks disappeared from history.
Current and historical feedback remain available in the result.

### Check live review activity

```sh
git-review status --is-reviewing --json
```

Successful output is `{"version":1,"command":"status","is_reviewing":true}` or `{"version":1,"command":"status","is_reviewing":false}`.
Without `--json`, the command prints `Is reviewing: true` or `Is reviewing: false`.
The flag reports whether the current branch owns the live review worktree lock without changing review state or creating files.
A running automatic review holds this lock across all selected stacks.
A persisted `reviewing` attempt alone does not indicate a running process.
A review on another branch returns `is_reviewing: false`, including when that branch is in a linked worktree.
The lock remains repository-wide to prevent concurrent reviews from using the shared review checkout.
The flag cannot be combined with `--needs-review` or `--verbose`.
Command failures exit with status 1 and emit the standard versioned JSON error object.

### Collect saved feedback

```sh
git-review feedback
git-review feedback --json
```

This read-only command returns commit-review notes persisted by git-review and file-review notes from tuicr, including existing saved sessions.
It does not launch tuicr, recover an attempt, or change either review store.
Interrupted commit-review sessions whose notes were not captured by git-review still require explicit interactive recovery.

Feedback results use the versioned result envelope, with `"command":"feedback"`.
The optional `feedback` array contains records with these fields:

| Field | Meaning |
| --- | --- |
| `attempt` | Full persisted attempt, including its ID, captured stack, saved review, response, and replacement metadata |
| `historical` | False only for the latest unaddressed attempt matching a current stack by exact commits or completed review content |

Saved reviews retain comments from all scopes, including drafts and session notes.
Records appear in attempt order.
Superseded attempts, changed or removed stacks, and addressed attempts remain historical context.
The command does not infer that historical feedback needs another fix or that a changed stack resolved it.
Malformed stored review data is an error rather than silently omitted feedback.

The optional `fileFeedback` array contains file-review results with `"command":"files"`, the saved session path, comments, and session notes.
It includes local sessions in the feature checkout and its annotation directories, not commit-review sessions in the detached checkout.
These records are repository-scoped and remain visible after branch changes while their tuicr sessions contain notes.
They do not imply current stack feedback or authorize fixup commits.
Address them as new commits, logically organized and sized.
Resolve or remove obsolete notes in tuicr when they no longer apply.

The result is `pending` when current commit feedback or any saved file feedback exists and `idle` otherwise.
An `idle` result can still include historical records.
Each feedback array is omitted when it has no records, and `entries` is empty.
Active attempts remain visible in `active`, but collecting feedback does not resume them.

### Review a stack

```sh
git-review commits
git-review commits HEAD
git-review commits abc123..def456
```

`run` remains as a deprecated alias for `commits`.

Without an argument, git-review reviews each eligible, non-deferred stack in oldest-first order, once per invocation.
Approval or feedback does not stop this sequence.
A commit reference selects only its containing stack.
The displayed `original..tip` form has inclusive endpoints and must identify exactly one complete stack.
An explicit selection can review an approved stack again or select a later stack without approving earlier stacks.

git-review launches tuicr directly in a stable, detached worktree checked out at the stack's tip.
The tuicr revision expression uses the original's parent through the stack tip, so the original commit is included.
Tuicr's editor therefore opens the reviewed file version rather than the feature checkout's current version.
Make fixes in the feature checkout, not the review worktree.
The review checkout is disposable and is only for exploring the reviewed code, not making fixes.
Before reuse, git-review discards tracked edits, untracked files, and ignored artifacts in that checkout.
It does not forcibly remove nested Git repositories.
git-review refuses to overwrite unrelated directories or attached branch checkouts at the review path.
It also refuses to use a worktree held by another git-review process.

After tuicr exits, git-review validates its saved review:

| Saved result | Status |
| --- | --- |
| Every file reviewed, without comments or session notes | `approved` |
| Any comments or session notes, including drafts | `feedback` |
| No reviewed files, comments, or session notes, including no saved session | `cancelled` (attempt discarded) |
| Some reviewed files, but incomplete and without notes | `incomplete` |
| Corrupt, unsupported, unreadable, or ambiguous session | Command error |

Commit-message review marks count toward completion.
A successful process exit is not proof of approval.
An untouched review leaves no attempt in git-review's history and does not require recovery on the next invocation.

### Review a range of stacks

```sh
git-review commits --start abc123
git-review commits --start abc123 --end def456 --json
git-review commits --end def456
```

Each invocation reviews eligible stacks within the inclusive bounds in oldest-first order.
After approval or feedback, git-review continues to the next eligible stack.
Each eligible stack opens at most once per invocation.
Feedback remains unresolved and requires an explicit recovery or replacement choice on a later invocation.
An incomplete review, cancellation, a required decision, or an error stops the command.
A boundary can identify any commit in a stack and includes the whole stack.
An omitted start uses the first feature stack, and an omitted end uses the last.
Boundaries must be in oldest-first order and belong to the feature-branch review range.
Bounds cannot be combined with the positional stack selector.
Approval and deferral still control automatic selection.
An active attempt takes precedence over bounds and requires recovery, replacement, or cancellation.
Recovery reopens that attempt's captured range, even when it lies outside the selected bounds.
After approval or feedback from that attempt, review continues within the selected bounds.
Recovery, replacement, and session choices apply only to the initial attempt.
When all stacks within the bounds are approved, the result is `range_complete`, even if other stacks remain unresolved.
If the sequence finishes with feedback within the bounds from this invocation, the result is `feedback`, not `range_complete`.
If only deferred stacks remain unresolved within the bounds, the result is `no_waiting` instead.
The result's view and entries still describe the feature branch, not only the bounded range.

### Recover or replace an attempt

An interrupted, incomplete, or feedback attempt on the selected stack requires an explicit choice.
In an interactive terminal, git-review prompts to recover, replace, or cancel.
With JSON output or redirected streams, it returns `decision_required` without a prompt.

```sh
git-review commits --recover
git-review commits --replace
git-review commits --recover --session <session-id>
```

Recovery reopens tuicr at the previous attempt's exact captured range and preserves its attempt ID.
Tuicr restores saved comments and review marks for that checkout and range.
git-review records the result only after tuicr exits.
Replacement starts another attempt and retains the previous attempt in history.
Neither operation proceeds while the review worktree is held by a live git-review/tuicr process.

`--session` accepts an exact saved session ID, index slug, or indexed `sessions/<id>.json` path.
It resolves ambiguous saved sessions and is not an arbitrary filesystem path.
If an interrupted original disappeared and no lineage identifies its replacement, use `git-review commits <replacement-commit> --replace`.

### Review branch changes or annotate files

```sh
git-review files
git-review files --all
git-review files <path>
git-review files --untracked
git-review files --untracked --json
```

Without a path or scope flag, git-review reviews the committed branch diff from the merge-base with upstream to HEAD.
This mode requires a configured upstream and excludes uncommitted changes.
With `--all`, it runs `tuicr tui --all-files` to review every tracked file.
With a path, it runs `tuicr tui --file <path>` for that file or directory.
With `--untracked` and no path, it runs `tuicr tui --file .` to include untracked files in the current directory.
`--untracked` cannot be combined with an explicit path.
`--all` cannot be combined with `--untracked` or an explicit path.
Use `--session` to select an exact saved session when several file reviews match the same repository.

File reviews run in the feature checkout, not the detached review worktree.
`--all`, explicit paths, and `--untracked` do not require a configured upstream.
They hold the same repository-wide worktree lock as `commits` reviews, so `status --is-reviewing` reports true during either review.
They do not create git-review attempts or affect `status` counts.
Retrieve their comments and session notes with `git-review feedback`, in the separate `fileFeedback` JSON array or the terminal “File feedback” section.
`approve` applies only to commit stacks, not file reviews.

Successful `files` output uses `"command":"files"` to distinguish it from `"command":"commits"`.
It contains the reviewed repository, requested path, `allFiles` and `untracked` flags, and the saved file review with comments and session notes.
Branch diff sessions are matched to the captured commit range at the Git repository root.
Other saved sessions are matched to tuicr's canonical file or directory scope, not just the Git repository root.
File comment paths are relative to the Git repository root, including reviews started from a subdirectory.
Statuses mirror stack reviews without recovery decisions:

| Saved result | Status |
| --- | --- |
| Every file reviewed, without comments or session notes | `approved` |
| Any comments or session notes, including drafts | `feedback` |
| No reviewed files, comments, or session notes, including no saved session | `cancelled` (no feedback saved) |
| Some reviewed files, but incomplete and without notes | `incomplete` |

Address `files` feedback as one or more new commits, logically organized and sized.
Do not use `fixup!`, `amend!`, or `reword!` commits for file feedback.
Address `commits` feedback with native Git fixup commits inserted immediately after their target stacks instead.

### Record explicit user approval

```sh
git-review approve <start_commit>
git-review approve <start_commit> <end_commit>
```

Endpoints are inclusive and accept Git commit references such as abbreviated SHAs or `HEAD`.
Supply separate endpoints, not a Git `A..B` expression.
The selected range must contain complete stacks, including all attached fixups.
Pending feedback on a current stack or an active attempt blocks approval.
Explicit approval can approve an incomplete stack without deleting its prior attempts.

Only a user should authorize this command.
Agents must not infer approval or approve commits on the user's behalf.

## JSON output for agents

All commands accept `--json`, before or after the subcommand.
Each command produces one JSON value followed by a newline on stdout.
Successful `status` output is an ordered array of state counts.
Other successful commands produce a versioned JSON object.
`commits` results use `"command":"commits"` with stack entries and attempts.
`files` results use `"command":"files"` with the file scope and saved file review.
JSON and terminal output consume the same typed result.
JSON retains full commit IDs, attempts, saved comments, session notes, and optional previous-version lineage.
Tuicr renders its interactive view through the controlling terminal, with diagnostics on stderr, not the JSON stream.
git-review launches tuicr with `--stdout` to bypass clipboard confirmation and discards its Markdown export.
Feedback comes from the persisted review session.
`--json` disables git-review's recovery prompt, but a new tuicr review still requires an interactive terminal.

Successful results other than `status` contain:

| Field | Meaning |
| --- | --- |
| `version` | Output schema version, currently `1` |
| `command` | Canonical command name |
| `status` | Command outcome |
| `view` | Branch, upstream, merge-base, HEAD, current stacks, and state path |
| `entries` | Unresolved stacks, or the stacks selected by approval/review |
| `active` | Persisted active attempts |
| `decision` | Required explicit choice, when present |
| `feedback` | Current and historical commit-review feedback records, when notes exist |
| `fileFeedback` | Saved file-review results from tuicr, when notes exist |

Needs-review outcomes are `pending` or `idle`.
Review outcomes include `approved`, `feedback`, `incomplete`, `decision_required`, `cancelled`, `idle`, `range_complete`, and `no_waiting`.
`range_complete` means every stack in the selected range, or the full default range, is approved.
`no_waiting` means that deferred reviews remain unresolved, but none are eligible for automatic selection.
An explicit stack selection returns that stack's outcome, such as `approved`.
A valid result exits with status 0, including feedback and required decisions.
An agent must inspect the result's status rather than equate exit status 0 with approval.

Command failures exit with status 1 and produce a JSON error object:

```json
{"version":1,"command":"status","status":"error","error":"..."}
```

Help, version, and shell completion output retain their usual formats.

## State and compatibility

Branch state lives at:

```text
<git-common-dir>/tuicr-reviews/<sha256-of-branch-name>.json
```

The state retains Pi's version-1 attempt format with optional `changeId` lineage metadata.
The branch name determines the state file, so a branch rename does not automatically migrate its history.
Writes use an exclusive `.lock` file and atomic replacement.
After a crashed writer, remove a stale state lock only after confirming that no writer is running.

Each repository uses one persistent, detached review checkout:

```text
<git-common-dir>/git-review/worktree
```

All feature checkouts and branches sharing that Git common directory use the same review checkout and lock.
git-review checks out the selected stack tip so tuicr's editor opens the correct file version.
Tuicr associates saved sessions with the checkout path and commit range, not just individual file paths.
Identical stacks can reuse the same saved session across branches, while git-review's attempt history remains branch-specific.
No migration from the previous cache-based checkout location is performed.

The adjacent `worktree.git-review.lock` file uses an operating-system lock that releases when its last holder exits.
Do not delete this lock file to bypass an active review.
The checkout remains in place between reviews, so routine reviews do not create disposable worktrees that need garbage collection.
Other tools must not use or edit this checkout while git-review holds its lock.
Prepare fixes in the feature checkout or a separate writable workspace, never in the review checkout.

git-review reads tuicr's platform review store, then checks the other platform's location for existing sessions.
Supported formats are review index `2.0` and saved session `1.3`.
It never writes tuicr annotations or rewrites feature-branch history.
Compatibility with Pi's branch state requires Pi to adopt the `tuicr-reviews` path.

## Patch cache maintenance

Use these commands to prepare fingerprints for a later `git-review status` or to prune the repository's shared cache:

```sh
git-review cache warm           # Warm the stack containing HEAD
git-review cache warm <commit>  # Warm the current stack containing this commit
git-review cache prune          # Apply the existing retention limits
```

Warming requires a configured upstream and linear feature history, like `status`.
It computes only the selected stack's fingerprint, including attached fixups, and leaves other missing entries for `status`.
The commit must belong to the current feature range.
Neither command changes review history or launches tuicr.
Cache storage remains best-effort, as it is for `status`.
Both commands produce no stdout on success unless you pass `--json`.
JSON results use `{"version":1,"command":"warm","status":"ok"}` or the same structure with `"command":"prune"`.

Git's `post-commit` hook receives no arguments, so `cache warm` defaults to `HEAD`.
Add this command to your existing hook, or use this script as the executable `post-commit` hook in your configured hooks directory:

```sh
#!/bin/sh
git-review cache warm >/dev/null 2>&1 || :
```

This best-effort hook suppresses errors, including commits on branches without an upstream.
It runs synchronously and moves the selected fingerprint computation from the next status call into the commit hook.
It does not cache a complete status result.

Pruning does not require an upstream or an attached branch.
It removes fingerprints older than 30 days and temporary files older than 24 hours.
It also evicts the oldest fingerprints to enforce the 16 MiB and 10,000-entry limits.
Each pass scans at most 50,000 directory entries and skips maintenance if another pruner holds the lock.
Pruning an absent cache succeeds without creating it.
Interactive reviews already run this maintenance automatically.

The cache commands enter through `internal/cli/cache/cache.go` and the `Repository` methods in `internal/review/git.go`.
The retention policy lives in `internal/review/maintenance.go`.

## Changed stacks and Change-Ids

git-review records an optional `patchHash` for each stack's combined diff, from the original commit's parent to the final fixup.
New hashes use Git's `patch-id --verbatim` with a `v2:` prefix and preserve paths, file modes, whitespace, binary content, and diff context.
They ignore text blob IDs, hunk line numbers, file ordering, commit messages, authors, timestamps, and commit identities.
Whitespace remains significant for every file type, including Python indentation.

Within the same repository and branch history, matching non-empty patch hashes preserve approval and pending feedback.
Rewording, rebasing onto unrelated changes, or squashing attached fixups can therefore preserve review status.
A changed diff requires another review.
A change outside the diff context can shift a hunk without invalidating approval.
Matching remains conservative: changes to diff context still require another review.
Matching patches do not guarantee unchanged behavior when surrounding code changes.
Empty diffs omit the hash, so unrelated empty commits cannot share approval.
Older `v1:` hashes remain readable but do not match `v2:` hashes.
Records with an older hash or no hash still retain their status for exact commits and parent matches, but rewritten stacks require another review.

Stored attempts retain their original commit scope, including feedback locations and saved session identity.
Active and incomplete attempts do not transfer status through a patch hash.
Session recovery continues to use the original recorded commits, not the rewritten stack.
A newer unfinished attempt or an addressed replacement prevents an older matching approval from reappearing.

Feedback from obsolete content remains historical context, not a global blocker.
Changed stack content supersedes a prior result but does not prove that feedback was addressed.
The next review supplies that check.

When an original commit has a Gerrit `Change-Id` trailer, git-review uses it to connect stack versions within the same repository and feature branch.
It never transfers approval through a Change-Id.
git-review rejects malformed or multiple trailers and duplicate Change-Ids on current original commits.
It does not guess lineage for split or combined changes.

### Generate Change-Ids with a commit hook

```sh
git-review commit-msg <message-file>
```

This command updates the message file in place and produces no output on success.
It does not require an upstream or a review session.
Its compatibility reference is the Gerrit hook in [`contrib/commit-msg`](contrib/commit-msg).
Existing Change-Ids remain unchanged, including during an amend.
Like that reference version, it also adds IDs to `fixup!` and `squash!` messages.

To use it without a hook manager, add this command to your executable `commit-msg` hook:

```sh
#!/bin/sh
exec git-review commit-msg "$1"
```

Use `git rev-parse --git-path hooks` to locate the default hooks directory.
If you set `core.hooksPath`, use that directory instead.
If another hook already exists, integrate the command rather than replace the hook.

The command honors Gerrit's configuration:

- `gerrit.createChangeId=false` disables changes to the message.
- `gerrit.reviewUrl` selects a Gerrit `Link` trailer instead of a `Change-Id` trailer.

Review lineage currently requires a `Change-Id` trailer, not a `Link` trailer.
Empty or comment-only messages fail unless ID creation is disabled.
Successful hook calls remain silent even with `--json`.

## Development

Install the pinned tools and repository hooks:

```sh
mise install
mise run install-hooks
```

The install task removes existing Lefthook hooks before installing hk.
Hook configuration lives in [`hk.pkl`](hk.pkl), with commands in [`mise.toml`](mise.toml).

```sh
mise run tests
mise run ci-tests
mise run linters
```

The standard suite includes compiled-binary tests for listing, approval, errors, JSON output, and persistence across invocations.
Focused unit and integration tests also use synthetic saved sessions and fake tuicr executables.

Run the real-tuicr functional suite with:

```sh
mise run functional-tests
```

This task requires tuicr on `PATH` and permission to create pseudo-terminals.
It runs the compiled git-review binary against real tuicr, using terminal key input and tuicr's public annotation CLI.
It checks complete reviews, incomplete feedback, and interactive recovery of a saved review after git-review is killed.
It also selects recover, replace, and cancel through the actual terminal prompt and checks the resulting status through another CLI invocation.
A terminal emulator reconstructs screen updates instead of matching raw ANSI output.
The real-tuicr tests skip in the standard suite unless `GIT_REVIEW_REAL_TUICR=1` is set.
When enabled, a missing tuicr executable or unavailable pseudo-terminal fails the tests.
The suite is verified with tuicr v0.27.0.

All tests use temporary repositories.
Each test package removes inherited `GIT_*` variables before its tests start and disables global/system Git configuration.
This isolates in-process Git calls and child commands from a surrounding rebase's repository, index, object directory, and injected configuration.
Real-tuicr tests isolate HOME and the XDG config, cache, and data directories for child processes.
They do not open reviews of an existing feature branch or access the user's saved review sessions.
