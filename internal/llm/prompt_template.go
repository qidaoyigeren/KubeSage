package llm

import (
	"fmt"
	"strings"
)

// DiagnosticStage represents the current phase of the diagnostic process.
type DiagnosticStage string

const (
	StageEarly  DiagnosticStage = "early"  // Steps 1-3: broad evidence collection
	StageMid    DiagnosticStage = "mid"    // Steps 4-8: hypothesis-focused investigation
	StageLate   DiagnosticStage = "late"   // Steps 9+: confirm or exclude, prepare conclusion
	StageReport DiagnosticStage = "report" // Final report generation
)

// DetermineStage returns the diagnostic stage based on current step index and max steps.
func DetermineStage(stepIndex, maxSteps int) DiagnosticStage {
	if maxSteps <= 0 {
		maxSteps = 12
	}
	ratio := float64(stepIndex) / float64(maxSteps)
	switch {
	case ratio < 0.25:
		return StageEarly
	case ratio < 0.67:
		return StageMid
	default:
		return StageLate
	}
}

// StageGuidance returns stage-specific instructions to append to system prompts.
func StageGuidance(stage DiagnosticStage) string {
	switch stage {
	case StageEarly:
		return strings.Join([]string{
			"You are in the EARLY investigation stage.",
			"Focus on broad evidence collection across multiple sources.",
			"Do not commit to a single hypothesis yet — keep multiple possibilities open.",
			"Prefer parallel tool calls to gather independent evidence quickly.",
			"Look for: pod status, events, logs, topology, and metrics if available.",
		}, "\n")
	case StageMid:
		return strings.Join([]string{
			"You are in the MID investigation stage.",
			"Focus on the highest-confidence hypotheses and collect distinguishing evidence.",
			"Evidence that discriminates between competing hypotheses is most valuable now.",
			"Runbook lookups for specific failure patterns are appropriate at this stage.",
			"Narrow your focus — stop collecting evidence that doesn't help differentiate hypotheses.",
		}, "\n")
	case StageLate:
		return strings.Join([]string{
			"You are in the LATE investigation stage.",
			"Your primary goal is to confirm or exclude the leading hypothesis.",
			"Only collect evidence that can definitively change the diagnosis.",
			"Prepare to produce a final conclusion with confidence assessment.",
			"If you cannot reach high confidence, be honest about residual uncertainty.",
		}, "\n")
	case StageReport:
		return strings.Join([]string{
			"You are generating the FINAL diagnostic report.",
			"Synthesize all evidence into a coherent root cause narrative.",
			"Cite specific evidence refs for each factual claim.",
			"Be explicit about confidence levels and remaining uncertainties.",
		}, "\n")
	default:
		return ""
	}
}

// FaultTypeHint returns fault-specific investigation guidance for the LLM.
func FaultTypeHint(faultType string) string {
	switch strings.ToLower(strings.TrimSpace(faultType)) {
	case "oomkilled":
		return strings.Join([]string{
			"FAULT CONTEXT — OOMKilled:",
			"  - Compare container memory_limit vs actual usage trends.",
			"  - Check if memory grew gradually (leak) or spiked suddenly (load spike).",
			"  - Verify if limit was set too low relative to typical workload needs.",
			"  - Look for Java heap / Go GC / cache-related memory patterns.",
		}, "\n")
	case "crashloopbackoff":
		return strings.Join([]string{
			"FAULT CONTEXT — CrashLoopBackOff:",
			"  - Check container exit codes and termination reasons.",
			"  - Verify startup command, environment variables, and dependencies.",
			"  - Inspect init containers — failure there blocks main containers.",
			"  - Check for missing ConfigMaps, Secrets, or volume mounts.",
		}, "\n")
	case "imagepullbackoff":
		return strings.Join([]string{
			"FAULT CONTEXT — ImagePullBackOff:",
			"  - Verify image name, tag, and registry are correct.",
			"  - Check imagePullSecrets and registry authentication.",
			"  - Validate network connectivity to the registry.",
			"  - Check if the image exists in the specified registry.",
		}, "\n")
	case "pending":
		return strings.Join([]string{
			"FAULT CONTEXT — Pod Pending:",
			"  - Check node resource availability (CPU, memory, disk, PID).",
			"  - Look for taints/tolerations or nodeSelector mismatches.",
			"  - Verify PVC binding status and storage class availability.",
			"  - Check for affinity/anti-affinity constraints.",
		}, "\n")
	case "probefailed":
		return strings.Join([]string{
			"FAULT CONTEXT — Probe Failed:",
			"  - Distinguish liveness (restart) vs readiness (no traffic) probes.",
			"  - Check probe configuration: endpoint, timing, thresholds.",
			"  - Verify the application actually listens on the probed port.",
			"  - Look for slow startup (increase initialDelaySeconds).",
		}, "\n")
	case "nodenotready":
		return strings.Join([]string{
			"FAULT CONTEXT — Node Not Ready:",
			"  - Check node conditions: MemoryPressure, DiskPressure, PIDPressure, NetworkUnavailable.",
			"  - Verify kubelet status and node lease renewals.",
			"  - Look for correlated pod evictions or OOM kills on the same node.",
			"  - Check for recent node events or taints.",
		}, "\n")
	case "evicted":
		return strings.Join([]string{
			"FAULT CONTEXT — Pod Evicted:",
			"  - Identify which resource pressure triggered the eviction.",
			"  - Check node-level resource usage vs capacity.",
			"  - Look for other pods on the same node that may be consuming excess resources.",
			"  - Verify QoS class (Guaranteed > Burstable > BestEffort).",
		}, "\n")
	case "init_error":
		return strings.Join([]string{
			"FAULT CONTEXT — Init Container Error:",
			"  - Inspect init container logs and exit codes.",
			"  - Check init container configuration: command, args, environment.",
			"  - Verify dependencies the init container needs (DB, secrets, config files).",
			"  - Check if init container timeout is too short.",
		}, "\n")
	default:
		return ""
	}
}

