package agent

import (
	"context"
	"fmt"
	"strings"
	"time"

	"kubesage/internal/diagnostic"

	corev1 "k8s.io/api/core/v1"
)

type podConfigReference struct {
	ContainerName      string `json:"container_name"`
	ContainerType      string `json:"container_type"`
	Source             string `json:"source"`
	Kind               string `json:"kind"`
	Name               string `json:"name"`
	Key                string `json:"key,omitempty"`
	VolumeName         string `json:"volume_name,omitempty"`
	MountPath          string `json:"mount_path,omitempty"`
	SubPath            string `json:"sub_path,omitempty"`
	Optional           bool   `json:"optional"`
	LivePodSpec        bool   `json:"live_pod_spec"`
	ObjectInspected    bool   `json:"object_inspected"`
	ObjectExists       *bool  `json:"object_exists,omitempty"`
	KeyExists          *bool  `json:"key_exists,omitempty"`
	ValueBytes         int    `json:"value_bytes,omitempty"`
	ContentStatus      string `json:"content_status,omitempty"`
	ResourceVersion    string `json:"resource_version,omitempty"`
	InspectionError    string `json:"inspection_error,omitempty"`
	SemanticValidation string `json:"semantic_validation"`
}

type configVolumeSource struct {
	kind     string
	name     string
	optional bool
	keys     []string
}

type ConfigObjectSnapshot struct {
	Kind            string         `json:"kind"`
	Namespace       string         `json:"namespace"`
	Name            string         `json:"name"`
	Exists          bool           `json:"exists"`
	ResourceVersion string         `json:"resource_version,omitempty"`
	KeySizes        map[string]int `json:"key_sizes,omitempty"`
}

type ConfigReferenceInspector interface {
	GetLivePod(ctx context.Context, namespace, podName string) (*corev1.Pod, error)
	InspectConfigObject(ctx context.Context, namespace, kind, name string) (ConfigObjectSnapshot, error)
}

func newConfigRefsTool(snapshot SnapshotFunc, inspector ConfigReferenceInspector) Tool {
	const name = "k8s.get_config_refs"
	return simpleTool{
		meta: ToolMetadata{
			Name:        name,
			Description: "Read the live Pod spec and inspect referenced ConfigMap/Secret existence, keys, and non-sensitive value sizes. Semantic content correctness still requires an application schema.",
			InputSchema: map[string]string{"namespace": "string", "pod_name": "string", "container_name": "string"},
			RiskLevel:   "low",
			ReadOnly:    true,
			Timeout:     10 * time.Second,
		},
		fn: func(ctx context.Context, input map[string]interface{}, state *ReadOnlyToolState) ToolResult {
			if state == nil {
				return failedTool(name, "tool state is nil")
			}
			diagCtx := state.GetDiagnosticContext()
			var delta *ToolStateDelta
			livePodSpec := false
			livePodError := ""
			if inspector != nil {
				pod, err := inspector.GetLivePod(ctx, state.GetGoal().Namespace, state.GetGoal().PodName)
				if err == nil && pod != nil {
					diagCtx = &diagnostic.DiagnosticContext{
						Namespace: pod.Namespace,
						PodName:   pod.Name,
						Pod:       pod,
					}
					delta = &ToolStateDelta{DiagnosticContext: diagCtx}
					livePodSpec = true
				} else if err != nil {
					livePodError = err.Error()
				}
			}
			if diagCtx == nil || diagCtx.Pod == nil {
				if snapshot == nil {
					return failedTool(name, "snapshot collector is not configured")
				}
				refreshed, err := snapshot(ctx, snapshotGoalForTool("k8s.get_pod", state.GetGoal(), nil))
				if err != nil {
					return failedTool(name, err.Error())
				}
				diagCtx = diagnosticContextForTool("k8s.get_pod", refreshed)
				delta = &ToolStateDelta{DiagnosticContext: diagCtx}
			}
			if diagCtx == nil || diagCtx.Pod == nil {
				return failedTool(name, "Pod snapshot is unavailable")
			}

			containerName := strings.TrimSpace(stringInput(input, "container_name"))
			refs, containerFound := collectPodConfigReferences(diagCtx.Pod, containerName)
			for index := range refs {
				refs[index].LivePodSpec = livePodSpec
			}
			inspectConfigReferences(ctx, diagCtx.Namespace, refs, inspector)
			result := configReferenceResult(refs, containerName, containerFound)
			if !livePodSpec {
				result.Warnings = append(result.Warnings, "live Pod read was unavailable; configuration references came from the diagnostic snapshot")
			}
			if livePodError != "" {
				result.Warnings = append(result.Warnings, "live Pod read failed: "+livePodError)
			}
			result.StateDelta = delta
			return result
		},
	}
}

