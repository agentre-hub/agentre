package codex

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"

	cagoblocks "github.com/cago-frame/agents/agent/blocks"
	"github.com/cago-frame/cago/pkg/logger"
	"go.uber.org/zap"

	"github.com/agentre-hub/agentre/internal/pkg/agentruntime"
	"github.com/agentre-hub/agentre/pkg/codex"
)

// defaultModelID 是 Agentre 在 codex CLI login 路径没有显式 provider model、
// 且 runtime 没法从事件里可靠观测模型时写入 RunResult.Model 的兜底值。
const defaultModelID = "gpt-5.5"

// cxStream 是 codex.Stream 的窄接口。
type cxStream interface {
	Next() bool
	Event() codex.Event
	SessionID() string
}

// cxSteerStream codex.Stream 实现的 turn/steer。
type cxSteerStream interface {
	Steer(ctx context.Context, text string) error
}

// cxInterruptable codex.Stream 实现的 turn/interrupt。Abort 发 RPC 让 app
// server 终止当前 turn —— 不杀子进程。
type cxInterruptable interface {
	Interrupt(ctx context.Context) error
}

type cxUserInputStream interface {
	SubmitUserInput(ctx context.Context, requestID string, answers map[string][]string) error
}

type cxApprovalStream interface {
	SubmitApproval(ctx context.Context, requestID string, allow, alwaysAllowSession bool) error
}

type cxClientAdapter struct {
	client *codex.Client
	sid    string

	streamMu sync.Mutex
	stream   *codex.Stream
	sess     *codex.Session
}

func (a *cxClientAdapter) ID() string { return a.sid }
func (a *cxClientAdapter) Model() string {
	a.streamMu.Lock()
	sess := a.sess
	a.streamMu.Unlock()
	if sess == nil {
		return ""
	}
	return sess.Model()
}
func (a *cxClientAdapter) Close(ctx context.Context) error {
	a.streamMu.Lock()
	sess := a.sess
	a.stream = nil
	a.sess = nil
	a.streamMu.Unlock()
	if sess != nil {
		return sess.Close(ctx)
	}
	return a.client.Close(ctx)
}

func (a *cxClientAdapter) ensureSession(ctx context.Context) (*codex.Session, error) {
	a.streamMu.Lock()
	if a.sess != nil {
		sess := a.sess
		a.streamMu.Unlock()
		return sess, nil
	}
	a.streamMu.Unlock()

	var opts []codex.RunOption
	if a.sid != "" {
		opts = append(opts, codex.Resume(a.sid))
	}
	sess, err := a.client.OpenSession(ctx, opts...)
	if err != nil {
		return nil, err
	}
	a.streamMu.Lock()
	if a.sess != nil {
		_ = sess.Close(context.Background())
		sess = a.sess
	} else {
		a.sess = sess
	}
	a.streamMu.Unlock()
	return sess, nil
}

func (a *cxClientAdapter) Stream(ctx context.Context, prompt string, collaborationMode string) (cxStream, error) {
	return a.StreamInput(ctx, []codex.UserInput{codex.TextInput(prompt)}, collaborationMode)
}

func (a *cxClientAdapter) StreamInput(ctx context.Context, input []codex.UserInput, collaborationMode string) (cxStream, error) {
	var opts []codex.RunOption
	a.streamMu.Lock()
	hasSession := a.sess != nil
	a.streamMu.Unlock()
	if a.sid != "" && !hasSession {
		opts = append(opts, codex.Resume(a.sid))
	}
	if strings.TrimSpace(collaborationMode) != "" {
		opts = append(opts, codex.RunCollaborationMode(codex.CollaborationMode(strings.TrimSpace(collaborationMode))))
	}
	sess, err := a.ensureSession(ctx)
	if err != nil {
		return nil, err
	}
	s, err := sess.StreamInput(ctx, input, opts...)
	if err != nil {
		return nil, err
	}
	a.streamMu.Lock()
	a.stream = s
	a.sid = sess.ID()
	a.streamMu.Unlock()
	return s, nil
}

func (a *cxClientAdapter) Compact(ctx context.Context) (cxStream, error) {
	if strings.TrimSpace(a.sid) == "" {
		return nil, fmt.Errorf("agentruntime/runtimes/codex: missing provider session id for compact")
	}
	sess, err := a.ensureSession(ctx)
	if err != nil {
		return nil, err
	}
	s, err := sess.Compact(ctx)
	if err != nil {
		return nil, err
	}
	a.streamMu.Lock()
	a.stream = s
	a.streamMu.Unlock()
	return s, nil
}

