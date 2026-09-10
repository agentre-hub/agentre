package app

import (
	"github.com/cago-frame/cago/pkg/i18n"

	"github.com/agentre-hub/agentre/internal/pkg/code"
	"github.com/agentre-hub/agentre/internal/service/chat_svc"
	"github.com/agentre-hub/agentre/internal/service/chat_svc/ipc"
)

// ListChatAgents 聚合返回左栏 Agent 列表（含每个 Agent 的最近会话和可对话状态）。
func (a *App) ListChatAgents() (*chat_svc.ListAgentsResponse, error) {
	return chat_svc.Chat().ListAgents(a.ctx, &chat_svc.ListAgentsRequest{})
}

// ListChatAgentSessions 给「查看全部 N 个会话」popover 翻页拉数据。
func (a *App) ListChatAgentSessions(req *chat_svc.ListAgentSessionsRequest) (*chat_svc.ListAgentSessionsResponse, error) {
	return chat_svc.Chat().ListAgentSessions(a.ctx, req)
}

// ListChatIndexSessions 给单一会话索引翻页：scope="recent" 是跨 agent、跨项目的全局
// 最近活动（「按时间」档），scope="free" 是未挂项目的会话（「随手对话」组）。
func (a *App) ListChatIndexSessions(req *chat_svc.ListIndexSessionsRequest) (*chat_svc.ListIndexSessionsResponse, error) {
	return chat_svc.Chat().ListIndexSessions(a.ctx, req)
}

// ListAgentExecTargetAvailability 逐档判定 R15 执行目标列表的可用性，供组织架构页
// Agent 详情展示（任务 12）。projectID<=0 表示自由会话，不做项目路径判定。
func (a *App) ListAgentExecTargetAvailability(agentID int64, projectID int64) ([]chat_svc.ExecTargetAvailabilityView, error) {
	return chat_svc.Chat().ListExecTargetAvailability(a.ctx, agentID, projectID)
}

// EnsureChatSession 为某 agent 建一个普通用户会话并返回 sessionId（不发消息、不起 turn）。
// 供前端 ! 命令在「新会话占位态」(还没 sessionId)先坐实一个真实会话，之后命令才有
// cwd 可解析、卡片有 transcript 可渲染。projectID 可为 0(自由会话)。
func (a *App) EnsureChatSession(agentID int64, projectID int64) (int64, error) {
	resp, err := chat_svc.Chat().EnsureSession(a.ctx, &chat_svc.EnsureSessionRequest{
		Purpose:   chat_svc.SessionPurposeUserChat,
		AgentID:   agentID,
		ProjectID: projectID,
	})
	if err != nil {
		return 0, err
	}
	return resp.SessionID, nil
}

// LoadChatSession 拉单个 session 的 detail + 全部消息。
func (a *App) LoadChatSession(req *chat_svc.LoadSessionRequest) (*chat_svc.LoadSessionResponse, error) {
	return chat_svc.Chat().LoadSession(a.ctx, req)
}

// LoadChatMessageBlocks 取回更早一段消息的正文 —— LoadChatSession 只给最近一个窗口的
// 完整正文,前端向上滚动时靠它续接转录。
func (a *App) LoadChatMessageBlocks(req *chat_svc.LoadMessageBlocksRequest) (*chat_svc.LoadMessageBlocksResponse, error) {
	return chat_svc.Chat().LoadMessageBlocks(a.ctx, req)
}

// LoadChatSessionBlocksByType 按块类型点查整条会话的那一类块,供后台任务面板 / 大纲 /
// 变更这些派生视图取数,免得为了算它们把整条转录搬到前端。
func (a *App) LoadChatSessionBlocksByType(req *chat_svc.LoadSessionBlocksByTypeRequest) (*chat_svc.LoadSessionBlocksByTypeResponse, error) {
	return chat_svc.Chat().LoadSessionBlocksByType(a.ctx, req)
}

// GetChatLaunchCommand 把当前 session 的 CLI 后端配置拼成可在终端粘贴运行的命令。
// 仅 claudecode / codex / piagent 有效；builtin 返回 ChatLaunchCommandNotAvailable。
// 命令中的 gateway token 故意写成占位符 <TOKEN>，不发放实际 token，用户自行替换。
func (a *App) GetChatLaunchCommand(req *chat_svc.LaunchCommandRequest) (*chat_svc.LaunchCommandResponse, error) {
	return chat_svc.Chat().GetLaunchCommand(a.ctx, req)
}

