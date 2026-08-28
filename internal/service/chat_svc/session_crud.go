package chat_svc

import (
	"context"
	"html"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/cago-frame/agents/agent/blocks"
	"github.com/cago-frame/cago/pkg/consts"
	"github.com/cago-frame/cago/pkg/i18n"
	"go.uber.org/zap"

	"github.com/agentre-hub/agentre/internal/model/entity/chat_entity"
	"github.com/agentre-hub/agentre/internal/pkg/agentruntime"

	"github.com/agentre-hub/agentre/internal/pkg/code"
	"github.com/agentre-hub/agentre/internal/repository/agent_backend_repo"
	"github.com/agentre-hub/agentre/internal/repository/agent_repo"
	"github.com/agentre-hub/agentre/internal/repository/chat_repo"
	"github.com/agentre-hub/agentre/internal/service/chat_svc/ipc"
)

// sessionTitleFromFirstMessage 从首条用户消息派生会话标题。
// @ 提及会把 `<agent id="1">名字</agent>` 这类 XML 写进消息正文，正文里是对的，
// 但标题会显示在 tab / 侧栏 / 标题栏 —— 直接用会露出一坨裸 XML。这里把标签还原成可读的 `@名字`。
func sessionTitleFromFirstMessage(text string) string {
	out := mentionXMLRe.ReplaceAllStringFunc(text, func(m string) string {
		sub := mentionXMLRe.FindStringSubmatch(m)
		if len(sub) < 3 {
			return m
		}
		return "@" + html.UnescapeString(sub[2])
	})
	return strings.TrimSpace(out)
}

func textOfMessage(m *chat_entity.Message) string {
	bs, _ := m.GetBlocks()
	for _, b := range bs {
		if tb, ok := b.(blocks.TextBlock); ok {
			return tb.Text
		}
		if tb, ok := b.(*blocks.TextBlock); ok && tb != nil {
			return tb.Text
		}
	}
	return ""
}

func (s *chatSvc) Rename(ctx context.Context, req *RenameRequest) (*RenameResponse, error) {
	title := strings.TrimSpace(req.Title)
	if utf8.RuneCountInString(title) > renameTitleMaxRunes {
		return nil, i18n.NewError(ctx, code.ChatTitleTooLong)
	}
	sess, err := chat_repo.Session().Find(ctx, req.SessionID)
	if err != nil {
		return nil, operationFailedWithCause(ctx, err)
	}
	if sess == nil {
		return nil, i18n.NewError(ctx, code.ChatSessionNotFound)
	}
	sess.Title = title
	if err := chat_repo.Session().Update(ctx, sess); err != nil {
		return nil, operationFailedWithCause(ctx, err)
	}
	return &RenameResponse{}, nil
}

func (s *chatSvc) Delete(ctx context.Context, req *DeleteRequest) (*DeleteResponse, error) {
	if err := chat_repo.Session().SoftDelete(ctx, req.SessionID); err != nil {
		return nil, operationFailedWithCause(ctx, err)
	}
	// DB 已删，释放该 session 的常驻 CLI 子进程（best-effort，cache miss 时 no-op）。
	// 按 runtime 注册表广播而不是逐个后端点名：点名的写法每加一个有常驻子进程的
	// 后端就会静默漏一处，漏掉的表现是机器上多一个永不退出的 CLI。
	agentruntime.CloseSessionEverywhere(ctx, req.SessionID)
	// 子进程已关，撤销并清掉它的常驻 gateway token（token 寿命跟随子进程）。
	s.revokeChatToken(req.SessionID)
	return &DeleteResponse{}, nil
}

// MarkSessionRead 推进会话 last_read_at 到至少 req.Timestamp (unix ms)。
// Timestamp <= 0 时改用 time.Now()。repo 层 MarkRead 自带「仅当新 ts 严格大于旧值时
// 才写入」的单调语义，所以乱序到达的 stream-done 不会把已读时间冲回旧值。
func (s *chatSvc) MarkSessionRead(ctx context.Context, req *MarkSessionReadRequest) (*MarkSessionReadResponse, error) {
	if req == nil || req.SessionID <= 0 {
		return nil, i18n.NewError(ctx, code.InvalidParameter)
	}
	ts := req.Timestamp
	if ts <= 0 {
		ts = time.Now().UnixMilli()
	}
	if err := chat_repo.Session().MarkRead(ctx, req.SessionID, ts); err != nil {
		return nil, operationFailedWithCause(ctx, err)
	}
	return &MarkSessionReadResponse{}, nil
}

