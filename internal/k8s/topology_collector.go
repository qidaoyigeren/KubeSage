package k8s

import (
	"context"
	"errors"

	"kubesage/internal/diagnostic"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	discoveryv1 "k8s.io/api/discovery/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
)

type TopologyCollector struct {
	client *Client
}

// NewTopologyCollector creates a collector for pod ownership and service
// topology.
func NewTopologyCollector(client *Client) *TopologyCollector {
	return &TopologyCollector{client: client}
}

// 负责查 Pod 所属拓扑关系
// Collect gathers owner, sibling pod, service, endpoint, and node topology for
// a pod.
func (c *TopologyCollector) Collect(ctx context.Context, pod *corev1.Pod) (*diagnostic.TopologyInfo, error) {
	if c.client == nil || c.client.Clientset == nil {
		return nil, errors.New("kubernetes client is not initialized")
	}
	if pod == nil {
		return nil, errors.New("pod is nil")
	}
	ctx, cancel := context.WithTimeout(ctx, c.client.Timeout)
	defer cancel()

	info := &diagnostic.TopologyInfo{NodeName: pod.Spec.NodeName}
	rs := c.findReplicaSet(ctx, pod)
	if rs != nil {
		info.ReplicaSetName = rs.Name
		if deploy := c.findDeployment(ctx, rs); deploy != nil {
			info.DeploymentName = deploy.Name
			info.OtherPods, info.Workload = c.collectDeploymentPods(ctx, deploy, pod.Name)
		}
	}
	info.SelectedServices, info.EndpointSummaries = c.findSelectedServices(ctx, pod)
	info.Node = c.getNodeHealth(ctx, pod.Spec.NodeName)
	return info, nil
}

// findReplicaSet follows the pod owner reference to its ReplicaSet.
func (c *TopologyCollector) findReplicaSet(ctx context.Context, pod *corev1.Pod) *appsv1.ReplicaSet {
	for _, owner := range pod.OwnerReferences {
		if owner.Kind != "ReplicaSet" {
			continue
		}
		rs, err := c.client.Clientset.AppsV1().ReplicaSets(pod.Namespace).Get(ctx, owner.Name, metav1.GetOptions{})
		if err == nil {
			return rs
		}
	}
	return nil
}

// findDeployment follows the ReplicaSet owner reference to its Deployment.
func (c *TopologyCollector) findDeployment(ctx context.Context, rs *appsv1.ReplicaSet) *appsv1.Deployment {
	for _, owner := range rs.OwnerReferences {
		if owner.Kind != "Deployment" {
			continue
		}
		deploy, err := c.client.Clientset.AppsV1().Deployments(rs.Namespace).Get(ctx, owner.Name, metav1.GetOptions{})
		if err == nil {
			return deploy
		}
	}
	return nil
}

// collectDeploymentPods lists sibling pods and summarizes their health.
func (c *TopologyCollector) collectDeploymentPods(ctx context.Context, deploy *appsv1.Deployment, currentPodName string) ([]diagnostic.PodBrief, *diagnostic.WorkloadInfo) {
	selector, err := metav1.LabelSelectorAsSelector(deploy.Spec.Selector)
	if err != nil {
		return nil, nil
	}
	pods, err := c.client.Clientset.CoreV1().Pods(deploy.Namespace).List(ctx, metav1.ListOptions{LabelSelector: selector.String()})
	if err != nil {
		return nil, nil
	}
	desired := int32(0)
	if deploy.Spec.Replicas != nil {
		desired = *deploy.Spec.Replicas
	}
	workload := &diagnostic.WorkloadInfo{
		Kind:            "Deployment",
		Name:            deploy.Name,
		DesiredReplicas: desired,
		TotalPods:       len(pods.Items),
	}
	result := make([]diagnostic.PodBrief, 0, len(pods.Items))
	for _, item := range pods.Items {
		brief := podBrief(item)
		if item.Name == currentPodName {
			continue
		}
		result = append(result, brief)
		workload.OtherPods++
		workload.OtherRestartCount += brief.Restarts
		if item.Status.Phase == corev1.PodRunning {
			workload.OtherRunning++
		}
		if brief.Ready {
			workload.OtherReady++
		}
		if brief.Abnormal {
			workload.OtherAbnormal++
		}
	}
	return result, workload
}

