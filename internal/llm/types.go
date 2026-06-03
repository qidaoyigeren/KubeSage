package llm

import (
	"context"
	"strings"

	"kubesage/internal/diagnostic"
)

// LLMClient hides the concrete OpenAI-compatible provider from diagnosis code.
type LLMClient interface {
	GenerateDiagnosisSummary(ctx context.Context, prompt Prompt) (*EnhancedSummary, error)
}

type GroundedSummaryGenerator interface {
	GenerateGroundedSummary(ctx context.Context, prompt GroundedPrompt) (*GroundedSummary, error)
}

type UsageRecord struct {
	Provider         string
	Model            string
	PromptTokens     int
	CompletionTokens int
	TotalTokens      int
	LatencyMS        int64
	EstimatedCost    float64
}

type UsageReporter interface {
	LastUsage() UsageRecord
}

// Prompt stores the system and user messages sent to the chat API.
type Prompt struct {
	System string
	User   string
}

type GroundedPrompt struct {
	Prompt
	RuleDiagnosis RuleBasedResult     `json:"rule_diagnosis"`
	Evidences     []GroundingEvidence `json:"evidences"`
}

type GroundingEvidence struct {
	ID         string `json:"evidence_id"`
	SourceType string `json:"source_type"`
	Title      string `json:"title"`
	Content    string `json:"content"`
	Severity   string `json:"severity"`
}

// EnhancedSummary is the strict JSON schema expected from the LLM.
type EnhancedSummary struct {
	RootCauseSummary  string   `json:"root_cause_summary"`
	ConfidenceScore   float64  `json:"confidence_score"`
	EvidenceReasoning string   `json:"evidence_reasoning"`
	SuggestedActions  []string `json:"suggested_actions"`
	RiskLevel         string   `json:"risk_level"`
	NeedHumanConfirm  bool     `json:"need_human_confirm"`
}

type GroundedSummary struct {
	RootCauseConfirmation  RootCauseConfirmation `json:"root_cause_confirmation"`
	EvidenceChain          []GroundedClaim       `json:"evidence_chain"`
	AdditionalObservations []GroundedObservation `json:"additional_observations,omitempty"`
	QualityWarnings        []string              `json:"quality_warnings,omitempty"`
}

type RootCauseConfirmation struct {
	AgreementLevel string   `json:"agreement_level"`
	Summary        string   `json:"summary"`
	EvidenceRefs   []string `json:"evidence_refs"`
}

type GroundedClaim struct {
	Claim          string   `json:"claim"`
	EvidenceRefs   []string `json:"evidence_refs"`
	GroundingScore float64  `json:"grounding_score,omitempty"`
	Verified       bool     `json:"verified"`
}

type GroundedObservation struct {
	Text         string   `json:"text"`
	EvidenceRefs []string `json:"evidence_refs,omitempty"`
}

// RuleBasedResult preserves the deterministic analyzer output before LLM
// enhancement.
type RuleBasedResult struct {
	FaultType           string                         `json:"fault_type"`
	RootCauseSummary    string                         `json:"root_cause_summary"`
	ConfidenceScore     float64                        `json:"confidence_score"`
	ImpactAnalysis      string                         `json:"impact_analysis"`
	SuggestedActions    []string                       `json:"suggested_actions"`
	RemediationActions  []diagnostic.RemediationAction `json:"remediation_actions"`
	RiskLevel           string                         `json:"risk_level"`
	NeedHumanConfirm    bool                           `json:"need_human_confirm"`
	PrimaryRootCause    *diagnostic.RootCauseFactor    `json:"primary_root_cause,omitempty"`
	ContributingFactors []diagnostic.RootCauseFactor   `json:"contributing_factors,omitempty"`
}

func (s GroundedSummary) ToEnhancedSummary(rule RuleBasedResult) *EnhancedSummary {
	parts := make([]string, 0, len(s.EvidenceChain)+len(s.QualityWarnings)+1)
	if s.RootCauseConfirmation.Summary != "" {
		parts = append(parts, s.RootCauseConfirmation.Summary)
	}
	for _, claim := range s.EvidenceChain {
		if claim.Claim == "" {
			continue
		}
		suffix := ""
		if len(claim.EvidenceRefs) > 0 {
			suffix = " [" + strings.Join(claim.EvidenceRefs, ",") + "]"
		}
		if !claim.Verified {
			suffix += " (unverified)"
		}
		parts = append(parts, claim.Claim+suffix)
	}
	for _, warning := range s.QualityWarnings {
		if warning != "" {
			parts = append(parts, "quality warning: "+warning)
		}
	}
	return &EnhancedSummary{
		RootCauseSummary:  rule.RootCauseSummary,
		ConfidenceScore:   rule.ConfidenceScore,
		EvidenceReasoning: strings.Join(parts, "\n"),
		SuggestedActions:  append([]string(nil), rule.SuggestedActions...),
		RiskLevel:         rule.RiskLevel,
		NeedHumanConfirm:  true,
	}
}
