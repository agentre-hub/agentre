package claudecodehook

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"time"
)

// RunPostTool implements the PostToolUse hook event. It reads the claude hook
// payload from stdin, fetches the SteerInbox over HTTP, and writes the hook
// output to stdout. Errors are logged to stderr but never fail the hook —
// a hook failure could disrupt the agent loop.
//
// PostToolUse 比 PreToolUse 安全：当前 tool 已成事实，新指令只在 tool_result
// 旁边作为补充上下文出现，不会回头改写 / 跳过正在跑的 tool。
func RunPostTool(baseURL, token string, in io.Reader, out io.Writer) {
	payload, err := parsePayload(in)
	if err != nil {
		fmt.Fprintf(os.Stderr, "claudecodehook post-tool: %v\n", err)
		emitPostToolContext(out, "")
		return
	}
	if payload.SessionID == "" {
		emitPostToolContext(out, "")
		return
	}
	// Subagent (Task tool) 内层工具的 PostToolUse 也走这里; 此时 drain inbox
	// 会把用户排队的消息错误地注入 subagent 上下文,subagent 会按 directive 掉
	// 头响应 —— 与"subagent 是被主 agent 委派的子任务,不该接收主对话层指令"
	// 的语义冲突,同时 chat_svc 会在 subagent 中间错误切分 turn。
	// 主 agent 的 hook payload 不带 agent_id, 等下一次主 agent tool 收尾再 drain.
	//
	// 子代理的工具仍要报它跑完之后的目录(只参与认领,不移动主位置):请求带上
	// agent_id,gateway 据此只记位置、不 drain;应答里就算有消息也不注入。没有目录
	// 可报(老 CLI)时一次请求都不发。
	query := inboxQuery{SessionID: payload.SessionID, ToolUseID: payload.ToolUseID, Cwd: payload.Cwd, AgentID: payload.AgentID}
	if payload.AgentID != "" && (payload.Cwd == "" || payload.ToolUseID == "") {
		emitPostToolContext(out, "")
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	messages, err := fetchInbox(ctx, baseURL, token, query)
	if err != nil {
		fmt.Fprintf(os.Stderr, "claudecodehook post-tool: fetch inbox: %v\n", err)
		emitPostToolContext(out, "")
		return
	}
	if payload.AgentID != "" {
		emitPostToolContext(out, "")
		return
	}
	emitPostToolContext(out, formatSteerContext(messages))
}

// emitPostToolContext writes the PostToolUse hookSpecificOutput JSON. If
// additionalContext is empty the field is omitted (lets the model proceed
// without extra steering instructions). PostToolUse does NOT carry
// permissionDecision — the tool already ran.
func emitPostToolContext(out io.Writer, additionalContext string) {
	type hookOut struct {
		HookEventName     string `json:"hookEventName"`
		AdditionalContext string `json:"additionalContext,omitempty"`
	}
	payload := struct {
		HookSpecificOutput hookOut `json:"hookSpecificOutput"`
	}{
		HookSpecificOutput: hookOut{
			HookEventName:     "PostToolUse",
			AdditionalContext: additionalContext,
		},
	}
	_ = json.NewEncoder(out).Encode(payload)
}

// formatSteerContext renders queued steer messages as an XML-wrapped block.
// 用 <user-message> 包裹原文 + 一行简短指令前缀，让模型识别为「用户在工具
// 边界上插入的新指令」，按高优先级处理；保持 PostToolUse 安全性（不会改写
// 已经跑完的 tool），但措辞带 directive 让模型实际掉头，不被中性文案吃掉。
//
// 选 XML 而非 plain prefix 的原因：claude 系列模型对 `<tag>...</tag>` 结构
// 化输入特别敏感，能可靠区分「系统/外部投递的新指令」与「assistant 自己的
// 计划文本」，避免上下文混淆。
func formatSteerContext(messages []string) string {
	if len(messages) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("<user-message-while-working priority=\"high\">\n")
	b.WriteString("The user sent the following message while you were working. ")
	b.WriteString("Treat it as a new directive that supersedes the prior plan; ")
	b.WriteString("acknowledge it and adjust your next step before continuing.\n")
	for i, msg := range messages {
		b.WriteString("<message index=\"")
		b.WriteString(strconv.Itoa(i + 1))
		b.WriteString("\">\n")
		b.WriteString(msg)
		b.WriteString("\n</message>\n")
	}
	b.WriteString("</user-message-while-working>")
	return b.String()
}
