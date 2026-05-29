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