func collectPodConfigReferences(pod *corev1.Pod, containerName string) ([]podConfigReference, bool) {
	if pod == nil {
		return nil, false
	}
	volumeSources := make(map[string][]configVolumeSource, len(pod.Spec.Volumes))
	for _, volume := range pod.Spec.Volumes {
		switch {
		case volume.ConfigMap != nil:
			volumeSources[volume.Name] = append(volumeSources[volume.Name], configVolumeSource{
				kind: "ConfigMap", name: volume.ConfigMap.Name, optional: boolValue(volume.ConfigMap.Optional), keys: configMapItemKeys(volume.ConfigMap.Items),
			})
		case volume.Secret != nil:
			volumeSources[volume.Name] = append(volumeSources[volume.Name], configVolumeSource{
				kind: "Secret", name: volume.Secret.SecretName, optional: boolValue(volume.Secret.Optional), keys: secretItemKeys(volume.Secret.Items),
			})
		case volume.PersistentVolumeClaim != nil:
			volumeSources[volume.Name] = append(volumeSources[volume.Name], configVolumeSource{
				kind: "PersistentVolumeClaim", name: volume.PersistentVolumeClaim.ClaimName,
			})
		}
		if volume.Projected != nil {
			for _, source := range volume.Projected.Sources {
				if source.ConfigMap != nil {
					volumeSources[volume.Name] = append(volumeSources[volume.Name], configVolumeSource{
						kind: "ConfigMap", name: source.ConfigMap.Name, optional: boolValue(source.ConfigMap.Optional), keys: configMapItemKeys(source.ConfigMap.Items),
					})
				}
				if source.Secret != nil {
					volumeSources[volume.Name] = append(volumeSources[volume.Name], configVolumeSource{
						kind: "Secret", name: source.Secret.Name, optional: boolValue(source.Secret.Optional), keys: secretItemKeys(source.Secret.Items),
					})
				}
			}
		}
	}

	refs := []podConfigReference{}
	containerFound := containerName == ""
	appendContainer := func(container corev1.Container, containerType string) {
		if containerName != "" && container.Name != containerName {
			return
		}
		containerFound = true
		for _, mount := range container.VolumeMounts {
			for _, source := range volumeSources[mount.Name] {
				ref := podConfigReference{
					ContainerName:      container.Name,
					ContainerType:      containerType,
					Source:             "volume_mount",
					Kind:               source.kind,
					Name:               source.name,
					VolumeName:         mount.Name,
					MountPath:          mount.MountPath,
					SubPath:            mount.SubPath,
					Optional:           source.optional,
					SemanticValidation: "not_available_without_application_schema",
				}
				if len(source.keys) == 0 {
					refs = append(refs, ref)
					continue
				}
				for _, key := range source.keys {
					keyRef := ref
					keyRef.Key = key
					refs = append(refs, keyRef)
				}
			}
		}
		for _, source := range container.EnvFrom {
			if source.ConfigMapRef != nil {
				refs = append(refs, podConfigReference{
					ContainerName: container.Name, ContainerType: containerType, Source: "env_from",
					Kind: "ConfigMap", Name: source.ConfigMapRef.Name, Optional: boolValue(source.ConfigMapRef.Optional),
					SemanticValidation: "not_available_without_application_schema",
				})
			}
			if source.SecretRef != nil {
				refs = append(refs, podConfigReference{
					ContainerName: container.Name, ContainerType: containerType, Source: "env_from",
					Kind: "Secret", Name: source.SecretRef.Name, Optional: boolValue(source.SecretRef.Optional),
					SemanticValidation: "not_available_without_application_schema",
				})
			}
		}
		for _, env := range container.Env {
			if env.ValueFrom == nil {
				continue
			}
			if env.ValueFrom.ConfigMapKeyRef != nil {
				ref := env.ValueFrom.ConfigMapKeyRef
				refs = append(refs, podConfigReference{
					ContainerName: container.Name, ContainerType: containerType, Source: "env",
					Kind: "ConfigMap", Name: ref.Name, Key: ref.Key, Optional: boolValue(ref.Optional),
					SemanticValidation: "not_available_without_application_schema",
				})
			}
			if env.ValueFrom.SecretKeyRef != nil {
				ref := env.ValueFrom.SecretKeyRef
				refs = append(refs, podConfigReference{
					ContainerName: container.Name, ContainerType: containerType, Source: "env",
					Kind: "Secret", Name: ref.Name, Key: ref.Key, Optional: boolValue(ref.Optional),
					SemanticValidation: "not_available_without_application_schema",
				})
			}
		}
	}
	for _, container := range pod.Spec.InitContainers {
		appendContainer(container, "init")
	}
	for _, container := range pod.Spec.Containers {
		appendContainer(container, "app")
	}
	return refs, containerFound
}

