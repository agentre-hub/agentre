package piagent

import (
	"context"
	"encoding/json"
	"fmt"
)

// Model 是 pi 自带模型目录里的一条。Provider 是 pi 自己的供应商标识，
// ID 是模型 id：交给 pi 的值是 "provider/id"（set_model 拆成 {provider, modelId}）。
type Model struct {
	Provider string `json:"provider"`
	ID       string `json:"id"`
	Name     string `json:"name"`
}

// ModelCatalog 的生效模型只来自 get_state，不从列表顺序或名字猜。
type ModelCatalog struct {
	Models    []Model
	Effective Model
}

// modelQueryStateWire 是 get_state 应答里查询进程关心的那几项。
type modelQueryStateWire struct {
	Model *Model `json:"model"`
}

// QueryModels 起一个独立的短命 RPC 进程问 pi 自带模型列表与生效模型：只发
// get_available_models / get_state，不发送任何用户消息、不带 --model、不挂会话
// --session、不带思考档位与扩展 —— 它回答的是「CLI 默认会用什么」，不是某条会话。
func (c *Client) QueryModels(ctx context.Context) (ModelCatalog, error) {
	query := *c
	query.model = ""
	query.thinking = ""
	query.systemPrompt = ""
	query.session = ""
	query.noSession = false
	query.sessionDir = ""
	query.extensions = nil
	proc, err := query.startRPC(ctx)
	if err != nil {
		return ModelCatalog{}, err
	}
	// 查完即退：查询进程不属于任何会话，绝不进入会话池。
	defer func() { _ = proc.terminate(context.Background(), c.killGrace) }()

	response, err := callRPC(ctx, proc,
		map[string]any{"id": "cli-models", "type": "get_available_models"},
		"get_available_models", "cli-models")
	if err != nil {
		return ModelCatalog{}, fmt.Errorf("piagent: get_available_models: %w", err)
	}
	var listed struct {
		Models []Model `json:"models"`
	}
	if err := json.Unmarshal(response.Data, &listed); err != nil {
		return ModelCatalog{}, fmt.Errorf("piagent decode get_available_models data: %w", err)
	}
	if listed.Models == nil {
		return ModelCatalog{}, fmt.Errorf("piagent: get_available_models did not return a model catalog (CLI version may be unsupported)")
	}
	response, err = callRPC(ctx, proc,
		map[string]any{"id": "cli-state", "type": "get_state"},
		"get_state", "cli-state")
	if err != nil {
		return ModelCatalog{}, fmt.Errorf("piagent: get_state: %w", err)
	}
	var state modelQueryStateWire
	if err := json.Unmarshal(response.Data, &state); err != nil {
		return ModelCatalog{}, fmt.Errorf("piagent decode get_state data: %w", err)
	}
	catalog := ModelCatalog{Models: listed.Models}
	if state.Model != nil {
		catalog.Effective = *state.Model
	}
	return catalog, nil
}
