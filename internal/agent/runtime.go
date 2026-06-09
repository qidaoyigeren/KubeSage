package agent

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"kubesage/internal/diagnostic"
	"kubesage/internal/model"
	"kubesage/internal/observability"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
)

type Runtime struct {
	store        Store
	registry     *ToolRegistry
	planner      Planner
	hypotheses   *HypothesisEngine
	verification *VerificationPlanner
	analyzer     Analyzer
	policy       *RemediationPolicy
}

type RuntimeDeps struct {
	Store    Store
	Registry *ToolRegistry
	Analyzer Analyzer
	Policy   *RemediationPolicy
	Planner  Planner
}

// NewRuntime creates the Agent Runtime MVP coordinator.
func NewRuntime(deps RuntimeDeps) *Runtime {
	hypotheses := NewHypothesisEngine()
	// Wire LLM-assisted hypothesis scoring if the planner is an LLMPlanner
	// with a client that also implements LLMHypothesisScorer.
	if llmPlanner, ok := deps.Planner.(*LLMPlanner); ok && llmPlanner.HasLLMClient() {
		if scorer, ok := llmPlanner.client.(LLMHypothesisScorer); ok {
			hypotheses.WithLLMScorer(scorer)
		}
	}
	return &Runtime{
		store:        deps.Store,
		registry:     deps.Registry,
		planner:      firstPlanner(deps.Planner),
		hypotheses:   hypotheses,
		verification: NewVerificationPlanner(),
		analyzer:     deps.Analyzer,
		policy:       deps.Policy,
	}
}

