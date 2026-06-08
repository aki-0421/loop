package main

import (
	"fmt"
	"os"
	"runtime/debug"
	"strings"

	"github.com/aki-0421/loop/internal/cli"
)

var (
	version = "dev"
	commit  = "none"
	date    = "unknown"
)

func main() {
	if isVersionRequest(os.Args[1:]) {
		v, c, d := resolvedBuildInfo()
		fmt.Printf("loop %s (commit %s, built %s)\n", v, c, d)
		return
	}
	if err := cli.Run(os.Args[1:]); err != nil {
		if code, ok := cli.ExitCode(err); ok {
			if err.Error() != "" {
				fmt.Fprintln(os.Stderr, err)
			}
			os.Exit(code)
		}
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func isVersionRequest(args []string) bool {
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if arg == "version" || arg == "--version" || arg == "-v" {
			return true
		}
		if !strings.HasPrefix(arg, "--") {
			return false
		}
		name, _, hasValue := strings.Cut(strings.TrimPrefix(arg, "--"), "=")
		if hasValue {
			continue
		}
		switch name {
		case "agent", "config", "cwd", "log-level":
			i++
			if i >= len(args) {
				return false
			}
		case "json", "no-color", "help":
		default:
			return false
		}
	}
	return false
}

func resolvedBuildInfo() (string, string, string) {
	v := strings.TrimSpace(version)
	c := strings.TrimSpace(commit)
	d := strings.TrimSpace(date)

	if info, ok := debug.ReadBuildInfo(); ok {
		if isUnsetVersion(v) && info.Main.Version != "" && info.Main.Version != "(devel)" {
			v = info.Main.Version
		}
		settings := buildSettings(info.Settings)
		if isUnsetCommit(c) {
			c = settings["vcs.revision"]
		}
		if isUnsetDate(d) {
			d = settings["vcs.time"]
		}
		if settings["vcs.modified"] == "true" && v != "" && !strings.HasSuffix(v, "-dirty") {
			v += "-dirty"
		}
	}

	if v == "" {
		v = "dev"
	}
	if c == "" {
		c = "unknown"
	}
	if d == "" {
		d = "unknown"
	}
	return v, c, d
}

func buildSettings(settings []debug.BuildSetting) map[string]string {
	out := make(map[string]string, len(settings))
	for _, setting := range settings {
		out[setting.Key] = setting.Value
	}
	return out
}

func isUnsetVersion(v string) bool {
	return v == "" || v == "dev"
}

func isUnsetCommit(c string) bool {
	return c == "" || c == "none" || c == "unknown"
}

func isUnsetDate(d string) bool {
	return d == "" || d == "unknown"
}
