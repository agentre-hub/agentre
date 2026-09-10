package peer

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/agentre-hub/agentre/pkg/wire/agentrewire"
	"github.com/agentre-hub/agentre/pkg/wire/protorpc"
)

// TestProductionInboundServesTranscriptImportScan 钉住「控制台的『导入历史会话』对
// kind=desktop 的机器打得通」。
//
// 机器清单里桌面端与 agentred 并列(useMachineReachability.tsx),server 后端拿到指纹
// 就直接拨 transcriptImport.*,全链路不筛 kind —— 桌面端不挂这一族方法的话,用户按下
// 的每一次导入都只会撞 -32601,而这条链路上没有任何一处会把它翻成人话。
//
// 断言打在**生产装配**上(productionProtobufInboundDeps + NewProtobufInboundRegistry),
// 不是一份测试专用的注册面:漏挂端口正是这条缺陷的形状,测试若自己装配就永远看不见它。
func TestProductionInboundServesTranscriptImportScan(t *testing.T) {
	registry := NewProtobufInboundRegistry(productionProtobufInboundDeps())
	clientTransport, serverTransport := peerProtoPipePair()
	client := protorpc.NewConn(clientTransport, protorpc.NewRegistry())
	server := protorpc.NewConn(serverTransport, registry)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go client.Serve(ctx)
	go server.Serve(ctx)
	// 握手已经过了:这条测试问的是这台机器答不答得出这个方法,不是鉴权。
	server.SetAuth(protorpc.AuthState{Authenticated: true, DeviceFingerprint: "sha256:caller"})

	got, err := protorpc.CallMethod(ctx, client, uint32(agentrewire.RpcMethod_RPC_METHOD_TRANSCRIPT_IMPORT_SCAN),
		&agentrewire.TranscriptImportScanRequest{},
		func() *agentrewire.TranscriptImportScanResponse { return &agentrewire.TranscriptImportScanResponse{} })

	require.NoError(t, err, "桌面端答不出 transcriptImport.scan:控制台的导入历史会话在这台机器上根本打不通")
	// 逐档答话就是「读取器来自全局注册表」的证据:这张表由各 CLI runtime 包的 init()
	// 填,桌面端进程里本来就是满的。某台机器上没装那个 CLI 时那一档答 unavailable,
	// 仍然是一档 —— 所以这里只数档位,不断言候选内容(那取决于跑测试这台机器的磁盘)。
	require.NotEmpty(t, got.GetBackends(), "一档都没有:这一族挂上了,底下的读取器却是空的")
}
