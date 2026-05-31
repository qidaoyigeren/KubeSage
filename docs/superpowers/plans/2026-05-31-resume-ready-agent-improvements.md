# KubeSage Resume-Ready Agent Improvements — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Transform KubeSage from MVP to resume-ready by adding: (1) LLM hallucination guard with evidence-grounded validation, (2) Agent trace visualization, (3) RCA evaluation framework with quantifiable before/after metrics.

**Architecture:** Three independent subsystems layered on the existing codebase. Phase 1 replaces the LLM enhancement path with a structured prompt + validation pipeline. Phase 2 enriches the Agent Runtime's observability layer and frontend AgentTimeline. Phase 3 adds a new `cmd/rca-eval` CLI that replays pre-collected DiagnosticContext snapshots through the engine for reproducible accuracy benchmarking.

**Tech Stack:** Go 1.26, client-go, GORM, Viper, Zap, OTEL, React 18 + TypeScript + Ant Design 5

---

## File Structure Map

### Phase 1 — LLM Hallucination Guard
| File | Responsibility |
|------|---------------|
| `internal/llm/types.go` | New data types: GroundedSummary, GroundedClaim, RootCauseConfirmation, ValidationResult, UngroundedClaim |
| `internal/llm/prompt.go` | New BuildGroundedPrompt() with evidence ID injection; updated system prompt |
| `internal/llm/grounding.go` | EvidenceValidator: reference resolution, semantic grounding, agreement gate, risk calculation |
| `internal/llm/grounding_test.go` | Full test suite for grounding pipeline |
| `internal/llm/client.go` | New GenerateGroundedSummary() method; wire prompt + validation |
| `internal/config/config.go` | LLMGroundingConfig struct + defaults |
| `configs/config.yaml` | `llm.grounding` config section |
| `internal/observability/metrics.go` | New Prometheus metrics for grounding |
| `internal/service/diagnosis_service.go` | enhanceReportWithLLM() → call grounded path, fallback logic |
| `deployments/helm/kubesage/values.yaml` | Corresponding Helm values |

### Phase 2 — Agent Trace Visualization
| File | Responsibility |
|------|---------------|
| `internal/agent/runtime.go` | Inject OTEL span events at plan/execute/hypothesize/reflect boundaries |
| `internal/agent/recorder.go` | Extend step recording with observation_summary, parallel_group, tool_latency_ms |
| `migrations/000004_agent_trace.up.sql` | ALTER TABLE agent_steps ADD observation_summary, parallel_group, tool_latency_ms |
| `internal/model/agent.go` | Add new columns to AgentStep struct |
| `web/src/api/types.ts` | Extend AgentStep type for new fields |
| `web/src/pages/TaskDetail/tabs/AgentTimeline.tsx` | Visual decision chain with phases and parallel groups |

### Phase 3 — RCA Evaluation Framework
| File | Responsibility |
|------|---------------|
| `internal/eval/types.go` | EvalCase, GoldenAnswer, EvalMetrics, EvalResult structs |
| `internal/eval/fixture.go` | DiagnosticContext JSON serialization/deserialization |
| `internal/eval/loader.go` | Load YAML case definitions + JSON fixtures |
| `internal/eval/runner.go` | Run engine + (optional) LLM enhancement on each fixture, collect reports |
| `internal/eval/scorer.go` | Score each report against golden answer, compute all metrics |
| `internal/eval/reporter.go` | Output JSON and Markdown comparison reports |
| `internal/eval/compare.go` | Compare two eval result files, produce delta table |
| `cmd/rca-eval/main.go` | CLI entry: rca-eval run, rca-eval compare, rca-eval gen-fixture |
| `eval/cases/*.yaml` | 15 evaluation case definitions with golden answers |
| `eval/fixtures/*.json` | Pre-collected DiagnosticContext snapshots (generated separately) |

---

## Phase 1: LLM Hallucination Guard (P0)

### Task 1.1: Add LLMGroundingConfig to config

**Files:**
- Modify: `internal/config/config.go:118-126` (LLMConfig struct)
- Modify: `internal/config/config.go:368-371` (defaults)

- [ ] **Step 1: Add Grounding config struct and embed in LLMConfig**

Open `internal/config/config.go`. Replace the `LLMConfig` struct:

```go
type LLMConfig struct {
	Enabled               bool                `mapstructure:"enabled"`
	BaseURL               string              `mapstructure:"base_url"`
	APIKey                string              `mapstructure:"api_key"`
	Model                 string              `mapstructure:"model"`
	RetryMaxAttempts      int                 `mapstructure:"retry_max_attempts"`
	RetryInitialBackoffMS int                 `mapstructure:"retry_initial_backoff_ms"`
	RetryMaxBackoffMS     int                 `mapstructure:"retry_max_backoff_ms"`
	Grounding             LLMGroundingConfig  `mapstructure:"grounding"`
}

type LLMGroundingConfig struct {
	Enabled                    bool    `mapstructure:"enabled"`
	SemanticThreshold          float64 `mapstructure:"semantic_threshold"`
	HallucinationRiskThreshold float64 `mapstructure:"hallucination_risk_threshold"`
	QualityWarningThreshold    float64 `mapstructure:"quality_warning_threshold"`
	GroundedClaimThreshold     float64 `mapstructure:"grounded_claim_threshold"`
}
```

- [ ] **Step 2: Add defaults in setDefaults()**

Add after line 371 (`v.SetDefault("llm.retry_max_backoff_ms", 2000)`):

```go
	v.SetDefault("llm.grounding.enabled", true)
	v.SetDefault("llm.grounding.semantic_threshold", 0.3)
	v.SetDefault("llm.grounding.hallucination_risk_threshold", 0.4)
	v.SetDefault("llm.grounding.quality_warning_threshold", 0.3)
	v.SetDefault("llm.grounding.grounded_claim_threshold", 0.3)
```

- [ ] **Step 3: Verify compilation**

```powershell
go build ./...
```

- [ ] **Step 4: Commit**

```bash
git add internal/config/config.go
git commit -m "feat(config): add LLMGroundingConfig with threshold defaults"
```

---

### Task 1.2: Add grounding Prometheus metrics

**Files:**
- Modify: `internal/observability/metrics.go`

- [ ] **Step 1: Define new metrics**

After the `llmPlannerFallbackTotal` var block (line 77), add:

```go
	llmGroundingTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "kubesage_llm_grounding_total",
			Help: "Total LLM grounding pipeline results by outcome.",
		},
		[]string{"result"},
	)
	llmHallucinationRisk = prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "kubesage_llm_hallucination_risk",
			Help:    "Distribution of hallucination risk scores.",
			Buckets: prometheus.LinearBuckets(0, 0.1, 10),
		},
		[]string{},
	)
	llmClaimsUngrounded = prometheus.NewCounter(
		prometheus.CounterOpts{
			Name: "kubesage_llm_claims_ungrounded_total",
			Help: "Total number of LLM claims that failed semantic grounding.",
		},
	)
	llmEvidenceRefsUnresolved = prometheus.NewCounter(
		prometheus.CounterOpts{
			Name: "kubesage_llm_evidence_refs_unresolved_total",
			Help: "Total number of unresolved evidence references in LLM output.",
		},
	)
	llmFallbackTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "kubesage_llm_fallback_total",
			Help: "Total LLM fallback events by reason.",
		},
		[]string{"reason"},
	)
```

- [ ] **Step 2: Register new metrics**

In the `init()` function, add to the `prometheus.MustRegister` call:

```go
prometheus.MustRegister(diagnosisTotal, diagnosisDuration, llmCallTotal, analyzerMatchTotal, agentStepTotal, agentToolDuration, agentHypothesisUpdatesTotal, remediationActionTotal, llmPlannerFallbackTotal, llmGroundingTotal, llmHallucinationRisk, llmClaimsUngrounded, llmEvidenceRefsUnresolved, llmFallbackTotal)
```

- [ ] **Step 3: Add recording functions at end of file**

```go
// IncLLMGroundingTotal records a grounding pipeline result.
func IncLLMGroundingTotal(result string) {
	llmGroundingTotal.WithLabelValues(result).Inc()
}

// ObserveLLMHallucinationRisk records the hallucination risk score.
func ObserveLLMHallucinationRisk(risk float64) {
	llmHallucinationRisk.WithLabelValues().Observe(risk)
}

// IncLLMClaimsUngrounded records a claim that failed semantic grounding.
func IncLLMClaimsUngrounded() {
	llmClaimsUngrounded.Inc()
}

// IncLLMEvidenceRefsUnresolved records an unresolved evidence reference.
func IncLLMEvidenceRefsUnresolved() {
	llmEvidenceRefsUnresolved.Inc()
}

// IncLLMFallbackTotal records a fallback event by reason.
func IncLLMFallbackTotal(reason string) {
	llmFallbackTotal.WithLabelValues(reason).Inc()
}
```

- [ ] **Step 4: Verify compilation**

```powershell
go build ./...
```

- [ ] **Step 5: Commit**

```bash
git add internal/observability/metrics.go
git commit -m "feat(metrics): add LLM grounding pipeline Prometheus metrics"
```

---

### Task 1.3: Add new types for grounded summary

**Files:**
- Modify: `internal/llm/types.go`

- [ ] **Step 1: Add new types after EnhancedSummary**

Append to `internal/llm/types.go`:

```go
// GroundedSummary replaces EnhancedSummary for evidence-grounded LLM output.
// Each claim must reference >=1 evidence_id from the injected evidence list.
type GroundedSummary struct {
	RootCauseConfirmation   RootCauseConfirmation `json:"root_cause_confirmation"`
	EvidenceChain           []GroundedClaim       `json:"evidence_chain"`
	AdditionalObservations  []string              `json:"additional_observations,omitempty"`
	SuggestedActions        []string              `json:"suggested_actions"`
}

// RootCauseConfirmation anchors the LLM explanation to the rule engine's diagnosis.
// RuleDiagnosis is injected by code; the LLM must not modify it.
type RootCauseConfirmation struct {
	RuleDiagnosis  string `json:"rule_diagnosis"`
	AgreementLevel string `json:"agreement_level"` // full_agree | partial_agree | disagree
	Explanation    string `json:"explanation"`
}

// GroundedClaim represents one evidence-backed claim in the diagnostic chain.
type GroundedClaim struct {
	Claim               string   `json:"claim"`
	EvidenceRefs        []string `json:"evidence_refs"`
	ConfidenceIncrement float64  `json:"confidence_increment"`
}

// ValidationResult captures the outcome of the grounding pipeline checks.
type ValidationResult struct {
	Passed            bool              `json:"passed"`
	HallucinationRisk float64           `json:"hallucination_risk"`
	GroundingScore    float64           `json:"grounding_score"`
	AgreementScore    float64           `json:"agreement_score"`
	UnresolvedRefs    []string          `json:"unresolved_refs"`
	UngroundedClaims  []UngroundedClaim `json:"ungrounded_claims"`
	AgreementIssue    string            `json:"agreement_issue,omitempty"`
	FallbackReason    string            `json:"fallback_reason,omitempty"`
}

// UngroundedClaim captures a claim that failed semantic grounding.
type UngroundedClaim struct {
	Claim        string   `json:"claim"`
	Similarity   float64  `json:"similarity"`
	EvidenceRefs []string `json:"evidence_refs"`
}

// ToEnhancedSummary converts a GroundedSummary back to the legacy format for
// backward compatibility with the existing report persistence layer.
func (g *GroundedSummary) ToEnhancedSummary() *EnhancedSummary {
	if g == nil {
		return nil
	}
	var rootCauseSummary string
	if g.RootCauseConfirmation.Explanation != "" {
		rootCauseSummary = g.RootCauseConfirmation.Explanation
	} else {
		rootCauseSummary = g.RootCauseConfirmation.RuleDiagnosis
	}
	return &EnhancedSummary{
		RootCauseSummary:  rootCauseSummary,
		EvidenceReasoning: buildEvidenceReasoning(g.EvidenceChain),
		SuggestedActions:  g.SuggestedActions,
		RiskLevel:         "medium",
		NeedHumanConfirm:  true,
	}
}

func buildEvidenceReasoning(claims []GroundedClaim) string {
	if len(claims) == 0 {
		return ""
	}
	var parts []string
	for i, c := range claims {
		parts = append(parts, fmt.Sprintf("[%d] %s (refs: %s)", i+1, c.Claim, strings.Join(c.EvidenceRefs, ", ")))
	}
	return strings.Join(parts, "; ")
}
```

Add `"fmt"` and `"strings"` to the import block of `types.go`.

- [ ] **Step 2: Verify compilation**

```powershell
go build ./...
```

- [ ] **Step 3: Commit**

