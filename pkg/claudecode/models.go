package claudecode

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
)

// Model 是 initialize 应答中的模型选项，别名与解析后的 ID 分开保留。
type Model struct {
	Value         string `json:"value"`
	DisplayName   string `json:"displayName"`
	ResolvedModel string `json:"resolvedModel"`
}

// ModelCatalog 的生效模型只来自 get_settings，不从 default 选项猜测。
type ModelCatalog struct {
	Models         []Model
	EffectiveModel string
}

// QueryModels 起独立的短命查询进程，不发用户消息，也不继承客户端的模型参数。
func (c *Client) QueryModels(ctx context.Context) (ModelCatalog, error) {
	query := *c
	query.model = ""
	query.sessionID = ""
	// 启动健康检查尚未返回 Session 时也已拥有进程，取消必须一并回收。
	var spawned *process
	query.spawner = func(ctx context.Context, spec processSpec) (*process, error) {
		p, err := c.spawn(ctx, spec)
		spawned = p
		return p, err
	}
	defer func() {
		if spawned != nil {
			spawned.kill()
		}
	}()
	sess, err := query.OpenSession(ctx)
	if err != nil {
		return ModelCatalog{}, err
	}
	// 查询不需要优雅等待后台任务，确保取消或协议不兼容时也不会留下进程。
	defer func() { _ = sess.Close(ctx) }()
	payload, err := sess.queryControl(ctx, "initialize")
	if err != nil {
		return ModelCatalog{}, err
	}
	var initialized struct {
		Models []Model `json:"models"`
	}
	if err := json.Unmarshal(payload, &initialized); err != nil {
		return ModelCatalog{}, fmt.Errorf("claudecode: initialize models: %w", err)
	}
	if initialized.Models == nil {
		return ModelCatalog{}, errors.New("claudecode: initialize did not return a model catalog (CLI version may be unsupported)")
	}
	payload, err = sess.queryControl(ctx, "get_settings")
	if err != nil {
		return ModelCatalog{}, err
	}
	var settings struct {
		Applied struct {
			Model string `json:"model"`
		} `json:"applied"`
	}
	if err := json.Unmarshal(payload, &settings); err != nil {
		return ModelCatalog{}, fmt.Errorf("claudecode: get_settings: %w", err)
	}
	return ModelCatalog{Models: initialized.Models, EffectiveModel: settings.Applied.Model}, nil
}

// queryControl 与常驻 reader 配对应答；不占用任何 user turn。
func (s *Session) queryControl(ctx context.Context, subtype string) (json.RawMessage, error) {
	return s.requestControl(ctx, map[string]any{"subtype": subtype})
}

// CurrentModel 查询常驻进程当前实际模型。
func (s *Session) CurrentModel(ctx context.Context) (string, error) {
	payload, err := s.requestControl(ctx, map[string]any{"subtype": "get_settings"})
	if err != nil {
		return "", err
	}
	var settings struct {
		Applied struct {
			Model string `json:"model"`
		} `json:"applied"`
	}
	if err := json.Unmarshal(payload, &settings); err != nil {
		return "", fmt.Errorf("claudecode: get_settings: %w", err)
	}
	return settings.Applied.Model, nil
}

// SetModel 切换常驻进程的模型；空串省略 model 字段，回到 CLI 默认。
func (s *Session) SetModel(ctx context.Context, value string) error {
	request := map[string]any{"subtype": "set_model"}
	if value != "" {
		request["model"] = value
	}
	_, err := s.requestControl(ctx, request)
	return err
}

// requestControl 统一查询与模型切换的应答配对，不占用用户轮。
func (s *Session) requestControl(ctx context.Context, request map[string]any) (json.RawMessage, error) {
	subtype, _ := request["subtype"].(string)
	reqID := newControlRequestID()
	ch := make(chan controlResponse, 1)
	s.stdinMu.Lock()
	if s.closed {
		s.stdinMu.Unlock()
		return nil, errors.New("claudecode: session closed")
	}
	s.ctrlMu.Lock()
	if s.ctrlPending == nil {
		s.ctrlPending = map[string]chan controlResponse{}
	}
	s.ctrlPending[reqID] = ch
	s.ctrlMu.Unlock()
	defer s.forgetControlRequest(reqID)
	err := json.NewEncoder(s.proc.stdin).Encode(map[string]any{"type": "control_request", "request_id": reqID, "request": request})
	s.stdinMu.Unlock()
	if err != nil {
		return nil, err
	}
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case resp := <-ch:
		if resp.Subtype != "success" {
			return nil, fmt.Errorf("claudecode: %s rejected (subtype=%q): %s", subtype, resp.Subtype, resp.Error)
		}
		return resp.Response, nil
	case <-s.readerDone:
		return nil, s.controlRequestAbortedErr(subtype)
	}
}