func (s *chatSvc) EnsureSession(ctx context.Context, req *EnsureSessionRequest) (*EnsureSessionResponse, error) {
	if req == nil {
		return nil, i18n.NewError(ctx, code.InvalidParameter)
	}
	switch req.Purpose {
	case SessionPurposeSubagentCall:
		return s.createSubagentSession(ctx, req.AgentID, req.ProjectID, req.Title)
	case SessionPurposeUserChat:
		return s.createUserChatSession(ctx, req.AgentID, req.ProjectID, req.Title)
	default:
		return nil, i18n.NewError(ctx, code.InvalidParameter)
	}
}

// createUserChatSession 建一个普通用户会话(每次新建)。与 createSubagentSession 同形, 唯一区别:
// Purpose 留空 —— 这是用户在侧栏可见、可继续对话的正常会话, 不是隐藏的隔离子会话。
// 供 ! 命令在「新会话占位态」(还没 sessionId)先坐实会话, 之后命令有 cwd 可解析、卡片有 transcript 可渲染。
func (s *chatSvc) createUserChatSession(ctx context.Context, agentID, projectID int64, title string) (*EnsureSessionResponse, error) {
	if agentID <= 0 {
		return nil, i18n.NewError(ctx, code.InvalidParameter)
	}
	// 普通用户会话是交互式的(有人审阅), 套用「先 plan 后 bypass」派生 → planFirst=true。
	permissionMode := s.launchPermissionModeForAgent(ctx, agentID, true)
	sess := &chat_entity.Session{
		AgentID:                agentID,
		ProjectID:              projectID,
		PermissionMode:         permissionMode,
		PermissionModeAtLaunch: permissionMode,
		Title:                  strings.TrimSpace(title),
		AgentStatus:            "idle",
		Status:                 consts.ACTIVE,
		// Purpose 留空 = 普通用户会话。
	}
	if err := chat_repo.Session().Create(ctx, sess); err != nil {
		return nil, operationFailedWithCause(ctx, err, zap.Int64("agentId", agentID))
	}
	return &EnsureSessionResponse{SessionID: sess.ID, Created: true}, nil
}

// createSubagentSession 为子 agent 调用建一个全新的一次性隔离会话(每次新建)。
// 不做幂等复用 —— 每次 agent_call 都要干净的隔离上下文。
func (s *chatSvc) createSubagentSession(ctx context.Context, agentID, projectID int64, title string) (*EnsureSessionResponse, error) {
	if agentID <= 0 {
		return nil, i18n.NewError(ctx, code.InvalidParameter)
	}
	// 子 agent 调用是自律执行(没人审阅计划), 直接尊重配置的 bypass → planFirst=false。
	permissionMode := s.launchPermissionModeForAgent(ctx, agentID, false)
	sess := &chat_entity.Session{
		AgentID:                agentID,
		ProjectID:              projectID,
		Purpose:                chat_entity.SessionPurposeSubagent,
		PermissionMode:         permissionMode,
		PermissionModeAtLaunch: permissionMode,
		Title:                  strings.TrimSpace(title),
		AgentStatus:            "idle",
		Status:                 consts.ACTIVE,
	}
	if err := chat_repo.Session().Create(ctx, sess); err != nil {
		return nil, operationFailedWithCause(ctx, err, zap.Int64("agentId", agentID))
	}
	return &EnsureSessionResponse{SessionID: sess.ID, Created: true}, nil
}

// launchPermissionModeForAgent 解析某 agent 后端在新建会话时的默认权限模式。
// 只做轻量只读解析(agent → backend → createPermissionMode), 不做 provider/gateway 可聊性校验
// —— 那些属于 send 起手时的职责。解析不出(agent/后端缺失或后端无权限模式概念)时返回空串,
// 由 runtime 首轮回填 at_launch 兜底。
// planFirst: 交互式会话传 true(套用「先 plan 后 bypass」派生), 自律会话
// (subagent 调用)传 false(直接尊重配置的 bypass)。
func (s *chatSvc) launchPermissionModeForAgent(ctx context.Context, agentID int64, planFirst bool) string {
	a, err := agent_repo.Agent().Find(ctx, agentID)
	if err != nil || a == nil {
		return ""
	}
	be, err := agent_backend_repo.AgentBackend().Find(ctx, a.AgentBackendID)
	if err != nil || be == nil {
		return ""
	}
	mode, err := ipc.CreatePermissionMode(ctx, be, "", planFirst)
	if err != nil {
		return ""
	}
	return mode
}
