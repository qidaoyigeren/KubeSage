package rag

import "context"

type HybridRetriever struct {
	Vector       Retriever
	Fallback     Retriever
	PreferVector bool
}

func NewHybridRetriever(vector, fallback Retriever, preferVector bool) Retriever {
	if vector == nil {
		return fallback
	}
	if fallback == nil {
		return vector
	}
	return &HybridRetriever{Vector: vector, Fallback: fallback, PreferVector: preferVector}
}

func (r *HybridRetriever) Retrieve(ctx context.Context, faultType, query string, limit int) ([]Hit, error) {
	if r == nil {
		return nil, nil
	}
	if r.PreferVector {
		hits, err := r.Vector.Retrieve(ctx, faultType, query, limit)
		if err == nil && len(hits) > 0 {
			return hits, nil
		}
		return r.Fallback.Retrieve(ctx, faultType, query, limit)
	}
	hits, err := r.Fallback.Retrieve(ctx, faultType, query, limit)
	if err == nil && len(hits) > 0 {
		return hits, nil
	}
	return r.Vector.Retrieve(ctx, faultType, query, limit)
}
