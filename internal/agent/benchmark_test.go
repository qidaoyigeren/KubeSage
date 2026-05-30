package agent

import (
	"context"
	"strings"
	"testing"
	"time"

	"kubesage/internal/diagnostic"
)

func BenchmarkAgentPlanning(b *testing.B) {
	planner := NewRulePlanner()
	tools := []ToolMetadata{
		{Name: "k8s.get_pod"}, {Name: "runbook.search"}, {Name: "k8s.get_previous_logs"},
		{Name: "prometheus.query_range"}, {Name: "loki.query_logs"}, {Name: "k8s.get_topology"},
	}
	goal := Goal{Namespace: "default", PodName: "api-0", ExpectedFault: "OOMKilled"}
	for i := 0; i < b.N; i++ {
		_ = planner.BuildInitialPlan(context.Background(), goal, tools)
	}
}

func BenchmarkToolExecutionDispatch(b *testing.B) {
	registry, err := NewDefaultRegistry(RegistryOptions{
		Snapshot: func(ctx context.Context, goal Goal) (*diagnostic.DiagnosticContext, error) {
			return oomContext(), nil
		},
		Policy: NewRemediationPolicy(true),
	})
	if err != nil {
		b.Fatal(err)
	}
	tool, _ := registry.Get("k8s.get_pod")
	state := &ToolState{TaskID: 1, Goal: Goal{Namespace: "default", PodName: "api-0"}}
	input := map[string]interface{}{"namespace": "default", "pod_name": "api-0"}
	for i := 0; i < b.N; i++ {
		state.DiagnosticContext = nil
		_ = tool.Execute(context.Background(), input, state)
	}
}

func BenchmarkHypothesisScoring(b *testing.B) {
	engine := NewHypothesisEngine()
	records := []diagnostic.EvidenceRecord{
		{SourceType: "prometheus", Title: "OOM memory working set near limit", Content: "oom memory working set limit", Timestamp: time.Now()},
		{SourceType: "k8s_log", Title: "Previous logs", Content: "fatal oom memory allocation", Timestamp: time.Now()},
	}
	for i := 0; i < b.N; i++ {
		_ = engine.Update(1, nil, records)
	}
}

func BenchmarkLogKeywordExtraction(b *testing.B) {
	text := strings.Repeat("startup config missing connection refused timeout readiness probe failed oom ", 100)
	keywords := []string{"oom", "config", "secret", "timeout", "probe", "refused"}
	for i := 0; i < b.N; i++ {
		matches := 0
		for _, keyword := range keywords {
			if strings.Contains(text, keyword) {
				matches++
			}
		}
		if matches == 0 {
			b.Fatal("expected keyword match")
		}
	}
}
