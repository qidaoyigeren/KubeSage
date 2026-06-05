# Local Full Startup

This is the full local topology:

- MySQL runs on the host and is configured in `configs/config.yaml`.
- Redis runs in Docker.
- Postgres + pgvector runs in Docker for Runbook vector search.
- Prometheus runs in Kubernetes for real cluster metrics.
- Loki runs in Kubernetes for real Pod logs.
- Promtail runs in Kubernetes and ships Pod logs to Loki.
- OpenTelemetry Collector runs in Docker.
- KubeSage backend can run either on the host with `go run ./cmd/server` or in Docker.
- The React frontend runs on the host with Vite.

## 1. Start infrastructure

```powershell
docker compose up -d redis pgvector otel-collector
```

If pgvector was previously created with the wrong embedding dimension, reset only
the vector store:

```powershell
docker compose down
docker volume rm kubesage_pgvector_data
docker compose up -d redis pgvector otel-collector
```

Deploy the in-cluster metrics/log stack:

```powershell
kubectl apply -k deployments/observability
kubectl -n kubesage-observability port-forward --address 0.0.0.0 svc/prometheus 9091:9090
```

Open another terminal:

```powershell
kubectl -n kubesage-observability port-forward --address 0.0.0.0 svc/loki 3102:3100
```

## 2. Host backend configuration

For host `go run ./cmd/server`, use loopback addresses:

```yaml
mysql:
  host: "127.0.0.1"

redis:
  enabled: true
  address: "127.0.0.1:6379"

prometheus:
  base_url: "http://127.0.0.1:9091"

loki:
  enabled: true
  base_url: "http://127.0.0.1:3102"

otel:
  enabled: true
  exporter: "otlphttp"
  endpoint: "127.0.0.1:4318"

rag:
  vector_store: "pgvector"
  pgvector:
    dsn: "postgres://kubesage:kubesage@127.0.0.1:5432/kubesage_vector?sslmode=disable"
```

For a Docker backend, the Compose file already provides environment overrides
for Redis, pgvector, Prometheus, Loki, and OTel. Prometheus and Loki point to
the Kubernetes port-forwards through `host.docker.internal`. MySQL still comes
from `configs/config.yaml`, so use `host.docker.internal` for `mysql.host` if
the backend runs in Docker.

## 3. Start backend on host

```powershell
go mod download
go run ./cmd/server
```

Health and observability checks:

```powershell
curl http://127.0.0.1:8080/healthz
curl http://127.0.0.1:8080/readyz
curl http://127.0.0.1:8080/metrics
curl http://127.0.0.1:9091/-/ready
curl http://127.0.0.1:3102/ready
```

## 4. Start frontend

```powershell
cd web
npm install
npm run dev
```

Open:

```text
http://127.0.0.1:5173
```

## 5. Connect Kubernetes metrics and logs

For real RCA metrics/logs, deploy the in-cluster collectors:

```powershell
kubectl apply -k deployments/observability
kubectl -n kubesage-observability port-forward --address 0.0.0.0 svc/prometheus 9091:9090
kubectl -n kubesage-observability port-forward --address 0.0.0.0 svc/loki 3102:3100
```

Details and validation commands are in `docs/K8S_OBSERVABILITY.md`.

## 6. What each observability component does

Prometheus has two roles:

- It scrapes KubeSage's own `/metrics` endpoint for system health and agent
  runtime metrics.
- It can also serve Kubernetes workload metrics for RCA, but only if your
  cluster metrics are scraped into this Prometheus or you point
  `prometheus.base_url` to an existing cluster Prometheus.

Loki is queried by `loki.query_logs` for centralized pod logs. The provided
Kubernetes Promtail DaemonSet tails `/var/log/pods` and labels logs with
`namespace`, `pod`, and `container`.

OpenTelemetry Collector receives KubeSage traces over OTLP HTTP. The provided
collector config exports traces with the debug exporter, so spans are visible in
the collector logs:

```powershell
docker compose logs -f otel-collector
```

For a UI, add Tempo/Jaeger/Grafana later; the current collector is enough to
verify that KubeSage emits traces.
