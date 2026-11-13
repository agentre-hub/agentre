package server_svc

import (
	"context"
	"fmt"
	"net/http"
)

// 执行端那一份的去向（POST /v1/saved-sessions/delete 应答的 machine_status）。应答返回
// 时 server 那一份一定已经没了，这两个值只说执行端。
const (
	// MachineStatusDeleted 那台机器在线，它那一份也已经删了。
	MachineStatusDeleted = "deleted"
	// MachineStatusPending 联系不上那台机器，server 已记下待办，它下次上线时补删。
	MachineStatusPending = "pending"
)

type deleteSavedSessionReq struct {
	ConversationID string `json:"conversation_id"`
	// MachineFingerprint 本机执行的对话没有执行端，省掉字段而不是发空串（server 要求
	// 非空时至少 8 位）。
	MachineFingerprint string `json:"machine_fingerprint,omitempty"`
}

type deleteSavedSessionResp struct {
	MachineStatus string `json:"machine_status"`
}

// DeleteSavedSession 请 server 删掉账号里的这条对话，并让执行端也删（server 侧幂等）。
// machineFingerprint 是执行这条对话的机器（本机执行为空）：账号里没有它的索引行时，
// server 靠它找执行端。
//
// 未登录时不发请求，交回 ErrNotLoggedIn；server 够不着交回 ErrServerUnreachable；
// server 拒绝（4xx / 非零业务码）同样以错误交回 —— 都表示 server 那一份没删成。
func (s *service) DeleteSavedSession(ctx context.Context, conversationID, machineFingerprint string) (string, error) {
	if err := s.requireLogin(ctx); err != nil {
		return "", err
	}
	req := deleteSavedSessionReq{ConversationID: conversationID, MachineFingerprint: machineFingerprint}
	var status string
	err := s.withAuth(ctx, func(ctx context.Context) error {
		var env envelope[deleteSavedSessionResp]
		_, doErr := s.getClient().do(ctx, http.MethodPost, "/v1/saved-sessions/delete", req, &env)
		if doErr != nil {
			return doErr
		}
		if env.Code != 0 {
			return fmt.Errorf("server: delete saved session rejected with code %d", env.Code)
		}
		status = env.Data.MachineStatus
		return nil
	})
	if err != nil {
		return "", err
	}
	return status, nil
}
