package memory

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/aki-0421/loop/internal/artifactdb"
	"github.com/aki-0421/loop/internal/pr"
)

const (
	LabelQuestion = "loop:question"
	LabelProposal = "loop:proposal"
)

type ContextSyncResult struct {
	Repo     string
	Full     bool
	Since    string
	Fetched  int
	Upserted int
	SyncedAt string
	Records  []Record
}

type IssueQuestionOptions struct {
	WorkDir     string
	RunsDir     string
	Title       string
	Body        string
	RunID       string
	IterationID string
	GHPath      string
}

type IssueReportOptions struct {
	WorkDir     string
	RunsDir     string
	Title       string
	Body        string
	Kind        string
	RunID       string
	IterationID string
	GHPath      string
}

func SyncIssues(ctx context.Context, opts SyncOptions) (ContextSyncResult, error) {
	repo, _, _, err := ResolveGitHubRepository(ctx, opts.WorkDir)
	if err != nil {
		return ContextSyncResult{}, err
	}
	globalPath := artifactdb.GlobalDBPathFromRunsPath(opts.RunsDir)
	syncedAt := time.Now().UTC().Format(time.RFC3339)
	lastSync, err := artifactdb.GitHubContextLastSync(globalPath, issueContextSyncKey(repo))
	if err != nil {
		return ContextSyncResult{}, err
	}
	full := opts.Full || lastSync == ""
	query := fmt.Sprintf("repo:%s is:issue", repo)
	if !full {
		query = fmt.Sprintf("repo:%s is:issue updated:>=%s", repo, lastSync)
	}
	runner := pr.Runner{Dir: opts.WorkDir, GHPath: opts.GHPath}
	records, err := fetchGitHubContextSearch(ctx, runner, []contextSearchRequest{{Query: query, IncludeIssues: true}}, syncedAt)
	if err != nil {
		return ContextSyncResult{}, err
	}
	records = filterContextRecords(records, repo)
	upserted := 0
	if full {
		if err := artifactdb.ReplaceGitHubContext(globalPath, repo, []string{"issue", "issue-comment"}, records, syncedAt, issueContextSyncKey(repo)); err != nil {
			return ContextSyncResult{}, err
		}
		upserted = len(records)
	} else {
		upserted, err = artifactdb.ApplyGitHubContextSync(globalPath, repo, records, syncedAt, issueContextSyncKey(repo))
		if err != nil {
			return ContextSyncResult{}, err
		}
	}
	_, _ = artifactdb.ApplyGitHubContextSync(globalPath, repo, nil, syncedAt, updateContextSyncKey(repo))
	return ContextSyncResult{Repo: repo, Full: full, Since: lastSync, Fetched: len(records), Upserted: upserted, SyncedAt: syncedAt, Records: recordsFromGitHubContext(records)}, nil
}

func SyncGitHubUpdates(ctx context.Context, opts SyncOptions) (ContextSyncResult, error) {
	repo, _, _, err := ResolveGitHubRepository(ctx, opts.WorkDir)
	if err != nil {
		return ContextSyncResult{}, err
	}
	globalPath := artifactdb.GlobalDBPathFromRunsPath(opts.RunsDir)
	syncedAt := time.Now().UTC().Format(time.RFC3339)
	lastSync, err := artifactdb.GitHubContextLastSync(globalPath, updateContextSyncKey(repo))
	if err != nil {
		return ContextSyncResult{}, err
	}
	if lastSync == "" {
		lastSync = syncedAt
	}
	runner := pr.Runner{Dir: opts.WorkDir, GHPath: opts.GHPath}
	records, err := fetchGitHubContextSearch(ctx, runner, []contextSearchRequest{
		{Query: fmt.Sprintf("repo:%s is:issue updated:>=%s", repo, lastSync), IncludeIssues: true},
		{Query: fmt.Sprintf("repo:%s is:pr updated:>=%s", repo, lastSync), IncludePRComments: true},
	}, syncedAt)
	if err != nil {
		return ContextSyncResult{}, err
	}
	records = filterContextRecords(records, repo)
	upserted, err := artifactdb.ApplyGitHubContextSync(globalPath, repo, records, syncedAt, updateContextSyncKey(repo))
	if err != nil {
		return ContextSyncResult{}, err
	}
	return ContextSyncResult{Repo: repo, Full: false, Since: lastSync, Fetched: len(records), Upserted: upserted, SyncedAt: syncedAt, Records: recordsFromGitHubContext(records)}, nil
}

