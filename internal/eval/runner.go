package eval

import (
	"context"
	"fmt"
	"time"

	"kubesage/internal/diagnostic"
	diagnosticanalyzer "kubesage/internal/diagnostic/analyzer"
)

// RunnerOptions configures an offline fixture replay run.
type RunnerOptions struct {
	CasesPath          string
	DefaultTimeout     time.Duration
	IncludeFullReports bool
}

// RunSuite loads cases, replays fixtures through the DiagnosisEngine, and
// scores every report against the golden answers.
func RunSuite(ctx context.Context, opts RunnerOptions) (*EvalSuiteResult, error) {
	if opts.CasesPath == "" {
		return nil, fmt.Errorf("cases path is required")
	}
	cases, err := LoadCases(opts.CasesPath)
	if err != nil {
		return nil, err
	}
	return RunCases(ctx, cases, opts)
}

// RunCases replays already-loaded cases. It is useful for tests and custom
// callers that want to filter cases before running.
func RunCases(ctx context.Context, cases []EvalCase, opts RunnerOptions) (*EvalSuiteResult, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	results := make([]EvalCaseResult, 0, len(cases))
	for _, c := range cases {
		result := runCase(ctx, c, opts)
		if !opts.IncludeFullReports {
			result.Report = nil
		}
		results = append(results, result)
	}
	return &EvalSuiteResult{
		GeneratedAt: time.Now().UTC(),
		CasesPath:   opts.CasesPath,
		Metrics:     AggregateMetrics(results),
		Results:     results,
	}, nil
}

func runCase(parent context.Context, c EvalCase, opts RunnerOptions) EvalCaseResult {
	timeout := opts.DefaultTimeout
	if c.MaxDurationSeconds > 0 {
		timeout = time.Duration(c.MaxDurationSeconds) * time.Second
	}
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	ctx, cancel := context.WithTimeout(parent, timeout)
	defer cancel()

	start := time.Now()
	diagCtx, err := ReadFixture(c.Fixture, ctx)
	if err != nil {
		return ScoreReport(c, nil, time.Since(start).Milliseconds(), err)
	}
	report, err := diagnoseFixture(diagCtx)
	durationMS := time.Since(start).Milliseconds()
	return ScoreReport(c, report, durationMS, err)
}

func diagnoseFixture(ctx *diagnostic.DiagnosticContext) (*diagnostic.Report, error) {
	registry, err := diagnosticanalyzer.NewDefaultRegistry(nil)
	if err != nil {
		return nil, err
	}
	engine := diagnostic.NewDiagnosisEngine(registry.Analyzers()...)
	return engine.Diagnose(ctx)
}
