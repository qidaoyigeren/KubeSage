package llm

import (
	"context"

	"kubesage/internal/diagnostic"
)

// LLMClient hides the concrete OpenAI-compatible provider from diagnosis code.
type LLMClient interface {
	GenerateDiagnosisSummary(ctx context.Context, prompt Prompt) (*EnhancedSummary, error)
}

// Prompt stores the system and user messages sent to the chat API.
type Prompt struct {
	System string
	User   string
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

// RuleBasedResult preserves the deterministic analyzer output before LLM
// enhancement.
type RuleBasedResult struct {
	FaultType          string                         `json:"fault_type"`
	RootCauseSummary   string                         `json:"root_cause_summary"`
	ConfidenceScore    float64                        `json:"confidence_score"`
	ImpactAnalysis     string                         `json:"impact_analysis"`
	SuggestedActions   []string                       `json:"suggested_actions"`
	RemediationActions []diagnostic.RemediationAction `json:"remediation_actions"`
	RiskLevel          string                         `json:"risk_level"`
	NeedHumanConfirm   bool                           `json:"need_human_confirm"`
}