// findSelectedServices finds Services whose selectors match the pod labels.
func (c *TopologyCollector) findSelectedServices(ctx context.Context, pod *corev1.Pod) ([]diagnostic.ServiceBrief, []diagnostic.EndpointBrief) {
	services, err := c.client.Clientset.CoreV1().Services(pod.Namespace).List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, nil
	}
	serviceBriefs := []diagnostic.ServiceBrief{}
	endpointBriefs := []diagnostic.EndpointBrief{}
	for _, service := range services.Items {
		if len(service.Spec.Selector) == 0 || !selectorMatches(service.Spec.Selector, pod.Labels) {
			continue
		}
		endpointSummary := c.countAvailableEndpointSlices(ctx, pod.Namespace, service.Name)
		serviceBriefs = append(serviceBriefs, diagnostic.ServiceBrief{
			Name:               service.Name,
			Type:               string(service.Spec.Type),
			Selector:           service.Spec.Selector,
			AvailableEndpoints: endpointSummary.AvailableEndpoints,
		})
		endpointBriefs = append(endpointBriefs, endpointSummary)
	}
	return serviceBriefs, endpointBriefs
}

// countAvailableEndpointSlices counts available EndpointSlice endpoints for a
// Service.
func (c *TopologyCollector) countAvailableEndpointSlices(ctx context.Context, namespace, serviceName string) diagnostic.EndpointBrief {
	summary := diagnostic.EndpointBrief{ServiceName: serviceName}
	selector := labels.Set{discoveryv1.LabelServiceName: serviceName}.String()
	slices, err := c.client.Clientset.DiscoveryV1().EndpointSlices(namespace).List(ctx, metav1.ListOptions{LabelSelector: selector})
	if err != nil {
		return summary
	}
	summary.EndpointSlices = len(slices.Items)
	for _, slice := range slices.Items {
		for _, endpoint := range slice.Endpoints {
			if endpoint.Conditions.Ready != nil && !*endpoint.Conditions.Ready {
				continue
			}
			summary.AvailableEndpoints += len(endpoint.Addresses)
		}
	}
	return summary
}

// getNodeHealth loads pressure and Ready conditions for the pod's node.
func (c *TopologyCollector) getNodeHealth(ctx context.Context, nodeName string) *diagnostic.NodeHealth {
	if nodeName == "" {
		return nil
	}
	node, err := c.client.Clientset.CoreV1().Nodes().Get(ctx, nodeName, metav1.GetOptions{})
	if err != nil {
		return nil
	}
	health := &diagnostic.NodeHealth{Name: node.Name}
	for _, condition := range node.Status.Conditions {
		isTrue := condition.Status == corev1.ConditionTrue
		switch condition.Type {
		case corev1.NodeReady:
			health.Ready = isTrue
		case corev1.NodeMemoryPressure:
			health.MemoryPressure = isTrue
		case corev1.NodeDiskPressure:
			health.DiskPressure = isTrue
		case corev1.NodePIDPressure:
			health.PIDPressure = isTrue
		}
	}
	return health
}

// selectorMatches reports whether all selector labels are present on the pod.
func selectorMatches(selector, podLabels map[string]string) bool {
	for key, value := range selector {
		if podLabels[key] != value {
			return false
		}
	}
	return true
}

// podReady reports whether the pod Ready condition is true.
func podReady(pod corev1.Pod) bool {
	for _, condition := range pod.Status.Conditions {
		if condition.Type == corev1.PodReady {
			return condition.Status == corev1.ConditionTrue
		}
	}
	return false
}

// podBrief converts a Kubernetes Pod into the compact topology shape.
func podBrief(pod corev1.Pod) diagnostic.PodBrief {
	ready := podReady(pod)
	phase := string(pod.Status.Phase)
	return diagnostic.PodBrief{
		Name:     pod.Name,
		Phase:    phase,
		Ready:    ready,
		Abnormal: pod.Status.Phase != corev1.PodRunning || !ready,
		Restarts: totalRestarts(pod),
	}
}

// totalRestarts sums restart counts across all containers in a pod.
func totalRestarts(pod corev1.Pod) int32 {
	var total int32
	for _, status := range pod.Status.ContainerStatuses {
		total += status.RestartCount
	}
	return total
}
