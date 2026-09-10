// Package fake 提供 e2e 专用的确定性 agent runtime:不起任何子进程,按 req.UserText
// 回显一段固定前缀文本后正常结束。只有独立 E2E composition root 导入本包。
package fake

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/agentre-hub/agentre/internal/pkg/agentruntime"
	"github.com/agentre-hub/agentre/internal/pkg/agentruntime/capability"
)

// ReplyPrefix 是所有假回复的前缀,前端据此断言并与用户消息区分。
const ReplyPrefix = "e2e-fake-reply: "

// ContextWindowTokens 是 fake 上报的模型上下文窗口。
//
// 必须上报,否则 chat_svc.resolveContextWindowWithRuntime 三级兜底全落空
// (session 无值 → provider 无值 → llmcatalog 查不到 "e2e-fake-model"),
// session.ContextWindow 留 0,前端底栏 `contextUsage.max > 0` 为假,
// ContextMeter 整块不渲染 —— e2e 里就没法观测上下文相关的任何行为。
//
// 取 1M 而不是随便一个数:顺带让 formatTokens 的 M 档在真实 app 里被走到。
const ContextWindowTokens = 1_000_000

// SystemAssertDirectivePrefix 触发 system prompt 可观测断言:e2e-assert-system:<needle>。
const SystemAssertDirectivePrefix = "e2e-assert-system:"

// CwdAssertDirectivePrefix 触发本轮工作目录断言:e2e-assert-cwd:<absolute path>。
// 它只回显 RunRequest.Cwd 是否命中，让 E2E 在 runtime 所拥有的边界观察项目路径；
// chat_sessions.cwd 对项目会话可以为空，不能拿持久化字段替代执行时解析结果。
const CwdAssertDirectivePrefix = "e2e-assert-cwd:"

// SubagentCallDirectivePrefix 触发调用子 agent 的用户指令:
// e2e-subagent-call:<子agent名>:<交给它的prompt>。需 agent 开启 subagent 工具
// (注入 /mcp/subagent/);agent_call 无审批,同步阻塞到子 agent 跑完返回其文本。
const SubagentCallDirectivePrefix = "e2e-subagent-call:"

// OrgCreateDeptDirectivePrefix 触发组织架构工具建部门的用户指令:
// e2e-org-create-dept:<部门名>。需 agent 开启 org 工具(注入 /mcp/org/);
// org 写工具需用户审批,调用挂起直至 e2e spec 点批准。
const OrgCreateDeptDirectivePrefix = "e2e-org-create-dept:"

// AskUserQuestionDirectivePrefix 触发 AskUserQuestion 失效终态的用户指令:e2e-ask:<question>。
// fake emit 一条 UserAskRequest(单问两选)后直接 Done 而不等回答 → chat_svc finalize 把未答的
// ask 标 expired,前端卡片转「已失效」终态(无需任何工具,纯 runtime 事件)。
const AskUserQuestionDirectivePrefix = "e2e-ask:"

// ToolPermissionDirectivePrefix 触发「工具调用审批」接缝的用户指令:e2e-tool-permission:<toolName>。
// fake emit 一条 ToolPermissionRequest 并阻塞到客户端 submitToolPermission 决策回来,
// 再 emit ToolPermissionResolved 并继续回显收尾。它是 web 端到端里「批准一次工具调用」的
// 接缝(桌面 e2e 的 org/subagent/hook 走 MCP 隧道,真身 handler 在桌面进程内;web 没有
// 桌面,审批协议本身足够 —— ToolPermissionRequest/Resolved + ToolPermissionSink)。
const ToolPermissionDirectivePrefix = "e2e-tool-permission:"

// HookCreateDirectivePrefix 触发脚本 Hook 创作工具的用户指令:e2e-hook-create:<name>。
// 需 agent 开启 hook 工具(注入 /mcp/hook/);hook_create 是写工具需审批,挂起等 UI 批准
// (镜像 org_create_department 接缝)。
const HookCreateDirectivePrefix = "e2e-hook-create:"

// BackgroundTaskDirectivePrefix 触发「后台任务完成 → CLI 自主续轮」接缝的用户指令:
// e2e-bg-task:<label>。本轮照常回显收尾,随后延迟推一轮 agentruntime.AutonomousTurn
// 到 AutonomousTurns(sessionID) 的 channel(详见 autoturn.go)。
const BackgroundTaskDirectivePrefix = "e2e-bg-task:"

