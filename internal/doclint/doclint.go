package doclint

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"html"
	"net/url"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

type Options struct {
	Root              string
	Entry             string
	RequiredReachable []string
	Excludes          []string
}

type Report struct {
	Status            string             `json:"status"`
	Entry             string             `json:"entry"`
	RequiredReachable []string           `json:"required_reachable"`
	Excludes          []string           `json:"excludes"`
	Tracked           []string           `json:"tracked"`
	Reachable         []string           `json:"reachable"`
	Unreachable       []string           `json:"unreachable_required"`
	InvalidReferences []InvalidReference `json:"invalid_references"`
}

type InvalidReference struct {
	Source string `json:"source"`
	Line   int    `json:"line"`
	Target string `json:"target"`
	Reason string `json:"reason"`
}

type UsageError struct {
	Err error
}

func (e UsageError) Error() string {
	if e.Err == nil {
		return ""
	}
	return e.Err.Error()
}

func (e UsageError) Unwrap() error { return e.Err }

type reference struct {
	Line     int
	Target   string
	Explicit bool
}

var (
	markdownLinkRE = regexp.MustCompile(`(!?)\[[^\]\n]*\]\(([^)\n]+)\)`)
	refDefRE       = regexp.MustCompile(`^\s*\[[^\]\n]+\]:\s*(\S+)`)
	htmlHrefRE     = regexp.MustCompile(`(?i)\bhref\s*=\s*["']([^"']+)["']`)
	plainPathRE    = regexp.MustCompile("(?i)(^|[\\s\"'`(<\\[])([A-Za-z0-9._~+@%/-]+?\\.(?:md|markdown)(?:[?#][A-Za-z0-9._~+@%/=&;:.-]*)?)")
)

func Analyze(ctx context.Context, opts Options) (Report, error) {
	root, err := normalizeRoot(opts.Root)
	if err != nil {
		return Report{}, err
	}
	excludes, err := normalizePatterns(opts.Excludes, "excludes")
	if err != nil {
		return Report{}, err
	}
	requiredPatterns, err := normalizePatterns(opts.RequiredReachable, "requiredReachable")
	if err != nil {
		return Report{}, err
	}
	entry, err := normalizeEntry(root, opts.Entry)
	if err != nil {
		return Report{}, err
	}
	if path.Dir(entry) != "." {
		return Report{}, usageErrorf("entry markdown must be at the repository root: %s", entry)
	}

	tracked, err := trackedMarkdown(ctx, root)
	if err != nil {
		return Report{}, err
	}
	tracked = filterExcluded(tracked, excludes)
	trackedSet := make(map[string]bool, len(tracked))
	for _, file := range tracked {
		trackedSet[file] = true
	}
	if isExcluded(entry, excludes) {
		return Report{}, usageErrorf("entry markdown is excluded: %s", entry)
	}
	if !isMarkdownPath(entry) {
		return Report{}, usageErrorf("entry must be a markdown file: %s", entry)
	}
	if !trackedSet[entry] {
		return Report{}, usageErrorf("entry markdown is not tracked by git: %s", entry)
	}
	required, err := requiredReachable(tracked, requiredPatterns)
	if err != nil {
		return Report{}, err
	}

	graph := make(map[string][]string, len(tracked))
	invalid := []InvalidReference{}
	for _, source := range tracked {
		refs, err := referencesInFile(filepath.Join(root, filepath.FromSlash(source)))
		if err != nil {
			return Report{}, err
		}
		for _, ref := range refs {
			target, ok, outside := resolveReference(source, ref.Target)
			if !ok {
				continue
			}
			if outside {
				invalid = appendInvalid(invalid, InvalidReference{
					Source: source,
					Line:   ref.Line,
					Target: cleanReferenceTarget(ref.Target),
					Reason: "outside repository",
				})
				continue
			}
			if !ref.Explicit && !referenceHasSlash(ref.Target) && !trackedSet[target] {
				rootTarget := cleanRepoPath(referencePathPart(ref.Target))
				if trackedSet[rootTarget] {
					target = rootTarget
				}
			}
			if isExcluded(target, excludes) {
				continue
			}
			if trackedSet[target] {
				graph[source] = append(graph[source], target)
				continue
			}
			if shouldReportMissingPlainReference(ref) {
				invalid = appendInvalid(invalid, InvalidReference{
					Source: source,
					Line:   ref.Line,
					Target: cleanReferenceTarget(ref.Target),
					Reason: missingReason(root, target),
				})
			}
		}
	}

	reachable := reachableDocuments(entry, graph)
	unreachable := unreachableRequired(required, reachable)
	sortInvalid(invalid)

	status := "ok"
	if len(unreachable) > 0 || len(invalid) > 0 {
		status = "findings"
	}
	return Report{
		Status:            status,
		Entry:             entry,
		RequiredReachable: required,
		Excludes:          excludes,
		Tracked:           tracked,
		Reachable:         sortedKeys(reachable),
		Unreachable:       unreachable,
		InvalidReferences: invalid,
	}, nil
}

