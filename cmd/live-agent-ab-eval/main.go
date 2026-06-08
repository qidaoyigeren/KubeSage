package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"kubesage/internal/agent"
	"kubesage/internal/config"
	"kubesage/internal/diagnostic"
	diagnosticanalyzer "kubesage/internal/diagnostic/analyzer"
	evalpkg "kubesage/internal/eval"
	"kubesage/internal/k8s"
	"kubesage/internal/llm"
	"kubesage/internal/loki"
	"kubesage/internal/model"
	"kubesage/internal/prometheus"
	"kubesage/internal/service"

	"go.uber.org/zap"
	corev1 "k8s.io/api/core/v1"
)

const (
	armRule   = "rule_baseline"
	armDirect = "direct_llm"
	armAgent  = "agent"
)

var (
	prometheusSamplesPattern = regexp.MustCompile(`samples=(\d+)`)
	lokiEntriesPattern       = regexp.MustCompile(`entries=(\d+)`)
)

type usageStats struct {
	Calls            int   `json:"calls"`
	PromptTokens     int   `json:"prompt_tokens"`
	CompletionTokens int   `json:"completion_tokens"`
	TotalTokens      int   `json:"total_tokens"`
	LLMLatencyMS     int64 `json:"llm_latency_ms"`
}

type sourceStats struct {
	Events               int `json:"events"`
	KubernetesLogBytes   int `json:"kubernetes_log_bytes"`
	LokiContainers       int `json:"loki_containers"`
	LokiBytes            int `json:"loki_bytes"`
	PrometheusQueries    int `json:"prometheus_queries"`
	PrometheusSamples    int `json:"prometheus_samples"`
	PrometheusQueryFails int `json:"prometheus_query_failures"`
	PVCs                 int `json:"pvcs"`
	NodeSnapshots        int `json:"node_snapshots"`
}

type toolStats struct {
	Planned        []string `json:"planned,omitempty"`
	Executed       []string `json:"executed,omitempty"`
	Failed         []string `json:"failed,omitempty"`
	Calls          int      `json:"calls"`
	Failures       int      `json:"failures"`
	PrometheusUsed bool     `json:"prometheus_used"`
	LokiUsed       bool     `json:"loki_used"`
}

type armResult struct {
	ActualFaultType    string      `json:"actual_fault_type"`
	RootCauseSummary   string      `json:"root_cause_summary"`
	Confidence         float64     `json:"confidence"`
	Passed             bool        `json:"passed"`
	FaultTypeMatched   bool        `json:"fault_type_matched"`
	RootCauseMatched   bool        `json:"root_cause_matched"`
	KeyEvidenceMatched bool        `json:"key_evidence_matched"`
	RemediationMatched bool        `json:"remediation_matched"`
	Hallucination      bool        `json:"hallucination_detected"`
	Overconfidence     bool        `json:"overconfidence_detected"`
	SafetyPassed       bool        `json:"safety_passed"`
	DurationMS         int64       `json:"duration_ms"`
	Usage              usageStats  `json:"usage"`
	Sources            sourceStats `json:"sources"`
	Tools              toolStats   `json:"tools,omitempty"`
	StopReason         string      `json:"stop_reason,omitempty"`
	StepsExecuted      int         `json:"steps_executed,omitempty"`
	MissingEvidence    []string    `json:"missing_evidence,omitempty"`
	MissingRemediation []string    `json:"missing_remediation,omitempty"`
	Errors             []string    `json:"errors,omitempty"`
}

type caseResult struct {
	ID         string               `json:"id"`
	ScenarioID string               `json:"scenario_id"`
	Iteration  int                  `json:"iteration"`
	Name       string               `json:"name"`
	Expected   string               `json:"expected_fault_type"`
	Live       sourceStats          `json:"live_snapshot"`
	Arms       map[string]armResult `json:"arms"`
}

type armAggregate struct {
	Metrics             evalpkg.EvalMetrics `json:"metrics"`
	TotalTokens         int                 `json:"total_tokens"`
	AverageTokens       int                 `json:"average_tokens"`
	TotalLLMCalls       int                 `json:"total_llm_calls"`
	ToolCalls           int                 `json:"tool_calls"`
	ToolFailures        int                 `json:"tool_failures"`
	PrometheusCaseHits  int                 `json:"prometheus_case_hits"`
	LokiCaseHits        int                 `json:"loki_case_hits"`
	PrometheusToolCases int                 `json:"prometheus_tool_cases"`
	LokiToolCases       int                 `json:"loki_tool_cases"`
}

type evaluationResult struct {
	GeneratedAt       time.Time               `json:"generated_at"`
	Namespace         string                  `json:"namespace"`
	CasesPath         string                  `json:"cases_path"`
	UniqueScenarios   int                     `json:"unique_scenarios"`
	RequestedRuns     int                     `json:"requested_runs"`
	Methodology       []string                `json:"methodology"`
	Arms              map[string]armAggregate `json:"arms"`
	RepeatConsistency map[string]float64      `json:"repeat_consistency"`
	Cases             []caseResult            `json:"cases"`
	ResumeEvidence    string                  `json:"resume_evidence"`
}

type scheduledCase struct {
	Case      evalpkg.EvalCase
	SampleID  string
	Iteration int
}

type memoryStore struct {
	steps      []model.AgentStep
	hypotheses []model.Hypothesis
	executions []model.RemediationExecution
}

func (s *memoryStore) CreateStep(_ context.Context, step *model.AgentStep) error {
	step.ID = uint(len(s.steps) + 1)
	s.steps = append(s.steps, *step)
	return nil
}

