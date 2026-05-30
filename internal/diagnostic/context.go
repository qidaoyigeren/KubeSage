package diagnostic

import (
	"context"
	"time"

	corev1 "k8s.io/api/core/v1"
)

type DiagnosticContext struct {
	// RequestContext carries the diagnosis worker timeout into optional downstream
	// calls such as Prometheus queries.
	RequestContext context.Context
	Namespace      string
	PodName        string
	Pod            *corev1.Pod
	Events         []corev1.Event
	Logs           []ContainerLogs
	PVCs           []PVCBrief
	Topology       *TopologyInfo
	Correlations   *CorrelationInfo
	MetricTrends   []MetricTrend
	NodeSnapshots  []NodeSnapshot
	RunbookHits    []RunbookHit
	FaultTime      time.Time
	LogWindowStart time.Time
	LogWindowEnd   time.Time
	LogsPrecise    bool
	LogFallback    string
	// MetricsEnabled is controlled by the API request. Analyzers should use it
	// to decide whether optional metric enrichment should run.
	MetricsEnabled bool
}

type ContainerLogs struct {
	ContainerName string
	Current       string
	Previous      string
	Loki          string    `json:"loki,omitempty"`
	FaultTime     time.Time `json:"fault_time,omitempty"`
	WindowStart   time.Time `json:"window_start,omitempty"`
	WindowEnd     time.Time `json:"window_end,omitempty"`
	Precise       bool      `json:"precise"`
	Fallback      string    `json:"fallback,omitempty"`
}

type PVCBrief struct {
	Name                    string `json:"name"`
	Phase                   string `json:"phase"`
	StorageClass            string `json:"storage_class,omitempty"`
	VolumeName              string `json:"volume_name,omitempty"`
	PVName                  string `json:"pv_name,omitempty"`
	PVPhase                 string `json:"pv_phase,omitempty"`
	ReclaimPolicy           string `json:"reclaim_policy,omitempty"`
	Capacity                string `json:"capacity,omitempty"`
	StorageClassProvisioner string `json:"storage_class_provisioner,omitempty"`
	VolumeBindingMode       string `json:"volume_binding_mode,omitempty"`
	SelectedNode            string `json:"selected_node,omitempty"`
}

type TopologyInfo struct {
	ReplicaSetName    string          `json:"replica_set_name,omitempty"`
	DeploymentName    string          `json:"deployment_name,omitempty"`
	NodeName          string          `json:"node_name,omitempty"`
	Workload          *WorkloadInfo   `json:"workload,omitempty"`
	OtherPods         []PodBrief      `json:"other_pods,omitempty"`
	SelectedServices  []ServiceBrief  `json:"selected_services,omitempty"`
	EndpointSummaries []EndpointBrief `json:"endpoint_summaries,omitempty"`
	Node              *NodeHealth     `json:"node,omitempty"`
}

type PodBrief struct {
	Name     string `json:"name"`
	Phase    string `json:"phase"`
	Ready    bool   `json:"ready"`
	Abnormal bool   `json:"abnormal"`
	Restarts int32  `json:"restarts"`
}

type WorkloadInfo struct {
	Kind              string `json:"kind,omitempty"`
	Name              string `json:"name,omitempty"`
	DesiredReplicas   int32  `json:"desired_replicas"`
	TotalPods         int    `json:"total_pods"`
	OtherPods         int    `json:"other_pods"`
	OtherRunning      int    `json:"other_running"`
	OtherReady        int    `json:"other_ready"`
	OtherAbnormal     int    `json:"other_abnormal"`
	OtherRestartCount int32  `json:"other_restart_count"`
}

type ServiceBrief struct {
	Name               string            `json:"name"`
	Type               string            `json:"type"`
	Selector           map[string]string `json:"selector"`
	AvailableEndpoints int               `json:"available_endpoints"`
}

type EndpointBrief struct {
	ServiceName        string `json:"service_name"`
	AvailableEndpoints int    `json:"available_endpoints"`
	EndpointSlices     int    `json:"endpoint_slices"`
}

type NodeHealth struct {
	Name           string `json:"name"`
	Ready          bool   `json:"ready"`
	MemoryPressure bool   `json:"memory_pressure"`
	DiskPressure   bool   `json:"disk_pressure"`
	PIDPressure    bool   `json:"pid_pressure"`
}

type CorrelationInfo struct {
	NodeName          string     `json:"node_name,omitempty"`
	DeploymentName    string     `json:"deployment_name,omitempty"`
	Namespace         string     `json:"namespace,omitempty"`
	NodeAbnormalPods  []PodBrief `json:"node_abnormal_pods,omitempty"`
	PeerAbnormalPods  []PodBrief `json:"peer_abnormal_pods,omitempty"`
	NamespaceHotPods  []PodBrief `json:"namespace_hot_pods,omitempty"`
	NodeAbnormalCount int        `json:"node_abnormal_count"`
	PeerAbnormalCount int        `json:"peer_abnormal_count"`
	NamespaceHotCount int        `json:"namespace_hot_count"`
}

type MetricTrend struct {
	Profile        string    `json:"profile"`
	Metric         string    `json:"metric"`
	Window         string    `json:"window"`
	Classification string    `json:"classification"`
	Query          string    `json:"query,omitempty"`
	SampleCount    int       `json:"sample_count"`
	FirstValue     float64   `json:"first_value"`
	LastValue      float64   `json:"last_value"`
	MinValue       float64   `json:"min_value"`
	MaxValue       float64   `json:"max_value"`
	AvgValue       float64   `json:"avg_value"`
	GrowthRatio    float64   `json:"growth_ratio"`
	Error          string    `json:"error,omitempty"`
	WindowStart    time.Time `json:"window_start,omitempty"`
	WindowEnd      time.Time `json:"window_end,omitempty"`
}

type NodeSnapshot struct {
	Name              string `json:"name"`
	AllocatableCPU    string `json:"allocatable_cpu"`
	AllocatableMemory string `json:"allocatable_memory"`
	Ready             bool   `json:"ready"`
}

type RunbookHit struct {
	Title   string `json:"title"`
	Content string `json:"content"`
}

type EvidenceRecord struct {
	SourceType string
	Title      string
	Content    string
	Severity   string
	Raw        interface{}
	Timestamp  time.Time
}
