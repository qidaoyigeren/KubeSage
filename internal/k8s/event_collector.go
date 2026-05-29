package k8s

import (
	"context"
	"errors"
	"fmt"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/fields"
)

type EventCollector struct {
	client *Client
}

// NewEventCollector creates a Kubernetes Event collector.
func NewEventCollector(client *Client) *EventCollector {
	return &EventCollector{client: client}
}

// 负责查这个 Pod 相关的 Events：
// ListPodEvents lists Kubernetes Events related to the given pod.
func (c *EventCollector) ListPodEvents(ctx context.Context, pod *corev1.Pod) ([]corev1.Event, error) {
	if c.client == nil || c.client.Clientset == nil {
		return nil, errors.New("kubernetes client is not initialized")
	}
	if pod == nil {
		return nil, fmt.Errorf("pod is nil")
	}
	ctx, cancel := context.WithTimeout(ctx, c.client.Timeout)
	defer cancel()

	selector := fields.AndSelectors(
		fields.OneTermEqualSelector("involvedObject.kind", "Pod"),
		fields.OneTermEqualSelector("involvedObject.name", pod.Name),
		fields.OneTermEqualSelector("involvedObject.namespace", pod.Namespace),
	)
	events, err := c.client.Clientset.CoreV1().Events(pod.Namespace).List(ctx, metav1.ListOptions{FieldSelector: selector.String()})
	if err != nil {
		return nil, err
	}
	return events.Items, nil
}