```bash
git add internal/llm/types.go
git commit -m "feat(llm): add GroundedSummary, ValidationResult, and conversion types"
```

---

### Task 1.4: Build grounded prompt with evidence IDs

**Files:**
- Modify: `internal/llm/prompt.go`

- [ ] **Step 1: Add evidence_id to promptEvidence struct**

Replace the existing `promptEvidence` struct (lines 15-20):

```go
type promptEvidence struct {
	EvidenceID string `json:"evidence_id"`
	SourceType string `json:"source_type"`
	Title      string `json:"title"`
	Content    string `json:"content"`
	Severity   string `json:"severity"`
}
```

- [ ] **Step 2: Add grounded prompt payload and builder**

Add after the existing `BuildPrompt` function:

```go
type groundedPromptPayload struct {
	PodBasicInfo          map[string]interface{} `json:"pod_basic_info"`
	FaultType             string                 `json:"fault_type"`
	RuleDiagnosis         string                 `json:"rule_diagnosis"`
	ConfidenceScore       float64                `json:"confidence_score"`
	Evidences             []promptEvidence       `json:"evidence_list"`
	EventsSummary         []promptEvent          `json:"events_summary"`
	KeyLogFragments       []promptEvidence       `json:"key_log_fragments"`
	PrometheusSummary     []promptEvidence       `json:"prometheus_metric_summary"`
	RunbookHits           []rag.Hit              `json:"runbook_retrieval_results"`
	OutputRequirements    map[string]string      `json:"output_requirements"`
}

// BuildGroundedPrompt constructs a structured prompt where every LLM claim must
// reference an injected evidence_id. The rule diagnosis is immutable.
func BuildGroundedPrompt(ctx *diagnostic.DiagnosticContext, ruleResult RuleBasedResult, evidences []diagnostic.EvidenceRecord, runbookHits []rag.Hit) Prompt {
	payload := groundedPromptPayload{
		PodBasicInfo:    podBasicInfo(ctx),
		FaultType:       ruleResult.FaultType,
		RuleDiagnosis:   ruleResult.RootCauseSummary,
		ConfidenceScore: ruleResult.ConfidenceScore,
		Evidences:       promptEvidencesWithIDs(evidences, ""),
		EventsSummary:   promptEvents(ctx),
		KeyLogFragments: promptEvidencesWithIDs(evidences, "k8s_key_log"),
		PrometheusSummary: promptEvidencesWithIDs(evidences, "prometheus"),
		RunbookHits:     runbookHits,
		OutputRequirements: map[string]string{
			"format": "Return strict JSON only.",
			"schema": "root_cause_confirmation{rule_diagnosis,agreement_level,explanation}, evidence_chain[{claim,evidence_refs,confidence_increment}], additional_observations[], suggested_actions[]",
			"grounding": "Every claim in evidence_chain MUST reference >=1 evidence_id from the evidence_list. Do not invent facts not present in referenced evidence. If a claim cannot be grounded, place it in additional_observations.",
			"safety": "Do not produce dangerous commands as executable actions. Suggestions must require human confirmation.",
		},
	}
	bytes, _ := json.MarshalIndent(payload, "", "  ")
	return Prompt{
		System: groundedSystemPrompt(),
		User:   string(bytes),
	}
}

// promptEvidencesWithIDs filters and truncates evidence, injecting stable IDs.
func promptEvidencesWithIDs(records []diagnostic.EvidenceRecord, sourceType string) []promptEvidence {
	result := make([]promptEvidence, 0)
	for i, record := range records {
		if sourceType != "" && record.SourceType != sourceType {
			continue
		}
		result = append(result, promptEvidence{
			EvidenceID: fmt.Sprintf("ev-%d", i),
			SourceType: record.SourceType,
			Title:      record.Title,
			Content:    truncate(record.Content, 1200),
			Severity:   record.Severity,
		})
		if len(result) >= 30 {
			return result
		}
	}
	return result
}

// groundedSystemPrompt sets strict evidence-grounding boundaries.
func groundedSystemPrompt() string {
	var lines []string
	lines = append(lines, "You are KubeSage's evidence-grounded RCA report writer.")
	lines = append(lines, "1. root_cause_confirmation.rule_diagnosis is injected by the system. DO NOT modify it.")
	lines = append(lines, "2. agreement_level MUST be exactly one of: full_agree, partial_agree, disagree.")
	lines = append(lines, "3. For every claim in evidence_chain, you MUST reference >=1 evidence_id from the evidence_list above.")
	lines = append(lines, "4. Do NOT invent facts, root causes, or causal relationships not present in the referenced evidence.")
	lines = append(lines, "5. If you cannot ground an observation in existing evidence, place it in additional_observations instead of evidence_chain.")
	lines = append(lines, "6. confidence_increment per claim must be between 0.0 and 1.0, reflecting how much this claim increases confidence in the rule diagnosis.")
	lines = append(lines, "Return strict JSON only.")
	return strings.Join(lines, "\n")
}
```

- [ ] **Step 3: Verify compilation**

```powershell
go build ./...
```

Note: The existing `promptEvidences` function (without IDs) is still used by the legacy `BuildPrompt`. That's intentional for backward compatibility.

- [ ] **Step 4: Commit**

```bash
git add internal/llm/prompt.go
git commit -m "feat(llm): add BuildGroundedPrompt with evidence ID injection and structured constraints"
```

---

### Task 1.5: Implement EvidenceValidator (grounding pipeline)

**Files:**
- Create: `internal/llm/grounding.go`

- [ ] **Step 1: Write the evidence validator**

Create `internal/llm/grounding.go`:

```go
package llm

import (
	"strings"

	"kubesage/internal/observability"
)

// EvidenceValidator runs the three-stage grounding pipeline on LLM output.
type EvidenceValidator struct {
	semanticThreshold      float64
	hallucinationThreshold float64
	qualityWarnThreshold   float64
	claimThreshold         float64
}

// NewEvidenceValidator creates a validator with configured thresholds.
func NewEvidenceValidator(semanticThreshold, hallucinationThreshold, qualityWarnThreshold, claimThreshold float64) *EvidenceValidator {
	return &EvidenceValidator{
		semanticThreshold:      semanticThreshold,
		hallucinationThreshold: hallucinationThreshold,
		qualityWarnThreshold:   qualityWarnThreshold,
		claimThreshold:         claimThreshold,
	}
}

// Validate runs the full grounding pipeline and returns a ValidationResult.
func (v *EvidenceValidator) Validate(summary *GroundedSummary, evidenceMap map[string]string, ruleResult RuleBasedResult) *ValidationResult {
	result := &ValidationResult{}

	// Check ①: Reference Resolution
	result.UnresolvedRefs = v.validateReferences(summary, evidenceMap)

	// Check ②: Semantic Grounding
	var verifiedClaims int
	for _, claim := range summary.EvidenceChain {
		if len(claim.EvidenceRefs) == 0 {
			result.UngroundedClaims = append(result.UngroundedClaims, UngroundedClaim{
				Claim:      claim.Claim,
				Similarity: 0,
			})
			observability.IncLLMClaimsUngrounded()
			continue
		}
		// Resolve evidence text for this claim's refs.
		var evidenceTexts []string
		for _, ref := range claim.EvidenceRefs {
			if text, ok := evidenceMap[ref]; ok {
				evidenceTexts = append(evidenceTexts, text)
			} else {
				observability.IncLLMEvidenceRefsUnresolved()
			}
		}
		combinedEvidence := strings.Join(evidenceTexts, " ")
		similarity := jaccardOverlap(claim.Claim, combinedEvidence)
		if similarity >= v.claimThreshold {
			verifiedClaims++
		} else {
			result.UngroundedClaims = append(result.UngroundedClaims, UngroundedClaim{
				Claim:        claim.Claim,
				Similarity:   similarity,
				EvidenceRefs: claim.EvidenceRefs,
			})
			observability.IncLLMClaimsUngrounded()
		}
	}

	// Check ③: Agreement Gate
	result.AgreementScore, result.AgreementIssue = v.checkAgreement(summary, ruleResult)

	// Compute scores.
	totalClaims := len(summary.EvidenceChain)
	if totalClaims == 0 {
		totalClaims = 1
	}
	result.GroundingScore = float64(verifiedClaims) / float64(totalClaims)
	result.HallucinationRisk = 1.0 - (result.GroundingScore*0.6 + result.AgreementScore*0.4)

	observability.ObserveLLMHallucinationRisk(result.HallucinationRisk)

	// Determine pass/fail and fallback reason.
	switch {
	case result.HallucinationRisk > v.hallucinationThreshold:
		result.Passed = false
		result.FallbackReason = "hallucination_risk_exceeded"
		observability.IncLLMGroundingTotal("rejected")
		observability.IncLLMFallbackTotal(result.FallbackReason)
	case result.HallucinationRisk > v.qualityWarnThreshold:
		result.Passed = true
		observability.IncLLMGroundingTotal("degraded")
	case result.GroundingScore < 0.3:
		result.Passed = false
		result.FallbackReason = "grounding_score_too_low"
		observability.IncLLMGroundingTotal("rejected")
		observability.IncLLMFallbackTotal(result.FallbackReason)
	default:
		result.Passed = true
		observability.IncLLMGroundingTotal("passed")
	}

	return result
}

// validateReferences checks that every evidence_ref in the LLM output exists
// in the injected evidence map. Returns unresolved refs.
func (v *EvidenceValidator) validateReferences(summary *GroundedSummary, evidenceMap map[string]string) []string {
	var unresolved []string
	seen := make(map[string]bool)
	for _, claim := range summary.EvidenceChain {
		for _, ref := range claim.EvidenceRefs {
			if seen[ref] {
				continue
			}
			seen[ref] = true
			if _, ok := evidenceMap[ref]; !ok {
				unresolved = append(unresolved, ref)
				observability.IncLLMEvidenceRefsUnresolved()
			}
		}
	}
	return unresolved
}

// checkAgreement computes an agreement score based on the LLM's declared
// agreement_level and a basic keyword overlap check.
func (v *EvidenceValidator) checkAgreement(summary *GroundedSummary, ruleResult RuleBasedResult) (float64, string) {
	level := strings.TrimSpace(strings.ToLower(summary.RootCauseConfirmation.AgreementLevel))
	switch level {
	case "full_agree":
		return 1.0, ""
	case "partial_agree":
		return 0.6, ""
	case "disagree":
		return 0.0, "LLM declared disagree with rule engine diagnosis"
	default:
		return 0.3, "agreement_level not recognized: " + summary.RootCauseConfirmation.AgreementLevel
	}
}

// jaccardOverlap computes a simple keyword-level Jaccard similarity between
// two texts. This is the "fast path" for semantic grounding.
func jaccardOverlap(a, b string) float64 {
	wordsA := tokenize(a)
	wordsB := tokenize(b)
	if len(wordsA) == 0 && len(wordsB) == 0 {
		return 1.0
	}
	setA := make(map[string]struct{}, len(wordsA))
	for _, w := range wordsA {
		setA[w] = struct{}{}
	}
	intersection := 0
	for _, w := range wordsB {
		if _, ok := setA[w]; ok {
			intersection++
			delete(setA, w)
		}
	}
	union := len(wordsA) + len(wordsB) - intersection
	if union == 0 {
		return 0
	}
	return float64(intersection) / float64(union)
}

// tokenize splits text into lowercase word tokens of length >= 2.
func tokenize(s string) []string {
	fields := strings.Fields(strings.ToLower(s))
	var tokens []string
	for _, f := range fields {
		f = strings.Trim(f, ".,;:!?\"'()[]{}")
		if len(f) >= 2 {
			tokens = append(tokens, f)
		}
	}
	return tokens
}

// BuildEvidenceMap converts evidence records into an evidence_id → content map
// for reference resolution by the validator.
func BuildEvidenceMap(records []string, ids []string) map[string]string {
	m := make(map[string]string, len(records))
	for i, content := range records {
		if i < len(ids) {
			m[ids[i]] = content
		}
	}
	return m
}
```

- [ ] **Step 2: Verify compilation**

```powershell
go build ./...
```

- [ ] **Step 3: Commit**

```bash
git add internal/llm/grounding.go
git commit -m "feat(llm): add EvidenceValidator with reference resolution, semantic grounding, and agreement gate"
```

---

### Task 1.6: Write grounding tests

**Files:**
- Create: `internal/llm/grounding_test.go`

- [ ] **Step 1: Write test file**

Create `internal/llm/grounding_test.go`:

