package devicefacts

import (
	"sync"
	"time"

	"github.com/agentre-hub/agentre/internal/model/entity/agent_backend_entity"
	"github.com/agentre-hub/agentre/pkg/syncwire"
)

// connection 是一次连接的结果。
type connection struct {
	ok bool
	at int64
}

// ConnectionLog 记这台设备最近一次连每个服务型端点(Hermes serve、OpenClaw 网关)的
// 结果与时刻,供设备情况报「上次连接是否成功」。只在内存里:重启后一律回到 never。
// 按规范化后的端点地址记 —— 两份指向同一个端点的配置共用一个结果,与凭据按地址存放
// (Hermes)同一口径。只记结果与时刻,不记错误原文:那里可能带着端点回显的凭据。
type ConnectionLog struct {
	now func() time.Time

	mu   sync.Mutex
	last map[string]connection
}

// NewConnectionLog 构造一份连接记录;now 为 nil 用 time.Now。
func NewConnectionLog(now func() time.Time) *ConnectionLog {
	if now == nil {
		now = time.Now
	}
	return &ConnectionLog{now: now, last: map[string]connection{}}
}

var defaultConnections = NewConnectionLog(nil)

// DefaultConnections 是这个进程的连接记录:连接测试在这里记,设备情况从这里读。
func DefaultConnections() *ConnectionLog { return defaultConnections }

// Record 记一次真正发出去的连接尝试(hermes / openclaw)。地址不合法时不记 —— 那不是
// 一次连接。
func (l *ConnectionLog) Record(runtime, endpoint string, ok bool) {
	key := connectionKey(runtime, endpoint)
	if key == "" {
		return
	}
	l.mu.Lock()
	l.last[key] = connection{ok: ok, at: l.now().UnixMilli()}
	l.mu.Unlock()
}

// Last 一份引擎配置的端点最近一次连接的结果(syncwire.Connection*)与时刻。
func (l *ConnectionLog) Last(e Engine) (status string, at int64) {
	endpoint := e.HermesURL
	if e.Runtime == runtimeOpenClaw {
		endpoint = e.OpenClawGatewayURL
	}
	key := connectionKey(e.Runtime, endpoint)
	l.mu.Lock()
	c, ok := l.last[key]
	l.mu.Unlock()
	switch {
	case key == "" || !ok:
		return syncwire.ConnectionNever, 0
	case c.ok:
		return syncwire.ConnectionSucceeded, c.at
	default:
		return syncwire.ConnectionFailed, c.at
	}
}

func connectionKey(runtime, endpoint string) string {
	var (
		normalized string
		err        error
	)
	switch runtime {
	case runtimeHermes:
		normalized, err = agent_backend_entity.NormalizeHermesURL(endpoint)
	case runtimeOpenClaw:
		normalized, err = agent_backend_entity.NormalizeOpenClawGatewayURL(endpoint)
	default:
		return ""
	}
	if err != nil {
		return ""
	}
	return runtime + "\x00" + normalized
}
