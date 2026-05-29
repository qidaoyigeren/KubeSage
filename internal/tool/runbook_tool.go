package tool

import "context"

type RunbookTool struct{}

// Name returns the tool identifier.
func (t *RunbookTool) Name() string { return "runbook" }

// Execute is a placeholder; runbook retrieval is implemented in the rag package.
func (t *RunbookTool) Execute(ctx context.Context, input map[string]interface{}) (*Result, error) {
	return &Result{Name: t.Name(), Data: "runbook keyword retriever is available in rag package"}, nil
}
