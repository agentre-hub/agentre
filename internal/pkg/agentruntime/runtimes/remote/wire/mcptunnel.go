package wire

import (
	"encoding/json"
	"fmt"
	"strings"
)

// 本文件是内置工具 MCP 反向隧道「够不着发起端」时的那一条应答,daemon 侧与 desktop 侧
// 共用同一份构造(R17)。放在 wire 而不是各写一份,理由与 MCPProxyRequest/Response 放在
// 这里是同一条:隧道有两跳,两跳都可能失败,而 CLI 子进程里的 MCP 客户端分不出是哪一跳
// —— 它只能读到最终那几个字节。两边各抄一份措辞就会漂移成两种说法,模型看到的"同一件事"
// 变成两件。wire 本就是这两跳共享 JSON shape、防手抄漂移的地方。
//
// 三个失败点用的是同一句话,因为对模型来说它们是同一件事(这次工具调用够不着发起端,
// 没有结果):daemon 侧调用前解不出目标 / 调用途中目标死掉(见 handlers.NewMCPTunnelHandler),
// desktop 侧收到了却重放不了(见 remote.handleMCPProxy)。

// mcpTunnelErrorCode 是隧道够不着发起端时用的 JSON-RPC「server error」码,复用
// org/subagent/hooktool_svc 的 writeRPCError 已经在用的 -32000(工具执行期失败的通用码,
// 落在 JSON-RPC 保留的 -32000~-32099 应用错误段)——让这条代答的应答,在 MCP 客户端眼里
// 与「真服务器执行工具时失败了」的应答别无二致。
const mcpTunnelErrorCode = -32000

// mcpTunnelLabel 从隧道路径 "/mcp/<name>/..." 里取出 server 名,拼成「哪个能力不可用」
// 那半句话的主语。取不出时退化成一个通用短语而不是拼出个畸形句子。
func mcpTunnelLabel(path string) string {
	parts := strings.SplitN(strings.Trim(path, "/"), "/", 3)
	if len(parts) >= 2 && parts[0] == "mcp" && parts[1] != "" {
		return fmt.Sprintf("the %q built-in tool", parts[1])
	}
	return "this built-in tool"
}

// mcpTunnelRequestID 尽力从 MCP 请求 body 里取出 JSON-RPC 的 "id",让代答的错误应答能
// 对上号(MCP-over-HTTP 客户端按 id 关联请求/应答)。body 不是合法 JSON 或没带 id 时退化
// 成 JSON null —— 仍是一个格式合法的 JSON-RPC id,只是关联不上而已,好过直接不回。
func mcpTunnelRequestID(body []byte) json.RawMessage {
	var env struct {
		ID json.RawMessage `json:"id"`
	}
	if err := json.Unmarshal(body, &env); err != nil || len(env.ID) == 0 {
		return json.RawMessage("null")
	}
	return env.ID
}

// MCPTunnelUnavailableResponse 构造那条应答:HTTP 200 包一个 JSON-RPC error。
//
// 状态码必须是 2xx:非 2xx 会让 CLI 内嵌的 MCP 客户端把整条应答当传输层故障丢弃,body 里
// 的话模型永远读不到,会话白白等一个读不出语义的错误(R17)。200 + JSON-RPC error 正是本
// 仓库其余几个内置工具 MCP server(writeRPCError)在工具执行失败时用的形状,MCP 客户端读
// 它就是读一次普通的工具调用失败,原样喂给模型当 tool 输出。
//
// 措辞把三件事讲给模型听——哪个能力不可用、它依赖发起会话的那台桌面端在线、以及这不是
// 瞬时故障,不要重试,绕过去或留到之后。纯英文措辞:这段字符串喂给 LLM,不进 UI,不受
// AGENTS.md 的前端 i18n 规则约束(该规则管的是 react-i18next 的可见 UI 文案)。
//
// path 是隧道路径(如 /mcp/org/),body 是 CLI 发来的原始 JSON-RPC 请求体(只用来取 id)。
//
// 例外是 agrctl 的 /ctl/*（同一条隧道也载它）：agrctl 读的是 ctl 契约——失败是非 2xx +
// {"error": …}——把 200 的 JSON-RPC 信封当成一个空的成功响应（list 零条、写入「成功」）。
// 所以 /ctl/ 路径回 502 + ctl 形状的错误。
func MCPTunnelUnavailableResponse(path string, body []byte) MCPProxyResponse {
	if strings.HasPrefix(path, "/ctl/") {
		raw, _ := json.Marshal(map[string]string{
			"error": "the Agentre desktop that owns this session could not handle the request right now; try again once it is back",
		})
		return MCPProxyResponse{
			Status:  502,
			Headers: map[string][]string{"Content-Type": {"application/json"}},
			Body:    raw,
		}
	}
	env := struct {
		JSONRPC string          `json:"jsonrpc"`
		ID      json.RawMessage `json:"id"`
		Error   struct {
			Code    int    `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}{JSONRPC: "2.0", ID: mcpTunnelRequestID(body)}
	env.Error.Code = mcpTunnelErrorCode
	env.Error.Message = fmt.Sprintf(
		"%s is unavailable: it depends on the desktop client that started this session, "+
			"and that client is offline right now. This is not a transient failure — do not "+
			"retry; proceed without it, or leave this for later until the client reconnects.",
		mcpTunnelLabel(path),
	)
	// ID 来自 mcpTunnelRequestID,要么是 "null" 要么是 encoding/json 校验过的原文,
	// 其余字段都是基本类型:这里的 Marshal 没有失败路径。
	raw, _ := json.Marshal(env)
	return MCPProxyResponse{
		Status:  200,
		Headers: map[string][]string{"Content-Type": {"application/json"}},
		Body:    raw,
	}
}
