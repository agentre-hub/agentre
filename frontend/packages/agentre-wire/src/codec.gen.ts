/**
 * wire 协议帧类型与编解码:与 wire.go 的 JSON tag 逐字段同构。
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

import {
  type WireObject,
  decodeWire,
  encodeWire,
  optArr,
  optArrOf,
  optBool,
  optNum,
  optObj,
  optOf,
  optStr,
  reqArrOf,
  reqBool,
  reqNum,
  reqStr,
} from "./runtime";

/**
 * ModelSummary describes a single model configured for a daemon provider.
 * Non-sensitive：只含稳定 key / 实际 model id / 展示名 / 启用态，绝不含凭证。
 */
export interface ModelSummary extends WireObject {
  key: string;
  modelId: string;
  name?: string;
  enabled: boolean;
}

export function decodeModelSummary(v: unknown): ModelSummary {
  return decodeWire<ModelSummary>(v, "ModelSummary", (o) => {
    o.key = reqStr(o.key, "ModelSummary.key");
    o.modelId = reqStr(o.modelId, "ModelSummary.modelId");
    o.name = optStr(o.name, "ModelSummary.name");
    o.enabled = reqBool(o.enabled, "ModelSummary.enabled");
  });
}

export function encodeModelSummary(v: ModelSummary): string {
  return encodeWire(v);
}

/**
 * ProviderSummary describes a single LLM provider configured in the daemon
 * state. Returned by health.ping so desktop watcher can render sync status.
 * 只含非敏感字段：Provider/Model 的稳定 key + 展示名 + 实际 model id；
 * APIKey / BaseURL 永不进这条目录（凭证执行侧本地化）。
 */
export interface ProviderSummary extends WireObject {
  key: string;
  name: string;
  type: string;
  defaultModelKey?: string;
  models?: ModelSummary[];
}

export function decodeProviderSummary(v: unknown): ProviderSummary {
  return decodeWire<ProviderSummary>(v, "ProviderSummary", (o) => {
    o.key = reqStr(o.key, "ProviderSummary.key");
    o.name = reqStr(o.name, "ProviderSummary.name");
    o.type = reqStr(o.type, "ProviderSummary.type");
    o.defaultModelKey = optStr(
      o.defaultModelKey,
      "ProviderSummary.defaultModelKey",
    );
    o.models = optArrOf(o.models, "ProviderSummary.models", decodeModelSummary);
  });
}

export function encodeProviderSummary(v: ProviderSummary): string {
  return encodeWire(v);
}

/**
 * OK 大部分 mutating 方法不需要返回值,统一返这个空 struct 表示成功。
 * 是「成功无 payload」。
 */
export type OK = WireObject;

export function decodeOK(v: unknown): OK {
  return decodeWire<OK>(v, "OK", () => {});
}

export function encodeOK(v: OK): string {
  return encodeWire(v);
}

/**
 * PeerSessionControlResult reports that another endpoint won the race to answer
 * a pending ask or tool permission. Older peers return an empty object, whose
 * zero value preserves the original successful outcome.
 */
export interface PeerSessionControlResult extends WireObject {
  alreadyHandled?: boolean;
}

export function decodePeerSessionControlResult(
  v: unknown,
): PeerSessionControlResult {
  return decodeWire<PeerSessionControlResult>(
    v,
    "PeerSessionControlResult",
    (o) => {
      o.alreadyHandled = optBool(
        o.alreadyHandled,
        "PeerSessionControlResult.alreadyHandled",
      );
    },
  );
}

export function encodePeerSessionControlResult(
  v: PeerSessionControlResult,
): string {
  return encodeWire(v);
}

export interface GoalParams extends WireObject {
  conversationId: string;
  peerFingerprint?: string;
  agentId?: number;
  providerSessionId: string;
  backend?: unknown;
  cwd?: string;
  objective?: string;
  status?: string;
  tokenBudget?: number;

  /**
   * LLMProviderKey / LLMModelKey 与 RunParams 同形（决策 11）：goal 与 turn 共用
   * 同一个 CLI 会话池，两边解析不一致会让启动期比对键反复翻转。daemon 按这组
   * key 从自家目录解析，wire 永不携带 APIKey / BaseURL / Provider 行正文。
   */
  llmProviderKey?: string;
  llmModelKey?: string;
}

export function decodeGoalParams(v: unknown): GoalParams {
  return decodeWire<GoalParams>(v, "GoalParams", (o) => {
    o.conversationId = reqStr(o.conversationId, "GoalParams.conversationId");
    o.peerFingerprint = optStr(o.peerFingerprint, "GoalParams.peerFingerprint");
    o.agentId = optNum(o.agentId, "GoalParams.agentId");
    o.providerSessionId = reqStr(
      o.providerSessionId,
      "GoalParams.providerSessionId",
    );
    o.cwd = optStr(o.cwd, "GoalParams.cwd");
    o.objective = optStr(o.objective, "GoalParams.objective");
    o.status = optStr(o.status, "GoalParams.status");
    o.tokenBudget = optNum(o.tokenBudget, "GoalParams.tokenBudget");
    o.llmProviderKey = optStr(o.llmProviderKey, "GoalParams.llmProviderKey");
    o.llmModelKey = optStr(o.llmModelKey, "GoalParams.llmModelKey");
  });
}

export function encodeGoalParams(v: GoalParams): string {
  return encodeWire(v);
}

export interface GoalResult extends WireObject {
  goal?: unknown;
}

export function decodeGoalResult(v: unknown): GoalResult {
  return decodeWire<GoalResult>(v, "GoalResult", () => {});
}

export function encodeGoalResult(v: GoalResult): string {
  return encodeWire(v);
}

export interface GoalClearResult extends WireObject {
  cleared: boolean;
}

export function decodeGoalClearResult(v: unknown): GoalClearResult {
  return decodeWire<GoalClearResult>(v, "GoalClearResult", (o) => {
    o.cleared = reqBool(o.cleared, "GoalClearResult.cleared");
  });
}

export function encodeGoalClearResult(v: GoalClearResult): string {
  return encodeWire(v);
}

/** CapabilitiesParams 按 BackendType 查 daemon 端 runtime 的能力矩阵。 */
export interface CapabilitiesParams extends WireObject {
  backendType: string;
}

export function decodeCapabilitiesParams(v: unknown): CapabilitiesParams {
  return decodeWire<CapabilitiesParams>(v, "CapabilitiesParams", (o) => {
    o.backendType = reqStr(o.backendType, "CapabilitiesParams.backendType");
  });
}

export function encodeCapabilitiesParams(v: CapabilitiesParams): string {
  return encodeWire(v);
}

/** CapabilitiesResult 直接透传 capability.Capabilities — 含 Set 映射 + PermissionModeMeta。 */
export interface CapabilitiesResult extends WireObject {
  capabilities: unknown;
}

export function decodeCapabilitiesResult(v: unknown): CapabilitiesResult {
  return decodeWire<CapabilitiesResult>(v, "CapabilitiesResult", () => {});
}

export function encodeCapabilitiesResult(v: CapabilitiesResult): string {
  return encodeWire(v);
}

/**
 * HistoryMessageWire 是 agentruntime.HistoryMessage 的 wire 镜像。blocks 字段
 * 走 blocks.StoredBlock(已经是 discriminated envelope)。
 */
export interface HistoryMessageWire extends WireObject {
  role: string;
  blocks?: unknown[];
}

export function decodeHistoryMessageWire(v: unknown): HistoryMessageWire {
  return decodeWire<HistoryMessageWire>(v, "HistoryMessageWire", (o) => {
    o.role = reqStr(o.role, "HistoryMessageWire.role");
    o.blocks = optArr(o.blocks, "HistoryMessageWire.blocks");
  });
}

export function encodeHistoryMessageWire(v: HistoryMessageWire): string {
  return encodeWire(v);
}

/**
 * RunParams 是 runtime.run 的请求体。镜像 agentruntime.RunRequest 跨进程需要的
 * 字段子集。Backend 用 json.RawMessage 透传,避免 wire 层硬依赖 entity 内部结构。
 *
 * 故意没有 Provider / GatewayURL / GatewayToken:
 *   - Provider 含明文 APIKey,desktop 不该每个 turn 越线漂移到远端 daemon;
 *   - GatewayURL/Token 来自 desktop 本机 127.0.0.1,在 daemon 主机上根本拨不到。
 *
 * daemon 端 handlers/runtime.go 在 Run 入口处自己用 ProviderLookup + 自家
 * Gateway 解出这三者,desktop 端 chat_svc.runTurn 检 be.IsRemote() 后也不再填。
 */
