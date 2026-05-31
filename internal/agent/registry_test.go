package agent

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"kubesage/internal/config"
	"kubesage/internal/diagnostic"
	"kubesage/internal/loki"
	"kubesage/internal/prometheus"
)

func TestToolRegistryRegisterAndLookup(t *testing.T) {
	registry := NewToolRegistry()
	tool := confirmedTool{}
	if err := registry.Register(tool); err != nil {
		t.Fatal(err)
	}
	if err := registry.Register(tool); err == nil {
		t.Fatalf("expected duplicate registration error")
	}
	if _, ok := registry.Get("k8s.get_pod"); !ok {
		t.Fatalf("expected registered tool")
	}
	if _, ok := registry.Get("missing"); ok {
		t.Fatalf("missing tool should not be found")
	}
}

func TestDefaultRegistryAliasesCanonicalTools(t *testing.T) {
	registry, err := NewDefaultRegistry(RegistryOptions{
		Snapshot: func(ctx context.Context, goal Goal) (*diagnostic.DiagnosticContext, error) {
			return oomContext(), nil
		},
		Policy: NewRemediationPolicy(true),
	})
	if err != nil {
		t.Fatal(err)
	}
	for alias, canonical := range map[string]string{
		"k8s.get_previous_logs":     "k8s.get_logs",
		"k8s.get_pvc_status":        "k8s.get_pvc",
		"remediation.dry_run_patch": "remediation.dry_run",
	} {
		aliasTool, ok := registry.Get(alias)
		if !ok {
			t.Fatalf("missing alias %s", alias)
		}
		canonicalTool, ok := registry.Get(canonical)
		if !ok {
			t.Fatalf("missing canonical tool %s", canonical)
		}
		if aliasTool.Metadata().Name != canonicalTool.Metadata().Name {
			t.Fatalf("alias %s resolved to %s, want %s", alias, aliasTool.Metadata().Name, canonicalTool.Metadata().Name)
		}
	}
}

func TestDefaultRegistryCriticalAndNonCriticalMetadata(t *testing.T) {
	registry, err := NewDefaultRegistry(RegistryOptions{
		Snapshot: func(ctx context.Context, goal Goal) (*diagnostic.DiagnosticContext, error) {
			return oomContext(), nil
		},
		Policy: NewRemediationPolicy(true),
	})
	if err != nil {
		t.Fatal(err)
	}
	podTool, ok := registry.Get("k8s.get_pod")
	if !ok {
		t.Fatalf("missing pod tool")
	}
	if !podTool.Metadata().Critical {
		t.Fatalf("k8s.get_pod must be critical")
	}
	runbookTool, ok := registry.Get("runbook.search")
	if !ok {
		t.Fatalf("missing runbook tool")
	}
	if runbookTool.Metadata().Critical {
		t.Fatalf("runbook.search must be non-critical")
	}
}

func TestPrometheusQueryRangeToolUsesRealClient(t *testing.T) {
	now := time.Unix(1700000000, 0).UTC()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/query_range" {
			t.Fatalf("unexpected prometheus path: %s", r.URL.Path)
		}
		if r.URL.Query().Get("query") != "up" {
			t.Fatalf("unexpected query: %s", r.URL.Query().Get("query"))
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprintf(w, `{"status":"success","data":{"result":[{"metric":{"job":"test"},"values":[[%d,"1"]]}]}}`, now.Unix())
	}))
	defer server.Close()

	registry, err := NewDefaultRegistry(RegistryOptions{
		Prometheus: prometheus.NewClient(config.PrometheusConfig{BaseURL: server.URL, TimeoutSeconds: 2}),
		Policy:     NewRemediationPolicy(true),
	})
	if err != nil {
		t.Fatal(err)
	}
	tool, ok := registry.Get("prometheus.query_range")
	if !ok {
		t.Fatal("missing prometheus tool")
	}
	result := tool.Execute(context.Background(), map[string]interface{}{
		"query":        "up",
		"start":        now.Add(-time.Minute).Format(time.RFC3339),
		"end":          now.Format(time.RFC3339),
		"step_seconds": 30,
	}, &ToolState{})
	if !result.Success {
		t.Fatalf("prometheus tool failed: %s", result.Error)
	}
	if len(result.EvidenceRecords) != 1 || result.EvidenceRecords[0].SourceType != "prometheus" {
		t.Fatalf("expected prometheus evidence, got %#v", result.EvidenceRecords)
	}
	if len(result.MissingEvidence) != 0 {
		t.Fatalf("unexpected missing evidence: %#v", result.MissingEvidence)
	}
}

func TestLokiQueryLogsToolUsesRealClient(t *testing.T) {
	now := time.Unix(1700000000, 0).UTC()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/loki/api/v1/query_range" {
			t.Fatalf("unexpected loki path: %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprintf(w, `{"status":"success","data":{"result":[{"stream":{"pod":"api-0"},"values":[["%d","boom"]]}]}}`, now.UnixNano())
	}))
	defer server.Close()

	registry, err := NewDefaultRegistry(RegistryOptions{
		Loki:   loki.NewClient(config.LokiConfig{BaseURL: server.URL, TimeoutSeconds: 2}),
		Policy: NewRemediationPolicy(true),
	})
	if err != nil {
		t.Fatal(err)
	}
	tool, ok := registry.Get("loki.query_logs")
	if !ok {
		t.Fatal("missing loki tool")
	}
	result := tool.Execute(context.Background(), map[string]interface{}{
		"namespace":      "default",
		"pod_name":       "api-0",
		"container_name": "app",
		"start":          now.Add(-time.Minute).Format(time.RFC3339),
		"end":            now.Format(time.RFC3339),
	}, &ToolState{})
	if !result.Success {
		t.Fatalf("loki tool failed: %s", result.Error)
	}
	if len(result.EvidenceRecords) != 1 || result.EvidenceRecords[0].SourceType != "loki" {
		t.Fatalf("expected loki evidence, got %#v", result.EvidenceRecords)
	}
}
