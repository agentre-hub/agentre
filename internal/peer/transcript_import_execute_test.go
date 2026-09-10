package peer

import (
	"context"
	"testing"

	"github.com/cago-frame/cago/pkg/i18n"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"github.com/agentre-hub/agentre/internal/pkg/code"
	importwire "github.com/agentre-hub/agentre/internal/pkg/transcriptimport/wire"
	"github.com/agentre-hub/agentre/internal/service/chat_import_svc"
	"github.com/agentre-hub/agentre/internal/service/remote_device_svc"
	"github.com/agentre-hub/agentre/internal/service/remote_device_svc/mock_remote_device_svc"
	"github.com/agentre-hub/agentre/pkg/wire/protorpc"
)

// transcript_import_execute_test.go 钉住 transcriptimport.execute 在桌面端这一侧的
// 判断:号照用调用方铸的、Agent 按同步标识认、重复导入收敛到库里那条、点名别的
// origin 一律拒。「转录怎么落成一条会话」不在这里 —— 那归 chat_import_svc,本层
// 只证明它把线上的入参如实翻成了那一层的请求,没有绕过它另写一条导入。

const (
	execConvID    = "0199a0b1-c2d3-7e4f-8a9b-0c1d2e3f4a5b"
	execStoredID  = "0199a0b1-c2d3-7e4f-8a9b-000000000088"
	execAgentSync = "agent-sync-abc"
)

// fakeChatImport 只顶 Import 这一格:记下收到的请求,答一份预置结果。
type fakeChatImport struct {
	chat_import_svc.ChatImportSvc
	got  *chat_import_svc.ImportRequest
	resp *chat_import_svc.ImportResponse
	err  error
}

func (f *fakeChatImport) Import(_ context.Context, req *chat_import_svc.ImportRequest, _ chat_import_svc.ProgressFunc) (*chat_import_svc.ImportResponse, error) {
	f.got = req
	return f.resp, f.err
}

func newExecutePort(imports chat_import_svc.ChatImportSvc) *desktopTranscriptImport {
	port := newDesktopTranscriptImport()
	port.imports = func() chat_import_svc.ChatImportSvc { return imports }
	return port
}

// withDesktopFingerprint 把本机指纹钉成确定值,「点名 origin」那道门才有得比。
func withDesktopFingerprint(t *testing.T, fingerprint string) {
	t.Helper()
	ctrl := gomock.NewController(t)
	device := mock_remote_device_svc.NewMockRemoteDeviceSvc(ctrl)
	prev := remote_device_svc.Default()
	remote_device_svc.SetDefault(device)
	t.Cleanup(func() {
		remote_device_svc.SetDefault(prev)
		ctrl.Finish()
	})
	device.EXPECT().DeviceFingerprint().Return(fingerprint, nil).AnyTimes()
}

func executeParams() importwire.ExecuteParams {
	return importwire.ExecuteParams{
		Backend:        "claudecode",
		Locator:        "projects/x/sess.jsonl",
		ConversationID: execConvID,
		AgentSyncID:    execAgentSync,
		// 历史兼容那一格照发:跨机它是发起端库里的自增主键,本机不该采信。
		AgentID: 999,
	}
}

// TestTranscriptImportExecute_UsesCallerMintedIDAndSyncAgent:发起端铸号、对端从不
// 发号(与 runtime.run 同一条规矩),认 Agent 只看账号级同步标识。
func TestTranscriptImportExecute_UsesCallerMintedIDAndSyncAgent(t *testing.T) {
	imports := &fakeChatImport{resp: &chat_import_svc.ImportResponse{
		SessionID: 55, ConversationID: execConvID, ProviderSessionID: "prov-sess-1",
		Title: "修一个 bug", Cwd: "/work/x", ImportedTurns: 2,
	}}

	got, err := newExecutePort(imports).Execute(context.Background(), executeParams())

	require.NoError(t, err)
	require.NotNil(t, imports.got)
	assert.Equal(t, execConvID, imports.got.ConversationID, "号照用调用方铸的那个")
	assert.Equal(t, execAgentSync, imports.got.AgentSyncID)
	assert.Zero(t, imports.got.AgentID, "线上送来的本地主键一格都不往下传:跨机同号 Agent 会静默错位")
	assert.Zero(t, imports.got.DeviceID, "转录在本机磁盘上")
	assert.Empty(t, imports.got.Cwd, "留空 = 用磁盘转录里记的 cwd,续跑要起在原目录")
	assert.Zero(t, imports.got.ProjectID, "wire 上没有项目这一格,没人替用户挑 → 自由会话")

	assert.Equal(t, execConvID, got.ConversationID)
	assert.Equal(t, "prov-sess-1", got.ProviderSessionID)
	assert.Equal(t, "修一个 bug", got.Title)
	assert.Equal(t, "/work/x", got.Cwd)
	assert.Equal(t, 2, got.Turns)
	assert.False(t, got.AlreadyImported)
}