// LongThinkingDirectivePrefix 触发「超长思考流」接缝的用户指令:e2e-long-thinking:<runes>。
// emit <runes> 个思考字符(8 rune 一片,受 AGENTRE_E2E_FAKE_CHUNK_DELAY_MS 节奏控制)后
// 接一段短文本再 Done。仅用于本地 e2e 验证:超长思考流时前端 streaming 视图是否还会
// 卡死(thinking block 应只渲染有界尾巴,而非每个 chunk 重排整段文本)。仅 e2e 构建存在。
const LongThinkingDirectivePrefix = "e2e-long-thinking:"

// toolPermissionDecision 是 submitToolPermission 投回的审批结果。
type toolPermissionDecision struct {
	allowed     bool
	alwaysAllow bool
	denyReason  string
}

// pendingToolPermission 是一条正在等待审批的工具调用:waiter 快照(ToolPermissionSink 的读侧,
// 供 runtime.session.pendingWaiters 回答)与决策投递 channel(ToolPermissionSink 的写侧)合一。
type pendingToolPermission struct {
	requestID string
	toolName  string
	input     json.RawMessage
	done      chan toolPermissionDecision
}

// Runtime 实现 agentruntime.Runtime + agentruntime.AutonomousTurnSource(见 autoturn.go)。
type Runtime struct {
	// mu 保护 autoTurns。Run(每轮)与 AutonomousTurns(chat_svc 每会话订阅一次)
	// 在不同 goroutine 并发访问同一张表。
	mu sync.Mutex
	// autoTurns:sessionID → 该会话的自主续轮 channel。惰性建,进程生命周期内常驻
	// (fake 没有子进程 evict,故不 close;详见 autoturn.go 的契约说明)。
	autoTurns map[int64]chan agentruntime.AutonomousTurn
	// inTurn:sessionID → 进行中的用户轮数,镜像 claudecode 的 claudeActive.inTurn,
	// 供 Steer 判定「有没有可注入的活跃用户轮」(见 autoturn.go)。
	inTurn map[int64]int

	// permMu 保护 permissions。ToolPermissionSink(SubmitToolPermission,来自 daemon 的
	// RPC goroutine)与 WaiterLister(PendingWaiters,同样来自 RPC goroutine)、以及本轮
	// 的 fanout goroutine 并发访问同一张表。
	permMu sync.Mutex
	// permissions:sessionID → 该会话此刻阻塞中的工具审批。fake 没有子进程 evict,一轮
	// 结束后由该轮的 fanout goroutine 清掉(见 Run 的 defer),进程内不会泄漏。
	permissions map[int64]*pendingToolPermission
}

// New 返回一个 fake runtime。
func New() *Runtime {
	return &Runtime{
		autoTurns:   make(map[int64]chan agentruntime.AutonomousTurn),
		inTurn:      make(map[int64]int),
		permissions: make(map[int64]*pendingToolPermission),
	}
}

// Capabilities 返回最小能力集:CapAbort 支撑停止按钮;
// CapMCPTools 让 e2e 的 MCP 工具注入接缝生效(org/subagent/hook 等写工具
// 需要 backend 声明此 cap 才会被注入);CapSetPermission 让前端的
// isModeSwitchable 为真,PermissionModePill 才会渲染出来供 e2e 观测
// (fake 不真的执行权限模式,只是让这块 UI 有观测对象)。fake 实际消费注入的
// MCPServers(调各 tool endpoint),但不真正执行 LLM,只回显文本。
func (r *Runtime) Capabilities() capability.Capabilities {
	return capability.Capabilities{
		Set: map[capability.Capability]bool{
			capability.CapAbort:         true,
			capability.CapMCPTools:      true,
			capability.CapSetPermission: true,
		},
	}
}

