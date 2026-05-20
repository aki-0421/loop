package memory

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"path"
	"strconv"
	"strings"
	"time"

	"github.com/aki-0421/loop/internal/artifactdb"
	"github.com/aki-0421/loop/internal/gitx"
	"github.com/aki-0421/loop/internal/pr"
)

var ErrNoGitHubRemote = errors.New("no GitHub origin remote found")

type SyncOptions struct {
	WorkDir string
	RunsDir string
	Full    bool
	GHPath  string
}

type SyncResult struct {
	Repo     string
	Full     bool
	Since    string
	Fetched  int
	Upserted int
	Deleted  int
	SyncedAt string
}

type FetchOptions struct {
	WorkDir string
	RunsDir string
	Ref     string
	GHPath  string
}

type RateLimitError struct {
	Remaining int
	ResetAt   string
}

func (e RateLimitError) Error() string {
	if e.ResetAt != "" {
		return fmt.Sprintf("GitHub API rate limit is too low (%d remaining; resets at %s)", e.Remaining, e.ResetAt)
	}
	return fmt.Sprintf("GitHub API rate limit is too low (%d remaining)", e.Remaining)
}

func Sync(ctx context.Context, opts SyncOptions) (SyncResult, error) {
	repo, owner, name, err := ResolveGitHubRepository(ctx, opts.WorkDir)
	if err != nil {
		return SyncResult{}, err
	}
	globalPath := artifactdb.GlobalDBPathFromRunsPath(opts.RunsDir)
	syncedAt := time.Now().UTC().Format(time.RFC3339)
	runner := pr.Runner{Dir: opts.WorkDir, GHPath: opts.GHPath}
	lastSync, err := artifactdb.PRMemoryLastSync(globalPath, repo)
	if err != nil {
		return SyncResult{}, err
	}
	full := opts.Full || lastSync == ""
	if full {
		records, err := fetchSearchPullRequests(ctx, runner, []string{
			fmt.Sprintf("repo:%s is:pr is:open", repo),
			fmt.Sprintf("repo:%s is:pr is:merged", repo),
		}, syncedAt)
		if err != nil {
			return SyncResult{}, err
		}
		records = filterRepoRecords(records, repo)
		if err := artifactdb.ReplacePRMemory(globalPath, repo, records, syncedAt); err != nil {
			return SyncResult{}, err
		}
		return SyncResult{Repo: repo, Full: true, Fetched: len(records), Upserted: countIncludedPRMemory(records), SyncedAt: syncedAt}, nil
	}
	query := fmt.Sprintf("repo:%s is:pr updated:>=%s", repo, lastSync)
	records, err := fetchSearchPullRequests(ctx, runner, []string{query}, syncedAt)
	if err != nil {
		return SyncResult{}, err
	}
	records = filterRepoRecords(records, repo)
	upserted, deleted, err := artifactdb.ApplyPRMemorySync(globalPath, repo, records, syncedAt)
	if err != nil {
		return SyncResult{}, err
	}
	_ = owner
	_ = name
	return SyncResult{Repo: repo, Full: false, Since: lastSync, Fetched: len(records), Upserted: upserted, Deleted: deleted, SyncedAt: syncedAt}, nil
}

func FetchPullRequest(ctx context.Context, opts FetchOptions) (Record, error) {
	repo, owner, name, err := ResolveGitHubRepository(ctx, opts.WorkDir)
	if err != nil {
		return Record{}, err
	}
	number := PullRequestNumber(opts.Ref)
	if number <= 0 {
		return Record{}, errors.New("pull request number is required")
	}
	runner := pr.Runner{Dir: opts.WorkDir, GHPath: opts.GHPath}
	record, err := fetchPullRequestByNumber(ctx, runner, owner, name, number, time.Now().UTC().Format(time.RFC3339))
	if err != nil {
		return Record{}, err
	}
	record.Repo = repo
	globalPath := artifactdb.GlobalDBPathFromRunsPath(opts.RunsDir)
	switch strings.ToLower(record.State) {
	case "open", "merged":
		if err := artifactdb.UpsertPRMemory(globalPath, record); err != nil {
			return Record{}, err
		}
	default:
		if err := artifactdb.DeletePRMemory(globalPath, repo, number); err != nil {
			return Record{}, err
		}
	}
	return recordFromPRMemory(record), nil
}

func ResolveGitHubRepository(ctx context.Context, dir string) (repo, owner, name string, err error) {
	if strings.TrimSpace(dir) == "" {
		dir = "."
	}
	out, err := (gitx.Runner{Dir: dir}).Run(ctx, "remote", "get-url", "origin")
	if err != nil {
		return "", "", "", ErrNoGitHubRemote
	}
	repo, owner, name, ok := parseGitHubRemote(strings.TrimSpace(out))
	if !ok {
		return "", "", "", ErrNoGitHubRemote
	}
	return repo, owner, name, nil
}

