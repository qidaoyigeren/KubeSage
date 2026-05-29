package k8s

import (
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
