package k8s

import (
	"context"
	"errors"

	"kubesage/internal/diagnostic"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

type NodeCollector struct {
	client *Client
}

// NewNodeCollector creates a Kubernetes Node collector.
func NewNodeCollector(client *Client) *NodeCollector {
	return &NodeCollector{client: client}
}

// 负责查 Node 基础资源快照：
// ListNodeSnapshots lists node allocatable resources and readiness state.
func (c *NodeCollector) ListNodeSnapshots(ctx context.Context) ([]diagnostic.NodeSnapshot, error) {
	if c.client == nil || c.client.Clientset == nil {
		return nil, errors.New("kubernetes client is not initialized")
	}
	ctx, cancel := context.WithTimeout(ctx, c.client.Timeout)
	defer cancel()
	nodes, err := c.client.Clientset.CoreV1().Nodes().List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, err
	}
	result := make([]diagnostic.NodeSnapshot, 0, len(nodes.Items))
	for _, node := range nodes.Items {
		result = append(result, diagnostic.NodeSnapshot{
			Name:              node.Name,
			AllocatableCPU:    node.Status.Allocatable.Cpu().String(),
			AllocatableMemory: node.Status.Allocatable.Memory().String(),
			Ready:             isNodeReady(node),
		})
	}
	return result, nil
}

// isNodeReady reports whether the NodeReady condition is true.
func isNodeReady(node corev1.Node) bool {
	for _, condition := range node.Status.Conditions {
		if condition.Type == corev1.NodeReady {
			return condition.Status == corev1.ConditionTrue
		}
	}
	return false
}
