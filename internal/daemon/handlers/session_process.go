package handlers

import (
	"context"
	"errors"
	"fmt"

	"github.com/cago-frame/cago/pkg/logger"
	"go.uber.org/zap"

	"github.com/agentre-hub/agentre/internal/pkg/procscan"
	"github.com/agentre-hub/agentre/pkg/wire/agentrewire"
	"github.com/agentre-hub/agentre/pkg/wire/devicefp"
	"github.com/agentre-hub/agentre/pkg/wire/rpcerror"
)

// SessionProcessScanner 是 procscan.Scanner 在本层用到的那两个方法:按会话标记列出
// 进程、结束前重新核对身份再结束。
type SessionProcessScanner interface {
	List(ctx context.Context, convID string) ([]procscan.Listed, error)
	Kill(ctx context.Context, convID string, pid int, startedAtMs int64) error
}

// SessionProcessSessionPort 只用来回答「这条会话是不是调用方的」。
type SessionProcessSessionPort interface {
	Find(ctx context.Context, peerFingerprint devicefp.Initiator, peerSessionID string) (*SessionRecord, error)
}

// SessionProcessDeps 是会话进程 handler 的依赖。
type SessionProcessDeps struct {
	Scanner           SessionProcessScanner
	Sessions          SessionProcessSessionPort
	LoggedInAccountID func() string
}

// SessionProcessHandlers 处理 runtime.listSessionProcesses / runtime.killSessionProcess。
//
// 它刻意不经 RuntimeHandlers.resolveSessionCapability:那条路要求会话此刻挂着 CLI,
// 而这一族最常见的场景恰恰是 CLI 已被空闲清扫回收、它起的 dev server 还在跑。对端
// 隔离因此不能借 h.sessions(只认本连接接管过的会话)那道闸,改由这里按对端收窄查
// 会话行 —— 与补齐族 findSession 同一个判据:只认调用方自己的、或已登录账号点名的
// 那个对端的会话。
type SessionProcessHandlers struct{ deps SessionProcessDeps }

func NewSessionProcessHandlers(deps SessionProcessDeps) *SessionProcessHandlers {
	return &SessionProcessHandlers{deps: deps}
}

// List 列出这条会话留下的进程。扫描失败原样上抛(注册处折成内部错误),调用方据此
// 什么都不画,而不是画一个假的空清单。
func (h *SessionProcessHandlers) List(ctx context.Context, req *agentrewire.RuntimeListSessionProcessesRequest) (*agentrewire.RuntimeListSessionProcessesResponse, error) {
	if err := h.requireOwned(ctx, req.GetConversationId(), devicefp.Initiator(req.GetPeerFingerprint())); err != nil {
		return nil, err
	}
	listed, err := h.deps.Scanner.List(ctx, req.GetConversationId())
	if err != nil {
		return nil, fmt.Errorf("scan session processes: %w", err)
	}
	out := make([]*agentrewire.SessionProcess, 0, len(listed))
	for _, p := range listed {
		ports := make([]uint32, 0, len(p.Ports))
		for _, port := range p.Ports {
			ports = append(ports, uint32(port))
		}
		out = append(out, &agentrewire.SessionProcess{
			Pid:         uint32(p.PID),
			Command:     p.Command,
			Ports:       ports,
			StartedAtMs: p.StartedAtMs,
			Cwd:         p.Cwd,
		})
	}
	return &agentrewire.RuntimeListSessionProcessesResponse{Processes: out}, nil
}

// Kill 结束这条会话的某一个进程。身份核对(pid 仍在、标记仍是本会话、启动时刻一致)
// 在 procscan.Kill 里,不符时答 CodeSessionProcessNotOwned 且不发信号。
func (h *SessionProcessHandlers) Kill(ctx context.Context, req *agentrewire.RuntimeKillSessionProcessRequest) (*agentrewire.Empty, error) {
	if req.GetPid() == 0 {
		return nil, &rpcerror.Error{Code: rpcerror.CodeInvalidParams, Message: "pid required"}
	}
	conversationID := req.GetConversationId()
	if err := h.requireOwned(ctx, conversationID, devicefp.Initiator(req.GetPeerFingerprint())); err != nil {
		return nil, err
	}
	pid := int(req.GetPid())
	if err := h.deps.Scanner.Kill(ctx, conversationID, pid, req.GetStartedAtMs()); err != nil {
		if errors.Is(err, procscan.ErrNotSessionProcess) {
			return nil, &rpcerror.Error{Code: rpcerror.CodeSessionProcessNotOwned, Message: err.Error()}
		}
		return nil, fmt.Errorf("kill session process: %w", err)
	}
	logger.Ctx(ctx).Info("handlers.SessionProcessHandlers.Kill: session process terminated",
		zap.String("conversationId", conversationID), zap.Int("pid", pid))
	return &agentrewire.Empty{}, nil
}

// requireOwned 校验会话身份并确认这条会话属于调用方(或已登录账号点名的那个对端)。
// 不属于、或根本不存在,都答 ErrSessionNotFound —— 不让这一族成为「那台机器上有没有
// 这条会话」的探测器。
func (h *SessionProcessHandlers) requireOwned(ctx context.Context, conversationID string, originPeer devicefp.Initiator) error {
	if err := ErrInvalidConversationID(conversationID); err != nil {
		return err
	}
	peer, err := ResolveSessionPeer(ctx, originPeer, h.deps.LoggedInAccountID)
	if err != nil {
		return err
	}
	row, err := h.deps.Sessions.Find(ctx, peer, conversationID)
	if err != nil {
		return fmt.Errorf("find session: %w", err)
	}
	if row == nil {
		return rpcerror.ErrSessionNotFound
	}
	return nil
}
