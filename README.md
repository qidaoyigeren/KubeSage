# KubeSage

## Agent Runtime MVP

KubeSage enables the Agent Runtime by default:

```yaml
agent:
  enabled: true
  max_steps: 12
  tool_timeout_seconds: 10
  enable_dry_run_preview: true
```

When `agent.enabled=false`, diagnosis falls back to the legacy snapshot + rule analyzer + LLM summary path.

The Agent Runtime records every planning decision, tool call, observation, hypothesis update, remediation policy decision, and verification plan into `agent_steps`, `hypotheses`, and `remediation_executions`. The API builds `agent_timeline` from `agent_steps` at query time; `diagnosis_reports` only stores `agent_execution_summary` and `agent_report_snapshot`.

MVP stop conditions are `max_steps`, `confirmed_hypothesis`, `timeout`, `no_effective_tool`, and `critical_tool_failed`.

MVP remediation is validation-only: no real cluster mutation is executed, `remediation.dry_run_patch` does not run a shell command, dry-run preview only validates policy, medium-risk actions require human approval, high-risk actions are proposal-only, and forbidden actions are always blocked.

Runbook Markdown files may include YAML frontmatter as planner hints:

```yaml
---
fault_type: OOMKilled
checks:
  - inspect termination state
recommended_tools:
  - k8s.get_previous_logs
  - prometheus.query_range
remediation_candidates:
  - adjust_memory_limit
risk_policy:
  adjust_memory_limit: high
stop_conditions:
  - confirmed hypothesis
---
```

Runbook hints can add safe follow-up checks, but they never override the built-in remediation policy.

KubeSage 是一个面向 Kubernetes 高频 Pod 故障的智能 RCA Agent MVP，当前聚焦：

- CrashLoopBackOff
- OOMKilled
- Pod Pending
- Readiness / Liveness Probe Failed

MVP 只做诊断，不做自动修复。报告中的建议动作会标注风险等级和 `need_human_confirm`。

## 技术栈

- Go + Gin
- client-go
- MySQL + GORM
- Viper
- Zap
- Docker Compose
- 预留 Prometheus、Loki、Runbook RAG、Alertmanager Webhook 扩展点

## MySQL

默认配置连接本机或外部 MySQL，不会自动通过 Docker 启动 MySQL。

请先自行创建数据库和账号，也可以直接执行 `scripts/init.sql` 中的 SQL。

默认连接配置见 `configs/config.yaml`：

```yaml
mysql:
  host: "127.0.0.1"
  port: 3306
  username: "kubesage"
  password: "kubesage"
  database: "kubesage"
```

如果临时想用 Docker 启动 MySQL，可以显式启用 compose profile：

```bash
docker compose -f deployments/docker-compose.yaml --profile mysql up -d
```

## 可靠性、安全与可观测性

KubeSage 当前内置了一组轻量级运行保护：

- 手动 API 和 Alertmanager 触发都会经过同一个 per-pod TTL 锁。生产环境建议开启 Redis 分布式锁，默认 300 秒内同一 `namespace/pod` 只允许一个诊断任务运行。
- 诊断 goroutine 内部带 `recover()`，panic 会把任务标记为 failed，并写入错误摘要。
- Kubernetes、Prometheus、LLM 的只读调用支持指数退避重试。
- `/healthz` 是轻量 liveness；`/readyz` 会检查 MySQL ping 和 Kubernetes API ping。
- `/metrics` 通过 Prometheus `client_golang` 暴露指标：`diagnosis_total`、`diagnosis_duration_seconds` histogram、`llm_call_total`、`analyzer_match_total`。
- OpenTelemetry trace 可贯穿 HTTP API、Snapshot、Analyze、LLM、Persist 阶段。

API 认证可通过 Bearer token 开启：

```yaml
server:
  auth_token: ""
```

生产环境建议只通过环境变量注入：

```bash
export KUBESAGE_SERVER_AUTH_TOKEN=change-me
```

开启后，`/api/v1/*` 需要携带：

```bash
Authorization: Bearer change-me
```

诊断可靠性参数：

```yaml
diagnosis:
  task_timeout_seconds: 60
  pod_lock_ttl_seconds: 300
  retry_max_attempts: 2
  retry_initial_backoff_ms: 100
  retry_max_backoff_ms: 1000
```

Redis 分布式锁：

```yaml
redis:
  enabled: true
  address: "redis:6379"
  password: ""
  db: 0
  key_prefix: "kubesage"
```

OpenTelemetry：

```yaml
otel:
  enabled: true
  service_name: "kubesage"
  exporter: "otlp"
  endpoint: "otel-collector:4318"
  insecure: true
```

如果 `otel.exporter=stdout`，trace 会直接输出到标准输出，适合本地调试。

