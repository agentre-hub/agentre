package acp

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"
	"sync"

	acpsdk "github.com/coder/acp-go-sdk"

	"github.com/cago-frame/cago/pkg/logger"
	"go.uber.org/zap"

	"github.com/agentre-hub/agentre/internal/pkg/agentruntime"
	"github.com/agentre-hub/agentre/internal/pkg/cliprocess"
)

// errAgentBinaryNotFound 表示 acpCommand 解析不到可执行文件。
var errAgentBinaryNotFound = errors.New("acp: agent executable not found (check the backend's acpCommand)")

// launchSpec 是启动一个 ACP agent 子进程的全部参数。
type launchSpec struct {
	command string
	args    []string
	cwd     string
	// env 是 BuildACPEnv 产出的 "K=V" 列表(只含 env_json 用户变量 —— ACP agent
	// 自带凭证,不注入网关变量,也不注入每轮变化的一次性 token)。
	env []string
}

// agentTransport 是一条到 ACP agent 的字节通道(读 agent stdout / 写 agent stdin)
// 加收尾控制。生产实现是 cliprocess 子进程;单测换成内存管道对(seam 见
// dialAgentTransport)。Close 的超时升级由 CLISessionPool.closeWithTimeout 负责,
// 实现只做「关 stdin → 等退出」。
type agentTransport interface {
	io.Reader
	io.Writer
	Close(ctx context.Context) error
	Kill() error
	PID() int
	// StderrTail 交回 stderr 有界缓冲的当前内容,进程死亡时的错误信息用。
	StderrTail() string
}

// dialAgentTransport 是子进程 spawn 的 seam:生产走 cliprocess.Start;单测换成
// 内存 fake agent。包级 var 而非 New() 参数,照 claudecode.SetSessionFactoryForTest
// 的注入口径 —— defaultRuntime 不在构造期钉死资源。
var dialAgentTransport = func(ctx context.Context, spec launchSpec) (agentTransport, error) {
	proc, err := cliprocess.Start(ctx, cliprocess.Options{
		Binary: spec.command,
		Args:   spec.args,
		Cwd:    spec.cwd,
		Env:    spec.env,
	}, errAgentBinaryNotFound)
	if err != nil {
		return nil, err
	}
	return newProcTransport(proc), nil
}

// procTransport 把 cliprocess.Handle 适配成 agentTransport。
type procTransport struct {
	proc   cliprocess.Handle
	stderr *cliprocess.LockedBuffer

	closeOnce sync.Once
	closeErr  error
}

func newProcTransport(proc cliprocess.Handle) *procTransport {
	t := &procTransport{proc: proc, stderr: &cliprocess.LockedBuffer{}}
	// stderr 同时进有界环形缓冲(给错误信息用)和 Debug 日志。
	go pumpStderr(t.stderr, proc.Stderr())
	return t
}

func (t *procTransport) Read(p []byte) (int, error)  { return t.proc.Stdout().Read(p) }
func (t *procTransport) Write(p []byte) (int, error) { return t.proc.Stdin().Write(p) }

// Close 优雅收尾:关 stdin → 等子进程退出。卡死(不读 stdin)的 agent 由池的
// closeWithTimeout 在宽限期后 Kill 兜底 —— 那一刀落下后 Wait 自然返回。
func (t *procTransport) Close(ctx context.Context) error {
	t.closeOnce.Do(func() {
		if c, ok := t.proc.Stdin().(io.Closer); ok {
			_ = c.Close()
		}
		t.closeErr = t.proc.Wait()
	})
	return t.closeErr
}

// Kill 整组 SIGKILL(cliprocess 自带进程组投递,连 MCP server 等孙进程一起带走)。
func (t *procTransport) Kill() error { return t.proc.Kill() }

// PID 交出子进程号;拿不到时为 0。池快照用它把进程与会话对上。
func (t *procTransport) PID() int {
	if p, ok := t.proc.(interface{ PID() int }); ok {
		return p.PID()
	}
	return 0
}

