package k8s

import (
	"context"
	"errors"

	"kubesage/internal/diagnostic"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

type CorrelationCollector struct {
	client *Client
}

func NewCorrelationCollector(client *Client) *CorrelationCollector {
	return &CorrelationCollector{client: client}
}

func (c *CorrelationCollector) Collect(ctx context.Context, pod *corev1.Pod, topology *diagnostic.TopologyInfo) (*diagnostic.CorrelationInfo, error) {
	if c.client == nil || c.client.Clientset == nil {
		return nil, errors.New("kubernetes client is not initialized")
	}
	if pod == nil {
		return nil, errors.New("pod is nil")
	}
	ctx, cancel := context.WithTimeout(ctx, c.client.Timeout)
	defer cancel()

	info := &diagnostic.CorrelationInfo{
		NodeName:  pod.Spec.NodeName,
		Namespace: pod.Namespace,
	}
	if topology != nil {
		info.DeploymentName = topology.DeploymentName
		info.PeerAbnormalPods = abnormalPods(topology.OtherPods, 20)
		info.PeerAbnormalCount = len(info.PeerAbnormalPods)
	}
	if pod.Spec.NodeName != "" {
		info.NodeAbnormalPods = c.listNodeAbnormalPods(ctx, pod)
		info.NodeAbnormalCount = len(info.NodeAbnormalPods)
	}
	info.NamespaceHotPods = c.listNamespaceHotPods(ctx, pod)
	info.NamespaceHotCount = len(info.NamespaceHotPods)
	return info, nil
}

func (c *CorrelationCollector) listNodeAbnormalPods(ctx context.Context, current *corev1.Pod) []diagnostic.PodBrief {
	pods, err := c.client.Clientset.CoreV1().Pods("").List(ctx, metav1.ListOptions{FieldSelector: "spec.nodeName=" + current.Spec.NodeName})
	if err != nil {
		pods, err = c.client.Clientset.CoreV1().Pods(current.Namespace).List(ctx, metav1.ListOptions{FieldSelector: "spec.nodeName=" + current.Spec.NodeName})
		if err != nil {
			return nil
		}
	}
	result := make([]diagnostic.PodBrief, 0)
	for _, item := range pods.Items {
		if item.Namespace == current.Namespace && item.Name == current.Name {
			continue
		}
		brief := podBrief(item)
		if brief.Abnormal {
			result = append(result, brief)
			if len(result) >= 20 {
				return result
			}
		}
	}
	return result
}

func (c *CorrelationCollector) listNamespaceHotPods(ctx context.Context, current *corev1.Pod) []diagnostic.PodBrief {
	pods, err := c.client.Clientset.CoreV1().Pods(current.Namespace).List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil
	}
	result := make([]diagnostic.PodBrief, 0)
	for _, item := range pods.Items {
		if item.Name == current.Name {
			continue
		}
		brief := podBrief(item)
		if brief.Abnormal || brief.Restarts > 0 {
			result = append(result, brief)
			if len(result) >= 20 {
				return result
			}
		}
	}
	return result
}

func abnormalPods(pods []diagnostic.PodBrief, limit int) []diagnostic.PodBrief {
	result := make([]diagnostic.PodBrief, 0)
	for _, pod := range pods {
		if !pod.Abnormal && pod.Restarts == 0 {
			continue
		}
		result = append(result, pod)
		if limit > 0 && len(result) >= limit {
			return result
		}
	}
	return result
}