数据库迁移默认启用：

```yaml
migrations:
  enabled: true
  dir: "./migrations"
```

关闭 migrations 时才会回退到 GORM `AutoMigrate`，生产环境不建议关闭。

## 配置 kubeconfig

编辑 `configs/config.yaml`：

```yaml
kubernetes:
  kubeconfig: "~/.kube/config"
```

服务会使用本地 kubeconfig 读取 Kubernetes 集群。当前 RBAC 只需要只读权限：Pods、Pod logs、Events、Services、Endpoints、EndpointSlices、Nodes、ReplicaSets、Deployments。

## Kubernetes 拓扑分析

诊断时会同时采集当前 Pod 的原生拓扑信息，并作为 `source_type=k8s_topology` 的 evidence 保存：

- 通过 `ownerReferences` 找到所属 ReplicaSet
- 通过 ReplicaSet 继续找到所属 Deployment
- 汇总同 Deployment 下其他 Pod 的 running、ready、abnormal 和 restart count
- 查询 selector 命中当前 Pod 的 Service
- 查询这些 Service 的 EndpointSlice，并统计可用 endpoints 数量
- 查询当前 Pod 所在 Node 的 Ready、MemoryPressure、DiskPressure、PIDPressure

报告的 `impact_analysis` 会结合拓扑信息补充影响判断：多副本且其他副本正常时影响较低；Service endpoints 全部不可用时影响较高；Node 存在 Pressure 时提示可能是节点级问题。

Pending 诊断会直接读取 Pod 引用的 PersistentVolumeClaim 状态，并将 PVC `phase/storageClass/volumeName/capacity` 保存为 `source_type=k8s_pvc` 的 evidence。Analyzer 的 `confidence_score` 不再完全依赖固定常量，会根据终止状态、Events、关键日志、Prometheus、PVC、Node 等证据进行加权。

## Prometheus 配置

OOMKilled 诊断在启用 `include_metrics` 时会调用 Prometheus HTTP API，按 `lastState.terminated.finishedAt` 作为故障时间，查询前后 5 分钟的 `container_memory_working_set_bytes`，并将查询结果写入 evidence。

编辑 `configs/config.yaml`：

```yaml
prometheus:
  base_url: "http://prometheus:9090"
  timeout_seconds: 10
  retry_max_attempts: 2
  retry_initial_backoff_ms: 100
  retry_max_backoff_ms: 1000
```

`api_key` 不建议写入配置文件，请通过环境变量注入：

```bash
export KUBESAGE_PROMETHEUS_BASE_URL=http://127.0.0.1:9090
```

如果 Prometheus 未配置、不可访问或查询不到数据，诊断任务不会失败；系统会保存一条 `source_type=prometheus`、`severity=warning` 的 evidence，说明指标查询被跳过或失败。

## Loki 配置

KubeSage 可以把 Loki 作为 Kubernetes Pod logs 之外的日志源。启用后，精准日志窗口会同时查询 Loki `query_range`，并把命中的关键日志作为 evidence 保存。

```yaml
loki:
  enabled: true
  base_url: "http://loki:3100"
  tenant_id: ""
  timeout_seconds: 10
```

默认 Loki selector 使用 `{namespace="<ns>",pod="<pod>",container="<container>"}`。如果集群日志标签不同，需要在 `internal/loki/client.go` 调整查询表达式。

## LLM 与 Runbook RAG

KubeSage 可以在规则诊断完成后调用 DeepSeek 或其他 OpenAI-compatible API，对实时上下文、Evidence、关键日志、Prometheus 摘要和 Runbook 检索结果做自然语言增强总结。LLM 只负责总结、解释和生成建议，不会执行任何操作；如果 LLM 调用失败，系统会自动降级为原有规则报告。

编辑 `configs/config.yaml`：

```yaml
llm:
  enabled: true
  base_url: "https://api.deepseek.com"
  api_key: ""
  model: "deepseek-chat"
  retry_max_attempts: 2
  retry_initial_backoff_ms: 200
  retry_max_backoff_ms: 2000
```

也可以通过环境变量覆盖：

```bash
export KUBESAGE_LLM_ENABLED=true
export KUBESAGE_LLM_BASE_URL=https://api.deepseek.com
export KUBESAGE_LLM_API_KEY=sk-...
export KUBESAGE_LLM_MODEL=deepseek-chat
```

LLM Prompt 会包含：

- Pod 基本信息
- 规则诊断故障类型
- Evidence 列表
- Events 摘要
- 关键日志片段
- Prometheus 指标摘要
- Runbook 检索结果

LLM 必须返回结构化 JSON：

