package claudecode

import (
	"context"
	"encoding/json"
	"errors"
	"maps"
	"strings"

	"github.com/cago-frame/cago/pkg/logger"
	"go.uber.org/zap"

	"github.com/agentre-ai/agentre/internal/pkg/agentruntime"
	"github.com/agentre-ai/agentre/pkg/claudecode"
)

// ccStream 是 pkg/claudecode.Stream 的窄接口,便于测试注入 fake。
type ccStream interface {
	Next() bool
	Event() claudecode.Event
	SessionID() string
}

// ccSessionHandle 包装一次 Stream + Close + 控制协议入口(Interrupt /
// SetPermissionMode / RespondToControl)。
type ccSessionHandle interface {
	Close(context.Context) error
	ID() string
	// Stream 发起一轮 Turn。images 非空时透传到 user frame 的 base64 image
	// content block(CLI stream-json 原生支持);为空时退化成 text-only。
	Stream(ctx context.Context, prompt string, images []claudecode.Image) (ccStream, error)
	// RespondToControl 配对响应 claude 端发的 control_request{subtype:"can_use_tool"}。
	// 由 control dispatcher / answer sink 触发;其它场景不调用。
	RespondToControl(ctx context.Context, requestID string, result claudecode.PermissionResult) error
	// Interrupt 发 control_request{subtype:"interrupt"} 软中断当前 turn。
	// CLI 回 control_response 后 Session 收到 result 帧让本轮 Turn 自然返 done,
	// **子进程保留**。失败时 runner 走 Close + cache.Remove 兜底。
	Interrupt(ctx context.Context) error
	// Kill 硬杀子进程(SIGKILL)。用于 startup 看门狗在「turn 起步后迟迟无帧」(典型:
	// CLI 卡 MCP 初始化连不上 gateway)时强制收尾 —— Interrupt/Close 的优雅路径对卡在
	// socket 上、不读 stdin 的 CLI 无效, 只有 SIGKILL 能让 drainStream 解阻塞。
	Kill(ctx context.Context) error
	// SetPermissionMode 写一帧 control_request{subtype:"set_permission_mode"}
	// 让 CLI 在两个 Turn 之间切换 permission mode。mode 取
	// {default, acceptEdits, plan, bypassPermissions}。只能在 Turn 之间调用,
	// 期间该方法会阻塞到当前 Turn 收尾。
	SetPermissionMode(ctx context.Context, mode string) error
	// StopTask 写一帧 control_request{subtype:"stop_task", task_id} 停掉某个后台任务
	// (run_in_background Bash / subagent)。**子进程保留**;后台任务跨 turn 存活,
	// 空闲态也能停。CLI 回 not_found/not_running 视为幂等成功。
	StopTask(ctx context.Context, taskID string) error
	// ExitErr 子进程已退出时返其分类后的退出错误(如 claudecode.ErrSessionNotFound
	// 或 *claudecode.ProcessExitError);还活着 / 没 spawn 返 nil。
	// 0-frame fallback 用它替换 "subprocess produced no events" 通用消息,
	// 让 chat_svc 能 errors.Is 出准确语义。
	ExitErr() error
	// AutonomousTurns 返回底层 Session 的自主续轮 channel(后台任务完成 CLI 自主
	// 跑的一轮)。子进程退出时 close。Runtime.AutonomousTurns 桥接它成 agentruntime 流。
	AutonomousTurns() <-chan *claudecode.AutoTurn
	// SubagentActivity 返回底层 Session 的后台 subagent 活动流 channel(后台 subagent 在
	// 空闲态产生的内部活动)。子进程退出时 close。Runtime.SubagentActivity 桥接它成 agentruntime 流。
	SubagentActivity() <-chan *claudecode.SubagentActivity
}

// ccLaunchSpec 是 ccSessionFactory 的全部入参;具名结构体避免每次新增可选
// 参数就改一遍签名。
type ccLaunchSpec struct {
	Req      agentruntime.RunRequest
	Env      map[string]string
	Cwd      string
	Settings string // 非空时透传 --settings <value>;CLI 接受 JSON 字符串或文件路径
	// SessionUUID 非空时透传 --session-id <uuid>
	SessionUUID    string
	PermissionMode string // 非空时透传 --permission-mode <mode>;空 = 走 args.go 默认
	// DefaultPermissionMode 是 spawn CLI 子进程时下发给 --permission-mode 的备选值。
	// 空串 → 由 pkg/claudecode 兜底(acceptEdits)。优先级低于 spec.PermissionMode。
	DefaultPermissionMode string
}

