package daemon

import (
	"testing"

	"github.com/agentre-hub/agentre/internal/pkg/wireinbound"
	"github.com/agentre-hub/agentre/pkg/wire/protorpc"
)

// agentred 这一侧的注册面必须覆盖它作为被调方的义务。与桌面端那条(internal/peer 的
// contract_guard_test.go)是同一张表的两半。
//
// **要取两级的并集。** agentred 把方法挂在两处:目录/补齐族在 daemon 级注册面上,
// 运行时族在**每条连接**的注册面上(bindProtobufConn)。只问其中一处会把另一半误判
// 成缺口 —— 而这正是本轮重构之所以成立的那个性质:端口缺席就不注册,所以同一个
// RegisterSessionMethods 在两处各挂各的。
func TestContract_AgentredRegistersWhatCallersSend(t *testing.T) {
	t.Parallel()

	daemon, err := New(Options{DataDir: t.TempDir()})
	if err != nil {
		t.Fatalf("造 daemon: %v", err)
	}
	t.Cleanup(func() { closeDB(daemon.db) })

	registry := daemon.protobufRegistry.Clone()
	conn := protorpc.NewConn(nil, registry)
	daemon.bindProtobufConn(conn)

	result := wireinbound.CheckContract(wireinbound.HostAgentred, registry.RegisteredMethods())

	for _, missing := range result.Missing {
		t.Errorf("agentred 没注册 %s,而 %v 会发它(判据:%s)。\n"+
			"要么在 agentred 实现它,要么收窄调用侧的目标筛选;若确认暂时不修,"+
			"去 wireinbound.KnownGaps() 里记一行并写清欠着什么。",
			missing.Method, missing.Callers, missing.Evidence)
	}
	for _, stale := range result.Stale {
		t.Errorf("KnownGaps 里 %s / %s 这一行该删了:这一侧已经实现了它,或者它本就不是这一侧的义务。\n"+
			"留着一条不成立的豁免,下一次它会掩护一个真缺口。原记录理由:%s",
			stale.Host, stale.Method, stale.Reason)
	}
}
