package service

import (
	"context"
	"fmt"
	"strings"
	"time"

	"kubesage/internal/config"
	"kubesage/internal/k8s"
)

type PodAuthorizer interface {
	AuthorizePodDiagnosis(ctx context.Context, token, namespace, podName string) error
}

type KubernetesAuthorizer struct {
	cfg    config.AuthConfig
	client *k8s.Client
}

func NewKubernetesAuthorizer(cfg config.AuthConfig, client *k8s.Client) *KubernetesAuthorizer {
	return &KubernetesAuthorizer{cfg: cfg, client: client}
}

func (a *KubernetesAuthorizer) AuthorizePodDiagnosis(ctx context.Context, token, namespace, podName string) error {
	if a == nil || !strings.EqualFold(a.cfg.Mode, "kubernetes_tokenreview") {
		return nil
	}
	if a.client == nil {
		return fmt.Errorf("kubernetes authorizer is not configured")
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	user, ok, err := a.client.ReviewToken(ctx, token)
	if err != nil {
		return err
	}
	if !ok || user == nil {
		return fmt.Errorf("token is not authenticated")
	}
	allowed, reason, err := a.client.SubjectCanGetPod(ctx, *user, namespace, podName)
	if err != nil {
		return err
	}
	if !allowed {
		if reason == "" {
			reason = "subject is not allowed to get target pod"
		}
		return fmt.Errorf("%s", reason)
	}
	return nil
}
