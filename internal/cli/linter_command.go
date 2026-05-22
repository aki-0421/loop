package cli

import (
	"context"
	"flag"
	"fmt"
	"os"
	"strings"

	"github.com/aki-0421/loop/internal/config"
	"github.com/aki-0421/loop/internal/doclint"
	"github.com/aki-0421/loop/internal/gitx"
)

func commandLinter(ctx context.Context, g globals, args []string) error {
	if len(args) == 0 {
		return codedError{2, fmt.Errorf("usage: loop linter <document>")}
	}
	switch args[0] {
	case "document":
		return commandLinterDocument(ctx, g, args[1:])
	default:
		return codedError{2, fmt.Errorf("unknown linter subcommand %q", args[0])}
	}
}

func commandLinterDocument(ctx context.Context, g globals, args []string) error {
	args = flagsFirst(args, map[string]bool{"config": true})
	fs := flag.NewFlagSet("linter document", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	strict := fs.Bool("strict", false, "exit non-zero when document findings are found")
	configPath := fs.String("config", g.ConfigPath, "loop config path")
	if err := fs.Parse(args); err != nil {
		return codedError{2, err}
	}
	if fs.NArg() != 0 {
		return codedError{2, fmt.Errorf("usage: loop linter document [--strict] [--config <path>]")}
	}

	root, err := gitx.RepoRoot(ctx, ".")
	if err != nil {
		return codedError{1, err}
	}
	cfg, err := config.Load(config.LoadOptions{
		CWD:        root,
		ConfigPath: *configPath,
		Overrides:  config.Overrides{Agent: g.Agent, NoColor: g.NoColor},
	})
	if err != nil {
		return codedError{3, err}
	}
	docCfg := cfg.Linter.Document
	report, err := doclint.Analyze(ctx, doclint.Options{
		Root:              root,
		Entry:             docCfg.Entry,
		RequiredReachable: docCfg.RequiredReachable,
		Excludes:          docCfg.Excludes,
	})
	if err != nil {
		if doclint.IsUsageError(err) {
			return codedError{2, err}
		}
		return codedError{1, err}
	}

	value := map[string]any{
		"status":               report.Status,
		"entry":                report.Entry,
		"strict":               *strict,
		"config":               *configPath,
		"required_reachable":   report.RequiredReachable,
		"excludes":             report.Excludes,
		"tracked":              report.Tracked,
		"reachable":            report.Reachable,
		"unreachable_required": report.Unreachable,
		"invalid_references":   report.InvalidReferences,
	}
	if err := printResult(g, value, formatDocumentLintReport(report)); err != nil {
		return err
	}
	if *strict && report.HasFindings() {
		return codedError{1, fmt.Errorf("document linter found %d issue(s)", report.FindingCount())}
	}
	return nil
}

func formatDocumentLintReport(report doclint.Report) string {
	if !report.HasFindings() {
		return "document linter ok\n"
	}
	var b strings.Builder
	fmt.Fprintf(&b, "document linter warning: found %d issue(s)\n", report.FindingCount())
	if len(report.Unreachable) > 0 {
		b.WriteString("\nunreachable required documents:\n")
		for _, file := range report.Unreachable {
			fmt.Fprintf(&b, "  %s\n", file)
		}
	}
	if len(report.InvalidReferences) > 0 {
		b.WriteString("\ninvalid references:\n")
		for _, ref := range report.InvalidReferences {
			fmt.Fprintf(&b, "  %s:%d %s (%s)\n", ref.Source, ref.Line, ref.Target, ref.Reason)
		}
	}
	return b.String()
}