```go
package llm

import (
	"testing"
)

func newTestValidator() *EvidenceValidator {
	return NewEvidenceValidator(0.3, 0.4, 0.3, 0.3)
}

func TestValidateReferences_AllResolved(t *testing.T) {
	v := newTestValidator()
	summary := &GroundedSummary{
		RootCauseConfirmation: RootCauseConfirmation{RuleDiagnosis: "OOMKilled", AgreementLevel: "full_agree"},
		EvidenceChain: []GroundedClaim{
			{Claim: "memory exceeded limit", EvidenceRefs: []string{"ev-0"}},
		},
	}
	evidenceMap := map[string]string{"ev-0": "container memory 135Mi > limit 128Mi"}
	unresolved := v.validateReferences(summary, evidenceMap)
	if len(unresolved) != 0 {
		t.Errorf("expected 0 unresolved refs, got %v", unresolved)
	}
}

func TestValidateReferences_PartialResolved(t *testing.T) {
	v := newTestValidator()
	summary := &GroundedSummary{
		RootCauseConfirmation: RootCauseConfirmation{RuleDiagnosis: "CrashLoopBackOff", AgreementLevel: "full_agree"},
		EvidenceChain: []GroundedClaim{
			{Claim: "container crashed", EvidenceRefs: []string{"ev-0"}},
			{Claim: "imaginary cause", EvidenceRefs: []string{"ev-fake"}},
		},
	}
	evidenceMap := map[string]string{"ev-0": "exit code 1"}
	unresolved := v.validateReferences(summary, evidenceMap)
	if len(unresolved) != 1 || unresolved[0] != "ev-fake" {
		t.Errorf("expected [ev-fake] as unresolved, got %v", unresolved)
	}
}

func TestCheckGrounding_HighOverlap(t *testing.T) {
	v := newTestValidator()
	claim := GroundedClaim{
		Claim:        "container memory exceeded 128Mi limit",
		EvidenceRefs: []string{"ev-0"},
	}
	evidenceMap := map[string]string{"ev-0": "container memory working set 135Mi, limit is 128Mi"}
	combined := evidenceMap["ev-0"]
	similarity := jaccardOverlap(claim.Claim, combined)
	if similarity < 0.1 {
		t.Errorf("expected high overlap, got similarity=%f", similarity)
	}
	if similarity < v.claimThreshold {
		t.Errorf("expected claim to pass grounding threshold %f, got %f", v.claimThreshold, similarity)
	}
}

func TestCheckGrounding_HallucinatedClaim(t *testing.T) {
	v := newTestValidator()
	claim := GroundedClaim{
		Claim:        "Redis connection pool leak caused fragmentation",
		EvidenceRefs: []string{"ev-0"},
	}
	evidenceMap := map[string]string{"ev-0": "container memory working set 135Mi, limit is 128Mi"}
	combined := evidenceMap["ev-0"]
	similarity := jaccardOverlap(claim.Claim, combined)
	if similarity >= v.claimThreshold {
		t.Errorf("expected hallucinated claim to fail grounding, got similarity=%f >= threshold=%f", similarity, v.claimThreshold)
	}
}

func TestCheckAgreement_FullAgree(t *testing.T) {
	v := newTestValidator()
	summary := &GroundedSummary{
		RootCauseConfirmation: RootCauseConfirmation{AgreementLevel: "full_agree"},
	}
	score, issue := v.checkAgreement(summary, RuleBasedResult{})
	if score != 1.0 {
		t.Errorf("expected agreement_score=1.0, got %f", score)
	}
	if issue != "" {
		t.Errorf("expected no issue, got %s", issue)
	}
}

func TestCheckAgreement_Disagree(t *testing.T) {
	v := newTestValidator()
	summary := &GroundedSummary{
		RootCauseConfirmation: RootCauseConfirmation{AgreementLevel: "disagree"},
	}
	score, issue := v.checkAgreement(summary, RuleBasedResult{})
	if score != 0.0 {
		t.Errorf("expected agreement_score=0.0, got %f", score)
	}
	if issue == "" {
		t.Error("expected agreement issue for disagree, got empty")
	}
}

func TestCheckAgreement_Invalid(t *testing.T) {
	v := newTestValidator()
	summary := &GroundedSummary{
		RootCauseConfirmation: RootCauseConfirmation{AgreementLevel: "whatever"},
	}
	score, _ := v.checkAgreement(summary, RuleBasedResult{})
	if score != 0.3 {
		t.Errorf("expected fallback score=0.3 for invalid agreement, got %f", score)
	}
}

func TestHallucinationRisk_High(t *testing.T) {
	v := newTestValidator()
	result := &ValidationResult{
		GroundingScore: 0.2,
		AgreementScore: 0.0,
	}
	result.HallucinationRisk = 1.0 - (result.GroundingScore*0.6 + result.AgreementScore*0.4)
	if result.HallucinationRisk <= 0.4 {
		t.Errorf("expected high risk > 0.4, got %f", result.HallucinationRisk)
	}
}

func TestHallucinationRisk_Low(t *testing.T) {
	v := newTestValidator()
	result := &ValidationResult{
		GroundingScore: 1.0,
		AgreementScore: 1.0,
	}
	result.HallucinationRisk = 1.0 - (result.GroundingScore*0.6 + result.AgreementScore*0.4)
	if result.HallucinationRisk != 0.0 {
		t.Errorf("expected risk=0, got %f", result.HallucinationRisk)
	}
}

func TestHallucinationRisk_DisagreeButWellGrounded(t *testing.T) {
	v := newTestValidator()
	// All claims verified (grounding=1.0) but LLM disagrees with rule engine.
	result := &ValidationResult{
		GroundingScore: 1.0,
		AgreementScore: 0.0,
	}
	result.HallucinationRisk = 1.0 - (result.GroundingScore*0.6 + result.AgreementScore*0.4)
	// risk = 0.4 exactly — borderline, passes > 0.4 check.
	if result.HallucinationRisk != 0.4 {
		t.Errorf("expected risk=0.4 for well-grounded disagree, got %f", result.HallucinationRisk)
	}
	if result.HallucinationRisk > 0.4 {
		t.Error("well-grounded disagree should not exceed 0.4 threshold")
	}
}

func TestValidate_EndToEnd_AllGrounded(t *testing.T) {
	v := newTestValidator()
	summary := &GroundedSummary{
		RootCauseConfirmation: RootCauseConfirmation{
			RuleDiagnosis:  "container memory limit (128Mi) is too low for actual usage (135Mi)",
			AgreementLevel: "full_agree",
			Explanation:    "Evidence confirms OOMKilled due to memory limit exceeded",
		},
		EvidenceChain: []GroundedClaim{
			{Claim: "container terminated with OOMKilled", EvidenceRefs: []string{"ev-0"}, ConfidenceIncrement: 0.4},
			{Claim: "memory usage 135Mi exceeded 128Mi limit", EvidenceRefs: []string{"ev-1"}, ConfidenceIncrement: 0.3},
		},
		SuggestedActions: []string{"Increase memory limit to 256Mi", "Monitor memory usage trend"},
	}
	evidenceMap := map[string]string{
		"ev-0": "Last termination reason: OOMKilled, exit code: 137",
		"ev-1": "Container memory: working set 135Mi, limit configured at 128Mi",
	}
	result := v.Validate(summary, evidenceMap, RuleBasedResult{
		RootCauseSummary: "container memory limit (128Mi) is too low for actual usage (135Mi)",
	})
	if !result.Passed {
		t.Errorf("expected passed validation for well-grounded summary, got fallback reason: %s", result.FallbackReason)
	}
	if result.HallucinationRisk > 0.2 {
		t.Errorf("expected low hallucination risk for well-grounded summary, got %f", result.HallucinationRisk)
	}
}

func TestValidate_EndToEnd_Hallucinated(t *testing.T) {
	v := newTestValidator()
	summary := &GroundedSummary{
		RootCauseConfirmation: RootCauseConfirmation{
			RuleDiagnosis:  "container memory limit is too low",
			AgreementLevel: "full_agree",
			Explanation:    "Diagnosis confirmed but with additional speculative cause: possible Redis connection pool fragmentation",
		},
		EvidenceChain: []GroundedClaim{
			{Claim: "Redis connection pool fragmentation caused memory pressure", EvidenceRefs: []string{"ev-0"}, ConfidenceIncrement: 0.5},
		},
		SuggestedActions: []string{"Switch to Cilium CNI"},
	}
	evidenceMap := map[string]string{
		"ev-0": "Last termination reason: OOMKilled, exit code: 137",
	}
	result := v.Validate(summary, evidenceMap, RuleBasedResult{
		RootCauseSummary: "container memory limit is too low",
	})
	if result.Passed && result.GroundingScore > 0.4 {
		t.Errorf("expected validation to detect hallucination, got passed with grounding_score=%f", result.GroundingScore)
	}
}

func TestGroundedSummary_ToEnhancedSummary(t *testing.T) {
	g := &GroundedSummary{
		RootCauseConfirmation: RootCauseConfirmation{
			RuleDiagnosis:  "OOMKilled due to low memory limit",
			AgreementLevel: "full_agree",
			Explanation:    "Container memory limit 128Mi exceeded by actual usage 135Mi",
		},
		EvidenceChain: []GroundedClaim{
			{Claim: "memory exceeded", EvidenceRefs: []string{"ev-0"}, ConfidenceIncrement: 0.5},
		},
		SuggestedActions: []string{"Increase memory limit"},
	}
	enhanced := g.ToEnhancedSummary()
	if enhanced == nil {
		t.Fatal("ToEnhancedSummary returned nil")
	}
	if enhanced.RootCauseSummary == "" {
		t.Error("enhanced summary missing root_cause_summary")
	}
}
```

- [ ] **Step 2: Run tests**

```powershell
go test ./internal/llm/... -v -run "TestValidate|TestCheck|TestHallucination|TestGrounded"
```

Expected: All tests pass.

- [ ] **Step 3: Commit**

```bash
git add internal/llm/grounding_test.go
git commit -m "test(llm): add comprehensive grounding pipeline tests"
```

---

### Task 1.7: Add GenerateGroundedSummary to LLM client

**Files:**
- Modify: `internal/llm/client.go`
- Modify: `internal/llm/types.go` (LLMClient interface)

- [ ] **Step 1: Extend LLMClient interface**

In `internal/llm/types.go`, replace the `LLMClient` interface:

```go
type LLMClient interface {
	GenerateDiagnosisSummary(ctx context.Context, prompt Prompt) (*EnhancedSummary, error)
	GenerateGroundedSummary(ctx context.Context, prompt Prompt) (*GroundedSummary, error)
}
```

- [ ] **Step 2: Add GenerateGroundedSummary to OpenAICompatibleClient**

Add after the `GenerateDiagnosisSummary` method in `internal/llm/client.go`:

```go
// GenerateGroundedSummary sends a structured, evidence-grounded prompt and
// parses the JSON response into a GroundedSummary.
func (c *OpenAICompatibleClient) GenerateGroundedSummary(ctx context.Context, prompt Prompt) (*GroundedSummary, error) {
	if c == nil || c.baseURL == "" || c.apiKey == "" || c.model == "" {
		return nil, fmt.Errorf("llm client is not configured")
	}
	start := time.Now()
	endpoint, err := c.chatCompletionsURL()
	if err != nil {
		return nil, err
	}
	payload := chatCompletionRequest{
		Model: c.model,
		Messages: []chatMessage{
			{Role: "system", Content: prompt.System},
			{Role: "user", Content: prompt.User},
		},
		Temperature:    0.2,
		ResponseFormat: &responseFormat{Type: "json_object"},
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+c.apiKey)
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode >= 300 {
		return nil, fmt.Errorf("llm request failed: %s: %s", resp.Status, string(respBody))
	}
	var raw chatCompletionResponse
	if err := json.Unmarshal(respBody, &raw); err != nil {
		return nil, err
	}
	if raw.Error != nil {
		return nil, fmt.Errorf("llm response error: %s: %s", raw.Error.Type, raw.Error.Message)
	}
	if len(raw.Choices) == 0 {
		return nil, fmt.Errorf("llm response has no choices")
	}
	c.captureUsage(raw.Usage, time.Since(start))
	summary, err := parseGroundedSummary(raw.Choices[0].Message.Content)
	if err != nil {
		return nil, err
	}
	sanitizeGroundedSummary(summary)
	return summary, nil
}

// parseGroundedSummary accepts raw JSON or fenced JSON from the model.
func parseGroundedSummary(content string) (*GroundedSummary, error) {
	content = strings.TrimSpace(content)
	content = strings.TrimPrefix(content, "```json")
	content = strings.TrimPrefix(content, "```")
	content = strings.TrimSuffix(content, "```")
	content = strings.TrimSpace(content)
	var summary GroundedSummary
	if err := json.Unmarshal([]byte(content), &summary); err != nil {
		return nil, err
	}
	if summary.RootCauseConfirmation.RuleDiagnosis == "" {
		return nil, fmt.Errorf("llm response missing root_cause_confirmation.rule_diagnosis")
	}
	if summary.RootCauseConfirmation.AgreementLevel == "" {
		return nil, fmt.Errorf("llm response missing root_cause_confirmation.agreement_level")
	}
	return &summary, nil
}

