/**
 * `reduceFrames`：wire 事件帧 → 本包的 `TranscriptMessage[]`。
 *
 * 这是两个宿主与桌面聊天页之间**唯一**的形态差：桌面端自己的会话由 Go 侧
 * `chat_svc` 落库时把块算好、经 Wails 零转换喂进包；而拿得到的只是 wire 事件流的
 * 那些面（agentre-server 的 relay、桌面端的 Peer Tab）要在浏览器里补上同一次投影。
 *
 * 名字不是新造的 —— `dto.ts` 开头就写着这件事该由 `reduceFrames()` 做。
 *
 * ## 归属：为什么这份实现在包里
 *
 * 同一套归约此前两个宿主各写了一份，而且**更完整的那份在消费方仓库**：
 * agentre-server 的 `lib/transcriptFrames.ts` 归约到本包的 DTO（canonical 工具卡 /
 * plan / exec-approval / compact 边界一应俱全），桌面端 Peer Tab 的
 * `peer-transcript.ts` 只有五种块、其余一律落自造的 `raw` —— 而本包的行模型根本没有
 * `raw` 这一档，那些块最终渲染成一行 `(debug) unimplemented block type: raw`，
 * 载荷连看都看不到。所以合一的方向是反向搬运：实现进包，两个宿主此后只保留各自的
 * **帧来源**（server 的 relay socket、桌面端的 `peer_svc` Wails 事件）。
 *
 * ## 每个 kind 的归宿
 *
 * 照着桌面端 `chat_svc/dispatcher_wiring.go` 那张表钉 —— 那边每个 kind 都注册了
 * handler，**一个都没丢，也一个都没当未知事件印 JSON**：
 *
 *   正文块        text_delta / thinking_delta / user_message /
 *                 tool_use_start / tool_result / compact_boundary / plan_updated
 *   交互卡        ask_user_question / tool_permission_request /
 *                 exec_approval_requested（带 canonical，包的卡才渲染得出来）
 *   **回填原卡**  ask_user_question_answered / tool_permission_resolved /
 *                 exec_approval_resolved —— 决议是那张卡的终态，不是新的一条
 *   消息级        usage（token 列）/ error（errorText）/ done（结束本条）
 *   不进正文      context_window_updated / runtime_status /
 *                 permission_mode_changed（都是会话级）、steer_consumed（轮次
 *                 切分）、tool_use_end（新 API 无对应事件）、retry（桌面端明写
 *                 「只 emit 不落 block」）
 *   真·未知       词表外的字符串 → notice 块 + 原样 raw（R8）
 *
 * 「不进正文」这一档是**记而不显**：桌面端把它们显示在 Composer（上下文进度条、
 * 「正在压缩上下文…」chip），拿 wire 事件的那两个面各自决定要不要承载
 * （`reduceSessionState` 就是给底栏用的那一份）。这是已知缺口，不是丢弃 ——
 * 缺的是显示面，不是数据。
 */
import type {
  TranscriptBlock,
  TranscriptBlockSubagent,
  TranscriptMessage,
} from "./dto";
import type { CanonicalDTO } from "./canonical-tool/types";
import {
  EventAskUserQuestion,
  EventAskUserQuestionAnswered,
  EventCompactBoundary,
  EventContextWindowUpdated,
  EventDone,
  EventError,
  EventExecApprovalRequested,
  EventExecApprovalResolved,
  EventPermissionModeChanged,
  EventPlanUpdated,
  EventOutputActivity,
  EventRetry,
  EventRuntimeStatus,
  EventSteerConsumed,
  EventSubagentDone,
  EventSubagentModel,
  EventSubagentProgress,
  EventSubagentStarted,
  EventTextDelta,
  EventThinkingDelta,
  EventToolPermissionRequest,
  EventToolPermissionResolved,
  EventToolResult,
  EventToolUseEnd,
  EventToolUseStart,
  EventUsage,
  EventUnrecognizedBlock,
  EventUserMessage,
  type EventKind,
} from "../event-kinds.gen";

/** 一帧 relay 事件。`event` 在 Go 侧是 json.RawMessage，对 wire 完全不透明。 */
export interface TranscriptFrame {
  sessionId: number;
  event?: unknown;
  seq?: number;
}

// ── 载荷读取：全部是「读不到就当没有」，坏帧不许把整段转录带崩 ──────────────

function obj(v: unknown): Record<string, unknown> | undefined {
  return typeof v === "object" && v !== null
    ? (v as Record<string, unknown>)
    : undefined;
}

/**
 * 事件载荷里的判别值。这里只能**断言**成 EventKind 而不是校验：运行期照样可能
 * 来一个词表外的字符串（比本仓新的 daemon、坏帧）。兜住它的是 switch 的 default。
 */
function kindOf(ev: unknown): EventKind | undefined {
  const k = obj(ev)?.kind;
  return typeof k === "string" ? (k as EventKind) : undefined;
}

function str(ev: unknown, key: string): string | undefined {
  const v = obj(ev)?.[key];
  return typeof v === "string" ? v : undefined;
}

function bool(ev: unknown, key: string): boolean | undefined {
  const v = obj(ev)?.[key];
  return typeof v === "boolean" ? v : undefined;
}

function num(ev: unknown, key: string): number | undefined {
  const v = obj(ev)?.[key];
  return typeof v === "number" ? v : undefined;
}

function record(ev: unknown, key: string): Record<string, unknown> {
  return obj(obj(ev)?.[key]) ?? {};
}