func (r Report) HasFindings() bool {
	return len(r.Unreachable) > 0 || len(r.InvalidReferences) > 0
}

func (r Report) FindingCount() int {
	return len(r.Unreachable) + len(r.InvalidReferences)
}

func normalizeRoot(root string) (string, error) {
	if strings.TrimSpace(root) == "" {
		root = "."
	}
	abs, err := filepath.Abs(root)
	if err != nil {
		return "", err
	}
	return abs, nil
}

func normalizeEntry(root, entry string) (string, error) {
	entry = strings.TrimSpace(entry)
	if entry == "" {
		return "", usageErrorf("entry markdown is required")
	}
	if filepath.IsAbs(entry) {
		rel, err := filepath.Rel(root, entry)
		if err != nil {
			return "", usageErrorf("entry markdown must be inside the repository: %s", entry)
		}
		entry = rel
	}
	rel := cleanRepoPath(entry)
	if rel == "" || rel == "." || rel == ".." || strings.HasPrefix(rel, "../") {
		return "", usageErrorf("entry markdown must be inside the repository: %s", entry)
	}
	return rel, nil
}

func normalizePatterns(values []string, field string) ([]string, error) {
	seen := map[string]bool{}
	out := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			return nil, usageErrorf("%s requires non-empty paths", field)
		}
		if filepath.IsAbs(value) {
			return nil, usageErrorf("%s must be repository-relative: %s", field, value)
		}
		rel := cleanRepoPath(value)
		if rel == "" || rel == "." || rel == ".." || strings.HasPrefix(rel, "../") {
			return nil, usageErrorf("%s must stay inside the repository: %s", field, value)
		}
		if !seen[rel] {
			out = append(out, rel)
			seen[rel] = true
		}
	}
	sort.Strings(out)
	return out, nil
}

func cleanRepoPath(value string) string {
	value = filepath.ToSlash(value)
	for strings.HasPrefix(value, "./") {
		value = strings.TrimPrefix(value, "./")
	}
	return path.Clean(value)
}

func trackedMarkdown(ctx context.Context, root string) ([]string, error) {
	cmd := exec.CommandContext(ctx, "git", "ls-files", "-z")
	cmd.Dir = root
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("list git-tracked files: %w", err)
	}
	parts := bytes.Split(out, []byte{0})
	files := []string{}
	for _, part := range parts {
		if len(part) == 0 {
			continue
		}
		rel := cleanRepoPath(string(part))
		if !isMarkdownPath(rel) || hasPathSegment(rel, "skills") {
			continue
		}
		files = append(files, rel)
	}
	sort.Strings(files)
	return files, nil
}

func filterExcluded(files, excludes []string) []string {
	out := make([]string, 0, len(files))
	for _, file := range files {
		if !isExcluded(file, excludes) {
			out = append(out, file)
		}
	}
	return out
}

func isExcluded(rel string, excludes []string) bool {
	rel = cleanRepoPath(rel)
	if hasPathSegment(rel, "skills") {
		return true
	}
	for _, exclude := range excludes {
		if patternMatches(exclude, rel) {
			return true
		}
	}
	return false
}

