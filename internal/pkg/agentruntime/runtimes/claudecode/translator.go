package claudecode

import (
	"encoding/json"
	"fmt"

	"github.com/cago-frame/agents/provider"

	"github.com/agentre-hub/agentre/internal/pkg/agentruntime"
	"github.com/agentre-hub/agentre/internal/pkg/agentruntime/canonical"
	"github.com/agentre-hub/agentre/internal/pkg/llmcatalog"
	"github.com/agentre-hub/agentre/pkg/claudecode"
)

// translate 把单帧 claudecode.Event 翻译成 0/1/n 个 sealed agentruntime.Event。
//
// emit canonical 识别:全权委托给 canonical.FromToolUse(见 recognizeCanonical),
// 覆盖 Write → FileWrite、Edit/MultiEdit → FileEdit、TodoWrite → PlanUpdate、
// Task/Agent → AgentSpawn(只填 description/subagent_type/prompt 静态字段;
// 运行时累计态由 SubagentStarted/Progress/Done 经 SubagentStateBlock 维护,
// 前端 AgentSpawnCard 读 toolBlock.subagent overlay)。
// AskUserQuestion / ExitPlanMode 仍走 control_request 路径,此处保留
// isAskUserQuestionToolName 过滤,不进 recognizeCanonical。
//
// subagent 文本过滤:ParentToolUseID 非空的帧来自 Task/Agent 子会话。工具事件
// (ToolCall / ToolResult)有 ParentToolCallID 字段承载这层归属,由下游渲染成派遣卡
// 里的嵌套块;而 agentruntime.TextDelta / ThinkingDelta 结构上不承载 parent,一旦
// emit 就与主 agent 自己的文本无法区分,会被 handlers.TextDeltaHandler 累进主会话
// 正文并推上前端气泡(sess-2667:后台 subagent 在主轮结束后继续吐旁白,英文句子被
// 贴进已收尾的中文消息末尾,且因 subagentChildBlocks 只收嵌套工具块而不落库,
// 刷新即消失)。子代理旁白按产品决策丢弃,故在这唯一的生产点就不 emit —— remote
// 侧 daemon 内跑的是同一个 translator,一并修好,不需要额外的 wire 字段。
//
// usage / stopErr 与旧 translator 同步:EventDone 时填 usage;EventError 时填
// stopErr。
func translate(ev claudecode.Event) (events []agentruntime.Event, usage *provider.Usage, stopErr error) {
	switch ev.Kind {
	case claudecode.EventTextDelta:
		// 子代理内部帧的旁白丢弃,不进主会话正文 —— 见下方 subagent 文本过滤说明。
		if ev.ParentToolUseID == "" {
			events = append(events, agentruntime.TextDelta{Text: ev.Text})
		}
	case claudecode.EventThinkingDelta:
		if ev.ParentToolUseID == "" {
			events = append(events, agentruntime.ThinkingDelta{Text: ev.Text})
		}
	case claudecode.EventContentBlockStart:
		// 纯计时信号:pkg/claudecode 已经滤掉子代理内部帧(见 parseStreamEventFrame),
		// 这里无脑透传成 host-neutral 的 OutputActivity。
		events = append(events, agentruntime.OutputActivity{})
	case claudecode.EventPreToolUse:
		// AskUserQuestion 走独立的 control_request 路径 emit UserAskRequest,
		// PreToolUse 这条会被前端再渲染成通用 ToolInvocationCard 重复一遍。
		if ev.Tool != nil && !isAskUserQuestionToolName(ev.Tool.Name) {
			tc := agentruntime.ToolCall{
				ID:               ev.Tool.ID,
				Name:             ev.Tool.Name,
				Input:            ev.Tool.Input,
				ParentToolCallID: ev.ParentToolUseID,
			}
			tc.Canonical = recognizeCanonical(ev.Tool.Name, ev.Tool.Input)
			events = append(events, tc)
		}
	case claudecode.EventPostToolUse:
		// 同 PreToolUse:AskUserQuestion 的 tool_result 由 UserAskRequest/Resolved
		// 承载,这条 PostToolUse 不入流避免重复卡片。异步子代理的启动回执同理由
		// SubagentStarted/Done 承载,见 isAsyncLaunchReceipt。
		if ev.Tool != nil && !isAskUserQuestionToolName(ev.Tool.Name) &&
			!isAsyncLaunchReceipt(ev.Tool.ResultMeta) {
			isErr := ev.Tool.Err != nil
			events = append(events, agentruntime.ToolResult{
				ToolCallID:       ev.Tool.ID,
				Content:          string(ev.Tool.Response),
				IsError:          isErr,
				ParentToolCallID: ev.ParentToolUseID,
				Meta:             append([]byte(nil), ev.Tool.ResultMeta...),
			})
		}
	case claudecode.EventTaskStarted, claudecode.EventTaskProgress, claudecode.EventTaskNotification:
		if ev.Tool != nil && ev.Tool.Subagent != nil {
			info := subagentInfoFromMeta(ev.Tool.Subagent)
			switch ev.Kind {
			case claudecode.EventTaskStarted:
				events = append(events, agentruntime.SubagentStarted{ToolCallID: ev.Tool.ID, Info: info})
			case claudecode.EventTaskProgress:
				events = append(events, agentruntime.SubagentProgress{ToolCallID: ev.Tool.ID, Info: info})
			case claudecode.EventTaskNotification:
				events = append(events, agentruntime.SubagentDone{ToolCallID: ev.Tool.ID, Info: info})
			}
		}
	case claudecode.EventSubagentModel:
		// R2:pkg/claudecode 侧(parseAssistantContentWithUsage)是这条不变量
		// (ParentToolUseID != "" && Model != "")的唯一生产者,已经保证只在两者都
		// 非空时才产出这个 Kind(见 pkg/claudecode/session_test.go 的覆盖)。
		// 同进程内不再重复判空——wrap-up 复审第三轮 Finding 2 判定此处原有的
		// 防御式二次判空是与生产者重复的冗余守卫,已删除。
		events = append(events, agentruntime.SubagentModel{ToolCallID: ev.ParentToolUseID, Model: ev.Model})
	case claudecode.EventError:
		if ev.Err != nil {
			events = append(events, agentruntime.ErrorEvent{Err: ev.Err})
			stopErr = ev.Err
		}
	case claudecode.EventRetry:
		// system.api_retry 帧:把 CLI 的结构化字段渲染成两路同形的 Retry,前端
		// RetryNoticeCard 零分支。
		if ev.Retry != nil {
			events = append(events, agentruntime.Retry{
				Message: formatRetryMessage(ev.Retry),
				Details: formatRetryDetails(ev.Retry),
				Attempt: ev.Retry.Attempt,
				Max:     ev.Retry.MaxAttempts,
			})
		}
	case claudecode.EventPermissionModeChanged:
		if ev.PermissionMode != "" {
			events = append(events, agentruntime.PermissionModeChanged{Mode: ev.PermissionMode})
		}
	case claudecode.EventCompactBoundary:
		// system.compact_boundary 帧:CLI 内部已完成上下文压缩,LLM 只看得到摘要。
		// 透传 metadata 给 chat_svc,由它落 system message + 通知前端折叠旧消息。
		// Compact 帧理论上一定带 CompactEvent(parseSystemTask 会初始化),nil 保护
		// 兼容老解析路径。
		var info agentruntime.CompactBoundary
		if ev.Compact != nil {
			info.PreTokens = ev.Compact.PreTokens
			info.PostTokens = ev.Compact.PostTokens
			info.Trigger = ev.Compact.Trigger
			info.DurationMs = ev.Compact.DurationMs
		}
		events = append(events, info)
	case claudecode.EventStatus:
		// system{subtype:"status",status:<非空>} 帧:CLI 通报运行状态过渡 (compacting 等)。
		// 空 Status 不 emit —— 静默忽略与 EventPermissionModeChanged 同款守门规则。
		// 清理信号由 EventCompactBoundary / EventDone / EventError 路径传达。
		if ev.Status != "" {
			events = append(events, agentruntime.RuntimeStatus{Status: ev.Status})
		}
	case claudecode.EventInit:
		// system.init 帧带 model:Claude Code SDK 协议本身不报上下文窗口大小,
		// 这里查 cago llmcatalog 兜底,emit ContextWindowUpdated 让前端 turn 内
		// 就能看到窗口总量,不必等 EventDone 才显示进度条。catalog miss → 不 emit,
		// chat_svc resolveContextWindow* 仍会用解析出的 ContextWindow / ModelID
		// 兜底,不依赖本事件存在。
		if ev.Model != "" {
			if info, ok := llmcatalog.Lookup(ev.Model); ok && info.ContextWindow > 0 {
				events = append(events, agentruntime.ContextWindowUpdated{Tokens: info.ContextWindow})
			}
		}
	case claudecode.EventUsage:
		// 主 agent 帧的 per-call usage:turn 内每次 API call 边界都推一条,让
		// 上层(chat_svc → 前端 Composer 进度条)阶梯式刷新「已用上下文」。
		// TotalInputTokens 按 Anthropic family 聚合 = prompt + cached + cacheCreation
		// (spec §A token contract;event.go:109 的 UsageUpdate 文档)。
		u := ev.Usage
		events = append(events, agentruntime.UsageUpdate{
			Usage:            &u,
			TotalInputTokens: u.PromptTokens + u.CachedTokens + u.CacheCreationTokens,
		})
	case claudecode.EventDone:
		u := ev.Usage
		usage = &u
	}
	return
}

