package tool

import "context"

type K8STool struct{}

// Name returns the tool identifier.
func (t *K8STool) Name() string { return "kubernetes" }

// Execute is a placeholder for future Kubernetes tool execution.
func (t *K8STool) Execute(ctx context.Context, input map[string]interface{}) (*Result, error) {
	return &Result{Name: t.Name(), Data: input}, nil
}
