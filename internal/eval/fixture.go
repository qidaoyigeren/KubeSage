package eval

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"kubesage/internal/diagnostic"

	corev1 "k8s.io/api/core/v1"
)

// DiagnosticFixture is the portable JSON shape used for deterministic RCA
// replay. It deliberately omits RequestContext, which is process-local state.
type DiagnosticFixture struct {
	Namespace      string                      `json:"namespace"`
	PodName        string                      `json:"pod_name"`
	Pod            *corev1.Pod                 `json:"pod,omitempty"`
	Events         []corev1.Event              `json:"events,omitempty"`
	Logs           []diagnostic.ContainerLogs  `json:"logs,omitempty"`
	PVCs           []diagnostic.PVCBrief       `json:"pvcs,omitempty"`
	Topology       *diagnostic.TopologyInfo    `json:"topology,omitempty"`
	Correlations   *diagnostic.CorrelationInfo `json:"correlations,omitempty"`
	MetricTrends   []diagnostic.MetricTrend    `json:"metric_trends,omitempty"`
	NodeSnapshots  []diagnostic.NodeSnapshot   `json:"node_snapshots,omitempty"`
	RunbookHits    []diagnostic.RunbookHit     `json:"runbook_hits,omitempty"`
	FaultTime      time.Time                   `json:"fault_time,omitempty"`
	LogWindowStart time.Time                   `json:"log_window_start,omitempty"`
	LogWindowEnd   time.Time                   `json:"log_window_end,omitempty"`
	LogsPrecise    bool                        `json:"logs_precise"`
	LogFallback    string                      `json:"log_fallback,omitempty"`
	MetricsEnabled bool                        `json:"metrics_enabled"`
}

// FixtureFromContext converts an in-memory diagnostic snapshot into the JSON
// fixture shape.
func FixtureFromContext(ctx *diagnostic.DiagnosticContext) DiagnosticFixture {
	if ctx == nil {
		return DiagnosticFixture{}
	}
	namespace := ctx.Namespace
	podName := ctx.PodName
	if ctx.Pod != nil {
		if namespace == "" {
			namespace = ctx.Pod.Namespace
		}
		if podName == "" {
			podName = ctx.Pod.Name
		}
	}
	return DiagnosticFixture{
		Namespace:      namespace,
		PodName:        podName,
		Pod:            ctx.Pod,
		Events:         ctx.Events,
		Logs:           ctx.Logs,
		PVCs:           ctx.PVCs,
		Topology:       ctx.Topology,
		Correlations:   ctx.Correlations,
		MetricTrends:   ctx.MetricTrends,
		NodeSnapshots:  ctx.NodeSnapshots,
		RunbookHits:    ctx.RunbookHits,
		FaultTime:      ctx.FaultTime,
		LogWindowStart: ctx.LogWindowStart,
		LogWindowEnd:   ctx.LogWindowEnd,
		LogsPrecise:    ctx.LogsPrecise,
		LogFallback:    ctx.LogFallback,
		MetricsEnabled: ctx.MetricsEnabled,
	}
}

// ToContext restores a fixture into a DiagnosticContext ready for analyzers.
func (f DiagnosticFixture) ToContext(parent context.Context) *diagnostic.DiagnosticContext {
	if parent == nil {
		parent = context.Background()
	}
	namespace := f.Namespace
	podName := f.PodName
	if f.Pod != nil {
		if namespace == "" {
			namespace = f.Pod.Namespace
		}
		if podName == "" {
			podName = f.Pod.Name
		}
	}
	return &diagnostic.DiagnosticContext{
		RequestContext: parent,
		Namespace:      namespace,
		PodName:        podName,
		Pod:            f.Pod,
		Events:         f.Events,
		Logs:           f.Logs,
		PVCs:           f.PVCs,
		Topology:       f.Topology,
		Correlations:   f.Correlations,
		MetricTrends:   f.MetricTrends,
		NodeSnapshots:  f.NodeSnapshots,
		RunbookHits:    f.RunbookHits,
		FaultTime:      f.FaultTime,
		LogWindowStart: f.LogWindowStart,
		LogWindowEnd:   f.LogWindowEnd,
		LogsPrecise:    f.LogsPrecise,
		LogFallback:    f.LogFallback,
		MetricsEnabled: f.MetricsEnabled,
	}
}

// MarshalFixture serializes a DiagnosticContext fixture as indented JSON.
func MarshalFixture(ctx *diagnostic.DiagnosticContext) ([]byte, error) {
	data, err := json.MarshalIndent(FixtureFromContext(ctx), "", "  ")
	if err != nil {
		return nil, fmt.Errorf("marshal fixture: %w", err)
	}
	return append(data, '\n'), nil
}

// UnmarshalFixture parses a fixture JSON document.
func UnmarshalFixture(data []byte, parent context.Context) (*diagnostic.DiagnosticContext, error) {
	var fixture DiagnosticFixture
	if err := json.Unmarshal(data, &fixture); err != nil {
		return nil, fmt.Errorf("unmarshal fixture: %w", err)
	}
	ctx := fixture.ToContext(parent)
	if ctx.Namespace == "" || ctx.PodName == "" {
		return nil, fmt.Errorf("fixture must include namespace and pod_name or pod metadata")
	}
	return ctx, nil
}

// ReadFixture loads one DiagnosticContext fixture from disk.
func ReadFixture(path string, parent context.Context) (*diagnostic.DiagnosticContext, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read fixture %s: %w", path, err)
	}
	return UnmarshalFixture(data, parent)
}

// WriteFixture writes one DiagnosticContext fixture to disk.
func WriteFixture(path string, ctx *diagnostic.DiagnosticContext) error {
	data, err := MarshalFixture(ctx)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("create fixture dir: %w", err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		return fmt.Errorf("write fixture %s: %w", path, err)
	}
	return nil
}