func CreateIssueQuestion(ctx context.Context, opts IssueQuestionOptions) (Record, error) {
	title := strings.TrimSpace(opts.Title)
	body := strings.TrimSpace(opts.Body)
	if title == "" {
		return Record{}, errors.New("issue title is required")
	}
	if body == "" {
		return Record{}, errors.New("issue body is required")
	}
	repo, owner, name, err := ResolveGitHubRepository(ctx, opts.WorkDir)
	if err != nil {
		return Record{}, err
	}
	runner := pr.Runner{Dir: opts.WorkDir, GHPath: opts.GHPath}
	required := []string{LabelQuestion}
	repositoryID, labels, err := ensureLoopLabels(ctx, runner, owner, name, required)
	if err != nil {
		return Record{}, err
	}
	body = appendQuestionMetadata(body, opts)
	issue, err := createQuestionIssue(ctx, runner, repositoryID, title, body, labels)
	if err != nil {
		return Record{}, err
	}
	issue.Repo = repo
	globalPath := artifactdb.GlobalDBPathFromRunsPath(opts.RunsDir)
	if err := artifactdb.UpsertGitHubContext(globalPath, issue); err != nil {
		return Record{}, err
	}
	return recordFromGitHubContext(issue), nil
}

func CreateIssueReport(ctx context.Context, opts IssueReportOptions) (Record, error) {
	title := strings.TrimSpace(opts.Title)
	body := strings.TrimSpace(opts.Body)
	if title == "" {
		return Record{}, errors.New("issue title is required")
	}
	if body == "" {
		return Record{}, errors.New("issue body is required")
	}
	repo, owner, name, err := ResolveGitHubRepository(ctx, opts.WorkDir)
	if err != nil {
		return Record{}, err
	}
	runner := pr.Runner{Dir: opts.WorkDir, GHPath: opts.GHPath}
	required := []string{LabelProposal}
	repositoryID, labels, err := ensureLoopLabels(ctx, runner, owner, name, required)
	if err != nil {
		return Record{}, err
	}
	body = appendReportMetadata(body, opts)
	issue, err := createQuestionIssue(ctx, runner, repositoryID, title, body, labels)
	if err != nil {
		return Record{}, err
	}
	issue.Repo = repo
	globalPath := artifactdb.GlobalDBPathFromRunsPath(opts.RunsDir)
	if err := artifactdb.UpsertGitHubContext(globalPath, issue); err != nil {
		return Record{}, err
	}
	return recordFromGitHubContext(issue), nil
}

