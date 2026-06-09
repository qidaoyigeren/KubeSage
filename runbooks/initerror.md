---
fault_type: InitError
checks:
  - pod.status.reason (starts with Init)
  - initContainer.status.state.waiting.reason
  - initContainer.status.state.terminated.exitCode
  - initContainer.status.lastState.terminated.exitCode
  - initContainer restartCount
  - pod events for Failed/BackOff/Error/Init
  - init container logs
recommended_tools:
  - kubectl_describe
  - kubectl_logs
  - kubectl_get_events
remediation_candidates:
  - fix init container command
  - fix init container dependencies
  - fix init container permissions
  - fix init container resource limits
risk_policy:
  modify_init_container: medium
  remove_init_container: high
stop_conditions:
  - init container failure caused by external dependency
  - init container uses image from private registry (redirect to ImagePullBackOff)
---

# InitError Runbook

## 故障现象

Pod 长时间处于 `Pending` 或 `Init:0/1` 状态，`kubectl get pod` 显示 `Init:Error` 或 `Init:CrashLoopBackOff`。
`pod.status.reason` 以 `Init` 开头，Init 容器在业务容器启动前失败，导致整个 Pod 无法继续启动。

## 常见原因

### 按退出码分类

| 退出码 | 含义 | 常见根因 |
|--------|------|----------|
| `exitCode=1` | 启动失败 | 命令执行失败、配置错误、依赖不可达 |
| `exitCode=2` | 参数错误 | 命令参数不正确或缺少必需参数 |
| `exitCode=126` | 权限不足 | 脚本文件没有执行权限 |
| `exitCode=127` | 命令不存在 | 指定的命令或脚本路径错误 |
| `exitCode=137` | 内存不足 | Init 容器 OOMKilled |
| `exitCode=143` | 优雅终止 | 被 SIGTERM 信号终止 |

### 按 Init 容器用途分类

| Init 容器用途 | 常见失败原因 |
|---------------|-------------|
| 数据库 Migration | 数据库连接失败、migration 脚本错误、锁等待超时 |
| 依赖等待 | 目标服务不可达、DNS 解析失败、网络策略阻断 |
| 配置初始化 | ConfigMap/Secret 不存在、文件写入权限不足 |
| 权限设置 | SecurityContext 限制、文件系统只读、SELinux 策略 |
| 资源下载 | 外部资源不可达、下载超时、磁盘空间不足 |
| 服务注册 | 注册中心不可达、认证失败 |

### 按日志关键词分类

| 日志关键词 | 可能根因 |
|-----------|----------|
| `connection refused` / `timeout` | 依赖服务不可达 |
| `permission denied` | 文件或目录权限不足 |
| `migration` / `schema` | 数据库 migration 失败 |
| `error` / `failed` | 通用启动错误 |
| `no such file or directory` | 文件或脚本路径错误 |
| `OOMKilled` / `killed` | 内存不足 |

## 推荐排查步骤

1. **查看 Init 容器状态**：`kubectl describe pod <pod>` 中的 `Init Containers` 部分，查看 waiting reason 和 exitCode。
2. **查看 Init 容器日志**：`kubectl logs <pod> -c <init-container>` 或 `--previous` 查看前一次日志。
3. **查看 Events**：关注 `Failed`、`BackOff`、`Error`、`Init` 相关事件。
4. **检查 Init 容器配置**：查看 `command`、`args`、`volumeMounts`、`env` 配置是否正确。
5. **验证依赖服务**：确认 Init 容器等待的依赖（数据库、DNS、Service）是否可用。
6. **检查资源限制**：确认 Init 容器的 CPU/Memory limits 是否足够。
7. **检查镜像**：确认 Init 容器的镜像是否存在且可拉取（如果是镜像问题，应转 ImagePullBackOff）。

## 处理建议

### 按场景处理

| 场景 | 处理方案 |
|------|----------|
| Migration 失败 | 检查数据库连接串、migration 脚本、锁状态 |
| 依赖等待超时 | 确认目标服务状态，检查网络策略和 DNS |
| 配置文件缺失 | 确认 ConfigMap/Secret 是否已创建，挂载路径是否正确 |
| 权限不足 | 调整 SecurityContext 或文件权限 |
| OOMKilled | 增加 Init 容器的 memory limit |
| 命令不存在 | 修正 command 路径，确认镜像中包含该命令 |

### Init 容器设计最佳实践

- Init 容器应设置合理的超时，避免无限等待。
- 依赖等待类 Init 容器应使用循环重试 + 指数退避。
- Migration 类 Init 容器应具备幂等性，支持重复执行。
- 为 Init 容器设置独立的 resource limits，避免影响业务容器。

### Init 容器日志查看

```bash
# 查看当前 Init 容器日志
kubectl logs <pod> -c <init-container-name>

# 查看前一次 Init 容器日志（如果已重启）
kubectl logs <pod> -c <init-container-name> --previous

# 查看所有 Init 容器状态
kubectl get pod <pod> -o jsonpath='{.status.initContainerStatuses[*].name}' | tr ' ' '\n'
```

## 风险提示

- 修改 Init 容器配置可能影响 Pod 启动流程，风险等级为 **medium**。
- 移除 Init 容器可能导致业务容器在依赖未就绪时启动，引发更多问题。
- 数据库 Migration 类 Init 容器的修改需要 DBA 审核。
- 生产环境操作需保留变更记录，建议先在预发环境验证。

## 常用命令

```bash
# 查看 Init 容器状态
kubectl describe pod <pod> | grep -A30 "Init Containers"

# 查看 Init 容器日志
kubectl logs <pod> -c <init-container> --tail=200

# 查看 Init 容器配置
kubectl get pod <pod> -o jsonpath='{.spec.initContainers}' | jq

# 查看 Pod 事件
kubectl get events --field-selector involvedObject.name=<pod> | grep -i init
```
