package skills

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/aki-0421/loop/internal/config"
)

func TestInstallFromPathDoesNotOverwriteUnlessForced(t *testing.T) {
	repo := t.TempDir()
	source := filepath.Join(repo, "source", "SKILL.md")
	mustWrite(t, source, `---
name: loop
description: Loop skill.
version: 1
---
# loop
`)
	path, err := InstallFromPath(source, repo, CanonicalProjectDir, false)
	if err != nil {
		t.Fatal(err)
	}
	wantPath := filepath.Join(repo, ".agents", "skills", "loop", "SKILL.md")
	if path != wantPath {
		t.Fatalf("installed path = %s, want %s", path, wantPath)
	}
	if _, err := InstallFromPath(source, repo, CanonicalProjectDir, false); err == nil {
		t.Fatal("expected duplicate install to require --force")
	} else if !strings.Contains(err.Error(), "already exists") {
		t.Fatalf("duplicate error = %v", err)
	}
	forcedSource := filepath.Join(repo, "forced", "SKILL.md")
	mustWrite(t, forcedSource, `---
name: loop
description: Forced loop skill.
version: 1
---
# loop
`)
	if _, err := InstallFromPath(forcedSource, repo, CanonicalProjectDir, true); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "Forced loop skill.") {
		t.Fatal("force install did not overwrite existing skill")
	}
}

func TestDoctorValidatesFrontMatter(t *testing.T) {
	repo := t.TempDir()
	dir := filepath.Join(repo, ".agents", "skills")
	writeLoopSkill(t, repo, CanonicalProjectDir)
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
	writeLoopSkill(t, repo, CanonicalProjectDir)
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
	if _, err := os.Stat(filepath.Join(repo, ".loop", "skills")); !os.IsNotExist(err) {
		t.Fatalf(".loop/skills should not be created, err=%v", err)
	}
}

func TestListDiscoveredMergesAgentSkillDirs(t *testing.T) {
	repo := t.TempDir()
	cfg, err := config.Default()
	if err != nil {
		t.Fatal(err)
	}
	writeLoopSkill(t, repo, filepath.Join(".codex", "skills"))
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

func writeLoopSkill(t *testing.T, repoRoot, dir string) string {
	t.Helper()
	base := dir
	if !filepath.IsAbs(base) {
		base = filepath.Join(repoRoot, dir)
	}
	path := filepath.Join(base, "loop", "SKILL.md")
	mustWrite(t, path, `---
name: loop
description: Loop skill.
version: 1
---
# loop
`)
	return path
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
