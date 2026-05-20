package cli

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aki-0421/loop/internal/config"
	"github.com/aki-0421/loop/internal/gitx"
)

func TestCommitCommandCreatesValidatedCommit(t *testing.T) {
	ctx := context.Background()
	repo := newCleanupRepo(t)
	mustWrite(t, filepath.Join(repo, "weather.txt"), "sunny\n")
	oldwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(repo); err != nil {
		t.Fatal(err)
	}
	defer func() {
		_ = os.Chdir(oldwd)
	}()

	out, err := captureStdout(t, func() error {
		return commandCommit(ctx, globals{}, []string{"--type", "F", "add", "weather", "app"})
	})
	if err != nil {
		t.Fatalf("loop commit: %v", err)
	}
	if !strings.Contains(out, "F: add weather app") {
		t.Fatalf("commit output = %q", out)
	}
	if got := git(t, repo, "log", "--format=%s", "-1"); strings.TrimSpace(got) != "F: add weather app" {
		t.Fatalf("commit subject = %q", got)
	}
	if got := strings.TrimSpace(git(t, repo, "status", "--short")); got != "" {
		t.Fatalf("working tree should be clean, status = %q", got)
	}
}

func TestCommitCommandRejectsInvalidMessageBeforeCommitting(t *testing.T) {
	ctx := context.Background()
	repo := newCleanupRepo(t)
	before := strings.TrimSpace(git(t, repo, "rev-parse", "HEAD"))
	mustWrite(t, filepath.Join(repo, "weather.txt"), "rain\n")
	oldwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(repo); err != nil {
		t.Fatal(err)
	}
	defer func() {
		_ = os.Chdir(oldwd)
	}()

	err = commandCommit(ctx, globals{}, []string{"--type", "F", "Add weather app."})
	if err == nil {
		t.Fatal("loop commit should reject invalid message")
	}
	for _, want := range []string{"lowercase", "period"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("error missing %q: %v", want, err)
		}
	}
	after := strings.TrimSpace(git(t, repo, "rev-parse", "HEAD"))
	if after != before {
		t.Fatalf("HEAD changed after rejected commit: before=%s after=%s", before, after)
	}
}

func TestCommitCommandRequiresTypeFlag(t *testing.T) {
	err := commandCommit(context.Background(), globals{}, []string{"F", "add", "weather", "app"})
	if err == nil {
		t.Fatal("loop commit should reject positional type")
	}
	if !strings.Contains(err.Error(), "--type") {
		t.Fatalf("error should mention --type: %v", err)
	}
}

func TestValidateIterationCommitSubjectsReportsBadCommit(t *testing.T) {
	cfg := config.Defaults()
	err := validateIterationCommitSubjects([]gitx.Commit{{Hash: "abc1234567890", Subject: "bad message."}}, cfg)
	if err == nil {
		t.Fatal("expected bad commit subject to fail")
	}
	if !strings.Contains(err.Error(), "bad message.") {
		t.Fatalf("error = %v", err)
	}
}
