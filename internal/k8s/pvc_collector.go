package k8s

import (
	"context"
	"errors"
	"fmt"

	"kubesage/internal/diagnostic"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

type PVCCollector struct {
	client *Client
}

// NewPVCCollector creates a collector for PVCs referenced by a pod.
func NewPVCCollector(client *Client) *PVCCollector {
	return &PVCCollector{client: client}
}

// ListPodPVCs loads PVC phases for every persistentVolumeClaim volume on a pod.
func (c *PVCCollector) ListPodPVCs(ctx context.Context, pod *corev1.Pod) ([]diagnostic.PVCBrief, error) {
	if c.client == nil || c.client.Clientset == nil {
		return nil, errors.New("kubernetes client is not initialized")
	}
	if pod == nil {
		return nil, fmt.Errorf("pod is nil")
	}
	result := []diagnostic.PVCBrief{}
	for _, volume := range pod.Spec.Volumes {
		if volume.PersistentVolumeClaim == nil {
			continue
		}
		name := volume.PersistentVolumeClaim.ClaimName
		pvc, err := c.client.Clientset.CoreV1().PersistentVolumeClaims(pod.Namespace).Get(ctx, name, metav1.GetOptions{})
		if err != nil {
			result = append(result, diagnostic.PVCBrief{Name: name, Phase: "Unknown"})
			continue
		}
		brief := diagnostic.PVCBrief{
			Name:       pvc.Name,
			Phase:      string(pvc.Status.Phase),
			VolumeName: pvc.Spec.VolumeName,
		}
		if pvc.Spec.StorageClassName != nil {
			brief.StorageClass = *pvc.Spec.StorageClassName
		}
		if storage := pvc.Status.Capacity.Storage(); storage != nil {
			brief.Capacity = storage.String()
		}
		result = append(result, brief)
	}
	return result, nil
}
