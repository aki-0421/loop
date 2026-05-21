package artifactdb

import (
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
	"unicode"

	_ "modernc.org/sqlite"
)

const GlobalDBName = "loop.db"

var ErrNotFound = errors.New("artifact not found")

type Artifact struct {
	Name      string
	Content   string
	UpdatedAt string
}

type SearchOptions struct {
	Query       string
	RunID       string
	IterationID string
	Artifact    string
	Limit       int
}

type SearchHit struct {
	RunID       string
	IterationID string
	Artifact    string
	Content     string
	Rank        float64
}

type PRMemoryRecord struct {
	Repo      string
	Number    int
	URL       string
	State     string
	Title     string
	Body      string
	UpdatedAt string
	MergedAt  string
	FetchedAt string
}

type PRMemorySearchOptions struct {
	Query string
	Repo  string
	Limit int
}

type PRMemorySearchHit struct {
	Record PRMemoryRecord
	Rank   float64
}

type GitHubContextRecord struct {
	Repo      string
	Kind      string
	Number    int
	CommentID string
	URL       string
	State     string
	Title     string
	Body      string
	Author    string
	Labels    string
	UpdatedAt string
	ClosedAt  string
	FetchedAt string
}

type GitHubContextSearchOptions struct {
	Query string
	Repo  string
	Kind  string
	Limit int
}

type GitHubContextSearchHit struct {
	Record GitHubContextRecord
	Rank   float64
}

func GlobalDBPathForIteration(iterationDir string) string {
	clean := filepath.Clean(iterationDir)
	parts := splitPath(clean)
	for i := 0; i < len(parts)-1; i++ {
		if parts[i] == ".loop" && parts[i+1] == "runs" {
			return filepath.Join(joinPath(parts[:i+1]), GlobalDBName)
		}
	}
	return ""
}

func GlobalDBPathFromRunsPath(path string) string {
	clean := filepath.Clean(path)
	parts := splitPath(clean)
	for i := 0; i < len(parts)-1; i++ {
		if parts[i] == ".loop" && parts[i+1] == "runs" {
			return filepath.Join(joinPath(parts[:i+1]), GlobalDBName)
		}
	}
	return filepath.Join(filepath.Dir(clean), GlobalDBName)
}

func ParseIterationDir(iterationDir string) (string, string) {
	parts := splitPath(filepath.Clean(iterationDir))
	for i := 0; i < len(parts)-3; i++ {
		if parts[i] == ".loop" && parts[i+1] == "runs" && parts[i+3] == "iterations" && i+4 < len(parts) {
			return parts[i+2], parts[i+4]
		}
	}
	if len(parts) >= 1 {
		return "", parts[len(parts)-1]
	}
	return "", ""
}

var artifactFileNames = map[string]string{
	"runtime":            "runtime.json",
	"plan":               "plan.md",
	"todo":               "todo.md",
	"worklog":            "worklog.md",
	"validation":         "validation.md",
	"pr-title":           "pr-title.txt",
	"pr-body":            "pr-body.md",
	"pr-state":           "pr-state.json",
	"pr-checks":          "pr-checks.json",
	"pr-check-log":       "pr-check-log.txt",
	"github-updates":     "github-updates.md",
	"agent-prompt-audit": "agent-prompt-audit.md",
}

func ArtifactPath(iterationDir, name string) (string, error) {
	if strings.TrimSpace(iterationDir) == "" {
		return "", errors.New("iteration directory is required")
	}
	file, ok := artifactFileNames[name]
	if !ok {
		return "", fmt.Errorf("%w: %s", ErrNotFound, name)
	}
	return filepath.Join(iterationDir, file), nil
}

func ArtifactNames() []string {
	names := make([]string, 0, len(artifactFileNames))
	for name := range artifactFileNames {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func ValidationOutputPath(iterationDir, name string) (string, error) {
	name = sanitizeArtifactFilePart(name)
	if name == "" {
		name = "command"
	}
	return filepath.Join(iterationDir, "validation-output-"+name+".log"), nil
}

func sanitizeArtifactFilePart(name string) string {
	name = strings.ToLower(strings.TrimSpace(name))
	var b strings.Builder
	lastDash := false
	for _, r := range name {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
			lastDash = false
			continue
		}
		if !lastDash {
			b.WriteByte('-')
			lastDash = true
		}
	}
	return strings.Trim(b.String(), "-")
}

func Read(iterationDir, name string) (string, error) {
	path, err := ArtifactPath(iterationDir, name)
	if err != nil {
		return "", err
	}
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return "", fmt.Errorf("%w: %s", ErrNotFound, name)
	}
	if err != nil {
		return "", err
	}
	return string(data), nil
}

func Write(iterationDir, name, content string) error {
	path, err := ArtifactPath(iterationDir, name)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, []byte(content), 0o644)
}

func Append(iterationDir, name, content string) error {
	path, err := ArtifactPath(iterationDir, name)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = f.WriteString(content)
	return err
}

