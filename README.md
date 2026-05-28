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

`loop` turns one Markdown instruction file into a controlled sequence of agent runs. Each iteration happens in its own Git worktree, asks the agent to plan and commit reviewable units of work, validates the result, then either integrates the branch or cleans it up.

It is built for repositories where AI work should leave behind the same things good human work does: small commits, clear branches, validation evidence, pull request context, and enough logs to explain what happened later.

## Features

- **Repeatable iterations**: run until a CLI-provided goal is satisfied, an iteration limit is reached, or a terminal error stops the run.
- **Git-native isolation**: every iteration starts on a temporary branch in a separate worktree, then integrates by local squash merge or pull request.
- **Validated commits**: agents create commits through `loop commit`, which stages changes and enforces the repository's commit contract.
- **Agent-facing workflow commands**: agents use `loop iteration`, `loop branch`, `loop pr`, `loop issue`, and `loop memory` instead of guessing file paths or lifecycle details.
- **Pull request mode**: agents can create PRs, wait for checks, fetch failed job logs, fix failures, and merge through `gh`.
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
- Read only the documents and code relevant to the selected iteration slice.
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
2. The CLI creates an isolated worktree on a temporary `wip/<iteration>` branch.
3. The configured agent receives the instruction plus a compact loop skill bootstrap.
4. The agent writes a plan and TODOs, edits the repository, renames the branch through `loop branch rename`, and creates commits through `loop commit`.
5. The agent closes the iteration with `loop iteration close --merge` or `loop iteration close --skip-merge`.
6. The CLI validates the close handoff, runs configured validation, integrates merge closes, cleans up worktrees and branches, and starts the next iteration when needed.

In PR mode, the agent creates and manages the pull request through `loop pr create`, `loop pr checks`, `loop pr logs`, and `loop pr merge` before closing with `--merge`.

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

Use pull request mode by config when every run should go through GitHub:

```yaml
version: 1

git:
  integration:
    mode: pr
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

The durable iteration directory keeps audit files such as `prompt.md`, `effective-config.yaml`, `agent-events.jsonl`, `errors.log`, `pr-state.json`, `pr-checks.json`, and `github-updates.md`. Disposable active-work artifacts such as plans, TODOs, validation output, and PR draft text are accessed through `loop iteration` commands while the iteration is running.

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
