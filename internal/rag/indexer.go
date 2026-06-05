package rag

import (
	"context"
	"strings"

	"kubesage/internal/config"
)

type IndexChunk struct {
	ID        string `json:"id"`
	RunbookID uint   `json:"runbook_id"`
	FaultType string `json:"fault_type"`
	Title     string `json:"title"`
	Content   string `json:"content"`
}

type Indexer interface {
	Upsert(ctx context.Context, chunks []IndexChunk) error
}

type VectorStore interface {
	Indexer
	Retriever
	EnsureCollection(ctx context.Context, dimension int) error
}

func NewIndexer(cfg config.RAGConfig) VectorStore {
	switch strings.ToLower(strings.TrimSpace(cfg.VectorStore)) {
	case "qdrant":
		return newQdrantIndexer(cfg)
	case "pgvector":
		return newPGVectorIndexer(cfg)
	default:
		return nil
	}
}
