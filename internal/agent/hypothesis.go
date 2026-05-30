package agent

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"kubesage/internal/diagnostic"
	"kubesage/internal/model"
	"kubesage/internal/observability"
)

type HypothesisEngine struct{}

type HypothesisScore struct {
	Type              string
	Summary           string
	Confidence        float64
	Status            string
	SupportingRefs    []string
	ContradictingRefs []string
	MissingEvidence   []string
	RejectedReason    string
}

// NewHypothesisEngine creates the evidence-weighted hypothesis scorer.
func NewHypothesisEngine() *HypothesisEngine {
	return &HypothesisEngine{}
}

// Update recalculates candidate root-cause hypotheses from observations and
// analyzer evidence.
func (e *HypothesisEngine) Update(taskID uint, ctx *diagnostic.DiagnosticContext, records []diagnostic.EvidenceRecord) []HypothesisScore {
	_ = taskID
	facts := evidenceFacts(ctx, records)
	candidates := []HypothesisScore{
		scoreMemoryLimitTooLow(facts),
		scoreApplicationMemoryLeak(facts),
		scoreNodeMemoryPressure(facts),
		scoreBadConfig(facts),
		scoreMissingSecretOrConfigMap(facts),
		scoreDependencyUnavailable(facts),
		scoreProbeMisconfigured(facts),
		scorePVCUnbound(facts),
		scoreSchedulingConstraint(facts),
	}
	for i := range candidates {
		if len(candidates[i].SupportingRefs) == 0 {
			candidates[i].Confidence = 0.15
		}
		candidates[i].Confidence = clamp(candidates[i].Confidence)
		switch {
		case candidates[i].Confidence >= 0.75:
			candidates[i].Status = model.HypothesisStatusConfirmed
		case candidates[i].Confidence <= 0.20 || (len(candidates[i].ContradictingRefs) > 0 && len(candidates[i].SupportingRefs) == 0):
			candidates[i].Status = model.HypothesisStatusRejected
			if candidates[i].RejectedReason == "" {
				candidates[i].RejectedReason = "insufficient supporting evidence or contradicting observations"
			}
		default:
			candidates[i].Status = model.HypothesisStatusActive
		}
		observability.IncAgentHypothesisUpdatesTotal(candidates[i].Type, candidates[i].Status)
	}
	return candidates
}

// ToModels converts in-memory scores into database rows.
func (e *HypothesisEngine) ToModels(taskID uint, scores []HypothesisScore) []model.Hypothesis {
	now := time.Now()
	result := make([]model.Hypothesis, 0, len(scores))
	for _, score := range scores {
		result = append(result, model.Hypothesis{
			TaskID:                    taskID,
			HypothesisType:            score.Type,
			Summary:                   score.Summary,
			ConfidenceScore:           score.Confidence,
			Status:                    score.Status,
			SupportingEvidenceRefs:    jsonText(score.SupportingRefs),
			ContradictingEvidenceRefs: jsonText(score.ContradictingRefs),
			MissingEvidence:           jsonText(score.MissingEvidence),
			RejectedReason:            score.RejectedReason,
			CreatedAt:                 now,
			UpdatedAt:                 now,
		})
	}
	return result
}

type facts struct {
	text       string
	refsByKey  map[string][]string
	sourceRefs map[string][]string
}

func evidenceFacts(ctx *diagnostic.DiagnosticContext, records []diagnostic.EvidenceRecord) facts {
	f := facts{refsByKey: map[string][]string{}, sourceRefs: map[string][]string{}}
	for i, record := range records {
		ref := evidenceRef(record, i)
		text := strings.ToLower(record.SourceType + " " + record.Title + " " + record.Content)
		f.text += "\n" + text
		f.sourceRefs[record.SourceType] = append(f.sourceRefs[record.SourceType], ref)
		for _, key := range []string{
			"oom", "oomkilled", "memory", "working set", "limit", "backoff", "crashloop", "config", "secret", "configmap",
			"refused", "timeout", "unhealthy", "probe", "failedscheduling", "taint", "selector", "pvc", "bound", "memorypressure",
		} {
			if strings.Contains(text, key) {
				f.refsByKey[key] = append(f.refsByKey[key], ref)
			}
		}
	}
	if ctx != nil && ctx.Topology != nil && ctx.Topology.Node != nil && ctx.Topology.Node.MemoryPressure {
		f.text += "\nnode memorypressure"
		f.refsByKey["memorypressure"] = append(f.refsByKey["memorypressure"], "topology:node_memory_pressure")
	}
	for _, pvc := range ctxPVCs(ctx) {
		if pvc.Phase != "Bound" {
			ref := "pvc:" + pvc.Name
			f.refsByKey["pvc"] = append(f.refsByKey["pvc"], ref)
			f.refsByKey["bound"] = append(f.refsByKey["bound"], ref)
			f.text += "\npvc unbound"
		}
	}
	return f
}

func scoreMemoryLimitTooLow(f facts) HypothesisScore {
	s := base("memory_limit_too_low", "Container memory limit may be too low for observed workload.")
	s.add(0.25, refs(f, "oom", "oomkilled")...)
	s.add(0.20, refs(f, "working set", "limit")...)
	s.add(0.10, f.sourceRefs["prometheus"]...)
	if !contains(f.text, "prometheus") {
		s.MissingEvidence = append(s.MissingEvidence, "memory metrics")
	}
	return s
}