// Run executes the auditable Agent loop and returns the final diagnostic report.
func (r *Runtime) Run(ctx context.Context, opts RuntimeOptions) (result *RunResult, err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			err = fmt.Errorf("agent runtime panic: %v", recovered)
			result = &RunResult{StopReason: StopReasonCriticalToolFailed}
		}
	}()
	if r == nil || r.registry == nil || r.analyzer == nil {
		return nil, fmt.Errorf("agent runtime is not fully configured")
	}
	if opts.MaxSteps <= 0 {
		opts.MaxSteps = 12
	}
	if opts.ToolTimeout <= 0 {
		opts.ToolTimeout = 10 * time.Second
	}
	if opts.MaxReflectionSteps <= 0 {
		opts.MaxReflectionSteps = 4
	}
	if opts.MaxReflectionRounds <= 0 {
		opts.MaxReflectionRounds = 4
	}
	if opts.MaxToolCallsPerTool <= 0 {
		opts.MaxToolCallsPerTool = 3
	}
	if opts.MaxRunbookSearches <= 0 {
		opts.MaxRunbookSearches = 2
	}
	if opts.MaxLLMTokens <= 0 {
		opts.MaxLLMTokens = 12000
	}
	if opts.ReflectionTimeout <= 0 {
		opts.ReflectionTimeout = 15 * time.Second
	}
	ctx = WithLLMTokenBudget(ctx, opts.MaxLLMTokens)

	ctx, span := observability.Tracer().Start(ctx, "agent.runtime",
		trace.WithAttributes(
			attribute.Int64("task.id", int64(opts.TaskID)),
			attribute.String("k8s.namespace", opts.Goal.Namespace),
			attribute.String("k8s.pod.name", opts.Goal.PodName),
		),
	)
	defer span.End()
	if opts.TraceID == "" {
		opts.TraceID = span.SpanContext().TraceID().String()
	}

	recorder := newStepRecorder(r.store, opts.TaskID, opts.TraceID)
	recorder.record(ctx, stepRecord{Stage: StagePlan, Status: model.AgentStepStatusSuccess, Input: opts.Goal, Reason: "received diagnosis goal"})

	toolMetadata := r.registry.Metadata()
	state := newToolState(opts.TaskID, opts.Goal, toolMetadata, ctx)
	planCtx, planSpan := observability.Tracer().Start(ctx, "agent.planner")
	plan := r.planner.BuildInitialPlan(planCtx, opts.Goal, toolMetadata)
	span.AddEvent("agent.plan.completed", trace.WithAttributes(attribute.Int("agent.plan.steps", len(plan.Steps))))
	planSpan.End()
	recorder.record(planCtx, stepRecord{Stage: StagePlan, ToolName: plannerToolName(r.planner), Status: model.AgentStepStatusSuccess, Input: opts.Goal, Output: plan, Reason: plan.Summary})

	// Capture initial plan metadata for RunResult.
	initialPlanSummary := plan.Summary
	initialPlannedTools := make([]string, 0, len(plan.Steps))
	for _, s := range plan.Steps {
		initialPlannedTools = append(initialPlannedTools, s.ToolName)
	}

	allEvidence := []diagnostic.EvidenceRecord{}
	latestScores := []HypothesisScore{}
	stopReason := ""
	stepsExecuted := 0
	executedToolNames := []string{}
	toolCallCounts := map[string]int{}
	reflectionRounds := 0
	reflectionSteps := 0
	reflectionBudgetLogged := false
	lastReflection := ReflectionResult{ShouldContinue: true}
	hasLastReflection := false
	var preAnalysis *PreAnalysisDecision

	for stopReason == "" {
		if err := ctx.Err(); err != nil {
			stopReason = StopReasonTimeout
			break
		}
		if stepsExecuted >= opts.MaxSteps {
			stopReason = StopReasonMaxSteps
			break
		}
		if preAnalysis != nil {
			applyTargetedEvidencePlan(&plan, state, preAnalysis)
		}
		ensureConfigFailureEvidence(&plan, state)
		steps := nextPlanSteps(&plan)
		if len(steps) == 0 {
			if enforceRuntimeEvidencePlan(&plan, state, preAnalysis) {
				continue
			}
			stopReason = StopReasonPlanComplete
			break
		}
		steps = enforceToolCallBudgets(steps, toolCallCounts, opts, recorder, ctx)
		if len(steps) == 0 {
			continue
		}
		executions := r.executePlanSteps(ctx, steps, state, opts.ToolTimeout)
		for _, execution := range executions {
			step := execution.Step
			result := execution.Result
			if execution.MissingTool {
				step.Skipped = true
				recorder.record(ctx, stepRecord{Stage: StageDecision, ToolName: step.ToolName, Status: model.AgentStepStatusSkipped, Input: step, Reason: "tool is not registered"})
				continue
			}
			executedToolNames = appendUniqueStrings(append(executedToolNames, canonicalToolName(step.ToolName)))
			status := model.AgentStepStatusSuccess
			if !result.Success {
				status = model.AgentStepStatusFailed
			}
			toolStep := recorder.record(ctx, stepRecord{
				Stage:              StageToolCall,
				ToolName:           step.ToolName,
				Status:             status,
				Input:              step.Input,
				Output:             result,
				Duration:           time.Duration(result.DurationMS) * time.Millisecond,
				Reason:             step.Reason,
				ObservationSummary: result.Observation,
				ParallelGroup:      step.ParallelGroup,
				ToolLatencyMS:      result.DurationMS,
			})
			recorder.record(ctx, stepRecord{
				ParentStepID:       stepIDPtr(toolStep),
				Stage:              StageObservation,
				ToolName:           step.ToolName,
				Status:             status,
				Output:             map[string]interface{}{"observation": result.Observation, "observation_data": result.ObservationData, "warnings": result.Warnings, "missing_evidence": result.MissingEvidence, "error": result.Error},
				Reason:             result.Observation,
				ObservationSummary: result.Observation,
				ParallelGroup:      step.ParallelGroup,
				ToolLatencyMS:      result.DurationMS,
			})
			stepsExecuted++
			markStepComplete(&plan, step.ID)
			if result.Success {
				markToolComplete(state, step.ToolName, step.Input)
			}
			state.RecordToolResult(result)
			if len(result.EvidenceRecords) > 0 {
				allEvidence = append(allEvidence, result.EvidenceRecords...)
			}
			if r.stepIsCritical(step) && !result.Success {
				stopReason = StopReasonCriticalToolFailed
			}
		}
		if preAnalysis == nil && stopReason == "" && shouldRunPreAnalyzer(executions) && state.DiagnosticContext != nil && state.DiagnosticContext.Pod != nil {
			decision, preErr := r.runPreAnalyzer(ctx, recorder, &plan, state)
			if preErr == nil && decision != nil {
				preAnalysis = decision
			}
		}
		hypothesisCtx, hypothesisSpan := observability.Tracer().Start(ctx, "agent.hypothesis_update")
		latestScores = r.hypotheses.UpdateContext(ctx, opts.TaskID, state.DiagnosticContext, allEvidence)
		span.AddEvent("agent.hypothesis.updated", trace.WithAttributes(attribute.Int("agent.hypothesis.count", len(latestScores))))
		hypothesisSpan.End()
		recorder.record(hypothesisCtx, stepRecord{
			Stage:  StageReflection,
			Status: model.AgentStepStatusSuccess,
			Input:  map[string]interface{}{"tools": toolNames(steps)},
			Output: latestScores,
			Reason: "updated root-cause hypotheses from latest observation",
		})
		convergence := r.hypotheses.DecideConvergence(latestScores)
		recorder.record(ctx, stepRecord{
			Stage:  StageReflection,
			Status: model.AgentStepStatusSuccess,
			Output: convergence,
			Reason: convergence.Reason,
		})

		// LLM-driven reflection: ask the LLM whether to continue or stop.
		if rp, ok := r.planner.(ReflectivePlanner); ok {
			canReflect, budgetReason := reflectionBudgetAvailable(ctx, reflectionRounds, reflectionSteps, opts)
			if !canReflect && !reflectionBudgetLogged {
				reflectionBudgetLogged = true
				recorder.record(ctx, stepRecord{
					Stage:  StageReflection,
					Status: model.AgentStepStatusSkipped,
					Output: map[string]interface{}{
						"reflection_rounds": reflectionRounds,
						"reflection_steps":  reflectionSteps,
						"llm_tokens":        LLMTokenUsage(ctx),
					},
					Reason: budgetReason,
				})
			}
			var reflection ReflectionResult
			var reflectErr error
			if canReflect {
				reflectionRounds++
				reflectCtx, cancel := context.WithTimeout(ctx, opts.ReflectionTimeout)
				reflection, reflectErr = rp.Reflect(reflectCtx, plan, state, latestScores)
				cancel()
			} else {
				reflectErr = fmt.Errorf("%s", budgetReason)
			}
			if reflectErr == nil {
				lastReflection = reflection
				hasLastReflection = true
				span.AddEvent("agent.reflection.decision", trace.WithAttributes(
					attribute.Bool("agent.reflection.should_continue", reflection.ShouldContinue),
					attribute.Int("agent.reflection.new_steps", len(reflection.NewSteps)),
				))
				recorder.record(ctx, stepRecord{
					Stage:  StageReflection,
					Status: model.AgentStepStatusSuccess,
					Input:  map[string]interface{}{"should_continue": reflection.ShouldContinue},
					Output: reflection,
					Reason: reflection.Reason,
				})
				// Append any new steps suggested by LLM reflection.
				appendedReflectionSteps := 0
				if reflection.ShouldContinue {
					for _, newStep := range reflection.NewSteps {
						if reflectionSteps >= opts.MaxReflectionSteps {
							recorder.record(ctx, stepRecord{
								Stage:    StageDecision,
								ToolName: newStep.ToolName,
								Status:   model.AgentStepStatusSkipped,
								Input:    newStep.Input,
								Reason:   "reflection step budget exhausted",
							})
							continue
						}
						var ok bool
						newStep, ok = sanitizeReflectionStep(newStep, state.ToolMetadataSnapshot(), state.Goal)
						if !ok {
							continue
						}
						if redundantReflectionStep(&plan, state, newStep) {
							continue
						}
						newStep.ID = uniqueReflectionStepID(&plan, newStep)
						newStep.AppendedBy = "llm_reflection"
						plan.Steps = append(plan.Steps, newStep)
						appendedReflectionSteps++
						reflectionSteps++
					}
				}
				if enforceRuntimeEvidencePlan(&plan, state, preAnalysis) {
					continue
				}
				if appendedReflectionSteps > 0 || hasPendingReflectionSteps(&plan) {
					continue
				}
				if !reflection.ShouldContinue {
					if convergence.Converged {
						if hasPendingRequiredEvidence(&plan) {
							continue
						}
						if preAnalysis == nil && ensureRunnableConvergenceEvidence(&plan, state, convergence, "confirmed hypothesis still has runnable distinguishing evidence") {
							recorder.record(ctx, stepRecord{
								Stage:  StageReflection,
								Status: model.AgentStepStatusSuccess,
								Output: convergence,
								Reason: "confirmed hypothesis still has runnable distinguishing evidence; continue collection before stopping",
							})
							continue
						}
						stopReason = stopReasonLLMReflectionComplete
						break
					}
					appendDistinguishingEvidence(&plan, state, convergence)
				}
			}
		}

		if stopReason != "" {
			break
		}
		if ensureConfigFailureEvidence(&plan, state) {
			continue
		}
		if enforceRuntimeEvidencePlan(&plan, state, preAnalysis) {
			continue
		}
		if preAnalysis == nil {
			appendDistinguishingEvidence(&plan, state, convergence)
		}
		if convergence.Converged {
			if hasPendingRequiredEvidence(&plan) {
				continue
			}
			if preAnalysis == nil && ensureRunnableConvergenceEvidence(&plan, state, convergence, "confirmed hypothesis still has runnable distinguishing evidence") {
				recorder.record(ctx, stepRecord{
					Stage:  StageReflection,
					Status: model.AgentStepStatusSuccess,
					Output: convergence,
					Reason: "confirmed hypothesis still has runnable distinguishing evidence; continue collection before stopping",
				})
				continue
			}
			stopReason = StopReasonConfirmedHypothesis
			break
		}
		if preAnalysis == nil {
			if last, ok := lastCompletedExecution(executions); ok {
				if llmTokenBudgetAvailable(ctx) {
					r.planner.AdjustPlan(&plan, state, last.Result, latestScores)
				} else {
					NewRulePlanner().AdjustPlan(&plan, state, last.Result, latestScores)
				}
			}
		}
		dropCompletedSnapshotSteps(&plan, state)
		if nextPlanStep(&plan) == nil {
			if enforceRuntimeEvidencePlan(&plan, state, preAnalysis) {
				continue
			}
			if ok, reason := shouldConfirmWithExhaustedEvidence(&plan, state, convergence, confirmedThreshold(r.hypotheses)); ok {
				recorder.record(ctx, stepRecord{
					Stage:  StageReflection,
					Status: model.AgentStepStatusSuccess,
					Output: map[string]interface{}{"convergence": convergence, "evidence_exhausted": true},
					Reason: reason,
				})
				stopReason = StopReasonConfirmedHypothesis
				break
			}
			if ok, reason := shouldStopAfterLLMReflectionWithExhaustedEvidence(&plan, state, convergence, lastReflection, hasLastReflection); ok {
				recorder.record(ctx, stepRecord{
					Stage:  StageReflection,
					Status: model.AgentStepStatusSuccess,
					Output: map[string]interface{}{"convergence": convergence, "evidence_exhausted": true, "llm_reflection": lastReflection},
					Reason: reason,
				})
				stopReason = stopReasonLLMReflectionComplete
				break
			}
			stopReason = StopReasonNoEffectiveTool
			break
		}
	}

	if state.DiagnosticContext == nil {
		err := fmt.Errorf("agent stopped before pod context was available: %s", stopReason)
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		return &RunResult{StopReason: stopReason, StepsExecuted: stepsExecuted}, err
	}

	report, err := r.runAnalyzer(ctx, recorder, state)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		return nil, err
	}
	report.Evidences = appendUniqueEvidence(report.Evidences, allEvidence...)
	alignmentScores := r.hypotheses.UpdateContext(ctx, opts.TaskID, state.DiagnosticContext, report.Evidences)
	alignmentModels := r.hypotheses.ToModels(opts.TaskID, alignmentScores)
	alignment := AlignReportWithHypotheses(report, alignmentModels)
	if alignment.Changed {
		recorder.record(ctx, stepRecord{
			Stage:  StageDecision,
			Status: model.AgentStepStatusSuccess,
			Output: alignment,
			Reason: alignment.Reason,
		})
	}
	state.Report = report

	remediationResult := r.executeTool(ctx, recorder, "remediation.generate_actions", map[string]interface{}{"fault_type": report.FaultType}, state, opts.ToolTimeout)
	executedToolNames = appendUniqueStrings(append(executedToolNames, "remediation.generate_actions"))
	if state.Report != nil {
		report = state.Report
	}
	if !remediationResult.Success && remediationResult.Error != "" {
		report.Evidences = append(report.Evidences, diagnostic.EvidenceRecord{
			SourceType: "agent",
			Title:      "Remediation generation failed",
			Content:    remediationResult.Error,
			Severity:   "warning",
			Raw:        remediationResult,
			Timestamp:  time.Now(),
		})
	}

	verificationCtx, verificationSpan := observability.Tracer().Start(ctx, "agent.verification")
	verificationPlan := r.verification.Build(report.RemediationActions)
	verificationSpan.End()
	recorder.record(verificationCtx, stepRecord{
		Stage:  StageVerification,
		Status: model.AgentStepStatusSuccess,
		Input:  report.RemediationActions,
		Output: verificationPlan,
		Reason: "generated verification plan for remediation proposals",
	})

	finalCtx, finalSpan := observability.Tracer().Start(ctx, "agent.hypothesis_update")
	finalScores := r.hypotheses.UpdateContext(ctx, opts.TaskID, state.DiagnosticContext, report.Evidences)
	span.AddEvent("agent.hypothesis.finalized", trace.WithAttributes(attribute.Int("agent.hypothesis.count", len(finalScores))))
	finalSpan.End()
	hypothesisModels := r.hypotheses.ToModels(opts.TaskID, finalScores)
	if err := r.store.CreateHypotheses(finalCtx, hypothesisModels); err != nil {
		return nil, err
	}
	if err := r.store.CreateRemediationExecutions(ctx, state.Executions); err != nil {
		return nil, err
	}

	snapshot := BuildReportSnapshot(report, hypothesisModels, state.RunbookHits, state.Executions, verificationPlan, stopReason)
	report.AgentExecutionSummary = snapshot.AgentExecutionSummary
	report.AgentReportSnapshot = snapshot
	report.VerificationPlan = verificationPlan
	report.ResidualRisks = snapshot.ResidualRisks

	recorder.record(ctx, stepRecord{
		Stage:  StageDecision,
		Status: model.AgentStepStatusSuccess,
		Output: map[string]interface{}{"stop_reason": stopReason, "fault_type": report.FaultType},
		Reason: "produced final agent report",
	})
	span.AddEvent("agent.runtime.completed", trace.WithAttributes(
		attribute.String("agent.stop_reason", stopReason),
		attribute.String("diagnosis.fault_type", report.FaultType),
		attribute.Int("agent.steps_executed", stepsExecuted),
	))

	return &RunResult{
		Report:            report,
		PreAnalysis:       preAnalysis,
		DiagContext:       state.DiagnosticContext,
		RunbookHits:       state.RunbookHits,
		Hypotheses:        hypothesisModels,
		Executions:        state.Executions,
		VerificationPlan:  verificationPlan,
		StopReason:        stopReason,
		StepsExecuted:     stepsExecuted,
		PlanSummary:       initialPlanSummary,
		PlannedToolNames:  initialPlannedTools,
		ExecutedToolNames: executedToolNames,
	}, nil
}

