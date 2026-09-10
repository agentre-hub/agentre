package sync_svc

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/cago-frame/cago/pkg/consts"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"github.com/agentre-hub/agentre/internal/model/entity/agent_entity"
	"github.com/agentre-hub/agentre/internal/model/entity/syncmeta_entity"
	"github.com/agentre-hub/agentre/internal/pkg/syncwire"
	"github.com/agentre-hub/agentre/internal/repository/agent_repo"
	"github.com/agentre-hub/agentre/internal/repository/agent_repo/mock_agent_repo"
	"github.com/agentre-hub/agentre/internal/repository/syncstate_repo"
	"github.com/agentre-hub/agentre/internal/repository/syncstate_repo/mock_syncstate_repo"
)

// stubAvatarTransport 是 avatarTransport 的替身：记下每一次 Put / Get，按脚本回应。
type stubAvatarTransport struct {
	putCalls []avatarPutCall

	getCalled      int
	getLastHash    string
	getContent     string
	getContentType string
	getErr         error
}

func (s *stubAvatarTransport) PutAvatar(_ context.Context, contentHash, contentType, content string) error {
	s.putCalls = append(s.putCalls, avatarPutCall{Hash: contentHash, ContentType: contentType, Content: content})
	return nil
}

func (s *stubAvatarTransport) GetAvatar(_ context.Context, contentHash string) (string, string, error) {
	s.getCalled++
	s.getLastHash = contentHash
	if s.getErr != nil {
		return "", "", s.getErr
	}
	return s.getContent, s.getContentType, nil
}

const testAvatarDataURL = "data:image/png;base64,QUFBQQ=="

// TestAgentAdapter_LoadEmitsAvatarHashNotContent R16a 的守卫断言：同步载荷里的
// Agent 只带 AvatarDataURL 的内容哈希，不带正文；哈希参与内容比较所以换头像
// 照常触发同步（这里先验证 load 侧只上行哈希）。
func TestAgentAdapter_LoadEmitsAvatarHashNotContent(t *testing.T) {
	ctrl := gomock.NewController(t)
	state := mock_syncstate_repo.NewMockSyncStateRepo(ctrl)
	state.EXPECT().FindRow(gomock.Any(), syncwire.KindAgent, "agent-1", gomock.Any()).DoAndReturn(
		func(_ context.Context, _, _ string, dest any) (bool, error) {
			row := dest.(*agent_entity.Agent)
			*row = agent_entity.Agent{
				ID: 5, Name: "Ava", AvatarDataURL: testAvatarDataURL,
				SyncMeta: syncmeta_entity.SyncMeta{SyncID: "agent-1", SyncVersion: 3},
			}
			return true, nil
		})
	syncstate_repo.RegisterSyncState(state)

	avatar := &stubAvatarTransport{}
	out, err := (&agentAdapter{avatar: avatar}).load(context.Background(), "agent-1")
	require.NoError(t, err)
	require.NotNil(t, out)

	var payload map[string]any
	require.NoError(t, json.Unmarshal(out.Payload, &payload))
	assert.Equal(t, avatarHash(testAvatarDataURL), payload["avatar_hash"])
	assert.NotContains(t, payload, "avatar_data_url", "正文一律不进同步载荷")
	assert.NoError(t, syncwire.GuardPayload(syncwire.KindAgent, out.Payload), "守卫也认可这份载荷不带头像正文")

	require.Len(t, avatar.putCalls, 1, "本机持有正文时应该单独推一次给对端")
	assert.Equal(t, avatarHash(testAvatarDataURL), avatar.putCalls[0].Hash)
	assert.Equal(t, testAvatarDataURL, avatar.putCalls[0].Content)
	assert.Equal(t, "image/png", avatar.putCalls[0].ContentType)
}

