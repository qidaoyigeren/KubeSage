package agent

import (
	"strings"

	"kubesage/internal/diagnostic"
)

type VerificationPlanner struct{}

// NewVerificationPlanner creates the remediation verification planner.
func NewVerificationPlanner() *VerificationPlanner {
	return &VerificationPlanner{}
}

// Build creates a verification plan for every remediation proposal. The plan
// describes what to check later; MVP does not execute real post-change checks.
func (p *VerificationPlanner) Build(actions []diagnostic.RemediationAction) []VerificationPlan {
	plans := make([]VerificationPlan, 0, len(actions))
	for _, action := range actions {
		plans = append(plans, verificationForAction(action))
	}
	return plans
}

func verificationForAction(action diagnostic.RemediationAction) VerificationPlan {
	plan := VerificationPlan{
		ActionID:         action.ActionID,
		WhatToCheck:      "Re-check pod status, events, and relevant logs after the proposed action.",
		ToolToUse:        "k8s.get_events",
		SuccessCondition: "No new warning events for the original failure mode.",
		TimeoutSeconds:   300,
	}
	switch strings.ToLower(action.ActionType) {
	case "adjust_memory_limit":
		plan.WhatToCheck = "Check whether OOMKilled events stop and memory working set stays below the reviewed limit."
		plan.ToolToUse = "prometheus.query_range"
		plan.SuccessCondition = "No new OOMKilled termination and memory usage remains below limit."
	case "extend_probe_initial_delay", "fix_probe_path":
		plan.WhatToCheck = "Check readiness/liveness events and service endpoints after probe changes."
		plan.ToolToUse = "k8s.get_events"
		plan.SuccessCondition = "Readiness/liveness probe failures decrease or disappear."
	case "check_configmap_secret":
		plan.WhatToCheck = "Check CrashLoopBackOff events and previous logs after configuration correction."
		plan.ToolToUse = "k8s.get_previous_logs"
		plan.SuccessCondition = "CrashLoopBackOff stops and startup logs no longer show config errors."
	case "check_node_taint_toleration":
		plan.WhatToCheck = "Check FailedScheduling events after node selector, affinity, or toleration review."
		plan.ToolToUse = "k8s.get_events"
		plan.SuccessCondition = "Scheduler no longer emits the original constraint failure."
	case "view_previous_logs":
		plan.WhatToCheck = "Confirm the suspected log signature matches the root-cause hypothesis."
		plan.ToolToUse = "k8s.get_previous_logs"
		plan.SuccessCondition = "Observed log evidence supports or rejects the active hypothesis."
	}
	return plan
}
