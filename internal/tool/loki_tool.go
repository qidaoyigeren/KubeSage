package tool

import (
	"context"
	"time"
)

type LokiLogEntry struct {
	Timestamp time.Time `json:"timestamp"`
	Line      string    `json:"line"`
}

type LokiClient interface {
	QueryPodLogs(ctx context.Context, namespace, podName, containerName string, start, end time.Time) ([]LokiLogEntry, error)
}

type LokiTool struct{}

func (t *LokiTool) Name() string { return "loki" }

func (t *LokiTool) Execute(ctx context.Context, input map[string]interface{}) (*Result, error) {
	return &Result{Name: t.Name(), Data: "loki tool is reserved; MVP uses Kubernetes Pod logs"}, nil
}