func PullRequestNumber(ref string) int {
	ref = strings.TrimSpace(strings.TrimSuffix(ref, "/"))
	if ref == "" {
		return 0
	}
	if n, err := strconv.Atoi(ref); err == nil {
		return n
	}
	for _, marker := range []string{"/pull/", "/pr/"} {
		if i := strings.LastIndex(ref, marker); i >= 0 {
			rest := ref[i+len(marker):]
			if slash := strings.Index(rest, "/"); slash >= 0 {
				rest = rest[:slash]
			}
			if query := strings.IndexAny(rest, "?#"); query >= 0 {
				rest = rest[:query]
			}
			if n, err := strconv.Atoi(rest); err == nil {
				return n
			}
		}
	}
	if i := strings.LastIndex(ref, "#"); i >= 0 {
		if n, err := strconv.Atoi(ref[i+1:]); err == nil {
			return n
		}
	}
	return 0
}

func parseGitHubRemote(raw string) (repo, owner, name string, ok bool) {
	raw = strings.TrimSpace(strings.TrimSuffix(raw, ".git"))
	if strings.HasPrefix(raw, "git@github.com:") {
		return splitOwnerRepo(strings.TrimPrefix(raw, "git@github.com:"))
	}
	if strings.HasPrefix(raw, "ssh://git@github.com/") {
		return splitOwnerRepo(strings.TrimPrefix(raw, "ssh://git@github.com/"))
	}
	u, err := url.Parse(raw)
	if err == nil && strings.EqualFold(u.Host, "github.com") {
		return splitOwnerRepo(strings.TrimPrefix(path.Clean(u.Path), "/"))
	}
	return "", "", "", false
}

func splitOwnerRepo(value string) (repo, owner, name string, ok bool) {
	parts := strings.Split(strings.Trim(value, "/"), "/")
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return "", "", "", false
	}
	owner = parts[0]
	name = strings.TrimSuffix(parts[1], ".git")
	return owner + "/" + name, owner, name, true
}

const searchPullRequestsGraphQL = `
query($searchQuery: String!, $after: String) {
  search(type: ISSUE, query: $searchQuery, first: 50, after: $after) {
    pageInfo {
      hasNextPage
      endCursor
    }
    nodes {
      ... on PullRequest {
        number
        url
        state
        title
        body
        updatedAt
        mergedAt
        repository {
          nameWithOwner
        }
      }
    }
  }
  rateLimit {
    remaining
    resetAt
    cost
  }
}`

const pullRequestByNumberGraphQL = `
query($owner: String!, $name: String!, $number: Int!) {
  repository(owner: $owner, name: $name) {
    pullRequest(number: $number) {
      number
      url
      state
      title
      body
      updatedAt
      mergedAt
      repository {
        nameWithOwner
      }
    }
  }
  rateLimit {
    remaining
    resetAt
    cost
  }
}`

type graphQLRateLimit struct {
	Remaining int    `json:"remaining"`
	ResetAt   string `json:"resetAt"`
	Cost      int    `json:"cost"`
}

type graphQLPageInfo struct {
	HasNextPage bool   `json:"hasNextPage"`
	EndCursor   string `json:"endCursor"`
}

type graphQLPullRequest struct {
	Number     int     `json:"number"`
	URL        string  `json:"url"`
	State      string  `json:"state"`
	Title      string  `json:"title"`
	Body       string  `json:"body"`
	UpdatedAt  string  `json:"updatedAt"`
	MergedAt   *string `json:"mergedAt"`
	Repository struct {
		NameWithOwner string `json:"nameWithOwner"`
	} `json:"repository"`
}

type graphQLError struct {
	Message string `json:"message"`
}

type graphQLSearchResponse struct {
	Data struct {
		Search struct {
			PageInfo graphQLPageInfo      `json:"pageInfo"`
			Nodes    []graphQLPullRequest `json:"nodes"`
		} `json:"search"`
		RateLimit graphQLRateLimit `json:"rateLimit"`
	} `json:"data"`
	Errors []graphQLError `json:"errors"`
}

type graphQLPRResponse struct {
	Data struct {
		Repository struct {
			PullRequest *graphQLPullRequest `json:"pullRequest"`
		} `json:"repository"`
		RateLimit graphQLRateLimit `json:"rateLimit"`
	} `json:"data"`
	Errors []graphQLError `json:"errors"`
}

