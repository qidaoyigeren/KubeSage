package rag

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"path"
	"strings"
	"time"

	"kubesage/internal/config"
)

type qdrantIndexer struct {
	baseURL    string
	apiKey     string
	collection string
	embedding  *embeddingClient
	http       *http.Client
}

type embeddingClient struct {
	baseURL string
	apiKey  string
	model   string
	http    *http.Client
}

func newQdrantIndexer(cfg config.RAGConfig) *qdrantIndexer {
	if !strings.EqualFold(cfg.VectorStore, "qdrant") || strings.TrimSpace(cfg.Qdrant.BaseURL) == "" {
		return nil
	}
	collection := strings.TrimSpace(cfg.Qdrant.Collection)
	if collection == "" {
		collection = "kubesage_runbooks"
	}
	return &qdrantIndexer{
		baseURL:    strings.TrimRight(normalizeURL(cfg.Qdrant.BaseURL, "http"), "/"),
		apiKey:     strings.TrimSpace(cfg.Qdrant.APIKey),
		collection: collection,
		embedding: &embeddingClient{
			baseURL: strings.TrimRight(normalizeURL(cfg.Embedding.BaseURL, "https"), "/"),
			apiKey:  strings.TrimSpace(cfg.Embedding.APIKey),
			model:   strings.TrimSpace(cfg.Embedding.Model),
			http:    &http.Client{Timeout: 30 * time.Second},
		},
		http: &http.Client{Timeout: 30 * time.Second},
	}
}

// EnsureCollection creates the Qdrant collection if it does not already exist.
// dimension is the embedding vector size (e.g. 1536 for text-embedding-ada-002).
func (i *qdrantIndexer) EnsureCollection(ctx context.Context, dimension int) error {
	if i == nil {
		return nil
	}
	// Check if collection already exists.
	endpoint, err := i.url("/collections/" + i.collection)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint.String(), nil)
	if err != nil {
		return err
	}
	if i.apiKey != "" {
		req.Header.Set("api-key", i.apiKey)
	}
	resp, err := i.http.Do(req)
	if err != nil {
		return err
	}
	resp.Body.Close()
	if resp.StatusCode == http.StatusOK {
		return nil // already exists
	}

	// Create collection with the given vector dimension.
	body, _ := json.Marshal(map[string]interface{}{
		"vectors": map[string]interface{}{
			"size":     dimension,
			"distance": "Cosine",
		},
	})
	createEndpoint, err := i.url("/collections/" + i.collection)
	if err != nil {
		return err
	}
	createReq, err := http.NewRequestWithContext(ctx, http.MethodPut, createEndpoint.String(), bytes.NewReader(body))
	if err != nil {
		return err
	}
	createReq.Header.Set("Content-Type", "application/json")
	if i.apiKey != "" {
		createReq.Header.Set("api-key", i.apiKey)
	}
	createResp, err := i.http.Do(createReq)
	if err != nil {
		return err
	}
	defer createResp.Body.Close()
	if createResp.StatusCode >= 300 {
		return fmt.Errorf("qdrant create collection failed: %s", createResp.Status)
	}
	return nil
}

func (i *qdrantIndexer) Upsert(ctx context.Context, chunks []IndexChunk) error {
	if i == nil || len(chunks) == 0 {
		return nil
	}
	points := make([]map[string]interface{}, 0, len(chunks))
	for _, chunk := range chunks {
		vector, err := i.embedding.Embed(ctx, chunk.Content)
		if err != nil {
			return err
		}
		points = append(points, map[string]interface{}{
			"id":     chunk.ID,
			"vector": vector,
			"payload": map[string]interface{}{
				"runbook_id": chunk.RunbookID,
				"fault_type": chunk.FaultType,
				"title":      chunk.Title,
				"content":    chunk.Content,
			},
		})
	}
	body, _ := json.Marshal(map[string]interface{}{"points": points})
	endpoint, err := i.url("/collections/" + i.collection + "/points")
	if err != nil {
		return err
	}
	q := endpoint.Query()
	q.Set("wait", "true")
	endpoint.RawQuery = q.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodPut, endpoint.String(), bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	if i.apiKey != "" {
		req.Header.Set("api-key", i.apiKey)
	}
	resp, err := i.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return fmt.Errorf("qdrant upsert failed: %s", resp.Status)
	}
	return nil
}

func (i *qdrantIndexer) Retrieve(ctx context.Context, faultType, query string, limit int) ([]Hit, error) {
	if i == nil {
		return nil, fmt.Errorf("qdrant retriever is not configured")
	}
	if limit <= 0 {
		limit = 3
	}
	vector, err := i.embedding.Embed(ctx, strings.TrimSpace(faultType+" "+query))
	if err != nil {
		return nil, err
	}
	body := map[string]interface{}{
		"vector":       vector,
		"limit":        limit,
		"with_payload": true,
	}
	if strings.TrimSpace(faultType) != "" {
		body["filter"] = map[string]interface{}{
			"must": []map[string]interface{}{
				{
					"key":   "fault_type",
					"match": map[string]interface{}{"text": faultType},
				},
			},
		}
	}
	bodyBytes, _ := json.Marshal(body)
	endpoint, err := i.url("/collections/" + i.collection + "/points/search")
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint.String(), bytes.NewReader(bodyBytes))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	if i.apiKey != "" {
		req.Header.Set("api-key", i.apiKey)
	}
	resp, err := i.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return nil, fmt.Errorf("qdrant search failed: %s", resp.Status)
	}
	var parsed struct {
		Result []struct {
			Score   float64 `json:"score"`
			Payload struct {
				Title     string `json:"title"`
				Content   string `json:"content"`
				FaultType string `json:"fault_type"`
			} `json:"payload"`
		} `json:"result"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&parsed); err != nil {
		return nil, err
	}
	hits := make([]Hit, 0, len(parsed.Result))
	for _, item := range parsed.Result {
		title := item.Payload.Title
		if item.Payload.FaultType != "" && title != "" {
			title = item.Payload.FaultType + " / " + title
		}
		hits = append(hits, Hit{
			Title:   title,
			Content: item.Payload.Content,
			Score:   int(item.Score * 100),
		})
	}
	return hits, nil
}

func (i *qdrantIndexer) url(apiPath string) (*url.URL, error) {
	u, err := url.Parse(i.baseURL)
	if err != nil {
		return nil, err
	}
	u.Path = path.Join(u.Path, apiPath)
	return u, nil
}

func (c *embeddingClient) Embed(ctx context.Context, text string) ([]float64, error) {
	if c == nil || c.baseURL == "" || c.apiKey == "" || c.model == "" {
		return nil, fmt.Errorf("embedding provider is not configured")
	}
	u, err := url.Parse(c.baseURL)
	if err != nil {
		return nil, err
	}
	u.Path = path.Join(u.Path, "/embeddings")
	body, _ := json.Marshal(map[string]interface{}{"model": c.model, "input": text})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u.String(), bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+c.apiKey)
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return nil, fmt.Errorf("embedding request failed: %s", resp.Status)
	}
	var parsed struct {
		Data []struct {
			Embedding []float64 `json:"embedding"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&parsed); err != nil {
		return nil, err
	}
	if len(parsed.Data) == 0 || len(parsed.Data[0].Embedding) == 0 {
		return nil, fmt.Errorf("embedding response has no vector")
	}
	return parsed.Data[0].Embedding, nil
}

func normalizeURL(raw, scheme string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" || strings.Contains(raw, "://") {
		return raw
	}
	return scheme + "://" + raw
}
