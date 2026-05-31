package modelprovider

import (
	"context"
)

type MockProvider struct {
	Response string
}

func (p MockProvider) Stream(ctx context.Context, _ Request) (<-chan Chunk, error) {
	ch := make(chan Chunk, 8)
	response := p.Response
	if response == "" {
		response = "我已经接收到任务，并会以远端 agent 的方式处理。\n\n当前实现会把会话状态保存在 Control Plane，由独立 Runtime 执行本次请求，并在回复中实时更新当前状态。"
	}
	go func() {
		defer close(ch)
		select {
		case <-ctx.Done():
			ch <- Chunk{Error: ctx.Err(), Done: true}
			return
		case ch <- Chunk{Text: response}:
		}
		ch <- Chunk{Done: true}
	}()
	return ch, nil
}
