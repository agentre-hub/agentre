package protowire_test

import (
	"testing"
	"time"

	"github.com/cago-frame/agents/agent/blocks"
	"github.com/cago-frame/agents/provider"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/agentre-hub/agentre/internal/model/entity/agent_backend_entity"
	"github.com/agentre-hub/agentre/internal/pkg/agentruntime"
	"github.com/agentre-hub/agentre/internal/pkg/agentruntime/runtimes/remote/protowire"
	pkgimport "github.com/agentre-hub/agentre/internal/pkg/transcriptimport"
	"github.com/agentre-hub/agentre/internal/pkg/transcriptimport/wire"
)

// TestTranscriptScanRoundTripsThreeStateVerbatim:三态取值必须在字节流里如实往返 ——
// 「问出来就是没有」(ok + 空列表)与「这台机器答不出」(unavailable + 原因)一旦
// 在编解码里被抹平,UI 上就只剩一句「这台机器没有会话」。
func TestTranscriptScanRoundTripsThreeStateVerbatim(t *testing.T) {
	started := time.Date(2026, 8, 1, 9, 0, 0, 0, time.UTC)
	in := wire.ScanResult{Backends: []wire.BackendScan{
		{Backend: "claudecode", Status: wire.StatusOK, Candidates: []pkgimport.Candidate{{
			Backend: agent_backend_entity.TypeClaudeCode, ProviderSessionID: "s-1", Title: "标题",
			Cwd: "/tmp/x", StartedAt: started, EndedAt: started.Add(time.Hour), Turns: 7,
			Origin: pkgimport.OriginTerminal, Locator: "loc-1",
		}}},
		{Backend: "codex", Status: wire.StatusOK},
		{Backend: "piagent", Status: wire.StatusUnavailable, Reason: "目录读不动"},
	}}

	got := protowire.TranscriptScanResultFromProto(protowire.TranscriptScanResultToProto(in))

	require.Len(t, got.Backends, 3)
	want, gotCandidate := in.Backends[0].Candidates[0], got.Backends[0].Candidates[0]
	assert.True(t, want.StartedAt.Equal(gotCandidate.StartedAt), "最后活动时间原样往返")
	assert.True(t, want.EndedAt.Equal(gotCandidate.EndedAt))
	want.StartedAt, want.EndedAt = gotCandidate.StartedAt, gotCandidate.EndedAt
	assert.Equal(t, want, gotCandidate)
	assert.Equal(t, wire.StatusOK, got.Backends[1].Status, "ok + 空列表不能退化成 unavailable")
	assert.Empty(t, got.Backends[1].Candidates)
	assert.Equal(t, wire.StatusUnavailable, got.Backends[2].Status)
	assert.Equal(t, "目录读不动", got.Backends[2].Reason)
}

// TestTranscriptScanParamsRoundTripsFilter:过滤条件原样过 wire,零值 Since 仍是零值
// —— 用毫秒 0 冒充「1970 年之后」会让远端把全部候选都算进来。
func TestTranscriptScanParamsRoundTripsFilter(t *testing.T) {
	since := time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC)

	full := protowire.TranscriptScanParamsFromProto(protowire.TranscriptScanParamsToProto(wire.ScanParams{
		Backends: []string{"codex"},
		Filter:   pkgimport.Filter{CwdPrefix: "/tmp", Since: since, TitleQuery: "查", Limit: 20},
	}))
	assert.Equal(t, []string{"codex"}, full.Backends)
	assert.Equal(t, since, full.Filter.Since.UTC())
	assert.Equal(t, 20, full.Filter.Limit)

	empty := protowire.TranscriptScanParamsFromProto(protowire.TranscriptScanParamsToProto(wire.ScanParams{}))
	assert.True(t, empty.Filter.Since.IsZero(), "零值 Since 不能变成 1970")
	assert.Empty(t, empty.Backends)
}

