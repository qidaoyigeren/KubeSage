package k8s

import (
	"context"
	"errors"

	"kubesage/internal/diagnostic"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
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
			info.OtherPods = c.listDeploymentPods(ctx, deploy, pod.Name)
		}
	}
	info.SelectedServices, info.EndpointSummaries = c.findSelectedServices(ctx, pod)
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

// listDeploymentPods lists sibling pods owned by the same Deployment.
func (c *TopologyCollector) listDeploymentPods(ctx context.Context, deploy *appsv1.Deployment, currentPodName string) []diagnostic.PodBrief {
	selector := labels.Set(deploy.Spec.Selector.MatchLabels).String()
	pods, err := c.client.Clientset.CoreV1().Pods(deploy.Namespace).List(ctx, metav1.ListOptions{LabelSelector: selector})
	if err != nil {
		return nil
	}
	result := make([]diagnostic.PodBrief, 0, len(pods.Items))
	for _, item := range pods.Items {
		if item.Name == currentPodName {
			continue
		}
		result = append(result, diagnostic.PodBrief{
			Name:     item.Name,
			Phase:    string(item.Status.Phase),
			Ready:    podReady(item),
			Restarts: totalRestarts(item),
		})
	}
	return result
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
		readyCount := c.countReadyEndpoints(ctx, pod.Namespace, service.Name)
		serviceBriefs = append(serviceBriefs, diagnostic.ServiceBrief{
			Name:      service.Name,
			Type:      string(service.Spec.Type),
			Selector:  service.Spec.Selector,
			Endpoints: readyCount,
		})
		endpointBriefs = append(endpointBriefs, diagnostic.EndpointBrief{ServiceName: service.Name, ReadyCount: readyCount})
	}
	return serviceBriefs, endpointBriefs
}

// countReadyEndpoints counts ready endpoint addresses for a Service.
func (c *TopologyCollector) countReadyEndpoints(ctx context.Context, namespace, serviceName string) int {
	endpoints, err := c.client.Clientset.CoreV1().Endpoints(namespace).Get(ctx, serviceName, metav1.GetOptions{})
	if err != nil {
		return 0
	}
	count := 0
	for _, subset := range endpoints.Subsets {
		count += len(subset.Addresses)
	}
	return count
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

// totalRestarts sums restart counts across all containers in a pod.
func totalRestarts(pod corev1.Pod) int32 {
	var total int32
	for _, status := range pod.Status.ContainerStatuses {
		total += status.RestartCount
	}
	return total
}
