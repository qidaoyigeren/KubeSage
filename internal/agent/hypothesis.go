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
			"application_memory_leak":     {"oom": 0.20, "memory": 0.30},
			"node_memory_pressure":        {"memorypressure": 0.35, "k8s_topology": 0.10},
			"bad_config":                  {"config": 0.30, "backoff": 0.20},
			"missing_secret_or_configmap": {"secret_configmap": 0.15, "object_missing": 0.45, "key_missing": 0.40},
			"dependency_unavailable":      {"refused_timeout": 0.25, "backoff_probe": 0.10},
			"probe_misconfigured":         {"probe_unhealthy": 0.45},
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
	return e.UpdateContext(context.Background(), taskID, ctx, records)
}

func (e *HypothesisEngine) UpdateContext(callContext context.Context, taskID uint, ctx *diagnostic.DiagnosticContext, records []diagnostic.EvidenceRecord) []HypothesisScore {
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
		if len(candidates[i].SupportingRefs) == 0 {
			candidates[i].Confidence = 0.15
		}
		candidates[i].Confidence = clamp(candidates[i].Confidence)
	}

	// LLM-assisted re-ranking: blend keyword scores with LLM scores.
	if e.llmScorer != nil {
		if llmRanked, err := e.llmScorer.ScoreHypotheses(callContext, candidates); err == nil {
			candidates = blendScores(candidates, llmRanked, 0.6)
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
		if negativeDiagnosticEvidence(record) {
			continue
		}
		ref := evidenceRef(record, i)
		text := strings.ToLower(record.SourceType + " " + record.Title + " " + record.Content)
		f.text += "\n" + text
		f.sourceRefs[record.SourceType] = append(f.sourceRefs[record.SourceType], ref)
		for _, key := range []string{
			"oom", "oomkilled", "memory", "working set", "limit", "limitratio", "sustainednearlimit", "progressive_growth", "backoff", "crashloop", "config", "secret", "configmap",
			"refused", "timeout", "unhealthy", "probe", "failedscheduling", "taint", "selector", "pvc", "bound",
			"imagepull", "errimagepull", "imagepullbackoff", "unauthorized", "denied", "manifest",
			"initerror", "initcontainer", "init container", "exitcode", "evicted", "eviction", "nodenotready", "notready",
		} {
			if key == "memory" && pressureFalseOnly(text, "memorypressure") {
				continue
			}
			if key == "config" && !configErrorSignal(text) {
				continue
			}
			if strings.Contains(text, key) {
				f.refsByKey[key] = append(f.refsByKey[key], ref)
			}
		}
		if strings.Contains(text, "memorypattern=memory_limit_too_low") {
			f.refsByKey["memory_limit_too_low_pattern"] = append(f.refsByKey["memory_limit_too_low_pattern"], ref)
		}
		if strings.Contains(text, "memorypattern=application_memory_leak") {
			f.refsByKey["application_memory_leak_pattern"] = append(f.refsByKey["application_memory_leak_pattern"], ref)
		}
		if strings.Contains(text, "objectinspected=true") && strings.Contains(text, "objectexists=false") {
			f.refsByKey["config_object_missing"] = append(f.refsByKey["config_object_missing"], ref)
		}
		if strings.Contains(text, "keyexists=false") || strings.Contains(text, "contentstatus=key_missing") {
			f.refsByKey["config_key_missing"] = append(f.refsByKey["config_key_missing"], ref)
		}
		if strings.Contains(text, "contentstatus=empty") {
			f.refsByKey["config_content_empty"] = append(f.refsByKey["config_content_empty"], ref)
		}
		for _, key := range []string{"memorypressure", "diskpressure", "pidpressure"} {
			if positiveConditionSignal(text, key) {
				f.refsByKey[key] = append(f.refsByKey[key], ref)
			}
		}
	}
	if ctx != nil && ctx.Topology != nil && ctx.Topology.Node != nil {
		node := ctx.Topology.Node
		if node.MemoryPressure {
			f.text += "\nnode memorypressure"
			f.refsByKey["memorypressure"] = append(f.refsByKey["memorypressure"], "topology:node_memory_pressure")
		}
		if node.DiskPressure {
			f.text += "\nnode diskpressure"
			f.refsByKey["diskpressure"] = append(f.refsByKey["diskpressure"], "topology:node_disk_pressure")
		}
		if node.PIDPressure {
			f.text += "\nnode pidpressure"
			f.refsByKey["pidpressure"] = append(f.refsByKey["pidpressure"], "topology:node_pid_pressure")
		}
		if !node.Ready {
			f.text += "\nnode notready"
			f.refsByKey["notready"] = append(f.refsByKey["notready"], "topology:node_not_ready")
			f.refsByKey["nodenotready"] = append(f.refsByKey["nodenotready"], "topology:node_not_ready")
		}
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

func negativeDiagnosticEvidence(record diagnostic.EvidenceRecord) bool {
	return strings.EqualFold(strings.TrimSpace(record.SourceType), "diagnostic_engine") &&
		strings.EqualFold(strings.TrimSpace(record.Title), "No analyzer matched")
}

func (e *HypothesisEngine) scoreMemoryLimitTooLow(f facts) HypothesisScore {
	s := base("memory_limit_too_low", "Container memory limit may be too low for observed workload.")
	s.add(e.weight(s.Type, "oom", 0.25), refs(f, "oom", "oomkilled")...)
	patternRefs := refs(f, "memory_limit_too_low_pattern")
	s.add(e.weight(s.Type, "working_set_limit", 0.20), patternRefs...)
	s.add(e.weight(s.Type, "prometheus", 0.10), f.sourceRefs["prometheus"]...)
	s.add(0.05, refs(f, "working set", "limit", "limitratio", "sustainednearlimit")...)
	if !contains(f.text, "prometheus") {
		s.MissingEvidence = append(s.MissingEvidence, "memory metrics")
	} else if len(patternRefs) == 0 {
		s.MissingEvidence = append(s.MissingEvidence, "memory curve shape: high baseline, low pre-OOM slope, and post-restart return")
	}
	return s
}

func (e *HypothesisEngine) scoreApplicationMemoryLeak(f facts) HypothesisScore {
	s := base("application_memory_leak", "Application memory may grow until the container is killed.")
	s.add(e.weight(s.Type, "oom", 0.20), refs(f, "oom", "oomkilled")...)
	growthRefs := refs(f, "application_memory_leak_pattern", "progressive_growth")
	s.add(e.weight(s.Type, "memory", 0.30), growthRefs...)
	if len(growthRefs) == 0 {
		s.MissingEvidence = append(s.MissingEvidence, "heap/profile or sustained memory growth evidence")
	}
	return s
}

func (e *HypothesisEngine) scoreNodeMemoryPressure(f facts) HypothesisScore {
	s := base("node_memory_pressure", "Node-level memory pressure may contribute to pod instability.")
	s.add(e.weight(s.Type, "memorypressure", 0.35), refs(f, "memorypressure")...)
	if len(s.SupportingRefs) == 0 {
		s.MissingEvidence = append(s.MissingEvidence, "node pressure observation")
	}
	return s
}

func (e *HypothesisEngine) scoreBadConfig(f facts) HypothesisScore {
	s := base("bad_config", "Application startup may fail because of invalid configuration.")
	s.add(e.weight(s.Type, "config", 0.25), refs(f, "config", "configmap")...)
	s.add(e.weight(s.Type, "backoff", 0.15), refs(f, "backoff", "crashloop")...)
	s.add(0.30, refs(f, "config_content_empty")...)
	if len(s.SupportingRefs) == 0 {
		s.MissingEvidence = append(s.MissingEvidence, "logs or events mentioning configuration errors")
	}
	return s
}

func (e *HypothesisEngine) scoreMissingSecretOrConfigMap(f facts) HypothesisScore {
	s := base("missing_secret_or_configmap", "A referenced Secret or ConfigMap may be missing or incomplete.")
	s.add(e.weight(s.Type, "secret_configmap", 0.15), refs(f, "secret", "configmap")...)
	missingRefs := refs(f, "config_object_missing")
	keyRefs := refs(f, "config_key_missing")
	s.add(e.weight(s.Type, "object_missing", 0.45), missingRefs...)
	s.add(e.weight(s.Type, "key_missing", 0.40), keyRefs...)
	if len(missingRefs) == 0 && len(keyRefs) == 0 {
		s.MissingEvidence = append(s.MissingEvidence, "direct ConfigMap/Secret object and key inspection")
	}
	return s
}

func (e *HypothesisEngine) scoreDependencyUnavailable(f facts) HypothesisScore {
	s := base("dependency_unavailable", "A downstream dependency may be unavailable during startup or health checks.")
	s.add(e.weight(s.Type, "refused_timeout", 0.25), refs(f, "refused", "timeout")...)
	s.add(e.weight(s.Type, "backoff_probe", 0.10), refs(f, "backoff", "probe")...)
	if len(s.SupportingRefs) == 0 {
		s.MissingEvidence = append(s.MissingEvidence, "dependency timeout/refused log evidence")
	}
	return s
}

func (e *HypothesisEngine) scoreProbeMisconfigured(f facts) HypothesisScore {
	s := base("probe_misconfigured", "Readiness or liveness probe settings may not match the application.")
	s.add(e.weight(s.Type, "probe_unhealthy", 0.30), refs(f, "probe", "unhealthy")...)
	if len(s.SupportingRefs) == 0 {
		s.MissingEvidence = append(s.MissingEvidence, "probe events and probe spec")
	}
	return s
}

func (e *HypothesisEngine) scorePVCUnbound(f facts) HypothesisScore {
	s := base("pvc_unbound", "A PersistentVolumeClaim may be unbound and blocking scheduling/startup.")
	s.add(e.weight(s.Type, "pvc_bound", 0.35), refs(f, "pvc", "bound")...)
	if len(s.SupportingRefs) == 0 {
		s.MissingEvidence = append(s.MissingEvidence, "pvc status")
	}
	return s
}

func (e *HypothesisEngine) scoreSchedulingConstraint(f facts) HypothesisScore {
	s := base("scheduling_constraint", "Scheduling constraints may prevent the pod from running.")
	s.add(e.weight(s.Type, "scheduler", 0.30), refs(f, "failedscheduling", "taint", "selector")...)
	if len(s.SupportingRefs) == 0 {
		s.MissingEvidence = append(s.MissingEvidence, "scheduler events")
	}
	return s
}

func (e *HypothesisEngine) scoreImagePullFailed(f facts) HypothesisScore {
	s := base("image_pull_failed", "Container image cannot be pulled due to wrong name, missing tag, or registry authentication failure.")
	s.add(e.weight(s.Type, "imagepull", 0.35), refs(f, "imagepull", "errimagepull", "imagepullbackoff")...)
	s.add(e.weight(s.Type, "registry_auth", 0.15), refs(f, "unauthorized", "denied", "manifest")...)
	if len(s.SupportingRefs) == 0 {
		s.MissingEvidence = append(s.MissingEvidence, "image pull events")
	}
	return s
}

func (e *HypothesisEngine) scoreInitContainerCrash(f facts) HypothesisScore {
	s := base("init_container_crash", "An init container is failing, possibly due to dependency issues or misconfiguration.")
	initRefs := refs(f, "initerror", "initcontainer", "init container")
	s.add(e.weight(s.Type, "init_error", 0.30), initRefs...)
	if len(initRefs) > 0 {
		s.add(e.weight(s.Type, "init_exit", 0.15), refs(f, "exitcode")...)
	}
	if len(s.SupportingRefs) == 0 {
		s.MissingEvidence = append(s.MissingEvidence, "init container status and logs")
	}
	return s
}

func (e *HypothesisEngine) scoreNodeEviction(f facts) HypothesisScore {
	s := base("node_eviction", "Pod was evicted due to node resource pressure (memory, disk, or PID).")
	s.add(e.weight(s.Type, "evicted", 0.35), refs(f, "evicted", "eviction")...)
	s.add(e.weight(s.Type, "node_pressure", 0.15), refs(f, "memorypressure", "diskpressure", "pidpressure")...)
	if len(s.SupportingRefs) == 0 {
		s.MissingEvidence = append(s.MissingEvidence, "eviction events and node pressure conditions")
	}
	return s
}

func (e *HypothesisEngine) scoreNodeNotReady(f facts) HypothesisScore {
	s := base("node_not_ready", "The node hosting this pod is in NotReady state, causing pod instability.")
	s.add(e.weight(s.Type, "nodenotready", 0.40), refs(f, "nodenotready", "notready")...)
	s.add(e.weight(s.Type, "node_pressure", 0.10), refs(f, "memorypressure", "diskpressure")...)
	if len(s.SupportingRefs) == 0 {
		s.MissingEvidence = append(s.MissingEvidence, "node Ready condition and pressure events")
	}
	return s
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

func pressureFalseOnly(text, key string) bool {
	compact := strings.NewReplacer(" ", "", "_", "", "-", "").Replace(strings.ToLower(text))
	key = strings.NewReplacer(" ", "", "_", "", "-", "").Replace(strings.ToLower(key))
	return strings.Contains(compact, key+"=false") &&
		!strings.Contains(compact, "memorylimit") &&
		!strings.Contains(compact, "memoryrequest") &&
		!strings.Contains(compact, "workingset") &&
		!strings.Contains(compact, "oom")
}

func configErrorSignal(text string) bool {
	compact := strings.NewReplacer("_", "", "-", "", " ", "").Replace(strings.ToLower(text))
	if strings.Contains(compact, "containerimageconfiguration") ||
		strings.Contains(compact, "imagepull") ||
		strings.Contains(compact, "errimagepull") {
		return false
	}
	if strings.Contains(compact, "k8sconfigref") &&
		!strings.Contains(compact, "objectinspected=trueobjectexists=false") &&
		!strings.Contains(compact, "keyexists=false") &&
		!strings.Contains(compact, "contentstatus=empty") {
		return false
	}
	for _, needle := range []string{
		"config",
		"configuration",
		"configmap",
		"config.yaml",
		"missingconfig",
		"invalidconfig",
		"parseerror",
	} {
		if strings.Contains(text, needle) || strings.Contains(compact, needle) {
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

// blendScores merges keyword-based and LLM-based hypothesis scores using a
// weighted blend. keywordWeight is the weight for keyword scores (e.g. 0.6),
// and (1 - keywordWeight) is used for LLM scores.
func blendScores(keyword []HypothesisScore, llm []HypothesisScore, keywordWeight float64) []HypothesisScore {
	llmByType := map[string]HypothesisScore{}
	for _, s := range llm {
		llmByType[s.Type] = s
	}
	llmWeight := 1.0 - keywordWeight
	for i := range keyword {
		if llmScore, ok := llmByType[keyword[i].Type]; ok {
			if len(keyword[i].SupportingRefs) == 0 && len(llmScore.SupportingRefs) == 0 {
				continue
			}
			keyword[i].Confidence = keyword[i].Confidence*keywordWeight + llmScore.Confidence*llmWeight
			// Merge LLM supporting refs that are not already present.
			keyword[i].SupportingRefs = appendUnique(keyword[i].SupportingRefs, llmScore.SupportingRefs...)
			if llmScore.Summary != "" {
				keyword[i].Summary = keyword[i].Summary + " [LLM: " + llmScore.Summary + "]"
			}
		}
	}
	return keyword
}
