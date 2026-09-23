package sync_svc

import "sync"

// ConfigChangeEmitter pushes「本机刚成功写完这几类资源」给上层（生产是 Wails
// EventsEmit，单测是 spy）。与 Emitter（sync:applied）的区别是：这条通知**不**依赖
// 登录态或同步引擎——Wails、orgtool、ctl 三条写入路径直接从五个域服务里调用它，
// 未登录（R12 的「什么都没有」）时它照样要响，因为它刷的是本机自己的界面，不是在
// 汇报「上行了什么」（docs/specs/2026-09-22-agrctl-resource-management.md「Real-time
// refresh」、design decision 11）。
type ConfigChangeEmitter func(kinds []string)

// ConfigChangedEvent 是上面那条通知在 Wails 事件总线上的名字。
const ConfigChangedEvent = "config:changed"

var (
	configChangeMu   sync.Mutex
	configChangeEmit ConfigChangeEmitter
)

// SetConfigChangeEmitter 由 App.Startup 在 wails ctx 就绪后绑定（与 SetEmitter 同一个
// 套路）。装配之前的写入没有听众，静默是对的（单机构建 / 单元测试）。
func SetConfigChangeEmitter(emit ConfigChangeEmitter) {
	configChangeMu.Lock()
	defer configChangeMu.Unlock()
	configChangeEmit = emit
}

// NotifyConfigChanged 告诉前端「刚才这次写入动了哪几类资源」。只在写入**已经落库
// 成功**之后调用——失败或回滚的路径不许调它，前端据此原样刷新，不会因为一次失败的
// 写入白拉一遍列表。没有资源类型或没有装配 emitter 时都是空操作。
func NotifyConfigChanged(kinds ...string) {
	if len(kinds) == 0 {
		return
	}
	configChangeMu.Lock()
	emit := configChangeEmit
	configChangeMu.Unlock()
	if emit == nil {
		return
	}
	emit(kinds)
}