func uniqueReflectionStepID(plan *Plan, step PlanStep) string {
	base := strings.TrimSpace(step.ID)
	if base == "" {
		base = "reflect-" + strings.NewReplacer(".", "-", "_", "-").Replace(step.ToolName)
	}
	used := map[string]bool{}
	if plan != nil {
		for _, existing := range plan.Steps {
			used[existing.ID] = true
		}
	}
	if !used[base] {
		return base
	}
	for index := 2; ; index++ {
		candidate := fmt.Sprintf("%s-%d", base, index)
		if !used[candidate] {
			return candidate
		}
	}
}

func hasPendingReflectionSteps(plan *Plan) bool {
	if plan == nil {
		return false
	}
	for _, step := range plan.Steps {
		if step.AppendedBy == "llm_reflection" && !step.Completed && !step.Skipped {
			return true
		}
	}
	return false
}

func reflectionBudgetAvailable(ctx context.Context, rounds, steps int, opts RuntimeOptions) (bool, string) {
	switch {
	case opts.MaxReflectionRounds > 0 && rounds >= opts.MaxReflectionRounds:
		return false, "reflection round budget exhausted"
	case opts.MaxReflectionSteps > 0 && steps >= opts.MaxReflectionSteps:
		return false, "reflection step budget exhausted"
	case !llmTokenBudgetAvailable(ctx):
		return false, "LLM token budget exhausted"
	default:
		return true, ""
	}
}

