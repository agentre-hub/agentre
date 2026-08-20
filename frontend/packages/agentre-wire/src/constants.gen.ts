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

/**
 * MethodSessionDelete 删掉这一端上的那条会话:agentred 上是会话行与它的整段通知
 * 日志,桌面端上是**那台电脑自己那条对话本体**。两种端一视同仁地受理它 —— 会话
 * 在哪台机器上执行,删除就在哪台机器上生效。
 *
 * 它同样是**新增**方法而不是给既有方法加参数:老版本的执行端不认识它,会回
 * method-not-found,调用方据此判定「这台机器这辈子都答不了」并如实降级(判据见
 * ErrSessionDeleteUnsupported),而不是把一条永远失败的删除重试到天荒地老。
 */
export const MethodSessionDelete = "runtime.session.delete";

/**
 * MethodSkillsCatalog 列出**这台机器上**某一档执行目标的技能目录:已装包(含全局
 * 启用态)并上 agentre 的推荐包,逐行标注这一档授权了没有。它替掉浏览器控制台里
 * 「手打 skill id」——浏览器此前没有任何办法知道那台机器上到底装了什么。
 *
 * 它**不在** runtime.* 下:技能装在机器上,与任何一轮执行无关,问它不需要、也不该
 * 需要一条会话。
 *
 * 授权集由**调用方随请求带上**(SkillCatalogParams.Authorized),而不是执行端自己去
 * 查:执行目标与它的技能授权(R15e「一档一块」)存在组织架构库里,agentred 上没有
 * 那个库 —— 让它猜等于让它拿别的档、或者干脆拿空授权来答。谁掌握那一档的授权谁
 * 就得说出来,这样「一档一块」在协议上就是显式的,不靠两边默契。
 */
export const MethodSkillsCatalog = "skills.catalog";

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
 * ErrCodePeerExecutionUnavailable:会话钉住的执行目标(agentred)当前不可用。
 *
 * 与上面五个不同,它**不进 SentinelFromCode**:那张表翻的是 daemon 回给
 * remote 客户端的 agentruntime sentinel,而这一条是桌面端 peer 回给浏览器的,
 * 两条链路不同。放这里的唯一理由是让它跟着 wire 一起生成给 TS ——
 * 浏览器要靠它把「执行目标不可用」(停用写入 + 状态横幅)与普通拒绝区分开,
 * 此前是在 agentre-server 里手抄的一个魔数,改了这边不会有任何地方变红。
 *
 * 应答里同时带类型化 data(accepted / historyAvailable / executionUnavailable)。
 */
export const ErrCodePeerExecutionUnavailable = -32015;

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

/** SkillDiscoveryOK 目录是问出来的,可以照它增删。 */
export const SkillDiscoveryOK = "ok";

/**
 * SkillDiscoveryUnavailable 这台机器此刻答不出:CLI 找不到、枚举失败。
 * 目录为空**不代表**没有包,调用方不得据此认为可添加集是空的。
 */
export const SkillDiscoveryUnavailable = "unavailable";

/**
 * SkillDiscoveryUnsupported 这个 backend 类型没有技能这一说(builtin / piagent /
 * openclaw 都不声明 CapSkills)。与 unavailable 不同,它是**稳定**的答案:再问一次、
 * 等机器空闲了再问,结果都一样。
 */
export const SkillDiscoveryUnsupported = "unsupported";
