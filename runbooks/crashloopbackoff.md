# CrashLoopBackOff Runbook

## 故障现象

Pod 反复启动失败，`kubectl get pod` 显示 `CrashLoopBackOff`，容器 `restartCount` 持续增加。

## 常见原因

- 应用启动参数、配置文件或环境变量错误。
- 依赖服务不可达，例如数据库、缓存、消息队列连接失败。
- 镜像内启动命令或入口脚本异常。
- 端口冲突、权限不足、文件缺失。
- 退出码 137 时通常与 OOMKilled 相关。

## 推荐排查步骤

1. 查看 `lastState.terminated.reason`、`exitCode`、`finishedAt`。
2. 查看 `kubectl logs <pod> -c <container> --previous --tail=200`。
3. 查看 Events 中的 `BackOff`、`Failed`、`Error`。
4. 核对最近发布变更、ConfigMap、Secret、环境变量和启动命令。

## 处理建议

- `exitCode=1`：优先检查应用启动失败、配置加载失败和依赖连接失败。
- `exitCode=137`：转入 OOMKilled 排查。
- 日志包含 `missing`、`refused`、`timeout` 时，优先核对配置和下游依赖。

## 风险提示

当前 MVP 只做诊断，不做自动修复。重启、回滚或修改配置都需要人工确认；风险等级通常为 medium，生产环境操作需保留变更记录。
