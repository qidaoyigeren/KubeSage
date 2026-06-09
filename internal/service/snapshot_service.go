package service

import (
	"context"
	"strings"
	"time"

	"kubesage/internal/config"
	"kubesage/internal/diagnostic"
	"kubesage/internal/k8s"
	"kubesage/internal/loki"
	"kubesage/internal/prometheus"
	"kubesage/internal/resilience"

	"go.uber.org/zap"
	corev1 "k8s.io/api/core/v1"
)

type SnapshotService struct {
	cfg          *config.Config
	log          *zap.Logger
	client       *k8s.Client
	pods         *k8s.PodCollector
	events       *k8s.EventCollector
	logs         *k8s.LogCollector
	nodes        *k8s.NodeCollector
	pvcs         *k8s.PVCCollector
	topology     *k8s.TopologyCollector
	correlations *k8s.CorrelationCollector
	loki         *loki.Client
	prometheus   *prometheus.Client
}

// NewSnapshotService creates the Kubernetes snapshot collector service.
func NewSnapshotService(cfg *config.Config, client *k8s.Client, log *zap.Logger, lokiClient *loki.Client, prometheusClients ...*prometheus.Client) *SnapshotService {
	var prometheusClient *prometheus.Client
	if len(prometheusClients) > 0 {
		prometheusClient = prometheusClients[0]
	}
	return &SnapshotService{
		cfg:          cfg,
		log:          log,
		client:       client,
		pods:         k8s.NewPodCollector(client),
		events:       k8s.NewEventCollector(client),
		logs:         k8s.NewLogCollector(client),
		nodes:        k8s.NewNodeCollector(client),
		pvcs:         k8s.NewPVCCollector(client),
		topology:     k8s.NewTopologyCollector(client),
		correlations: k8s.NewCorrelationCollector(client),
		loki:         lokiClient,
		prometheus:   prometheusClient,
	}
}

// Collect gathers pod, event, log, topology, and node context for analyzers.
func (s *SnapshotService) Collect(ctx context.Context, req PodDiagnosisRequest) (*diagnostic.DiagnosticContext, error) {
	var pod *corev1.Pod
	if err := resilience.Do(ctx, s.retryConfig(), func() error {
		var err error
		pod, err = s.pods.GetPod(ctx, req.Namespace, req.PodName)
		return err
	}); err != nil {
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
		var events []corev1.Event
		if err := resilience.Do(ctx, s.retryConfig(), func() error {
			var err error
			events, err = s.events.ListPodEvents(ctx, pod)
			return err
		}); err != nil {
			s.log.Warn("collect events failed", zap.Error(err))
		} else {
			result.Events = events
		}
	}
	if req.IncludeLogs {
		faultTime, fallback := resolveFaultTime(pod, result.Events, req.AlertTime)
		before, after := s.logWindow()
		var logs []diagnostic.ContainerLogs
		if err := resilience.Do(ctx, s.retryConfig(), func() error {
			var err error
			logs, err = s.logs.CollectPodLogs(ctx, pod, k8s.LogCollectionOptions{
				ContainerName: req.ContainerName,
				FaultTime:     faultTime,
				WindowBefore:  before,
				WindowAfter:   after,
			})
			return err
		}); err != nil {
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
			s.collectLokiLogs(ctx, result)
		}
	}
	var pvcs []diagnostic.PVCBrief
	if err := resilience.Do(ctx, s.retryConfig(), func() error {
		var err error
		pvcs, err = s.pvcs.ListPodPVCs(ctx, pod)
		return err
	}); err != nil {
		s.log.Warn("collect pvc snapshots failed", zap.Error(err))
	} else {
		result.PVCs = pvcs
	}
	var topology *diagnostic.TopologyInfo
	if err := resilience.Do(ctx, s.retryConfig(), func() error {
		var err error
		topology, err = s.topology.Collect(ctx, pod)
		return err
	}); err != nil {
		s.log.Warn("collect topology failed", zap.Error(err))
	} else {
		result.Topology = topology
	}
	var correlations *diagnostic.CorrelationInfo
	if err := resilience.Do(ctx, s.retryConfig(), func() error {
		var err error
		correlations, err = s.correlations.Collect(ctx, pod, result.Topology)
		return err
	}); err != nil {
		s.log.Warn("collect correlation context failed", zap.Error(err))
	} else {
		result.Correlations = correlations
	}
	var nodes []diagnostic.NodeSnapshot
	if err := resilience.Do(ctx, s.retryConfig(), func() error {
		var err error
		nodes, err = s.nodes.ListNodeSnapshots(ctx)
		return err
	}); err != nil {
		s.log.Warn("collect node snapshots failed", zap.Error(err))
	} else {
		result.NodeSnapshots = nodes
	}
	s.collectMetricTrends(ctx, result, req)

	return result, nil
}

// collectLokiLogs enriches collected Kubernetes logs with Loki entries when
// Loki is configured. Failure is warning-only and never blocks diagnosis.
func (s *SnapshotService) collectLokiLogs(ctx context.Context, result *diagnostic.DiagnosticContext) {
	if s == nil || s.loki == nil || !s.loki.Configured() || result == nil || len(result.Logs) == 0 {
		return
	}
	start, end := result.LogWindowStart, result.LogWindowEnd
	if start.IsZero() || end.IsZero() {
		end = time.Now()
		start = end.Add(-10 * time.Minute)
	}
	for i := range result.Logs {
		container := result.Logs[i].ContainerName
		var entries []loki.LogEntry
		if err := resilience.Do(ctx, s.retryConfig(), func() error {
			var err error
			entries, err = s.loki.QueryPodLogs(ctx, result.Namespace, result.PodName, container, start, end)
			return err
		}); err != nil {
			s.log.Warn("collect loki logs failed", zap.String("container", container), zap.Error(err))
			continue
		}
		result.Logs[i].Loki = loki.FormatEntries(entries)
	}
}

// retryConfig returns the configured retry policy for Kubernetes read calls.
func (s *SnapshotService) retryConfig() resilience.RetryConfig {
	if s == nil || s.cfg == nil {
		return resilience.FromMilliseconds(1, 0, 0)
	}
	return resilience.FromMilliseconds(
		s.cfg.Diagnosis.RetryMaxAttempts,
		s.cfg.Diagnosis.RetryInitialBackoffMS,
		s.cfg.Diagnosis.RetryMaxBackoffMS,
	)
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
