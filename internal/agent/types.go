package agent

import (
	"context"
	"sync"
	"time"

	"kubesage/internal/diagnostic"
	"kubesage/internal/model"
)

const (
	StagePlan         = "plan"
	StagePreAnalysis  = "pre_analysis"
	StageToolCall     = "tool_call"
	StageObservation  = "observation"
	StageReflection   = "reflection"
	StageDecision     = "decision"
	StageAction       = "action"
	StageVerification = "verification"

	StopReasonMaxSteps            = "max_steps"
	StopReasonConfirmedHypothesis = "confirmed_hypothesis"
	StopReasonTimeout             = "timeout"
	StopReasonNoEffectiveTool     = "no_effective_tool"
	StopReasonCriticalToolFailed  = "critical_tool_failed"
	StopReasonPlanComplete        = "plan_complete"
)

type Goal struct {
	Namespace         string
	PodName           string
	ContainerName     string
	ExpectedFault     string
	AlertName         string
	AlertSeverity     string
	IncludeLogs       bool
	IncludeEvents     bool
	IncludeMetrics    bool
	AlertTime         *time.Time
	HistoricalContext string `json:"historical_context,omitempty"` // injected by runtime from cross-session memory
}

type RuntimeOptions struct {
	TaskID              uint
	TraceID             string
	MaxSteps            int
	ToolTimeout         time.Duration
	MaxReflectionSteps  int
	MaxReflectionRounds int
	MaxToolCallsPerTool int
	MaxRunbookSearches  int
	MaxLLMTokens        int
	ReflectionTimeout   time.Duration
	EnableDryRunPreview bool
	Goal                Goal
	// ModelName is used to look up model-specific token profiles.
	// When set, the budget is configured with model-aware defaults.
	ModelName string
}

type Planner interface {
	BuildInitialPlan(ctx context.Context, goal Goal, tools []ToolMetadata) Plan
	AdjustPlan(plan *Plan, state *ToolState, last ToolResult, hypotheses []HypothesisScore)
}

type PlanClient interface {
	GenerateAgentPlan(ctx context.Context, prompt PlanPrompt) (Plan, error)
	GeneratePlanAdjustment(ctx context.Context, prompt AdjustmentPrompt) (Plan, error)
	GenerateReflection(ctx context.Context, prompt ReflectionPrompt) (ReflectionResult, error)
}

// TokenUsageReporter exposes cumulative LLM usage without coupling the Agent
// package to a concrete provider.
type TokenUsageReporter interface {
	CumulativeTokenUsage() int
}

type PlanPrompt struct {
	Goal   Goal           `json:"goal"`
	Tools  []ToolMetadata `json:"tools"`
	Safety []string       `json:"safety"`
}

type AdjustmentPrompt struct {
	CurrentPlan  Plan              `json:"current_plan"`
	Observations []string          `json:"observations"`
	Hypotheses   []HypothesisScore `json:"hypotheses"`
	Tools        []ToolMetadata    `json:"tools"`
	Safety       []string          `json:"safety"`
	Goal         Goal              `json:"goal"`
	Evidence     string            `json:"evidence,omitempty"`
}

// ReflectionResult is returned by ReflectivePlanner after LLM-driven reflection.
type ReflectionResult struct {
	ShouldContinue bool       `json:"should_continue"`
	Reason         string     `json:"reason"`
	NewSteps       []PlanStep `json:"new_steps,omitempty"`
}

// ReflectionPrompt contains the context for LLM reflection decisions.
type ReflectionPrompt struct {
	Plan         Plan              `json:"plan"`
	Goal         Goal              `json:"goal"`
	Hypotheses   []HypothesisScore `json:"hypotheses"`
	Tools        []ToolMetadata    `json:"tools"`
	Safety       []string          `json:"safety"`
	Evidence     string            `json:"evidence,omitempty"`
	Observations []string          `json:"observations,omitempty"`
}

// ReflectivePlanner extends Planner with LLM-driven reflection capabilities.
type ReflectivePlanner interface {
	Planner
	Reflect(ctx context.Context, plan Plan, state *ToolState, hypotheses []HypothesisScore) (ReflectionResult, error)
}

type SnapshotFunc func(context.Context, Goal) (*diagnostic.DiagnosticContext, error)

type Store interface {
	CreateStep(context.Context, *model.AgentStep) error
	CreateHypotheses(context.Context, []model.Hypothesis) error
	CreateRemediationExecutions(context.Context, []model.RemediationExecution) error
}