// Run 把 ReplyPrefix+UserText 分片流式发送后 emit Done。
func (r *Runtime) Run(ctx context.Context, req agentruntime.RunRequest) (<-chan agentruntime.Event, *agentruntime.RunResult, error) {
	out := make(chan agentruntime.Event, 8)
	result := &agentruntime.RunResult{
		ProviderSessionID: fmt.Sprintf("e2e-fake-%d", req.SessionID),
		Model:             "e2e-fake-model",
		ContextWindow:     ContextWindowTokens,
	}
	reply := ReplyPrefix + req.UserText
	if needle, ok := parseOnePartDirective(req.UserText, SystemAssertDirectivePrefix); ok {
		if strings.Contains(req.SystemPrompt, needle) {
			reply += "\ne2e-system-ok:" + needle
		} else {
			reply += "\ne2e-system-missing:" + needle
		}
	}
	if expected, ok := parseOnePartDirective(req.UserText, CwdAssertDirectivePrefix); ok {
		if req.Cwd == expected {
			reply += "\ne2e-cwd-ok:" + expected
		} else {
			reply += "\ne2e-cwd-mismatch:" + req.Cwd
		}
	}
	chunkDelay := configuredChunkDelay()
	// 同步登记「用户轮进行中」(Run 返回时就得可见),供 Steer 判定活跃轮。
	r.enterTurn(req.SessionID)
	go func() {
		defer close(out)
		defer r.leaveTurn(req.SessionID)
		// 超长思考流接缝(e2e-long-thinking:<runes>):先流式发一大段 thinking,再发短文本
		// 收尾,用于本地验证前端长思考流不再卡死。提前 return,跳过下面的回显/工具接缝。
		if rawRunes, found := parseOnePartDirective(req.UserText, LongThinkingDirectivePrefix); found {
			n, _ := strconv.Atoi(rawRunes)
			if n > 0 {
				if err := r.emitLongThinking(ctx, out, n, chunkDelay); err != nil {
					return
				}
			}
			for _, chunk := range splitChunks("e2e-fake-reply: long-thinking-done", 8) {
				select {
				case <-ctx.Done():
					return
				case out <- agentruntime.TextDelta{Text: chunk}:
				}
			}
			select {
			case <-ctx.Done():
				return
			case out <- agentruntime.Done{}:
			}
			return
		}
		for i, chunk := range splitChunks(reply, 8) {
			if i > 0 && chunkDelay > 0 {
				timer := time.NewTimer(chunkDelay)
				select {
				case <-ctx.Done():
					timer.Stop()
					return
				case <-timer.C:
				}
			}
			select {
			case <-ctx.Done():
				return
			case out <- agentruntime.TextDelta{Text: chunk}:
			}
		}
		// subagent 接缝:agent 开启 subagent 工具时注入 /mcp/subagent/;按指令调 agent_call
		// (无审批,同步阻塞到子 agent 在隔离会话跑完返回其文本)。失败只写 stderr。
		if spec, ok := findGroupToolServer(req.MCPServers, "agent_call"); ok {
			if name, prompt, found := parseTwoPartDirective(req.UserText, SubagentCallDirectivePrefix); found {
				if err := postToolCall(ctx, spec, "agent_call", map[string]any{
					"agent_name": name,
					"prompt":     prompt,
				}); err != nil {
					fmt.Fprintf(os.Stderr, "fake: agent_call failed: %v\n", err)
				}
			}
		}
		// org 接缝:agent 开启 org 工具时注入 /mcp/org/;按指令调 org_create_department
		// (写工具需审批,挂起等 UI 批准,e2e spec 负责点批准)。失败只写 stderr。
		if spec, ok := findGroupToolServer(req.MCPServers, "org_create_department"); ok {
			if name, found := parseOnePartDirective(req.UserText, OrgCreateDeptDirectivePrefix); found {
				if err := postToolCall(ctx, spec, "org_create_department", map[string]any{
					"name": name,
				}); err != nil {
					fmt.Fprintf(os.Stderr, "fake: org_create_department failed: %v\n", err)
				}
			}
		}
		// hook 接缝:agent 开启 hook 工具时注入 /mcp/hook/;按 e2e-hook-create:<name> 指令调
		// hook_create(写工具需审批,挂起等 UI 批准,镜像 org 接缝)。失败只写 stderr。
		if spec, ok := findGroupToolServer(req.MCPServers, "hook_create"); ok {
			if name, found := parseOnePartDirective(req.UserText, HookCreateDirectivePrefix); found {
				if err := postToolCall(ctx, spec, "hook_create", map[string]any{
					"name":         name,
					"interpreter":  "bash",
					"command":      "echo '{\"events\":[]}'",
					"scheduleExpr": "*/5 * * * *",
				}); err != nil {
					fmt.Fprintf(os.Stderr, "fake: hook_create failed: %v\n", err)
				}
			}
		}
		// AskUserQuestion 失效终态接缝:e2e-ask:<question> → emit 一条未答的 UserAskRequest,
		// 随后直接 Done。chat_svc finalize 把未答的 ask 标 expired → 卡片转「已失效」。
		if question, found := parseOnePartDirective(req.UserText, AskUserQuestionDirectivePrefix); found {
			select {
			case <-ctx.Done():
				return
			case out <- agentruntime.UserAskRequest{
				RequestID:  fmt.Sprintf("e2e-ask-%d", req.SessionID),
				ToolCallID: fmt.Sprintf("e2e-ask-tc-%d", req.SessionID),
				Questions: []agentruntime.AskQuestion{{
					ID:       "q1",
					Question: question,
					Header:   "e2e",
					Options: []agentruntime.AskOption{
						{Label: "Yes", Description: "yes"},
						{Label: "No", Description: "no"},
					},
				}},
			}:
			}
		}
		// 工具调用审批接缝:e2e-tool-permission:<toolName> → emit 一条 ToolPermissionRequest
		// 并阻塞到 submitToolPermission 决策回来,再 emit ToolPermissionResolved 并继续收尾。
		// 这是 web 端到端里「批准一次工具调用」的接缝(见 ToolPermissionDirectivePrefix 注释)。
		if toolName, found := parseOnePartDirective(req.UserText, ToolPermissionDirectivePrefix); found {
			if !r.blockOnToolPermission(ctx, out, req.SessionID, toolName) {
				return
			}
		}
		select {
		case <-ctx.Done():
			return
		case out <- agentruntime.Done{}:
		}
		// 后台任务接缝:e2e-bg-task:<label> → 本轮收尾后延迟推一轮自主续轮
		// (镜像 CLI 在 run_in_background 任务完成后自主跑的一轮)。只在本轮真正跑完
		// (没被 ctx 取消)时排,取消的轮不该凭空多出一条 assistant 轮。
		if label, found := parseOnePartDirective(req.UserText, BackgroundTaskDirectivePrefix); found {
			r.scheduleAutonomousTurn(req.SessionID, label)
		}
	}()
	return out, result, nil
}

