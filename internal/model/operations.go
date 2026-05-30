package model

import "time"

const (
	FeedbackRatingUseful    = "useful"
	FeedbackRatingNotUseful = "not_useful"
)

type DiagnosisFeedback struct {
	ID                     uint      `json:"id" gorm:"primaryKey"`
	TaskID                 uint      `json:"task_id" gorm:"index;not null"`
	Rating                 string    `json:"rating" gorm:"size:32;index;not null"`
	CorrectedRootCause     string    `json:"corrected_root_cause" gorm:"type:text"`
	HelpfulEvidenceRefs    JSONText  `json:"helpful_evidence_refs" gorm:"type:longtext"`
	HelpfulToolNames       JSONText  `json:"helpful_tool_names" gorm:"type:longtext"`
	HelpfulHypothesisTypes JSONText  `json:"helpful_hypothesis_types" gorm:"type:longtext"`
	Comment                string    `json:"comment" gorm:"type:text"`
	CreatedBy              string    `json:"created_by" gorm:"size:128;index"`
	CreatedAt              time.Time `json:"created_at"`
}

type AuditLog struct {
	ID           uint      `json:"id" gorm:"primaryKey"`
	Actor        string    `json:"actor" gorm:"size:128;index"`
	Action       string    `json:"action" gorm:"size:128;index;not null"`
	Namespace    string    `json:"namespace" gorm:"size:128;index"`
	ResourceKind string    `json:"resource_kind" gorm:"size:64;index"`
	ResourceName string    `json:"resource_name" gorm:"size:255;index"`
	TaskID       *uint     `json:"task_id" gorm:"index"`
	Summary      string    `json:"summary" gorm:"type:text"`
	MetadataJSON JSONText  `json:"metadata_json" gorm:"type:longtext"`
	CreatedAt    time.Time `json:"created_at"`
}

type Runbook struct {
	ID        uint           `json:"id" gorm:"primaryKey"`
	FaultType string         `json:"fault_type" gorm:"size:128;index;not null"`
	Title     string         `json:"title" gorm:"size:255;not null"`
	Content   string         `json:"content" gorm:"type:longtext"`
	HintsJSON JSONText       `json:"hints_json" gorm:"type:longtext"`
	Version   int            `json:"version" gorm:"not null;default:1"`
	CreatedBy string         `json:"created_by" gorm:"size:128;index"`
	UpdatedBy string         `json:"updated_by" gorm:"size:128;index"`
	CreatedAt time.Time      `json:"created_at"`
	UpdatedAt time.Time      `json:"updated_at"`
	Chunks    []RunbookChunk `json:"chunks,omitempty" gorm:"foreignKey:RunbookID"`
}

type RunbookChunk struct {
	ID         uint      `json:"id" gorm:"primaryKey"`
	RunbookID  uint      `json:"runbook_id" gorm:"index;not null"`
	ChunkIndex int       `json:"chunk_index" gorm:"not null"`
	Title      string    `json:"title" gorm:"size:255"`
	Content    string    `json:"content" gorm:"type:longtext"`
	VectorID   string    `json:"vector_id" gorm:"size:255;index"`
	CreatedAt  time.Time `json:"created_at"`
}

type DiagnosisQueueDeadLetter struct {
	ID          uint      `json:"id" gorm:"primaryKey"`
	Stream      string    `json:"stream" gorm:"size:128;index;not null"`
	MessageID   string    `json:"message_id" gorm:"size:128;index"`
	TaskID      uint      `json:"task_id" gorm:"index"`
	PayloadJSON JSONText  `json:"payload_json" gorm:"type:longtext"`
	Error       string    `json:"error" gorm:"type:text"`
	Attempts    int       `json:"attempts"`
	CreatedAt   time.Time `json:"created_at"`
}

type LLMUsageRecord struct {
	ID               uint      `json:"id" gorm:"primaryKey"`
	TaskID           uint      `json:"task_id" gorm:"index"`
	Provider         string    `json:"provider" gorm:"size:64;index"`
	Model            string    `json:"model" gorm:"size:128;index"`
	PromptTokens     int       `json:"prompt_tokens"`
	CompletionTokens int       `json:"completion_tokens"`
	TotalTokens      int       `json:"total_tokens"`
	LatencyMS        int64     `json:"latency_ms"`
	EstimatedCost    float64   `json:"estimated_cost"`
	CreatedAt        time.Time `json:"created_at"`
}
