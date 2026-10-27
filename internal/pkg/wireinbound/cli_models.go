package wireinbound

import (
	"context"

	"github.com/agentre-hub/agentre/pkg/wire/agentrewire"
	"github.com/agentre-hub/agentre/pkg/wire/protorpc"
)

// RegisterCLIModelsList 挂设备级 CLI 查询：连接必须已鉴权，端口缺席则不注册。
// 两种宿主共用闸门和错误转换，确保 CLI 原文通过 RPC error 返回。
func RegisterCLIModelsList(registry *protorpc.Registry, list func(context.Context, *agentrewire.CLIModelsListRequest) (*agentrewire.CLIModelsListResponse, error)) {
	if list == nil {
		return
	}
	protorpc.RegisterMethod(registry, uint32(agentrewire.RpcMethod_RPC_METHOD_CLI_MODELS_LIST),
		func() *agentrewire.CLIModelsListRequest { return &agentrewire.CLIModelsListRequest{} },
		Authenticated(converted(list)))
}
