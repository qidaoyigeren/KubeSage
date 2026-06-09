---
fault_type: ProbeFailed
checks:
  - pod events for Unhealthy
  - readinessProbe configuration
  - livenessProbe configuration
  - startupProbe configuration
  - application health endpoint logs
  - service endpoints count
  - container restartCount
recommended_tools:
  - kubectl_describe
  - kubectl_logs
  - kubectl_get_endpoints
remediation_candidates:
  - fix probe path/port
  - increase initialDelaySeconds
  - increase timeoutSeconds
  - configure startupProbe
  - fix application health endpoint
risk_policy:
  modify_liveness: high
  modify_readiness: medium
  modify_startup: low
stop_conditions:
  - liveness probe timeout caused by upstream dependency
  - readiness probe failure during rolling update is expected
---

# Probe Failed Runbook

## 故障现象

Events 出现 `Unhealthy`，message 包含 `Readiness probe failed`、`Liveness probe failed` 或 `Startup probe failed`。
Liveness 失败会导致 kubelet 重启容器，Readiness 失败会导致 Pod 从 Service endpoints 中移除。

## 常见原因

### 按探针类型分类

| 探针类型 | 失败后果 | 常见原因 |
|----------|----------|----------|
| **Readiness** | 从 endpoints 移除，不接收流量 | 依赖不可用、启动未完成、偶发超时 |
| **Liveness** | kubelet 重启容器 | 死锁、启动卡住、端口未监听 |
| **Startup** | 容器被视为启动失败 | 启动耗时过长、初始化任务阻塞 |

### 按失败表现分类

| 失败表现 | Events 关键词 | 可能根因 |
|----------|---------------|----------|
| 404 Not Found | `404` | probe path 配置错误，健康检查端点不存在 |
| 503 Service Unavailable | `503` | 应用依赖不可用，健康检查主动返回失败 |
| Connection refused | `connection refused` | 端口未监听，应用未启动或启动失败 |
| Timeout | `timeout` | 健康检查接口响应慢，可能是依赖慢查询 |
| Connection reset | `connection reset` | 应用崩溃或正在重启 |

### 慢启动场景

- 应用初始化耗时长（如加载大量数据、预热缓存）。
- JVM 应用 GC 预热导致启动慢。
- 数据库 migration 在 init container 或启动时执行。
- 冷启动时需要下载模型或加载配置文件。

## 推荐排查步骤

1. **查看探针配置**：检查 `readinessProbe`、`livenessProbe`、`startupProbe` 的 path、port、scheme、initialDelaySeconds、timeoutSeconds、periodSeconds、failureThreshold。
2. **查看 Unhealthy 事件**：获取具体的失败原因和 HTTP 状态码。
3. **查看应用日志**：搜索 `health`、`ready`、`live`、`timeout`、`connection refused` 相关错误。
4. **检查 Service endpoints**：`kubectl get endpoints <service>` 确认可用 Pod 数量。
5. **手动测试健康检查**：`kubectl exec <pod> -- curl localhost:<port>/<path>` 验证端点是否正常。
6. **检查依赖服务**：确认下游依赖（数据库、缓存、外部 API）是否可用。
7. **对比其他 Pod**：确认是单个 Pod 问题还是全部 Pod 问题。

## 处理建议

### 探针配置优化

| 场景 | 优化建议 |
|------|----------|
| 慢启动服务 | 配置 `startupProbe` 或提高 `initialDelaySeconds` |
| 偶发慢请求 | 提高 `timeoutSeconds`（如 3s→5s）和 `failureThreshold`（如 3→5） |
| 依赖偶发不可用 | Readiness 接口对非核心依赖做降级，不返回 503 |
| 路径配置错误 | 修正 probe path，确认应用实际暴露的健康检查端口 |
| 端口配置错误 | 确认 container port 与 probe port 一致 |

### Liveness vs Readiness 设计原则

- **Liveness**：只检查进程自身是否存活（如主循环是否阻塞），不要依赖外部服务。
- **Readiness**：检查是否准备好接收流量，可以包含依赖检查。
- **Startup**：专用于慢启动场景，启动期间 Liveness 被禁用。

### 配置示例

```yaml
# 慢启动服务的推荐配置
startupProbe:
  httpGet:
    path: /healthz
    port: 8080
  initialDelaySeconds: 10
  periodSeconds: 5
  failureThreshold: 30    # 最多等待 150s
readinessProbe:
  httpGet:
    path: /ready
    port: 8080
  periodSeconds: 10
  failureThreshold: 3
livenessProbe:
  httpGet:
    path: /healthz
    port: 8080
  periodSeconds: 15
  failureThreshold: 3
```

## 风险提示

- 盲目放宽 Liveness 配置可能掩盖真实故障（如死锁），导致容器长时间无响应，风险等级为 **high**。
- 盲目收紧 Readiness 配置可能造成流量抖动，正常波动导致 Pod 频繁从 endpoints 移除。
- 修改探针配置需要评估对滚动更新的影响。
- 生产环境操作需保留变更记录，建议先在预发环境验证。

## 常用命令

```bash
# 查看探针配置
kubectl get pod <pod> -o jsonpath='{.spec.containers[0].livenessProbe}' | jq
kubectl get pod <pod> -o jsonpath='{.spec.containers[0].readinessProbe}' | jq

# 查看 Unhealthy 事件
kubectl get events --field-selector involvedObject.name=<pod> | grep Unhealthy

# 手动测试健康检查
kubectl exec <pod> -- curl -s localhost:8080/healthz

# 查看 Service endpoints
kubectl get endpoints <service>
```