// recognizeCanonical 按工具名 + raw input JSON 识别已知 canonical 形状。
// 解析失败 / 工具不认识 → 返 nil,表示走 raw tool_use 路径(前端通用 ToolInvocationCard)。
//
// 识别本身全权委托给 canonical.FromToolUse —— live emit 路径(这里)和 replay
// 路径(chat_svc LoadSession 重建 tool_use 实体时调 canonical.FromToolUse)共用
// 同一份识别,避免两边各搞一套漂移。
func recognizeCanonical(name string, rawInput json.RawMessage) canonical.CanonicalTool {
	if len(rawInput) == 0 {
		return nil
	}
	var m map[string]any
	if err := json.Unmarshal(rawInput, &m); err != nil {
		return nil
	}
	if ct, ok := canonical.FromToolUse(name, m); ok {
		return ct
	}
	return nil
}

// isAskUserQuestionToolName 识别 AskUserQuestion 工具名(snake/Pascal 双写)。
func isAskUserQuestionToolName(name string) bool {
	return name == "AskUserQuestion" || name == "ask_user_question"
}

// asyncLaunchedStatus 是 CLI 在 user 帧顶层 toolUseResult 里给异步子代理启动回执打的
// 标记(与之同行的还有 isAsync:true / agentId / output_file)。
const asyncLaunchedStatus = "async_launched"