/** 未知载荷的可读形态。序列化失败（循环引用等）时退回 String，不抛。 */
function pretty(v: unknown): string {
  try {
    return JSON.stringify(v, null, 2) ?? String(v);
  } catch {
    return String(v);
  }
}

/**
 * 一条空消息。用量 / 计时 / 时间戳的零值不是占位，是包里有意义的输入：
 * `durationMs > 0` 才渲染 meta footer、`createtime` 为 0 时时间戳渲染成空串。
 * 填零 = 不显示，而不是显示成 0。
 *
 * 这一层一个数都不编。用量与计时由链路自己送过来：usage 帧逐跳喂 token 列，
 * 终态帧（runtime.runResultDone）带这一轮的模型与计时 —— 后者由 agentred 就着
 * 它扇出的同一条事件流量出来，口径与桌面端 chat_svc 共用 internal/pkg/turnstats。
 * 送不到的（createtime 就是一例）仍旧留零。
 */
function emptyMessage(
  id: number,
  role: string,
  sessionId: number,
): TranscriptMessage {
  return {
    id,
    sessionId,
    role,
    blocks: [],
    model: "",
    promptTokens: 0,
    completionTokens: 0,
    cachedTokens: 0,
    cacheCreationTokens: 0,
    reasoningTokens: 0,
    totalInputTokens: 0,
    durationMs: 0,
    errorText: "",
    seq: id,
    createtime: 0,
  };
}

/**
 * 归约状态。刻意是一个可变的局部对象而不是纯函数折叠：整段流每来一帧就重算一次
 * （见文件末尾对 id 稳定性的说明），一次 O(n) 的可变累积比 n 次数组拷贝便宜得多，
 * 而对外仍是纯函数 —— 入参不被碰，出参每次都是新对象。
 */
interface State {
  messages: TranscriptMessage[];
  /** 当前还能继续吸收块的那条助手消息。user 消息与 done 之后置空。 */
  open: TranscriptMessage | null;
  /**
   * 这一轮的那条助手消息 —— 终态帧带来的 meta（模型 / 计时 / 用量）盖在它身上。
   *
   * 与 `open` 分开，因为两者的寿命不同：`error` 会把 `open` 收掉（错误卡挂在末行，
   * 继续追加块会让它漂到后来的正文之后），而终态帧紧随其后 —— 只认 `open` 的话，
   * 报错的那一轮就永远没有模型与耗时，而那恰恰是最需要看这两个数的时候。
   * 用户消息开启新一轮时置空：没有助手消息的一轮不该把 meta 倒挂到上一轮头上。
   */
  turn: TranscriptMessage | null;
  nextId: number;
  /**
   * 本批帧改动过的消息。增量投影据此只给它们换新身份，其余保持引用不变
   * （见 createTranscriptProjector）。整段重算的 reduceFrames 用不到它，但它跟着
   * State 走比额外开一条路径便宜。
   */
  touched: Set<TranscriptMessage>;
}

function newState(): State {
  return {
    messages: [],
    open: null,
    turn: null,
    nextId: 1,
    touched: new Set(),
  };
}

function openAssistant(st: State, sessionId: number): TranscriptMessage {
  // 拿到它就是要改它：消息级字段（usage / model / errorText）与块都从这里进。
  if (st.open) {
    st.touched.add(st.open);
    return st.open;
  }
  const msg = emptyMessage(st.nextId++, "assistant", sessionId);
  st.messages.push(msg);
  st.open = msg;
  st.turn = msg;
  st.touched.add(msg);
  return msg;
}

/**
 * 把一份 usage 落到消息的 token 列上。
 *
 * completion / reasoning 是**累加**的：一轮里每个内部 API call 都发一条 usage 帧，
 * 而这两项是那一跳自己的产出，覆盖等于只留最后一跳。桌面端 usageWriterAdapter
 * 用的正是 `+=`，此前这里两个都是覆盖，多跳的一轮在控制台上只报得出最后一跳的
 * 接收量。prompt / cached / cacheCreation 相反：它们是「这一跳送进去多少」，取最新。
 *
 * `final` 是终态帧那一次 —— 它带的 usage 是**最后一跳**的值，不是合计，所以此时
 * 累加的两项一个都不碰（turn_run.go 那段注释是同一条规矩）。它只补齐从来不发
 * usage 帧的那些后端：那种情况下累加值是 0，正好由它兜底。
 */
function applyUsage(
  msg: TranscriptMessage,
  usage: Record<string, unknown>,
  opts?: { final?: boolean },
): void {
  const take = (key: string, into: keyof TranscriptMessage) => {
    const v = usage[key];
    if (typeof v === "number") (msg[into] as number) = v;
  };
  const add = (key: string, into: keyof TranscriptMessage) => {
    const v = usage[key];
    if (typeof v !== "number") return;
    if (opts?.final) {
      if ((msg[into] as number) === 0) (msg[into] as number) = v;
      return;
    }
    (msg[into] as number) += v;
  };
  take("promptTokens", "promptTokens");
  take("cachedTokens", "cachedTokens");
  take("cacheCreationTokens", "cacheCreationTokens");
  add("completionTokens", "completionTokens");
  add("reasoningTokens", "reasoningTokens");
}

/**
 * 找到某个 requestId 对应的那张卡。**从后往前**找：同一个 requestId 理论上只会
 * 出现一次，但真出现重复时，回填最近的那张才是用户正在看的那张。
 */
