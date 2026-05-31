package sandbox

import (
	"strings"
	"testing"

	"niceagent/common/protocol"
)

func TestContainerExecutorBuildsHardenedDockerArgs(t *testing.T) {
	executor := NewContainerExecutor("busybox:1.36")
	executor.CPUs = "0.5"
	executor.Memory = "256m"
	executor.PidsLimit = 64

	args := executor.dockerArgs("/tmp/ws", protocol.SandboxCommand{
		Command: []string{"echo", "hello"},
		Network: false,
	})
	joined := strings.Join(args, " ")

	for _, want := range []string{
		"--cpus 0.5",
		"--memory 256m",
		"--pids-limit 64",
		"--read-only",
		"--cap-drop ALL",
		"--security-opt no-new-privileges",
		"--tmpfs /tmp:rw,noexec,nosuid,size=64m",
		"-v /tmp/ws:/workspace",
		"--network none",
		"busybox:1.36 echo hello",
	} {
		if !strings.Contains(joined, want) {
			t.Fatalf("docker args %q missing %q", joined, want)
		}
	}
}
