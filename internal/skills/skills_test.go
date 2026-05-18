package skills

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/aki-0421/loop/internal/config"
)

func TestInstallDefaultsDoesNotOverwriteUnlessForced(t *testing.T) {
	repo := t.TempDir()
	result, err := InstallDefaults(repo, CanonicalProjectDir, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Installed) != 1 {
		t.Fatalf("installed %d skills, want 1: %+v", len(result.Installed), result)
	}
	path := filepath.Join(repo, ".agents", "skills", "loop", "SKILL.md")
	if err := os.WriteFile(path, []byte("custom"), 0o644); err != nil {
		t.Fatal(err)
	}
	result, err = InstallDefaults(repo, CanonicalProjectDir, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Skipped) != 1 {
		t.Fatalf("skipped %d skills, want 1: %+v", len(result.Skipped), result)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "custom" {
		t.Fatal("customized skill was overwritten without force")
	}
	if _, err := InstallBuiltIn(repo, CanonicalProjectDir, "loop", true); err != nil {
		t.Fatal(err)
	}
	data, err = os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "name: loop") {
		t.Fatal("force install did not restore built-in skill")
	}
}

func TestDoctorValidatesFrontMatter(t *testing.T) {
	repo := t.TempDir()
	dir := filepath.Join(repo, ".agents", "skills")
	if _, err := InstallBuiltIn(repo, CanonicalProjectDir, "loop", false); err != nil {
		t.Fatal(err)
	}
	report := Doctor(dir)
	if !report.OK() {
		t.Fatalf("valid skill failed doctor: %+v", report)
	}

	bad := filepath.Join(repo, ".agents", "skills", "bad-skill", "SKILL.md")
	mustWrite(t, bad, `---
name: wrong
description: ""
version: 2
---
# bad
`)
	report = Doctor(filepath.Join(repo, ".agents", "skills"))
	if report.OK() {
		t.Fatal("bad skill passed doctor")
	}
	joined := strings.Join(report.Errors, "\n")
	for _, want := range []string{"name must match directory", "description is required", "version must be 1"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("doctor errors missing %q:\n%s", want, joined)
		}
	}
}

func TestSyncCopyOffAndSymlink(t *testing.T) {
	repo := t.TempDir()
	if _, err := InstallBuiltIn(repo, CanonicalProjectDir, "loop", false); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.Default()
	if err != nil {
		t.Fatal(err)
	}
	cfg.Skills.SourceDir = CanonicalProjectDir
	cfg.Skills.Targets = map[string]config.SkillTarget{
		"codex": {Mode: "copy", Path: filepath.Join(".codex", "skills")},
		"noop":  {Mode: "off"},
	}
	if runtime.GOOS != "windows" {
		cfg.Skills.Targets["links"] = config.SkillTarget{Mode: "symlink", Path: filepath.Join(".links", "skills")}
	}
	results, err := Sync(repo, cfg)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != len(cfg.Skills.Targets) {
		t.Fatalf("sync results = %+v", results)
	}
	copied := filepath.Join(repo, ".codex", "skills", "loop", "SKILL.md")
	data, err := os.ReadFile(copied)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "name: loop") {
		t.Fatalf("copied skill content wrong:\n%s", string(data))
	}
	if runtime.GOOS != "windows" {
		info, err := os.Lstat(filepath.Join(repo, ".links", "skills", "loop"))
		if err != nil {
			t.Fatal(err)
		}
		if info.Mode()&os.ModeSymlink == 0 {
			t.Fatal("expected symlinked skill target")
		}
	}
	if _, err := os.Stat(filepath.Join(repo, "noop")); !os.IsNotExist(err) {
		t.Fatalf("off target should not create a path, err=%v", err)
	}
}

func TestPreferredInstallDirUsesExistingAgentSkillDir(t *testing.T) {
	repo := t.TempDir()
	cfg, err := config.Default()
	if err != nil {
		t.Fatal(err)
	}
	existing := filepath.Join(repo, ".codex", "skills")
	if err := os.MkdirAll(existing, 0o755); err != nil {
		t.Fatal(err)
	}
	dir := PreferredInstallDir(repo, cfg)
	if dir != existing {
		t.Fatalf("preferred dir = %s, want %s", dir, existing)
	}
	if _, err := InstallDefaults(repo, dir, false); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(repo, ".loop", "skills")); !os.IsNotExist(err) {
		t.Fatalf(".loop/skills should not be created, err=%v", err)
	}
	if _, err := os.Stat(filepath.Join(existing, "loop", "SKILL.md")); err != nil {
		t.Fatal(err)
	}
}

func TestListDiscoveredMergesAgentSkillDirs(t *testing.T) {
	repo := t.TempDir()
	cfg, err := config.Default()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := InstallBuiltIn(repo, filepath.Join(".codex", "skills"), "loop", false); err != nil {
		t.Fatal(err)
	}
	mustWrite(t, filepath.Join(repo, CanonicalProjectDir, "project-helper", "SKILL.md"), `---
name: project-helper
description: Project helper skill.
version: 1
---
# project-helper
`)
	list, err := ListDiscovered(repo, cfg)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, skill := range list {
		names = append(names, skill.Name)
		if filepath.IsAbs(skill.Path) {
			t.Fatalf("skill path should be relative, got %s", skill.Path)
		}
	}
	got := strings.Join(names, ",")
	if got != "loop,project-helper" {
		t.Fatalf("discovered skills = %s", got)
	}
}

func mustWrite(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}
