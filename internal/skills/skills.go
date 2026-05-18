package skills

import (
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/aki-0421/loop/internal/assets"
	"github.com/aki-0421/loop/internal/config"
)

var DefaultNames = []string{
	"loop",
}

type Skill struct {
	Name        string
	Path        string
	Description string
	Version     string
}

type InstallResult struct {
	Installed []string
	Skipped   []string
}

type SyncResult struct {
	Agent string
	Mode  string
	Path  string
}

type DoctorReport struct {
	Errors []string
}

func (r DoctorReport) OK() bool {
	return len(r.Errors) == 0
}

func InstallDefaults(repoRoot, dir string, force bool) (InstallResult, error) {
	var result InstallResult
	for _, name := range DefaultNames {
		path, err := InstallBuiltIn(repoRoot, dir, name, force)
		if err != nil {
			return result, err
		}
		if path == "" {
			result.Skipped = append(result.Skipped, name)
		} else {
			result.Installed = append(result.Installed, name)
		}
	}
	return result, nil
}

func InstallBuiltIn(repoRoot, dir, name string, force bool) (string, error) {
	if dir == "" {
		return "", fmt.Errorf("skill directory is required")
	}
	name, err := safeSkillName(name)
	if err != nil {
		return "", err
	}
	data, err := assets.Read("templates/skills/" + name + "/SKILL.md")
	if err != nil {
		return "", fmt.Errorf("read built-in skill %s: %w", name, err)
	}
	path := filepath.Join(absDir(repoRoot, dir), name, "SKILL.md")
	if !force {
		if _, err := os.Stat(path); err == nil {
			return "", nil
		} else if !os.IsNotExist(err) {
			return "", err
		}
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return "", err
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		return "", err
	}
	return path, nil
}

func InstallFromPath(source, repoRoot, destDir string, force bool) (string, error) {
	info, err := os.Stat(source)
	if err != nil {
		return "", err
	}
	var sourceSkill string
	var name string
	if info.IsDir() {
		sourceSkill = filepath.Join(source, "SKILL.md")
		name = filepath.Base(source)
	} else {
		sourceSkill = source
		name = strings.TrimSuffix(filepath.Base(source), filepath.Ext(source))
	}
	data, err := os.ReadFile(sourceSkill)
	if err != nil {
		return "", err
	}
	meta, err := Parse(data)
	if err == nil && meta.Name != "" {
		name = meta.Name
	}
	name, err = safeSkillName(name)
	if err != nil {
		return "", err
	}
	target := filepath.Join(absDir(repoRoot, destDir), name, "SKILL.md")
	if !force {
		if _, err := os.Stat(target); err == nil {
			return "", fmt.Errorf("%s already exists; use --force to overwrite", target)
		}
	}
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return "", err
	}
	if err := os.WriteFile(target, data, 0o644); err != nil {
		return "", err
	}
	return target, nil
}

func List(dir string) ([]Skill, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var out []Skill
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		path := filepath.Join(dir, entry.Name(), "SKILL.md")
		data, err := os.ReadFile(path)
		if err != nil {
			out = append(out, Skill{Name: entry.Name(), Path: path})
			continue
		}
		skill, err := Parse(data)
		if err != nil && skill.Name == "" {
			skill.Name = entry.Name()
		}
		if skill.Name == "" {
			skill.Name = entry.Name()
		}
		skill.Path = path
		out = append(out, skill)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

func Doctor(dir string) DoctorReport {
	list, err := List(dir)
	if err != nil {
		return DoctorReport{Errors: []string{err.Error()}}
	}
	var issues []string
	for _, skill := range list {
		dirName := filepath.Base(filepath.Dir(skill.Path))
		if skill.Name == "" {
			issues = append(issues, skill.Path+": name is required")
		}
		if skill.Name != "" && skill.Name != dirName {
			issues = append(issues, skill.Path+": name must match directory")
		}
		if skill.Description == "" {
			issues = append(issues, skill.Path+": description is required")
		}
		if skill.Version != "1" {
			issues = append(issues, skill.Path+": version must be 1")
		}
	}
	return DoctorReport{Errors: issues}
}

func Parse(data []byte) (Skill, error) {
	text := string(data)
	if !strings.HasPrefix(text, "---\n") {
		return Skill{}, fmt.Errorf("missing front matter")
	}
	rest := strings.TrimPrefix(text, "---\n")
	head, _, ok := strings.Cut(rest, "\n---")
	if !ok {
		return Skill{}, fmt.Errorf("unterminated front matter")
	}
	var skill Skill
	for _, line := range strings.Split(head, "\n") {
		key, value, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		value = strings.Trim(strings.TrimSpace(value), `"'`)
		switch strings.TrimSpace(key) {
		case "name":
			skill.Name = value
		case "description":
			skill.Description = value
		case "version":
			skill.Version = value
		}
	}
	if skill.Name == "" {
		return skill, fmt.Errorf("missing name")
	}
	return skill, nil
}

func safeSkillName(name string) (string, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return "", fmt.Errorf("skill name is required")
	}
	if name != filepath.Base(name) || name == "." || name == ".." {
		return "", fmt.Errorf("invalid skill name %q", name)
	}
	for _, r := range name {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '-' || r == '_' || r == '.' {
			continue
		}
		return "", fmt.Errorf("invalid skill name %q", name)
	}
	return name, nil
}

func Sync(repoRoot string, cfg config.Config) ([]SyncResult, error) {
	sourceDir := absDir(repoRoot, cfg.Skills.SourceDir)
	var results []SyncResult
	for agent, target := range cfg.Skills.Targets {
		mode := target.Mode
		if mode == "" {
			mode = "copy"
		}
		targetDir := absDir(repoRoot, target.Path)
		if mode == "off" {
			results = append(results, SyncResult{Agent: agent, Mode: mode, Path: targetDir})
			continue
		}
		if err := SyncDir(sourceDir, targetDir, mode); err != nil {
			return results, err
		}
		results = append(results, SyncResult{Agent: agent, Mode: mode, Path: targetDir})
	}
	sort.Slice(results, func(i, j int) bool { return results[i].Agent < results[j].Agent })
	return results, nil
}

func SyncDir(sourceDir, targetDir, mode string) error {
	if mode == "" {
		mode = "copy"
	}
	if mode == "off" {
		return nil
	}
	if targetDir == "" {
		return fmt.Errorf("skill target path is required")
	}
	if err := os.MkdirAll(targetDir, 0o755); err != nil {
		return err
	}
	entries, err := os.ReadDir(sourceDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		src := filepath.Join(sourceDir, entry.Name())
		dst := filepath.Join(targetDir, entry.Name())
		if mode == "symlink" {
			_ = os.RemoveAll(dst)
			if err := os.Symlink(src, dst); err != nil {
				return err
			}
			continue
		}
		if err := copyDir(src, dst); err != nil {
			return err
		}
	}
	return nil
}

func copyDir(src, dst string) error {
	if err := os.RemoveAll(dst); err != nil {
		return err
	}
	return filepath.WalkDir(src, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)
		info, err := d.Info()
		if err != nil {
			return err
		}
		if d.IsDir() {
			return os.MkdirAll(target, info.Mode().Perm())
		}
		in, err := os.Open(path)
		if err != nil {
			return err
		}
		defer in.Close()
		out, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, info.Mode().Perm())
		if err != nil {
			return err
		}
		if _, err := io.Copy(out, in); err != nil {
			_ = out.Close()
			return err
		}
		return out.Close()
	})
}