// TestTranscriptImportExecute_GivenAlreadyImported_ThenConvergesOnTheStoredOne:
// 重复导入是可预期的正常分支,不是错误。交回的是**库里那条**的身份(调用方手上
// 只有它刚铸的号),轮数为 0 —— 一行都没再写。
func TestTranscriptImportExecute_GivenAlreadyImported_ThenConvergesOnTheStoredOne(t *testing.T) {
	imports := &fakeChatImport{resp: &chat_import_svc.ImportResponse{
		SessionID: 88, ConversationID: execStoredID, ProviderSessionID: "prov-sess-1",
		Title: "早就导过的那条", Cwd: "/work/x", AlreadyImported: true,
	}}

	got, err := newExecutePort(imports).Execute(context.Background(), executeParams())

	require.NoError(t, err)
	assert.True(t, got.AlreadyImported)
	assert.Equal(t, execStoredID, got.ConversationID, "指的是库里那条,不是本次铸的号")
	assert.Equal(t, "早就导过的那条", got.Title)
	assert.Zero(t, got.Turns, "一行都没再写")
}

// TestTranscriptImportExecute_GivenUnknownAgentSyncID_ThenInvalidParams:标识在这台
// 机器上解不出是**调用方的参数在这里不成立**,不是本机故障。答 -32603 只会把人引去
// 查目标机器的日志。
func TestTranscriptImportExecute_GivenUnknownAgentSyncID_ThenInvalidParams(t *testing.T) {
	imports := &fakeChatImport{err: chat_import_svc.ErrAgentNotFound}

	got, err := newExecutePort(imports).Execute(context.Background(), executeParams())

	require.Nil(t, got)
	var rpcErr *protorpc.Error
	require.ErrorAs(t, err, &rpcErr)
	assert.Equal(t, protorpc.CodeInvalidParams, rpcErr.Code)
}

// TestTranscriptImportExecute_GivenNoAgentSyncID_ThenRefusesBeforeImporting:缺标识时
// 不退回 AgentID —— 采信那个号正是这条分支要防的跨机撞号。
func TestTranscriptImportExecute_GivenNoAgentSyncID_ThenRefusesBeforeImporting(t *testing.T) {
	imports := &fakeChatImport{}
	params := executeParams()
	params.AgentSyncID = ""

	_, err := newExecutePort(imports).Execute(context.Background(), params)

	var rpcErr *protorpc.Error
	require.ErrorAs(t, err, &rpcErr)
	assert.Equal(t, protorpc.CodeInvalidParams, rpcErr.Code)
	assert.Nil(t, imports.got, "连导入都不该发起")
}

// TestTranscriptImportExecute_GivenMalformedConversationID_ThenInvalidParams:
// 号不合法在碰任何存储之前就挡下 —— 它是「这不是一条对话身份」与「这条对话不在
// 本机」的分界。
func TestTranscriptImportExecute_GivenMalformedConversationID_ThenInvalidParams(t *testing.T) {
	imports := &fakeChatImport{}
	params := executeParams()
	params.ConversationID = "42"

	_, err := newExecutePort(imports).Execute(context.Background(), params)

	var rpcErr *protorpc.Error
	require.ErrorAs(t, err, &rpcErr)
	assert.Equal(t, protorpc.CodeInvalidParams, rpcErr.Code)
	assert.Nil(t, imports.got)
}

// TestTranscriptImportExecute_PeerFingerprintNomination 钉住点名 origin 这道门:
// 语义与 runtime.run 的同名字段一致 —— 省略 = 调用方自己的对端,点名自己等价于省略,
// 点名**别人**是账号级能力,配对身份一律被拒。桌面端只认得自己这一个 origin:导进来
// 的会话就落在这台机器上。
func TestTranscriptImportExecute_PeerFingerprintNomination(t *testing.T) {
	withDesktopFingerprint(t, "sha256:desktop")
	for _, tc := range []struct {
		name        string
		fingerprint string
		allowed     bool
	}{
		{"省略即本机", "", true},
		{"点名本机等价于省略", "sha256:desktop", true},
		{"点名别的 origin 被拒", "sha256:another-machine", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			imports := &fakeChatImport{resp: &chat_import_svc.ImportResponse{
				SessionID: 55, ConversationID: execConvID,
			}}
			params := executeParams()
			params.PeerFingerprint = tc.fingerprint

			_, err := newExecutePort(imports).Execute(context.Background(), params)

			if tc.allowed {
				require.NoError(t, err)
				require.NotNil(t, imports.got)
				return
			}
			var rpcErr *protorpc.Error
			require.ErrorAs(t, err, &rpcErr)
			assert.Equal(t, int32(-32001), rpcErr.Code)
			assert.Nil(t, imports.got, "越权的请求一行都不该写")
		})
	}
}

// TestImportExecuteError_SpeaksTheSameThreeStateAsAgentred:桌面端与 agentred 用同一
// 套线上词汇回答失败。不翻的话每一种失败都是 -32603,「这台机器上没装那个 CLI」与
// 「转录文件没了」在控制台上长成同一句话,而两条各有各的出路。
func TestImportExecuteError_SpeaksTheSameThreeStateAsAgentred(t *testing.T) {
	ctx := context.Background()
	require.ErrorIs(t, importExecuteError(i18n.NewError(ctx, code.ChatImportBackendUnavailable)),
		importwire.ErrBackendUnavailable)
	require.ErrorIs(t, importExecuteError(i18n.NewError(ctx, code.ChatImportTranscriptOpenFailed)),
		importwire.ErrTranscriptOpen)
}