export interface RunParams extends WireObject {
  backend: unknown;
  agentId: number;
  conversationId: string;

  /**
   * PeerFingerprint 点名这一轮要落在**哪个对端**名下的那条会话上(R9)。会话键是
   * (发起端指纹, 会话 id),而清单(SessionSummary.PeerFingerprint)是客户端学 origin
   * 的唯一来源;省略 = 调用方自己的对端,与控制族(attach / pull / abort / submit)
   * 同一条 ResolveSessionPeer 约定 —— 点名别人的 origin 是账号级能力,配对身份点名
   * 一律被拒。不点名地给别人的会话开新一轮,会在调用方名下另建一条同号会话:上下文
   * (决策 8 的 provider_session_id)续不上,事件也落到另一个 journal 分区,发起端与
   * 它的其余订阅者一条都收不到(R6 / R18 的前提)。
   */
  peerFingerprint?: string;
  cwd?: string;

  /**
   * Title 是该会话此刻的标题(R7)。桌面端每轮携带当前值,daemon 幂等覆盖;调用方
   * 不带这一格时保持空串。改名后最多滞后一轮生效。
   */
  title?: string;

  /**
   * AgentSyncID 是该会话所属 Agent 的账号级同步标识(块 1,决策 3 的 ULID,不是本地
   * 自增 agent_id)。会话列表按它解析 Agent 名与头像(R5)。
   */
  agentSyncId?: string;

  /**
   * ProjectSyncID 是该会话所属项目的账号级同步标识。它与 AgentSyncID 同批携带、
   * 由 daemon 幂等覆盖。
   *
   * 之所以要发起端报而不是让服务端从 cwd 推:日活跃统计按项目分组,而那条通道只
   * 上行计数、不上行任何路径 —— 推不出来。空串 = 这一轮不属于任何项目(或调用方
   * 是老版本),不是「未知待推导」。
   */
  projectSyncId?: string;
  systemPrompt?: string;
  providerSessionId?: string;

  /**
   * FreshSession 声明这一轮**必须起全新会话**:即使 daemon 在自家落库里存了这条会话
   * 的 provider_session_id,也不许续(挂账修复,2026-08-11)。决策 8 之后「空
   * ProviderSessionID」的语义被重载成「用落库那份续话」,而 regenerate 与 provider 会话
   * 失效恢复这两条路径的空字段本意是「全新」—— 两者在 wire 上不可区分,daemon 拿旧 id
   * 顶掉:regenerate 退化成续旧上下文、gone 恢复永远撞同一个失效 id。本字段是那个
   * 「全新」意图的显式出口。三种取值:ProviderSessionID 非空 = resume;空 +
   * FreshSession=false(缺省)= 决策 8 续话(daemon 续落库那份);空 + FreshSession=true
   * = 全新,忽略落库。浏览器续话不置它;桌面端在本地 sess.ProviderSessionID 为空时置它。
   */
  freshSession?: boolean;
  userText?: string;
  userBlocks?: unknown[];
  history?: HistoryMessageWire[];
  compact?: boolean;
  forkAnchor?: string;
  permissionMode?: string;
  collaborationMode?: string;

  /**
   * MCPServers 注入给 runtime 的 MCP tool server（org/subagent/hook 工具等）。漏传会让
   * 远程后端的 launch-time MCP 注入失效，故必须随 wire 过线。
   */
  mcpServers?: unknown[];

  /**
   * EnabledPlugins 注入给 runtime 的 per-agent plugin/skill-pack 覆盖。漏传会让
   * 远程 CapSkills 后端展示可配置但实际继承全局配置。
   */
  enabledPlugins?: Record<string, boolean>;

  /**
   * LLMProviderKey 是 desktop 端关联的 provider stable key（UUID）。
   * daemon 用它做 ProviderLookup（FindByKey），不需要 desktop 越线传 APIKey。
   * 决策 9 后它携带 effectiveProviderKey（会话 provider_key 优先），daemon 自解。
   */
  llmProviderKey?: string;

  /**
   * LLMModelKey 是执行侧目标的稳定 ModelKey（决策 11）：空 = provider-default
   * （daemon 解析该 Provider 当前默认模型），非空 = fixed-model（daemon 精确解析
   * 该模型，缺失/停用/旧 daemon 一律严格拒绝，绝不静默降级为默认模型）。
   */
  llmModelKey?: string;

  /**
   * ReasoningEffort 是本轮的**有效**思考力度(会话覆盖 > 后端配置,由发起端的那一个
   * 边界函数合成),与 LLMProviderKey / LLMModelKey 同形单列过线。
   *
   * 它不塞进 Backend 负载:浏览器端派发时送出的负载只有一个 {type} 空壳,力度在那条
   * 路径上恒为空(spec 2026-09-01 决策 4)。
   *
   * 空串在这里是「调用方什么都没说」,**不是**「用户选了默认档」:执行侧取值时
   * run 参数非空优先、为空回落 backend 负载里的力度(硬不变量 6)。留空的是**同代**
   * 调用方 —— 没有会话级覆盖的轮次,以及尚未接线该字段的浏览器派发;跨代对端不在
   * 此列:方法集变更已把协议窗口收成单点 0.2.0,握手期即被拒。会话真的改回
   * 「跟随后端配置」时,发起端合成出来的仍是后端配置那个值,两者不冲突。
   */
  reasoningEffort?: string;

  /**
   * SourceDevice / SourceDeviceName 是「开新一轮」发起方的设备身份（R18/R19）。
   * 浏览器**每轮**随 runtime.run 声明自己的设备指纹与显示名（如「Chrome · macOS」）：
   * 握手（auth.account）只带指纹、不带显示名，而 R19 要的是「人能认出的名字」，所以
   * 名字走这里而不是握手。daemon 据此在事件流开头注入一条 user_message 标记，扇出给
   * 同一条会话的其余订阅者，让桌面端把这一轮落成一行带来源标识的用户消息。桌面端自己
   * 发消息不传这两个字段 → 不注入、消息不带来源标识，单端界面零变化（R17 既有承诺不变）。
   */
  sourceDevice?: string;
  sourceDeviceName?: string;
}

export function decodeRunParams(v: unknown): RunParams {
  return decodeWire<RunParams>(v, "RunParams", (o) => {
    o.agentId = reqNum(o.agentId, "RunParams.agentId");
    o.conversationId = reqStr(o.conversationId, "RunParams.conversationId");
    o.peerFingerprint = optStr(o.peerFingerprint, "RunParams.peerFingerprint");
    o.cwd = optStr(o.cwd, "RunParams.cwd");
    o.title = optStr(o.title, "RunParams.title");
    o.agentSyncId = optStr(o.agentSyncId, "RunParams.agentSyncId");
    o.projectSyncId = optStr(o.projectSyncId, "RunParams.projectSyncId");
    o.systemPrompt = optStr(o.systemPrompt, "RunParams.systemPrompt");
    o.providerSessionId = optStr(
      o.providerSessionId,
      "RunParams.providerSessionId",
    );
    o.freshSession = optBool(o.freshSession, "RunParams.freshSession");
    o.userText = optStr(o.userText, "RunParams.userText");
    o.userBlocks = optArr(o.userBlocks, "RunParams.userBlocks");
    o.history = optArrOf(
      o.history,
      "RunParams.history",
      decodeHistoryMessageWire,
    );
    o.compact = optBool(o.compact, "RunParams.compact");
    o.forkAnchor = optStr(o.forkAnchor, "RunParams.forkAnchor");
    o.permissionMode = optStr(o.permissionMode, "RunParams.permissionMode");
    o.collaborationMode = optStr(
      o.collaborationMode,
      "RunParams.collaborationMode",
    );
    o.mcpServers = optArr(o.mcpServers, "RunParams.mcpServers");
    o.enabledPlugins = optObj(o.enabledPlugins, "RunParams.enabledPlugins");
    o.llmProviderKey = optStr(o.llmProviderKey, "RunParams.llmProviderKey");
    o.llmModelKey = optStr(o.llmModelKey, "RunParams.llmModelKey");
    o.reasoningEffort = optStr(o.reasoningEffort, "RunParams.reasoningEffort");
    o.sourceDevice = optStr(o.sourceDevice, "RunParams.sourceDevice");
    o.sourceDeviceName = optStr(
      o.sourceDeviceName,
      "RunParams.sourceDeviceName",
    );
  });
}

