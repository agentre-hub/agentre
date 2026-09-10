package exec_target_svc

import (
	"context"

	"go.uber.org/zap"

	"github.com/agentre-hub/agentre/internal/model/entity/agent_entity"
	"github.com/agentre-hub/agentre/internal/pkg/httpgateway"
	"github.com/agentre-hub/agentre/internal/pkg/svcerr"
)

// ExecTargetSvc 是执行目标域的对外口。四个读方法各自回答一个问题：
// 派到哪一档、每一档现在能不能用、指定的那一档合不合法、这条会话在哪台机器的哪个目录。
type ExecTargetSvc interface {
	// PickExecTarget 按 R15 顺序为一个 Agent 挑第一个可用的执行目标档：本机没配对 /
	// 已配对但离线 / 会话绑的项目在那台机器上没配路径 / 既有 BlockReason 四类各自跳过。
	// projectID <= 0（自由会话）不做「该机器上有没有配这个项目的路径」这一项判定。
	// 列表为空 → ChatAgentNoBackend；全部不可用 → *ExecTargetNoneAvailableError（逐档
	// 原因，Wails 只透 Error() 字符串，因此原因也编进了那条字符串里）。
	// 不做会话粘性 —— 挑到之后钉不钉在这一档由调用方决定（R15b，块 4）。
	PickExecTarget(ctx context.Context, agentID int64, projectID int64) (*ExecTargetChoice, error)
	// ListExecTargetAvailability 逐档判定一个 Agent 的执行目标列表可用性（R15，任务
	// 12 的组织架构页用）。与 PickExecTarget 的关键差异是不提前返回——每一档都要给出
	// 结果，供界面同时展示。
	ListExecTargetAvailability(ctx context.Context, agentID int64, projectID int64) ([]ExecTargetAvailabilityView, error)
	// ValidateExecTargetOverride 校验 R15a 手动指定的执行目标此刻是否合法可用。
	ValidateExecTargetOverride(ctx context.Context, agentID, projectID, agentBackendID int64) error
	// ResolveLocalCommandScope 为已有 session 或未持久化的 agent/project 目标解析历史作用域。
	ResolveLocalCommandScope(ctx context.Context, req *ResolveLocalCommandScopeRequest) (*LocalCommandScope, error)
	// ResolveSessionWorkspace 把 sessionID 解析成 {deviceID, cwd}(deviceID 为 0
	// 即本机会话)。实现 workspace_fs_svc 的 SessionWorkspaceResolver 窄接口,
	// 由 bootstrap 注入 —— 让那个服务不必跨域读 chat / agent / agent_backend 表。
	ResolveSessionWorkspace(ctx context.Context, sessionID int64) (deviceID int64, cwd string, err error)
	// ResolvedExecTargets 取一个 Agent 的执行目标并按 R14 解析出本端顺序。
	ResolvedExecTargets(ctx context.Context, agentID int64) ([]*agent_entity.AgentExecTarget, error)
}

var defaultExecTarget ExecTargetSvc

var defaultGateway httpgateway.TokenRouter

// ExecTarget 返回进程单例；bootstrap 之外的调用方（内含 chat_svc）也可以用
// NewExecTarget 自带一个网关句柄构造一份，本服务除网关外没有任何可变状态。
func ExecTarget() ExecTargetSvc { return defaultExecTarget }

// RegisterExecTarget 登记进程单例。与 chat_svc.RegisterChat 同一手法：注册时机可能
// 早于 RegisterGateway（bootstrap 先接线服务、后起网关），也可能晚于它，因此这里
// 补上已经注入过的网关，两种顺序都不丢。
func RegisterExecTarget(impl ExecTargetSvc) {
	if s, ok := impl.(*execTargetSvc); ok && s.gateway == nil {
		s.gateway = defaultGateway
	}
	defaultExecTarget = impl
}

// NewExecTarget 造一份带指定网关的实例。gateway 只影响本地 CLI 后端的
// BlockReasonGatewayNotRunning 判定，nil = 网关未接线（判为未运行）。
func NewExecTarget(gateway httpgateway.TokenRouter) ExecTargetSvc {
	return &execTargetSvc{gateway: gateway}
}

// RegisterGateway 由 bootstrap 注入 httpgateway 单例，与 chat_svc.RegisterGateway 同一时机。
func RegisterGateway(g httpgateway.TokenRouter) {
	defaultGateway = g
	if s, ok := defaultExecTarget.(*execTargetSvc); ok {
		s.gateway = g
	}
}

type execTargetSvc struct {
	gateway httpgateway.TokenRouter
}

// execTargetErrors 是本包的错误报告口。CallerSkip=1 抵消下面那层薄 wrapper,
// 让日志的 caller 字段仍然指向真正的业务调用点。
var execTargetErrors = svcerr.Reporter{LogMessage: "exec_target_svc: operation failed", CallerSkip: 1}

func operationFailedWithCause(ctx context.Context, cause error, fields ...zap.Field) error {
	return execTargetErrors.OperationFailedWithCause(ctx, cause, fields...)
}