```json
{
  "root_cause_summary": "...",
  "confidence_score": 0.86,
  "evidence_reasoning": "...",
  "suggested_actions": ["..."],
  "risk_level": "medium",
  "need_human_confirm": true
}
```

报告会同时保存 `rule_based_result` 和 `llm_enhanced_summary`。规则结果始终保留，LLM 输出只作为增强总结；高风险命令类建议会被过滤，且所有建议都需要人工确认。

## 精准日志窗口

OOMKilled、CrashLoopBackOff 和 Probe Failed 诊断会优先根据故障时间抓取关键窗口内日志，而不是只取最近日志。故障时间来源优先级为：

1. Pod `lastState.terminated.finishedAt`
2. Pod Events 的 `lastTimestamp`
3. API 或 Alertmanager 传入的 `alert_time` / `startsAt`

窗口配置在 `configs/config.yaml`：

```yaml
diagnosis:
  log_window_before_seconds: 120
  log_window_after_seconds: 60
```

日志采集会用 Kubernetes Pod logs 的 `SinceTime` 查询窗口开始时间，并在本地过滤窗口结束之后的日志。`previous` logs 也会一起采集。若无法确定 `fault_time`，会退化为最近 200 行日志。

命中关键字的日志会作为独立 evidence 保存，`source_type=k8s_key_log`，便于报告中单独查看关键日志片段。

## Remediation 建议

KubeSage 会在规则诊断和拓扑分析之后生成结构化 `remediation_actions`。MVP 阶段所有动作都只是建议，不会自动执行；`command_preview` 仅用于人工复核或 dry-run 预览。

每个动作包含：

```json
{
  "action_type": "view_previous_logs",
  "description": "Review previous logs for container api to inspect the failure window before restart.",
  "command_preview": "kubectl logs -n default api-0 -c api --previous --timestamps --tail=200",
  "risk_level": "low",
  "need_human_confirm": false,
  "executable": false
}
```

当前支持的建议类型：

- `view_previous_logs`：查看 previous logs，低风险，只读。
- `adjust_memory_limit`：调整 memory limit，高风险，必须人工确认，命令预览使用 `--dry-run=server`。
- `check_configmap_secret`：检查 ConfigMap / Secret，低风险，只读。
- `extend_probe_initial_delay`：延长 probe `initialDelaySeconds`，中风险，必须人工确认，命令预览使用 dry-run。
- `fix_probe_path`：修正 readiness / liveness HTTP path，中风险，必须人工确认，命令预览使用 dry-run。
- `check_node_taint_toleration`：检查 node taint / toleration，低风险，只读。

安全约束：

- MVP 阶段 `executable=false`，系统不会自动执行修复。
- 中高风险动作会强制 `need_human_confirm=true`。
- 禁止生成删除数据库、删除 PVC、删除 Namespace 等危险动作；相关命令会被过滤。

## 启动服务

```bash
go mod tidy
go run ./cmd/server
```

构建镜像：

```bash
docker build -t kubesage:latest .
```

Helm 部署模板位于 `deployments/helm/kubesage`：

```bash
helm install kubesage deployments/helm/kubesage -n kubesage --create-namespace
```

健康检查：

```bash
curl http://127.0.0.1:8080/healthz
curl http://127.0.0.1:8080/readyz
curl http://127.0.0.1:8080/metrics
```

## Pod 诊断接口

```bash
curl -X POST http://127.0.0.1:8080/api/v1/diagnose/pod \
  -H "Content-Type: application/json" \
  -d '{
    "namespace": "default",
    "pod_name": "example-pod",
    "include_logs": true,
    "include_events": true,
    "include_metrics": true,
    "alert_time": "2026-05-29T10:00:00+08:00"
  }'
```

返回任务 ID 后查询详情：

```bash
curl http://127.0.0.1:8080/api/v1/diagnose/tasks/1
```

分页查询任务：

```bash
curl "http://127.0.0.1:8080/api/v1/diagnose/tasks?page=1&page_size=20"
```

## Alertmanager Webhook

KubeSage 暴露 Alertmanager 标准 webhook 接口：

```text
POST /api/v1/alertmanager/webhook
```

Alertmanager receiver 示例：

```yaml
receivers:
  - name: kubesage
    webhook_configs:
      - url: http://kubesage.kubesage.svc.cluster.local:8080/api/v1/alertmanager/webhook
        send_resolved: false
```

Webhook 会从每条 alert 的 `labels` 中读取：

- `namespace`
- `pod`
- `container`
- `alertname`
- `severity`

如果 `pod` label 不存在，会尝试从 `annotations.description` 中解析 Pod 名称。当前内置告警类型映射：