// GetSessionGitState 拉某 session 对应 cwd 的 git 状态快照, 供右侧上下文侧栏的
// branch / worktree / dirty / ahead·behind 几个 chip 用。本地与远端(agentred)
// backend 都取真实快照(远端经 workspacefs.gitState RPC);设备未配对 / cwd
// 解析不出 / 调用失败(含旧 daemon 的方法不存在)一律降级为 notARepo=true,
// 让前端折叠 chip 区,而不是把异常暴露给这个纯展示区域。
func (a *App) GetSessionGitState(req *chat_svc.GetSessionGitStateRequest) (*chat_svc.GetSessionGitStateResponse, error) {
	return chat_svc.Chat().GetSessionGitState(a.ctx, req)
}

// SendChatMessage 发一条用户消息并起异步 turn；返回 stream 事件名。
func (a *App) SendChatMessage(req *chat_svc.SendRequest) (*chat_svc.SendResponse, error) {
	return chat_svc.Chat().Send(a.ctx, req)
}

// ChatReadDroppedImages 按绝对路径读取拖入的图片候选,做 stat/类型/大小校验后归类
// (image=可附件 / path=降级为纯路径)。供 composer 拖拽链路区分图片与文件/文件夹。
func (a *App) ChatReadDroppedImages(req *chat_svc.ReadDroppedImagesRequest) (*chat_svc.ReadDroppedImagesResponse, error) {
	return chat_svc.ReadDroppedImages(a.ctx, req)
}

// CompactChatSession 触发 Codex app-server 原生 thread/compact/start。
// 不创建用户消息；压缩完成后 runtime 会 emit compact_boundary 供前端折叠历史。
func (a *App) CompactChatSession(req *chat_svc.CompactRequest) (*chat_svc.CompactResponse, error) {
	return chat_svc.Chat().Compact(a.ctx, req)
}

func (a *App) GetChatGoal(req *chat_svc.GoalRequest) (*chat_svc.GoalResponse, error) {
	return chat_svc.Chat().GetGoal(a.ctx, req)
}

func (a *App) SetChatGoal(req *chat_svc.SetGoalRequest) (*chat_svc.GoalResponse, error) {
	return chat_svc.Chat().SetGoal(a.ctx, req)
}

func (a *App) StartChatGoal(req *chat_svc.StartGoalRequest) (*chat_svc.StartGoalResponse, error) {
	return chat_svc.Chat().StartGoal(a.ctx, req)
}

func (a *App) ClearChatGoal(req *chat_svc.ClearGoalRequest) (*chat_svc.ClearGoalResponse, error) {
	return chat_svc.Chat().ClearGoal(a.ctx, req)
}

// EnqueueChatMessage 在 AI 还在回答时把一条新的用户消息插入当前 turn。
// claudecode 走 PreToolUse hook + additionalContext，codex 走 turn/steer RPC。
// 没有 in-flight turn 时返回 ChatSteerNoActive。响应里带 queuedId + cancellable
// 给前端，后续 CancelQueuedChatMessage 按 ID 撤回。
func (a *App) EnqueueChatMessage(req *chat_svc.EnqueueRequest) (*chat_svc.EnqueueResponse, error) {
	return chat_svc.Chat().Enqueue(a.ctx, req)
}

// CancelQueuedChatMessage 撤回 Enqueue 投递但尚未被 AI 消费的排队消息。
// QueuedID 为空表示清空整条队列。codex 后端不支持撤回（turn/steer 一发即弃），
// 返回 ChatCancelUnsupported。
func (a *App) CancelQueuedChatMessage(req *chat_svc.CancelQueuedRequest) (*chat_svc.CancelQueuedResponse, error) {
	return chat_svc.Chat().CancelQueued(a.ctx, req)
}