export function encodeRunParams(v: RunParams): string {
  return encodeWire(v);
}

/**
 * MCPProxyRequest 是 daemon→desktop 隧道里一次 MCP HTTP 请求的封装。daemon 把 CLI 子进程
 * 打到 daemon 本地 /mcp/* 的请求原样装包发回 desktop;desktop 用本机真 gateway 重放。
 * MCP-over-HTTP 是纯请求/应答(server→client SSE 被 handler 405 掉),所以单帧封装即可。
 */
export interface MCPProxyRequest extends WireObject {
  /** 如 /mcp/org/ */
  path: string;

  /** HTTP 方法(POST/GET/...) */
  method: string;

  /** 含 Authorization: Bearer <token> */
  headers?: Record<string, string[]>;

  /** MCP JSON-RPC HTTP body */
  body?: string;
}

export function decodeMCPProxyRequest(v: unknown): MCPProxyRequest {
  return decodeWire<MCPProxyRequest>(v, "MCPProxyRequest", (o) => {
    o.path = reqStr(o.path, "MCPProxyRequest.path");
    o.method = reqStr(o.method, "MCPProxyRequest.method");
    o.headers = optObj(o.headers, "MCPProxyRequest.headers");
    o.body = optStr(o.body, "MCPProxyRequest.body");
  });
}

export function encodeMCPProxyRequest(v: MCPProxyRequest): string {
  return encodeWire(v);
}

/** MCPProxyResponse 是 desktop 重放 MCPProxyRequest 后的 HTTP 应答封装。 */
export interface MCPProxyResponse extends WireObject {
  status: number;
  headers?: Record<string, string[]>;
  body?: string;
}

export function decodeMCPProxyResponse(v: unknown): MCPProxyResponse {
  return decodeWire<MCPProxyResponse>(v, "MCPProxyResponse", (o) => {
    o.status = reqNum(o.status, "MCPProxyResponse.status");
    o.headers = optObj(o.headers, "MCPProxyResponse.headers");
    o.body = optStr(o.body, "MCPProxyResponse.body");
  });
}

export function encodeMCPProxyResponse(v: MCPProxyResponse): string {
  return encodeWire(v);
}

/**
 * RunAck 是 runtime.run 的同步返回,只回 echo 客户端传的 sessionID 供它确认。
 * 真实 events 走 NotifyEvent 异步推;终态 RunResult 走 NotifyRunResultDone。
 *
 * ProviderSessionID 是 runtime 在事件流开始前已经确认的 provider 原生
 * Session 身份（当前由 Pi Agent 使用），让 desktop 可在流结束前持久化。
 *
 * LaunchPermissionMode 是 daemon 端 runtime spawn CLI 子进程实际下发的
 * --permission-mode 值(claudecode 专用)。同步随 ack 回来,客户端立即写入
 * RunResult.LaunchPermissionMode,让 chat_svc 在主进程侧持久化到
 * session.PermissionModeAtLaunch。空串 = runtime 未指定(其它 backend / 复
 * 用现有 CLI 进程)。
 *
 * ProviderFallbackKey 是决策 9 的回退信号:wire 带 effectiveProviderKey(会话
 * provider_key 优先),daemon 自解时该 key 缺失/非 active → 回退 agent 绑定(或 CLI
 * 登录态)执行,并把被回退的 key 放进此字段回传。非空时桌面端据此追加一条持久
 * notice(与本地 Q3 一致);空串 = 未回退。
 */
export interface RunAck extends WireObject {
  conversationId: string;
  providerSessionId?: string;
  launchPermissionMode?: string;
  providerFallbackKey?: string;
}

export function decodeRunAck(v: unknown): RunAck {
  return decodeWire<RunAck>(v, "RunAck", (o) => {
    o.conversationId = reqStr(o.conversationId, "RunAck.conversationId");
    o.providerSessionId = optStr(
      o.providerSessionId,
      "RunAck.providerSessionId",
    );
    o.launchPermissionMode = optStr(
      o.launchPermissionMode,
      "RunAck.launchPermissionMode",
    );
    o.providerFallbackKey = optStr(
      o.providerFallbackKey,
      "RunAck.providerFallbackKey",
    );
  });
}

export function encodeRunAck(v: RunAck): string {
  return encodeWire(v);
}

/** SteerParams 等同 agentruntime.Steerer.Steer 的入参。 */
export interface SteerParams extends WireObject {
  conversationId: string;
  peerFingerprint?: string;
  queuedId?: string;
  text: string;
}

export function decodeSteerParams(v: unknown): SteerParams {
  return decodeWire<SteerParams>(v, "SteerParams", (o) => {
    o.conversationId = reqStr(o.conversationId, "SteerParams.conversationId");
    o.peerFingerprint = optStr(
      o.peerFingerprint,
      "SteerParams.peerFingerprint",
    );
    o.queuedId = optStr(o.queuedId, "SteerParams.queuedId");
    o.text = reqStr(o.text, "SteerParams.text");
  });
}

export function encodeSteerParams(v: SteerParams): string {
  return encodeWire(v);
}

/** CancelSteerParams 等同 agentruntime.SteerCanceler.CancelSteer 的入参。 */
export interface CancelSteerParams extends WireObject {
  conversationId: string;
  peerFingerprint?: string;
  queuedId?: string;
}

export function decodeCancelSteerParams(v: unknown): CancelSteerParams {
  return decodeWire<CancelSteerParams>(v, "CancelSteerParams", (o) => {
    o.conversationId = reqStr(
      o.conversationId,
      "CancelSteerParams.conversationId",
    );
    o.peerFingerprint = optStr(
      o.peerFingerprint,
      "CancelSteerParams.peerFingerprint",
    );
    o.queuedId = optStr(o.queuedId, "CancelSteerParams.queuedId");
  });
}

export function encodeCancelSteerParams(v: CancelSteerParams): string {
  return encodeWire(v);
}

/**
 * CancelSteerResult 返已撤销的 queuedID 列表(空 queuedID 表示「清空所有未消费」,
 * daemon 据此返若干 id)。
 */
export interface CancelSteerResult extends WireObject {
  removed?: string[];
}

export function decodeCancelSteerResult(v: unknown): CancelSteerResult {
  return decodeWire<CancelSteerResult>(v, "CancelSteerResult", (o) => {
    o.removed = optArr(o.removed, "CancelSteerResult.removed");
  });
}

export function encodeCancelSteerResult(v: CancelSteerResult): string {
  return encodeWire(v);
}

/** DrainParams 等同 agentruntime.SteerDrainer.DrainPending 的入参。 */
export interface DrainParams extends WireObject {
  conversationId: string;
  peerFingerprint?: string;
}

export function decodeDrainParams(v: unknown): DrainParams {
  return decodeWire<DrainParams>(v, "DrainParams", (o) => {
    o.conversationId = reqStr(o.conversationId, "DrainParams.conversationId");
    o.peerFingerprint = optStr(
      o.peerFingerprint,
      "DrainParams.peerFingerprint",
    );
  });
}

export function encodeDrainParams(v: DrainParams): string {
  return encodeWire(v);
}

/**
 * DrainResult 返本轮 daemon 已 ack 但 hook 没拉走的 mid-turn steer 列表,
 * chat_svc 拿来 emit StreamSteerConsumed + persistAutoContinueTurn。
 */
export interface DrainResult extends WireObject {
  steers?: unknown[];
}

export function decodeDrainResult(v: unknown): DrainResult {
  return decodeWire<DrainResult>(v, "DrainResult", (o) => {
    o.steers = optArr(o.steers, "DrainResult.steers");
  });
}

export function encodeDrainResult(v: DrainResult): string {
  return encodeWire(v);
}

/**
 * AbortParams 等同 agentruntime.Aborter.Abort 的入参。
 * TurnToken 语义同 agentruntime:0 = 中断当前活跃轮;非 0 = 仅当该轮仍是当前活跃轮才中断。
 */
export interface AbortParams extends WireObject {
  conversationId: string;
  peerFingerprint?: string;
  turnToken?: number;
}

export function decodeAbortParams(v: unknown): AbortParams {
  return decodeWire<AbortParams>(v, "AbortParams", (o) => {
    o.conversationId = reqStr(o.conversationId, "AbortParams.conversationId");
    o.peerFingerprint = optStr(
      o.peerFingerprint,
      "AbortParams.peerFingerprint",
    );
    o.turnToken = optNum(o.turnToken, "AbortParams.turnToken");
  });
}

