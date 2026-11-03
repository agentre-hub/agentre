package chat_svc

import (
	"context"
	"errors"

	"github.com/cago-frame/cago/pkg/i18n"
	"github.com/cago-frame/cago/pkg/logger"
	"go.uber.org/zap"

	"github.com/agentre-hub/agentre/internal/model/entity/agent_backend_entity"
	"github.com/agentre-hub/agentre/internal/model/entity/chat_entity"
	"github.com/agentre-hub/agentre/internal/pkg/agentruntime"
	"github.com/agentre-hub/agentre/internal/pkg/code"
	"github.com/agentre-hub/agentre/internal/pkg/procscan"
	"github.com/agentre-hub/agentre/internal/repository/chat_repo"
	"github.com/agentre-hub/agentre/internal/service/exec_target_svc"
)

// SessionProcessScanner 是本机会话进程扫描/结束的端口，生产实现是 *procscan.Scanner。
// 签名与 procscan 一致，使其无需适配器即可注入。
type SessionProcessScanner interface {
	List(ctx context.Context, convID string) ([]procscan.Listed, error)
	Kill(ctx context.Context, convID string, pid int, startedAtMs int64) error
}

var defaultSessionProcessScanner SessionProcessScanner

// RegisterSessionProcessScanner 由 bootstrap 注入本机扫描器。
func RegisterSessionProcessScanner(s SessionProcessScanner) {
	defaultSessionProcessScanner = s
}

// RemoteSessionProcessInspectorFunc 取得会话所在设备的远端进程查询端，并交回用完即还的释放函数。
// 出错时 release 仍可安全调用。
type RemoteSessionProcessInspectorFunc func(ctx context.Context, be *agent_backend_entity.AgentBackend) (agentruntime.SessionProcessInspector, func(), error)

var remoteSessionProcessInspector RemoteSessionProcessInspectorFunc

// RegisterRemoteSessionProcessInspector 覆盖远端查询端的取法；传 nil 恢复默认（借设备共享的 remote runtime）。
func RegisterRemoteSessionProcessInspector(f RemoteSessionProcessInspectorFunc) {
	remoteSessionProcessInspector = f
}

// SessionProcessSvc 是「会话进程与端口」这块能力的服务面，由 *chatSvc 实现。
// 单独成接口，不并进 ChatSvc：这是一块自成一体的小能力，绑定层经 SessionProcesses() 取用。
type SessionProcessSvc interface {
	ListSessionProcesses(ctx context.Context, req *ListSessionProcessesRequest) (*ListSessionProcessesResponse, error)
	KillSessionProcess(ctx context.Context, req *KillSessionProcessRequest) (*KillSessionProcessResponse, error)
	// SessionExecDevice 交回会话的执行设备（paired_agentreds.id）；0 = 本机执行。
	// 远程会话还没记录执行设备时报 RemoteDeviceNotFound —— 不能把它当成本机。
	// 绑定层据此决定「打开端口」是直接给本机地址还是经该设备的端口转发。
	SessionExecDevice(ctx context.Context, sessionID int64) (int64, error)
}

// SessionProcesses 返回已注册的 ChatSvc 上的会话进程能力；未注册或实现不带该能力时为 nil。
func SessionProcesses() SessionProcessSvc {
	s, _ := defaultChat.(SessionProcessSvc)
	return s
}

// loadSessionTarget 读会话并判定它跑在本机还是远端（同 StopBackgroundTask 的取法）。
func (s *chatSvc) loadSessionTarget(ctx context.Context, sessionID int64) (*chat_entity.Session, *agent_backend_entity.AgentBackend, bool, error) {
	if sessionID <= 0 {
		return nil, nil, false, i18n.NewError(ctx, code.InvalidParameter)
	}
	sess, err := chat_repo.Session().Find(ctx, sessionID)
	if err != nil {
		return nil, nil, false, err
	}
	if sess == nil {
		return nil, nil, false, i18n.NewError(ctx, code.ChatSessionNotFound)
	}
	_, be, _, err := s.resolveAgentBackend(ctx, sess, sess.AgentID, sess.ProjectID)
	if err != nil {
		return nil, nil, false, err
	}
	return sess, be, exec_target_svc.BackendTargetsRemote(be), nil
}

func (s *chatSvc) SessionExecDevice(ctx context.Context, sessionID int64) (int64, error) {
	sess, _, remote, err := s.loadSessionTarget(ctx, sessionID)
	if err != nil {
		return 0, err
	}
	if !remote {
		return 0, nil
	}
	if !sess.RanOnDaemon() {
		return 0, i18n.NewError(ctx, code.RemoteDeviceNotFound)
	}
	return sess.ExecDeviceID, nil
}

// ListSessionProcesses 列出会话留下的存活进程及其监听端口。任何扫描失败都按「没有可列的」
// 处理（返回空列表）：这一节只是附加信息，失败时让界面不出现它，而不是出错态。
func (s *chatSvc) ListSessionProcesses(ctx context.Context, req *ListSessionProcessesRequest) (*ListSessionProcessesResponse, error) {
	if req == nil {
		return nil, i18n.NewError(ctx, code.InvalidParameter)
	}
	sess, be, remote, err := s.loadSessionTarget(ctx, req.SessionID)
	if err != nil {
		return nil, err
	}
	var views []SessionProcessView
	if remote {
		views = s.listRemoteSessionProcesses(ctx, sess, be)
	} else {
		views = s.listLocalSessionProcesses(ctx, sess)
	}
	return &ListSessionProcessesResponse{Processes: views}, nil
}