| alertname | fault_type |
| --- | --- |
| `KubePodCrashLooping` | `CrashLoopBackOff` |
| `KubePodOOMKilled` | `OOMKilled` |
| `KubePodNotReady` | `ProbeFailed/Pending` |
| `KubePodPending` | `Pending` |

相同 `namespace + pod + alertname` 在 5 分钟内只会创建一个诊断任务，重复 webhook 会返回已有 `task_id`，并标记 `deduped=true`。

```bash
curl -X POST http://127.0.0.1:8080/api/v1/alertmanager/webhook \
  -H "Content-Type: application/json" \
  -d '{
    "receiver": "kubesage",
    "status": "firing",
    "alerts": [
      {
        "status": "firing",
        "labels": {
          "alertname": "KubePodCrashLooping",
          "namespace": "default",
          "pod": "example-pod",
          "container": "app",
          "severity": "warning"
        },
        "annotations": {
          "description": "Pod example-pod is crash looping"
        },
        "startsAt": "2026-05-29T10:00:00+08:00"
      }
    ]
  }'
```

返回示例：

```json
{
  "code": 0,
  "message": "success",
  "data": {
    "tasks": [
      {
        "task_id": 1,
        "namespace": "default",
        "pod_name": "example-pod",
        "container_name": "app",
        "alertname": "KubePodCrashLooping",
        "severity": "warning",
        "fault_type": "CrashLoopBackOff",
        "deduped": false,
        "skipped": false
      }
    ]
  }
}
```

## 示例诊断报告 JSON

```json
{
  "task_id": 1,
  "namespace": "default",
  "pod_name": "api-7c9c9d6b5d-abcde",
  "fault_type": "CrashLoopBackOff",
  "root_cause_summary": "容器退出码为 1，更倾向应用启动失败、配置错误或依赖不可用。",
  "confidence_score": 0.82,
  "evidences": [
    {
      "source_type": "k8s_pod_status",
      "title": "Container restart evidence",
      "content": "container=api restartCount=8 lastReason=Error exitCode=1",
      "severity": "warning"
    },
    {
      "source_type": "k8s_event",
      "title": "BackOff",
      "content": "Back-off restarting failed container api in pod api-7c9c9d6b5d-abcde",
      "severity": "warning"
    },
    {
      "source_type": "runbook",
      "title": "crashloopbackoff / 推荐排查步骤",
      "content": "查看 lastState.terminated.reason、exitCode、finishedAt，并查看 previous logs。",
      "severity": "info"
    }
  ],
  "impact_analysis": "该 Pod 可能无法稳定提供服务；如果 Deployment 其他副本不足，可能造成业务不可用。",
  "suggested_actions": [
    "查看 previous logs 中的启动错误栈和最近发布变更。",
    "确认启动命令、配置文件、环境变量、依赖服务地址和端口是否正确。"
  ],
  "remediation_actions": [
    {
      "action_type": "view_previous_logs",
      "description": "Review previous logs for container api to inspect the failure window before restart.",
      "command_preview": "kubectl logs -n default api-7c9c9d6b5d-abcde -c api --previous --timestamps --tail=200",
      "risk_level": "low",
      "need_human_confirm": false,
      "executable": false
    },
    {
      "action_type": "check_configmap_secret",
      "description": "Check referenced ConfigMaps, Secrets, envFrom entries, and mounted files for missing keys or invalid values.",
      "command_preview": "kubectl describe pod -n default api-7c9c9d6b5d-abcde; kubectl get configmap,secret -n default --show-labels",
      "risk_level": "low",
      "need_human_confirm": false,
      "executable": false
    }
  ],
  "risk_level": "medium",
  "need_human_confirm": true,
  "generated_at": "2026-05-29T10:00:00+08:00"
}
```

## 项目结构

```text
cmd/server              程序入口
configs                 配置文件
internal/api            Gin 路由、统一响应、HTTP handler
internal/service        诊断任务编排
internal/k8s            Kubernetes 采集器
internal/diagnostic     诊断引擎与 analyzer
internal/repository     GORM 数据访问
internal/prometheus     Prometheus HTTP API 查询客户端
internal/rag            Runbook 关键词检索 MVP
internal/tool           未来 Agent tool 抽象
runbooks                四类故障 runbook
deployments             Docker Compose 和 Kubernetes 部署模板
scripts                 初始化脚本
```

## 扩展建议

- Prometheus：已支持 `query_range` 指标查询，可继续扩展 CPU、重启次数、网络和磁盘指标证据。
- Loki：将 `internal/tool/loki_tool.go` 替换为真实 Loki client，保留 Kubernetes Pod logs 作为 fallback。
- Runbook RAG：将 `rag.Retriever` 接口接入 Qdrant 或 pgvector。
- Alertmanager：扩展 label 映射，支持从 owner references 反查工作负载。
