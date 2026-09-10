package agentruntime_test

import (
	"testing"

	"github.com/agentre-hub/agentre/internal/model/entity/agent_backend_entity"
	"github.com/agentre-hub/agentre/internal/pkg/agentruntime"
	"github.com/agentre-hub/agentre/internal/pkg/agentruntime/capability"

	// 触发 NEW runtime 子包 init() 注册到 RuntimeFor 表
	_ "github.com/agentre-hub/agentre/internal/pkg/agentruntime/runtimes/builtin"
	_ "github.com/agentre-hub/agentre/internal/pkg/agentruntime/runtimes/claudecode"
	_ "github.com/agentre-hub/agentre/internal/pkg/agentruntime/runtimes/codex"
	_ "github.com/agentre-hub/agentre/internal/pkg/agentruntime/runtimes/piagent"
)

// TestAllRegisteredRunnersImplementSteerer 守护契约:每个 backend type 注册的
// runner 都必须实现 Steerer。chat_svc.Enqueue 走 type-assertion,缺一个就会
// 让该后端整个回退到 ChatSteerUnsupported。
func TestAllRegisteredRunnersImplementSteerer(t *testing.T) {
	for _, bt := range []agent_backend_entity.BackendType{
		agent_backend_entity.TypeBuiltin,
		agent_backend_entity.TypeClaudeCode,
		agent_backend_entity.TypeCodex,
		agent_backend_entity.TypePiAgent,
	} {
		r := agentruntime.RuntimeFor(bt)
		if r == nil {
			t.Errorf("backend %q has no registered runner", bt)
			continue
		}
		if _, ok := r.(agentruntime.Steerer); !ok {
			t.Errorf("backend %q runner does not implement Steerer", bt)
		}
	}
}

// TestApprovalCapableRunnersImplementWaiterLister 守护 R7:凡是自称支持工具
// 审批协议(CapToolPermission)的 backend,都必须能枚举自己当前阻塞中的
// waiter。daemon 的待决策查询走 type-assertion,缺一个就让该后端的远端会话在
// 断连重连后拿到空列表 —— 而 R9 不给 waiter 设任何过期时限,用户永远没有机会
// 回答,会话就此永久挂起。
//
// 不声明该能力的 backend(builtin / piagent)本来就没有审批协议,按 R7 回落
// 空快照即可,不在守护范围内。
func TestApprovalCapableRunnersImplementWaiterLister(t *testing.T) {
	for _, bt := range []agent_backend_entity.BackendType{
		agent_backend_entity.TypeBuiltin,
		agent_backend_entity.TypeClaudeCode,
		agent_backend_entity.TypeCodex,
		agent_backend_entity.TypePiAgent,
	} {
		r := agentruntime.RuntimeFor(bt)
		if r == nil {
			t.Errorf("backend %q has no registered runner", bt)
			continue
		}
		if !r.Capabilities().Has(capability.CapToolPermission) {
			continue
		}
		if _, ok := r.(agentruntime.WaiterLister); !ok {
			t.Errorf("backend %q declares CapToolPermission but its runner does not implement WaiterLister", bt)
		}
	}
}