func enforceToolCallBudgets(steps []*PlanStep, counts map[string]int, opts RuntimeOptions, recorder *stepRecorder, ctx context.Context) []*PlanStep {
	runnable := make([]*PlanStep, 0, len(steps))
	for _, step := range steps {
		tool := canonicalToolName(step.ToolName)
		limit := opts.MaxToolCallsPerTool
		if tool == "runbook.search" && opts.MaxRunbookSearches > 0 && (limit <= 0 || opts.MaxRunbookSearches < limit) {
			limit = opts.MaxRunbookSearches
		}
		if limit > 0 && counts[tool] >= limit {
			step.Skipped = true
			recorder.record(ctx, stepRecord{
				Stage:    StageDecision,
				ToolName: step.ToolName,
				Status:   model.AgentStepStatusSkipped,
				Input:    step.Input,
				Reason:   fmt.Sprintf("tool call budget exhausted: %s limit=%d", tool, limit),
			})
			continue
		}
		counts[tool]++
		runnable = append(runnable, step)
	}
	return runnable
}

func lastCompletedExecution(executions []planExecution) (planExecution, bool) {
	for index := len(executions) - 1; index >= 0; index-- {
		if !executions[index].MissingTool {
			return executions[index], true
		}
	}
	return planExecution{}, false
}

