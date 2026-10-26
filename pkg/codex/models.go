package codex

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
)

// Model 是 app-server model/list 应答中的一条模型选项。
// ID 是交给 CLI 的值（turn/start 的 model），Model 是 CLI 侧的模型标识。
type Model struct {
	ID                     string `json:"id"`
	Model                  string `json:"model"`
	DisplayName            string `json:"displayName"`
	IsDefault              bool   `json:"isDefault"`
	DefaultReasoningEffort string `json:"defaultReasoningEffort"`
	Hidden                 bool   `json:"hidden"`
}

// ModelCatalog 的生效模型只来自 config/read，不从 isDefault 猜测。
type ModelCatalog struct {
	Models         []Model
	EffectiveModel string
}

// QueryModels 起独立的短命查询进程问 CLI 自带模型列表与生效模型：
// 不发用户消息、不起 thread，也不带客户端的模型参数。
func (c *Client) QueryModels(ctx context.Context) (ModelCatalog, error) {
	query := *c
	query.model = ""
	app, err := query.startApp(ctx)
	if err != nil {
		return ModelCatalog{}, err
	}
	// 查完即退：查询进程不属于任何会话，绝不进入会话池。
	defer func() { _ = app.terminate(context.Background(), c.killGrace) }()
	if err := initializeApp(ctx, app); err != nil {
		return ModelCatalog{}, err
	}
	raw, err := app.Call(ctx, appMethodModelList, nil)
	if err != nil {
		return ModelCatalog{}, err
	}
	var listed struct {
		Data []Model `json:"data"`
	}
	if err := json.Unmarshal(raw, &listed); err != nil {
		return ModelCatalog{}, fmt.Errorf("codex: model/list: %w", err)
	}
	if listed.Data == nil {
		return ModelCatalog{}, errors.New("codex: model/list did not return a model catalog (CLI version may be unsupported)")
	}
	catalog := ModelCatalog{Models: make([]Model, 0, len(listed.Data))}
	for _, m := range listed.Data {
		if m.Hidden {
			continue
		}
		catalog.Models = append(catalog.Models, m)
	}
	raw, err = app.Call(ctx, appMethodConfigRead, nil)
	if err != nil {
		return ModelCatalog{}, err
	}
	var applied struct {
		Config struct {
			Model string `json:"model"`
		} `json:"config"`
	}
	if err := json.Unmarshal(raw, &applied); err != nil {
		return ModelCatalog{}, fmt.Errorf("codex: config/read: %w", err)
	}
	catalog.EffectiveModel = applied.Config.Model
	return catalog, nil
}