func (s *chatSvc) listLocalSessionProcesses(ctx context.Context, sess *chat_entity.Session) []SessionProcessView {
	scanner := defaultSessionProcessScanner
	if scanner == nil {
		return nil
	}
	listed, err := scanner.List(ctx, sess.ConversationID)
	if err != nil {
		if errors.Is(err, procscan.ErrUnsupported) {
			logger.Ctx(ctx).Debug("chat_svc.ListSessionProcesses: platform unsupported",
				zap.Int64("sessionId", sess.ID))
		} else {
			logger.Ctx(ctx).Warn("chat_svc.ListSessionProcesses: scan failed",
				zap.Int64("sessionId", sess.ID), zap.Error(err))
		}
		return nil
	}
	views := make([]SessionProcessView, 0, len(listed))
	for _, p := range listed {
		ports := make([]int, 0, len(p.Ports))
		for _, port := range p.Ports {
			ports = append(ports, int(port))
		}
		views = append(views, SessionProcessView{
			PID: p.PID, Command: p.Command, Ports: ports, StartedAt: p.StartedAtMs, Cwd: p.Cwd,
		})
	}
	return views
}

// holdRemoteInspector 取会话所在设备的远端查询端（默认借设备共享的 remote runtime）。
// 出错时交回的 release 是可安全调用的 no-op。
func (s *chatSvc) holdRemoteInspector(ctx context.Context, be *agent_backend_entity.AgentBackend) (agentruntime.SessionProcessInspector, func(), error) {
	if remoteSessionProcessInspector != nil {
		return remoteSessionProcessInspector(ctx, be)
	}
	rt, release, err := s.holdRemoteRuntime(ctx, be)
	if err != nil {
		return nil, release, err
	}
	return rt, release, nil
}

// listRemoteSessionProcesses 经执行设备列出远程会话的进程。设备离线、RPC 失败、不支持都按「没有可列的」
// 处理（记日志、返回空），与本机扫描失败同口径。
func (s *chatSvc) listRemoteSessionProcesses(ctx context.Context, sess *chat_entity.Session, be *agent_backend_entity.AgentBackend) []SessionProcessView {
	insp, release, err := s.holdRemoteInspector(ctx, be)
	defer release()
	if err != nil {
		logger.Ctx(ctx).Warn("chat_svc.ListSessionProcesses: remote runtime unavailable",
			zap.Int64("sessionId", sess.ID), zap.Error(err))
		return nil
	}
	listed, err := insp.ListSessionProcesses(ctx, sess.ConversationID)
	if err != nil {
		logger.Ctx(ctx).Warn("chat_svc.ListSessionProcesses: remote list failed",
			zap.Int64("sessionId", sess.ID), zap.Error(err))
		return nil
	}
	views := make([]SessionProcessView, 0, len(listed))
	for _, p := range listed {
		views = append(views, SessionProcessView{
			PID: p.PID, Command: p.Command, Ports: p.Ports, StartedAt: p.StartedAtMs, Cwd: p.Cwd,
		})
	}
	return views
}

// KillSessionProcess 结束会话留下的一个进程。进程身份核对（属于本会话、未被复用）由扫描器负责。
func (s *chatSvc) KillSessionProcess(ctx context.Context, req *KillSessionProcessRequest) (*KillSessionProcessResponse, error) {
	if req == nil || req.PID <= 0 {
		return nil, i18n.NewError(ctx, code.InvalidParameter)
	}
	sess, be, remote, err := s.loadSessionTarget(ctx, req.SessionID)
	if err != nil {
		return nil, err
	}
	var killErr error
	if remote {
		killErr = s.killRemoteSessionProcess(ctx, sess, be, req)
	} else {
		killErr = s.killLocalSessionProcess(ctx, sess, req)
	}
	if killErr != nil {
		if errors.Is(killErr, procscan.ErrNotSessionProcess) || errors.Is(killErr, agentruntime.ErrSessionProcessNotOwned) {
			return nil, i18n.NewError(ctx, code.SessionProcessNotOwned)
		}
		return nil, operationFailedWithCause(ctx, killErr,
			zap.Int64("sessionId", req.SessionID), zap.Int("pid", req.PID))
	}
	logger.Ctx(ctx).Info("chat_svc.KillSessionProcess: killed",
		zap.Int64("sessionId", req.SessionID), zap.Int("pid", req.PID))
	return &KillSessionProcessResponse{}, nil
}

func (s *chatSvc) killLocalSessionProcess(ctx context.Context, sess *chat_entity.Session, req *KillSessionProcessRequest) error {
	if defaultSessionProcessScanner == nil {
		return procscan.ErrUnsupported
	}
	return defaultSessionProcessScanner.Kill(ctx, sess.ConversationID, req.PID, req.StartedAt)
}

// killRemoteSessionProcess 经执行设备结束远程会话的一个进程；身份核对由 agentred 负责。
func (s *chatSvc) killRemoteSessionProcess(ctx context.Context, sess *chat_entity.Session, be *agent_backend_entity.AgentBackend, req *KillSessionProcessRequest) error {
	insp, release, err := s.holdRemoteInspector(ctx, be)
	defer release()
	if err != nil {
		return err
	}
	return insp.KillSessionProcess(ctx, sess.ConversationID, req.PID, req.StartedAt)
}
