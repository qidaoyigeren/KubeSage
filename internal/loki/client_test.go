package loki

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"kubesage/internal/config"
)

// TestQueryPodLogs verifies Loki query_range construction and response parsing.
func TestQueryPodLogs(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/loki/api/v1/query_range" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		wantQuery := `{namespace="default",pod="api-0",container="api"}`
		if got := r.URL.Query().Get("query"); got != wantQuery {
			t.Fatalf("unexpected query: %s", got)
		}
		if got := r.Header.Get("X-Scope-OrgID"); got != "tenant-a" {
			t.Fatalf("unexpected tenant header: %s", got)
		}
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"status": "success",
			"data": map[string]interface{}{
				"result": []map[string]interface{}{
					{"stream": map[string]string{"container": "api"}, "values": [][]string{{"1000000000", "panic: config missing"}}},
				},
			},
		})
	}))
	defer server.Close()

	client := NewClient(config.LokiConfig{BaseURL: server.URL, TenantID: "tenant-a", TimeoutSeconds: 1})
	entries, err := client.QueryPodLogs(context.Background(), "default", "api-0", "api", time.Unix(1, 0), time.Unix(2, 0))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Line != "panic: config missing" {
		t.Fatalf("unexpected entries: %#v", entries)
	}
}
