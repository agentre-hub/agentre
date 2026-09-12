package chat_import_svc

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"github.com/agentre-hub/agentre/internal/model/entity/agent_backend_entity"
	"github.com/agentre-hub/agentre/internal/pkg/agentruntime"
	"github.com/agentre-hub/agentre/internal/pkg/transcriptimport"
	"github.com/agentre-hub/agentre/internal/pkg/transcriptimport/wire"
)

// fakeGateway 顶替一次真实的 daemon 往返(真实往返由 internal/daemon 的
// protorpc 用例钉住,这里只钉聚合层怎么读它的答案)。
type fakeGateway struct {
	scan      *wire.ScanResult
	scanErr   error
	scanCalls int
	scanReqs  []wire.ScanParams
	open      *wire.OpenResult
	openErr   error
	pages     []wire.TurnsResult
	turnsReqs []wire.TurnsParams
	deviceIDs []int64
}

func (f *fakeGateway) Scan(_ context.Context, deviceID int64, params wire.ScanParams) (*wire.ScanResult, error) {
	f.scanCalls++
	f.scanReqs = append(f.scanReqs, params)
	f.deviceIDs = append(f.deviceIDs, deviceID)
	if f.scanErr != nil {
		return nil, f.scanErr
	}
	return f.scan, nil
}

func (f *fakeGateway) Open(_ context.Context, _ int64, _ wire.OpenParams) (*wire.OpenResult, error) {
	if f.openErr != nil {
		return nil, f.openErr
	}
	return f.open, nil
}

func (f *fakeGateway) Turns(_ context.Context, _ int64, params wire.TurnsParams) (*wire.TurnsResult, error) {
	f.turnsReqs = append(f.turnsReqs, params)
	for _, page := range f.pages {
		if len(page.Turns) > 0 && page.Turns[0].Index == params.StartIndex {
			return &page, nil
		}
	}
	return &wire.TurnsResult{NextIndex: params.StartIndex}, nil
}

func swapGateway(t *testing.T, gw transcriptGateway) *fakeGateway {
	t.Helper()
	prev := remoteGateway
	remoteGateway = gw
	t.Cleanup(func() { remoteGateway = prev })
	fake, _ := gw.(*fakeGateway)
	return fake
}

// TestListCandidates_RemoteDeviceAnswersPerBackend:已配对 daemon 按后端分别答
// ok / unavailable —— 那台机器上没装 codex,只让 codex 那一档报原因,claude 照常
// 列出候选(spec「远端」+「发现、去重与来源」)。
func TestListCandidates_RemoteDeviceAnswersPerBackend(t *testing.T) {
	m := withMocks(t)
	installSource(t, &fakeSource{backend: agent_backend_entity.TypeClaudeCode})
	installSource(t, &fakeSource{backend: agent_backend_entity.TypeCodex})
	installSource(t, &fakeSource{backend: agent_backend_entity.TypePiAgent})
	ended := time.Date(2026, 8, 3, 12, 0, 0, 0, time.UTC)
	gw := swapGateway(t, &fakeGateway{scan: &wire.ScanResult{Backends: []wire.BackendScan{
		{Backend: "claudecode", Status: wire.StatusOK, Candidates: []transcriptimport.Candidate{{
			Backend: agent_backend_entity.TypeClaudeCode, ProviderSessionID: "r-1", Title: "远端那条",
			Cwd: "/srv/work", EndedAt: ended, Turns: 6, Locator: "loc-1",
		}}},
		{Backend: "codex", Status: wire.StatusUnavailable, Reason: "permission denied"},
		{Backend: "piagent", Status: wire.StatusOK},
	}}})
	m.session.EXPECT().ListIDsByProviderSessions(gomock.Any(), []string{"r-1"}).Return(map[string]int64{}, nil)

	got, err := m.svc.ListCandidates(context.Background(), &ListCandidatesRequest{DeviceID: 7, CwdPrefix: "/srv"})

	require.NoError(t, err)
	require.Len(t, got.Candidates, 1)
	assert.Equal(t, "r-1", got.Candidates[0].ProviderSessionID)
	require.Len(t, gw.scanReqs, 1)
	assert.Equal(t, "/srv", gw.scanReqs[0].Filter.CwdPrefix, "过滤条件在远端那一侧生效,不是拉回来再筛")
	assert.Equal(t, "loc-1", got.Candidates[0].Locator, "定位符原样带回,后面照它 open")
	require.Len(t, got.Issues, 1)
	assert.Equal(t, "codex", got.Issues[0].Backend)
	assert.Equal(t, ScanStatusUnavailable, got.Issues[0].Status)
	assert.Contains(t, got.Issues[0].Reason, "permission denied")
	assert.Equal(t, 1, gw.scanCalls, "一台设备一次往返,不是每个后端各打一发")
	assert.Equal(t, []int64{7}, gw.deviceIDs)
}

