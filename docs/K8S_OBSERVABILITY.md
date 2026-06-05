# Kubernetes Metrics And Logs For RCA

KubeSage can query Prometheus and Loki during diagnosis, but those systems must
contain Kubernetes workload data.

This repo provides a minimal in-cluster observability stack:

- Prometheus scrapes kubelet cAdvisor metrics for container CPU/memory.
- kube-state-metrics exports pod status metrics such as restart count.
- Loki stores logs.
- Promtail tails pod logs and labels them with `namespace`, `pod`, and
  `container`.

## 1. Deploy in the Kubernetes cluster

```powershell
kubectl apply -k deployments/observability
```

Wait for pods:

```powershell
kubectl -n kubesage-observability get pods
```

## 2. Forward Prometheus and Loki to the host

Use this when KubeSage backend runs locally with `go run ./cmd/server`:

```powershell
kubectl -n kubesage-observability port-forward --address 0.0.0.0 svc/prometheus 9091:9090
```

Open another terminal. This repo uses host port `3102` for the cluster Loki
forward so it does not collide with an optional local Docker Loki on `3100`:

```powershell
kubectl -n kubesage-observability port-forward --address 0.0.0.0 svc/loki 3102:3100
```

Then keep these values in `configs/config.yaml`:

```yaml
prometheus:
  base_url: "http://127.0.0.1:9091"

loki:
  enabled: true
  base_url: "http://127.0.0.1:3102"
```

If KubeSage runs in Docker Compose, keep the same port-forwards running and use
the compose defaults:

```text
KUBESAGE_PROMETHEUS_BASE_URL=http://host.docker.internal:9091
KUBESAGE_LOKI_BASE_URL=http://host.docker.internal:3102
```

If KubeSage runs inside Kubernetes instead, use service DNS:

```yaml
prometheus:
  base_url: "http://prometheus.kubesage-observability.svc.cluster.local:9090"

loki:
  enabled: true
  base_url: "http://loki.kubesage-observability.svc.cluster.local:3100"
```

## 3. Verify Prometheus metrics

Open:

```text
http://127.0.0.1:9091/targets
```

Expected targets:

- `kubernetes-cadvisor`
- `kube-state-metrics`

Run these queries in Prometheus:

```promql
container_memory_working_set_bytes
container_cpu_usage_seconds_total
kube_pod_container_status_restarts_total
```

For a specific pod:

```promql
container_memory_working_set_bytes{namespace="default",pod="your-pod"}
container_cpu_usage_seconds_total{namespace="default",pod="your-pod"}
kube_pod_container_status_restarts_total{namespace="default",pod="your-pod"}
```

## 4. Verify Loki labels

Check Loki readiness:

```powershell
curl http://127.0.0.1:3102/ready
```

Query logs:

```powershell
curl "http://127.0.0.1:3102/loki/api/v1/query_range?query={namespace=`"default`"}&limit=5"
```

For a specific pod:

```powershell
curl "http://127.0.0.1:3102/loki/api/v1/query_range?query={namespace=`"default`",pod=`"your-pod`"}&limit=5"
```

KubeSage's `loki.query_logs` uses this selector shape:

```logql
{namespace="default",pod="your-pod",container="your-container"}
```

## 5. Start KubeSage

With port-forwards running:

```powershell
go run ./cmd/server
```

When a diagnosis uses:

```json
{
  "include_logs": true,
  "include_events": true,
  "include_metrics": true
}
```

KubeSage can enrich evidence with:

- cAdvisor CPU/memory samples from Prometheus
- kube-state-metrics restart counters
- centralized pod logs from Loki

## Notes

This stack is intentionally small and uses `emptyDir` storage. It is enough for
local development and demos. For production, use persistent storage and a managed
Prometheus/Loki setup such as your existing platform observability stack.