type Analyzer interface {
	Diagnose(*diagnostic.DiagnosticContext) (*diagnostic.Report, error)
}

type Retriever interface {
	Retrieve(ctx context.Context, faultType, query string, limit int) ([]RunbookHit, error)
}

type RunbookHit struct {
	Title            string   `json:"title"`
	Content          string   `json:"content"`
	Score            int      `json:"score"`
	RecommendedTools []string `json:"recommended_tools,omitempty"`
	StopConditions   []string `json:"stop_conditions,omitempty"`
}

type Plan struct {
	Summary              string     `json:"plan_summary"`
	Steps                []PlanStep `json:"steps"`
	ExpectedObservations []string   `json:"expected_observations"`
	StopCondition        []string   `json:"stop_condition"`
}

type PlanStep struct {
	ID            string                 `json:"id"`
	ToolName      string                 `json:"tool_name"`
	Input         map[string]interface{} `json:"input,omitempty"`
	Reason        string                 `json:"reason"`
	Critical      bool                   `json:"critical"`
	Completed     bool                   `json:"-"`
	Skipped       bool                   `json:"-"`
	AppendedBy    string                 `json:"appended_by,omitempty"`
	ParallelGroup string                 `json:"parallel_group,omitempty"`
}

type ToolMetadata struct {
	Name        string            `json:"name"`
	Description string            `json:"description"`
	InputSchema map[string]string `json:"input_schema"`
	RiskLevel   string            `json:"risk_level"`
	ReadOnly    bool              `json:"read_only"`
	Timeout     time.Duration     `json:"timeout"`
	Critical    bool              `json:"critical"`
}

type ToolResult struct {
	ToolName        string                      `json:"tool_name"`
	Success         bool                        `json:"success"`
	Observation     string                      `json:"observation"`
	ObservationData interface{}                 `json:"observation_data,omitempty"`
	Warnings        []string                    `json:"warnings,omitempty"`
	MissingEvidence []string                    `json:"missing_evidence,omitempty"`
	EvidenceRecords []diagnostic.EvidenceRecord `json:"evidence_records,omitempty"`
	Error           string                      `json:"error,omitempty"`
	DurationMS      int64                       `json:"duration_ms"`
	Data            interface{}                 `json:"data,omitempty"`
	StateDelta      *ToolStateDelta             `json:"-"`
}

type ToolStateDelta struct {
	Goal                  *Goal
	DiagnosticContext     *diagnostic.DiagnosticContext
	Report                *diagnostic.Report
	SetRunbookHits        bool
	RunbookHits           []RunbookHit
	SetRemediationActions bool
	RemediationActions    []diagnostic.RemediationAction
	SetExecutions         bool
	Executions            []model.RemediationExecution
	AppendExecutions      []model.RemediationExecution
}

type ToolState struct {
	mu                  sync.Mutex
	TaskID              uint
	Goal                Goal
	AvailableTools      []ToolMetadata
	Observations        []ObservationRecord
	EvidenceRecords     []diagnostic.EvidenceRecord
	DiagnosticContext   *diagnostic.DiagnosticContext
	Report              *diagnostic.Report
	RunbookHits         []RunbookHit
	RemediationActions  []diagnostic.RemediationAction
	Executions          []model.RemediationExecution
	CompletedTools      map[string]bool
	CompletedToolInputs map[string][]map[string]interface{}
	RunContext          context.Context
}

type ObservationRecord struct {
	ToolName        string    `json:"tool_name"`
	Success         bool      `json:"success"`
	Observation     string    `json:"observation"`
	Warnings        []string  `json:"warnings,omitempty"`
	MissingEvidence []string  `json:"missing_evidence,omitempty"`
	EvidenceRefs    []string  `json:"evidence_refs,omitempty"`
	Error           string    `json:"error,omitempty"`
	Timestamp       time.Time `json:"timestamp"`
}

type Tool interface {
	Metadata() ToolMetadata
	Execute(ctx context.Context, input map[string]interface{}, state *ToolState) ToolResult
}

// ReadOnlyToolState is an immutable snapshot of ToolState passed to tool
// function closures. Tools must not mutate this struct; they express state
// changes via ToolResult.StateDelta instead.
type ReadOnlyToolState struct {
	TaskID              uint
	Goal                Goal
	DiagnosticContext   *diagnostic.DiagnosticContext
	Report              *diagnostic.Report
	RunbookHits         []RunbookHit
	CompletedTools      map[string]bool
	CompletedToolInputs map[string][]map[string]interface{}
}

