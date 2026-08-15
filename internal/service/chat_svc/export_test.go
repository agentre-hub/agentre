package chat_svc

import (
	"context"

	"github.com/agentre-ai/agentre/internal/model/entity/agent_backend_entity"
	"github.com/agentre-ai/agentre/internal/model/entity/chat_entity"
	"github.com/agentre-ai/agentre/internal/pkg/agentruntime"
)

// MessageTextExport 暴露 messageText 给外部测试包验证纯文本拼接逻辑。
func MessageTextExport(m *chat_entity.Message) (string, error) {
	return messageText(m)
}

// ToChatMessageForTest 暴露 toChatMessage 给外部测试包:用**真实的转录读路径**
// (内部走 peerMessageSourceOf,只认落库 block data 里的来源)把一条已落库的消息行
// 投影成前端 DTO。断言「刷新 / 重开会话之后来源还在」必须走这条路 —— 在 BlocksJSON
// 里找子串既证明不了读得回来,也会把实时事件里的临时覆盖误判为已落库。
func ToChatMessageForTest(m *chat_entity.Message) (ChatMessage, error) {
	return toChatMessage(m)
}

// DriveAutonomousTurnForTest 暴露 driveAutonomousTurn 给外部测试包,直接驱动一轮
// 自主续轮(不经 watcher goroutine,便于同步断言落库 + stream)。
func DriveAutonomousTurnForTest(ctx context.Context, svc ChatSvc, sessionID int64, be *agent_backend_entity.AgentBackend, at agentruntime.AutonomousTurn) {
	svc.(*chatSvc).driveAutonomousTurn(ctx, sessionID, be, at)
}

// StartAutonomousWatcherForTest 暴露 startAutonomousWatcher 给外部测试包,验证
// watcher goroutine 起停 + 逐轮驱动。
func StartAutonomousWatcherForTest(svc ChatSvc, sessionID int64, be *agent_backend_entity.AgentBackend, src agentruntime.AutonomousTurnSource) {
	svc.(*chatSvc).startAutonomousWatcher(sessionID, be, src)
}

// IsAutonomousWatcherActiveForTest 报告某 session 是否还有活跃 watcher(去重位是否
// 占着)。watcher 在底层 AutonomousTurns channel close 后退出并清位 → 返 false。
func IsAutonomousWatcherActiveForTest(svc ChatSvc, sessionID int64) bool {
	_, ok := svc.(*chatSvc).autoWatchers.Load(sessionID)
	return ok
}

// DriveSubagentActivityForTest 暴露 driveSubagentActivity 给外部测试包,直接驱动一轮
// 后台 subagent 内部活动流(不经 watcher goroutine,便于同步断言落库 + stream)。
func DriveSubagentActivityForTest(ctx context.Context, svc ChatSvc, sessionID int64, be *agent_backend_entity.AgentBackend, act agentruntime.SubagentActivity) {
	svc.(*chatSvc).driveSubagentActivity(ctx, sessionID, be, act)
}

// StartSubagentActivityWatcherForTest 暴露 startSubagentActivityWatcher 给外部测试包。
func StartSubagentActivityWatcherForTest(svc ChatSvc, sessionID int64, be *agent_backend_entity.AgentBackend, src agentruntime.SubagentActivitySource) {
	svc.(*chatSvc).startSubagentActivityWatcher(sessionID, be, src)
}

// IsSubagentActivityWatcherActiveForTest 报告某 session 是否还有活跃 subagent-activity
// watcher(去重位是否占着)。channel close 后退出并清位 → 返 false。
func IsSubagentActivityWatcherActiveForTest(svc ChatSvc, sessionID int64) bool {
	_, ok := svc.(*chatSvc).subagentActivityWatchers.Load(sessionID)
	return ok
}

// ConvertOldEventToNewForTest 暴露 convertOldEventToNew 给 chat_svc_test 包的
// fake runner。生产路径 (runTurn drain) 直接吃 NEW Event channel,本函数仅给
// 老 fixture 当桥接(用 RuntimeEvent{Kind: ...} 字面量驱动,内部转 NEW Event
// 再回吐给 chat_svc dispatcher)。fixture 全部改写成直接构造 NEW Event 后即可删除。
func ConvertOldEventToNewForTest(ev agentruntime.RuntimeEvent) agentruntime.Event {
	return convertOldEventToNew(ev)
}
