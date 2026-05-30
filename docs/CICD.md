# NiceAgent CI/CD 与阿里云发布

本文记录当前 CI/CD 方案。第一版目标是把 NiceAgent 从“本地可运行”推进到“可自动检查、可构建镜像、可手动发布到阿里云”的状态。

## 选型

- CI：GitHub Actions。
- 镜像仓库：阿里云 Container Registry，简称 ACR。
- 云端运行：阿里云 ACK Kubernetes 集群。
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
  - 推送到 `main`：自动构建、推送镜像，并默认执行 ACK 部署。
  - 手动 `workflow_dispatch`：可指定 environment、image tag，并可选择是否部署 ACK。
- 内容：
  - 登录 ACR。
  - 构建并推送：
    - `niceagent-control-plane`
    - `niceagent-agent-runtime`
    - `niceagent-sandbox-executor`
  - 如果部署开关为 true 且配置了 ACK kubeconfig，则自动创建/更新 ACK 镜像拉取凭据，并发布到 ACK。

## GitHub Secrets

需要在 GitHub 仓库或对应 Environment 中配置：

```text
ALIYUN_ACR_REGISTRY=registry.cn-hangzhou.aliyuncs.com
ALIYUN_ACR_NAMESPACE=your-namespace
ALIYUN_ACR_USERNAME=your-acr-username
ALIYUN_ACR_PASSWORD=your-acr-password
ALIYUN_ACK_KUBE_CONFIG=base64-encoded-kubeconfig
INTERNAL_API_TOKEN=optional-internal-api-token
```

`ALIYUN_ACK_KUBE_CONFIG` 生成方式示例：

```bash
base64 -w 0 ~/.kube/config
```

macOS 可以使用：

```bash
base64 -i ~/.kube/config | tr -d '\n'
```

如果暂时只想发布镜像，不想部署到 ACK，可以不配置 `ALIYUN_ACK_KUBE_CONFIG`，并在手动触发 CD 时保持 `deploy_to_ack=false`。

推送到 `main` 时，CD 会自动把部署开关视为 true，不需要在网页上手动选择 `deploy_to_ack`。

手动运行 `CD Aliyun` 时：

- `environment`：通常保持 `staging`。
- `image_tag`：留空时使用当前 commit SHA。
- `deploy_to_ack`：默认是 `true`，如只想推镜像不部署，可改为 `false`。

工作流会先推送三服务镜像，再执行 `kubectl apply`。如果配置了 `INTERNAL_API_TOKEN`，工作流会同步创建 `niceagent-internal-api` Secret；如果没有配置，则内部 API token 保持为空，适合早期验证链路。

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
control-plane.yaml
agent-runtime.yaml
sandbox-executor.yaml
redis.yaml
secrets.example.yaml
```

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
- CD 采用手动触发，避免在云资源和安全边界未稳定前自动上线。

## 下一步

- 接入 Postgres/RDS 持久化后，再允许 Control Plane 多副本。
- 将 Redis 替换为阿里云托管 Redis 或 ACK 内 StatefulSet。
- 增加生产 Ingress、TLS、域名和访问控制。
- 增加镜像漏洞扫描和 SBOM。
- 将 sandbox executor 切换到真正的容器沙箱执行路径。