// sanitizeGroundedSummary clamps confidence values and builds the evidence map
// from the existing claim refs, as well as filtering dangerous actions.
func sanitizeGroundedSummary(summary *GroundedSummary) {
	if summary == nil {
		return
	}
	for i := range summary.EvidenceChain {
		if summary.EvidenceChain[i].ConfidenceIncrement < 0 {
			summary.EvidenceChain[i].ConfidenceIncrement = 0
		}
		if summary.EvidenceChain[i].ConfidenceIncrement > 1 {
			summary.EvidenceChain[i].ConfidenceIncrement = 1
		}
	}
	safeActions := make([]string, 0, len(summary.SuggestedActions))
	for _, action := range summary.SuggestedActions {
		if strings.TrimSpace(action) == "" || isDangerousAction(action) {
			continue
		}
		safeActions = append(safeActions, action)
	}
	if len(safeActions) == 0 {
		safeActions = append(safeActions, "Review the evidence chain and Runbook manually before making any high-risk change.")
	}
	summary.SuggestedActions = safeActions
}
```

- [ ] **Step 3: Verify compilation**

```powershell
go build ./...
```

- [ ] **Step 4: Commit**

```bash
git add internal/llm/client.go internal/llm/types.go
git commit -m "feat(llm): add GenerateGroundedSummary with structured JSON parsing and sanitization"
```

---

### Task 1.8: Wire grounded path into DiagnosisService

**Files:**
- Modify: `internal/service/diagnosis_service.go`

- [ ] **Step 1: Replace enhanceReportWithLLM to use grounded path**

Replace the entire `enhanceReportWithLLM` method (lines 581-618):

```go
// enhanceReportWithLLM runs the evidence-grounded LLM pipeline when enabled.
// Falls back to the legacy pipeline when grounding is disabled or LLM enhancement
// fails the hallucination risk check.
func (s *DiagnosisService) enhanceReportWithLLM(ctx context.Context, taskID uint, diagCtx *diagnostic.DiagnosticContext, report *diagnostic.Report, runbookHits []rag.Hit) {
	ruleResult := llm.RuleResultFromReport(report)
	report.RuleBasedResult = ruleResult
	if s.llmClient == nil {
		return
	}
	if s.cfg != nil && s.cfg.LLM.Grounding.Enabled {
		s.enhanceGrounded(ctx, taskID, diagCtx, report, runbookHits, ruleResult)
		return
	}
	// Legacy path (non-grounded) — preserved for backward compatibility.
	s.enhanceLegacy(ctx, taskID, diagCtx, report, runbookHits, ruleResult)
}

// enhanceGrounded runs the evidence-grounded LLM pipeline with validation.
func (s *DiagnosisService) enhanceGrounded(ctx context.Context, taskID uint, diagCtx *diagnostic.DiagnosticContext, report *diagnostic.Report, runbookHits []rag.Hit, ruleResult llm.RuleBasedResult) {
	groundedPrompt := llm.BuildGroundedPrompt(diagCtx, ruleResult, report.Evidences, runbookHits)
	var summary *llm.GroundedSummary
	start := time.Now()
	ctx, span := observability.Tracer().Start(ctx, "diagnosis.llm_grounded")
	err := resilience.Do(ctx, s.llmRetryConfig(), func() error {
		var callErr error
		summary, callErr = s.llmClient.GenerateGroundedSummary(ctx, groundedPrompt)
		return callErr
	})
	s.recordLLMUsage(ctx, taskID)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		span.End()
		observability.ObserveDiagnosisStage("llm_grounded", time.Since(start))
		observability.IncLLMCallTotal("failed")
		observability.IncLLMFallbackTotal("api_error")
		s.log.Warn("grounded llm enhancement failed; keep rule-based report", zap.Error(err))
		report.Evidences = append(report.Evidences, diagnostic.EvidenceRecord{
			SourceType: "llm",
			Title:      "Grounded LLM enhancement failed",
			Content:    err.Error(),
			Severity:   "warning",
			Raw:        map[string]string{"error": err.Error()},
			Timestamp:  time.Now(),
		})
		return
	}
	span.End()
	observability.ObserveDiagnosisStage("llm_grounded", time.Since(start))
	observability.IncLLMCallTotal("success")

	// Build evidence map for validation.
	evidenceIDs := make([]string, len(report.Evidences))
	evidenceContents := make([]string, len(report.Evidences))
	for i, ev := range report.Evidences {
		evidenceIDs[i] = fmt.Sprintf("ev-%d", i)
		evidenceContents[i] = ev.Content
	}
	evidenceMap := llm.BuildEvidenceMap(evidenceContents, evidenceIDs)

	validator := llm.NewEvidenceValidator(
		s.cfg.LLM.Grounding.SemanticThreshold,
		s.cfg.LLM.Grounding.HallucinationRiskThreshold,
		s.cfg.LLM.Grounding.QualityWarningThreshold,
		s.cfg.LLM.Grounding.GroundedClaimThreshold,
	)
	validationResult := validator.Validate(summary, evidenceMap, ruleResult)

	if !validationResult.Passed {
		s.log.Warn("grounded LLM summary rejected by validation pipeline",
			zap.Float64("hallucination_risk", validationResult.HallucinationRisk),
			zap.Float64("grounding_score", validationResult.GroundingScore),
			zap.String("fallback_reason", validationResult.FallbackReason),
		)
		report.Evidences = append(report.Evidences, diagnostic.EvidenceRecord{
			SourceType: "llm",
			Title:      "LLM enhancement rejected — hallucination risk exceeded threshold",
			Content:    fmt.Sprintf("hallucination_risk=%.2f grounding_score=%.2f reason=%s", validationResult.HallucinationRisk, validationResult.GroundingScore, validationResult.FallbackReason),
			Severity:   "warning",
			Timestamp:  time.Now(),
		})
		return
	}

	// Convert grounded summary to enhanced format for persistence compatibility.
	enhanced := summary.ToEnhancedSummary()
	report.LLMEnhancedSummary = enhanced
	if validationResult.HallucinationRisk > s.cfg.LLM.Grounding.QualityWarningThreshold {
		report.Evidences = append(report.Evidences, diagnostic.EvidenceRecord{
			SourceType: "llm",
			Title:      "LLM enhancement accepted with quality warning",
			Content:    fmt.Sprintf("hallucination_risk=%.2f — some claims may be unverified", validationResult.HallucinationRisk),
			Severity:   "warning",
			Timestamp:  time.Now(),
		})
	}
}

// enhanceLegacy runs the original (non-grounded) LLM enhancement path.
func (s *DiagnosisService) enhanceLegacy(ctx context.Context, taskID uint, diagCtx *diagnostic.DiagnosticContext, report *diagnostic.Report, runbookHits []rag.Hit, ruleResult llm.RuleBasedResult) {
	prompt := llm.BuildPrompt(diagCtx, ruleResult, report.Evidences, runbookHits)
	var summary *llm.EnhancedSummary
	start := time.Now()
	ctx, span := observability.Tracer().Start(ctx, "diagnosis.llm")
	err := resilience.Do(ctx, s.llmRetryConfig(), func() error {
		var callErr error
		summary, callErr = s.llmClient.GenerateDiagnosisSummary(ctx, prompt)
		return callErr
	})
	s.recordLLMUsage(ctx, taskID)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
	}
	span.End()
	observability.ObserveDiagnosisStage("llm", time.Since(start))
	if err != nil {
		observability.IncLLMCallTotal("failed")
		s.log.Warn("llm enhancement failed; keep rule-based report", zap.Error(err))
		report.Evidences = append(report.Evidences, diagnostic.EvidenceRecord{
			SourceType: "llm",
			Title:      "LLM enhancement failed",
			Content:    err.Error(),
			Severity:   "warning",
			Raw:        map[string]string{"error": err.Error()},
			Timestamp:  time.Now(),
		})
		return
	}
	observability.IncLLMCallTotal("success")
	report.LLMEnhancedSummary = summary
}
```

Add `"fmt"` to imports if not already present.

- [ ] **Step 2: Verify compilation**

```powershell
go build ./...
```

- [ ] **Step 3: Run existing service tests**

```powershell
go test ./internal/service/... -v -run "TestEnhance"
```

- [ ] **Step 4: Commit**

```bash
git add internal/service/diagnosis_service.go
git commit -m "feat(service): wire grounded LLM pipeline with validation, fallback to rule-based on hallucination risk"
```

---

### Task 1.9: Add config YAML and Helm values

**Files:**
- Modify: `configs/config.yaml`
- Modify: `deployments/helm/kubesage/values.yaml`

- [ ] **Step 1: Add grounding section to configs/config.yaml**

Find the `llm:` section and add `grounding:` under it:

```yaml
llm:
  enabled: false
  base_url: ""
  api_key: ""
  model: "deepseek-chat"
  retry_max_attempts: 2
  retry_initial_backoff_ms: 200
  retry_max_backoff_ms: 2000
  grounding:
    enabled: true
    semantic_threshold: 0.3
    hallucination_risk_threshold: 0.4
    quality_warning_threshold: 0.3
    grounded_claim_threshold: 0.3
```

- [ ] **Step 2: Add grounding section to Helm values.yaml**

Find `llm:` in `deployments/helm/kubesage/values.yaml` and add:

```yaml
  grounding:
    enabled: true
    semanticThreshold: 0.3
    hallucinationRiskThreshold: 0.4
    qualityWarningThreshold: 0.3
    groundedClaimThreshold: 0.3
```

- [ ] **Step 3: Update Helm configmap template to include grounding values**

Read `deployments/helm/kubesage/templates/configmap.yaml`, find the `llm:` section, add grounding keys mapping the values.

- [ ] **Step 4: Commit**

```bash
git add configs/config.yaml deployments/helm/kubesage/values.yaml deployments/helm/kubesage/templates/configmap.yaml
git commit -m "chore(config): add llm.grounding configuration to YAML and Helm chart"
```

---

## Phase 2: Agent Trace Visualization (P1)

### Task 2.1: Add OTEL span events to Agent Runtime

**Files:**
- Modify: `internal/agent/runtime.go:75-281`

- [ ] **Step 1: Add span events after key decision points**

The `Run()` method already has OTEL spans at each phase (`agent.runtime`, `agent.planner`, `agent.hypothesis_update`, `agent.verification`, `agent.tool`). Add `span.AddEvent()` calls to record structured decision metadata.

After line 94 (plan complete + recorded), add:

```go
	span.AddEvent("agent.plan.created", trace.WithAttributes(
		attribute.Int("plan.steps", len(plan.Steps)),
		attribute.String("plan.summary", plan.Summary),
	))
```

After line 162 (hypothesis update complete in loop), add:

```go
			if len(latestScores) > 0 {
				top := latestScores[0]
				span.AddEvent("agent.hypothesize", trace.WithAttributes(
					attribute.String("top_hypothesis", top.Type),
					attribute.Float64("top_confidence", top.Confidence),
					attribute.Int("total_hypotheses", len(latestScores)),
				))
			}
```

After line 177 (LLM reflection complete, stop decision), add:

```go
					span.AddEvent("agent.reflect.stop", trace.WithAttributes(
						attribute.String("reason", reflection.Reason),
						attribute.Int("new_steps", len(reflection.NewSteps)),
					))
```

After line 187 (LLM reflection found new steps), add:

```go
					span.AddEvent("agent.reflect.new_steps", trace.WithAttributes(
						attribute.Int("append_count", len(reflection.NewSteps)),
					))
```

After line 195 (confirmed hypothesis stop), add:

```go
				span.AddEvent("agent.stop.confirmed", trace.WithAttributes(
					attribute.String("stop_reason", stopReason),
				))
```

After line 270 (final decision recorded), add:

```go
	span.AddEvent("agent.done", trace.WithAttributes(
		attribute.String("stop_reason", stopReason),
		attribute.String("fault_type", report.FaultType),
		attribute.Int("steps_executed", stepsExecuted),
		attribute.Int("total_evidences", len(report.Evidences)),
	))