func shouldRunPreAnalyzer(executions []planExecution) bool {
	for _, execution := range executions {
		if execution.MissingTool || !execution.Result.Success {
			continue
		}
		if execution.Result.StateDelta != nil && execution.Result.StateDelta.DiagnosticContext != nil {
			return true
		}
		switch canonicalToolName(execution.Step.ToolName) {
		case "k8s.get_pod", "k8s.get_events", "k8s.get_logs", "k8s.get_topology", "k8s.get_pvc":
			return true
		}
	}
	return false
}

func (r *Runtime) runPreAnalyzer(ctx context.Context, recorder *stepRecorder, plan *Plan, state *ToolState) (*PreAnalysisDecision, error) {
	analyzeCtx, span := observability.Tracer().Start(ctx, "agent.pre_analyzer")
	report, err := r.analyzer.Diagnose(state.DiagnosticContext)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		recorder.record(analyzeCtx, stepRecord{
			Stage:  StagePreAnalysis,
			Status: model.AgentStepStatusFailed,
			Reason: "pre-analyzer failed; retained the existing evidence plan",
			Output: map[string]interface{}{"error": err.Error()},
		})
		span.End()
		return nil, err
	}
	if report == nil {
		err = fmt.Errorf("pre-analyzer returned a nil report")
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		recorder.record(analyzeCtx, stepRecord{
			Stage:  StagePreAnalysis,
			Status: model.AgentStepStatusFailed,
			Reason: "pre-analyzer returned no result; retained the existing evidence plan",
			Output: map[string]interface{}{"error": err.Error()},
		})
		span.End()
		return nil, err
	}

	decision := buildPreAnalysisDecision(report, state)
	output := map[string]interface{}{
		"locked":          decision != nil,
		"candidate_fault": report.FaultType,
		"confidence":      report.ConfidenceScore,
	}
	reason := "pre-analyzer result lacked a matching structured signal; retained the existing evidence plan"
	if decision != nil {
		state.SetExpectedFault(decision.FaultType)
		rewrite := applyTargetedEvidencePlan(plan, state, decision)
		output["decision"] = decision
		output["scheduled_tools"] = rewrite.scheduled
		output["skipped_tools"] = rewrite.skipped
		reason = "locked the structured fault classification and generated a targeted evidence plan"
		span.SetAttributes(
			attribute.String("diagnosis.pre_analysis.fault_type", decision.FaultType),
			attribute.Int("diagnosis.pre_analysis.evidence_tools", len(decision.EvidenceTools)),
		)
	}
	recorder.record(analyzeCtx, stepRecord{
		Stage:  StagePreAnalysis,
		Status: model.AgentStepStatusSuccess,
		Output: output,
		Reason: reason,
	})
	span.End()
	return decision, nil
}

