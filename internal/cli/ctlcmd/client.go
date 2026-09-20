package ctlcmd

import (
	"github.com/agentre-hub/agentre/internal/pkg/ctlclient"
)

// endpoint 已解析的控制端点(base URL + bearer token)。连接层住在
// internal/pkg/ctlclient,与 agrctl acp 共用同一份解析/请求实现。
type endpoint = ctlclient.Endpoint

// resolveEndpoint 按优先级解析控制端点：flag > 环境变量 > AppDataDir 握手文件。
func resolveEndpoint(flagURL, flagToken string, lookupEnv func(string) (string, bool)) (endpoint, error) {
	return ctlclient.Resolve(flagURL, flagToken, lookupEnv)
}