func (s *memoryStore) CreateHypotheses(_ context.Context, hypotheses []model.Hypothesis) error {
	s.hypotheses = append(s.hypotheses, hypotheses...)
	return nil
}

func (s *memoryStore) CreateRemediationExecutions(_ context.Context, executions []model.RemediationExecution) error {
	s.executions = append(s.executions, executions...)
	return nil
}

type noopRetriever struct{}

func (noopRetriever) Retrieve(context.Context, string, string, int) ([]agent.RunbookHit, error) {
	return nil, nil
}

func main() {
	configPath := flag.String("config", "configs/config.yaml", "configuration file")
	casesPath := flag.String("cases", "eval/cases/core.yaml", "comma-separated golden case files; fixtures are not read")
	namespace := flag.String("namespace", "eval-bank", "namespace containing live fault pods")
	maxCases := flag.Int("max", 0, "maximum cases to run; 0 runs all")
	samples := flag.Int("samples", 0, "exact number of balanced online runs; 0 runs each scenario once")
	resume := flag.Bool("resume", false, "resume completed sample IDs from the existing output JSON")
	caseTimeout := flag.Duration("case-timeout", 3*time.Minute, "timeout per Agent case")
	outPath := flag.String("out", "artifacts/live-agent-ab-results.json", "JSON output")
	summaryPath := flag.String("summary", "artifacts/live-agent-ab-summary.md", "Markdown output")
	flag.Parse()

	if err := run(*configPath, *casesPath, *namespace, *maxCases, *samples, *resume, *caseTimeout, *outPath, *summaryPath); err != nil {
		fmt.Fprintf(os.Stderr, "live Agent A/B evaluation failed: %v\n", err)
		os.Exit(1)
	}
}

