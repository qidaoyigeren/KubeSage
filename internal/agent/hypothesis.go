package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"
	"unicode"

	"kubesage/internal/diagnostic"
	"kubesage/internal/model"
	"kubesage/internal/observability"

	corev1 "k8s.io/api/core/v1"
)

// LLMHypothesisScorer optionally re-ranks hypothesis candidates using an LLM.
type LLMHypothesisScorer interface {
	ScoreHypotheses(ctx context.Context, candidates []HypothesisScore) ([]HypothesisScore, error)
}

type HypothesisEngine struct {
	config    HypothesisScoringConfig
	llmScorer LLMHypothesisScorer
}

// WithLLMScorer sets an optional LLM-based scorer that re-ranks candidates
// after keyword-based scoring.
func (e *HypothesisEngine) WithLLMScorer(scorer LLMHypothesisScorer) *HypothesisEngine {
	e.llmScorer = scorer
	return e
}

type HypothesisScoringConfig struct {
	ConfirmedThreshold float64
	RejectedThreshold  float64
	Weights            map[string]map[string]float64
}

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
func NewHypothesisEngine(configs ...HypothesisScoringConfig) *HypothesisEngine {
	cfg := DefaultHypothesisScoringConfig()
	if len(configs) > 0 {
		cfg = configs[0].WithDefaults(cfg)
	}
	return &HypothesisEngine{config: cfg}
}

func DefaultHypothesisScoringConfig() HypothesisScoringConfig {
	return HypothesisScoringConfig{
		ConfirmedThreshold: 0.75,
		RejectedThreshold:  0.20,
		Weights: map[string]map[string]float64{
			"memory_limit_too_low":        {"oom": 0.25, "working_set_limit": 0.20, "prometheus": 0.10},
			"application_memory_leak":     {"oom": 0.20, "memory": 0.10},
			"node_memory_pressure":        {"memorypressure": 0.35, "k8s_topology": 0.10},
			"bad_config":                  {"config": 0.30, "backoff": 0.20},
			"missing_secret_or_configmap": {"secret_configmap": 0.30},
			"dependency_unavailable":      {"refused_timeout": 0.25, "backoff_probe": 0.10},
			"probe_misconfigured":         {"probe_unhealthy": 0.30, "probe_spec": 0.15},
			"pvc_unbound":                 {"pvc_bound": 0.45},
			"scheduling_constraint":       {"scheduler": 0.45},
			// New hypothesis types for expanded analyzer coverage.
			"image_pull_failed":    {"imagepull": 0.45, "registry_auth": 0.15},
			"init_container_crash": {"init_error": 0.35, "init_exit": 0.20},
			"node_eviction":        {"evicted": 0.45, "node_pressure": 0.15},
			"node_not_ready":       {"nodenotready": 0.45, "node_pressure": 0.10},
		},
	}
}

func (c HypothesisScoringConfig) WithDefaults(defaults HypothesisScoringConfig) HypothesisScoringConfig {
	if c.ConfirmedThreshold <= 0 {
		c.ConfirmedThreshold = defaults.ConfirmedThreshold
	}
	if c.RejectedThreshold <= 0 {
		c.RejectedThreshold = defaults.RejectedThreshold
	}
	if c.Weights == nil {
		c.Weights = defaults.Weights
		return c
	}
	for hypothesis, weights := range defaults.Weights {
		if c.Weights[hypothesis] == nil {
			c.Weights[hypothesis] = weights
			continue
		}
		for key, value := range weights {
			if _, ok := c.Weights[hypothesis][key]; !ok {
				c.Weights[hypothesis][key] = value
			}
		}
	}
	return c
}

