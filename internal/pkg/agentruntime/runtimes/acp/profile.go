package acp

import (
	"errors"
	"fmt"
	"strings"

	"github.com/agentre-hub/agentre/internal/model/entity/agent_backend_entity"
	"github.com/agentre-hub/agentre/internal/pkg/agentruntime"
)

// backendProfile 是同一套 ACP runtime 在不同后端类型之间的全部差异:池键里的类型、
// 怎么起进程、env 怎么装、握手后要不要认证、模型由谁报。会话复用、协议握手、
// 翻译、审批、中断、续话一律共用,不复制第二份。
type backendProfile struct {
	backendType agent_backend_entity.BackendType
	// launch 交回可执行文件与 argv。
	launch func(b *agent_backend_entity.AgentBackend) (command string, args []string, err error)
	// env 装配子进程要**写入**的变量;unsetEnv 是要先从继承环境里删掉的键。
	env      func(req agentruntime.RunRequest) (map[string]string, error)
	unsetEnv []string
	// authMethod 非空 = initialize 之后、开会话之前必须 authenticate 这个方法。
	authMethod string
	// reportsModel = 模型由 Agentre 选、经网关生效,RunResult.Model 报本轮解析出的模型。
	reportsModel bool
	// binaryNotFound 造「可执行文件不存在」的错误,带上实际尝试的 command;
	// nil 退回通用 acp 的 errAgentBinaryNotFound。
	binaryNotFound func(command string) error
}

// acpProfile 是通用 acp 后端:用户给的 acpCommand + acpArgs,agent 自带
// provider/model/凭证,不认证、不注入网关变量、不报模型。
var acpProfile = backendProfile{
	backendType: agent_backend_entity.TypeACP,
	launch: func(b *agent_backend_entity.AgentBackend) (string, []string, error) {
		command := strings.TrimSpace(b.ACPCommand)
		if command == "" {
			return "", nil, errors.New("acp runtime: backend has no acpCommand configured")
		}
		return command, b.ACPArgs, nil
	},
	env: func(req agentruntime.RunRequest) (map[string]string, error) {
		return agentruntime.BuildACPEnv(req.Backend, agentruntime.CLIDeps{Ctl: req.CtlCredentials(), Session: req.ConversationID})
	},
}

// antigravityDefaultBinary / antigravityAuthMethod:agy 默认按 PATH 查找,经
// gemini-api-key 认证(GEMINI_API_KEY 里是会话网关 token,agy 不校验它,真正的
// 校验在网关)。没有这一步 agy 的 session/new 回 -32000 "Authentication required"。
const (
	antigravityDefaultBinary = "agy"
	antigravityAuthMethod    = "gemini-api-key"
)

// antigravityProfile 是 agy(`agy acp`):绑定 gemini 供应商,LLM 流量经 Agentre 网关,
// 用户在 Agentre 里选的模型就是实际调用的模型(网关按 token 的目标改写路径模型)。
var antigravityProfile = backendProfile{
	backendType: agent_backend_entity.TypeAntigravity,
	launch: func(b *agent_backend_entity.AgentBackend) (string, []string, error) {
		// cli_path 已含按设备覆盖(agentred 在选 runtime 前把覆盖写进 be.CLIPath)。
		command := strings.TrimSpace(b.CLIPath)
		if command == "" {
			command = antigravityDefaultBinary
		}
		return command, []string{"acp"}, nil
	},
	env: func(req agentruntime.RunRequest) (map[string]string, error) {
		return agentruntime.BuildAntigravityEnv(req.Backend, agentruntime.CLIDeps{
			Token:      req.GatewayToken,
			GatewayURL: req.GatewayURL,
			Ctl:        req.CtlCredentials(),
			Session:    req.ConversationID,
		})
	},
	binaryNotFound: func(command string) error {
		return fmt.Errorf("agy executable not found: %s", command)
	},
	unsetEnv:     agentruntime.AntigravityClearedEnv(),
	authMethod:   antigravityAuthMethod,
	reportsModel: true,
}
