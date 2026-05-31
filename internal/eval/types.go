package eval

import (
	"time"

	"kubesage/internal/diagnostic"
)

// EvalCase describes one offline RCA replay case backed by a serialized
// DiagnosticContext fixture and a human-reviewed golden answer.
type EvalCase struct {
	ID                 string       `json:"id" yaml:"id"`
	Name               string       `json:"name,omitempty" yaml:"name,omitempty"`
	Description        string       `json:"description,omitempty" yaml:"description,omitempty"`
	Fixture            string       `json:"fixture" yaml:"fixture"`
	Tags               []string     `json:"tags,omitempty" yaml:"tags,omitempty"`
	MaxDurationSeconds int          `json:"max_duration_seconds,omitempty" yaml:"max_duration_seconds,omitempty"`
	GoldenAnswer       GoldenAnswer `json:"golden_answer" yaml:"golden_answer"`

	SourcePath string `json:"-" yaml:"-"`
}

// GoldenAnswer is the stable expected RCA surface used by the scorer.
type GoldenAnswer struct {
	ExpectedFaultType      string   `json:"expected_fault_type" yaml:"expected_fault_type"`
	RootCause              string   `json:"root_cause" yaml:"root_cause"`
	KeyEvidences           []string `json:"key_evidences" yaml:"key_evidences"`
	ShouldNotContain       []string `json:"should_not_contain,omitempty" yaml:"should_not_contain,omitempty"`
	AcceptableRemediations []string `json:"acceptable_remediations" yaml:"acceptable_remediations"`
	ConfidenceMin          float64  `json:"confidence_min" yaml:"confidence_min"`
	ConfidenceMax          float64  `json:"confidence_max,omitempty" yaml:"confidence_max,omitempty"`
}

// EvalCaseResult stores the full scoring outcome for one case.
type EvalCaseResult struct {
	ID                       string             `json:"id"`
	Name                     string             `json:"name,omitempty"`
	Tags                     []string           `json:"tags,omitempty"`
	Fixture                  string             `json:"fixture"`
	ExpectedFaultType        string             `json:"expected_fault_type"`
	ActualFaultType          string             `json:"actual_fault_type"`
	RootCauseSummary         string             `json:"root_cause_summary"`
	Confidence               float64            `json:"confidence"`
	DurationMS               int64              `json:"duration_ms"`
	MaxDurationMS            int64              `json:"max_duration_ms,omitempty"`
	DurationMatched          bool               `json:"duration_matched"`
	FaultTypeMatched         bool               `json:"fault_type_matched"`
	RootCauseMatched         bool               `json:"root_cause_matched"`
	KeyEvidenceMatched       bool               `json:"key_evidence_matched"`
	RemediationMatched       bool               `json:"remediation_matched"`
	ConfidenceMatched        bool               `json:"confidence_matched"`
	HallucinationDetected    bool               `json:"hallucination_detected"`
	OverconfidenceDetected   bool               `json:"overconfidence_detected"`
	DangerousSuggestionCount int                `json:"dangerous_suggestion_count"`
	Passed                   bool               `json:"passed"`
	MissingEvidence          []string           `json:"missing_evidence,omitempty"`
	MissingRemediations      []string           `json:"missing_remediations,omitempty"`
	UnexpectedTerms          []string           `json:"unexpected_terms,omitempty"`
	DangerousSuggestions     []string           `json:"dangerous_suggestions,omitempty"`
	Errors                   []string           `json:"errors,omitempty"`
	Report                   *diagnostic.Report `json:"report,omitempty"`
	GoldenAnswer             GoldenAnswer       `json:"golden_answer"`
}

// EvalMetrics aggregates suite-level RCA quality, safety, and speed metrics.
type EvalMetrics struct {
	TotalCases               int     `json:"total_cases"`
	PassedCases              int     `json:"passed_cases"`
	FailedCases              int     `json:"failed_cases"`
	RCAAccuracy              float64 `json:"rca_accuracy"`
	FaultTypeAccuracy        float64 `json:"fault_type_accuracy"`
	RootCauseAccuracy        float64 `json:"root_cause_accuracy"`
	KeyEvidenceRecall        float64 `json:"key_evidence_recall"`
	RemediationRecall        float64 `json:"remediation_recall"`
	HallucinationRate        float64 `json:"hallucination_rate"`
	OverconfidenceRate       float64 `json:"overconfidence_rate"`
	SafetyPassRate           float64 `json:"safety_pass_rate"`
	DangerousSuggestionCount int     `json:"dangerous_suggestion_count"`
	AverageDurationMS        int64   `json:"average_duration_ms"`
	P95DurationMS            int64   `json:"p95_duration_ms"`
}

// EvalSuiteResult is the JSON artifact produced by rca-eval run.
type EvalSuiteResult struct {
	GeneratedAt time.Time        `json:"generated_at"`
	CasesPath   string           `json:"cases_path,omitempty"`
	Metrics     EvalMetrics      `json:"metrics"`
	Results     []EvalCaseResult `json:"results"`
}