func inspectConfigReferences(ctx context.Context, namespace string, refs []podConfigReference, inspector ConfigReferenceInspector) {
	if inspector == nil {
		return
	}
	cache := map[string]ConfigObjectSnapshot{}
	errorsByObject := map[string]string{}
	for index := range refs {
		ref := &refs[index]
		if ref.Kind != "ConfigMap" && ref.Kind != "Secret" {
			continue
		}
		cacheKey := strings.ToLower(ref.Kind) + "/" + ref.Name
		snapshot, ok := cache[cacheKey]
		if !ok {
			if priorError, failed := errorsByObject[cacheKey]; failed {
				ref.InspectionError = priorError
				continue
			}
			var err error
			snapshot, err = inspector.InspectConfigObject(ctx, namespace, ref.Kind, ref.Name)
			if err != nil {
				errorsByObject[cacheKey] = err.Error()
				ref.InspectionError = err.Error()
				continue
			}
			cache[cacheKey] = snapshot
		}
		ref.ObjectInspected = true
		ref.ObjectExists = boolPointer(snapshot.Exists)
		ref.ResourceVersion = snapshot.ResourceVersion
		if !snapshot.Exists {
			ref.ContentStatus = "object_missing"
			continue
		}
		if ref.Key == "" {
			ref.ContentStatus = fmt.Sprintf("object_present_keys=%d", len(snapshot.KeySizes))
			continue
		}
		size, exists := snapshot.KeySizes[ref.Key]
		ref.KeyExists = boolPointer(exists)
		ref.ValueBytes = size
		switch {
		case !exists:
			ref.ContentStatus = "key_missing"
		case size == 0:
			ref.ContentStatus = "empty"
		default:
			ref.ContentStatus = "non_empty"
		}
	}
}

func configReferenceResult(refs []podConfigReference, containerName string, containerFound bool) ToolResult {
	result := ToolResult{Success: true, Data: refs}
	if !containerFound {
		result.Observation = fmt.Sprintf("Container %s was not found in the Pod spec", containerName)
		result.Warnings = []string{result.Observation}
		result.MissingEvidence = []string{fmt.Sprintf("configuration references for container %s", containerName)}
		return result
	}
	if len(refs) == 0 {
		result.Observation = "No ConfigMap, Secret, or PVC references were found in the selected Pod containers"
		result.MissingEvidence = []string{"pod ConfigMap/Secret reference evidence"}
		return result
	}

	inspected, missingObjects, missingKeys, emptyValues := summarizeConfigInspection(refs)
	result.Observation = fmt.Sprintf(
		"Collected %d Pod configuration references; inspected=%d missingObjects=%d missingKeys=%d emptyValues=%d",
		len(refs), inspected, missingObjects, missingKeys, emptyValues,
	)
	result.EvidenceRecords = make([]diagnostic.EvidenceRecord, 0, len(refs))
	for _, ref := range refs {
		severity := "info"
		if ref.ContentStatus == "object_missing" || ref.ContentStatus == "key_missing" || ref.ContentStatus == "empty" || ref.InspectionError != "" {
			severity = "warning"
		}
		result.EvidenceRecords = append(result.EvidenceRecords, diagnostic.EvidenceRecord{
			SourceType: "k8s_config_ref",
			Title:      ref.Kind + " reference",
			Content: fmt.Sprintf(
				"container=%s containerType=%s source=%s kind=%s name=%s key=%s volume=%s mountPath=%s subPath=%s optional=%t livePodSpec=%t objectInspected=%t objectExists=%s keyExists=%s valueBytes=%d contentStatus=%s resourceVersion=%s inspectionError=%q semanticValidation=%s",
				ref.ContainerName, ref.ContainerType, ref.Source, ref.Kind, ref.Name, ref.Key, ref.VolumeName, ref.MountPath, ref.SubPath, ref.Optional,
				ref.LivePodSpec, ref.ObjectInspected, optionalBoolString(ref.ObjectExists), optionalBoolString(ref.KeyExists), ref.ValueBytes, ref.ContentStatus,
				ref.ResourceVersion, ref.InspectionError, ref.SemanticValidation,
			),
			Severity:  severity,
			Raw:       ref,
			Timestamp: time.Now(),
		})
	}
	return result
}

func summarizeConfigInspection(refs []podConfigReference) (inspected, missingObjects, missingKeys, emptyValues int) {
	for _, ref := range refs {
		if ref.ObjectInspected {
			inspected++
		}
		switch ref.ContentStatus {
		case "object_missing":
			missingObjects++
		case "key_missing":
			missingKeys++
		case "empty":
			emptyValues++
		}
	}
	return
}

func configMapItemKeys(items []corev1.KeyToPath) []string {
	keys := make([]string, 0, len(items))
	for _, item := range items {
		keys = append(keys, item.Key)
	}
	return keys
}

func secretItemKeys(items []corev1.KeyToPath) []string {
	return configMapItemKeys(items)
}

func boolPointer(value bool) *bool {
	return &value
}

func optionalBoolString(value *bool) string {
	if value == nil {
		return "not_applicable"
	}
	if *value {
		return "true"
	}
	return "false"
}

func boolValue(value *bool) bool {
	return value != nil && *value
}
