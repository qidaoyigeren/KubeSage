package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"kubesage/internal/agent"
	"kubesage/internal/config"
	"kubesage/internal/diagnostic"
	diagnosticanalyzer "kubesage/internal/diagnostic/analyzer"
	"kubesage/internal/k8s"
	"kubesage/internal/llm"
	"kubesage/internal/loki"
	"kubesage/internal/model"
	"kubesage/internal/prometheus"
	"kubesage/internal/service"

	"go.uber.org/zap"
)

type TestCase struct {
	Pod        string `json:"pod"`
	Expected   string `json:"expected"`
	LabelFault string `json:"label_fault"`
	CaseID     string `json:"case_id"`
	Namespace  string `json:"namespace"`
}

type CaseResult struct {
	CaseID         string   `json:"case_id"`
	Pod            string   `json:"pod"`
	Expected       string   `json:"expected"`
	Actual         string   `json:"actual"`
	Match          bool     `json:"match"`
	Confidence     float64  `json:"confidence"`
	RootCause      string   `json:"root_cause"`
	StopReason     string   `json:"stop_reason"`
	StepsExecuted  int      `json:"steps_executed"`
	ElapsedMS      int64    `json:"elapsed_ms"`
	PlannerSummary string   `json:"planner_summary"`
	PlannedTools   []string `json:"planned_tools"`
	ExecutedTools  []string `json:"executed_tools"`
	Hypotheses     []string `json:"hypotheses"`
	Error          string   `json:"error,omitempty"`
}

type Summary struct {
	GeneratedAt  string       `json:"generated_at"`
	TotalCases   int          `json:"total_cases"`
	MatchedCases int          `json:"matched_cases"`
	FailedCases  int          `json:"failed_cases"`
	ErrorCases   int          `json:"error_cases"`
	Accuracy     float64      `json:"accuracy"`
	AvgElapsedMS int64        `json:"avg_elapsed_ms"`
	Results      []CaseResult `json:"results"`
}

func main() {
	casesPath := flag.String("cases", "real-agent-cases.json", "path to test cases JSON")
	configPath := flag.String("config", "configs/config.yaml", "path to config.yaml")
	namespace := flag.String("namespace", "eval-bank", "default namespace for test pods")
	maxCases := flag.Int("max", 0, "max number of cases to run (0 = all)")
	outPath := flag.String("out", "real-agent-llm-results.json", "output results JSON path")
	summaryPath := flag.String("summary", "real-agent-llm-summary.md", "output summary markdown path")
	flag.Parse()

	if err := run(*casesPath, *configPath, *namespace, *maxCases, *outPath, *summaryPath); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}

