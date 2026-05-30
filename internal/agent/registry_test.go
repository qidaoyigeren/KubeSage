package agent

import (
	"context"
	"testing"

	"kubesage/internal/diagnostic"
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
