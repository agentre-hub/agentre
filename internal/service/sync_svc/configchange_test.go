package sync_svc

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// configchange.go 是「本机写入成功后喊一声 config:changed」的注入点（emitter-injection
// 先例见本包的 SetEmitter/announce）。它刻意与 SyncSvc 的登录态、账号键解耦：Wails、
// orgtool、ctl 三条写入路径都在未登录时也要能让本机其它页面刷新（R12 只管「有没有东西
// 上行」，不管「这台机器自己写完了要不要刷自己的界面」）。
func TestNotifyConfigChanged(t *testing.T) {
	t.Cleanup(func() { SetConfigChangeEmitter(nil) })

	t.Run("装配了 emitter 就把变更涉及的资源类型原样带过去", func(t *testing.T) {
		var got [][]string
		SetConfigChangeEmitter(func(kinds []string) {
			got = append(got, kinds)
		})

		NotifyConfigChanged("llm_provider")

		assert.Equal(t, [][]string{{"llm_provider"}}, got)
	})

	t.Run("没装配 emitter 时静默，不 panic", func(t *testing.T) {
		SetConfigChangeEmitter(nil)

		assert.NotPanics(t, func() {
			NotifyConfigChanged("agent")
		})
	})

	t.Run("没有资源类型时不喊", func(t *testing.T) {
		called := false
		SetConfigChangeEmitter(func([]string) { called = true })

		NotifyConfigChanged()

		assert.False(t, called)
	})
}
