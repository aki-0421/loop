package skills

import (
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/aki-0421/loop/internal/config"
)

const (
	CanonicalProjectDir = ".agents/skills"
	legacyLoopDir       = ".loop/skills"
)

type Directory struct {
	Agent  string
	Path   string
	Legacy bool
}

func PreferredInstallDir(repoRoot string, cfg config.Config) string {
	for _, dir := range DiscoverDirs(repoRoot, cfg) {
		if !dir.Legacy {
			return dir.Path
		}
	}
	return absDir(repoRoot, CanonicalProjectDir)
}

func DiscoverDirs(repoRoot string, cfg config.Config) []Directory {
	candidates := projectCandidates(cfg)
	seen := map[string]bool{}
	var dirs []Directory
	for _, candidate := range candidates {
		path := absDir(repoRoot, candidate.Path)
		clean := filepath.Clean(path)
		if seen[clean] {
			continue
		}
		seen[clean] = true
		info, err := os.Stat(clean)
		if err != nil || !info.IsDir() {
			continue
		}
		dirs = append(dirs, Directory{Agent: candidate.Agent, Path: clean, Legacy: isLegacyDir(candidate.Path)})
	}
	hasCurrentDir := false
	for _, dir := range dirs {
		if !dir.Legacy {
			hasCurrentDir = true
			break
		}
	}
	if hasCurrentDir {
		filtered := dirs[:0]
		for _, dir := range dirs {
			if !dir.Legacy {
				filtered = append(filtered, dir)
			}
		}
		dirs = filtered
	}
	return dirs
}

func ListDiscovered(repoRoot string, cfg config.Config) ([]Skill, error) {
	dirs := DiscoverDirs(repoRoot, cfg)
	if len(dirs) == 0 {
		dirs = []Directory{{Agent: "project", Path: PreferredInstallDir(repoRoot, cfg)}}
	}
	seen := map[string]bool{}
	var merged []Skill
	for _, dir := range dirs {
		list, err := List(dir.Path)
		if err != nil {
			return nil, err
		}
		for _, skill := range list {
			if seen[skill.Name] {
				continue
			}
			seen[skill.Name] = true
			skill.Path = relPath(repoRoot, skill.Path)
			merged = append(merged, skill)
		}
	}
	sort.Slice(merged, func(i, j int) bool { return merged[i].Name < merged[j].Name })
	return merged, nil
}

func DoctorDiscovered(repoRoot string, cfg config.Config) DoctorReport {
	dirs := DiscoverDirs(repoRoot, cfg)
	if len(dirs) == 0 {
		dirs = []Directory{{Agent: "project", Path: PreferredInstallDir(repoRoot, cfg)}}
	}
	var issues []string
	for _, dir := range dirs {
		report := Doctor(dir.Path)
		issues = append(issues, report.Errors...)
	}
	return DoctorReport{Errors: issues}
}

func projectCandidates(cfg config.Config) []Directory {
	var out []Directory
	add := func(agent, path string) {
		path = strings.TrimSpace(path)
		if path == "" {
			return
		}
		out = append(out, Directory{Agent: agent, Path: filepath.FromSlash(path), Legacy: isLegacyDir(path)})
	}

	if cfg.Skills.SourceDir != "" && !isLegacyDir(cfg.Skills.SourceDir) {
		add("configured", cfg.Skills.SourceDir)
	}
	add("project", CanonicalProjectDir)

	agents := make([]string, 0, len(cfg.Skills.Targets))
	for agent := range cfg.Skills.Targets {
		agents = append(agents, agent)
	}
	sort.Strings(agents)
	for _, agent := range agents {
		add(agent, cfg.Skills.Targets[agent].Path)
	}

	for _, candidate := range []Directory{
		{Agent: "codex", Path: ".codex/skills"},
		{Agent: "claude", Path: ".claude/skills"},
		{Agent: "cline", Path: ".cline/skills"},
		{Agent: "cursor", Path: ".cursor/skills"},
		{Agent: "codebuddy", Path: ".codebuddy/skills"},
		{Agent: "gemini", Path: ".gemini/skills"},
		{Agent: "opencode", Path: ".opencode/skills"},
		{Agent: "project", Path: "skills"},
		{Agent: "project", Path: "skills/.curated"},
		{Agent: "loop-legacy", Path: legacyLoopDir, Legacy: true},
	} {
		add(candidate.Agent, candidate.Path)
	}
	return out
}

func absDir(repoRoot, dir string) string {
	dir = filepath.FromSlash(strings.TrimSpace(dir))
	if filepath.IsAbs(dir) {
		return filepath.Clean(dir)
	}
	return filepath.Join(repoRoot, dir)
}

func relPath(repoRoot, path string) string {
	rel, err := filepath.Rel(repoRoot, path)
	if err != nil || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || rel == ".." {
		return path
	}
	return filepath.ToSlash(rel)
}

func isLegacyDir(path string) bool {
	clean := filepath.ToSlash(filepath.Clean(filepath.FromSlash(strings.TrimSpace(path))))
	return clean == legacyLoopDir
}
