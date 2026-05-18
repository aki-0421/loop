# Git and Pull Request Workflow

## Base branch

The base branch comes from config or `--base`. `loop init` detects a default in this order:

1. `develop`.
2. `main`.
3. Current branch.

Before each iteration, the CLI updates the base branch when configured to pull automatically.

## Branch names

The initial branch is numbered and temporary:

```text
wip/0001
wip/0002
```

The final branch is renamed by the CLI using the agent proposal:

```text
feat/add-usage-report-command
fix/normalize-empty-config-values
refactor/extract-git-runner
```

The agent only proposes `kind`, `slug`, and optional `final_name` in the `result` artifact. It must keep working on the initial branch. The CLI performs the final rename after validation passes and immediately before local merge integration or PR creation.

Allowed branch kinds by default:

- `feat`
- `fix`
- `refactor`
- `docs`
- `test`
- `style`
- `build`
- `ci`
- `chore`

Branch slugs:

- Use lowercase English words.
- Use hyphens between words.
- Avoid ticket-system assumptions.
- Avoid `loop/` prefixes by default.
- Append the iteration suffix only when needed to avoid collisions.

## Commit creation through the CLI

The agent requests commits during the iteration by running:

```bash
loop commit <type> <short imperative message>
```

The CLI stages repository changes, validates the message, creates the commit on the current iteration branch, and prints the resulting commit SHA and subject. Runtime files under `.loop/runs/`, including `iteration.db`, `prompt.md`, `agent-events.jsonl`, and `errors.log`, are not committed.

Agents must not run `git add` or `git commit` directly. If `loop commit` rejects the type or message, the command exits non-zero with a human-readable validation error so the agent can immediately retry with corrected arguments.

Commit message format:

```text
<PREFIX>: <short imperative message>
```

Default prefixes:

| Prefix | Use for |
| --- | --- |
| `F` | Features, fixes, user-visible behavior |
| `T` | Tests and test utilities |
| `R` | Refactoring |
| `D` | Documentation |
| `S` | Style and presentation |
| `V` | Versioning and dependencies |
| `C` | Configuration and tooling |

The CLI may reject commits that do not match the configured pattern when `git.commits.enforcePattern=true`.

TODOs are sized so that one completed TODO corresponds to one `loop commit` invocation, except no-change confirmations. The agent marks a TODO complete only after the matching commit exists, unless the TODO required no repository change.

## Local merge mode

Local merge mode runs locally:

```bash
git checkout <base>
git pull --ff-only
git merge --squash <iteration-branch>
git commit -m "<summary_sentence>"
```

After commit, the CLI may delete the iteration branch according to cleanup settings.

## Pull request mode

Pull request mode uses `gh`:

1. Rename the completed iteration branch to the agent-proposed final branch name.
2. Push branch.
3. Generate PR title and body through the agent.
4. Create PR.
5. Wait for checks when configured.
6. Prepare the local checkout for branch deletion by removing the iteration worktree when present and checking out the base branch.
7. Merge through squash merge when checks pass and auto-merge is enabled. The squash commit subject is the generated PR title.
8. Pull the base branch.
9. Delete branch according to cleanup settings.

Command shape:

```bash
git push -u origin <branch>
gh pr create --base <base> --head <branch> --title "<title>" --body-file <body-file>
gh pr checks <pr> --watch
git checkout <base>
gh pr merge <pr> --squash --subject "<title>" --body-file <body-file> --delete-branch
git pull --ff-only
```

The implementation stores command outputs in the iteration directory.

## PR template handling

The agent reads pull request template text through `loop iteration read pr-template`, fills it, and writes the PR title and body artifacts in the template language. If a repository template exists, the command returns it. If no repository template exists, the command returns CLI-owned fallback text.

If the agent does not write the `pr-body` artifact, the CLI fallback reads the repository template when present and appends a minimal loop note in the same detected language.

## Check waiting

When pull request mode has `waitChecks=true`, the CLI waits for provider checks through `gh`. If GitHub reports no checks for the PR branch, the wait step is treated as skipped rather than failed. If checks fail:

- The CLI records the failing checks.
- The CLI starts a new repair iteration on the same branch when configured.
- The check-failure repair prompt is embedded by the CLI, not implemented as a skill. It includes the failing check output and instructs the agent to search the web for the exact error or likely root cause before editing, then record the search queries and findings in the worklog.
- If no repair attempts remain, the run stops with a failed integration state.

When `mergeWhenChecksPass=true`, the CLI merges without asking the user.

## Cleanup

Cleanup is configurable:

| Resource | Default cleanup |
| --- | --- |
| Completed local branch in local merge mode | delete |
| Completed remote branch in PR mode | delete through `gh` when available |
| Failed branch | keep |
| Worktree after completed integration | remove |
| Worktree after failure | keep |

Runtime logs remain under `.loop/runs/` until removed by the user.
