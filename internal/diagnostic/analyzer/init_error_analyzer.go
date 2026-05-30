package analyzer

import (
	"fmt"
	"strings"
	"time"

	"kubesage/internal/diagnostic"
)

type InitErrorAnalyzer struct{}

func NewInitErrorAnalyzer() *InitErrorAnalyzer {
	return &InitErrorAnalyzer{}
}

func (a *InitErrorAnalyzer) Name() string { return "init_error" }

func (a *InitErrorAnalyzer) Metadata() diagnostic.AnalyzerMetadata {
	return diagnostic.AnalyzerMetadata{
		Name:             a.Name(),
		FaultType:        "InitError",
		Priority:         80,
		MatchSignals:     []string{"initContainer.waiting.reason", "initContainer.terminated.exitCode!=0", "pod.status.reason starts Init"},
		RequiredEvidence: []string{"k8s_pod_status", "k8s_event", "k8s_log"},
	}
}

func (a *InitErrorAnalyzer) Match(ctx *diagnostic.DiagnosticContext) bool {
	if ctx == nil || ctx.Pod == nil {
		return false
	}
	if strings.HasPrefix(strings.ToLower(ctx.Pod.Status.Reason), "init") {
		return true
	}
	for _, status := range ctx.Pod.Status.InitContainerStatuses {
		if status.State.Waiting != nil && status.State.Waiting.Reason != "" && status.State.Waiting.Reason != "PodInitializing" {
			return true
		}
		if status.LastTerminationState.Terminated != nil && status.LastTerminationState.Terminated.ExitCode != 0 {
			return true
		}
		if status.State.Terminated != nil && status.State.Terminated.ExitCode != 0 {
			return true
		}
	}
	return false
}

func (a *InitErrorAnalyzer) Analyze(ctx *diagnostic.DiagnosticContext) (*diagnostic.AnalyzeResult, error) {
	evidences := []diagnostic.EvidenceRecord{}
	summary := "Init container failed before application containers started."
	actions := []string{
		"Inspect init container command, args, mounted config, and previous logs.",
		"Verify dependencies that init containers wait for, such as migrations, DNS, and service endpoints.",
	}

	for _, status := range ctx.Pod.Status.InitContainerStatuses {
		content := fmt.Sprintf("initContainer=%s ready=%t restartCount=%d", status.Name, status.Ready, status.RestartCount)
		if status.State.Waiting != nil {
			content += fmt.Sprintf(" waitingReason=%s waitingMessage=%s", status.State.Waiting.Reason, status.State.Waiting.Message)
		}
		if status.State.Terminated != nil {
			content += fmt.Sprintf(" terminatedReason=%s exitCode=%d", status.State.Terminated.Reason, status.State.Terminated.ExitCode)
		}
		if status.LastTerminationState.Terminated != nil {
			content += fmt.Sprintf(" lastReason=%s lastExitCode=%d", status.LastTerminationState.Terminated.Reason, status.LastTerminationState.Terminated.ExitCode)
		}
		evidences = append(evidences, diagnostic.EvidenceRecord{
			SourceType: "k8s_pod_status",
			Title:      "Init container status",
			Content:    content,
			Severity:   "critical",
			Raw:        status,
			Timestamp:  time.Now(),
		})
	}
	for _, container := range ctx.Pod.Spec.InitContainers {
		evidences = append(evidences, diagnostic.EvidenceRecord{
			SourceType: "k8s_pod_status",
			Title:      "Init container spec",
			Content:    fmt.Sprintf("initContainer=%s image=%s command=%v args=%v", container.Name, container.Image, container.Command, container.Args),
			Severity:   "info",
			Raw:        container,
			Timestamp:  time.Now(),
		})
	}
	for _, event := range ctx.Events {
		if containsAny(event.Reason+" "+event.Message, []string{"Failed", "BackOff", "Error", "Init"}) {
			evidences = append(evidences, eventEvidence(event, "warning"))
		}
	}
	evidences = append(evidences, keyLogEvidences(ctx.Logs, "Key Init container log fragments", initErrorLogKeywords, "warning")...)

	if hasEvidence(evidences, "k8s_key_log", "") {
		summary = "Init container logs contain failure keywords; root cause is likely init command/config/dependency failure."
	}

	return &diagnostic.AnalyzeResult{
		AnalyzerName:     a.Name(),
		FaultType:        "InitError",
		RootCauseSummary: summary,
		ConfidenceScore:  initErrorConfidence(evidences),
		Evidences:        evidences,
		ImpactAnalysis:   "Application containers will not start until all init containers complete successfully.",
		SuggestedActions: actions,
		RiskLevel:        "medium",
		NeedHumanConfirm: true,
	}, nil
}

var initErrorLogKeywords = []string{"error", "failed", "timeout", "permission denied", "migration", "connection refused"}

func initErrorConfidence(evidences []diagnostic.EvidenceRecord) float64 {
	score := 0.72
	if hasEvidence(evidences, "k8s_pod_status", "Init container status") {
		score += 0.10
	}
	if hasEvidence(evidences, "k8s_key_log", "") {
		score += 0.05
	}
	if hasEvidence(evidences, "k8s_event", "") {
		score += 0.04
	}
	return clampConfidence(score)
}
