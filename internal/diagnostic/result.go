package diagnostic

type AnalyzeResult struct {
	AnalyzerName     string
	FaultType        string
	RootCauseSummary string
	ConfidenceScore  float64
	Evidences        []EvidenceRecord
	ImpactAnalysis   string
	SuggestedActions []string
	RiskLevel        string
	NeedHumanConfirm bool
}

type RootCauseFactor struct {
	AnalyzerName     string   `json:"analyzer_name,omitempty"`
	FaultType        string   `json:"fault_type"`
	Summary          string   `json:"summary"`
	ConfidenceScore  float64  `json:"confidence_score"`
	EvidenceRefs     []string `json:"evidence_refs,omitempty"`
	ContributingRole string   `json:"contributing_role,omitempty"`
}

// RemediationAction describes one advisory remediation step with risk metadata.
type RemediationAction struct {
	ActionID         string `json:"action_id,omitempty"`
	ActionType       string `json:"action_type"`
	Description      string `json:"description"`
	CommandPreview   string `json:"command_preview"`
	RiskLevel        string `json:"risk_level"`
	NeedHumanConfirm bool   `json:"need_human_confirm"`
	Executable       bool   `json:"executable"`
}

type Report struct {
	Namespace             string
	PodName               string
	FaultType             string
	RootCauseSummary      string
	ConfidenceScore       float64
	Evidences             []EvidenceRecord
	ImpactAnalysis        string
	SuggestedActions      []string
	RemediationActions    []RemediationAction
	RiskLevel             string
	NeedHumanConfirm      bool
	PrimaryRootCause      *RootCauseFactor
	ContributingFactors   []RootCauseFactor
	RuleBasedResult       interface{}
	LLMEnhancedSummary    interface{}
	AgentExecutionSummary string
	AgentReportSnapshot   interface{}
	VerificationPlan      interface{}
	ResidualRisks         []string
}
