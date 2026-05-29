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

type Report struct {
	Namespace        string
	PodName          string
	FaultType        string
	RootCauseSummary string
	ConfidenceScore  float64
	Evidences        []EvidenceRecord
	ImpactAnalysis   string
	SuggestedActions []string
	RiskLevel        string
	NeedHumanConfirm bool
}
