# Security and Control Boundaries

## Repository scope

`loop` runs inside one Git repository. It must not read or write outside the repository except for:

- User config under `~/.config/loop/`.
- Agent-specific skill targets configured by the user.
- Temporary files inside system temp directories when required by the OS.

## Shell commands

The CLI runs shell commands for Git, `gh`, validation, and agent launch. Commands come from configuration or built-in Git and `gh` operations.
Validation command strings are executed through the user's default shell when available, not through a hard-coded POSIX shell. This makes command lookup follow the user's configured shell environment more closely, while keeping the configured command string visible in logs.

Rules:

- Log every command name and working directory.
- Redact configured environment keys.
- Do not log complete environment maps.
- Preserve command output in iteration logs.
- Respect configured timeouts.

## Automated run control

The CLI and agent perform configured operations without asking the user. This is the only run behavior.

To keep behavior inspectable, every action must be recorded as an event. Push, pull request creation, check waiting, merge, cleanup, agent command execution, and agent file reads must appear in the iteration event log, a task event log, or a CLI event stream. Agent thinking, raw file contents, and raw diffs must not be persisted.

## File protection

The CLI may reject or pause destructive operations based on configuration:

- Deleting tracked files outside the intended change set.
- Modifying `.git/` internals directly.
- Rewriting the base branch history.
- Removing runtime logs before run completion.
- Running configured forbidden commands.

## Integration guard

Integration requires:

- A clean working tree except ignored runtime files.
- Valid merge close JSON.
- Required validation commands not failed.
- A non-empty summary sentence for changed iterations.
- A branch name matching configured branch rules.

## Secret handling

The agent may see repository files and command output. `loop` does not claim to sandbox the agent. The CLI must avoid adding extra secrets to the agent environment unless configured.

Redaction patterns are applied to logs only. Redaction does not prevent the agent from using environment variables that were intentionally passed to it.
