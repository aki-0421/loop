package memory

import (
	"sort"
	"strings"
	"time"

	"github.com/aki-0421/loop/internal/artifactdb"
)

type Record struct {
	Kind      string   `json:"kind"`
	Repo      string   `json:"repo"`
	Number    int      `json:"number"`
	CommentID string   `json:"comment_id,omitempty"`
	URL       string   `json:"url"`
	State     string   `json:"state"`
	Title     string   `json:"title"`
	Body      string   `json:"body,omitempty"`
	Author    string   `json:"author,omitempty"`
	Labels    string   `json:"labels,omitempty"`
	UpdatedAt string   `json:"updated_at"`
	ClosedAt  string   `json:"closed_at,omitempty"`
	MergedAt  string   `json:"merged_at,omitempty"`
	FetchedAt string   `json:"fetched_at"`
	Summary   string   `json:"summary"`
	Excerpt   string   `json:"excerpt"`
	Keywords  []string `json:"keywords"`

	RunID       string `json:"run_id,omitempty"`
	IterationID string `json:"iteration_id,omitempty"`
	Path        string `json:"path,omitempty"`
	Artifact    string `json:"artifact,omitempty"`
	Branch      string `json:"branch,omitempty"`
}

type Index struct {
	Version int      `json:"version"`
	Records []Record `json:"records"`
}

type SearchOptions struct {
	Query string
	Repo  string
	Limit int

	RunID       string
	IterationID string
	Artifact    string
}

func Recent(runsPath string, limit int) ([]Record, error) {
	return RecentWithRepo(runsPath, "", limit)
}

func RecentWithRepo(runsPath, repo string, limit int) ([]Record, error) {
	globalPath := artifactdb.GlobalDBPathFromRunsPath(runsPath)
	items, err := artifactdb.RecentPRMemory(globalPath, repo, limit)
	if err != nil {
		return nil, err
	}
	contextItems, err := artifactdb.RecentGitHubContext(globalPath, repo, limit)
	if err != nil {
		return nil, err
	}
	records := recordsFromPRMemory(items)
	records = append(records, recordsFromGitHubContext(contextItems)...)
	sortRecords(records)
	if limit > 0 && len(records) > limit {
		records = records[:limit]
	}
	return records, nil
}

func Search(runsPath, query string, limit int) ([]Record, error) {
	return SearchWithOptions(runsPath, SearchOptions{Query: query, Limit: limit})
}

func SearchWithOptions(runsPath string, opts SearchOptions) ([]Record, error) {
	globalPath := artifactdb.GlobalDBPathFromRunsPath(runsPath)
	hits, err := artifactdb.SearchPRMemory(globalPath, artifactdb.PRMemorySearchOptions{
		Query: opts.Query,
		Repo:  opts.Repo,
		Limit: opts.Limit,
	})
	if err != nil {
		return nil, err
	}
	records := make([]Record, 0, len(hits))
	for _, hit := range hits {
		records = append(records, recordFromPRMemory(hit.Record))
	}
	contextHits, err := artifactdb.SearchGitHubContext(globalPath, artifactdb.GitHubContextSearchOptions{
		Query: opts.Query,
		Repo:  opts.Repo,
		Limit: opts.Limit,
	})
	if err != nil {
		return nil, err
	}
	for _, hit := range contextHits {
		records = append(records, recordFromGitHubContext(hit.Record))
	}
	sortRecords(records)
	if opts.Limit > 0 && len(records) > opts.Limit {
		records = records[:opts.Limit]
	}
	return records, nil
}

func Compact(runsDir string) (Index, error) {
	records, err := BuildRecords(runsDir)
	if err != nil {
		return Index{}, err
	}
	return Index{Version: 3, Records: records}, nil
}

func BuildRecords(runsPath string) ([]Record, error) {
	globalPath := artifactdb.GlobalDBPathFromRunsPath(runsPath)
	items, err := artifactdb.RecentPRMemory(globalPath, "", 0)
	if err != nil {
		return nil, err
	}
	contextItems, err := artifactdb.RecentGitHubContext(globalPath, "", 0)
	if err != nil {
		return nil, err
	}
	records := recordsFromPRMemory(items)
	records = append(records, recordsFromGitHubContext(contextItems)...)
	sortRecords(records)
	return records, nil
}

func recordsFromPRMemory(items []artifactdb.PRMemoryRecord) []Record {
	records := make([]Record, 0, len(items))
	for _, item := range items {
		records = append(records, recordFromPRMemory(item))
	}
	return records
}

func recordFromPRMemory(item artifactdb.PRMemoryRecord) Record {
	return Record{
		Kind:      "pr",
		Repo:      item.Repo,
		Number:    item.Number,
		URL:       item.URL,
		State:     item.State,
		Title:     item.Title,
		Body:      item.Body,
		UpdatedAt: item.UpdatedAt,
		MergedAt:  item.MergedAt,
		FetchedAt: item.FetchedAt,
		Summary:   item.Title,
		Excerpt:   firstSentence(item.Body),
		Keywords:  keywords(item.Title + "\n" + item.Body),
		Path:      item.URL,
		Artifact:  "pull-request",
	}
}

func recordsFromGitHubContext(items []artifactdb.GitHubContextRecord) []Record {
	records := make([]Record, 0, len(items))
	for _, item := range items {
		records = append(records, recordFromGitHubContext(item))
	}
	return records
}

func recordFromGitHubContext(item artifactdb.GitHubContextRecord) Record {
	title := item.Title
	if strings.TrimSpace(title) == "" && item.CommentID != "" {
		title = item.Kind + " #" + strings.TrimSpace(item.URL)
	}
	return Record{
		Kind:      item.Kind,
		Repo:      item.Repo,
		Number:    item.Number,
		CommentID: item.CommentID,
		URL:       item.URL,
		State:     item.State,
		Title:     title,
		Body:      item.Body,
		Author:    item.Author,
		Labels:    item.Labels,
		UpdatedAt: item.UpdatedAt,
		ClosedAt:  item.ClosedAt,
		FetchedAt: item.FetchedAt,
		Summary:   title,
		Excerpt:   firstSentence(item.Body),
		Keywords:  keywords(title + "\n" + item.Body),
		Path:      item.URL,
		Artifact:  item.Kind,
	}
}

func sortRecords(records []Record) {
	sort.SliceStable(records, func(i, j int) bool {
		left := recordSortTime(records[i])
		right := recordSortTime(records[j])
		if !left.Equal(right) {
			return left.After(right)
		}
		if records[i].Repo != records[j].Repo {
			return records[i].Repo < records[j].Repo
		}
		if records[i].Number != records[j].Number {
			return records[i].Number > records[j].Number
		}
		return records[i].Kind < records[j].Kind
	})
}

func recordSortTime(record Record) time.Time {
	for _, value := range []string{record.MergedAt, record.ClosedAt, record.UpdatedAt, record.FetchedAt} {
		if t, err := time.Parse(time.RFC3339, strings.TrimSpace(value)); err == nil {
			return t
		}
	}
	return time.Time{}
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