func WriteValidationOutput(iterationDir, name, output string) error {
	path, err := ValidationOutputPath(iterationDir, name)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, []byte(output), 0o644)
}

func List(iterationDir string) ([]Artifact, error) {
	var out []Artifact
	for _, name := range ArtifactNames() {
		path, err := ArtifactPath(iterationDir, name)
		if err != nil {
			return nil, err
		}
		data, err := os.ReadFile(path)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return nil, err
		}
		info, err := os.Stat(path)
		if err != nil {
			return nil, err
		}
		out = append(out, Artifact{Name: name, Content: string(data), UpdatedAt: info.ModTime().UTC().Format(time.RFC3339)})
	}
	return out, nil
}

func MirrorGlobal(globalDBPath, runID, iterationID, artifact, content string) error {
	if globalDBPath == "" || runID == "" || iterationID == "" {
		return nil
	}
	db, err := openGlobal(globalDBPath)
	if err != nil {
		return err
	}
	defer db.Close()
	if err := ensureGlobal(db); err != nil {
		return err
	}
	updated := now()
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`INSERT INTO runs(run_id, updated_at) VALUES(?, ?)
ON CONFLICT(run_id) DO UPDATE SET updated_at = excluded.updated_at`, runID, updated); err != nil {
		return err
	}
	if _, err := tx.Exec(`INSERT INTO iterations(run_id, iteration_id, updated_at) VALUES(?, ?, ?)
ON CONFLICT(run_id, iteration_id) DO UPDATE SET updated_at = excluded.updated_at`, runID, iterationID, updated); err != nil {
		return err
	}
	if _, err := tx.Exec(`INSERT INTO artifact_index(run_id, iteration_id, artifact, content, updated_at) VALUES(?, ?, ?, ?, ?)
ON CONFLICT(run_id, iteration_id, artifact) DO UPDATE SET content = excluded.content, updated_at = excluded.updated_at`,
		runID, iterationID, artifact, content, updated); err != nil {
		return err
	}
	if _, err := tx.Exec(`DELETE FROM artifact_index_fts WHERE run_id = ? AND iteration_id = ? AND artifact = ?`, runID, iterationID, artifact); err != nil {
		return err
	}
	if strings.TrimSpace(content) != "" {
		if _, err := tx.Exec(`INSERT INTO artifact_index_fts(run_id, iteration_id, artifact, content) VALUES(?, ?, ?, ?)`, runID, iterationID, artifact, content); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func RecentSummaries(globalDBPath, runID string, limit int) ([]string, error) {
	db, err := openGlobal(globalDBPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	defer db.Close()
	if err := ensureGlobal(db); err != nil {
		return nil, err
	}
	query := `SELECT content FROM artifact_index WHERE artifact = 'summary'`
	args := []any{}
	if runID != "" {
		query += ` AND run_id = ?`
		args = append(args, runID)
	}
	query += ` ORDER BY run_id DESC, iteration_id DESC`
	if limit > 0 {
		query += ` LIMIT ?`
		args = append(args, limit)
	}
	rows, err := db.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var content string
		if err := rows.Scan(&content); err != nil {
			return nil, err
		}
		out = append(out, content)
	}
	return out, rows.Err()
}

func SummaryHits(globalDBPath, runID string, limit int) ([]SearchHit, error) {
	db, err := openGlobal(globalDBPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	defer db.Close()
	if err := ensureGlobal(db); err != nil {
		return nil, err
	}
	query := `SELECT run_id, iteration_id, artifact, content FROM artifact_index WHERE artifact = 'summary'`
	args := []any{}
	if runID != "" {
		query += ` AND run_id = ?`
		args = append(args, runID)
	}
	query += ` ORDER BY run_id DESC, iteration_id DESC`
	if limit > 0 {
		query += ` LIMIT ?`
		args = append(args, limit)
	}
	rows, err := db.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []SearchHit
	for rows.Next() {
		var hit SearchHit
		if err := rows.Scan(&hit.RunID, &hit.IterationID, &hit.Artifact, &hit.Content); err != nil {
			return nil, err
		}
		out = append(out, hit)
	}
	return out, rows.Err()
}

func SearchGlobal(globalDBPath string, opts SearchOptions) ([]SearchHit, error) {
	query := ftsQuery(opts.Query)
	if query == "" {
		return nil, nil
	}
	db, err := openGlobal(globalDBPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	defer db.Close()
	if err := ensureGlobal(db); err != nil {
		return nil, err
	}
	sqlText := `SELECT run_id, iteration_id, artifact, content, bm25(artifact_index_fts) AS rank
FROM artifact_index_fts WHERE artifact_index_fts MATCH ?`
	args := []any{query}
	if opts.RunID != "" {
		sqlText += ` AND run_id = ?`
		args = append(args, opts.RunID)
	}
	if opts.IterationID != "" {
		sqlText += ` AND iteration_id = ?`
		args = append(args, opts.IterationID)
	}
	if opts.Artifact != "" {
		sqlText += ` AND artifact = ?`
		args = append(args, opts.Artifact)
	}
	sqlText += ` ORDER BY rank, run_id DESC, iteration_id DESC`
	if opts.Limit > 0 {
		sqlText += ` LIMIT ?`
		args = append(args, opts.Limit)
	}
	rows, err := db.Query(sqlText, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []SearchHit
	for rows.Next() {
		var hit SearchHit
		if err := rows.Scan(&hit.RunID, &hit.IterationID, &hit.Artifact, &hit.Content, &hit.Rank); err != nil {
			return nil, err
		}
		out = append(out, hit)
	}
	return out, rows.Err()
}

func RebuildGlobalFromRuns(runsDir string) (int, error) {
	globalPath := GlobalDBPathFromRunsPath(runsDir)
	db, err := openGlobal(globalPath)
	if err != nil {
		return 0, err
	}
	defer db.Close()
	if err := ensureGlobal(db); err != nil {
		return 0, err
	}
	for _, stmt := range []string{
		`DELETE FROM artifact_index`,
		`DELETE FROM artifact_index_fts`,
		`DELETE FROM iterations`,
		`DELETE FROM runs`,
		`DELETE FROM iteration_results`,
	} {
		if _, err := db.Exec(stmt); err != nil {
			return 0, err
		}
	}
	return 0, nil
}

func WriteResultHandoff(globalDBPath, runID, iterationID, resultJSON string) error {
	if runID == "" || iterationID == "" {
		return errors.New("run id and iteration id are required")
	}
	db, err := openGlobal(globalDBPath)
	if err != nil {
		return err
	}
	defer db.Close()
	if err := ensureGlobal(db); err != nil {
		return err
	}
	_, err = db.Exec(`INSERT INTO iteration_results(run_id, iteration_id, result_json, updated_at) VALUES(?, ?, ?, ?)
ON CONFLICT(run_id, iteration_id) DO UPDATE SET result_json = excluded.result_json, updated_at = excluded.updated_at`,
		runID, iterationID, resultJSON, now())
	return err
}

func ReadResultHandoff(globalDBPath, runID, iterationID string) (string, error) {
	if runID == "" || iterationID == "" {
		return "", fmt.Errorf("%w: result", ErrNotFound)
	}
	db, err := openGlobal(globalDBPath)
	if err != nil {
		if os.IsNotExist(err) {
			return "", fmt.Errorf("%w: result", ErrNotFound)
		}
		return "", err
	}
	defer db.Close()
	if err := ensureGlobal(db); err != nil {
		return "", err
	}
	var resultJSON string
	err = db.QueryRow(`SELECT result_json FROM iteration_results WHERE run_id = ? AND iteration_id = ?`, runID, iterationID).Scan(&resultJSON)
	if errors.Is(err, sql.ErrNoRows) {
		return "", fmt.Errorf("%w: result", ErrNotFound)
	}
	return resultJSON, err
}

func ClearResultHandoff(globalDBPath, runID, iterationID string) error {
	if runID == "" || iterationID == "" {
		return nil
	}
	db, err := openGlobal(globalDBPath)
	if err != nil {
		return err
	}
	defer db.Close()
	if err := ensureGlobal(db); err != nil {
		return err
	}
	_, err = db.Exec(`DELETE FROM iteration_results WHERE run_id = ? AND iteration_id = ?`, runID, iterationID)
	return err
}

func UpsertPRMemory(globalDBPath string, record PRMemoryRecord) error {
	if record.Repo == "" || record.Number <= 0 {
		return errors.New("repo and positive pull request number are required")
	}
	db, err := openGlobal(globalDBPath)
	if err != nil {
		return err
	}
	defer db.Close()
	if err := ensureGlobal(db); err != nil {
		return err
	}
	return upsertPRMemory(db, record)
}

func ReplacePRMemory(globalDBPath, repo string, records []PRMemoryRecord, syncedAt string) error {
	if repo == "" {
		return errors.New("repo is required")
	}
	if syncedAt == "" {
		syncedAt = now()
	}
	db, err := openGlobal(globalDBPath)
	if err != nil {
		return err
	}
	defer db.Close()
	if err := ensureGlobal(db); err != nil {
		return err
	}
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`DELETE FROM github_records WHERE repo = ? AND kind = ?`, repo, "pr"); err != nil {
		return err
	}
	if _, err := tx.Exec(`DELETE FROM github_records_fts WHERE repo = ? AND kind = ?`, repo, "pr"); err != nil {
		return err
	}
	for _, record := range records {
		record.Repo = repo
		if !includePRMemory(record) {
			continue
		}
		if err := upsertPRMemoryTx(tx, record); err != nil {
			return err
		}
	}
	if err := setGlobalMetadataTx(tx, "pr_memory.last_sync."+repo, syncedAt); err != nil {
		return err
	}
	return tx.Commit()
}

func ApplyPRMemorySync(globalDBPath, repo string, records []PRMemoryRecord, syncedAt string) (upserted, deleted int, err error) {
	if repo == "" {
		return 0, 0, errors.New("repo is required")
	}
	if syncedAt == "" {
		syncedAt = now()
	}
	db, err := openGlobal(globalDBPath)
	if err != nil {
		return 0, 0, err
	}
	defer db.Close()
	if err := ensureGlobal(db); err != nil {
		return 0, 0, err
	}
	tx, err := db.Begin()
	if err != nil {
		return 0, 0, err
	}
	defer tx.Rollback()
	for _, record := range records {
		record.Repo = repo
		if includePRMemory(record) {
			if err := upsertPRMemoryTx(tx, record); err != nil {
				return 0, 0, err
			}
			upserted++
			continue
		}
		if record.Number > 0 {
			if err := deletePRMemoryTx(tx, repo, record.Number); err != nil {
				return 0, 0, err
			}
			deleted++
		}
	}
	if err := setGlobalMetadataTx(tx, "pr_memory.last_sync."+repo, syncedAt); err != nil {
		return 0, 0, err
	}
	return upserted, deleted, tx.Commit()
}

func DeletePRMemory(globalDBPath, repo string, number int) error {
	if repo == "" || number <= 0 {
		return errors.New("repo and positive pull request number are required")
	}
	db, err := openGlobal(globalDBPath)
	if err != nil {
		return err
	}
	defer db.Close()
	if err := ensureGlobal(db); err != nil {
		return err
	}
	return deletePRMemory(db, repo, number)
}

func RecentPRMemory(globalDBPath, repo string, limit int) ([]PRMemoryRecord, error) {
	db, err := openGlobal(globalDBPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	defer db.Close()
	if err := ensureGlobal(db); err != nil {
		return nil, err
	}
	query := `SELECT repo, number, url, state, title, body, updated_at, merged_at, fetched_at FROM github_records WHERE kind = ?`
	args := []any{"pr"}
	if repo != "" {
		query += ` AND repo = ?`
		args = append(args, repo)
	}
	query += ` ORDER BY COALESCE(NULLIF(merged_at, ''), updated_at) DESC, number DESC`
	if limit > 0 {
		query += ` LIMIT ?`
		args = append(args, limit)
	}
	rows, err := db.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []PRMemoryRecord
	for rows.Next() {
		var record PRMemoryRecord
		if err := rows.Scan(&record.Repo, &record.Number, &record.URL, &record.State, &record.Title, &record.Body, &record.UpdatedAt, &record.MergedAt, &record.FetchedAt); err != nil {
			return nil, err
		}
		out = append(out, record)
	}
	return out, rows.Err()
}

func SearchPRMemory(globalDBPath string, opts PRMemorySearchOptions) ([]PRMemorySearchHit, error) {
	query := ftsQuery(opts.Query)
	if query == "" {
		return nil, nil
	}
	db, err := openGlobal(globalDBPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	defer db.Close()
	if err := ensureGlobal(db); err != nil {
		return nil, err
	}
	sqlText := `SELECT repo, number, url, state, title, body, updated_at, merged_at, fetched_at, bm25(github_records_fts) AS rank
FROM github_records_fts WHERE github_records_fts MATCH ? AND kind = ?`
	args := []any{query, "pr"}
	if opts.Repo != "" {
		sqlText += ` AND repo = ?`
		args = append(args, opts.Repo)
	}
	sqlText += ` ORDER BY rank, COALESCE(NULLIF(merged_at, ''), updated_at) DESC, number DESC`
	if opts.Limit > 0 {
		sqlText += ` LIMIT ?`
		args = append(args, opts.Limit)
	}
	rows, err := db.Query(sqlText, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []PRMemorySearchHit
	for rows.Next() {
		var hit PRMemorySearchHit
		if err := rows.Scan(&hit.Record.Repo, &hit.Record.Number, &hit.Record.URL, &hit.Record.State, &hit.Record.Title, &hit.Record.Body, &hit.Record.UpdatedAt, &hit.Record.MergedAt, &hit.Record.FetchedAt, &hit.Rank); err != nil {
			return nil, err
		}
		out = append(out, hit)
	}
	return out, rows.Err()
}

func CountPRMemory(globalDBPath, repo string) (int, error) {
	db, err := openGlobal(globalDBPath)
	if err != nil {
		if os.IsNotExist(err) {
			return 0, nil
		}
		return 0, err
	}
	defer db.Close()
	if err := ensureGlobal(db); err != nil {
		return 0, err
	}
	query := `SELECT COUNT(*) FROM github_records WHERE kind = ?`
	args := []any{"pr"}
	if repo != "" {
		query += ` AND repo = ?`
		args = append(args, repo)
	}
	var count int
	if err := db.QueryRow(query, args...).Scan(&count); err != nil {
		return 0, err
	}
	return count, nil
}

func PRMemoryLastSync(globalDBPath, repo string) (string, error) {
	if repo == "" {
		return "", nil
	}
	db, err := openGlobal(globalDBPath)
	if err != nil {
		if os.IsNotExist(err) {
			return "", nil
		}
		return "", err
	}
	defer db.Close()
	if err := ensureGlobal(db); err != nil {
		return "", err
	}
	var value string
	err = db.QueryRow(`SELECT value FROM global_metadata WHERE key = ?`, "pr_memory.last_sync."+repo).Scan(&value)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	return value, err
}

func UpsertGitHubContext(globalDBPath string, record GitHubContextRecord) error {
	if record.Repo == "" || record.Kind == "" || record.Number <= 0 {
		return errors.New("repo, kind, and positive GitHub number are required")
	}
	db, err := openGlobal(globalDBPath)
	if err != nil {
		return err
	}
	defer db.Close()
	if err := ensureGlobal(db); err != nil {
		return err
	}
	return upsertGitHubContext(db, record)
}

func ReplaceGitHubContext(globalDBPath, repo string, kinds []string, records []GitHubContextRecord, syncedAt, metadataKey string) error {
	if repo == "" {
		return errors.New("repo is required")
	}
	if syncedAt == "" {
		syncedAt = now()
	}
	db, err := openGlobal(globalDBPath)
	if err != nil {
		return err
	}
	defer db.Close()
	if err := ensureGlobal(db); err != nil {
		return err
	}
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, kind := range normalizeContextKinds(kinds) {
		if _, err := tx.Exec(`DELETE FROM github_records WHERE repo = ? AND kind = ?`, repo, kind); err != nil {
			return err
		}
		if _, err := tx.Exec(`DELETE FROM github_records_fts WHERE repo = ? AND kind = ?`, repo, kind); err != nil {
			return err
		}
	}
	for _, record := range records {
		record.Repo = repo
		if err := upsertGitHubContextTx(tx, record); err != nil {
			return err
		}
	}
	if metadataKey != "" {
		if err := setGlobalMetadataTx(tx, metadataKey, syncedAt); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func ApplyGitHubContextSync(globalDBPath, repo string, records []GitHubContextRecord, syncedAt, metadataKey string) (int, error) {
	if repo == "" {
		return 0, errors.New("repo is required")
	}
	if syncedAt == "" {
		syncedAt = now()
	}
	db, err := openGlobal(globalDBPath)
	if err != nil {
		return 0, err
	}
	defer db.Close()
	if err := ensureGlobal(db); err != nil {
		return 0, err
	}
	tx, err := db.Begin()
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	upserted := 0
	for _, record := range records {
		record.Repo = repo
		if err := upsertGitHubContextTx(tx, record); err != nil {
			return 0, err
		}
		upserted++
	}
	if metadataKey != "" {
		if err := setGlobalMetadataTx(tx, metadataKey, syncedAt); err != nil {
			return 0, err
		}
	}
	return upserted, tx.Commit()
}

func RecentGitHubContext(globalDBPath, repo string, limit int) ([]GitHubContextRecord, error) {
	db, err := openGlobal(globalDBPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	defer db.Close()
	if err := ensureGlobal(db); err != nil {
		return nil, err
	}
	query := `SELECT repo, kind, number, comment_id, url, state, title, body, author, labels, updated_at, closed_at, fetched_at FROM github_records WHERE kind <> ?`
	args := []any{"pr"}
	if repo != "" {
		query += ` AND repo = ?`
		args = append(args, repo)
	}
	query += ` ORDER BY updated_at DESC, number DESC, kind, comment_id`
	if limit > 0 {
		query += ` LIMIT ?`
		args = append(args, limit)
	}
	rows, err := db.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanGitHubContextRows(rows)
}

func SearchGitHubContext(globalDBPath string, opts GitHubContextSearchOptions) ([]GitHubContextSearchHit, error) {
	query := ftsQuery(opts.Query)
	if query == "" {
		return nil, nil
	}
	opts.Kind = normalizeGitHubContextKind(opts.Kind)
	db, err := openGlobal(globalDBPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	defer db.Close()
	if err := ensureGlobal(db); err != nil {
		return nil, err
	}
	sqlText := `SELECT repo, kind, number, comment_id, url, state, title, body, author, labels, updated_at, closed_at, fetched_at, bm25(github_records_fts) AS rank
FROM github_records_fts WHERE github_records_fts MATCH ? AND kind <> ?`
	args := []any{query, "pr"}
	if opts.Repo != "" {
		sqlText += ` AND repo = ?`
		args = append(args, opts.Repo)
	}
	if opts.Kind != "" {
		sqlText += ` AND kind = ?`
		args = append(args, opts.Kind)
	}
	sqlText += ` ORDER BY rank, updated_at DESC, number DESC`
	if opts.Limit > 0 {
		sqlText += ` LIMIT ?`
		args = append(args, opts.Limit)
	}
	rows, err := db.Query(sqlText, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []GitHubContextSearchHit
	for rows.Next() {
		var hit GitHubContextSearchHit
		if err := scanGitHubContextRow(rows, &hit.Record, &hit.Rank); err != nil {
			return nil, err
		}
		out = append(out, hit)
	}
	return out, rows.Err()
}

func CountGitHubContext(globalDBPath, repo, kind string) (int, error) {
	kind = normalizeGitHubContextKind(kind)
	db, err := openGlobal(globalDBPath)
	if err != nil {
		if os.IsNotExist(err) {
			return 0, nil
		}
		return 0, err
	}
	defer db.Close()
	if err := ensureGlobal(db); err != nil {
		return 0, err
	}
	query := `SELECT COUNT(*) FROM github_records`
	args := []any{}
	var clauses []string
	if kind == "" {
		clauses = append(clauses, "kind <> ?")
		args = append(args, "pr")
	}
	if repo != "" {
		clauses = append(clauses, "repo = ?")
		args = append(args, repo)
	}
	if kind != "" {
		clauses = append(clauses, "kind = ?")
		args = append(args, kind)
	}
	if len(clauses) > 0 {
		query += ` WHERE ` + strings.Join(clauses, " AND ")
	}
	var count int
	if err := db.QueryRow(query, args...).Scan(&count); err != nil {
		return 0, err
	}
	return count, nil
}

func GitHubContextLastSync(globalDBPath, metadataKey string) (string, error) {
	if metadataKey == "" {
		return "", nil
	}
	db, err := openGlobal(globalDBPath)
	if err != nil {
		if os.IsNotExist(err) {
			return "", nil
		}
		return "", err
	}
	defer db.Close()
	if err := ensureGlobal(db); err != nil {
		return "", err
	}
	var value string
	err = db.QueryRow(`SELECT value FROM global_metadata WHERE key = ?`, metadataKey).Scan(&value)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	return value, err
}

func OpenBlockingGitHubIssues(globalDBPath, repo string, numbers []int) ([]GitHubContextRecord, error) {
	if repo == "" || len(numbers) == 0 {
		return nil, nil
	}
	db, err := openGlobal(globalDBPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	defer db.Close()
	if err := ensureGlobal(db); err != nil {
		return nil, err
	}
	placeholders := make([]string, 0, len(numbers))
	args := []any{repo, "issue", "open"}
	for _, number := range numbers {
		if number <= 0 {
			continue
		}
		placeholders = append(placeholders, "?")
		args = append(args, number)
	}
	if len(placeholders) == 0 {
		return nil, nil
	}
	query := `SELECT repo, kind, number, comment_id, url, state, title, body, author, labels, updated_at, closed_at, fetched_at
FROM github_records WHERE repo = ? AND kind = ? AND state = ? AND number IN (` + strings.Join(placeholders, ",") + `)`
	rows, err := db.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	records, err := scanGitHubContextRows(rows)
	if err != nil {
		return nil, err
	}
	out := records[:0]
	for _, record := range records {
		if contextLabelsContain(record.Labels, "loop:blocking") {
			out = append(out, record)
		}
	}
	return out, nil
}

func upsertPRMemory(db *sql.DB, record PRMemoryRecord) error {
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := upsertPRMemoryTx(tx, record); err != nil {
		return err
	}
	return tx.Commit()
}

func upsertPRMemoryTx(tx *sql.Tx, record PRMemoryRecord) error {
	record.State = normalizePRMemoryState(record.State)
	if record.FetchedAt == "" {
		record.FetchedAt = now()
	}
	if _, err := tx.Exec(`INSERT INTO github_records(repo, kind, number, comment_id, url, state, title, body, author, labels, updated_at, closed_at, merged_at, fetched_at)
VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(repo, kind, number, comment_id) DO UPDATE SET
	url = excluded.url,
	state = excluded.state,
	title = excluded.title,
	body = excluded.body,
	author = excluded.author,
	labels = excluded.labels,
	updated_at = excluded.updated_at,
	closed_at = excluded.closed_at,
	merged_at = excluded.merged_at,
	fetched_at = excluded.fetched_at`,
		record.Repo, "pr", record.Number, "", record.URL, record.State, record.Title, record.Body, "", "", record.UpdatedAt, "", record.MergedAt, record.FetchedAt); err != nil {
		return err
	}
	if _, err := tx.Exec(`DELETE FROM github_records_fts WHERE repo = ? AND kind = ? AND number = ? AND comment_id = ?`, record.Repo, "pr", record.Number, ""); err != nil {
		return err
	}
	if strings.TrimSpace(record.Title+"\n"+record.Body) != "" {
		if _, err := tx.Exec(`INSERT INTO github_records_fts(repo, kind, number, comment_id, url, state, title, body, author, labels, updated_at, closed_at, merged_at, fetched_at)
VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			record.Repo, "pr", record.Number, "", record.URL, record.State, record.Title, record.Body, "", "", record.UpdatedAt, "", record.MergedAt, record.FetchedAt); err != nil {
			return err
		}
	}
	return nil
}

func deletePRMemory(db *sql.DB, repo string, number int) error {
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := deletePRMemoryTx(tx, repo, number); err != nil {
		return err
	}
	return tx.Commit()
}

func deletePRMemoryTx(tx *sql.Tx, repo string, number int) error {
	if _, err := tx.Exec(`DELETE FROM github_records WHERE repo = ? AND kind = ? AND number = ? AND comment_id = ?`, repo, "pr", number, ""); err != nil {
		return err
	}
	_, err := tx.Exec(`DELETE FROM github_records_fts WHERE repo = ? AND kind = ? AND number = ? AND comment_id = ?`, repo, "pr", number, "")
	return err
}

func upsertGitHubContext(db *sql.DB, record GitHubContextRecord) error {
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := upsertGitHubContextTx(tx, record); err != nil {
		return err
	}
	return tx.Commit()
}

func upsertGitHubContextTx(tx *sql.Tx, record GitHubContextRecord) error {
	record.Kind = normalizeGitHubContextKind(record.Kind)
	record.State = strings.ToLower(strings.TrimSpace(record.State))
	if record.FetchedAt == "" {
		record.FetchedAt = now()
	}
	if record.CommentID == "" {
		record.CommentID = ""
	}
	if _, err := tx.Exec(`INSERT INTO github_records(repo, kind, number, comment_id, url, state, title, body, author, labels, updated_at, closed_at, merged_at, fetched_at)
VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(repo, kind, number, comment_id) DO UPDATE SET
	url = excluded.url,
	state = excluded.state,
	title = excluded.title,
	body = excluded.body,
	author = excluded.author,
	labels = excluded.labels,
	updated_at = excluded.updated_at,
	closed_at = excluded.closed_at,
	merged_at = excluded.merged_at,
	fetched_at = excluded.fetched_at`,
		record.Repo, record.Kind, record.Number, record.CommentID, record.URL, record.State, record.Title, record.Body, record.Author, record.Labels, record.UpdatedAt, record.ClosedAt, "", record.FetchedAt); err != nil {
		return err
	}
	if _, err := tx.Exec(`DELETE FROM github_records_fts WHERE repo = ? AND kind = ? AND number = ? AND comment_id = ?`,
		record.Repo, record.Kind, record.Number, record.CommentID); err != nil {
		return err
	}
	if strings.TrimSpace(record.Title+"\n"+record.Body) != "" {
		if _, err := tx.Exec(`INSERT INTO github_records_fts(repo, kind, number, comment_id, url, state, title, body, author, labels, updated_at, closed_at, merged_at, fetched_at)
VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			record.Repo, record.Kind, record.Number, record.CommentID, record.URL, record.State, record.Title, record.Body, record.Author, record.Labels, record.UpdatedAt, record.ClosedAt, "", record.FetchedAt); err != nil {
			return err
		}
	}
	return nil
}

func scanGitHubContextRows(rows *sql.Rows) ([]GitHubContextRecord, error) {
	var out []GitHubContextRecord
	for rows.Next() {
		var record GitHubContextRecord
		if err := rows.Scan(&record.Repo, &record.Kind, &record.Number, &record.CommentID, &record.URL, &record.State, &record.Title, &record.Body, &record.Author, &record.Labels, &record.UpdatedAt, &record.ClosedAt, &record.FetchedAt); err != nil {
			return nil, err
		}
		out = append(out, record)
	}
	return out, rows.Err()
}

func scanGitHubContextRow(rows *sql.Rows, record *GitHubContextRecord, rank *float64) error {
	return rows.Scan(&record.Repo, &record.Kind, &record.Number, &record.CommentID, &record.URL, &record.State, &record.Title, &record.Body, &record.Author, &record.Labels, &record.UpdatedAt, &record.ClosedAt, &record.FetchedAt, rank)
}

func normalizeContextKinds(kinds []string) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(kinds))
	for _, kind := range kinds {
		kind = normalizeGitHubContextKind(kind)
		if kind == "" || seen[kind] {
			continue
		}
		seen[kind] = true
		out = append(out, kind)
	}
	return out
}

func normalizeGitHubContextKind(kind string) string {
	return strings.ToLower(strings.TrimSpace(kind))
}

func contextLabelsContain(labels, want string) bool {
	want = strings.ToLower(strings.TrimSpace(want))
	for _, label := range strings.Split(labels, ",") {
		if strings.ToLower(strings.TrimSpace(label)) == want {
			return true
		}
	}
	return false
}

func setGlobalMetadataTx(tx *sql.Tx, key, value string) error {
	_, err := tx.Exec(`INSERT INTO global_metadata(key, value) VALUES(?, ?)
ON CONFLICT(key) DO UPDATE SET value = excluded.value`, key, value)
	return err
}

func includePRMemory(record PRMemoryRecord) bool {
	state := normalizePRMemoryState(record.State)
	return state == "open" || state == "merged"
}

func normalizePRMemoryState(state string) string {
	return strings.ToLower(strings.TrimSpace(state))
}

func openGlobal(path string) (*sql.DB, error) {
	if path == "" {
		return nil, os.ErrNotExist
	}
	if _, err := os.Stat(path); err != nil {
		if os.IsNotExist(err) {
			if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
				return nil, err
			}
		} else {
			return nil, err
		}
	}
	return sql.Open("sqlite", sqliteDSN(path))
}

func sqliteDSN(path string) string {
	return path + "?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)"
}

func ensureGlobal(db *sql.DB) error {
	stmts := []string{
		`PRAGMA journal_mode = WAL`,
		`CREATE TABLE IF NOT EXISTS runs(run_id TEXT PRIMARY KEY, updated_at TEXT NOT NULL)`,
		`CREATE TABLE IF NOT EXISTS iterations(run_id TEXT NOT NULL, iteration_id TEXT NOT NULL, updated_at TEXT NOT NULL, PRIMARY KEY(run_id, iteration_id))`,
		`CREATE TABLE IF NOT EXISTS artifact_index(run_id TEXT NOT NULL, iteration_id TEXT NOT NULL, artifact TEXT NOT NULL, content TEXT NOT NULL, updated_at TEXT NOT NULL, PRIMARY KEY(run_id, iteration_id, artifact))`,
		`CREATE VIRTUAL TABLE IF NOT EXISTS artifact_index_fts USING fts5(run_id UNINDEXED, iteration_id UNINDEXED, artifact UNINDEXED, content, tokenize = 'unicode61')`,
		`CREATE TABLE IF NOT EXISTS iteration_results(run_id TEXT NOT NULL, iteration_id TEXT NOT NULL, result_json TEXT NOT NULL, updated_at TEXT NOT NULL, PRIMARY KEY(run_id, iteration_id))`,
		`CREATE TABLE IF NOT EXISTS global_metadata(key TEXT PRIMARY KEY, value TEXT NOT NULL)`,
		`CREATE TABLE IF NOT EXISTS github_records(repo TEXT NOT NULL, kind TEXT NOT NULL, number INTEGER NOT NULL, comment_id TEXT NOT NULL DEFAULT '', url TEXT NOT NULL, state TEXT NOT NULL, title TEXT NOT NULL, body TEXT NOT NULL, author TEXT NOT NULL, labels TEXT NOT NULL, updated_at TEXT NOT NULL, closed_at TEXT NOT NULL, merged_at TEXT NOT NULL, fetched_at TEXT NOT NULL, PRIMARY KEY(repo, kind, number, comment_id))`,
		`CREATE VIRTUAL TABLE IF NOT EXISTS github_records_fts USING fts5(repo UNINDEXED, kind UNINDEXED, number UNINDEXED, comment_id UNINDEXED, url UNINDEXED, state UNINDEXED, title, body, author UNINDEXED, labels UNINDEXED, updated_at UNINDEXED, closed_at UNINDEXED, merged_at UNINDEXED, fetched_at UNINDEXED, tokenize = 'unicode61')`,
	}
	for _, stmt := range stmts {
		if _, err := db.Exec(stmt); err != nil {
			return err
		}
	}
	return nil
}

func ftsQuery(query string) string {
	var terms []string
	for _, field := range strings.FieldsFunc(strings.TrimSpace(query), func(r rune) bool {
		return unicode.IsSpace(r)
	}) {
		field = strings.Trim(field, `"`)
		if field == "" {
			continue
		}
		field = strings.ReplaceAll(field, `"`, `""`)
		terms = append(terms, `"`+field+`"`)
	}
	return strings.Join(terms, " ")
}

func now() string {
	return time.Now().UTC().Format(time.RFC3339)
}

func splitPath(path string) []string {
	volume := filepath.VolumeName(path)
	trimmed := strings.TrimPrefix(path, volume)
	trimmed = strings.Trim(trimmed, string(filepath.Separator))
	if trimmed == "" {
		if volume != "" {
			return []string{volume + string(filepath.Separator)}
		}
		return nil
	}
	parts := strings.Split(trimmed, string(filepath.Separator))
	if filepath.IsAbs(path) {
		if volume != "" {
			return append([]string{volume + string(filepath.Separator)}, parts...)
		}
		return append([]string{string(filepath.Separator)}, parts...)
	}
	return parts
}

func joinPath(parts []string) string {
	if len(parts) == 0 {
		return ""
	}
	if parts[0] == string(filepath.Separator) || strings.HasSuffix(parts[0], string(filepath.Separator)) {
		return filepath.Join(parts...)
	}
	return filepath.Join(parts...)
}
