package model

import "time"

const (
	AgentStepStatusSuccess = "success"
	AgentStepStatusFailed  = "failed"
	AgentStepStatusSkipped = "skipped"

	HypothesisStatusActive    = "active"
	HypothesisStatusRejected  = "rejected"
	HypothesisStatusConfirmed = "confirmed"

	RemediationExecutionStatusProposed        = "proposed"
	RemediationExecutionStatusBlocked         = "blocked"
	RemediationExecutionStatusDryRunPending   = "dry_run_pending"
	RemediationExecutionStatusDryRunSuccess   = "dry_run_success"
	RemediationExecutionStatusDryRunFailed    = "dry_run_failed"
	RemediationExecutionStatusPendingApproval = "pending_approval"
)

// AgentStep records one auditable step in an agent diagnosis run.
type AgentStep struct {
	ID               uint      `json:"id" gorm:"primaryKey"`
	TaskID           uint      `json:"task_id" gorm:"index;not null"`
	ParentStepID     *uint     `json:"parent_step_id,omitempty" gorm:"index"`
	TraceID          string    `json:"trace_id" gorm:"size:64;index"`
	StepIndex        int       `json:"step_index" gorm:"index;not null"`
	Stage            string    `json:"stage" gorm:"size:32;index;not null"`
	ToolName         string    `json:"tool_name,omitempty" gorm:"size:128;index"`
	InputJSON        JSONText  `json:"input_json,omitempty" gorm:"type:longtext"`
	OutputJSON       JSONText  `json:"output_json,omitempty" gorm:"type:longtext"`
	Status           string    `json:"status" gorm:"size:32;index;not null"`
	DurationMS       int64     `json:"duration_ms"`
	ReasoningSummary string    `json:"reasoning_summary" gorm:"type:text"`
	CreatedAt        time.Time `json:"created_at"`
}

// Hypothesis stores a candidate root cause and the evidence references used to
// score it. Evidence refs are stable strings, not database evidence IDs.
type Hypothesis struct {
	ID                        uint      `json:"id" gorm:"primaryKey"`
	TaskID                    uint      `json:"task_id" gorm:"index;not null"`
	HypothesisType            string    `json:"hypothesis_type" gorm:"size:128;index;not null"`
	Summary                   string    `json:"summary" gorm:"type:text"`
	ConfidenceScore           float64   `json:"confidence_score"`
	Status                    string    `json:"status" gorm:"size:32;index;not null"`
	SupportingEvidenceRefs    JSONText  `json:"supporting_evidence_refs" gorm:"type:longtext"`
	ContradictingEvidenceRefs JSONText  `json:"contradicting_evidence_refs" gorm:"type:longtext"`
	MissingEvidence           JSONText  `json:"missing_evidence" gorm:"type:longtext"`
	RejectedReason            string    `json:"rejected_reason,omitempty" gorm:"type:text"`
	UpdatedAt                 time.Time `json:"updated_at"`
	CreatedAt                 time.Time `json:"created_at"`
}

// RemediationExecution tracks the MVP lifecycle for policy-vetted remediation
// proposals. It intentionally has no real executed state.
type RemediationExecution struct {
	ID             uint      `json:"id" gorm:"primaryKey"`
	TaskID         uint      `json:"task_id" gorm:"index;not null"`
	ActionID       string    `json:"action_id" gorm:"size:128;index;not null"`
	Status         string    `json:"status" gorm:"size:32;index;not null"`
	RiskLevel      string    `json:"risk_level" gorm:"size:32;index"`
	CommandPreview string    `json:"command_preview" gorm:"type:text"`
	DryRunOutput   string    `json:"dry_run_output" gorm:"type:longtext"`
	ApprovalBy     string    `json:"approval_by,omitempty" gorm:"size:128"`
	CreatedAt      time.Time `json:"created_at"`
	UpdatedAt      time.Time `json:"updated_at"`
}
