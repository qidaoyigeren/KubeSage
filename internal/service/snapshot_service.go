package service

import (
	"context"
	"strings"
	"time"

	"kubesage/internal/config"
	"kubesage/internal/diagnostic"
	"kubesage/internal/k8s"

	"go.uber.org/zap"
	corev1 "k8s.io/api/core/v1"
)

type SnapshotService struct {
	cfg      *config.Config
	log      *zap.Logger
	pods     *k8s.PodCollector
	events   *k8s.EventCollector
	logs     *k8s.LogCollector
	nodes    *k8s.NodeCollector
	topology *k8s.TopologyCollector
}

// NewSnapshotService creates the Kubernetes snapshot collector service.
func NewSnapshotService(cfg *config.Config, client *k8s.Client, log *zap.Logger) *SnapshotService {
	return &SnapshotService{
		cfg:      cfg,
		log:      log,
		pods:     k8s.NewPodCollector(client),
		events:   k8s.NewEventCollector(client),
		logs:     k8s.NewLogCollector(client),
		nodes:    k8s.NewNodeCollector(client),
		topology: k8s.NewTopologyCollector(client),
	}
}

// Collect gathers pod, event, log, topology, and node context for analyzers.
func (s *SnapshotService) Collect(ctx context.Context, req PodDiagnosisRequest) (*diagnostic.DiagnosticContext, error) {
	pod, err := s.pods.GetPod(ctx, req.Namespace, req.PodName)
	if err != nil {
		return nil, err
	}
	result := &diagnostic.DiagnosticContext{
		RequestContext: ctx,
		Namespace:      req.Namespace,
		PodName:        req.PodName,
		Pod:            pod,
		MetricsEnabled: req.IncludeMetrics,
	}

	if req.IncludeEvents || req.IncludeLogs {
		events, err := s.events.ListPodEvents(ctx, pod)
		if err != nil {
			s.log.Warn("collect events failed", zap.Error(err))
		} else {
			result.Events = events
		}
	}
	if req.IncludeLogs {
		faultTime, fallback := resolveFaultTime(pod, result.Events, req.AlertTime)
		before, after := s.logWindow()
		logs, err := s.logs.CollectPodLogs(ctx, pod, k8s.LogCollectionOptions{
			ContainerName: req.ContainerName,
			FaultTime:     faultTime,
			WindowBefore:  before,
			WindowAfter:   after,
		})
		if err != nil {
			s.log.Warn("collect logs failed", zap.Error(err))
		} else {
			result.Logs = logs
			result.FaultTime = faultTime
			result.LogsPrecise = !faultTime.IsZero()
			result.LogFallback = fallback
			if !faultTime.IsZero() {
				result.LogWindowStart = faultTime.Add(-before)
				result.LogWindowEnd = faultTime.Add(after)
			}
		}
	}
	topology, err := s.topology.Collect(ctx, pod)
	if err != nil {
		s.log.Warn("collect topology failed", zap.Error(err))
	} else {
		result.Topology = topology
	}
	nodes, err := s.nodes.ListNodeSnapshots(ctx)
	if err != nil {
		s.log.Warn("collect node snapshots failed", zap.Error(err))
	} else {
		result.NodeSnapshots = nodes
	}

	return result, nil
}

// logWindow returns the configured log window with safe defaults.
func (s *SnapshotService) logWindow() (time.Duration, time.Duration) {
	before := 120 * time.Second
	after := 60 * time.Second
	if s.cfg != nil {
		if s.cfg.Diagnosis.LogWindowBeforeSeconds > 0 {
			before = time.Duration(s.cfg.Diagnosis.LogWindowBeforeSeconds) * time.Second
		}
		if s.cfg.Diagnosis.LogWindowAfterSeconds > 0 {
			after = time.Duration(s.cfg.Diagnosis.LogWindowAfterSeconds) * time.Second
		}
	}
	return before, after
}

// resolveFaultTime picks the best known fault timestamp for precise log
// collection: container termination time, then relevant event time, then alert
// time.
func resolveFaultTime(pod *corev1.Pod, events []corev1.Event, alertTime *time.Time) (time.Time, string) {
	if t, ok := latestTerminationTime(pod); ok {
		return t, ""
	}
	if t, ok := latestRelevantEventTime(events); ok {
		return t, ""
	}
	if alertTime != nil && !alertTime.IsZero() {
		return *alertTime, ""
	}
	return time.Time{}, "fault_time unavailable; collected latest 200 log lines"
}

// latestTerminationTime returns the newest lastState.terminated.finishedAt
// across pod containers.
func latestTerminationTime(pod *corev1.Pod) (time.Time, bool) {
	if pod == nil {
		return time.Time{}, false
	}
	var latest time.Time
	for _, status := range pod.Status.ContainerStatuses {
		terminated := status.LastTerminationState.Terminated
		if terminated == nil || terminated.FinishedAt.IsZero() {
			continue
		}
		if latest.IsZero() || terminated.FinishedAt.Time.After(latest) {
			latest = terminated.FinishedAt.Time
		}
	}
	return latest, !latest.IsZero()
}

// latestRelevantEventTime returns the newest timestamp from fault-related pod
// events, falling back to the newest event timestamp if no reason/message is
// obviously fault-related.
func latestRelevantEventTime(events []corev1.Event) (time.Time, bool) {
	var latestRelevant time.Time
	var latestAny time.Time
	for _, event := range events {
		ts, ok := eventTimestamp(event)
		if !ok {
			continue
		}
		if latestAny.IsZero() || ts.After(latestAny) {
			latestAny = ts
		}
		if isFaultEvent(event) && (latestRelevant.IsZero() || ts.After(latestRelevant)) {
			latestRelevant = ts
		}
	}
	if !latestRelevant.IsZero() {
		return latestRelevant, true
	}
	return latestAny, !latestAny.IsZero()
}

// eventTimestamp returns the best timestamp available on a Kubernetes Event.
func eventTimestamp(event corev1.Event) (time.Time, bool) {
	if !event.LastTimestamp.IsZero() {
		return event.LastTimestamp.Time, true
	}
	if !event.EventTime.IsZero() {
		return event.EventTime.Time, true
	}
	if !event.FirstTimestamp.IsZero() {
		return event.FirstTimestamp.Time, true
	}
	return time.Time{}, false
}

// isFaultEvent detects event reasons/messages that usually represent a pod
// failure moment.
func isFaultEvent(event corev1.Event) bool {
	text := strings.ToLower(event.Reason + " " + event.Message)
	keywords := []string{"oom", "backoff", "failed", "error", "unhealthy", "probe"}
	for _, keyword := range keywords {
		if strings.Contains(text, keyword) {
			return true
		}
	}
	return false
}