func run(casesPath, configPath, namespace string, maxCases int, outPath, summaryPath string) error {
	// Load test cases.
	casesData, err := os.ReadFile(casesPath)
	if err != nil {
		return fmt.Errorf("read cases: %w", err)
	}
	// Strip UTF-8 BOM if present.
	if len(casesData) >= 3 && casesData[0] == 0xEF && casesData[1] == 0xBB && casesData[2] == 0xBF {
		casesData = casesData[3:]
	}
	var cases []TestCase
	if err := json.Unmarshal(casesData, &cases); err != nil {
		return fmt.Errorf("parse cases: %w", err)
	}
	// Fill default namespace.
	for i := range cases {
		if cases[i].Namespace == "" {
			cases[i].Namespace = namespace
		}
	}
	if maxCases > 0 && len(cases) > maxCases {
		cases = cases[:maxCases]
	}
	fmt.Fprintf(os.Stderr, "Loaded %d test cases\n", len(cases))

	// Load config.
	cfg, err := config.Load(configPath)
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}

	// Create logger.
	log, _ := zap.NewDevelopment()

	// Create K8s client.
	k8sClient, err := k8s.NewClient(cfg.Kubernetes)
	if err != nil {
		return fmt.Errorf("k8s client: %w", err)
	}

	// Create LLM client.
	var llmClient llm.LLMClient
	if cfg.LLM.Enabled {
		llmClient = llm.NewOpenAICompatibleClient(cfg.LLM)
		fmt.Fprintf(os.Stderr, "LLM client enabled: model=%s base_url=%s\n", cfg.LLM.Model, cfg.LLM.BaseURL)
	} else {
		fmt.Fprintf(os.Stderr, "WARNING: LLM disabled in config, will use rule planner\n")
	}

	// Create optional clients.
	var lokiClient *loki.Client
	if cfg.Loki.BaseURL != "" || cfg.Loki.Address != "" {
		lokiClient = loki.NewClient(cfg.Loki)
	}
	var promClient *prometheus.Client
	if cfg.Prometheus.BaseURL != "" || cfg.Prometheus.Address != "" {
		promClient = prometheus.NewClient(cfg.Prometheus)
	}

	// Create snapshot service.
	snapshotSvc := service.NewSnapshotService(cfg, k8sClient, log, lokiClient, promClient)

	// Create analyzer engine.
	registry, err := diagnosticanalyzer.NewDefaultRegistry(promClient)
	if err != nil {
		return fmt.Errorf("analyzer registry: %w", err)
	}
	engine := diagnostic.NewDiagnosisEngine(registry.Analyzers()...)

	// Create planner.
	var planner agent.Planner
	if cfg.Planner.Type == "llm" && llmClient != nil {
		if pc, ok := llmClient.(agent.PlanClient); ok {
			planner = agent.NewLLMPlanner(pc)
			fmt.Fprintf(os.Stderr, "Using LLM planner\n")
		} else {
			planner = agent.NewLLMPlanner()
			fmt.Fprintf(os.Stderr, "WARNING: LLM client does not implement PlanClient, using rule fallback\n")
		}
	} else {
		planner = agent.NewRulePlanner()
		fmt.Fprintf(os.Stderr, "Using rule planner\n")
	}

	// Create tool registry.
	snapshotFunc := func(ctx context.Context, goal agent.Goal) (*diagnostic.DiagnosticContext, error) {
		req := service.PodDiagnosisRequest{
			Namespace:     goal.Namespace,
			PodName:       goal.PodName,
			ContainerName: goal.ContainerName,
			ExpectedFault: goal.ExpectedFault,
			IncludeLogs:   true,
			IncludeEvents: true,
		}
		return snapshotSvc.Collect(ctx, req)
	}
	toolTimeout := time.Duration(cfg.Agent.ToolTimeoutSeconds) * time.Second
	if toolTimeout <= 0 {
		toolTimeout = 10 * time.Second
	}
	toolRegistry, err := agent.NewDefaultRegistry(agent.RegistryOptions{
		Snapshot:        snapshotFunc,
		ConfigInspector: snapshotSvc,
		Retriever:       noopRetriever{},
		Policy:          agent.NewRemediationPolicy(false),
		Prometheus:      promClient,
		Loki:            lokiClient,
		ToolTimeout:     toolTimeout,
	})
	if err != nil {
		return fmt.Errorf("tool registry: %w", err)
	}

	// Create runtime.
	runtime := agent.NewRuntime(agent.RuntimeDeps{
		Store:    noopStore{},
		Registry: toolRegistry,
		Analyzer: engine,
		Policy:   agent.NewRemediationPolicy(false),
		Planner:  planner,
	})

	maxSteps := cfg.Agent.MaxSteps
	if maxSteps <= 0 {
		maxSteps = 12
	}

	// Run test cases.
	results := make([]CaseResult, 0, len(cases))
	for i, tc := range cases {
		fmt.Fprintf(os.Stderr, "\n[%d/%d] Running case: %s (pod=%s expected=%s)\n",
			i+1, len(cases), tc.CaseID, tc.Pod, tc.Expected)

		result := runCase(runtime, tc, maxSteps, toolTimeout, cfg.Agent)
		results = append(results, result)

		status := "✓"
		if !result.Match {
			status = "✗"
		}
		if result.Error != "" {
			status = "ERROR"
		}
		fmt.Fprintf(os.Stderr, "  %s actual=%s confidence=%.2f elapsed=%dms planner=%s\n",
			status, result.Actual, result.Confidence, result.ElapsedMS,
			truncStr(result.PlannerSummary, 60))
	}

	// Write results JSON.
	resultsJSON, _ := json.MarshalIndent(results, "", "  ")
	if err := os.WriteFile(outPath, resultsJSON, 0644); err != nil {
		return fmt.Errorf("write results: %w", err)
	}
	fmt.Fprintf(os.Stderr, "\nResults written to %s\n", outPath)

	// Build and write summary.
	summary := buildSummary(results)
	summaryJSON, _ := json.MarshalIndent(summary, "", "  ")
	summaryJSONPath := strings.TrimSuffix(outPath, filepath.Ext(outPath)) + "-summary.json"
	if err := os.WriteFile(summaryJSONPath, summaryJSON, 0644); err != nil {
		return fmt.Errorf("write summary JSON: %w", err)
	}

	if summaryPath != "" {
		if err := writeMarkdownSummary(summaryPath, summary); err != nil {
			return fmt.Errorf("write summary markdown: %w", err)
		}
		fmt.Fprintf(os.Stderr, "Summary written to %s\n", summaryPath)
	}

	fmt.Fprintf(os.Stderr, "\n=== Results: %d/%d matched, accuracy=%.1f%%, avg=%dms ===\n",
		summary.MatchedCases, summary.TotalCases, summary.Accuracy*100, summary.AvgElapsedMS)

	return nil
}

