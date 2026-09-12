package wireinbound

import (
	"context"

	"github.com/agentre-hub/agentre/internal/pkg/cliprober"

	"github.com/agentre-hub/agentre/pkg/wire/agentrewire"
	"github.com/agentre-hub/agentre/pkg/wire/protorpc"
)

// EngineDeps 是引擎探测一族(engine.* / cli.resolvePath)的端口。
//
// **闸门是端口,不写死在这里。** 两种执行端这一族的准入并不相同:agentred 的
// engine.scan / engine.test 要求「账号已登录」(requireProtobufLoggedIn),
// cli.resolvePath 只要求连接已鉴权(requireProtobufAuth)。把某一句写死进共用注册面,
// 就会在某一侧悄悄放宽或收紧准入 —— 而这一族读的是本机 $PATH 与供应商凭据。
//
// **端口缺席就不注册**,与外围族、会话族同一条纪律:「这台机器答不出这个方法」在
// 协议上只有一种说法 —— method not found。调用方据此判定「换台机器」;若改成回一个
// 空成功,它会把「没装 CLI」和「这台机器不管这事」读成同一件事。
type EngineDeps struct {
	// Auth 是这一族的族闸门。它必须给,否则整族不挂:一个忘了装闸门的注册面,
	// 会让任何拨得进来的连接读到本机 $PATH 上的 CLI 清单与绝对路径。
	Auth func(context.Context) error
	// Scan 报告本机各 CLI 后端「认不认得出」。回包里只有 backendType 与 status,
	// 没有路径 —— 路径是本机磁盘布局,浏览器不需要也不该拿到。
	Scan func(context.Context, *agentrewire.EngineScanRequest) (*agentrewire.EngineScanResponse, error)
	// Test 拿供应商配置对上游打一次最小请求。它与 Scan 分开挂,是因为两者的
	// 数据来源不同:Scan 读磁盘,Test 读本机存的供应商凭据。
	Test func(context.Context, *agentrewire.EngineTestRequest) (*agentrewire.EngineTestResponse, error)
	// ResolveCLIPath 在本机 CLI 搜索路径里查某个后端类型的可执行文件绝对路径。
	// 与 Scan 不同,这一条**要**给出路径:调用方拿它去写后端配置。
	ResolveCLIPath func(context.Context, *agentrewire.CLIResolvePathRequest) (*agentrewire.CLIResolvePathResponse, error)
}

// RegisterEngineMethods 把引擎探测一族挂上。两种执行端共用这一处注册,所以
// 「对面认不认识这个方法」不取决于对面是 agentred 还是桌面端 —— 调用方不必先猜,
// 也不必按 kind 分两套目标筛选。
func RegisterEngineMethods(registry *protorpc.Registry, deps EngineDeps) {
	if deps.Auth == nil {
		return
	}
	if deps.Scan != nil {
		protorpc.RegisterMethod(registry, uint32(agentrewire.RpcMethod_RPC_METHOD_ENGINE_SCAN),
			func() *agentrewire.EngineScanRequest { return &agentrewire.EngineScanRequest{} },
			gated(deps.Auth, deps.Scan))
	}
	if deps.Test != nil {
		protorpc.RegisterMethod(registry, uint32(agentrewire.RpcMethod_RPC_METHOD_ENGINE_TEST),
			func() *agentrewire.EngineTestRequest { return &agentrewire.EngineTestRequest{} },
			gated(deps.Auth, deps.Test))
	}
	if deps.ResolveCLIPath != nil {
		protorpc.RegisterMethod(registry, uint32(agentrewire.RpcMethod_RPC_METHOD_CLI_RESOLVE_PATH),
			func() *agentrewire.CLIResolvePathRequest { return &agentrewire.CLIResolvePathRequest{} },
			gated(deps.Auth, deps.ResolveCLIPath))
	}
}

// EngineScanResponseFrom 把本机 CLI 探测结果折成线上的应答。
//
// 两种执行端共用它,所以状态词汇("recognized" / "unchecked")只有一处定义:调用方
// 按这两个字面量分支,任何一侧多写一个词都会让另一侧的界面读不出来。
//
// **路径被刻意丢掉。** 探测结果里带着本机绝对路径,而回包只说认不认得出 ——
// 磁盘布局是这台机器的事,浏览器既不需要也不该拿到(与 agentred 同一条判断)。
func EngineScanResponseFrom(probes []cliprober.CLIProbeResult) *agentrewire.EngineScanResponse {
	response := &agentrewire.EngineScanResponse{Items: make([]*agentrewire.EngineScanItem, 0, len(probes))}
	for _, probe := range probes {
		response.Items = append(response.Items, &agentrewire.EngineScanItem{BackendType: probe.BackendType, Status: EngineScanStatus(probe.Found)})
	}
	return response
}

// EngineScanStatus 是这一格在线上的**全部两种说法**。调用方按这两个字面量分支,
// 任何一侧多写一个词,另一侧的界面就读不出来 —— 所以它只在这里定义一次。
func EngineScanStatus(found bool) string {
	if found {
		return "recognized"
	}
	return "unchecked"
}
