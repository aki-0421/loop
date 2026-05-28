<h1 align="center">loop</h1>

<p align="center">A Git-native harness for repeatable AI coding iterations.</p>

<p align="center">
  <a href="#quick-start">Quick Start</a> |
  <a href="spec/00-index.md">Specification</a> |
  <a href="#development">Development</a>
</p>

<p align="center">
  <a href="https://pkg.go.dev/github.com/aki-0421/loop"><img alt="Go Reference" src="https://pkg.go.dev/badge/github.com/aki-0421/loop.svg"></a>
</p>

`loop` turns one Markdown instruction file into a controlled sequence of role-orchestrated agent runs. Each iteration is one AI sprint-sized pull request: the CLI creates the branch, asks a planner agent for a dependency-aware task tree, runs coding agents in isolated task worktrees, squash-merges completed tasks into the iteration branch, validates and reviews the result, then opens and manages the pull request.

It is built for repositories where AI work should leave behind the same things good human work does: small commits, clear branches, validation evidence, pull request context, and enough logs to explain what happened later.

## Features

- **Repeatable iterations**: run until a CLI-provided goal is satisfied, an iteration limit is reached, or a terminal error stops the run.
- **Git-native isolation**: every iteration and coding task runs in CLI-created branches and worktrees, then task branches are squash-merged into the iteration branch.
- **Role orchestration**: one configured adapter is reused as planner, coding, and review agent with role-specific prompts and environment.
- **Dependency-aware task scheduling**: planner task trees include dependencies and conflicts so independent coding tasks can run in parallel.
- **CLI-owned commits and pull requests**: the CLI stages, commits, pushes, checks, repairs, and merges from validated role handoffs.
- **AI sprint PRs**: planner output is scoped to a coherent autonomous development sprint, not a small review-sized batch.
- **Auditable runtime state**: prompts, effective config, event logs, errors, PR state, check output, and run state are stored under `.loop/`.
- **GitHub context memory**: recent PRs, Issues, and comments are cached in `.loop/loop.db` and searched on demand.
- **Skills instead of giant prompts**: `loop init` delegates default skill installation to `npx skills` while the CLI injects only a compact bootstrap into each agent run.

## Quick Start

Prerequisites
- Initialized Git repository
- Node.js/npm with `npx` available for default skill installation.
- A Codex CLI on `PATH`; the built-in default adapter runs `codex exec --json`.
- GitHub CLI (`gh`) authenticated for pull request mode, GitHub Issues, and GitHub-backed memory sync.
- Go 1.25 or newer

Install `loop` with Go:

```sh
go install github.com/aki-0421/loop/cmd/loop@latest
```


Initialize a Git repository for loop:

```sh
loop init
```

Update `.loop/config.yaml` after initialization so the Codex adapter runs with the intended model, reasoning effort, approval policy:

```yaml
version: 1

agent:
  adapters:
    codex:
      args:
        - exec
        - --json
        - -m
        - gpt-5.5
        - -c
        - model_reasoning_effort="high"
        - -c
        - approval_policy="never"
        - -s
        - danger-full-access
```

Write an instruction file:

```md
# Instructions

You are acting as a senior coding agent for this repository.

## Goal

Build the product according to the repository source of truth.

## Source of truth

- Use the specifications and documents under `docs/` as the primary source of truth.
- Check the existing codebase, tests, examples, and nearby repository documents before assuming new behavior.
- Read only the documents and code relevant to the selected AI sprint scope.
- Do not restate repository documentation in this prompt; navigate to it when needed.

## Coding policy

...
...
```

Run the loop:


```sh
loop run task.md \
  --pr
```


## How It Works

1. `loop run` loads config, validates the instruction file, creates a run id, and starts iteration `0001`.
2. The CLI creates the iteration branch and worktree before any agent starts.
3. The planner agent explores the repository and writes a strict AI sprint-level `task-tree` handoff with dependencies and conflicts.
4. The CLI schedules ready tasks, creates one worktree per coding task, and runs non-conflicting coding agents in parallel.
5. Each coding agent edits files and writes a `task-result`; the CLI commits the work from task metadata and squash-merges it into the iteration branch.
6. The CLI runs configured validation, then asks the review agent for a `review-result`.
7. Validation failures or review findings become repair tasks until the review passes or the fix-cycle limit is reached.
8. The CLI creates the PR, waits for checks, performs configured merge behavior, cleans up worktrees and branches, and starts the next iteration when needed.

In automated PR mode, checks passing leads to a CLI-owned squash merge. Post-hoc review happens after or outside the autonomous development loop and is not a PR sizing constraint.

## Commands

Human-facing commands:

| Command | Purpose |
| --- | --- |
| `loop init` | Create repository-local config and install the default skill through `npx skills`. |
| `loop run <instruction.md>` | Run one or more automated coding iterations. |
| `loop version` | Print build version, commit, and date. |

Agent-facing commands are included in the compact agent help:

```sh
loop help agent
```

Run `loop help <command>` for human-facing help.

## Configuration

`loop init` writes a minimal `.loop/config.yaml`; built-in defaults supply the rest.

```yaml
version: 1

run:
  maxIterations: 3

validation:
  commands:
    - name: test
      run: make test
      required: true
```

Pull request mode is the built-in default. Use local merge mode only for fully local repositories or tests:

```yaml
version: 1

git:
  integration:
    mode: local_merge
```

Use another agent by adding an adapter:

```yaml
version: 1

agent:
  default: claude
  adapters:
    claude:
      command: claude
      prompt: stdin
```

Configuration is layered from built-in defaults, user config, repository config, `LOOP_` environment variables, and command flags. See [Configuration](spec/03-configuration.md) for the full contract.

## Runtime Files

Default Codex setup files:

```text
.loop/config.yaml
.loop/.gitignore
skills-lock.json
.agents/skills/loop/SKILL.md
```

Ignored runtime files:

```text
.loop/runs/
.loop/worktrees/
.loop/tmp/
.loop/locks/
.loop/loop.db
```

The durable iteration directory keeps audit files such as `prompt.md`, `effective-config.yaml`, `agent-events.jsonl`, `task-tree.json`, `task-results/`, `review-result.json`, `errors.log`, `pr-state.json`, `pr-checks.json`, and `github-updates.md`. Disposable active-work artifacts such as runtime context, validation output, and prompt audits are accessed through CLI commands while the iteration is running.

## Development

Use `make` as the entry point:

```sh
make test
make build
make verify
make ci
```

Create a local snapshot build:

```sh
make release-snapshot
```

## Project Docs

- [Specification index](spec/00-index.md)
- [CLI contract](spec/02-cli.md)
- [Iteration workflow](spec/06-iteration-workflow.md)
- [Git and PR workflow](spec/07-git-and-pr.md)
- [Memory and logs](spec/08-memory-and-logs.md)
- [Go implementation notes](spec/13-go-implementation.md)
- [Pull request template](.github/PULL_REQUEST_TEMPLATE.md)
