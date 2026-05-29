package tool

import "context"

type RunbookTool struct{}

func (t *RunbookTool) Name() string { return "runbook" }

func (t *RunbookTool) Execute(ctx context.Context, input map[string]interface{}) (*Result, error) {
	return &Result{Name: t.Name(), Data: "runbook keyword retriever is available in rag package"}, nil
}
