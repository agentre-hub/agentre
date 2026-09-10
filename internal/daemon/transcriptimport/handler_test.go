package transcriptimport_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/agentre-hub/agentre/internal/daemon/transcriptimport"
	"github.com/agentre-hub/agentre/internal/model/entity/agent_backend_entity"
	"github.com/agentre-hub/agentre/internal/pkg/agentruntime"
	pkgimport "github.com/agentre-hub/agentre/internal/pkg/transcriptimport"
	"github.com/agentre-hub/agentre/internal/pkg/transcriptimport/wire"
)

// TestScan_AnswersPerBackendOkOrUnavailable:一台机器上某个后端的目录读不动 / 根本
// 没装那个 CLI,只让它自己那一档报 unavailable,其余照常 ok 出结果(spec「发现、
// 去重与来源」)。整体报错会让装了三个 CLI 的机器少装一个就什么都看不见。
func TestScan_AnswersPerBackendOkOrUnavailable(t *testing.T) {
	h := transcriptimport.NewHandlers(transcriptimport.Options{Sources: func() []pkgimport.Source {
		return []pkgimport.Source{
			&fakeSource{backend: agent_backend_entity.TypeClaudeCode, candidates: []pkgimport.Candidate{{
				Backend: agent_backend_entity.TypeClaudeCode, ProviderSessionID: "c-1", Title: "一条",
				Cwd: "/tmp/x", Locator: "loc-1", Turns: 3,
			}}},
			&fakeSource{backend: agent_backend_entity.TypeCodex, scanErr: errors.New("boom")},
		}
	}})

	got, err := h.Scan(context.Background(), wire.ScanParams{Filter: pkgimport.Filter{CwdPrefix: "/tmp"}})

	require.NoError(t, err, "一档失败不该整体报错")
	require.Len(t, got.Backends, 2)
	assert.Equal(t, string(agent_backend_entity.TypeClaudeCode), got.Backends[0].Backend)
	assert.Equal(t, wire.StatusOK, got.Backends[0].Status)
	require.Len(t, got.Backends[0].Candidates, 1)
	assert.Equal(t, "c-1", got.Backends[0].Candidates[0].ProviderSessionID)
	assert.Equal(t, wire.StatusUnavailable, got.Backends[1].Status, "读不动的那一档自报原因")
	assert.Contains(t, got.Backends[1].Reason, "boom")
	assert.Empty(t, got.Backends[1].Candidates)
}

// TestTurns_PagesWithoutMaterialisingWholeTranscript:分页取轮次,一页只握住这一页 ——
// 读取器的 Turns 是推式流,daemon 侧不得为了应答把整份转录攒在内存里。
func TestTurns_PagesWithoutMaterialisingWholeTranscript(t *testing.T) {
	src := &fakeSource{backend: agent_backend_entity.TypeClaudeCode, transcript: &fakeTranscript{
		meta:  pkgimport.Meta{ProviderSessionID: "s-1", Turns: 5},
		turns: makeTurns(5),
	}}
	h := transcriptimport.NewHandlers(transcriptimport.Options{Sources: func() []pkgimport.Source { return []pkgimport.Source{src} }})

	first, err := h.Turns(context.Background(), wire.TurnsParams{
		Backend: string(agent_backend_entity.TypeClaudeCode), Locator: "loc-1", MaxTurns: 2,
	})
	require.NoError(t, err)
	require.Len(t, first.Turns, 2)
	assert.Equal(t, 0, first.Turns[0].Index)
	assert.Equal(t, 1, first.Turns[1].Index)
	assert.True(t, first.HasMore)
	assert.Equal(t, 2, first.NextIndex)
	assert.Equal(t, 3, src.transcript.yielded, "一页只走到「够这一页 + 一条探路」为止,不把整份转录解完")

	last, err := h.Turns(context.Background(), wire.TurnsParams{
		Backend: string(agent_backend_entity.TypeClaudeCode), Locator: "loc-1", StartIndex: 4, MaxTurns: 2,
	})
	require.NoError(t, err)
	require.Len(t, last.Turns, 1)
	assert.Equal(t, 4, last.Turns[0].Index)
	assert.False(t, last.HasMore, "最后一页要说清后面没有了")
	assert.True(t, src.transcript.closed, "每一页用完都关掉转录")
}

// TestTurns_UnknownBackendIsTypedNotGeneric:这台机器上没有这个后端的读取器时给出
// 可翻译的 typed 错误,而不是笼统失败 —— host 侧要据它说「这台机器没装那个 CLI」。
func TestTurns_UnknownBackendIsTypedNotGeneric(t *testing.T) {
	h := transcriptimport.NewHandlers(transcriptimport.Options{Sources: func() []pkgimport.Source { return nil }})

	_, err := h.Turns(context.Background(), wire.TurnsParams{Backend: "codex", Locator: "loc"})

	require.ErrorIs(t, err, wire.ErrBackendUnavailable)
}