function findBlock(
  st: State,
  match: (b: TranscriptBlock) => boolean,
): TranscriptBlock | undefined {
  for (let i = st.messages.length - 1; i >= 0; i--) {
    const blocks = st.messages[i].blocks;
    for (let j = blocks.length - 1; j >= 0; j--) {
      if (match(blocks[j])) {
        // 回填改的是**先前**某条消息里的块，那条消息因此也变了——不标记的话
        // 下游的行缓存会继续交出回填之前的那一份。
        st.touched.add(st.messages[i]);
        return blocks[j];
      }
    }
  }
  return undefined;
}

/** 追加正文/思考增量：同类型且紧邻就并进去，否则另起一个块。 */
function appendText(msg: TranscriptMessage, type: string, text: string): void {
  const last = msg.blocks[msg.blocks.length - 1];
  if (last && last.type === type) {
    last.text = `${last.text ?? ""}${text}`;
    return;
  }
  msg.blocks.push({ type, text });
}

/**
 * 内层工具块的归属：父 tool_use 与 run id。两个字段都是「有才带」——
 * protobuf 的空串读作「不是内层」，写成空串会让 buildRenderItems 把顶层工具也
 * 当成子代理内层块摘走（它只判 `b.parentToolUseId` 真不真）。
 */
function attachNesting(block: TranscriptBlock, ev: unknown): void {
  const parent = str(ev, "parentToolCallId");
  if (parent) block.parentToolUseId = parent;
  const runId = str(ev, "subagentRunId");
  if (runId) block.subagentRunId = runId;
}

/**
 * kind → `CanonicalDTO` 上承载它的那个属性名。与 Go 侧
 * `view.CanonicalDTO` 的 json 标签一一对应（chat_svc/view/chat_block.go）——
 * 桌面端喂进来的就是那个结构体，两边必须同名，否则同一张卡一侧读得到一侧读不到。
 */
const CANONICAL_SLOTS: Readonly<Record<string, string>> = {
  "file.write": "fileWrite",
  "file.edit": "fileEdit",
  "user.ask": "userAsk",
  "plan.update": "planUpdate",
  "plan.approve_request": "planApprove",
  "agent.spawn": "agentSpawn",
  "tool.permission": "toolPermission",
};

/**
 * wire 上的 canonical 是**扁平**的 `{kind, ...字段}`（Go 侧 canonical.MarshalTool
 * 把 Kind 与内嵌结构一起 marshal），而本包的 `CanonicalDTO` 是**嵌套**的
 * `{kind, fileEdit:{…}}`（卡片读的是后者）。这里做那一次转换。
 *
 * 除掉 kind 之后不需要逐字段搬：`view.CanonicalDTO` 的内层就是扁平化之前的那个
 * 结构体本身，字段名逐字相同。
 *
 * 词表外的 kind（比本仓新的对端）**不挂 canonical**，退回裸工具卡 —— 入参照样
 * 在，如实呈现（R8）。编一个空壳出来只会让 CanonicalToolRouter 画一张读不出内容
 * 的卡，比普通工具卡更糟。
 */
function canonicalFromWire(ev: unknown): CanonicalDTO | undefined {
  const flat = obj(obj(ev)?.canonical);
  const kind = typeof flat?.kind === "string" ? flat.kind : "";
  const slot = CANONICAL_SLOTS[kind];
  if (!flat || !slot) return undefined;
  const { kind: _kind, ...rest } = flat;
  return { kind, [slot]: rest } as CanonicalDTO;
}

/**
 * 找到该由谁承载这条 subagent 帧的 overlay。
 *
 * **先按 task_id 找**：CLI 恢复一个子代理（SendMessage）时沿用同一个 task_id、
 * 换一个 tool_use_id 重发整套帧，只按 tool call 找会给恢复段另起一张卡 —— 而承载它
 * 的那次调用（SendMessage）不是 agent.spawn 工具名，那张卡永远没人渲染。Go 侧
 * handlers/subagent.go 的 adoptResumedSubagent 是同一条规矩。
 *
 * 只认已经有 overlay 的块：前台 bash 在 started 时就被跳过了，后续 progress/done
 * 不该反过来给它补一个（那会污染后台任务面板）。
 */
function findSubagentHost(
  st: State,
  toolCallId: string,
  taskId: string,
): TranscriptBlock | undefined {
  if (taskId) {
    const byTask = findBlock(
      st,
      (b) => b.subagent?.taskId === taskId && b.type === "tool_use",
    );
    if (byTask) return byTask;
  }
  return findBlock(
    st,
    (b) => b.toolUseId === toolCallId && b.type === "tool_use" && !!b.subagent,
  );
}

/**
 * 这次派遣是不是后台的。两种工具的默认相反（判据与 Go 侧 isBackgroundSubagent、
 * 前端 background-tasks/derive.ts 同源，读的都是发起它的 tool_use 入参）：
 *   - local_agent（Agent）：默认后台，只有显式 run_in_background===false 才是前台；
 *   - local_bash（Bash）：默认前台，只有显式 run_in_background===true 才是后台。
 * 其它 kind（含空 kind 的旧帧）一律按前台处理。
 */
function isBackgroundSubagent(block: TranscriptBlock): boolean {
  const explicit = block.toolInput?.run_in_background;
  const bg = typeof explicit === "boolean" ? explicit : undefined;
  switch (block.subagent?.kind) {
    case "local_agent":
      return bg !== false;
    case "local_bash":
      return bg === true;
    default:
      return false;
  }
}

/**
 * 一轮正常收尾时，把仍挂在 waiting/running 的**前台**派遣翻成 canceled。
 *
 * 只翻前台：后台任务（默认后台的 Agent / run_in_background 的 Bash）本就有权活过
 * 发起它的那一轮，跟着翻会让卡片显示「已停止」而任务其实还在跑（桌面端 sess-3275
 * 就是这个）。不翻的话另一头也不行：等不到 done 的前台卡会永远转下去。
 */
