# Contributing to loop

Thanks for considering a contribution to `loop`.

`loop` is a Git-native autonomous coding harness. Changes should preserve the core contract: the CLI owns orchestration, Git lifecycle operations, pull request lifecycle operations, durable artifacts, validation, and cleanup.

## Before You Start

- Read [README.md](README.md) for the product overview.
- Read [spec/00-index.md](spec/00-index.md) to find the relevant specification.
- Keep changes scoped to one coherent behavior or documentation improvement.
- Use English for repository content, including docs, comments, tests, examples, and generated templates.

## Development

Use `make` as the entry point:

```sh
make test
make build
make verify
make ci
```

Do not run `go test` or `go build` directly by default. If a needed command is missing, prefer adding a Make target.

## Pull Requests

Good pull requests include:

- A focused summary of the behavior or documentation change.
- The validation commands you ran.
- Any relevant `.loop/` artifact paths or screenshots when the change affects runtime behavior or the terminal renderer.
- Notes about compatibility, migration, or config changes when applicable.

For changes involving branches, commits, task worktrees, pull requests, review modes, or merge behavior, add or update Git integration tests where practical.

## Implementation Guidelines

- Follow the existing Go package structure and Makefile workflow.
- Keep planner, coding, QA review, and merge roles distinct.
- Keep iteration and pull request concepts distinct.
- Preserve CLI-owned Git and GitHub lifecycle boundaries.
- Keep SQLite-backed tests CGO-free by default.
- Avoid unrelated refactors and generated-file churn.

## License

By contributing to this repository, you agree that your contributions are licensed under the [Apache License 2.0](LICENSE).
