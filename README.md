# loop

`loop` is a command-line tool for running and managing automated coding iterations in a Git repository.

It keeps iteration state, logs, artifacts, skills, and validation results close to the project so repeated agent work can be resumed, inspected, and reviewed.

## Installation

Download the archive for your platform from the GitHub Releases page, extract it, and place the `loop` binary somewhere on your `PATH`.

Available release archives use this naming pattern:

```text
loop_<version>_<os>_<arch>
```

## Usage

```sh
loop help
loop init
loop doctor
loop run
loop status
loop resume
```

Run `loop help <command>` for detailed help for a specific command.

## Development

Run the test suite:

```sh
make test
```

Build and verify the local binary:

```sh
make ci
```

Create a local snapshot build with GoReleaser:

```sh
make release-snapshot
```