// ccClientAdapter 把 *claudecode.Session 适配成 ccSessionHandle。
// 与之前每个 turn spawn 一次不同:现在 OpenSession 在 factory 里发生一次,
// Stream 实际上调用 Session.Turn,跨多个 turn 复用同一个子进程。
type ccClientAdapter struct {
	sess *claudecode.Session
	sid  string // 由 OpenSession 时的 --session-id 决定;首个 turn 后用 sess.SessionID() 覆盖
}

func (a *ccClientAdapter) ID() string { return a.sid }

func (a *ccClientAdapter) Close(ctx context.Context) error {
	if a.sess == nil {
		return nil
	}
	return a.sess.Close(ctx)
}

// Interrupt 把 control_request{interrupt} 写到 CLI stdin;CLI 软中断当前 turn
// 后会发 result 帧让 Session.Turn 自然收尾。**子进程不动**。
func (a *ccClientAdapter) Interrupt(ctx context.Context) error {
	if a.sess == nil {
		return nil
	}
	return a.sess.Interrupt(ctx)
}

// Kill 硬杀底层子进程(SIGKILL)。runtime 的 startup 看门狗在「turn 起步后迟迟无帧」
// 时调用 —— 优雅 Interrupt/Close 对卡在 socket 上的 CLI 无效, 只有 SIGKILL 能保证
// drainStream 解阻塞收尾。
func (a *ccClientAdapter) Kill(_ context.Context) error {
	if a.sess == nil {
		return nil
	}
	a.sess.Kill()
	return nil
}

// PID 转发到底层 claudecode.Session.PID(池快照的排查字段)。
func (a *ccClientAdapter) PID() int {
	if a.sess == nil {
		return 0
	}
	return a.sess.PID()
}

// StopTask 转发到底层 claudecode.Session.StopTask,写 control_request{stop_task}
// 停单个后台任务。**子进程不动**;后台任务跨 turn 存活,空闲态也能停。
func (a *ccClientAdapter) StopTask(ctx context.Context, taskID string) error {
	if a.sess == nil {
		return errors.New("agentruntime/runtimes/claudecode: session not opened")
	}
	return a.sess.StopTask(ctx, taskID)
}

// SetPermissionMode 转发到底层 claudecode.Session.SetPermissionMode。抢 turnMu,
// 所以会阻塞到当前 Turn 收尾 —— caller 不应该在 Stream 还没 drain 完的状态下
// 调用,否则要等到当前 turn 自然 done。
func (a *ccClientAdapter) SetPermissionMode(ctx context.Context, mode string) error {
	if a.sess == nil {
		return errors.New("agentruntime/runtimes/claudecode: session not opened")
	}
	return a.sess.SetPermissionMode(ctx, mode)
}

// ExitErr 透传 claudecode.Session.ExitErr。
func (a *ccClientAdapter) ExitErr() error {
	if a.sess == nil {
		return nil
	}
	return a.sess.ExitErr()
}

// AutonomousTurns 透传 claudecode.Session.AutonomousTurns(后台任务自主续轮)。
func (a *ccClientAdapter) AutonomousTurns() <-chan *claudecode.AutoTurn {
	if a.sess == nil {
		return nil
	}
	return a.sess.AutonomousTurns()
}

// SubagentActivity 透传 claudecode.Session.SubagentActivity(后台 subagent 内部活动流)。
func (a *ccClientAdapter) SubagentActivity() <-chan *claudecode.SubagentActivity {
	if a.sess == nil {
		return nil
	}
	return a.sess.SubagentActivity()
}

// RespondToControl 转发到底层 claudecode.Session。stdinMu 由 Session 内部保护,
// 多个并发 control_request 可以串行写。
func (a *ccClientAdapter) RespondToControl(ctx context.Context, requestID string, result claudecode.PermissionResult) error {
	if a.sess == nil {
		return errors.New("agentruntime/runtimes/claudecode: session not opened")
	}
	return a.sess.RespondToControl(ctx, requestID, result)
}

// Stream 在持久化 session 上发起一轮 Turn,把 Session.Turn 返回的 <-chan Event
// 转成 ccStream iterator,让 drain 逻辑那侧不动。
func (a *ccClientAdapter) Stream(ctx context.Context, prompt string, images []claudecode.Image) (ccStream, error) {
	ch, err := a.sess.Turn(ctx, prompt, images...)
	if err != nil {
		return nil, err
	}
	return &ccChanStream{ch: ch, sidFn: a.sess.SessionID}, nil
}

// ccChanStream 把 <-chan claudecode.Event 适配成 ccStream(Next/Event/SessionID)。
// 一个 channel 对应一个 turn;result 帧到达后 channel 关闭,Next 返回 false。
type ccChanStream struct {
	ch    <-chan claudecode.Event
	cur   claudecode.Event
	sidFn func() string
}

func (s *ccChanStream) Next() bool {
	ev, ok := <-s.ch
	if !ok {
		return false
	}
	s.cur = ev
	return true
}

