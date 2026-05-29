package k8s

import (
	"context"
	"errors"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

type PodCollector struct {
	client *Client
}

// NewPodCollector creates a Kubernetes Pod collector.
func NewPodCollector(client *Client) *PodCollector {
	return &PodCollector{client: client}
}

// 负责查 Pod 本体：
// GetPod loads one pod by namespace and name.
func (c *PodCollector) GetPod(ctx context.Context, namespace, podName string) (*corev1.Pod, error) {
	if c.client == nil || c.client.Clientset == nil {
		return nil, errors.New("kubernetes client is not initialized")
	}
	ctx, cancel := context.WithTimeout(ctx, c.client.Timeout)
	defer cancel()
	return c.client.Clientset.CoreV1().Pods(namespace).Get(ctx, podName, metav1.GetOptions{})
}
