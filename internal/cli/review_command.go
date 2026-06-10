package cli

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/aki-0421/loop/internal/artifactdb"
	"github.com/aki-0421/loop/internal/workflow"
)

const reviewFindingKind = "review-finding"

func commandReview(ctx context.Context, g globals, args []string) error {
	if len(args) == 0 {
		return codedError{2, fmt.Errorf("usage: loop review <finding> ...")}
	}
	switch args[0] {
	case "finding":
		return commandReviewFinding(ctx, g, args[1:])
	default:
		return codedError{2, fmt.Errorf("unknown review subcommand %q", args[0])}
	}
}

func commandReviewFinding(ctx context.Context, g globals, args []string) error {
	if len(args) == 0 {
		return codedError{2, fmt.Errorf("usage: loop review finding <add|list|clear> ...")}
	}
	switch args[0] {
	case "add":
		return commandReviewFindingAdd(ctx, g, args[1:])
	case "list":
		return commandReviewFindingList(ctx, g, args[1:])
	case "clear":
		return commandReviewFindingClear(ctx, g, args[1:])
	default:
		return codedError{2, fmt.Errorf("unknown review finding subcommand %q", args[0])}
	}
}

func commandReviewFindingAdd(ctx context.Context, g globals, args []string) error {
	var acceptance stringListFlag
	fs := flag.NewFlagSet("review finding add", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	iterDir := fs.String("iteration-dir", "", "iteration directory")
	dirAlias := fs.String("dir", "", "iteration directory")
	runID := fs.String("run", os.Getenv("LOOP_RUN_ID"), "run id")
	iteration := fs.String("iteration", defaultIterationEnv(), "iteration id")
	id := fs.String("id", "", "finding id")
	taskID := fs.String("task", "", "related task id")
	title := fs.String("title", "", "finding title")
	description := fs.String("description", "", "finding description")
	descriptionFile := fs.String("description-file", "", "path to finding description")
	fs.Var(&acceptance, "acceptance", "acceptance criterion; repeatable")
	args = flagsFirst(args, map[string]bool{
		"iteration-dir": true, "dir": true, "run": true, "iteration": true,
		"id": true, "task": true, "title": true, "description": true,
		"description-file": true, "acceptance": true,
	})
	if err := fs.Parse(args); err != nil {
		return codedError{2, err}
	}
	if *dirAlias != "" {
		*iterDir = *dirAlias
	}
	if fs.NArg() != 0 || strings.TrimSpace(*id) == "" || strings.TrimSpace(*title) == "" || len(acceptance) == 0 {
		return codedError{2, fmt.Errorf("usage: loop review finding add --id <id> --title <title> (--description <text>|--description-file <path>) --acceptance <text>...")}
	}
	desc, err := readReviewFindingDescription(*description, *descriptionFile)
	if err != nil {
		return codedError{2, err}
	}
	finding := workflow.ReviewFinding{
		ID:          strings.TrimSpace(*id),
		TaskID:      strings.TrimSpace(*taskID),
		Title:       strings.TrimSpace(*title),
		Description: strings.TrimSpace(desc),
		Acceptance:  append([]string(nil), acceptance...),
	}
	if err := validateReviewFinding(finding); err != nil {
		return codedError{2, err}
	}
	resolvedDir, err := resolveIterationDir(ctx, g, *iterDir, *runID, *iteration)
	if err != nil {
		return codedError{1, err}
	}
	run, iter, globalPath, err := reviewFindingStorage(resolvedDir, *runID, *iteration)
	if err != nil {
		return codedError{2, err}
	}
	data, err := workflow.MarshalIndent(finding)
	if err != nil {
		return codedError{1, err}
	}
	if err := artifactdb.WriteRoleHandoff(globalPath, run, iter, reviewFindingKind, finding.ID, string(data)); err != nil {
		return codedError{1, err}
	}
	if err := writeReviewFindingsAudit(resolvedDir, globalPath, run, iter); err != nil {
		return codedError{1, err}
	}
	return printResult(g, map[string]any{"finding": finding.ID}, fmt.Sprintf("recorded review finding %s\n", finding.ID))
}

func commandReviewFindingList(ctx context.Context, g globals, args []string) error {
	fs := flag.NewFlagSet("review finding list", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	iterDir := fs.String("iteration-dir", "", "iteration directory")
	dirAlias := fs.String("dir", "", "iteration directory")
	runID := fs.String("run", os.Getenv("LOOP_RUN_ID"), "run id")
	iteration := fs.String("iteration", defaultIterationEnv(), "iteration id")
	args = flagsFirst(args, map[string]bool{"iteration-dir": true, "dir": true, "run": true, "iteration": true})
	if err := fs.Parse(args); err != nil {
		return codedError{2, err}
	}
	if *dirAlias != "" {
		*iterDir = *dirAlias
	}
	if fs.NArg() != 0 {
		return codedError{2, fmt.Errorf("usage: loop review finding list [--iteration-dir <dir>|--run <run-id> --iteration <n>]")}
	}
	resolvedDir, err := resolveIterationDir(ctx, g, *iterDir, *runID, *iteration)
	if err != nil {
		return codedError{1, err}
	}
	run, iter, globalPath, err := reviewFindingStorage(resolvedDir, *runID, *iteration)
	if err != nil {
		return codedError{2, err}
	}
	findings, err := readRecordedReviewFindings(globalPath, run, iter)
	if err != nil {
		return codedError{1, err}
	}
	var lines strings.Builder
	for _, finding := range findings {
		lines.WriteString(finding.ID)
		lines.WriteString("\t")
		lines.WriteString(finding.Title)
		lines.WriteString("\n")
	}
	return printResult(g, map[string]any{"findings": findings}, lines.String())
}

func commandReviewFindingClear(ctx context.Context, g globals, args []string) error {
	fs := flag.NewFlagSet("review finding clear", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	iterDir := fs.String("iteration-dir", "", "iteration directory")
	dirAlias := fs.String("dir", "", "iteration directory")
	runID := fs.String("run", os.Getenv("LOOP_RUN_ID"), "run id")
	iteration := fs.String("iteration", defaultIterationEnv(), "iteration id")
	args = flagsFirst(args, map[string]bool{"iteration-dir": true, "dir": true, "run": true, "iteration": true})
	if err := fs.Parse(args); err != nil {
		return codedError{2, err}
	}
	if *dirAlias != "" {
		*iterDir = *dirAlias
	}
	if fs.NArg() > 1 {
		return codedError{2, fmt.Errorf("usage: loop review finding clear [id] [--iteration-dir <dir>|--run <run-id> --iteration <n>]")}
	}
	id := ""
	if fs.NArg() == 1 {
		id = strings.TrimSpace(fs.Arg(0))
	}
	resolvedDir, err := resolveIterationDir(ctx, g, *iterDir, *runID, *iteration)
	if err != nil {
		return codedError{1, err}
	}
	run, iter, globalPath, err := reviewFindingStorage(resolvedDir, *runID, *iteration)
	if err != nil {
		return codedError{2, err}
	}
	if id != "" {
		if err := artifactdb.ClearRoleHandoff(globalPath, run, iter, reviewFindingKind, id); err != nil {
			return codedError{1, err}
		}
	} else if err := clearRecordedReviewFindings(globalPath, run, iter); err != nil {
		return codedError{1, err}
	}
	if err := writeReviewFindingsAudit(resolvedDir, globalPath, run, iter); err != nil {
		return codedError{1, err}
	}
	return printResult(g, map[string]any{"cleared": firstNonEmpty(id, "all")}, "cleared review findings\n")
}

func readReviewFindingDescription(inline, file string) (string, error) {
	inline = strings.TrimSpace(inline)
	file = strings.TrimSpace(file)
	if inline != "" && file != "" {
		return "", errors.New("--description and --description-file cannot be combined")
	}
	if file != "" {
		data, err := os.ReadFile(file)
		if err != nil {
			return "", err
		}
		return string(data), nil
	}
	if inline == "" {
		return "", errors.New("--description or --description-file is required")
	}
	return inline, nil
}

func validateReviewFinding(finding workflow.ReviewFinding) error {
	result := workflow.ReviewResult{
		SchemaVersion:  workflow.SchemaVersion,
		Status:         "changes_requested",
		Summary:        "Review finding.",
		GoalEvaluation: "Review finding.",
		Findings:       []workflow.ReviewFinding{finding},
	}
	if problems := workflow.ValidateReviewResult(result); len(problems) > 0 {
		for i, problem := range problems {
			problems[i] = strings.TrimPrefix(problem, "findings[0].")
		}
		return fmt.Errorf("invalid review finding: %s", strings.Join(problems, "; "))
	}
	return nil
}

func reviewFindingStorage(iterDir, runID, iteration string) (string, string, string, error) {
	if strings.TrimSpace(iterDir) == "" {
		return "", "", "", errors.New("iteration directory is required")
	}
	run, iter := artifactdb.ParseIterationDir(iterDir)
	run = firstNonEmpty(run, strings.TrimSpace(runID))
	iter = firstNonEmpty(iter, strings.TrimSpace(iteration))
	if run == "" || iter == "" || iter == "latest" {
		return "", "", "", errors.New("run id and concrete iteration id are required")
	}
	globalPath := artifactdb.GlobalDBPathForIteration(iterDir)
	if globalPath == "" {
		return "", "", "", errors.New("iteration directory must be under the loop runs directory")
	}
	return run, iter, globalPath, nil
}

func readRecordedReviewFindings(globalPath, runID, iterationID string) ([]workflow.ReviewFinding, error) {
	handoffs, err := artifactdb.ListRoleHandoffs(globalPath, runID, iterationID, reviewFindingKind)
	if err != nil {
		return nil, err
	}
	findings := make([]workflow.ReviewFinding, 0, len(handoffs))
	for _, handoff := range handoffs {
		var finding workflow.ReviewFinding
		dec := json.NewDecoder(strings.NewReader(handoff.Payload))
		dec.DisallowUnknownFields()
		if err := dec.Decode(&finding); err != nil {
			return nil, err
		}
		if err := validateReviewFinding(finding); err != nil {
			return nil, err
		}
		findings = append(findings, finding)
	}
	return findings, nil
}

func clearRecordedReviewFindings(globalPath, runID, iterationID string) error {
	handoffs, err := artifactdb.ListRoleHandoffs(globalPath, runID, iterationID, reviewFindingKind)
	if err != nil {
		return err
	}
	for _, handoff := range handoffs {
		if err := artifactdb.ClearRoleHandoff(globalPath, runID, iterationID, reviewFindingKind, handoff.TaskID); err != nil {
			return err
		}
	}
	return nil
}

func writeReviewFindingsAudit(iterDir, globalPath, runID, iterationID string) error {
	findings, err := readRecordedReviewFindings(globalPath, runID, iterationID)
	if err != nil {
		return err
	}
	data, err := workflow.MarshalIndent(struct {
		SchemaVersion int                      `json:"schema_version"`
		Findings      []workflow.ReviewFinding `json:"findings"`
	}{
		SchemaVersion: workflow.SchemaVersion,
		Findings:      findings,
	})
	if err != nil {
		return err
	}
	if err := os.MkdirAll(iterDir, 0o755); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(iterDir, "review-findings.json"), data, 0o644)
}

func applyRecordedReviewFindings(review workflow.ReviewResult, recorded []workflow.ReviewFinding) workflow.ReviewResult {
	if len(recorded) == 0 {
		return review
	}
	merged := make([]workflow.ReviewFinding, 0, len(recorded)+len(review.Findings))
	index := map[string]int{}
	for _, finding := range recorded {
		key := finding.ID
		index[key] = len(merged)
		merged = append(merged, finding)
	}
	for _, finding := range review.Findings {
		key := finding.ID
		if i, ok := index[key]; ok {
			merged[i] = finding
			continue
		}
		index[key] = len(merged)
		merged = append(merged, finding)
	}
	review.Findings = merged
	review.GoalComplete = false
	if review.Status == "approved" {
		review.Status = "changes_requested"
		review.Summary = fmt.Sprintf("Changes requested from %d recorded review finding(s).", len(merged))
	}
	return review
}

func syntheticReviewResultFromFindings(findings []workflow.ReviewFinding) workflow.ReviewResult {
	return workflow.ReviewResult{
		SchemaVersion:  workflow.SchemaVersion,
		Status:         "changes_requested",
		Summary:        fmt.Sprintf("Changes requested from %d recorded review finding(s).", len(findings)),
		GoalEvaluation: "The iteration requires repair before approval.",
		Findings:       findings,
	}
}
