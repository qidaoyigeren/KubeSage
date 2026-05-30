package k8s

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"kubesage/internal/config"

	authnv1 "k8s.io/api/authentication/v1"
	authzv1 "k8s.io/api/authorization/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/client-go/util/homedir"
)

type Client struct {
	Clientset *kubernetes.Clientset
	Timeout   time.Duration
	TailLines int64
}

// Ping verifies Kubernetes API connectivity with a cheap discovery request.
func (c *Client) Ping(ctx context.Context) error {
	if c == nil || c.Clientset == nil {
		return fmt.Errorf("kubernetes client is not configured")
	}
	return c.Clientset.Discovery().RESTClient().Get().AbsPath("/version").Do(ctx).Error()
}

func (c *Client) ReviewToken(ctx context.Context, token string) (*authnv1.UserInfo, bool, error) {
	if c == nil || c.Clientset == nil {
		return nil, false, fmt.Errorf("kubernetes client is not configured")
	}
	review, err := c.Clientset.AuthenticationV1().TokenReviews().Create(ctx, &authnv1.TokenReview{
		Spec: authnv1.TokenReviewSpec{Token: token},
	}, metav1.CreateOptions{})
	if err != nil {
		return nil, false, err
	}
	return &review.Status.User, review.Status.Authenticated, nil
}

func (c *Client) SubjectCanGetPod(ctx context.Context, user authnv1.UserInfo, namespace, podName string) (bool, string, error) {
	if c == nil || c.Clientset == nil {
		return false, "", fmt.Errorf("kubernetes client is not configured")
	}
	review, err := c.Clientset.AuthorizationV1().SubjectAccessReviews().Create(ctx, &authzv1.SubjectAccessReview{
		Spec: authzv1.SubjectAccessReviewSpec{
			User:   user.Username,
			Groups: user.Groups,
			ResourceAttributes: &authzv1.ResourceAttributes{
				Namespace: namespace,
				Verb:      "get",
				Group:     "",
				Resource:  "pods",
				Name:      podName,
			},
		},
	}, metav1.CreateOptions{})
	if err != nil {
		return false, "", err
	}
	return review.Status.Allowed, review.Status.Reason, nil
}

// NewClient builds a Kubernetes clientset from kubeconfig or in-cluster config.
func NewClient(cfg config.KubernetesConfig) (*Client, error) {
	restConfig, err := buildRestConfig(cfg.Kubeconfig)
	if err != nil {
		return nil, err
	}
	clientset, err := kubernetes.NewForConfig(restConfig)
	if err != nil {
		return nil, err
	}
	timeout := time.Duration(cfg.RequestTimeoutSeconds) * time.Second
	if timeout <= 0 {
		timeout = 15 * time.Second
	}
	tailLines := cfg.DefaultLogTailLines
	if tailLines <= 0 {
		tailLines = 200
	}
	return &Client{Clientset: clientset, Timeout: timeout, TailLines: tailLines}, nil
}

// buildRestConfig resolves the Kubernetes REST config from the configured path,
// falling back to in-cluster or default home kubeconfig behavior.
func buildRestConfig(kubeconfig string) (*rest.Config, error) {
	if kubeconfig == "" {
		if cfg, err := rest.InClusterConfig(); err == nil {
			return cfg, nil
		}
		kubeconfig = filepath.Join(homedir.HomeDir(), ".kube", "config")
	}
	kubeconfig = expandHome(kubeconfig)
	return clientcmd.BuildConfigFromFlags("", kubeconfig)
}

// expandHome replaces a leading ~ with the current user's home directory.
func expandHome(path string) string {
	if path == "" {
		return path
	}
	if strings.HasPrefix(path, "~") {
		home, err := os.UserHomeDir()
		if err == nil {
			rest := strings.TrimPrefix(path, "~")
			rest = strings.TrimPrefix(rest, "/")
			rest = strings.TrimPrefix(rest, "\\")
			return filepath.Join(home, rest)
		}
	}
	return path
}
