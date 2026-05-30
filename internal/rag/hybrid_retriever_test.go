package rag

import (
	"context"
	"fmt"
	"testing"
)

type mockRetriever struct {
	hits []Hit
	err  error
}

func (m *mockRetriever) Retrieve(ctx context.Context, faultType, query string, limit int) ([]Hit, error) {
	return m.hits, m.err
}

func TestHybridRetriever_KeywordFirst(t *testing.T) {
	keywordHits := []Hit{{Title: "kw-hit", Score: 50}}
	vectorHits := []Hit{{Title: "vec-hit", Score: 80}}

	r := NewHybridRetriever(
		&mockRetriever{hits: vectorHits},
		&mockRetriever{hits: keywordHits},
		false,
	)

	hits, err := r.Retrieve(context.Background(), "OOMKilled", "memory", 5)
	if err != nil {
		t.Fatalf("Retrieve() error = %v", err)
	}
	if len(hits) != 1 || hits[0].Title != "kw-hit" {
		t.Errorf("expected keyword hit, got %v", hits)
	}
}

func TestHybridRetriever_VectorFirst(t *testing.T) {
	keywordHits := []Hit{{Title: "kw-hit", Score: 50}}
	vectorHits := []Hit{{Title: "vec-hit", Score: 80}}

	r := NewHybridRetriever(
		&mockRetriever{hits: vectorHits},
		&mockRetriever{hits: keywordHits},
		true,
	)

	hits, err := r.Retrieve(context.Background(), "OOMKilled", "memory", 5)
	if err != nil {
		t.Fatalf("Retrieve() error = %v", err)
	}
	if len(hits) != 1 || hits[0].Title != "vec-hit" {
		t.Errorf("expected vector hit, got %v", hits)
	}
}

func TestHybridRetriever_VectorFallbackToKeyword(t *testing.T) {
	keywordHits := []Hit{{Title: "kw-hit", Score: 50}}

	r := NewHybridRetriever(
		&mockRetriever{hits: nil, err: fmt.Errorf("vector unavailable")},
		&mockRetriever{hits: keywordHits},
		true,
	)

	hits, err := r.Retrieve(context.Background(), "OOMKilled", "memory", 5)
	if err != nil {
		t.Fatalf("Retrieve() error = %v", err)
	}
	if len(hits) != 1 || hits[0].Title != "kw-hit" {
		t.Errorf("expected fallback to keyword hit, got %v", hits)
	}
}

func TestHybridRetriever_NilRetrievers(t *testing.T) {
	r := NewHybridRetriever(nil, nil, false)
	if r != nil {
		t.Errorf("expected nil retriever when both inputs are nil, got %v", r)
	}
}