type planExecution struct {
	Step        *PlanStep
	Result      ToolResult
	MissingTool bool
}

func (r *Runtime) executePlanSteps(ctx context.Context, steps []*PlanStep, state *ToolState, timeout time.Duration) []planExecution {
	results := make([]planExecution, len(steps))
	var wg sync.WaitGroup
	for i, step := range steps {
		tool, ok := r.registry.Get(step.ToolName)
		if !ok {
			results[i] = planExecution{Step: step, MissingTool: true}
			continue
		}
		wg.Add(1)
		go func(index int, step *PlanStep, tool Tool) {
			defer wg.Done()
			toolCtx, cancel := context.WithTimeout(ctx, timeout)
			defer cancel()
			toolCtx, toolSpan := observability.Tracer().Start(toolCtx, "agent.tool",
				trace.WithAttributes(
					attribute.String("agent.tool", step.ToolName),
					attribute.String("agent.parallel_group", step.ParallelGroup),
				),
			)
			result := executeToolWithDeadline(toolCtx, tool, step.Input, state.SnapshotForTool())
			if !result.Success {
				toolSpan.RecordError(fmt.Errorf("%s", result.Error))
				toolSpan.SetStatus(codes.Error, result.Error)
			}
			toolSpan.End()
			observeToolMetrics(result)
			results[index] = planExecution{Step: step, Result: result}
		}(i, step, tool)
	}
	wg.Wait()
	return results
}