func requiredReachable(tracked, patterns []string) ([]string, error) {
	if len(patterns) == 0 {
		return append([]string(nil), tracked...), nil
	}
	seen := map[string]bool{}
	out := []string{}
	for _, pattern := range patterns {
		for _, file := range tracked {
			if patternMatches(pattern, file) && !seen[file] {
				out = append(out, file)
				seen[file] = true
			}
		}
	}
	sort.Strings(out)
	return out, nil
}

func patternMatches(pattern, rel string) bool {
	pattern = cleanRepoPath(pattern)
	rel = cleanRepoPath(rel)
	if hasGlob(pattern) {
		return globMatches(pattern, rel)
	}
	if rel == pattern || strings.HasPrefix(rel, pattern+"/") {
		return true
	}
	if !isMarkdownPath(pattern) {
		if rel == pattern+".md" || rel == pattern+".markdown" {
			return true
		}
	}
	return false
}

func hasGlob(pattern string) bool {
	return strings.ContainsAny(pattern, "*?[")
}

func globMatches(pattern, rel string) bool {
	re := strings.Builder{}
	re.WriteString("^")
	for i := 0; i < len(pattern); i++ {
		ch := pattern[i]
		switch ch {
		case '*':
			if i+1 < len(pattern) && pattern[i+1] == '*' {
				if i+2 < len(pattern) && pattern[i+2] == '/' {
					re.WriteString("(?:.*/)?")
					i += 2
				} else {
					re.WriteString(".*")
					i++
				}
			} else {
				re.WriteString("[^/]*")
			}
		case '?':
			re.WriteString("[^/]")
		default:
			re.WriteString(regexp.QuoteMeta(string(ch)))
		}
	}
	re.WriteString("$")
	ok, err := regexp.MatchString(re.String(), rel)
	return err == nil && ok
}

func hasPathSegment(rel, segment string) bool {
	for _, part := range strings.Split(rel, "/") {
		if part == segment {
			return true
		}
	}
	return false
}

func referencesInFile(filename string) ([]reference, error) {
	data, err := os.ReadFile(filename)
	if err != nil {
		return nil, err
	}
	lines := strings.Split(string(data), "\n")
	inFence := false
	var refs []reference
	for i, line := range lines {
		lineNo := i + 1
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "```") || strings.HasPrefix(trimmed, "~~~") {
			inFence = !inFence
			continue
		}
		if inFence {
			continue
		}
		refs = append(refs, referencesInLine(line, lineNo)...)
	}
	return refs, nil
}

func referencesInLine(line string, lineNo int) []reference {
	var refs []reference
	for _, match := range markdownLinkRE.FindAllStringSubmatch(line, -1) {
		if len(match) == 3 && match[1] != "!" {
			refs = append(refs, reference{Line: lineNo, Target: match[2], Explicit: true})
		}
	}
	if match := refDefRE.FindStringSubmatch(line); len(match) == 2 {
		refs = append(refs, reference{Line: lineNo, Target: match[1], Explicit: true})
	}
	for _, match := range htmlHrefRE.FindAllStringSubmatch(line, -1) {
		if len(match) == 2 {
			refs = append(refs, reference{Line: lineNo, Target: match[1], Explicit: true})
		}
	}
	plainLine := markdownLinkRE.ReplaceAllString(line, " ")
	plainLine = htmlHrefRE.ReplaceAllString(plainLine, " ")
	if refDefRE.MatchString(line) {
		plainLine = ""
	}
	for _, match := range plainPathRE.FindAllStringSubmatch(plainLine, -1) {
		if len(match) == 3 {
			refs = append(refs, reference{Line: lineNo, Target: match[2]})
		}
	}
	return refs
}