func scoreApplicationMemoryLeak(f facts) HypothesisScore {
	s := base("application_memory_leak", "Application memory may grow until the container is killed.")
	s.add(0.20, refs(f, "oom", "oomkilled")...)
	s.add(0.10, refs(f, "memory")...)
	s.MissingEvidence = append(s.MissingEvidence, "heap/profile or sustained memory growth evidence")
	return s
}

func scoreNodeMemoryPressure(f facts) HypothesisScore {
	s := base("node_memory_pressure", "Node-level memory pressure may contribute to pod instability.")
	s.add(0.35, refs(f, "memorypressure")...)
	s.add(0.10, f.sourceRefs["k8s_topology"]...)
	if len(s.SupportingRefs) == 0 {
		s.MissingEvidence = append(s.MissingEvidence, "node pressure observation")
	}
	return s
}

func scoreBadConfig(f facts) HypothesisScore {
	s := base("bad_config", "Application startup may fail because of invalid configuration.")
	s.add(0.25, refs(f, "config", "configmap")...)
	s.add(0.15, refs(f, "backoff", "crashloop")...)
	if len(s.SupportingRefs) == 0 {
		s.MissingEvidence = append(s.MissingEvidence, "logs or events mentioning configuration errors")
	}
	return s
}

func scoreMissingSecretOrConfigMap(f facts) HypothesisScore {
	s := base("missing_secret_or_configmap", "A referenced Secret or ConfigMap may be missing or incomplete.")
	s.add(0.30, refs(f, "secret", "configmap")...)
	if len(s.SupportingRefs) == 0 {
		s.MissingEvidence = append(s.MissingEvidence, "secret/configmap reference evidence")
	}
	return s
}

func scoreDependencyUnavailable(f facts) HypothesisScore {
	s := base("dependency_unavailable", "A downstream dependency may be unavailable during startup or health checks.")
	s.add(0.25, refs(f, "refused", "timeout")...)
	s.add(0.10, refs(f, "backoff", "probe")...)
	if len(s.SupportingRefs) == 0 {
		s.MissingEvidence = append(s.MissingEvidence, "dependency timeout/refused log evidence")
	}
	return s
}

func scoreProbeMisconfigured(f facts) HypothesisScore {
	s := base("probe_misconfigured", "Readiness or liveness probe settings may not match the application.")
	s.add(0.30, refs(f, "probe", "unhealthy")...)
	if len(s.SupportingRefs) == 0 {
		s.MissingEvidence = append(s.MissingEvidence, "probe events and probe spec")
	}
	return s
}

func scorePVCUnbound(f facts) HypothesisScore {
	s := base("pvc_unbound", "A PersistentVolumeClaim may be unbound and blocking scheduling/startup.")
	s.add(0.35, refs(f, "pvc", "bound")...)
	if len(s.SupportingRefs) == 0 {
		s.MissingEvidence = append(s.MissingEvidence, "pvc status")
	}
	return s
}

func scoreSchedulingConstraint(f facts) HypothesisScore {
	s := base("scheduling_constraint", "Scheduling constraints may prevent the pod from running.")
	s.add(0.30, refs(f, "failedscheduling", "taint", "selector")...)
	if len(s.SupportingRefs) == 0 {
		s.MissingEvidence = append(s.MissingEvidence, "scheduler events")
	}
	return s
}

func base(kind, summary string) HypothesisScore {
	return HypothesisScore{Type: kind, Summary: summary, Confidence: 0.30}
}

func (s *HypothesisScore) add(weight float64, refs ...string) {
	if len(refs) == 0 {
		return
	}
	s.Confidence += weight
	s.SupportingRefs = appendUnique(s.SupportingRefs, refs...)
}

func refs(f facts, keys ...string) []string {
	result := []string{}
	for _, key := range keys {
		result = appendUnique(result, f.refsByKey[key]...)
	}
	return result
}

func evidenceRef(record diagnostic.EvidenceRecord, index int) string {
	source := strings.TrimSpace(record.SourceType)
	if source == "" {
		source = "evidence"
	}
	title := strings.TrimSpace(record.Title)
	if title == "" {
		title = fmt.Sprintf("%d", index+1)
	}
	return source + ":" + title
}

func ctxPVCs(ctx *diagnostic.DiagnosticContext) []diagnostic.PVCBrief {
	if ctx == nil {
		return nil
	}
	return ctx.PVCs
}

func contains(text, needle string) bool {
	return strings.Contains(strings.ToLower(text), strings.ToLower(needle))
}

func appendUnique(items []string, additions ...string) []string {
	seen := map[string]struct{}{}
	for _, item := range items {
		seen[item] = struct{}{}
	}
	for _, item := range additions {
		if strings.TrimSpace(item) == "" {
			continue
		}
		if _, ok := seen[item]; ok {
			continue
		}
		seen[item] = struct{}{}
		items = append(items, item)
	}
	return items
}

func jsonText(value interface{}) model.JSONText {
	bytes, err := json.Marshal(value)
	if err != nil {
		return model.JSONText("null")
	}
	return model.JSONText(bytes)
}

func clamp(v float64) float64 {
	if v < 0 {
		return 0
	}
	if v > 1 {
		return 1
	}
	return v
}