func (t *procTransport) StderrTail() string { return t.stderr.String() }

// pumpStderr 把 agent stderr 逐行收进环形缓冲并打 Debug 日志。用 Default()
// 而非 ctx logger:transport 跨轮存活,没有稳定的 ctx。
func pumpStderr(buf *cliprocess.LockedBuffer, r io.Reader) {
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for scanner.Scan() {
		line := scanner.Bytes()
		_, _ = buf.Write(append([]byte(nil), line...))
		_, _ = buf.Write([]byte("\n"))
		if ln := strings.TrimSpace(string(line)); ln != "" {
			logger.Default().Debug("acp runtime: agent stderr", zap.String("line", ln))
		}
	}
	if err := scanner.Err(); err != nil {
		logger.Default().Debug("acp runtime: agent stderr pump stopped", zap.Error(err))
	}
}

// negotiated 是 initialize 握手敲定的结果:协议版本已判定为 v1,并记下
// 图片 / MCP-http / loadSession 这三项**按连接协商**的能力 —— 静态能力矩阵只
// 宣告「这条通道存在」,用不用由这里的值决定。
type negotiated struct {
	loadSession  bool
	promptImage  bool
	mcpHTTP      bool
	agentName    string
	agentTitle   string
	agentVersion string
	authMethods  []string
}

// agentreClientVersion 与 Keychain/Build 信息无关,只是 ACP agentInfo 的展示值;
// 起一个常量避免各调用点漂移。
const agentreClientName = "agentre"

// handshake 完成 initialize 握手、协议版本判定与 agentCapabilities 记录 ——
// 整个连接的版本决策收在这一处。
//
//   - 请求里 protocolVersion 填本实现支持的**最高版本**(= 1,ACP 的协商方式:
//     client 报最高的,agent 回它能给的);
//   - 回包版本不是 1 → 关掉连接、返回可读错误。绝不能硬按 v1 去解析一个 v2
//     对端 —— 那只会得到一屏莫名的解析失败,是最坏的失败形态;
//   - 将来支持 v2 的位置是「在握手之后按版本挑一个适配器」(translator 只处理
//     v1 的 SessionUpdate union)。不写任何 v2 字段、分支或占位代码 ——
//     没有 v2 agent 可验证,写了就是发明兼容层。
func handshake(ctx context.Context, conn *acpsdk.ClientSideConnection) (negotiated, error) {
	resp, err := conn.Initialize(ctx, acpsdk.InitializeRequest{
		ProtocolVersion: acpsdk.ProtocolVersionNumber,
		// fs / terminal 不宣告:ACP Agent 自带文件与命令工具,代理 fs 不增加
		// 能力,也不引入第二套文件权限面(Client 侧方法一律 method-not-found。
		ClientCapabilities: acpsdk.ClientCapabilities{
			Fs:       acpsdk.FileSystemCapabilities{},
			Terminal: false,
		},
		ClientInfo: &acpsdk.Implementation{Name: agentreClientName},
	})
	if err != nil {
		return negotiated{}, fmt.Errorf("acp: initialize handshake failed: %w", err)
	}
	if resp.ProtocolVersion != acpsdk.ProtocolVersionNumber {
		return negotiated{}, fmt.Errorf(
			"acp: agent %q requires ACP protocol version %d, but this build supports only v1; disconnecting",
			agentDisplayName(resp.AgentInfo), resp.ProtocolVersion)
	}
	n := negotiated{
		loadSession: resp.AgentCapabilities.LoadSession,
		promptImage: resp.AgentCapabilities.PromptCapabilities.Image,
		mcpHTTP:     resp.AgentCapabilities.McpCapabilities.Http,
		authMethods: make([]string, 0, len(resp.AuthMethods)),
	}
	if info := resp.AgentInfo; info != nil {
		n.agentName = info.Name
		n.agentVersion = info.Version
		if info.Title != nil {
			n.agentTitle = *info.Title
		}
	}
	for _, m := range resp.AuthMethods {
		if id := authMethodID(m); id != "" {
			n.authMethods = append(n.authMethods, id)
		}
	}
	return n, nil
}

