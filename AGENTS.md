# AGENTS.md

## Table of Contents

- [Language](#language)
- [General Guidance](#general-guidance)
- [Development Commands](#development-commands)
- [Working Notes](#working-notes)

## Language

All repository content must be written in English. This includes documentation, comments, tests, fixtures, examples, user-facing text, and generated templates committed to the repository.

## General Guidance

Agents working in this repository should follow the existing Go implementation, specifications, and Makefile structure. Keep changes scoped to the requested work and avoid unrelated refactors or generated-file churn.

Use [README.md](README.md) and the [specification index](spec/00-index.md) as the starting points for repository documentation navigation.

## Development Commands

Use `make` as the entry point for development commands. Do not run `go test` or `go build` directly by default; prefer these Make targets:

- `make test`: Run the full test suite.
- `make build`: Build the CLI binary.
- `make verify`: Check the basic behavior of the built CLI.
- `make ci`: Run tests, build, and verification together.
- `make clean`: Remove build artifacts.

If a needed development command is missing from the Makefile, consider adding an appropriate target instead of relying on one-off commands.

## Working Notes

- Follow the testing guidance in `spec/12-testing.md`.
- For changes involving commits or branch operations, prioritize Git integration tests.
- SQLite-backed tests should follow the Makefile settings and must stay CGO-free by default.
