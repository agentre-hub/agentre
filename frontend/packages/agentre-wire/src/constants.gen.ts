/**
 * wire 协议常量:RPC 方法名 / 通知名 / 错误码 / 会话生命周期 / 拉取上限。
 *
 * 本文件由 Go 生成器产出,**不要手改** —— 手改会被下一次重新生成覆盖,
 * 而且 TestGeneratedTSFresh 会立刻变红。
 *
 * 真理源:  internal/pkg/agentruntime/runtimes/remote/wire/wire.go
 * 生成器:  internal/pkg/agentruntime/runtimes/remote/wire/tsgen_test.go
 * 重新生成:
 *
 *   WIRE_TS_WRITE=1 go test ./internal/pkg/agentruntime/runtimes/remote/wire/ -run TestWriteTSCodec
 *
 * 边界:wire 包**之外**的类型一律映射成 unknown。它们大多没有 JSON tag
 * (按 Go 字段名裸序列化,如 agentruntime.MCPServerSpec 的 Name/URL),
 * TS 侧从未为它们建过类型;追进去等于凭空发明一份新契约。
 *
 * 格式:生成器直接输出 Prettier(printWidth 80,本仓默认配置)的形态,
 * 与手写代码同一套 ESLint 规则,没有整文件豁免。格式化是产物的一部分 ——
 * 若放到生成之后当外部工序,「重新生成 → 逐字节比对」的守卫会永久误报。
 */

export const MethodCapabilities = "runtime.capabilities";

export const MethodRun = "runtime.run";

export const MethodSteer = "runtime.steer";

export const MethodCancelSteer = "runtime.cancelSteer";

export const MethodDrainPending = "runtime.drainPending";

export const MethodAbort = "runtime.abort";

export const MethodStopBackgroundTask = "runtime.stopBackgroundTask";

export const MethodSetPermissionMode = "runtime.setPermissionMode";

export const MethodSubmitAnswer = "runtime.submitAnswer";

export const MethodSubmitToolPermission = "runtime.submitToolPermission";

export const MethodGetGoal = "runtime.goal.get";

export const MethodSetGoal = "runtime.goal.set";

export const MethodClearGoal = "runtime.goal.clear";

/**
 * 断连重连的补齐族。客户端重连后的三步是 list → attach → pull(→ pendingWaiters),
 * 每一步都限定在调用方自己的对端范围内(R16),对端身份取自那条连接的鉴权状态,
 * 不由参数携带 —— 参数里的对端标识等于让任何已配对设备点名读别人的会话。
 *
 * 老版本 daemon 不认识这四个方法,会回 method-not-found;客户端据此判定该 daemon
 * 不支持本规格并回落到「断连即终止」(R18),所以它们必须是**新增**方法而不是给
 * 既有方法加参数。
 */
export const MethodSessionList = "runtime.session.list";

export const MethodSessionPull = "runtime.session.pull";

export const MethodSessionPendingWaiters = "runtime.session.pendingWaiters";

/**
 * MethodSessionAttach 是**显式接管**:客户端声明「这条会话此后由我消费」,daemon
 * 受理后才把该会话的通知推送目标改到这条连接上。
 *
 * 它必须独立存在,不能并进 list / pull:list 只是看一眼有哪些会话(看一眼不该改变
 * 任何东西),pull 是只读补齐(它在补齐**完成前**就改推送目标才对,不然补齐期间的
 * 实时通知会只落库不推送)。今天 daemon 侧的认领是隐式的 —— 任何被受理的 runtime.*
 * 都会把流指向发起它的那条连接,哪怕那条连接根本不打算消费它。补齐族不走这条隐式
 * 路径,所以重连的客户端需要一个不含副作用的入口明说这件事。
 */
export const MethodSessionAttach = "runtime.session.attach";

/** daemon → client 通知。 */
export const NotifyEvent = "runtime.event";

export const NotifyRunResultDone = "runtime.runResultDone";

/**
 * MethodMCPProxy 是 daemon → client 的反向请求(request/response):daemon 上的 CLI
 * 子进程访问内置工具 MCP(org/subagent/group/workflow)时,这些 /mcp/* handler 的真身
 * 在 desktop。daemon 把 CLI 打到本地的 HTTP 请求原样隧道回 desktop 执行,应答原路返回,
 * 修「remote 不支持内置工具(URL 是 desktop 的 127.0.0.1)」。鉴权靠 Header 里 desktop
 * 轮起手时签的 MCP token(随 RunRequest.MCPServers 下发),在 desktop 侧校验。
 */
export const MethodMCPProxy = "runtime.mcpProxy";

/**
 * 自主续轮(AutonomousTurnSource):backend 自发跑的一轮,daemon 转发给 client。
 * 一轮 = Started → Event* → Done(同一 sessionID,串行,无重叠);Event 复用
 * EventFrame、Done 复用 RunResultDoneFrame,只是走各自的 notify 方法区分归属
 * (普通 Run 流 vs 自主续轮流),sessionID 仍负责会话路由。
 */
export const NotifyAutonomousTurnStarted = "runtime.autonomousTurn.started";

export const NotifyAutonomousTurnEvent = "runtime.autonomousTurn.event";

export const NotifyAutonomousTurnDone = "runtime.autonomousTurn.done";

export const ErrCodeNoActiveTurn = -32010;

export const ErrCodeSteerNotFound = -32011;

export const ErrCodeUnsupported = -32012;

export const ErrCodeAborted = -32013;

export const ErrCodeSessionNotFound = -32014;

/**
 * CapLLMModelTargetV1 是 daemon 在 health.ping 里公布的能力位：本 daemon 支持
 * 按 ModelKey 解析 fixed-model（决策 11）。桌面端据此在 Picker 里禁用不支持
 * fixed-model 的旧 daemon 上的固定模型选项 —— 旧 daemon 即使收到 ModelKey 也会
 * 静默按 provider-default 执行，正是规格禁止的降级，所以必须先查能力位再允许选择。
 */
export const CapLLMModelTargetV1 = "llm-model-target-v1";

export const SessionLifecycleRunning = "running";

export const SessionLifecycleIdle = "idle";

export const SessionLifecycleInterrupted = "interrupted";

export const DefaultSessionPullLimit = 200;

export const MaxSessionPullLimit = 1000;
