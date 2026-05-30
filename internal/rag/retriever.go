package rag

import "context"

type Hit struct {
	Title   string       `json:"title"`
	Content string       `json:"content"`
	Score   int          `json:"score"`
	Hints   RunbookHints `json:"hints,omitempty"`
}

type Retriever interface {
	Retrieve(ctx context.Context, faultType, query string, limit int) ([]Hit, error)
}
