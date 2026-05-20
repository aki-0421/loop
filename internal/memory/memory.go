package memory

import (
	"strings"

	"github.com/aki-0421/loop/internal/artifactdb"
)

type Record struct {
	Repo      string   `json:"repo"`
	Number    int      `json:"number"`
	URL       string   `json:"url"`
	State     string   `json:"state"`
	Title     string   `json:"title"`
	Body      string   `json:"body,omitempty"`
	UpdatedAt string   `json:"updated_at"`
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
	items, err := artifactdb.RecentPRMemory(artifactdb.GlobalDBPathFromRunsPath(runsPath), repo, limit)
	if err != nil {
		return nil, err
	}
	return recordsFromPRMemory(items), nil
}

func Search(runsPath, query string, limit int) ([]Record, error) {
	return SearchWithOptions(runsPath, SearchOptions{Query: query, Limit: limit})
}

func SearchWithOptions(runsPath string, opts SearchOptions) ([]Record, error) {
	hits, err := artifactdb.SearchPRMemory(artifactdb.GlobalDBPathFromRunsPath(runsPath), artifactdb.PRMemorySearchOptions{
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
	items, err := artifactdb.RecentPRMemory(artifactdb.GlobalDBPathFromRunsPath(runsPath), "", 0)
	if err != nil {
		return nil, err
	}
	return recordsFromPRMemory(items), nil
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