// TestTranscriptTurnsRoundTripsEventsAndDiskTimes:一页轮次连同事件序列、磁盘时间、
// fork 锚点与用量原样往返。事件走既有的 sealed event 逐字段编解码 —— 转录导入与
// 线上执行共用同一份编解码,不另开一套。
func TestTranscriptTurnsRoundTripsEventsAndDiskTimes(t *testing.T) {
	started := time.Date(2026, 8, 2, 10, 30, 0, 0, time.UTC)
	in := wire.TurnsResult{
		Turns: []pkgimport.Turn{{
			Index: 3, UserText: "帮我改一下",
			UserImages: []blocks.ImageBlock{{MediaType: "image/png", Source: blocks.BlobSource{Inline: []byte{1, 2, 3}}}},
			Events: []agentruntime.Event{
				agentruntime.TextDelta{Text: "好的"},
				agentruntime.ToolCall{ID: "t1", Name: "Read", Input: []byte(`{"path":"a.go"}`)},
				agentruntime.ToolResult{ToolCallID: "t1", Content: "内容"},
			},
			Usage:     &provider.Usage{PromptTokens: 11, TotalTokens: 42},
			Model:     "claude-opus-5",
			StartedAt: started, EndedAt: started.Add(2 * time.Minute),
			ForkAnchor: "uuid-9", ErrorText: "中断",
		}},
		NextIndex: 4, HasMore: true,
	}

	encoded, err := protowire.TranscriptTurnsResultToProto(in)
	require.NoError(t, err)
	got, err := protowire.TranscriptTurnsResultFromProto(encoded)
	require.NoError(t, err)

	require.Len(t, got.Turns, 1)
	assert.Equal(t, in.Turns[0].Index, got.Turns[0].Index)
	assert.Equal(t, in.Turns[0].UserText, got.Turns[0].UserText)
	assert.Equal(t, in.Turns[0].UserImages, got.Turns[0].UserImages)
	assert.Equal(t, in.Turns[0].Events, got.Turns[0].Events)
	assert.Equal(t, in.Turns[0].Usage, got.Turns[0].Usage)
	assert.Equal(t, started, got.Turns[0].StartedAt.UTC(), "时间取磁盘值")
	assert.Equal(t, "uuid-9", got.Turns[0].ForkAnchor)
	assert.Equal(t, "中断", got.Turns[0].ErrorText)
	assert.Equal(t, 4, got.NextIndex)
	assert.True(t, got.HasMore)

	// 没有用量的那一轮解出来仍是 nil:全零 usage 会被当成「这一轮真的用了 0 token」。
	blank, err := protowire.TranscriptTurnsResultToProto(wire.TurnsResult{Turns: []pkgimport.Turn{{Index: 0}}})
	require.NoError(t, err)
	back, err := protowire.TranscriptTurnsResultFromProto(blank)
	require.NoError(t, err)
	assert.Nil(t, back.Turns[0].Usage)
	assert.True(t, back.Turns[0].StartedAt.IsZero(), "磁盘上没有时间就是没有,不拿 1970 冒充")
}

// TestTranscriptMetaRoundTripsGaps:缺口声明是契约的一等公民,必须跟着元信息过 wire
// —— 远端导入同样要在导入前说清「思维链在磁盘上是空的」。
func TestTranscriptMetaRoundTripsGaps(t *testing.T) {
	in := wire.OpenResult{Meta: pkgimport.Meta{
		Backend: agent_backend_entity.TypePiAgent, ProviderSessionID: "s-9", Title: "标题",
		Cwd: "/tmp/y", Model: "glm-5.3", Turns: 12, ToolCalls: 40, Compactions: 1,
		Origin: pkgimport.OriginAgentre,
		Gaps: []pkgimport.Gap{
			{Kind: pkgimport.GapThinkingUnavailable, Count: 3},
			{Kind: pkgimport.GapUnparsableRecords, Count: 2, Detail: "agent-3.jsonl"},
		},
	}}

	got := protowire.TranscriptOpenResultFromProto(protowire.TranscriptOpenResultToProto(in))

	assert.Equal(t, in.Meta, got.Meta)
}