function cancelRunningForegroundSubagents(st: State): void {
  for (const msg of st.messages) {
    for (const b of msg.blocks) {
      const sa = b.subagent;
      if (!sa || (sa.status !== "running" && sa.status !== "waiting")) continue;
      if (isBackgroundSubagent(b)) continue;
      sa.status = "canceled";
      st.touched.add(msg);
    }
  }
}

/** 数字/文本累计态的零值守卫：0 与空串读作「这一帧没上报」，不覆盖已记录值。 */
function mergeSubagentCounters(
  into: TranscriptBlockSubagent,
  info: Record<string, unknown>,
): void {
  // 刻意不叫 `t` —— 包里 `t("…")` 是 i18n 取文案的写法,i18n.test.tsx 的静态扫描
  // 会把这里的局部取值当成翻译键,报「缺 zh-CN / 缺 en」。
  const numOf = (key: string) =>
    typeof info[key] === "number" ? (info[key] as number) : 0;
  const textOf = (key: string) =>
    typeof info[key] === "string" ? (info[key] as string) : "";
  if (numOf("toolUses")) into.toolUses = numOf("toolUses");
  if (numOf("totalTokens")) into.totalTokens = numOf("totalTokens");
  if (numOf("durationMs")) into.durationMs = numOf("durationMs");
  if (textOf("lastToolName")) into.lastToolName = textOf("lastToolName");
  if (textOf("summary")) into.summary = textOf("summary");
  if (textOf("mode")) into.mode = textOf("mode");
  if (!into.taskId && textOf("taskId")) into.taskId = textOf("taskId");
  if (Array.isArray(info.runs) && info.runs.length > 0) {
    into.runs = info.runs as TranscriptBlockSubagent["runs"];
  }
}

// ── 各 kind 的落点 ─────────────────────────────────────────────────────────