const searchGitHubContextGraphQL = `
query($searchQuery: String!, $after: String) {
  search(type: ISSUE, query: $searchQuery, first: 50, after: $after) {
    pageInfo {
      hasNextPage
      endCursor
    }
    nodes {
      __typename
      ... on Issue {
        number
        url
        state
        title
        body
        updatedAt
        closedAt
        author { login }
        labels(first: 20) { nodes { name } }
        repository { nameWithOwner }
        comments(first: 20, orderBy: {field: UPDATED_AT, direction: DESC}) {
          nodes {
            id
            url
            body
            updatedAt
            author { login }
          }
        }
      }
      ... on PullRequest {
        number
        url
        state
        title
        body
        updatedAt
        closedAt
        mergedAt
        author { login }
        labels(first: 20) { nodes { name } }
        repository { nameWithOwner }
        comments(first: 20, orderBy: {field: UPDATED_AT, direction: DESC}) {
          nodes {
            id
            url
            body
            updatedAt
            author { login }
          }
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

const repositoryLabelsGraphQL = `
query($owner: String!, $name: String!, $labelQuery: String!) {
  repository(owner: $owner, name: $name) {
    id
    labels(first: 100, query: $labelQuery) {
      nodes {
        id
        name
      }
    }
  }
  rateLimit {
    remaining
    resetAt
    cost
  }
}`

const createLabelGraphQL = `
mutation($repositoryId: ID!, $name: String!, $color: String!, $description: String!) {
  createLabel(input: {repositoryId: $repositoryId, name: $name, color: $color, description: $description}) {
    label {
      id
      name
    }
  }
}`

type contextSearchRequest struct {
	Query             string
	IncludeIssues     bool
	IncludePRComments bool
}

type graphQLContextLabel struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

type graphQLContextComment struct {
	ID        string `json:"id"`
	URL       string `json:"url"`
	Body      string `json:"body"`
	UpdatedAt string `json:"updatedAt"`
	Author    *struct {
		Login string `json:"login"`
	} `json:"author"`
}

type graphQLContextNode struct {
	Typename  string `json:"__typename"`
	Number    int    `json:"number"`
	URL       string `json:"url"`
	State     string `json:"state"`
	Title     string `json:"title"`
	Body      string `json:"body"`
	UpdatedAt string `json:"updatedAt"`
	ClosedAt  string `json:"closedAt"`
	MergedAt  string `json:"mergedAt"`
	Author    *struct {
		Login string `json:"login"`
	} `json:"author"`
	Labels struct {
		Nodes []graphQLContextLabel `json:"nodes"`
	} `json:"labels"`
	Repository struct {
		NameWithOwner string `json:"nameWithOwner"`
	} `json:"repository"`
	Comments struct {
		Nodes []graphQLContextComment `json:"nodes"`
	} `json:"comments"`
}

type graphQLContextSearchResponse struct {
	Data struct {
		Search struct {
			PageInfo graphQLPageInfo      `json:"pageInfo"`
			Nodes    []graphQLContextNode `json:"nodes"`
		} `json:"search"`
		RateLimit graphQLRateLimit `json:"rateLimit"`
	} `json:"data"`
	Errors []graphQLError `json:"errors"`
}

type graphQLRepositoryLabelsResponse struct {
	Data struct {
		Repository struct {
			ID     string `json:"id"`
			Labels struct {
				Nodes []graphQLContextLabel `json:"nodes"`
			} `json:"labels"`
		} `json:"repository"`
		RateLimit graphQLRateLimit `json:"rateLimit"`
	} `json:"data"`
	Errors []graphQLError `json:"errors"`
}

type graphQLCreateLabelResponse struct {
	Data struct {
		CreateLabel struct {
			Label graphQLContextLabel `json:"label"`
		} `json:"createLabel"`
	} `json:"data"`
	Errors []graphQLError `json:"errors"`
}

type graphQLCreateIssueResponse struct {
	Data struct {
		CreateIssue struct {
			Issue graphQLContextNode `json:"issue"`
		} `json:"createIssue"`
	} `json:"data"`
	Errors []graphQLError `json:"errors"`
}

func fetchGitHubContextSearch(ctx context.Context, runner pr.Runner, requests []contextSearchRequest, fetchedAt string) ([]artifactdb.GitHubContextRecord, error) {
	var out []artifactdb.GitHubContextRecord
	for _, request := range requests {
		after := ""
		for {
			result, err := runner.GraphQL(ctx, searchGitHubContextGraphQL, map[string]string{
				"searchQuery": request.Query,
				"after":       after,
			})
			if err != nil {
				return nil, err
			}
			var response graphQLContextSearchResponse
			if err := json.Unmarshal([]byte(result.Stdout), &response); err != nil {
				return nil, fmt.Errorf("parse GitHub context response: %w", err)
			}
			if err := graphQLErrors(response.Errors); err != nil {
				return nil, err
			}
			for _, node := range response.Data.Search.Nodes {
				switch node.Typename {
				case "Issue":
					if request.IncludeIssues {
						out = append(out, issueContextRecordFromGraphQL(node, fetchedAt))
						out = append(out, commentContextRecordsFromGraphQL(node, "issue-comment", fetchedAt)...)
					}
				case "PullRequest":
					if request.IncludePRComments {
						out = append(out, commentContextRecordsFromGraphQL(node, "pr-comment", fetchedAt)...)
					}
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
				return nil, errors.New("GitHub context pagination returned an empty cursor")
			}
		}
	}
	return dedupeContextRecords(out), nil
}

func ensureLoopLabels(ctx context.Context, runner pr.Runner, owner, name string, required []string) (string, []graphQLContextLabel, error) {
	result, err := runner.GraphQL(ctx, repositoryLabelsGraphQL, map[string]string{
		"owner":      owner,
		"name":       name,
		"labelQuery": "loop:",
	})
	if err != nil {
		return "", nil, err
	}
	var response graphQLRepositoryLabelsResponse
	if err := json.Unmarshal([]byte(result.Stdout), &response); err != nil {
		return "", nil, fmt.Errorf("parse GitHub label response: %w", err)
	}
	if err := graphQLErrors(response.Errors); err != nil {
		return "", nil, err
	}
	if err := checkRateLimit(response.Data.RateLimit, false); err != nil {
		return "", nil, err
	}
	repositoryID := response.Data.Repository.ID
	if repositoryID == "" {
		return "", nil, errors.New("GitHub repository id was not returned")
	}
	labelsByName := map[string]graphQLContextLabel{}
	for _, label := range response.Data.Repository.Labels.Nodes {
		labelsByName[label.Name] = label
	}
	labels := make([]graphQLContextLabel, 0, len(required))
	for _, labelName := range required {
		labelName = strings.TrimSpace(labelName)
		if labelName == "" {
			continue
		}
		label, ok := labelsByName[labelName]
		if !ok {
			label, err = createLoopLabel(ctx, runner, repositoryID, labelName)
			if err != nil {
				return "", nil, err
			}
		}
		labels = append(labels, label)
	}
	return repositoryID, labels, nil
}

func createLoopLabel(ctx context.Context, runner pr.Runner, repositoryID, name string) (graphQLContextLabel, error) {
	color, description := loopLabelPresentation(name)
	result, err := runner.GraphQL(ctx, createLabelGraphQL, map[string]string{
		"repositoryId": repositoryID,
		"name":         name,
		"color":        color,
		"description":  description,
	})
	if err != nil {
		return graphQLContextLabel{}, err
	}
	var response graphQLCreateLabelResponse
	if err := json.Unmarshal([]byte(result.Stdout), &response); err != nil {
		return graphQLContextLabel{}, fmt.Errorf("parse GitHub create label response: %w", err)
	}
	if err := graphQLErrors(response.Errors); err != nil {
		return graphQLContextLabel{}, err
	}
	if response.Data.CreateLabel.Label.ID == "" {
		return graphQLContextLabel{}, fmt.Errorf("GitHub label %q was not created", name)
	}
	return response.Data.CreateLabel.Label, nil
}

func loopLabelPresentation(name string) (string, string) {
	switch name {
	case LabelProposal:
		return "1D76DB", "Improvement proposal from loop agent"
	default:
		return "FBCA04", "Clarification requested by loop"
	}
}

func createQuestionIssue(ctx context.Context, runner pr.Runner, repositoryID, title, body string, labels []graphQLContextLabel) (artifactdb.GitHubContextRecord, error) {
	query := createIssueGraphQL(labels)
	result, err := runner.GraphQL(ctx, query, map[string]string{
		"repositoryId": repositoryID,
		"title":        title,
		"body":         body,
	})
	if err != nil {
		return artifactdb.GitHubContextRecord{}, err
	}
	var response graphQLCreateIssueResponse
	if err := json.Unmarshal([]byte(result.Stdout), &response); err != nil {
		return artifactdb.GitHubContextRecord{}, fmt.Errorf("parse GitHub create issue response: %w", err)
	}
	if err := graphQLErrors(response.Errors); err != nil {
		return artifactdb.GitHubContextRecord{}, err
	}
	record := issueContextRecordFromGraphQL(response.Data.CreateIssue.Issue, time.Now().UTC().Format(time.RFC3339))
	if record.Number <= 0 || record.URL == "" {
		return artifactdb.GitHubContextRecord{}, errors.New("GitHub issue was not created")
	}
	record.Labels = labelsString(labels)
	return record, nil
}

func createIssueGraphQL(labels []graphQLContextLabel) string {
	var quoted []string
	for _, label := range labels {
		if label.ID == "" {
			continue
		}
		quoted = append(quoted, strconv.Quote(label.ID))
	}
	labelIDs := strings.Join(quoted, ", ")
	if labelIDs != "" {
		labelIDs = ", labelIds: [" + labelIDs + "]"
	}
	return `
