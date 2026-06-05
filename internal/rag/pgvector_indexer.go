package rag

import (
	"context"
	"database/sql"
	"fmt"
	"math"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"kubesage/internal/config"

	_ "github.com/lib/pq"
)

const defaultPGVectorTable = "runbook_vectors"

type pgVectorIndexer struct {
	dsn       string
	table     string
	embedding *embeddingClient
	db        *sql.DB
}

func newPGVectorIndexer(cfg config.RAGConfig) *pgVectorIndexer {
	dsn := strings.TrimSpace(cfg.PGVector.DSN)
	if dsn == "" {
		return nil
	}
	table := strings.TrimSpace(cfg.PGVector.Table)
	if table == "" {
		table = defaultPGVectorTable
	}
	db, err := sql.Open("postgres", dsn)
	if err != nil {
		return nil
	}
	return &pgVectorIndexer{
		dsn:   dsn,
		table: table,
		embedding: &embeddingClient{
			baseURL: strings.TrimRight(normalizeURL(cfg.Embedding.BaseURL, "https"), "/"),
			apiKey:  strings.TrimSpace(cfg.Embedding.APIKey),
			model:   strings.TrimSpace(cfg.Embedding.Model),
			http:    &http.Client{Timeout: 30 * time.Second},
		},
		db: db,
	}
}

// EnsureCollection creates the pgvector extension and the runbook vector table.
func (i *pgVectorIndexer) EnsureCollection(ctx context.Context, dimension int) error {
	if i == nil || i.db == nil {
		return nil
	}
	if dimension <= 0 {
		dimension = 1536
	}
	table, err := quoteIdentifierPath(i.table)
	if err != nil {
		return err
	}
	if err := i.db.PingContext(ctx); err != nil {
		return err
	}
	if _, err := i.db.ExecContext(ctx, `CREATE EXTENSION IF NOT EXISTS vector`); err != nil {
		return err
	}
	createTable := fmt.Sprintf(`
CREATE TABLE IF NOT EXISTS %s (
	id TEXT PRIMARY KEY,
	runbook_id BIGINT NOT NULL DEFAULT 0,
	fault_type TEXT NOT NULL DEFAULT '',
	title TEXT NOT NULL DEFAULT '',
	content TEXT NOT NULL DEFAULT '',
	embedding vector(%d) NOT NULL,
	updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
)`, table, dimension)
	if _, err := i.db.ExecContext(ctx, createTable); err != nil {
		return err
	}
	_, _ = i.db.ExecContext(ctx, fmt.Sprintf(
		`CREATE INDEX IF NOT EXISTS %s ON %s (fault_type)`,
		quoteIdentifier(indexName(i.table, "fault_type_idx")),
		table,
	))
	_, _ = i.db.ExecContext(ctx, fmt.Sprintf(
		`CREATE INDEX IF NOT EXISTS %s ON %s USING hnsw (embedding vector_cosine_ops)`,
		quoteIdentifier(indexName(i.table, "embedding_hnsw_idx")),
		table,
	))
	return nil
}

func (i *pgVectorIndexer) Upsert(ctx context.Context, chunks []IndexChunk) error {
	if i == nil || i.db == nil || len(chunks) == 0 {
		return nil
	}
	table, err := quoteIdentifierPath(i.table)
	if err != nil {
		return err
	}
	stmt := fmt.Sprintf(`
INSERT INTO %s (id, runbook_id, fault_type, title, content, embedding, updated_at)
VALUES ($1, $2, $3, $4, $5, $6::vector, now())
ON CONFLICT (id) DO UPDATE SET
	runbook_id = EXCLUDED.runbook_id,
	fault_type = EXCLUDED.fault_type,
	title = EXCLUDED.title,
	content = EXCLUDED.content,
	embedding = EXCLUDED.embedding,
	updated_at = now()`, table)
	for _, chunk := range chunks {
		vector, err := i.embedding.Embed(ctx, chunk.Content)
		if err != nil {
			return err
		}
		literal, err := pgVectorLiteral(vector)
		if err != nil {
			return err
		}
		if _, err := i.db.ExecContext(ctx, stmt, chunk.ID, chunk.RunbookID, chunk.FaultType, chunk.Title, chunk.Content, literal); err != nil {
			return err
		}
	}
	return nil
}