// Update recalculates candidate root-cause hypotheses from observations and
// analyzer evidence.
func (e *HypothesisEngine) Update(taskID uint, ctx *diagnostic.DiagnosticContext, records []diagnostic.EvidenceRecord) []HypothesisScore {
	_ = taskID
	facts := evidenceFacts(ctx, records)
	candidates := []HypothesisScore{
		e.scoreMemoryLimitTooLow(facts),
		e.scoreApplicationMemoryLeak(facts),
		e.scoreNodeMemoryPressure(facts),
		e.scoreBadConfig(facts),
		e.scoreMissingSecretOrConfigMap(facts),
		e.scoreDependencyUnavailable(facts),
		e.scoreProbeMisconfigured(facts),
		e.scorePVCUnbound(facts),
		e.scoreSchedulingConstraint(facts),
		e.scoreImagePullFailed(facts),
		e.scoreInitContainerCrash(facts),
		e.scoreNodeEviction(facts),
		e.scoreNodeNotReady(facts),
	}
	for i := range candidates {
		candidates[i].Confidence = clamp(candidates[i].Confidence)
	}

	// LLM scoring is advisory only. Evidence-derived scores remain authoritative:
	// the LLM may reduce confidence when semantics conflict, but it cannot raise
	// confidence or invent supporting evidence.
	if e.llmScorer != nil {
		if llmRanked, err := e.llmScorer.ScoreHypotheses(context.Background(), candidates); err == nil {
			candidates = blendScores(candidates, llmRanked, 0.85)
		}
	}

	for i := range candidates {
		candidates[i].Confidence = clamp(candidates[i].Confidence)
		switch {
		case candidates[i].Confidence >= e.config.ConfirmedThreshold:
			candidates[i].Status = model.HypothesisStatusConfirmed
		case candidates[i].Confidence <= e.config.RejectedThreshold || (len(candidates[i].ContradictingRefs) > 0 && len(candidates[i].SupportingRefs) == 0):
			candidates[i].Status = model.HypothesisStatusRejected
			if candidates[i].RejectedReason == "" {
				if len(candidates[i].ContradictingRefs) > 0 {
					candidates[i].RejectedReason = "explicit structured evidence contradicts this hypothesis"
				} else {
					candidates[i].RejectedReason = "insufficient supporting evidence"
				}
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
	refsByKey map[string][]string
}

func evidenceFacts(ctx *diagnostic.DiagnosticContext, records []diagnostic.EvidenceRecord) facts {
	f := facts{refsByKey: map[string][]string{}}
	for i, record := range records {
		if negativeDiagnosticEvidence(record) {
			continue
		}
		ref := evidenceRef(record, i)
		source := strings.ToLower(strings.TrimSpace(record.SourceType))
		text := strings.ToLower(source + " " + record.Title + " " + record.Content)

		if oomFailureSignal(text) {
			addFactRef(&f, "oom_signal", ref)
		}
		if prometheusMemorySignal(source, text) {
			addFactRef(&f, "prometheus_memory", ref)
		}
		if workingSetNearLimitSignal(source, text) {
			addFactRef(&f, "working_set_limit", ref)
		}
		if memoryGrowthSignal(source, text) {
			addFactRef(&f, "memory_growth", ref)
		}
		if configErrorSignal(text) {
			addFactRef(&f, "config_error", ref)
		}
		if missingSecretOrConfigMapSignal(text) {
			addFactRef(&f, "secret_configmap_missing", ref)
		}
		if pvcUnboundSignal(source, text) {
			addFactRef(&f, "pvc_unbound", ref)
		}
		if probeFailureSignal(source, text) {
			addFactRef(&f, "probe_failure", ref)
		}
		if probeSpecSignal(source, text) {
			addFactRef(&f, "probe_spec", ref)
		}
		if source == "k8s_topology" {
			addFactRef(&f, "topology_context", ref)
		}

		for _, key := range []string{
			"backoff", "crashloop", "refused", "timeout", "failedscheduling", "taint", "selector",
			"imagepull", "errimagepull", "imagepullbackoff", "unauthorized", "denied", "manifest",
			"initerror", "initcontainer", "init container", "exitcode", "evicted", "eviction", "nodenotready", "notready",
		} {
			if strings.Contains(text, key) {
				addFactRef(&f, key, ref)
			}
		}
		for _, key := range []string{"memorypressure", "diskpressure", "pidpressure"} {
			if positiveConditionSignal(text, key) {
				addFactRef(&f, key, ref)
			}
		}
	}
	addStructuredContextFacts(&f, ctx)
	if ctx != nil && ctx.Topology != nil && ctx.Topology.Node != nil {
		node := ctx.Topology.Node
		addFactRef(&f, "topology_context", "topology:node_context")
		if node.MemoryPressure {
			addFactRef(&f, "memorypressure", "topology:node_memory_pressure")
		} else {
			addFactRef(&f, "memorypressure_false", "topology:node_memory_pressure_false")
		}
		if node.DiskPressure {
			addFactRef(&f, "diskpressure", "topology:node_disk_pressure")
		}
		if node.PIDPressure {
			addFactRef(&f, "pidpressure", "topology:node_pid_pressure")
		}
		if !node.Ready {
			addFactRef(&f, "notready", "topology:node_not_ready")
			addFactRef(&f, "nodenotready", "topology:node_not_ready")
		} else {
			addFactRef(&f, "node_ready", "topology:node_ready")
		}
	}
	for _, pvc := range ctxPVCs(ctx) {
		if pvc.Phase != "Bound" {
			ref := "pvc:" + pvc.Name
			addFactRef(&f, "pvc_unbound", ref)
		}
	}
	return f
}

func addStructuredContextFacts(f *facts, ctx *diagnostic.DiagnosticContext) {
	if f == nil || ctx == nil {
		return
	}
	if ctx.Pod != nil {
		podRef := "pod:" + firstNonEmptyString(ctx.PodName, ctx.Pod.Name)
		for _, container := range ctx.Pod.Spec.Containers {
			if container.ReadinessProbe != nil || container.LivenessProbe != nil || container.StartupProbe != nil {
				addFactRef(f, "probe_spec", podRef+":probe_spec:"+container.Name)
			}
		}
		for _, status := range ctx.Pod.Status.ContainerStatuses {
			addContainerStateFacts(f, podRef+":container:"+status.Name, status.State, false)
			addContainerStateFacts(f, podRef+":container:"+status.Name+":last", status.LastTerminationState, false)
		}
		for _, status := range ctx.Pod.Status.InitContainerStatuses {
			addContainerStateFacts(f, podRef+":init:"+status.Name, status.State, true)
			addContainerStateFacts(f, podRef+":init:"+status.Name+":last", status.LastTerminationState, true)
		}
		if strings.EqualFold(strings.TrimSpace(ctx.Pod.Status.Reason), "Evicted") {
			addFactRef(f, "evicted", podRef+":status_reason")
			addFactRef(f, "eviction", podRef+":status_reason")
		}
	}
	for i, trend := range ctx.MetricTrends {
		if trend.SampleCount <= 0 || strings.EqualFold(trend.Classification, "query_failed") {
			continue
		}
		metric := strings.ToLower(trend.Metric)
		if !strings.Contains(metric, "memory") {
			continue
		}
		ref := fmt.Sprintf("metric_trend:%s:%d", trend.Classification, i+1)
		addFactRef(f, "prometheus_memory", ref)
		if strings.EqualFold(trend.Classification, "progressive_growth") {
			addFactRef(f, "memory_growth", ref)
		}
	}
}

func addContainerStateFacts(f *facts, ref string, state corev1.ContainerState, initContainer bool) {
	if state.Waiting != nil {
		reason := strings.ToLower(strings.TrimSpace(state.Waiting.Reason))
		switch reason {
		case "crashloopbackoff":
			addFactRef(f, "crashloop", ref)
			addFactRef(f, "backoff", ref)
		case "imagepullbackoff", "errimagepull":
			addFactRef(f, "imagepull", ref)
			addFactRef(f, reason, ref)
		}
		if initContainer && reason != "" && reason != "podinitializing" {
			addFactRef(f, "initcontainer", ref)
			addFactRef(f, "initerror", ref)
		}
	}
	if state.Terminated == nil {
		return
	}
	reason := strings.ToLower(strings.TrimSpace(state.Terminated.Reason))
	if reason == "oomkilled" {
		addFactRef(f, "oom_signal", ref)
	}
	if state.Terminated.ExitCode != 0 {
		addFactRef(f, "exitcode", ref)
	}
	if initContainer && (state.Terminated.ExitCode != 0 || (reason != "" && reason != "completed")) {
		addFactRef(f, "initcontainer", ref)
		addFactRef(f, "initerror", ref)
	}
}

func negativeDiagnosticEvidence(record diagnostic.EvidenceRecord) bool {
	return strings.EqualFold(strings.TrimSpace(record.SourceType), "diagnostic_engine") &&
		strings.EqualFold(strings.TrimSpace(record.Title), "No analyzer matched")
}

func (e *HypothesisEngine) scoreMemoryLimitTooLow(f facts) HypothesisScore {
	s := newHypothesisScore("memory_limit_too_low", "Container memory limit may be too low for observed workload.")
	s.required(e.weight(s.score.Type, "oom", 0.25), "container termination status", refs(f, "oom_signal")...)
	s.required(e.weight(s.score.Type, "working_set_limit", 0.20), "memory metrics", refs(f, "working_set_limit")...)
	s.optional(e.weight(s.score.Type, "prometheus", 0.10), refs(f, "prometheus_memory")...)
	return s.finish(e.config)
}

func (e *HypothesisEngine) scoreApplicationMemoryLeak(f facts) HypothesisScore {
	s := newHypothesisScore("application_memory_leak", "Application memory may grow until the container is killed.")
	s.required(e.weight(s.score.Type, "oom", 0.20), "container termination status", refs(f, "oom_signal")...)
	s.required(e.weight(s.score.Type, "memory", 0.10), "heap/profile or sustained memory growth evidence", refs(f, "memory_growth")...)
	return s.finish(e.config)
}

func (e *HypothesisEngine) scoreNodeMemoryPressure(f facts) HypothesisScore {
	s := newHypothesisScore("node_memory_pressure", "Node-level memory pressure may contribute to pod instability.")
	s.required(e.weight(s.score.Type, "memorypressure", 0.35), "node pressure observation", refs(f, "memorypressure")...)
	s.requiredContext(e.weight(s.score.Type, "k8s_topology", 0.10), "topology context", refs(f, "topology_context")...)
	s.contradict(refs(f, "memorypressure_false")...)
	return s.finish(e.config)
}

func (e *HypothesisEngine) scoreBadConfig(f facts) HypothesisScore {
	s := newHypothesisScore("bad_config", "Application startup may fail because of invalid configuration.")
	s.required(e.weight(s.score.Type, "config", 0.25), "logs or events mentioning configuration errors", refs(f, "config_error")...)
	s.required(e.weight(s.score.Type, "backoff", 0.15), "restart or backoff evidence", refs(f, "backoff", "crashloop")...)
	return s.finish(e.config)
}

func (e *HypothesisEngine) scoreMissingSecretOrConfigMap(f facts) HypothesisScore {
	s := newHypothesisScore("missing_secret_or_configmap", "A referenced Secret or ConfigMap may be missing or incomplete.")
	s.required(e.weight(s.score.Type, "secret_configmap", 0.30), "secret/configmap reference evidence", refs(f, "secret_configmap_missing")...)
	return s.finish(e.config)
}

func (e *HypothesisEngine) scoreDependencyUnavailable(f facts) HypothesisScore {
	s := newHypothesisScore("dependency_unavailable", "A downstream dependency may be unavailable during startup or health checks.")
	s.required(e.weight(s.score.Type, "refused_timeout", 0.25), "dependency timeout/refused log evidence", refs(f, "refused", "timeout")...)
	s.required(e.weight(s.score.Type, "backoff_probe", 0.10), "restart or probe impact evidence", refs(f, "backoff", "probe_failure")...)
	return s.finish(e.config)
}

func (e *HypothesisEngine) scoreProbeMisconfigured(f facts) HypothesisScore {
	s := newHypothesisScore("probe_misconfigured", "Readiness or liveness probe settings may not match the application.")
	s.required(e.weight(s.score.Type, "probe_unhealthy", 0.30), "probe failure events", refs(f, "probe_failure")...)
	s.required(e.weight(s.score.Type, "probe_spec", 0.15), "probe spec", refs(f, "probe_spec")...)
	return s.finish(e.config)
}

func (e *HypothesisEngine) scorePVCUnbound(f facts) HypothesisScore {
	s := newHypothesisScore("pvc_unbound", "A PersistentVolumeClaim may be unbound and blocking scheduling/startup.")
	s.required(e.weight(s.score.Type, "pvc_bound", 0.35), "pvc status", refs(f, "pvc_unbound")...)
	return s.finish(e.config)
}

func (e *HypothesisEngine) scoreSchedulingConstraint(f facts) HypothesisScore {
	s := newHypothesisScore("scheduling_constraint", "Scheduling constraints may prevent the pod from running.")
	s.required(e.weight(s.score.Type, "scheduler", 0.30), "scheduler events", refs(f, "failedscheduling", "taint", "selector")...)
	return s.finish(e.config)
}

func (e *HypothesisEngine) scoreImagePullFailed(f facts) HypothesisScore {
	s := newHypothesisScore("image_pull_failed", "Container image cannot be pulled due to wrong name, missing tag, or registry authentication failure.")
	s.required(e.weight(s.score.Type, "imagepull", 0.35), "image pull events", refs(f, "imagepull", "errimagepull", "imagepullbackoff")...)
	s.optional(e.weight(s.score.Type, "registry_auth", 0.15), refs(f, "unauthorized", "denied", "manifest")...)
	return s.finish(e.config)
}

func (e *HypothesisEngine) scoreInitContainerCrash(f facts) HypothesisScore {
	s := newHypothesisScore("init_container_crash", "An init container is failing, possibly due to dependency issues or misconfiguration.")
	initRefs := refs(f, "initerror", "initcontainer", "init container")
	s.required(e.weight(s.score.Type, "init_error", 0.30), "init container status and logs", initRefs...)
	if len(initRefs) > 0 {
		s.optional(e.weight(s.score.Type, "init_exit", 0.15), refs(f, "exitcode")...)
	} else {
		s.optional(e.weight(s.score.Type, "init_exit", 0.15))
	}
	return s.finish(e.config)
}

func (e *HypothesisEngine) scoreNodeEviction(f facts) HypothesisScore {
	s := newHypothesisScore("node_eviction", "Pod was evicted due to node resource pressure (memory, disk, or PID).")
	s.required(e.weight(s.score.Type, "evicted", 0.35), "eviction events", refs(f, "evicted", "eviction")...)
	s.optional(e.weight(s.score.Type, "node_pressure", 0.15), refs(f, "memorypressure", "diskpressure", "pidpressure")...)
	return s.finish(e.config)
}

func (e *HypothesisEngine) scoreNodeNotReady(f facts) HypothesisScore {
	s := newHypothesisScore("node_not_ready", "The node hosting this pod is in NotReady state, causing pod instability.")
	s.required(e.weight(s.score.Type, "nodenotready", 0.40), "node Ready condition", refs(f, "nodenotready", "notready")...)
	s.optional(e.weight(s.score.Type, "node_pressure", 0.10), refs(f, "memorypressure", "diskpressure")...)
	s.contradict(refs(f, "node_ready")...)
	return s.finish(e.config)
}

func (e *HypothesisEngine) weight(hypothesis, key string, fallback float64) float64 {
	if e == nil {
		return fallback
	}
	if values, ok := e.config.Weights[hypothesis]; ok {
		if value, ok := values[key]; ok {
			return value
		}
	}
	return fallback
}

const (
	hypothesisPriorConfidence = 0.10
	requiredEvidenceCapGap    = 0.05
	confirmedEvidenceFloorGap = 0.05
)

// hypothesisScoreBuilder treats configured weights as relative evidence
// strength inside one hypothesis. This avoids comparing raw weight sums across
// hypotheses while preserving their domain-specific evidence priorities.
type hypothesisScoreBuilder struct {
	score           HypothesisScore
	matchedWeight   float64
	totalWeight     float64
	requiredMissing bool
}

func newHypothesisScore(kind, summary string) *hypothesisScoreBuilder {
	return &hypothesisScoreBuilder{
		score: HypothesisScore{Type: kind, Summary: summary},
	}
}

func (s *hypothesisScoreBuilder) required(weight float64, missing string, evidenceRefs ...string) {
	s.add(weight, evidenceRefs...)
	if len(evidenceRefs) == 0 {
		s.requiredMissing = true
		s.score.MissingEvidence = appendUnique(s.score.MissingEvidence, missing)
	}
}

func (s *hypothesisScoreBuilder) requiredContext(weight float64, missing string, evidenceRefs ...string) {
	if weight < 0 {
		weight = 0
	}
	s.totalWeight += weight
	if len(evidenceRefs) == 0 {
		s.requiredMissing = true
		s.score.MissingEvidence = appendUnique(s.score.MissingEvidence, missing)
		return
	}
	s.matchedWeight += weight
}

func (s *hypothesisScoreBuilder) optional(weight float64, evidenceRefs ...string) {
	s.add(weight, evidenceRefs...)
}

func (s *hypothesisScoreBuilder) add(weight float64, evidenceRefs ...string) {
	if weight < 0 {
		weight = 0
	}
	s.totalWeight += weight
	if len(evidenceRefs) == 0 {
		return
	}
	s.matchedWeight += weight
	s.score.SupportingRefs = appendUnique(s.score.SupportingRefs, evidenceRefs...)
}

func (s *hypothesisScoreBuilder) contradict(evidenceRefs ...string) {
	s.score.ContradictingRefs = appendUnique(s.score.ContradictingRefs, evidenceRefs...)
}

func (s *hypothesisScoreBuilder) finish(config HypothesisScoringConfig) HypothesisScore {
	confirmedThreshold := config.ConfirmedThreshold
	if confirmedThreshold <= 0 {
		confirmedThreshold = 0.75
	}
	rejectedThreshold := config.RejectedThreshold
	if rejectedThreshold <= 0 {
		rejectedThreshold = 0.20
	}

	confidence := hypothesisPriorConfidence
	if s.totalWeight > 0 && len(s.score.SupportingRefs) > 0 {
		evidenceRatio := clamp(s.matchedWeight / s.totalWeight)
		confidence = hypothesisPriorConfidence + (1-hypothesisPriorConfidence)*evidenceRatio
	}
	if s.requiredMissing {
		confidence = minFloat(confidence, confirmedThreshold-requiredEvidenceCapGap)
	} else if len(s.score.SupportingRefs) > 0 {
		confidence = maxFloat(confidence, confirmedThreshold+confirmedEvidenceFloorGap)
	}
	if len(s.score.ContradictingRefs) > 0 {
		confidence = minFloat(confidence, rejectedThreshold)
	}
	s.score.Confidence = clamp(confidence)
	return s.score
}

func refs(f facts, keys ...string) []string {
	result := []string{}
	for _, key := range keys {
		result = appendUnique(result, f.refsByKey[key]...)
	}
	return result
}

func addFactRef(f *facts, key, ref string) {
	if f == nil || strings.TrimSpace(key) == "" {
		return
	}
	f.refsByKey[key] = appendUnique(f.refsByKey[key], ref)
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

func positiveConditionSignal(text, key string) bool {
	compact := strings.NewReplacer(" ", "", "_", "", "-", "").Replace(strings.ToLower(text))
	key = strings.NewReplacer(" ", "", "_", "", "-", "").Replace(strings.ToLower(key))
	if strings.Contains(compact, key+"=false") || strings.Contains(compact, key+":false") || strings.Contains(compact, key+"false") {
		return false
	}
	return strings.Contains(compact, key+"=true") || strings.Contains(compact, key+":true") || strings.Contains(compact, key)
}

func oomFailureSignal(text string) bool {
	text = strings.ToLower(text)
	if strings.Contains(text, "reason=oomkilled") ||
		strings.Contains(text, "reason: oomkilled") ||
		strings.Contains(text, "oomkilled") ||
		strings.Contains(text, "out of memory") ||
		strings.Contains(text, "cannot allocate memory") {
		return true
	}
	if strings.Contains(text, "oom_score") || strings.Contains(text, "oom score") {
		return false
	}
	return containsStandaloneTerm(text, "oom")
}

func configErrorSignal(text string) bool {
	text = strings.ToLower(text)
	compact := strings.NewReplacer("_", "", "-", "", " ", "").Replace(text)
	if strings.Contains(compact, "containerimageconfiguration") ||
		strings.Contains(compact, "imagepull") ||
		strings.Contains(compact, "errimagepull") {
		return false
	}
	hasConfig := strings.Contains(text, "config") ||
		strings.Contains(text, "configuration") ||
		strings.Contains(text, "configmap")
	if !hasConfig {
		return false
	}
	for _, failure := range []string{
		"missing", "not found", "does not exist", "invalid", "malformed",
		"parse error", "failed to parse", "failed to load", "failed to read",
		"unknown field", "unmarshal", "required key", "permission denied",
	} {
		if strings.Contains(text, failure) {
			return true
		}
	}
	return strings.Contains(compact, "missingconfig") ||
		strings.Contains(compact, "invalidconfig") ||
		strings.Contains(compact, "configparseerror")
}

func missingSecretOrConfigMapSignal(text string) bool {
	text = strings.ToLower(text)
	if !strings.Contains(text, "secret") && !strings.Contains(text, "configmap") {
		return false
	}
	for _, failure := range []string{
		"missing", "not found", "does not exist", "failed", "forbidden",
		"required key", "key not found", "couldn't find key", "cannot find",
	} {
		if strings.Contains(text, failure) {
			return true
		}
	}
	return false
}

func prometheusMemorySignal(source, text string) bool {
	if source != "prometheus" && source != "prometheus_trend" {
		return false
	}
	if unavailableObservation(text) {
		return false
	}
	return strings.Contains(text, "memory") &&
		(strings.Contains(text, "working set") ||
			strings.Contains(text, "working_set") ||
			strings.Contains(text, "progressive_growth") ||
			strings.Contains(text, "sudden_spike") ||
			strings.Contains(text, "near limit"))
}

func workingSetNearLimitSignal(source, text string) bool {
	if !prometheusMemorySignal(source, text) {
		return false
	}
	return strings.Contains(text, "near limit") ||
		strings.Contains(text, "limit ratio") ||
		strings.Contains(text, "working set limit") ||
		strings.Contains(text, "working_set_limit")
}

func memoryGrowthSignal(source, text string) bool {
	if !prometheusMemorySignal(source, text) {
		return false
	}
	return strings.Contains(text, "progressive_growth") ||
		strings.Contains(text, "sustained growth") ||
		strings.Contains(text, "steadily increasing")
}

func pvcUnboundSignal(source, text string) bool {
	if source != "k8s_pvc" {
		return false
	}
	if strings.Contains(text, "phase=bound") || strings.Contains(text, "phase: bound") {
		return false
	}
	return strings.Contains(text, "unbound") ||
		strings.Contains(text, "not bound") ||
		strings.Contains(text, "phase=pending") ||
		strings.Contains(text, "phase: pending")
}

func probeFailureSignal(source, text string) bool {
	text = strings.ToLower(text)
	if strings.Contains(text, "probe failed") ||
		strings.Contains(text, "readiness probe failure") ||
		strings.Contains(text, "liveness probe failure") ||
		strings.Contains(text, "startup probe failure") {
		return true
	}
	return source == "k8s_event" &&
		strings.Contains(text, "unhealthy") &&
		strings.Contains(text, "probe")
}

func probeSpecSignal(source, text string) bool {
	if source != "k8s_pod" && source != "k8s_pod_status" {
		return false
	}
	compact := strings.NewReplacer("_", "", "-", "", " ", "").Replace(strings.ToLower(text))
	hasProbe := strings.Contains(compact, "readinessprobe") ||
		strings.Contains(compact, "livenessprobe") ||
		strings.Contains(compact, "startupprobe")
	if !hasProbe {
		return false
	}
	return strings.Contains(compact, "path=") ||
		strings.Contains(compact, "port=") ||
		strings.Contains(compact, "timeoutseconds") ||
		strings.Contains(compact, "initialdelayseconds") ||
		strings.Contains(compact, "periodseconds") ||
		strings.Contains(compact, "failurethreshold")
}

func unavailableObservation(text string) bool {
	for _, marker := range []string{
		"unavailable", "skipped", "failed", "empty", "not configured",
		"no samples", "no data",
	} {
		if strings.Contains(text, marker) {
			return true
		}
	}
	return false
}

func containsStandaloneTerm(text, term string) bool {
	for _, token := range strings.FieldsFunc(strings.ToLower(text), func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsNumber(r) && r != '_'
	}) {
		if token == term {
			return true
		}
	}
	return false
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

func minFloat(a, b float64) float64 {
	if a < b {
		return a
	}
	return b
}

func maxFloat(a, b float64) float64 {
	if a > b {
		return a
	}
	return b
}

// blendScores applies a bounded LLM penalty to evidence-derived scores.
// The LLM cannot raise confidence, add supporting refs, or replace the
// evidence-grounded summary.
func blendScores(evidence []HypothesisScore, llm []HypothesisScore, evidenceWeight float64) []HypothesisScore {
	llmByType := map[string]HypothesisScore{}
	for _, s := range llm {
		llmByType[s.Type] = s
	}
	if evidenceWeight < 0.8 || evidenceWeight > 1 {
		evidenceWeight = 0.85
	}
	llmWeight := 1.0 - evidenceWeight
	for i := range evidence {
		if llmScore, ok := llmByType[evidence[i].Type]; ok {
			if len(evidence[i].SupportingRefs) == 0 {
				continue
			}
			adjusted := evidence[i].Confidence*evidenceWeight + clamp(llmScore.Confidence)*llmWeight
			if adjusted < evidence[i].Confidence {
				evidence[i].Confidence = adjusted
			}
		}
	}
	return evidence
}