// isAsyncLaunchReceipt 判定这条 tool_result 是不是「异步子代理已派发」的回执。
//
// 派发异步 subagent 时 Agent 工具**立刻**返回一段自称 internal metadata 的文本
// (agentId / output_file / "never quote or paste any part of it … into a
// user-facing reply"),而子代理真正的产出几分钟后才随 task_notification 到达、
// 落在 SubagentInfo.Summary 上。两者是不同的事件,回执不是这次工具调用的结果,
// 因此在这唯一的生产点就不 emit —— 否则它会占住 AgentSpawnCard 的 SUMMARY 区
// (派发瞬间即填满,且永不被真摘要替换),还把内部元数据摊给用户看。
//
// 判据取结构化的 status 字段而非匹配那段英文散文。同步 Task/Agent 没有这个标记,
// 其 tool_result 确实就是子代理的产出,照常入流。
func isAsyncLaunchReceipt(meta json.RawMessage) bool {
	if len(meta) == 0 {
		return false
	}
	var probe struct {
		Status string `json:"status"`
	}
	if err := json.Unmarshal(meta, &probe); err != nil {
		return false
	}
	return probe.Status == asyncLaunchedStatus
}

// subagentInfoFromMeta 镜像顶层 claudecode.go 同名函数;返值类型从指针改为
// 值(SubagentStarted/Progress/Done 的 Info 字段直接是值)。nil 入参产零值
// SubagentInfo,留给下游做差量合并。
func subagentInfoFromMeta(m *claudecode.SubagentMeta) agentruntime.SubagentInfo {
	if m == nil {
		return agentruntime.SubagentInfo{}
	}
	return agentruntime.SubagentInfo{
		TaskID:          m.TaskID,
		SubagentType:    m.SubagentType,
		Kind:            m.TaskType,
		TaskDescription: m.TaskDescription,
		Prompt:          m.Prompt,
		LastToolName:    m.LastToolName,
		ToolUses:        m.ToolUses,
		TotalTokens:     m.TotalTokens,
		DurationMs:      m.DurationMs,
		Status:          m.Status,
	}
}

// formatRetryMessage 镜像顶层 claudecode.go formatClaudeRetryMessage。
func formatRetryMessage(r *claudecode.RetryEvent) string {
	switch {
	case r.ErrorStatus > 0 && r.ErrorCode != "":
		return fmt.Sprintf("HTTP %d %s", r.ErrorStatus, r.ErrorCode)
	case r.ErrorStatus > 0:
		return fmt.Sprintf("HTTP %d", r.ErrorStatus)
	default:
		return r.ErrorCode
	}
}

// formatRetryDetails 镜像顶层 claudecode.go formatClaudeRetryDetails。
func formatRetryDetails(r *claudecode.RetryEvent) string {
	if r.DelayMs <= 0 {
		return ""
	}
	if r.DelayMs < 1000 {
		return fmt.Sprintf("≈%.0fms 后重试", r.DelayMs)
	}
	return fmt.Sprintf("≈%.1fs 后重试", r.DelayMs/1000)
}
