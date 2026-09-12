package agenttool

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"slices"
	"strings"

	"github.com/agentre-hub/agentre/internal/model/entity/agent_entity"
)

// AgentLookupFunc 按 id 实时查调用者 agent(校验工具开关用);查不到返回 (nil, nil)。
type AgentLookupFunc func(ctx context.Context, agentID int64) (*agent_entity.Agent, error)

// ReadHandler 是一个只读工具的处理器:返回给 agent 的文本结果(由 server 包成 MCP text
// content)。返回 InvalidParams 之类的 *RPCError 可指定 JSON-RPC 错误码,其余错误按 -32000 上报。
type ReadHandler func(ctx context.Context, ref Ref, rawArgs json.RawMessage) (string, error)

// RPCError 是携带 JSON-RPC 错误码的错误。
type RPCError struct {
	Code    int
	Message string
}

func (e *RPCError) Error() string { return e.Message }

// InvalidParams 造一个 -32602(参数非法)错误。
func InvalidParams(msg string) error { return &RPCError{Code: -32602, Message: msg} }

// ServerConfig 是一个内置工具 MCP server 的宿主接线。骨架(令牌、信封、四个方法分支、
// GET 405、bootstrap 窗口 503、工具开关实时校验)归 server;schema 与写操作分派归宿主。
type ServerConfig struct {
	ToolKey     string                 // agenttool.KeyX:决定实时开关校验查哪个开关,以及哪些工具名属于本 server
	ServerName  string                 // initialize 回的 serverInfo.name
	Schemas     []any                  // tools/list 返回的 schema 列表
	Ready       func() bool            // bootstrap 窗口守卫:返回 false → 503(nil = 始终就绪)
	LookupAgent AgentLookupFunc        // 实时查调用者 agent
	Read        map[string]ReadHandler // 只读工具处理器表(工具名 → 处理器)
	Write       *WriteGate             // 写工具审批闸门;nil = 本 server 没有写工具
}

// Server 是三个内置工具(org / hook / subagent)共用的 MCP-over-HTTP server 骨架。
//
// 身份: 无状态签名 token,投递时塞进 mcp-config 的 Authorization header。Lookup 只验签
// (无状态),工具开关由 tools/call 时实时查 DB(LookupAgent + ToolEnabled)判定 —— 用户
// 关掉开关后旧 token 立即失效。
type Server struct {
	cfg    ServerConfig
	secret []byte // per-server HMAC 签名密钥(本机回投,进程内即可)
}

// NewServer 造一个工具 server;每个实例自持签名密钥,故令牌不跨 server 通用。
func NewServer(cfg ServerConfig) *Server {
	return &Server{cfg: cfg, secret: randSecret()}
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodGet { // claude 开 server→client SSE; 我们不推送 → 405(claude 容忍)
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	var rpc struct {
		ID     json.RawMessage `json:"id"`
		Method string          `json:"method"`
		Params struct {
			ProtocolVersion string          `json:"protocolVersion"`
			Name            string          `json:"name"`
			Arguments       json.RawMessage `json:"arguments"`
		} `json:"params"`
	}
	if err := json.NewDecoder(r.Body).Decode(&rpc); err != nil {
		writeRPCError(w, nil, -32700, "parse error")
		return
	}
	switch rpc.Method {
	case "initialize":
		pv := rpc.Params.ProtocolVersion
		if pv == "" {
			pv = "2025-06-18"
		}
		writeRPCResult(w, rpc.ID, map[string]any{
			"protocolVersion": pv,
			"serverInfo":      map[string]any{"name": s.cfg.ServerName, "version": "1"},
			"capabilities":    map[string]any{"tools": map[string]any{"listChanged": false}},
		})
	case "notifications/initialized":
		w.WriteHeader(http.StatusAccepted)
	case "tools/list":
		writeRPCResult(w, rpc.ID, map[string]any{"tools": s.cfg.Schemas})
	case "tools/call":
		s.handleToolsCall(w, r, rpc.ID, rpc.Params.Name, rpc.Params.Arguments)
	default:
		writeRPCError(w, rpc.ID, -32601, "method not found")
	}
}

// handleToolsCall 依次过鉴权(令牌)、可用性(bootstrap 窗口)、授权(工具开关实时校验)
// 三道闸,再按工具名分派到只读处理器或写工具审批闸门。
func (s *Server) handleToolsCall(w http.ResponseWriter, r *http.Request, rpcID json.RawMessage, tool string, rawArgs json.RawMessage) {
	ref, ok := s.Lookup(bearer(r))
	if !ok {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	if s.cfg.Ready != nil && !s.cfg.Ready() { // bootstrap 窗口期(RegisterDeps 未执行)的保险闸
		http.Error(w, "service unavailable", http.StatusServiceUnavailable)
		return
	}
	// 实时开关校验:用户关掉开关后旧 token 立即失效。只认令牌里的 agent。
	a, err := s.cfg.LookupAgent(r.Context(), ref.AgentID)
	if err != nil || a == nil || !a.ToolEnabled(s.cfg.ToolKey) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	if h, isRead := s.cfg.Read[tool]; isRead {
		text, err := h(r.Context(), ref, rawArgs)
		if err != nil {
			var rpcErr *RPCError
			if errors.As(err, &rpcErr) {
				writeRPCError(w, rpcID, rpcErr.Code, rpcErr.Message)
				return
			}
			writeRPCError(w, rpcID, -32000, err.Error())
			return
		}
		writeRPCResult(w, rpcID, textResult(text))
		return
	}
	if !s.isWriteTool(tool) {
		writeRPCError(w, rpcID, -32601, "unknown tool")
		return
	}
	s.cfg.Write.serve(w, r, rpcID, ref, tool, rawArgs)
}

// isWriteTool 判断 tool 是否是本 server 暴露的写工具(注册表里除只读工具之外的全部)。
func (s *Server) isWriteTool(name string) bool {
	if s.cfg.Write == nil {
		return false
	}
	if _, isRead := s.cfg.Read[name]; isRead {
		return false
	}
	def, ok := Lookup(s.cfg.ToolKey)
	if !ok {
		return false
	}
	return slices.Contains(def.ToolNames, name)
}

func bearer(r *http.Request) string {
	return strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
}

func writeRPCResult(w http.ResponseWriter, id json.RawMessage, result any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": id, "result": result})
}

func writeRPCError(w http.ResponseWriter, id json.RawMessage, code int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": id, "error": map[string]any{"code": code, "message": msg}})
}

// textResult 把一段文本包成 MCP tool result 结构。
func textResult(text string) map[string]any {
	return map[string]any{"content": []any{map[string]any{"type": "text", "text": text}}}
}