export function encodeAbortParams(v: AbortParams): string {
  return encodeWire(v);
}

/** AbortResult 是 MethodAbort 的应答,携带被中断轮的类型(agentruntime.AbortOutcome.TurnKind)。 */
export interface AbortResult extends WireObject {
  turnKind: string;
}

export function decodeAbortResult(v: unknown): AbortResult {
  return decodeWire<AbortResult>(v, "AbortResult", (o) => {
    o.turnKind = reqStr(o.turnKind, "AbortResult.turnKind");
  });
}

export function encodeAbortResult(v: AbortResult): string {
  return encodeWire(v);
}

/** StopBackgroundTaskParams 等同 agentruntime.BackgroundTaskStopper.StopBackgroundTask 的入参。 */
export interface StopBackgroundTaskParams extends WireObject {
  conversationId: string;
  peerFingerprint?: string;
  taskId: string;
}

export function decodeStopBackgroundTaskParams(
  v: unknown,
): StopBackgroundTaskParams {
  return decodeWire<StopBackgroundTaskParams>(
    v,
    "StopBackgroundTaskParams",
    (o) => {
      o.conversationId = reqStr(
        o.conversationId,
        "StopBackgroundTaskParams.conversationId",
      );
      o.peerFingerprint = optStr(
        o.peerFingerprint,
        "StopBackgroundTaskParams.peerFingerprint",
      );
      o.taskId = reqStr(o.taskId, "StopBackgroundTaskParams.taskId");
    },
  );
}

export function encodeStopBackgroundTaskParams(
  v: StopBackgroundTaskParams,
): string {
  return encodeWire(v);
}

/** SetPermissionModeParams 等同 agentruntime.PermissionModeSetter.SetPermissionMode 的入参。 */
export interface SetPermissionModeParams extends WireObject {
  conversationId: string;
  peerFingerprint?: string;
  mode: string;
}

export function decodeSetPermissionModeParams(
  v: unknown,
): SetPermissionModeParams {
  return decodeWire<SetPermissionModeParams>(
    v,
    "SetPermissionModeParams",
    (o) => {
      o.conversationId = reqStr(
        o.conversationId,
        "SetPermissionModeParams.conversationId",
      );
      o.peerFingerprint = optStr(
        o.peerFingerprint,
        "SetPermissionModeParams.peerFingerprint",
      );
      o.mode = reqStr(o.mode, "SetPermissionModeParams.mode");
    },
  );
}

export function encodeSetPermissionModeParams(
  v: SetPermissionModeParams,
): string {
  return encodeWire(v);
}

/**
 * SetModelTargetParams 改这条会话钉的 LLM ModelTarget,语义等同桌面端的
 * chat_svc.SetChatSessionModelTarget:
 *   - ProviderKey 空 + ModelKey 空 = 改回「跟随 Agent 绑定」(CLI 后端即回到自身
 *     登录态)。这是一个**要写下去的值**,不是「不改」—— 用户从固定模型改回跟随
 *     绑定时不清空,就等于这次改动没发生;
 *   - ProviderKey 非空 + ModelKey 空 = 该供应商当前的默认模型;
 *   - 两者都非空 = 固定模型。
 *
 * 新目标自**下一轮**生效,正在跑的那一轮不受影响。会话不存在时报错而不是折成
 * 成功:那会让调用方以为下一轮会用新模型,而实际上一行都没写。
 */
export interface SetModelTargetParams extends WireObject {
  conversationId: string;
  peerFingerprint?: string;
  providerKey?: string;
  modelKey?: string;
}

export function decodeSetModelTargetParams(v: unknown): SetModelTargetParams {
  return decodeWire<SetModelTargetParams>(v, "SetModelTargetParams", (o) => {
    o.conversationId = reqStr(
      o.conversationId,
      "SetModelTargetParams.conversationId",
    );
    o.peerFingerprint = optStr(
      o.peerFingerprint,
      "SetModelTargetParams.peerFingerprint",
    );
    o.providerKey = optStr(o.providerKey, "SetModelTargetParams.providerKey");
    o.modelKey = optStr(o.modelKey, "SetModelTargetParams.modelKey");
  });
}

export function encodeSetModelTargetParams(v: SetModelTargetParams): string {
  return encodeWire(v);
}

/**
 * SetSessionReasoningEffortParams 改这条会话钉的思考力度,语义与 SetModelTargetParams
 * 逐条对齐:
 *   - ReasoningEffort 空串 = 改回「跟随后端配置」。这是一个**要写下去的值**,不是
 *     「不改」—— 用户从固定档改回跟随配置时不清空,就等于这次改动没发生;
 *   - 非空 = 六档词表里的那一档(low / medium / high / xhigh / max)。
 *
 * 新档位自**下一轮**生效,正在跑的那一轮不受影响。会话不存在时报错而不是折成成功:
 * 那会让调用方以为下一轮会用新档位,而实际上一行都没写。
 */
export interface SetSessionReasoningEffortParams extends WireObject {
  conversationId: string;
  peerFingerprint?: string;
  reasoningEffort?: string;
}

export function decodeSetSessionReasoningEffortParams(
  v: unknown,
): SetSessionReasoningEffortParams {
  return decodeWire<SetSessionReasoningEffortParams>(
    v,
    "SetSessionReasoningEffortParams",
    (o) => {
      o.conversationId = reqStr(
        o.conversationId,
        "SetSessionReasoningEffortParams.conversationId",
      );
      o.peerFingerprint = optStr(
        o.peerFingerprint,
        "SetSessionReasoningEffortParams.peerFingerprint",
      );
      o.reasoningEffort = optStr(
        o.reasoningEffort,
        "SetSessionReasoningEffortParams.reasoningEffort",
      );
    },
  );
}

export function encodeSetSessionReasoningEffortParams(
  v: SetSessionReasoningEffortParams,
): string {
  return encodeWire(v);
}

/** SubmitAnswerParams 等同 agentruntime.AskAnswerSink.SubmitAnswer 的入参。 */
export interface SubmitAnswerParams extends WireObject {
  conversationId: string;
  peerFingerprint?: string;
  requestId: string;
  questions?: unknown[];
  answers?: unknown[];
  skipped?: boolean;
}

export function decodeSubmitAnswerParams(v: unknown): SubmitAnswerParams {
  return decodeWire<SubmitAnswerParams>(v, "SubmitAnswerParams", (o) => {
    o.conversationId = reqStr(
      o.conversationId,
      "SubmitAnswerParams.conversationId",
    );
    o.peerFingerprint = optStr(
      o.peerFingerprint,
      "SubmitAnswerParams.peerFingerprint",
    );
    o.requestId = reqStr(o.requestId, "SubmitAnswerParams.requestId");
    o.questions = optArr(o.questions, "SubmitAnswerParams.questions");
    o.answers = optArr(o.answers, "SubmitAnswerParams.answers");
    o.skipped = optBool(o.skipped, "SubmitAnswerParams.skipped");
  });
}

export function encodeSubmitAnswerParams(v: SubmitAnswerParams): string {
  return encodeWire(v);
}

/** SubmitToolPermissionParams 等同 agentruntime.ToolPermissionSink.SubmitToolPermission 的入参。 */
export interface SubmitToolPermissionParams extends WireObject {
  conversationId: string;
  peerFingerprint?: string;
  requestId: string;
  allow: boolean;
  alwaysAllowSession?: boolean;
  denyReason?: string;
}

export function decodeSubmitToolPermissionParams(
  v: unknown,
): SubmitToolPermissionParams {
  return decodeWire<SubmitToolPermissionParams>(
    v,
    "SubmitToolPermissionParams",
    (o) => {
      o.conversationId = reqStr(
        o.conversationId,
        "SubmitToolPermissionParams.conversationId",
      );
      o.peerFingerprint = optStr(
        o.peerFingerprint,
        "SubmitToolPermissionParams.peerFingerprint",
      );
      o.requestId = reqStr(o.requestId, "SubmitToolPermissionParams.requestId");
      o.allow = reqBool(o.allow, "SubmitToolPermissionParams.allow");
      o.alwaysAllowSession = optBool(
        o.alwaysAllowSession,
        "SubmitToolPermissionParams.alwaysAllowSession",
      );
      o.denyReason = optStr(
        o.denyReason,
        "SubmitToolPermissionParams.denyReason",
      );
    },
  );
}