function applyFrame(
  st: State,
  frame: TranscriptFrame,
  sessionId: number,
): void {
  const ev = frame.event;
  const kind = kindOf(ev);

  switch (kind) {
    case EventTextDelta: {
      const text = str(ev, "text") ?? "";
      if (text) appendText(openAssistant(st, sessionId), "text", text);
      return;
    }

    case EventThinkingDelta: {
      const text = str(ev, "text") ?? "";
      if (text) appendText(openAssistant(st, sessionId), "thinking", text);
      return;
    }

    case EventUserMessage: {
      // 用户消息自成一条，且**不吸收**后续块 —— 后面的思考/工具属于助手那一轮。
      const msg = emptyMessage(st.nextId++, "user", sessionId);
      msg.blocks.push({ type: "text", text: str(ev, "text") ?? "" });
      // 两个字段都要：`sourceDevice` 是发起端指纹，buildSourceByMessageId
      // 拿它跟本机指纹比对 —— **本机发出的那条不该标来源**（server 宿主此前自己建表，
      // 不做这个比对，于是你自己发的消息也挂着「From Chrome on macOS」）。
      // `sourceDeviceName` 只是它的显示名，没有时回退到指纹本身。
      const device = str(ev, "sourceDevice");
      if (device) msg.sourceDevice = device;
      const deviceName = str(ev, "sourceDeviceName");
      if (deviceName) msg.sourceDeviceName = deviceName;
      st.messages.push(msg);
      st.open = null;
      st.turn = null;
      return;
    }

    case EventToolUseStart: {
      const block: TranscriptBlock = {
        type: "tool_use",
        toolUseId: str(ev, "id") ?? "",
        toolName: str(ev, "name") ?? "",
        toolInput: record(ev, "input"),
      };
      // 内层（子代理里跑的）工具带父调用与 run id。带上它们，buildRenderItems 才会
      // 把这些块从同级正文里摘走、归进父派遣卡的 STEPS —— 不带就是几十张裸工具卡
      // 平铺在正文里，而派遣卡是空的。
      attachNesting(block, ev);
      const c = canonicalFromWire(ev);
      if (c) block.canonical = c;
      openAssistant(st, sessionId).blocks.push(block);
      return;
    }

    case EventToolResult: {
      // 按 toolUseId 配对：一条助手消息可以同时起 Read + Grep，只看「最后一张
      // 未完成的卡」会把 Read 的输出挂到 Grep 上。包的 buildRenderItems 也是按
      // toolUseId 把入参与输出合成同一张卡的。
      const block: TranscriptBlock = {
        type: "tool_result",
        toolUseId: str(ev, "toolCallId") ?? "",
        text: str(ev, "content") ?? "",
        isError: bool(ev, "isError") ?? false,
      };
      attachNesting(block, ev);
      openAssistant(st, sessionId).blocks.push(block);
      return;
    }

    case EventAskUserQuestion: {
      const requestId = str(ev, "requestId") ?? "";
      const questions = Array.isArray(obj(ev)?.questions)
        ? (obj(ev)?.questions as NonNullable<
            TranscriptBlock["askUserQuestion"]
          >["questions"])
        : [];
      openAssistant(st, sessionId).blocks.push({
        type: "ask_user_question",
        askUserQuestion: { requestId, questions },
        // canonical 是本包那些交互卡渲染的前提：没有它 CanonicalToolRouter 回落
        // 到 RawToolCard，端口也永远调不到。
        canonical: { kind: "user.ask", userAsk: { requestId, questions } },
      });
      return;
    }

    case EventAskUserQuestionAnswered: {
      // 回填那一张提问卡。找不到就什么都不做 —— 造一张只有答案没有问题的空卡
      // 比不画更糟（桌面端 Mutate 命中不了同样直接 return）。
      const requestId = str(ev, "requestId");
      const block = findBlock(
        st,
        (b) => b.askUserQuestion?.requestId === requestId,
      );
      if (!block?.askUserQuestion) return;
      const answers = obj(ev)?.answers;
      block.askUserQuestion.answered = true;
      if (Array.isArray(answers)) block.askUserQuestion.answers = answers;
      if (bool(ev, "skipped")) block.askUserQuestion.skipped = true;
      if (block.canonical?.userAsk) {
        block.canonical.userAsk.answered = true;
        if (Array.isArray(answers)) block.canonical.userAsk.answers = answers;
      }
      return;
    }

    case EventToolPermissionRequest: {
      const requestId = str(ev, "requestId") ?? "";
      const toolName = str(ev, "toolName") ?? "";
      const toolInput = record(ev, "input");
      openAssistant(st, sessionId).blocks.push({
        type: "tool_permission_request",
        toolPermission: { requestId, toolName, toolInput },
        canonical: {
          kind: "tool.permission",
          toolPermission: { requestId, toolName, toolInput },
        },
      });
      return;
    }

    case EventToolPermissionResolved: {
      // **这就是用户看到「未知事件 · tool_permission_resolved」的那一条。**
      // 它是自己刚点下的那一下的回执，桌面端 ToolPermissionResolvedHandler 用
      // turn.Mutate 回同一条块把卡切成只读态，不新增任何东西。
      const requestId = str(ev, "requestId");
      const block = findBlock(
        st,
        (b) => b.toolPermission?.requestId === requestId,
      );
      if (!block?.toolPermission) return;
      const allowed = bool(ev, "allowed") ?? false;
      const alwaysAllow = bool(ev, "alwaysAllow");
      block.toolPermission.resolved = true;
      block.toolPermission.allowed = allowed;
      if (alwaysAllow !== undefined) block.toolPermission.alwaysAllow = true;
      if (block.canonical?.toolPermission) {
        block.canonical.toolPermission.resolved = true;
        block.canonical.toolPermission.allowed = allowed;
        if (alwaysAllow !== undefined) {
          block.canonical.toolPermission.alwaysAllow = true;
        }
      }
      return;
    }

    case EventExecApprovalRequested: {
      openAssistant(st, sessionId).blocks.push({
        type: "exec_approval",
        execApproval: {
          id: str(ev, "id") ?? "",
          commandText: str(ev, "commandText") ?? "",
          status: str(ev, "status") ?? "pending",
        },
      });
      return;
    }

    case EventExecApprovalResolved: {
      const id = str(ev, "id");
      const block = findBlock(st, (b) => b.execApproval?.id === id);
      if (!block?.execApproval) return;
      block.execApproval.status = str(ev, "status") ?? "resolved";
      const decision = str(ev, "decision");
      if (decision) block.execApproval.decision = decision;
      return;
    }

    case EventPlanUpdated: {
      openAssistant(st, sessionId).blocks.push({
        type: "plan",
        canonical: {
          kind: "plan.update",
          planUpdate: {
            text: str(ev, "text"),
            steps: Array.isArray(obj(ev)?.steps)
              ? (obj(ev)?.steps as { step: string; status: string }[])
              : undefined,
          },
        },
      });
      return;
    }

    case EventCompactBoundary: {
      // 与下面那一批相反：桌面端 CompactBoundaryHandler 是**落 block** 的，
      // 本包里也就有 CompactBoundaryDivider 在等着它。
      openAssistant(st, sessionId).blocks.push({
        type: "compact_boundary",
        compact: {
          preTokens: num(ev, "preTokens"),
          trigger: str(ev, "trigger"),
          at: num(ev, "at") ?? 0,
        },
      });
      return;
    }

    case EventUsage: {
      // 消息级，不是块。桌面端 UsageUpdateHandler 也是 patch 到 assistantMsg。
      const msg = openAssistant(st, sessionId);
      applyUsage(msg, record(ev, "usage"));
      // totalInputTokens 是 UsageUpdate 自己的字段，不在嵌套的 Usage 里
      // （event_wire.go 的 UsageUpdate.MarshalJSON 把它与 usage 平级写出）。
      // 从 usage 里取等于永远取不到，Composer 的「已用上下文」就一直是 0。
      const totalInput = num(ev, "totalInputTokens");
      if (totalInput !== undefined) msg.totalInputTokens = totalInput;
      const model = str(ev, "model");
      if (model) msg.model = model;
      return;
    }

    case EventError: {
      // 本包在消息级有 errorText（末行渲染 ErrorCard），这就是 error 的对应物。
      // 落位后结束该条：errorText 挂在末行，继续追加块会让错误卡漂到后来的正文之后。
      const msg = openAssistant(st, sessionId);
      msg.errorText = str(ev, "message") ?? "error";
      st.open = null;
      return;
    }

    case EventDone: {
      // 一轮的 meta：模型、本轮计时、以及没有 usage 帧的后端的用量兜底。
      //
      // 这些不是本包编出来的，两个生产者各用自己填得起的载体送过来，落点是这里：
      //   - 桌面端 chat_svc 在 runtime **之上**收口，算完落库时手里就有，直接填在
      //     `done` 事件上（`agentruntime.Done` 的四个字段）；
      //   - agentred 在事件流**之上**量表（口径与 chat_svc 共用
      //     `internal/pkg/turnstats`），知道结果时 `done` 早转发出去了，于是盖在
      //     `runtime.runResultDone` 终态帧上，由宿主随一条合成的 `done` 交进来。
      //
      // 零值一律跳过：它读作「这一端没上报」，不是「这一轮零耗时」。runtime 自己
      // emit 的 `Done` 四格全空，同一段流里它可能排在带数的那条之后 —— 不跳过就会
      // 把已经填好的数抹掉。
      //
      // 落点是 `st.turn` 而不是 `st.open`：见 State.turn 的注释。
      const msg = st.turn;
      if (msg) {
        st.touched.add(msg);
        const model = str(ev, "model");
        if (model) msg.model = model;
        const durationMs = num(ev, "durationMs");
        if (durationMs) msg.durationMs = durationMs;
        const firstTokenMs = num(ev, "firstTokenMs");
        if (firstTokenMs) msg.firstTokenMs = firstTokenMs;
        const tokensPerSec = num(ev, "tokensPerSec");
        if (tokensPerSec) msg.tokensPerSec = tokensPerSec;
        applyUsage(msg, record(ev, "usage"), { final: true });
      }
      cancelRunningForegroundSubagents(st);
      st.open = null;
      return;
    }

    // ── 认得，但归宿不在转录正文里 ─────────────────────────────────────────
    //
    // 这些**不是**「不认识」。桌面端每一条都有 handler，只是落点在会话状态或
    // Composer 上，不在正文。拿 wire 事件的那两个面还没有那层承载面，所以此刻是记而不显 ——
    // 缺的是显示面，不是数据。把它们铺成 JSON 卡是此前那条老路最响的症状。
    case EventContextWindowUpdated: // → 会话的上下文窗口（桌面端 Composer 进度条）
    case EventRuntimeStatus: // → 过渡态，桌面端 handler 明写「不落 block」
    case EventPermissionModeChanged: // → 会话的 permission_mode
    case EventSteerConsumed: // → 轮次切分，桌面端在 runTurn 提前拦截
    case EventToolUseEnd: // → 新 API 里没有对应 Event（event_convert.go:15）
    case EventRetry: // → 桌面端 RetryHandler 明写「只 emit 不落 block」
    case EventOutputActivity: // → 纯计时信号（记首 token），本身没有正文
      // output_activity 随 agentre 那边**另一轮**在途改动进入词表（那一轮的规格是
      // `agentre/docs/specs/2026-08-23-chat-surface-alignment.md`，wire 事件这一侧尚未
      // 落地）。这里按「认得但不落正文」处理 —— 与两个宿主今日的可观察行为一致；
      // 真正的落点（比如首 token 之前的等待呈现）由那一轮决定，不在这里替它定。
      return;

    // subagent 生命周期：累计进外层那张 tool_use 卡的 `subagent`，由
    // AgentSpawnCard 与桌面端读同一份字段。规则照搬 Go 侧
    // chat_svc/handlers/subagent.go：前台 bash 不建 overlay、零值不覆盖、
    // 模型 first-wins、同 task_id 换 tool call 归一到原卡。
    case EventSubagentStarted: {
      const toolCallId = str(ev, "toolCallId") ?? "";
      const info = record(ev, "info");
      const taskId = typeof info.taskId === "string" ? info.taskId : "";
      const kind2 = typeof info.kind === "string" ? info.kind : "";
      const status =
        (typeof info.status === "string" && info.status) || "running";

      // 恢复重开：同一个 task 换了 tool call，认领回原卡，不另起一张。被覆盖的
      // 那一段终态收进 resumes —— 归一不等于把「失败发生过」抹掉。
      const resumed = taskId
        ? findBlock(
            st,
            (b) => b.subagent?.taskId === taskId && b.toolUseId !== toolCallId,
          )
        : undefined;
      if (resumed?.subagent) {
        resumed.subagent.resumes = [
          ...(resumed.subagent.resumes ?? []),
          {
            status: resumed.subagent.status ?? "",
            summary: resumed.subagent.summary,
          },
        ];
        resumed.subagent.status = status;
        resumed.subagent.summary = undefined;
        return;
      }

      const host = findBlock(
        st,
        (b) => b.toolUseId === toolCallId && b.type === "tool_use",
      );
      if (!host) return;
      // 真实 CLI 对*每一次* Bash 都发 local_bash 帧，但只有 run_in_background 的那些
      // 才是后台任务；给普通前台 bash 挂 overlay 会污染后台任务面板。
      if (
        kind2 === "local_bash" &&
        host.toolInput?.run_in_background !== true
      ) {
        return;
      }
      host.subagent = {
        taskId,
        kind: kind2 || undefined,
        taskDescription:
          typeof info.taskDescription === "string"
            ? info.taskDescription
            : undefined,
        status,
      };
      mergeSubagentCounters(host.subagent, info);
      return;
    }

    case EventSubagentProgress: {
      const info = record(ev, "info");
      const host = findSubagentHost(
        st,
        str(ev, "toolCallId") ?? "",
        typeof info.taskId === "string" ? info.taskId : "",
      );
      if (!host?.subagent) return;
      if (typeof info.status === "string" && info.status) {
        host.subagent.status = info.status;
      }
      mergeSubagentCounters(host.subagent, info);
      return;
    }

    case EventSubagentDone: {
      const info = record(ev, "info");
      const host = findSubagentHost(
        st,
        str(ev, "toolCallId") ?? "",
        typeof info.taskId === "string" ? info.taskId : "",
      );
      if (!host?.subagent) return;
      host.subagent.status =
        (typeof info.status === "string" && info.status) || "completed";
      mergeSubagentCounters(host.subagent, info);
      return;
    }

    case EventSubagentModel: {
      const model = str(ev, "model");
      if (!model) return;
      const host = findSubagentHost(st, str(ev, "toolCallId") ?? "", "");
      // first-wins：模型一经记录，后续内部帧（子代理内部用小模型做摘要那种）
      // 不再改写。
      if (!host?.subagent || host.subagent.model) return;
      host.subagent.model = model;
      return;
    }

    case EventUnrecognizedBlock: {
      // 发送方读不懂的块。它与 default 分支的区别只在**是谁读不懂**:这一条是
      // 发送方明说「我投射不出这一块」并把原件带过来了,不是收到一个词表外
      // 的判别值。落点相同(notice 才画得出载荷),但文案要照实说清楚是哪一档。
      const raw = {
        blockType: str(ev, "blockType") ?? "",
        data: obj(ev)?.data,
      };
      openAssistant(st, sessionId).blocks.push({
        type: "notice",
        text: `${raw.blockType || "?"} ${pretty(raw.data ?? null)}`,
        raw,
      });
      return;
    }

    case undefined:
    default: {
      // 类型上这里已经是 never（上面覆盖了 EventKind 的全部取值），所以 Go 那边
      // 新增一个 kind 时这一句会立刻变红，逼一次「它该落到哪」的决定。
      // 但 default 分支本身要留着：kindOf 是断言不是校验，运行期照样可能收到
      // 词表外的字符串，那时如实呈现（R8）比抛错或吞掉正确。
      if (kind !== undefined) {
        const _unhandledEventKind: never = kind;
        void _unhandledEventKind;
      }
      // 落 `notice` 而不是 `unknown`，是为了让载荷**画得出来**。
      //
      // 本包 `unknown` 那一档只渲染一行 `(debug) unimplemented block type: …`，
      // 压根不读 `raw` —— DTO 上有这个字段，渲染层没用它。用 `unknown` 就等于
      // 把载荷藏了，正是 R8（不识别的如实呈现，不隐藏）要拦的那件事。
      // `notice` 原样渲染 block.text，宽度 max-w-measure（在对话列内）、底色
      // muted 不抢正文，是此刻唯一能既如实又不出列的落点。
      //
      // `raw` 照样填上：它是给消费方的结构化载荷，而且本包哪天让 unknown 读它，
      // 这里换回去就是一行的事。
      const raw = obj(ev) ?? { event: ev };
      openAssistant(st, sessionId).blocks.push({
        type: "notice",
        text: `${kind ?? "?"} ${pretty(raw)}`,
        raw,
      });
      return;
    }
  }
}