mutation($repositoryId: ID!, $title: String!, $body: String!) {
  createIssue(input: {repositoryId: $repositoryId, title: $title, body: $body` + labelIDs + `}) {
    issue {
      __typename
      number
      url
      state
      title
      body
      updatedAt
      closedAt
      author { login }
      labels(first: 20) { nodes { name } }
      repository { nameWithOwner }
    }
  }
}`
}

func appendQuestionMetadata(body string, opts IssueQuestionOptions) string {
	var b strings.Builder
	b.WriteString(strings.TrimRight(body, "\n"))
	b.WriteString("\n\n<!-- loop:question\n")
	if opts.RunID != "" {
		fmt.Fprintf(&b, "run_id: %s\n", opts.RunID)
	}
	if opts.IterationID != "" {
		fmt.Fprintf(&b, "iteration_id: %s\n", opts.IterationID)
	}
	b.WriteString("-->\n")
	return b.String()
}

func appendReportMetadata(body string, opts IssueReportOptions) string {
	var b strings.Builder
	b.WriteString(strings.TrimRight(body, "\n"))
	b.WriteString("\n\n<!-- loop:proposal\n")
	if opts.RunID != "" {
		fmt.Fprintf(&b, "run_id: %s\n", opts.RunID)
	}
	if opts.IterationID != "" {
		fmt.Fprintf(&b, "iteration_id: %s\n", opts.IterationID)
	}
	fmt.Fprintf(&b, "kind: %s\n", normalizeReportKind(opts.Kind))
	b.WriteString("-->\n")
	return b.String()
}

func normalizeReportKind(kind string) string {
	switch strings.ToLower(strings.TrimSpace(kind)) {
	case "tool", "tools":
		return "tool"
	case "doc", "docs", "documentation":
		return "docs"
	case "guardrail", "guardrails":
		return "guardrail"
	case "observability", "logs", "metrics", "trace", "traces":
		return "observability"
	case "env", "environment":
		return "environment"
	case "workflow", "process":
		return "workflow"
	case "other", "":
		return "other"
	default:
		return "other"
	}
}

func issueContextRecordFromGraphQL(node graphQLContextNode, fetchedAt string) artifactdb.GitHubContextRecord {
	return artifactdb.GitHubContextRecord{
		Repo:      node.Repository.NameWithOwner,
		Kind:      "issue",
		Number:    node.Number,
		URL:       node.URL,
		State:     strings.ToLower(node.State),
		Title:     node.Title,
		Body:      node.Body,
		Author:    contextAuthor(node.Author),
		Labels:    labelsString(node.Labels.Nodes),
		UpdatedAt: node.UpdatedAt,
		ClosedAt:  node.ClosedAt,
		FetchedAt: fetchedAt,
	}
}

func commentContextRecordsFromGraphQL(node graphQLContextNode, kind, fetchedAt string) []artifactdb.GitHubContextRecord {
	records := make([]artifactdb.GitHubContextRecord, 0, len(node.Comments.Nodes))
	for _, comment := range node.Comments.Nodes {
		if comment.ID == "" || strings.TrimSpace(comment.Body) == "" {
			continue
		}
		records = append(records, artifactdb.GitHubContextRecord{
			Repo:      node.Repository.NameWithOwner,
			Kind:      kind,
			Number:    node.Number,
			CommentID: comment.ID,
			URL:       comment.URL,
			State:     strings.ToLower(node.State),
			Title:     node.Title,
			Body:      comment.Body,
			Author:    contextAuthor(comment.Author),
			Labels:    labelsString(node.Labels.Nodes),
			UpdatedAt: comment.UpdatedAt,
			ClosedAt:  node.ClosedAt,
			FetchedAt: fetchedAt,
		})
	}
	return records
}

func filterContextRecords(records []artifactdb.GitHubContextRecord, repo string) []artifactdb.GitHubContextRecord {
	out := records[:0]
	for _, record := range records {
		if record.Repo == "" || strings.EqualFold(record.Repo, repo) {
			record.Repo = repo
			out = append(out, record)
		}
	}
	return out
}

func dedupeContextRecords(records []artifactdb.GitHubContextRecord) []artifactdb.GitHubContextRecord {
	seen := map[string]int{}
	out := make([]artifactdb.GitHubContextRecord, 0, len(records))
	for _, record := range records {
		key := strings.Join([]string{record.Repo, record.Kind, strconv.Itoa(record.Number), record.CommentID}, "\x00")
		if idx, ok := seen[key]; ok {
			out[idx] = record
			continue
		}
		seen[key] = len(out)
		out = append(out, record)
	}
	return out
}

func labelsString(labels []graphQLContextLabel) string {
	names := make([]string, 0, len(labels))
	seen := map[string]bool{}
	for _, label := range labels {
		name := strings.TrimSpace(label.Name)
		if name == "" || seen[name] {
			continue
		}
		seen[name] = true
		names = append(names, name)
	}
	return strings.Join(names, ",")
}

func contextAuthor(author *struct {
	Login string `json:"login"`
}) string {
	if author == nil {
		return ""
	}
	return author.Login
}

func issueContextSyncKey(repo string) string {
	return "github_context.issues.last_sync." + repo
}

func updateContextSyncKey(repo string) string {
	return "github_context.updates.last_sync." + repo
}
