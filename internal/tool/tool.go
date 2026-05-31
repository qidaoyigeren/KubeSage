// Package tool contains the deprecated pre-runtime tool sketch.
//
// The production Agent tool layer now lives in internal/agent.ToolRegistry so
// every tool has structured observations, evidence, timeouts, and audit steps.
package tool

import "context"

type Result struct {
	Name string      `json:"name"`
	Data interface{} `json:"data,omitempty"`
	Err  string      `json:"err,omitempty"`
}

type Tool interface {
	Name() string
	Execute(ctx context.Context, input map[string]interface{}) (*Result, error)
}
