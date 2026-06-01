# NiceAgent CI/CD 与阿里云发布

本文记录当前 CI/CD 方案。第一版目标是把 NiceAgent 从“本地可运行”推进到“可自动检查、可构建镜像、可推送到阿里云 ACR”的状态；ACK 部署暂时保留为手动入口，避免早期阶段持续产生集群费用。

## 选型

- CI：GitHub Actions。
- 镜像仓库：阿里云 Container Registry，简称 ACR。
- 云端运行：阿里云 ACK Kubernetes 集群，后续再启用。
- 本地云化验证：kind 或 k3d，在本机 Docker 中运行 Kubernetes。
- 部署方式：先构建并推送三服务镜像，再用 `kubectl apply` 发布 `deployments/k8s` 模板。

阿里云官方文档中，ACR 支持通过 `docker login <registry>` 登录并推送镜像；ACK 支持通过 kubeconfig 使用 `kubectl` 连接集群。当前流水线就是围绕这两个入口设计的。

## 工作流

`.github/workflows/ci.yml`

- 触发：`pull_request`、推送到 `main` 或 `master`。
- 内容：
  - 安装 Go 和 Node.js。
  - `npm ci`。
  - `make check-js`。
  - `make test`。
  - `make compose-config`。
  - 构建三服务 Docker 镜像，但不推送。

`.github/workflows/cd-aliyun.yml`

- 触发：
  - 推送到 `main`：自动构建并推送镜像到 ACR，不部署 ACK。
  - 手动 `workflow_dispatch`：可指定 environment、image tag；只有显式选择 `deploy_to_ack=true` 且配置了 ACK kubeconfig 时才部署 ACK。
- 内容：
  - 登录 ACR。
  - 构建并推送：
    - `niceagent-control-plane`
    - `niceagent-agent-runtime`
    - `niceagent-sandbox-executor`
  - 默认到推送镜像为止。
  - 如果手动部署开关为 true 且配置了 ACK kubeconfig，则自动创建/更新 ACK 镜像拉取凭据，并发布到 ACK。

## GitHub Secrets

需要在 GitHub 仓库或对应 Environment 中配置：

```text
ALIYUN_ACR_REGISTRY=registry.cn-hangzhou.aliyuncs.com
ALIYUN_ACR_NAMESPACE=your-namespace
ALIYUN_ACR_USERNAME=your-acr-username
ALIYUN_ACR_PASSWORD=your-acr-password
ALIYUN_ACK_KUBE_CONFIG=base64-encoded-kubeconfig
INTERNAL_API_TOKEN=required-random-internal-api-token-for-ack
```

`ALIYUN_ACK_KUBE_CONFIG` 生成方式示例：

```bash
base64 -w 0 ~/.kube/config
```

macOS 可以使用：

```bash
base64 -i ~/.kube/config | tr -d '\n'
```

如果暂时只想发布镜像，不想部署到 ACK，可以不配置 `ALIYUN_ACK_KUBE_CONFIG`。推送到 `main` 时，CD 会自动把部署开关视为 false，只构建并推送 ACR 镜像。

手动运行 `CD Aliyun` 时：

- `environment`：通常保持 `staging`。
- `image_tag`：留空时使用当前 commit SHA。
- `deploy_to_ack`：默认是 `false`；只有确认要发布到 ACK 时才改为 `true`。

工作流会先推送三服务镜像。只有启用 ACK 部署时才执行 `kubectl apply`。ACK/K8s manifest 默认设置 `INTERNAL_API_TOKEN_REQUIRED=true`，因此部署到 ACK 前必须配置 `INTERNAL_API_TOKEN`，工作流会同步创建 `niceagent-internal-api` Secret；不要把该 token 写入代码仓库。

## 本地 Kubernetes 验证

如果想跑得更接近云部署，但暂时不创建 ACK，可以在 Mac 上使用 kind。kind 会在本机 Docker 里启动 Kubernetes 节点，不会产生云费用。

完整操作手册见 [LOCAL_K8S.md](LOCAL_K8S.md)。

准备工具：

```bash
brew install kind kubectl
```

创建本地集群：

```bash
make kind-create
```

构建三服务镜像、加载到 kind、应用 Kubernetes 模板：

```bash
make kind-deploy
```

查看 Pod 和 Service：

```bash
make k8s-status
```

访问 Control Plane：

```bash
make k8s-port-forward
```

然后打开 `http://127.0.0.1:8080`。

删除本地集群：

```bash
make kind-delete
```

本地 K8s 运行的是同一套 `deployments/k8s` 模板，差异主要是：

- kind 不会自动提供阿里云 SLB，访问时使用 `kubectl port-forward`。
- 本地镜像通过 `kind load docker-image` 注入集群，不需要从 ACR 拉取。
- `LoadBalancer` Service 在 kind 中通常不会拿到公网 IP，这是正常现象。

## 本地镜像构建

```bash
make docker-build
```

指定本地 tag：

```bash
make docker-build IMAGE_REGISTRY=niceagent IMAGE_TAG=dev
```

## ACK 部署模板

当前 Kubernetes 模板位于 `deployments/k8s`：

```text
namespace.yaml
configmap.yaml
sandbox-hardening.yaml
control-plane.yaml
agent-runtime.yaml
sandbox-executor.yaml
redis.yaml
secrets.example.yaml
```

`sandbox-hardening.yaml` 包含基础 `LimitRange`、`ResourceQuota` 和只允许 Agent Runtime 访问 Sandbox Executor 的 `NetworkPolicy`。它是最小 K8s 加固边界，不等同于强隔离 sandbox；更强隔离仍需要 RuntimeClass、独立节点池或 microVM worker。

`control-plane` 的 Service 第一版使用 `LoadBalancer`，在 ACK 上可能创建云负载均衡资源。正式环境建议后续改为 Ingress、HTTPS 证书和域名接入。

内部 API token 不提交真实值。需要启用时：

```bash
cp deployments/k8s/secrets.example.yaml /tmp/niceagent-internal-api.yaml
# 修改 token 后执行
kubectl apply -f /tmp/niceagent-internal-api.yaml
```

如果 ACR 仓库是私有仓库，当前 CD 工作流会自动在 ACK 中创建镜像拉取凭据，并绑定到 `niceagent` namespace 的默认 ServiceAccount。等价手动命令如下，主要用于本地排查：

```bash
kubectl -n niceagent create secret docker-registry aliyun-acr \
  --docker-server=registry.cn-hangzhou.aliyuncs.com \
  --docker-username="$ALIYUN_ACR_USERNAME" \
  --docker-password="$ALIYUN_ACR_PASSWORD"

kubectl -n niceagent patch serviceaccount default \
  -p '{"imagePullSecrets":[{"name":"aliyun-acr"}]}'
```

## 当前边界

- Control Plane 仍默认 memory store，不适合多副本生产运行。
- `redis.yaml` 是集群内临时 Redis，不是生产级托管 Redis。
- 当前没有接入 RDS/Postgres 持久化，重启会丢状态。
- Sandbox Executor 仍是 local executor 风格，不是强隔离生产沙箱。
- 推送到 `main` 的 CD 只发布镜像，不自动部署 ACK，避免在云资源和安全边界未稳定前自动上线。

## 下一步

- 接入 Postgres/RDS 持久化后，再允许 Control Plane 多副本。
- 将 Redis 替换为阿里云托管 Redis 或 ACK 内 StatefulSet。
- 增加生产 Ingress、TLS、域名和访问控制。
- 增加镜像漏洞扫描和 SBOM。
- 将 sandbox executor 切换到真正的容器沙箱执行路径。
