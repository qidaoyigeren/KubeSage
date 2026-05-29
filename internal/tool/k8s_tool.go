package tool

import "context"

type K8STool struct{}

func (t *K8STool) Name() string { return "kubernetes" }

func (t *K8STool) Execute(ctx context.Context, input map[string]interface{}) (*Result, error) {
	return &Result{Name: t.Name(), Data: input}, nil
}