func runCase(rt *agent.Runtime, tc TestCase, maxSteps int, toolTimeout time.Duration, cfg config.AgentConfig) CaseResult {
	start := time.Now()
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	goal := agent.Goal{
		Namespace:     tc.Namespace,
		PodName:       tc.Pod,
		ExpectedFault: tc.Expected,
	}

	result, err := rt.Run(ctx, agent.RuntimeOptions{
		Goal:                goal,
		MaxSteps:            maxSteps,
		ToolTimeout:         toolTimeout,
		MaxReflectionSteps:  cfg.MaxReflectionSteps,
		MaxReflectionRounds: cfg.MaxReflectionRounds,
		MaxToolCallsPerTool: cfg.MaxToolCallsPerTool,
		MaxRunbookSearches:  cfg.MaxRunbookSearches,
		MaxLLMTokens:        cfg.MaxLLMTokens,
		ReflectionTimeout:   time.Duration(cfg.ReflectionTimeoutSeconds) * time.Second,
	})
	elapsed := time.Since(start)

	cr := CaseResult{
		CaseID:    tc.CaseID,
		Pod:       tc.Pod,
		Expected:  tc.Expected,
		ElapsedMS: elapsed.Milliseconds(),
	}

	if err != nil {
		cr.Error = err.Error()
		return cr
	}

	if result.Report != nil {
		cr.Actual = result.Report.FaultType
		cr.Confidence = result.Report.ConfidenceScore
		cr.RootCause = result.Report.RootCauseSummary
	}
	cr.StopReason = result.StopReason
	cr.StepsExecuted = result.StepsExecuted
	cr.PlannerSummary = result.PlanSummary
	cr.PlannedTools = result.PlannedToolNames
	cr.ExecutedTools = result.ExecutedToolNames

	// Match expected vs actual with fault-type equivalence.
	cr.Match = faultTypesMatch(tc.Expected, cr.Actual)

	// Extract hypotheses.
	if result.Hypotheses != nil {
		for _, h := range result.Hypotheses {
			cr.Hypotheses = append(cr.Hypotheses,
				fmt.Sprintf("%s=%.2f(%s)", h.HypothesisType, h.ConfidenceScore, h.Status))
		}
	}

	return cr
}

func normalizeFaultName(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	s = strings.ReplaceAll(s, "_", "")
	s = strings.ReplaceAll(s, "-", "")
	s = strings.ReplaceAll(s, " ", "")
	s = strings.ReplaceAll(s, ":", "")
	return s
}

// faultTypesMatch checks if expected and actual fault types are equivalent,
// handling naming convention differences (e.g. "Init:Error" vs "InitContainerError",
// "Pending" vs "PodPending", and composite faults like "CrashLoopBackOff,ProbeFailed").
func faultTypesMatch(expected, actual string) bool {
	if strings.EqualFold(strings.TrimSpace(expected), strings.TrimSpace(actual)) {
		return true
	}
	expNorm := normalizeFaultName(expected)
	actNorm := normalizeFaultName(actual)
	if expNorm == actNorm {
		return true
	}
	// Map common equivalences.
	equivalences := map[string][]string{
		"initcontainererror": {"initerror", "initcontainererror"},
		"podpending":         {"pending", "podpending"},
		"imagepullbackoff":   {"imagepullbackoff", "errimagepull"},
		"oomkilled":          {"oomkilled", "oom"},
		"evicted":            {"evicted"},
		"crashloopbackoff":   {"crashloopbackoff"},
		"probefailed":        {"probefailed", "livenesscheckfailed", "readinesscheckfailed"},
		"nodenotready":       {"nodenotready"},
	}
	// Find canonical form for expected.
	expCanonical := ""
	for canonical, aliases := range equivalences {
		for _, alias := range aliases {
			if alias == expNorm {
				expCanonical = canonical
				break
			}
		}
		if expCanonical != "" {
			break
		}
	}
	if expCanonical == "" {
		expCanonical = expNorm
	}
	// Check if actual contains the expected canonical form (handles composite faults).
	if strings.Contains(actNorm, expCanonical) {
		return true
	}
	// Also check the reverse.
	actCanonical := ""
	for canonical, aliases := range equivalences {
		for _, alias := range aliases {
			if alias == actNorm {
				actCanonical = canonical
				break
			}
		}
		if actCanonical != "" {
			break
		}
	}
	if actCanonical == "" {
		actCanonical = actNorm
	}
	if expCanonical == actCanonical {
		return true
	}
	// Check if expected is an alias of the actual canonical (e.g. "initerror" is alias of "initcontainererror").
	if aliases, ok := equivalences[actCanonical]; ok {
		for _, alias := range aliases {
			if alias == expNorm {
				return true
			}
		}
	}
	// Check if actual is an alias of the expected canonical.
	if aliases, ok := equivalences[expCanonical]; ok {
		for _, alias := range aliases {
			if alias == actNorm {
				return true
			}
		}
	}
	return false
}