func (i *pgVectorIndexer) Retrieve(ctx context.Context, faultType, query string, limit int) ([]Hit, error) {
	if i == nil || i.db == nil {
		return nil, fmt.Errorf("pgvector retriever is not configured")
	}
	if limit <= 0 {
		limit = 3
	}
	vector, err := i.embedding.Embed(ctx, strings.TrimSpace(faultType+" "+query))
	if err != nil {
		return nil, err
	}
	literal, err := pgVectorLiteral(vector)
	if err != nil {
		return nil, err
	}
	hits, err := i.retrieveWithFilter(ctx, literal, faultType, limit)
	if err != nil {
		return nil, err
	}
	if len(hits) > 0 || strings.TrimSpace(faultType) == "" {
		return hits, nil
	}
	return i.retrieveWithFilter(ctx, literal, "", limit)
}

func (i *pgVectorIndexer) retrieveWithFilter(ctx context.Context, vectorLiteral, faultType string, limit int) ([]Hit, error) {
	table, err := quoteIdentifierPath(i.table)
	if err != nil {
		return nil, err
	}
	query := fmt.Sprintf(`
SELECT title, content, fault_type, 1 - (embedding <=> $1::vector) AS score
FROM %s
WHERE ($2 = '' OR lower(fault_type) = lower($2))
ORDER BY embedding <=> $1::vector
LIMIT $3`, table)
	rows, err := i.db.QueryContext(ctx, query, vectorLiteral, strings.TrimSpace(faultType), limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	hits := []Hit{}
	for rows.Next() {
		var title, content, itemFaultType string
		var score float64
		if err := rows.Scan(&title, &content, &itemFaultType, &score); err != nil {
			return nil, err
		}
		if itemFaultType != "" && title != "" {
			title = itemFaultType + " / " + title
		}
		hits = append(hits, Hit{
			Title:   title,
			Content: content,
			Score:   int(clampScore(score) * 100),
		})
	}
	return hits, rows.Err()
}

func pgVectorLiteral(values []float64) (string, error) {
	if len(values) == 0 {
		return "", fmt.Errorf("embedding vector is empty")
	}
	parts := make([]string, 0, len(values))
	for _, value := range values {
		if math.IsNaN(value) || math.IsInf(value, 0) {
			return "", fmt.Errorf("embedding vector contains invalid value")
		}
		parts = append(parts, strconv.FormatFloat(value, 'g', -1, 64))
	}
	return "[" + strings.Join(parts, ",") + "]", nil
}

var identifierPattern = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

func quoteIdentifierPath(name string) (string, error) {
	parts := strings.Split(strings.TrimSpace(name), ".")
	if len(parts) == 0 || len(parts) > 2 {
		return "", fmt.Errorf("invalid pgvector table name")
	}
	quoted := make([]string, 0, len(parts))
	for _, part := range parts {
		if !identifierPattern.MatchString(part) {
			return "", fmt.Errorf("invalid pgvector identifier: %s", part)
		}
		quoted = append(quoted, quoteIdentifier(part))
	}
	return strings.Join(quoted, "."), nil
}

func quoteIdentifier(name string) string {
	return `"` + strings.ReplaceAll(name, `"`, `""`) + `"`
}

func indexName(table, suffix string) string {
	table = strings.ReplaceAll(table, ".", "_")
	table = regexp.MustCompile(`[^A-Za-z0-9_]`).ReplaceAllString(table, "_")
	if table == "" {
		table = defaultPGVectorTable
	}
	return table + "_" + suffix
}

func clampScore(score float64) float64 {
	if score < 0 {
		return 0
	}
	if score > 1 {
		return 1
	}
	return score
}