export function encodeSubmitToolPermissionParams(
  v: SubmitToolPermissionParams,
): string {
  return encodeWire(v);
}

/**
 * SessionSummary 是会话清单里的一条:标识 + 生命周期状态 + 是否正在等待输入 + 最新 seq。
 *
 * LatestSeq 取自 daemon 通知日志里该会话的 MAX(seq)(唯一真相源),客户端拿它与自己
 * 存的游标一比就知道断连期间落下了多少条。
 */
export interface SessionSummary extends WireObject {
  conversationId: string;
  peerFingerprint?: string;
  agentId?: number;

  /**
   * Title / AgentSyncID / ProviderSessionID 是 R7 + 决策 8 的新列:会话标题、所属
   * Agent 的账号级同步标识、以及续话要用的 provider 原生会话身份。三者每轮由调用
   * 方携带、幂等覆盖,所以还没跑过第一轮的会话这几格就是空的(标题由首条消息派生)。
   * 缺这些字段时如实留空(空串,不猜、不填占位名)。
   */
  title?: string;
  agentSyncId?: string;
  providerSessionId?: string;
  cwd?: string;

  /**
   * ProjectSyncID 是这条会话所属项目的**账号级同步标识**,由**桌面端**交出。
   *
   * 这一维在两种执行端上不是同一件事:agentred 的会话有一个落库的 cwd,账号那边
   * 拿 (指纹, cwd) 去比它给每台机器配的项目路径就判得出归属;桌面端没有「这条会话
   * 的 cwd」这种东西 —— 工作目录是每轮按项目本机路径现算的 —— 而且它的本机路径
   * 不流动、只存在账号的上报组里,压根不在那份名单中。两头都对不上,于是桌面端的
   * 每一条对话在账号侧都只能落进「随手对话」。真正流动的事实是项目同步标识本身,
   * 所以它自己说出来。
   *
   * 交出的是同步标识而不是本地自增主键:那是账号里跨机通用的那个名字。项目还没
   * 认领同步标识时(未登录期间建的行,R12a 之前)如实留空 —— 拿本地主键凑一个,
   * 账号那边会照它建出一个永远配不上真项目的组。自由会话同样留空。
   */
  projectSyncId?: string;
  backendType?: string;
  lifecycleState: string;
  waitingForInput?: boolean;
  latestSeq: number;

  /**
   * LastMessageAt 是这条会话最后一次活动的时刻(Unix 毫秒),取自 daemon_sessions 的
   * last_message_at —— 每轮起手幂等覆盖时一并推进。会话清单要显示「最后活动时间」
   * (R5),而它的唯一真相源在执行端这台机器上。还没记过活动时间的会话报 0,由
   * 客户端如实表达为「未知」而不是猜一个时刻。
   */
  lastMessageAt?: number;

  /**
   * ProviderKey / ModelKey 是这条会话**自己**钉的 LLM ModelTarget,与桌面端
   * chat_sessions.provider_key / model_key 逐字同义(chat_entity/session.go):
   *   - 两者皆空 = 跟随 Agent 绑定(inherit-agent),每轮从 agent 的后端绑定解析;
   *   - ProviderKey 非空 + ModelKey 空 = 该供应商当前的默认模型;
   *   - 两者都非空 = 固定模型。
   *
   * 空**是一个有含义的取值**:它表示这条对话跟随 Agent 绑定。
   */
  providerKey?: string;
  modelKey?: string;

  /**
   * ReasoningEffort 是这条会话在执行端记下的思考力度(六档词表,空 = 跟随后端配置)。
   * 与上面两格一样**只供显示**:执行路径的力度取自 RunParams.ReasoningEffort,不读
   * 这一列。老执行端不发这个字段,解出来是空串。
   */
  reasoningEffort?: string;
}

export function decodeSessionSummary(v: unknown): SessionSummary {
  return decodeWire<SessionSummary>(v, "SessionSummary", (o) => {
    o.conversationId = reqStr(
      o.conversationId,
      "SessionSummary.conversationId",
    );
    o.peerFingerprint = optStr(
      o.peerFingerprint,
      "SessionSummary.peerFingerprint",
    );
    o.agentId = optNum(o.agentId, "SessionSummary.agentId");
    o.title = optStr(o.title, "SessionSummary.title");
    o.agentSyncId = optStr(o.agentSyncId, "SessionSummary.agentSyncId");
    o.providerSessionId = optStr(
      o.providerSessionId,
      "SessionSummary.providerSessionId",
    );
    o.cwd = optStr(o.cwd, "SessionSummary.cwd");
    o.projectSyncId = optStr(o.projectSyncId, "SessionSummary.projectSyncId");
    o.backendType = optStr(o.backendType, "SessionSummary.backendType");
    o.lifecycleState = reqStr(
      o.lifecycleState,
      "SessionSummary.lifecycleState",
    );
    o.waitingForInput = optBool(
      o.waitingForInput,
      "SessionSummary.waitingForInput",
    );
    o.latestSeq = reqNum(o.latestSeq, "SessionSummary.latestSeq");
    o.lastMessageAt = optNum(o.lastMessageAt, "SessionSummary.lastMessageAt");
    o.providerKey = optStr(o.providerKey, "SessionSummary.providerKey");
    o.modelKey = optStr(o.modelKey, "SessionSummary.modelKey");
    o.reasoningEffort = optStr(
      o.reasoningEffort,
      "SessionSummary.reasoningEffort",
    );
  });
}

export function encodeSessionSummary(v: SessionSummary): string {
  return encodeWire(v);
}

/**
 * SessionListResult 是 MethodSessionList 的应答:这台 daemon 上的会话。调用方自己的
 * 对端永远在范围内;daemon 已认领账号时 ListAll 会把全部对端的会话一并列出(账号可见
 * 性,见 handlers/session_catchup.go 的 List),范围不再只有「调用这条连接的对端」。
 */
export interface SessionListResult extends WireObject {
  sessions: SessionSummary[];
}

export function decodeSessionListResult(v: unknown): SessionListResult {
  return decodeWire<SessionListResult>(v, "SessionListResult", (o) => {
    o.sessions = reqArrOf(
      o.sessions,
      "SessionListResult.sessions",
      decodeSessionSummary,
    );
  });
}

export function encodeSessionListResult(v: SessionListResult): string {
  return encodeWire(v);
}

/**
 * SessionPullParams 是 MethodSessionPull 的请求:给定会话与起始游标,取其后的通知。
 * Cursor 是**已经收到的**最后一个 seq(独占),所以首次补齐传 0。
 */
export interface SessionPullParams extends WireObject {
  conversationId: string;
  peerFingerprint?: string;
  cursor: number;
  limit?: number;
}

export function decodeSessionPullParams(v: unknown): SessionPullParams {
  return decodeWire<SessionPullParams>(v, "SessionPullParams", (o) => {
    o.conversationId = reqStr(
      o.conversationId,
      "SessionPullParams.conversationId",
    );
    o.peerFingerprint = optStr(
      o.peerFingerprint,
      "SessionPullParams.peerFingerprint",
    );
    o.cursor = reqNum(o.cursor, "SessionPullParams.cursor");
    o.limit = optNum(o.limit, "SessionPullParams.limit");
  });
}

export function encodeSessionPullParams(v: SessionPullParams): string {
  return encodeWire(v);
}

/**
 * JournaledNotification 是日志里的一行:那条本该发出的通知的原样 (method, params)。
 *
 * Params **不含 seq** —— 落库时 seq 还没盖上去,它是日志行自己的列。补齐的客户端必须
 * 按 method 把 Params 解成对应的帧、把这里的 Seq 盖上去、再喂进与实时同一套 handler,
 * 否则每一帧都解出 seq=0,会被「不大于游标就丢弃」的规则整段吞掉(R6)。
 * Params 装的是**帧本身**(EventFrame / RunResultDoneFrame /
 * AutonomousTurnStartedFrame 之一),不是它的 JSON 字节。这一页补齐从头到尾在
 * Protobuf 与密封值之间走,中间摆一个 json.RawMessage 只会让每一行在服务端与
 * 客户端各自多走一轮 marshal→unmarshal —— 而日志行本身早就是 Protobuf 字节
 * (见 protowire.EncodeNotification),那次 JSON 连存储格式都不是。
 *
 * json tag 在这里不驱动序列化(下面的 MarshalJSON / UnmarshalJSON 才是),但必须
 * 与 journaledNotificationWire 一字不差:TS 编解码生成器读的是 tag,读不到自定义
 * marshaler。TestJournaledNotificationWireTagsMatchMarshaler 守住这一致性。
 */
