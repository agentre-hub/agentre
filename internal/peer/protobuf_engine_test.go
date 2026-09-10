package peer

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/agentre-hub/agentre/internal/pkg/llmurl"
	"github.com/agentre-hub/agentre/pkg/wire/agentrewire"
	"github.com/agentre-hub/agentre/pkg/wire/protorpc"
)

// dialProductionInbound 起一条**生产装配**的入站连接。
//
// 用生产装配而不是测试自己拼的端口集:漏挂端口正是这一族缺陷的形状,测试若自己装配
// 就永远看不见它(与 protobuf_transcriptimport_test.go 同一条理由)。
func dialProductionInbound(t *testing.T) (context.Context, *protorpc.Conn) {
	t.Helper()
	registry := NewProtobufInboundRegistry(productionProtobufInboundDeps(newDevicePortForward()))
	clientTransport, serverTransport := peerProtoPipePair()
	client := protorpc.NewConn(clientTransport, protorpc.NewRegistry())
	server := protorpc.NewConn(serverTransport, registry)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	go client.Serve(ctx)
	go server.Serve(ctx)
	// 握手已经过了:这些用例问的是这台机器答不答得出这个方法,不是鉴权。
	server.SetAuth(protorpc.AuthState{Authenticated: true, DeviceFingerprint: "sha256:caller"})
	return ctx, client
}

// 控制台的引擎面板经 executionDevice() 放行 desktop(EXECUTION_DEVICE_KINDS 含
// desktop),拿到指纹就直接拨 engine.scan —— 桌面端不挂这一族的话,用户在桌面机器上
// 按下的每一次「扫描本机 CLI」都只会撞 -32601,而前端把它 catch 成一片空列表,看起来
// 与「这台机器上一个 CLI 都没装」一模一样。
//
// 桌面端答得出它并不需要新能力:同一个 cliprober.ScanAllCLIs 本来就在
// agent_backend_svc.ScanAndCreateAgentBackends 上跑着。
func TestProductionInboundServesEngineScan(t *testing.T) {
	ctx, client := dialProductionInbound(t)

	got, err := protorpc.CallMethod(ctx, client, uint32(agentrewire.RpcMethod_RPC_METHOD_ENGINE_SCAN),
		&agentrewire.EngineScanRequest{},
		func() *agentrewire.EngineScanResponse { return &agentrewire.EngineScanResponse{} })

	require.NoError(t, err, "桌面端答不出 engine.scan:控制台在桌面机器上扫不了 CLI")
	// 只数档位、不断言命中:装没装 CLI 取决于跑测试这台机器的磁盘,而「一档都没有」
	// 说明这一族挂上了但底下的探测表是空的 —— 那是真缺陷。
	require.NotEmpty(t, got.GetItems(), "一档都没有:方法挂上了,底下的 CLI 探测表却是空的")
	for _, item := range got.GetItems() {
		require.NotEmpty(t, item.GetBackendType(), "档位没有 backend type")
		require.Contains(t, []string{"recognized", "unchecked"}, item.GetStatus(),
			"status 只有 recognized / unchecked 两种说法(与 agentred 逐字相同)")
	}
}

// cli.resolvePath 同上:enginePorts.ts 的这一处也经 executionDevice() 放行 desktop。
// 桌面端本来就有 cliprober.ResolveCLIPath(agent_backend_svc.resolveCLIPathLocal 用的
// 就是它),缺的只是把它接到线上。
func TestProductionInboundServesCLIResolvePath(t *testing.T) {
	ctx, client := dialProductionInbound(t)

	got, err := protorpc.CallMethod(ctx, client, uint32(agentrewire.RpcMethod_RPC_METHOD_CLI_RESOLVE_PATH),
		&agentrewire.CLIResolvePathRequest{Type: "claudecode"},
		func() *agentrewire.CLIResolvePathResponse { return &agentrewire.CLIResolvePathResponse{} })

	require.NoError(t, err, "桌面端答不出 cli.resolvePath")
	// 装没装 claudecode 取决于跑测试这台机器,所以钉的是两者的**一致性**:
	// found 为真时必须给得出路径,为假时必须不给 —— 一个「found=false 却带路径」
	// 的答复会让调用方把空路径写进后端配置。
	if got.GetFound() {
		require.NotEmpty(t, got.GetPath(), "found=true 却没有路径")
	} else {
		require.Empty(t, got.GetPath(), "found=false 却带回了路径")
	}
}