func (a *cxClientAdapter) GetGoal(ctx context.Context) (*codex.Goal, error) {
	if strings.TrimSpace(a.sid) == "" {
		return nil, fmt.Errorf("agentruntime/runtimes/codex: missing provider session id for goal")
	}
	sess, err := a.ensureSession(ctx)
	if err != nil {
		return nil, err
	}
	return sess.GetGoal(ctx)
}

func (a *cxClientAdapter) SetGoal(ctx context.Context, update codex.GoalUpdate) (*codex.Goal, error) {
	sess, err := a.ensureSession(ctx)
	if err != nil {
		return nil, err
	}
	goal, err := sess.SetGoal(ctx, update)
	if err != nil {
		return nil, err
	}
	a.streamMu.Lock()
	a.sid = sess.ID()
	a.streamMu.Unlock()
	return goal, nil
}

func (a *cxClientAdapter) ClearGoal(ctx context.Context) (bool, error) {
	if strings.TrimSpace(a.sid) == "" {
		return false, fmt.Errorf("agentruntime/runtimes/codex: missing provider session id for goal")
	}
	sess, err := a.ensureSession(ctx)
	if err != nil {
		return false, err
	}
	return sess.ClearGoal(ctx)
}

// RewindTo 走 thread/rollback,把 sid 推回 numTurns 之前的状态。anchor 是十进制
// numTurns(chat_svc 按 user msg count 算)。
func (a *cxClientAdapter) RewindTo(ctx context.Context, anchor string) (string, error) {
	if strings.TrimSpace(a.sid) == "" {
		return "", fmt.Errorf("agentruntime/runtimes/codex: missing provider session id for rollback")
	}
	numTurns, err := strconv.Atoi(strings.TrimSpace(anchor))
	if err != nil || numTurns <= 0 {
		return "", fmt.Errorf("agentruntime/runtimes/codex: invalid rollback anchor %q", anchor)
	}
	sess, err := a.ensureSession(ctx)
	if err != nil {
		return "", err
	}
	sid, err := sess.RewindTo(ctx, strconv.Itoa(numTurns))
	if err != nil {
		return "", err
	}
	a.sid = sid
	return a.sid, nil
}

func (a *cxClientAdapter) ActiveStream() cxSteerStream {
	a.streamMu.Lock()
	defer a.streamMu.Unlock()
	if a.stream == nil {
		return nil
	}
	return a.stream
}

func (a *cxClientAdapter) ActiveInterruptor() cxInterruptable {
	a.streamMu.Lock()
	defer a.streamMu.Unlock()
	if a.stream == nil {
		return nil
	}
	return a.stream
}

func userInputsFromBlocks(bs []cagoblocks.ContentBlock) ([]codex.UserInput, func(), error) {
	if len(bs) == 0 {
		return nil, nil, nil
	}
	var (
		inputs []codex.UserInput
		tmpDir string
	)
	for i, b := range bs {
		switch v := b.(type) {
		case cagoblocks.TextBlock:
			inputs = append(inputs, codex.TextInput(v.Text))
		case *cagoblocks.TextBlock:
			if v != nil {
				inputs = append(inputs, codex.TextInput(v.Text))
			}
		case cagoblocks.ImageBlock:
			path, err := materializeImage(&tmpDir, i, v)
			if err != nil {
				if tmpDir != "" {
					_ = os.RemoveAll(tmpDir)
				}
				return nil, nil, err
			}
			inputs = append(inputs, codex.LocalImageInput(path, codex.ImageDetailHigh))
		case *cagoblocks.ImageBlock:
			if v == nil {
				continue
			}
			path, err := materializeImage(&tmpDir, i, *v)
			if err != nil {
				if tmpDir != "" {
					_ = os.RemoveAll(tmpDir)
				}
				return nil, nil, err
			}
			inputs = append(inputs, codex.LocalImageInput(path, codex.ImageDetailHigh))
		}
	}
	var cleanup func()
	if tmpDir != "" {
		cleanup = func() { _ = os.RemoveAll(tmpDir) }
	}
	return inputs, cleanup, nil
}