export interface JournaledNotification extends WireObject {
  seq: number;
  method: string;
  params: unknown;

  /**
   * Createtime 是这一帧在**原点**发生的时刻(Unix 毫秒),取自日志行自己的列。
   * 0 = 那一端还没升级到会报它,读者据此退回自己的收帧时刻,而不是把 0 当 1970。
   *
   * **没有 omitempty**:这一族结构的 json tag 就是 TS 编解码生成器读的那份契约
   * (TestJournaledNotificationWireTagsMatchMarshaler 守着「tag 列表 == 实际发出的
   * 键」)。省掉零值会让「报了 0」与「这一版根本没有这个字段」在线上长得一模一样。
   */
  createtime: number;
}

export function decodeJournaledNotification(v: unknown): JournaledNotification {
  return decodeWire<JournaledNotification>(v, "JournaledNotification", (o) => {
    o.seq = reqNum(o.seq, "JournaledNotification.seq");
    o.method = reqStr(o.method, "JournaledNotification.method");
    o.createtime = reqNum(o.createtime, "JournaledNotification.createtime");
  });
}

export function encodeJournaledNotification(v: JournaledNotification): string {
  return encodeWire(v);
}

/**
 * SessionPullResult 是一页补齐:按 seq 升序的通知、翻页用的新游标、以及是否还有更多。
 * Cursor 在空页上**保持不变**(不回退到 0),否则客户端会把整段日志重放一遍。
 *
 * OldestSeq 是该会话此刻**现存最老的那一行**的 seq(一条日志都没有时为 0)。它存在的
 * 唯一理由是日志的老前缀可能不在了 —— agentred 自己已经不回收(规格 2026-08-18
 * 决策 8),但库可能被从外部恢复或截断:
 * 客户端的游标会落在那段已经不存在的区间里,补洞拉取因此
 * 永远拉不到 游标+1 那一条 —— 每一页的第一条都被判成跳号丢弃,游标原地不动,此后连
 * 实时通知也全被当成跳号,会话没有错误、没有跳号地冻住(与 8496c291 修的越界冻结同类)。
 * 客户端据它把游标复位到 OldestSeq-1(那截尾巴是真的没有了),照 dropCursorAboveHighWater
 * 的样子留一条 Warn,然后从现存最老的一行接着补。
 */
export interface SessionPullResult extends WireObject {
  notifications?: JournaledNotification[];
  cursor: number;
  hasMore: boolean;
  oldestSeq?: number;
}

export function decodeSessionPullResult(v: unknown): SessionPullResult {
  return decodeWire<SessionPullResult>(v, "SessionPullResult", (o) => {
    o.notifications = optArrOf(
      o.notifications,
      "SessionPullResult.notifications",
      decodeJournaledNotification,
    );
    o.cursor = reqNum(o.cursor, "SessionPullResult.cursor");
    o.hasMore = reqBool(o.hasMore, "SessionPullResult.hasMore");
    o.oldestSeq = optNum(o.oldestSeq, "SessionPullResult.oldestSeq");
  });
}

export function encodeSessionPullResult(v: SessionPullResult): string {
  return encodeWire(v);
}

/** SessionPendingWaitersParams 是 MethodSessionPendingWaiters 的请求。 */
export interface SessionPendingWaitersParams extends WireObject {
  conversationId: string;
  peerFingerprint?: string;
}

export function decodeSessionPendingWaitersParams(
  v: unknown,
): SessionPendingWaitersParams {
  return decodeWire<SessionPendingWaitersParams>(
    v,
    "SessionPendingWaitersParams",
    (o) => {
      o.conversationId = reqStr(
        o.conversationId,
        "SessionPendingWaitersParams.conversationId",
      );
      o.peerFingerprint = optStr(
        o.peerFingerprint,
        "SessionPendingWaitersParams.peerFingerprint",
      );
    },
  );
}

export function encodeSessionPendingWaitersParams(
  v: SessionPendingWaitersParams,
): string {
  return encodeWire(v);
}

/**
 * SessionPendingWaitersResult 是某会话此刻仍在阻塞的全部待决策,载荷足以重建审批 /
 * 提问卡片。两个列表都可能为空:未实现审批协议的 backend、以及不属于调用方的会话,
 * 都回空列表而不是报错(R7)。
 */
export interface SessionPendingWaitersResult extends WireObject {
  toolPermissions?: unknown[];
  askUserQuestions?: unknown[];
}

export function decodeSessionPendingWaitersResult(
  v: unknown,
): SessionPendingWaitersResult {
  return decodeWire<SessionPendingWaitersResult>(
    v,
    "SessionPendingWaitersResult",
    (o) => {
      o.toolPermissions = optArr(
        o.toolPermissions,
        "SessionPendingWaitersResult.toolPermissions",
      );
      o.askUserQuestions = optArr(
        o.askUserQuestions,
        "SessionPendingWaitersResult.askUserQuestions",
      );
    },
  );
}

export function encodeSessionPendingWaitersResult(
  v: SessionPendingWaitersResult,
): string {
  return encodeWire(v);
}

/** SessionAttachParams 是 MethodSessionAttach 的请求。 */
export interface SessionAttachParams extends WireObject {
  conversationId: string;
  peerFingerprint?: string;
}

export function decodeSessionAttachParams(v: unknown): SessionAttachParams {
  return decodeWire<SessionAttachParams>(v, "SessionAttachParams", (o) => {
    o.conversationId = reqStr(
      o.conversationId,
      "SessionAttachParams.conversationId",
    );
    o.peerFingerprint = optStr(
      o.peerFingerprint,
      "SessionAttachParams.peerFingerprint",
    );
  });
}

export function encodeSessionAttachParams(v: SessionAttachParams): string {
  return encodeWire(v);
}

/**
 * SessionAttachResult 交回客户端接着补齐需要的东西:会话此刻的生命周期状态、backend
 * 类型,以及此刻的最新 seq(高水位)。
 *
 * 接管成功后该会话的实时通知就推给这条连接;客户端随后按自己的游标 pull 到拉平即可,
 * 接管与读高水位之间落库的那几条会在同一轮 pull 里被带出来。
 */
export interface SessionAttachResult extends WireObject {
  conversationId: string;
  backendType?: string;
  lifecycleState: string;
  latestSeq: number;
}

export function decodeSessionAttachResult(v: unknown): SessionAttachResult {
  return decodeWire<SessionAttachResult>(v, "SessionAttachResult", (o) => {
    o.conversationId = reqStr(
      o.conversationId,
      "SessionAttachResult.conversationId",
    );
    o.backendType = optStr(o.backendType, "SessionAttachResult.backendType");
    o.lifecycleState = reqStr(
      o.lifecycleState,
      "SessionAttachResult.lifecycleState",
    );
    o.latestSeq = reqNum(o.latestSeq, "SessionAttachResult.latestSeq");
  });
}

export function encodeSessionAttachResult(v: SessionAttachResult): string {
  return encodeWire(v);
}

/**
 * SessionDeleteParams 是 MethodSessionDelete 的请求。PeerFingerprint 的语义与补齐族
 * 完全一致:省略 = 调用方自己的对端,点名别人是账号级能力(见 handlers.ResolveSessionPeer)。
 * 这是本 wire 上第一个破坏性方法,越界的代价不再是「读到了不该读的」而是「删掉了
 * 别人的对话」,所以它绝不能自成一套宽松的范围规则。
 */
export interface SessionDeleteParams extends WireObject {
  conversationId: string;
  peerFingerprint?: string;
}

export function decodeSessionDeleteParams(v: unknown): SessionDeleteParams {
  return decodeWire<SessionDeleteParams>(v, "SessionDeleteParams", (o) => {
    o.conversationId = reqStr(
      o.conversationId,
      "SessionDeleteParams.conversationId",
    );
    o.peerFingerprint = optStr(
      o.peerFingerprint,
      "SessionDeleteParams.peerFingerprint",
    );
  });
}

export function encodeSessionDeleteParams(v: SessionDeleteParams): string {
  return encodeWire(v);
}

