package piagent

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"

	"github.com/cago-frame/cago/pkg/logger"
	"go.uber.org/zap"

	"github.com/agentre-hub/agentre/internal/pkg/agentruntime"
	"github.com/agentre-hub/agentre/internal/pkg/agentruntime/runtimes/piagent/mcpbridge"
	"github.com/agentre-hub/agentre/pkg/piagent"
)

type stream interface {
	Next() bool
	Event() piagent.Event
	SessionID() string
	Err() error
}

type userAnchorStream interface {
	UserAnchor() string
}

type turnSpec struct {
	forkAnchor string
}

type steerStream interface {
	Steer(ctx context.Context, text string) error
}

type interruptable interface {
	Interrupt(ctx context.Context) error
}

type preparedTurnStream interface {
	Start(context.Context) (stream, error)
	SessionID() string
	Close(context.Context) error
}

type turnStreamPreparer interface {
	PrepareStreamTurn(ctx context.Context, prompt string, mode string, images []piagent.Image, turn turnSpec) (preparedTurnStream, error)
}

type clientAdapter struct {
	client *piagent.Client
	sid    string
	// chatSessionID 只为收尾时删掉这条会话的 MCP 桥配置:配置的寿命跟着 RPC 进程,
	// 而进程现在跨轮活着,不能再随某一轮的结束被删掉。
	chatSessionID int64
	ownsMCPConfig bool

	sessionMu sync.Mutex
	session   *piagent.Session
	closeOnce sync.Once

	streamMu sync.Mutex
	stream   *piagent.Stream
}

func (a *clientAdapter) ID() string { return a.sid }

// ensureSession 惰性开出常驻 RPC 会话:进程在这里起一次,之后每轮都在它上面开。
func (a *clientAdapter) ensureSession(ctx context.Context) (*piagent.Session, error) {
	a.sessionMu.Lock()
	defer a.sessionMu.Unlock()
	if a.session != nil {
		return a.session, nil
	}
	session, err := a.client.OpenSession(ctx)
	if err != nil {
		return nil, err
	}
	a.session = session
	return session, nil
}

// Close 收掉这条会话的 RPC 进程与它的 MCP 桥配置。幂等:失败路径会同步关一次,池的
// 收尾又会异步关一次。
func (a *clientAdapter) Close(ctx context.Context) error {
	var closeErr error
	a.closeOnce.Do(func() { closeErr = a.shutdown(ctx) })
	return closeErr
}

func (a *clientAdapter) shutdown(ctx context.Context) error {
	a.streamMu.Lock()
	stream := a.stream
	a.stream = nil
	a.streamMu.Unlock()
	var closeErr error
	if stream != nil {
		closeErr = stream.Close(ctx)
	}
	a.sessionMu.Lock()
	session := a.session
	a.session = nil
	a.sessionMu.Unlock()
	if session != nil {
		if err := session.Close(ctx); closeErr == nil && err != nil {
			closeErr = err
		}
	}
	// MCP 桥配置随进程一起走:进程没了才删得。
	if a.ownsMCPConfig && a.chatSessionID > 0 {
		if err := mcpbridge.RemoveConfig(a.chatSessionID); closeErr == nil && err != nil {
			closeErr = err
		}
	}
	if err := a.client.Close(ctx); closeErr == nil && err != nil {
		closeErr = err
	}
	return closeErr
}

func (a *clientAdapter) Stream(ctx context.Context, prompt string, mode string, images []piagent.Image) (stream, error) {
	return a.startStream(ctx, prompt, mode, images, nil)
}

func (a *clientAdapter) StreamTurn(ctx context.Context, prompt string, mode string, images []piagent.Image, turn turnSpec) (stream, error) {
	return a.startStream(ctx, prompt, mode, images, &turn)
}

func (a *clientAdapter) startStream(ctx context.Context, prompt string, mode string, images []piagent.Image, turn *turnSpec) (stream, error) {
	opts, err := turnRunOptions(mode, images, turn)
	if err != nil {
		return nil, err
	}
	session, err := a.ensureSession(ctx)
	if err != nil {
		return nil, err
	}
	s, err := session.Stream(ctx, prompt, opts...)
	if err != nil {
		return nil, err
	}
	a.setActiveStream(s)
	return s, nil
}

