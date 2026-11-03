package remote

import (
	"context"

	"github.com/agentre-hub/agentre/internal/pkg/agentruntime"
	"github.com/agentre-hub/agentre/pkg/wire/agentrewire"
	"github.com/agentre-hub/agentre/pkg/wire/wirecall"
)

// SessionProcessClient 是会话进程的列出 / 结束这两个**无状态** RPC 的调用面:只要一条已鉴权连接,
// 不需要也不创建 *Runtime。
//
// 为什么不挂在 *Runtime 上:Runtime 是一条连接上通知 handler 的属主,面板每 5 秒查一次,如果
// 为此借(或新建)设备的 runtime,恰在断连重连的窗口里就会给新连接造出第二个 runtime;旧
// runtime 重连 adoptConn 时再注册同一批 handler,protorpc 当场 panic「duplicate method ID」。
//
// peer_fingerprint 留空:agentred 据此按「调用这条连接的对端」认会话归属 —— 面板只列本机
// 自己的会话。
type SessionProcessClient struct {
	caller wirecall.Caller
}

// NewSessionProcessClient 用一条已鉴权连接(通常是池里借来的 lease.Client())构造调用面。
func NewSessionProcessClient(caller wirecall.Caller) *SessionProcessClient {
	return &SessionProcessClient{caller: caller}
}

var _ agentruntime.SessionProcessInspector = (*SessionProcessClient)(nil)

// ListSessionProcesses 问执行端这条会话留下的存活进程及其监听端口。
func (c *SessionProcessClient) ListSessionProcesses(ctx context.Context, conversationID string) ([]agentruntime.SessionProcess, error) {
	resp, err := wirecall.RuntimeListSessionProcesses(ctx, c.caller, &agentrewire.RuntimeListSessionProcessesRequest{
		ConversationId: conversationID,
	})
	if err != nil {
		return nil, fromProtobufError(err)
	}
	out := make([]agentruntime.SessionProcess, 0, len(resp.GetProcesses()))
	for _, p := range resp.GetProcesses() {
		var ports []int
		for _, port := range p.GetPorts() {
			ports = append(ports, int(port))
		}
		out = append(out, agentruntime.SessionProcess{
			PID: int(p.GetPid()), Command: p.GetCommand(), Ports: ports,
			StartedAtMs: p.GetStartedAtMs(), Cwd: p.GetCwd(),
		})
	}
	return out, nil
}

// KillSessionProcess 让执行端结束这条会话的一个进程;进程已不属于该会话时落成
// agentruntime.ErrSessionProcessNotOwned。
func (c *SessionProcessClient) KillSessionProcess(ctx context.Context, conversationID string, pid int, startedAtMs int64) error {
	_, err := wirecall.RuntimeKillSessionProcess(ctx, c.caller, &agentrewire.RuntimeKillSessionProcessRequest{
		ConversationId: conversationID,
		Pid:            uint32(pid), // #nosec G115 -- pid 来自执行端回报的 uint32,往返不溢出。
		StartedAtMs:    startedAtMs,
	})
	if err != nil {
		return fromProtobufError(err)
	}
	return nil
}
