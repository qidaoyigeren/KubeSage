package prometheus

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"kubesage/internal/config"
)

// TestQueryMemoryWorkingSetBytes verifies query_range URL construction and
// matrix sample parsing for memory working set metrics.
func TestQueryMemoryWorkingSetBytes(t *testing.T) {
	start := time.Unix(100, 0)
	end := time.Unix(160, 0)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/prometheus/api/v1/query_range" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		query := `container_memory_working_set_bytes{namespace="default",pod="pod-a",container="app"}`
		if got := r.URL.Query().Get("query"); got != query {
			t.Fatalf("unexpected query: %s", got)
		}
		if got := r.URL.Query().Get("start"); got != "100" {
			t.Fatalf("unexpected start: %s", got)
		}
		if got := r.URL.Query().Get("end"); got != "160" {
			t.Fatalf("unexpected end: %s", got)
		}
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"status": "success",
			"data": map[string]interface{}{
				"resultType": "matrix",
				"result": []map[string]interface{}{
					{
						"metric": map[string]string{"container": "app"},
						"values": [][]interface{}{
							{float64(100), "1024"},
							{float64(130), "2048"},
						},
					},
				},
			},
		})
	}))
	defer server.Close()

	client := NewClient(config.PrometheusConfig{
		BaseURL:        server.URL + "/prometheus",
		TimeoutSeconds: 1,
	})
	result, err := client.QueryMemoryWorkingSetBytes(context.Background(), "default", "pod-a", "app", start, end)
	if err != nil {
		t.Fatalf("query failed: %v", err)
	}
	points := result.AllPoints()
	if len(points) != 2 {
		t.Fatalf("expected 2 points, got %d", len(points))
	}
	if points[0].Value != 1024 || points[1].Value != 2048 {
		t.Fatalf("unexpected points: %#v", points)
	}
}

// TestQueryRangePrometheusError verifies Prometheus error responses are
// returned as Go errors.
func TestQueryRangePrometheusError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]string{
			"status":    "error",
			"errorType": "bad_data",
			"error":     "invalid query",
		})
	}))
	defer server.Close()

	client := NewClient(config.PrometheusConfig{BaseURL: server.URL})
	_, err := client.QueryRange(context.Background(), "bad", time.Unix(100, 0), time.Unix(160, 0), 30*time.Second)
	if err == nil {
		t.Fatal("expected error")
	}
}