// authMethodID 抽出一种认证方式的 id(三种变体各带一个 Id 字段)。
func authMethodID(m acpsdk.AuthMethod) string {
	switch {
	case m.EnvVar != nil:
		return m.EnvVar.Id
	case m.Terminal != nil:
		return m.Terminal.Id
	case m.Agent != nil:
		return m.Agent.Id
	default:
		return ""
	}
}

func agentDisplayName(info *acpsdk.Implementation) string {
	if info == nil {
		return "unknown"
	}
	return info.Name
}

// acpClient 实现 SDK 的 acp.Client:session/update 路由进会话的 update 通道,
// session/request_permission 走审批 waiter,fs / terminal 一律 method-not-found
// (能力没宣告,agent 不该调;调了也得到明确的拒绝而不是挂死)。
type acpClient struct {
	sess *agentSession
}

var _ acpsdk.Client = (*acpClient)(nil)

// SessionUpdate 是 agent 推来的通知(SDK 保证按到达顺序串行回调)。
func (c *acpClient) SessionUpdate(_ context.Context, params acpsdk.SessionNotification) error {
	c.sess.pushUpdate(params)
	return nil
}

// RequestPermission 阻塞等待 SubmitToolPermission 投回的用户决策。
// SDK 的 Client 接口不把 JSON-RPC id 交给实现方,所以 RequestID 由本层合成
// (会话内自增的 "acp-perm-N");它在 agentre 侧本来就是 backend 私有句柄,
// 回写时按它找到这里的 waiter 即可。
func (c *acpClient) RequestPermission(ctx context.Context, params acpsdk.RequestPermissionRequest) (acpsdk.RequestPermissionResponse, error) {
	return c.sess.awaitPermission(ctx, params)
}

func (c *acpClient) ReadTextFile(context.Context, acpsdk.ReadTextFileRequest) (acpsdk.ReadTextFileResponse, error) {
	return acpsdk.ReadTextFileResponse{}, acpsdk.NewMethodNotFound(string(acpsdk.ClientMethodFsReadTextFile))
}

func (c *acpClient) WriteTextFile(context.Context, acpsdk.WriteTextFileRequest) (acpsdk.WriteTextFileResponse, error) {
	return acpsdk.WriteTextFileResponse{}, acpsdk.NewMethodNotFound(string(acpsdk.ClientMethodFsWriteTextFile))
}

func (c *acpClient) CreateTerminal(context.Context, acpsdk.CreateTerminalRequest) (acpsdk.CreateTerminalResponse, error) {
	return acpsdk.CreateTerminalResponse{}, acpsdk.NewMethodNotFound(string(acpsdk.ClientMethodTerminalCreate))
}

func (c *acpClient) KillTerminal(context.Context, acpsdk.KillTerminalRequest) (acpsdk.KillTerminalResponse, error) {
	return acpsdk.KillTerminalResponse{}, acpsdk.NewMethodNotFound(string(acpsdk.ClientMethodTerminalKill))
}

func (c *acpClient) TerminalOutput(context.Context, acpsdk.TerminalOutputRequest) (acpsdk.TerminalOutputResponse, error) {
	return acpsdk.TerminalOutputResponse{}, acpsdk.NewMethodNotFound(string(acpsdk.ClientMethodTerminalOutput))
}

func (c *acpClient) ReleaseTerminal(context.Context, acpsdk.ReleaseTerminalRequest) (acpsdk.ReleaseTerminalResponse, error) {
	return acpsdk.ReleaseTerminalResponse{}, acpsdk.NewMethodNotFound(string(acpsdk.ClientMethodTerminalRelease))
}