func materializeImage(tmpDir *string, idx int, img cagoblocks.ImageBlock) (string, error) {
	if strings.TrimSpace(img.Source.URL) != "" {
		return "", fmt.Errorf("agentruntime/runtimes/codex: image URL blocks are not supported yet")
	}
	if len(img.Source.Inline) == 0 {
		return "", fmt.Errorf("agentruntime/runtimes/codex: empty image block")
	}
	if *tmpDir == "" {
		dir, err := os.MkdirTemp("", "agentre-codex-images-*")
		if err != nil {
			return "", err
		}
		*tmpDir = dir
	}
	path := filepath.Join(*tmpDir, fmt.Sprintf("image-%d%s", idx, imageExt(img.MediaType)))
	if err := os.WriteFile(path, img.Source.Inline, 0o600); err != nil {
		return "", err
	}
	return path, nil
}

func imageExt(mediaType string) string {
	switch strings.ToLower(strings.TrimSpace(mediaType)) {
	case "image/jpeg", "image/jpg":
		return ".jpg"
	case "image/webp":
		return ".webp"
	default:
		return ".png"
	}
}

type cxSessionHandle interface {
	Close(context.Context) error
	ID() string
	Model() string
	Stream(ctx context.Context, prompt string, collaborationMode string) (cxStream, error)
	StreamInput(ctx context.Context, input []codex.UserInput, collaborationMode string) (cxStream, error)
	Compact(ctx context.Context) (cxStream, error)
	GetGoal(ctx context.Context) (*codex.Goal, error)
	SetGoal(ctx context.Context, update codex.GoalUpdate) (*codex.Goal, error)
	ClearGoal(ctx context.Context) (bool, error)
	RewindTo(ctx context.Context, anchor string) (string, error)
	ActiveStream() cxSteerStream
	ActiveInterruptor() cxInterruptable
}

type codexLaunchSpec struct {
	binary            string
	cwd               string
	env               map[string]string
	model             string
	systemPrompt      string
	sandbox           codex.SandboxMode
	approval          codex.ApprovalPolicy
	config            []string
	sessionID         int64  // chat_sessions.ID —— 仅给 raw-frame debug 日志定位用
	providerSessionID string // codex thread/provider session —— 同上
}

// gatewayDeps 装配 codex 网关门控(spec 2026-08-10 决策 6):是否给子进程带
// gateway token/URL 看本轮有没有 effective provider(req.EffectiveProviderKey()，
// 会话 provider_key 覆盖 agent 绑定后的那家)，不再单看 backend 是否绑定——CLI 登录态
// 后端(req.Backend.LLMProviderKey=="")上会话选了 agentre 供应商时(req.Provider 非空)
// 也要装配，否则 chat_svc 签的 token 永远传不到 BuildCodexConfig，登录态无法被接管。
func gatewayDeps(req agentruntime.RunRequest) CLIDeps {
	if req.Backend == nil || req.EffectiveProviderKey() == "" {
		return CLIDeps{}
	}
	return CLIDeps{Token: req.GatewayToken, GatewayURL: req.GatewayURL}
}

// codexEffectiveModel 统一模型解析规则(codex 版):#26 会话级模型覆盖已移除,
// effectiveModel 取执行侧解析结果(EffectiveLLMConfig v1 seam)的 ModelID。codex
// 无 backend 默认模型字段 (DefaultModel 仅 claudecode 用),CLI 登录态(无供应商)
// 返回空,交由调用方按 defaultModelID 兜底。它是 buildLaunchSpec 与 acquireSession
// 判「模型变化 → evict」的同一取值源。
func codexEffectiveModel(req agentruntime.RunRequest) string {
	if req.Effective != nil {
		return strings.TrimSpace(req.Effective.ModelID)
	}
	return ""
}

func codexEffectiveModelKey(req agentruntime.RunRequest) string {
	if req.Effective == nil {
		return ""
	}
	return strings.TrimSpace(req.Effective.ModelKey)
}

func buildLaunchSpec(req agentruntime.RunRequest, env map[string]string, cwd string) codexLaunchSpec {
	binary := strings.TrimSpace(req.Backend.CLIPath)
	if binary == "" {
		binary = DefaultBinary()
	}
	spec := codexLaunchSpec{
		binary:            binary,
		cwd:               cwd,
		env:               env,
		systemPrompt:      req.SystemPrompt,
		config:            BuildCodexConfig(gatewayDeps(req)),
		sessionID:         req.SessionID,
		providerSessionID: req.ProviderSessionID,
	}
	spec.config = append(spec.config, buildMCPServerConfig(req.MCPServers)...)
	spec.config = append(spec.config, codex.PluginEnabledConfig(req.EnabledPlugins)...)
	if eff := reasoningEffortConfigValue(req.Backend.ReasoningEffort); eff != "" {
		spec.config = append(spec.config, `model_reasoning_effort="`+eff+`"`)
	}
	spec.model = codexEffectiveModel(req)
	if sb := strings.TrimSpace(req.Backend.Sandbox); sb != "" {
		spec.sandbox = codex.SandboxMode(sb)
	}
	if ap := strings.TrimSpace(req.Backend.Approval); ap != "" {
		spec.approval = codex.ApprovalPolicy(ap)
	}
	return spec
}

