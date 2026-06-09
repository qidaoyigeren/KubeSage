---
fault_type: ImagePullBackOff
checks:
  - container.status.waiting.reason
  - pod events for Failed/ErrImagePull/ImagePullBackOff
  - container.image (repository, tag, registry)
  - pod.spec.imagePullSecrets
  - namespace imagePullSecrets
  - imagePullPolicy
  - registry network reachability
recommended_tools:
  - kubectl_describe
  - kubectl_get_events
  - kubectl_get_secrets
remediation_candidates:
  - fix image name or tag
  - create/update imagePullSecret
  - fix registry network access
  - fix registry TLS certificate
  - correct imagePullPolicy
risk_policy:
  update_image: medium
  modify_imagePullSecret: medium
stop_conditions:
  - registry is down (external dependency)
  - image tag deleted from registry
---

# ImagePullBackOff Runbook

## 故障现象

`kubectl get pod` 显示 `ImagePullBackOff` 或 `ErrImagePull`，容器状态 `waiting.reason=ImagePullBackOff`。
kubelet 无法从镜像仓库拉取容器镜像，按指数退避重试。

## 常见原因

### 按错误类型分类

| 错误类型 | Events 关键词 | 含义 |
|----------|---------------|------|
| 镜像不存在 | `manifest unknown` / `not found` | 镜像仓库中不存在该镜像或标签 |
| 认证失败 | `unauthorized` / `authentication` | imagePullSecret 无效或缺失 |
| 权限不足 | `pull access denied` | 凭据有效但无权拉取该镜像 |
| 证书错误 | `x509` / `certificate` | TLS 证书不受信任或已过期 |
| 网络不可达 | `timeout` / `dial tcp` | 无法连接到镜像仓库 |
| 镜像格式错误 | `invalid reference` | 镜像名称格式不正确 |

### 按常见场景分类

| 场景 | 典型原因 |
|------|----------|
| 新部署失败 | 镜像 tag 尚未推送到仓库，或 tag 名称拼写错误 |
| 已有部署突然失败 | imagePullSecret 过期、被删除，或 registry 凭据轮转 |
| 私有仓库部署 | 未创建 imagePullSecret，或 Secret 未挂载到 Pod |
| 自签名 registry | 节点不信任 registry 的 TLS 证书 |
| 镜像 tag 被覆盖 | 使用 `latest` tag 但仓库中该 tag 已被移除 |
| Init 容器镜像拉取失败 | Init 容器的镜像配置同样会触发 ImagePullBackOff |

## 推荐排查步骤

1. **查看 Events 详情**：`kubectl describe pod <pod>` 获取具体的拉取失败错误信息。
2. **确认镜像名称和标签**：检查 `container.image` 字段，确认 registry 地址、镜像名、tag 是否正确。
3. **验证镜像存在**：使用 `docker pull` 或 `crictl pull` 在节点上手动拉取镜像验证。
4. **检查 imagePullSecrets**：
   - `kubectl get pod <pod> -o jsonpath='{.spec.imagePullSecrets}'`
   - `kubectl get secret <secret> -n <namespace> -o yaml`
   - 确认 Secret 的 `type=kubernetes.io/dockerconfigjson` 且数据有效。
5. **检查 Secret 是否过期**：对比 Secret 创建时间和 registry token 有效期。
6. **检查 imagePullPolicy**：确认是否为 `Always`（每次拉取）、`IfNotPresent`（本地没有才拉取）或 `Never`。
7. **检查网络连通性**：从节点上 `curl` 或 `telnet` 镜像仓库地址，确认端口可达。
8. **检查 TLS 证书**：如果使用自签名证书，确认节点信任该 CA。

## 处理建议

### 按场景处理

| 场景 | 处理方案 |
|------|----------|
| 镜像/tag 不存在 | 修正镜像名称或确认 CI/CD 是否已推送镜像 |
| imagePullSecret 缺失 | 创建 Secret 并添加到 Pod spec：`kubectl create secret docker-registry` |
| imagePullSecret 过期 | 更新 Secret 中的凭据（用户名/密码/token） |
| 凭据无权限 | 在镜像仓库中为该账号授予 pull 权限 |
| 自签名证书不信任 | 将 CA 证书添加到节点信任链，或配置 `--insecure-registry` |
| 网络不通 | 检查节点到 registry 的网络策略、防火墙、DNS 解析 |
| imagePullPolicy=Always 但 tag 不稳定 | 改为 `IfNotPresent` 或使用稳定的 tag（非 `latest`） |

### 创建 imagePullSecret

```bash
# 创建 Docker Hub 凭据
kubectl create secret docker-registry my-registry-secret \
  --docker-server=<registry-server> \
  --docker-username=<username> \
  --docker-password=<password> \
  --docker-email=<email> \
  -n <namespace>

# 在 Pod spec 中引用
# spec.imagePullSecrets:
#   - name: my-registry-secret
```

### 验证镜像是否可拉取

```bash
# 在集群节点上手动拉取
crictl pull <image>:<tag>
# 或
docker pull <image>:<tag>

# 检查 registry 连通性
curl -v https://<registry>/v2/
```

## 风险提示

- 修改镜像 tag 可能引入未经测试的版本，风险等级为 **medium**。
- 更新 imagePullSecret 时需确保新凭据有效，否则会加剧故障。
- 使用 `--insecure-registry` 会降低安全性，仅建议在内网测试环境使用。
- 生产环境操作需保留变更记录，建议先在预发环境验证。

## 常用命令

```bash
# 查看拉取失败详情
kubectl describe pod <pod> | grep -A10 "Events"

# 查看 Pod 的 imagePullSecrets
kubectl get pod <pod> -o jsonpath='{.spec.imagePullSecrets}'

# 查看 Secret 内容（base64 编码）
kubectl get secret <secret> -o jsonpath='{.data.\.dockerconfigjson}' | base64 -d

# 查看 Pod 的镜像配置
kubectl get pod <pod> -o jsonpath='{range .spec.containers[*]}{.name}{"\t"}{.image}{"\t"}{.imagePullPolicy}{"\n"}{end}'
```
