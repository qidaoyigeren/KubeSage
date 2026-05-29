package tool

import "context"

type PrometheusTool struct{}

// Name returns the tool identifier.
func (t *PrometheusTool) Name() string { return "prometheus" }

// Execute is a placeholder; metric enrichment currently uses prometheus.Client.
func (t *PrometheusTool) Execute(ctx context.Context, input map[string]interface{}) (*Result, error) {
	return &Result{Name: t.Name(), Data: "prometheus tool is reserved for metric RCA enrichment"}, nil
}
