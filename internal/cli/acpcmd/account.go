package acpcmd

import (
	"context"
	"errors"
	"fmt"
	"strconv"

	acpsdk "github.com/coder/acp-go-sdk"

	"github.com/agentre-hub/agentre/internal/cli/ctlcmd"
)

// accountDriver 是账号执行者（个人 token，经 relay 直连承载会话的机器）要提供的会话操作；
// 实现是 ctlcmd.AccountDriver，与 `agrctl send / stop / answer` 同一套代码。
type accountDriver interface {
	Prompt(ctx context.Context, req ctlcmd.PromptRequest) (string, <-chan ctlcmd.TurnEvent, error)
	Stop(ctx context.Context, conversationID string) error
	Answer(ctx context.Context, conversationID, requestID string, allow bool) error
	Close()
}

// newAccountSession 登记一个 ACP 会话。账号模式下会话在第一条 prompt 派发时才在设备上
// 创建（与 agrctl send 一样），所以此刻还没有 conversation_id。
func (a *agentHandler) newAccountSession() acpsdk.NewSessionResponse {
	acpID := acpsdk.SessionId(fmt.Sprintf("agentre-account-%d", a.seq.Add(1)))
	a.mu.Lock()
	a.sessions[acpID] = ""
	a.mu.Unlock()
	return acpsdk.NewSessionResponse{SessionId: acpID}
}

// cancelAccount 停掉会话当前这一轮；还没派发过 prompt 的会话没有可停的。
func (a *agentHandler) cancelAccount(conversationID string) error {
	if conversationID == "" {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), cancelStopTimeout)
	defer cancel()
	if err := a.account.Stop(ctx, conversationID); err != nil {
		return fmt.Errorf("acp: stop session: %w", err)
	}
	return nil
}

// accountTurn 派发一条 prompt 并把这一轮的事件翻译成 Prompt 循环吃的 streamEvent。
func (a *agentHandler) accountTurn(ctx context.Context, session acpsdk.SessionId, text string, images []sendImage) (<-chan streamEvent, <-chan error, error) {
	conv, _ := a.sessionOf(session)
	req := ctlcmd.PromptRequest{Conversation: conv, Text: text, Agent: a.agent, AgentID: a.agentID}
	if a.project > 0 {
		req.Project = strconv.FormatInt(a.project, 10)
	}
	for _, img := range images {
		req.Images = append(req.Images, img.DataURL)
	}
	id, turn, err := a.account.Prompt(ctx, req)
	if err != nil {
		return nil, nil, err
	}
	if id == "" {
		return nil, nil, errors.New("the account returned an empty session id")
	}
	a.mu.Lock()
	a.sessions[session] = id
	a.mu.Unlock()
	events := make(chan streamEvent, 64)
	go func() {
		defer close(events)
		for ev := range turn {
			select {
			case events <- streamEventOf(ev):
			case <-ctx.Done():
				return
			}
		}
	}()
	noErr := make(chan error)
	close(noErr)
	return events, noErr, nil
}

func streamEventOf(ev ctlcmd.TurnEvent) streamEvent {
	switch ev.Kind {
	case ctlcmd.TurnText:
		return streamEvent{Kind: kindChunk, Delta: ev.Delta}
	case ctlcmd.TurnThinking:
		return streamEvent{Kind: kindThinking, Delta: ev.Delta}
	case ctlcmd.TurnToolCall:
		return streamEvent{Kind: kindToolUse, ToolCallID: ev.ToolCallID, ToolName: ev.ToolName, ToolInput: ev.ToolInput}
	case ctlcmd.TurnToolResult:
		return streamEvent{Kind: kindToolResult, ToolCallID: ev.ToolCallID, ToolResult: ev.ToolResult, IsError: ev.IsError}
	case ctlcmd.TurnPermission:
		return streamEvent{Kind: kindToolPermissionRequest, ToolPermission: &streamToolPermission{
			RequestID: ev.RequestID, ToolName: ev.ToolName, ToolInput: ev.ToolInput}}
	case ctlcmd.TurnDone:
		return streamEvent{Kind: kindDone}
	case ctlcmd.TurnError:
		return streamEvent{Kind: kindError, Error: ev.Error}
	}
	return streamEvent{}
}
