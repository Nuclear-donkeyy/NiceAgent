package executor

import "niceagent/common/sandbox"

func NewLocalExecutor() *sandbox.Executor {
	return sandbox.NewExecutor()
}
