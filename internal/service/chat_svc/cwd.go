package chat_svc

import (
	"context"
	"errors"

	"github.com/cago-frame/cago/pkg/i18n"
	"github.com/cago-frame/cago/pkg/utils/httputils"
	"gorm.io/gorm"

	"github.com/agentre-hub/agentre/internal/model/entity/agent_backend_entity"
	"github.com/agentre-hub/agentre/internal/model/entity/chat_entity"
	"github.com/agentre-hub/agentre/internal/pkg/agentruntime"
	"github.com/agentre-hub/agentre/internal/pkg/code"
	"github.com/agentre-hub/agentre/internal/repository/agent_backend_repo"
	"github.com/agentre-hub/agentre/internal/repository/agent_repo"
	"github.com/agentre-hub/agentre/internal/repository/chat_repo"
	"github.com/agentre-hub/agentre/internal/repository/project_location_repo"
)

// cwdUnavailableReasonFor 把 resolveSessionCwd 的错误分类成结构化字符串（R10），
// 供 LoadSession 填充 ChatSessionDetail.CwdUnavailableReason——Wails 边界只过
// Error() 字符串，这是唯一能让前端区分"本机未配置路径 / 远端未配置路径 / 压根
// 没有 cwd"三种情形的通道。err 不携带这两个已知码时返回空串，前端按既有的笼统
// 提示兜底，不引入第四种未知状态。
func cwdUnavailableReasonFor(err error) string {
	var appErr *httputils.Error
	if !errors.As(err, &appErr) {
		return ""
	}
	switch appErr.Code {
	case code.ProjectLocalPathMissing:
		return "local-path-missing"
	case code.ProjectLocationMissing:
		return "location-missing"
	default:
		return ""
	}
}

// CwdResolver session → cwd 解析器；project_svc 在启动时调 RegisterCwdResolver
// 注入；未注入时回退到 agentruntime.AgentCwd（保留 Agent 级老行为）。
//
// 引入回调而不是直接 import project_svc 是为了让 chat_svc 不依赖 project_svc
// 的具体实现 —— project_svc 已经依赖 chat_repo，反向再 import 会成环。
type CwdResolver func(ctx context.Context, session *chat_entity.Session) (string, error)

var resolveCwdFn CwdResolver

// RegisterCwdResolver 由 bootstrap 注入 project-aware 实现。
func RegisterCwdResolver(fn CwdResolver) { resolveCwdFn = fn }

// resolveSessionCwd 按 backend 走本地 / 远端两条路径：
//   - be 为 nil 或 be.IsLocal()：沿用 CwdResolver 回调（project.Path / AgentCwd fallback）。
//   - be.IsRemote() + ProjectID > 0：查 project_locations(project_id, device_id)
//     拿远端机器上的 path；找不到 → ProjectLocationMissing（前端引导用户去
//     Project Settings → Members 配置）。
//   - be.IsRemote() + ProjectID = 0（自由会话）：返回空 cwd，把兜底权下放给远端
//     daemon 端的 runtime —— claudecode/codex 在 cwd=="" 时会自己调 AgentCwd
//     落到远端机器的 <AppDataDir>/agents/<agent_id>/。
//
// 会话自己钉了工作目录（chat_sessions.cwd 非空）时**先认它**，本地与远端两条路
// 都是：那一列是导入本地会话的落点（spec 2026-08-26「续跑」：工作目录取磁盘转录里
// 记录的 cwd），而 claude 的 --resume 按 cwd 定位 project 目录 —— 现算出来的
// project.Path / AgentCwd 换了目录，那条 provider session id 在那儿根本不存在。
// 远端那一支尤其不能漏：机器轴导进来的会话正是 ProjectID = 0 的那一支，回落空串
// 等于把它送去远端机器的 <AppDataDir>/agents/<id>。
//
// session 为 nil 时返回 "". be 为 nil 视作本地，是为了 GetLaunchCommand 这种
// 还在拼命令的场景能复用 —— 那条路径目前还没引入 device 概念。
func resolveSessionCwd(ctx context.Context, sess *chat_entity.Session, be *agent_backend_entity.AgentBackend) (string, error) {
	if sess == nil {
		return "", nil
	}
	if sess.Cwd != "" {
		return sess.Cwd, nil
	}
	// self 档（R13 认领后本机 backend 的 DeviceFingerprint 是本机指纹）也按本机处理：
	// cwd 走本机 CwdResolver（project.Path / AgentCwd fallback）。
	if !beTargetsRemote(be) {
		if resolveCwdFn != nil {
			return resolveCwdFn(ctx, sess)
		}
		return agentruntime.AgentCwd(sess.AgentID)
	}
	if sess.ProjectID == 0 {
		return "", nil
	}
	repo := project_location_repo.ProjectLocation()
	if repo == nil {
		return "", errors.New("project_location_repo not registered")
	}
	// 远端档按（project, device_fingerprint）自然键查行 —— backend 的 DeviceFingerprint 只有
	// 规范指纹一种形态，project_locations 的数值 device_id 是同一行的缓存列（决策
	// 26），不拿指纹去匹配它。
	loc, err := repo.FindByProjectAndFingerprint(ctx, sess.ProjectID, be.DeviceFingerprint)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return "", i18n.NewError(ctx, code.ProjectLocationMissing)
		}
		return "", err
	}
	return loc.Path, nil
}

