package handlers

import (
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/agentre-hub/agentre/internal/pkg/agentruntime"
	"github.com/agentre-hub/agentre/internal/pkg/agentruntime/runtimes/remote/wire"
	"github.com/agentre-hub/agentre/internal/pkg/conversationid"
)

// daemonGatewayBase 取 daemon 本机 gateway base URL;gateway 未装配(测试 / 未启)时返回空,
// rewriteMCPServersForDaemon 据此保守保留原 URL。
func daemonGatewayBase(g GatewayPort) string {
	if g == nil {
		return ""
	}
	return g.URL()
}

// rewriteMCPServersForDaemon rewrites each desktop MCP server URL to the daemon
// gateway and embeds its originating (peerFingerprint, conversationId) query
// pair. This lets the local tunnel resolve the initiating connection exactly
// rather than guessing from a global active connection. Paths, headers, tools,
// and names remain unchanged; desktop validates the original authorization token.
// 返回新 slice,不就地改入参;daemonBaseFn 惰性求值 —— 无 MCP server 时根本不取(也就不
// 触碰 gateway),base 为空 / 解析失败时保守返回原 specs。
//
// 这条 URL 随 --mcp-config 交给 claude-code / codex 子进程:它是本轮唯一一处对话身份
// 离开 daemon 进程边界的地方,写进去与解回来必须是同一个值(见 NewMCPTunnelHandler)。
func rewriteMCPServersForDaemon(specs []agentruntime.MCPServerSpec, daemonBaseFn func() string, peerFingerprint string, conversationID string) []agentruntime.MCPServerSpec {
	if len(specs) == 0 || daemonBaseFn == nil {
		return specs
	}
	daemonBase := daemonBaseFn()
	if daemonBase == "" {
		return specs
	}
	base, err := url.Parse(daemonBase)
	if err != nil || base.Host == "" {
		return specs
	}
	out := make([]agentruntime.MCPServerSpec, len(specs))
	for i, s := range specs {
		out[i] = s
		u, perr := url.Parse(s.URL)
		if perr != nil {
			continue // 解析失败保留原样,不丢这条 server
		}
		u.Scheme = base.Scheme
		u.Host = base.Host
		query := u.Query()
		query.Set("peerFingerprint", peerFingerprint)
		query.Set("conversationId", conversationID)
		u.RawQuery = query.Encode()
		out[i].URL = u.String()
	}
	return out
}

// hopByHopTunnelHeaders 是不该跨隧道转发的逐跳头(+ Host/Content-Length,desktop 重放时
// 由 http.Client 按目标 URL / body 重算)。其余头(Authorization / Content-Type / Accept /
// Mcp-* 等)原样转发。
var hopByHopTunnelHeaders = map[string]bool{
	"Host": true, "Content-Length": true, "Connection": true, "Keep-Alive": true,
	"Transfer-Encoding": true, "Te": true, "Trailer": true, "Upgrade": true, "Proxy-Connection": true,
}

func sanitizeTunnelHeaders(h http.Header) map[string][]string {
	if len(h) == 0 {
		return nil
	}
	out := make(map[string][]string, len(h))
	for k, vs := range h {
		if hopByHopTunnelHeaders[http.CanonicalHeaderKey(k)] {
			continue
		}
		cp := make([]string, len(vs))
		copy(cp, vs)
		out[k] = cp
	}
	return out
}