func resolveReference(source, raw string) (string, bool, bool) {
	target := cleanReferenceTarget(raw)
	if target == "" || strings.HasPrefix(target, "#") || isExternalReference(target) {
		return "", false, false
	}
	target = stripAnchorAndQuery(target)
	if target == "" {
		return "", false, false
	}
	if unescaped, err := url.PathUnescape(target); err == nil {
		target = unescaped
	}
	if !isMarkdownPath(target) {
		return "", false, false
	}
	if filepath.IsAbs(filepath.FromSlash(target)) || path.IsAbs(target) {
		return "", true, true
	}
	sourceDir := path.Dir(source)
	if sourceDir == "." {
		sourceDir = ""
	}
	resolved := cleanRepoPath(path.Join(sourceDir, target))
	if resolved == "." || resolved == ".." || strings.HasPrefix(resolved, "../") {
		return "", true, true
	}
	return resolved, true, false
}

func cleanReferenceTarget(raw string) string {
	target := strings.TrimSpace(html.UnescapeString(raw))
	if strings.HasPrefix(target, "<") {
		if end := strings.Index(target, ">"); end >= 0 {
			target = target[1:end]
		}
	} else if fields := strings.Fields(target); len(fields) > 0 {
		target = fields[0]
	}
	target = strings.Trim(target, "\"'")
	return strings.TrimSpace(target)
}

func stripAnchorAndQuery(target string) string {
	cut := len(target)
	for _, marker := range []string{"#", "?"} {
		if i := strings.Index(target, marker); i >= 0 && i < cut {
			cut = i
		}
	}
	return target[:cut]
}

func referencePathPart(raw string) string {
	target := stripAnchorAndQuery(cleanReferenceTarget(raw))
	if unescaped, err := url.PathUnescape(target); err == nil {
		target = unescaped
	}
	return filepath.ToSlash(target)
}

func referenceHasSlash(raw string) bool {
	return strings.Contains(referencePathPart(raw), "/")
}

func isExternalReference(target string) bool {
	lower := strings.ToLower(target)
	if strings.HasPrefix(lower, "//") {
		return true
	}
	if i := strings.Index(lower, ":"); i >= 0 {
		if slash := strings.Index(lower, "/"); slash == -1 || i < slash {
			return true
		}
	}
	return false
}

func isMarkdownPath(rel string) bool {
	ext := strings.ToLower(path.Ext(stripAnchorAndQuery(filepath.ToSlash(rel))))
	return ext == ".md" || ext == ".markdown"
}

func shouldReportMissingPlainReference(ref reference) bool {
	if ref.Explicit {
		return true
	}
	return referenceHasSlash(ref.Target)
}

func missingReason(root, target string) string {
	if _, err := os.Stat(filepath.Join(root, filepath.FromSlash(target))); err == nil {
		return "not tracked"
	}
	return "not found"
}

func reachableDocuments(entry string, graph map[string][]string) map[string]bool {
	reachable := map[string]bool{entry: true}
	queue := []string{entry}
	for len(queue) > 0 {
		current := queue[0]
		queue = queue[1:]
		for _, next := range graph[current] {
			if reachable[next] {
				continue
			}
			reachable[next] = true
			queue = append(queue, next)
		}
	}
	return reachable
}

func unreachableRequired(required []string, reachable map[string]bool) []string {
	unreachable := []string{}
	for _, file := range required {
		if !reachable[file] {
			unreachable = append(unreachable, file)
		}
	}
	return unreachable
}

func sortedKeys(values map[string]bool) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func appendInvalid(items []InvalidReference, item InvalidReference) []InvalidReference {
	for _, existing := range items {
		if existing.Source == item.Source && existing.Line == item.Line && existing.Target == item.Target && existing.Reason == item.Reason {
			return items
		}
	}
	return append(items, item)
}

func sortInvalid(items []InvalidReference) {
	sort.Slice(items, func(i, j int) bool {
		if items[i].Source != items[j].Source {
			return items[i].Source < items[j].Source
		}
		if items[i].Line != items[j].Line {
			return items[i].Line < items[j].Line
		}
		if items[i].Target != items[j].Target {
			return items[i].Target < items[j].Target
		}
		return items[i].Reason < items[j].Reason
	})
}

func usageErrorf(format string, args ...any) error {
	return UsageError{Err: fmt.Errorf(format, args...)}
}

func IsUsageError(err error) bool {
	var usage UsageError
	return errors.As(err, &usage)
}