func buildMCPServerConfig(specs []agentruntime.MCPServerSpec) []string {
	if len(specs) == 0 {
		return nil
	}
	out := make([]string, 0, len(specs)*4)
	for _, s := range specs {
		name := strings.TrimSpace(s.Name)
		url := strings.TrimSpace(s.URL)
		if name == "" || url == "" {
			continue
		}
		prefix := "mcp_servers." + tomlKey(name)
		out = append(out, prefix+".url="+strconv.Quote(url))
		if len(s.Headers) > 0 {
			keys := make([]string, 0, len(s.Headers))
			for k := range s.Headers {
				keys = append(keys, k)
			}
			sort.Strings(keys)
			for _, k := range keys {
				out = append(out, prefix+".http_headers."+tomlKey(k)+"="+strconv.Quote(s.Headers[k]))
			}
		}
		if len(s.Tools) > 0 {
			out = append(out, prefix+".enabled_tools="+tomlStringArray(s.Tools))
			out = append(out, prefix+".default_tools_approval_mode="+strconv.Quote("approve"))
		}
	}
	return out
}

func tomlKey(s string) string {
	return s
}

func tomlStringArray(items []string) string {
	var b strings.Builder
	b.WriteByte('[')
	for i, item := range items {
		if i > 0 {
			b.WriteByte(',')
		}
		b.WriteString(strconv.Quote(item))
	}
	b.WriteByte(']')
	return b.String()
}

// reasoningEffortConfigValue 把落库的 reasoning_effort 映射为 codex CLI 配置值。
// 与顶层 clienv.go.codexReasoningEffortConfigValue 等价 —— low/medium/high/xhigh
// 直传,max 向下并到 high;off / 非法值 → "" 不下发。
func reasoningEffortConfigValue(s string) string {
	switch s {
	case "low", "medium", "high", "xhigh":
		return s
	case "max":
		return "high"
	default:
		return ""
	}
}

func (s codexLaunchSpec) options() []codex.Option {
	opts := []codex.Option{
		codex.WithBinary(s.binary),
		codex.WithCwd(s.cwd),
		codex.WithEnv(s.env),
		codex.WithSystemPrompt(s.systemPrompt),
		codex.WithRawSink(cxRawFrameSink(s.sessionID, s.providerSessionID)),
	}
	if s.model != "" {
		opts = append(opts, codex.WithModel(s.model))
	}
	for _, c := range s.config {
		opts = append(opts, codex.WithConfig(c))
	}
	if s.sandbox != "" {
		opts = append(opts, codex.WithSandbox(s.sandbox))
	}
	if s.approval != "" {
		opts = append(opts, codex.WithApproval(s.approval))
	}
	return opts
}

// cxRawFrameSink 返回一个把 codex app-server 每行原始 stdout 帧打到 debug 日志的回调。
// 语义同 claudecode 的 ccRawFrameSink:由「Debug Logging」开关热控(关时 zap 直接丢弃,
// 近零开销),用 logger.Default() 取当前全局 logger 故热重载即时生效。
func cxRawFrameSink(sessionID int64, providerSessionID string) func([]byte) {
	return func(line []byte) {
		logger.Default().Debug("codex runtime: raw frame",
			zap.Int64("sessionID", sessionID),
			zap.String("providerSessionID", providerSessionID),
			zap.ByteString("frame", line))
	}
}

// cxSessionFactory 生产路径;测试 SetSessionFactoryForTest 替换。
var cxSessionFactory = func(req agentruntime.RunRequest, env map[string]string, cwd string) (cxSessionHandle, error) {
	client := codex.New(buildLaunchSpec(req, env, cwd).options()...)
	return &cxClientAdapter{client: client, sid: req.ProviderSessionID}, nil
}

// SetSessionFactoryForTest 仅测试用;restore 闭包恢复默认。
func SetSessionFactoryForTest(fn func(agentruntime.RunRequest, map[string]string, string) (cxSessionHandle, error)) func() {
	old := cxSessionFactory
	cxSessionFactory = fn
	return func() { cxSessionFactory = old }
}