// BuildDynamicSystemPrompt constructs a stage- and fault-aware system prompt.
// baseRole is the core role description (e.g., "KubeSage's Kubernetes RCA planner").
// stage and faultType add contextual guidance.
func BuildDynamicSystemPrompt(baseRole string, stage DiagnosticStage, faultType string, extraRules ...string) string {
	var parts []string
	parts = append(parts, baseRole)

	if guidance := StageGuidance(stage); guidance != "" {
		parts = append(parts, "", guidance)
	}

	if hint := FaultTypeHint(faultType); hint != "" {
		parts = append(parts, "", hint)
	}

	if len(extraRules) > 0 {
		parts = append(parts, "", strings.Join(extraRules, "\n"))
	}

	return strings.Join(parts, "\n")
}

// PlanSystemPrompt returns the system prompt for the plan generation phase.
func PlanSystemPrompt(stage DiagnosticStage, faultType string) string {
	base := "You are KubeSage's Kubernetes RCA planner."
	rules := []string{
		"Return a JSON object with EXACTLY these fields:",
		`  "plan_summary": string — one-sentence diagnosis strategy,`,
		`  "steps": array of objects, each with "tool_name" (string), "reason" (string), "critical" (bool), "input" (object),`,
		`  "expected_observations": array of strings,`,
		`  "stop_condition": array of strings.`,
		"Each step.tool_name MUST be one of the provided tool names exactly as listed.",
		"Each step.input should contain namespace and pod_name from the goal.",
		"Do not propose remediation execution or cluster mutation.",
		"Return JSON only, no markdown fences.",
	}
	return BuildDynamicSystemPrompt(base, stage, faultType, rules...)
}

// AdjustmentSystemPrompt returns the system prompt for plan adjustment.
func AdjustmentSystemPrompt(stage DiagnosticStage, faultType string) string {
	base := "You are KubeSage's Kubernetes RCA planner revising a diagnostic plan mid-execution."
	rules := []string{
		"You will receive the current plan, observations so far, hypothesis scores, and available tools.",
		"Return a revised JSON plan with EXACTLY these fields:",
		`  "plan_summary": string,`,
		`  "steps": array of objects, each with "tool_name" (string), "reason" (string), "critical" (bool), "input" (object),`,
		`  "expected_observations": array of strings,`,
		`  "stop_condition": array of strings.`,
		"Each step.tool_name MUST be one of the provided tool names exactly as listed.",
		"Do not propose remediation execution or cluster mutation.",
		"Remove steps that are no longer needed and add steps to fill evidence gaps.",
		"Return JSON only, no markdown fences.",
	}
	return BuildDynamicSystemPrompt(base, stage, faultType, rules...)
}

// ReflectionSystemPrompt returns the system prompt for reflection.
func ReflectionSystemPrompt(stage DiagnosticStage, faultType string) string {
	base := "You are KubeSage's reflection engine for Kubernetes root cause analysis."
	rules := []string{
		"Given the current diagnostic plan, observations, hypothesis scores, and evidence,",
		"decide whether the agent should continue investigating or the evidence is sufficient.",
		"Return JSON: {\"should_continue\": bool, \"reason\": string, \"new_steps\": []}",
		"new_steps is optional. Each step: {\"id\": string, \"tool_name\": string, \"input\": object, \"reason\": string, \"critical\": bool}",
		"Only suggest new steps if there are clear evidence gaps that would change the diagnosis.",
		fmt.Sprintf("%s%s%s", "The same tool may be called again only when input is materially different, ", "such as another container_name or a more specific runbook query.", ""),
		"When logs mention a missing config file, prefer k8s.get_config_refs plus k8s.get_events; use runbook.search for a specific known pattern.",
		"k8s.get_pvc is storage evidence and must not be used as a substitute for ConfigMap or Secret inspection.",
		"If multiple high-confidence hypotheses are close, continue and collect distinguishing evidence.",
		"Stop only when the top hypothesis has confidence >= 0.75, top1-top2 gap >= 0.15, and no key evidence is missing.",
	}
	return BuildDynamicSystemPrompt(base, stage, faultType, rules...)
}

// HypothesisSystemPrompt returns the system prompt for hypothesis scoring.
func HypothesisSystemPrompt() string {
	return strings.Join([]string{
		"You are KubeSage's hypothesis scoring engine.",
		"Given hypothesis candidates with evidence-derived confidence scores,",
		"identify semantic conflicts that justify lowering confidence.",
		"Do not raise confidence, invent evidence, or treat a keyword mention as proof of root cause.",
		"Return JSON: {\"hypotheses\": [{\"type\": string, \"confidence\": float, \"summary\": string}]}",
		"Only include hypotheses you want to adjust. Keep type names exactly as provided.",
		"Confidence must be between 0 and 1.",
	}, "\n")
}