// GetTaskID returns the task identifier.
func (s *ReadOnlyToolState) GetTaskID() uint {
	if s == nil {
		return 0
	}
	return s.TaskID
}

// GetGoal returns a copy of the current goal.
func (s *ReadOnlyToolState) GetGoal() Goal {
	if s == nil {
		return Goal{}
	}
	return s.Goal
}

// GetDiagnosticContext returns the current diagnostic context snapshot.
func (s *ReadOnlyToolState) GetDiagnosticContext() *diagnostic.DiagnosticContext {
	if s == nil {
		return nil
	}
	return s.DiagnosticContext
}

// GetReport returns the current report snapshot.
func (s *ReadOnlyToolState) GetReport() *diagnostic.Report {
	if s == nil {
		return nil
	}
	return s.Report
}

// GetRunbookHits returns the current runbook hits.
func (s *ReadOnlyToolState) GetRunbookHits() []RunbookHit {
	if s == nil {
		return nil
	}
	return s.RunbookHits
}

// GetCompletedTools returns the set of completed tool names.
func (s *ReadOnlyToolState) GetCompletedTools() map[string]bool {
	if s == nil {
		return nil
	}
	return s.CompletedTools
}

// GetCompletedToolInputs returns the successful input variants recorded for
// each tool.
func (s *ReadOnlyToolState) GetCompletedToolInputs() map[string][]map[string]interface{} {
	if s == nil {
		return nil
	}
	return s.CompletedToolInputs
}

type RunResult struct {
	Report            *diagnostic.Report
	PreAnalysis       *PreAnalysisDecision
	DiagContext       *diagnostic.DiagnosticContext
	RunbookHits       []RunbookHit
	Hypotheses        []model.Hypothesis
	Executions        []model.RemediationExecution
	VerificationPlan  []VerificationPlan
	StopReason        string
	StepsExecuted     int
	PlanSummary       string   `json:"-"`
	PlannedToolNames  []string `json:"-"`
	ExecutedToolNames []string `json:"-"`
}

type VerificationPlan struct {
	ActionID         string `json:"action_id"`
	WhatToCheck      string `json:"what_to_check"`
	ToolToUse        string `json:"tool_to_use"`
	SuccessCondition string `json:"success_condition"`
	TimeoutSeconds   int    `json:"timeout_seconds"`
}

type ReportSnapshot struct {
	RuleBasedResult       interface{}                    `json:"rule_based_result,omitempty"`
	AgentExecutionSummary string                         `json:"agent_execution_summary"`
	Hypotheses            []model.Hypothesis             `json:"hypotheses"`
	EvidenceChain         []EvidenceRef                  `json:"evidence_chain"`
	PrimaryRootCause      *RootCauseFactor               `json:"primary_root_cause,omitempty"`
	ContributingFactors   []RootCauseFactor              `json:"contributing_factors,omitempty"`
	RootCauseEvidenceRefs []string                       `json:"root_cause_evidence_refs"`
	ConfidenceBreakdown   []ConfidenceComponent          `json:"confidence_breakdown"`
	MissingEvidence       []string                       `json:"missing_evidence"`
	RunbookGuidance       []RunbookHit                   `json:"runbook_guidance"`
	LLMEnhancedSummary    interface{}                    `json:"llm_enhanced_summary,omitempty"`
	RemediationActions    []diagnostic.RemediationAction `json:"remediation_actions"`
	RemediationExecutions []model.RemediationExecution   `json:"remediation_executions"`
	VerificationPlan      []VerificationPlan             `json:"verification_plan"`
	ResidualRisks         []string                       `json:"residual_risks"`
	StopReason            string                         `json:"stop_reason"`
}

type RootCauseFactor struct {
	HypothesisType  string   `json:"hypothesis_type"`
	Summary         string   `json:"summary"`
	ConfidenceScore float64  `json:"confidence_score"`
	Status          string   `json:"status"`
	EvidenceRefs    []string `json:"evidence_refs,omitempty"`
	MissingEvidence []string `json:"missing_evidence,omitempty"`
	Reason          string   `json:"reason,omitempty"`
}

type EvidenceRef struct {
	Ref        string `json:"ref"`
	SourceType string `json:"source_type"`
	Title      string `json:"title"`
	Severity   string `json:"severity"`
}

type ConfidenceComponent struct {
	Source       string   `json:"source"`
	Weight       float64  `json:"weight"`
	EvidenceRefs []string `json:"evidence_refs,omitempty"`
	Missing      bool     `json:"missing"`
	Reason       string   `json:"reason"`
}