func (s *ccChanStream) Event() claudecode.Event { return s.cur }

func (s *ccChanStream) SessionID() string {
	if s.sidFn != nil {
		return s.sidFn()
	}
	return ""
}

// resolveLaunchMode 选 --permission-mode 值。优先级:用户 turn override (perTurn)
// → backend admin default (backendDefault) → ""。
//
// 例外: backendDefault == "bypassPermissions" 时 launch 永远锁 bypass —— 这是
// 「先 plan 后 bypass」工作流的承重柱: bypass-lockout 按 permission_mode_at_launch
// 判定, 必须 = bypass 才能解锁运行时切回 bypass; 同时 PlanApproveCard 主按钮也
// 按 launch == bypass 决定显示「批准并跳过权限确认」。stored mode 与 launch 不
// 一致时, spawn 后由 runtime 主动发 SetPermissionMode(perTurn) 把 CLI 校准到
// 用户当前选的 mode。
func resolveLaunchMode(perTurn, backendDefault string) string {
	if backendDefault == "bypassPermissions" {
		return "bypassPermissions"
	}
	if perTurn != "" {
		return perTurn
	}
	return backendDefault
}

// ccSessionFactory 由 init 写为真实路径;测试通过 SetSessionFactoryForTest 替换。
//
// 每个 chat session 调用一次(首轮或 fork 时),spawn 一个常驻 claude 子进程。
// runner 会缓存返回的 handle 给后续 Turn 复用。
// ccBuildClientOpts 把 ccLaunchSpec 翻译成 claudecode.Client 选项列表。抽成
// 独立函数是为了让单测在不 spawn 真子进程的前提下断言「绑了 provider 的后端
// 会下发 --model」这条不变量(spec §B token contract;Bug 1 防回归)。
// binary 由 caller 决定:真路径走 ccSessionFactory 解析,测试可以传 stub 串。
// claudeEffectiveModel 统一模型解析规则:#26 会话级模型覆盖已移除,effectiveModel =
// firstNonEmpty(解析出的 ModelID, backend.DefaultModel),TrimSpace 后返回。
// 绑了 Agentre 供应商时取执行侧解析结果(EffectiveLLMConfig v1 seam)的 ModelID；
// CLI 登录态(无供应商)回退 backend.DefaultModel —— Claude 的 DefaultModel 是
// native 模式的独立自由文本 CLI 字段,只在 native 时生效(决策 13)。
func claudeEffectiveModel(req agentruntime.RunRequest) string {
	if req.Effective != nil {
		if pm := strings.TrimSpace(req.Effective.ModelID); pm != "" {
			return pm
		}
	}
	return strings.TrimSpace(req.Backend.DefaultModel)
}

func ccBuildClientOpts(spec ccLaunchSpec, binary string) []claudecode.Option {
	env := spec.Env
	// 注入 MCP server 时拉长 CLI 的 MCP 工具调用超时:orgtool 写操作会同步挂起
	// 等用户审批(approvalTimeout=4min),默认 60s 撑不住。值为毫秒。
	if len(spec.Req.MCPServers) > 0 {
		merged := make(map[string]string, len(env)+2)
		maps.Copy(merged, env)
		if _, ok := merged["MCP_TIMEOUT"]; !ok {
			merged["MCP_TIMEOUT"] = "600000"
		}
		if _, ok := merged["MCP_TOOL_TIMEOUT"]; !ok {
			merged["MCP_TOOL_TIMEOUT"] = "600000"
		}
		env = merged
	}
	opts := []claudecode.Option{
		claudecode.WithBinary(binary),
		claudecode.WithCwd(spec.Cwd),
		claudecode.WithEnv(env),
		claudecode.WithSettingsEnv(env),
		claudecode.WithSystemPrompt(spec.Req.SystemPrompt),
		// 启用 stdio control protocol:把 AskUserQuestion 这种交互式工具的
		// permission gate 从 CLI 的 TUI 拉到 agentre UI;headless 下不开
		// 这个 flag,AskUserQuestion 会被 CLI 自动 deny,turn 直接挂掉。
		claudecode.WithPermissionPromptTool("stdio"),
		claudecode.WithRawSink(ccRawFrameSink(spec.Req.SessionID, spec.SessionUUID)),
	}
	// --model 取值优先级(解析出的 ModelID → backend.DefaultModel,见
	// claudeEffectiveModel):解析出的 ModelID 是绑了 LLM provider(如 GLM / openrouter
	// 等非 Anthropic 直连场景,必须下发才能让 CLI 在 system.init 帧报真实模型 id);
	// backend.DefaultModel 兜底 CLI 登录态、未绑 provider 时的自定义模型(如
	// claude-fable-5)。两者全空 → 不下发,CLI 落到本地登录态默认 model。绑 provider
	// 的行为不变;顺带让 CLI 登录态下 result.Model → assistantMsg.Model 链也能写对。
	if model := claudeEffectiveModel(spec.Req); model != "" {
		opts = append(opts, claudecode.WithModel(model))
	}
	if spec.SessionUUID != "" {
		opts = append(opts, claudecode.WithSessionID(spec.SessionUUID))
	}
	if spec.Settings != "" {
		opts = append(opts, claudecode.WithSettings(spec.Settings))
	}
	if mode := resolveLaunchMode(spec.PermissionMode, spec.DefaultPermissionMode); mode != "" {
		opts = append(opts, claudecode.WithPermissionMode(mode))
	}
	if eff := spec.Req.Backend.ReasoningEffort; eff != "" {
		opts = append(opts, claudecode.WithEffort(eff))
	}
	// 注入的 MCP tool server(org/subagent/hook 工具):翻成 --mcp-config + 把对应 tool 放进
	// --allowedTools。仅 claudecode runtime(声明 CapMCPTools)消费,其它 runtime 忽略
	// RunRequest.MCPServers。
	if len(spec.Req.MCPServers) > 0 {
		cfg, allow := buildMcpConfigJSON(spec.Req.MCPServers)
		opts = append(opts, claudecode.WithMcpConfig(cfg))
		opts = append(opts, claudecode.WithAllowedTools(allow...))
	}
	return opts
}

