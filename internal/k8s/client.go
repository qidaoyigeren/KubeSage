package k8s

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"kubesage/internal/config"

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