// ResolveSessionCwd is the public adapter for resolveSessionCwd, exposed so
// terminal_svc (and other future services) can reuse the same project /
// device cwd resolution rules without re-implementing them.
func ResolveSessionCwd(ctx context.Context, sess *chat_entity.Session, be *agent_backend_entity.AgentBackend) (string, error) {
	return resolveSessionCwd(ctx, sess, be)
}

// ResolveSessionWorkspace 把 sessionID 解析成 {deviceID, cwd},实现
// workspace_fs_svc 自己声明的 SessionWorkspaceResolver 窄接口(在 bootstrap
// 注入)。放在 chat_svc 是因为这里本来就持有 session → agent → backend 的查询
// 与 resolveSessionCwd 的本地/远端规则;workspace_fs_svc 因此不必跨域去读
// chat / agent / agent_backend 三张表。
//
// deviceID 为 0 表示本机会话。远端 backend 的 DeviceFingerprint 解析不出整数时直接报
// 错,而不是回落 0 —— 那会让调用方拿着远端机器上的路径去列本机文件系统。
func (s *chatSvc) ResolveSessionWorkspace(ctx context.Context, sessionID int64) (int64, string, error) {
	if sessionID <= 0 {
		return 0, "", i18n.NewError(ctx, code.InvalidParameter)
	}
	sess, err := chat_repo.Session().Find(ctx, sessionID)
	if err != nil {
		return 0, "", operationFailedWithCause(ctx, err)
	}
	if sess == nil {
		return 0, "", i18n.NewError(ctx, code.ChatSessionNotFound)
	}
	a, err := agent_repo.Agent().Find(ctx, sess.AgentID)
	if err != nil {
		return 0, "", operationFailedWithCause(ctx, err)
	}
	// 钉住的那一档优先（R15b / 决策36）：主档在本机、会话钉在某台 agentred 上时，
	// 回到主档会拿本机路径去列远端机器的文件。
	var be *agent_backend_entity.AgentBackend
	if backendID := sessionBackendID(sess, a); backendID > 0 {
		be, err = agent_backend_repo.AgentBackend().Find(ctx, backendID)
		if err != nil {
			return 0, "", operationFailedWithCause(ctx, err)
		}
	}

	var deviceID int64
	// self 档（R13 认领后本机 backend 的 DeviceFingerprint 是本机指纹）也按本机处理：deviceID 0
	// 让 workspace_fs_svc 走本机文件系统，而不是去配对表里找行报 RemoteDeviceNotFound。
	if beTargetsRemote(be) {
		id, ok := localPairedDeviceID(ctx, be.DeviceFingerprint)
		if !ok {
			return 0, "", i18n.NewError(ctx, code.RemoteDeviceNotFound)
		}
		deviceID = id
	}

	cwd, err := resolveSessionCwd(ctx, sess, be)
	if err != nil {
		return 0, "", err
	}
	return deviceID, cwd, nil
}
