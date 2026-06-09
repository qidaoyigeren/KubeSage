package service

import (
	"context"
	"fmt"
	"strings"

	"kubesage/internal/agent"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func (s *SnapshotService) GetLivePod(ctx context.Context, namespace, podName string) (*corev1.Pod, error) {
	if s == nil || s.client == nil || s.client.Clientset == nil {
		return nil, fmt.Errorf("kubernetes client is not configured")
	}
	return s.client.Clientset.CoreV1().Pods(namespace).Get(ctx, podName, metav1.GetOptions{})
}

func (s *SnapshotService) InspectConfigObject(ctx context.Context, namespace, kind, name string) (agent.ConfigObjectSnapshot, error) {
	result := agent.ConfigObjectSnapshot{
		Kind:      kind,
		Namespace: namespace,
		Name:      name,
		KeySizes:  map[string]int{},
	}
	if s == nil || s.client == nil || s.client.Clientset == nil {
		return result, fmt.Errorf("kubernetes client is not configured")
	}

	switch strings.ToLower(strings.TrimSpace(kind)) {
	case "configmap":
		item, err := s.client.Clientset.CoreV1().ConfigMaps(namespace).Get(ctx, name, metav1.GetOptions{})
		if apierrors.IsNotFound(err) {
			return result, nil
		}
		if err != nil {
			return result, err
		}
		result.Exists = true
		result.ResourceVersion = item.ResourceVersion
		for key, value := range item.Data {
			result.KeySizes[key] = len([]byte(value))
		}
		for key, value := range item.BinaryData {
			result.KeySizes[key] = len(value)
		}
		return result, nil
	case "secret":
		item, err := s.client.Clientset.CoreV1().Secrets(namespace).Get(ctx, name, metav1.GetOptions{})
		if apierrors.IsNotFound(err) {
			return result, nil
		}
		if err != nil {
			return result, err
		}
		result.Exists = true
		result.ResourceVersion = item.ResourceVersion
		for key, value := range item.Data {
			result.KeySizes[key] = len(value)
		}
		return result, nil
	default:
		return result, fmt.Errorf("unsupported config object kind %q", kind)
	}
}

var _ agent.ConfigReferenceInspector = (*SnapshotService)(nil)