// TestOpen_ReturnsMetaAndClosesTranscript:Open 只取元信息,取完就把转录关掉 ——
// daemon 侧不持有跨调用的句柄(没有句柄就没有泄漏)。
func TestOpen_ReturnsMetaAndClosesTranscript(t *testing.T) {
	src := &fakeSource{backend: agent_backend_entity.TypeCodex, transcript: &fakeTranscript{
		meta: pkgimport.Meta{
			Backend: agent_backend_entity.TypeCodex, ProviderSessionID: "s-2", Title: "标题",
			Gaps: []pkgimport.Gap{{Kind: pkgimport.GapThinkingUnavailable, Count: 2}},
		},
	}}
	h := transcriptimport.NewHandlers(transcriptimport.Options{Sources: func() []pkgimport.Source { return []pkgimport.Source{src} }})

	got, err := h.Open(context.Background(), wire.OpenParams{Backend: "codex", Locator: "loc-2"})

	require.NoError(t, err)
	assert.Equal(t, "s-2", got.Meta.ProviderSessionID)
	require.Len(t, got.Meta.Gaps, 1)
	assert.Equal(t, pkgimport.GapThinkingUnavailable, got.Meta.Gaps[0].Kind)
	assert.True(t, src.transcript.closed)
}

// ── fakes ───────────────────────────────────────────────────────────────────

func makeTurns(n int) []pkgimport.Turn {
	out := make([]pkgimport.Turn, 0, n)
	base := time.Date(2026, 8, 1, 9, 0, 0, 0, time.UTC)
	for i := range n {
		out = append(out, pkgimport.Turn{
			Index: i, UserText: "问题", Model: "claude-opus-5",
			Events:    []agentruntime.Event{agentruntime.TextDelta{Text: "回答"}},
			StartedAt: base, EndedAt: base.Add(time.Minute),
		})
	}
	return out
}

type fakeTranscript struct {
	meta    pkgimport.Meta
	turns   []pkgimport.Turn
	closed  bool
	yielded int
	// yieldErr / yieldErrAfter 让回放在第 yieldErrAfter 轮之后半途读坏,覆盖
	// 「导到一半失败」这条边界。
	yieldErr      error
	yieldErrAfter int
}

func (f *fakeTranscript) Meta() pkgimport.Meta { return f.meta }

func (f *fakeTranscript) Turns(ctx context.Context, yield func(pkgimport.Turn) error) error {
	for _, t := range f.turns {
		if err := ctx.Err(); err != nil {
			return err
		}
		if f.yieldErr != nil && f.yielded >= f.yieldErrAfter {
			return f.yieldErr
		}
		f.yielded++
		if err := yield(t); err != nil {
			return err
		}
	}
	return nil
}

func (f *fakeTranscript) Close() error {
	f.closed = true
	return nil
}

type fakeSource struct {
	backend    agent_backend_entity.BackendType
	candidates []pkgimport.Candidate
	scanErr    error
	transcript *fakeTranscript
	openErr    error
}

func (f *fakeSource) Backend() agent_backend_entity.BackendType { return f.backend }

func (f *fakeSource) Scan(_ context.Context, _ pkgimport.Filter) ([]pkgimport.Candidate, error) {
	if f.scanErr != nil {
		return nil, f.scanErr
	}
	return f.candidates, nil
}

func (f *fakeSource) Open(_ context.Context, _ pkgimport.Locator) (pkgimport.Transcript, error) {
	if f.openErr != nil {
		return nil, f.openErr
	}
	return f.transcript, nil
}

// TestTurns_StopsPageOnByteBudget:一轮里塞着几 MB 工具结果时,页按字节预算提前收,
// 并如实说 HasMore —— 只按轮数分页会撞上 protorpc 的 16 MiB 帧上限,而撞上的表现
// 是整条导入失败,不是少一页。
func TestTurns_StopsPageOnByteBudget(t *testing.T) {
	heavy := makeTurns(4)
	for i := range heavy {
		heavy[i].Events = []agentruntime.Event{agentruntime.ToolResult{Content: string(make([]byte, 4096))}}
	}
	src := &fakeSource{backend: agent_backend_entity.TypeClaudeCode, transcript: &fakeTranscript{turns: heavy}}
	h := transcriptimport.NewHandlers(transcriptimport.Options{
		Sources:      func() []pkgimport.Source { return []pkgimport.Source{src} },
		MaxPageBytes: 5000,
	})

	got, err := h.Turns(context.Background(), wire.TurnsParams{
		Backend: string(agent_backend_entity.TypeClaudeCode), Locator: "loc-1", MaxTurns: 4,
	})

	require.NoError(t, err)
	require.Len(t, got.Turns, 2, "预算允许的轮数之外提前收页")
	assert.True(t, got.HasMore, "提前收的页后面还有,不能靠「页不满」被当成结束")
	assert.Equal(t, 2, got.NextIndex)
}
