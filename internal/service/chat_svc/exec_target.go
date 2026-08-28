package chat_svc

import (
	"context"

	"github.com/cago-frame/cago/pkg/i18n"
	"go.uber.org/zap"

	"github.com/agentre-hub/agentre/internal/model/entity/agent_backend_entity"
	"github.com/agentre-hub/agentre/internal/model/entity/agent_entity"
	"github.com/agentre-hub/agentre/internal/model/entity/chat_entity"
	"github.com/agentre-hub/agentre/internal/pkg/code"
	"github.com/agentre-hub/agentre/internal/repository/agent_backend_repo"
	"github.com/agentre-hub/agentre/internal/repository/agent_repo"
	"github.com/agentre-hub/agentre/internal/repository/chat_repo"
)

// sessionBackendID 是「这条会话该用哪个 backend」的单一读口（R15b / 决策36）：
// 钉住了就用钉住的那一档，没钉住（首轮 / 全部老会话）才回落到 Agent 的主档。
//
// 凡是拿着一条**已持久化会话**去解析 backend 的读路径都必须经过这里——同一台机器
// 上可以有多档，回到 a.AgentBackendID 会让续轮拿到另一档的 CLI 路径、供应商、模型
// 路由与技能授权，正是决策 16 禁止的中途漂移。这里只读不写：钉住的写入是 startTurn
// 的职责（pinExecTargetIfUnset），查询一下不该把会话钉死。
func sessionBackendID(sess *chat_entity.Session, a *agent_entity.Agent) int64 {
	if sess != nil && sess.ExecAgentBackendID > 0 {
		return sess.ExecAgentBackendID
	}
	if a == nil {
		return 0
	}
	return a.AgentBackendID
}

// execDeviceID 返回 ! 命令应在哪台设备执行：remote 后端取其 DeviceID；本地与指向本机
// 的 self 档（R13 认领后本机 backend 的 DeviceID 是本机指纹）都返回空串。
func execDeviceID(ctx context.Context, be *agent_backend_entity.AgentBackend) string {
	if beTargetsRemote(be) {
		return be.DeviceFingerprint
	}
	return ""
}

// resolveExecTarget 解析一个 session 值对象的当前执行目标。sess 可以是已持久化
// 会话，也可以是只带 AgentID/ProjectID 的未持久化预会话对象；该方法只读仓储。
//
// 已持久化且已经钉住某一档的会话（R15b / 决策36）优先用那一档 —— "!" 命令的执行
// 范围必须跟续轮实际解析到的 backend 一致，不能各算各的（同一台机器上可以有多档）。
// 未钉住（预会话 / 老会话）时回落到 Agent 的当前主档，与今天一致；本方法只读，不写回
// 钉住状态 —— 那是 startTurn 的职责，不能因为用户只是查询一下就把会话钉死。
func (s *chatSvc) resolveExecTarget(ctx context.Context, sess *chat_entity.Session) (*LocalCommandScope, error) {
	if sess == nil || sess.AgentID <= 0 || sess.ProjectID < 0 {
		return nil, i18n.NewError(ctx, code.InvalidParameter)
	}
	a, err := agent_repo.Agent().Find(ctx, sess.AgentID)
	if err != nil {
		return nil, operationFailedWithCause(ctx, err, zap.Int64("agentId", sess.AgentID))
	}
	if a == nil {
		return nil, i18n.NewError(ctx, code.AgentNotFound)
	}
	backendID := sessionBackendID(sess, a)
	if backendID <= 0 {
		return nil, i18n.NewError(ctx, code.ChatAgentNoBackend)
	}
	be, err := agent_backend_repo.AgentBackend().Find(ctx, backendID)
	if err != nil {
		return nil, operationFailedWithCause(ctx, err, zap.Int64("backendId", backendID))
	}
	if be == nil {
		return nil, i18n.NewError(ctx, code.AgentBackendNotFound)
	}
	if beTargetsRemote(be) {
		deviceID, ok := localPairedDeviceID(ctx, be.DeviceFingerprint)
		if !ok || deviceID <= 0 {
			return nil, i18n.NewError(ctx, code.AgentBackendInvalidDevice)
		}
	}
	cwd, err := resolveSessionCwd(ctx, sess, be)
	if err != nil {
		return nil, err
	}
	return &LocalCommandScope{DeviceID: execDeviceID(ctx, be), Cwd: cwd}, nil
}

// ResolveLocalCommandScope 为已有 session，或尚未创建 session 的 agent/project 目标
// 解析当前设备/cwd。预会话分支只构造值对象，不调用 Session.Create/Ensure。
func (s *chatSvc) ResolveLocalCommandScope(ctx context.Context, req *ResolveLocalCommandScopeRequest) (*LocalCommandScope, error) {
	sess, err := localCommandScopeSession(ctx, req)
	if err != nil {
		return nil, err
	}
	return s.resolveExecTarget(ctx, sess)
}

func localCommandScopeSession(ctx context.Context, req *ResolveLocalCommandScopeRequest) (*chat_entity.Session, error) {
	if req == nil || req.SessionID < 0 || req.AgentID < 0 || req.ProjectID < 0 {
		return nil, i18n.NewError(ctx, code.InvalidParameter)
	}
	if req.SessionID > 0 {
		if req.AgentID != 0 || req.ProjectID != 0 {
			return nil, i18n.NewError(ctx, code.InvalidParameter)
		}
		sess, err := chat_repo.Session().Find(ctx, req.SessionID)
		if err != nil {
			return nil, operationFailedWithCause(ctx, err, zap.Int64("sessionId", req.SessionID))
		}
		if sess == nil {
			return nil, i18n.NewError(ctx, code.ChatSessionNotFound)
		}
		return sess, nil
	}
	if req.AgentID <= 0 {
		return nil, i18n.NewError(ctx, code.InvalidParameter)
	}
	return &chat_entity.Session{AgentID: req.AgentID, ProjectID: req.ProjectID}, nil
}