func run(configPath, casesPath, namespace string, maxCases, samples int, resume bool, caseTimeout time.Duration, outPath, summaryPath string) error {
	cfg, err := config.Load(configPath)
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}
	if !cfg.LLM.Enabled {
		return fmt.Errorf("llm must be enabled for direct LLM and Agent arms")
	}
	if strings.TrimSpace(cfg.LLM.APIKey) == "" {
		return fmt.Errorf("llm api key is empty")
	}
	if caseTimeout <= 0 {
		caseTimeout = 3 * time.Minute
	}

	cases, err := loadCasePaths(casesPath)
	if err != nil {
		return fmt.Errorf("load cases: %w", err)
	}
	if maxCases > 0 && len(cases) > maxCases {
		cases = cases[:maxCases]
	}
	schedule := buildSchedule(cases, samples)
	if len(schedule) == 0 {
		return fmt.Errorf("no scheduled cases")
	}

	k8sClient, err := k8s.NewClient(cfg.Kubernetes)
	if err != nil {
		return fmt.Errorf("create Kubernetes client: %w", err)
	}
	var lokiClient *loki.Client
	if cfg.Loki.Enabled || cfg.Loki.BaseURL != "" || cfg.Loki.Address != "" {
		lokiClient = loki.NewClient(cfg.Loki)
	}
	var promClient *prometheus.Client
	if cfg.Prometheus.BaseURL != "" || cfg.Prometheus.Address != "" {
		promClient = prometheus.NewClient(cfg.Prometheus)
	}
	snapshotService := service.NewSnapshotService(cfg, k8sClient, zap.NewNop(), lokiClient, promClient)
	analyzerRegistry, err := diagnosticanalyzer.NewDefaultRegistry(promClient)
	if err != nil {
		return fmt.Errorf("create analyzer registry: %w", err)
	}
	engine := diagnostic.NewDiagnosisEngine(analyzerRegistry.Analyzers()...)

	result := evaluationResult{
		GeneratedAt:     time.Now(),
		Namespace:       namespace,
		CasesPath:       casesPath,
		UniqueScenarios: len(cases),
		RequestedRuns:   len(schedule),
		Methodology: []string{
			"All evidence is collected from the live Kubernetes cluster; fixture files are not loaded.",
			"Evaluation labels under kubesage.io are removed before diagnosis.",
			"Live Pod and synthetic Node names are stable opaque hashes, so resource names do not expose the expected fault type.",
			"Rule and Direct LLM arms receive the same full live snapshot.",
			"Agent receives only namespace and pod name, then selects registered read-only tools through Plan-Execute-Reflect.",
			"Prometheus and Loki usage is counted only when live samples or log bytes are returned.",
			fmt.Sprintf("%d online runs are balanced across %d unique live fault scenarios; repeated runs are reported as repeated measurements, not unique incidents.", len(schedule), len(cases)),
		},
		Arms:              map[string]armAggregate{},
		RepeatConsistency: map[string]float64{},
		Cases:             make([]caseResult, 0, len(schedule)),
	}
	if resume {
		resumed, resumeErr := readEvaluationResult(outPath)
		if resumeErr != nil && !os.IsNotExist(resumeErr) {
			return fmt.Errorf("resume output: %w", resumeErr)
		}
		if resumeErr == nil {
			if resumed.Namespace != namespace || resumed.CasesPath != casesPath || resumed.RequestedRuns != len(schedule) {
				return fmt.Errorf("resume output does not match namespace, cases path, or requested runs")
			}
			result = *resumed
			fmt.Fprintf(os.Stderr, "resuming %d/%d completed runs\n", len(result.Cases), len(schedule))
		}
	}
	completed := make(map[string]bool, len(result.Cases))
	for _, item := range result.Cases {
		completed[item.ID] = true
	}

	for index, scheduled := range schedule {
		if completed[scheduled.SampleID] {
			continue
		}
		evalCase := scheduled.Case
		scenarioID := evalCase.ID
		podName := evalpkg.LiveResourceName(scenarioID)
		evalCase.ID = scheduled.SampleID
		evalCase.MaxDurationSeconds = 0
		fmt.Fprintf(os.Stderr, "[%d/%d] live case %s scenario=%s pod=%s iteration=%d\n", index+1, len(schedule), scheduled.SampleID, scenarioID, podName, scheduled.Iteration)

		snapshotCtx, cancelSnapshot := context.WithTimeout(context.Background(), caseTimeout)
		snapshotStart := time.Now()
		snapshot, snapshotErr := snapshotService.Collect(snapshotCtx, service.PodDiagnosisRequest{
			Namespace:      namespace,
			PodName:        podName,
			IncludeEvents:  true,
			IncludeLogs:    true,
			IncludeMetrics: true,
		})
		cancelSnapshot()
		snapshotDuration := time.Since(snapshotStart).Milliseconds()
		if snapshotErr == nil {
			redactEvaluationLabels(snapshot)
		}

		caseOutput := caseResult{
			ID:         scheduled.SampleID,
			ScenarioID: scenarioID,
			Iteration:  scheduled.Iteration,
			Name:       evalCase.Name,
			Expected:   evalCase.GoldenAnswer.ExpectedFaultType,
			Live:       collectSourceStats(snapshot),
			Arms:       map[string]armResult{},
		}

		ruleReport, ruleErr := diagnoseRule(engine, snapshot, snapshotErr)
		ruleScore := evalpkg.ScoreReport(evalCase, ruleReport, snapshotDuration, ruleErr)
		caseOutput.Arms[armRule] = armResultFromScore(ruleScore, usageStats{}, caseOutput.Live, toolStats{}, "", 0)

		directReport, directUsage, directDuration, directErr := diagnoseDirect(cfg, snapshot, snapshotErr, caseTimeout)
		directScore := evalpkg.ScoreReport(evalCase, directReport, directDuration, directErr)
		caseOutput.Arms[armDirect] = armResultFromScore(directScore, directUsage, caseOutput.Live, toolStats{}, "", 0)

		agentReport, agentUsage, agentSources, agentTools, stopReason, steps, agentDuration, agentErr := diagnoseAgent(
			cfg,
			snapshotService,
			engine,
			promClient,
			lokiClient,
			namespace,
			podName,
			uint(index+1),
			caseTimeout,
		)
		agentScore := evalpkg.ScoreReport(evalCase, agentReport, agentDuration, agentErr)
		caseOutput.Arms[armAgent] = armResultFromScore(agentScore, agentUsage, agentSources, agentTools, stopReason, steps)

		result.Cases = append(result.Cases, caseOutput)
		fmt.Fprintf(
			os.Stderr,
			"  rule=%s direct=%s agent=%s tokens(direct=%d agent=%d) sources(prom=%d loki=%dB)\n",
			passLabel(ruleScore),
			passLabel(directScore),
			passLabel(agentScore),
			directUsage.TotalTokens,
			agentUsage.TotalTokens,
			caseOutput.Live.PrometheusSamples,
			caseOutput.Live.LokiBytes,
		)
		finalizeResult(&result)
		if err := writeJSON(outPath, result); err != nil {
			return fmt.Errorf("write checkpoint: %w", err)
		}
		if err := writeMarkdown(summaryPath, result); err != nil {
			return fmt.Errorf("write summary checkpoint: %w", err)
		}
	}

	finalizeResult(&result)

	if err := writeJSON(outPath, result); err != nil {
		return err
	}
	if err := writeMarkdown(summaryPath, result); err != nil {
		return err
	}
	fmt.Fprintf(os.Stderr, "wrote %s and %s\n", outPath, summaryPath)
	return nil
}

func loadCasePaths(raw string) ([]evalpkg.EvalCase, error) {
	var cases []evalpkg.EvalCase
	seen := map[string]bool{}
	for _, path := range strings.Split(raw, ",") {
		path = strings.TrimSpace(path)
		if path == "" {
			continue
		}
		loaded, err := evalpkg.LoadCases(path)
		if err != nil {
			return nil, err
		}
		for _, item := range loaded {
			if seen[item.ID] {
				return nil, fmt.Errorf("duplicate case id %s", item.ID)
			}
			seen[item.ID] = true
			cases = append(cases, item)
		}
	}
	return cases, nil
}

func buildSchedule(cases []evalpkg.EvalCase, samples int) []scheduledCase {
	if len(cases) == 0 {
		return nil
	}
	if samples <= 0 {
		samples = len(cases)
	}
	schedule := make([]scheduledCase, 0, samples)
	for index := 0; index < samples; index++ {
		item := cases[index%len(cases)]
		iteration := index/len(cases) + 1
		sampleID := item.ID
		if samples != len(cases) {
			sampleID = fmt.Sprintf("%s-run-%02d", item.ID, iteration)
		}
		schedule = append(schedule, scheduledCase{
			Case:      item,
			SampleID:  sampleID,
			Iteration: iteration,
		})
	}
	return schedule
}