// TestAgentAdapter_LoadTwice_UploadsTheSameContentOnce R16a/决策 28：正文按内容
// 哈希**单独传一次**，「接收方尚未持有该哈希时才」传。改个名字就把几 MB 的头像
// 正文重发一遍，正是决策 28 要避免的那个代价。换了头像（哈希变了）当然要再传。
func TestAgentAdapter_LoadTwice_UploadsTheSameContentOnce(t *testing.T) {
	ctrl := gomock.NewController(t)
	state := mock_syncstate_repo.NewMockSyncStateRepo(ctrl)
	dataURL := testAvatarDataURL
	state.EXPECT().FindRow(gomock.Any(), syncwire.KindAgent, "agent-1", gomock.Any()).DoAndReturn(
		func(_ context.Context, _, _ string, dest any) (bool, error) {
			row := dest.(*agent_entity.Agent)
			*row = agent_entity.Agent{
				ID: 5, Name: "Ava", AvatarDataURL: dataURL,
				SyncMeta: syncmeta_entity.SyncMeta{SyncID: "agent-1"},
			}
			return true, nil
		}).AnyTimes()
	syncstate_repo.RegisterSyncState(state)

	avatar := &stubAvatarTransport{}
	ad := &agentAdapter{avatar: avatar}
	ctx := context.Background()

	_, err := ad.load(ctx, "agent-1")
	require.NoError(t, err)
	_, err = ad.load(ctx, "agent-1")
	require.NoError(t, err)
	assert.Len(t, avatar.putCalls, 1, "同一份正文只推一次")

	dataURL = "data:image/png;base64,aGVsbG8td29ybGQ="
	_, err = ad.load(ctx, "agent-1")
	require.NoError(t, err)
	require.Len(t, avatar.putCalls, 2, "换头像照常再推一次（哈希变了）")
	assert.Equal(t, avatarHash(dataURL), avatar.putCalls[1].Hash)
}

// TestAgentAdapter_LoadWithoutAvatar_SkipsUpload 没有自定义头像时载荷里的
// avatar_hash 为空，也不触发任何一次头像上传。
func TestAgentAdapter_LoadWithoutAvatar_SkipsUpload(t *testing.T) {
	ctrl := gomock.NewController(t)
	state := mock_syncstate_repo.NewMockSyncStateRepo(ctrl)
	state.EXPECT().FindRow(gomock.Any(), syncwire.KindAgent, "agent-1", gomock.Any()).DoAndReturn(
		func(_ context.Context, _, _ string, dest any) (bool, error) {
			row := dest.(*agent_entity.Agent)
			*row = agent_entity.Agent{ID: 5, Name: "Ava", SyncMeta: syncmeta_entity.SyncMeta{SyncID: "agent-1"}}
			return true, nil
		})
	syncstate_repo.RegisterSyncState(state)

	avatar := &stubAvatarTransport{}
	out, err := (&agentAdapter{avatar: avatar}).load(context.Background(), "agent-1")
	require.NoError(t, err)

	var payload map[string]any
	require.NoError(t, json.Unmarshal(out.Payload, &payload))
	assert.NotContains(t, payload, "avatar_hash")
	assert.Empty(t, avatar.putCalls)
}

// TestAgentAdapter_LoadAvatarUploadFailure_StillReturnsPayload 上传失败不阻塞
// Agent 本身照常同步（R16a）：load 仍然返回一份合法载荷。
func TestAgentAdapter_LoadAvatarUploadFailure_StillReturnsPayload(t *testing.T) {
	ctrl := gomock.NewController(t)
	state := mock_syncstate_repo.NewMockSyncStateRepo(ctrl)
	state.EXPECT().FindRow(gomock.Any(), syncwire.KindAgent, "agent-1", gomock.Any()).DoAndReturn(
		func(_ context.Context, _, _ string, dest any) (bool, error) {
			row := dest.(*agent_entity.Agent)
			*row = agent_entity.Agent{
				ID: 5, Name: "Ava", AvatarDataURL: testAvatarDataURL,
				SyncMeta: syncmeta_entity.SyncMeta{SyncID: "agent-1"},
			}
			return true, nil
		})
	syncstate_repo.RegisterSyncState(state)

	avatar := &failingPutAvatarTransport{err: errors.New("upload failed")}
	out, err := (&agentAdapter{avatar: avatar}).load(context.Background(), "agent-1")
	require.NoError(t, err, "头像上传失败不该让整个 Agent 上行失败")
	require.NotNil(t, out)

	var payload map[string]any
	require.NoError(t, json.Unmarshal(out.Payload, &payload))
	assert.Equal(t, avatarHash(testAvatarDataURL), payload["avatar_hash"])
}

type failingPutAvatarTransport struct{ err error }

