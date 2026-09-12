package protowire

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"

	"github.com/agentre-hub/agentre/internal/pkg/agentruntime"
	"github.com/agentre-hub/agentre/internal/pkg/agentruntime/runtimes/remote/wire"
	"github.com/agentre-hub/agentre/pkg/wire/agentrewire"
)

// Given 一份含二进制 Input 与多字段提问的待决策快照, When 它经
// PendingWaitersResponseToProto 过线再经 PendingWaitersResponseFromProto 回来,
// Then 二进制 Input 逐字节不变、问题与选项的每个字段都保真。
//
// 这是 session.pendingWaiters 的生产接缝:daemon/protobuf_registry.go 与
// peer/protobuf_inbound.go 用 ToProto 装配应答,remote/runtime.go 用 FromProto 还原。
// 会拒绝的错误实现:把 Input 当 UTF-8 字符串搬运(0x00/0xFF 会被改写),或漏搬
// MultiSelect / IsOther / IsSecret / Option.Preview 之类的布尔与次要字段。
func TestPendingWaitersRoundTripPreservesBinaryInputAndQuestions(t *testing.T) {
	want := wire.SessionPendingWaitersResult{
		ToolPermissions: []agentruntime.PendingToolPermission{{
			RequestID: "perm-1", ToolName: "Bash", Input: json.RawMessage{0, 1, 2, 255},
		}},
		AskUserQuestions: []agentruntime.PendingAskUserQuestion{{
			RequestID: "ask-1",
			Questions: []agentruntime.AskQuestion{{
				ID: "q-1", Question: "Choose", Header: "Mode", MultiSelect: true,
				IsOther: true, IsSecret: true,
				Options: []agentruntime.AskOption{{Label: "A", Description: "first", Preview: "a"}},
			}},
		}},
	}

	// methodID 路径把应答体放进 Response.encoded_payload,过线的就是这串字节。
	encoded, err := proto.Marshal(PendingWaitersResponseToProto(want))
	require.NoError(t, err)
	response := new(agentrewire.SessionPendingWaitersResponse)
	require.NoError(t, proto.Unmarshal(encoded, response))

	got := PendingWaitersResponseFromProto(response)
	require.Equal(t, want.ToolPermissions, got.ToolPermissions)
	require.Equal(t, want.AskUserQuestions, got.AskUserQuestions)
}

// Given 一个 backend 没有任何待决策, When 快照过同一条线, Then 两个列表都回空
// 而不是 nil 解引用 —— R7 明写「未实现审批协议 / 不属于调用方的会话回空列表」,
// 空快照是这条路径的常态而非异常。
func TestPendingWaitersEmptySnapshotSurvivesAsEmptyLists(t *testing.T) {
	encoded, err := proto.Marshal(PendingWaitersResponseToProto(wire.SessionPendingWaitersResult{}))
	require.NoError(t, err)
	response := new(agentrewire.SessionPendingWaitersResponse)
	require.NoError(t, proto.Unmarshal(encoded, response))

	got := PendingWaitersResponseFromProto(response)
	require.Empty(t, got.ToolPermissions)
	require.Empty(t, got.AskUserQuestions)
}
