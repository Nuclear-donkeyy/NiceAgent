# Sandbox 与 Artifact

## 产品功能

Sandbox 是系统级 CLI 和文件类 skill 的执行边界。用户不需要逐次授权 CLI，但 agent 的命令必须在受限环境中运行，并留下可审计结果。

目标能力：

- 每个 run 拥有独立 workspace。
- CLI 命令在容器或更强隔离环境中执行。
- stdout、stderr、exit code、duration、输出截断、策略拒绝都可审计。
- 生成文件登记为 artifact，前端可展示和下载，刷新后可恢复。
- `workspace.read` 能读取已登记 artifact 或 workspace 元数据。

## 成熟方案调研

近期可落地路径是 Docker/runc 容器执行。Docker 支持 CPU、内存等资源限制，也可以通过 `--network none`、只读 rootfs、capability drop、seccomp、`no-new-privileges` 等机制收缩风险面。参考：[Docker resource constraints](https://docs.docker.com/engine/containers/resource_constraints/)。

Kubernetes 生产层可用 Job/Pod 承载 sandbox task，并通过 requests/limits、ephemeral-storage、LimitRange、ResourceQuota 和 NetworkPolicy 控制资源与网络。参考：[Kubernetes ResourceQuota](https://kubernetes.io/docs/concepts/policy/resource-quotas/)、[Kubernetes NetworkPolicy](https://kubernetes.io/docs/reference/kubernetes-api/networking/network-policy-v1/)。

强隔离路径可以分层推进：

- gVisor：在容器形态下拦截 syscall，降低宿主内核暴露面，适合先增强隔离。参考：[gVisor docs](https://gvisor.dev/docs/)。
- Kata Containers：用轻量 VM 承载容器，隔离强于普通容器，但运维复杂度更高。参考：[Kata Containers](https://katacontainers.org/)。
- Firecracker：microVM 适合长期专用 sandbox fleet，但需要 KVM、rootfs、网络、快照和镜像管理。参考：[Firecracker](https://github.com/firecracker-microvm/firecracker)。

## 当前仓库现状

`packages/common/sandbox/executor.go` 已有 local executor，包含 allowlist/dangerous 策略、超时、workspace 目录创建、环境变量过滤和输出截断。allowlist 目前包括 `curl`、`wget`、`dig`、`nslookup`、`date`、`echo`、`pwd`、`ls`；危险命令会按系统 CLI 只读策略拒绝。

`packages/common/sandbox/container.go` 已有 Docker `ContainerExecutor`，支持 `--cpus`、`--memory`、`--pids-limit`、`--read-only`、`--cap-drop ALL`、`no-new-privileges`、`tmpfs`、workspace volume 和可选 `--network none`。`services/sandbox-executor` 支持 `EXECUTOR_MODE=local|container`，但 Compose 里默认仍是 `local`，container 还没有成为默认生产路径。K8s 已有第一版加固模板：`sandbox-hardening.yaml` 提供 `LimitRange`、`ResourceQuota` 和 Sandbox Executor ingress `NetworkPolicy`，`sandbox-executor.yaml` 已设置非 root、禁止提权、drop capabilities、`RuntimeDefault` seccomp 和只读 rootfs。RuntimeClass、独立节点池和更严格 egress policy 仍待后续补齐。

`services/sandbox-executor` 已独立成服务，并按配置装配 local 或 container executor，通过内部 HTTP API 暴露执行能力。Agent Runtime 如果配置了 `SANDBOX_EXECUTOR_URL` 会走 HTTP executor；否则回退到进程内 local executor。

协议层已有 `Workspace` 和 `Artifact` 类型，`RunCompleteRequest` 和 `RunResult` 支持 `Artifacts`。Postgres 迁移已包含 workspace metadata、`artifacts` 表和索引；memory store 与 Postgres store 都支持 workspace/artifact repository。创建 run 时会登记 workspace。

Sandbox 执行后会扫描 workspace `output/` 下的新增或修改文件，生成 artifact metadata 和 workspace diff。Agent Runtime 会从 tool observation 中提取 artifacts，并在 run complete 时交给 Control Plane 持久化；Control Plane 会写入 `artifact.created` event。前端已有 artifact domain/API、`ArtifactList` 展示和下载入口，刷新后可按 run 恢复 artifact metadata。

`workspace.read` 已能通过 Control Plane 内部 API 列出当前 run artifacts，返回 workspace/artifact 元数据摘要，并安全读取已登记文本 artifact 的内容摘要。摘要 action 不读取文件内容，只汇总 artifact 数量、总大小、MIME 分布、文本 artifact 数量、latest artifact 和 artifact 路径清单；读取路径复用 artifact metadata、workspace root、`output/` 限制、symlink escape 检查、MIME 文本限制和最大读取字节数。

已落地能力：

- Sandbox Executor 已独立成服务，支持 `EXECUTOR_MODE=local|container`，Runtime 可通过 `SANDBOX_EXECUTOR_URL` 调用。
- Local executor 已有只读系统 CLI allowlist、危险命令拒绝、超时、workspace 创建、环境过滤和输出截断。
- Docker `ContainerExecutor` 已具备 CPU、内存、PID、只读 rootfs、cap drop、no-new-privileges、tmpfs、workspace mount 和网络开关。
- `SANDBOX_CONTAINER_ALLOWED_IMAGES` 已提供逗号分隔镜像白名单，`EXECUTOR_MODE=container` 时会拒绝不在白名单内的 `SANDBOX_CONTAINER_IMAGE`；`/healthz` 会暴露当前 executor mode、container image 和白名单。
- workspace metadata、artifact 表/API/download、`artifact.created` event、前端 artifact 展示/下载和刷新恢复已落地。
- artifact 下载和 `workspace.read` 文本读取复用 user/project/run 权限、`output/` 限制、path clean、symlink escape 和 MIME/大小检查。
- K8s 已有基础 `NetworkPolicy`、`ResourceQuota`、`LimitRange` 和 Sandbox Executor `securityContext`。

仍待落地能力：

- Compose 和生产部署默认切到 container executor，并补 egress policy 和资源容量建议。
- artifact 增量可见性、PDF/音视频/表格等复杂预览、过期清理和外部对象存储归档。
- RuntimeClass、独立节点池、gVisor/Kata/Firecracker 等更强隔离 profile。

## 扩展点

- Sandbox Executor 增加 `EXECUTOR_MODE=local|container|k8s`，默认逐步切到 `container`。
- `SandboxResult` 扩展 artifact、workspace diff、resource usage、audit id、policy decision。
- Control Plane 增加 `artifacts` 表、artifact repository、list/download API。
- Runtime 在 tool 调用后根据 Sandbox result 写入 `artifact.created`。
- `workspace.read` 继续扩展更多 workspace 元数据和 artifact preview，但仍只能读取已登记 artifact 或经过 Control Plane 校验的只读资源。
- K8s 已有基础 NetworkPolicy、ResourceQuota、LimitRange 和 securityContext；容器镜像白名单已进入配置面，后续补 RuntimeClass、独立节点池、egress policy 和生产镜像清单。

## 技术架构

推荐链路：

```text
Agent Runtime 调用 cli.exec
  -> Sandbox Executor 创建/定位 run workspace
  -> 容器挂载 /workspace/input /workspace/output /workspace/tmp
  -> 执行命令并捕获 stdout/stderr/exit code/duration
  -> 扫描 output diff
  -> 生成 artifact metadata
  -> Control Plane 持久化 artifact
  -> Runtime 发送 tool.* 和 artifact.created
  -> 前端在聊天界面展示产物
```

Workspace 建议分区：

- `/workspace/input`：只读输入，放用户上传或系统准备材料。
- `/workspace/output`：可写输出，只从这里登记 artifact。
- `/workspace/tmp`：临时文件，run 结束后可清理。

Artifact 元数据建议包括：

- `id`
- `run_id`
- `chat_id`
- `user_id`
- `workspace_id`
- `path`
- `name`
- `mime_type`
- `size_bytes`
- `sha256`
- `storage_backend`
- `storage_key`
- `created_at`
- `deleted_at`

## 技术方案

当前最小闭环已经具备，不追求一步到位的微虚拟机；下一步重点是把默认执行路径和产物治理继续推向生产化：

- `artifacts` 表、repository、workspace 记录、artifact list/download API 和前端展示已落地。
- Sandbox Executor 已能扫描 output 目录并生成 artifact metadata；后续补增量可见性、过期清理和对象存储归档。
- artifact path clean、symlink、`..`、绝对路径检查和 `output/` 限制已落地，后续继续扩展更多 preview 类型。
- ContainerExecutor 已支持只读 rootfs、cap drop、no-new-privileges、pids limit、资源限制和环境变量白名单；后续把 Compose/生产默认执行路径切到 container profile。
- 网络策略保持“默认关闭，按 skill/runtime_config 显式允许”。

容器默认路径稳定后，再评估 gVisor/Kata/Firecracker。普通 SaaS 早期可以先用 Docker + K8s policy，强多租户或不可信代码执行再切 RuntimeClass 或专用 microVM worker。

## 分阶段落地

1. 持久化与只读闭环：`artifacts` 表、workspace 记录、artifact list/download API、`artifact.created` event、`workspace.read` artifact summary/list/text read。
2. 容器默认执行：Sandbox Executor 接入 ContainerExecutor，加资源、网络和 security flags。
3. Artifact 产品化：前端展示、下载、失败提示、过期状态、run replay 恢复。
4. K8s 加固：基础 NetworkPolicy、ResourceQuota、LimitRange 和 Sandbox Executor securityContext 已落地；容器镜像白名单已进入配置面，后续继续补 RuntimeClass、独立节点池、egress policy 和生产镜像清单。
5. 强隔离选型：gVisor/Kata 作为可选 profile，Firecracker 放长期专用执行池。

## 风险与验收

风险：

- 容器共享宿主内核，不等于强安全沙箱。
- SSRF 或内网探测绕过 CLI 只读策略。
- workspace path、symlink 或 artifact 下载越权。
- artifact 泄露 secret 或过大文件拖垮存储。
- 长期进程或资源耗尽影响宿主。

验收：

- `/cli echo hello` 成功。
- 破坏性命令被策略拒绝，不触发用户审批。
- 超时命令被终止，输出被稳定截断。
- 生成 artifact 后刷新页面仍可看到 metadata。
- `../`、绝对路径、symlink 不能越界。
- K8s 默认拒绝非白名单出口。
- artifact 下载按 user/project/run 权限校验。

## 参考资料

- [Docker resource constraints](https://docs.docker.com/engine/containers/resource_constraints/)
- [Kubernetes NetworkPolicy](https://kubernetes.io/docs/reference/kubernetes-api/networking/network-policy-v1/)
- [Kubernetes ResourceQuota](https://kubernetes.io/docs/concepts/policy/resource-quotas/)
- [gVisor docs](https://gvisor.dev/docs/)
- [Kata Containers](https://katacontainers.org/)
- [Firecracker](https://github.com/firecracker-microvm/firecracker)