func fetchSearchPullRequests(ctx context.Context, runner pr.Runner, queries []string, fetchedAt string) ([]artifactdb.PRMemoryRecord, error) {
	var out []artifactdb.PRMemoryRecord
	for _, query := range queries {
		after := ""
		for {
			result, err := runner.GraphQL(ctx, searchPullRequestsGraphQL, map[string]string{
				"searchQuery": query,
				"after":       after,
			})
			if err != nil {
				return nil, err
			}
			var response graphQLSearchResponse
			if err := json.Unmarshal([]byte(result.Stdout), &response); err != nil {
				return nil, fmt.Errorf("parse GitHub pull request memory response: %w", err)
			}
			if err := graphQLErrors(response.Errors); err != nil {
				return nil, err
			}
			for _, node := range response.Data.Search.Nodes {
				record := prMemoryRecordFromGraphQL(node, fetchedAt)
				if record.Number > 0 {
					out = append(out, record)
				}
			}
			page := response.Data.Search.PageInfo
			if err := checkRateLimit(response.Data.RateLimit, page.HasNextPage); err != nil {
				return nil, err
			}
			if !page.HasNextPage {
				break
			}
			after = page.EndCursor
			if after == "" {
				return nil, errors.New("GitHub pull request memory pagination returned an empty cursor")
			}
		}
	}
	return dedupePRRecords(out), nil
}

func fetchPullRequestByNumber(ctx context.Context, runner pr.Runner, owner, name string, number int, fetchedAt string) (artifactdb.PRMemoryRecord, error) {
	result, err := runner.GraphQL(ctx, pullRequestByNumberGraphQL, map[string]string{
		"owner":  owner,
		"name":   name,
		"number": strconv.Itoa(number),
	})
	if err != nil {
		return artifactdb.PRMemoryRecord{}, err
	}
	var response graphQLPRResponse
	if err := json.Unmarshal([]byte(result.Stdout), &response); err != nil {
		return artifactdb.PRMemoryRecord{}, fmt.Errorf("parse GitHub pull request response: %w", err)
	}
	if err := graphQLErrors(response.Errors); err != nil {
		return artifactdb.PRMemoryRecord{}, err
	}
	if response.Data.Repository.PullRequest == nil {
		return artifactdb.PRMemoryRecord{}, fmt.Errorf("pull request #%d not found", number)
	}
	if err := checkRateLimit(response.Data.RateLimit, false); err != nil {
		return artifactdb.PRMemoryRecord{}, err
	}
	return prMemoryRecordFromGraphQL(*response.Data.Repository.PullRequest, fetchedAt), nil
}

func prMemoryRecordFromGraphQL(pr graphQLPullRequest, fetchedAt string) artifactdb.PRMemoryRecord {
	mergedAt := ""
	if pr.MergedAt != nil {
		mergedAt = *pr.MergedAt
	}
	return artifactdb.PRMemoryRecord{
		Repo:      pr.Repository.NameWithOwner,
		Number:    pr.Number,
		URL:       pr.URL,
		State:     strings.ToLower(pr.State),
		Title:     pr.Title,
		Body:      pr.Body,
		UpdatedAt: pr.UpdatedAt,
		MergedAt:  mergedAt,
		FetchedAt: fetchedAt,
	}
}

func filterRepoRecords(records []artifactdb.PRMemoryRecord, repo string) []artifactdb.PRMemoryRecord {
	out := records[:0]
	for _, record := range records {
		if record.Repo == "" || strings.EqualFold(record.Repo, repo) {
			record.Repo = repo
			out = append(out, record)
		}
	}
	return out
}

func dedupePRRecords(records []artifactdb.PRMemoryRecord) []artifactdb.PRMemoryRecord {
	seen := map[string]int{}
	out := make([]artifactdb.PRMemoryRecord, 0, len(records))
	for _, record := range records {
		key := record.Repo + "#" + strconv.Itoa(record.Number)
		if idx, ok := seen[key]; ok {
			out[idx] = record
			continue
		}
		seen[key] = len(out)
		out = append(out, record)
	}
	return out
}

func countIncludedPRMemory(records []artifactdb.PRMemoryRecord) int {
	count := 0
	for _, record := range records {
		switch strings.ToLower(strings.TrimSpace(record.State)) {
		case "open", "merged":
			count++
		}
	}
	return count
}

func graphQLErrors(items []graphQLError) error {
	if len(items) == 0 {
		return nil
	}
	messages := make([]string, 0, len(items))
	for _, item := range items {
		if strings.TrimSpace(item.Message) != "" {
			messages = append(messages, item.Message)
		}
	}
	if len(messages) == 0 {
		return errors.New("GitHub GraphQL request failed")
	}
	return errors.New(strings.Join(messages, "; "))
}

func checkRateLimit(rate graphQLRateLimit, hasNextPage bool) error {
	if rate.Remaining <= 0 {
		return RateLimitError{Remaining: rate.Remaining, ResetAt: rate.ResetAt}
	}
	if hasNextPage && rate.Remaining <= 1 {
		return RateLimitError{Remaining: rate.Remaining, ResetAt: rate.ResetAt}
	}
	return nil
}