func (f *failingPutAvatarTransport) PutAvatar(context.Context, string, string, string) error {
	return f.err
}
func (f *failingPutAvatarTransport) GetAvatar(context.Context, string) (string, string, error) {
	return "", "", f.err
}

// TestAgentAdapter_ApplyFetchesAvatarWhenNotHeld 接收方尚未持有该哈希时才单独
// 取一次正文：新建的 Agent 没有任何既有内容，遇到非空 avatar_hash 必须去取。
func TestAgentAdapter_ApplyFetchesAvatarWhenNotHeld(t *testing.T) {
	ctrl := gomock.NewController(t)
	state := mock_syncstate_repo.NewMockSyncStateRepo(ctrl)
	state.EXPECT().FindRow(gomock.Any(), syncwire.KindAgent, "agent-1", gomock.Any()).Return(false, nil)
	syncstate_repo.RegisterSyncState(state)

	agents := mock_agent_repo.NewMockAgentRepo(ctrl)
	var created *agent_entity.Agent
	agents.EXPECT().Create(gomock.Any(), gomock.Any()).DoAndReturn(
		func(_ context.Context, a *agent_entity.Agent) error { created = a; return nil })
	agent_repo.RegisterAgent(agents)

	hash := avatarHash(testAvatarDataURL)
	avatar := &stubAvatarTransport{getContent: testAvatarDataURL, getContentType: "image/png"}

	err := (&agentAdapter{avatar: avatar}).apply(context.Background(), &inbound{
		Kind: syncwire.KindAgent, SyncID: "agent-1", Version: 1,
		Payload: []byte(`{"name":"Landed","avatar_hash":"` + hash + `"}`),
	}, map[string]int64{})

	require.NoError(t, err)
	require.NotNil(t, created)
	assert.Equal(t, testAvatarDataURL, created.AvatarDataURL)
	assert.Equal(t, 1, avatar.getCalled, "尚未持有这份哈希，必须取一次")
	assert.Equal(t, hash, avatar.getLastHash)
}

// TestAgentAdapter_ApplySkipsFetchWhenAlreadyHoldingSameHash 已经持有同一份哈希
// 内容时不重新取（R16a）。
func TestAgentAdapter_ApplySkipsFetchWhenAlreadyHoldingSameHash(t *testing.T) {
	ctrl := gomock.NewController(t)
	hash := avatarHash(testAvatarDataURL)

	state := mock_syncstate_repo.NewMockSyncStateRepo(ctrl)
	state.EXPECT().FindRow(gomock.Any(), syncwire.KindAgent, "agent-1", gomock.Any()).DoAndReturn(
		func(_ context.Context, _, _ string, dest any) (bool, error) {
			row := dest.(*agent_entity.Agent)
			*row = agent_entity.Agent{
				ID: 5, AvatarDataURL: testAvatarDataURL,
				SyncMeta: syncmeta_entity.SyncMeta{SyncID: "agent-1"},
			}
			return true, nil
		})
	syncstate_repo.RegisterSyncState(state)

	agents := mock_agent_repo.NewMockAgentRepo(ctrl)
	var updated *agent_entity.Agent
	agents.EXPECT().UpdateRow(gomock.Any(), gomock.Any()).DoAndReturn(
		func(_ context.Context, a *agent_entity.Agent) error { updated = a; return nil })
	agent_repo.RegisterAgent(agents)

	avatar := &stubAvatarTransport{}
	err := (&agentAdapter{avatar: avatar}).apply(context.Background(), &inbound{
		Kind: syncwire.KindAgent, SyncID: "agent-1", Version: 2,
		Payload: []byte(`{"name":"Renamed","avatar_hash":"` + hash + `"}`),
	}, map[string]int64{})

	require.NoError(t, err)
	require.NotNil(t, updated)
	assert.Equal(t, testAvatarDataURL, updated.AvatarDataURL)
	assert.Zero(t, avatar.getCalled, "已经持有同一份哈希，不该再取一次")
}

