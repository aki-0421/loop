package memory

import (
	"path/filepath"
	"strings"

	"github.com/aki-0421/loop/internal/artifactdb"
)

type Record struct {
	RunID       string   `json:"run_id"`
	IterationID string   `json:"iteration_id"`
	Path        string   `json:"path"`
	Artifact    string   `json:"artifact,omitempty"`
	Branch      string   `json:"branch"`
	Summary     string   `json:"summary"`
	Keywords    []string `json:"keywords"`
}

type Index struct {
	Version int      `json:"version"`
	Records []Record `json:"records"`
}

type SearchOptions struct {
	Query       string
	RunID       string
	IterationID string
	Artifact    string
	Limit       int
}

func Recent(runsPath string, limit int) ([]string, error) {
	items, err := artifactdb.RecentSummaries(artifactdb.GlobalDBPathFromRunsPath(runsPath), runIDFromPath(runsPath), limit)
	if err != nil {
		return nil, err
	}
	return items, nil
}

func Search(runsPath, query string, limit int) ([]Record, error) {
	return SearchWithOptions(runsPath, SearchOptions{Query: query, RunID: runIDFromPath(runsPath), Limit: limit})
}

func SearchWithOptions(runsPath string, opts SearchOptions) ([]Record, error) {
	if opts.RunID == "" {
		opts.RunID = runIDFromPath(runsPath)
	}
	hits, err := artifactdb.SearchGlobal(artifactdb.GlobalDBPathFromRunsPath(runsPath), artifactdb.SearchOptions{
		Query:       opts.Query,
		RunID:       opts.RunID,
		IterationID: opts.IterationID,
		Artifact:    opts.Artifact,
		Limit:       opts.Limit,
	})
	if err != nil {
		return nil, err
	}
	records := make([]Record, 0, len(hits))
	for _, hit := range hits {
		records = append(records, recordFromHit(runsPath, hit))
	}
	return records, nil
}

func Compact(runsDir string) (Index, error) {
	if _, err := artifactdb.RebuildGlobalFromRuns(runsDir); err != nil {
		return Index{}, err
	}
	records, err := BuildRecords(runsDir)
	if err != nil {
		return Index{}, err
	}
	return Index{Version: 2, Records: records}, nil
}

func BuildRecords(runsPath string) ([]Record, error) {
	hits, err := artifactdb.SummaryHits(artifactdb.GlobalDBPathFromRunsPath(runsPath), runIDFromPath(runsPath), 0)
	if err != nil {
		return nil, err
	}
	records := make([]Record, 0, len(hits))
	for _, hit := range hits {
		records = append(records, recordFromHit(runsPath, hit))
	}
	return records, nil
}

func recordFromHit(runsPath string, hit artifactdb.SearchHit) Record {
	path := filepath.Join(filepath.Dir(artifactdb.GlobalDBPathFromRunsPath(runsPath)), "runs", hit.RunID, "iterations", hit.IterationID, hit.Artifact)
	record := parseSummaryRecord(runsPath, path, hit.Content)
	record.RunID = hit.RunID
	record.IterationID = hit.IterationID
	record.Artifact = hit.Artifact
	if hit.Artifact != "summary" {
		record.Summary = firstSentence(hit.Content)
		record.Keywords = keywords(hit.Content)
	}
	return record
}

func parseSummaryRecord(root, path, text string) Record {
	rel, _ := filepath.Rel(root, path)
	parts := strings.Split(rel, string(filepath.Separator))
	record := Record{Path: path, Artifact: "summary", Summary: firstSentence(text), Keywords: keywords(text)}
	if len(parts) >= 4 {
		record.RunID = parts[0]
		record.IterationID = parts[2]
	}
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "- Branch:") {
			record.Branch = strings.TrimSpace(strings.TrimPrefix(line, "- Branch:"))
		}
		if strings.HasPrefix(line, "- Squash summary:") {
			record.Summary = strings.TrimSpace(strings.TrimPrefix(line, "- Squash summary:"))
		}
	}
	return record
}

func runIDFromPath(path string) string {
	parts := strings.Split(filepath.Clean(path), string(filepath.Separator))
	if len(parts) >= 2 && parts[len(parts)-2] == "runs" {
		return parts[len(parts)-1]
	}
	return ""
}

func firstSentence(text string) string {
	text = strings.TrimSpace(strings.ReplaceAll(text, "\n", " "))
	if len(text) > 200 {
		return text[:200]
	}
	return text
}

func keywords(text string) []string {
	seen := map[string]bool{}
	var out []string
	for _, field := range strings.FieldsFunc(strings.ToLower(text), func(r rune) bool {
		return !(r >= 'a' && r <= 'z') && !(r >= '0' && r <= '9')
	}) {
		if len(field) < 4 || seen[field] {
			continue
		}
		seen[field] = true
		out = append(out, field)
		if len(out) >= 20 {
			break
		}
	}
	return out
}