// NewMCPTunnelHandler 返回挂在 daemon 本机 gateway /mcp/ 上的隧道入口:把 CLI 子进程的
// MCP HTTP 请求装包,经 NotifierPort 反向请求(MethodMCPProxy)隧道回 desktop 执行,再把
// 应答原样写回 CLI。MCP-over-HTTP 是纯请求/应答,单帧足够。
//
// notifierFn resolves the peer/conversation identity embedded by the daemon in
// the local MCP URL query. The exact (peerFingerprint, conversationId) pair
// selects its originating live connection; malformed, unknown, or offline
// origins have no fallback target and must not be cross-routed to another client.
//
// 隧道够不着发起会话的桌面端时,不能回裸 HTTP 错误。够不着有两种:调用之前就解不出目标
// (桌面端已离线),以及解出了目标、请求也发出去了,桌面端却在答复之前死掉(rpc.ErrConnClosed)
// —— 对模型来说是同一件事,因此答的也是同一句话。裸 HTTP 之所以不行:非 2xx 状态码会让 CLI 内嵌的
// MCP 客户端把整条应答当传输层故障丢弃,body 里的话模型永远读不到,会话也因此白白等一个
// 读不出语义的错误(R17)。改为 HTTP 200 包一个 JSON-RPC error:这正是本仓库其余几个内置
// 工具 MCP server(org/subagent/hooktool_svc 的 writeRPCError)在工具执行失败时使用的
// 形状,MCP 客户端读它就是读一次普通的工具调用失败,原样喂给模型当 tool 输出——而不是让
// CLI 报一个模型看不懂的基础设施错误。见 writeMCPTunnelUnavailable。
func NewMCPTunnelHandler(notifierFn func(peerFingerprint string, conversationID string) NotifierPort) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			http.Error(w, "mcp tunnel: read body", http.StatusBadRequest)
			return
		}
		peerFingerprint := r.URL.Query().Get("peerFingerprint")
		conversationID := r.URL.Query().Get("conversationId")
		var n NotifierPort
		if peerFingerprint != "" && conversationid.Validate(conversationID) == nil {
			n = notifierFn(peerFingerprint, conversationID)
		}
		if n == nil {
			// 降级分支必须留痕(observability.md 强制埋点 3):这条应答只进 CLI 子进程,
			// 发起端按定义已经离线,daemon 日志是事后唯一能回答「为什么 agent 说这个工具
			// 不可用」的地方。只记路径 —— body 里可能有工具入参,整包不进日志。
			log.Printf("mcpproxy.tunnel: no target, answered LLM-readable unavailable error path=%s", strconv.Quote(r.URL.Path))
			writeMCPTunnelUnavailable(w, r.URL.Path, body)
			return
		}
		req := wire.MCPProxyRequest{
			Path:    r.URL.Path,
			Method:  r.Method,
			Headers: sanitizeTunnelHeaders(r.Header),
			Body:    body,
		}
		var resp wire.MCPProxyResponse
		if err := n.Request(r.Context(), wire.MethodMCPProxy, req, &resp); err != nil {
			// 目标是解出来了,请求也发出去了,但这一趟没走完(桌面端在调用途中被杀 /
			// 链路被 RST → rpc.ErrConnClosed,或对端答不了这个方法)。对模型来说这与
			// 「调用之前就没有目标」是同一件事:发起端此刻够不着,这次工具调用没结果。
			// 所以答的是同一句话,而不是把传输层错误糊成裸 HTTP —— 非 2xx 会让 MCP 客户端
			// 把整条应答当传输层故障丢弃(R17)。真实原因只留在下面这行日志里。
			log.Printf("mcpproxy.tunnel: target lost mid-call, answered LLM-readable unavailable error for %s err=%v",
				mcpTunnelUnavailableLabel(r.URL.Path), err)
			writeMCPTunnelUnavailable(w, r.URL.Path, body)
			return
		}
		for k, vs := range resp.Headers {
			for _, v := range vs {
				w.Header().Add(k, v)
			}
		}
		if resp.Status == 0 {
			resp.Status = http.StatusOK
		}
		w.WriteHeader(resp.Status)
		_, _ = w.Write(resp.Body)
	})
}

// mcpTunnelUnavailableLabel 从隧道路径 "/mcp/<name>/..." 里取出 server 名,拼成
// 「哪个能力不可用」那半句话的主语,只用于下面那行日志——让日志里的指代与答给模型的那句话
// 对得上。答给模型的那句话由 wire.MCPTunnelUnavailableResponse 独家构造(桌面端那一跳共用
// 同一份),这里不重复拼装它。取不出 server 名时退化成一个通用短语而不是拼出个畸形句子。
func mcpTunnelUnavailableLabel(path string) string {
	parts := strings.SplitN(strings.Trim(path, "/"), "/", 3)
	if len(parts) >= 2 && parts[0] == "mcp" && parts[1] != "" {
		return fmt.Sprintf("the %q built-in tool", parts[1])
	}
	return "this built-in tool"
}

// writeMCPTunnelUnavailable 见 NewMCPTunnelHandler 顶部注释:HTTP 200 + JSON-RPC error。
// 应答由 wire.MCPTunnelUnavailableResponse 构造 —— 隧道的另一跳(桌面端收到了却重放不了,
// 见 remote.handleMCPProxy)答的是同一份,CLI 里的 MCP 客户端分不出是哪一跳失败的,两边
// 各拼一份措辞就会漂移成两种说法。这里只负责把它写成 HTTP 应答。
func writeMCPTunnelUnavailable(w http.ResponseWriter, path string, body []byte) {
	resp := wire.MCPTunnelUnavailableResponse(path, body)
	for k, vs := range resp.Headers {
		for _, v := range vs {
			w.Header().Add(k, v)
		}
	}
	w.WriteHeader(resp.Status)
	_, _ = w.Write(resp.Body)
}
