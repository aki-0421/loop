package artifactdb

import (
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode"

	_ "github.com/mattn/go-sqlite3"
)

const (
	LocalDBName  = "iteration.db"
	GlobalDBName = "loop.db"
)

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

func LocalDBPath(iterationDir string) string {
	return filepath.Join(iterationDir, LocalDBName)
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

func Read(iterationDir, name string) (string, error) {
	dbPath := LocalDBPath(iterationDir)
	if _, err := os.Stat(dbPath); err == nil {
		db, err := openLocal(dbPath)
		if err != nil {
			return "", err
		}
		defer db.Close()
		var content string
		err = db.QueryRow(`SELECT content FROM artifacts WHERE name = ?`, name).Scan(&content)
		if err == nil {
			return content, nil
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return "", err
		}
	} else if err != nil && !os.IsNotExist(err) {
		return "", err
	}
	return "", fmt.Errorf("%w: %s", ErrNotFound, name)
}

func Write(iterationDir, name, content string) error {
	return write(iterationDir, name, content, false)
}

func Append(iterationDir, name, content string) error {
	return write(iterationDir, name, content, true)
}

func WriteValidationOutput(iterationDir, name, output string) error {
	db, err := openLocal(LocalDBPath(iterationDir))
	if err != nil {
		return err
	}
	defer db.Close()
	if err := ensureLocal(db); err != nil {
		return err
	}
	_, err = db.Exec(`INSERT INTO validation_outputs(name, output, updated_at) VALUES(?, ?, ?)
ON CONFLICT(name) DO UPDATE SET output = excluded.output, updated_at = excluded.updated_at`, name, output, now())
	return err
}

func List(iterationDir string) ([]Artifact, error) {
	dbPath := LocalDBPath(iterationDir)
	if _, err := os.Stat(dbPath); err == nil {
		db, err := openLocal(dbPath)
		if err != nil {
			return nil, err
		}
		defer db.Close()
		rows, err := db.Query(`SELECT name, content, updated_at FROM artifacts ORDER BY name`)
		if err != nil {
			return nil, err
		}
		defer rows.Close()
		var out []Artifact
		for rows.Next() {
			var item Artifact
			if err := rows.Scan(&item.Name, &item.Content, &item.UpdatedAt); err != nil {
				return nil, err
			}
			out = append(out, item)
		}
		return out, rows.Err()
	}
	return nil, nil
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
	if _, err := os.Stat(runsDir); err != nil {
		if os.IsNotExist(err) {
			return 0, nil
		}
		return 0, err
	}
	runFilter := ""
	if filepath.Base(filepath.Dir(filepath.Clean(runsDir))) == "runs" {
		runFilter = filepath.Base(filepath.Clean(runsDir))
	}
	if runFilter != "" {
		for _, stmt := range []string{
			`DELETE FROM artifact_index WHERE run_id = ?`,
			`DELETE FROM artifact_index_fts WHERE run_id = ?`,
			`DELETE FROM iterations WHERE run_id = ?`,
			`DELETE FROM runs WHERE run_id = ?`,
		} {
			if _, err := db.Exec(stmt, runFilter); err != nil {
				return 0, err
			}
		}
	} else {
		for _, stmt := range []string{
			`DELETE FROM artifact_index`,
			`DELETE FROM artifact_index_fts`,
			`DELETE FROM iterations`,
			`DELETE FROM runs`,
		} {
			if _, err := db.Exec(stmt); err != nil {
				return 0, err
			}
		}
	}
	count := 0
	err = filepath.WalkDir(runsDir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() || filepath.Base(filepath.Dir(path)) != "iterations" {
			return nil
		}
		runID, iterationID := ParseIterationDir(path)
		if _, err := os.Stat(LocalDBPath(path)); err != nil {
			if os.IsNotExist(err) {
				return nil
			}
			return err
		}
		items, err := List(path)
		if err != nil {
			return err
		}
		for _, item := range items {
			if err := MirrorGlobal(globalPath, runID, iterationID, item.Name, item.Content); err != nil {
				return err
			}
			count++
		}
		return nil
	})
	return count, err
}

