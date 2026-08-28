package chat_svc

import (
	"context"
	"time"

	"github.com/cago-frame/cago/pkg/i18n"

	"github.com/agentre-hub/agentre/internal/daemon/protorpc"
	"github.com/agentre-hub/agentre/internal/model/entity/agent_backend_entity"
	"github.com/agentre-hub/agentre/internal/model/entity/chat_entity"
	"github.com/agentre-hub/agentre/internal/pkg/agentruntime/runtimes/remote/protowire"
	"github.com/agentre-hub/agentre/internal/pkg/code"
	"github.com/agentre-hub/agentre/internal/pkg/workspacefs"
	"github.com/agentre-hub/agentre/internal/repository/agent_backend_repo"
	"github.com/agentre-hub/agentre/internal/repository/agent_repo"
	"github.com/agentre-hub/agentre/internal/repository/chat_repo"
	"github.com/agentre-hub/agentre/pkg/wire/agentrewire"
)

// GetSessionGitState 拉某 session 的 git 状态快照。
func (s *chatSvc) GetSessionGitState(ctx context.Context, req *GetSessionGitStateRequest) (*GetSessionGitStateResponse, error) {
	if req == nil || req.SessionID <= 0 {
		return nil, i18n.NewError(ctx, code.InvalidParameter)
	}
	sess, err := chat_repo.Session().Find(ctx, req.SessionID)
	if err != nil {
		return nil, operationFailedWithCause(ctx, err)
	}
	if sess == nil {
		return nil, i18n.NewError(ctx, code.ChatSessionNotFound)
	}
	a, err := agent_repo.Agent().Find(ctx, sess.AgentID)
	if err != nil {
		return nil, operationFailedWithCause(ctx, err)
	}
	var be *agent_backend_entity.AgentBackend
	if a != nil && a.AgentBackendID > 0 {
		be, err = agent_backend_repo.AgentBackend().Find(ctx, a.AgentBackendID)
		if err != nil {
			return nil, operationFailedWithCause(ctx, err)
		}
	}
	return s.getSessionGitStateForSession(ctx, sess, be)
}

// getSessionGitStateForSession 按 backend 走本地 / 远端两条路径:
//   - 本地(含 R13 认领后的本机指纹档):直接调叶子包 workspacefs.GitState 读 cwd。
//   - 远端(claudecode/codex on agentred):经设备连接池调 workspacefs.gitState
//     RPC,daemon 侧调的是同一份叶子包实现(硬不变量 5:本地会话与远端 agentred
//     会话行为一致)。
//
// 两条路径都遵循同一条容错约定:cwd 解析不出 / 设备未配对 / RPC 调用失败(含
// 旧 daemon 报「方法不存在」)一律降级为 notARepo=true 让前端折叠 chip 区,不
// 冒泡 error —— session git chip 是纯展示区域,不应该因为这些情形而挂掉。
func (s *chatSvc) getSessionGitStateForSession(ctx context.Context, sess *chat_entity.Session, be *agent_backend_entity.AgentBackend) (*GetSessionGitStateResponse, error) {
	if beTargetsRemote(be) {
		return s.getSessionGitStateRemote(ctx, sess, be), nil
	}
	cwd, err := resolveSessionCwd(ctx, sess, be)
	if err != nil || cwd == "" {
		// by-design: cwd 解析失败时 UI chip 不应崩,降级为 notARepo 让前端折叠。
		return notARepoResponse(), nil //nolint:nilerr // 见上方注释
	}
	return &GetSessionGitStateResponse{State: viewFromGitState(workspacefs.GitState(ctx, cwd))}, nil
}

// getSessionGitStateRemote 走 workspacefs.gitState RPC 取远端机器上 cwd 的只读
// git 状态快照。租约借不到(未配对/离线)、cwd 解析失败、RPC 调用失败(含旧
// daemon 报「方法不存在」)一律降级为 notARepo,与本机分支的容错约定一致。
func (s *chatSvc) getSessionGitStateRemote(ctx context.Context, sess *chat_entity.Session, be *agent_backend_entity.AgentBackend) *GetSessionGitStateResponse {
	deviceID, ok := localPairedDeviceID(ctx, be.DeviceFingerprint)
	if !ok {
		return notARepoResponse()
	}
	cwd, err := resolveSessionCwd(ctx, sess, be)
	if err != nil || cwd == "" {
		return notARepoResponse()
	}

	lease, err := s.pool().Borrow(ctx, deviceID)
	if err != nil {
		return notARepoResponse()
	}
	defer lease.Release()

	response, err := protorpc.CallMethod(ctx, lease.Client().Conn(), uint32(agentrewire.RpcMethod_RPC_METHOD_WORKSPACE_FS_GIT_STATE), &agentrewire.WorkspaceFsGitStateRequest{Root: cwd}, func() *agentrewire.WorkspaceFsGitStateResponse { return &agentrewire.WorkspaceFsGitStateResponse{} })
	if err != nil {
		return notARepoResponse()
	}
	resp := protowire.WorkspaceGitStateResponseFromProto(response)
	return &GetSessionGitStateResponse{State: ChatSessionGitState{
		Branch: resp.Branch, Worktree: resp.Worktree, Dirty: resp.Dirty,
		Ahead: resp.Ahead, Behind: resp.Behind, HasUpstream: resp.HasUpstream,
		NotARepo: resp.NotARepo, UpdatedAt: time.Now().UnixMilli(),
	}}
}

// viewFromGitState 把叶子包的 GitStateResult 映射成 session git chip 的
// ChatSessionGitState。CommonDir 不在这个视图里——它是下游任务(工作根认领)
// 才消费的字段,session git chip 只关心 branch/worktree/dirty/ahead·behind。
func viewFromGitState(res workspacefs.GitStateResult) ChatSessionGitState {
	return ChatSessionGitState{
		Branch: res.Branch, Worktree: res.Worktree, Dirty: res.Dirty,
		Ahead: res.Ahead, Behind: res.Behind, HasUpstream: res.HasUpstream,
		NotARepo: res.NotARepo, UpdatedAt: time.Now().UnixMilli(),
	}
}

func notARepoResponse() *GetSessionGitStateResponse {
	return &GetSessionGitStateResponse{State: ChatSessionGitState{
		NotARepo:  true,
		UpdatedAt: time.Now().UnixMilli(),
	}}
}