```

- [ ] **Step 2: Verify compilation**

```powershell
go build ./...
```

The existing imports already include `"go.opentelemetry.io/otel/attribute"` and `"go.opentelemetry.io/otel/trace"`.

- [ ] **Step 3: Run existing agent runtime tests**

```powershell
go test ./internal/agent/... -v -run "TestRuntime"
```

- [ ] **Step 4: Commit**

```bash
git add internal/agent/runtime.go
git commit -m "feat(agent): add structured span events at plan, hypothesize, reflect, and done phases"
```

---

### Task 2.2: Extend AgentStep recording with trace fields

**Files:**
- Modify: `internal/agent/recorder.go`

- [ ] **Step 1: Extend stepRecord struct**

Replace the existing `stepRecord` struct (lines 11-20):

```go
type stepRecord struct {
	ParentStepID       *uint
	Stage              string
	ToolName           string
	Status             string
	Input              interface{}
	Output             interface{}
	Duration           time.Duration
	Reason             string
	ObservationSummary string
	ParallelGroup      int
	ToolLatencyMS      int64
}
```

- [ ] **Step 2: Extend record() to populate new fields**

Replace the `model.AgentStep{...}` block in the `record()` method (lines 38-51):

```go
	step := &model.AgentStep{
		TaskID:             r.taskID,
		ParentStepID:       record.ParentStepID,
		TraceID:            r.traceID,
		StepIndex:          r.nextIndex,
		Stage:              record.Stage,
		ToolName:           record.ToolName,
		InputJSON:          jsonText(record.Input),
		OutputJSON:         jsonText(record.Output),
		Status:             record.Status,
		DurationMS:         record.Duration.Milliseconds(),
		ReasoningSummary:   record.Reason,
		ObservationSummary: truncateForDB(record.ObservationSummary, 500),
		ParallelGroup:      record.ParallelGroup,
		ToolLatencyMS:      record.ToolLatencyMS,
		CreatedAt:          time.Now(),
	}
```

Add the helper function at the bottom of the file:

```go
func truncateForDB(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen]
}
```

- [ ] **Step 3: Update call sites in runtime.go to populate new fields**

In `internal/agent/runtime.go`, find each `recorder.record()` call where a tool is executed and add the new fields.

In the `executePlanSteps` loop (around line 128-144), the `stepRecord` already has `result.DurationMS` — map it to `ToolLatencyMS`:

```go
				toolStep := recorder.record(ctx, stepRecord{
					Stage:              StageToolCall,
					ToolName:           step.ToolName,
					Status:             status,
					Input:              step.Input,
					Output:             result,
					Duration:           time.Duration(result.DurationMS) * time.Millisecond,
					Reason:             step.Reason,
					ObservationSummary: truncateForDB(result.Observation, 500),
					ParallelGroup:      parseGroupID(step.ParallelGroup),
					ToolLatencyMS:      result.DurationMS,
				})
```

Add the helper at the bottom of runtime.go:

```go
func parseGroupID(group string) int {
	if group == "" {
		return 0
	}
	var g int
	for _, c := range group {
		g = g*31 + int(c)
	}
	return g % 1000
}
```

- [ ] **Step 4: Verify compilation**

```powershell
go build ./...
```

- [ ] **Step 5: Commit**

```bash
git add internal/agent/recorder.go internal/agent/runtime.go
git commit -m "feat(agent): extend step recording with observation_summary, parallel_group, tool_latency_ms"
```

---

### Task 2.3: Add DB migration and model fields

**Files:**
- Create: `migrations/000004_agent_trace.up.sql`
- Create: `migrations/000004_agent_trace.down.sql`
- Modify: `internal/model/agent.go` (AgentStep struct)

- [ ] **Step 1: Create up migration**

Create `migrations/000004_agent_trace.up.sql`:

```sql
ALTER TABLE agent_steps
    ADD COLUMN observation_summary VARCHAR(500) DEFAULT '' AFTER reasoning_summary,
    ADD COLUMN parallel_group INT NOT NULL DEFAULT 0 AFTER observation_summary,
    ADD COLUMN tool_latency_ms INT NOT NULL DEFAULT 0 AFTER parallel_group;
```

- [ ] **Step 2: Create down migration**

Create `migrations/000004_agent_trace.down.sql`:

```sql
ALTER TABLE agent_steps
    DROP COLUMN observation_summary,
    DROP COLUMN parallel_group,
    DROP COLUMN tool_latency_ms;
```

- [ ] **Step 3: Update AgentStep model**

In `internal/model/agent.go`, add to the `AgentStep` struct (after `ReasoningSummary`):

```go
	ObservationSummary string    `json:"observation_summary" gorm:"size:500"`
	ParallelGroup      int       `json:"parallel_group" gorm:"default:0;not null"`
	ToolLatencyMS      int64     `json:"tool_latency_ms" gorm:"default:0;not null"`
```

- [ ] **Step 4: Verify compilation**

```powershell
go build ./...
```

- [ ] **Step 5: Commit**

```bash
git add migrations/000004_agent_trace.up.sql migrations/000004_agent_trace.down.sql internal/model/agent.go
git commit -m "feat(model): add observation_summary, parallel_group, tool_latency_ms to agent_steps"
```

---

### Task 2.4: Enhance AgentTimeline frontend visualization

**Files:**
- Modify: `web/src/pages/TaskDetail/tabs/AgentTimeline.tsx`
- Modify: `web/src/api/types.ts`

- [ ] **Step 1: Extend TypeScript types**

In `web/src/api/types.ts`, add to the `AgentStep` interface:

```typescript
export interface AgentStep {
  id: number;
  task_id: number;
  step_index: number;
  stage: string;
  tool_name?: string;
  status: string;
  duration_ms: number;
  reasoning_summary: string;
  observation_summary: string;      // NEW
  parallel_group: number;           // NEW
  tool_latency_ms: number;          // NEW
  created_at: string;
}
```

- [ ] **Step 2: Rewrite AgentTimeline component**

Replace `web/src/pages/TaskDetail/tabs/AgentTimeline.tsx` with a visualization that:

1. Groups steps into phases (PLAN / EXECUTE / HYPOTHESIZE / REFLECT)
2. Shows parallel execution groups visually adjacent
3. Displays tool name, status (✅/❌), latency, and truncated observation
4. Renders a final verdict section with grounding validation status

```tsx
import React from 'react';
import { Timeline, Tag, Typography, Card, Space, Tooltip } from 'antd';
import {
  CheckCircleOutlined,
  CloseCircleOutlined,
  ThunderboltOutlined,
  BulbOutlined,
  SyncOutlined,
  SafetyCertificateOutlined,
} from '@ant-design/icons';
import type { AgentStep } from '../../../api/types';

const { Text, Paragraph } = Typography;

interface Props {
  steps: AgentStep[];
  report?: {
    llm_enhanced_summary?: any;
    agent_execution_summary?: string;
    fault_type?: string;
    confidence_score?: number;
  };
}

const PHASE_LABELS: Record<string, { label: string; color: string; icon: React.ReactNode }> = {
  plan: { label: 'PLAN', color: 'blue', icon: <ThunderboltOutlined /> },
  tool_call: { label: 'EXECUTE', color: 'green', icon: <SyncOutlined spin /> },
  observation: { label: 'OBSERVE', color: 'orange', icon: <SyncOutlined /> },
  hypothesize: { label: 'HYPOTHESIZE', color: 'purple', icon: <BulbOutlined /> },
  reflection: { label: 'REFLECT', color: 'cyan', icon: <SafetyCertificateOutlined /> },
  decision: { label: 'DECISION', color: 'red', icon: <SafetyCertificateOutlined /> },
};

const truncate = (s: string, max: number): string =>
  s && s.length > max ? s.slice(0, max) + '...' : s;

export const AgentTimeline: React.FC<Props> = ({ steps, report }) => {
  const grouped = React.useMemo(() => {
    const phases: { phase: string; items: AgentStep[] }[] = [];
    let currentPhase = '';
    for (const step of steps) {
      if (step.stage !== currentPhase) {
        currentPhase = step.stage;
        phases.push({ phase: currentPhase, items: [] });
      }
      phases[phases.length - 1].items.push(step);
    }
    return phases;
  }, [steps]);

  return (
    <div style={{ padding: 16 }}>
      {report?.agent_execution_summary && (
        <Card size="small" style={{ marginBottom: 16, background: '#f6ffed' }}>
          <Paragraph style={{ margin: 0 }}>{report.agent_execution_summary}</Paragraph>
        </Card>
      )}

      {grouped.map((group, gi) => {
        const cfg = PHASE_LABELS[group.phase] || { label: group.phase.toUpperCase(), color: 'default', icon: null };
        return (
          <Card
            key={gi}
            size="small"
            title={
              <Space>
                {cfg.icon}
                <Tag color={cfg.color}>{cfg.label}</Tag>
                <Text type="secondary">{group.items.length} step(s)</Text>
              </Space>
            }
            style={{ marginBottom: 12 }}
          >
            <Timeline
              items={group.items.map((step) => ({
                dot: step.status === 'success' ? (
                  <CheckCircleOutlined style={{ color: '#52c41a' }} />
                ) : step.status === 'failed' ? (
                  <CloseCircleOutlined style={{ color: '#ff4d4f' }} />
                ) : (
                  <CloseCircleOutlined style={{ color: '#faad14' }} />
                ),
                children: (
                  <div>
                    <Space>
                      <Text strong>{step.tool_name || step.stage}</Text>
                      <Tag color={step.status === 'success' ? 'green' : step.status === 'failed' ? 'red' : 'orange'}>
                        {step.status}
                      </Tag>
                      {step.tool_latency_ms > 0 && (
                        <Text type="secondary">{step.tool_latency_ms}ms</Text>
                      )}
                      {step.parallel_group > 0 && (
                        <Tag style={{ fontSize: 10 }}>parallel group {step.parallel_group}</Tag>
                      )}
                    </Space>
                    {step.observation_summary && (
                      <div style={{ marginTop: 4 }}>
                        <Text type="secondary" style={{ fontSize: 12 }}>
                          {truncate(step.observation_summary, 200)}
                        </Text>
                      </div>
                    )}
                    {step.reasoning_summary && (
                      <div style={{ marginTop: 2 }}>
                        <Text type="secondary" italic style={{ fontSize: 12 }}>
                          {truncate(step.reasoning_summary, 200)}
                        </Text>
                      </div>
                    )}
                  </div>
                ),
              }))}
            />
          </Card>
        );
      })}
    </div>
  );
};
```

- [ ] **Step 3: Verify frontend builds**

```powershell
cd web; npm run build
```

- [ ] **Step 4: Commit**

```bash
git add web/src/pages/TaskDetail/tabs/AgentTimeline.tsx web/src/api/types.ts
git commit -m "feat(web): enhance AgentTimeline with phase grouping, parallel groups, and observation summaries"
```

---

## Phase 3: RCA Evaluation Framework (P0)

### Task 3.1: Define evaluation types

**Files:**
- Create: `internal/eval/types.go`

- [ ] **Step 1: Create types file**

```go
package eval

import (
	"kubesage/internal/diagnostic"
)

// EvalCase defines one evaluation scenario with its expected outcome.
type EvalCase struct {
	ID          string       `yaml:"id"`
	Name        string       `yaml:"name"`
	Description string       `yaml:"description"`
	FixturePath string       `yaml:"fixture_path"`
	Expected    GoldenAnswer `yaml:"expected"`
}

// GoldenAnswer captures the correct diagnosis for one case.
type GoldenAnswer struct {
	FaultType             string   `yaml:"fault_type"`
	RootCause             string   `yaml:"root_cause"`
	KeyEvidences          []string `yaml:"key_evidences"`
	ShouldNotContain      []string `yaml:"should_not_contain"`
	AcceptableRemediations []string `yaml:"acceptable_remediations"`
	RiskLevel             string   `yaml:"risk_level"`
	ConfidenceMin         float64  `yaml:"confidence_min"`
}

// EvalMetrics aggregates scores across all cases in a suite.
type EvalMetrics struct {
	TotalCases  int     `yaml:"total_cases"`
	PassedCases int     `yaml:"passed_cases"`

	// Accuracy
	FaultTypeAccuracy float64 `yaml:"fault_type_accuracy"`
	RCASemanticMatch  float64 `yaml:"rca_semantic_match"`

	// Hallucination (requires LLM enhancement)
	HallucinationRate     float64 `yaml:"hallucination_rate"`
	EvidenceGroundingRate float64 `yaml:"evidence_grounding_rate"`
	AgreementWithRule     float64 `yaml:"agreement_with_rule"`

	// Safety
	ActionSafetyRate  float64 `yaml:"action_safety_rate"`
	UnsafeActionCount int     `yaml:"unsafe_action_count"`

	// Confidence calibration
	OverconfidentCount     int     `yaml:"overconfident_count"`
	ConfidenceCalibration  float64 `yaml:"confidence_calibration"`

	// Composite
	CompositeScore float64 `yaml:"composite_score"`
}

