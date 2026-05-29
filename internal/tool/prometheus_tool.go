package tool

import "context"

type PrometheusTool struct{}

func (t *PrometheusTool) Name() string { return "prometheus" }

func (t *PrometheusTool) Execute(ctx context.Context, input map[string]interface{}) (*Result, error) {
	return &Result{Name: t.Name(), Data: "prometheus tool is reserved for metric RCA enrichment"}, nil
}
