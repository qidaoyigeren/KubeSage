package diagnostic

type Analyzer interface {
	Name() string
	// Match only decides whether this analyzer should participate in RCA.
	Match(ctx *DiagnosticContext) bool
	// Analyze generates evidence and suggestions without changing cluster state.
	Analyze(ctx *DiagnosticContext) (*AnalyzeResult, error)
}