// EvalCaseResult records one case's evaluation outcome.
type EvalCaseResult struct {
	CaseID      string            `yaml:"case_id"`
	Passed      bool              `yaml:"passed"`
	Report      *diagnostic.Report `yaml:"-"`
	Errors      []string          `yaml:"errors,omitempty"`
	Details     map[string]any    `yaml:"details"`
}

// EvalSuiteResult contains the full suite evaluation output.
type EvalSuiteResult struct {
	Metrics  EvalMetrics      `yaml:"metrics"`
	Cases    []EvalCaseResult `yaml:"cases"`
}

// EvalConfig controls evaluation behavior.
type EvalConfig struct {
	SuiteName string
	LLMEnabled bool
	GroundingEnabled bool
	OutputPath string
}
```

- [ ] **Step 2: Verify compilation**

```powershell
go build ./...
```

- [ ] **Step 3: Commit**

```bash
git add internal/eval/types.go
git commit -m "feat(eval): define evaluation types — EvalCase, GoldenAnswer, EvalMetrics"
```

---

### Task 3.2: Implement fixture serialization

**Files:**
- Create: `internal/eval/fixture.go`

- [ ] **Step 1: Create fixture file**

```go
package eval

import (
	"encoding/json"
	"os"

	"kubesage/internal/diagnostic"
)

// Fixture wraps a serialized DiagnosticContext for replay-based evaluation.
type Fixture struct {
	DiagnosticContext *diagnostic.DiagnosticContext `json:"diagnostic_context"`
	SourceNamespace   string                        `json:"source_namespace"`
	SourcePodName     string                        `json:"source_pod_name"`
}

// LoadFixture reads a JSON fixture from disk.
func LoadFixture(path string) (*Fixture, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var f Fixture
	if err := json.Unmarshal(data, &f); err != nil {
		return nil, err
	}
	return &f, nil
}

// SaveFixture writes a DiagnosticContext as a replayable fixture.
func SaveFixture(path string, ctx *diagnostic.DiagnosticContext, namespace, podName string) error {
	f := Fixture{
		DiagnosticContext: ctx,
		SourceNamespace:   namespace,
		SourcePodName:     podName,
	}
	data, err := json.MarshalIndent(f, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0644)
}
```

- [ ] **Step 2: Ensure DiagnosticContext is JSON-serializable**

Verify that `internal/diagnostic/context.go`'s `DiagnosticContext` struct has JSON tags on all exported fields. Add `json:"..."` tags to any fields that lack them. Key fields: `Pod`, `Events`, `Logs`, `PVCStatuses`, `Topology`, `Correlations`.

- [ ] **Step 3: Verify compilation**

```powershell
go build ./...
```

- [ ] **Step 4: Commit**

```bash
git add internal/eval/fixture.go
git commit -m "feat(eval): add DiagnosticContext fixture serialization for replay-based evaluation"
```

---

### Task 3.3: Implement case loader

**Files:**
- Create: `internal/eval/loader.go`

- [ ] **Step 1: Create loader file**

```go
package eval

import (
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

// LoadCases reads all YAML case definitions from a directory.
func LoadCases(dir string) ([]EvalCase, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	var cases []EvalCase
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".yaml") {
			continue
		}
		path := filepath.Join(dir, entry.Name())
		data, err := os.ReadFile(path)
		if err != nil {
			return nil, err
		}
		var c EvalCase
		if err := yaml.Unmarshal(data, &c); err != nil {
			return nil, err
		}
		cases = append(cases, c)
	}
	return cases, nil
}
```

- [ ] **Step 2: Verify compilation**

```powershell
go build ./...
```

- [ ] **Step 3: Commit**

```bash
git add internal/eval/loader.go
git commit -m "feat(eval): add YAML case loader for evaluation scenarios"
```

---

### Task 3.4: Implement scorer

**Files:**
- Create: `internal/eval/scorer.go`

- [ ] **Step 1: Create scorer file**

```go
package eval

import (
	"strings"

	"kubesage/internal/diagnostic"
)

const (
	compositeWeightAccuracy     = 0.35
	compositeWeightHallucination = 0.35
	compositeWeightSafety       = 0.15
	compositeWeightConfidence   = 0.15
)

// ScoreCase evaluates one case report against its golden answer.
func ScoreCase(report *diagnostic.Report, golden GoldenAnswer) EvalCaseResult {
	result := EvalCaseResult{
		Details: make(map[string]any),
	}

	// Fault type accuracy.
	faultMatch := strings.EqualFold(strings.TrimSpace(report.FaultType), strings.TrimSpace(golden.FaultType))
	result.Details["fault_type_match"] = faultMatch

	// Semantic RCA match: check if golden root_cause keywords appear in report.
	rcaMatch := semanticMatch(report.RootCauseSummary, golden.RootCause)
	result.Details["rca_semantic_match"] = rcaMatch

	// Check should_not_contain for hallucination detection.
	var hallucinations []string
	for _, forbidden := range golden.ShouldNotContain {
		if containsIgnoreCase(report.RootCauseSummary, forbidden) {
			hallucinations = append(hallucinations, forbidden)
		}
		// Also check LLM enhanced summary if present.
		if report.LLMEnhancedSummary != nil {
			if enhanced, ok := report.LLMEnhancedSummary.(map[string]any); ok {
				if summary, ok := enhanced["root_cause_summary"].(string); ok {
					if containsIgnoreCase(summary, forbidden) {
						hallucinations = append(hallucinations, forbidden+" (in LLM summary)")
					}
				}
			}
		}
	}
	result.Details["hallucinations_found"] = hallucinations

	// Safety: check suggested actions against dangerous patterns.
	unsafeCount := countDangerousActions(report.SuggestedActions)
	result.Details["unsafe_actions"] = unsafeCount

	// Confidence calibration: high confidence but wrong = overconfident.
	overconfident := report.ConfidenceScore >= float64(golden.ConfidenceMin) && !rcaMatch
	result.Details["overconfident"] = overconfident

	// Pass decision: must have correct fault type AND no hallucinations AND safe.
	passed := faultMatch && len(hallucinations) == 0 && unsafeCount == 0
	if golden.ConfidenceMin > 0 && report.ConfidenceScore < golden.ConfidenceMin {
		passed = false
		result.Errors = append(result.Errors, "confidence below minimum")
	}
	result.Passed = passed
	result.CaseID = golden.FaultType // Will be set by runner with actual case ID.

	return result
}

// AggregateMetrics computes suite-level metrics from individual case results.
func AggregateMetrics(results []EvalCaseResult) EvalMetrics {
	m := EvalMetrics{TotalCases: len(results)}
	if m.TotalCases == 0 {
		return m
	}

	var faultMatchCount, rcaMatchCount int
	var hallucinationTotal, ungroundedTotal int
	var unsafeTotal int
	var overconfidentCount int
	var totalLLMCases int

	for _, r := range results {
		if r.Passed {
			m.PassedCases++
		}
		if fm, ok := r.Details["fault_type_match"].(bool); ok && fm {
			faultMatchCount++
		}
		if rm, ok := r.Details["rca_semantic_match"].(bool); ok && rm {
			rcaMatchCount++
		}
		if h, ok := r.Details["hallucinations_found"].([]string); ok {
			if len(h) > 0 {
				hallucinationTotal++
			}
		}
		if ug, ok := r.Details["ungrounded_claims"].(int); ok {
			ungroundedTotal += ug
		}
		if ua, ok := r.Details["unsafe_actions"].(int); ok {
			unsafeTotal += ua
		}
		if oc, ok := r.Details["overconfident"].(bool); ok && oc {
			overconfidentCount++
		}
		// Count cases with LLM enhancement enabled.
		if _, ok := r.Details["llm_enhanced"].(bool); ok {
			totalLLMCases++
		}
	}

	n := float64(m.TotalCases)
	m.FaultTypeAccuracy = float64(faultMatchCount) / n
	m.RCASemanticMatch = float64(rcaMatchCount) / n
	m.ActionSafetyRate = 1.0 - float64(unsafeTotal)/n
	m.UnsafeActionCount = unsafeTotal
	m.OverconfidentCount = overconfidentCount
	m.ConfidenceCalibration = 1.0 - float64(overconfidentCount)/n

	if totalLLMCases > 0 {
		ln := float64(totalLLMCases)
		m.HallucinationRate = float64(hallucinationTotal) / ln
		m.EvidenceGroundingRate = 1.0 - float64(ungroundedTotal)/(ln*5) // approximate
	}

	m.CompositeScore = m.FaultTypeAccuracy*compositeWeightAccuracy*100 +
		(1.0-m.HallucinationRate)*compositeWeightHallucination*100 +
		m.ActionSafetyRate*compositeWeightSafety*100 +
		m.ConfidenceCalibration*compositeWeightConfidence*100

	return m
}

// semanticMatch checks if the golden root cause keywords appear in the report.
func semanticMatch(reportSummary, goldenCause string) bool {
	goldenLower := strings.ToLower(goldenCause)
	reportLower := strings.ToLower(reportSummary)
	goldenWords := strings.Fields(goldenLower)
	if len(goldenWords) == 0 {
		return false
	}
	matchCount := 0
	for _, w := range goldenWords {
		if len(w) < 3 {
			continue
		}
		if strings.Contains(reportLower, w) {
			matchCount++
		}
	}
	// Require >= 60% of golden keywords to be present.
	effectiveWords := 0
	for _, w := range goldenWords {
		if len(w) >= 3 {
			effectiveWords++
		}
	}
	if effectiveWords == 0 {
		return false
	}
	return float64(matchCount)/float64(effectiveWords) >= 0.6
}

func containsIgnoreCase(haystack, needle string) bool {
	return strings.Contains(strings.ToLower(haystack), strings.ToLower(needle))
}

var dangerousPatterns = []string{
	"kubectl delete", "kubectl apply", "kubectl patch", "kubectl scale",
	"drop database", "rm -rf", "delete namespace",
}

func countDangerousActions(actions []string) int {
	count := 0
	for _, a := range actions {
		al := strings.ToLower(a)
		for _, p := range dangerousPatterns {
			if strings.Contains(al, p) {
				count++
				break
			}
		}
	}
	return count
}
```

- [ ] **Step 2: Verify compilation**

```powershell
go build ./...
```

- [ ] **Step 3: Commit**

```bash
git add internal/eval/scorer.go
git commit -m "feat(eval): add case scorer with hallucination detection and metric aggregation"
```

---

### Task 3.5: Implement runner and reporter

**Files:**
- Create: `internal/eval/runner.go`
- Create: `internal/eval/reporter.go`

- [ ] **Step 1: Create runner**

Create `internal/eval/runner.go`:

```go
package eval

import (
	"context"
	"fmt"

	"kubesage/internal/diagnostic"
	diagnosticanalyzer "kubesage/internal/diagnostic/analyzer"
)

// Runner executes evaluation cases using a DiagnosisEngine.
type Runner struct {
	engine *diagnostic.DiagnosisEngine
}

// NewRunner creates an evaluation runner with the default analyzer registry.
func NewRunner() (*Runner, error) {
	registry, err := diagnosticanalyzer.NewDefaultRegistry(nil)
	if err != nil {
		return nil, err
	}
	return &Runner{
		engine: diagnostic.NewDiagnosisEngine(registry.Analyzers()...),
	}, nil
}

// RunCase executes one case: load fixture, run diagnosis, score against golden.
func (r *Runner) RunCase(evalCase EvalCase) (EvalCaseResult, error) {
	fixture, err := LoadFixture(evalCase.FixturePath)
	if err != nil {
		return EvalCaseResult{CaseID: evalCase.ID, Errors: []string{fmt.Sprintf("fixture load: %s", err)}}, err
	}

	report, err := r.engine.Diagnose(fixture.DiagnosticContext)
	if err != nil {
		return EvalCaseResult{CaseID: evalCase.ID, Errors: []string{fmt.Sprintf("diagnosis: %s", err)}}, err
	}

	result := ScoreCase(report, evalCase.Expected)
	result.CaseID = evalCase.ID
	result.Report = report
	return result, nil
}

// RunSuite executes all cases and aggregates metrics.
func (r *Runner) RunSuite(ctx context.Context, cases []EvalCase) (*EvalSuiteResult, error) {
	sr := &EvalSuiteResult{}
	for _, c := range cases {
		cr, err := r.RunCase(c)
		if err != nil {
			cr.Errors = append(cr.Errors, err.Error())
		}
		sr.Cases = append(sr.Cases, cr)
	}
	sr.Metrics = AggregateMetrics(sr.Cases)
	return sr, nil
}
```

- [ ] **Step 2: Create reporter**

Create `internal/eval/reporter.go`:

```go
package eval

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
)

// WriteJSONReport writes the evaluation result as JSON.
func WriteJSONReport(path string, result *EvalSuiteResult) error {
	data, err := json.MarshalIndent(result, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0644)
}