// TestListCandidates_RemoteFailureLeavesLocalMachineAlone:远端那台答不出不影响本机
// —— 本机走注册表,压根不经过网关。
func TestListCandidates_RemoteFailureLeavesLocalMachineAlone(t *testing.T) {
	m := withMocks(t)
	installSource(t, &fakeSource{backend: agent_backend_entity.TypeClaudeCode, candidates: []transcriptimport.Candidate{{
		Backend: agent_backend_entity.TypeClaudeCode, ProviderSessionID: "local-1", Locator: "loc-l",
	}}})
	installSource(t, &fakeSource{backend: agent_backend_entity.TypeCodex})
	installSource(t, &fakeSource{backend: agent_backend_entity.TypePiAgent})
	gw := swapGateway(t, &fakeGateway{scanErr: errDeviceOffline})
	m.session.EXPECT().ListIDsByProviderSessions(gomock.Any(), gomock.Any()).Return(map[string]int64{}, nil).Times(2)

	remote, err := m.svc.ListCandidates(context.Background(), &ListCandidatesRequest{DeviceID: 7})
	require.NoError(t, err)
	require.Len(t, remote.Issues, 1)

	local, err := m.svc.ListCandidates(context.Background(), &ListCandidatesRequest{DeviceID: 0})

	require.NoError(t, err)
	require.Len(t, local.Candidates, 1)
	assert.Equal(t, "local-1", local.Candidates[0].ProviderSessionID)
	assert.Empty(t, local.Issues)
	assert.Equal(t, 1, gw.scanCalls, "本机那一问不该打到远端去")
}

// TestListCandidates_RemoteDeviceOfflineIsUnavailable:拨不通是整台设备的一句话,
// 不按后端重复三遍。
func TestListCandidates_RemoteDeviceOfflineIsUnavailable(t *testing.T) {
	m := withMocks(t)
	installSource(t, &fakeSource{backend: agent_backend_entity.TypeClaudeCode})
	installSource(t, &fakeSource{backend: agent_backend_entity.TypeCodex})
	installSource(t, &fakeSource{backend: agent_backend_entity.TypePiAgent})
	swapGateway(t, &fakeGateway{scanErr: errDeviceOffline})
	m.session.EXPECT().ListIDsByProviderSessions(gomock.Any(), gomock.Any()).Return(map[string]int64{}, nil)

	got, err := m.svc.ListCandidates(context.Background(), &ListCandidatesRequest{DeviceID: 7})

	require.NoError(t, err)
	assert.Empty(t, got.Candidates)
	require.Len(t, got.Issues, 1)
	assert.Equal(t, ScanStatusUnavailable, got.Issues[0].Status)
}

// TestRemoteTranscript_TurnsStreamsPageByPage:远端转录逐轮吐给调用方,页是取回的
// 单位而不是回放的单位 —— 调用方看到的仍是「一轮一轮」,与本机读取器同一个契约。
func TestRemoteTranscript_TurnsStreamsPageByPage(t *testing.T) {
	gw := &fakeGateway{
		open: &wire.OpenResult{Meta: transcriptimport.Meta{ProviderSessionID: "r-9", Turns: 3}},
		pages: []wire.TurnsResult{
			{Turns: []transcriptimport.Turn{
				{Index: 0, UserText: "一", Events: []agentruntime.Event{agentruntime.TextDelta{Text: "答一"}}},
				{Index: 1, UserText: "二"},
			}, NextIndex: 2, HasMore: true},
			{Turns: []transcriptimport.Turn{{Index: 2, UserText: "三"}}, NextIndex: 3},
		},
	}
	src := remoteSources(7, gw)
	require.NotEmpty(t, src)

	tr, err := src[0].Open(context.Background(), "loc-1")
	require.NoError(t, err)
	assert.Equal(t, "r-9", tr.Meta().ProviderSessionID)

	var seen []string
	require.NoError(t, tr.Turns(context.Background(), func(turn transcriptimport.Turn) error {
		seen = append(seen, turn.UserText)
		return nil
	}))
	assert.Equal(t, []string{"一", "二", "三"}, seen)
	require.Len(t, gw.turnsReqs, 2, "翻到最后一页就停,不空转再问一次")
	assert.Equal(t, 0, gw.turnsReqs[0].StartIndex)
	assert.Equal(t, 2, gw.turnsReqs[1].StartIndex)
	assert.Equal(t, "loc-1", gw.turnsReqs[0].Locator, "定位符原样回传给出它的那台机器")
	assert.Equal(t, string(src[0].Backend()), gw.turnsReqs[0].Backend)
	require.NoError(t, tr.Close())
}

// TestRemoteTranscript_YieldErrorStopsFetching:契约里 yield 返回非 nil 就立刻停止
// 回放 —— 预览取前几轮靠它,不能为看一眼就把整份转录传回来(spec「远端」)。
func TestRemoteTranscript_YieldErrorStopsFetching(t *testing.T) {
	gw := &fakeGateway{
		open: &wire.OpenResult{Meta: transcriptimport.Meta{ProviderSessionID: "r-9"}},
		pages: []wire.TurnsResult{
			{Turns: []transcriptimport.Turn{{Index: 0}, {Index: 1}}, NextIndex: 2, HasMore: true},
			{Turns: []transcriptimport.Turn{{Index: 2}}, NextIndex: 3},
		},
	}
	src := remoteSources(7, gw)
	require.NotEmpty(t, src)
	tr, err := src[0].Open(context.Background(), "loc-1")
	require.NoError(t, err)

	stop := errors.New("够了")
	err = tr.Turns(context.Background(), func(transcriptimport.Turn) error { return stop })

	require.ErrorIs(t, err, stop, "yield 的错误原样返回")
	assert.Len(t, gw.turnsReqs, 1, "第一页就够,不再翻下一页")
}
