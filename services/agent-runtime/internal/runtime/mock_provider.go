package runtime

import (
	"context"
)

type MockProvider struct {
	Response string
}

func (p MockProvider) Stream(ctx context.Context, _ ModelRequest) (<-chan ModelChunk, error) {
	ch := make(chan ModelChunk, 8)
	response := p.Response
	if response == "" {
		response = "我已经接收到任务，并会以无状态 agent runtime 的方式处理。\n\n当前实现会把聊天状态保存在 Control Plane，把本次请求封装成可追踪的 Run，并通过事件流返回执行过程。"
	}
	go func() {
		defer close(ch)
		select {
		case <-ctx.Done():
			ch <- ModelChunk{Error: ctx.Err(), Done: true}
			return
		case ch <- ModelChunk{Text: response}:
		}
		ch <- ModelChunk{Done: true}
	}()
	return ch, nil
}
