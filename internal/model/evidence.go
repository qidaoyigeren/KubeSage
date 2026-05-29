package model

import "time"

type Evidence struct {
	ID         uint      `json:"id" gorm:"primaryKey"`
	TaskID     uint      `json:"task_id" gorm:"index;not null"`
	SourceType string    `json:"source_type" gorm:"size:64;index;not null"`
	Title      string    `json:"title" gorm:"size:255;not null"`
	Content    string    `json:"content" gorm:"type:longtext"`
	Severity   string    `json:"severity" gorm:"size:32;index"`
	RawJSON    string    `json:"raw_json" gorm:"type:longtext"`
	CreatedAt  time.Time `json:"created_at"`
}