func (r *Runtime) stepIsCritical(step *PlanStep) bool {
	if r == nil || step == nil {
		return false
	}
	return step.Critical && r.toolIsCritical(step.ToolName)
}

func (r *Runtime) toolIsCritical(name string) bool {
	if r == nil || r.registry == nil {
		return false
	}
	tool, ok := r.registry.Get(name)
	return ok && tool.Metadata().Critical
}

func plannerToolName(planner Planner) string {
	if _, ok := planner.(*LLMPlanner); ok {
		return "llm.plan"
	}
	return "rule.plan"
}

func (r *Runtime) runAnalyzer(ctx context.Context, recorder *stepRecorder, state *ToolState) (*diagnostic.Report, error) {
	analyzeCtx, span := observability.Tracer().Start(ctx, "agent.analyzer")
	report, err := r.analyzer.Diagnose(state.DiagnosticContext)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
	}
	span.End()
	status := model.AgentStepStatusSuccess
	if err != nil {
		status = model.AgentStepStatusFailed
	}
	recorder.record(analyzeCtx, stepRecord{
		Stage:  StageDecision,
		Status: status,
		Output: report,
		Reason: "ran existing rule analyzers",
	})
	return report, err
}

func (r *Runtime) executeTool(ctx context.Context, recorder *stepRecorder, name string, input map[string]interface{}, state *ToolState, timeout time.Duration) ToolResult {
	tool, ok := r.registry.Get(name)
	if !ok {
		result := failedTool(name, "tool is not registered")
		recorder.record(ctx, stepRecord{Stage: StageToolCall, ToolName: name, Status: model.AgentStepStatusFailed, Input: input, Output: result, Reason: result.Error})
		return result
	}
	toolCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	spanName := "agent.tool"
	if name == "remediation.generate_actions" || name == "remediation.dry_run" || name == "remediation.dry_run_patch" {
		spanName = "agent.remediation_policy"
	}
	toolCtx, span := observability.Tracer().Start(toolCtx, spanName, trace.WithAttributes(attribute.String("agent.tool", name)))
	result := executeToolWithDeadline(toolCtx, tool, input, state.SnapshotForTool())
	if !result.Success && result.Error != "" {
		span.RecordError(fmt.Errorf("%s", result.Error))
		span.SetStatus(codes.Error, result.Error)
	}
	span.End()
	status := model.AgentStepStatusSuccess
	if !result.Success {
		status = model.AgentStepStatusFailed
	}
	observeToolMetrics(result)
	state.RecordToolResult(result)
	toolStep := recorder.record(ctx, stepRecord{
		Stage:              StageToolCall,
		ToolName:           name,
		Status:             status,
		Input:              input,
		Output:             result,
		Duration:           time.Duration(result.DurationMS) * time.Millisecond,
		Reason:             "executed agent tool",
		ObservationSummary: result.Observation,
		ToolLatencyMS:      result.DurationMS,
	})
	recorder.record(ctx, stepRecord{
		ParentStepID:       stepIDPtr(toolStep),
		Stage:              StageObservation,
		ToolName:           name,
		Status:             status,
		Output:             map[string]interface{}{"observation": result.Observation, "observation_data": result.ObservationData, "warnings": result.Warnings, "missing_evidence": result.MissingEvidence, "error": result.Error},
		Reason:             result.Observation,
		ObservationSummary: result.Observation,
		ToolLatencyMS:      result.DurationMS,
	})
	return result
}