func (a *clientAdapter) PrepareStreamTurn(
	ctx context.Context,
	prompt string,
	mode string,
	images []piagent.Image,
	turn turnSpec,
) (preparedTurnStream, error) {
	opts, err := turnRunOptions(mode, images, &turn)
	if err != nil {
		return nil, err
	}
	session, err := a.ensureSession(ctx)
	if err != nil {
		return nil, err
	}
	prepared, err := session.PrepareStream(ctx, prompt, opts...)
	if err != nil {
		return nil, err
	}
	a.sid = prepared.SessionID()
	return &clientPreparedTurn{adapter: a, prepared: prepared}, nil
}

func turnRunOptions(mode string, images []piagent.Image, turn *turnSpec) ([]piagent.RunOption, error) {
	// Resume 不在这里下发：会话复用走 Client 级 --session（WithSession）。每个
	// runtime turn 都记录原生 user anchor；分叉 turn 由同一个 per-turn option
	// 在当前 RPC 进程里先 fork，再发送 prompt。
	var opts []piagent.RunOption
	if turn != nil {
		switch {
		case turn.forkAnchor == "":
			opts = append(opts, piagent.RunCaptureUserAnchor())
		case strings.TrimSpace(turn.forkAnchor) != turn.forkAnchor:
			return nil, errors.New("piagent runtime: invalid fork anchor")
		default:
			opts = append(opts, piagent.RunForkAnchor(turn.forkAnchor))
		}
	}
	if strings.TrimSpace(mode) != "" {
		opts = append(opts, piagent.RunPermissionMode(piagent.PermissionMode(strings.TrimSpace(mode))))
	}
	if len(images) > 0 {
		opts = append(opts, piagent.WithImages(images))
	}
	return opts, nil
}

func (a *clientAdapter) setActiveStream(s *piagent.Stream) {
	a.sid = s.SessionID()
	a.streamMu.Lock()
	a.stream = s
	a.streamMu.Unlock()
}

type clientPreparedTurn struct {
	adapter  *clientAdapter
	prepared *piagent.PreparedStream
}

func (p *clientPreparedTurn) Start(ctx context.Context) (stream, error) {
	s, err := p.prepared.Start(ctx)
	if err != nil {
		return nil, err
	}
	p.adapter.setActiveStream(s)
	return s, nil
}

func (p *clientPreparedTurn) SessionID() string { return p.prepared.SessionID() }
func (p *clientPreparedTurn) Close(ctx context.Context) error {
	return p.prepared.Close(ctx)
}

func (a *clientAdapter) Compact(ctx context.Context) (stream, error) {
	session, err := a.ensureSession(ctx)
	if err != nil {
		return nil, err
	}
	s, err := session.Compact(ctx)
	if err != nil {
		return nil, err
	}
	a.sid = s.SessionID()
	a.streamMu.Lock()
	a.stream = s
	a.streamMu.Unlock()
	return s, nil
}

func (a *clientAdapter) RewindTo(context.Context, string) (string, error) {
	return "", agentruntime.ErrUnsupported
}

func (a *clientAdapter) ActiveStream() steerStream {
	a.streamMu.Lock()
	defer a.streamMu.Unlock()
	if a.stream == nil {
		return nil
	}
	return a.stream
}

func (a *clientAdapter) ActiveInterruptor() interruptable {
	a.streamMu.Lock()
	defer a.streamMu.Unlock()
	if a.stream == nil {
		return nil
	}
	return a.stream
}

type sessionHandle interface {
	Close(context.Context) error
	ID() string
	Stream(ctx context.Context, prompt string, mode string, images []piagent.Image) (stream, error)
	StreamTurn(ctx context.Context, prompt string, mode string, images []piagent.Image, turn turnSpec) (stream, error)
	Compact(ctx context.Context) (stream, error)
	RewindTo(ctx context.Context, anchor string) (string, error)
	ActiveStream() steerStream
	ActiveInterruptor() interruptable
}

