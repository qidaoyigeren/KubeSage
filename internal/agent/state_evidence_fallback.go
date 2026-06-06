package agent

import (
	"strconv"
	"strings"

	corev1 "k8s.io/api/core/v1"
)

func enforceStateDrivenEvidenceFallback(plan *Plan, state *ToolState) bool {
	if plan == nil || state == nil || state.DiagnosticContext == nil || state.DiagnosticContext.Pod == nil {
		return false
	}
	pod := state.DiagnosticContext.Pod
	requests := evidenceRequestsForPodState(pod)
	added := false
	for _, request := range requests {
		if appendIfMissing(plan, request.tool, request.reason, state) {
			added = true
		}
	}
	return added
}

type evidenceRequest struct {
	tool   string
	reason string
}

func evidenceRequestsForPodState(pod *corev1.Pod) []evidenceRequest {
	if pod == nil {
		return nil
	}
	reasons := strings.ToLower(strings.Join(podStatusReasons(pod), " "))
	requests := []evidenceRequest{}
	add := func(tool, reason string) {
		requests = append(requests, evidenceRequest{tool: tool, reason: reason})
	}

	switch {
	case strings.Contains(reasons, "crashloopbackoff") || strings.Contains(reasons, "error"):
		add("k8s.get_events", "Pod status indicates restart/backoff; collect Events to distinguish scheduler, image, and runtime causes")
		add("k8s.get_logs", "Pod status indicates restart/backoff; collect previous/current logs for startup failure evidence")
		add("k8s.get_topology", "Pod status indicates restart/backoff; collect topology for blast-radius and node context")
	case strings.Contains(reasons, "imagepullbackoff") || strings.Contains(reasons, "errimagepull"):
		add("k8s.get_events", "Pod status indicates image pull failure; collect Events for registry/tag/auth details")
	case strings.Contains(reasons, "oomkilled") || strings.Contains(reasons, "exitcode=137"):
		add("k8s.get_logs", "Pod status indicates OOMKilled; collect logs around the termination window")
		add("prometheus.query_range", "Pod status indicates OOMKilled; collect memory metrics if Prometheus is available")
		add("k8s.get_topology", "Pod status indicates OOMKilled; collect node pressure and workload context")
	case strings.Contains(reasons, "evicted"):
		add("k8s.get_events", "Pod status indicates eviction; collect Events for eviction reason and resource pressure details")
		add("prometheus.query_range", "Pod status indicates eviction; collect node resource usage trends before eviction")
		add("k8s.get_topology", "Pod status indicates eviction; collect node pressure and co-located workload context")
	case pod.Status.Phase == corev1.PodPending:
		add("k8s.get_events", "Pod is Pending; collect scheduler Events")
		add("k8s.get_pvc", "Pod is Pending; inspect referenced PVCs")
		add("k8s.get_topology", "Pod is Pending; collect node and scheduling topology")
	}

	if podReadyFalse(pod) && podHasProbe(pod) {
		add("k8s.get_events", "Pod readiness is false and probes are configured; collect probe Events")
		add("k8s.get_logs", "Pod readiness is false and probes are configured; collect application logs")
	}
	return requests
}

func podStatusReasons(pod *corev1.Pod) []string {
	if pod == nil {
		return nil
	}
	reasons := []string{string(pod.Status.Phase)}
	for _, status := range pod.Status.InitContainerStatuses {
		reasons = append(reasons, containerStatusReasons(status)...)
	}
	for _, status := range pod.Status.ContainerStatuses {
		reasons = append(reasons, containerStatusReasons(status)...)
	}
	for _, condition := range pod.Status.Conditions {
		reasons = append(reasons, string(condition.Type), string(condition.Status), condition.Reason, condition.Message)
	}
	return reasons
}

func containerStatusReasons(status corev1.ContainerStatus) []string {
	reasons := []string{}
	if status.State.Waiting != nil {
		reasons = append(reasons, status.State.Waiting.Reason, status.State.Waiting.Message)
	}
	if status.State.Terminated != nil {
		reasons = append(reasons, status.State.Terminated.Reason, "exitCode="+strconv.Itoa(int(status.State.Terminated.ExitCode)))
	}
	if status.LastTerminationState.Terminated != nil {
		reasons = append(reasons, status.LastTerminationState.Terminated.Reason, "exitCode="+strconv.Itoa(int(status.LastTerminationState.Terminated.ExitCode)))
	}
	return reasons
}