/**
 * 整段帧流 → 消息列表。
 *
 * 消息 id 由**出现顺序**决定，不是从帧里派生：SessionDetail 每来一条实时帧就把
 * 整段流重新归约一次，前缀相同则 id 逐个相同，行 key 因此稳定。id 一漂就是整段
 * unmount/remount —— 文本选中被清掉、滚动锚点丢失，块越多重建越贵。
 */
export function reduceFrames(
  frames: readonly TranscriptFrame[],
  sessionId: number,
): TranscriptMessage[] {
  const st = newState();
  for (const frame of frames) applyFrame(st, frame, sessionId);
  return st.messages;
}

/**
 * 增量投影器：只归约新到的那几帧，并且**只给被改到的那条消息换新身份**。
 *
 * `reduceFrames` 每次都从空状态重建全部消息对象。id 是稳定的（见它自己的注释），
 * 但引用不是，而下游吃的正是引用：
 *
 *   - 本本包的 `TranscriptRowView` 是 `React.memo`，行对象来自一个以
 *     `TranscriptMessage` 为键的 WeakMap 缓存。每帧换一批新对象 = 每帧全表 miss =
 *     整段行组件重渲染；
 *   - 助手正文在流式期间每来一个 token 就整段重新解析 markdown（remark-gfm +
 *     highlight.js 的语言自动探测），总工作量 O(n²)。
 *
 * 内部状态是可变累积，对外仍然是纯的：入参不被碰，返回的数组与其中变过的消息都是
 * 新对象，没变的那些原样交还。
 */
