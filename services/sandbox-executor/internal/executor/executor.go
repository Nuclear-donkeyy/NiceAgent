package executor

import (
	"context"

	"niceagent/common/protocol"
	"niceagent/common/sandbox"
	"niceagent/sandbox-executor/internal/config"
)

type Executor interface {
	Execute(ctx context.Context, request protocol.SandboxCommand) protocol.SandboxResult
}

func NewLocalExecutor() *sandbox.Executor {
	return sandbox.NewExecutor()
}

func NewConfiguredExecutor(cfg config.Config) Executor {
	local := configuredLocal(cfg)
	if cfg.ExecutorMode == "container" {
		container := sandbox.NewContainerExecutor(cfg.ContainerImage)
		container.Local = local
		container.DockerBinary = cfg.ContainerDockerBinary
		container.CPUs = cfg.ContainerCPUs
		container.Memory = cfg.ContainerMemory
		container.PidsLimit = cfg.ContainerPidsLimit
		container.ReadOnlyRootFS = cfg.ContainerReadOnlyRoot
		container.DropAllCaps = cfg.ContainerDropAllCaps
		container.NoNewPrivileges = cfg.ContainerNoNewPrivs
		container.Tmpfs = cfg.ContainerTmpfs
		container.FallbackToLocal = cfg.ContainerLocalFallback
		return container
	}
	return local
}

func configuredLocal(cfg config.Config) *sandbox.Executor {
	local := sandbox.NewExecutor()
	local.WorkspaceRoot = cfg.WorkspaceRoot
	local.MaxOutputBytes = cfg.MaxOutputBytes
	local.MaxArtifacts = cfg.MaxArtifacts
	local.MaxArtifactBytes = cfg.MaxArtifactBytes
	local.MaxArtifactTotal = cfg.MaxArtifactTotalBytes
	return local
}