// buildMcpConfigJSON 把 MCPServerSpec 列表转成 claude CLI 的 --mcp-config JSON,
// 并返回需要加进 --allowedTools 的 tool 名(约定 mcp__<Name>__<tool>(如 mcp__org__org_create_department))。
// JSON 形态对齐 transport spike:
// {"mcpServers":{"<name>":{"type":"http","url":"...","headers":{...}}}}
func buildMcpConfigJSON(specs []agentruntime.MCPServerSpec) (string, []string) {
	type mcpServer struct {
		Type    string            `json:"type"`
		URL     string            `json:"url"`
		Headers map[string]string `json:"headers,omitempty"`
	}
	servers := map[string]mcpServer{}
	allow := make([]string, 0, len(specs))
	for _, s := range specs {
		servers[s.Name] = mcpServer{Type: "http", URL: s.URL, Headers: s.Headers}
		for _, tool := range s.Tools {
			allow = append(allow, "mcp__"+s.Name+"__"+tool)
		}
	}
	b, _ := json.Marshal(map[string]any{"mcpServers": servers})
	return string(b), allow
}

// ccRawFrameSink 返回一个把 claudecode 子进程每行原始 stdout 帧打到 debug 日志的回调。
//
// 由「Settings → Version & Update → Debug Logging」开关热控:关时全局 logger 落在
// info 级,zap core 直接丢弃这条 Debug,zap.ByteString 不拷贝 line,近零开销;开时
// 落全量原始帧。用 logger.Default()(而非 ctx logger)因为 Session 起在 context.Background()
// 且 readLoop 跨多轮存活;Default() 每次取当前全局 logger,故热重载即时生效。定位字段
// 带 chat sessionID + provider session UUID,便于按会话 grep(见 debugging.md)。
func ccRawFrameSink(sessionID int64, providerSessionUUID string) func([]byte) {
	return func(line []byte) {
		logger.Default().Debug("claudecode runtime: raw frame",
			zap.Int64("sessionID", sessionID),
			zap.String("providerSessionID", providerSessionUUID),
			zap.ByteString("frame", line))
	}
}

var ccSessionFactory = func(spec ccLaunchSpec) (ccSessionHandle, error) {
	binary := strings.TrimSpace(spec.Req.Backend.CLIPath)
	if binary == "" {
		binary = DefaultBinary()
	}
	client := claudecode.New(ccBuildClientOpts(spec, binary)...)

	var runOpts []claudecode.RunOption
	if spec.Req.ProviderSessionID != "" {
		runOpts = append(runOpts, claudecode.Resume(spec.Req.ProviderSessionID))
	}
	if spec.Req.ForkAnchor != "" {
		runOpts = append(runOpts, claudecode.ResumeSessionAt(spec.Req.ForkAnchor), claudecode.ForkSession())
	}

	sess, err := client.OpenSession(context.Background(), runOpts...)
	if err != nil {
		return nil, err
	}
	sid := spec.Req.ProviderSessionID
	if sid == "" {
		sid = spec.SessionUUID
	}
	return &ccClientAdapter{sess: sess, sid: sid}, nil
}

// SetSessionFactoryForTest 仅测试用;restore 闭包恢复默认。
func SetSessionFactoryForTest(fn func(ccLaunchSpec) (ccSessionHandle, error)) func() {
	old := ccSessionFactory
	ccSessionFactory = fn
	return func() { ccSessionFactory = old }
}
