package tool

import (
	"context"
	"fmt"
	"time"

	"kubesage/internal/loki"
)

type LokiLogEntry struct {
	Timestamp time.Time `json:"timestamp"`
	Line      string    `json:"line"`
}

type lokiQueryClient interface {
	QueryPodLogs(ctx context.Context, namespace, podName, containerName string, start, end time.Time) ([]loki.LogEntry, error)
}

type LokiTool struct {
	client lokiQueryClient
}

// NewLokiTool creates a tool wrapper around the real Loki HTTP client.
func NewLokiTool(client lokiQueryClient) *LokiTool {
	return &LokiTool{client: client}
}

// Name returns the tool identifier.
func (t *LokiTool) Name() string { return "loki" }

// Execute queries Loki for a pod log window.
func (t *LokiTool) Execute(ctx context.Context, input map[string]interface{}) (*Result, error) {
	if t.client == nil {
		return &Result{Name: t.Name(), Err: "loki client is not configured"}, nil
	}
	namespace := stringInput(input, "namespace")
	podName := stringInput(input, "pod_name")
	containerName := stringInput(input, "container_name")
	start, end, err := timeWindowInput(input)
	if err != nil {
		return &Result{Name: t.Name(), Err: err.Error()}, nil
	}
	entries, err := t.client.QueryPodLogs(ctx, namespace, podName, containerName, start, end)
	if err != nil {
		return &Result{Name: t.Name(), Err: err.Error()}, nil
	}
	return &Result{Name: t.Name(), Data: entries}, nil
}

// stringInput extracts a string map value.
func stringInput(input map[string]interface{}, key string) string {
	if value, ok := input[key].(string); ok {
		return value
	}
	return ""
}

// timeWindowInput parses RFC3339 start/end values, defaulting to last 10m.
func timeWindowInput(input map[string]interface{}) (time.Time, time.Time, error) {
	end := time.Now()
	start := end.Add(-10 * time.Minute)
	if raw := stringInput(input, "start"); raw != "" {
		parsed, err := time.Parse(time.RFC3339, raw)
		if err != nil {
			return time.Time{}, time.Time{}, fmt.Errorf("invalid start: %w", err)
		}
		start = parsed
	}
	if raw := stringInput(input, "end"); raw != "" {
		parsed, err := time.Parse(time.RFC3339, raw)
		if err != nil {
			return time.Time{}, time.Time{}, fmt.Errorf("invalid end: %w", err)
		}
		end = parsed
	}
	return start, end, nil
}