func write(iterationDir, name, content string, appendMode bool) error {
	db, err := openLocal(LocalDBPath(iterationDir))
	if err != nil {
		return err
	}
	defer db.Close()
	if err := ensureLocal(db); err != nil {
		return err
	}
	updated := now()
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if appendMode {
		if _, err := tx.Exec(`INSERT INTO artifacts(name, content, updated_at) VALUES(?, ?, ?)
ON CONFLICT(name) DO UPDATE SET content = artifacts.content || excluded.content, updated_at = excluded.updated_at`, name, content, updated); err != nil {
			return err
		}
	} else {
		if _, err := tx.Exec(`INSERT INTO artifacts(name, content, updated_at) VALUES(?, ?, ?)
ON CONFLICT(name) DO UPDATE SET content = excluded.content, updated_at = excluded.updated_at`, name, content, updated); err != nil {
			return err
		}
	}
	var stored string
	if err := tx.QueryRow(`SELECT content FROM artifacts WHERE name = ?`, name).Scan(&stored); err != nil {
		return err
	}
	if _, err := tx.Exec(`DELETE FROM artifact_fts WHERE name = ?`, name); err != nil {
		return err
	}
	if strings.TrimSpace(stored) != "" {
		if _, err := tx.Exec(`INSERT INTO artifact_fts(name, content) VALUES(?, ?)`, name, stored); err != nil {
			return err
		}
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	runID, iterationID := ParseIterationDir(iterationDir)
	return MirrorGlobal(GlobalDBPathForIteration(iterationDir), runID, iterationID, name, stored)
}

func openLocal(path string) (*sql.DB, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, err
	}
	return sql.Open("sqlite3", path+"?_busy_timeout=5000")
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
	return sql.Open("sqlite3", path+"?_busy_timeout=5000")
}

func ensureLocal(db *sql.DB) error {
	stmts := []string{
		`PRAGMA journal_mode = WAL`,
		`CREATE TABLE IF NOT EXISTS metadata(key TEXT PRIMARY KEY, value TEXT NOT NULL)`,
		`CREATE TABLE IF NOT EXISTS artifacts(name TEXT PRIMARY KEY, content TEXT NOT NULL, updated_at TEXT NOT NULL)`,
		`CREATE TABLE IF NOT EXISTS validation_outputs(name TEXT PRIMARY KEY, output TEXT NOT NULL, updated_at TEXT NOT NULL)`,
		`CREATE VIRTUAL TABLE IF NOT EXISTS artifact_fts USING fts5(name UNINDEXED, content, tokenize = 'unicode61')`,
	}
	for _, stmt := range stmts {
		if _, err := db.Exec(stmt); err != nil {
			return err
		}
	}
	return nil
}

func ensureGlobal(db *sql.DB) error {
	stmts := []string{
		`PRAGMA journal_mode = WAL`,
		`CREATE TABLE IF NOT EXISTS runs(run_id TEXT PRIMARY KEY, updated_at TEXT NOT NULL)`,
		`CREATE TABLE IF NOT EXISTS iterations(run_id TEXT NOT NULL, iteration_id TEXT NOT NULL, updated_at TEXT NOT NULL, PRIMARY KEY(run_id, iteration_id))`,
		`CREATE TABLE IF NOT EXISTS artifact_index(run_id TEXT NOT NULL, iteration_id TEXT NOT NULL, artifact TEXT NOT NULL, content TEXT NOT NULL, updated_at TEXT NOT NULL, PRIMARY KEY(run_id, iteration_id, artifact))`,
		`CREATE VIRTUAL TABLE IF NOT EXISTS artifact_index_fts USING fts5(run_id UNINDEXED, iteration_id UNINDEXED, artifact UNINDEXED, content, tokenize = 'unicode61')`,
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