func buildSummary(results []CaseResult) Summary {
	summary := Summary{
		GeneratedAt: time.Now().Format(time.RFC3339),
		TotalCases:  len(results),
		Results:     results,
	}
	var totalElapsed int64
	for _, r := range results {
		if r.Error != "" {
			summary.ErrorCases++
		} else if r.Match {
			summary.MatchedCases++
		} else {
			summary.FailedCases++
		}
		totalElapsed += r.ElapsedMS
	}
	if summary.TotalCases > 0 {
		summary.AvgElapsedMS = totalElapsed / int64(summary.TotalCases)
		if summary.ErrorCases == 0 {
			summary.Accuracy = float64(summary.MatchedCases) / float64(summary.TotalCases)
		}
	}
	return summary
}

func writeMarkdownSummary(path string, summary Summary) error {
	var sb strings.Builder
	sb.WriteString("# Real-Agent LLM Planner Evaluation\n\n")
	sb.WriteString(fmt.Sprintf("Generated: %s\n\n", summary.GeneratedAt))
	sb.WriteString("## Summary\n\n")
	sb.WriteString(fmt.Sprintf("| Metric | Value |\n"))
	sb.WriteString(fmt.Sprintf("|--------|-------|\n"))
	sb.WriteString(fmt.Sprintf("| Total Cases | %d |\n", summary.TotalCases))
	sb.WriteString(fmt.Sprintf("| Matched | %d |\n", summary.MatchedCases))
	sb.WriteString(fmt.Sprintf("| Failed | %d |\n", summary.FailedCases))
	sb.WriteString(fmt.Sprintf("| Errors | %d |\n", summary.ErrorCases))
	sb.WriteString(fmt.Sprintf("| Accuracy | %.1f%% |\n", summary.Accuracy*100))
	sb.WriteString(fmt.Sprintf("| Avg Latency | %dms |\n", summary.AvgElapsedMS))
	sb.WriteString("\n## Cases\n\n")
	sb.WriteString("| # | Case | Expected | Actual | Match | Confidence | Latency | Stop Reason |\n")
	sb.WriteString("|---|------|----------|--------|-------|------------|---------|-------------|\n")
	for i, r := range summary.Results {
		match := "✓"
		if r.Error != "" {
			match = "ERROR"
		} else if !r.Match {
			match = "✗"
		}
		sb.WriteString(fmt.Sprintf("| %d | %s | %s | %s | %s | %.2f | %dms | %s |\n",
			i+1, r.CaseID, r.Expected, r.Actual, match, r.Confidence, r.ElapsedMS, r.StopReason))
	}
	return os.WriteFile(path, []byte(sb.String()), 0644)
}

func truncStr(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}

// noopStore implements agent.Store with no-op methods.
type noopStore struct{}

func (noopStore) CreateStep(context.Context, *model.AgentStep) error         { return nil }
func (noopStore) CreateHypotheses(context.Context, []model.Hypothesis) error { return nil }
func (noopStore) CreateRemediationExecutions(context.Context, []model.RemediationExecution) error {
	return nil
}

// noopRetriever implements agent.Retriever with no results.
type noopRetriever struct{}

func (noopRetriever) Retrieve(ctx context.Context, faultType, query string, limit int) ([]agent.RunbookHit, error) {
	return nil, nil
}