// StopChatMessage 软中断当前正在跑的 turn。claudecode 走 stream-json
// control_request{interrupt}、codex 走 turn/interrupt RPC、builtin 走 ctx cancel —
// 三个后端都保留子进程 / 内存状态，下一条消息直接续。
// 无活跃 turn 时返 ChatStopNoActive，前端把按钮回灰。
func (a *App) StopChatMessage(req *chat_svc.StopRequest) (*chat_svc.StopResponse, error) {
	return chat_svc.Chat().Stop(a.ctx, req)
}

// StopBackgroundTask 停掉某个后台任务 / 子 agent(run_in_background bash / subagent),
// 而非中断整个 turn。走 CLI control_request{stop_task}。仅 claudecode 后端支持(按
// capability 门控前端按钮);缺 task_id 的老会话返 ChatStopBgTaskUnknown。
func (a *App) StopBackgroundTask(req *chat_svc.StopBackgroundTaskRequest) (*chat_svc.StopBackgroundTaskResponse, error) {
	return chat_svc.Chat().StopBackgroundTask(a.ctx, req)
}

// SetChatPermissionMode 切换 claude code 会话的 permission mode（default /
// acceptEdits / plan / bypassPermissions）。对齐 Claude TUI 的 Shift+Tab 行为。
// 仅 claudecode 后端支持；codex / builtin 返 ChatPermissionModeUnsupported。
// 会话尚未起（CLI 子进程不在 LRU）时返 ChatPermissionModeNoActive。
func (a *App) SetChatPermissionMode(req *chat_svc.SetPermissionModeRequest) (*chat_svc.SetPermissionModeResponse, error) {
	return chat_svc.Chat().SetPermissionMode(a.ctx, req)
}

// SetChatSessionModelTarget 切换已有会话的 LLM ModelTarget（ProviderKey/ModelKey 双空 =
// 跟随 agent 绑定，CLI 后端即回到自身登录态）。原子写 provider_key + model_key 两列，自
// 下一轮生效，不打断正在进行的轮；所选 provider/model 缺失 / 停用 / 与后端类型不兼容时
// 拒绝写入并报错，会话保持原 target。
func (a *App) SetChatSessionModelTarget(req *chat_svc.SetChatSessionModelTargetRequest) (*chat_svc.SetChatSessionModelTargetResponse, error) {
	return chat_svc.Chat().SetChatSessionModelTarget(a.ctx, req)
}

// SetChatSessionReasoningEffort 切换已有会话的思考力度（空串 = 跟随该会话那一档
// backend 的配置）。只写 reasoning_effort 一列，自下一轮 spawn 生效，不打断正在进行的
// 轮、也不驱逐任何进程池条目；档位不在六档表里时拒绝写入并报错，会话保持原档位。
func (a *App) SetChatSessionReasoningEffort(req *chat_svc.SetChatSessionReasoningEffortRequest) (*chat_svc.SetChatSessionReasoningEffortResponse, error) {
	return chat_svc.Chat().SetChatSessionReasoningEffort(a.ctx, req)
}

// RegenerateChatMessage 截掉指定 assistant 消息之前的 user 锚点后，用同一段
// user 文本重新走一遍 turn。Step 1：仅 builtin 后端实际工作；CLI 后端在 runner
// 接入 Rewinder 之前返回 ChatRegenerateUnsupported。
func (a *App) RegenerateChatMessage(req *chat_svc.RegenerateRequest) (*chat_svc.SendResponse, error) {
	return chat_svc.Chat().Regenerate(a.ctx, req)
}

// EditChatMessage 编辑历史 user 消息后用新文本重跑 turn。
// 截到 target user（含）开始的全部历史，replay 新文本；fork 路径与 Regenerate 共用。
func (a *App) EditChatMessage(req *chat_svc.EditRequest) (*chat_svc.SendResponse, error) {
	return chat_svc.Chat().Edit(a.ctx, req)
}

// RenameChatSession 重命名会话。
func (a *App) RenameChatSession(req *chat_svc.RenameRequest) (*chat_svc.RenameResponse, error) {
	return chat_svc.Chat().Rename(a.ctx, req)
}

// DeleteChatSession 软删会话。
func (a *App) DeleteChatSession(req *chat_svc.DeleteRequest) (*chat_svc.DeleteResponse, error) {
	return chat_svc.Chat().Delete(a.ctx, req)
}

