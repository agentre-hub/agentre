package peer

import (
	"context"
	"strings"

	"github.com/agentre-hub/agentre/internal/daemon/handlers"
	"github.com/agentre-hub/agentre/internal/pkg/cliprober"
	"github.com/agentre-hub/agentre/internal/pkg/llmurl"
	"github.com/agentre-hub/agentre/internal/pkg/wireinbound"
	"github.com/agentre-hub/agentre/internal/service/llm_provider_svc"
	"github.com/agentre-hub/agentre/pkg/wire/agentrewire"
)

// 桌面端的引擎探测一族。
//
// 这三个方法此前只有 agentred 答得出,而控制台的引擎面板经 executionDevice() 同时
// 放行 desktop 与 agentred(EXECUTION_DEVICE_KINDS),于是在桌面机器上按下的每一次
// 扫描 / 测试 / 查路径都撞 -32601 —— 前端把它 catch 成空结果,看起来与「这台机器上
// 什么都没装」一模一样。
//
// 桌面端答得出它们并不需要新能力:同一个 cliprober 本来就在
// agent_backend_svc.ScanAndCreateAgentBackends 与 resolveCLIPathLocal 上跑着,
// 这里只是把它接到线上。

// desktopEngineScan 扫本机 $PATH 上的 CLI 后端。
func desktopEngineScan(_ context.Context, _ *agentrewire.EngineScanRequest) (*agentrewire.EngineScanResponse, error) {
	return wireinbound.EngineScanResponseFrom(cliprober.ScanAllCLIs()), nil
}

// desktopResolveCLIPath 查某个后端类型在本机的可执行文件绝对路径。
//
// 非 CLI 类型(builtin / 拼错的字面量)如实报错,而不是回一个 found=false 的成功答复:
// 后者会让调用方以为「这台机器上没装」,于是去装一个根本不存在的 CLI。折错误上线走
// ConvertError,与 agentred 的 protobufError 同一条映射 —— 两侧对同一个输入答同一句话。
func desktopResolveCLIPath(_ context.Context, request *agentrewire.CLIResolvePathRequest) (*agentrewire.CLIResolvePathResponse, error) {
	path, found, err := cliprober.ResolveCLIPath(request.GetType())
	if err != nil {
		return nil, wireinbound.ConvertError(err)
	}
	return &agentrewire.CLIResolvePathResponse{Path: path, Found: found}, nil
}

// desktopEngineProviders 是桌面端这一侧的供应商来源:库(llm_provider_svc),
// 而不是 agentred 的 daemon state。
//
// 只换这一跳:探测、计时,以及「未配置」与「上游失败」两句话仍由
// handlers.EngineHandlers.Test 共用 —— 控制台把 message 原样显示给用户,
// 两种执行端对同一个输入必须说同一句话。
type desktopEngineProviders struct{}

func (desktopEngineProviders) ProviderAndModel(ctx context.Context, providerKey, modelKey string) (llmurl.Provider, string, bool) {
	// ResolveTarget 已经把「找不到 / 未启用 / 没有默认模型 / 模型不属于这个供应商」
	// 全部判成错误。在这一族里它们都是同一件事:没配好。
	resolved, err := llm_provider_svc.LLMProvider().ResolveTarget(ctx,
		llm_provider_svc.ModelTarget{ProviderKey: providerKey, ModelKey: modelKey})
	if err != nil || resolved == nil || strings.TrimSpace(resolved.APIKey) == "" {
		return llmurl.Provider{}, "", false
	}
	return llmurl.Provider{Type: resolved.ProviderType, BaseURL: resolved.BaseURL, APIKey: resolved.APIKey}, resolved.ModelID, true
}

// newDesktopEngineTest 造 engine.test 的处理函数。供应商来源从参数进来,生产装配给
// 的是库,测试给的是桩 —— 这一族的答话形状因此测得动,而不必先支起一个库。
//
// 只装了供应商端口:Scan 与 ResolveCLIPath 各自直接走 cliprober,Discover 桌面端
// 不挂(控制台不对 desktop 发它)。
//
// 上游失败不是 RPC 错误:控制台读的是 {ok, message} 两格,一个错误应答会被它当成
// 「这台机器不管这事」,而不是「这个供应商没配好」——— 与 agentred 同一条判断。
func newDesktopEngineTest(providers handlers.EngineProviderPort) func(context.Context, *agentrewire.EngineTestRequest) (*agentrewire.EngineTestResponse, error) {
	engine := handlers.NewEngineHandlers(handlers.EngineDeps{Providers: providers})
	return func(ctx context.Context, request *agentrewire.EngineTestRequest) (*agentrewire.EngineTestResponse, error) {
		result, err := engine.Test(ctx, handlers.EngineTestParams{
			ProviderKey: request.GetProviderKey(),
			ModelKey:    request.GetModelKey(),
		})
		if err != nil {
			return nil, wireinbound.ConvertError(err)
		}
		return &agentrewire.EngineTestResponse{Ok: result.OK, Message: result.Message, LatencyMs: result.LatencyMs}, nil
	}
}