// piRawFrameSink reports only safe metadata for each pi-agent stdout frame.
// pkg/piagent hands every subprocess stdout frame here unchanged. Debug Logging
// intentionally records the complete frame for protocol troubleshooting.
func piRawFrameSink(sessionID int64, providerSessionID string) func([]byte) {
	return func(line []byte) {
		fields := []zap.Field{
			zap.Int64("sessionID", sessionID),
			zap.String("providerSessionID", providerSessionID),
			zap.Int("frameBytes", len(line)),
			zap.ByteString("frame", line),
		}
		var head struct {
			Type    string `json:"type"`
			Command string `json:"command"`
		}
		if err := json.Unmarshal(line, &head); err != nil {
			fields = append(fields, zap.Bool("parseFailed", true))
		} else {
			if frameType, ok := safePiFrameType(head.Type); ok {
				fields = append(fields, zap.String("frameType", frameType))
			}
			if command, ok := safePiResponseCommand(head.Command); ok {
				fields = append(fields, zap.String("responseCommand", command))
			}
		}
		logger.Default().Debug("piagent runtime: raw frame", fields...)
	}
}

func safePiFrameType(frameType string) (string, bool) {
	switch frameType {
	case "response",
		"message_start", "message_update", "message_end",
		"agent_end", "agent_settled",
		"tool_execution_start", "tool_execution_update", "tool_execution_end",
		"compaction_start", "compaction_end", "auto_retry_start":
		return frameType, true
	default:
		return "", false
	}
}

func safePiResponseCommand(command string) (string, bool) {
	switch command {
	case "get_state", "prompt", "get_session_stats", "compact", "get_commands":
		return command, true
	default:
		return "", false
	}
}

// providerRunConfig 装配绑定供应商时的 provider 会话参数（APIKey 校验与 env 注入在
// Run 层完成，见 runtime.go）：返回 --model 值（effectiveModel = 解析出的 ModelID，
// 非空时为 "agentre-<key>/<model>"）与物化后的 provider 扩展绝对路径。ModelID 为空
// （保存时已拦截，此处仅兜底）时沿用现状：返回零值不报错，不注入模型也不物化扩展。
// 模型名（Type 不可识别 / 模型空）出错一律显式返回，不静默吞掉后走无绑定运行。
// #26 会话级模型覆盖已移除,不再有 override 参与。
// cfg 是执行侧解析结果（EffectiveLLMConfig v1 seam）：模型 id 取解析出的 ModelID。
func providerRunConfig(cfg *agentruntime.EffectiveLLMConfig) (model string, extPath string, err error) {
	if cfg == nil {
		return "", "", nil
	}
	m := strings.TrimSpace(cfg.ModelID)
	if m == "" {
		return "", "", nil
	}
	// 复用 PiAgentProviderModelName 的 "agentre-<key>/<model>" 拼装 + Type 校验。
	model, err = agentruntime.PiAgentProviderModelName(cfg)
	if err != nil {
		return "", "", err
	}
	// 扩展与 --model 必须出自同一个 effectiveModel(都用 cfg):PiAgentProviderExtension
	// 渲染的 registerProvider 只声明 models:[<cfg.ModelID>]。扩展按内容哈希落盘。
	extPath, err = MaterializeProviderExtension(cfg)
	if err != nil {
		return "", "", err
	}
	return model, extPath, nil
}

// piModelFallback 未绑 provider（或解析出的 ModelID 空）时的 --model 兜底：
// effectiveModel = defaultModelForBackend。裸 CLI 模型 id 直接作 --model 下发（走 pi
// 自身登录/配置），不经 agentre 网关。
func piModelFallback(req agentruntime.RunRequest) string {
	return defaultModelForBackend(req.Backend)
}

// piResultModelPlaceholder 是 RunResult.Model 在 pi 真实 usage 帧上报前的占位：
// effectiveModel = firstNonEmpty(解析出的 ModelID, backendDefault)。pi 每轮在 usage 帧
// 上报真实模型 id 会覆盖它（runtime.go result.Model = raw.Model）；仅当 pi 不报模型
// （极少）时落到这里。
func piResultModelPlaceholder(req agentruntime.RunRequest) string {
	if req.Effective != nil {
		if pm := strings.TrimSpace(req.Effective.ModelID); pm != "" {
			return pm
		}
	}
	return defaultModelForBackend(req.Backend)
}