func (c *acpClient) WaitForTerminalExit(context.Context, acpsdk.WaitForTerminalExitRequest) (acpsdk.WaitForTerminalExitResponse, error) {
	return acpsdk.WaitForTerminalExitResponse{}, acpsdk.NewMethodNotFound(string(acpsdk.ClientMethodTerminalWaitForExit))
}

// newAgentSession 起 transport、包 ClientSideConnection、跑握手,交回一个
// 完成协商的会话。调用方负责失败路径上的 Close。
func newAgentSession(ctx context.Context, tr agentTransport) (*agentSession, error) {
	s := &agentSession{
		tr:      tr,
		updates: make(chan acpsdk.SessionNotification, updateChannelCap),
	}
	s.conn = acpsdk.NewClientSideConnection(&acpClient{sess: s}, tr, tr)
	caps, err := handshake(ctx, s.conn)
	if err != nil {
		return nil, err
	}
	s.caps = caps
	return s, nil
}

// updateChannelCap 是 update 通道的缓冲上限。SDK 在 Prompt 返回前会等所有
// pre-response 通知处理完(水位线),所以一轮的全部 update 在 Prompt 返回时
// 已经进到这条通道里;缓冲只需覆盖「drain 循环短暂落后」的窗口。
const updateChannelCap = 256

// spawnAgent 起 ACP agent 子进程并完成握手。
func spawnAgent(ctx context.Context, spec launchSpec) (*agentSession, error) {
	tr, err := dialAgentTransport(ctx, spec)
	if err != nil {
		return nil, err
	}
	s, err := newAgentSession(ctx, tr)
	if err != nil {
		_ = tr.Close(context.Background())
		return nil, err
	}
	return s, nil
}

// renderMCPServers 把 RunRequest.MCPServers 渲染成 ACP 的 http transport MCP
// server 列表。协商不到 mcpCapabilities.http 时返回 (空列表, len(specs)):不注入、
// 该轮照常跑 —— MCP 注入是宿主为 org/subagent 工具加的通道,不是用户当轮的
// 显式动作,失败不该毁掉这轮(降级事实由调用方记日志)。
//
// 返回的**永远是非 nil 空切片**,不是 nil:SDK 的 NewSessionRequest / LoadSessionRequest
// 的 McpServers 字段没有 omitempty,nil 切片会序列化成 `"mcpServers": null`,而真实
// ACP agent 会直接拒 (实测 hermes acp 的 pydantic 校验报 `-32602 Invalid params:
// Input should be a valid list, input: null`)——会话根本起不来,三端全废。
// 「不注入」在线上必须写成 `[]`。
func renderMCPServers(caps negotiated, specs []agentruntime.MCPServerSpec) (servers []acpsdk.McpServer, skipped int) {
	if len(specs) == 0 {
		return []acpsdk.McpServer{}, 0
	}
	if !caps.mcpHTTP {
		return []acpsdk.McpServer{}, len(specs)
	}
	servers = make([]acpsdk.McpServer, 0, len(specs))
	for _, spec := range specs {
		headers := make([]acpsdk.HttpHeader, 0, len(spec.Headers))
		names := make([]string, 0, len(spec.Headers))
		for k := range spec.Headers {
			names = append(names, k)
		}
		sort.Strings(names)
		for _, k := range names {
			headers = append(headers, acpsdk.HttpHeader{Name: k, Value: spec.Headers[k]})
		}
		servers = append(servers, acpsdk.McpServer{Http: &acpsdk.McpServerHttpInline{
			Type:    "http",
			Name:    spec.Name,
			Url:     spec.URL,
			Headers: headers,
		}})
	}
	return servers, 0
}

// stderrTailLimited 交回 stderr 尾巴(截到 2KB),拼进进程死亡类错误。
func stderrTailLimited(s string) string {
	const tailCap = 2048
	if len(s) > tailCap {
		s = s[len(s)-tailCap:]
	}
	return strings.TrimSpace(s)
}
