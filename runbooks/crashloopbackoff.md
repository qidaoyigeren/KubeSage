---
fault_type: CrashLoopBackOff
checks:
  - container.status.waiting.reason
  - container.restartCount
  - lastState.terminated.exitCode
  - lastState.terminated.reason
  - previous container logs
  - pod events for BackOff/Failed/Error
  - configmap/secret recent changes
recommended_tools:
  - kubectl_logs
  - kubectl_describe
  - kubectl_get_events
remediation_candidates:
  - fix application config
  - fix dependency connection
  - rollback deployment
  - increase resource limits
risk_policy:
  rollback: medium
  restart_pod: low
  modify_config: medium
stop_conditions:
  - exitCode=137 (redirect to OOMKilled)
  - application crash confirmed as upstream dependency issue
---

# CrashLoopBackOff Runbook

## 故障现象

Pod 反复启动失败，`kubectl get pod` 显示 `CrashLoopBackOff`，容器 `restartCount` 持续增加。
kubelet 按指数退避策略重启容器，间隔从 10s、20s、40s 逐步增大至 5min。

## 常见原因

### 按退出码分类

| 退出码 | 含义 | 常见根因 |
|--------|------|----------|
| `exitCode=1` | 应用启动失败 | 配置加载失败、依赖连接失败、未捕获异常 |
| `exitCode=2` | panic 或 fatal 错误 | Go panic、fatal log、参数解析失败 |
| `exitCode=126` | 文件权限问题 | 命令不可执行、文件权限不足 |
| `exitCode=137` | 内存不足（SIGKILL） | OOMKilled，应转入 OOMKilled Runbook |
| `exitCode=143` | 优雅终止（SIGTERM） | 健康检查失败后被 kubelet 杀死 |

### 按日志关键词分类

| 日志关键词 | 可能根因 |
|-----------|----------|
| `config error` / `missing` | ConfigMap 或 Secret 配置错误、环境变量缺失 |
| `connection refused` / `timeout` | 依赖服务不可达（数据库、Redis、MQ） |
| `permission denied` / `EACCES` | 文件权限不足、ServiceAccount 权限不足 |
| `panic` / `fatal` | 代码 bug、空指针、数组越界 |
| `bind: address already in use` | 端口冲突，另一个进程占用目标端口 |
| `no such file or directory` | 挂载卷路径错误、配置文件缺失 |
| `certificate` / `x509` | TLS 证书问题、证书过期或不信任 |

## 推荐排查步骤

1. **查看容器退出信息**：`lastState.terminated.reason`、`exitCode`、`finishedAt`。
2. **查看前一次日志**：`kubectl logs <pod> -c <container> --previous --tail=200`。
3. **查看 Events**：关注 `BackOff`、`Failed`、`Error` 事件。
4. **核对最近变更**：检查最近 Deployment 发布、ConfigMap、Secret、环境变量和启动命令的变更记录。
5. **检查依赖服务**：确认数据库、缓存、消息队列等下游服务是否可达。
6. **验证镜像**：确认镜像是否能本地正常启动，入口命令是否正确。
7. **检查资源限制**：确认 CPU/Memory limits 是否足够应用启动。

## 处理建议

### 按场景处理

| 场景 | 处理方案 |
|------|----------|
| 配置错误 | 回滚 ConfigMap/Secret 到上一个正确版本 |
| 依赖不可达 | 确认依赖服务状态，检查网络策略和 Service 配置 |
| 端口冲突 | 修改应用端口或杀死占用端口的进程 |
| 权限不足 | 修复文件权限或补充 SecurityContext/ServiceAccount 配置 |
| 最近发布引入 | 回滚 Deployment 到上一个稳定版本 |
| exitCode=137 | 转入 OOMKilled 排查 |

### 回滚操作

```bash
# 查看 Deployment 历史
kubectl rollout history deployment/<name>

# 回滚到上一版本
kubectl rollout undo deployment/<name>

# 回滚到指定版本
kubectl rollout undo deployment/<name> --to-revision=<N>
```

## 风险提示

- 回滚操作会触发 Pod 重建，短暂影响服务可用性，风险等级通常为 **medium**。
- 修改配置需确认不会影响其他使用相同 ConfigMap/Secret 的服务。
- 生产环境操作需保留变更记录，建议先在预发环境验证。

## 常用命令

```bash
# 查看 Pod 状态和重启次数
kubectl get pod <pod> -o wide

# 查看前一次容器日志
kubectl logs <pod> -c <container> --previous --tail=200

# 查看 Pod 事件
kubectl describe pod <pod> | grep -A20 "Events"

# 查看最近 ConfigMap 变更
kubectl get configmap <name> -o yaml
```
