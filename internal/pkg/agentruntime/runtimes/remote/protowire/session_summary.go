package protowire

import (
	"github.com/agentre-hub/agentre/internal/pkg/agentruntime/runtimes/remote/wire"
	"github.com/agentre-hub/agentre/pkg/wire/agentrewire"
)

// 会话摘要的编解码只许有这一处。
//
// 它此前被手抄了四份:桌面端出站应答、agentred 出站应答、桌面端解对端清单、桌面端解
// agentred 清单。四份逐字相同,而 proto3 分辨不了「没发这一格」与「发了零值」—— 加第
// 17 个字段时漏掉其中任何一处,编译器不会报错,往返测试也不会红:发的是零值、解的也是
// 零值,只是标题变空、最后活动时间变 0、思考力度悄悄退回「跟随后端配置」。这类缺陷只
// 会在用户的清单里露面。TS 侧同一件事本来就只有一份(agentre-wire 的
// sessionListFromProtobuf),Go 侧收进这里以后也是一份。

// SessionSummaryToProto 把一条会话摘要编成线格式。
func SessionSummaryToProto(value wire.SessionSummary) *agentrewire.SessionSummary {
	return &agentrewire.SessionSummary{
		ConversationId:    value.ConversationID,
		PeerFingerprint:   value.PeerFingerprint,
		AgentId:           value.AgentID,
		Title:             value.Title,
		AgentSyncId:       value.AgentSyncID,
		ProviderSessionId: value.ProviderSessionID,
		Cwd:               value.Cwd,
		ProjectSyncId:     value.ProjectSyncID,
		BackendType:       value.BackendType,
		LifecycleState:    value.LifecycleState,
		WaitingForInput:   value.WaitingForInput,
		LatestSeq:         value.LatestSeq,
		LastMessageAt:     value.LastMessageAt,
		ProviderKey:       value.ProviderKey,
		ModelKey:          value.ModelKey,
		ReasoningEffort:   value.ReasoningEffort,
	}
}

// SessionSummaryFromProto 把线格式的一条会话摘要解回来。全程走 getter,对端发来的空
// 指针解成零值摘要而不是 panic —— 清单里的一条坏行不该打断整条补齐链路。
func SessionSummaryFromProto(value *agentrewire.SessionSummary) wire.SessionSummary {
	return wire.SessionSummary{
		ConversationID:    value.GetConversationId(),
		PeerFingerprint:   value.GetPeerFingerprint(),
		AgentID:           value.GetAgentId(),
		Title:             value.GetTitle(),
		AgentSyncID:       value.GetAgentSyncId(),
		ProviderSessionID: value.GetProviderSessionId(),
		Cwd:               value.GetCwd(),
		ProjectSyncID:     value.GetProjectSyncId(),
		BackendType:       value.GetBackendType(),
		LifecycleState:    value.GetLifecycleState(),
		WaitingForInput:   value.GetWaitingForInput(),
		LatestSeq:         value.GetLatestSeq(),
		LastMessageAt:     value.GetLastMessageAt(),
		ProviderKey:       value.GetProviderKey(),
		ModelKey:          value.GetModelKey(),
		ReasoningEffort:   value.GetReasoningEffort(),
	}
}