export interface TranscriptProjector {
  /**
   * 投影一段帧流。frames 是上一次那一段的**延长**时只归约新增部分，否则整段重算。
   * 同一个数组重复调用是幂等的（StrictMode 下 useMemo 的工厂会跑两次）。
   */
  project(frames: readonly TranscriptFrame[]): TranscriptMessage[];
}

export function createTranscriptProjector(
  sessionId: number,
): TranscriptProjector {
  let st = newState();
  let output: TranscriptMessage[] = [];
  let consumed = 0;
  let tail: TranscriptFrame | undefined;

  return {
    project(frames) {
      // 「是上一段的延长」的判据取 O(1) 的两条：长度没缩短，且上一次消费到的最后
      // 一帧仍是同一个对象。首屏加载、切会话、镜像日志被裁剪都会整段换掉，那时
      // 两条至少有一条不成立，退回整段重算。
      const extended =
        frames.length >= consumed &&
        (consumed === 0 || frames[consumed - 1] === tail);
      if (!extended) {
        st = newState();
        output = [];
        consumed = 0;
        tail = undefined;
      }
      if (frames.length === consumed) return output;
      for (let i = consumed; i < frames.length; i++) {
        applyFrame(st, frames[i], sessionId);
      }
      consumed = frames.length;
      tail = frames[consumed - 1];
      output = commit(st, output);
      return output;
    },
  };
}