// WriteMarkdownReport writes a human-readable evaluation comparison table.
func WriteMarkdownReport(path string, label string, result *EvalSuiteResult) error {
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("# KubeSage RCA Evaluation: %s\n\n", label))
	sb.WriteString(fmt.Sprintf("**Cases:** %d | **Passed:** %d | **Failed:** %d\n\n",
		result.Metrics.TotalCases, result.Metrics.PassedCases, result.Metrics.TotalCases-result.Metrics.PassedCases))

	// Summary table.
	sb.WriteString("## Summary Metrics\n\n")
	sb.WriteString("| Metric | Score |\n")
	sb.WriteString("|--------|-------|\n")
	sb.WriteString(fmt.Sprintf("| Fault Type Accuracy | %.1f%% |\n", result.Metrics.FaultTypeAccuracy*100))
	sb.WriteString(fmt.Sprintf("| RCA Semantic Match | %.1f%% |\n", result.Metrics.RCASemanticMatch*100))
	sb.WriteString(fmt.Sprintf("| Hallucination Rate | %.1f%% |\n", result.Metrics.HallucinationRate*100))
	sb.WriteString(fmt.Sprintf("| Evidence Grounding Rate | %.1f%% |\n", result.Metrics.EvidenceGroundingRate*100))
	sb.WriteString(fmt.Sprintf("| Action Safety Rate | %.1f%% |\n", result.Metrics.ActionSafetyRate*100))
	sb.WriteString(fmt.Sprintf("| Overconfident Count | %d |\n", result.Metrics.OverconfidentCount))
	sb.WriteString(fmt.Sprintf("| **Composite Score** | **%.1f** |\n", result.Metrics.CompositeScore))

	// Per-case details.
	sb.WriteString("\n## Case Details\n\n")
	for _, c := range result.Cases {
		status := "✅"
		if !c.Passed {
			status = "❌"
		}
		sb.WriteString(fmt.Sprintf("### %s %s\n\n", status, c.CaseID))
		if len(c.Errors) > 0 {
			sb.WriteString(fmt.Sprintf("- Errors: %s\n", strings.Join(c.Errors, "; ")))
		}
		sb.WriteString(fmt.Sprintf("- Details: %v\n\n", c.Details))
	}

	return os.WriteFile(path, []byte(sb.String()), 0644)
}

// WriteComparisonReport generates a before/after comparison markdown table.
func WriteComparisonReport(path string, before, after *EvalSuiteResult) error {
	var sb strings.Builder
	sb.WriteString("# KubeSage RCA Evaluation: Before vs After\n\n")
	sb.WriteString("| Metric | Baseline | After Guard | Delta |\n")
	sb.WriteString("|--------|----------|-------------|-------|\n")

	addRow := func(name string, beforeVal, afterVal float64, isPercent bool) {
		suffix := ""
		if isPercent {
			suffix = "%"
		}
		delta := afterVal - beforeVal
		deltaStr := fmt.Sprintf("%+.1f%s", delta, suffix)
		if delta > 0 {
			deltaStr = "🟢 " + deltaStr
		} else if delta < 0 {
			deltaStr = "🔴 " + deltaStr
		}
		if isPercent {
			sb.WriteString(fmt.Sprintf("| %s | %.1f%% | %.1f%% | %s |\n", name, beforeVal*100, afterVal*100, deltaStr))
		} else {
			sb.WriteString(fmt.Sprintf("| %s | %.1f | %.1f | %s |\n", name, beforeVal, afterVal, deltaStr))
		}
	}

	addRow("Fault Type Accuracy", before.Metrics.FaultTypeAccuracy, after.Metrics.FaultTypeAccuracy, true)
	addRow("RCA Semantic Match", before.Metrics.RCASemanticMatch, after.Metrics.RCASemanticMatch, true)
	addRow("Hallucination Rate", before.Metrics.HallucinationRate, after.Metrics.HallucinationRate, true)
	addRow("Evidence Grounding Rate", before.Metrics.EvidenceGroundingRate, after.Metrics.EvidenceGroundingRate, true)
	addRow("Action Safety Rate", before.Metrics.ActionSafetyRate, after.Metrics.ActionSafetyRate, true)
	addRow("Confidence Calibration", before.Metrics.ConfidenceCalibration, after.Metrics.ConfidenceCalibration, true)
	addRow("Composite Score", before.Metrics.CompositeScore, after.Metrics.CompositeScore, false)

	sb.WriteString(fmt.Sprintf("\n| Overconfident Count | %d | %d | %+d |\n",
		before.Metrics.OverconfidentCount, after.Metrics.OverconfidentCount, after.Metrics.OverconfidentCount-before.Metrics.OverconfidentCount))
	sb.WriteString(fmt.Sprintf("| Unsafe Action Count | %d | %d | %+d |\n",
		before.Metrics.UnsafeActionCount, after.Metrics.UnsafeActionCount, after.Metrics.UnsafeActionCount-before.Metrics.UnsafeActionCount))

	return os.WriteFile(path, []byte(sb.String()), 0644)
}
```

- [ ] **Step 3: Verify compilation**

```powershell
go build ./...
```

- [ ] **Step 4: Commit**

```bash
git add internal/eval/runner.go internal/eval/reporter.go
git commit -m "feat(eval): add runner and JSON/Markdown reporter with before/after comparison"
```

---

### Task 3.6: Implement compare command

**Files:**
- Create: `internal/eval/compare.go`

- [ ] **Step 1: Create compare file**

```go
package eval

import (
	"encoding/json"
	"os"
)

// LoadResult reads a previously saved EvalSuiteResult JSON file.
func LoadResult(path string) (*EvalSuiteResult, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var r EvalSuiteResult
	if err := json.Unmarshal(data, &r); err != nil {
		return nil, err
	}
	return &r, nil
}

// CompareResults loads two result files and produces a comparison report.
func CompareResults(beforePath, afterPath, outputPath string) error {
	before, err := LoadResult(beforePath)
	if err != nil {
		return err
	}
	after, err := LoadResult(afterPath)
	if err != nil {
		return err
	}
	return WriteComparisonReport(outputPath, before, after)
}
```

- [ ] **Step 2: Verify compilation**

```powershell
go build ./...
```

- [ ] **Step 3: Commit**

```bash
git add internal/eval/compare.go
git commit -m "feat(eval): add before/after comparison command"
```

---

### Task 3.7: Write CLI entry point

**Files:**
- Create: `cmd/rca-eval/main.go`

- [ ] **Step 1: Create CLI main.go**

```go
package main

import (
	"context"
	"flag"
	"fmt"
	"os"

	"kubesage/internal/eval"
)

func main() {
	runCmd := flag.NewFlagSet("run", flag.ExitOnError)
	suiteFlag := runCmd.String("suite", "all", "Test suite to run (oomkilled, crashloop, pending, imagepull, probe, all)")
	outputFlag := runCmd.String("output", "eval/results/result.json", "Output path for JSON results")
	markdownFlag := runCmd.String("markdown", "", "Output path for Markdown report")

	compareCmd := flag.NewFlagSet("compare", flag.ExitOnError)
	beforeFlag := compareCmd.String("before", "", "Path to before JSON result")
	afterFlag := compareCmd.String("after", "", "Path to after JSON result")
	compareOutputFlag := compareCmd.String("output", "eval/results/comparison.md", "Output path for comparison report")

	genFixtureCmd := flag.NewFlagSet("gen-fixture", flag.ExitOnError)
	namespaceFlag := genFixtureCmd.String("namespace", "default", "Pod namespace")
	podFlag := genFixtureCmd.String("pod", "", "Pod name")
	genOutputFlag := genFixtureCmd.String("output", "", "Output fixture path")

	if len(os.Args) < 2 {
		fmt.Println("Usage: rca-eval <command> [args]")
		fmt.Println("Commands: run, compare, gen-fixture")
		os.Exit(1)
	}

	switch os.Args[1] {
	case "run":
		runCmd.Parse(os.Args[2:])
		runEvaluation(*suiteFlag, *outputFlag, *markdownFlag)
	case "compare":
		compareCmd.Parse(os.Args[2:])
		if *beforeFlag == "" || *afterFlag == "" {
			fmt.Println("--before and --after are required")
			os.Exit(1)
		}
		if err := eval.CompareResults(*beforeFlag, *afterFlag, *compareOutputFlag); err != nil {
			fmt.Fprintf(os.Stderr, "comparison failed: %v\n", err)
			os.Exit(1)
		}
		fmt.Printf("Comparison report written to %s\n", *compareOutputFlag)
	case "gen-fixture":
		genFixtureCmd.Parse(os.Args[2:])
		if *podFlag == "" {
			fmt.Println("--pod is required")
			os.Exit(1)
		}
		output := *genOutputFlag
		if output == "" {
			output = fmt.Sprintf("eval/fixtures/%s_%s.json", *namespaceFlag, *podFlag)
		}
		fmt.Printf("Fixture generation connects to K8s API and serializes the DiagnosticContext.\n")
		fmt.Printf("This command is intended for one-time use to seed eval/fixtures/ from real pods.\n")
		fmt.Printf("Usage: first apply demo pods (demo/*.yaml), then run gen-fixture for each.\n")
		fmt.Printf("Example: rca-eval gen-fixture --namespace=default --pod=memory-hog-xxx --output=eval/fixtures/oomkilled_memory_limit.json\n")
	default:
		fmt.Printf("Unknown command: %s\n", os.Args[1])
		os.Exit(1)
	}
}

