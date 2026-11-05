package remote_device_svc

import "time"

// IdleTimer 把空闲计时器的窄接口导出给外部测试包的手动时钟。
type IdleTimer = idleTimer

// WithIdleAfterFunc 让外部测试包注入空闲计时器的来源。
func WithIdleAfterFunc(fn func(time.Duration, func()) IdleTimer) Option {
	return withIdleAfterFunc(fn)
}