func executeToolWithDeadline(ctx context.Context, tool Tool, input map[string]interface{}, state *ToolState) ToolResult {
	done := make(chan ToolResult, 1)
	start := time.Now()
	go func() {
		done <- tool.Execute(ctx, input, state)
	}()
	select {
	case result := <-done:
		return result
	case <-ctx.Done():
		meta := tool.Metadata()
		errText := "tool deadline exceeded"
		if err := ctx.Err(); err != nil {
			errText = err.Error()
		}
		return ToolResult{
			ToolName:        meta.Name,
			Success:         false,
			Observation:     "tool timed out before producing an observation",
			Error:           errText,
			MissingEvidence: []string{meta.Name},
			DurationMS:      time.Since(start).Milliseconds(),
		}
	}
}

func hasConfirmed(scores []HypothesisScore) bool {
	for _, score := range scores {
		if score.Status == model.HypothesisStatusConfirmed {
			return true
		}
	}
	return false
}

func confirmedThreshold(engine *HypothesisEngine) float64 {
	if engine != nil && engine.config.ConfirmedThreshold > 0 {
		return engine.config.ConfirmedThreshold
	}
	return convergenceMinConfidence
}

func appendUniqueEvidence(existing []diagnostic.EvidenceRecord, additions ...diagnostic.EvidenceRecord) []diagnostic.EvidenceRecord {
	seen := map[string]struct{}{}
	for _, record := range existing {
		seen[evidenceRef(record, 0)] = struct{}{}
	}
	for _, record := range additions {
		key := evidenceRef(record, 0)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		existing = append(existing, record)
	}
	return existing
}

func stepIDPtr(step *model.AgentStep) *uint {
	if step == nil || step.ID == 0 {
		return nil
	}
	return &step.ID
}