/**
 * 把可变的归约状态定格成一份对外的快照：变过的消息换新身份，没变的原样交还。
 *
 * blocks 只做浅拷贝。块对象本身是被 applyFrame 就地改的，但会被就地改的块只可能
 * 属于 touched 里的消息——没被标记的消息，它的块这一批一个都没碰过。
 */
function commit(st: State, previous: TranscriptMessage[]): TranscriptMessage[] {
  let changed = previous.length !== st.messages.length;
  const next = previous.slice(0, st.messages.length);
  for (let i = 0; i < st.messages.length; i++) {
    const working = st.messages[i];
    if (next[i] !== undefined && !st.touched.has(working)) continue;
    next[i] = { ...working, blocks: [...working.blocks] };
    changed = true;
  }
  st.touched.clear();
  return changed ? next : previous;
}

/**
 * 会话级状态：不进转录正文，但要显示在 Composer 底栏上的那两样。
 *
 * `applyFrame` 里对这两个 kind 的处理是 `return`（记而不显），那段注释写得很直白 ——
 * 「桌面端每一条都有 handler，只是落点在会话状态或 Composer 上……拿 wire 事件的那两个
 * 面还没有那层承载面，此刻是记而不显 —— 缺的是显示面，不是数据」。底栏就是那层承载面，因此
 * 这里单独归约一遍；`reduceFrames` 一个字都不改（正文与会话状态是两件事）。
 *
 * 权限模式只接受 runtime 已经上报的稳定字符串；选项能力仍由 Composer 宿主决定，
 * 归约器不猜合法模式清单。
 */
export interface SessionRuntimeState {
  /** 模型可用的上下文窗口（tokens）。0 = 还没探到，界面据此整块不显示。 */
  contextWindow: number;
  /** runtime 当前权限模式；空串表示尚未上报。 */
  permissionMode: string;
}

export function reduceSessionState(
  frames: readonly TranscriptFrame[],
): SessionRuntimeState {
  const st: SessionRuntimeState = { contextWindow: 0, permissionMode: "" };
  for (const frame of frames) {
    const ev = frame.event;
    switch (kindOf(ev)) {
      case EventPermissionModeChanged: {
        const mode = str(ev, "mode");
        if (mode) st.permissionMode = mode;
        break;
      }
      case EventContextWindowUpdated: {
        // Tokens=0 是「没探到」（Go 侧 ContextWindowUpdated 的原话），不是
        // 「窗口为 0」——拿它覆盖已经探到的值会让底栏那条进度条凭空消失。
        const tokens = num(ev, "tokens");
        if (tokens && tokens > 0) st.contextWindow = tokens;
        break;
      }
      case EventUsage: {
        // usage 帧上也带 contextWindow（omitempty）：没有单独那条帧的后端由它兜住。
        // 与 totalInputTokens 一样，它是 UsageUpdate 自己的字段而非嵌套 Usage
        // 的字段 —— 从 usage 里取永远取不到，这条兜底就等于不存在。
        const win = num(ev, "contextWindow");
        if (win !== undefined && win > 0) st.contextWindow = win;
        break;
      }
      default:
        break;
    }
  }
  return st;
}

/**
 * 转录**自己已经渲染出可点卡片**的那些 requestId。
 *
 * 用途只有一个：跟 `runtime.session.pendingWaiters` 那份清单去重。
 *
 * 两份清单来源不同，都不能删：转录里的卡来自事件流（浏览器手上有那一帧才画得
 * 出来），waiters 来自一次 RPC（那台机器此刻真正阻塞着的是哪些）。镜像日志被
 * 裁剪、或浏览器从中途接进来时，会有「waiters 里有、事件流里没有」的待决 ——
 * 那种只有 DecisionPanel 兜得住。反过来两边都有的，就该只显示转录里那张，
 * 否则同一个审批在屏幕上出现两次。
 *
 * 只算**未决**的：已决议的卡是只读态，不再占 waiters。
 */
export function interactiveRequestIds(
  messages: readonly TranscriptMessage[],
): Set<string> {
  const ids = new Set<string>();
  for (const msg of messages) {
    for (const b of msg.blocks) {
      const perm = b.toolPermission;
      if (perm && !perm.resolved) ids.add(perm.requestId);
      const ask = b.askUserQuestion;
      if (ask && !ask.answered && !ask.skipped) ids.add(ask.requestId);
    }
  }
  return ids;
}

/**
 * 这一帧会不会让**助手那一条**开口。
 *
 * 「新一轮跑起来了，但对端还一个字都没回」这个空窗要靠一枚占位消息撑住（转录的
 * `pendingAssistant`），而撤掉占位的判据只能是「助手真的开口了」。此前用的判据是
 * 「这条会话又来帧了」—— 而一轮的**第一帧**恰恰是 daemon 把用户自己那句话回声
 * 回来（agentred 的 run 日志里那一轮的 kinds 就写着 `map[UserMessage:1]`）。占位
 * 因此在对端还没开口时就被撤掉，这一轮再没有别的东西能把三点点亮。
 *
 * 判据不另立一张 kind 表：直接把这一帧**单独**归约一遍，看它自己能不能长出一条
 * 助手消息。归宿表只有 `applyFrame` 那一份，这里跟着它走，不会各自漂移。
 *
 * 回填类的帧（决议、回答）单独归约时找不到那张卡，因此为 false —— 这是对的：
 * 卡本来就是助手先画出来的，那时占位早已撤掉。
 */
export function opensAssistantMessage(
  frame: TranscriptFrame,
  sessionId: number,
): boolean {
  return reduceFrames([frame], sessionId).some((m) => m.role === "assistant");
}