/**
 * SessionDeleteResult 交回删除的**后置条件**:应答返回时,这一端已经没有这条会话了。
 *
 * 它有意不是「删了几行」:删除必须幂等 —— server 那份先删、执行端离线时留一条待办,
 * 待办会重放,而且上一次可能删到一半(会话行没了、日志还剩着)。已经不在的会话回
 * Deleted=false 会让调用方把它当成没删干净并永远重放下去,回错误更糟。两种端存的
 * 东西也不一样(agentred 是会话行 + 日志,桌面端是 chat_sessions 与它的消息),
 * 只有后置条件才是两边都答得准的同一件事。
 */
export interface SessionDeleteResult extends WireObject {
  deleted: boolean;
}

export function decodeSessionDeleteResult(v: unknown): SessionDeleteResult {
  return decodeWire<SessionDeleteResult>(v, "SessionDeleteResult", (o) => {
    o.deleted = reqBool(o.deleted, "SessionDeleteResult.deleted");
  });
}

export function encodeSessionDeleteResult(v: SessionDeleteResult): string {
  return encodeWire(v);
}

/**
 * SkillAuthorization 是这一档执行目标上的一条技能授权(桌面端 agent_exec_targets
 * 那一行的 skills_json 里的一项,字段名逐字相同,好让调用方原样搬运)。
 */
export interface SkillAuthorization extends WireObject {
  id: string;
  enabled: boolean;
}

export function decodeSkillAuthorization(v: unknown): SkillAuthorization {
  return decodeWire<SkillAuthorization>(v, "SkillAuthorization", (o) => {
    o.id = reqStr(o.id, "SkillAuthorization.id");
    o.enabled = reqBool(o.enabled, "SkillAuthorization.enabled");
  });
}

export function encodeSkillAuthorization(v: SkillAuthorization): string {
  return encodeWire(v);
}

/**
 * SkillCatalogParams 是 MethodSkillsCatalog 的请求。
 *
 * 请求里没有 agentId / execTargetId:执行端上没有组织架构库,那两个号码在它这里
 * 什么都指不到。要答的那一档由**调用方**限定 —— 它连的这台机器 + 它带上来的这份
 * 授权集,合起来就是「一档」。
 */
export interface SkillCatalogParams extends WireObject {
  /** BackendType 决定用哪个发现器、以及推荐包那半边取哪一张表。 */
  backendType: string;

  /**
   * Authorized 是这一档已经授权的包(可为空 = 一个都没授权)。它只用来给目录的每
   * 一行盖上 Enabled,不会被写到任何地方 —— 执行端不持有授权,只是照着标注。
   */
  authorized?: SkillAuthorization[];

  /** CLIPath 一般留空,由执行端自己解析本机 CLI 路径(调用方不知道对面的 claude 在哪)。 */
  cliPath?: string;
}

export function decodeSkillCatalogParams(v: unknown): SkillCatalogParams {
  return decodeWire<SkillCatalogParams>(v, "SkillCatalogParams", (o) => {
    o.backendType = reqStr(o.backendType, "SkillCatalogParams.backendType");
    o.authorized = optArrOf(
      o.authorized,
      "SkillCatalogParams.authorized",
      decodeSkillAuthorization,
    );
    o.cliPath = optStr(o.cliPath, "SkillCatalogParams.cliPath");
  });
}

export function encodeSkillCatalogParams(v: SkillCatalogParams): string {
  return encodeWire(v);
}

/**
 * SkillPackSummary 是目录里的一行 —— 恰好是画一行要读的那几格(桌面端
 * skillPacksToCatalog → CapabilityPicker 的 CatalogItem)。
 *
 * 它刻意**不是** skill_svc.SkillPackDTO 的照搬:source / recommended /
 * effectiveEnabled 都是桌面端内部口径,浏览器一格也没读,搬过来只会变成两份要同步
 * 的真相。
 */
export interface SkillPackSummary extends WireObject {
  id: string;
  name: string;

  /** Description 是包的一句话说明。 */
  description?: string;

  /** Skills 是包内的 skill 名 —— 界面用它给出条数、展开时列出内容。 */
  skills?: string[];

  /**
   * Installed 这台机器上装了没有。没装的行只能看不能授权(要先去装),这是分组
   * 「可安装 / 可启用 / 已继承」的第一根轴。
   */
  installed?: boolean;

  /** Enabled 这一档显式授权了没有(= 请求里 Authorized 带的那份)。 */
  enabled?: boolean;

  /**
   * GloballyEnabled CLI 全局启用态(claude plugin list --json 的 enabled)。三态
   * 「继承全局 / 强制开 / 强制关」里的「继承」指的就是它。
   */
  globallyEnabled?: boolean;
}

export function decodeSkillPackSummary(v: unknown): SkillPackSummary {
  return decodeWire<SkillPackSummary>(v, "SkillPackSummary", (o) => {
    o.id = reqStr(o.id, "SkillPackSummary.id");
    o.name = reqStr(o.name, "SkillPackSummary.name");
    o.description = optStr(o.description, "SkillPackSummary.description");
    o.skills = optArr(o.skills, "SkillPackSummary.skills");
    o.installed = optBool(o.installed, "SkillPackSummary.installed");
    o.enabled = optBool(o.enabled, "SkillPackSummary.enabled");
    o.globallyEnabled = optBool(
      o.globallyEnabled,
      "SkillPackSummary.globallyEnabled",
    );
  });
}

export function encodeSkillPackSummary(v: SkillPackSummary): string {
  return encodeWire(v);
}

/**
 * SkillCatalogResult 是 MethodSkillsCatalog 的应答。
 *
 * Discovery **没有 omitempty**:它必须每次都在字节流里。可选字段缺席时解出零值,
 * 而这里的零值是空串 —— 调用方就得替它猜一个含义,猜错的方向恰恰是最危险的那个
 * (把「问不出来」当成「没有包」)。
 */
export interface SkillCatalogResult extends WireObject {
  packs: SkillPackSummary[];
  discovery: string;
}

export function decodeSkillCatalogResult(v: unknown): SkillCatalogResult {
  return decodeWire<SkillCatalogResult>(v, "SkillCatalogResult", (o) => {
    o.packs = reqArrOf(
      o.packs,
      "SkillCatalogResult.packs",
      decodeSkillPackSummary,
    );
    o.discovery = reqStr(o.discovery, "SkillCatalogResult.discovery");
  });
}

export function encodeSkillCatalogResult(v: SkillCatalogResult): string {
  return encodeWire(v);
}

/** ProjectSetLocalPathParams 指定某个项目在**这台机器上**的本机路径。 */
export interface ProjectSetLocalPathParams extends WireObject {
  projectSyncId: string;
  path: string;
}

export function decodeProjectSetLocalPathParams(
  v: unknown,
): ProjectSetLocalPathParams {
  return decodeWire<ProjectSetLocalPathParams>(
    v,
    "ProjectSetLocalPathParams",
    (o) => {
      o.projectSyncId = reqStr(
        o.projectSyncId,
        "ProjectSetLocalPathParams.projectSyncId",
      );
      o.path = reqStr(o.path, "ProjectSetLocalPathParams.path");
    },
  );
}

export function encodeProjectSetLocalPathParams(
  v: ProjectSetLocalPathParams,
): string {
  return encodeWire(v);
}

/**
 * ProjectClearLocalPathParams 把某个项目在这台机器上打回「本机未配置路径」。
 *
 * **机器上的目录一个字节都不动**,去掉的只是「这个项目在本机落在哪」这条记录。
 */
export interface ProjectClearLocalPathParams extends WireObject {
  projectSyncId: string;
}

export function decodeProjectClearLocalPathParams(
  v: unknown,
): ProjectClearLocalPathParams {
  return decodeWire<ProjectClearLocalPathParams>(
    v,
    "ProjectClearLocalPathParams",
    (o) => {
      o.projectSyncId = reqStr(
        o.projectSyncId,
        "ProjectClearLocalPathParams.projectSyncId",
      );
    },
  );
}

export function encodeProjectClearLocalPathParams(
  v: ProjectClearLocalPathParams,
): string {
  return encodeWire(v);
}

/**
 * ProjectLocalPathResult 是两个写方法共同的应答:生效之后的状态。
 *
 * 带回路径正文是刻意的:上报是 30 秒轮询,浏览器重新去 server 拉只会拿到旧快照。
 * 调用方据此就地更新那一行,不必等下一轮。
 */
export interface ProjectLocalPathResult extends WireObject {
  /** Path 是生效后的本机路径;清除之后为空。 */
  path: string;

