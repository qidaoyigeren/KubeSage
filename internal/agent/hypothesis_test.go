package agent

import (
	"testing"
	"time"

	"kubesage/internal/diagnostic"
	"kubesage/internal/model"
)

func TestHypothesisEngineScoresEvidenceRefs(t *testing.T) {
	engine := NewHypothesisEngine()
	scores := engine.Update(1, nil, []diagnostic.EvidenceRecord{
		{SourceType: "prometheus", Title: "OOM memory working set near limit", Content: "oom memory working set limit", Severity: "critical", Timestamp: time.Now()},
		{SourceType: "k8s_log", Title: "Previous logs", Content: "fatal oom memory allocation", Severity: "warning", Timestamp: time.Now()},
	})
	memory := findScore(scores, "memory_limit_too_low")
	if memory == nil {
		t.Fatalf("missing memory hypothesis")
	}
	if memory.Status != model.HypothesisStatusConfirmed {
		t.Fatalf("expected confirmed memory hypothesis, got %s score %.2f", memory.Status, memory.Confidence)
	}
	if len(memory.SupportingRefs) == 0 {
		t.Fatalf("expected supporting refs")
	}
}

func TestHypothesisEngineRejectsUnsupportedCandidates(t *testing.T) {
	engine := NewHypothesisEngine()
	scores := engine.Update(1, nil, []diagnostic.EvidenceRecord{{SourceType: "k8s_event", Title: "BackOff", Content: "backoff restarting", Timestamp: time.Now()}})
	pvc := findScore(scores, "pvc_unbound")
	if pvc == nil {
		t.Fatalf("missing pvc hypothesis")
	}
	if pvc.Status != model.HypothesisStatusRejected {
		t.Fatalf("expected unsupported pvc hypothesis rejected, got %s score %.2f", pvc.Status, pvc.Confidence)
	}
	if len(pvc.MissingEvidence) == 0 {
		t.Fatalf("expected missing evidence")
	}
}

func TestHypothesisEngineDoesNotTreatFalseNodePressureAsSupport(t *testing.T) {
	engine := NewHypothesisEngine()
	ctx := &diagnostic.DiagnosticContext{
		Topology: &diagnostic.TopologyInfo{
			Node: &diagnostic.NodeHealth{
				Name:           "node-a",
				Ready:          true,
				MemoryPressure: false,
				DiskPressure:   false,
				PIDPressure:    false,
			},
		},
	}
	scores := engine.Update(1, ctx, []diagnostic.EvidenceRecord{
		{
			SourceType: "k8s_topology",
			Title:      "Kubernetes workload, service, and node topology",
			Content:    "node=node-a nodeReady=true memoryPressure=false diskPressure=false pidPressure=false",
			Severity:   "info",
			Timestamp:  time.Now(),
		},
	})
	memoryPressure := findScore(scores, "node_memory_pressure")
	if memoryPressure == nil {
		t.Fatalf("missing node memory pressure hypothesis")
	}
	if memoryPressure.Status != model.HypothesisStatusRejected {
		t.Fatalf("expected false pressure hypothesis rejected, got %s score %.2f refs=%v", memoryPressure.Status, memoryPressure.Confidence, memoryPressure.SupportingRefs)
	}
	if len(memoryPressure.SupportingRefs) != 0 {
		t.Fatalf("expected no supporting refs for false pressure, got %v", memoryPressure.SupportingRefs)
	}
}

func findScore(scores []HypothesisScore, kind string) *HypothesisScore {
	for i := range scores {
		if scores[i].Type == kind {
			return &scores[i]
		}
	}
	return nil
}