// 非 CLI 后端类型(builtin / 拼错的字符串)要如实报错,而不是回一个 found=false 的
// 成功答复:后者会让调用方以为「这台机器上没装」,于是去装一个根本不存在的 CLI。
func TestProductionInboundRejectsUnknownCLIType(t *testing.T) {
	ctx, client := dialProductionInbound(t)

	_, err := protorpc.CallMethod(ctx, client, uint32(agentrewire.RpcMethod_RPC_METHOD_CLI_RESOLVE_PATH),
		&agentrewire.CLIResolvePathRequest{Type: "not-a-cli"},
		func() *agentrewire.CLIResolvePathResponse { return &agentrewire.CLIResolvePathResponse{} })

	require.Error(t, err, "无效的 backend type 被当成了「没装」")
	// 这一句是这条用例的**非空证据**:方法没挂上时它同样会拿到一个 error
	// (-32601),断言若只到 require.Error 为止,就会在缺陷仍在时照样绿。
	require.NotContains(t, err.Error(), "method not found",
		"这条用例是被 -32601 满足的,不是被真正的拒绝满足的")
}

// engine.test:控制台引擎面板的「测试连接」。enginePorts.ts:774 的 testBackend 分支
// 同样经 executionDevice() 放行 desktop(:728 那一处走 onlineAgentred() 已收窄,
// 是安全的 —— 同一个方法两个调用点限定不同)。
//
// 「桌面端挂没挂上这个方法」由契约守卫(contract_guard_test.go)在**生产装配**上钉;
// 这两条钉的是它的**答话形状** —— 那一段要打库,拨号拨不到,所以供应商来源从端口进来。
//
// 未配置的供应商要回 ok=false + 一句人话,而不是一个 RPC 错误:控制台读的是
// {ok, message} 两格,一个错误应答会被它当成「这台机器不管这事」,而不是
// 「这个供应商没配好」。
func TestDesktopEngineTestAnswersUnconfiguredAsVerdict(t *testing.T) {
	handle := newDesktopEngineTest(stubEngineProviders{})

	got, err := handle(context.Background(), &agentrewire.EngineTestRequest{ProviderKey: "no-such-provider"})

	require.NoError(t, err, "「没配好」被答成了 RPC 错误")
	require.False(t, got.GetOk(), "不存在的供应商却报测试通过")
	require.NotEmpty(t, got.GetMessage(), "ok=false 却没给出原因,控制台上是一颗沉默的按钮")
}

// 配好的供应商真的会去打上游,并把耗时带回来 —— 控制台拿它显示延迟。
// 少了 latencyMs 那一格,界面上是一句「连接成功」加一片空白。
func TestDesktopEngineTestProbesUpstreamAndReportsLatency(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, err := w.Write([]byte(`{"choices":[{"message":{"content":"ok"}}]}`))
		require.NoError(t, err)
	}))
	t.Cleanup(server.Close)
	handle := newDesktopEngineTest(stubEngineProviders{
		provider: llmurl.Provider{Type: "openai-chat", BaseURL: server.URL, APIKey: "k"},
		modelID:  "gpt-test",
		ok:       true,
	})

	got, err := handle(context.Background(), &agentrewire.EngineTestRequest{ProviderKey: "configured"})

	require.NoError(t, err)
	require.True(t, got.GetOk(), "上游答了却报失败:%s", got.GetMessage())
	require.NotNil(t, got.LatencyMs, "耗时没带回来")
}

// stubEngineProviders 顶掉「取供应商配置」那一跳(生产上它打库)。
type stubEngineProviders struct {
	provider llmurl.Provider
	modelID  string
	ok       bool
}

func (s stubEngineProviders) ProviderAndModel(context.Context, string, string) (llmurl.Provider, string, bool) {
	return s.provider, s.modelID, s.ok
}
