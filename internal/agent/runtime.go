package agent

import (
	"context"
	"fmt"
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

	state := &ToolState{TaskID: opts.TaskID, Goal: opts.Goal}
	planCtx, planSpan := observability.Tracer().Start(ctx, "agent.planner")
	plan := r.planner.BuildInitialPlan(planCtx, opts.Goal, r.registry.Metadata())
	planSpan.End()
	recorder.record(planCtx, stepRecord{Stage: StagePlan, ToolName: plannerToolName(r.planner), Status: model.AgentStepStatusSuccess, Input: opts.Goal, Output: plan, Reason: plan.Summary})

	allEvidence := []diagnostic.EvidenceRecord{}
	latestScores := []HypothesisScore{}
	stopReason := ""
	stepsExecuted := 0

	for stopReason == "" {
		if err := ctx.Err(); err != nil {
			stopReason = StopReasonTimeout
			break
		}
		if stepsExecuted >= opts.MaxSteps {
			stopReason = StopReasonMaxSteps
			break
		}
		steps := nextPlanSteps(&plan)
		if len(steps) == 0 {
			stopReason = StopReasonPlanComplete
			break
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
			status := model.AgentStepStatusSuccess
			if !result.Success {
				status = model.AgentStepStatusFailed
			}
			toolStep := recorder.record(ctx, stepRecord{
				Stage:    StageToolCall,
				ToolName: step.ToolName,
				Status:   status,
				Input:    step.Input,
				Output:   result,
				Duration: time.Duration(result.DurationMS) * time.Millisecond,
				Reason:   step.Reason,
			})
			recorder.record(ctx, stepRecord{
				ParentStepID: stepIDPtr(toolStep),
				Stage:        StageObservation,
				ToolName:     step.ToolName,
				Status:       status,
				Output:       map[string]interface{}{"observation": result.Observation, "error": result.Error},
				Reason:       result.Observation,
			})
			stepsExecuted++
			markStepComplete(&plan, step.ID)
			if len(result.EvidenceRecords) > 0 {
				allEvidence = append(allEvidence, result.EvidenceRecords...)
			}
			if step.Critical && !result.Success {
				stopReason = StopReasonCriticalToolFailed
			}
		}
		hypothesisCtx, hypothesisSpan := observability.Tracer().Start(ctx, "agent.hypothesis_update")
		latestScores = r.hypotheses.Update(opts.TaskID, state.DiagnosticContext, allEvidence)
		hypothesisSpan.End()
		recorder.record(hypothesisCtx, stepRecord{
			Stage:  StageReflection,
			Status: model.AgentStepStatusSuccess,
			Input:  map[string]interface{}{"tools": toolNames(steps)},
			Output: latestScores,
			Reason: "updated root-cause hypotheses from latest observation",
		})

		// LLM-driven reflection: ask the LLM whether to continue or stop.
		if rp, ok := r.planner.(ReflectivePlanner); ok {
			reflection, reflectErr := rp.Reflect(ctx, plan, state, latestScores)
			if reflectErr == nil {
				recorder.record(ctx, stepRecord{
					Stage:  StageReflection,
					Status: model.AgentStepStatusSuccess,
					Input:  map[string]interface{}{"should_continue": reflection.ShouldContinue},
					Output: reflection,
					Reason: reflection.Reason,
				})
				if !reflection.ShouldContinue {
					stopReason = stopReasonLLMReflectionComplete
					break
				}
				// Append any new steps suggested by LLM reflection.
				for _, newStep := range reflection.NewSteps {
					if newStep.ID == "" {
						newStep.ID = "reflect-" + newStep.ToolName
					}
					newStep.AppendedBy = "llm_reflection"
					plan.Steps = append(plan.Steps, newStep)
				}
			}
		}

		if stopReason != "" {
			break
		}
		if hasConfirmed(latestScores) {
			stopReason = StopReasonConfirmedHypothesis
			break
		}
		for _, execution := range executions {
			if !execution.MissingTool {
				r.planner.AdjustPlan(&plan, state, execution.Result, latestScores)
			}
		}
		if nextPlanStep(&plan) == nil {
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
	state.Report = report

	remediationResult := r.executeTool(ctx, recorder, "remediation.generate_actions", map[string]interface{}{"fault_type": report.FaultType}, state, opts.ToolTimeout)
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
	finalScores := r.hypotheses.Update(opts.TaskID, state.DiagnosticContext, report.Evidences)
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

	return &RunResult{
		Report:           report,
		DiagContext:      state.DiagnosticContext,
		RunbookHits:      state.RunbookHits,
		Hypotheses:       hypothesisModels,
		Executions:       state.Executions,
		VerificationPlan: verificationPlan,
		StopReason:       stopReason,
		StepsExecuted:    stepsExecuted,
	}, nil
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
			result := tool.Execute(toolCtx, step.Input, state)
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
	if name == "remediation.generate_actions" || name == "remediation.dry_run_patch" {
		spanName = "agent.remediation_policy"
	}
	toolCtx, span := observability.Tracer().Start(toolCtx, spanName, trace.WithAttributes(attribute.String("agent.tool", name)))
	result := tool.Execute(toolCtx, input, state)
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
	toolStep := recorder.record(ctx, stepRecord{
		Stage:    StageToolCall,
		ToolName: name,
		Status:   status,
		Input:    input,
		Output:   result,
		Duration: time.Duration(result.DurationMS) * time.Millisecond,
		Reason:   "executed agent tool",
	})
	recorder.record(ctx, stepRecord{
		ParentStepID: stepIDPtr(toolStep),
		Stage:        StageObservation,
		ToolName:     name,
		Status:       status,
		Output:       map[string]interface{}{"observation": result.Observation, "error": result.Error},
		Reason:       result.Observation,
	})
	return result
}

func hasConfirmed(scores []HypothesisScore) bool {
	for _, score := range scores {
		if score.Status == model.HypothesisStatusConfirmed {
			return true
		}
	}
	return false
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
