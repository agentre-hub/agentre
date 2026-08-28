package chat_svc

import (
	"sync"
	"sync/atomic"
	"time"

	"github.com/cago-frame/agents/provider"

	"github.com/agentre-hub/agentre/internal/model/entity/llm_provider_entity"
	"github.com/agentre-hub/agentre/internal/pkg/agentruntime/runtimes/builtin"
	"github.com/agentre-hub/agentre/internal/service/remote_device_svc"
)

// SetProviderBuilderForTest 让现有测试通过包级 hook 注入 fake provider。
// 实际机制在 builtin runtime 子包 — builtin runner 用它来构造 cago provider.Provider,
// 避免单测打真 LLM 网络。
func SetProviderBuilderForTest(b func(*llm_provider_entity.LLMProvider) (provider.Provider, error)) {
	builtin.SetBuiltinProviderBuilderForTest(b)
}

func ResetProviderBuilderForTest() {
	builtin.ResetBuiltinProviderBuilderForTest()
}

var streamRunning sync.Map

func (s *chatSvc) markStreamRunningForTest(id int64) {
	flag := &atomic.Bool{}
	flag.Store(true)
	streamRunning.Store(id, flag)
}

func (s *chatSvc) markStreamDoneForTest(id int64) {
	if v, ok := streamRunning.Load(id); ok {
		v.(*atomic.Bool).Store(false)
	}
}

func WaitForStreamForTest(_ ChatSvc, id int64) {
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		v, ok := streamRunning.Load(id)
		if !ok || !v.(*atomic.Bool).Load() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// setConnPoolForTest 替换 chatSvc 用的 ConnPool(测试注入)。nil 恢复生产
// (remote_device_svc.Default().Pool())。
func (s *chatSvc) setConnPoolForTest(p remote_device_svc.ConnPool) {
	s.testHookPool = p
}

// SetConnPoolForTest 是 setConnPoolForTest 的外部包测试入口 —— chat_test.go 用
// 它给整个 chatSvc 注入失败/可控的 ConnPool 来打远端 dial 错误路径。
func SetConnPoolForTest(svc ChatSvc, p remote_device_svc.ConnPool) {
	if c, ok := svc.(*chatSvc); ok {
		c.setConnPoolForTest(p)
	}
}