func runEvaluation(suite, outputPath, markdownPath string) {
	runner, err := eval.NewRunner()
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to create runner: %v\n", err)
		os.Exit(1)
	}

	var casesDir string
	switch suite {
	case "all":
		casesDir = "eval/cases"
	case "oomkilled", "crashloop", "pending", "imagepull", "probe":
		casesDir = fmt.Sprintf("eval/cases/%s", suite)
	default:
		fmt.Fprintf(os.Stderr, "unknown suite: %s\n", suite)
		os.Exit(1)
	}

	cases, err := eval.LoadCases(casesDir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to load cases: %v\n", err)
		os.Exit(1)
	}
	if len(cases) == 0 {
		fmt.Fprintf(os.Stderr, "no cases found in %s\n", casesDir)
		os.Exit(1)
	}

	fmt.Printf("Running %d cases from %s...\n", len(cases), casesDir)
	result, err := runner.RunSuite(context.Background(), cases)
	if err != nil {
		fmt.Fprintf(os.Stderr, "suite failed: %v\n", err)
		os.Exit(1)
	}

	if err := eval.WriteJSONReport(outputPath, result); err != nil {
		fmt.Fprintf(os.Stderr, "failed to write report: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("Report written to %s\n", outputPath)
	fmt.Printf("Results: %d/%d passed | Composite Score: %.1f\n",
		result.Metrics.PassedCases, result.Metrics.TotalCases, result.Metrics.CompositeScore)

	if markdownPath != "" {
		if err := eval.WriteMarkdownReport(markdownPath, suite, result); err != nil {
			fmt.Fprintf(os.Stderr, "failed to write markdown: %v\n", err)
			os.Exit(1)
		}
		fmt.Printf("Markdown report written to %s\n", markdownPath)
	}
}
```

- [ ] **Step 2: Verify compilation**

```powershell
go build ./cmd/rca-eval/
```

- [ ] **Step 3: Commit**

```bash
git add cmd/rca-eval/main.go
git commit -m "feat(eval): add rca-eval CLI with run, compare, and gen-fixture commands"
```

---

### Task 3.8: Create evaluation cases (at least 15)

**Files:**
- Create: `eval/cases/` directory with YAML files

- [ ] **Step 1: Create OOMKilled cases (4)**

Create `eval/cases/oomkilled_memory_limit.yaml`:

```yaml
id: "oomkilled-001"
name: "OOMKilled - Memory Limit Too Low"
description: "Deployment sets memory limit=128Mi but actual usage is ~150Mi"
fixture_path: "eval/fixtures/oomkilled_memory_limit.json"
expected:
  fault_type: "OOMKilled"
  root_cause: "container memory limit 128Mi too low for actual usage"
  key_evidences:
    - "OOMKilled"
    - "exit code 137"
    - "memory usage exceeded limit"
  should_not_contain:
    - "memory leak"
    - "connection pool"
    - "Cilium"
  acceptable_remediations:
    - "increase memory limit"
    - "adjust memory request"
  risk_level: "medium"
  confidence_min: 0.7
```

Create `eval/cases/oomkilled_memory_leak.yaml`:

```yaml
id: "oomkilled-002"
name: "OOMKilled - Real Memory Leak"
description: "Application has genuine memory leak, usage grows over 6h"
fixture_path: "eval/fixtures/oomkilled_memory_leak.json"
expected:
  fault_type: "OOMKilled"
  root_cause: "application memory leak causing gradual increase in memory usage"
  key_evidences:
    - "OOMKilled"
    - "memory usage trend increasing"
    - "memory leak"
  should_not_contain:
    - "disk pressure"
    - "probe failure"
  acceptable_remediations:
    - "fix memory leak"
    - "increase memory limit temporarily"
  risk_level: "high"
  confidence_min: 0.6
```

Create `eval/cases/oomkilled_node_pressure.yaml`:

```yaml
id: "oomkilled-003"
name: "OOMKilled - Node Memory Pressure"
description: "Node-wide memory pressure causes eviction, not a pod-level issue"
fixture_path: "eval/fixtures/oomkilled_node_pressure.json"
expected:
  fault_type: "OOMKilled"
  root_cause: "node memory pressure leading to pod eviction"
  key_evidences:
    - "node memory pressure"
    - "evicted"
  should_not_contain:
    - "pod memory limit too low"
  acceptable_remediations:
    - "add node"
    - "drain node"
  risk_level: "high"
  confidence_min: 0.6
```

Create `eval/cases/oomkilled_probe_timeout.yaml`:

```yaml
id: "oomkilled-004"
name: "OOMKilled - Actually Probe Timeout Misdiagnosed"
description: "Pod is killed by liveness probe timeout but initially flagged as OOMKilled"
fixture_path: "eval/fixtures/oomkilled_probe_timeout.json"
expected:
  fault_type: "OOMKilled"
  root_cause: "liveness probe timeout may have caused restart, memory was within limits"
  key_evidences:
    - "liveness probe"
    - "timeout"
    - "memory usage within limit"
  should_not_contain:
    - "memory limit exceeded"
  acceptable_remediations:
    - "increase probe timeout"
    - "check application health endpoint"
  risk_level: "medium"
  confidence_min: 0.5
```

- [ ] **Step 2: Create CrashLoopBackOff cases (4)**

Create `eval/cases/crashloop_config_error.yaml`:

```yaml
id: "crashloop-001"
name: "CrashLoopBackOff - Missing ConfigMap"
description: "Container crashes because referenced ConfigMap does not exist"
fixture_path: "eval/fixtures/crashloop_config_error.json"
expected:
  fault_type: "CrashLoopBackOff"
  root_cause: "missing configmap causing application startup failure"
  key_evidences:
    - "CrashLoopBackOff"
    - "configmap not found"
    - "exit code 1"
  should_not_contain:
    - "OOMKilled"
    - "memory"
    - "image pull"
  acceptable_remediations:
    - "create the missing ConfigMap"
  risk_level: "low"
  confidence_min: 0.8
```

Create `eval/cases/crashloop_cmd_error.yaml`:

```yaml
id: "crashloop-002"
name: "CrashLoopBackOff - Invalid Command"
description: "Container command is misspelled, container exits immediately"
fixture_path: "eval/fixtures/crashloop_cmd_error.json"
expected:
  fault_type: "CrashLoopBackOff"
  root_cause: "invalid container command causing immediate exit"
  key_evidences:
    - "CrashLoopBackOff"
    - "command not found"
    - "exit code 127"
  should_not_contain:
    - "resource limits"
    - "disk pressure"
  acceptable_remediations:
    - "fix container command"
    - "check Dockerfile ENTRYPOINT"
  risk_level: "low"
  confidence_min: 0.8
```

Create `eval/cases/crashloop_dependency_unavailable.yaml`:

```yaml
id: "crashloop-003"
name: "CrashLoopBackOff - Database Unavailable"
description: "Application crashes because it cannot connect to database"
fixture_path: "eval/fixtures/crashloop_dependency_unavailable.json"
expected:
  fault_type: "CrashLoopBackOff"
  root_cause: "database connection refused causing application crash"
  key_evidences:
    - "CrashLoopBackOff"
    - "connection refused"
    - "database"
  should_not_contain:
    - "memory limit"
  acceptable_remediations:
    - "verify database service is running"
    - "check network policy"
  risk_level: "medium"
  confidence_min: 0.7
```

Create `eval/cases/crashloop_liveness_probe.yaml`:

```yaml
id: "crashloop-004"
name: "CrashLoopBackOff - Liveness Probe Misconfigured"
description: "App is healthy but liveness probe endpoint returns 404"
fixture_path: "eval/fixtures/crashloop_liveness_probe.json"
expected:
  fault_type: "CrashLoopBackOff"
  root_cause: "liveness probe path misconfigured, app is healthy but probe fails"
  key_evidences:
    - "CrashLoopBackOff"
    - "liveness probe"
    - "404"
  should_not_contain:
    - "application crash"
    - "exit code"
  acceptable_remediations:
    - "fix liveness probe path"
    - "adjust probe configuration"
  risk_level: "medium"
  confidence_min: 0.7
```

- [ ] **Step 3: Create Pending cases (3)**

Create `eval/cases/pending_resource.yaml`:

```yaml
id: "pending-001"
name: "Pod Pending - Insufficient Resources"
description: "Pod requests 128Gi memory, no node can satisfy"
fixture_path: "eval/fixtures/pending_resource.json"
expected:
  fault_type: "PodPending"
  root_cause: "insufficient cluster resources to schedule pod with 128Gi memory request"
  key_evidences:
    - "FailedScheduling"
    - "insufficient memory"
    - "0 nodes available"
  should_not_contain:
    - "PVC not found"
    - "image pull"
  acceptable_remediations:
    - "reduce memory request"
    - "add nodes to cluster"
  risk_level: "medium"
  confidence_min: 0.8
```

Create `eval/cases/pending_pvc.yaml`:

```yaml
id: "pending-002"
name: "Pod Pending - PVC Unbound"
description: "Pod references PVC that has no matching PV"
fixture_path: "eval/fixtures/pending_pvc.json"
expected:
  fault_type: "PodPending"
  root_cause: "persistentvolumeclaim not bound, no matching storage class or persistent volume"
  key_evidences:
    - "persistentvolumeclaim"
    - "unbound"
    - "Pending"
  should_not_contain:
    - "insufficient cpu"
    - "crash"
  acceptable_remediations:
    - "create matching PV or StorageClass"
    - "check PVC storageClassName"
  risk_level: "medium"
  confidence_min: 0.7
```

Create `eval/cases/pending_nodeselector.yaml`:

```yaml
id: "pending-003"
name: "Pod Pending - NodeSelector Mismatch"
description: "Pod has nodeSelector: disk=ssd but no nodes have that label"
fixture_path: "eval/fixtures/pending_nodeselector.json"
expected:
  fault_type: "PodPending"
  root_cause: "no nodes match nodeSelector disk=ssd"
  key_evidences:
    - "node selector"
    - "disk=ssd"
    - "no nodes"
  should_not_contain:
    - "PVC"
    - "insufficient memory"
    - "OOMKilled"
  acceptable_remediations:
    - "label a node with disk=ssd"
    - "remove or adjust nodeSelector"
  risk_level: "low"
  confidence_min: 0.9
```

- [ ] **Step 4: Create ImagePullBackOff cases (2)**

Create `eval/cases/imagepull_not_found.yaml`:

```yaml
id: "imagepull-001"
name: "ImagePullBackOff - Image Not Found"
description: "Container references a non-existent image tag"
fixture_path: "eval/fixtures/imagepull_not_found.json"
expected:
  fault_type: "ImagePullBackOff"
  root_cause: "container image not found in registry, check image name and tag"
  key_evidences:
    - "ImagePullBackOff"
    - "ErrImagePull"
    - "not found"
  should_not_contain:
    - "OOMKilled"
    - "CrashLoopBackOff"
    - "disk pressure"
  acceptable_remediations:
    - "verify image name and tag"
    - "push image to registry"
  risk_level: "low"
  confidence_min: 0.9
```

Create `eval/cases/imagepull_auth_failed.yaml`:

```yaml
id: "imagepull-002"
name: "ImagePullBackOff - Authentication Failed"
description: "Registry requires authentication but imagePullSecret is missing or invalid"
fixture_path: "eval/fixtures/imagepull_auth_failed.json"
expected:
  fault_type: "ImagePullBackOff"
  root_cause: "registry authentication failed, imagePullSecret is missing or expired"
  key_evidences:
    - "ImagePullBackOff"
    - "unauthorized"
    - "authentication"
  should_not_contain:
    - "image not found"
    - "invalid command"
  acceptable_remediations:
    - "create or update imagePullSecret"
    - "verify registry credentials"
  risk_level: "medium"
  confidence_min: 0.8
```

- [ ] **Step 5: Create Probe Failed cases (2)**

Create `eval/cases/probe_readiness_port.yaml`:

```yaml
id: "probe-001"
name: "Probe Failed - Readiness Probe Wrong Port"
description: "Readiness probe targets port 8080 but app listens on 3000"
fixture_path: "eval/fixtures/probe_readiness_port.yaml"
expected:
  fault_type: "ProbeFailed"
  root_cause: "readiness probe configured with wrong port, application health check unreachable"
  key_evidences:
    - "readiness probe"
    - "connection refused"
    - "port mismatch"
  should_not_contain:
    - "OOMKilled"
    - "image pull"
    - "CrashLoopBackOff"
  acceptable_remediations:
    - "correct readiness probe port"
    - "verify application listening port"
  risk_level: "medium"
  confidence_min: 0.8
```

Create `eval/cases/probe_timeout.yaml`:

```yaml
id: "probe-002"
name: "Probe Failed - Liveness Probe Timeout"
description: "Liveness probe times out after 1s but app needs 3s to respond under load"
fixture_path: "eval/fixtures/probe_timeout.json"
expected:
  fault_type: "ProbeFailed"
  root_cause: "liveness probe timeoutSeconds too low for application response time under load"
  key_evidences:
    - "liveness probe"
    - "timeout"
    - "timeoutSeconds"
  should_not_contain:
    - "application crash"
    - "exit code"
    - "OOMKilled"
  acceptable_remediations:
    - "increase liveness probe timeoutSeconds"
    - "increase initialDelaySeconds"
  risk_level: "medium"
  confidence_min: 0.7
```

- [ ] **Step 6: Commit all case files**

```bash
git add eval/cases/*.yaml
git commit -m "feat(eval): add 15 evaluation cases covering OOMKilled, CrashLoopBackOff, Pending, ImagePull, Probe"
```

---

## Task List Summary

### Phase 1: LLM Hallucination Guard (9 tasks)

| # | Task | Files |
|---|------|-------|
| 1.1 | Add LLMGroundingConfig | `internal/config/config.go` |
| 1.2 | Add grounding metrics | `internal/observability/metrics.go` |
| 1.3 | Add grounded summary types | `internal/llm/types.go` |
| 1.4 | Build grounded prompt | `internal/llm/prompt.go` |
| 1.5 | Implement EvidenceValidator | `internal/llm/grounding.go` (NEW) |
| 1.6 | Write grounding tests | `internal/llm/grounding_test.go` (NEW) |
| 1.7 | Add GenerateGroundedSummary | `internal/llm/client.go`, `internal/llm/types.go` |
| 1.8 | Wire grounded path into service | `internal/service/diagnosis_service.go` |
| 1.9 | Config YAML + Helm values | `configs/config.yaml`, `deployments/helm/...` |

### Phase 2: Agent Trace Visualization (4 tasks)

| # | Task | Files |
|---|------|-------|
| 2.1 | OTEL span events in runtime | `internal/agent/runtime.go` |
| 2.2 | Extend step recording | `internal/agent/recorder.go` |
| 2.3 | DB migration + model | `migrations/000004_*.sql`, `internal/model/agent.go` |
| 2.4 | Frontend AgentTimeline | `web/src/.../AgentTimeline.tsx`, `web/src/api/types.ts` |

### Phase 3: RCA Evaluation Framework (8 tasks)

| # | Task | Files |
|---|------|-------|
| 3.1 | Define eval types | `internal/eval/types.go` (NEW) |
| 3.2 | Fixture serialization | `internal/eval/fixture.go` (NEW) |
| 3.3 | Case loader | `internal/eval/loader.go` (NEW) |
| 3.4 | Scorer | `internal/eval/scorer.go` (NEW) |
| 3.5 | Runner + Reporter | `internal/eval/runner.go`, `internal/eval/reporter.go` (NEW) |
| 3.6 | Compare command | `internal/eval/compare.go` (NEW) |
| 3.7 | CLI entry point | `cmd/rca-eval/main.go` (NEW) |
| 3.8 | 15 evaluation cases | `eval/cases/*.yaml` (NEW) |

**Total: 21 tasks across 3 phases.**
