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
	Topology       *TopologyInfo
	NodeSnapshots  []NodeSnapshot
	RunbookHits    []RunbookHit
	// MetricsEnabled is controlled by the API request. Analyzers should use it
	// to decide whether optional metric enrichment should run.
	MetricsEnabled bool
}

type ContainerLogs struct {
	ContainerName string
	Current       string
	Previous      string
}

type TopologyInfo struct {
	ReplicaSetName    string          `json:"replica_set_name,omitempty"`
	DeploymentName    string          `json:"deployment_name,omitempty"`
	NodeName          string          `json:"node_name,omitempty"`
	OtherPods         []PodBrief      `json:"other_pods,omitempty"`
	SelectedServices  []ServiceBrief  `json:"selected_services,omitempty"`
	EndpointSummaries []EndpointBrief `json:"endpoint_summaries,omitempty"`
}

type PodBrief struct {
	Name     string `json:"name"`
	Phase    string `json:"phase"`
	Ready    bool   `json:"ready"`
	Restarts int32  `json:"restarts"`
}

type ServiceBrief struct {
	Name      string            `json:"name"`
	Type      string            `json:"type"`
	Selector  map[string]string `json:"selector"`
	Endpoints int               `json:"endpoints"`
}

type EndpointBrief struct {
	ServiceName string `json:"service_name"`
	ReadyCount  int    `json:"ready_count"`
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
