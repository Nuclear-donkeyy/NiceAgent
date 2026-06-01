# NiceAgent 本地 Kubernetes 部署测试

本文记录一次在 macOS + Docker Desktop + kind 上跑通 NiceAgent 的本地 Kubernetes 流程。目标是用本机环境尽量贴近 ACK 的部署形态，同时避免创建云集群带来的持续费用。

## 适用场景

- 想验证 `deployments/k8s` 里的 Kubernetes YAML。
- 想模拟云上三服务拆分部署：Control Plane、Agent Runtime、Sandbox Executor。
- 想在不创建 ACK 集群的情况下跑通端到端聊天和 `/cli` 工具链路。

当前本地 K8s 仍使用 memory store 和集群内临时 Redis，不代表生产级部署。

## 前置条件

需要本机已有：

- Docker Desktop，并且 Docker daemon 正常运行。
- `kubectl`。
- `kind`。

检查命令：

```bash
docker info
kubectl version --client
kind version
```

如果本机没有 Homebrew，可以把 `kind` 下载到仓库本地工具目录：

```bash
mkdir -p .local/bin
curl -sSLo .local/bin/kind https://kind.sigs.k8s.io/dl/v0.26.0/kind-darwin-arm64
chmod +x .local/bin/kind
PATH="$PWD/.local/bin:$PATH" kind version
```

Intel Mac 使用 `kind-darwin-amd64`。Linux 或其他平台按 kind 官方发布文件名替换。

## 创建本地集群

如果 `kind` 已经在 `PATH` 中：

```bash
make kind-create
```

如果使用仓库内的 `.local/bin/kind`：

```bash
PATH="$PWD/.local/bin:$PATH" make kind-create
```

创建完成后确认当前 context：

```bash
kubectl config current-context
```

期望输出：

```text
kind-niceagent
```

## 构建和部署

一条命令完成三件事：构建三服务镜像、加载镜像到 kind、应用 Kubernetes YAML。

```bash
PATH="$PWD/.local/bin:$PATH" make kind-deploy
```

等价流程：

```bash
make docker-build
PATH="$PWD/.local/bin:$PATH" make kind-load
make k8s-apply
```

`k8s-apply` 会应用 `deployments/k8s/sandbox-hardening.yaml`。这份模板提供默认 `LimitRange`、namespace `ResourceQuota` 和 Sandbox Executor 的基础 `NetworkPolicy`，用于本地提前发现资源配置或服务访问边界问题。

部署成功后查看状态：

```bash
kubectl -n niceagent get pods,svc
```

期望所有 Pod 都是 `Running`：

```text
niceagent-control-plane      1/1 Running
niceagent-agent-runtime      1/1 Running
niceagent-agent-runtime      1/1 Running
niceagent-sandbox-executor   1/1 Running
niceagent-redis              1/1 Running
```

`niceagent-control-plane` 的 Service 类型当前是 `LoadBalancer`。在 kind 中 `EXTERNAL-IP` 通常会一直是 `<pending>`，这是正常现象，本地访问使用端口转发。

## 本地访问

启动端口转发：

```bash
kubectl -n niceagent port-forward svc/niceagent-control-plane 8080:8080
```

保持这个命令运行，然后打开：

```text
http://127.0.0.1:8080
```

健康检查：

```bash
curl -sS http://127.0.0.1:8080/healthz
```

期望输出：

```json
{"status":"ok"}
```

## 端到端冒烟测试

创建会话：

```bash
curl -sS -X POST http://127.0.0.1:8080/api/chats
```

从返回 JSON 中取 `id`，例如：

```text
chat_20260530161647_68560826603e1bb8
```

发送一条 CLI 消息：

```bash
curl -sS -X POST \
  -H 'Content-Type: application/json' \
  -d '{"content":"/cli echo hello"}' \
  http://127.0.0.1:8080/api/chats/<chat_id>/messages
```

从返回 JSON 中取 `run.id`，再查看 run 状态：

```bash
curl -sS http://127.0.0.1:8080/api/runs/<run_id>
```

期望 `status` 为：

```json
"succeeded"
```

查看会话消息：

```bash
curl -sS http://127.0.0.1:8080/api/chats/<chat_id>
```

期望 assistant 消息里包含：

```text
CLI 执行结果：
hello
```

这说明 Control Plane -> Agent Runtime -> Sandbox Executor 的跨服务链路已经跑通。

## 常见问题

### Redis Pod ImagePullBackOff

如果看到：

```bash
kubectl -n niceagent get pods
```

里 `niceagent-redis` 是 `ImagePullBackOff`，先看事件：

```bash
kubectl -n niceagent describe pod -l app=niceagent-redis
```

如果错误类似：

```text
failed to resolve reference "docker.io/library/redis:7-alpine"
proxyconnect tcp: dial tcp 127.0.0.1:7897: connect: connection refused
```

说明 kind 节点容器里继承了不可用的代理配置，导致节点自己拉不到 Docker Hub 镜像。可以在宿主机先拉取 Redis，再把单平台镜像导入 kind 节点。

Apple Silicon：

```bash
docker pull redis:7-alpine
docker image save --platform linux/arm64 -o /private/tmp/redis-7-alpine-arm64.tar redis:7-alpine
docker cp /private/tmp/redis-7-alpine-arm64.tar niceagent-control-plane:/redis-7-alpine.tar
docker exec --privileged niceagent-control-plane \
  ctr --namespace=k8s.io images import /redis-7-alpine.tar
kubectl -n niceagent delete pod -l app=niceagent-redis
kubectl -n niceagent rollout status deployment/niceagent-redis --timeout=120s
```

Intel Mac 把 `linux/arm64` 改为 `linux/amd64`。

如果直接执行 `kind load docker-image redis:7-alpine --name niceagent` 报 `content digest ... not found`，也使用上面的 `docker image save --platform ...` 方式。

### kubectl 无法连接 kind API

如果普通命令出现：

```text
Unable to connect to the server: dial tcp 127.0.0.1:<port>: connect: operation not permitted
```

通常是当前运行环境对本机端口访问有限制。可以在普通终端里执行同样的 `kubectl` 命令，或在需要访问 Docker/kind/kubectl 的自动化环境中授予本机网络和 Docker socket 权限。

### 8080 端口被占用

检查占用：

```bash
lsof -iTCP:8080 -sTCP:LISTEN -nP
```

换一个本地端口：

```bash
kubectl -n niceagent port-forward svc/niceagent-control-plane 18080:8080
```

然后访问：

```text
http://127.0.0.1:18080
```

## 清理

停止端口转发：在运行 `kubectl port-forward` 的终端按 `Ctrl+C`。

删除本地 kind 集群：

```bash
PATH="$PWD/.local/bin:$PATH" make kind-delete
```

如果 `kind` 已在系统 `PATH` 中：

```bash
make kind-delete
```

删除后，本地 Kubernetes 里的 NiceAgent Pod、Service、临时 Redis 和 workspace 状态都会一起消失。