  /** Configured 为假即这个项目在这台机器上处于「本机未配置路径」。 */
  configured: boolean;
}

export function decodeProjectLocalPathResult(
  v: unknown,
): ProjectLocalPathResult {
  return decodeWire<ProjectLocalPathResult>(
    v,
    "ProjectLocalPathResult",
    (o) => {
      o.path = reqStr(o.path, "ProjectLocalPathResult.path");
      o.configured = reqBool(o.configured, "ProjectLocalPathResult.configured");
    },
  );
}

export function encodeProjectLocalPathResult(
  v: ProjectLocalPathResult,
): string {
  return encodeWire(v);
}

/**
 * EventFrame wraps a single agentruntime.Event for delivery over NotifyEvent.
 * ConversationID is transport metadata so the receiving end can route by
 * conversation.
 *
 * Event 是**密封事件本身**,不是它的 JSON 字节。这条帧在进程内只被 protowire 读,
 * 而 protowire 要的就是 Event —— 中间摆一个 json.RawMessage 的后果是每帧在两端
 * 各自多走一轮 Event → JSON → Event(生产者 marshal、协议层 unmarshal 再 marshal
 * 成 proto,接收端反过来再来一遍),而这条链路上根本没有第二种载荷形态需要它当
 * 通用容器。
 *
 * 线上形态一个字节都没变:下面的 MarshalJSON / UnmarshalJSON 仍旧落
 * {"conversationId":…,"event":{"kind":…},"seq":…},由各 Event 自己的 MarshalJSON 与
 * agentruntime.UnmarshalEvent 负责 —— 通知日志里的旧行、旧版本对端、黄金样本
 * 都照常读得出来。
 * json tag 在这里**不驱动序列化**(下面的 MarshalJSON / UnmarshalJSON 才是),
 * 但必须与 eventFrameWire 一字不差:TS 编解码生成器读的是 tag,读不到自定义
 * marshaler。两处一旦分家,生成出来的 decodeEventFrame 会去找 `ConversationID` 这样
 * 根本不存在的键 —— 编译期无声,浏览器侧全线解码失败。
 * TestEventFrameWireTagsMatchMarshaler 守住这一致性。
 */
export interface EventFrame extends WireObject {
  conversationId: string;
  event: unknown;
  seq?: number;
}

export function decodeEventFrame(v: unknown): EventFrame {
  return decodeWire<EventFrame>(v, "EventFrame", (o) => {
    o.conversationId = reqStr(o.conversationId, "EventFrame.conversationId");
    o.seq = optNum(o.seq, "EventFrame.seq");
  });
}

export function encodeEventFrame(v: EventFrame): string {
  return encodeWire(v);
}

/**
 * RunResultDoneFrame 在 daemon 端 events channel close 之后发一次,带完整 RunResult。
 * 客户端拿到后填回 *remote.Runtime 持有的 *RunResult 指针,然后才 close 客户端的
 * events channel,匹配 chat_svc 的契约(chat.go:1683-1722 在 channel close 后才读 result)。
 *
 * StopErrMsg / StopErrCode 用来在客户端把 RunResult.StopErr 重新 hydrate 成正确的
 * sentinel(ErrAborted 等)。StopErrCode = 0 表示无 sentinel,StopErrMsg 仅作显示;
 * = -32013 表示 ErrAborted;等等。
 */
export interface RunResultDoneFrame extends WireObject {
  conversationId: string;
  providerSessionId?: string;
  usage?: UsageWire | null;
  userAnchor?: string;
  model?: string;
  contextWindow?: number;
  turnToken?: number;
  stopErrMsg?: string;
  stopErrCode?: number;
  seq?: number;

  /**
   * 本轮的计时,由 daemon 就着它自己扇出的那条事件流量出来(口径与映射见
   * internal/pkg/turnstats)。按帧重建转录的消费方(浏览器控制台 / peer 视图)
   * 没有第二个来源:桌面端本机会话上那三个数是 chat_svc 在 runtime 之上算完落
   * 自己库的,过不了 wire。
   *
   * DurationMs 是墙上时间、**含**工具空档;TokensPerSec 的分母只数生成段。
   */
  durationMs?: number;
  firstTokenMs?: number;
  tokensPerSec?: number;
}

export function decodeRunResultDoneFrame(v: unknown): RunResultDoneFrame {
  return decodeWire<RunResultDoneFrame>(v, "RunResultDoneFrame", (o) => {
    o.conversationId = reqStr(
      o.conversationId,
      "RunResultDoneFrame.conversationId",
    );
    o.providerSessionId = optStr(
      o.providerSessionId,
      "RunResultDoneFrame.providerSessionId",
    );
    o.usage = optOf(o.usage, decodeUsageWire);
    o.userAnchor = optStr(o.userAnchor, "RunResultDoneFrame.userAnchor");
    o.model = optStr(o.model, "RunResultDoneFrame.model");
    o.contextWindow = optNum(
      o.contextWindow,
      "RunResultDoneFrame.contextWindow",
    );
    o.turnToken = optNum(o.turnToken, "RunResultDoneFrame.turnToken");
    o.stopErrMsg = optStr(o.stopErrMsg, "RunResultDoneFrame.stopErrMsg");
    o.stopErrCode = optNum(o.stopErrCode, "RunResultDoneFrame.stopErrCode");
    o.seq = optNum(o.seq, "RunResultDoneFrame.seq");
    o.durationMs = optNum(o.durationMs, "RunResultDoneFrame.durationMs");
    o.firstTokenMs = optNum(o.firstTokenMs, "RunResultDoneFrame.firstTokenMs");
    o.tokensPerSec = optNum(o.tokensPerSec, "RunResultDoneFrame.tokensPerSec");
  });
}

export function encodeRunResultDoneFrame(v: RunResultDoneFrame): string {
  return encodeWire(v);
}

/**
 * AutonomousTurnStartedFrame 在一轮自主续轮开始时由 daemon 发一次。客户端据此
 * 新建一个 agentruntime.AutonomousTurn 推给 AutonomousTurns() 的消费方,并把随后
 * 的 NotifyAutonomousTurnEvent(EventFrame)路由进它的 Events,直到 NotifyAutonomousTurnDone
 * (RunResultDoneFrame)填回该轮 RunResult 并 close。
 */
export interface AutonomousTurnStartedFrame extends WireObject {
  conversationId: string;
  trigger?: string;
  turnToken?: number;
  seq?: number;
}

export function decodeAutonomousTurnStartedFrame(
  v: unknown,
): AutonomousTurnStartedFrame {
  return decodeWire<AutonomousTurnStartedFrame>(
    v,
    "AutonomousTurnStartedFrame",
    (o) => {
      o.conversationId = reqStr(
        o.conversationId,
        "AutonomousTurnStartedFrame.conversationId",
      );
      o.trigger = optStr(o.trigger, "AutonomousTurnStartedFrame.trigger");
      o.turnToken = optNum(o.turnToken, "AutonomousTurnStartedFrame.turnToken");
      o.seq = optNum(o.seq, "AutonomousTurnStartedFrame.seq");
    },
  );
}

export function encodeAutonomousTurnStartedFrame(
  v: AutonomousTurnStartedFrame,
): string {
  return encodeWire(v);
}

/**
 * UsageWire mirrors provider.Usage with stable lowerCamelCase tags. provider.Usage
 * has no JSON tags so we wrap it for wire stability(同 event_wire.go 里同名 helper)。
 */
export interface UsageWire extends WireObject {
  promptTokens: number;
  completionTokens: number;
  reasoningTokens: number;
  cachedTokens: number;
  cacheCreationTokens: number;
  totalTokens: number;
}

export function decodeUsageWire(v: unknown): UsageWire {
  return decodeWire<UsageWire>(v, "UsageWire", (o) => {
    o.promptTokens = reqNum(o.promptTokens, "UsageWire.promptTokens");
    o.completionTokens = reqNum(
      o.completionTokens,
      "UsageWire.completionTokens",
    );
    o.reasoningTokens = reqNum(o.reasoningTokens, "UsageWire.reasoningTokens");
    o.cachedTokens = reqNum(o.cachedTokens, "UsageWire.cachedTokens");
    o.cacheCreationTokens = reqNum(
      o.cacheCreationTokens,
      "UsageWire.cacheCreationTokens",
    );
    o.totalTokens = reqNum(o.totalTokens, "UsageWire.totalTokens");
  });
}

export function encodeUsageWire(v: UsageWire): string {
  return encodeWire(v);
}
