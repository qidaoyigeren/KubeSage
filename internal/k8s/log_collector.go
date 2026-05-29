package k8s

import (
	"context"
	"errors"
	"io"

	"kubesage/internal/diagnostic"

	corev1 "k8s.io/api/core/v1"
)

type LogCollector struct {
	client *Client
}

func NewLogCollector(client *Client) *LogCollector {
	return &LogCollector{client: client}
}

func (c *LogCollector) CollectPodLogs(ctx context.Context, pod *corev1.Pod, containerName string) ([]diagnostic.ContainerLogs, error) {
	if c.client == nil || c.client.Clientset == nil {
		return nil, errors.New("kubernetes client is not initialized")
	}
	if pod == nil {
		return nil, errors.New("pod is nil")
	}
	names := []string{}
	if containerName != "" {
		names = append(names, containerName)
	} else {
		for _, container := range pod.Spec.Containers {
			names = append(names, container.Name)
		}
	}

	result := make([]diagnostic.ContainerLogs, 0, len(names))
	for _, name := range names {
		// Current and previous logs are best-effort; one failed stream should not
		// prevent the rest of the diagnostic context from being collected.
		current, _ := c.getLogs(ctx, pod.Namespace, pod.Name, name, false)
		previous, _ := c.getLogs(ctx, pod.Namespace, pod.Name, name, true)
		result = append(result, diagnostic.ContainerLogs{
			ContainerName: name,
			Current:       current,
			Previous:      previous,
		})
	}
	return result, nil
}

func (c *LogCollector) getLogs(ctx context.Context, namespace, podName, container string, previous bool) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, c.client.Timeout)
	defer cancel()
	tail := c.client.TailLines
	req := c.client.Clientset.CoreV1().Pods(namespace).GetLogs(podName, &corev1.PodLogOptions{
		Container: container,
		Previous:  previous,
		TailLines: &tail,
	})
	stream, err := req.Stream(ctx)
	if err != nil {
		return "", err
	}
	defer stream.Close()
	bytes, err := io.ReadAll(stream)
	if err != nil {
		return "", err
	}
	return string(bytes), nil
}
