package service

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"kubesage/internal/k8s"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
)

func TestConfigReferenceInspectorReadsLiveObjectsWithoutReturningSecretValues(t *testing.T) {
	server := kubernetesCoreAPIServer(t)
	defer server.Close()
	clientset, err := kubernetes.NewForConfig(&rest.Config{Host: server.URL})
	if err != nil {
		t.Fatal(err)
	}
	service := &SnapshotService{client: &k8s.Client{Clientset: clientset}}

	pod, err := service.GetLivePod(context.Background(), "default", "api-0")
	if err != nil || pod.ResourceVersion != "9" {
		t.Fatalf("expected live Pod, got pod=%#v err=%v", pod, err)
	}
	configMap, err := service.InspectConfigObject(context.Background(), "default", "ConfigMap", "app-config")
	if err != nil || !configMap.Exists || configMap.KeySizes["config.yaml"] != 10 || configMap.KeySizes["binary"] != 3 {
		t.Fatalf("unexpected ConfigMap snapshot: %#v err=%v", configMap, err)
	}
	secret, err := service.InspectConfigObject(context.Background(), "default", "Secret", "app-secret")
	if err != nil || !secret.Exists || secret.KeySizes["token"] != 24 {
		t.Fatalf("unexpected Secret snapshot: %#v err=%v", secret, err)
	}
}

func TestConfigReferenceInspectorDistinguishesNotFound(t *testing.T) {
	server := kubernetesCoreAPIServer(t)
	defer server.Close()
	clientset, err := kubernetes.NewForConfig(&rest.Config{Host: server.URL})
	if err != nil {
		t.Fatal(err)
	}
	service := &SnapshotService{client: &k8s.Client{Clientset: clientset}}

	result, err := service.InspectConfigObject(context.Background(), "default", "ConfigMap", "missing")
	if err != nil {
		t.Fatal(err)
	}
	if result.Exists {
		t.Fatalf("missing object must not be reported as present: %#v", result)
	}
}

func kubernetesCoreAPIServer(t *testing.T) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		var object interface{}
		switch r.URL.Path {
		case "/api/v1/namespaces/default/pods/api-0":
			object = &corev1.Pod{
				TypeMeta:   metav1.TypeMeta{APIVersion: "v1", Kind: "Pod"},
				ObjectMeta: metav1.ObjectMeta{Namespace: "default", Name: "api-0", ResourceVersion: "9"},
			}
		case "/api/v1/namespaces/default/configmaps/app-config":
			object = &corev1.ConfigMap{
				TypeMeta:   metav1.TypeMeta{APIVersion: "v1", Kind: "ConfigMap"},
				ObjectMeta: metav1.ObjectMeta{Namespace: "default", Name: "app-config", ResourceVersion: "10"},
				Data:       map[string]string{"config.yaml": "port: 8080"},
				BinaryData: map[string][]byte{"binary": {1, 2, 3}},
			}
		case "/api/v1/namespaces/default/secrets/app-secret":
			object = &corev1.Secret{
				TypeMeta:   metav1.TypeMeta{APIVersion: "v1", Kind: "Secret"},
				ObjectMeta: metav1.ObjectMeta{Namespace: "default", Name: "app-secret", ResourceVersion: "11"},
				Data:       map[string][]byte{"token": []byte("do-not-return-this-value")},
			}
		default:
			w.WriteHeader(http.StatusNotFound)
			_ = json.NewEncoder(w).Encode(&metav1.Status{
				TypeMeta: metav1.TypeMeta{APIVersion: "v1", Kind: "Status"},
				Status:   metav1.StatusFailure,
				Reason:   metav1.StatusReasonNotFound,
				Code:     http.StatusNotFound,
			})
			return
		}
		if err := json.NewEncoder(w).Encode(object); err != nil {
			t.Fatal(err)
		}
	}))
}
