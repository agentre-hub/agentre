package chat_import_svc

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"github.com/agentre-hub/agentre/internal/model/entity/agent_backend_entity"
	"github.com/agentre-hub/agentre/internal/pkg/transcriptimport"
)

// TestListCandidates_OneBackendFailureDoesNotSuppressOthers 是发现聚合的主判据
// (spec「发现、去重与来源」):某个后端的目录读不动 / 不存在 / 这台机器上根本没装
// 那个 CLI,都只让它自己那一档报出原因,其余后端照常列出。
//
// 顺带钉住另外两条:合并后按最后活动时间倒序(用户看到的是一条跨后端的时间线),
// 库里已经有同一个 provider_session_id 的候选**照常列出但不可选**并带上那条会话的
// id —— 藏起来会让用户以为扫描漏了。
func TestListCandidates_OneBackendFailureDoesNotSuppressOthers(t *testing.T) {
	m := withMocks(t)
	older := time.Date(2026, 4, 1, 8, 0, 0, 0, time.UTC)
	newer := time.Date(2026, 4, 2, 8, 0, 0, 0, time.UTC)
	installSource(t, &fakeSource{
		backend: agent_backend_entity.TypeClaudeCode,
		candidates: []transcriptimport.Candidate{
			{
				Backend: agent_backend_entity.TypeClaudeCode, ProviderSessionID: "c-old",
				Title: "老的", Cwd: testCwd, StartedAt: older, EndedAt: older,
				Turns: 4, Origin: transcriptimport.OriginTerminal, Locator: "loc-old",
			},
			{
				Backend: agent_backend_entity.TypeClaudeCode, ProviderSessionID: "c-new",
				Title: "新的", Cwd: testCwd, StartedAt: newer, EndedAt: newer,
				Turns: 9, Origin: transcriptimport.OriginAgentre, Locator: "loc-new",
			},
		},
	})
	broken := installSource(t, &fakeSource{
		backend: agent_backend_entity.TypeCodex,
		scanErr: errBoom,
	})

	m.session.EXPECT().ListIDsByProviderSessions(gomock.Any(), []string{"c-old", "c-new"}).
		Return(map[string]int64{"c-new": 77}, nil)

	got, err := m.svc.ListCandidates(context.Background(), &ListCandidatesRequest{
		Backends:  []string{string(agent_backend_entity.TypeClaudeCode), string(agent_backend_entity.TypeCodex)},
		CwdPrefix: "/tmp",
		Since:     older.UnixMilli(),
	})

	require.NoError(t, err)
	require.Len(t, got.Candidates, 2, "codex 那一档失败不该带走 claude 的结果")
	assert.Equal(t, "c-new", got.Candidates[0].ProviderSessionID, "按最后活动时间倒序")
	assert.Equal(t, "c-old", got.Candidates[1].ProviderSessionID)

	assert.True(t, got.Candidates[0].Imported, "库里已有 → 列出但不可选")
	assert.Equal(t, int64(77), got.Candidates[0].ImportedSessionID, "给得出「打开」的去处")
	assert.False(t, got.Candidates[1].Imported)
	assert.Equal(t, "agentre", got.Candidates[0].Origin, "来源标记只用于展示,原样带出")
	assert.Equal(t, 9, got.Candidates[0].Turns)

	require.Len(t, got.Issues, 1)
	assert.Equal(t, string(agent_backend_entity.TypeCodex), got.Issues[0].Backend)
	assert.Contains(t, got.Issues[0].Reason, "boom")

	// 过滤条件原样交给各后端的 Scan —— 扫描只读元信息,过滤在读取器那一侧完成。
	require.Len(t, broken.scanFilters, 1)
	assert.Equal(t, "/tmp", broken.scanFilters[0].CwdPrefix)
	assert.Equal(t, older, broken.scanFilters[0].Since.UTC())
}

// 远端设备那一维的判据搬去了 remote_test.go:接线之后「答不出」不再是一个笼统的
// 错误,而是三态里的 unavailable / unsupported 各说各的话。
