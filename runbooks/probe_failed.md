# Probe Failed Runbook

## 故障现象

Events 出现 `Unhealthy`，message 包含 `Readiness probe failed` 或 `Liveness probe failed`。

## 常见原因

- probe path、port、scheme 配置错误。
- 应用启动慢，但 initialDelaySeconds 太短。
- 健康检查接口依赖下游服务，偶发超时。
- timeoutSeconds 或 failureThreshold 太低。

## 推荐排查步骤

1. 查看 readinessProbe、livenessProbe、startupProbe 配置。
2. 查看 Unhealthy Event message。
3. 查看应用日志中 health、ready、live、timeout、connection refused 相关错误。
4. 对比 Service endpoints 数量和 Deployment 其他 Pod 状态。

## 处理建议

- Readiness 失败不一定重启，只表示暂时不接流量。
- Liveness 失败会导致 kubelet 重启容器。
- 慢启动服务建议配置 startupProbe 或提高 initialDelaySeconds。
- 偶发慢请求可评估提高 timeoutSeconds、periodSeconds 或 failureThreshold。

## 风险提示

盲目放宽 liveness 可能掩盖真实故障；盲目收紧 readiness 可能造成流量抖动。风险等级通常为 medium，需要人工确认。
