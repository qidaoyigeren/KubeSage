package agent

import (
	"testing"
	"time"

	"kubesage/internal/diagnostic"
	"kubesage/internal/model"
)

func TestHypothesisEngineRequiresCurveShapeBeforeConfirmingMemoryLimit(t *testing.T) {
	engine := NewHypothesisEngine()
	scores := engine.Update(1, nil, []diagnostic.EvidenceRecord{
		{SourceType: "prometheus", Title: "OOM memory working set near limit", Content: "oom memory working set limit", Severity: "critical", Timestamp: time.Now()},
		{SourceType: "k8s_log", Title: "Previous logs", Content: "fatal oom memory allocation", Severity: "warning", Timestamp: time.Now()},
	})
	memory := findScore(scores, "memory_limit_too_low")
	if memory == nil {
		t.Fatalf("missing memory hypothesis")
	}
	if memory.Status != model.HypothesisStatusActive || memory.Confidence >= 0.75 {
		t.Fatalf("generic OOM evidence must remain active at 0.70, got %s score %.2f", memory.Status, memory.Confidence)
	}
	if len(memory.SupportingRefs) == 0 {
		t.Fatalf("expected supporting refs")
	}
	if len(memory.MissingEvidence) == 0 {
		t.Fatal("expected missing memory curve-shape evidence")
	}
}

func TestHypothesisEngineConfirmsMemoryLimitWithCurveShape(t *testing.T) {
	engine := NewHypothesisEngine()
	scores := engine.Update(1, nil, []diagnostic.EvidenceRecord{{
		SourceType: "prometheus",
		Title:      "OOM memory working set pattern",
		Content:    "oom memory working set limit prometheus memoryPattern=memory_limit_too_low",
		Severity:   "critical",
		Timestamp:  time.Now(),
	}})
	memory := findScore(scores, "memory_limit_too_low")
	if memory == nil || memory.Status != model.HypothesisStatusConfirmed {
		t.Fatalf("expected curve-backed memory hypothesis to confirm, got %#v", memory)
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

func TestHypothesisEngineConfirmsBadConfigCrashLoop(t *testing.T) {
	engine := NewHypothesisEngine()
	scores := engine.Update(1, nil, []diagnostic.EvidenceRecord{
		{SourceType: "k8s_event", Title: "BackOff", Content: "Back-off restarting failed container", Severity: "warning", Timestamp: time.Now()},
		{SourceType: "k8s_log", Title: "Previous logs", Content: "fatal: missing config file /etc/app/config.yaml", Severity: "warning", Timestamp: time.Now()},
	})
	badConfig := findScore(scores, "bad_config")
	if badConfig == nil {
		t.Fatalf("missing bad_config hypothesis")
	}
	if badConfig.Status != model.HypothesisStatusConfirmed {
		t.Fatalf("expected confirmed bad_config hypothesis, got %s score %.2f", badConfig.Status, badConfig.Confidence)
	}
}

func TestHypothesisEnginePrefersImagePullOverImageConfigurationText(t *testing.T) {
	engine := NewHypothesisEngine()
	scores := engine.Update(1, nil, []diagnostic.EvidenceRecord{
		{SourceType: "k8s_pod_status", Title: "Container image configuration", Content: "container=app image=registry.example.com/app:missing imagePullPolicy=IfNotPresent", Severity: "warning", Timestamp: time.Now()},
		{SourceType: "k8s_event", Title: "Failed", Content: "Error: ImagePullBackOff", Severity: "critical", Timestamp: time.Now()},
	})
	imagePull := findScore(scores, "image_pull_failed")
	badConfig := findScore(scores, "bad_config")
	if imagePull == nil || badConfig == nil {
		t.Fatalf("missing hypotheses imagePull=%#v badConfig=%#v", imagePull, badConfig)
	}
	if imagePull.Status != model.HypothesisStatusConfirmed {
		t.Fatalf("expected confirmed image_pull_failed, got %s score %.2f", imagePull.Status, imagePull.Confidence)
	}
	if badConfig.Confidence >= imagePull.Confidence {
		t.Fatalf("image configuration text should not outrank image pull: image=%.2f bad_config=%.2f", imagePull.Confidence, badConfig.Confidence)
	}
}

func TestHypothesisEnginePrefersInitContainerCrashOverBadConfig(t *testing.T) {
	engine := NewHypothesisEngine()
	scores := engine.Update(1, nil, []diagnostic.EvidenceRecord{
		{SourceType: "k8s_pod_status", Title: "Init container status", Content: "init container init-config CrashLoopBackOff exitCode=1", Severity: "warning", Timestamp: time.Now()},
		{SourceType: "k8s_pod_status", Title: "Init container spec", Content: "initContainer command failed to render config: missing required key", Severity: "info", Timestamp: time.Now()},
		{SourceType: "k8s_event", Title: "BackOff", Content: "Back-off restarting failed container init-config", Severity: "warning", Timestamp: time.Now()},
	})
	initCrash := findScore(scores, "init_container_crash")
	badConfig := findScore(scores, "bad_config")
	if initCrash == nil || badConfig == nil {
		t.Fatalf("missing hypotheses init=%#v badConfig=%#v", initCrash, badConfig)
	}
	if initCrash.Status != model.HypothesisStatusConfirmed {
		t.Fatalf("expected confirmed init_container_crash, got %s score %.2f", initCrash.Status, initCrash.Confidence)
	}
	if initCrash.Confidence <= badConfig.Confidence {
		t.Fatalf("expected init container crash to outrank bad config, init=%.2f bad_config=%.2f", initCrash.Confidence, badConfig.Confidence)
	}
}

func TestHypothesisEngineDoesNotTreatProbeInitialDelayAsInitContainer(t *testing.T) {
	engine := NewHypothesisEngine()
	scores := engine.Update(1, nil, []diagnostic.EvidenceRecord{
		{SourceType: "k8s_event", Title: "Unhealthy", Content: "Readiness probe failed: HTTP probe failed with statuscode: 404", Severity: "warning", Timestamp: time.Now()},
		{SourceType: "k8s_pod_status", Title: "readinessProbe", Content: "path=/healthz port=8080 initialDelaySeconds=1 timeoutSeconds=1", Severity: "info", Timestamp: time.Now()},
		{SourceType: "k8s_pod_status", Title: "Container restart evidence", Content: "container=app restartCount=2 lastReason=Error exitCode=1", Severity: "warning", Timestamp: time.Now()},
	})
	probe := findScore(scores, "probe_misconfigured")
	initCrash := findScore(scores, "init_container_crash")
	if probe == nil || initCrash == nil {
		t.Fatalf("missing hypotheses probe=%#v init=%#v", probe, initCrash)
	}
	if probe.Status != model.HypothesisStatusConfirmed {
		t.Fatalf("expected confirmed probe hypothesis, got %s score %.2f", probe.Status, probe.Confidence)
	}
	if initCrash.Status == model.HypothesisStatusConfirmed || initCrash.Confidence >= probe.Confidence {
		t.Fatalf("initialDelaySeconds should not imply init container crash: init=%.2f/%s probe=%.2f/%s", initCrash.Confidence, initCrash.Status, probe.Confidence, probe.Status)
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