// findGroupToolServer 返回首个广告 tool 的注入 MCP server(无 → !ok)。
func findGroupToolServer(specs []agentruntime.MCPServerSpec, tool string) (agentruntime.MCPServerSpec, bool) {
	for _, s := range specs {
		if slices.Contains(s.Tools, tool) {
			return s, true
		}
	}
	return agentruntime.MCPServerSpec{}, false
}

// blockOnToolPermission emit 一条 ToolPermissionRequest 并阻塞到 SubmitToolPermission 决策回来,
// 再 emit ToolPermissionResolved。返回 false 表示 ctx 已取消、调用方应直接收尾(不投 Done)。
func (r *Runtime) blockOnToolPermission(ctx context.Context, out chan<- agentruntime.Event, sessionID int64, toolName string) bool {
	requestID := fmt.Sprintf("e2e-tp-%d", sessionID)
	input, _ := json.Marshal(map[string]any{
		"tool":   toolName,
		"source": "e2e-tool-permission",
	})
	pending := &pendingToolPermission{
		requestID: requestID,
		toolName:  toolName,
		input:     input,
		done:      make(chan toolPermissionDecision, 1),
	}
	r.permMu.Lock()
	r.permissions[sessionID] = pending
	r.permMu.Unlock()
	// 轮末(决策已投回 / ctx 取消)清掉这条 pending,避免 stale waiter 被下次
	// pendingWaiters 报出去。
	defer func() {
		r.permMu.Lock()
		delete(r.permissions, sessionID)
		r.permMu.Unlock()
	}()

	select {
	case <-ctx.Done():
		return false
	case out <- agentruntime.ToolPermissionRequest{
		RequestID:  requestID,
		ToolCallID: fmt.Sprintf("e2e-tc-%d", sessionID),
		ToolName:   toolName,
		Input:      input,
	}:
	}
	var decision toolPermissionDecision
	select {
	case <-ctx.Done():
		return false
	case decision = <-pending.done:
	}
	select {
	case <-ctx.Done():
		return false
	case out <- agentruntime.ToolPermissionResolved{
		RequestID:   requestID,
		Allowed:     decision.allowed,
		AlwaysAllow: decision.alwaysAllow,
		DenyReason:  decision.denyReason,
	}:
	}
	return true
}

// SubmitToolPermission 实现 agentruntime.ToolPermissionSink:把审批决策投回阻塞中的
// 那条 e2e-tool-permission 接缝。waiter 已不存在(该轮已结束 / 被 ctx 取消 / 已答过)
// 时幂等返回 nil —— daemon 的 idempotentSubmitResult 把「waiter gone」折叠成成功(R8)。
func (r *Runtime) SubmitToolPermission(_ context.Context, sessionID int64, _ string, allow, alwaysAllowSession bool, denyReason string) error {
	r.permMu.Lock()
	pending := r.permissions[sessionID]
	r.permMu.Unlock()
	if pending == nil {
		return nil
	}
	// 非阻塞投递:channel 容量 1,决策只投一次;已投过 / 没人在收都算幂等成功。
	select {
	case pending.done <- toolPermissionDecision{
		allowed:     allow,
		alwaysAllow: alwaysAllowSession,
		denyReason:  denyReason,
	}:
		return nil
	default:
		return nil
	}
}

