package ctlcmd

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"strings"

	"github.com/agentre-hub/agentre/internal/pkg/ctlaccount"
	"github.com/agentre-hub/agentre/internal/pkg/ctlclient"
)

// agrctl acp 的账号模式（spec 决策 11）：acp 经标准输入输出被编辑器拉起，没有终端，所以
// 账号模式只认个人 token（chat + approve）。会话操作与 `agrctl send / tail / stop / answer`
// 同一套——本文件只是把 accountChat 包成 acp 要的形状，不另写一遍 relay 派发。

// 一条轮次事件的种类。
const (
	TurnText       = "text"
	TurnThinking   = "thinking"
	TurnToolCall   = "tool_call"
	TurnToolResult = "tool_result"
	// TurnPermission 是需要回答的审批（工具权限或审批卡）；用 AccountDriver.Answer 作答。
	TurnPermission = "permission"
	TurnDone       = "done"
	TurnError      = "error"
)

// TurnEvent 是一轮对话里 acp 关心的事件；其余帧不翻译。
type TurnEvent struct {
	Kind       string
	Delta      string
	Error      string
	ToolCallID string
	ToolName   string
	ToolInput  json.RawMessage
	ToolResult string
	IsError    bool
	// RequestID 是 TurnPermission 的作答句柄。
	RequestID string
}

// PromptRequest 是一次 prompt：Conversation 为空则按 Agent / AgentID / Project 新开一条会话。
type PromptRequest struct {
	Conversation string
	Text         string
	// Images 是 data URL。
	Images  []string
	Agent   string
	AgentID int64
	Project string
}

// AccountDriver 用账号执行者（个人 token）驱动会话。
type AccountDriver struct {
	chat *ctlaccount.Client
}

// ResolveACPExecutor 为 acp 选执行者：显式 --endpoint、Agentre 会话注入或桌面握手 → 桌面端点；
// 个人 token（AGENTRE_TOKEN）→ 账号驱动。本机只有登录态时拒绝并给出配置个人 token 的写法。
// 单独的 --token（不带 --endpoint）仍是桌面的控制 token，与此前一致。
func ResolveACPExecutor(endpoint, token string, lookupEnv func(string) (string, bool), stderr io.Writer) (ctlclient.Endpoint, *AccountDriver, error) {
	if endpoint == "" && token != "" {
		ep, err := ctlclient.Resolve("", token, lookupEnv)
		return ep, nil, err
	}
	s := &sys{stdin: strings.NewReader(""), stdout: io.Discard, stderr: stderr, lookupEnv: lookupEnv}
	x, err := selectExecutor(s, endpoint, token)
	if err != nil {
		return ctlclient.Endpoint{}, nil, err
	}
	if x.kind != execAccount {
		return x.ep, nil, nil
	}
	if !x.acct.PersonalToken() {
		return ctlclient.Endpoint{}, nil, errors.New(acpNeedsToken)
	}
	return ctlclient.Endpoint{}, &AccountDriver{chat: newAccountChat(s, x, []string{"acp"})}, nil
}

// acpNeedsToken 是账号模式没配个人 token 时 acp 的报错（含编辑器里的配置写法）。
const acpNeedsToken = `agrctl acp talks to your account with a personal token that has the chat and approve scopes.
Create one at your own terminal:  agrctl create token --name editor --scope chat,approve
then set it in the editor's agent server configuration, for example:
  {"command": "agrctl", "args": ["acp", "--agent", "<name>"], "env": {"AGENTRE_TOKEN": "<the token>"}}
(AGENTRE_SERVER_URL selects another server.)`

func (d *AccountDriver) Close() { d.chat.Close() }

// Prompt 发送 prompt 并流式交回这一轮的事件。返回会话号；events 在本轮收尾（最后一条是 done
// 或 error）或 ctx 取消时关闭。续聊时只交回这条 prompt 之后的帧。
func (d *AccountDriver) Prompt(ctx context.Context, req PromptRequest) (string, <-chan TurnEvent, error) {
	in := ctlaccount.SendInput{Text: req.Text, Session: req.Conversation, Agent: req.Agent, AgentID: req.AgentID, Project: req.Project}
	for _, u := range req.Images {
		in.Images = append(in.Images, sendImage{DataURL: u})
	}
	var from int64
	if req.Conversation != "" {
		seq, err := d.chat.LatestSeq(ctx, req.Conversation)
		if err != nil {
			return "", nil, err
		}
		from = seq
	}
	reply, err := d.chat.Send(ctx, in)
	if err != nil {
		return "", nil, err
	}
	id := reply.ConversationID
	events := make(chan TurnEvent, 64)
	tailCtx, cancel := context.WithCancel(ctx)
	w := &turnWriter{ctx: tailCtx, cancel: cancel, out: events}
	go func() {
		defer close(events)
		defer cancel()
		err := d.chat.Tail(tailCtx, w, id, ctlaccount.TailCursor(from), false)
		if err != nil && !w.finished {
			select {
			case events <- TurnEvent{Kind: TurnError, Error: err.Error()}:
			case <-ctx.Done():
			}
		}
	}()
	return id, events, nil
}