// piUserModelID 把 pi 上报的模型 id 归一为面向用户的原始模型 id。
// 绑 provider 时 pi 实际运行的是 "agentre-<key>/<model>"(PiAgentProviderModelName 拼装
// 的 --model 值),usage 帧上报的模型也带这个前缀 —— 若直接吐给 chat_svc,transcript 的
// model 字段会带着机器前缀,与面向用户的解析出的 ModelID 对不上。剥掉与当前 provider
// 匹配的前缀后,上报值才与 ModelID 同语义。未绑 provider / 前缀不匹配时原样
// 返回,不误伤 CLI 登录态的裸模型名。
func piUserModelID(req agentruntime.RunRequest, reported string) string {
	reported = strings.TrimSpace(reported)
	if reported == "" || req.Effective == nil {
		return reported
	}
	prefix := "agentre-" + req.Effective.ProviderKey + "/"
	if strings.HasPrefix(reported, prefix) {
		return strings.TrimPrefix(reported, prefix)
	}
	return reported
}

var sessionFactory = func(req agentruntime.RunRequest, env map[string]string, cwd string) (sessionHandle, error) {
	binary := strings.TrimSpace(req.Backend.CLIPath)
	if binary == "" {
		binary = DefaultBinary()
	}
	model := ""
	var providerExtPath string
	if req.Effective != nil {
		var err error
		model, providerExtPath, err = providerRunConfig(req.Effective)
		if err != nil {
			return nil, err
		}
	}
	if model == "" {
		model = piModelFallback(req)
	}
	// MCP 注入：有 RunRequest.MCPServers 时，materialize 内嵌桥扩展 + 渲染会话私有
	// config，扩展路径走 --extension、config 路径走 AGENTRE_PI_MCP_CONFIG env。
	var extPaths []string
	if len(req.MCPServers) > 0 {
		p, err := mcpbridge.Materialize()
		if err != nil {
			return nil, err
		}
		cfgPath, err := mcpbridge.RenderConfig(req.MCPServers, req.SessionID)
		if err != nil {
			return nil, err
		}
		extPaths = append(extPaths, p)
		env = withEnv(env, mcpbridge.ConfigEnvVar, cfgPath)
	}
	// 绑定供应商时：物化 provider 扩展（pi.registerProvider，内容哈希无密钥），
	// 与 MCP 桥扩展并列追加 --extension。
	if providerExtPath != "" {
		extPaths = append(extPaths, providerExtPath)
	}
	opts := []piagent.Option{
		piagent.WithBinary(binary),
		piagent.WithCwd(cwd),
		piagent.WithEnv(env),
		piagent.WithModel(model),
		piagent.WithSystemPrompt(req.SystemPrompt),
		piagent.WithThinking(req.Backend.ReasoningEffort),
		piagent.WithRawSink(piRawFrameSink(req.SessionID, req.ProviderSessionID)),
	}
	for _, ep := range extPaths {
		opts = append(opts, piagent.WithExtension(ep))
	}
	// 跨 turn 上下文由 Pi 原生 Session ID 绑定。首轮不下发任何 Session flag，
	// 让 Pi 遵循自己的默认/用户配置存储；后续轮仅用 --session <native-id> 恢复。
	if sessionID := strings.TrimSpace(req.ProviderSessionID); sessionID != "" {
		opts = append(opts, piagent.WithSession(sessionID))
	}
	client := piagent.New(opts...)
	return &clientAdapter{
		client:        client,
		sid:           req.ProviderSessionID,
		chatSessionID: req.SessionID,
		ownsMCPConfig: len(req.MCPServers) > 0,
	}, nil
}

func SetSessionFactoryForTest(fn func(agentruntime.RunRequest, map[string]string, string) (sessionHandle, error)) func() {
	old := sessionFactory
	sessionFactory = fn
	return func() { sessionFactory = old }
}

// withEnv 返回 env 的副本并设置一个键，避免就地改调用方的 map。
func withEnv(env map[string]string, key, val string) map[string]string {
	out := make(map[string]string, len(env)+1)
	for k, v := range env {
		out[k] = v
	}
	out[key] = val
	return out
}
