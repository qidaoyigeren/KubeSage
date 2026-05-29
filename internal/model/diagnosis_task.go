package model

import "time"

const (
	TaskStatusPending = "pending"
	TaskStatusRunning = "running"
	TaskStatusSuccess = "success"
	TaskStatusFailed  = "failed"
)

type DiagnosisTask struct {
	ID               uint             `json:"id" gorm:"primaryKey"`
	Namespace        string           `json:"namespace" gorm:"size:128;index;not null"`
	PodName          string           `json:"pod_name" gorm:"size:255;index;not null"`
	Status           string           `json:"status" gorm:"size:32;index;not null"`
	AlertName        string           `json:"alert_name" gorm:"size:128;index"`
	AlertSeverity    string           `json:"alert_severity" gorm:"size:64;index"`
	FaultType        string           `json:"fault_type" gorm:"size:64;index"`
	RootCauseSummary string           `json:"root_cause_summary" gorm:"type:text"`
	ConfidenceScore  float64          `json:"confidence_score"`
	CreatedAt        time.Time        `json:"created_at"`
	UpdatedAt        time.Time        `json:"updated_at"`
	FinishedAt       *time.Time       `json:"finished_at"`
	Evidences        []Evidence       `json:"evidences,omitempty" gorm:"foreignKey:TaskID"`
	Report           *DiagnosisReport `json:"report,omitempty" gorm:"foreignKey:TaskID"`
}