func readEvaluationResult(path string) (*evaluationResult, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var result evaluationResult
	if err := json.Unmarshal(data, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

func finalizeResult(result *evaluationResult) {
	if result == nil {
		return
	}
	if result.Arms == nil {
		result.Arms = map[string]armAggregate{}
	}
	if result.RepeatConsistency == nil {
		result.RepeatConsistency = map[string]float64{}
	}
	for _, armName := range []string{armRule, armDirect, armAgent} {
		result.Arms[armName] = aggregateArm(armName, result.Cases)
		result.RepeatConsistency[armName] = repeatConsistency(armName, result.Cases)
	}
	result.ResumeEvidence = buildResumeEvidenceV2(*result)
}

func repeatConsistency(armName string, cases []caseResult) float64 {
	outputs := map[string]map[string]bool{}
	for _, item := range cases {
		run, ok := item.Arms[armName]
		if !ok || item.ScenarioID == "" {
			continue
		}
		if outputs[item.ScenarioID] == nil {
			outputs[item.ScenarioID] = map[string]bool{}
		}
		outputs[item.ScenarioID][run.ActualFaultType] = true
	}
	if len(outputs) == 0 {
		return 0
	}
	stable := 0
	for _, values := range outputs {
		if len(values) == 1 {
			stable++
		}
	}
	return float64(stable) / float64(len(outputs))
}

func buildResumeEvidenceV2(result evaluationResult) string {
	rule := result.Arms[armRule]
	direct := result.Arms[armDirect]
	agentArm := result.Arms[armAgent]
	return fmt.Sprintf(
		"在 %d 个独立真实故障场景上执行 %d 次 Rule / Direct LLM / Agent 在线 A/B，统一从 Kubernetes、Prometheus、Loki 实时采证并过滤答案标签；Agent 相比 Direct LLM 将故障类型准确率从 %.1f%% 提升至 %.1f%%、幻觉率从 %.1f%% 降至 %.1f%%、过度自信率从 %.1f%% 降至 %.1f%%，重复诊断输出一致性 %.1f%%；同时量化平均 token %d、平均延迟 %dms，并据规则基线 %.1f%% 严格 RCA / %dms 延迟确定规则快路径 + Agent 低置信慢路径。",
		result.UniqueScenarios,
		len(result.Cases),
		direct.Metrics.FaultTypeAccuracy*100,
		agentArm.Metrics.FaultTypeAccuracy*100,
		direct.Metrics.HallucinationRate*100,
		agentArm.Metrics.HallucinationRate*100,
		direct.Metrics.OverconfidenceRate*100,
		agentArm.Metrics.OverconfidenceRate*100,
		result.RepeatConsistency[armAgent]*100,
		agentArm.AverageTokens,
		agentArm.Metrics.AverageDurationMS,
		rule.Metrics.RCAAccuracy*100,
		rule.Metrics.AverageDurationMS,
	)
}

func diagnoseRule(engine *diagnostic.DiagnosisEngine, snapshot *diagnostic.DiagnosticContext, snapshotErr error) (*diagnostic.Report, error) {
	if snapshotErr != nil {
		return nil, snapshotErr
	}
	return engine.Diagnose(snapshot)
}

func diagnoseDirect(cfg *config.Config, snapshot *diagnostic.DiagnosticContext, snapshotErr error, timeout time.Duration) (*diagnostic.Report, usageStats, int64, error) {
	start := time.Now()
	if snapshotErr != nil {
		return nil, usageStats{}, time.Since(start).Milliseconds(), snapshotErr
	}
	client, err := newLLMClient(cfg.LLM)
	if err != nil {
		return nil, usageStats{}, time.Since(start).Milliseconds(), err
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	direct, err := client.GenerateDirectDiagnosis(ctx, compactSnapshot(snapshot))
	duration := time.Since(start).Milliseconds()
	usage := usageFromClient(client)
	if err != nil {
		return nil, usage, duration, err
	}
	return directReport(snapshot, direct), usage, duration, nil
}

func diagnoseAgent(
	cfg *config.Config,
	snapshotService *service.SnapshotService,
	engine *diagnostic.DiagnosisEngine,
	promClient *prometheus.Client,
	lokiClient *loki.Client,
	namespace, podName string,
	taskID uint,
	timeout time.Duration,
) (*diagnostic.Report, usageStats, sourceStats, toolStats, string, int, int64, error) {
	start := time.Now()
	client, err := newLLMClient(cfg.LLM)
	if err != nil {
		return nil, usageStats{}, sourceStats{}, toolStats{}, "", 0, time.Since(start).Milliseconds(), err
	}
	store := &memoryStore{}
	snapshotFunc := func(ctx context.Context, goal agent.Goal) (*diagnostic.DiagnosticContext, error) {
		snapshot, collectErr := snapshotService.Collect(ctx, service.PodDiagnosisRequest{
			Namespace:      goal.Namespace,
			PodName:        goal.PodName,
			ContainerName:  goal.ContainerName,
			IncludeEvents:  goal.IncludeEvents,
			IncludeLogs:    goal.IncludeLogs,
			IncludeMetrics: goal.IncludeMetrics,
			AlertTime:      goal.AlertTime,
		})
		if collectErr == nil {
			redactEvaluationLabels(snapshot)
		}
		return snapshot, collectErr
	}
	toolTimeout := time.Duration(cfg.Agent.ToolTimeoutSeconds) * time.Second
	if toolTimeout <= 0 {
		toolTimeout = 10 * time.Second
	}
	toolRegistry, err := agent.NewDefaultRegistry(agent.RegistryOptions{
		Snapshot:    snapshotFunc,
		Retriever:   noopRetriever{},
		Policy:      agent.NewRemediationPolicy(false),
		Prometheus:  promClient,
		Loki:        lokiClient,
		ToolTimeout: toolTimeout,
	})
	if err != nil {
		return nil, usageStats{}, sourceStats{}, toolStats{}, "", 0, time.Since(start).Milliseconds(), err
	}
	runtime := agent.NewRuntime(agent.RuntimeDeps{
		Store:    store,
		Registry: toolRegistry,
		Analyzer: engine,
		Policy:   agent.NewRemediationPolicy(false),
		Planner:  agent.NewLLMPlanner(client),
	})
	maxSteps := cfg.Agent.MaxSteps
	if maxSteps <= 0 {
		maxSteps = 12
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	runResult, runErr := runtime.Run(ctx, agent.RuntimeOptions{
		TaskID:      taskID,
		TraceID:     fmt.Sprintf("live-ab-%d-%s", taskID, podName),
		MaxSteps:    maxSteps,
		ToolTimeout: toolTimeout,
		Goal: agent.Goal{
			Namespace: namespace,
			PodName:   podName,
		},
	})
	duration := time.Since(start).Milliseconds()
	usage := usageFromClient(client)
	tools := collectToolStats(store.steps)
	if runResult == nil {
		return nil, usage, sourceStats{}, tools, "", 0, duration, runErr
	}
	tools.Planned = append([]string(nil), runResult.PlannedToolNames...)
	sources := collectSourceStats(runResult.DiagContext)
	mergeToolSourceStats(&sources, store.steps)
	return runResult.Report, usage, sources, tools, runResult.StopReason, runResult.StepsExecuted, duration, runErr
}

func newLLMClient(cfg config.LLMConfig) (*llm.OpenAICompatibleClient, error) {
	client := llm.NewOpenAICompatibleClient(cfg)
	concrete, ok := client.(*llm.OpenAICompatibleClient)
	if !ok {
		return nil, fmt.Errorf("unexpected LLM client type %T", client)
	}
	return concrete, nil
}

func usageFromClient(client *llm.OpenAICompatibleClient) usageStats {
	if client == nil {
		return usageStats{}
	}
	usage := client.CumulativeUsage()
	return usageStats{
		Calls:            client.UsageCallCount(),
		PromptTokens:     usage.PromptTokens,
		CompletionTokens: usage.CompletionTokens,
		TotalTokens:      usage.TotalTokens,
		LLMLatencyMS:     usage.LatencyMS,
	}
}

func directReport(snapshot *diagnostic.DiagnosticContext, direct *llm.DirectDiagnosis) *diagnostic.Report {
	if direct == nil {
		return nil
	}
	report := &diagnostic.Report{
		FaultType:        direct.FaultType,
		RootCauseSummary: direct.RootCauseSummary,
		ConfidenceScore:  direct.ConfidenceScore,
		SuggestedActions: append([]string(nil), direct.SuggestedActions...),
		RiskLevel:        direct.RiskLevel,
		NeedHumanConfirm: true,
		PrimaryRootCause: &diagnostic.RootCauseFactor{
			FaultType:        direct.FaultType,
			Summary:          direct.RootCauseSummary,
			ConfidenceScore:  direct.ConfidenceScore,
			ContributingRole: "primary",
		},
	}
	if snapshot != nil {
		report.Namespace = snapshot.Namespace
		report.PodName = snapshot.PodName
	}
	for _, evidence := range direct.Evidence {
		report.Evidences = append(report.Evidences, diagnostic.EvidenceRecord{
			SourceType: "direct_llm_evidence",
			Title:      "Evidence selected by Direct LLM",
			Content:    evidence,
			Severity:   "info",
			Timestamp:  time.Now(),
		})
	}
	for index, action := range direct.SuggestedActions {
		report.RemediationActions = append(report.RemediationActions, diagnostic.RemediationAction{
			ActionID:         fmt.Sprintf("direct-%d", index+1),
			ActionType:       "advisory",
			Description:      action,
			RiskLevel:        direct.RiskLevel,
			NeedHumanConfirm: true,
			Executable:       false,
		})
	}
	return report
}

func compactSnapshot(ctx *diagnostic.DiagnosticContext) map[string]interface{} {
	result := map[string]interface{}{
		"input_contract": map[string]string{
			"source": "live Kubernetes, Loki, and Prometheus evidence",
			"note":   "No expected fault label or golden answer is included.",
		},
	}
	if ctx == nil {
		return result
	}
	result["namespace"] = ctx.Namespace
	result["pod_name"] = ctx.PodName
	result["events"] = compactEvents(ctx.Events)
	result["logs"] = compactLogs(ctx.Logs)
	result["pvcs"] = ctx.PVCs
	result["topology"] = ctx.Topology
	result["correlations"] = ctx.Correlations
	result["metric_trends"] = ctx.MetricTrends
	result["node_snapshots"] = ctx.NodeSnapshots
	if ctx.Pod != nil {
		result["pod"] = compactPod(ctx.Pod)
	}
	return result
}

func compactPod(pod *corev1.Pod) map[string]interface{} {
	containers := make([]map[string]interface{}, 0, len(pod.Spec.Containers))
	statusByName := map[string]corev1.ContainerStatus{}
	for _, status := range pod.Status.ContainerStatuses {
		statusByName[status.Name] = status
	}
	for _, container := range pod.Spec.Containers {
		item := map[string]interface{}{
			"name":            container.Name,
			"image":           container.Image,
			"memory_request":  container.Resources.Requests.Memory().String(),
			"memory_limit":    container.Resources.Limits.Memory().String(),
			"cpu_request":     container.Resources.Requests.Cpu().String(),
			"cpu_limit":       container.Resources.Limits.Cpu().String(),
			"readiness_probe": container.ReadinessProbe,
			"liveness_probe":  container.LivenessProbe,
		}
		if status, ok := statusByName[container.Name]; ok {
			item["ready"] = status.Ready
			item["restart_count"] = status.RestartCount
			item["state"] = status.State
			item["last_state"] = status.LastTerminationState
		}
		containers = append(containers, item)
	}
	return map[string]interface{}{
		"phase":                   pod.Status.Phase,
		"reason":                  pod.Status.Reason,
		"message":                 pod.Status.Message,
		"node_name":               pod.Spec.NodeName,
		"conditions":              pod.Status.Conditions,
		"containers":              containers,
		"init_container_statuses": pod.Status.InitContainerStatuses,
		"tolerations":             pod.Spec.Tolerations,
		"volumes":                 pod.Spec.Volumes,
	}
}

func compactEvents(events []corev1.Event) []map[string]interface{} {
	result := make([]map[string]interface{}, 0, len(events))
	for _, event := range events {
		result = append(result, map[string]interface{}{
			"type":    event.Type,
			"reason":  event.Reason,
			"message": truncate(event.Message, 1200),
			"count":   event.Count,
			"last_at": event.LastTimestamp,
		})
		if len(result) >= 30 {
			break
		}
	}
	return result
}

func compactLogs(logs []diagnostic.ContainerLogs) []map[string]interface{} {
	result := make([]map[string]interface{}, 0, len(logs))
	for _, entry := range logs {
		result = append(result, map[string]interface{}{
			"container":       entry.ContainerName,
			"kubernetes_log":  truncate(firstNonEmpty(entry.Previous, entry.Current), 5000),
			"loki_log":        truncate(entry.Loki, 5000),
			"window_start":    entry.WindowStart,
			"window_end":      entry.WindowEnd,
			"precise_window":  entry.Precise,
			"fallback_reason": entry.Fallback,
		})
	}
	return result
}

func redactEvaluationLabels(ctx *diagnostic.DiagnosticContext) {
	if ctx == nil || ctx.Pod == nil {
		return
	}
	pod := ctx.Pod.DeepCopy()
	for key := range pod.Labels {
		if strings.HasPrefix(strings.ToLower(key), "kubesage.io/") {
			delete(pod.Labels, key)
		}
	}
	for key := range pod.Annotations {
		lower := strings.ToLower(key)
		if strings.HasPrefix(lower, "kubesage.io/") ||
			strings.Contains(lower, "expected") ||
			strings.Contains(lower, "fault-type") ||
			strings.Contains(lower, "case-id") {
			delete(pod.Annotations, key)
		}
	}
	ctx.Pod = pod
}

func collectSourceStats(ctx *diagnostic.DiagnosticContext) sourceStats {
	if ctx == nil {
		return sourceStats{}
	}
	stats := sourceStats{
		Events:        len(ctx.Events),
		PVCs:          len(ctx.PVCs),
		NodeSnapshots: len(ctx.NodeSnapshots),
	}
	for _, logs := range ctx.Logs {
		stats.KubernetesLogBytes += len(logs.Current) + len(logs.Previous)
		if strings.TrimSpace(logs.Loki) != "" {
			stats.LokiContainers++
			stats.LokiBytes += len(logs.Loki)
		}
	}
	for _, trend := range ctx.MetricTrends {
		stats.PrometheusQueries++
		stats.PrometheusSamples += trend.SampleCount
		if trend.Classification == "query_failed" {
			stats.PrometheusQueryFails++
		}
	}
	return stats
}

func collectToolStats(steps []model.AgentStep) toolStats {
	stats := toolStats{}
	seenExecuted := map[string]bool{}
	seenFailed := map[string]bool{}
	for _, step := range steps {
		if step.Stage != agent.StageToolCall || strings.TrimSpace(step.ToolName) == "" {
			continue
		}
		stats.Calls++
		if !seenExecuted[step.ToolName] {
			stats.Executed = append(stats.Executed, step.ToolName)
			seenExecuted[step.ToolName] = true
		}
		if step.Status == model.AgentStepStatusFailed {
			stats.Failures++
			if !seenFailed[step.ToolName] {
				stats.Failed = append(stats.Failed, step.ToolName)
				seenFailed[step.ToolName] = true
			}
		}
		switch step.ToolName {
		case "prometheus.query_range":
			stats.PrometheusUsed = true
		case "loki.query_logs":
			stats.LokiUsed = true
		case "k8s.get_logs":
			if strings.Contains(strings.ToLower(string(step.OutputJSON)), "loki") {
				stats.LokiUsed = true
			}
		}
	}
	return stats
}

func mergeToolSourceStats(stats *sourceStats, steps []model.AgentStep) {
	if stats == nil {
		return
	}
	for _, step := range steps {
		if step.Stage != agent.StageToolCall {
			continue
		}
		output := string(step.OutputJSON)
		switch step.ToolName {
		case "prometheus.query_range":
			stats.PrometheusQueries++
			if step.Status == model.AgentStepStatusFailed {
				stats.PrometheusQueryFails++
				continue
			}
			stats.PrometheusSamples += matchedCount(prometheusSamplesPattern, output)
		case "loki.query_logs":
			if step.Status == model.AgentStepStatusSuccess && matchedCount(lokiEntriesPattern, output) > 0 && stats.LokiContainers == 0 {
				stats.LokiContainers = 1
			}
		}
	}
}

func matchedCount(pattern *regexp.Regexp, value string) int {
	match := pattern.FindStringSubmatch(value)
	if len(match) != 2 {
		return 0
	}
	count, err := strconv.Atoi(match[1])
	if err != nil {
		return 0
	}
	return count
}

func armResultFromScore(score evalpkg.EvalCaseResult, usage usageStats, sources sourceStats, tools toolStats, stopReason string, steps int) armResult {
	return armResult{
		ActualFaultType:    score.ActualFaultType,
		RootCauseSummary:   score.RootCauseSummary,
		Confidence:         score.Confidence,
		Passed:             score.Passed,
		FaultTypeMatched:   score.FaultTypeMatched,
		RootCauseMatched:   score.RootCauseMatched,
		KeyEvidenceMatched: score.KeyEvidenceMatched,
		RemediationMatched: score.RemediationMatched,
		Hallucination:      score.HallucinationDetected,
		Overconfidence:     score.OverconfidenceDetected,
		SafetyPassed:       score.DangerousSuggestionCount == 0,
		DurationMS:         score.DurationMS,
		Usage:              usage,
		Sources:            sources,
		Tools:              tools,
		StopReason:         stopReason,
		StepsExecuted:      steps,
		MissingEvidence:    append([]string(nil), score.MissingEvidence...),
		MissingRemediation: append([]string(nil), score.MissingRemediations...),
		Errors:             append([]string(nil), score.Errors...),
	}
}

func aggregateArm(name string, cases []caseResult) armAggregate {
	scores := make([]evalpkg.EvalCaseResult, 0, len(cases))
	for _, caseItem := range cases {
		run, ok := caseItem.Arms[name]
		if !ok {
			continue
		}
		dangerousSuggestions := 0
		if !run.SafetyPassed {
			dangerousSuggestions = 1
		}
		scores = append(scores, evalpkg.EvalCaseResult{
			ID:                       caseItem.ID,
			DurationMS:               run.DurationMS,
			Passed:                   run.Passed,
			FaultTypeMatched:         run.FaultTypeMatched,
			RootCauseMatched:         run.RootCauseMatched,
			KeyEvidenceMatched:       run.KeyEvidenceMatched,
			RemediationMatched:       run.RemediationMatched,
			HallucinationDetected:    run.Hallucination,
			OverconfidenceDetected:   run.Overconfidence,
			DangerousSuggestionCount: dangerousSuggestions,
		})
	}
	aggregate := armAggregate{Metrics: evalpkg.AggregateMetrics(scores)}
	for _, caseItem := range cases {
		run, ok := caseItem.Arms[name]
		if !ok {
			continue
		}
		aggregate.TotalTokens += run.Usage.TotalTokens
		aggregate.TotalLLMCalls += run.Usage.Calls
		aggregate.ToolCalls += run.Tools.Calls
		aggregate.ToolFailures += run.Tools.Failures
		if run.Sources.PrometheusSamples > 0 {
			aggregate.PrometheusCaseHits++
		}
		if run.Sources.LokiBytes > 0 {
			aggregate.LokiCaseHits++
		}
		if run.Tools.PrometheusUsed {
			aggregate.PrometheusToolCases++
		}
		if run.Tools.LokiUsed {
			aggregate.LokiToolCases++
		}
	}
	if len(cases) > 0 {
		aggregate.AverageTokens = aggregate.TotalTokens / len(cases)
	}
	return aggregate
}

func buildResumeEvidence(result evaluationResult) string {
	rule := result.Arms[armRule]
	direct := result.Arms[armDirect]
	agentArm := result.Arms[armAgent]
	return fmt.Sprintf(
		"搭建真实 Kind 故障集三路在线 A/B 评测，%d 个案例统一从 Kubernetes、Prometheus、Loki 实时采证并过滤答案标签；Agent 相比 Direct LLM 将故障类型准确率从 %.1f%% 提升至 %.1f%%、幻觉率从 %.1f%% 降至 %.1f%%，但平均 token 从 %d 增至 %d、平均延迟从 %dms 增至 %dms；结合规则基线 %.1f%% 严格 RCA / %dms 延迟，验证规则快路径 + Agent 低置信慢路径的生产策略。",
		agentArm.Metrics.TotalCases,
		direct.Metrics.FaultTypeAccuracy*100,
		agentArm.Metrics.FaultTypeAccuracy*100,
		direct.Metrics.HallucinationRate*100,
		agentArm.Metrics.HallucinationRate*100,
		direct.AverageTokens,
		agentArm.AverageTokens,
		direct.Metrics.AverageDurationMS,
		agentArm.Metrics.AverageDurationMS,
		rule.Metrics.RCAAccuracy*100,
		rule.Metrics.AverageDurationMS,
	)
}

func writeJSON(path string, result evaluationResult) error {
	data, err := json.MarshalIndent(result, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal result: %w", err)
	}
	if err := writeFile(path, append(data, '\n')); err != nil {
		return err
	}
	return nil
}

func writeMarkdown(path string, result evaluationResult) error {
	var b strings.Builder
	b.WriteString("# Live Agent A/B Evaluation\n\n")
	b.WriteString(fmt.Sprintf("- Generated: %s\n", result.GeneratedAt.Format(time.RFC3339)))
	b.WriteString(fmt.Sprintf("- Namespace: `%s`\n", result.Namespace))
	b.WriteString(fmt.Sprintf("- Cases: `%s`\n\n", result.CasesPath))
	b.WriteString(fmt.Sprintf("- Unique live scenarios: %d\n", result.UniqueScenarios))
	b.WriteString(fmt.Sprintf("- Completed online runs: %d/%d\n\n", len(result.Cases), result.RequestedRuns))
	b.WriteString("## Methodology\n\n")
	for _, item := range result.Methodology {
		b.WriteString("- " + item + "\n")
	}
	b.WriteString("\n## Summary\n\n")
	b.WriteString("| Arm | Strict RCA | Fault type | Root cause | Evidence | Remediation | Safety | Avg latency | P95 latency | Avg tokens | LLM calls |\n")
	b.WriteString("| --- | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: |\n")
	for _, name := range []string{armRule, armDirect, armAgent} {
		armData := result.Arms[name]
		m := armData.Metrics
		b.WriteString(fmt.Sprintf(
			"| %s | %.1f%% | %.1f%% | %.1f%% | %.1f%% | %.1f%% | %.1f%% | %dms | %dms | %d | %d |\n",
			name,
			m.RCAAccuracy*100,
			m.FaultTypeAccuracy*100,
			m.RootCauseAccuracy*100,
			m.KeyEvidenceRecall*100,
			m.RemediationRecall*100,
			m.SafetyPassRate*100,
			m.AverageDurationMS,
			m.P95DurationMS,
			armData.AverageTokens,
			armData.TotalLLMCalls,
		))
	}

	b.WriteString("\n## Business Delta\n\n")
	direct := result.Arms[armDirect]
	agentArm := result.Arms[armAgent]
	rule := result.Arms[armRule]
	b.WriteString(fmt.Sprintf("- Agent vs Direct LLM fault-type accuracy: %+.1f percentage points\n", (agentArm.Metrics.FaultTypeAccuracy-direct.Metrics.FaultTypeAccuracy)*100))
	b.WriteString(fmt.Sprintf("- Agent vs Direct LLM strict RCA: %+.1f percentage points\n", (agentArm.Metrics.RCAAccuracy-direct.Metrics.RCAAccuracy)*100))
	b.WriteString(fmt.Sprintf("- Agent vs rule strict RCA: %+.1f percentage points\n", (agentArm.Metrics.RCAAccuracy-rule.Metrics.RCAAccuracy)*100))
	b.WriteString(fmt.Sprintf("- Agent tokens: %d total, %d average per case\n", agentArm.TotalTokens, agentArm.AverageTokens))
	b.WriteString(fmt.Sprintf("- Agent tool calls: %d total, %d failed\n", agentArm.ToolCalls, agentArm.ToolFailures))
	b.WriteString(fmt.Sprintf("- Live source coverage: Prometheus samples in %d/%d cases, Loki logs in %d/%d cases\n",
		rule.PrometheusCaseHits, rule.Metrics.TotalCases, rule.LokiCaseHits, rule.Metrics.TotalCases))
	b.WriteString(fmt.Sprintf("- Agent tool coverage: Prometheus called in %d cases, Loki used directly or through log enrichment in %d cases\n",
		agentArm.PrometheusToolCases, agentArm.LokiToolCases))
	b.WriteString(fmt.Sprintf("- Repeat output consistency: rule %.1f%%, Direct LLM %.1f%%, Agent %.1f%%\n",
		result.RepeatConsistency[armRule]*100,
		result.RepeatConsistency[armDirect]*100,
		result.RepeatConsistency[armAgent]*100))

	b.WriteString("\n## Cases\n\n")
	b.WriteString("| Case | Expected | Rule | Direct LLM | Agent | Direct tokens | Agent tokens | Agent tools |\n")
	b.WriteString("| --- | --- | --- | --- | --- | ---: | ---: | --- |\n")
	for _, item := range result.Cases {
		ruleRun := item.Arms[armRule]
		directRun := item.Arms[armDirect]
		agentRun := item.Arms[armAgent]
		b.WriteString(fmt.Sprintf(
			"| `%s` | `%s` | %s `%s` | %s `%s` | %s `%s` | %d | %d | %s |\n",
			item.ID+" ("+item.ScenarioID+")",
			item.Expected,
			resultLabel(ruleRun),
			ruleRun.ActualFaultType,
			resultLabel(directRun),
			directRun.ActualFaultType,
			resultLabel(agentRun),
			agentRun.ActualFaultType,
			directRun.Usage.TotalTokens,
			agentRun.Usage.TotalTokens,
			strings.Join(agentRun.Tools.Executed, ", "),
		))
	}

	b.WriteString("\n## Resume Evidence\n\n")
	b.WriteString(result.ResumeEvidence + "\n")
	return writeFile(path, []byte(b.String()))
}

func writeFile(path string, data []byte) error {
	if strings.TrimSpace(path) == "" || path == "-" {
		_, err := os.Stdout.Write(data)
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("create output directory: %w", err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	return nil
}

func resultLabel(result armResult) string {
	if len(result.Errors) > 0 {
		return "ERROR"
	}
	if result.Passed {
		return "PASS"
	}
	if result.FaultTypeMatched {
		return "PARTIAL"
	}
	return "FAIL"
}

func passLabel(result evalpkg.EvalCaseResult) string {
	if len(result.Errors) > 0 {
		return "ERROR"
	}
	if result.Passed {
		return "PASS"
	}
	if result.FaultTypeMatched {
		return "PARTIAL"
	}
	return "FAIL"
}

func truncate(value string, limit int) string {
	value = strings.TrimSpace(value)
	if limit <= 0 || len(value) <= limit {
		return value
	}
	return value[:limit] + "...<truncated>"
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}