// Stop 停掉会话当前这一轮。
func (d *AccountDriver) Stop(ctx context.Context, id string) error {
	_, err := d.chat.Stop(ctx, id)
	return err
}

// Answer 批准或拒绝一条 TurnPermission。
func (d *AccountDriver) Answer(ctx context.Context, id, requestID string, allow bool) error {
	_, err := d.chat.Answer(ctx, ctlaccount.AnswerInput{Session: id, Item: requestID, Allow: allow, Deny: !allow})
	return err
}

// turnWriter 是 tail 的输出口：逐行解析成 TurnEvent；见到本轮收尾就取消 tail。
type turnWriter struct {
	ctx      context.Context
	cancel   context.CancelFunc
	out      chan<- TurnEvent
	buf      bytes.Buffer
	finished bool
}

func (w *turnWriter) Write(p []byte) (int, error) {
	w.buf.Write(p)
	for {
		raw, err := w.buf.ReadBytes('\n')
		if err != nil {
			w.buf.Write(raw) // 半行留到下次
			return len(p), nil
		}
		var line ctlaccount.TailLine
		if json.Unmarshal(raw, &line) != nil {
			continue
		}
		ev, ok := turnEventOf(line)
		if !ok || w.finished {
			continue
		}
		select {
		case w.out <- ev:
		case <-w.ctx.Done():
			return len(p), nil
		}
		if ev.Kind == TurnDone || ev.Kind == TurnError {
			w.finished = true
			w.cancel()
		}
	}
}

// turnEventOf 把 tail 的一行译成 acp 关心的事件。
func turnEventOf(line ctlaccount.TailLine) (TurnEvent, bool) {
	var d struct {
		Kind       string          `json:"kind"`
		Text       string          `json:"text"`
		ID         string          `json:"id"`
		Name       string          `json:"name"`
		Input      json.RawMessage `json:"input"`
		ToolCallID string          `json:"toolCallId"`
		Content    string          `json:"content"`
		IsError    bool            `json:"isError"`
		RequestID  string          `json:"requestId"`
		ToolName   string          `json:"toolName"`
		ToolInput  string          `json:"toolInput"`
	}
	_ = json.Unmarshal(line.Data, &d)
	switch line.Type {
	case "text":
		return TurnEvent{Kind: TurnText, Delta: d.Text}, true
	case "thinking":
		return TurnEvent{Kind: TurnThinking, Delta: d.Text}, true
	case "tool_call":
		return TurnEvent{Kind: TurnToolCall, ToolCallID: d.ID, ToolName: d.Name, ToolInput: d.Input}, true
	case "tool_result":
		return TurnEvent{Kind: TurnToolResult, ToolCallID: d.ToolCallID, ToolResult: d.Content, IsError: d.IsError}, true
	case "pending":
		switch d.Kind {
		case "tool_permission_request":
			var b64 string // 与 tool_call 不同，这里的 input 是 bytes：protojson 写成 base64 字符串
			_ = json.Unmarshal(d.Input, &b64)
			return TurnEvent{Kind: TurnPermission, RequestID: d.RequestID, ToolName: d.ToolName, ToolInput: decodeBytes(b64)}, true
		case "tool_approval_requested":
			return TurnEvent{Kind: TurnPermission, RequestID: d.RequestID, ToolName: d.ToolName, ToolInput: decodeBytes(d.ToolInput)}, true
		}
	case "turn_done":
		if line.Error != "" {
			return TurnEvent{Kind: TurnError, Error: line.Error}, true
		}
		return TurnEvent{Kind: TurnDone}, true
	}
	return TurnEvent{}, false
}

// decodeBytes 还原 protojson 里 bytes 字段（base64 字符串）承载的 JSON 原文；不是 base64 就当
// 空——审批只是少展示一点入参，不影响作答。
func decodeBytes(s string) json.RawMessage {
	if s == "" {
		return nil
	}
	b, err := base64.StdEncoding.DecodeString(s)
	if err != nil || !json.Valid(b) {
		return nil
	}
	return b
}
