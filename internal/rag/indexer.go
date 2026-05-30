package rag

import "context"

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