// PendingWaiters 实现 agentruntime.WaiterLister:返回该会话此刻阻塞中的工具审批快照,
// 供 daemon 的 runtime.session.pendingWaiters 回答。没在等 → 空快照。
func (r *Runtime) PendingWaiters(_ context.Context, sessionID int64) agentruntime.WaiterSnapshot {
	r.permMu.Lock()
	pending := r.permissions[sessionID]
	r.permMu.Unlock()
	if pending == nil {
		return agentruntime.WaiterSnapshot{}
	}
	return agentruntime.WaiterSnapshot{
		ToolPermissions: []agentruntime.PendingToolPermission{{
			RequestID: pending.requestID,
			ToolName:  pending.toolName,
			Input:     pending.input,
		}},
	}
}

func parseOnePartDirective(text, prefix string) (value string, ok bool) {
	idx := strings.Index(text, prefix)
	if idx < 0 {
		return "", false
	}
	rest := text[idx+len(prefix):]
	if i := strings.IndexByte(rest, '\n'); i >= 0 {
		rest = rest[:i]
	}
	rest = strings.TrimSpace(rest)
	if rest == "" {
		return "", false
	}
	return rest, true
}

func parseTwoPartDirective(text, prefix string) (first, second string, ok bool) {
	raw, ok := parseOnePartDirective(text, prefix)
	if !ok {
		return "", "", false
	}
	first, second, found := strings.Cut(raw, ":")
	first, second = strings.TrimSpace(first), strings.TrimSpace(second)
	if !found || first == "" || second == "" {
		return "", "", false
	}
	return first, second, true
}

// postToolCall 对注入的 group MCP server 发一次无状态 tools/call(原 postGroupSend 泛化)。
// handler 的 tools/call 分支无状态,无需先做 initialize 握手。
func postToolCall(ctx context.Context, spec agentruntime.MCPServerSpec, tool string, args map[string]any) error {
	payload := map[string]any{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  "tools/call",
		"params":  map[string]any{"name": tool, "arguments": args},
	}
	buf, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, spec.URL, bytes.NewReader(buf))
	if err != nil {
		return err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	for k, v := range spec.Headers {
		httpReq.Header.Set(k, v)
	}
	resp, err := http.DefaultClient.Do(httpReq)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	_, _ = io.Copy(io.Discard, resp.Body)
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("%s: unexpected status %d", tool, resp.StatusCode)
	}
	return nil
}

func configuredChunkDelay() time.Duration {
	raw := os.Getenv("AGENTRE_E2E_FAKE_CHUNK_DELAY_MS")
	if raw == "" {
		return 0
	}
	ms, err := strconv.Atoi(raw)
	if err != nil || ms <= 0 {
		return 0
	}
	return time.Duration(ms) * time.Millisecond
}

// emitLongThinking 流式发送 runes 个思考字符(8 rune 一片),供长思考流压测。filler
// 混入 CJK(宽字符,加重 whitespace-pre-wrap 重排成本)+ 空格 + 少量换行,贴近真实思考文本。
func (r *Runtime) emitLongThinking(ctx context.Context, out chan<- agentruntime.Event, runes int, chunkDelay time.Duration) error {
	filler := []rune("思考流压力测试 thinking stream padding text 持续输出用于触发前端重排 ")
	var b []rune
	for i := 0; i < runes; i++ {
		b = append(b, filler[i%len(filler)])
	}
	for i, chunk := range splitChunks(string(b), 8) {
		if i > 0 && chunkDelay > 0 {
			timer := time.NewTimer(chunkDelay)
			select {
			case <-ctx.Done():
				timer.Stop()
				return ctx.Err()
			case <-timer.C:
			}
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case out <- agentruntime.ThinkingDelta{Text: chunk}:
		}
	}
	return nil
}

// splitChunks 按 rune 边界把 s 切成最多 n 个 rune 的片段。
func splitChunks(s string, n int) []string {
	if n <= 0 || s == "" {
		return nil
	}
	runes := []rune(s)
	out := make([]string, 0, (len(runes)+n-1)/n)
	for i := 0; i < len(runes); i += n {
		end := i + n
		if end > len(runes) {
			end = len(runes)
		}
		out = append(out, string(runes[i:end]))
	}
	return out
}
