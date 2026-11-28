package peer

import (
	"os"
	"testing"

	"github.com/agentre-hub/agentre/internal/service/chat_svc/sessionengine"
	"github.com/agentre-hub/agentre/internal/service/chat_svc/sessionengine/sessionenginetest"
)

// TestMain 装「旧夹具桥」：这里的用例按 Agent 的 AgentBackendID + 那一行后端搭夹具，还没钉住
// 的会话的运行时类型由成员生效的引擎配置回答（sessionengine.ForSession，生产里由 bootstrap
// 接线），桥把它答成那一行后端摊成的配置。
func TestMain(m *testing.M) {
	sessionengine.RegisterEffective(sessionenginetest.LegacyEffective)
	os.Exit(m.Run())
}
