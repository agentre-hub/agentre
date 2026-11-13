package wireinbound_test

import (
	"slices"
	"testing"

	"github.com/agentre-hub/agentre/internal/pkg/wireinbound"
	"github.com/agentre-hub/agentre/pkg/wire/agentrewire"
)

func requirementFor(t *testing.T, method agentrewire.RpcMethod) wireinbound.Requirement {
	t.Helper()
	for _, req := range wireinbound.Contract() {
		if req.Method == method {
			return req
		}
	}
	t.Fatalf("contract has no row for %v", method)
	return wireinbound.Requirement{}
}

// Given 未登录的桌面端删除一条跑在已配对 agentred 上的对话 (spec 2026-11-12 决策 7),
// When 它经连接池直接对那台 agentred 发 SESSION_DELETE,
// Then 契约表把「桌面端 → agentred」列为这个方法的调用方,且要求 agentred 答得出。
func TestContract_SessionDeleteAcceptsDesktopToAgentred(t *testing.T) {
	req := requirementFor(t, agentrewire.RpcMethod_RPC_METHOD_SESSION_DELETE)
	if !slices.Contains(req.Callers, wireinbound.CallerDesktopToAgentred) {
		t.Fatalf("SESSION_DELETE callers = %v, want %q among them", req.Callers, wireinbound.CallerDesktopToAgentred)
	}
	if !slices.Contains(req.Hosts, wireinbound.HostAgentred) {
		t.Fatalf("SESSION_DELETE hosts = %v, want agentred among them", req.Hosts)
	}
}

// 「桌面端 → agentred」的对端只可能是 agentred:列了这个调用方的行,Hosts 就必须含
// agentred,否则 daemon 侧的注册面守卫不会替这个调用方把关。
func TestContract_DesktopToAgentredRowsRequireAgentredHost(t *testing.T) {
	for _, req := range wireinbound.Contract() {
		if slices.Contains(req.Callers, wireinbound.CallerDesktopToAgentred) && !slices.Contains(req.Hosts, wireinbound.HostAgentred) {
			t.Errorf("%v lists %q as a caller but not agentred as a host", req.Method, wireinbound.CallerDesktopToAgentred)
		}
	}
}