// TestAgentAdapter_ApplyFallsBackToInitialsWhenFetchFails 取正文失败或超时时
// 降级为占位字母头像（既有 AgentAvatar 的 initials 分支），不阻塞这一行落地。
func TestAgentAdapter_ApplyFallsBackToInitialsWhenFetchFails(t *testing.T) {
	ctrl := gomock.NewController(t)
	state := mock_syncstate_repo.NewMockSyncStateRepo(ctrl)
	state.EXPECT().FindRow(gomock.Any(), syncwire.KindAgent, "agent-1", gomock.Any()).Return(false, nil)
	syncstate_repo.RegisterSyncState(state)

	agents := mock_agent_repo.NewMockAgentRepo(ctrl)
	var created *agent_entity.Agent
	agents.EXPECT().Create(gomock.Any(), gomock.Any()).DoAndReturn(
		func(_ context.Context, a *agent_entity.Agent) error { created = a; return nil })
	agent_repo.RegisterAgent(agents)

	hash := avatarHash(testAvatarDataURL)
	avatar := &stubAvatarTransport{getErr: errors.New("timeout")}

	err := (&agentAdapter{avatar: avatar}).apply(context.Background(), &inbound{
		Kind: syncwire.KindAgent, SyncID: "agent-1", Version: 1,
		Payload: []byte(`{"name":"Landed","avatar_hash":"` + hash + `"}`),
	}, map[string]int64{})

	require.NoError(t, err, "取头像失败不该阻塞这个 Agent 落地")
	require.NotNil(t, created)
	assert.Empty(t, created.AvatarDataURL, "降级为占位字母头像：AvatarDataURL 留空")
	assert.Equal(t, consts.ACTIVE, created.Status)
}

// applyOntoExistingAgent 跑一次「本机已有这个 Agent」的下行 apply，返回写回去的行。
func applyOntoExistingAgent(t *testing.T, existing *agent_entity.Agent, avatar avatarTransport, payload string) *agent_entity.Agent {
	t.Helper()
	ctrl := gomock.NewController(t)
	state := mock_syncstate_repo.NewMockSyncStateRepo(ctrl)
	state.EXPECT().FindRow(gomock.Any(), syncwire.KindAgent, "agent-1", gomock.Any()).DoAndReturn(
		func(_ context.Context, _, _ string, dest any) (bool, error) {
			*(dest.(*agent_entity.Agent)) = *existing
			return true, nil
		})
	syncstate_repo.RegisterSyncState(state)

	agents := mock_agent_repo.NewMockAgentRepo(ctrl)
	var updated *agent_entity.Agent
	agents.EXPECT().UpdateRow(gomock.Any(), gomock.Any()).DoAndReturn(
		func(_ context.Context, a *agent_entity.Agent) error { updated = a; return nil })
	agent_repo.RegisterAgent(agents)

	require.NoError(t, (&agentAdapter{avatar: avatar}).apply(context.Background(), &inbound{
		Kind: syncwire.KindAgent, SyncID: "agent-1", Version: 2, Payload: []byte(payload),
	}, map[string]int64{}))
	require.NotNil(t, updated)
	return updated
}

// TestAgentAdapter_ApplyGivenFetchFails_KeepsTheAvatarAlreadyHeld 取不到新头像正文时
// 不能把本机**已经有的**那份抹掉。
//
// 载荷里的 avatar_hash 非空 = 对端有自定义头像，只是这一次取正文失败（网络抖动 /
// 服务端还没落到这份内容）。此时把 AvatarDataURL 写空是净损失：本机原本那张图是好
// 的，抹掉之后 SaveMeta 又把版本推到最新，这一条再也不会重投，用户从此看到占位字母
// 头像，直到有人再编辑一次这个 Agent。留着旧图既不丢数据，也不会更错。
func TestAgentAdapter_ApplyGivenFetchFails_KeepsTheAvatarAlreadyHeld(t *testing.T) {
	existing := &agent_entity.Agent{ID: 9, Name: "Ava", AvatarDataURL: testAvatarDataURL}
	newHash := avatarHash("data:image/png;base64,QkJCQg==")

	updated := applyOntoExistingAgent(t, existing,
		&stubAvatarTransport{getErr: errors.New("timeout")},
		`{"name":"Ava","avatar_hash":"`+newHash+`"}`)

	assert.Equal(t, testAvatarDataURL, updated.AvatarDataURL,
		"取不到新正文时保留本机已有的头像，而不是抹成空")
}

