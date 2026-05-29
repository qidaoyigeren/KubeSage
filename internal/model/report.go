package model

import (
	"time"

	"gorm.io/gorm"
)

type DiagnosisReport struct {
	ID               uint       `json:"id" gorm:"primaryKey"`
	TaskID           uint       `json:"task_id" gorm:"uniqueIndex;not null"`
	Namespace        string     `json:"namespace" gorm:"size:128;index;not null"`
	PodName          string     `json:"pod_name" gorm:"size:255;index;not null"`
	FaultType        string     `json:"fault_type" gorm:"size:64;index"`
	RootCauseSummary string     `json:"root_cause_summary" gorm:"type:text"`
	ConfidenceScore  float64    `json:"confidence_score"`
	ImpactAnalysis   string     `json:"impact_analysis" gorm:"type:text"`
	SuggestedActions string     `json:"suggested_actions" gorm:"type:longtext"`
	RiskLevel        string     `json:"risk_level" gorm:"size:32"`
	NeedHumanConfirm bool       `json:"need_human_confirm"`
	CreatedAt        time.Time  `json:"created_at"`
	GeneratedAt      time.Time  `json:"generated_at" gorm:"-"`
	Evidences        []Evidence `json:"evidences,omitempty" gorm:"-"`
}

// AfterFind mirrors CreatedAt into GeneratedAt after GORM loads a report.
func (r *DiagnosisReport) AfterFind(tx *gorm.DB) error {
	r.GeneratedAt = r.CreatedAt
	return nil
}