// MarkChatSessionRead 推进会话「最后已读时间」(单调，旧 ts 自动忽略)。
// 前端打开会话或 stream done 后调用一次，驱动 sidebar attention bubble 的未读判定。
func (a *App) MarkChatSessionRead(req *chat_svc.MarkSessionReadRequest) (*chat_svc.MarkSessionReadResponse, error) {
	return chat_svc.Chat().MarkSessionRead(a.ctx, req)
}

// AnswerUserQuestion 提交 AskUserQuestion 交互卡片的答案。RequestID 来自
// stream 上的 StreamAskUserQuestion 事件；Skipped=true 时 Answers 可为空，
// runner 会按后端协议回写跳过信号。当前 claudecode / codex 后端支持；
// 其它 backend 接入需要实现 agentruntime.AskAnswerSink。
func (a *App) AnswerUserQuestion(req *chat_svc.AnswerUserQuestionRequest) (*chat_svc.AnswerUserQuestionResponse, error) {
	return chat_svc.Chat().AnswerUserQuestion(a.ctx, req)
}

// AnswerToolPermission 提交工具调用审批决策。RequestID 来自 stream 上的
// StreamToolPermissionRequest 事件；Allow=true + AlwaysAllowSession=true 时
// runner 会附加 updatedPermissions=[{addRules, [{toolName}], allow, session}]
// 让 SDK 内化为后续 allow rule。当前仅 claudecode 后端支持。
func (a *App) AnswerToolPermission(req *chat_svc.AnswerToolPermissionRequest) (*chat_svc.AnswerToolPermissionResponse, error) {
	return chat_svc.Chat().AnswerToolPermission(a.ctx, req)
}

// ResolveExecApproval returns an OpenClaw exec approval decision through the
// active Gateway runtime. The service validates the session/backend/decision.
func (a *App) ResolveExecApproval(req *chat_svc.ResolveExecApprovalRequest) (*chat_svc.ResolveExecApprovalResponse, error) {
	return chat_svc.Chat().ResolveExecApproval(a.ctx, req)
}

// AnswerToolApproval agent 内置工具(org / hook 等)写操作的审批决策
// (批准/拒绝),按 requestID 路由唤醒挂起的工具调用。各工具不再各自持 waiter / Answer,
// 统一经此入口落到 chat_svc 的通用审批管线。
func (a *App) AnswerToolApproval(req *chat_svc.AnswerToolApprovalRequest) (*chat_svc.AnswerToolApprovalResponse, error) {
	if req == nil {
		return nil, i18n.NewError(a.ctx, code.InvalidParameter)
	}
	if err := chat_svc.Chat().AnswerToolApproval(a.ctx, req.SessionID, req.RequestID, req.Allow); err != nil {
		return nil, err
	}
	return &chat_svc.AnswerToolApprovalResponse{}, nil
}

// ResolvePlanAction 计划审批/历史计划 actionId 的入口。前端按 provider-neutral
// plan action 渲染按钮 + 拿到原 actionId 回传,不再分支 backendType/source;
// 后端按 actionID 语义分发到 AnswerToolPermission / Send。
func (a *App) ResolvePlanAction(req *chat_svc.ResolvePlanActionRequest) (*chat_svc.ResolvePlanActionResponse, error) {
	return chat_svc.Chat().ResolvePlanAction(a.ctx, req)
}

// GetSessionCapabilities 把 session backend 的能力矩阵 + permission mode 元数据
// 上行给前端做 capability gating / PermissionModePill metadata 等 UI 投影。
// 取代之前散落在前端的"按 backend 字符串硬编码 caps"的开关。
func (a *App) GetSessionCapabilities(req *ipc.GetSessionCapabilitiesRequest) (*ipc.GetSessionCapabilitiesResponse, error) {
	return ipc.GetSessionCapabilities(a.ctx, req)
}

// GetBackendCapabilities 新对话场景下(尚无 sessionId)按 backend type 取能力矩阵,
// 让前端在"还没 spawn"时也能正确渲染 PermissionModePill / 起手 mode。
// 已有 session 走 GetSessionCapabilities,语义一致。
func (a *App) GetBackendCapabilities(req *ipc.GetBackendCapabilitiesRequest) (*ipc.GetSessionCapabilitiesResponse, error) {
	return ipc.GetBackendCapabilities(a.ctx, req)
}