// TestAgentAdapter_ApplyGivenContentHashMismatch_RejectsTheContent 内容寻址的正文
// 必须验哈希：取回来的正文哈希对不上就不能落库。
//
// 不验等于把「这份正文是什么」的决定权完全交给服务端——服务端返回任意 data: URL，
// 桌面端都会原样存进 agents.avatar_data_url 并渲染出来。验一次哈希是免费的，这正是
// 内容寻址的意义。
func TestAgentAdapter_ApplyGivenContentHashMismatch_RejectsTheContent(t *testing.T) {
	existing := &agent_entity.Agent{ID: 9, Name: "Ava"}
	askedHash := avatarHash(testAvatarDataURL)

	updated := applyOntoExistingAgent(t, existing,
		&stubAvatarTransport{getContent: "data:image/png;base64,ZXZpbA=="},
		`{"name":"Ava","avatar_hash":"`+askedHash+`"}`)

	assert.Empty(t, updated.AvatarDataURL, "正文哈希对不上就不落库")
}

// TestAgentAdapter_ApplyClearsAvatarWhenHashEmpty 对端删掉了自定义头像（载荷里
// avatar_hash 为空）时，本机也跟着清空，而不是继续留着旧内容。
func TestAgentAdapter_ApplyClearsAvatarWhenHashEmpty(t *testing.T) {
	ctrl := gomock.NewController(t)
	state := mock_syncstate_repo.NewMockSyncStateRepo(ctrl)
	state.EXPECT().FindRow(gomock.Any(), syncwire.KindAgent, "agent-1", gomock.Any()).DoAndReturn(
		func(_ context.Context, _, _ string, dest any) (bool, error) {
			row := dest.(*agent_entity.Agent)
			*row = agent_entity.Agent{
				ID: 5, AvatarDataURL: testAvatarDataURL,
				SyncMeta: syncmeta_entity.SyncMeta{SyncID: "agent-1"},
			}
			return true, nil
		})
	syncstate_repo.RegisterSyncState(state)

	agents := mock_agent_repo.NewMockAgentRepo(ctrl)
	var updated *agent_entity.Agent
	agents.EXPECT().UpdateRow(gomock.Any(), gomock.Any()).DoAndReturn(
		func(_ context.Context, a *agent_entity.Agent) error { updated = a; return nil })
	agent_repo.RegisterAgent(agents)

	avatar := &stubAvatarTransport{}
	err := (&agentAdapter{avatar: avatar}).apply(context.Background(), &inbound{
		Kind: syncwire.KindAgent, SyncID: "agent-1", Version: 2,
		Payload: []byte(`{"name":"Renamed"}`),
	}, map[string]int64{})

	require.NoError(t, err)
	require.NotNil(t, updated)
	assert.Empty(t, updated.AvatarDataURL)
	assert.Zero(t, avatar.getCalled)
}

// TestAgentAdapter_ApplyGivenNilAvatarTransport_FallsBackGracefully 单机模式
// （New(nil)）或直接构造 agentAdapter{} 的场景：没有网络出入口时不 panic，直接
// 降级为占位字母头像。
func TestAgentAdapter_ApplyGivenNilAvatarTransport_FallsBackGracefully(t *testing.T) {
	ctrl := gomock.NewController(t)
	state := mock_syncstate_repo.NewMockSyncStateRepo(ctrl)
	state.EXPECT().FindRow(gomock.Any(), syncwire.KindAgent, "agent-1", gomock.Any()).Return(false, nil)
	syncstate_repo.RegisterSyncState(state)

	agents := mock_agent_repo.NewMockAgentRepo(ctrl)
	var created *agent_entity.Agent
	agents.EXPECT().Create(gomock.Any(), gomock.Any()).DoAndReturn(
		func(_ context.Context, a *agent_entity.Agent) error { created = a; return nil })
	agent_repo.RegisterAgent(agents)

	hash := avatarHash(testAvatarDataURL)
	err := (&agentAdapter{}).apply(context.Background(), &inbound{
		Kind: syncwire.KindAgent, SyncID: "agent-1", Version: 1,
		Payload: []byte(`{"name":"Landed","avatar_hash":"` + hash + `"}`),
	}, map[string]int64{})

	require.NoError(t, err)
	assert.Empty(t, created.AvatarDataURL)
}
